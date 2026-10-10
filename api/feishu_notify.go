package main

// feishu_notify.go — 飞书通知（施工图 S4，极简实现不引 SDK）。
// 审核卡与发布结果使用后台选定的应用 profile 和多个审核群；凭证来自
// FEISHU_NOTIFICATION_APPS_JSON（未配置时兼容 FEISHU_APP_ID/SECRET +
// FEISHU_REVIEW_CHAT_ID）。P0 异常、恢复与心跳仍使用默认应用私聊
// PUBLISH_MONITOR_FEISHU_USER_ID，避免监控跟随文章审核群配置串线。
//   1) POST /open-apis/auth/v3/tenant_access_token/internal 换 tenant_access_token（缓存，
//      过期前刷新，简单 mutex）
//   2) POST /open-apis/im/v1/messages，审核走 chat_id，监控走 user_id；审核卡
//      读取返回的 message_id，并用 PATCH 原位更新后续状态。
// 任一路径缺少自己的收件人配置就单独禁用，监控不得回退到审核群。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
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
	return newFeishuNotifierForProfile(feishuNotificationApp{
		ID: "default", Name: "FBIF文章排版工具", AppID: appID, AppSecret: appSecret,
	}, chatID)
}

func newFeishuNotifierForProfile(profile feishuNotificationApp, chatID string) *feishuNotifier {
	if strings.TrimSpace(profile.AppID) == "" || strings.TrimSpace(profile.AppSecret) == "" || strings.TrimSpace(chatID) == "" {
		return nil
	}
	return &feishuNotifier{
		baseURL:     defaultFeishuBaseURL,
		appID:       strings.TrimSpace(profile.AppID),
		appSecret:   strings.TrimSpace(profile.AppSecret),
		receiveID:   strings.TrimSpace(chatID),
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
	_, err := n.sendCardWithID(ctx, card)
	return err
}

// sendCardWithID 发送 interactive 卡片并返回 message_id。只有拿到这个 ID，
// 发布终态才能更新当前卡片，而不是在群里再发一张新卡。
func (n *feishuNotifier) sendCardWithID(ctx context.Context, card map[string]any) (string, error) {
	content, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("feishu: 序列化卡片: %w", err)
	}
	messageID, err := n.sendMessageWithID(ctx, "interactive", string(content))
	if err != nil {
		return "", err
	}
	if !feishuMessageIDSafe(messageID) {
		return "", errors.New("feishu: 发卡响应缺少有效 message_id")
	}
	return messageID, nil
}

// sendMessage 发送内核：text 与 interactive 共用同一条 im/v1/messages 通道。
func (n *feishuNotifier) sendMessage(ctx context.Context, msgType, content string) error {
	_, err := n.sendMessageWithID(ctx, msgType, content)
	return err
}

