package main

// auth_login.go — 把 FBIF 统一登录（internal/auth）和本人飞书授权（internal/feishugrant）接到排版工具上。
// 两个模板都是原样复制、不改；这里只写本项目的那几项：用户库怎么认人、谁能授权飞书、「我是谁」回什么。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fbif-wechat-layout/api/internal/auth"
	"fbif-wechat-layout/api/internal/feishugrant"
)

const (
	// fbifCompanyCode 是 FBIF 在统一登录里的企业编号；FBIF 的 sub 就是飞书 union_id。
	fbifCompanyCode = "FJY632B9P3E"
	// accountCompanyCode 是账号密码登录的人（登录中心）的企业编号，和登录模板里的一样。
	accountCompanyCode = "account"
	// legacyFeishuUserPrefix：2026-11-10 换统一登录前用飞书登录过的人，用户 ID 是 feishu:<open_id>，都是 FBIF 的。
	legacyFeishuUserPrefix = "feishu:"
	// ssoSessionCookie 是登录模板的会话 cookie 名（模板里的 sessionCookie）；没有它的请求不去问登录模板。
	ssoSessionCookie = "__Host-fbif_sid"
)

// loginBackend 是「请求上是谁」的来源：生产是统一登录，本地端到端测试是 devlogin（只在 -tags devlogin 编译）。
type loginBackend interface {
	// register 挂登录和飞书授权的路由。
	register(mux *http.ServeMux)
	// wrap 包住整个服务：认出请求上的人（withLayoutUser），记请求日志。
	wrap(next http.Handler) http.Handler
	shutdown(ctx context.Context) error
}

// devLoginBackend 由 auth_devlogin.go（-tags devlogin）填上；生产编译里是 nil。
var devLoginBackend func(users *authUserStore) (loginBackend, feishuTokenSource)

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
	config := authConfig{
		FeishuAppID:   strings.TrimSpace(os.Getenv("FEISHU_LOGIN_APP_ID")),
		FeishuSecret:  strings.TrimSpace(os.Getenv("FEISHU_LOGIN_APP_SECRET")),
		SuperAdminIDs: parseAuthOpenIDList(os.Getenv("RULE_SUPER_ADMIN_OPEN_IDS")),
	}
	if productionRuntime() && (config.FeishuAppID == "" || config.FeishuSecret == "" || feishuGrantRedirectURL() == "") {
		return authConfig{}, errors.New("FEISHU_LOGIN_APP_ID、FEISHU_LOGIN_APP_SECRET 和 FEISHU_LOGIN_REDIRECT_URI 必须完整配置")
	}
	if productionRuntime() && len(config.SuperAdminIDs) == 0 {
		return authConfig{}, errors.New("RULE_SUPER_ADMIN_OPEN_IDS 必须配置至少一个超级管理员")
	}
	return config, nil
}

// feishuGrantRedirectURL 是本人授权飞书的回调（https://域名/auth/feishu/fbif/qr-callback），要登记在飞书应用的重定向 URL 里。
func feishuGrantRedirectURL() string {
	return strings.TrimSpace(os.Getenv("FEISHU_LOGIN_REDIRECT_URI"))
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

// feishuGrantScopes 是本人授权时要的飞书权限（offline_access 由授权模板自动加）：搜、导出云文档，列审核群，
// 知识库链接换算文档（wiki:node:read）。部署可用 FEISHU_LOGIN_SCOPES 整个换掉。
func feishuGrantScopes() string {
	if configured := strings.TrimSpace(os.Getenv("FEISHU_LOGIN_SCOPES")); configured != "" {
		return configured
	}
	// im:chat:readonly 是飞书真实存在的权限名；此前误写作 im:chat:read（飞书无此权限，授权时被静默丢弃），
	// 导致发布链路后台「审核群」列表始终读取失败。
	return "drive:drive.search:readonly drive:export:readonly im:chat:readonly wiki:node:read"
}

// feishuAppFor：这个人用哪个飞书应用授权。只有 FBIF 飞书登录的人能授权（本项目只有 FBIF 的飞书应用）：
// 统一登录的 FJY632B9P3E:<union_id>，和换统一登录前的老用户 feishu:<open_id>。按用户 ID 认，不读用户库。
func feishuAppFor(userID string) string {
	if strings.HasPrefix(userID, legacyFeishuUserPrefix) || strings.HasPrefix(userID, fbifCompanyCode+":") {
		return "fbif"
	}
	return ""
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

// Register 挂登录、飞书授权和「我是谁」（GET /api/auth/me）。
func (s *authService) Register(mux *http.ServeMux) {
	if s.login != nil {
		s.login.register(mux)
	}
	mux.HandleFunc("GET /api/auth/me", noStoreHandler(s.handleCurrentUser))
}

// Wrap 包住整个服务：认出请求上的人、记请求日志。
func (s *authService) Wrap(next http.Handler) http.Handler {
	if s.login == nil {
		return next
	}
	return s.login.wrap(next)
}

// Shutdown 在 http.Server.Shutdown 之后调：把登录日志的待写队列落盘。
func (s *authService) Shutdown(ctx context.Context) error {
	if s.login == nil {
		return nil
	}
	return s.login.shutdown(ctx)
}

func (s *authService) handleCurrentUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	switch {
	case errors.Is(err, errLoginUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "login_service_unavailable", "code": "login_service_unavailable"})
		return
	case err != nil:
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login_required", "code": "authentication_required", "login_url": "/auth/login"})
		return
	}
	public := s.publicAuthUser(user)
	public.Feishu = s.feishuGrantStatus(r, user)
	writeJSON(w, http.StatusOK, map[string]any{"user": public})
}

