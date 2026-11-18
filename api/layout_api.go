package main

// layout_api.go — 排版接口（/api/layout/v1/*）：给公司内部系统和同事（含他们的 AI 助手）用的程序化排版。
//
// 交一篇文章（链接、Word、HTML、Markdown、纯文本）和一个账号，拿回能直接粘进公众号后台或 FoodTalks 后台的成品 HTML。
// 只排版：不建发布单、不碰 site-sync/official-sync（红线 33），layoutAPI 里故意没有任何发布依赖。
//
// 每个请求都是内存里的一个操作（layout_operations.go）：POST 最多等 wait_seconds，排好了回 200，没排好回 202 和
// poll_url；GET poll_url 只读存好的结果，绝不重排。输入怎么变成交给排版程序（Node，LAYOUT_RENDER_URL）的载体
// 见 layout_source.go，飞书见 layout_feishu.go，图片转存与硬门见 layout_images.go，钥匙见 layout_callers.go，
// 错误码见 layout_errors.go，限额见 layout_limits.go，对外说明书由 layout_api_doc.go 从这些表生成。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---- 线上类型（只在这个文件解析与组装；doc 标签就是说明书和 OpenAPI 里的字段说明）----

// layoutRenderRequestJSON POST /api/layout/v1/render 的 JSON 请求体。url、html、markdown、text 正好给一个；
// Word 走 multipart/form-data，表单字段与这里同名，用 file 代替这四个。
type layoutRenderRequestJSON struct {
	Account     string `json:"account" doc:"按哪个账号排，必填。只能是「账号」表里的 id"`
	URL         string `json:"url,omitempty" doc:"公众号文章、普通网页或飞书云文档的链接，服务器自己认是哪种"`
	HTML        string `json:"html,omitempty" doc:"文章 HTML，整页或正文片段都行。图片要写公开的 https 绝对地址"`
	Markdown    string `json:"markdown,omitempty" doc:"Markdown 正文，按网页上传 .md 文件的规则排"`
	Text        string `json:"text,omitempty" doc:"纯文本，按网页上粘贴文字的规则排"`
	Title       string `json:"title,omitempty" doc:"标题。不填就用原文的（网页标题、Word 文件名、飞书文档名）；粘贴的 HTML、Markdown、文字没有单独的标题，不填就是空串"`
	Repost      *bool  `json:"repost,omitempty" doc:"true 按转载排，false 按原创排；不填按网页上同样操作的默认（见「能交什么」）。转载和原创的署名、页脚不一样"`
	WaitSeconds *int   `json:"wait_seconds,omitempty" doc:"POST 最多等几秒，0 到 100，默认 90。到点没排完回 202，再按 poll_url 取"`
}

// layoutResultJSON 排好了的 200。POST 直接回的和 GET poll_url 取到的逐字节一样。
type layoutResultJSON struct {
	Account    layoutAccountJSON   `json:"account" doc:"按哪个账号排的"`
	Title      string              `json:"title" doc:"文章标题，贴进后台时单独填。链接、Word、飞书文档的标题不在 html 里；粘贴的内容原样排，开头写的标题行会留在 html 里"`
	Summary    string              `json:"summary" doc:"摘要。FoodTalks 账号从原文开头的短导语取（取到时这段导语已从 html 里拿掉，和网页版一样）；没有就是空串"`
	HTML       string              `json:"html" doc:"成品：正文加页脚，样式全部写在标签上。粘进 account.paste_into 说的后台"`
	Repost     bool                `json:"repost" doc:"实际按转载（true）还是原创（false）排的"`
	Footer     bool                `json:"footer" doc:"html 里有没有页脚。纯文本里没有图片链接时只排正文，没有页脚"`
	Input      layoutInputJSON     `json:"input" doc:"服务器认出来的输入"`
	Images     []layoutImageJSON   `json:"images" doc:"html 里的每张图（去重，按出现顺序）和它在哪个图床"`
	Stats      layoutStatsJSON     `json:"stats" doc:"数量"`
	Rule       layoutRuleJSON      `json:"rule" doc:"用的哪一版排版规则：总是提交那一刻生效的版本"`
	Build      string              `json:"build" doc:"排版程序的版本（git 提交号）"`
	OutputHash string              `json:"output_hash" doc:"html 的 sha256（sha256: 开头）。两次结果一样，这个值就一样"`
	Warnings   []layoutWarningJSON `json:"warnings" doc:"排好了但要留意的事，没有就是空数组。种类见「提醒」"`
	RequestID  string              `json:"request_id" doc:"提交这篇时那次请求的编号，找人排查时报这个"`

	// check 校对要看的标题和正文纯文字，和网页上「AI 校对」卡送去查的逐字一样（同一篇查一次）。不出接口，智能体入口用。
	check articleCheckSource
}

