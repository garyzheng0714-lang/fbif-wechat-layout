package main

// feishu_notify.go — 飞书通知（施工图 S4，极简实现不引 SDK）。
// 应用「FBIF文章排版工具」（cli_aac5d107d9381bdf）按用途选择收件人：
//   - 审核卡与发布结果：FEISHU_REVIEW_CHAT_ID 审核群；
//   - P0 异常、恢复与心跳：PUBLISH_MONITOR_FEISHU_USER_ID 私聊。
//   1) POST /open-apis/auth/v3/tenant_access_token/internal 换 tenant_access_token（缓存，
//      过期前刷新，简单 mutex）
//   2) POST /open-apis/im/v1/messages，审核走 chat_id，监控走 user_id。
// 任一路径缺少自己的收件人配置就单独禁用，监控不得回退到审核群。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultFeishuBaseURL = "https://open.feishu.cn"

const publishMonitorFeishuUserIDEnv = "PUBLISH_MONITOR_FEISHU_USER_ID"

const (
	feishuReceiveIDTypeChat = "chat_id"
	feishuReceiveIDTypeUser = "user_id"
)

type feishuNotifier struct {
	baseURL     string
	appID       string
	appSecret   string
	receiveID   string
	receiveType string
	httpClient  *http.Client

	mu          sync.Mutex
	token       string
	tokenExpire time.Time
}

// newFeishuMonitorNotifierFromEnv 只构造 P0 监控私聊发送器。缺 user_id 时返回 nil；
// 调用方必须显式失败，不能把监控消息回退到审核群。
func newFeishuMonitorNotifierFromEnv() *feishuNotifier {
	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	userID := strings.TrimSpace(os.Getenv(publishMonitorFeishuUserIDEnv))
	if appID == "" || appSecret == "" || userID == "" {
		return nil
	}
	return &feishuNotifier{
		baseURL:     defaultFeishuBaseURL,
		appID:       appID,
		appSecret:   appSecret,
		receiveID:   userID,
		receiveType: feishuReceiveIDTypeUser,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
	}
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
		baseURL:     defaultFeishuBaseURL,
		appID:       appID,
		appSecret:   appSecret,
		receiveID:   chatID,
		receiveType: feishuReceiveIDTypeChat,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
	}
}

// send 向构造时选定的群或私聊收件人发一条文本。
func (n *feishuNotifier) send(ctx context.Context, text string) error {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("feishu: 序列化消息内容: %w", err)
	}
	return n.sendMessage(ctx, "text", string(content))
}

// sendCard 发一张 interactive 卡片（M4：审核卡）。content 是卡片 JSON 对象本体；
// 构建/发送失败由调用方降级回文本通知，卡片永远不是发布流水线的闸门。
func (n *feishuNotifier) sendCard(ctx context.Context, card map[string]any) error {
	content, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("feishu: 序列化卡片: %w", err)
	}
	return n.sendMessage(ctx, "interactive", string(content))
}

// sendMessage 发送内核：text 与 interactive 共用同一条 im/v1/messages 通道。
func (n *feishuNotifier) sendMessage(ctx context.Context, msgType, content string) error {
	receiveIDType := strings.TrimSpace(n.receiveType)
	receiveID := strings.TrimSpace(n.receiveID)
	if receiveID == "" {
		return errors.New("feishu: 缺少消息收件人")
	}
	if receiveIDType != feishuReceiveIDTypeChat && receiveIDType != feishuReceiveIDTypeUser {
		return errors.New("feishu: 不支持的消息收件人类型")
	}
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   msgType,
		"content":    content,
	})
	if err != nil {
		return fmt.Errorf("feishu: 序列化请求体: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.baseURL+"/open-apis/im/v1/messages?receive_id_type="+receiveIDType, bytes.NewReader(body))
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
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return fmt.Errorf("飞书发消息响应读取失败或超过 1 MB: %w", err)
	}
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
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return "", fmt.Errorf("飞书鉴权响应读取失败或超过 1 MB: %w", err)
	}
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

// uploadImageByURL 把一张图（OSS/外链）转成完整可见的方形封面，再上传为飞书图片
// 素材并返回 img_key（卡片 img 元素只认 img_key，不认外链 URL）。下载与上传都设
// 上限与超时；任何失败由调用方降级为「卡片不带图」，绝不阻塞流水线。
func (n *feishuNotifier) uploadImageByURL(ctx context.Context, imgURL string) (string, error) {
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return "", err
	}
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return "", fmt.Errorf("feishu: 构造封面下载请求: %w", err)
	}
	dlResp, err := n.httpClient.Do(dlReq)
	if err != nil {
		return "", fmt.Errorf("封面图下载失败: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("封面图下载失败（HTTP %d）", dlResp.StatusCode)
	}
	imgBytes, err := readBoundedBody(dlResp.Body, 10<<20)
	if err != nil {
		return "", fmt.Errorf("读封面图失败或超过 10 MB: %w", err)
	}
	imgBytes, err = squareFeishuCoverPNG(imgBytes)
	if err != nil {
		return "", fmt.Errorf("生成方形封面失败: %w", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("image_type", "message"); err != nil {
		return "", fmt.Errorf("feishu: 构造上传表单: %w", err)
	}
	fw, err := mw.CreateFormFile("image", "cover.png")
	if err != nil {
		return "", fmt.Errorf("feishu: 构造上传表单: %w", err)
	}
	if _, err := fw.Write(imgBytes); err != nil {
		return "", fmt.Errorf("feishu: 写上传表单: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("feishu: 收口上传表单: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.baseURL+"/open-apis/im/v1/images", &buf)
	if err != nil {
		return "", fmt.Errorf("feishu: 构造图片上传请求: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("飞书图片接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return "", fmt.Errorf("飞书图片上传响应读取失败或超过 1 MB: %w", err)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ImageKey string `json:"image_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return "", fmt.Errorf("飞书图片上传响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if env.Code != 0 || env.Data.ImageKey == "" {
		return "", fmt.Errorf("飞书图片上传失败 code=%d msg=%s", env.Code, env.Msg)
	}
	return env.Data.ImageKey, nil
}
