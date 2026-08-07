package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func authUsersPath(rulesPath string) string {
	if explicit := strings.TrimSpace(os.Getenv("AUTH_USERS_PATH")); explicit != "" {
		return explicit
	}
	if strings.TrimSpace(rulesPath) != "" {
		return filepath.Join(filepath.Dir(rulesPath), "auth-users.json")
	}
	return filepath.Join("data", "auth-users.json")
}

func authConfigFromEnvironment() (authConfig, error) {
	secret := []byte(strings.TrimSpace(os.Getenv("AUTH_SESSION_SECRET")))
	if len(secret) < 24 {
		if productionRuntime() {
			return authConfig{}, errors.New("AUTH_SESSION_SECRET must contain at least 24 bytes")
		}
		generated, err := randomURLToken(32)
		if err != nil {
			return authConfig{}, err
		}
		secret = []byte(generated)
		log.Printf("auth: AUTH_SESSION_SECRET 未配置，当前本地会话仅在本进程内有效")
	}
	config := authConfig{
		SessionSecret: secret,
		FeishuAppID:   strings.TrimSpace(os.Getenv("FEISHU_LOGIN_APP_ID")),
		FeishuSecret:  strings.TrimSpace(os.Getenv("FEISHU_LOGIN_APP_SECRET")),
		RedirectURI:   strings.TrimSpace(os.Getenv("FEISHU_LOGIN_REDIRECT_URI")),
		SessionTTL:    30 * 24 * time.Hour,
		SuperAdminIDs: parseAuthOpenIDList(os.Getenv("RULE_SUPER_ADMIN_OPEN_IDS")),
	}
	if productionRuntime() && (config.FeishuAppID == "" || config.FeishuSecret == "" || config.RedirectURI == "") {
		return authConfig{}, errors.New("FEISHU_LOGIN_APP_ID、FEISHU_LOGIN_APP_SECRET 和 FEISHU_LOGIN_REDIRECT_URI 必须完整配置")
	}
	if productionRuntime() && len(config.SuperAdminIDs) == 0 {
		return authConfig{}, errors.New("RULE_SUPER_ADMIN_OPEN_IDS 必须配置至少一个超级管理员")
	}
	return config, nil
}

func parseAuthOpenIDList(raw string) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		id := strings.TrimSpace(item)
		if feishuOpenIDSafe(id) {
			ids[id] = struct{}{}
		}
	}
	return ids
}

type passwordAccountSeed struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

func syncPasswordAccountsFromEnvironment(service *authService) error {
	encoded := strings.TrimSpace(os.Getenv("AUTH_PASSWORD_USERS_B64"))
	if encoded == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return errors.New("AUTH_PASSWORD_USERS_B64 is not valid Base64")
	}
	var accounts []passwordAccountSeed
	if err := json.Unmarshal(raw, &accounts); err != nil {
		return errors.New("AUTH_PASSWORD_USERS_B64 does not contain valid JSON")
	}
	for _, account := range accounts {
		username := strings.ToLower(strings.TrimSpace(account.Username))
		if username == "" || len(account.Password) < 8 || strings.TrimSpace(account.Name) == "" {
			return errors.New("AUTH_PASSWORD_USERS_B64 contains an invalid account")
		}
		passwordHash, err := hashPassword(account.Password)
		if err != nil {
			return err
		}
		existing, ok, err := service.users.FindByUsername(username)
		if err != nil {
			return err
		}
		user := authUser{
			ID:           "password:" + username,
			Name:         account.Name,
			AvatarURL:    account.AvatarURL,
			Username:     username,
			PasswordHash: passwordHash,
			AuthMode:     authModePassword,
			Status:       authUserActive,
		}
		if ok {
			user.CreatedAt = existing.CreatedAt
			user.SessionVersion = existing.SessionVersion
			user.Role = existing.Role
		}
		if err := service.users.Upsert(user); err != nil {
			return err
		}
	}
	return nil
}