// articleCheckSource 一篇送去校对的标题和正文纯文字。
type articleCheckSource struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type layoutAccountJSON struct {
	ID        string `json:"id" doc:"账号 id"`
	Name      string `json:"name" doc:"账号名"`
	PasteInto string `json:"paste_into" doc:"成品粘进哪个后台"`
}

type layoutInputJSON struct {
	Kind       string `json:"kind" doc:"认出来的输入种类：wechat_url、web_url、feishu_doc、word、html、markdown、text 之一"`
	URL        string `json:"url,omitempty" doc:"交的是链接时，那个链接"`
	FileName   string `json:"file_name,omitempty" doc:"交的是 Word 时的文件名"`
	SourceName string `json:"source_name,omitempty" doc:"抓到的来源：公众号名，或网页上写的来源"`
}

type layoutImageJSON struct {
	Src  string `json:"src" doc:"html 里这张图的地址"`
	Host string `json:"host" doc:"wechat：微信图床，只有公众号账号会留；fbif：FBIF 自己的图床（OSS、FoodTalks 图床 static.foodtalks.cn）"`
}

type layoutStatsJSON struct {
	Images         int `json:"images" doc:"正文图片数（不含页脚）"`
	ImagesMirrored int `json:"images_mirrored" doc:"这次转存到 OSS 的图片数（含 Word、飞书文档里的图和页脚图）"`
	Headings       int `json:"headings" doc:"小标题数"`
	HTMLBytes      int `json:"html_bytes" doc:"html 的字节数"`
}

type layoutRuleJSON struct {
	PresetID   string `json:"preset_id" doc:"规则编号"`
	Revision   int    `json:"revision" doc:"规则版本号"`
	ConfigHash string `json:"config_hash" doc:"规则内容的指纹"`
}

type layoutWarningJSON struct {
	Code    string `json:"code" doc:"提醒种类"`
	Message string `json:"message" doc:"给人看的说明"`
}

// layoutPendingJSON 还没排好的 202。
type layoutPendingJSON struct {
	Operation layoutOperationJSON `json:"operation" doc:"这篇在排的稿子"`
	RequestID string              `json:"request_id" doc:"这次请求的编号"`
}

type layoutOperationJSON struct {
	ID                string `json:"id" doc:"排版编号，op_ 开头"`
	State             string `json:"state" doc:"queued：排队等排版；running：正在抓取、转换、排版或转存图片"`
	PollURL           string `json:"poll_url" doc:"用同一把钥匙 GET 这个地址取结果，可带 ?wait_seconds=（最多 25）"`
	RetryAfterSeconds int    `json:"retry_after_seconds" doc:"建议隔几秒再取"`
}

type layoutErrorJSON struct {
	Error     layoutErrorBodyJSON `json:"error" doc:"出了什么错"`
	RequestID string              `json:"request_id" doc:"这次请求的编号，找人排查时报这个"`
}

type layoutErrorBodyJSON struct {
	Code              string `json:"code" doc:"错误种类，程序按它分支，见「出错」"`
	Message           string `json:"message" doc:"出了什么事，给人看"`
	Fix               string `json:"fix" doc:"该怎么办，给人看；AI 助手原话转告即可"`
	Retryable         bool   `json:"retryable" doc:"原样重发有没有可能成功"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty" doc:"建议等几秒再发；没有建议时不出现"`
}

