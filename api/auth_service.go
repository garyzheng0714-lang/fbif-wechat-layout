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
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const authSessionCookieName = "fbif_session"

var (
	errAuthenticationRequired = errors.New("authentication required")
	errFeishuLoginRequired    = errors.New("feishu login required")
	errFeishuReauthRequired   = errors.New("feishu reauthorization required")
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
	return s.refreshFeishuTokenLocked(ctx, user.ID)
}

// refreshFeishuTokenLocked 用库里当前的 refresh_token 换新令牌并原子轮换；调用方
// 必须持有 accessMu。refresh_token 只能用一次，所以每次都从库里重读，不信调用方
// 手里可能已被别人用掉的旧值。
func (s *authService) refreshFeishuTokenLocked(ctx context.Context, userID string) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		current, ok, err := s.users.Get(userID)
		if err != nil {
			return "", err
		}
		if !ok || current.Status != authUserActive {
			return "", errAuthenticationRequired
		}
		if current.FeishuReauthRequired || current.FeishuRefreshTokenSeal == "" {
			return "", errFeishuReauthRequired
		}
		usedSeal := current.FeishuRefreshTokenSeal
		refreshToken, err := s.openRefreshToken(usedSeal)
		if err != nil {
			// 会话密钥换过等导致解不开：这份令牌对本服务已不可用。
			return "", s.markFeishuReauthRequired(current, usedSeal, "seal unreadable")
		}
		token, err := s.feishu.RefreshUserToken(ctx, refreshToken)
		var oauthErr *feishuOAuthError
		if errors.As(err, &oauthErr) && oauthErr.reauthRequired() {
			reauthErr := s.markFeishuReauthRequired(current, usedSeal, fmt.Sprintf("code=%d error=%s", oauthErr.Code, oauthErr.OAuthError))
			if errors.Is(reauthErr, errFeishuTokenRotatedConcurrently) {
				continue
			}
			return "", reauthErr
		}
		if err != nil {
			// 飞书临时故障或网络问题：旧令牌未被确认作废，保留它等下次重试。
			return "", err
		}
		if token.RefreshToken == "" {
			// 旧令牌已用掉却没拿到新的，只能重新授权。
			return "", s.markFeishuReauthRequired(current, usedSeal, "refresh response has no refresh_token")
		}
		refreshSeal, err := s.sealRefreshToken(token.RefreshToken)
		if err != nil {
			return "", err
		}
		now := s.now()
		var expiresAt time.Time
		if token.RefreshExpiresIn > 0 {
			expiresAt = now.Add(token.RefreshExpiresIn)
		}
		if _, err := s.users.RotateFeishuRefreshToken(userID, refreshSeal, now, expiresAt); err != nil {
			return "", err
		}
		if token.AccessToken == "" || token.ExpiresIn <= 0 {
			return "", errors.New("feishu refresh response is incomplete")
		}
		s.accessCache[userID] = cachedUserAccessToken{
			Token:     token.AccessToken,
			ExpiresAt: now.Add(token.ExpiresIn),
		}
		return token.AccessToken, nil
	}
	return "", errFeishuReauthRequired
}

var errFeishuTokenRotatedConcurrently = errors.New("feishu refresh token was rotated concurrently")

func (s *authService) markFeishuReauthRequired(user authUser, usedSeal, reason string) error {
	marked, err := s.users.MarkFeishuReauthRequired(user.ID, usedSeal, s.now())
	if err != nil {
		return err
	}
	if !marked {
		return errFeishuTokenRotatedConcurrently
	}
	delete(s.accessCache, user.ID)
	log.Printf("auth: 飞书授权已失效，需重新登录 user=%s name=%s (%s)", user.ID, user.Name, reason)
	return errFeishuReauthRequired
}

