package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const authSessionCookieName = "fbif_session"

var (
	errAuthenticationRequired = errors.New("authentication required")
	errFeishuLoginRequired    = errors.New("feishu login required")
	errInvalidSession         = errors.New("invalid session")
	errRuleAdminRequired      = errors.New("rule admin required")
	errSuperAdminRequired     = errors.New("super admin required")
)

type authCapabilities struct {
	FeishuDocuments bool `json:"feishu_documents"`
	RuleAdmin       bool `json:"rule_admin"`
	RuleSuperAdmin  bool `json:"rule_super_admin"`
}

type authPublicUser struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	AvatarURL    string           `json:"avatar_url,omitempty"`
	Email        string           `json:"email,omitempty"`
	AuthMode     string           `json:"auth_mode"`
	Role         string           `json:"role"`
	Capabilities authCapabilities `json:"capabilities"`
}

type authConfig struct {
	SessionSecret []byte
	FeishuAppID   string
	FeishuSecret  string
	RedirectURI   string
	SessionTTL    time.Duration
	Now           func() time.Time
	SuperAdminIDs map[string]struct{}
}

type authSessionClaims struct {
	UserID  string `json:"uid"`
	Version int64  `json:"ver"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
}

type authOAuthState struct {
	ReturnTo string `json:"return_to"`
	Nonce    string `json:"nonce"`
	Expires  int64  `json:"exp"`
}

type cachedUserAccessToken struct {
	Token     string
	ExpiresAt time.Time
}

type authLoginAttempt struct {
	Failures      int
	WindowStarted time.Time
	BlockedUntil  time.Time
}

type authLoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]authLoginAttempt
}

func newAuthLoginLimiter() *authLoginLimiter {
	return &authLoginLimiter{attempts: map[string]authLoginAttempt{}}
}

func (l *authLoginLimiter) allowed(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.attempts[key]
	if !ok {
		return true, 0
	}
	if attempt.BlockedUntil.After(now) {
		return false, attempt.BlockedUntil.Sub(now)
	}
	if now.Sub(attempt.WindowStarted) >= 10*time.Minute {
		delete(l.attempts, key)
	}
	return true, 0
}

func (l *authLoginLimiter) recordFailure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	if attempt.WindowStarted.IsZero() || now.Sub(attempt.WindowStarted) >= 10*time.Minute {
		attempt = authLoginAttempt{WindowStarted: now}
	}
	attempt.Failures++
	if attempt.Failures >= 8 {
		attempt.BlockedUntil = now.Add(15 * time.Minute)
	}
	l.attempts[key] = attempt
}

func (l *authLoginLimiter) clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

type authService struct {
	config            authConfig
	users             *authUserStore
	feishu            feishuProvider
	accessMu          sync.Mutex
	accessCache       map[string]cachedUserAccessToken
	feishuExportJobs  *feishuExportJobManager
	loginLimiter      *authLoginLimiter
	dummyPasswordHash string
}

func newAuthService(config authConfig, users *authUserStore, provider feishuProvider) (*authService, error) {
	if len(config.SessionSecret) < 24 {
		return nil, errors.New("AUTH_SESSION_SECRET must contain at least 24 bytes")
	}
	if users == nil {
		return nil, errors.New("auth user store is required")
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 30 * 24 * time.Hour
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if provider == nil {
		provider = newHTTPFeishuProvider(http.DefaultClient)
	}
	dummyPasswordHash, err := hashPassword("not-a-real-user-password")
	if err != nil {
		return nil, fmt.Errorf("initialize password verifier: %w", err)
	}
	feishuExportJobs := newFeishuExportJobManager(config.Now)
	feishuExportJobs.startGC(feishuExportJobGCInterval)
	return &authService{
		config:            config,
		users:             users,
		feishu:            provider,
		accessCache:       map[string]cachedUserAccessToken{},
		feishuExportJobs:  feishuExportJobs,
		loginLimiter:      newAuthLoginLimiter(),
		dummyPasswordHash: dummyPasswordHash,
	}, nil
}

func (s *authService) now() time.Time {
	return s.config.Now().UTC()
}

func (s *authService) effectiveRole(user authUser) authRole {
	if user.AuthMode == authModeFeishu {
		if _, ok := s.config.SuperAdminIDs[strings.TrimSpace(user.FeishuOpenID)]; ok {
			return authRoleSuperAdmin
		}
	}
	// 超级管理员身份只来自部署配置，不能靠篡改 JSON 用户库自授予。
	role := normalizeAuthRole(user.Role)
	if role == authRoleSuperAdmin {
		return authRoleAdmin
	}
	return role
}

func (s *authService) publicAuthUser(user authUser) authPublicUser {
	role := s.effectiveRole(user)
	return authPublicUser{
		ID:        user.ID,
		Name:      user.Name,
		AvatarURL: user.AvatarURL,
		Email:     user.Email,
		AuthMode:  string(user.AuthMode),
		Role:      string(role),
		Capabilities: authCapabilities{
			FeishuDocuments: user.AuthMode == authModeFeishu,
			RuleAdmin:       role == authRoleAdmin || role == authRoleSuperAdmin,
			RuleSuperAdmin:  role == authRoleSuperAdmin,
		},
	}
}

func (s *authService) signPayload(kind string, payload []byte) string {
	payloadPart := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.config.SessionSecret)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(payloadPart))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payloadPart + "." + signature
}

func (s *authService) verifyPayload(kind, token string, target any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return errInvalidSession
	}
	mac := hmac.New(sha256.New, s.config.SessionSecret)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(actual, expected) {
		return errInvalidSession
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errInvalidSession
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errInvalidSession
	}
	return nil
}

func (s *authService) issueSession(user authUser) (string, error) {
	now := s.now()
	claims := authSessionClaims{
		UserID:  user.ID,
		Version: user.SessionVersion,
		Issued:  now.Unix(),
		Expires: now.Add(s.config.SessionTTL).Unix(),
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return s.signPayload("session", body), nil
}

func (s *authService) userFromSessionToken(token string) (authUser, error) {
	if strings.TrimSpace(token) == "" {
		return authUser{}, errAuthenticationRequired
	}
	var claims authSessionClaims
	if err := s.verifyPayload("session", token, &claims); err != nil {
		return authUser{}, errAuthenticationRequired
	}
	now := s.now().Unix()
	if claims.UserID == "" || claims.Expires <= now || claims.Issued > now+60 {
		return authUser{}, errAuthenticationRequired
	}
	user, ok, err := s.users.Get(claims.UserID)
	if err != nil {
		return authUser{}, err
	}
	if !ok || user.Status != authUserActive || user.SessionVersion != claims.Version {
		return authUser{}, errAuthenticationRequired
	}
	return user, nil
}

func sessionTokenFromRequest(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get("X-Session-Token")); token != "" {
		return token
	}
	cookie, err := r.Cookie(authSessionCookieName)
	if err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func (s *authService) currentUser(r *http.Request) (authUser, error) {
	return s.userFromSessionToken(sessionTokenFromRequest(r))
}

func (s *authService) requireFeishuUser(r *http.Request) (authUser, error) {
	user, err := s.currentUser(r)
	if err != nil {
		return authUser{}, err
	}
	if user.AuthMode != authModeFeishu {
		return authUser{}, errFeishuLoginRequired
	}
	return user, nil
}

func (s *authService) requireRuleAdmin(r *http.Request) (authUser, error) {
	user, err := s.currentUser(r)
	if err != nil {
		return authUser{}, err
	}
	role := s.effectiveRole(user)
	if role != authRoleAdmin && role != authRoleSuperAdmin {
		return authUser{}, errRuleAdminRequired
	}
	return user, nil
}

func (s *authService) requireSuperAdmin(r *http.Request) (authUser, error) {
	user, err := s.currentUser(r)
	if err != nil {
		return authUser{}, err
	}
	if s.effectiveRole(user) != authRoleSuperAdmin {
		return authUser{}, errSuperAdminRequired
	}
	return user, nil
}

func (s *authService) setSessionCookie(w http.ResponseWriter, token string) {
	s.setSessionCookiePersistence(w, token, true)
}

func (s *authService) setSessionCookiePersistence(w http.ResponseWriter, token string, persistent bool) {
	cookie := &http.Cookie{
		Name:     authSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   productionRuntime(),
		SameSite: http.SameSiteLaxMode,
	}
	if persistent {
		cookie.MaxAge = int(s.config.SessionTTL.Seconds())
		cookie.Expires = s.now().Add(s.config.SessionTTL)
	}
	http.SetCookie(w, cookie)
}

func (s *authService) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   productionRuntime(),
		SameSite: http.SameSiteLaxMode,
	})
}

func safeAuthReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/mp/fbif"
	}
	if strings.ContainsAny(raw, "\\\r\n") {
		return "/mp/fbif"
	}
	return raw
}

func randomURLToken(size int) (string, error) {
	body := make([]byte, size)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func (s *authService) issueOAuthState(returnTo string) (string, error) {
	nonce, err := randomURLToken(18)
	if err != nil {
		return "", err
	}
	state := authOAuthState{
		ReturnTo: safeAuthReturnTo(returnTo),
		Nonce:    nonce,
		Expires:  s.now().Add(10 * time.Minute).Unix(),
	}
	body, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return s.signPayload("oauth-state", body), nil
}

func (s *authService) verifyOAuthState(token string) (authOAuthState, error) {
	var state authOAuthState
	if err := s.verifyPayload("oauth-state", token, &state); err != nil {
		return state, err
	}
	if state.Expires <= s.now().Unix() || state.Nonce == "" {
		return state, errInvalidSession
	}
	state.ReturnTo = safeAuthReturnTo(state.ReturnTo)
	return state, nil
}

func (s *authService) tokenAEAD() (cipher.AEAD, error) {
	hash := sha256.Sum256(append([]byte("fbif-feishu-refresh-token\x00"), s.config.SessionSecret...))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *authService) sealRefreshToken(token string) (string, error) {
	aead, err := s.tokenAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, []byte(token), []byte("feishu-refresh-token"))
	return base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func (s *authService) openRefreshToken(seal string) (string, error) {
	aead, err := s.tokenAEAD()
	if err != nil {
		return "", err
	}
	body, err := base64.RawURLEncoding.DecodeString(seal)
	if err != nil || len(body) <= aead.NonceSize() {
		return "", errors.New("invalid refresh token seal")
	}
	plain, err := aead.Open(nil, body[:aead.NonceSize()], body[aead.NonceSize():], []byte("feishu-refresh-token"))
	if err != nil {
		return "", errors.New("invalid refresh token seal")
	}
	return string(plain), nil
}

func (s *authService) putAccessToken(userID, token string, expiresAt time.Time) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	s.accessCache[userID] = cachedUserAccessToken{Token: token, ExpiresAt: expiresAt}
}

func (s *authService) userAccessToken(ctx context.Context, user authUser) (string, error) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if cached := s.accessCache[user.ID]; cached.Token != "" && cached.ExpiresAt.After(s.now().Add(time.Minute)) {
		return cached.Token, nil
	}
	refreshToken, err := s.openRefreshToken(user.FeishuRefreshTokenSeal)
	if err != nil {
		return "", fmt.Errorf("feishu authorization expired: %w", err)
	}
	token, err := s.feishu.RefreshUserToken(ctx, refreshToken)
	if err != nil {
		return "", err
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 {
		return "", errors.New("feishu refresh response is incomplete")
	}
	refreshSeal, err := s.sealRefreshToken(token.RefreshToken)
	if err != nil {
		return "", err
	}
	if _, err := s.users.UpdateFeishuTokens(user.ID, refreshSeal, time.Time{}); err != nil {
		return "", err
	}
	s.accessCache[user.ID] = cachedUserAccessToken{
		Token:     token.AccessToken,
		ExpiresAt: s.now().Add(token.ExpiresIn),
	}
	return token.AccessToken, nil
}