// layoutWarningSpec Go 这边会加的提醒；排版程序（Node）回的提醒原样带出。
type layoutWarningSpec struct {
	Code    string
	Message string
}

const layoutWarningRenderNote = "render_note"

var layoutWarningSpecs = []layoutWarningSpec{
	{"reader_fallback", "这个网页直接抓不到正文，改用阅读模式取回的 Markdown 排的：小标题、加粗、图注可能不如直接抓准，请过目"},
	{layoutWarningRenderNote, "排版程序排的时候留意到的事，message 里写了具体是什么"},
}

func layoutWarning(code string) layoutWarningJSON {
	for _, spec := range layoutWarningSpecs {
		if spec.Code == code {
			return layoutWarningJSON(spec)
		}
	}
	panic("排版接口提醒不在 layoutWarningSpecs 表里：" + code)
}

// ---- 路由 ----

type layoutRoute struct {
	Method  string
	Path    string
	Auth    bool
	Summary string
}

// layoutAPIRoutes 说明书「接口一览」和 OpenAPI 读它；registerLayoutAPIRoutes 里的字面量和它由 TestLayoutAPIRoutesRegistered 对账。
var layoutAPIRoutes = []layoutRoute{
	{http.MethodPost, "/api/layout/v1/render", true, "交一篇文章，拿回排好的 HTML（或 202 + poll_url）"},
	{http.MethodGet, "/api/layout/v1/operations/{id}", true, "取还没排好的那篇（202 里的 poll_url）"},
	{http.MethodGet, "/api/layout/v1/docs", false, "这份说明书（Markdown），不用钥匙"},
	{http.MethodGet, "/api/layout/v1/openapi.json", false, "OpenAPI 3.1 描述，给程序和 AI 助手生成调用代码，不用钥匙"},
}

const layoutAPIPublicBase = "https://fbifmp-layout.foodtalks.cn"

func registerLayoutAPIRoutes(mux *http.ServeMux, api *layoutAPI) {
	// 路由写成字面量：tests/deploy-transaction.test.mjs 用正则从 mux.HandleFunc("…") 扫路由核对 Caddy 覆盖，
	// 表驱动注册它看不见。
	mux.HandleFunc("POST /api/layout/v1/render", api.handleRender)
	mux.HandleFunc("GET /api/layout/v1/operations/{id}", api.handleOperation)
	mux.HandleFunc("GET /api/layout/v1/docs", handleLayoutAPIDocs)
	mux.HandleFunc("GET /api/layout/v1/openapi.json", handleLayoutAPIOpenAPI)
}

// ---- 装配 ----

// layoutAPI 排版接口用到的东西。故意没有 publishPipeline、发布任务库：这条路建不了发布单（红线 33，TestLayoutNeverCreatesPublishJobs）。
type layoutAPI struct {
	callers  *layoutCallerDirectory
	rules    *rulePresetStore // 只读：按账号取图注长度阈值，和 /api/fetch-article 一样
	fetcher  layoutFetcher
	feishu   *layoutFeishu
	node     layoutRenderer
	images   layoutImageMirror
	limits   *layoutLimiter
	ops      *layoutOperationStore
	paused   func() bool
	now      func() time.Time
	nodeSlot chan struct{}
}

// newLayoutAPIFromEnv 生产装配：钥匙读中台配置包，抓取、Word、飞书、图片都用网页版已在用的那套函数。
func newLayoutAPIFromEnv(rules *rulePresetStore, auth *authService) *layoutAPI {
	api := newLayoutAPI(layoutAPIDeps{
		callers: newLayoutCallerDirectory(hubLayoutCallerSource{}, time.Now),
		rules:   rules,
		fetcher: articleLayoutFetcher{},
		feishu:  newLayoutFeishuFromEnv(auth),
		node:    newLayoutNodeClientFromEnv(),
		images:  newLayoutImageMirrorFromEnv(),
		paused:  publishWorkersPaused,
	})
	api.ops.startGC(time.Minute)
	return api
}

