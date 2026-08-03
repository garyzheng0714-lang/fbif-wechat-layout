package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestEditorAuthGateRedirectsAnonymousEditorEntries(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	handler := withEditorAuthGate(service, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("anonymous request reached the editor shell")
	}))

	tests := []struct {
		path     string
		returnTo string
	}{
		{"/", "/mp/fbif"},
		{"/index.html", "/mp/fbif"},
		{"/app.html", "/app.html"},
		{"/site.html", "/site.html"},
		{"/mp/fbif", "/mp/fbif"},
		{"/mp/fbif?v=release", "/mp/fbif?v=release"},
		{"/ft/ft-default", "/ft/ft-default"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusFound {
				t.Fatalf("status=%d, want 302", rec.Code)
			}
			want := "/login.html?return_to=" + url.QueryEscape(tc.returnTo)
			if got := rec.Header().Get("Location"); got != want {
				t.Fatalf("Location=%q, want %q", got, want)
			}
			if got := rec.Header().Get("X-Login-Required"); got != "1" {
				t.Fatalf("X-Login-Required=%q, want 1", got)
			}
		})
	}
}

func TestEditorAuthGateAllowsAuthenticatedEditorAndPublicAssets(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	user := authUser{
		ID:       "password:gate-user",
		Name:     "门禁用户",
		AuthMode: authModePassword,
		Status:   authUserActive,
	}
	token := mustSessionForUser(t, service, user)
	handler := withEditorAuthGate(service, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	authenticated := httptest.NewRecorder()
	handler.ServeHTTP(
		authenticated,
		requestWithSession(http.MethodGet, "/mp/fbif", token, nil),
	)
	if authenticated.Code != http.StatusNoContent {
		t.Fatalf("authenticated status=%d, want 204", authenticated.Code)
	}

	publicAsset := httptest.NewRecorder()
	handler.ServeHTTP(publicAsset, httptest.NewRequest(http.MethodGet, "/css/app-shell.css", nil))
	if publicAsset.Code != http.StatusNoContent {
		t.Fatalf("asset status=%d, want 204", publicAsset.Code)
	}
}
