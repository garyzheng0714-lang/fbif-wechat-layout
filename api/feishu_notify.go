package main

// feishu_notify.go — 飞书审核群文本通知（施工图 S4，极简实现不引 SDK）。
// 应用「FBIF文章排版工具」（cli_aac5d107d9381bdf）向审核群发纯文本：
//   1) POST /open-apis/auth/v3/tenant_access_token/internal 换 tenant_access_token（缓存，
//      过期前刷新，简单 mutex）
//   2) POST /open-apis/im/v1/messages?receive_id_type=chat_id 发 text 消息
// env：FEISHU_APP_ID / FEISHU_APP_SECRET / FEISHU_REVIEW_CHAT_ID；任一缺失 → 通知静默禁用
//（构造返回 nil，调用侧 WARN 一次），发送失败只 WARN 不 fail job——通知是加速器不是闸门。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultFeishuBaseURL = "https://open.feishu.cn"

type feishuNotifier struct {
	baseURL    string
	appID      string
	appSecret  string
	chatID     string
	httpClient *http.Client

	mu          sync.Mutex
	token       string
	tokenExpire time.Time
}

// newFeishuNotifierFromEnv 从 env 构造；任一配置缺失返回 nil（通知禁用，不影响流水线）。
func newFeishuNotifierFromEnv() *feishuNotifier {
	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	chatID := strings.TrimSpace(os.Getenv("FEISHU_REVIEW_CHAT_ID"))
	if appID == "" || appSecret == "" || chatID == "" {
		return nil
	}
	return &feishuNotifier{
		baseURL:    defaultFeishuBaseURL,
		appID:      appID,
		appSecret:  appSecret,
		chatID:     chatID,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// send 发一条群文本。所有错误都由调用方降级为 WARN 日志（施工图钉死：通知失败不 fail job）。
func (n *feishuNotifier) send(ctx context.Context, text string) error {
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("feishu: 序列化消息内容: %w", err)
	}
	body, err := json.Marshal(map[string]string{
		"receive_id": n.chatID,
		"msg_type":   "text",
		"content":    string(content),
	})
	if err != nil {
		return fmt.Errorf("feishu: 序列化请求体: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.baseURL+"/open-apis/im/v1/messages?receive_id_type=chat_id", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("feishu: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("飞书接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("飞书发消息响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if env.Code != 0 {
		return fmt.Errorf("飞书发消息失败 code=%d msg=%s", env.Code, env.Msg)
	}
	return nil
}

// tenantAccessToken 获取并缓存 tenant_access_token；离过期不足 60s 视为过期提前刷新。
func (n *feishuNotifier) tenantAccessToken(ctx context.Context) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.token != "" && time.Now().Before(n.tokenExpire.Add(-60*time.Second)) {
		return n.token, nil
	}
	body, err := json.Marshal(map[string]string{"app_id": n.appID, "app_secret": n.appSecret})
	if err != nil {
		return "", fmt.Errorf("feishu: 序列化鉴权请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.baseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("feishu: 构造鉴权请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("飞书鉴权接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"` // 秒
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return "", fmt.Errorf("飞书鉴权响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if env.Code != 0 || env.TenantAccessToken == "" {
		return "", fmt.Errorf("飞书鉴权失败 code=%d msg=%s", env.Code, env.Msg)
	}
	if env.Expire <= 0 {
		return "", errors.New("飞书鉴权响应缺少有效 expire")
	}
	n.token = env.TenantAccessToken
	n.tokenExpire = time.Now().Add(time.Duration(env.Expire) * time.Second)
	return n.token, nil
}