type layoutAPIDeps struct {
	callers *layoutCallerDirectory
	rules   *rulePresetStore
	fetcher layoutFetcher
	feishu  *layoutFeishu
	node    layoutRenderer
	images  layoutImageMirror
	paused  func() bool
	now     func() time.Time
}

func newLayoutAPI(deps layoutAPIDeps) *layoutAPI {
	now := deps.now
	if now == nil {
		now = time.Now
	}
	paused := deps.paused
	if paused == nil {
		paused = func() bool { return false }
	}
	return &layoutAPI{
		callers:  deps.callers,
		rules:    deps.rules,
		fetcher:  deps.fetcher,
		feishu:   deps.feishu,
		node:     deps.node,
		images:   deps.images,
		limits:   newLayoutLimiter(now),
		ops:      newLayoutOperationStore(now),
		paused:   paused,
		now:      now,
		nodeSlot: make(chan struct{}, layoutNodeConcurrent),
	}
}

// ---- 处理 ----

func newLayoutRequestID() string {
	raw := make([]byte, 9)
	if _, err := rand.Read(raw); err != nil {
		return "lr_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "lr_" + base64.RawURLEncoding.EncodeToString(raw)
}

// layoutRequestLog 一个请求一行日志：编号、调用方、账号、输入种类、结果、耗时、图片数、规则版本。不记正文。
type layoutRequestLog struct {
	RequestID string
	Caller    string
	Account   string
	Kind      string
	Status    int
	Code      string
	Started   time.Time
	Images    int
	Rule      string
}

func (l layoutRequestLog) write(now time.Time) {
	code := l.Code
	if code == "" {
		code = "ok"
	}
	log.Printf("layout-api: request_id=%s caller=%s account=%s input=%s status=%d code=%s ms=%d images=%d rule=%s",
		l.RequestID, firstNonEmpty(l.Caller, "-"), firstNonEmpty(l.Account, "-"), firstNonEmpty(l.Kind, "-"),
		l.Status, code, now.Sub(l.Started).Milliseconds(), l.Images, firstNonEmpty(l.Rule, "-"))
}

func (a *layoutAPI) reject(w http.ResponseWriter, entry layoutRequestLog, e *layoutError) {
	if e.cause != nil {
		log.Printf("layout-api: request_id=%s %v", entry.RequestID, e)
	}
	writeLayoutError(w, entry.RequestID, e)
	entry.Status, entry.Code = e.spec().Status, string(e.Code)
	entry.write(a.now())
}

func (a *layoutAPI) handleRender(w http.ResponseWriter, r *http.Request) {
	entry := layoutRequestLog{RequestID: newLayoutRequestID(), Started: a.now()}
	w.Header().Set("X-Request-ID", entry.RequestID)
	if a.paused() {
		a.reject(w, entry, layoutErr(layoutErrServiceRestarting))
		return
	}
	caller, lerr := a.callers.authenticate(bearerCredential(r))
	if lerr != nil {
		a.reject(w, entry, lerr)
		return
	}
	entry.Caller = caller.Code
	release, lerr := a.limits.acquireRender(caller.Code)
	if lerr != nil {
		a.reject(w, entry, lerr)
		return
	}
	req, lerr := parseLayoutRenderRequest(w, r)
	if lerr == nil && req.Kind == layoutInputWechatURL {
		lerr = a.limits.allowWechatFetch()
	}
	if lerr != nil {
		release()
		a.reject(w, entry, lerr)
		return
	}
	req.RequestID, req.Caller = entry.RequestID, caller
	entry.Account, entry.Kind = req.Account.ID, string(req.Kind)

	op, err := a.ops.create(caller.Code, entry.RequestID)
	if err != nil {
		release()
		a.reject(w, entry, layoutErr(layoutErrRenderFailed).withCause(err))
		return
	}
	a.launch(op, entry, req, release, func(result layoutResultJSON, lerr *layoutError) {
		a.ops.finish(op, layoutStoredFor(req.RequestID, result, lerr))
	})
	if !a.respond(w, r, op, req.Wait, entry.RequestID) {
		// 调用方在拿到编号之前就断开了：没人能取这篇，停掉省资源。
		op.cancel()
	}
}

// launch 后台排一篇，排完交给 deliver、记一行日志。排版接口（handleRender）和智能体入口（agent_mcp.go）共用这一段：
// 限额由调用方先拿到，release 在排完时放；所有输入最后都走 layout，所以成品只有一种排法。
func (a *layoutAPI) launch(op *layoutOperation, entry layoutRequestLog, req layoutRequest, release func(), deliver func(layoutResultJSON, *layoutError)) {
	ctx, cancel := context.WithTimeout(context.Background(), layoutOperationTimeout)
	op.cancel = cancel
	go func() {
		defer release()
		defer cancel()
		result, lerr := a.layout(ctx, op, req)
		done := entry
		done.Status = http.StatusOK
		if lerr != nil {
			if lerr.cause != nil {
				log.Printf("layout-api: request_id=%s %v", req.RequestID, lerr)
			}
			done.Status, done.Code = lerr.spec().Status, string(lerr.Code)
		} else {
			done.Images, done.Rule = len(result.Images), fmt.Sprintf("%s@%d", result.Rule.PresetID, result.Rule.Revision)
		}
		deliver(result, lerr)
		done.write(a.now())
	}()
}

func (a *layoutAPI) handleOperation(w http.ResponseWriter, r *http.Request) {
	entry := layoutRequestLog{RequestID: newLayoutRequestID(), Started: a.now(), Kind: "poll"}
	w.Header().Set("X-Request-ID", entry.RequestID)
	caller, lerr := a.callers.authenticate(bearerCredential(r))
	if lerr != nil {
		a.reject(w, entry, lerr)
		return
	}
	entry.Caller = caller.Code
	if lerr := a.limits.allowPoll(caller.Code); lerr != nil {
		a.reject(w, entry, lerr)
		return
	}
	wait, lerr := parseLayoutWait(r.URL.Query().Get("wait_seconds"), layoutPollDefaultWait, layoutPollMaxWait)
	if lerr != nil {
		a.reject(w, entry, lerr)
		return
	}
	op, lerr := a.ops.lookup(caller.Code, r.PathValue("id"))
	if lerr != nil {
		a.reject(w, entry, lerr)
		return
	}
	a.respond(w, r, op, wait, entry.RequestID)
}

// respond 最多等 wait：排好了回存好的那份（逐字节），没排好回 202。返回 false 表示调用方已经断开、什么都没写。
func (a *layoutAPI) respond(w http.ResponseWriter, r *http.Request, op *layoutOperation, wait time.Duration, requestID string) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-op.done:
		writeLayoutStored(w, op.result())
		return true
	case <-timer.C:
	case <-r.Context().Done():
		return false
	}
	// 等的这段时间里刚好排完，就直接给结果。
	select {
	case <-op.done:
		writeLayoutStored(w, op.result())
		return true
	default:
	}
	pending := layoutPendingJSON{
		Operation: layoutOperationJSON{
			ID:                op.id,
			State:             op.currentState(),
			PollURL:           layoutPollURL(op.id),
			RetryAfterSeconds: int(layoutPendingRetryAfter / time.Second),
		},
		RequestID: requestID,
	}
	w.Header().Set("Location", pending.Operation.PollURL)
	writeLayoutStored(w, layoutStoredResponse{
		Status:     http.StatusAccepted,
		Body:       layoutMarshal(pending),
		RetryAfter: pending.Operation.RetryAfterSeconds,
	})
	return true
}

