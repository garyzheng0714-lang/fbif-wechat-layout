package main

// ai_models.go — 本服务唯一的大模型入口（Xiaomi MiMo，OpenAI Responses 兼容接口）。
//
// 模型分工（2026-10-07 用户裁定）：
//   - Pro（mimo-v2.6-pro）：复杂、长程、高价值任务（发布风险检查属于这一类）。
//   - Flash（mimo-v2.6-flash）：看图（2026-10-07 实测 Pro 带图请求卡在服务端
//     PROCESSING 后报 INTERNAL_ERROR，用户改定看图用 Flash），以及翻译等高频、批量任务。
//
// 缩写标题、摘要、图注这类改一小段字的，走 aiRewriteCall（关掉思考），别的走 aiResponsesCall。
//
// 新增 AI 调用必须先在这里选档（aiTierPro / aiTierVision / aiTierFlash），禁止在业务
// 文件里写模型名字面量或另接一家供应商。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	mimoAPIKeyEnv      = "MIMO_API_KEY"
	mimoBaseURLEnv     = "MIMO_BASE_URL"
	mimoProModelEnv    = "MIMO_PRO_MODEL"
	mimoFlashModelEnv  = "MIMO_FLASH_MODEL"
	defaultMimoBaseURL = "https://api.xiaomimimo.com/v1"
	mimoResponsesPath  = "/responses"
	mimoChatPath       = "/chat/completions"

	defaultMimoProModel   = "mimo-v2.6-pro"
	defaultMimoFlashModel = "mimo-v2.6-flash"
)

type aiTier string

const (
	aiTierPro   aiTier = "pro"
	aiTierFlash aiTier = "flash"
	// aiTierVision 看图任务的档位，当前指向 Flash；Pro 看图恢复且用户改定时只改这里。
	aiTierVision = aiTierFlash
)

var errAIKeyMissing = errors.New(mimoAPIKeyEnv + " 未配置")

// aiHTTPClient 多模态推理慢（要看图、要思考长文）：2026-07-10 评测实测 120s 会让
// 40% 的长文超时，放宽到 300s——调用方都在异步阶段，慢无妨；测试桩即回不受影响。
// 不跟跳转：密钥只发往它绑定的地址（hub_secrets.go）。
var aiHTTPClient = &http.Client{Timeout: 300 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

// aiRetryDelays 429（限流，账号级 100 RPM）与 500/503（服务端过载）按 MiMo 错误码
// 文档做指数退避；其余错误（400/401/402/403/404/421）重试无意义，直接返回。
var aiRetryDelays = []time.Duration{2 * time.Second, 6 * time.Second}

func aiAPIKey() string { return strings.TrimSpace(os.Getenv(mimoAPIKeyEnv)) }

// aiCredential 这次调用用的密钥和接口根地址：接了中台只用中台发的（hub_secrets.go），没接读 env。
func aiCredential(ctx context.Context) (key, baseURL string, err error) {
	hub, on, err := hubSecrets()
	if on {
		if err != nil {
			return "", "", err
		}
		cred := hub.Credential(ctx)
		if cred == nil {
			return "", "", errHubMimoMissing
		}
		return cred.key, cred.origin + "/v1", nil
	}
	if key = aiAPIKey(); key == "" {
		return "", "", errAIKeyMissing
	}
	return key, aiBaseURL(), nil
}

func aiConfigured() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _, err := aiCredential(ctx)
	return err == nil
}

func aiModel(tier aiTier) string {
	envKey, fallback := mimoProModelEnv, defaultMimoProModel
	if tier == aiTierFlash {
		envKey, fallback = mimoFlashModelEnv, defaultMimoFlashModel
	}
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v
	}
	return fallback
}

func aiBaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv(mimoBaseURLEnv)), "/"); v != "" {
		return v
	}
	return defaultMimoBaseURL
}

type aiStatusError struct {
	Status  int
	Message string
}

func (e *aiStatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("MiMo 调用失败（HTTP %d）", e.Status)
	}
	return fmt.Sprintf("MiMo 调用失败（HTTP %d）: %s", e.Status, e.Message)
}

func aiRetryable(err error) bool {
	var se *aiStatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Status == http.StatusTooManyRequests || se.Status == http.StatusInternalServerError ||
		se.Status == http.StatusServiceUnavailable
}

// aiResponsesCall Responses API 调用内核：payload 组装、HTTP、输出文本提取。
// userContent 允许含 input_image（多模态）。
func aiResponsesCall(ctx context.Context, model, systemPrompt string, userContent []map[string]any) (string, error) {
	return aiCall(ctx, mimoResponsesPath, map[string]any{
		"model": model,
		"input": []any{
			map[string]any{"role": "system", "content": []map[string]any{{"type": "input_text", "text": systemPrompt}}},
			map[string]any{"role": "user", "content": userContent},
		},
	}, aiExtractOutputText)
}