// onLogin 把统一登录的人对上用户库（auth.Options.OnLogin）：老用户按 union_id 认回原来的 ID、角色和停用。
func (s *authService) onLogin(_ context.Context, id auth.Identity) (string, error) {
	login := authUser{
		ID:          id.Key(),
		Name:        id.Name,
		AvatarURL:   id.Picture,
		Email:       id.Email,
		CompanyCode: id.CompanyCode,
		AuthMode:    authModeFeishu,
		Status:      authUserActive,
	}
	if id.CompanyCode == accountCompanyCode {
		login.AuthMode = authModePassword
	}
	if id.CompanyCode == fbifCompanyCode {
		login.FeishuUnionID = id.Sub
	}
	user, err := s.users.RecordLogin(login, s.now())
	if err != nil {
		return "", err
	}
	if user.Status != authUserActive {
		return "", auth.ErrNotAllowed
	}
	return user.ID, nil
}

// ---------- 生产：统一登录 + 本人飞书授权 ----------

type ssoLogin struct {
	sso    *auth.SSO
	grants *feishugrant.Grants
}

func newRuntimeAuthService(ctx context.Context, rulesPath string) (*authService, error) {
	config, err := authConfigFromEnvironment()
	if err != nil {
		return nil, err
	}
	usersPath := authUsersPath(rulesPath)
	service, err := newAuthService(config, newAuthUserStore(usersPath), newHTTPFeishuProvider(&http.Client{Timeout: 45 * time.Second}))
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_DEV_LOGIN")), "true") {
		if devLoginBackend == nil || productionRuntime() {
			return nil, errors.New("AUTH_DEV_LOGIN 只给本地端到端测试用（go run -tags devlogin），生产不许开")
		}
		service.login, service.tokens = devLoginBackend(service.users)
		log.Printf("auth: 本地测试登录已开（/auth/dev-login），不是统一登录")
		return service, nil
	}
	login, err := newSSOLogin(ctx, service, filepath.Dir(usersPath))
	if err != nil {
		return nil, err
	}
	service.login = login
	if login.grants != nil {
		service.tokens = login.grants
	}
	return service, nil
}

// newSSOLogin 读登录模板的 SSO_* 环境变量（缺了启动失败），会话和飞书授权放在用户库同一个目录。
func newSSOLogin(ctx context.Context, service *authService, stateDir string) (*ssoLogin, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("auth: 建 %s: %w", stateDir, err)
	}
	store, err := auth.NewFileStore(filepath.Join(stateDir, "sso-sessions.json"))
	if err != nil {
		return nil, err
	}
	sso, err := auth.New(ctx, auth.Options{Store: store, OnLogin: service.onLogin})
	if err != nil {
		return nil, err
	}
	login := &ssoLogin{sso: sso}
	if service.config.FeishuAppID == "" || service.config.FeishuSecret == "" || feishuGrantRedirectURL() == "" {
		log.Printf("auth: 飞书应用没配全，谁都授权不了飞书（搜文档、导入、列群用不了）")
		return login, nil
	}
	login.grants, err = feishugrant.New(ctx, feishugrant.Options{
		Apps: map[string]feishugrant.App{"fbif": {
			AppID:       service.config.FeishuAppID,
			AppSecret:   service.config.FeishuSecret,
			RedirectURL: feishuGrantRedirectURL(),
		}},
		Scopes:  feishuGrantScopes(),
		SealKey: os.Getenv("SSO_TOKEN_KEY"),
		File:    filepath.Join(stateDir, "feishu-grants.json"),
		Who:     grantWho,
		Check:   service.checkGrant,
		Logger:  slog.Default(),
	})
	if err != nil {
		return nil, err
	}
	return login, nil
}