func layoutPollURL(id string) string { return "/api/layout/v1/operations/" + id }

// layoutStoredFor 排好的或出的错，存成接口回给调用方的那份。
func layoutStoredFor(requestID string, result layoutResultJSON, lerr *layoutError) layoutStoredResponse {
	if lerr != nil {
		return layoutErrorResponse(requestID, lerr)
	}
	return layoutStoredResponse{Status: http.StatusOK, Body: layoutMarshal(result)}
}

func (a *layoutAPI) layout(ctx context.Context, op *layoutOperation, req layoutRequest) (layoutResultJSON, *layoutError) {
	prepared, lerr := a.prepare(ctx, op, req)
	if lerr != nil {
		return layoutResultJSON{}, layoutContextError(ctx, lerr)
	}
	out, lerr := a.renderOnNode(ctx, op, layoutNodeRequest{
		Account: req.Account.ID,
		Title:   req.Title,
		Repost:  req.Repost,
		Input:   prepared.Input,
	})
	if lerr != nil {
		return layoutResultJSON{}, layoutContextError(ctx, lerr)
	}
	policy := layoutImagePolicyFor(req.Account)
	html, mirrored, lerr := mirrorLayoutImages(ctx, out.HTML, policy, a.images)
	if lerr != nil {
		return layoutResultJSON{}, layoutContextError(ctx, lerr)
	}
	images, lerr := checkLayoutImages(html, policy)
	if lerr != nil {
		return layoutResultJSON{}, lerr
	}
	sum := sha256.Sum256([]byte(html))
	warnings := append(append([]layoutWarningJSON{}, prepared.Warnings...), normalizeLayoutNodeWarnings(out.Warnings)...)
	return layoutResultJSON{
		Account:    layoutAccountView(req.Account),
		Title:      firstNonEmpty(req.Title, strings.TrimSpace(out.Title), prepared.Input.Title),
		Summary:    out.Summary,
		HTML:       html,
		Repost:     out.Repost,
		Footer:     out.Footer,
		Input:      prepared.View,
		Images:     images,
		Stats:      layoutStatsJSON{Images: out.Stats.Images, ImagesMirrored: prepared.ImagesMirrored + mirrored, Headings: out.Stats.Headings, HTMLBytes: len(html)},
		Rule:       out.Rule,
		Build:      out.Build,
		OutputHash: "sha256:" + hex.EncodeToString(sum[:]),
		Warnings:   warnings,
		RequestID:  req.RequestID,
		check:      out.Check,
	}, nil
}

