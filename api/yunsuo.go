package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	yunsuoBaseURLEnv    = "YUNSUO_BASE_URL"
	yunsuoUserCodeEnv   = "YUNSUO_USER_CODE"
	yunsuoAPIKeyEnv     = "YUNSUO_API_KEY"
	yunsuoCategoriesEnv = "YUNSUO_CATEGORIES"

	defaultYunsuoBaseURL = "https://www.yunsuofabu.com/v1"
	yunsuoMaxResponse    = 1 << 20
)

// yunsuoConfig 只剩旧云梭 lane 补查结果用的密钥：2026-10-24 起没有平台再经云梭送稿，外发账号改从
// 多维表格「平台账号」读（distribution_accounts.go）。没配密钥只让旧 lane 查不了，不挡别的。
type yunsuoConfig struct {
	BaseURL    string   `json:"base_url"`
	UserCode   string   `json:"user_code"`
	APIKey     string   `json:"-"`
	Categories []string `json:"categories"`
}

func (c yunsuoConfig) String() string {
	return fmt.Sprintf("{BaseURL:%q UserCode:%q APIKey:[REDACTED] Categories:%d}",
		c.BaseURL, c.UserCode, len(c.Categories))
}

// loadYunsuoConfig 错误信息只说缺少的变量，绝不回显 API key。
func loadYunsuoConfig() (yunsuoConfig, error) {
	cfg := yunsuoConfig{
		BaseURL:    strings.TrimSpace(os.Getenv(yunsuoBaseURLEnv)),
		UserCode:   strings.TrimSpace(os.Getenv(yunsuoUserCodeEnv)),
		APIKey:     strings.TrimSpace(os.Getenv(yunsuoAPIKeyEnv)),
		Categories: splitYunsuoList(os.Getenv(yunsuoCategoriesEnv)),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultYunsuoBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if !configuredHTTPBaseURL(cfg.BaseURL) {
		return yunsuoConfig{}, fmt.Errorf("%s 必须是 HTTPS 地址（仅本机回环地址允许 HTTP）", yunsuoBaseURLEnv)
	}
	if cfg.UserCode == "" {
		return yunsuoConfig{}, fmt.Errorf("云梭发布未配置：缺少 %s", yunsuoUserCodeEnv)
	}
	if cfg.APIKey == "" {
		return yunsuoConfig{}, fmt.Errorf("云梭发布未配置：缺少 %s", yunsuoAPIKeyEnv)
	}
	return cfg, nil
}

// yunsuoCategories 经云梭送稿要带的分类（只剩测试里的旧云梭写法用）。
func yunsuoCategories() []string {
	return splitYunsuoList(os.Getenv(yunsuoCategoriesEnv))
}

func splitYunsuoList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '\n', '\r':
			return true
		default:
			return false
		}
	})
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

// yunsuoPushPayload 是 /distribution/push 的图文(type=0)请求。
type yunsuoPushPayload struct {
	Type        int      `json:"type"`
	Title       string   `json:"title"`
	ShortTitle  string   `json:"short_title"`
	LongTitle   string   `json:"long_title"`
	AccountIDs  []string `json:"account_ids"`
	Keywords    string   `json:"keywords"`
	Description string   `json:"description,omitempty"`
	Categories  string   `json:"categories"`
	Cover       string   `json:"cover"`
	// 官方图文字段名就是 html（剥离标签后不能为空）。不要改成 content_html：
	// 那是本系统任务字段，云梭会当成没正文并返回「图文内容不能为空」。
	HTML string `json:"html"`
}

// 云梭 /distribution/status 的 status（官方文档 https://www.yunsuofabu.com/products.html）：
// 0 待发布；1 成功；2 异常、指数退避自动重试；3 平台不支持该内容类型；4 触发验证码、需养号、自动重试。
const (
	yunsuoStatusPending = 0
	yunsuoStatusCaptcha = 4
)

