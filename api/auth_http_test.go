package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fbif-wechat-layout/api/internal/feishugrant"
	"fbif-wechat-layout/api/internal/login"
)

type fakeFeishuProvider struct {
	searchResults []feishuDocument
	exportResult  feishuExportedFile
	exportErr     error
	chatResults   []feishuChat
	searchCalls   int
	exportCalls   int
	chatCalls     int
}

func (f *fakeFeishuProvider) SearchDocuments(_ context.Context, _ string, query string, limit int) ([]feishuDocument, error) {
	f.searchCalls++
	if query == "" || limit <= 0 {
		return nil, nil
	}
	return f.searchResults, nil
}

func (f *fakeFeishuProvider) ExportDocument(_ context.Context, _ string, ref feishuDocumentRef) (feishuExportedFile, error) {
	f.exportCalls++
	if ref.Token == "" {
		return feishuExportedFile{}, errFeishuInvalidDocument
	}
	if f.exportErr != nil {
		return feishuExportedFile{}, f.exportErr
	}
	return f.exportResult, nil
}

func (f *fakeFeishuProvider) ListChats(_ context.Context, _ string) ([]feishuChat, error) {
	f.chatCalls++
	return f.chatResults, nil
}

// fakeFeishuTokens 顶替飞书授权模板：按请求上的人给令牌，没授权回 ErrGrantRequired。
type fakeFeishuTokens struct {
	tokens map[string]string
	err    error // 不为空时 Token 一律回它（飞书暂时连不上之类）
}

func (f *fakeFeishuTokens) Token(r *http.Request) (string, error) {
	user, app := grantWho(r)
	if user == "" {
		return "", feishugrant.ErrLoginRequired
	}
	if app == "" {
		return "", feishugrant.ErrNoFeishu
	}
	if f.err != nil {
		return "", f.err
	}
	token, ok := f.tokens[user]
	if !ok {
		return "", feishugrant.ErrGrantRequired
	}
	return token, nil
}

func (f *fakeFeishuTokens) Status(r *http.Request) string {
	user, app := grantWho(r)
	switch {
	case user == "":
		return ""
	case app == "":
		return "none"
	case f.tokens[user] != "":
		return "ok"
	default:
		return "required"
	}
}

func newTestAuthService(t *testing.T, provider feishuProvider) *authService {
	t.Helper()
	store := newAuthUserStore(filepath.Join(t.TempDir(), "auth-users.json"))
	service, err := newAuthService(authConfig{
		FeishuAppID:  "cli_test",
		FeishuSecret: "app-secret",
		Now:          func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	}, store, provider)
	if err != nil {
		t.Fatalf("newAuthService: %v", err)
	}
	service.tokens = &fakeFeishuTokens{tokens: map[string]string{}}
	service.blocklist = &agentBlocklist{state: blocklistState{Synced: true, Entries: map[string]blockEntry{}}}
	return service
}

// putAccessToken 当这个人已经授权过飞书、手上有这个令牌。
func (s *authService) putAccessToken(userID, token string, _ time.Time) {
	s.tokens.(*fakeFeishuTokens).tokens[userID] = token
}

func registerTestAuthMux(service *authService) *http.ServeMux {
	mux := http.NewServeMux()
	service.Register(mux)
	registerFeishuDocumentRoutes(mux, service)
	registerFeishuChatRoutes(mux, service)
	return mux
}

// mustSessionForUser 把人放进用户库，返回他的用户 ID：测试里请求上的人由 requestWithSession 直接放进 context，
// 生产里是 auth_login.go 的 Wrap 读入口给的 X-FBIF-User 再放。
func mustSessionForUser(t *testing.T, service *authService, user authUser) string {
	t.Helper()
	if err := service.users.Upsert(user); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	return user.ID
}