func registerAuthRoutes(mux *http.ServeMux, service *authService) {
	mux.HandleFunc("GET /auth/feishu/fbif/login", noStoreHandler(service.handleFeishuLogin))
	mux.HandleFunc("GET /auth/feishu/fbif/callback", noStoreHandler(service.handleFeishuCallback))
	mux.HandleFunc("POST /auth/password/login", noStoreHandler(service.handlePasswordLogin))
	mux.HandleFunc("POST /auth/feishu/logout", noStoreHandler(service.handleLogout))
	mux.HandleFunc("GET /api/me", noStoreHandler(service.handleCurrentUser))
}

func noStoreHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		next(w, r)
	}
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func redirectFeishuLoginError(w http.ResponseWriter, r *http.Request, code string) {
	target := "/login.html?error=" + url.QueryEscape(strings.TrimSpace(code))
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *authService) handleFeishuLogin(w http.ResponseWriter, r *http.Request) {
	if s.config.FeishuAppID == "" || s.config.FeishuSecret == "" || s.config.RedirectURI == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "feishu_login_not_configured", "飞书登录尚未完成配置")
		return
	}
	state, err := s.issueOAuthState(r.URL.Query().Get("return_to"))
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "oauth_state_failed", "无法开始飞书登录")
		return
	}
	query := url.Values{
		"client_id":     {s.config.FeishuAppID},
		"redirect_uri":  {s.config.RedirectURI},
		"response_type": {"code"},
		"scope":         {feishuLoginScopes()},
		"state":         {state},
	}
	http.Redirect(w, r, feishuAccountsBaseURL+"/open-apis/authen/v1/authorize?"+query.Encode(), http.StatusFound)
}

// feishuLoginScopes 是登录后持续使用飞书 API 的权限契约。默认值覆盖已有
// 云文档导入能力和发布后台的群列表能力；部署可用环境变量显式替换，但必须
// 保留 offline_access，否则后端无法在 access_token 过期后安全刷新。
func feishuLoginScopes() string {
	if configured := strings.TrimSpace(os.Getenv("FEISHU_LOGIN_SCOPES")); configured != "" {
		return configured
	}
	// im:chat:readonly 是飞书真实存在的权限名；此前误写作 im:chat:read（飞书无此
	// 权限，授权时被静默丢弃），导致发布链路后台「审核群」列表始终读取失败。
	// wiki:node:read 供 /wiki/<token> 链接经 wiki/v2/spaces/get_node 换算出真实
	// 文档 token；缺它时知识库链接在 resolve 阶段就被飞书拒绝。
	return "offline_access drive:drive.search:readonly drive:export:readonly im:chat:readonly wiki:node:read"
}

