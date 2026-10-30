package main

// distribution_robot.go — 自己的分发服务（FBIF-多平台分发工具）的客户端。
//
// 分发服务在 B 机上用托管的浏览器登录态往平台编辑器里写稿、再按发布，A 机经 SSH 通道在本机
// 127.0.0.1:18095 访问它（接口说明书是那个仓库的 docs/api.md）。同一个平台同一个幂等键只建一篇
// 草稿：结果不明时用同一个键查（GET）或再提交（POST）都安全，分发服务自己去草稿箱对账。发布点下去
// 以后它绝不自动再点：同一篇再调发布只会等正在做的那次，或回平台上已经是什么样。
// 出错时分发服务说清这次有没有动手（outcome），这里照它认，不按 HTTP 状态猜；每个错误码的状态和
// outcome 存一份在 testdata/distribution-api.json（分发服务 npm run gen:docs 生成），测试按它核对。

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

// loadDistributionRobotConfig 平台限制与存草稿都经它；错误信息只说缺哪个变量，绝不回显 token。
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
	IdempotencyKey string  `json:"idempotency_key"`
	Account        string  `json:"account"` // 多维表格「平台账号」的 record_id
	Title          string  `json:"title"`
	Blocks         []Block `json:"blocks"` // 外发通用版正文块（distribution_blocks.go），分发服务按平台写成标签
	CoverURL       string  `json:"cover_url"`
}

// robotDraftRecord 分发服务对一个幂等键的记录（写草稿、发布的响应与 GET 同一份）。
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

	// 存成草稿以后才有：平台上现在的样子、已发布的文章链接、最近一次按的按钮（GET 与发布的响应带
	// action，写草稿的响应不带）、存进去时的草稿指纹。
	State      *robotDraftState    `json:"state"`
	ArticleURL string              `json:"article_url"`
	Action     *robotDraftAction   `json:"action"`
	Revision   *robotDraftRevision `json:"revision"`
}

// robotDraftState 分发服务按编号查到的平台现状。
type robotDraftState struct {
	// draft / reviewing / scheduled / published / hidden / withdrawn / rejected / deleted / unrecognized
	Value  string `json:"value"`
	Raw    string `json:"raw"`    // 平台自己的说法
	Reason string `json:"reason"` // 未通过时平台给的原因
	URL    string `json:"url"`
}

// robotDraftAction 分发服务记下的一次按按钮（发布、下线、删草稿）。
type robotDraftAction struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// pending 还没点；doing 可能已经点下去、结果还没看清；done 做成了；failed 明确没做成、可以再按；
	// unknown 点下去了、查了一阵没看清，要人去后台看
	Status   string `json:"status"`
	Outcome  string `json:"outcome"` // done 时：by_us 这次按的，already 按之前平台上就是这样
	Actor    string `json:"actor"`
	Evidence string `json:"evidence"`
	Error    string `json:"error"`
}

type robotDraftRevision struct {
	Hash   string `json:"hash"`
	Title  string `json:"title"`
	Chars  int    `json:"chars"`
	Images int    `json:"images"`
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

// 发布动作（action）的几种状态，和草稿记录的 status 是两回事。
const (
	robotActionPublish = "publish"

	robotActionPending = "pending"
	robotActionDoing   = "doing"
	robotActionDone    = "done"
	robotActionFailed  = "failed"
	robotActionUnknown = "unknown"
)

// 平台上这篇现在的样子（state.value）里排版工具要分开处理的几种。
const (
	robotStateReviewing = "reviewing"
	robotStateScheduled = "scheduled"
	robotStatePublished = "published"
	robotStateRejected  = "rejected"
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
	// Publish 把这个键存好的草稿发出去；actor 记进分发服务的这次动作（飞书 open_id 或 system:…）。
	Publish(ctx context.Context, platform, key, actor string) (robotDraftRecord, error)
}

// 分发服务错误响应里的 outcome：这次有没有动手。
const (
	robotOutcomeRejected  = "rejected"  // 没动手：点会生效的那一下之前就拒了
	robotOutcomeUncertain = "uncertain" // 说不准：可能已经做成，用同一个键查
	robotOutcomeHuman     = "human"     // 停下等人去平台后台看
)

// 三类错误都可能带着分发服务回的记录（Record）：发布出错时它说清这次动作停在哪。

// robotRejectedError 分发服务在点会生效的那一下之前就拒了（参数、token、封面、没登录、草稿被人改过、
// 平台上这篇已不能发布），没动手；存草稿的改好后同一个键或新键再提交都安全。
type robotRejectedError struct {
	Status  int
	Code    string
	Message string
	Record  *robotDraftRecord
}

func (e *robotRejectedError) Error() string {
	return fmt.Sprintf("分发工具没动手（HTTP %d %s）：%s", e.Status, e.Code, e.Message)
}

// robotReconcileError 要人去平台后台看：存草稿点了没拿到编号、草稿箱里也没对清（这次新建的同名草稿
// 不止一篇），或上次发布点下去后没看清做成没有（needs_human）。
type robotReconcileError struct {
	Code    string
	Message string
	Record  *robotDraftRecord
}

func (e *robotReconcileError) Error() string { return e.Message }

// robotUncertainError 平台出错、超时、断网或响应不完整：不知道做没做成，按同一个键查。
type robotUncertainError struct {
	Cause  string
	Record *robotDraftRecord
}

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
	return robotDraftRecord{}, c.replyError(status, raw)
}