// requestWithSession 造一个同源请求；userID 不为空就当这个人已登录。
func requestWithSession(method, target, userID string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Origin", "http://example.com")
	if userID != "" {
		req = req.WithContext(withLayoutUser(req.Context(), userID))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestPasswordUserCannotUseFeishuDocuments(t *testing.T) {
	provider := &fakeFeishuProvider{}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID: "account:recVendor", Name: "外部编辑", CompanyCode: accountCompanyCode, AuthMode: authModePassword, Status: authUserActive,
	})
	mux := registerTestAuthMux(service)

	meRec := httptest.NewRecorder()
	mux.ServeHTTP(meRec, requestWithSession(http.MethodGet, "/api/auth/me", session, nil))
	var me struct {
		User authPublicUser `json:"user"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &me); err != nil || meRec.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s err=%v", meRec.Code, meRec.Body.String(), err)
	}
	if me.User.Name != "外部编辑" || me.User.AuthMode != string(authModePassword) || me.User.Capabilities.FeishuDocuments || me.User.Capabilities.Agent || me.User.Feishu != "none" {
		t.Fatalf("account user must not get Feishu document or agent capability: %+v", me.User)
	}

	searchRec := httptest.NewRecorder()
	mux.ServeHTTP(searchRec, requestWithSession(http.MethodGet, "/api/feishu/docs/search?q=新品", session, nil))
	if searchRec.Code != http.StatusForbidden || !strings.Contains(searchRec.Body.String(), `"code":"feishu_login_required"`) {
		t.Fatalf("account search status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}
	if provider.searchCalls != 0 {
		t.Fatalf("account user reached Feishu provider")
	}
}

func TestCurrentUserEndpointReportsLoginAndGrantState(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	mux := registerTestAuthMux(service)

	anonymous := httptest.NewRecorder()
	mux.ServeHTTP(anonymous, requestWithSession(http.MethodGet, "/api/auth/me", "", nil))
	if anonymous.Code != http.StatusUnauthorized || !strings.Contains(anonymous.Body.String(), `"login_url":"/auth/login"`) {
		t.Fatalf("anonymous me status=%d body=%s", anonymous.Code, anonymous.Body.String())
	}
	if got := anonymous.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("me cache-control=%q", got)
	}

	unavailableReq := requestWithSession(http.MethodGet, "/api/auth/me", "", nil)
	unavailableReq = unavailableReq.WithContext(context.WithValue(unavailableReq.Context(), loginUnavailableKey{}, true))
	unavailable := httptest.NewRecorder()
	mux.ServeHTTP(unavailable, unavailableReq)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("登录服务连不上时不能当没登录：status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}

	session := mustSessionForUser(t, service, authUser{
		ID: fbifCompanyCode + ":on_new", Name: "新同事", CompanyCode: fbifCompanyCode, AuthMode: authModeFeishu, Status: authUserActive, FeishuUnionID: "on_new",
	})
	for _, tc := range []struct {
		grant bool
		want  string
	}{{false, "required"}, {true, "ok"}} {
		if tc.grant {
			service.putAccessToken(session, "u-new", time.Time{})
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/auth/me", session, nil))
		var payload struct {
			User authPublicUser `json:"user"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("me status=%d body=%s", rec.Code, rec.Body.String())
		}
		if payload.User.Feishu != tc.want || !payload.User.Capabilities.FeishuDocuments || !payload.User.Capabilities.Agent || payload.User.Role != string(authRoleUser) {
			t.Fatalf("grant=%v user=%+v", tc.grant, payload.User)
		}
	}

	if err := service.users.Upsert(authUser{ID: session, Status: authUserDisabled, AuthMode: authModeFeishu}); err != nil {
		t.Fatal(err)
	}
	disabled := httptest.NewRecorder()
	mux.ServeHTTP(disabled, requestWithSession(http.MethodGet, "/api/auth/me", session, nil))
	if disabled.Code != http.StatusUnauthorized {
		t.Fatalf("停用的人不能再用：status=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestFeishuGrantRequiredIsReportedForUngrantedUser(t *testing.T) {
	provider := &fakeFeishuProvider{}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID: "feishu:ou_old", Name: "老同事", AuthMode: authModeFeishu, Status: authUserActive, FeishuOpenID: "ou_old",
	})
	mux := registerTestAuthMux(service)
	for _, target := range []string{"/api/feishu/docs/search?q=新品", "/api/feishu/chats"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, requestWithSession(http.MethodGet, target, session, nil))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"feishu_grant_required"`) {
			t.Fatalf("%s status=%d body=%s", target, rec.Code, rec.Body.String())
		}
	}
	if provider.searchCalls != 0 || provider.chatCalls != 0 {
		t.Fatalf("没授权就不该调飞书：search=%d chats=%d", provider.searchCalls, provider.chatCalls)
	}

	service.tokens.(*fakeFeishuTokens).err = errors.New("飞书 502")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/feishu/docs/search?q=新品", session, nil))
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("飞书临时故障不能让人重新授权或重新登录：status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// fbifUserHeader 造入口给的 X-FBIF-User：JSON 再 base64url（不带 =）。
func fbifUserHeader(t *testing.T, u login.User) string {
	t.Helper()
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// wrappedMe 经 Wrap 调「我是谁」：header 为空就当请求上没带人。
func wrappedMe(t *testing.T, service *authService, path, header string) *httptest.ResponseRecorder {
	t.Helper()
	mux := registerTestAuthMux(service)
	mux.HandleFunc("POST /api/publish/site-sync", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"layout_user": layoutUserID(r.Context()), "header": r.Header.Get(login.Header)})
	})
	method := http.MethodGet
	if path == "/api/publish/site-sync" {
		method = http.MethodPost
	}
	req := httptest.NewRequest(method, path, nil)
	if header != "" {
		req.Header.Set(login.Header, header)
	}
	rec := httptest.NewRecorder()
	service.Wrap(mux).ServeHTTP(rec, req)
	return rec
}

// 入口给的人对上用户库：老用户（feishu:<open_id>）按 union_id 认回原来的编号、角色和 open_id；
// 新同事、账号密码登录的人按头上的 id 建；停用的 403（不回 401，免得去登录又回来来回跳）。
func TestGatewayUserKeepsLegacyUsersAndRoles(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	if err := service.users.Upsert(authUser{
		ID: "feishu:ou_admin", Name: "旧名字", AuthMode: authModeFeishu, Status: authUserActive,
		Role: authRoleAdmin, FeishuOpenID: "ou_admin", FeishuUnionID: "on_admin",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.users.Upsert(authUser{
		ID: "feishu:ou_gone", AuthMode: authModeFeishu, Status: authUserDisabled, FeishuOpenID: "ou_gone", FeishuUnionID: "on_gone",
	}); err != nil {
		t.Fatal(err)
	}
	me := func(header string) (int, authPublicUser) {
		rec := wrappedMe(t, service, "/api/auth/me", header)
		var body struct {
			User authPublicUser `json:"user"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body.User
	}

	code, user := me(fbifUserHeader(t, login.User{ID: fbifCompanyCode + ":on_admin", Name: "新名字", AvatarURL: "https://example.test/a.png", CompanyCode: fbifCompanyCode}))
	if code != http.StatusOK || user.ID != "feishu:ou_admin" || user.Role != string(authRoleAdmin) || user.Name != "新名字" {
		t.Fatalf("老用户要按 union_id 认回原来的 ID 和角色：%d %+v", code, user)
	}
	stored, _, _ := service.users.Get("feishu:ou_admin")
	if stored.CompanyCode != fbifCompanyCode || stored.FeishuOpenID != "ou_admin" || stored.LastLoginAt.IsZero() {
		t.Fatalf("要更新资料、保留 open_id、记下来过：%+v", stored)
	}

	if code, _ := me(fbifUserHeader(t, login.User{ID: fbifCompanyCode + ":on_gone", Name: "停用的人", CompanyCode: fbifCompanyCode})); code != http.StatusForbidden {
		t.Fatalf("本项目停用的人要 403：%d", code)
	}

	code, user = me(fbifUserHeader(t, login.User{ID: fbifCompanyCode + ":on_new", Name: "新同事", CompanyCode: fbifCompanyCode}))
	if code != http.StatusOK || user.ID != fbifCompanyCode+":on_new" || user.AuthMode != string(authModeFeishu) || user.Role != string(authRoleUser) {
		t.Fatalf("新同事：%d %+v", code, user)
	}
	if stored, _, _ := service.users.Get(fbifCompanyCode + ":on_new"); stored.FeishuUnionID != "on_new" {
		t.Fatalf("新同事要记 union_id：%+v", stored)
	}

	code, user = me(fbifUserHeader(t, login.User{ID: "account:recVendor", Name: "vendor", CompanyCode: accountCompanyCode, Apps: []string{"layout"}}))
	if code != http.StatusOK || user.ID != "account:recVendor" || user.AuthMode != string(authModePassword) || user.Feishu != "none" {
		t.Fatalf("账号密码登录：%d %+v", code, user)
	}

	if app := feishuAppFor("FMZYRL01RNE:on_fude"); app != "" {
		t.Fatalf("富的的人没有本项目的飞书应用，app=%q", app)
	}
}

