package main

// auth_service.go — 排版工具里「谁在用、是什么角色、能不能替他调飞书」的全部判断。
//
//   - 谁在用：FBIF 统一登录（internal/auth，登录模板原样复制）认人，接线在 auth_login.go；
//     本项目没有登录页、不收密码、不发会话。业务代码只调 currentUser / requireFeishuUser / requireRuleAdmin / requireSuperAdmin。
//   - 角色：记在 auth-users.json（auth_users.go）；超级管理员只认部署配置 RULE_SUPER_ADMIN_OPEN_IDS 里的 open_id。
//   - 调飞书开放平台（搜文档、导出、列群）：本人授权一次的 user_access_token（internal/feishugrant，模板原样复制），
//     只用 userAccessToken 取，令牌不出后端。

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"fbif-wechat-layout/api/internal/feishugrant"
)

var (
	errAuthenticationRequired = errors.New("authentication required")
	// errLoginUnavailable：登录服务暂时核对不了这个人（集成平台或中台连不上），回 503，不当没登录。
	errLoginUnavailable    = errors.New("login service unavailable")
	errFeishuLoginRequired = errors.New("feishu login required")
	// errFeishuGrantRequired：还没授权飞书，或飞书明确拒绝了续期；前端带他去 /auth/feishu-grant 授权一次。
	errFeishuGrantRequired = errors.New("feishu grant required")
	errRuleAdminRequired   = errors.New("rule admin required")
	errSuperAdminRequired  = errors.New("super admin required")
)

type authCapabilities struct {
	FeishuDocuments bool `json:"feishu_documents"`
	RuleAdmin       bool `json:"rule_admin"`
	RuleSuperAdmin  bool `json:"rule_super_admin"`
	// Agent 能接智能体（红线 51）：编辑器身份菜单据此给「接入智能体」；停用名单、账号状态在每次调用时另查。
	Agent bool `json:"agent"`
}

type authPublicUser struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	AvatarURL    string           `json:"avatar_url,omitempty"`
	Email        string           `json:"email,omitempty"`
	AuthMode     string           `json:"auth_mode"`
	Role         string           `json:"role"`
	Capabilities authCapabilities `json:"capabilities"`
	// Feishu 是飞书授权状态：ok 已授权；required 要授权（前端给「授权飞书」）；none 授权不了（不是 FBIF 飞书登录的）。
	Feishu string `json:"feishu,omitempty"`
}

type authConfig struct {
	// FeishuAppID / FeishuSecret 是本项目的飞书应用（FEISHU_LOGIN_APP_*）：本人授权用它，排版接口读文档用它的应用身份。
	FeishuAppID   string
	FeishuSecret  string
	Now           func() time.Time
	SuperAdminIDs map[string]struct{}
}

// feishuTokenSource 是飞书授权模板对本项目露出的两件事；生产是 *feishugrant.Grants，测试换成假的。
type feishuTokenSource interface {
	Token(r *http.Request) (string, error)
	Status(r *http.Request) string
}

type authService struct {
	config           authConfig
	users            *authUserStore
	feishu           feishuProvider
	tokens           feishuTokenSource // nil：飞书应用没配，谁都授权不了
	login            loginBackend      // nil：没装配登录（单元测试），请求上的人由测试放进 context
	feishuExportJobs *feishuExportJobManager
}

func newAuthService(config authConfig, users *authUserStore, provider feishuProvider) (*authService, error) {
	if users == nil {
		return nil, errors.New("auth user store is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if provider == nil {
		provider = newHTTPFeishuProvider(http.DefaultClient)
	}
	feishuExportJobs := newFeishuExportJobManager(config.Now)
	feishuExportJobs.startGC(feishuExportJobGCInterval)
	return &authService{
		config:           config,
		users:            users,
		feishu:           provider,
		feishuExportJobs: feishuExportJobs,
	}, nil
}

func (s *authService) now() time.Time {
	return s.config.Now().UTC()
}

// ---------- 请求上的人 ----------

type layoutUserKey struct{}
type loginUnavailableKey struct{}

// withLayoutUser 把本项目用户 ID 放进 context：登录接线（auth_login.go）给每个已登录请求放，后台任务取飞书令牌时也放。
func withLayoutUser(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, layoutUserKey{}, id)
}

func layoutUserID(ctx context.Context) string {
	id, _ := ctx.Value(layoutUserKey{}).(string)
	return id
}

func (s *authService) currentUser(r *http.Request) (authUser, error) {
	id := layoutUserID(r.Context())
	if id == "" {
		if unavailable, _ := r.Context().Value(loginUnavailableKey{}).(bool); unavailable {
			return authUser{}, errLoginUnavailable
		}
		return authUser{}, errAuthenticationRequired
	}
	user, ok, err := s.users.Get(id)
	if err != nil {
		return authUser{}, err
	}
	if !ok || user.Status != authUserActive {
		return authUser{}, errAuthenticationRequired
	}
	return user, nil
}

func (s *authService) requireFeishuUser(r *http.Request) (authUser, error) {
	user, err := s.currentUser(r)
	if err != nil {
		return authUser{}, err
	}
	if feishuAppFor(user.ID) == "" {
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

// agentCapable 身份菜单给不给「接入智能体」：按登录方式定（没有登录后端的测试按飞书同事算）。
func (s *authService) agentCapable(user authUser) bool {
	if s.login != nil {
		return s.login.agentCapable(user)
	}
	return feishuColleague(user)
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
			FeishuDocuments: feishuAppFor(user.ID) != "",
			RuleAdmin:       role == authRoleAdmin || role == authRoleSuperAdmin,
			RuleSuperAdmin:  role == authRoleSuperAdmin,
			Agent:           s.agentCapable(user),
		},
	}
}

// ---------- 飞书令牌 ----------

// userAccessToken 给这个人拿本人授权的飞书 user_access_token（快过期由授权模板替他换）；请求里、后台任务里都能调。
// 错误：errAuthenticationRequired、errFeishuLoginRequired（不是 FBIF 飞书登录的）、errFeishuGrantRequired（要授权），
// 别的是飞书暂时连不上。
func (s *authService) userAccessToken(ctx context.Context, user authUser) (string, error) {
	if s.tokens == nil || feishuAppFor(user.ID) == "" {
		return "", errFeishuLoginRequired
	}
	req, err := http.NewRequestWithContext(withLayoutUser(ctx, user.ID), http.MethodGet, "/", nil)
	if err != nil {
		return "", err
	}
	token, err := s.tokens.Token(req)
	switch {
	case errors.Is(err, feishugrant.ErrLoginRequired):
		return "", errAuthenticationRequired
	case errors.Is(err, feishugrant.ErrNoFeishu):
		return "", errFeishuLoginRequired
	case errors.Is(err, feishugrant.ErrGrantRequired):
		return "", errFeishuGrantRequired
	}
	return token, err
}

// feishuGrantStatus 是「我是谁」里的飞书授权状态（顺便记「最近用过」，30 天内用过的人后台替他续）。
func (s *authService) feishuGrantStatus(r *http.Request, user authUser) string {
	if s.tokens == nil || feishuAppFor(user.ID) == "" {
		return "none"
	}
	return s.tokens.Status(r)
}
