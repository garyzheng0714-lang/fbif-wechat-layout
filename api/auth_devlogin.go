//go:build devlogin

package main

// auth_devlogin.go — 只给本地端到端测试用的登录（go run -tags devlogin . 且 AUTH_DEV_LOGIN=true）：
// POST /auth/dev-login {"username","name"} 直接登成 account:<username>，不验密码。生产编译里没有这个文件；
// 编进来了、ENV=production 也拒绝启动（newRuntimeAuthService）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	devLoginCookie = "layout_dev_sid"
	// feishuGrantStartPath 在生产里是授权模板挂的 /auth/feishu-grant；本地测试没有飞书，直接回 next。
	feishuGrantStartPath = "/auth/feishu-grant"
)

func init() {
	devLoginBackend = func(users *authUserStore) (loginBackend, feishuTokenSource) {
		return &devLogin{users: users}, nil
	}
}

type devLogin struct{ users *authUserStore }

func (d *devLogin) register(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/dev-login", noStoreHandler(d.handleLogin))
	mux.HandleFunc("GET /auth/login", noStoreHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("本地测试登录：POST /auth/dev-login {\"username\":\"…\"}\n"))
	}))
	mux.HandleFunc("POST /auth/feishu/logout", noStoreHandler(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: devLoginCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET "+feishuGrantStartPath, func(w http.ResponseWriter, r *http.Request) {
		next := r.URL.Query().Get("next")
		if parsed, err := url.Parse(next); err != nil || next == "" || parsed.IsAbs() || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
			next = "/"
		}
		http.Redirect(w, r, next, http.StatusFound)
	})
}

func (d *devLogin) handleLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_login_request", "请输入账号")
		return
	}
	username := strings.ToLower(strings.TrimSpace(input.Username))
	if username == "" {
		writeAuthError(w, http.StatusBadRequest, "invalid_login_request", "请输入账号")
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = username
	}
	user, err := d.users.RecordLogin(authUser{
		ID:          accountCompanyCode + ":" + username,
		Name:        name,
		CompanyCode: accountCompanyCode,
		AuthMode:    authModePassword,
		Status:      authUserActive,
	}, time.Now())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "user_store_failed", "账号服务暂不可用")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: devLoginCookie, Value: user.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": user.ID})
}

func (d *devLogin) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(devLoginCookie); err == nil && c.Value != "" {
			r = r.WithContext(withLayoutUser(r.Context(), c.Value))
		}
		next.ServeHTTP(w, r)
	})
}

func (d *devLogin) shutdown(context.Context) error { return nil }

// page：本地测试照常发页面，没登录由页面自己的登录检查处理（本地 /auth/login 只是一行说明）。
func (d *devLogin) page(next http.Handler) http.Handler { return next }
