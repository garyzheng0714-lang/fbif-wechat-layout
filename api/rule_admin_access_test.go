package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRuleAdminAccessUsesFeishuSessionRoles(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	service.config.SuperAdminIDs = map[string]struct{}{"ou_super": {}}
	superToken := mustSessionForUser(t, service, authUser{
		ID: "feishu:ou_super", Name: "郑梽煌", AuthMode: authModeFeishu,
		FeishuOpenID: "ou_super", Status: authUserActive,
	})
	memberToken := mustSessionForUser(t, service, authUser{
		ID: "feishu:ou_member", Name: "普通成员", AuthMode: authModeFeishu,
		FeishuOpenID: "ou_member", Status: authUserActive,
	})

	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")), service)
	registerRuleAdminAccessHandlers(mux, service)

	request := func(method, target, token string, body []byte) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, requestWithSession(method, target, token, body))
		return rec
	}

	if rec := request(http.MethodGet, "/api/rules/admin/presets", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous rule admin status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodGet, "/api/rules/admin/presets", memberToken, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("ordinary member rule admin status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodGet, "/api/rules/admin/presets", superToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("super admin rule admin status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec := request(http.MethodGet, "/api/rules/admin/access-users", superToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("access user list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Users []ruleAdminAccessUserView `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode access users: %v", err)
	}
	if len(list.Users) != 2 {
		t.Fatalf("access user count=%d, want 2", len(list.Users))
	}
	for _, user := range list.Users {
		if user.ID == "feishu:ou_super" && user.Role != string(authRoleSuperAdmin) {
			t.Fatalf("super admin role=%q", user.Role)
		}
	}

	rec = request(http.MethodPut, "/api/rules/admin/access-users/feishu:ou_member", memberToken, []byte(`{"role":"admin"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ordinary member role update status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = request(http.MethodPut, "/api/rules/admin/access-users/feishu:ou_member", superToken, []byte(`{"role":"admin"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("super admin role update status=%d body=%s", rec.Code, rec.Body.String())
	}

	adminUser, ok, err := service.users.Get("feishu:ou_member")
	if err != nil || !ok {
		t.Fatalf("read promoted user: ok=%v err=%v", ok, err)
	}
	adminToken, err := service.issueSession(adminUser)
	if err != nil {
		t.Fatalf("issue admin session: %v", err)
	}
	if rec := request(http.MethodGet, "/api/rules/admin/presets", adminToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("promoted admin rule admin status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPut, "/api/rules/admin/access-users/feishu:ou_super", superToken, []byte(`{"role":"admin"}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("super admin downgrade status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRuleAdminPublicIdentityExposesEffectiveRole(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	service.config.SuperAdminIDs = map[string]struct{}{"ou_super": {}}
	token := mustSessionForUser(t, service, authUser{
		ID: "feishu:ou_super", Name: "郑梽煌", AuthMode: authModeFeishu,
		FeishuOpenID: "ou_super", Status: authUserActive,
	})
	mux := registerTestAuthMux(service)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/me", token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("current user status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		User authPublicUser `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode current user: %v", err)
	}
	if payload.User.Role != string(authRoleSuperAdmin) || !payload.User.Capabilities.RuleSuperAdmin {
		t.Fatalf("unexpected effective role: %+v", payload.User)
	}
}
