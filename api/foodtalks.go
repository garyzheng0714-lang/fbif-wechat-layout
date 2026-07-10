package main

// foodtalks.go — FoodTalks 网站发布 API 客户端（施工图 S3：从 FBIF资讯订阅仓
// internal/foodtalks/client.go 搬入，逻辑逐行保真，仅改 package main 并给标识符加
// foodTalks 前缀避免单包冲突）。唯一真相源：docs/foodtalks-api-contract.md
//（2026-07-10 冒烟实测），要点：
//   - 网关（契约 §1）：请求必须带 Origin + Referer + 浏览器形 UA，否则 nginx 直接 403 返回
//     HTML 错误页（不是 JSON）；应用层无/错 token 才是 401。
//   - 响应（契约 §2）：统一 {"code":0,"msg":"success","data":<newsId>}；业务失败也是
//     HTTP 200（code!=0 + 中文 msg），客户端必须判 code，不能信 HTTP 状态码。
//   - publishStatus（契约 §3）：0=草稿/1=发布/2=下线；单向门 1↛0（驳回回炉用 2）。
//     API 不校验枚举范围（乱值也 success），客户端只准发 0/1/2——本文件发前显式校验。
//   - PUT 是全量覆盖语义（契约 §7）：请求体与 POST 同一份完整字段集，不是增量 patch。
//
// 凭证 env-only（FOODTALKS_BASE_URL / FOODTALKS_TOKEN / FOODTALKS_ORIGIN），绝不入库、
// 入 git、入日志；缺任一构造即失败（fail loud），不留默认值兜底真实域名。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	foodTalksBaseURLEnv = "FOODTALKS_BASE_URL"
	foodTalksTokenEnv   = "FOODTALKS_TOKEN"
	foodTalksOriginEnv  = "FOODTALKS_ORIGIN"

	// foodTalksNewsPath 发布与编辑共用同一路径：POST 创建、PUT 全量覆盖编辑。
	foodTalksNewsPath = "/news/admin/news"

	// foodTalksBrowserUA 浏览器形 UA：nginx/WAF 层按「像不像浏览器」放行（契约 §1），
	// 非浏览器 UA 会被拦。值本身不敏感，固定一份即可，不必可配。
	foodTalksBrowserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

// publishStatus 已确认语义的枚举（契约 §3 实测）。API 侧不校验范围，客户端必须自律。
const (
	foodTalksPublishStatusDraft     = 0 // 草稿：进站态，前台完全不可见（流水线「先隐藏发布」用它）
	foodTalksPublishStatusPublished = 1 // 已发布：前台可见。⚠️ 单向门：发布过永远回不到草稿 0
	foodTalksPublishStatusOffline   = 2 // 下线：前台不可见，可再翻回 1；驳回回炉用它，不用 0
)

// errFoodTalksNotConfigured 凭证未配置——调用方应显式落败并提示，不得静默跳过。
var errFoodTalksNotConfigured = errors.New("FoodTalks 发布未配置（需 env FOODTALKS_BASE_URL / FOODTALKS_TOKEN；FOODTALKS_ORIGIN 仅测试环境需要）")

// errFoodTalksUnauthorized 应用层鉴权失败（HTTP 401，或带 JSON 体的 403）。
// 降级行为：job 落 failed 并给人话「FoodTalks 凭证过期」，不静默重试打爆接口。
var errFoodTalksUnauthorized = errors.New("FoodTalks 凭证过期或无效，请更换 FOODTALKS_TOKEN")

// errFoodTalksGatewayBlocked nginx/WAF 层拦截（HTTP 403 + 非 JSON 的 HTML 错误页，契约 §1）。
// 与 errFoodTalksUnauthorized 分开指路：这一类改 token 没用，要查请求头或出口 IP。
var errFoodTalksGatewayBlocked = errors.New("FoodTalks 网关拦截：请求缺 Origin/Referer 头，或服务器出口 IP 被拦")

// foodTalksAPIError 业务错误（HTTP 200 但 code!=0，契约 §2）。Msg 是 API 返回的中文人话原文
// （例：「不允许将已发布的资讯改为草稿状态！」），原样携带供 job 失败原因展示。
type foodTalksAPIError struct {
	Code int
	Msg  string
}

func (e *foodTalksAPIError) Error() string {
	return fmt.Sprintf("FoodTalks 业务错误 code=%d msg=%s", e.Code, e.Msg)
}