// renderOnNode Go 是唯一的队列：同时只送 layoutNodeConcurrent 篇给排版程序；排队到截止时间还没轮到的不送。
func (a *layoutAPI) renderOnNode(ctx context.Context, op *layoutOperation, req layoutNodeRequest) (layoutNodeResult, *layoutError) {
	if a.node == nil {
		return layoutNodeResult{}, newLayoutError(layoutErrRenderUnavailable, "排版程序没有接上")
	}
	op.setState(layoutStateQueued)
	select {
	case a.nodeSlot <- struct{}{}:
	case <-ctx.Done():
		return layoutNodeResult{}, layoutContextError(ctx, nil)
	}
	defer func() { <-a.nodeSlot }()
	if ctx.Err() != nil {
		return layoutNodeResult{}, layoutContextError(ctx, nil)
	}
	op.setState(layoutStateRunning)
	return a.node.Layout(ctx, req)
}

// layoutContextError 操作到了截止时间就统一说 timeout；调用方断开导致的取消也落成 timeout（这时没人看得到）。
func layoutContextError(ctx context.Context, lerr *layoutError) *layoutError {
	if ctx.Err() == nil && lerr != nil {
		return lerr
	}
	if ctx.Err() != nil {
		out := layoutErr(layoutErrTimeout)
		if lerr != nil {
			out.cause = lerr
		}
		return out
	}
	return layoutErr(layoutErrTimeout)
}

// ---- 请求解析 ----

// layoutRequest 解析完的请求，处理链只认它。
type layoutRequest struct {
	RequestID string
	Caller    layoutCaller
	Account   accountMeta
	Kind      layoutInputKind
	URL       string
	Feishu    feishuDocumentRef
	Body      string // html、markdown、text
	FileName  string
	File      []byte
	Title     string
	Repost    *bool
	Wait      time.Duration
	// FeishuAsPerson 智能体替人排飞书文档时取这个人的飞书令牌（先用它读，读不到再用应用身份）；排版接口不填。
	FeishuAsPerson func(context.Context) (string, error)
}