// 飞书 refresh_token 约 7 天、只能用一次；会话却是 30 天滑动续期。后台保活在
// 会话有效期内（30 天内用过）定期替用户轮换，用户就不会因为一周没用飞书功能
// 而被要求重新授权。连续 30 天没用则停止保活，令牌随之自然过期。
const (
	feishuTokenKeepAliveInterval = time.Hour
	feishuTokenRotateAfter       = 3 * 24 * time.Hour
	authLastSeenWriteInterval    = time.Hour
)

func (s *authService) feishuTokenRotationDue(user authUser, now time.Time) bool {
	if user.AuthMode != authModeFeishu || user.Status != authUserActive ||
		user.FeishuReauthRequired || user.FeishuRefreshTokenSeal == "" {
		return false
	}
	// 只认真实使用：保活轮换本身会刷新 UpdatedAt，拿它算活跃会让保活永不停止。
	// 上线前的老记录没有 LastSeenAt，也从未被保活轮换过，只有这时才用 UpdatedAt
	// （登录与此前的刷新都会写它）兜底一次。
	lastActive := user.LastSeenAt
	if user.LastLoginAt.After(lastActive) {
		lastActive = user.LastLoginAt
	}
	if user.LastSeenAt.IsZero() && user.FeishuTokenRefreshedAt.IsZero() && user.UpdatedAt.After(lastActive) {
		lastActive = user.UpdatedAt
	}
	if now.Sub(lastActive) > s.config.SessionTTL {
		return false
	}
	if user.FeishuTokenRefreshedAt.IsZero() {
		return true
	}
	rotateAfter := feishuTokenRotateAfter
	if !user.FeishuRefreshExpiresAt.IsZero() {
		if half := user.FeishuRefreshExpiresAt.Sub(user.FeishuTokenRefreshedAt) / 2; half > 0 && half < rotateAfter {
			rotateAfter = half
		}
	}
	return now.Sub(user.FeishuTokenRefreshedAt) >= rotateAfter
}

// keepFeishuTokensAlive 轮换所有到期的飞书授权，返回轮换成功与需重新授权的人数。
func (s *authService) keepFeishuTokensAlive(ctx context.Context) (rotated, reauth int) {
	users, err := s.users.List()
	if err != nil {
		log.Printf("auth: 飞书授权保活读取用户失败: %v", err)
		return 0, 0
	}
	for _, user := range users {
		if ctx.Err() != nil {
			return rotated, reauth
		}
		if !s.feishuTokenRotationDue(user, s.now()) {
			continue
		}
		switch err := s.keepFeishuTokenAlive(ctx, user.ID); {
		case err == nil:
			rotated++
		case errors.Is(err, errFeishuReauthRequired):
			reauth++
		case errors.Is(err, errFeishuTokenNotDue):
		default:
			log.Printf("auth: 飞书授权保活失败，下轮重试 user=%s: %v", user.ID, err)
		}
	}
	return rotated, reauth
}

var errFeishuTokenNotDue = errors.New("feishu token rotation not due")

func (s *authService) keepFeishuTokenAlive(ctx context.Context, userID string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	// 拿锁期间可能已有请求替他轮换过，重读后再判一次。
	current, ok, err := s.users.Get(userID)
	if err != nil {
		return err
	}
	if !ok || !s.feishuTokenRotationDue(current, s.now()) {
		return errFeishuTokenNotDue
	}
	_, err = s.refreshFeishuTokenLocked(ctx, userID)
	return err
}

func (s *authService) startFeishuTokenKeepAlive(paused func() bool) {
	go func() {
		// 启动后稍等再跑第一轮，避开部署验收窗口的启动高峰。
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for range timer.C {
			if !paused() {
				rotated, reauth := s.keepFeishuTokensAlive(context.Background())
				if rotated > 0 || reauth > 0 {
					log.Printf("auth: 飞书授权保活 轮换=%d 需重新登录=%d", rotated, reauth)
				}
			}
			timer.Reset(feishuTokenKeepAliveInterval)
		}
	}()
}