type yunsuoAccountStatus struct {
	PK            string            `json:"pk,omitempty"`
	PlatformName  string            `json:"platform_name,omitempty"`
	PublishTime   string            `json:"publish_time,omitempty"`
	Status        int               `json:"status"`
	Remark        string            `json:"remark,omitempty"`
	NextRetryTime string            `json:"next_retry_time,omitempty"`
	FailedCount   int               `json:"failed_count,omitempty"`
	CreatedType   string            `json:"created_type,omitempty"`
	AccountPool   yunsuoAccountPool `json:"accountPool"`
}

// yunsuoAccountPool 云梭回执里的账号（云梭自己的账号 id）。
type yunsuoAccountPool struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type yunsuoAPI interface {
	Push(context.Context, yunsuoPushPayload) (string, error)
	Status(context.Context, string) ([]yunsuoAccountStatus, error)
}

type yunsuoClient struct {
	baseURL  string
	userCode string
	apiKey   string
	http     *http.Client
	nonce    func() (string, error)
}

func newYunsuoClientFromEnv() (yunsuoAPI, error) {
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return nil, err
	}
	return newYunsuoClient(cfg), nil
}

func newYunsuoClient(cfg yunsuoConfig) *yunsuoClient {
	return &yunsuoClient{
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		userCode: cfg.UserCode,
		apiKey:   cfg.APIKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// Signature/Nonce/User-Code 不是 Go 自动剥离的标准认证头；禁止跟随
			// redirect，避免把可重放签名材料带到另一 origin。
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		nonce: newYunsuoNonce,
	}
}

// yunsuoRejectedError 表示本地请求尚未发出，或无副作用的状态查询被明确拒绝。
// Push 一旦拿到远端响应，负业务码也不能证明文章未被受理：有 content_pk
// 就继续对账，没有则提升为 uncertain，禁止盲目重发。
type yunsuoRejectedError struct {
	Operation string
	Code      int
	Message   string
}

func (e *yunsuoRejectedError) Error() string {
	return fmt.Sprintf("云梭%s被拒绝（code=%d）：%s", e.Operation, e.Code, safeYunsuoMessage(e.Message))
}

// yunsuoUncertainError 表示网络断开或响应不完整。Push 遇到它时，
// 远端可能已经受理，禁止盲目重发造成重复文章。
type yunsuoUncertainError struct {
	Operation string
	Cause     error
}

func (e *yunsuoUncertainError) Error() string {
	if e.Cause == nil {
		return "云梭" + e.Operation + "结果不确定"
	}
	return fmt.Sprintf("云梭%s结果不确定：%v", e.Operation, e.Cause)
}

func (e *yunsuoUncertainError) Unwrap() error { return e.Cause }

func isYunsuoRejectedError(err error) bool {
	var target *yunsuoRejectedError
	return errors.As(err, &target)
}

func isYunsuoUncertainError(err error) bool {
	var target *yunsuoUncertainError
	return errors.As(err, &target)
}

func (c *yunsuoClient) Push(ctx context.Context, payload yunsuoPushPayload) (string, error) {
	raw, err := c.post(ctx, "/distribution/push", payload, "推送")
	// content_pk 是远端已经建立分发任务的最强证据，优先级高于 HTTP 状态码
	// 和业务 code。供应商若返回矛盾响应，也必须先持久化它再查询状态。
	if contentPK := parseYunsuoContentPK(raw); contentPK != "" {
		return contentPK, nil
	}
	if err != nil {
		// 云梭文档把负码同时用于请求错误和服务错误；Push 已进入外调窗口后，
		// 没有 content_pk 的负码不能证明未受理，统一进入人工对账。
		if isYunsuoRejectedError(err) {
			// 不保留 rejected 的错误类型链；否则 errors.As 会让上层把这个
			// uncertain 结果重新误判成可安全重试的 rejected。
			return "", &yunsuoUncertainError{Operation: "推送", Cause: errors.New(err.Error())}
		}
		return "", err
	}
	return "", &yunsuoUncertainError{Operation: "推送", Cause: errors.New("成功响应缺少 content_pk")}
}

