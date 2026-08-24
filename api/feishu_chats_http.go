package main

// feishu_chats_http.go — 登录用户可见的飞书群目录。
// user_access_token 只在服务端调用 provider，永远不返回给浏览器。

import (
	"context"
	"errors"
	"net/http"
)

func registerFeishuChatRoutes(mux *http.ServeMux, service *authService) {
	mux.HandleFunc("GET /api/feishu/chats", noStoreHandler(service.handleFeishuChatList))
}

func feishuChatErrorResponse(err error) (int, string, string) {
	switch {
	case errors.Is(err, errAuthenticationRequired):
		return http.StatusUnauthorized, "authentication_required", "请先登录"
	case errors.Is(err, errFeishuLoginRequired):
		return http.StatusForbidden, "feishu_login_required", "读取审核群需要使用飞书账号登录。请退出后用飞书登录。"
	default:
		return http.StatusBadGateway, "feishu_chat_list_failed", "飞书群列表暂时读不到，请稍后重试。已保存的审核群不受影响。"
	}
}

func writeFeishuChatError(w http.ResponseWriter, err error) {
	status, code, message := feishuChatErrorResponse(err)
	writeAuthError(w, status, code, message)
}

func (s *authService) handleFeishuChatList(w http.ResponseWriter, r *http.Request) {
	user, err := s.requireFeishuUser(r)
	if err != nil {
		writeFeishuChatError(w, err)
		return
	}
	chats, err := s.listFeishuChats(r.Context(), user)
	if err != nil {
		writeFeishuChatError(w, err)
		return
	}
	if chats == nil {
		chats = []feishuChat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
}

func (s *authService) listFeishuChats(ctx context.Context, user authUser) ([]feishuChat, error) {
	accessToken, err := s.userAccessToken(ctx, user)
	if err != nil {
		return nil, err
	}
	return s.feishu.ListChats(ctx, accessToken)
}
