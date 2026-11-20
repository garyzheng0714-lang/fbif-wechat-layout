package main

// auth_login.go — 「请求上是谁」只读入口给的请求头 X-FBIF-User（internal/login，模板原样复制）；本人飞书授权
// （internal/feishugrant，模板原样复制）和登录分开做。本项目没有登录代码：入口（Caddy）每个请求先问 FBIF 登录中心，
// 没登录的页面由中心跳去登录、接口由中心回 401；/auth/*、/api/me 入口直接转给登录中心（本项目自己的两条授权路由例外，
// 见 deploy/caddy/fbifmp-layout.caddy）。这里只写本项目的那几项：请求头上的人对上用户库（老用户编号、角色不变）、
// 谁能授权飞书、「我是谁」回什么、智能体放谁。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fbif-wechat-layout/api/internal/feishugrant"
	"fbif-wechat-layout/api/internal/login"
)

const (
	// fbifCompanyCode 是 FBIF 在统一登录里的企业编号；FBIF 的 sub 就是飞书 union_id。
	fbifCompanyCode = "FJY632B9P3E"
	// accountCompanyCode 是账号密码登录的人（登录中心）的企业编号。
	accountCompanyCode = "account"
	// legacyFeishuUserPrefix：2026-11-10 换统一登录前用飞书登录过的人，用户 ID 是 feishu:<open_id>，都是 FBIF 的。
	legacyFeishuUserPrefix = "feishu:"
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

// newRuntimeAuthService 装配用户库、智能体的停用名单和本人飞书授权，都放在用户库同一个目录。
// 停用名单（agent_blocklist.go）用 SSO_HUB_URL、SSO_HUB_KEY 问中台，后台跟着 ctx 拉；
// 飞书授权 feishu-grants.json 的 refresh_token 用 SSO_TOKEN_KEY 加密：还是原来那把，已经授权过的人不用重新授权（变量名沿用登录模板时的叫法）。
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
	stateDir := filepath.Dir(usersPath)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("auth: 建 %s: %w", stateDir, err)
	}
	service.blocklist = newAgentBlocklistFromEnv(filepath.Join(stateDir, "agent-blocklist.json"))
	service.blocklist.Start(ctx)
	if config.FeishuAppID == "" || config.FeishuSecret == "" || feishuGrantRedirectURL() == "" {
		log.Printf("auth: 飞书应用没配全，谁都授权不了飞书（搜文档、导入、列群用不了）")
		return service, nil
	}
	grants, err := feishugrant.New(ctx, feishugrant.Options{
		Apps: map[string]feishugrant.App{"fbif": {
			AppID:       config.FeishuAppID,
			AppSecret:   config.FeishuSecret,
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
	service.grants = grants
	service.tokens = grants
	return service, nil
}

// Register 挂本人飞书授权（GET /auth/feishu-grant 和回调）和「我是谁」（GET /api/auth/me）。
func (s *authService) Register(mux *http.ServeMux) {
	if s.grants != nil {
		s.grants.Register(mux, s.requireLogin)
	}
	mux.HandleFunc("GET /api/auth/me", noStoreHandler(s.handleCurrentUser))
}

// programPathPrefixes 是不过登录的路径：别的程序调的（健康检查、监控端交稿和告警、飞书卡片回调、排版接口、程序化排版、
// 智能体的 MCP 和换令牌、传 Word），各用自己的钥匙或签名；带签名的审核预览页；按内容哈希取的图片缓存（上传服务、
// 微信后台不带登录也要取得到，和以前一样公开）。入口不拦，这里也不认请求头上的人（浏览器自己带的 X-FBIF-User 删掉）。
// 和 deploy/caddy/fbifmp-layout.caddy 的 @program 是同一张单子（tests/deploy-transaction.test.mjs 核对）。
// 授权页 /oauth/authorize 是人在浏览器里点的，要登录，不在这里。
var programPathPrefixes = []string{
	"/api/health",
	"/api/publish/site-sync",
	"/api/publish/official-sync",
	"/api/publish/monitor-alert",
	"/api/feishu/card-callback",
	"/api/layout/v1/",
	"/api/render",
	"/api/html-to-blocks",
	"/api/agent/uploads/",
	"/mcp",
	"/oauth/token",
	"/oauth/register",
	"/oauth/revoke",
	"/.well-known/oauth-",
	"/preview/",
	"/api/doc-cache/",
}

func programPath(path string) bool {
	for _, prefix := range programPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// Wrap 包住整个服务：请求头上有人就对上用户库，把用户 ID 放进 context；没有就照常往下走，
// 哪些接口要登录由各接口自己判断（currentUser）。正常情况下要登录的路径没登录到不了这里（入口挡了）。
func (s *authService) Wrap(next http.Handler) http.Handler {
	identified := login.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visitor, _ := login.Current(r)
		user, err := s.users.RecordVisit(visitorFromHeader(visitor), s.now())
		switch {
		case err != nil:
			// 用户库读写不了：当登录服务一时不可用（503），不能回 401，否则页面去登录、登完回来又 401，来回跳。
			log.Printf("auth: 用户库记不下 %s：%v", visitor.ID, err)
			r = r.WithContext(context.WithValue(r.Context(), loginUnavailableKey{}, true))
		case user.Status != authUserActive:
			// 管理员在本项目停用的人：入口放行了也不让用。回 403 不回 401，免得去登录、登完又被挡，来回跳。
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeAuthError(w, http.StatusForbidden, "account_disabled", "这个账号在排版工具里已停用，请找管理员")
			} else {
				http.Error(w, "这个账号在排版工具里已停用，请找管理员", http.StatusForbidden)
			}
			return
		default:
			r = r.WithContext(withLayoutUser(r.Context(), user.ID))
		}
		next.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if programPath(r.URL.Path) {
			r.Header.Del(login.Header)
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get(login.Header) == "" {
			next.ServeHTTP(w, r)
			return
		}
		identified.ServeHTTP(w, r)
	})
}

// visitorFromHeader 把入口给的人换成用户库的样子：FBIF 飞书登录的带上 union_id（sub 就是 union_id），
// 用户库按它认回换统一登录前的老编号 feishu:<open_id>。
func visitorFromHeader(u login.User) authUser {
	visitor := authUser{
		ID:          u.ID,
		Name:        u.Name,
		AvatarURL:   u.AvatarURL,
		Email:       u.Email,
		CompanyCode: u.CompanyCode,
		AuthMode:    authModeFeishu,
		Status:      authUserActive,
	}
	if u.CompanyCode == accountCompanyCode {
		visitor.AuthMode = authModePassword
	}
	if sub, ok := strings.CutPrefix(u.ID, fbifCompanyCode+":"); ok && u.CompanyCode == fbifCompanyCode {
		visitor.FeishuUnionID = sub
	}
	return visitor
}

// requireLogin 包住要登录才能用的路由（飞书授权两条）：请求上没有人回 401，前端带去登录。
func (s *authService) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if layoutUserID(r.Context()) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login_required", "code": "authentication_required", "login_url": "/auth/login"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Page 包住浏览器打开的页面（智能体授权页）：请求上没有人就去登录、登完回到这一页。入口没登录会先跳，
// 这里只兜入口没拦住的情况；登录中心一时查不了（入口放行、不带人）不跳，由页面说清楚。
func (s *authService) Page(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, unavailable := r.Context().Value(loginUnavailableKey{}).(bool)
		if layoutUserID(r.Context()) == "" && !unavailable && r.Header.Get(login.Header) == "" {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/auth/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
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

// grantWho：请求上的人（用户库里的编号，飞书授权按它存）和他用哪个飞书应用授权。
func grantWho(r *http.Request) (string, string) {
	id := layoutUserID(r.Context())
	if id == "" {
		return "", ""
	}
	return id, feishuAppFor(id)
}

// checkGrant：授权的飞书账号得是登录的这个人（FBIF 的 sub 就是 union_id），对上了记下他在本应用下的 open_id（超级管理员按它认）。
func (s *authService) checkGrant(r *http.Request, _ string, fu feishugrant.FeishuUser) error {
	visitor, _ := login.Current(r)
	return s.checkGrantUser(layoutUserID(r.Context()), visitor, fu)
}

func (s *authService) checkGrantUser(userID string, visitor login.User, fu feishugrant.FeishuUser) error {
	if userID == "" {
		return errors.New("没登录")
	}
	sub, ok := strings.CutPrefix(visitor.ID, fbifCompanyCode+":")
	if !ok || visitor.CompanyCode != fbifCompanyCode || fu.UnionID == "" || fu.UnionID != sub {
		return errors.New("授权的飞书账号和登录的不是同一个人（union_id 对不上）")
	}
	if err := s.users.SetFeishuOpenID(userID, fu.OpenID, s.now()); err != nil {
		return fmt.Errorf("记 open_id: %w", err)
	}
	return nil
}

// feishuColleague 只有飞书登录的 FBIF 同事能接智能体：账号密码登录的外部编辑没有飞书身份。
func feishuColleague(user authUser) bool {
	return feishuAppFor(user.ID) != "" && user.FeishuUnionID != ""
}

// agentCheck 这个人现在能不能让智能体替他用排版工具（agent_mcp.go 每次调用、每次换令牌都问）：
// 飞书登录的 FBIF 同事，并且不在停用名单上。智能体的令牌不经入口，离职、停用由这里查（名单从没拉到过也不放）。
func (s *authService) agentCheck(user authUser) error {
	if !feishuColleague(user) {
		return errors.New("只有用飞书登录的 FBIF 同事能让智能体用排版工具")
	}
	return s.blocklist.Check(user.FeishuUnionID)
}