func (c *yunsuoClient) Status(ctx context.Context, contentPK string) ([]yunsuoAccountStatus, error) {
	contentPK = strings.TrimSpace(contentPK)
	if contentPK == "" {
		return nil, &yunsuoRejectedError{Operation: "状态查询", Code: 0, Message: "content_pk 为空"}
	}
	raw, err := c.post(ctx, "/distribution/status", map[string]string{"content_pk": contentPK}, "状态查询")
	if err != nil {
		return nil, err
	}
	statuses, err := parseYunsuoStatuses(raw)
	if err != nil {
		return nil, &yunsuoUncertainError{Operation: "状态查询", Cause: err}
	}
	return statuses, nil
}

type yunsuoEnvelope struct {
	Code           *int            `json:"code"`
	Msg            string          `json:"msg"`
	Data           json.RawMessage `json:"data"`
	ContentPK      string          `json:"content_pk"`
	ContentPKCamel string          `json:"contentPk"`
	PK             string          `json:"pk"`
}

func (c *yunsuoClient) post(ctx context.Context, path string, payload any, operation string) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &yunsuoRejectedError{Operation: operation, Code: 0, Message: "请求参数无法序列化"}
	}
	nonce, err := c.nonce()
	if err != nil {
		return nil, &yunsuoUncertainError{Operation: operation, Cause: errors.New("无法生成请求随机数")}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, &yunsuoRejectedError{Operation: operation, Code: 0, Message: "请求地址无效"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Signature", yunsuoSignature(c.apiKey, nonce, c.userCode))
	req.Header.Set("Nonce", nonce)
	req.Header.Set("User-Code", c.userCode)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &yunsuoUncertainError{Operation: operation, Cause: errors.New("网络请求失败")}
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, yunsuoMaxResponse+1))
	if err != nil {
		return nil, &yunsuoUncertainError{Operation: operation, Cause: errors.New("响应读取失败")}
	}
	if len(responseBody) > yunsuoMaxResponse {
		return nil, &yunsuoUncertainError{Operation: operation, Cause: errors.New("响应超出大小限制")}
	}
	var envelope yunsuoEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, &yunsuoUncertainError{Operation: operation, Cause: fmt.Errorf("HTTP %d 返回了不完整响应", resp.StatusCode)}
	}
	responseData := envelope.Data
	if parseYunsuoContentPK(responseData) == "" {
		if contentPK := firstYunsuoValue(envelope.ContentPK, envelope.ContentPKCamel, envelope.PK); contentPK != "" {
			responseData, _ = json.Marshal(map[string]string{"content_pk": contentPK})
		}
	}
	// 5xx 只能说明服务端没有完整回答，不能证明不可逆的
	// Push 未经受理。即使响应体里带了负业务码，也必须进入
	// reconciliation，禁止按“明确拒绝”自动重发。
	if resp.StatusCode >= http.StatusInternalServerError {
		return responseData, &yunsuoUncertainError{Operation: operation, Cause: fmt.Errorf("HTTP %d 服务端错误", resp.StatusCode)}
	}
	if envelope.Code == nil || *envelope.Code == 0 {
		return responseData, &yunsuoUncertainError{Operation: operation, Cause: fmt.Errorf("HTTP %d 响应缺少已知业务 code", resp.StatusCode)}
	}
	code := *envelope.Code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// post 层保留 4xx 负码，供无副作用的状态查询显示明确原因；
		// Push 会先提取 content_pk，并把剩余拒绝提升为 uncertain。
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && code < 0 {
			message := envelope.Msg
			if c.apiKey != "" {
				message = strings.ReplaceAll(message, c.apiKey, "[REDACTED]")
			}
			return responseData, &yunsuoRejectedError{Operation: operation, Code: code, Message: message}
		}
		return responseData, &yunsuoUncertainError{Operation: operation, Cause: fmt.Errorf("HTTP %d 与业务 code=%d 无法确认请求结果", resp.StatusCode, code)}
	}
	if code < 0 {
		message := envelope.Msg
		if c.apiKey != "" {
			message = strings.ReplaceAll(message, c.apiKey, "[REDACTED]")
		}
		return responseData, &yunsuoRejectedError{Operation: operation, Code: code, Message: message}
	}
	if len(bytes.TrimSpace(responseData)) == 0 || bytes.Equal(bytes.TrimSpace(responseData), []byte("null")) {
		return nil, &yunsuoUncertainError{Operation: operation, Cause: errors.New("成功响应缺少 data")}
	}
	return responseData, nil
}

