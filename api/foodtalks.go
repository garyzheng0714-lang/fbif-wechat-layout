package main

// foodtalks.go — FoodTalks 网站发布 API 客户端（施工图 S3：从 FBIF资讯订阅仓
// internal/foodtalks/client.go 搬入，逻辑逐行保真，仅改 package main 并给标识符加
// foodTalks 前缀避免单包冲突）。唯一真相源：docs/foodtalks-api-contract.md
//（2026-07-10 冒烟 + 2026-08-14 双环境补测），要点：
//   - 网关（契约 §1）：测试环境需 Origin + Referer + 浏览器形 UA；正式环境白名单模式
//     必须反过来清空这些浏览器伪装头。应用层无/错 token 才是 401。
//   - 响应（契约 §2）：统一 {"code":0,"msg":"success","data":<newsId>}；业务失败也是
//     HTTP 200（code!=0 + 中文 msg），客户端必须判 code，不能信 HTTP 状态码。
//   - publishStatus（契约 §3）：0=草稿/1=发布/2=下线；单向门 1↛0（驳回回炉用 2）。
//     API 不校验枚举范围（乱值也 success），客户端只准发 0/1/2——本文件发前显式校验。
//   - PUT 是全量覆盖语义（契约 §7）：请求体与 POST 同一份完整字段集，不是增量 patch。
//
// 配置 env-only（FOODTALKS_BASE_URL / FOODTALKS_TOKEN；测试环境另配 FOODTALKS_ORIGIN），
// 绝不入库、入 git、入日志；缺 URL/token 即失败（fail loud），不以真实域名默认兜底。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

	// foodTalksTagTypeArticleSource CMS「来源出处」下拉的标签类型。
	// 文档曾写 GET /admin/tag/type/{tagTypeId}/list，正式环境该路径 404。
	// 2026-08-13 生产实测：GET /basic/tag/page?type=10&status=1&keywords=
	// 缺失时 POST /basic/admin/tag/by-name {"name","typeId"}，成功 data 为标签 id，同名幂等。
	foodTalksTagTypeArticleSource int64 = 10
	foodTalksTagPagePath                = "/basic/tag/page"
	foodTalksTagByNamePath              = "/basic/admin/tag/by-name"

	// foodTalksCompanySearchPath 公司库检索（2026-08-17 生产实测）：文档记载的
	// /company/admin/companies/page 对服务账号返回「Access is denied」（缺 company
	// 模块权限），而前台公开检索接口同域可用且字段更全（cnName/shortCnName/urlCode），
	// 用它做正文公司库关联的解析源。
	foodTalksCompanySearchPath = "/company/companies/search"

	// foodTalksBrowserUA 仅用于 FOODTALKS_ORIGIN 非空的测试网关：该环境要求浏览器形
	// UA；正式白名单模式恰好相反，request 构造会显式清空 UA（契约 §1）。
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

// foodTalksNewsTag 是发布/编辑接口确认过的 newsTags 元素结构。tagId 必须来自
// tag_type_id=6 的真实标签库；2026-08-14 复测确认 PUT 已修复，主链路会发送按规范化
// 名称解析到的现有标签且不自动创建。类型层同时禁止退回会 500 的字符串数组。
type foodTalksNewsTag struct {
	TagID   int64  `json:"tagId"`
	TagName string `json:"tagName"`
}

// foodTalksTag 标签库条目。来源出处下拉用 tagTypeId=10 的 id/name。
type foodTalksTag struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	TagTypeID int64  `json:"tagTypeId"`
	Sort      int    `json:"sort"`
}