// aiRewriteCall 改写一小段字（缩标题、摘要、图注），不让模型先思考。
// MiMo V2.6 默认先思考再答，Responses 接口不认关思考的参数：2026-11-03 实测带上 thinking.type=disabled
// 照样想 400–1800 个 token，缩一条图注 5–80 秒，#340 两条图注把 2 分钟耗光、都没缩成。
// Chat Completions 认 thinking.type=disabled，同一条 1.6–7 秒、不思考。字数由调用方按平台数法核对，超了再缩。
func aiRewriteCall(ctx context.Context, model, systemPrompt, text string) (string, error) {
	return aiCall(ctx, mimoChatPath, map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": text}},
		"thinking": map[string]string{"type": "disabled"},
	}, aiExtractChatText)
}

// aiCall 发一次 MiMo 请求（429、500、503 按 aiRetryDelays 退避重试），extract 从响应里取出模型的回答。
func aiCall(ctx context.Context, path string, body map[string]any, extract func([]byte, int) (string, error)) (string, error) {
	apiKey, baseURL, err := aiCredential(ctx)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("序列化 MiMo 请求失败: %w", err)
	}
	for attempt := 0; ; attempt++ {
		text, err := aiPostOnce(ctx, apiKey, baseURL+path, payload, extract)
		if err == nil || !aiRetryable(err) || attempt >= len(aiRetryDelays) {
			return text, err
		}
		select {
		case <-ctx.Done():
			return "", err
		case <-time.After(aiRetryDelays[attempt]):
		}
	}
}

func aiPostOnce(ctx context.Context, apiKey, url string, payload []byte, extract func([]byte, int) (string, error)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("构造 MiMo 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := aiHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("MiMo 接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 4<<20)
	if err != nil {
		return "", fmt.Errorf("读 MiMo 响应失败: %w", err)
	}
	if rec, ok := ctx.Value(aiUsageKey{}).(*aiUsage); ok && resp.StatusCode == http.StatusOK {
		rec.add(respBody)
	}
	return extract(respBody, resp.StatusCode)
}

// aiTokenUsage 几次 MiMo 调用一共用掉的 token（按响应里的 usage 记，算钱用）。
type aiTokenUsage struct {
	Calls             int `json:"calls"`
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`
	OutputTokens      int `json:"output_tokens"`
	ReasoningTokens   int `json:"reasoning_tokens,omitempty"` // 已算在 OutputTokens 里
}

// aiUsage 要记 token 的那件事用 withAIUsage 包一层 ctx，期间每次调用的 usage 都加进来；不包的调用照旧、不多解析一遍。
// Responses 接口回 input_tokens/output_tokens，Chat Completions 回 prompt_tokens/completion_tokens，两种都认。
type aiUsage struct {
	mu    sync.Mutex
	total aiTokenUsage
}

type aiUsageKey struct{}

func withAIUsage(ctx context.Context, rec *aiUsage) context.Context {
	return context.WithValue(ctx, aiUsageKey{}, rec)
}

func (u *aiUsage) add(respBody []byte) {
	var out struct {
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			PromptTokens       int `json:"prompt_tokens"`
			CompletionTokens   int `json:"completion_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			OutputTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(respBody, &out) != nil || out.Usage == nil {
		return
	}
	v := out.Usage
	u.mu.Lock()
	defer u.mu.Unlock()
	u.total.Calls++
	u.total.InputTokens += v.InputTokens + v.PromptTokens
	u.total.OutputTokens += v.OutputTokens + v.CompletionTokens
	u.total.CachedInputTokens += v.InputTokensDetails.CachedTokens + v.PromptTokensDetails.CachedTokens
	u.total.ReasoningTokens += v.OutputTokensDetails.ReasoningTokens + v.CompletionTokensDetails.ReasoningTokens
}

func (u *aiUsage) snapshot() aiTokenUsage {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.total
}

// aiDecode 读 MiMo 响应：HTTP 出错、带 error 的报错，否则解进 out。
func aiDecode(respBody []byte, statusCode int, out any) error {
	var e struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &e); err != nil {
		if statusCode != http.StatusOK {
			return &aiStatusError{Status: statusCode}
		}
		return fmt.Errorf("MiMo 响应不是预期 JSON（HTTP %d）: %w", statusCode, err)
	}
	msg := ""
	if e.Error != nil {
		msg = e.Error.Message
	}
	if statusCode != http.StatusOK {
		return &aiStatusError{Status: statusCode, Message: msg}
	}
	if msg != "" {
		return fmt.Errorf("MiMo 调用失败: %s", msg)
	}
	return json.Unmarshal(respBody, out)
}

// aiExtractChatText Chat Completions 响应 → 模型输出文本（第一个回答的 content）。
func aiExtractChatText(respBody []byte, statusCode int) (string, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := aiDecode(respBody, statusCode, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("MiMo 响应没有模型输出文本")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// aiExtractOutputText Responses API 响应 → 模型输出文本：遍历 output[].content[] 拼接
// output_text（跳过 reasoning 等非 message 项），HTTP/业务错误显式报错。MiMo 思考期间
// 会先吐换行保活，JSON 解析前不需要特殊处理（encoding/json 忽略前导空白）。
func aiExtractOutputText(respBody []byte, statusCode int) (string, error) {
	var out struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := aiDecode(respBody, statusCode, &out); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, item := range out.Output {
		if item.Type != "" && item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Type == "output_text" {
				sb.WriteString(c.Text)
			}
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", errors.New("MiMo 响应没有模型输出文本")
	}
	return text, nil
}