func parseLayoutWait(raw string, fallback, max time.Duration) (time.Duration, *layoutError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 || time.Duration(seconds)*time.Second > max {
		return 0, newLayoutError(layoutErrBadRequest, fmt.Sprintf("wait_seconds 要是 0 到 %d 的整数", int(max/time.Second)))
	}
	return time.Duration(seconds) * time.Second, nil
}

func layoutRequestTitle(raw string) (string, *layoutError) {
	title := strings.TrimSpace(raw)
	if len([]rune(title)) > layoutTitleMaxRunes {
		return "", newLayoutError(layoutErrBadRequest, fmt.Sprintf("title 不能超过 %d 字", layoutTitleMaxRunes))
	}
	return title, nil
}

func layoutAccountFor(id string) (accountMeta, *layoutError) {
	id = strings.TrimSpace(id)
	if id == "" {
		return accountMeta{}, newLayoutError(layoutErrBadRequest, "没写 account："+layoutAccountIDList())
	}
	meta, ok := accountMetaByID(id)
	if !ok {
		return accountMeta{}, newLayoutError(layoutErrUnknownAccount, "account 只能是 "+layoutAccountIDList()+"，这次给的是 "+strconv.Quote(id))
	}
	return meta, nil
}

func layoutAccountIDList() string {
	return strings.Join(catalogOrderIDs(), "、")
}

// ---- 账号在接口里的样子 ----

// layoutChannelSpec 一个渠道的成品贴到哪、图片怎么处理。按账号的 Channel（account_catalog.go）选，不看账号 id。
type layoutChannelSpec struct {
	Channel     string
	PasteInto   string
	AllowWechat bool   // 微信图床的图可以原样留（粘进公众号后台会自己转存；FoodTalks 页面有防盗链显示不出来）
	Images      string // 说明书「图片」一节
}

var layoutChannelSpecs = []layoutChannelSpec{
	{
		Channel: "mp", PasteInto: "微信公众号后台编辑器", AllowWechat: true,
		Images: "微信图床（mmbiz）和 FBIF 自己图床的图原样留，其余转存到 FBIF 的 OSS。粘进公众号后台编辑器时微信会把这些图转进自己的图床；" +
			"要是走微信草稿接口（draft/add）存稿，先用 uploadimg 把 host 是 fbif 的图换成微信图床地址，微信会过滤外部图片",
	},
	{
		Channel: "ft", PasteInto: "FoodTalks 后台编辑器", AllowWechat: false,
		Images: "FBIF 自己图床的图原样留，其余全部转存到 FBIF 的 OSS，微信图床的图也转：微信图床有防盗链，在 FoodTalks 页面上显示不出来",
	},
}

func layoutChannelFor(channel string) (layoutChannelSpec, bool) {
	for _, spec := range layoutChannelSpecs {
		if spec.Channel == channel {
			return spec, true
		}
	}
	return layoutChannelSpec{}, false
}

// layoutAccountName 账号在接口里的叫法：照账号表的名字，「网站」一律说成 FoodTalks（项目叫法裁定，2026-10-14）。
func layoutAccountName(meta accountMeta) string {
	return strings.Replace(meta.AccountName, "网站", "FoodTalks", 1)
}

func layoutAccountView(meta accountMeta) layoutAccountJSON {
	channel, _ := layoutChannelFor(meta.Channel)
	return layoutAccountJSON{ID: meta.ID, Name: layoutAccountName(meta), PasteInto: channel.PasteInto}
}

// ---- 不要钥匙的两个 ----

func handleLayoutAPIDocs(w http.ResponseWriter, _ *http.Request) {
	doc, _ := layoutAPIDocCached()
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(doc))
}

func handleLayoutAPIOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(layoutAPIOpenAPICached()))
}

// layoutRenderURL 新的排版程序进程（同一份 render-service 代码，RENDER_ROLE=layout）。旧 19096 一个字节不动。
func layoutRenderURL() string {
	if v := strings.TrimSpace(os.Getenv("LAYOUT_RENDER_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:19098"
}
