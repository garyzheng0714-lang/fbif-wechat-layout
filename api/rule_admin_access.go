package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

type ruleAdminAccessUserView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Email     string    `json:"email,omitempty"`
	AuthMode  string    `json:"auth_mode"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	LastLogin time.Time `json:"last_login_at,omitempty"`
}

func writeRuleAdminAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAuthenticationRequired):
		writeError(w, http.StatusUnauthorized, "authentication_required")
	case errors.Is(err, errRuleAdminRequired):
		writeError(w, http.StatusForbidden, "rule_admin_required")
	case errors.Is(err, errSuperAdminRequired):
		writeError(w, http.StatusForbidden, "super_admin_required")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func requireRuleAdminRequest(w http.ResponseWriter, r *http.Request, service *authService) bool {
	if service != nil {
		if _, err := service.requireRuleAdmin(r); err != nil {
			writeRuleAdminAccessError(w, err)
			return false
		}
		return true
	}
	// 只有没有装配 runtime auth 的单元测试 mux 才允许使用 CI 操作令牌；
	// 正式入口在 main 中始终传入 authService，不能绕过登录角色。
	if !productionRuntime() && validPublishAdminServiceToken(r.Header.Get(publishAdminServiceHeader)) {
		return true
	}
	writeError(w, http.StatusUnauthorized, "authentication_required")
	return false
}

func ruleAdminAccessView(service *authService, user authUser) ruleAdminAccessUserView {
	return ruleAdminAccessUserView{
		ID:        user.ID,
		Name:      user.Name,
		AvatarURL: user.AvatarURL,
		Email:     user.Email,
		AuthMode:  string(user.AuthMode),
		Role:      string(service.effectiveRole(user)),
		Status:    user.Status,
		LastLogin: user.LastLoginAt,
	}
}

func registerRuleAdminAccessHandlers(mux *http.ServeMux, service *authService) {
	if mux == nil || service == nil {
		return
	}

	mux.HandleFunc("GET /api/rules/admin/access-users", func(w http.ResponseWriter, r *http.Request) {
		if _, err := service.requireSuperAdmin(r); err != nil {
			writeRuleAdminAccessError(w, err)
			return
		}
		users, err := service.users.List()
		if err != nil {
			writeRuleAdminAccessError(w, err)
			return
		}
		views := make([]ruleAdminAccessUserView, 0, len(users))
		for _, user := range users {
			if user.Status == authUserDisabled {
				continue
			}
			views = append(views, ruleAdminAccessView(service, user))
		}
		sort.SliceStable(views, func(i, j int) bool {
			left := strings.ToLower(strings.TrimSpace(views[i].Name))
			right := strings.ToLower(strings.TrimSpace(views[j].Name))
			if left == right {
				return views[i].ID < views[j].ID
			}
			return left < right
		})
		writeJSON(w, http.StatusOK, map[string]any{"users": views})
	})

	mux.HandleFunc("PUT /api/rules/admin/access-users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, err := service.requireSuperAdmin(r); err != nil {
			writeRuleAdminAccessError(w, err)
			return
		}
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeError(w, http.StatusBadRequest, "user id is required")
			return
		}
		var input struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		role := normalizeAuthRole(authRole(input.Role))
		if role == authRoleSuperAdmin {
			writeError(w, http.StatusBadRequest, "超级管理员只能通过服务器配置设置")
			return
		}
		target, ok, err := service.users.Get(id)
		if err != nil {
			writeRuleAdminAccessError(w, err)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		if service.effectiveRole(target) == authRoleSuperAdmin {
			writeError(w, http.StatusBadRequest, "超级管理员不能被降级")
			return
		}
		updated, err := service.users.UpdateRole(id, role, service.now())
		if err != nil {
			writeRuleAdminAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": ruleAdminAccessView(service, updated)})
	})
}