func (s *authService) handleFeishuCallback(w http.ResponseWriter, r *http.Request) {
	if callbackError := strings.TrimSpace(r.URL.Query().Get("error")); callbackError != "" {
		code := "feishu_login_failed"
		if callbackError == "access_denied" {
			code = "denied"
		}
		redirectFeishuLoginError(w, r, code)
		return
	}
	state, err := s.verifyOAuthState(r.URL.Query().Get("state"))
	if err != nil {
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	token, err := s.feishu.ExchangeCode(r.Context(), code, s.config.RedirectURI)
	if err != nil {
		log.Printf("auth: Feishu code exchange failed: %v", err)
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	profile, err := s.feishu.GetUserProfile(r.Context(), token.AccessToken)
	if err != nil {
		log.Printf("auth: Feishu profile request failed: %v", err)
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	refreshSeal, err := s.sealRefreshToken(token.RefreshToken)
	if err != nil {
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	userID := "feishu:" + profile.OpenID
	existing, exists, err := s.users.Get(userID)
	if err != nil {
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	user := authUser{
		ID:                     userID,
		Name:                   profile.Name,
		AvatarURL:              profile.AvatarURL,
		Email:                  profile.Email,
		AuthMode:               authModeFeishu,
		Status:                 authUserActive,
		FeishuOpenID:           profile.OpenID,
		FeishuUnionID:          profile.UnionID,
		FeishuRefreshTokenSeal: refreshSeal,
		LastLoginAt:            s.now(),
	}
	if _, isSuperAdmin := s.config.SuperAdminIDs[profile.OpenID]; isSuperAdmin {
		user.Role = authRoleSuperAdmin
	}
	if exists {
		user.CreatedAt = existing.CreatedAt
		user.SessionVersion = existing.SessionVersion
		if existing.Status == authUserDisabled {
			redirectFeishuLoginError(w, r, "unauthorized_tenant")
			return
		}
	}
	if err := s.users.Upsert(user); err != nil {
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	stored, ok, err := s.users.Get(userID)
	if err != nil || !ok {
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	s.putAccessToken(userID, token.AccessToken, s.now().Add(token.ExpiresIn))
	sessionToken, err := s.issueSession(stored)
	if err != nil {
		redirectFeishuLoginError(w, r, "feishu_login_failed")
		return
	}
	s.setSessionCookie(w, sessionToken)
	fragment := url.Values{
		"session_token": {sessionToken},
		"return_to":     {state.ReturnTo},
	}.Encode()
	http.Redirect(w, r, "/auth-callback.html#"+fragment, http.StatusFound)
}

func passwordLoginRateKey(r *http.Request, username string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if parsed := net.ParseIP(host); parsed != nil && parsed.IsLoopback() {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
			host = forwarded
		}
	}
	return host + "\x00" + strings.ToLower(strings.TrimSpace(username))
}

func (s *authService) handlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember *bool  `json:"remember"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_login_request", "请输入账号和密码")
		return
	}
	rateKey := passwordLoginRateKey(r, input.Username)
	if allowed, retryAfter := s.loginLimiter.allowed(rateKey, s.now()); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
		writeAuthError(w, http.StatusTooManyRequests, "too_many_login_attempts", "登录尝试过多，请稍后再试")
		return
	}
	user, ok, err := s.users.FindByUsername(input.Username)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "user_store_failed", "账号服务暂不可用")
		return
	}
	passwordHash := s.dummyPasswordHash
	if ok {
		passwordHash = user.PasswordHash
	}
	passwordValid := verifyPassword(passwordHash, input.Password)
	if !ok || user.Status != authUserActive || !passwordValid {
		s.loginLimiter.recordFailure(rateKey, s.now())
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "账号或密码错误")
		return
	}
	s.loginLimiter.clear(rateKey)
	user.LastLoginAt = s.now()
	if err := s.users.Upsert(user); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "user_store_failed", "账号服务暂不可用")
		return
	}
	stored, _, _ := s.users.Get(user.ID)
	sessionToken, err := s.issueSession(stored)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "session_failed", "无法建立登录会话")
		return
	}
	remember := true
	if input.Remember != nil {
		remember = *input.Remember
	}
	s.setSessionCookiePersistence(w, sessionToken, remember)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"user":          s.publicAuthUser(stored),
		"session_token": sessionToken,
	})
}

func (s *authService) handleCurrentUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "authentication_required", "请先登录")
		return
	}
	sessionToken, err := s.issueSession(user)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "session_failed", "无法续期登录会话")
		return
	}
	s.setSessionCookie(w, sessionToken)
	w.Header().Set("X-Session-Token", sessionToken)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":          s.publicAuthUser(user),
		"session_token": sessionToken,
	})
}

func (s *authService) handleLogout(w http.ResponseWriter, r *http.Request) {
	if user, err := s.currentUser(r); err == nil {
		if err := s.users.IncrementSessionVersion(user.ID, s.now()); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "logout_failed", "退出登录失败")
			return
		}
		s.accessMu.Lock()
		delete(s.accessCache, user.ID)
		s.accessMu.Unlock()
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func newRuntimeAuthService(rulesPath string) (*authService, error) {
	config, err := authConfigFromEnvironment()
	if err != nil {
		return nil, err
	}
	provider := newHTTPFeishuProvider(&http.Client{Timeout: 45 * time.Second})
	provider.configure(config.FeishuAppID, config.FeishuSecret)
	service, err := newAuthService(config, newAuthUserStore(authUsersPath(rulesPath)), provider)
	if err != nil {
		return nil, err
	}
	if err := syncPasswordAccountsFromEnvironment(service); err != nil {
		return nil, fmt.Errorf("sync password accounts: %w", err)
	}
	return service, nil
}