// foodTalksNews 资讯全量字段集。JSON tag 与抓包接口文档逐字对齐（wiki「【API】FoodTalks 后台
// 资讯发布&编辑」）；PUT 是全量覆盖语义，调用方必须带完整字段，不能只带改动项。
// ID 不直接序列化：POST 时 API 要求传空字符串 ""、PUT 时传数字（同一字段两种形态），
// 由 CreateNews/UpdateNews 构造请求体时显式写入（见 foodTalksNewsBody）。
type foodTalksNews struct {
	ID int64 `json:"-"` // 资讯 id：CreateNews 必须为 0，UpdateNews 必须 >0（幂等锚点）

	Title               string             `json:"title"`
	CoverImg            string             `json:"coverImg"`
	SourceType          int                `json:"sourceType"`
	PublishStatus       int                `json:"publishStatus"` // 只准 0/1/2，见包常量
	IsPublic            int                `json:"isPublic"`      // ⚠️ 实测不是可见性开关（契约 §4），隐藏靠 publishStatus
	SourceID            int64              `json:"sourceId"`      // CMS「来源出处」下拉绑定的标签 id（tagTypeId=10）；0 时后台显示「请选择」
	SourceName          string             `json:"sourceName"`
	Author              string             `json:"author"`
	Summary             string             `json:"summary"`
	NewsTags            []foodTalksNewsTag `json:"newsTags"`
	IsLatest            int                `json:"isLatest"`         // 是否最新资讯；官方示例恒 1，首页最新模块按此过滤
	ImgPreviewEnable    bool               `json:"imgPreviewEnable"` // 接口文档中唯一的布尔字段；官方示例 true
	CommentEnable       int                `json:"commentEnable"`    // 是否允许评论；官方示例 1
	Content             string             `json:"content"`          // 正文 HTML；图片必须已转存（外链裸奔由流水线上游把关）
	PublishTime         string             `json:"publishTime"`      // 格式 YYYY-MM-DD HH:mm:ss
	PrimaryCategoryID   int64              `json:"primaryCategoryId"`
	SEOTitle            string             `json:"seoTitle"`
	SEOKeywords         string             `json:"seoKeywords"`
	SEODescription      string             `json:"seoDescription"`
	NewsRelationIDs     []int64            `json:"newsRelationIds"`
	CategoryIDs         []int64            `json:"categoryIds"`
	TopicSectionIDList  []int64            `json:"topicSectionIdList"`
	RelatedCompanyIDs   []int64            `json:"relatedCompanyIds"`
	RelatedFoodpediaIDs []int64            `json:"relatedFoodpediaIds"`
	Language            string             `json:"language"` // 中文站固定 "ZH"（由调用方传，本文件不设默认）
	TOC                 string             `json:"toc"`
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
		n.NewsTags = []foodTalksNewsTag{}
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
	origin     string               // Origin 头原值；Referer 由它派生（契约 §1：值随意有效域即可）
	authHealth *foodTalksAuthHealth // 可空；生产由 pipeline 装配，监控据此报凭证失效
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

// ListTagsByType 按标签类型查目录。来源出处下拉对应 type=10。
// keywords 非空时按名称检索（来源库有数千条，不能全量拉取再本地过滤）。
func (c *foodTalksClient) ListTagsByType(ctx context.Context, tagTypeID int64, keywords string) ([]foodTalksTag, error) {
	if tagTypeID <= 0 {
		return nil, fmt.Errorf("ListTagsByType 需要正数 tagTypeId，当前 %d", tagTypeID)
	}
	query := url.Values{}
	query.Set("current", "1")
	query.Set("size", "50")
	query.Set("type", strconv.FormatInt(tagTypeID, 10))
	query.Set("status", "1")
	if strings.TrimSpace(keywords) != "" {
		query.Set("keywords", strings.TrimSpace(keywords))
	}
	path := foodTalksTagPagePath + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 构造标签列表请求: %w", err)
	}
	c.applyHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("FoodTalks 接口不可达（GET %s）: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 读标签列表响应: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	default:
		return nil, c.statusError("接口", resp.StatusCode, respBody)
	}
	tags, err := parseFoodTalksTagList(respBody)
	if err != nil {
		return nil, fmt.Errorf("FoodTalks 标签列表无法解析（HTTP 200，响应片段：%s）: %w", c.snippet(respBody), err)
	}
	return tags, nil
}

type foodTalksCreateTagBody struct {
	Name   string `json:"name"`
	TypeID int64  `json:"typeId"`
}