func yunsuoSignature(apiKey, nonce, userCode string) string {
	sum := sha512.Sum512([]byte(apiKey + nonce + userCode))
	return hex.EncodeToString(sum[:])
}

func newYunsuoNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func parseYunsuoContentPK(raw json.RawMessage) string {
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		return strings.TrimSpace(direct)
	}
	var object struct {
		ContentPK      string `json:"content_pk"`
		ContentPKCamel string `json:"contentPk"`
		PK             string `json:"pk"`
	}
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	for _, value := range []string{object.ContentPK, object.ContentPKCamel, object.PK} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

type yunsuoStatusWire struct {
	PK            string `json:"pk"`
	PlatformName  string `json:"platform_name"`
	PublishTime   string `json:"publish_time"`
	Status        int    `json:"status"`
	Remark        string `json:"remark"`
	NextRetryTime string `json:"next_retry_time"`
	FailedCount   int    `json:"failed_count"`
	CreatedType   string `json:"created_type"`
	AccountPool   struct {
		ID           string `json:"id"`
		PK           string `json:"pk"`
		AccountID    string `json:"account_id"`
		Name         string `json:"name"`
		AccountName  string `json:"account_name"`
		PlatformName string `json:"platform_name"`
	} `json:"accountPool"`
	AccountPoolSnake struct {
		ID           string `json:"id"`
		PK           string `json:"pk"`
		AccountID    string `json:"account_id"`
		Name         string `json:"name"`
		AccountName  string `json:"account_name"`
		PlatformName string `json:"platform_name"`
	} `json:"account_pool"`
}

func parseYunsuoStatuses(raw json.RawMessage) ([]yunsuoAccountStatus, error) {
	var rows []yunsuoStatusWire
	if err := json.Unmarshal(raw, &rows); err != nil {
		var object struct {
			List    []yunsuoStatusWire `json:"list"`
			Items   []yunsuoStatusWire `json:"items"`
			Results []yunsuoStatusWire `json:"results"`
		}
		if objectErr := json.Unmarshal(raw, &object); objectErr != nil {
			return nil, errors.New("状态 data 格式不完整")
		}
		switch {
		case object.List != nil:
			rows = object.List
		case object.Items != nil:
			rows = object.Items
		case object.Results != nil:
			rows = object.Results
		default:
			return nil, errors.New("状态 data 缺少账号列表")
		}
	}
	if rows == nil {
		return nil, errors.New("状态 data 为 null")
	}
	out := make([]yunsuoAccountStatus, 0, len(rows))
	for _, row := range rows {
		accountID := firstYunsuoValue(row.AccountPool.ID, row.AccountPool.PK, row.AccountPool.AccountID)
		accountName := firstYunsuoValue(row.AccountPool.Name, row.AccountPool.AccountName, row.AccountPool.PlatformName)
		if accountID == "" {
			accountID = firstYunsuoValue(row.AccountPoolSnake.ID, row.AccountPoolSnake.PK, row.AccountPoolSnake.AccountID)
		}
		if accountName == "" {
			accountName = firstYunsuoValue(row.AccountPoolSnake.Name, row.AccountPoolSnake.AccountName, row.AccountPoolSnake.PlatformName)
		}
		out = append(out, yunsuoAccountStatus{
			PK:            strings.TrimSpace(row.PK),
			PlatformName:  strings.TrimSpace(row.PlatformName),
			PublishTime:   strings.TrimSpace(row.PublishTime),
			Status:        row.Status,
			Remark:        strings.TrimSpace(row.Remark),
			NextRetryTime: strings.TrimSpace(row.NextRetryTime),
			FailedCount:   row.FailedCount,
			CreatedType:   strings.TrimSpace(row.CreatedType),
			AccountPool:   yunsuoAccountPool{ID: accountID, Name: accountName},
		})
	}
	return out, nil
}

func firstYunsuoValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func safeYunsuoMessage(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return "服务端未返回原因"
	}
	runes := []rune(message)
	if len(runes) > 240 {
		message = string(runes[:240]) + "…"
	}
	return message
}
