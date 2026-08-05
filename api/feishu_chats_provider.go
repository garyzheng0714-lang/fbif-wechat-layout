package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// feishuChat 是管理后台展示用的最小群信息。chat_id 只作为服务端配置键
// 使用，不在前端直接要求用户输入；名称、头像等展示字段来自当前登录用户
// 可见的群列表。
type feishuChat struct {
	ChatID           string `json:"chat_id"`
	Name             string `json:"name"`
	Avatar           string `json:"avatar,omitempty"`
	Description      string `json:"description,omitempty"`
	External         bool   `json:"external,omitempty"`
	ChatMode         string `json:"chat_mode,omitempty"`
	GroupMessageType string `json:"group_message_type,omitempty"`
}

// ListChats 使用当前登录用户的 user_access_token 获取其所在的群。分页和
// chat_mode 过滤都在服务端完成，前端只接收可以用于选择的群展示信息。
func (p *httpFeishuProvider) ListChats(ctx context.Context, accessToken string) ([]feishuChat, error) {
	const pageSize = 100
	const maxChats = 5000
	pageToken := ""
	chats := make([]feishuChat, 0, 32)
	for page := 0; page < 100; page++ {
		var response struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Items []struct {
					ChatID           string `json:"chat_id"`
					Name             string `json:"name"`
					Avatar           string `json:"avatar"`
					Description      string `json:"description"`
					External         bool   `json:"external"`
					ChatMode         string `json:"chat_mode"`
					GroupMessageType string `json:"group_message_type"`
				} `json:"items"`
				PageToken string `json:"page_token"`
				HasMore   bool   `json:"has_more"`
			} `json:"data"`
		}
		values := url.Values{}
		values.Set("user_id_type", "open_id")
		values.Set("page_size", strconv.Itoa(pageSize))
		if pageToken != "" {
			values.Set("page_token", pageToken)
		}
		endpoint := p.apiBaseURL + "/open-apis/im/v1/chats?" + values.Encode()
		if err := p.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &response); err != nil {
			return nil, err
		}
		if response.Code != 0 {
			return nil, fmt.Errorf("feishu chat list failed code=%d msg=%s", response.Code, response.Msg)
		}
		for _, item := range response.Data.Items {
			chatID := strings.TrimSpace(item.ChatID)
			if chatID == "" {
				continue
			}
			// 用户所在的会话列表可能包含 p2p 单聊。发布审核只允许群，
			// 但兼容旧响应没有 chat_mode 的情况，不误删可用群。
			mode := strings.ToLower(strings.TrimSpace(item.ChatMode))
			if mode != "" && mode != "group" && mode != "topic" {
				continue
			}
			chats = append(chats, feishuChat{
				ChatID:           chatID,
				Name:             strings.TrimSpace(item.Name),
				Avatar:           strings.TrimSpace(item.Avatar),
				Description:      strings.TrimSpace(item.Description),
				External:         item.External,
				ChatMode:         mode,
				GroupMessageType: strings.TrimSpace(item.GroupMessageType),
			})
			if len(chats) >= maxChats {
				return chats, nil
			}
		}
		if !response.Data.HasMore || strings.TrimSpace(response.Data.PageToken) == "" {
			break
		}
		if response.Data.PageToken == pageToken {
			return nil, errors.New("feishu chat list returned a repeated page token")
		}
		pageToken = strings.TrimSpace(response.Data.PageToken)
	}
	return chats, nil
}
