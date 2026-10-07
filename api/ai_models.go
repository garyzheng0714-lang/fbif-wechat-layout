package main

// ai_models.go — 本服务唯一的大模型入口（Xiaomi MiMo，OpenAI Responses 兼容接口）。
//
// 模型分工（2026-10-07 用户裁定）：
//   - Pro（mimo-v2.6-pro）：复杂、长程、高价值任务（发布风险检查属于这一类）。
//   - Flash（mimo-v2.6-flash）：看图（2026-10-07 实测 Pro 带图请求卡在服务端
//     PROCESSING 后报 INTERNAL_ERROR，用户改定看图用 Flash），以及翻译等高频、批量任务。
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
	"time"
)

const (
	mimoAPIKeyEnv      = "MIMO_API_KEY"
	mimoBaseURLEnv     = "MIMO_BASE_URL"
	mimoProModelEnv    = "MIMO_PRO_MODEL"
	mimoFlashModelEnv  = "MIMO_FLASH_MODEL"
	defaultMimoBaseURL = "https://api.xiaomimimo.com/v1"
	mimoResponsesPath  = "/responses"

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
var aiHTTPClient = &http.Client{Timeout: 300 * time.Second}

// aiRetryDelays 429（限流，账号级 100 RPM）与 500/503（服务端过载）按 MiMo 错误码
// 文档做指数退避；其余错误（400/401/402/403/404/421）重试无意义，直接返回。
var aiRetryDelays = []time.Duration{2 * time.Second, 6 * time.Second}

func aiAPIKey() string { return strings.TrimSpace(os.Getenv(mimoAPIKeyEnv)) }

func aiConfigured() bool { return aiAPIKey() != "" }

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
	apiKey := aiAPIKey()
	if apiKey == "" {
		return "", errAIKeyMissing
	}
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"input": []any{
			map[string]any{"role": "system", "content": []map[string]any{{"type": "input_text", "text": systemPrompt}}},
			map[string]any{"role": "user", "content": userContent},
		},
	})
	if err != nil {
		return "", fmt.Errorf("序列化 MiMo 请求失败: %w", err)
	}
	for attempt := 0; ; attempt++ {
		text, err := aiResponsesOnce(ctx, apiKey, payload)
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

func aiResponsesOnce(ctx context.Context, apiKey string, payload []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aiBaseURL()+mimoResponsesPath, bytes.NewReader(payload))
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
	return aiExtractOutputText(respBody, resp.StatusCode)
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
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		if statusCode != http.StatusOK {
			return "", &aiStatusError{Status: statusCode}
		}
		return "", fmt.Errorf("MiMo 响应不是预期 JSON（HTTP %d）: %w", statusCode, err)
	}
	if statusCode != http.StatusOK {
		msg := ""
		if out.Error != nil {
			msg = out.Error.Message
		}
		return "", &aiStatusError{Status: statusCode, Message: msg}
	}
	if out.Error != nil && out.Error.Message != "" {
		return "", fmt.Errorf("MiMo 调用失败: %s", out.Error.Message)
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