// 没有头 401、头不对 401、别的程序调的路径不看头（浏览器带来的也删掉）。
func TestGatewayHeaderRequiredAndIgnoredOnProgramPaths(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	if rec := wrappedMe(t, service, "/api/auth/me", ""); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"login_url":"/auth/login"`) {
		t.Fatalf("没有头要 401：%d %s", rec.Code, rec.Body)
	}
	if rec := wrappedMe(t, service, "/api/auth/me", "%%%"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("头不对要 401：%d %s", rec.Code, rec.Body)
	}
	forged := fbifUserHeader(t, login.User{ID: fbifCompanyCode + ":on_boss", Name: "冒充", CompanyCode: fbifCompanyCode})
	rec := wrappedMe(t, service, "/api/publish/site-sync", forged)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "on_boss") {
		t.Fatalf("别的程序调的路径不认头上的人：%d %s", rec.Code, rec.Body)
	}
	if users, _ := service.users.List(); len(users) != 0 {
		t.Fatalf("不该记下冒充的人：%+v", users)
	}
	for _, path := range []string{"/api/health", "/api/feishu/card-callback", "/api/layout/v1/render", "/mcp", "/oauth/token", "/api/agent/uploads/x", "/preview/publish/1"} {
		if !programPath(path) {
			t.Fatalf("%s 应是别的程序调的路径", path)
		}
	}
	for _, path := range []string{"/api/auth/me", "/oauth/authorize", "/auth/feishu-grant", "/api/agent/results/x", "/mp/fbif"} {
		if programPath(path) {
			t.Fatalf("%s 要登录，不能当别的程序的路径", path)
		}
	}
}

// 同一个人资料没变、一小时内来过就不写盘。
func TestRecordVisitThrottlesWrites(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	now := time.Unix(1_800_000_000, 0).UTC()
	visit := authUser{ID: fbifCompanyCode + ":on_a", Name: "甲", AuthMode: authModeFeishu, FeishuUnionID: "on_a", CompanyCode: fbifCompanyCode}
	first, err := service.users.RecordVisit(visit, now)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := service.users.RecordVisit(visit, now.Add(30*time.Minute))
	if !second.LastLoginAt.Equal(first.LastLoginAt) {
		t.Fatalf("一小时内不该再记：%v → %v", first.LastLoginAt, second.LastLoginAt)
	}
	visit.Name = "甲改名"
	third, _ := service.users.RecordVisit(visit, now.Add(40*time.Minute))
	if third.Name != "甲改名" || !third.LastLoginAt.Equal(now.Add(40*time.Minute)) {
		t.Fatalf("资料变了要写：%+v", third)
	}
	fourth, _ := service.users.RecordVisit(visit, now.Add(2*time.Hour))
	if !fourth.LastLoginAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("过了一小时要记：%+v", fourth)
	}
}

func TestCheckGrantRequiresSamePersonAndRecordsOpenID(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	service.config.SuperAdminIDs = map[string]struct{}{"ou_boss": {}}
	userID := mustSessionForUser(t, service, authUser{
		ID: fbifCompanyCode + ":on_boss", Name: "老板", CompanyCode: fbifCompanyCode, AuthMode: authModeFeishu, Status: authUserActive,
	})
	visitor := login.User{ID: fbifCompanyCode + ":on_boss", CompanyCode: fbifCompanyCode}

	if err := service.checkGrantUser(userID, visitor, feishugrant.FeishuUser{OpenID: "ou_other", UnionID: "on_other"}); err == nil {
		t.Fatalf("授权的飞书账号不是登录的人，必须拒绝")
	}
	if err := service.checkGrantUser("account:rec", login.User{ID: "account:on_boss", CompanyCode: accountCompanyCode}, feishugrant.FeishuUser{OpenID: "ou_boss", UnionID: "on_boss"}); err == nil {
		t.Fatalf("账号密码登录的人不能拿别人的飞书授权")
	}
	if err := service.checkGrantUser("", login.User{}, feishugrant.FeishuUser{OpenID: "ou_boss", UnionID: "on_boss"}); err == nil {
		t.Fatalf("没登录不能授权")
	}
	user, _, _ := service.users.Get(userID)
	if service.effectiveRole(user) == authRoleSuperAdmin {
		t.Fatalf("还没授权就不该认出超级管理员")
	}

	if err := service.checkGrantUser(userID, visitor, feishugrant.FeishuUser{OpenID: "ou_boss", UnionID: "on_boss"}); err != nil {
		t.Fatalf("同一个人授权：%v", err)
	}
	user, _, _ = service.users.Get(userID)
	if user.FeishuOpenID != "ou_boss" || service.effectiveRole(user) != authRoleSuperAdmin {
		t.Fatalf("授权后要记下 open_id、按名单认出超级管理员：%+v", user)
	}

	// 老用户（feishu:<open_id>）：授权按用户库编号存，union_id 照入口头上的 sub 核。
	legacy := mustSessionForUser(t, service, authUser{ID: "feishu:ou_old", AuthMode: authModeFeishu, FeishuUnionID: "on_old", Status: authUserActive})
	if err := service.checkGrantUser(legacy, login.User{ID: fbifCompanyCode + ":on_old", CompanyCode: fbifCompanyCode}, feishugrant.FeishuUser{OpenID: "ou_old2", UnionID: "on_old"}); err != nil {
		t.Fatalf("老用户授权：%v", err)
	}
}

func TestGrantWhoUsesLayoutUser(t *testing.T) {
	for _, tc := range []struct{ id, app string }{
		{"feishu:ou_old", "fbif"},
		{fbifCompanyCode + ":on_new", "fbif"},
		{"FMZYRL01RNE:on_fude", ""},
		{"account:rec", ""},
	} {
		req := requestWithSession(http.MethodGet, "/", tc.id, nil)
		if user, app := grantWho(req); user != tc.id || app != tc.app {
			t.Fatalf("%s: user=%q app=%q", tc.id, user, tc.app)
		}
	}
	if user, app := grantWho(httptest.NewRequest(http.MethodGet, "/", nil)); user != "" || app != "" {
		t.Fatalf("没登录：user=%q app=%q", user, app)
	}
}

func TestFeishuDocumentRoutesRequireAuthentication(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	mux := registerTestAuthMux(service)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/feishu/docs/search?q=新品", "", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"authentication_required"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestFeishuChatRouteUsesLoggedInUserToken(t *testing.T) {
	provider := &fakeFeishuProvider{chatResults: []feishuChat{{ChatID: "oc_chat_1", Name: "内容审核群", ChatMode: "group"}}}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID: "feishu:ou_chat", Name: "张三", AuthMode: authModeFeishu, Status: authUserActive,
		FeishuOpenID: "ou_chat",
	})
	service.putAccessToken("feishu:ou_chat", "user-token", service.now().Add(time.Hour))
	rec := httptest.NewRecorder()
	registerTestAuthMux(service).ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/feishu/chats", session, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "内容审核群") {
		t.Fatalf("chat list status=%d body=%s", rec.Code, rec.Body.String())
	}
	if provider.chatCalls != 1 {
		t.Fatalf("chat provider calls=%d, want 1", provider.chatCalls)
	}
}

func TestFeishuLoginCanSearchAndExportDocx(t *testing.T) {
	provider := &fakeFeishuProvider{
		searchResults: []feishuDocument{{
			Token:      "doxcn_test",
			Type:       "docx",
			Title:      "2026 新品趋势",
			URL:        "https://example.feishu.cn/docx/doxcn_test",
			ModifiedAt: time.Unix(1_799_999_000, 0).UTC(),
		}},
		exportResult: feishuExportedFile{
			Filename: "2026 新品趋势.docx",
			Content:  []byte("PK\x03\x04docx-test"),
		},
	}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID:           "feishu:ou_test",
		Name:         "张三",
		AuthMode:     authModeFeishu,
		Status:       authUserActive,
		FeishuOpenID: "ou_test",
	})
	service.putAccessToken("feishu:ou_test", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)

	searchRec := httptest.NewRecorder()
	mux.ServeHTTP(searchRec, requestWithSession(http.MethodGet, "/api/feishu/docs/search?q="+url.QueryEscape("新品")+"&limit=8", session, nil))
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}
	if !strings.Contains(searchRec.Body.String(), `"title":"2026 新品趋势"`) {
		t.Fatalf("search result missing: %s", searchRec.Body.String())
	}

	exportBody := []byte(`{"token":"doxcn_test","type":"docx","title":"2026 新品趋势"}`)
	exportRec := httptest.NewRecorder()
	mux.ServeHTTP(exportRec, requestWithSession(http.MethodPost, "/api/feishu/docs/export", session, exportBody))
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", exportRec.Code, exportRec.Body.String())
	}
	if got := exportRec.Header().Get("Content-Type"); !strings.Contains(got, "application/vnd.openxmlformats-officedocument.wordprocessingml.document") {
		t.Fatalf("content-type=%q", got)
	}
	if got := exportRec.Header().Get("Content-Disposition"); !strings.Contains(got, "filename*=UTF-8''") {
		t.Fatalf("content-disposition=%q", got)
	}
	if !bytes.Equal(exportRec.Body.Bytes(), provider.exportResult.Content) {
		t.Fatalf("export bytes changed")
	}
	if provider.searchCalls != 1 || provider.exportCalls != 1 {
		t.Fatalf("provider calls search=%d export=%d", provider.searchCalls, provider.exportCalls)
	}
}

func TestFeishuDocumentExportStripsEmbeddedImagesBeforeBrowserDownload(t *testing.T) {
	imageBytes := make([]byte, 32<<10)
	if _, err := rand.New(rand.NewSource(1)).Read(imageBytes); err != nil {
		t.Fatal(err)
	}
	docx := zipFixture(t, map[string][]byte{
		"word/document.xml":            []byte(`<w:document/>`),
		"word/_rels/document.xml.rels": []byte(`<Relationships/>`),
		"word/media/image1.png":        imageBytes,
		"[Content_Types].xml":          []byte(`<Types/>`),
		"_rels/.rels":                  []byte(`<Relationships/>`),
	})
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{
			Filename: "图片文章.docx",
			Content:  docx,
		},
	}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID:           "feishu:ou_test",
		Name:         "张三",
		AuthMode:     authModeFeishu,
		Status:       authUserActive,
		FeishuOpenID: "ou_test",
	})
	service.putAccessToken("feishu:ou_test", "u-test", service.now().Add(time.Hour))

	rec := httptest.NewRecorder()
	body := []byte(`{"token":"doxcn_test","type":"docx","title":"图片文章"}`)
	registerTestAuthMux(service).ServeHTTP(
		rec,
		requestWithSession(http.MethodPost, "/api/feishu/docs/export", session, body),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-FBIF-Docx-Original-Bytes"); got == "" {
		t.Fatal("missing original byte count")
	}
	if got := rec.Header().Get("X-FBIF-Docx-Optimized-Bytes"); got == "" {
		t.Fatal("missing optimized byte count")
	}
	if got := rec.Header().Get("X-FBIF-Images-Deferred"); got != "true" {
		t.Fatalf("images deferred=%q, want true", got)
	}
	if rec.Body.Len() >= len(docx) {
		t.Fatalf("optimized docx size=%d, original=%d", rec.Body.Len(), len(docx))
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("open optimized docx: %v", err)
	}
	names := map[string]bool{}
	for _, file := range zr.File {
		names[file.Name] = true
	}
	if names["word/media/image1.png"] {
		t.Fatal("embedded image was still sent inside the browser DOCX")
	}
	if !names["word/_fbif_imageUrls.json"] {
		t.Fatal("optimized DOCX is missing deferred image URL map")
	}
}

func TestFeishuDocumentExportMapsPermissionFailure(t *testing.T) {
	provider := &fakeFeishuProvider{
		exportErr: &feishuExportTaskError{JobStatus: 110, Message: "no permission"},
	}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID:           "feishu:ou_test",
		Name:         "张三",
		AuthMode:     authModeFeishu,
		Status:       authUserActive,
		FeishuOpenID: "ou_test",
	})
	service.putAccessToken("feishu:ou_test", "u-test", service.now().Add(time.Hour))

	rec := httptest.NewRecorder()
	body := []byte(`{"token":"doxcn_test","type":"docx","title":"无权限文档"}`)
	registerTestAuthMux(service).ServeHTTP(
		rec,
		requestWithSession(http.MethodPost, "/api/feishu/docs/export", session, body),
	)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "feishu_document_permission_denied" {
		t.Fatalf("code=%q", payload.Code)
	}
}

func TestFeishuGrantRequestsDocumentAndWikiScopes(t *testing.T) {
	t.Setenv("FEISHU_LOGIN_SCOPES", "")
	scopes := strings.Fields(feishuGrantScopes())
	for _, want := range []string{"drive:drive.search:readonly", "drive:export:readonly", "im:chat:readonly", "wiki:node:read"} {
		found := false
		for _, scope := range scopes {
			if scope == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("授权权限缺 %q，现在是 %v", want, scopes)
		}
	}
}

// 身份菜单的「接入智能体」（红线 51）：只给飞书登录的 FBIF 同事；智能体每次调用还要查停用名单（从没拉到过不放）。
func TestAgentCapabilityOnlyForFeishuColleagues(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	colleague := authUser{ID: fbifCompanyCode + ":on_a", AuthMode: authModeFeishu, FeishuUnionID: "on_a", Status: authUserActive}
	vendor := authUser{ID: "account:recVendor", AuthMode: authModePassword, Status: authUserActive}
	noUnion := authUser{ID: fbifCompanyCode + ":on_b", AuthMode: authModeFeishu, Status: authUserActive}
	if !service.agentCapable(colleague) || service.agentCapable(vendor) || service.agentCapable(noUnion) {
		t.Fatalf("colleague=%v vendor=%v noUnion=%v", service.agentCapable(colleague), service.agentCapable(vendor), service.agentCapable(noUnion))
	}
	if err := service.agentCheck(vendor); err == nil || !strings.Contains(err.Error(), "飞书") {
		t.Fatalf("账号密码登录的不能接智能体：%v", err)
	}
	if err := service.agentCheck(colleague); err != nil {
		t.Fatalf("不在名单上的同事：%v", err)
	}
	service.blocklist.state.Entries["on_a"] = blockEntry{Reason: "离职"}
	if err := service.agentCheck(colleague); err == nil || !strings.Contains(err.Error(), "停用") {
		t.Fatalf("名单上的人不替他干活：%v", err)
	}
	service.blocklist = &agentBlocklist{}
	if err := service.agentCheck(colleague); err == nil {
		t.Fatal("名单从没拉到过不能放")
	}
	service.blocklist = nil
	if err := service.agentCheck(colleague); err == nil {
		t.Fatal("没有名单不能放")
	}
}