// Publish 发布要点会生效的按钮：分发服务最多等 60 秒，做完回 200，还没做完回 202（action.status
// 是 doing），之后 GET 看结果。点下去以后它绝不自动再点，所以说不准时再调也安全。
func (c *distributionRobotClient) Publish(ctx context.Context, platform, key, actor string) (robotDraftRecord, error) {
	body, err := json.Marshal(struct {
		Actor string `json:"actor"`
	}{actor})
	if err != nil {
		return robotDraftRecord{}, &robotRejectedError{Message: "请求参数无法序列化"}
	}
	status, raw, err := c.do(ctx, http.MethodPost, "/v1/platforms/"+url.PathEscape(platform)+"/drafts/"+url.PathEscape(key)+"/publish", body)
	if err != nil {
		return robotDraftRecord{}, &robotUncertainError{Cause: err.Error()}
	}
	if status == http.StatusOK || status == http.StatusAccepted {
		record, err := parseRobotDraftRecord(raw)
		if err != nil {
			return robotDraftRecord{}, &robotUncertainError{Cause: err.Error()}
		}
		return record, nil
	}
	return robotDraftRecord{}, c.replyError(status, raw)
}

// replyError 分发服务的错误回答按它说的 outcome 归类，不按 HTTP 状态猜。
func (c *distributionRobotClient) replyError(status int, raw []byte) error {
	reply := c.parseError(status, raw)
	switch reply.Outcome {
	case robotOutcomeRejected:
		return &robotRejectedError{Status: status, Code: reply.Code, Message: reply.Message, Record: reply.Record}
	case robotOutcomeHuman:
		return &robotReconcileError{Code: reply.Code, Message: reply.Message, Record: reply.Record}
	default:
		// 说不准，或没带认得的 outcome（不是分发服务回的）：不知道做没做成，用同一个键查。
		return &robotUncertainError{Cause: fmt.Sprintf("HTTP %d %s：%s", status, reply.Code, reply.Message), Record: reply.Record}
	}
}

func (c *distributionRobotClient) Platforms(ctx context.Context) ([]distributionPlatformInfo, error) {
	status, raw, err := c.do(ctx, http.MethodGet, "/v1/platforms", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("分发服务读平台限制失败（HTTP %d）：%s", status, c.parseError(status, raw).Message)
	}
	var body struct {
		Platforms []distributionPlatformInfo `json:"platforms"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("分发服务返回的平台限制无法解析")
	}
	return body.Platforms, nil
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
	reply := c.parseError(status, raw)
	if status == http.StatusNotFound && reply.Code == "not_found" {
		return robotDraftRecord{}, errRobotDraftNotFound
	}
	return robotDraftRecord{}, &robotUncertainError{Cause: fmt.Sprintf("HTTP %d %s：%s", status, reply.Code, reply.Message)}
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

// robotErrorReply 分发服务的错误回答：{error:{code,message,outcome}, record?}。
type robotErrorReply struct {
	Code, Message, Outcome string
	Record                 *robotDraftRecord
}

func (c *distributionRobotClient) parseError(status int, raw []byte) robotErrorReply {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Outcome string `json:"outcome"`
		} `json:"error"`
		Record *robotDraftRecord `json:"record"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Error.Message == "" {
		return robotErrorReply{Code: envelope.Error.Code, Message: fmt.Sprintf("HTTP %d 没有说明原因", status)}
	}
	message := envelope.Error.Message
	if c.token != "" {
		message = strings.ReplaceAll(message, c.token, "[REDACTED]")
	}
	if envelope.Record != nil {
		record := normalizeRobotDraftRecord(*envelope.Record)
		envelope.Record = &record
	}
	return robotErrorReply{
		Code: strings.TrimSpace(envelope.Error.Code), Message: safeDistributionMessage(message),
		Outcome: strings.TrimSpace(envelope.Error.Outcome), Record: envelope.Record,
	}
}

func parseRobotDraftRecord(raw []byte) (robotDraftRecord, error) {
	var record robotDraftRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return robotDraftRecord{}, errors.New("响应不是完整的草稿记录")
	}
	return normalizeRobotDraftRecord(record), nil
}

func normalizeRobotDraftRecord(record robotDraftRecord) robotDraftRecord {
	record.Status = strings.TrimSpace(record.Status)
	record.ArticleID = strings.TrimSpace(record.ArticleID)
	record.DraftURL = strings.TrimSpace(record.DraftURL)
	record.ArticleURL = strings.TrimSpace(record.ArticleURL)
	if record.State != nil {
		state := *record.State
		state.Value, state.URL = strings.TrimSpace(state.Value), strings.TrimSpace(state.URL)
		record.State = &state
	}
	if record.Action != nil {
		action := *record.Action
		action.ID, action.Kind, action.Status = strings.TrimSpace(action.ID), strings.TrimSpace(action.Kind), strings.TrimSpace(action.Status)
		record.Action = &action
	}
	return record
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
