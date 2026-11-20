// Package login 读入口给的「这是谁」。原样复制自 feishu-login-guide 技能 templates/gateway/go/login.go。
//
// 本项目不做登录：入口（Caddy）每个请求先问 FBIF 登录中心，没登录的页面由中心跳去登录、接口由中心回 401；
// 放行时中心把这是谁写进 X-FBIF-User（JSON 再 base64url，不带 =），入口先删掉浏览器自己带的同名头。
// /auth/*、/api/me 由入口直接转给中心，本项目不写这些路由。
//
// 本项目只听 127.0.0.1，外面的请求只能经入口进来，所以这个头可信。
package login

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

const Header = "X-FBIF-User"

type User struct {
	ID          string   `json:"id"` // 飞书登录：<company_code>:<sub>；账号密码登录：account:<记录 ID>。按人存数据、分权限都用它
	Name        string   `json:"name"`
	Email       string   `json:"email,omitempty"`
	AvatarURL   string   `json:"avatar_url,omitempty"`
	CompanyCode string   `json:"company_code"`   // FJY632B9P3E（FBIF）、FMZYRL01RNE（富的）、account（账号密码）
	Apps        []string `json:"apps,omitempty"` // 只有账号密码登录的人有：账号表里能用的应用
}

type ctxKey struct{}

// Require 包住要登录的处理：没有头或头不对回 401，前端跳 /auth/login。正常到不了这里就被入口挡了，到了说明入口配错，宁可挡住。
func Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := Parse(r.Header.Get(Header))
		if !ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"login_required","login_url":"/auth/login"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

// Current 是 Require 放进来的人。
func Current(r *http.Request) (User, bool) {
	u, ok := r.Context().Value(ctxKey{}).(User)
	return u, ok
}

// Parse 解 X-FBIF-User 的值。
func Parse(h string) (User, bool) {
	if h == "" {
		return User{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(h)
	if err != nil {
		return User{}, false
	}
	var u User
	if json.Unmarshal(raw, &u) != nil || u.ID == "" {
		return User{}, false
	}
	return u, true
}