// grantWho：请求上的人（排版工具认出的，或登录模板放进来的）和他用哪个飞书应用授权。
func grantWho(r *http.Request) (string, string) {
	id := layoutUserID(r.Context())
	if id == "" {
		if u := auth.CurrentUser(r); u != nil {
			id = u.ID
		}
	}
	if id == "" {
		return "", ""
	}
	return id, feishuAppFor(id)
}

// checkGrant：授权的飞书账号得是登录的这个人（FBIF 的 sub 就是 union_id），对上了记下他在本应用下的 open_id（超级管理员按它认）。
func (s *authService) checkGrant(r *http.Request, _ string, fu feishugrant.FeishuUser) error {
	return s.checkGrantUser(auth.CurrentUser(r), fu)
}

func (s *authService) checkGrantUser(u *auth.User, fu feishugrant.FeishuUser) error {
	if u == nil {
		return errors.New("没登录")
	}
	if u.CompanyCode != fbifCompanyCode || fu.UnionID == "" || fu.UnionID != u.Sub {
		return errors.New("授权的飞书账号和登录的不是同一个人（union_id 对不上）")
	}
	if err := s.users.SetFeishuOpenID(u.ID, fu.OpenID, s.now()); err != nil {
		return fmt.Errorf("记 open_id: %w", err)
	}
	return nil
}

func (l *ssoLogin) register(mux *http.ServeMux) {
	l.sso.Register(mux)
	if l.grants != nil {
		l.grants.Register(mux, l.sso.RequireUser)
	}
}

func (l *ssoLogin) shutdown(ctx context.Context) error { return l.sso.Shutdown(ctx) }

// machinePathPrefixes 是机器调的接口（监控端交稿、飞书卡片回调、排版接口、程序化排版、监控告警），各用自己的钥匙，
// 不是人的操作：不认人、不记登录日志。
var machinePathPrefixes = []string{
	"/api/publish/site-sync",
	"/api/publish/official-sync",
	"/api/publish/monitor-alert",
	"/api/feishu/card-callback",
	"/api/layout/v1/",
	"/api/render",
	"/api/html-to-blocks",
}

func machinePath(path string) bool {
	for _, prefix := range machinePathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// wrap：/api/ 下带会话 cookie 的请求先过登录模板认人（认出来就把用户 ID 放进 context），
// 认不出也照常往下走：哪些接口要登录由各接口自己判断（currentUser），公开接口照常开。
// 登录模板自己的路由（/api/me、/auth/*）它自己把门，这里不再认一遍。
func (l *ssoLogin) wrap(next http.Handler) http.Handler {
	probed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/me" {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(ssoSessionCookie); err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &probeRecorder{header: http.Header{}}
		var passed *http.Request
		l.sso.RequireUser(http.HandlerFunc(func(_ http.ResponseWriter, inner *http.Request) {
			passed = inner
		})).ServeHTTP(rec, r)
		// 登录模板续期或清掉 cookie 都写在 Set-Cookie 里，照样带给浏览器。
		for _, cookie := range rec.header.Values("Set-Cookie") {
			w.Header().Add("Set-Cookie", cookie)
		}
		if passed != nil {
			if u := auth.CurrentUser(passed); u != nil {
				next.ServeHTTP(w, passed.WithContext(withLayoutUser(passed.Context(), u.ID)))
				return
			}
		}
		if rec.status == http.StatusServiceUnavailable {
			r = r.WithContext(context.WithValue(r.Context(), loginUnavailableKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
	logged := l.sso.Middleware(probed)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if machinePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		logged.ServeHTTP(w, r)
	})
}

// probeRecorder 接住登录模板认人失败时写的回答（不给浏览器），只留状态码和 Set-Cookie。
type probeRecorder struct {
	header http.Header
	status int
}

func (p *probeRecorder) Header() http.Header { return p.header }
func (p *probeRecorder) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}
func (p *probeRecorder) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return len(b), nil
}
