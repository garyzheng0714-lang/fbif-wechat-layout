package main

// distribution_robot.go — 自己的分发服务（FBIF-多平台分发工具）的客户端。
//
// 分发服务在 B 机上用托管的浏览器登录态往平台编辑器里写稿，A 机经 SSH 通道在本机
// 127.0.0.1:18095 访问它（契约见那个仓库 README「接口」）。同一个平台同一个幂等键只建一篇
// 草稿：结果不明时用同一个键查（GET）或再提交（POST）都安全，分发服务自己去草稿箱对账。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	distributionAPIURLEnv   = "DISTRIBUTION_API_URL"
	distributionAPITokenEnv = "DISTRIBUTION_API_TOKEN"

	// 写一篇草稿约 30 秒，排在别的操作后面最长 5 分钟。
	distributionRobotTimeout     = 6 * time.Minute
	distributionRobotMaxResponse = 1 << 20
)

type distributionRobotConfig struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"-"`
}

func (c distributionRobotConfig) String() string {
	return fmt.Sprintf("{BaseURL:%q Token:[REDACTED]}", c.BaseURL)
}

// loadDistributionRobotConfig 外发账号与存草稿都经它；错误信息只说缺哪个变量，绝不回显 token。
func loadDistributionRobotConfig() (distributionRobotConfig, error) {
	cfg := distributionRobotConfig{
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv(distributionAPIURLEnv)), "/"),
		Token:   strings.TrimSpace(os.Getenv(distributionAPITokenEnv)),
	}
	if cfg.BaseURL == "" {
		return distributionRobotConfig{}, fmt.Errorf("分发工具未配置：缺少 %s", distributionAPIURLEnv)
	}
	if !configuredHTTPBaseURL(cfg.BaseURL) {
		return distributionRobotConfig{}, fmt.Errorf("%s 必须是 HTTPS 地址（仅本机回环地址允许 HTTP）", distributionAPIURLEnv)
	}
	if cfg.Token == "" {
		return distributionRobotConfig{}, fmt.Errorf("分发工具未配置：缺少 %s", distributionAPITokenEnv)
	}
	return cfg, nil
}

// robotDraftRequest 写草稿的请求体（POST /v1/platforms/{platform}/drafts）。
type robotDraftRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Account        string `json:"account"` // 多维表格「平台账号」的 record_id
	Title          string `json:"title"`
	HTML           string `json:"html"`
	CoverURL       string `json:"cover_url"`
}

// robotDraftRecord 分发服务对一个幂等键的记录（写草稿的 200 响应与 GET 同一份）。
type robotDraftRecord struct {
	// pending / saving / saved / failed / unknown
	Status        string              `json:"status"`
	ArticleID     string              `json:"article_id"`
	DraftURL      string              `json:"draft_url"`
	Expected      robotDraftExpected  `json:"expected"`
	ReadBack      *robotDraftReadBack `json:"read_back"`
	Match         *bool               `json:"match"`
	Mismatches    []string            `json:"mismatches"`
	ReadBackError *string             `json:"read_back_error"`
	Error         *string             `json:"error"`
	Assists       []robotDraftAssist  `json:"assists"`
	Replayed      bool                `json:"replayed"`
	Attempts      int                 `json:"attempts"`
}

// robotDraftAssist 写死的找法没找到按钮、改点了模型挑的元素的一步（平台改版的信号）。
// Wanted 是原来找的字，Picked 是模型挑的元素上的字；都来自平台页面，展示前要清洗。
type robotDraftAssist struct {
	Step   string   `json:"step"`
	Wanted []string `json:"wanted"`
	Picked string   `json:"picked"`
}

const (
	robotDraftPending = "pending"
	robotDraftSaving  = "saving"
	robotDraftSaved   = "saved"
	robotDraftFailed  = "failed"
	robotDraftUnknown = "unknown"
)

type robotDraftExpected struct {
	Headings int      `json:"headings"`
	Bold     int      `json:"bold"`
	Images   int      `json:"images"`
	Captions []string `json:"captions"`
}

type robotDraftReadBack struct {
	Title         string `json:"title"`
	Headings      int    `json:"headings"`
	Bold          int    `json:"bold"`
	Images        int    `json:"images"`
	Captions      int    `json:"captions"`
	CoverUploaded *bool  `json:"cover_uploaded"`
}

type distributionRobotAPI interface {
	SaveDraft(ctx context.Context, platform string, req robotDraftRequest) (robotDraftRecord, error)
	Draft(ctx context.Context, platform, key string) (robotDraftRecord, error)
}

// robotRejectedError 分发服务在点「存草稿」之前就拒了（参数、token、封面、没登录），没建稿；
// 改好后同一个键或新键再提交都安全。
type robotRejectedError struct {
	Status  int
	Code    string
	Message string
}

func (e *robotRejectedError) Error() string {
	return fmt.Sprintf("分发工具拒绝存草稿（HTTP %d %s）：%s", e.Status, e.Code, e.Message)
}

// robotReconcileError 点了存草稿没拿到编号，平台草稿箱里这次新建的同名草稿不止一篇，要人去删。
type robotReconcileError struct{ Message string }

func (e *robotReconcileError) Error() string { return e.Message }

// robotUncertainError 平台出错、超时、断网或响应不完整：不知道存没存成，按同一个键查。
type robotUncertainError struct{ Cause string }

func (e *robotUncertainError) Error() string {
	return "分发工具没有给出确定结果：" + e.Cause
}

// errRobotDraftNotFound GET 时分发服务没有这个键的记录：请求从没到过它，没建稿。
var errRobotDraftNotFound = errors.New("分发工具没收到过这次存草稿请求")

type distributionRobotClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newDistributionRobotClientFromEnv() (distributionRobotAPI, error) {
	cfg, err := loadDistributionRobotConfig()
	if err != nil {
		return nil, err
	}
	return newDistributionRobotClient(cfg), nil
}

func newDistributionRobotClient(cfg distributionRobotConfig) *distributionRobotClient {
	return &distributionRobotClient{
		baseURL: cfg.BaseURL,
		token:   cfg.Token,
		http: &http.Client{
			Timeout: distributionRobotTimeout,
			// 不跟随跳转，免得把 Bearer token 带去别的地方。
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *distributionRobotClient) SaveDraft(ctx context.Context, platform string, req robotDraftRequest) (robotDraftRecord, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return robotDraftRecord{}, &robotRejectedError{Message: "请求参数无法序列化"}
	}
	status, raw, err := c.do(ctx, http.MethodPost, "/v1/platforms/"+url.PathEscape(platform)+"/drafts", body)
	if err != nil {
		return robotDraftRecord{}, &robotUncertainError{Cause: err.Error()}
	}
	if status == http.StatusOK {
		record, err := parseRobotDraftRecord(raw)
		if err != nil {
			return robotDraftRecord{}, &robotUncertainError{Cause: err.Error()}
		}
		if record.Status != robotDraftSaved || strings.TrimSpace(record.ArticleID) == "" {
			return record, &robotUncertainError{Cause: "成功响应里没有存好的草稿编号"}
		}
		return record, nil
	}
	code, message := c.parseError(status, raw)
	switch {
	case status == http.StatusConflict && code == "needs_reconcile":
		return robotDraftRecord{}, &robotReconcileError{Message: message}
	case status >= 400 && status < 500:
		// 400 参数、401 token、404 平台、409 键已用于别的内容、413 太大、422 封面：都在点存草稿之前。
		return robotDraftRecord{}, &robotRejectedError{Status: status, Code: code, Message: message}
	case status == http.StatusServiceUnavailable && code == "login_required":
		return robotDraftRecord{}, &robotRejectedError{Status: status, Code: code, Message: message}
	default:
		return robotDraftRecord{}, &robotUncertainError{Cause: fmt.Sprintf("HTTP %d %s：%s", status, code, message)}
	}
}

// Accounts 多维表格「平台账号」里的号（分发服务 GET /v1/accounts）。
func (c *distributionRobotClient) Accounts(ctx context.Context) ([]externalAccount, error) {
	status, raw, err := c.do(ctx, http.MethodGet, "/v1/accounts", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		_, message := c.parseError(status, raw)
		return nil, fmt.Errorf("分发服务读账号失败（HTTP %d）：%s", status, message)
	}
	var body struct {
		Accounts []externalAccount `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("分发服务返回的账号无法解析")
	}
	return body.Accounts, nil
}