// CreateTagByName 按名称创建标签。来源出处用 typeId=10。
// 生产实测：成功 {code:0,data:<id>}；同名再调返回同一 id，不会建重复。
func (c *foodTalksClient) CreateTagByName(ctx context.Context, tagTypeID int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if tagTypeID <= 0 {
		return 0, fmt.Errorf("CreateTagByName 需要正数 typeId，当前 %d", tagTypeID)
	}
	if name == "" {
		return 0, fmt.Errorf("CreateTagByName 需要非空名称")
	}
	raw, err := json.Marshal(foodTalksCreateTagBody{Name: name, TypeID: tagTypeID})
	if err != nil {
		return 0, fmt.Errorf("foodtalks: 序列化创建标签请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+foodTalksTagByNamePath, bytes.NewReader(raw))
	if err != nil {
		return 0, fmt.Errorf("foodtalks: 构造创建标签请求: %w", err)
	}
	c.applyHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return 0, fmt.Errorf("FoodTalks 接口不可达（POST %s）: %w", foodTalksTagByNamePath, err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return 0, fmt.Errorf("foodtalks: 读创建标签响应: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusBadRequest:
		// 400 仍可能是 JSON 业务错误（「标签名称不能为空」），按 body.code 判定。
	default:
		return 0, c.statusError("接口", resp.StatusCode, respBody)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *int64 `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return 0, fmt.Errorf("FoodTalks 创建标签响应不是预期 JSON（HTTP %d，响应片段：%s）: %w", resp.StatusCode, c.snippet(respBody), err)
	}
	if env.Code != 0 {
		return 0, &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	if env.Data == nil || *env.Data <= 0 {
		return 0, errors.New("FoodTalks 创建标签成功但响应 data 不是正数 id")
	}
	return *env.Data, nil
}

func parseFoodTalksTagList(respBody []byte) ([]foodTalksTag, error) {
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, err
	}
	if env.Code != 0 {
		return nil, &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return []foodTalksTag{}, nil
	}
	var tags []foodTalksTag
	if err := json.Unmarshal(env.Data, &tags); err == nil {
		if tags == nil {
			tags = []foodTalksTag{}
		}
		return tags, nil
	}
	var wrapped struct {
		Records []foodTalksTag `json:"records"`
	}
	if err := json.Unmarshal(env.Data, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Records == nil {
		wrapped.Records = []foodTalksTag{}
	}
	return wrapped.Records, nil
}

func matchFoodTalksSourceID(tags []foodTalksTag, sourceName string) int64 {
	want := normalizeFoodTalksSourceName(sourceName)
	if want == "" {
		return 0
	}
	for _, tag := range tags {
		if normalizeFoodTalksSourceName(tag.Name) == want && tag.ID > 0 {
			return tag.ID
		}
	}
	return 0
}

func normalizeFoodTalksSourceName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, " ", "")
	name = strings.ReplaceAll(name, "　", "")
	return strings.ToLower(name)
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
	c.applyHeaders(req)

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("FoodTalks 接口不可达（%s %s）: %w", method, foodTalksNewsPath, err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 读响应: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// 继续走 body 判定——HTTP 200 不代表成功（契约 §2）。
	default:
		return nil, c.statusError("接口", resp.StatusCode, respBody)
	}

	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *int64 `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("FoodTalks 响应不是预期 JSON（HTTP 200，响应片段：%s）: %w", c.snippet(respBody), err)
	}
	if env.Code != 0 {
		return nil, &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	return env.Data, nil
}

// applyHeaders 统一请求头（send 与 PreviewToken 共用）：
// 浏览器头模式（origin 已配置）：测试环境网关要求 Origin/Referer/UA，缺任一 403（契约 §1）。
// 白名单模式（origin 未配置）：正式环境按 IP 放行，不伪装浏览器（2026-07-10 用户裁定，
// 实测纯 token 直达应用层）；显式置空 UA 压掉 Go 默认的 Go-http-client。
func (c *foodTalksClient) applyHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
		req.Header.Set("Referer", c.origin+"/")
		req.Header.Set("User-Agent", foodTalksBrowserUA)
	} else {
		req.Header.Set("User-Agent", "")
	}
}

// foodTalksNewsListItem 资讯列表页条目（同题防重用最小字段集）。
type foodTalksNewsListItem struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	PublishStatus int    `json:"publishStatus"`
	SourceName    string `json:"sourceName"`
	PublishTime   string `json:"publishTime"` // YYYY-MM-DD HH:mm:ss，北京时间
}

// foodTalksNewsDetail 资讯详情（GET /news/admin/news/{id}）。外发只读这篇，
// 不用任务里存过的历史正文。ID 必须从 JSON 读出，不能复用 foodTalksNews
// （那边的 ID 是 json:"-"，给 POST/PUT 用）。
type foodTalksNewsDetail struct {
	ID                 int64                           `json:"id"`
	Title              string                          `json:"title"`
	Language           string                          `json:"language"`
	CoverImg           string                          `json:"coverImg"`
	SourceType         int                             `json:"sourceType"`
	SourceID           int64                           `json:"sourceId"`
	SourceName         string                          `json:"sourceName"`
	Author             string                          `json:"author"`
	Summary            string                          `json:"summary"`
	Content            string                          `json:"content"`
	TOC                string                          `json:"toc"`
	NewsCategories     []foodTalksNewsCategoryDetail   `json:"newsCategories"`
	NewsTags           []foodTalksNewsTag              `json:"newsTags"`
	NewsRelation       []foodTalksNewsRelationDetail   `json:"newsRelation"`
	RelatedCompanies   []foodTalksRelatedCompanyDetail `json:"relatedCompanies"`
	RelatedFoodpedias  []foodTalksRelatedFoodpedia     `json:"relatedFoodpedias"`
	TopicSectionIDList []int64                         `json:"topicSectionIdList"`
	PublishStatus      int                             `json:"publishStatus"`
	IsPublic           bool                            `json:"isPublic"`
	IsLatest           int                             `json:"isLatest"`
	ImgPreviewEnable   bool                            `json:"imgPreviewEnable"`
	CommentEnable      int                             `json:"commentEnable"`
	SEOTitle           string                          `json:"seoTitle"`
	SEOKeywords        string                          `json:"seoKeywords"`
	SEODescription     string                          `json:"seoDescription"`
	PublishTime        string                          `json:"publishTime"`
}

type foodTalksNewsCategoryDetail struct {
	IsPrimary       int                           `json:"isPrimary"`
	CategoryTagID   int64                         `json:"categoryTagId"`
	ChildCategories []foodTalksNewsCategoryDetail `json:"childCategories"`
}

// foodTalksNewsRelationDetail 相关文章一行。2026-10-08 生产实测：关联文章 id 字段是
// relatedNewsId；id 是关联记录自身 id，newsId 是本稿 id，二者都不是关联文章。
type foodTalksNewsRelationDetail struct {
	ID            int64 `json:"id"`
	NewsID        int64 `json:"newsId"`
	RelatedNewsID int64 `json:"relatedNewsId"`
}

type foodTalksRelatedCompanyDetail struct {
	ID        int64 `json:"id"`
	CompanyID int64 `json:"companyId"`
}

type foodTalksRelatedFoodpedia struct {
	ID          int64 `json:"id"`
	FoodpediaID int64 `json:"foodpediaId"`
}

// GetNews 读取一篇资讯的当前详情（GET /news/admin/news/{id}）。
// 2026-08-19 生产实测：返回完整 content/coverImg/publishStatus，供外发只用
// 网站上最新已发布稿，而不是流水线库存里的历史版本。
func (c *foodTalksClient) GetNews(ctx context.Context, newsID int64) (foodTalksNewsDetail, error) {
	if newsID <= 0 {
		return foodTalksNewsDetail{}, fmt.Errorf("GetNews 需要已有资讯 id（>0），当前 id=%d", newsID)
	}
	path := foodTalksNewsPath + "/" + strconv.FormatInt(newsID, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return foodTalksNewsDetail{}, fmt.Errorf("foodtalks: 构造资讯详情请求: %w", err)
	}
	c.applyHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return foodTalksNewsDetail{}, fmt.Errorf("FoodTalks 接口不可达（GET %s）: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return foodTalksNewsDetail{}, fmt.Errorf("foodtalks: 读资讯详情响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return foodTalksNewsDetail{}, c.statusError("资讯详情", resp.StatusCode, respBody)
	}
	var env struct {
		Code int                  `json:"code"`
		Msg  string               `json:"msg"`
		Data *foodTalksNewsDetail `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return foodTalksNewsDetail{}, fmt.Errorf("FoodTalks 资讯详情响应不是预期 JSON（响应片段：%s): %w", c.snippet(respBody), err)
	}
	if env.Code != 0 {
		return foodTalksNewsDetail{}, &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	if env.Data == nil || env.Data.ID == 0 {
		return foodTalksNewsDetail{}, errors.New("FoodTalks 资讯详情响应缺 data")
	}
	return *env.Data, nil
}

// SearchNewsByTitle 按标题在资讯库检索（GET /news/admin/news/page?title=，
// 2026-08-14 实测：title 参数精确过滤，keywords 参数被后端忽略）。
// 同题防重用：返回前 20 条即可覆盖同名场景。
func (c *foodTalksClient) SearchNewsByTitle(ctx context.Context, title string) ([]foodTalksNewsListItem, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	query := url.Values{}
	query.Set("current", "1")
	query.Set("size", "20")
	query.Set("title", title)
	path := foodTalksNewsPath + "/page?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 构造标题检索请求: %w", err)
	}
	c.applyHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("FoodTalks 接口不可达（GET %s）: %w", foodTalksNewsPath+"/page", err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 读标题检索响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.statusError("标题检索", resp.StatusCode, respBody)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			Records []foodTalksNewsListItem `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("FoodTalks 标题检索响应不是预期 JSON（响应片段：%s): %w", c.snippet(respBody), err)
	}
	if env.Code != 0 {
		return nil, &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	if env.Data == nil {
		return []foodTalksNewsListItem{}, nil
	}
	return env.Data.Records, nil
}

// foodTalksCompany 公司库检索结果（2026-08-17 生产实测字段）。
// URLCode 是前台公司页地址段（/company/<urlCode>；数字 id 打不开公司页，实测 404 文案）。
type foodTalksCompany struct {
	ID          int64  `json:"id"`
	CnName      string `json:"cnName"`      // 全称：贵州茅台酒股份有限公司
	ShortCnName string `json:"shortCnName"` // 简称逗号表：贵州茅台,茅台股份,茅台（可空）
	EnName      string `json:"enName"`
	ShortEnName string `json:"shortEnName"`
	URLCode     string `json:"urlCode"`
}

// SearchCompanies 按关键词检索公司库（GET /company/companies/search）。
// 返回前 10 条即可覆盖匹配场景；调用方负责精确匹配与母/子公司选择。
func (c *foodTalksClient) SearchCompanies(ctx context.Context, keyword string) ([]foodTalksCompany, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	query := url.Values{}
	query.Set("current", "1")
	query.Set("size", "10")
	query.Set("keyword", keyword)
	path := foodTalksCompanySearchPath + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 构造公司检索请求: %w", err)
	}
	c.applyHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("FoodTalks 接口不可达（GET %s）: %w", foodTalksCompanySearchPath, err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 读公司检索响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.statusError("公司检索", resp.StatusCode, respBody)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			Records []foodTalksCompany `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("FoodTalks 公司检索响应不是预期 JSON（响应片段：%s): %w", c.snippet(respBody), err)
	}
	if env.Code != 0 {
		return nil, &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	if env.Data == nil {
		return []foodTalksCompany{}, nil
	}
	return env.Data.Records, nil
}

// PreviewToken 为未上架文章现铸官方预览 token（2026-07-10 从管理后台前端代码挖到并
// 实测：POST /news/admin/news/{id}/preview/token → {code:0, data:{id, token}}）。
// token 短期有效（前台横幅「将在短期内失效」）——**每次要用时现铸，不落库**。
// 前台预览页 = {FOODTALKS_FRONT_URL}/preview/news/{id}?token={token}。
func (c *foodTalksClient) PreviewToken(ctx context.Context, newsID int64) (string, error) {
	if newsID <= 0 {
		return "", fmt.Errorf("PreviewToken 需要已有资讯 id（>0），当前 id=%d", newsID)
	}
	path := "/news/admin/news/" + strconv.FormatInt(newsID, 10) + "/preview/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("foodtalks: 构造预览 token 请求: %w", err)
	}
	c.applyHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("FoodTalks 接口不可达（POST %s）: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return "", fmt.Errorf("foodtalks: 读预览 token 响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", c.statusError("预览 token 接口", resp.StatusCode, respBody)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return "", fmt.Errorf("FoodTalks 预览 token 响应不是预期 JSON（响应片段：%s): %w", c.snippet(respBody), err)
	}
	if env.Code != 0 {
		return "", &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	if env.Data == nil || strings.TrimSpace(env.Data.Token) == "" {
		return "", errors.New("FoodTalks 预览 token 响应缺 data.token")
	}
	return env.Data.Token, nil
}

// do 是全部 FoodTalks 请求的唯一出口：任何 2xx 都说明凭证被接受（业务 code 另判）。
// 失效结论由 statusError 记录，两处合起来就是监控的凭证信号。
func (c *foodTalksClient) do(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.authHealth.record(true)
	}
	return resp, err
}

// statusError 把非成功 HTTP 状态归类：401 与带 JSON 体的 403 是凭证失效（换 token），
// HTML 403 是网关拦截（查请求头/出口 IP），其余带打码片段原样报出。
func (c *foodTalksClient) statusError(what string, status int, body []byte) error {
	switch {
	case status == http.StatusUnauthorized:
		c.authHealth.record(false)
		return fmt.Errorf("%w（HTTP 401）", errFoodTalksUnauthorized)
	case status == http.StatusForbidden && json.Valid(body):
		c.authHealth.record(false)
		return fmt.Errorf("%w（HTTP 403）", errFoodTalksUnauthorized)
	case status == http.StatusForbidden:
		return fmt.Errorf("%w（HTTP 403，响应片段：%s）", errFoodTalksGatewayBlocked, c.snippet(body))
	default:
		return fmt.Errorf("FoodTalks %s异常（HTTP %d，响应片段：%s）", what, status, c.snippet(body))
	}
}

// snippet 截取响应前 200 字进错误信息（够定位 nginx 错误页/意外形态，不刷屏日志）。
// FoodTalks 鉴权失败会在 data 里原样回显所传 token（2026-09-28 生产实测），
// 所以先打码再截断——先截断会把切在边界上的半截 token 漏进日志。
func (c *foodTalksClient) snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if c.token != "" {
		s = strings.ReplaceAll(s, c.token, "[FOODTALKS_TOKEN]")
	}
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}