// sendMessageWithID 发送消息并读取响应里的 data.message_id。文本通知不需要
// 使用该 ID，但审核卡必须保存它以便后续 PATCH 原卡。
func (n *feishuNotifier) sendMessageWithID(ctx context.Context, msgType, content string) (string, error) {
	receiveIDType := strings.TrimSpace(n.receiveType)
	receiveID := strings.TrimSpace(n.receiveID)
	if receiveID == "" {
		return "", errors.New("feishu: 缺少消息收件人")
	}
	if receiveIDType != feishuReceiveIDTypeChat && receiveIDType != feishuReceiveIDTypeUser {
		return "", errors.New("feishu: 不支持的消息收件人类型")
	}
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   msgType,
		"content":    content,
	})
	if err != nil {
		return "", fmt.Errorf("feishu: 序列化请求体: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.baseURL+"/open-apis/im/v1/messages?receive_id_type="+receiveIDType, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("feishu: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("飞书接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return "", fmt.Errorf("飞书发消息响应读取失败或超过 1 MB: %w", err)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return "", fmt.Errorf("飞书发消息响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if env.Code != 0 {
		return "", fmt.Errorf("飞书发消息失败 code=%d msg=%s", env.Code, env.Msg)
	}
	return strings.TrimSpace(env.Data.MessageID), nil
}

// updateCard 原位更新当前应用发送的交互卡。飞书卡片使用 PATCH，而不是普通
// 文本消息的 PUT；content 仍是卡片 JSON 序列化后的字符串。
func (n *feishuNotifier) updateCard(ctx context.Context, messageID string, card map[string]any) error {
	messageID = strings.TrimSpace(messageID)
	if !feishuMessageIDSafe(messageID) {
		return errors.New("feishu: message_id 无效")
	}
	content, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("feishu: 序列化更新卡片: %w", err)
	}
	body, err := json.Marshal(map[string]string{"content": string(content)})
	if err != nil {
		return fmt.Errorf("feishu: 序列化更新卡片请求体: %w", err)
	}
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		n.baseURL+"/open-apis/im/v1/messages/"+url.PathEscape(messageID), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("feishu: 构造更新卡片请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("飞书更新卡片接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return fmt.Errorf("飞书更新卡片响应读取失败或超过 1 MB: %w", err)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("飞书更新卡片响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if env.Code != 0 {
		return fmt.Errorf("飞书更新卡片失败 code=%d msg=%s", env.Code, env.Msg)
	}
	return nil
}

// recallMessage 撤回机器人自己发出的消息（DELETE /im/v1/messages/{id}）。
// 用于审核卡「撤回重发」：超出撤回窗口等失败由调用方决定是否降级继续。
func (n *feishuNotifier) recallMessage(ctx context.Context, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	if !feishuMessageIDSafe(messageID) {
		return errors.New("feishu: message_id 无效")
	}
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		n.baseURL+"/open-apis/im/v1/messages/"+url.PathEscape(messageID), nil)
	if err != nil {
		return fmt.Errorf("feishu: 构造撤回请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("飞书撤回接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return fmt.Errorf("飞书撤回响应读取失败: %w", err)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("飞书撤回响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if env.Code != 0 {
		return fmt.Errorf("飞书撤回失败 code=%d msg=%s", env.Code, env.Msg)
	}
	return nil
}

func feishuMessageIDSafe(messageID string) bool {
	if messageID == "" || len(messageID) > 256 {
		return false
	}
	for _, r := range messageID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
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
	imgBytes, err := n.downloadCoverImage(ctx, imgURL)
	if err != nil {
		return "", err
	}
	imgBytes, err = squareFeishuCoverPNG(imgBytes)
	if err != nil {
		return "", fmt.Errorf("生成方形封面失败: %w", err)
	}
	return n.uploadImageBytes(ctx, "cover.png", imgBytes)
}

// uploadThumbByURL 下载封面并上传审核聚合卡用的 16:9 小图（thumbFeishuCoverJPEG）。
func (n *feishuNotifier) uploadThumbByURL(ctx context.Context, imgURL string) (string, error) {
	imgBytes, err := n.downloadCoverImage(ctx, imgURL)
	if err != nil {
		return "", err
	}
	imgBytes, err = thumbFeishuCoverJPEG(imgBytes)
	if err != nil {
		return "", err
	}
	return n.uploadImageBytes(ctx, "thumb.jpg", imgBytes)
}

func (n *feishuNotifier) downloadCoverImage(ctx context.Context, imgURL string) ([]byte, error) {
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("feishu: 构造封面下载请求: %w", err)
	}
	dlResp, err := n.httpClient.Do(dlReq)
	if err != nil {
		return nil, fmt.Errorf("封面图下载失败: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("封面图下载失败（HTTP %d）", dlResp.StatusCode)
	}
	imgBytes, err := readBoundedBody(dlResp.Body, 10<<20)
	if err != nil {
		return nil, fmt.Errorf("读封面图失败或超过 10 MB: %w", err)
	}
	return imgBytes, nil
}

// uploadImageBytes 把一张图片上传到飞书消息图片素材，返回卡片 img/custom_icon 可用的
// img_key。img_key 与上传它的应用绑定，换应用发卡必须由该应用重新上传。
func (n *feishuNotifier) uploadImageBytes(ctx context.Context, filename string, imgBytes []byte) (string, error) {
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("image_type", "message"); err != nil {
		return "", fmt.Errorf("feishu: 构造上传表单: %w", err)
	}
	fw, err := mw.CreateFormFile("image", filename)
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