func (c *distributionRobotClient) Draft(ctx context.Context, platform, key string) (robotDraftRecord, error) {
	status, raw, err := c.do(ctx, http.MethodGet, "/v1/platforms/"+url.PathEscape(platform)+"/drafts/"+url.PathEscape(key), nil)
	if err != nil {
		return robotDraftRecord{}, &robotUncertainError{Cause: err.Error()}
	}
	if status == http.StatusOK {
		record, err := parseRobotDraftRecord(raw)
		if err != nil {
			return robotDraftRecord{}, &robotUncertainError{Cause: err.Error()}
		}
		return record, nil
	}
	code, message := c.parseError(status, raw)
	if status == http.StatusNotFound && code == "not_found" {
		return robotDraftRecord{}, errRobotDraftNotFound
	}
	return robotDraftRecord{}, &robotUncertainError{Cause: fmt.Sprintf("HTTP %d %s：%s", status, code, message)}
}

func (c *distributionRobotClient) do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("请求地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, errors.New("网络请求失败或超时")
	}
	defer resp.Body.Close()
	raw, err := readBoundedBody(resp.Body, distributionRobotMaxResponse)
	if err != nil {
		return 0, nil, errors.New("响应读取失败")
	}
	return resp.StatusCode, raw, nil
}

func (c *distributionRobotClient) parseError(status int, raw []byte) (string, string) {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Error.Message == "" {
		return envelope.Error.Code, fmt.Sprintf("HTTP %d 没有说明原因", status)
	}
	message := envelope.Error.Message
	if c.token != "" {
		message = strings.ReplaceAll(message, c.token, "[REDACTED]")
	}
	return strings.TrimSpace(envelope.Error.Code), safeDistributionMessage(message)
}

func parseRobotDraftRecord(raw []byte) (robotDraftRecord, error) {
	var record robotDraftRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return robotDraftRecord{}, errors.New("响应不是完整的草稿记录")
	}
	record.Status = strings.TrimSpace(record.Status)
	record.ArticleID = strings.TrimSpace(record.ArticleID)
	record.DraftURL = strings.TrimSpace(record.DraftURL)
	return record, nil
}

// safeDistributionMessage 分发服务给的原因压成一行、最多 240 字，写进卡片与日志。
func safeDistributionMessage(message string) string {
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