// foodTalksNews 资讯全量字段集。JSON tag 与抓包接口文档逐字对齐（wiki「【API】FoodTalks 后台
// 资讯发布&编辑」）；PUT 是全量覆盖语义，调用方必须带完整字段，不能只带改动项。
// ID 不直接序列化：POST 时 API 要求传空字符串 ""、PUT 时传数字（同一字段两种形态），
// 由 CreateNews/UpdateNews 构造请求体时显式写入（见 foodTalksNewsBody）。
type foodTalksNews struct {
	ID int64 `json:"-"` // 资讯 id：CreateNews 必须为 0，UpdateNews 必须 >0（幂等锚点）

	Title               string   `json:"title"`
	CoverImg            string   `json:"coverImg"`
	SourceType          int      `json:"sourceType"`
	PublishStatus       int      `json:"publishStatus"` // 只准 0/1/2，见包常量
	IsPublic            int      `json:"isPublic"`      // ⚠️ 实测不是可见性开关（契约 §4），隐藏靠 publishStatus
	SourceID            int64    `json:"sourceId"`
	SourceName          string   `json:"sourceName"`
	Author              string   `json:"author"`
	Summary             string   `json:"summary"`
	NewsTags            []string `json:"newsTags"`
	IsLatest            int      `json:"isLatest"`
	ImgPreviewEnable    bool     `json:"imgPreviewEnable"` // 接口文档中唯一的布尔字段
	CommentEnable       int      `json:"commentEnable"`
	Content             string   `json:"content"`     // 正文 HTML；图片必须已转存（外链裸奔由流水线上游把关）
	PublishTime         string   `json:"publishTime"` // 格式 YYYY-MM-DD HH:mm:ss
	PrimaryCategoryID   int64    `json:"primaryCategoryId"`
	SEOTitle            string   `json:"seoTitle"`
	SEOKeywords         string   `json:"seoKeywords"`
	SEODescription      string   `json:"seoDescription"`
	NewsRelationIDs     []int64  `json:"newsRelationIds"`
	CategoryIDs         []int64  `json:"categoryIds"`
	TopicSectionIDList  []int64  `json:"topicSectionIdList"`
	RelatedCompanyIDs   []int64  `json:"relatedCompanyIds"`
	RelatedFoodpediaIDs []int64  `json:"relatedFoodpediaIds"`
	Language            string   `json:"language"` // 中文站固定 "ZH"（由调用方传，本文件不设默认）
	TOC                 string   `json:"toc"`
}

// foodTalksNewsBody 请求体 = id + News 全字段。id 是本 API 的怪点：POST 传空字符串 ""、
// PUT 传数字（抓包文档逐字口径）。用 json.RawMessage 显式承载两种字面形态，
// 不用 interface{} 糊。
type foodTalksNewsBody struct {
	ID json.RawMessage `json:"id"`
	foodTalksNews
}

// buildFoodTalksNewsBody 组装请求体，并把 nil 切片归一为空切片：抓包样例中数组字段一律是
// []，序列化成 null 属于契约外形态（后端行为未定义），不冒这个险。
func buildFoodTalksNewsBody(idRaw json.RawMessage, n foodTalksNews) foodTalksNewsBody {
	if n.NewsTags == nil {
		n.NewsTags = []string{}
	}
	if n.NewsRelationIDs == nil {
		n.NewsRelationIDs = []int64{}
	}
	if n.CategoryIDs == nil {
		n.CategoryIDs = []int64{}
	}
	if n.TopicSectionIDList == nil {
		n.TopicSectionIDList = []int64{}
	}
	if n.RelatedCompanyIDs == nil {
		n.RelatedCompanyIDs = []int64{}
	}
	if n.RelatedFoodpediaIDs == nil {
		n.RelatedFoodpediaIDs = []int64{}
	}
	return foodTalksNewsBody{ID: idRaw, foodTalksNews: n}
}

// validateFoodTalksPublishStatus 客户端侧枚举自律（契约 §3）：API 对乱值也返回 success，
// 一旦发出去行为未定义（且可能踩单向门），所以必须在发出前拦下。
func validateFoodTalksPublishStatus(v int) error {
	switch v {
	case foodTalksPublishStatusDraft, foodTalksPublishStatusPublished, foodTalksPublishStatusOffline:
		return nil
	}
	return fmt.Errorf("publishStatus=%d 不在允许集 {0=草稿,1=发布,2=下线}：API 不校验枚举，客户端只准发已确认语义的值", v)
}

// foodTalksClient FoodTalks 发布客户端。
type foodTalksClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
	origin     string // Origin 头原值；Referer 由它派生（契约 §1：值随意有效域即可）
}

// newFoodTalksClientFromEnv 从 env 构造；任一凭证缺失返回 errFoodTalksNotConfigured
// （fail loud）。不留默认 Base URL：测试/正式环境域名不同，写死任何一个都会让缺配置
// 静默打错环境。
func newFoodTalksClientFromEnv() (*foodTalksClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv(foodTalksBaseURLEnv)), "/")
	token := strings.TrimSpace(os.Getenv(foodTalksTokenEnv))
	// FOODTALKS_ORIGIN 是「浏览器头模式」开关（2026-07-10 用户裁定 + 双环境实测）：
	//   - 配置了 → 带 Origin/Referer/UA 三头（测试环境网关要求，否则 403）
	//   - 不配置 → 纯 token 直连（正式环境走 IP 白名单，后端要求不伪装浏览器）
	origin := strings.TrimRight(strings.TrimSpace(os.Getenv(foodTalksOriginEnv)), "/")
	if baseURL == "" || token == "" {
		return nil, errFoodTalksNotConfigured
	}
	return &foodTalksClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		token:      token,
		origin:     origin,
	}, nil
}

