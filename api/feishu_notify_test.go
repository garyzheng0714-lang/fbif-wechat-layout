package main

// feishu_notify_test.go — 飞书通知测试（httptest 桩，不触真实飞书）。
// 通知是流水线的加速器不是闸门，但 token 缓存和禁用语义仍要钉住：
// 缓存失效会让每条通知多打一次鉴权接口，env 缺失必须静默禁用而非报错刷屏。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFeishuNotifierSendAndTokenCache 发文本走 tenant_access_token 两步：token 只取一次
// （缓存生效），消息体形态与 receive_id_type=chat_id 契约对齐。
func TestFeishuNotifierSendAndTokenCache(t *testing.T) {
	tokenCalls := 0
	var gotAuth, gotQuery string
	var gotMsg struct {
		ReceiveID string `json:"receive_id"`
		MsgType   string `json:"msg_type"`
		Content   string `json:"content"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokenCalls++
			var req struct {
				AppID     string `json:"app_id"`
				AppSecret string `json:"app_secret"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.AppID != "cli_test" || req.AppSecret != "secret_test" {
				t.Errorf("鉴权凭证不符：%+v", req)
			}
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok-1","expire":7200}`)
		case "/open-apis/im/v1/messages":
			gotAuth = r.Header.Get("Authorization")
			gotQuery = r.URL.RawQuery
			json.NewDecoder(r.Body).Decode(&gotMsg)
			fmt.Fprint(w, `{"code":0,"msg":"ok"}`)
		default:
			t.Errorf("飞书桩收到意外路径 %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	n := &feishuNotifier{
		baseURL:    srv.URL,
		appID:      "cli_test",
		appSecret:  "secret_test",
		chatID:     "oc_test_chat",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	if err := n.send(context.Background(), "第一条"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	if err := n.send(context.Background(), "第二条"); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	if tokenCalls != 1 {
		t.Fatalf("token 应缓存复用（1 次），实际取了 %d 次", tokenCalls)
	}
	if gotAuth != "Bearer tok-1" || gotQuery != "receive_id_type=chat_id" {
		t.Fatalf("发消息请求形态不符：auth=%q query=%q", gotAuth, gotQuery)
	}
	if gotMsg.ReceiveID != "oc_test_chat" || gotMsg.MsgType != "text" || !strings.Contains(gotMsg.Content, "第二条") {
		t.Fatalf("消息体不符：%+v", gotMsg)
	}
}

// TestFeishuNotifierDisabledWhenEnvMissing FEISHU_* 任一缺失 → 构造返回 nil（通知禁用），
// 流水线照常运行——通知配置永远不该成为发布的阻塞项。
func TestFeishuNotifierDisabledWhenEnvMissing(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_x")
	if n := newFeishuNotifierFromEnv(); n != nil {
		t.Fatalf("缺 FEISHU_APP_SECRET 应禁用通知，实际返回了 notifier")
	}
	// 业务失败（code!=0）要转成 error 让调用方 WARN，不能静默吞掉。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok","expire":7200}`)
			return
		}
		fmt.Fprint(w, `{"code":230002,"msg":"bot not in chat"}`)
	}))
	defer srv.Close()
	n := &feishuNotifier{baseURL: srv.URL, appID: "a", appSecret: "b", chatID: "c", httpClient: srv.Client()}
	if err := n.send(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "230002") {
		t.Fatalf("code!=0 应报错并携带 code，实际 %v", err)
	}
}