// CreateNews 创建资讯（POST，id 传 ""），返回新资讯 id——调用方必须回填 publish job
// 作幂等锚点。n.ID 必须为 0：已有锚点的文章应走 UpdateNews，误走 CreateNews 会建重复文章。
func (c *foodTalksClient) CreateNews(ctx context.Context, n foodTalksNews) (int64, error) {
	if n.ID != 0 {
		return 0, fmt.Errorf("CreateNews 收到已有资讯 id=%d：已回填锚点的文章必须走 UpdateNews，否则会创建重复文章", n.ID)
	}
	if err := validateFoodTalksPublishStatus(n.PublishStatus); err != nil {
		return 0, err
	}
	newsID, err := c.send(ctx, http.MethodPost, buildFoodTalksNewsBody(json.RawMessage(`""`), n))
	if err != nil {
		return 0, err
	}
	if newsID == nil {
		// 契约 §2：成功时 data 就是资讯 id；拿不到 id 等于丢了幂等锚点，必须显式失败。
		return 0, errors.New("FoodTalks 创建成功但响应 data 为空，拿不到资讯 id（幂等锚点缺失）")
	}
	return *newsID, nil
}

// UpdateNews 编辑资讯（PUT，id 传数字）。全量覆盖语义（契约 §7）：n 必须是完整字段集，
// 缺的字段会被覆盖成零值。注意单向门：已发布(1)的文章不能改回草稿(0)，会收到业务错误。
func (c *foodTalksClient) UpdateNews(ctx context.Context, n foodTalksNews) error {
	if n.ID <= 0 {
		return fmt.Errorf("UpdateNews 需要已有资讯 id（>0），当前 id=%d：新文章应走 CreateNews", n.ID)
	}
	if err := validateFoodTalksPublishStatus(n.PublishStatus); err != nil {
		return err
	}
	_, err := c.send(ctx, http.MethodPut, buildFoodTalksNewsBody(json.RawMessage(strconv.FormatInt(n.ID, 10)), n))
	return err
}

// send 发一次请求并按契约分类结果：网络错 / 401 / 403(JSON=凭证, HTML=网关) / code!=0 业务错。
// 成功时返回 data（资讯 id；可能为 null 故用指针区分「没给」和 0）。
func (c *foodTalksClient) send(ctx context.Context, method string, body foodTalksNewsBody) (*int64, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 序列化请求体: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+foodTalksNewsPath, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 构造请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	// 浏览器头模式（origin 已配置）：测试环境网关要求 Origin/Referer/UA，缺任一 403（契约 §1）。
	// 白名单模式（origin 未配置）：正式环境按 IP 放行，不伪装浏览器（2026-07-10 用户裁定，
	// 实测纯 token 直达应用层）；显式置空 UA 压掉 Go 默认的 Go-http-client。
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
		req.Header.Set("Referer", c.origin+"/")
		req.Header.Set("User-Agent", foodTalksBrowserUA)
	} else {
		req.Header.Set("User-Agent", "")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FoodTalks 接口不可达（%s %s）: %w", method, foodTalksNewsPath, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 读响应: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// 继续走 body 判定——HTTP 200 不代表成功（契约 §2）。
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w（HTTP 401）", errFoodTalksUnauthorized)
	case http.StatusForbidden:
		// 403 两种成因必须分开指路：应用层 JSON → 换凭证；nginx HTML → 查请求头/出口 IP。
		if json.Valid(respBody) {
			return nil, fmt.Errorf("%w（HTTP 403）", errFoodTalksUnauthorized)
		}
		return nil, fmt.Errorf("%w（HTTP 403，响应片段：%s）", errFoodTalksGatewayBlocked, foodTalksSnippet(respBody))
	default:
		return nil, fmt.Errorf("FoodTalks 接口异常（HTTP %d，响应片段：%s）", resp.StatusCode, foodTalksSnippet(respBody))
	}

	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *int64 `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("FoodTalks 响应不是预期 JSON（HTTP 200，响应片段：%s）: %w", foodTalksSnippet(respBody), err)
	}
	if env.Code != 0 {
		return nil, &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	return env.Data, nil
}

// foodTalksSnippet 截取响应前 200 字节进错误信息（够定位 nginx 错误页/意外形态，不刷屏日志）。
func foodTalksSnippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}
