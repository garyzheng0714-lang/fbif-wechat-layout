package main

// publish_pipeline.go — 「公众号 → FoodTalks 网站」发布状态机（施工图 S2，
// WORKLOG-publish-pipeline-in-layout-2026-07-10.md；状态机语义照抄 FBIF资讯订阅仓
// internal/publishing/site.go，阶段执行进程内化——这是架构反转的最大红利）：
//
//	created → fetching（进程内抓文章存 blocks）→ rendering（render ft-default）
//	        → mirroring（图片批量转存 OSS + 重写）→ enriching（AI 预填发布参数，M5）
//	        → assessing（发布红线风险提示，失败降级 warning）
//	        → staging（CreateNews 草稿进站）
//	        → awaiting_review（人审闸门，自动推进到此为止）
//	        → [人 approve] → publicizing（已授权）→ publicizing_updating（已落 PUT intent）→ published
//	                                                        └→ publicize_reconciliation_required（结果不确定）
//	        → [人 reject]  → rejected（终态；文章留在 FoodTalks 草稿态，本就不可见）
//
// 🚨 用户红线（最高优先）：翻公开（UpdateNews publishStatus=1）是唯一不可逆动作
//（单向门 1↛0，契约 §3），只能由 review approve 端点触发；任何自动路径（site-sync、
// retry 与进程重启）都不得越过持久化审批版本；已进入 PUT 窗口的任务只能
// 人工对账——tests/publish_pipeline_test.go
// 的「无 approve 则 UpdateNews 永不被调」测试锁死这条。
//
// 依赖注入哲学：fetch/render/ossUpload 的默认实现**进程内复用**既有内核（不复制第二份，
// ADR 四层模型 L1）；FoodTalks 客户端与飞书通知每次现构造（凭证 env-only 运行期可变）；
// 测试注入 fake / httptest 桩。

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// sitePublishRulePackID 网站渠道默认排版规则包（registry 的 ft-default）。
const sitePublishRulePackID = "ft-default"

// publishRulePackContextKey 让抓取与渲染这两个既有函数签名继续保持稳定，
// 同时把一次提交选择的规则包沿着当前 worker 传下去。规则包本身会持久化在
// publishJob.RulePackID，context 只负责把它带到本次外部调用。
type publishRulePackContextKey struct{}

func normalizeSitePublishRulePackID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		id = sitePublishRulePackID
	}
	meta, ok := accountMetaByID(id)
	if !ok || meta.Channel != "ft" || meta.TemplateID != "foodtalks-site" {
		return "", fmt.Errorf("未知网站规则包 %q：请使用 account_catalog 中登记的 ft 规则包", id)
	}
	return id, nil
}

func withPublishRulePackID(ctx context.Context, rulePackID string) context.Context {
	return context.WithValue(ctx, publishRulePackContextKey{}, rulePackID)
}

func publishRulePackIDFromContext(ctx context.Context) string {
	if ctx != nil {
		if id, ok := ctx.Value(publishRulePackContextKey{}).(string); ok {
			if normalized, err := normalizeSitePublishRulePackID(id); err == nil {
				return normalized
			}
		}
	}
	return sitePublishRulePackID
}

func publishRulePackIDForJob(job publishJob) (string, error) {
	return normalizeSitePublishRulePackID(job.RulePackID)
}

// 可配项 env（MVP 先 env/常量，绝不散落硬编码，后续进管理后台）。
const (
	siteCategoryIDsEnv           = "FOODTALKS_CATEGORY_IDS"
	siteRepostCategoryIDsEnv     = "FOODTALKS_REPOST_CATEGORY_IDS"
	siteFrontURLEnv              = "FOODTALKS_FRONT_URL"
	siteOwnAccountsEnv           = "SITE_OWN_ACCOUNTS"
	siteAdminURLEnv              = "FOODTALKS_ADMIN_ARTICLE_URL" // readiness 用于校验 API/前台/后台目标环境一致
	defaultSiteCategoryIDs       = "52,58"                       // 原创：独家专栏（与 2026-07-10 冒烟一致；契约 §9：线上分类表待后端给全集）
	defaultSiteRepostCategoryIDs = "53"                          // 转载：食品新闻（2026-08-14 用户裁定 + 7 月实证，独家专栏是原创专属）
	defaultSiteFrontURL          = "https://test.foodtalks.cn"   // 默认测试环境前台；生产切换只改 env
	// 自家账号清单（原创判定，规范文档 §1）：近一月库内 source_type=0 的全部来源名。
	defaultSiteOwnAccounts = "FBIF食品饮料创新,FBIF食品饮料创新论坛,FoodTalks,FBIF食品创新展"
)

// 人审动作。
const (
	publishReviewApprove = "approve"
	publishReviewReject  = "reject"
	// 阻断级风险下的强制发布（2026-08-07 用户裁定：风险阻断只当警示不当锁）。
	// 持久化时归一为 approve + ReviewForced=true，publicize 侧凭 ReviewForced 放行。
	publishReviewForceApprove = "force_approve"
)

// errStagingOutcomeUncertain 表示 CreateNews 可能已被服务端执行，调用方绝不能把任务
// 降成普通 failed 后自动重试 POST。stageStage 会尽力落 reconciliation_required；若连
// 本地写也失败，则保留此前已持久化的 staging_creating intent，交给启动扫描转人工。
var errStagingOutcomeUncertain = errors.New("FoodTalks CreateNews 结果不确定")

// errPublicizeOutcomeUncertain 表示不可逆 PUT 已进入可能外调的窗口。此后不论
// 传输报错还是本地落盘报错，都不得用普通 failed/retry 盲重放。
var errPublicizeOutcomeUncertain = errors.New("FoodTalks UpdateNews 公开结果不确定")

// errPublishContentSuperseded 表示更高权威内容在当前阶段执行期间到达；旧 worker
// 只能放弃快照并从持久化的新版本继续，不能把它记成普通失败。
var errPublishContentSuperseded = errors.New("publish content version superseded")

var errPublishDuplicateSuperseded = errors.New("publish job superseded by canonical duplicate")

// foodTalksAPI FoodTalks 发布客户端能力面（*foodTalksClient 满足；测试可注入 fake）。
type foodTalksAPI interface {
	CreateNews(ctx context.Context, n foodTalksNews) (int64, error)
	UpdateNews(ctx context.Context, n foodTalksNews) error
	GetNews(ctx context.Context, newsID int64) (foodTalksNewsDetail, error)
	ListTagsByType(ctx context.Context, tagTypeID int64, keywords string) ([]foodTalksTag, error)
	CreateTagByName(ctx context.Context, tagTypeID int64, name string) (int64, error)
	SearchNewsByTitle(ctx context.Context, title string) ([]foodTalksNewsListItem, error)
	SearchCompanies(ctx context.Context, keyword string) ([]foodTalksCompany, error)
}

type publishResolvedRule struct {
	AccountID  string `json:"account_id"`
	PresetID   string `json:"preset_id"`
	Revision   int    `json:"revision"`
	ConfigHash string `json:"config_hash"`
	Source     string `json:"source"`
}

type publishRenderResult struct {
	HTML             string              `json:"html"`
	ResolvedRule     publishResolvedRule `json:"resolved_rule"`
	RendererBuildSHA string              `json:"renderer_build_sha"`
	OutputHash       string              `json:"output_hash"`
	Error            string              `json:"error,omitempty"`
}

func publishRenderOutputHash(html string) string {
	digest := sha256.Sum256([]byte(html))
	return fmt.Sprintf("sha256:%x", digest[:])
}

// publishPipeline 状态机执行器。函数字段的默认值在 newPublishPipeline 装配为
// 进程内复用实现，测试逐项替换。
type publishPipeline struct {
	store *publishJobStore
	rules *rulePresetStore
	// deliverySettings 是审核卡投递配置的服务端真相源。任务进入待审核时
	// 会把当前选择快照到 publishJob，后续改后台配置不会把旧卡切到别的群。
	deliverySettings *publishDeliverySettingsStore
	// publicDir 只用于 readiness 复核部署生成物；生产入口必须写入实际静态目录。
	publicDir string
	// workerPool 仅在生产入口显式启用。单元测试默认保留可注入的同步/异步
	// spawn，避免每个测试 pipeline 都创建常驻 goroutine。
	workerPool         *boundedWorkerPool
	externalWorkerPool *boundedWorkerPool
	// 网站生命周期与外部分发各有持久 outbox，但它们更新的是同一张飞书卡。
	// 单进程部署下统一串行发送，并在拿锁后重读任务，避免旧快照最后覆盖新状态。
	reviewCardUpdateMu sync.Mutex
	// 来源出处按名称检索（type=10）的进程内短缓存。测试可预置 sourceTags。
	sourceTagsMu     sync.Mutex
	sourceTags       []foodTalksTag
	sourceTagsLoaded time.Time
	sourceIDByName   map[string]int64
	sourceIDCacheAt  time.Time

	// fetch 公众号 URL → 标题 + 公众号名 + 结构化作者署名 + 页面规范 URL + 阅读原文链接 + 中立 Block[] JSON
	//（默认复用 fetchDirectBlocksWithOptions；byline 是发布者填的结构化字段，
	// 比 AI 从正文猜准确——AI 只在它为空时兜底，M7。originalSourceURL 是转载稿
	//「阅读原文」指向的原文地址，网站转载声明超链接优先用它）
	fetch func(ctx context.Context, articleURL string) (title, sourceName, byline, canonicalURL, originalSourceURL, blocksJSON string, err error)
	// render Block[] → 选定网站规则包内联 HTML（默认走 render_proxy 转发内核）。
	// 规则由渲染服务在每次请求内读取最新激活版本，调用方只传文章语义。
	render func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error)
	// ossUpload 整批图片 URL → OSS 转存（默认走 /api/oss-upload 的上游代理内核，urls 模式）
	ossUpload func(ctx context.Context, urls []string) (map[string]string, error)
	// enrich AI 预填发布参数（M5，默认 arkEnrichArticle）；nil / 失败都走规则兜底，
	// WARN 不 fail job——AI 是加速器不是闸门
	enrich func(ctx context.Context, job publishJob) (publishEnrichment, error)
	// assessRisk 发布前红线风险检查；失败降级 warning 并留痕，绝不把未检查当 pass，
	// 也绝不修改 ContentHTML。
	assessRisk func(ctx context.Context, job publishJob) (publishRiskAssessment, error)
	// foodTalks 每次任务执行现构造（凭证 env-only 运行期可变；缺配置的错误落到具体任务上）
	foodTalks func() (foodTalksAPI, error)
	// yunsuo 是原创文章外部分发客户端。它只在 FoodTalks 已发布卡片被人工点击后使用，
	// 与网站主状态机正交；测试可注入 fake，绝不在流水线自动审核路径调用。
	yunsuo func() (yunsuoAPI, error)
	// 云梭没有回调，content_pk 落盘后以安全查询轮询最终结果。
	externalDistributionPollInterval time.Duration
	externalDistributionPollTimeout  time.Duration
	cmsSyncInterval                  time.Duration
	repostSources                    *publishRepostSourceStore
	subscribeFeedWechatArticle       func(ctx context.Context, sourceName, articleURL string) error
	// notify 人审群通知；nil 或返回错误都不阻断流水线——人可以查任务列表，通知只是加速器
	notify func(ctx context.Context, text string) error
	// monitorNotify P0 监控私聊；与审核群物理分开，缺配置时监控 relay 必须失败，
	// 绝不能回退到 notify 把异常、恢复或心跳发进审核群。
	monitorNotify func(ctx context.Context, text string) error
	// notifyCard 审核交互卡片发送（M4）；nil 或失败降级回 notify 文本，同样不阻断流水线
	notifyCard func(ctx context.Context, card map[string]any) error
	// notifyCardWithID 与 notifyCard 相同，但返回飞书 message_id，供同一张审核卡后续原位更新。
	// 旧注入点保留作兼容回退；生产环境优先使用带 ID 的发送器。
	notifyCardWithID func(ctx context.Context, card map[string]any) (string, error)
	// updateCard 更新当前应用发送的飞书交互卡；没有已持久化 message_id 时不调用。
	updateCard func(ctx context.Context, messageID string, card map[string]any) error
	// recallCard 撤回当前应用发送的消息；仅 card-refresh 的 resend 路径使用。
	recallCard func(ctx context.Context, messageID string) error
	// coverImgKey 封面 URL → 飞书图片素材 img_key（卡片内呈现封面用）；nil 或失败
	// 降级为卡片不带图，同样不阻断流水线
	coverImgKey func(ctx context.Context, imgURL string) (string, error)
	// spawn 异步推进入口（生产 go routine；测试注入同步执行保证确定性）
	spawn func(func())
	// externalSpawn 将云梭的慢 HTTP 与 FoodTalks 主发布 worker 物理隔离；否则四个
	// 30 秒请求就能占满主池，使审核发布和卡片通知一起饥饿。
	externalSpawn func(func())
	// retryNotification 失败通知的延迟重试调度；测试可注入捕获回调。
	retryNotification func(time.Duration, func())
	now               func() time.Time
}

// enableBoundedWorkers 把所有发布任务和通知发送收敛到固定数量的 worker。
// 队列满时 submit 会形成背压；任务本身已经持久化，进程退出期间未执行的任务
// 会在下一次启动时由 recoverJobs 恢复。
func (p *publishPipeline) enableBoundedWorkers(workerCount, queueCapacity int) {
	pool := newBoundedWorkerPool(workerCount, queueCapacity)
	externalPool := newBoundedWorkerPool(2, 32)
	p.workerPool = pool
	p.externalWorkerPool = externalPool
	p.spawn = func(f func()) {
		if !pool.submit(f) {
			log.Printf("publish: worker pool 已停止，拒绝新的异步任务")
		}
	}
	p.externalSpawn = func(f func()) {
		if !externalPool.submit(f) {
			log.Printf("publish: external worker pool 已停止，拒绝新的云梭任务")
		}
	}
}

func (p *publishPipeline) shutdownWorkers(ctx context.Context) error {
	if p.externalWorkerPool != nil {
		if err := p.externalWorkerPool.shutdown(ctx); err != nil {
			return err
		}
	}
	if p.workerPool != nil {
		return p.workerPool.shutdown(ctx)
	}
	return nil
}

func (p *publishPipeline) spawnExternalWork(f func()) {
	if p.externalSpawn != nil {
		p.externalSpawn(f)
		return
	}
	// 单测沿用可注入的同步 spawn；未启用 bounded workers 的开发模式也保持异步。
	p.spawn(f)
}

// newPublishPipeline 生产装配：全部依赖用进程内复用的默认实现。
func newPublishPipeline(store *publishJobStore, rules *rulePresetStore) *publishPipeline {
	p := &publishPipeline{
		store:                            store,
		rules:                            rules,
		render:                           renderSiteHTML,
		ossUpload:                        ossUploadByURLs,
		enrich:                           arkEnrichArticle,
		assessRisk:                       arkAssessPublishRisk,
		foodTalks:                        func() (foodTalksAPI, error) { return newFoodTalksClientFromEnv() },
		yunsuo:                           func() (yunsuoAPI, error) { return newYunsuoClientFromEnv() },
		subscribeFeedWechatArticle:       defaultSubscribeFeedWechatArticle,
		externalDistributionPollInterval: 5 * time.Second,
		externalDistributionPollTimeout:  15 * time.Minute,
		spawn:                            func(f func()) { go f() },
		externalSpawn:                    func(f func()) { go f() },
		retryNotification:                func(delay time.Duration, f func()) { time.AfterFunc(delay, f) },
		now:                              time.Now,
	}
	if store != nil {
		p.repostSources = newPublishRepostSourceStore(publishRepostSourcesPath(store.path))
	}
	p.fetch = func(ctx context.Context, articleURL string) (string, string, string, string, string, string, error) {
		return fetchSiteArticleWithContext(ctx, rules, articleURL)
	}
	if n := newFeishuNotifierFromEnv(); n != nil {
		p.notify = n.send
		p.notifyCard = n.sendCard
		p.notifyCardWithID = n.sendCardWithID
		p.updateCard = n.updateCard
		p.recallCard = n.recallMessage
		p.coverImgKey = n.uploadImageByURL
	} else {
		log.Printf("publish: 飞书通知未配置（需 FEISHU_APP_ID / FEISHU_APP_SECRET / FEISHU_REVIEW_CHAT_ID），审核群通知禁用")
	}
	if n := newFeishuMonitorNotifierFromEnv(); n != nil {
		p.monitorNotify = n.send
	} else {
		log.Printf("publish: 监控私聊未配置（需 FEISHU_APP_ID / FEISHU_APP_SECRET / %s），监控 relay 禁用", publishMonitorFeishuUserIDEnv)
	}
	return p
}

// ---- source_key 归一化（幂等键，语义照抄资讯订阅仓 NormalizeSiteSourceKey）----

// normalizeSiteSourceKey 公众号文章 URL → 幂等键。
// 规范化：强制 https + 小写 host、去 fragment；长链身份只用 __biz/mid/idx。
// sn/chksm/scene 都不参与身份：同一篇文章的签名/分享上下文可变，保留会让同文重复建单；
// /s/<slug> 短链本身就是稳定定位，去全部 query。返回形如 site:https://mp.weixin.qq.com/...。
func normalizeSiteSourceKey(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("链接无法解析: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("链接必须以 http:// 或 https:// 开头")
	}
	host := strings.ToLower(u.Hostname())
	if host != "mp.weixin.qq.com" {
		return "", errors.New("仅支持微信公众号文章链接（mp.weixin.qq.com）")
	}
	if u.User != nil || u.Port() != "" {
		return "", errors.New("公众号文章链接不得包含用户信息或自定义端口")
	}
	u.Scheme = "https"
	u.Host = host
	u.Fragment = ""
	path := strings.TrimRight(u.Path, "/")
	// /s/<slug> 的 path 已是文章稳定身份：query（哪怕长得像 __biz/mid）都可能是
	// 分享上下文，必须全丢；尾斜杠同样归一。slug 大小写保持原样。
	if strings.HasPrefix(path, "/s/") && len(path) > len("/s/") && !strings.Contains(strings.TrimPrefix(path, "/s/"), "/") {
		u.Path = path
		u.RawPath = ""
		u.RawQuery = ""
		return "site:" + u.String(), nil
	}
	u.Path = path
	u.RawPath = ""
	if u.Path != "/s" {
		return "", errors.New("仅支持微信公众号文章路径 /s 或 /s/<slug>")
	}
	q := u.Query()
	for _, required := range []string{"__biz", "mid", "idx"} {
		if strings.TrimSpace(q.Get(required)) == "" {
			return "", errors.New("长链缺少文章身份参数 __biz/mid/idx，请从公众号文章页复制完整链接")
		}
	}
	kept := url.Values{}
	for _, k := range []string{"__biz", "mid", "idx"} {
		kept.Set(k, q.Get(k))
	}
	u.RawQuery = kept.Encode() // Encode 按 key 排序，同文不同参数顺序也归一
	return "site:" + u.String(), nil
}

// ---- 默认阶段实现（进程内复用既有内核，不复制第二份）----

// fetchSiteArticleWithContext 进程内复用 main.go 的抓取主路径
// fetchDirectBlocksWithOptions（含限流重试）；x-reader Markdown 回退喂不进渲染器，
// 此处按失败处理（fail loud）。规则包从当前 worker context 进入。
func fetchSiteArticleWithContext(ctx context.Context, rules *rulePresetStore, articleURL string) (string, string, string, string, string, string, error) {
	rulePackID := publishRulePackIDFromContext(ctx)
	title, blocks, sourceName, byline, canonicalURL, originalSourceURL, err := fetchDirectBlocksWithOptions(articleURL, articleBlockOptionsForAccount(rules, rulePackID))
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("文章抓取失败: %w", err)
	}
	if len(blocks) == 0 {
		return "", "", "", "", "", "", errors.New("未能从文章解析出内容块（链接可能已过期或被限流，请稍后重试）")
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("序列化内容块失败: %w", err)
	}
	return title, sourceName, byline, canonicalURL, originalSourceURL, string(raw), nil
}

// renderSiteHTML 进程内复用 render_proxy.go 的转发内核；自动发布强制 latest 模式，
// 由 Node 在每次请求内读取选定规则包的最新激活规则，不接受任务内旧快照。
func renderSiteHTML(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
	return renderSiteHTMLForPack(ctx, publishRulePackIDFromContext(ctx), title, source, blocksJSON, isRepost)
}

func renderSiteHTMLForPack(ctx context.Context, rulePackID, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
	rulePackID, err := normalizeSitePublishRulePackID(rulePackID)
	if err != nil {
		return publishRenderResult{}, err
	}
	token := renderToken()
	if token == "" {
		return publishRenderResult{}, errors.New("RENDER_TOKEN 未配置：无法调用渲染服务")
	}
	payload, err := json.Marshal(map[string]any{
		"title":    title,
		"source":   source,
		"blocks":   json.RawMessage(blocksJSON),
		"isRepost": isRepost,
		"packId":   rulePackID,
		"ruleMode": "latest",
	})
	if err != nil {
		return publishRenderResult{}, fmt.Errorf("序列化渲染请求失败: %w", err)
	}
	req, err := newRenderServiceRequest(ctx, payload, "Bearer "+token)
	if err != nil {
		return publishRenderResult{}, err
	}
	resp, err := renderServiceHTTPClient().Do(req)
	if err != nil {
		return publishRenderResult{}, fmt.Errorf("渲染服务未就绪（render-service 未启动或不可达）: %w", err)
	}
	defer resp.Body.Close()
	body, err := readBoundedBody(resp.Body, 16<<20)
	if err != nil {
		return publishRenderResult{}, fmt.Errorf("读渲染服务响应失败: %w", err)
	}
	var out publishRenderResult
	if err := json.Unmarshal(body, &out); err != nil {
		return publishRenderResult{}, fmt.Errorf("渲染服务响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return publishRenderResult{}, fmt.Errorf("渲染服务错误（HTTP %d）: %s", resp.StatusCode, out.Error)
		}
		return publishRenderResult{}, fmt.Errorf("渲染服务错误（HTTP %d）", resp.StatusCode)
	}
	if strings.TrimSpace(out.HTML) == "" {
		return publishRenderResult{}, errors.New("渲染服务返回空 HTML")
	}
	return out, nil
}

// ossUploadByURLs 进程内复用 handleImageUpload 的上游代理内核（urls 模式：上游服务器
// 内网并发「取图+传 OSS」）。返回 {原url: oss_url}；上游取图失败的 URL 不在响应里，
// 缺失项的重试与 fail loud 由 stageMirror 负责。
func ossUploadByURLs(ctx context.Context, urls []string) (map[string]string, error) {
	return ossUploadPayload(ctx, map[string]any{"urls": urls})
}

func ossUploadByBase64Images(ctx context.Context, images map[string]string) (map[string]string, error) {
	return ossUploadPayload(ctx, map[string]any{"base64_images": images})
}

func ossUploadPayload(ctx context.Context, payload map[string]any) (map[string]string, error) {
	endpoint := strings.TrimSpace(os.Getenv("WECHAT_UPLOAD_ENDPOINT"))
	if endpoint == "" {
		return nil, errors.New("WECHAT_UPLOAD_ENDPOINT 未配置：图片转存不可用")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化转存请求失败: %w", err)
	}
	origin := strings.TrimRight(strings.TrimSpace(os.Getenv("WECHAT_UPLOAD_ORIGIN")), "/")
	referer := ""
	if origin != "" {
		referer = origin + "/"
	}
	req, err := newUploadProxyRequest(ctx, endpoint, "/api/oss-upload", "application/json", origin, referer, body)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("图片转存上游不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 8<<20)
	if err != nil {
		return nil, fmt.Errorf("读转存响应失败: %w", err)
	}
	var out struct {
		Results map[string]string `json:"results"`
		Error   string            `json:"error"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("转存上游响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return nil, fmt.Errorf("图片转存失败（HTTP %d）: %s", resp.StatusCode, out.Error)
		}
		return nil, fmt.Errorf("图片转存失败（HTTP %d）", resp.StatusCode)
	}
	if out.Results == nil {
		out.Results = map[string]string{}
	}
	return out.Results, nil
}

// ---- 状态机推进 ----

// runJob 从当前阶段推进任务直至 awaiting_review / 终态 / failed。一般阶段出错落
// failed + failed_stage 供 retry；CreateNews 和公开 PUT 一旦进入持久 intent 窗口，结果
// 不确定时必须进 reconciliation，绝不走普通 retry。
func (p *publishPipeline) runJob(jobID int64) {
	ctx := context.Background()
	for {
		job, err := p.store.byID(jobID)
		if err != nil {
			log.Printf("publish: load job %d failed: %v", jobID, err)
			return
		}
		if job.PendingContentHash != "" {
			promoted, changed, promoteErr := p.promotePendingContent(jobID)
			if promoteErr != nil {
				log.Printf("publish: promote pending content for job %d failed: %v", jobID, promoteErr)
				return
			}
			if changed {
				job = promoted
			}
		}
		if job.terminal() || job.Stage == publishStageAwaitingReview || job.Stage == publishStageFailed ||
			job.Stage == publishStageStagingCreating || job.Stage == publishStageReconciliationRequired ||
			job.Stage == publishStagePublicizingUpdating || job.Stage == publishStagePublicizeReconciliationRequired ||
			job.Stage == publishStageUnpublishingUpdating || job.Stage == publishStageUnpublishReconciliationRequired ||
			job.Stage == publishStageRepublishingUpdating || job.Stage == publishStageRepublishReconciliationRequired {
			return
		}
		var stageErr error
		switch job.Stage {
		case publishStageCreated:
			_, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
				return current.Stage == publishStageCreated && current.ContentVersion == job.ContentVersion && current.PendingContentHash == ""
			}, func(current *publishJob) {
				current.Stage = publishStageFetching
				current.FailedStage = ""
				current.Error = ""
			})
			stageErr = err
			if err == nil && !swapped {
				stageErr = errPublishContentSuperseded
			}
		case publishStageFetching:
			stageErr = p.stageFetch(ctx, job)
		case publishStageRendering:
			stageErr = p.stageRender(ctx, job)
		case publishStageMirroring:
			stageErr = p.stageMirror(ctx, job)
		case publishStageEnriching:
			stageErr = p.stageEnrich(ctx, job)
		case publishStageAssessing, publishStageAssessingReview:
			stageErr = p.stageAssessRisk(ctx, job)
		case publishStageStaging:
			stageErr = p.stageStage(ctx, job)
		case publishStagePublicizing:
			stageErr = p.stagePublicize(ctx, job)
		case publishStageUnpublishing:
			stageErr = p.stageUnpublish(ctx, job)
		case publishStageRepublishing:
			stageErr = p.stageRepublish(ctx, job)
		default:
			stageErr = fmt.Errorf("未知任务阶段 %q", job.Stage)
		}
		if stageErr != nil {
			if errors.Is(stageErr, errPublishContentSuperseded) {
				continue
			}
			if errors.Is(stageErr, errPublishDuplicateSuperseded) {
				return
			}
			if latest, loadErr := p.store.byID(jobID); loadErr == nil && latest.PendingContentHash != "" && publishContentPromotableStage(latest) {
				continue
			}
			if errors.Is(stageErr, errStagingOutcomeUncertain) || errors.Is(stageErr, errPublicizeOutcomeUncertain) ||
				errors.Is(stageErr, errUnpublishOutcomeUncertain) || errors.Is(stageErr, errRepublishOutcomeUncertain) {
				// 已经进入持久 intent 窗口；Create 盲重放会建重文，
				// publicize 盲重放则会掩盖不可逆动作的真实结果。
				log.Printf("publish: job %d stage %s outcome uncertain: %v", jobID, job.Stage, stageErr)
				return
			}
			log.Printf("publish: job %d stage %s failed: %v", jobID, job.Stage, stageErr)
			if job.Stage == publishStagePublicizing {
				failed, swapped, persistErr := p.store.updateIf(jobID, func(current publishJob) bool {
					return current.Stage == publishStagePublicizing && current.PublicizeIntentKey == ""
				}, func(j *publishJob) {
					j.Stage = publishStageFailed
					j.FailedStage = publishStagePublicizing
					j.Error = stageErr.Error()
					enqueuePublishNotification(j, publishNotificationPublicizeFailed)
				})
				if persistErr != nil {
					log.Printf("publish: record job %d publicize failure failed: %v", jobID, persistErr)
					return
				}
				if !swapped {
					// 另一 worker 已经认领 PUT intent；它负责最终结果，
					// 本 worker 不得覆写其 publicizing_updating/reconciliation 状态。
					return
				}
				if failed.PendingContentHash != "" && publishContentPromotableStage(failed) {
					continue
				}
				p.schedulePublishNotification(failed.ID)
				return
			}
			if job.Stage == publishStageUnpublishing || job.Stage == publishStageRepublishing {
				kind := publishNotificationUnpublishFailed
				if job.Stage == publishStageRepublishing {
					kind = publishNotificationRepublishFailed
				}
				failed, swapped, persistErr := p.store.updateIf(jobID, func(current publishJob) bool {
					return current.Stage == job.Stage &&
						current.UnpublishIntentKey == "" && current.RepublishIntentKey == ""
				}, func(j *publishJob) {
					j.Stage = publishStageFailed
					j.FailedStage = job.Stage
					j.Error = stageErr.Error()
					enqueuePublishNotification(j, kind)
				})
				if persistErr != nil {
					log.Printf("publish: record job %d lifecycle failure failed: %v", jobID, persistErr)
					return
				}
				if !swapped {
					return
				}
				p.schedulePublishNotification(failed.ID)
				return
			}
			failed, err := p.store.setFailed(jobID, job.Stage, stageErr.Error())
			if err != nil {
				log.Printf("publish: record job %d failure failed: %v", jobID, err)
			}
			if err == nil && failed.PendingContentHash != "" && publishContentPromotableStage(failed) {
				continue
			}
			return
		}
	}
}

// stageFetch 抓公众号文章 → 标题 + 公众号名 + 结构化作者署名 + Block[] 落库。
// blocks 必须先落库再推进：公众号临时链接（含 chksm）会过期，后续阶段失败重试不再回源。
func (p *publishPipeline) stageFetch(ctx context.Context, job publishJob) error {
	rulePackID, err := publishRulePackIDForJob(job)
	if err != nil {
		return err
	}
	title, sourceName, byline, canonicalURL, originalSourceURL, blocksJSON, err := p.fetch(withPublishRulePackID(ctx, rulePackID), job.SourceURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(title) == "" {
		title = job.Title // 抓取无标题时保留任务上已有标题（人工补填路径）
	}
	if strings.TrimSpace(canonicalURL) != "" {
		canonicalKey, keyErr := normalizeSiteSourceKey(canonicalURL)
		if keyErr == nil {
			_, conflictingID, claimErr := p.store.claimSourceAlias(job.ID, canonicalKey)
			if claimErr != nil {
				return fmt.Errorf("持久化文章规范身份失败: %w", claimErr)
			}
			if conflictingID > 0 {
				_, swapped, markErr := p.store.updateIf(job.ID, func(current publishJob) bool {
					return current.Stage == publishStageFetching && current.FoodTalksNewsID == 0
				}, func(current *publishJob) {
					current.Stage = publishStageSupersededDuplicate
					current.SupersededByJobID = conflictingID
					current.Error = fmt.Sprintf("同一篇公众号文章已由发布任务 #%d 处理", conflictingID)
					current.FailedStage = ""
				})
				if markErr != nil {
					return fmt.Errorf("标记重复任务失败: %w", markErr)
				}
				if !swapped {
					return errPublishContentSuperseded
				}
				return fmt.Errorf("%w: winner job #%d", errPublishDuplicateSuperseded, conflictingID)
			}
		}
	}
	if strings.TrimSpace(title) == "" {
		return errors.New("未能从文章解析出标题：请确认链接是公众号文章页，或稍后重试")
	}
	seed := newPublishContentSeed(publishContentWebFetch, title, sourceName, byline, blocksJSON, job.CoverURL)
	_, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageFetching && current.ContentVersion == job.ContentVersion &&
			current.PendingContentHash == "" && publishContentRank(current.ContentSource) <= publishContentRank(seed.Source)
	}, func(current *publishJob) {
		applyPublishContentSeed(current, seed)
		current.OriginalSourceURL = preferOriginalSourceURL(originalSourceURL, current.OriginalSourceURL)
	})
	if err == nil && !swapped {
		return errPublishContentSuperseded
	}
	return err
}

var publishRuleConfigHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func validatePublishRenderResult(result publishRenderResult) error {
	return validatePublishRenderResultForPack(result, sitePublishRulePackID)
}

func validatePublishRenderResultForPack(result publishRenderResult, rulePackID string) error {
	if strings.TrimSpace(result.HTML) == "" {
		return errors.New("渲染服务返回空 HTML")
	}
	meta, ok := accountMetaByID(rulePackID)
	if !ok {
		return fmt.Errorf("未知网站规则包 %q", rulePackID)
	}
	resolved := result.ResolvedRule
	if resolved.AccountID != meta.ID || strings.TrimSpace(resolved.PresetID) == "" ||
		resolved.Revision < 1 || !publishRuleConfigHashPattern.MatchString(resolved.ConfigHash) || resolved.Source != "server" {
		return fmt.Errorf("渲染服务没有返回规则包 %q 本次请求实际使用的最新激活规则", rulePackID)
	}
	if strings.TrimSpace(result.RendererBuildSHA) == "" {
		return errors.New("渲染服务未返回 renderer build SHA")
	}
	wantOutputHash := publishRenderOutputHash(result.HTML)
	if result.OutputHash != wantOutputHash {
		return fmt.Errorf("渲染输出指纹不匹配：want %s, got %s", wantOutputHash, result.OutputHash)
	}
	return nil
}

// publishRenderSourceURL 渲染用的「原文链接」（转载声明《标题》超链接）。
// 转载稿优先用公众号「阅读原文」；自家号转发且阅读原文为空时不回退提交链接
// （那是自家转载页，不是出处原文——2026-08-18 猪油渣稿）。外号原文或自家原创
// 仍用提交链接。
func publishRenderSourceURL(job publishJob) string {
	original, _ := publishArticleProvenance(job)
	if !original {
		if u := strings.TrimSpace(job.OriginalSourceURL); isHTTPURL(u) {
			return u
		}
		name := strings.TrimSpace(job.SourceName)
		if name != "" && !siteOwnAccounts()[name] {
			return job.SourceURL
		}
		return ""
	}
	return job.SourceURL
}

// stageRender blocks → 选定网站规则包内联 HTML（与网站人工排版同源）。
// 每次调用 renderer 都读取最新激活规则；失败重试同样追随重试时的最新规则。
func (p *publishPipeline) stageRender(ctx context.Context, job publishJob) error {
	if strings.TrimSpace(job.BlocksJSON) == "" {
		return errors.New("任务缺少已抓取的内容块（blocks），无法排版；请重试从抓取阶段续跑")
	}
	rulePackID, err := publishRulePackIDForJob(job)
	if err != nil {
		return err
	}
	// 网站排版一律按转载切尾，与编辑器 URL 导入一致。publishSiteIsRepost 只决定
	// CMS 来源类型（原创/转载）和提示文案，不得再关掉切尾。
	p.ensureRepostOriginalURL(ctx, &job)
	result, err := p.render(withPublishRulePackID(ctx, rulePackID), job.Title, publishRenderSourceURL(job), job.BlocksJSON, true)
	if err != nil {
		return err
	}
	if err := validatePublishRenderResultForPack(result, rulePackID); err != nil {
		return err
	}
	if leak := publishHTMLWechatTrailerLeak(result.HTML); leak != "" {
		return fmt.Errorf("排版结果仍含公众号导流尾（%s），禁止发布到网站", leak)
	}
	_, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageRendering && current.ContentVersion == job.ContentVersion && current.PendingContentHash == ""
	}, func(j *publishJob) {
		j.ContentHTML = result.HTML
		j.RulePresetID = result.ResolvedRule.PresetID
		j.RuleRevision = result.ResolvedRule.Revision
		j.RuleConfigHash = result.ResolvedRule.ConfigHash
		j.RuleSnapshotJSON = ""
		j.RendererBuildSHA = result.RendererBuildSHA
		j.RenderOutputHash = result.OutputHash
		j.Stage = publishStageMirroring
		j.FailedStage = ""
		j.Error = ""
	})
	if err == nil && !swapped {
		return errPublishContentSuperseded
	}
	return err
}

// stageMirror 图片整批转存 OSS + 重写 src：一次整批 + 缺失单次重试，任一图仍失败即
// fail loud 列出 URL——mmbiz 图有防盗链，外链裸奔进站等于发布后丢图，不允许半成品继续
// 推进。封面同通道：无封面用正文第一张图兜底，再无则显式失败给出路。
func (p *publishPipeline) stageMirror(ctx context.Context, job publishJob) error {
	imageMap := map[string]string{}
	if strings.TrimSpace(job.ImageMap) != "" {
		if err := json.Unmarshal([]byte(job.ImageMap), &imageMap); err != nil {
			imageMap = map[string]string{}
		}
	}
	srcs := extractImgSrcs(job.ContentHTML)
	cover := strings.TrimSpace(job.CoverURL)
	if cover == "" || strings.HasPrefix(cover, "data:") {
		for _, src := range srcs {
			if !strings.HasPrefix(src, "data:") {
				cover = src
				break
			}
		}
	}
	if cover == "" {
		return errors.New("文章无可用封面图：正文没有图片且任务未设置封面，请补充封面后重试")
	}

	// 待转存集合 = 正文外链图 + 封面；跳过 data: 内联图与已有锚点的（重试不重传）。
	var pending []string
	seen := map[string]bool{}
	for _, src := range append(append([]string{}, srcs...), cover) {
		if src == "" || strings.HasPrefix(src, "data:") || seen[src] {
			continue
		}
		if _, done := imageMap[src]; done {
			continue
		}
		seen[src] = true
		pending = append(pending, src)
	}

	// 一次整批（上游服务器内网并发）；响应缺失的 URL 重试一批一次；仍缺 → 显式失败。
	for attempt := 0; len(pending) > 0 && attempt < 2; attempt++ {
		results, err := p.ossUpload(ctx, pending)
		if err != nil {
			return fmt.Errorf("图片批量转存失败: %w", err)
		}
		var missing []string
		for _, src := range pending {
			if oss := strings.TrimSpace(results[src]); oss != "" {
				imageMap[src] = oss
			} else {
				missing = append(missing, src)
			}
		}
		// 成功的先落库（幂等锚点：即使后续失败，重试也不重传这批）。
		mapJSON, _ := json.Marshal(imageMap)
		_, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.Stage == publishStageMirroring && current.ContentVersion == job.ContentVersion && current.PendingContentHash == ""
		}, func(j *publishJob) { j.ImageMap = string(mapJSON) })
		if err != nil {
			return err
		}
		if !swapped {
			return errPublishContentSuperseded
		}
		pending = missing
	}
	if len(pending) > 0 {
		return fmt.Errorf("以下图片重试后仍转存失败，不允许外链图裸奔进站：%s", strings.Join(pending, "、"))
	}

	finalHTML := rewriteImgSrcs(job.ContentHTML, imageMap)
	ossCover := imageMap[cover]
	if ossCover == "" {
		return fmt.Errorf("封面图转存结果缺失（%s）", cover)
	}
	_, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageMirroring && current.ContentVersion == job.ContentVersion && current.PendingContentHash == ""
	}, func(j *publishJob) {
		j.ContentHTML = finalHTML
		j.CoverURL = ossCover
		j.Stage = publishStageEnriching
		j.FailedStage = ""
		j.Error = ""
	})
	if err == nil && !swapped {
		return errPublishContentSuperseded
	}
	return err
}

// stageEnrich AI 预填发布参数（M5）：产元数据（summary/tags/author/ai_notes +
// 可能覆盖 cover_url），并按标签把公司库链接写入 ContentHTML，预览与草稿一致。
// AI 未装配 / 调用失败 / 解析失败 →
// fallbackEnrichment 规则兜底 + WARN，阶段照常推进——AI 是加速器不是闸门，
// 本函数只会因存储写失败而让任务落 failed。
func (p *publishPipeline) stageEnrich(ctx context.Context, job publishJob) error {
	var result publishEnrichment
	var err error
	if p.enrich == nil {
		err = errors.New("AI 预填未装配")
	} else {
		result, err = p.enrich(ctx, job)
	}
	if err != nil {
		log.Printf("publish: WARN job %d AI 预填失败，走规则兜底: %v", job.ID, err)
		result = fallbackEnrichment(job, err)
	}
	tagged := job
	tagged.Tags = result.Tags
	linkedHTML, _ := p.applySiteCompanyLinks(ctx, tagged)
	_, swapped, uerr := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageEnriching && current.ContentVersion == job.ContentVersion && current.PendingContentHash == ""
	}, func(j *publishJob) {
		if result.CoverURL != "" {
			j.CoverURL = result.CoverURL // AI 从候选里选中的封面覆盖默认第一图
		}
		j.Summary = result.Summary
		j.Tags = result.Tags
		if strings.TrimSpace(linkedHTML) != "" {
			j.ContentHTML = linkedHTML
		}
		if strings.TrimSpace(j.Author) == "" {
			// 结构化署名（fetching 从页面 meta/var author 直取）优先；AI 从正文
			// 识别只做兜底——发布者自己填的字段永远比模型猜的准（M7 用户裁定）。
			j.Author = result.Author
		}
		j.AISource = result.SourceName
		j.AINotes = result.Notes
		j.Stage = publishStageAssessing
		j.FailedStage = ""
		j.Error = ""
	})
	if uerr == nil && !swapped {
		return errPublishContentSuperseded
	}
	return uerr
}

// stageAssessRisk 产出发布风险提示并持久化。模型/解析失败统一 fallback warning，
// 任务继续到 staging；正文只读，写库时只触碰 Risk* 与 stage 字段。
func (p *publishPipeline) stageAssessRisk(ctx context.Context, job publishJob) error {
	result := assessPublishRiskWithFallback(ctx, job, p.assessRisk)
	nextStage := publishStageStaging
	if job.Stage == publishStageAssessingReview {
		nextStage = publishStageAwaitingReview
	}
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == job.Stage && current.ContentVersion == job.ContentVersion && current.PendingContentHash == ""
	}, func(j *publishJob) {
		j.RiskLevel = result.Level
		j.RiskItems = append([]publishRiskItem(nil), result.Items...)
		j.RiskHeadline = result.Headline
		j.RiskNotes = result.Notes
		j.RiskRaw = result.Raw
		j.Stage = nextStage
		j.FailedStage = ""
		j.Error = ""
		if nextStage == publishStageAwaitingReview {
			p.enqueueReviewNotification(j)
		}
	})
	if err == nil && !swapped {
		return errPublishContentSuperseded
	}
	if err == nil && nextStage == publishStageAwaitingReview {
		// 历史卡没有 risk 字段，也没有可更新的 message_id；补检后发一张新卡取代旧卡。
		// 不再走 stageStage/PUT，避免已待审草稿因外部更新失败丢审核态。
		p.schedulePublishNotification(updated.ID)
	}
	return err
}

// stageStage 草稿进站：已有 news id 走幂等 PUT；无 id 时必须先持久化
// staging_creating intent 再 POST。FoodTalks 没有服务端幂等键，因此 POST 返回错误、
// 或成功后本地 id 落盘失败，都属于结果不确定态，只能人工核对，绝不自动 POST 第二次。
func (p *publishPipeline) stageStage(ctx context.Context, job publishJob) error {
	ft, err := p.foodTalks()
	if err != nil {
		return err
	}
	frozen, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageStaging && current.ContentVersion == job.ContentVersion && current.PendingContentHash == ""
	}, func(current *publishJob) {
		freezePublishContent(current, p.now())
	})
	if err != nil {
		return err
	}
	if !swapped {
		return errPublishContentSuperseded
	}
	job = frozen
	if linked, _ := p.applySiteCompanyLinks(ctx, job); strings.TrimSpace(linked) != "" {
		job.ContentHTML = linked
	}
	news, err := p.buildSiteNews(ctx, job, foodTalksPublishStatusDraft)
	if err != nil {
		return err
	}
	newsID := job.FoodTalksNewsID
	if newsID > 0 {
		// 幂等锚点已回填：全量覆盖更新既有草稿，绝不重复 CreateNews
		//（网站无删除接口，重复创建即垃圾数据）。
		if guardErr := publishStagingTargetMismatch(job); guardErr != nil {
			return guardErr
		}
		// 红线 26：CMS 手工稿为准。锚点文章已在网站公开时，禁止再 PUT 草稿/正文。
		if _, published, err := p.findOwnPublishedCMSNews(ctx, job); err != nil {
			return fmt.Errorf("核对网站是否已发布失败，未覆盖正文: %w", err)
		} else if published {
			if _, ackErr := p.ackManualCMSPublication(ctx, job.ID); ackErr != nil {
				return ackErr
			}
			return nil
		}
		news.ID = newsID
		if err := ft.UpdateNews(ctx, news); err != nil {
			if foodTalksAlreadyPublishedDraftError(err) {
				if _, ackErr := p.ackManualCMSPublication(ctx, job.ID); ackErr != nil {
					return ackErr
				}
				return nil
			}
			return err
		}
		stagedTarget := currentFoodTalksTarget()
		updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.Stage == publishStageStaging && current.ContentVersion == job.ContentVersion &&
				current.ContentFrozenVersion == job.ContentVersion
		}, func(j *publishJob) {
			j.Stage = publishStageAwaitingReview
			j.StagingTarget = stagedTarget
			j.ContentHTML = job.ContentHTML
			j.FailedStage = ""
			j.Error = ""
			if j.PendingContentHash == "" {
				p.enqueueReviewNotification(j)
			}
		})
		if err != nil {
			return err
		}
		if !swapped {
			return errPublishContentSuperseded
		}
		p.schedulePublishNotification(updated.ID)
		return nil
	}

	intentKey := fmt.Sprintf("foodtalks-create:job-%d", job.ID)
	intentAt := p.now().UTC().Format(time.RFC3339)
	intentJob, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageStaging && current.FoodTalksNewsID == 0 && current.StagingIntentKey == "" &&
			current.ContentVersion == job.ContentVersion && current.ContentFrozenVersion == job.ContentVersion && current.PendingContentHash == ""
	}, func(j *publishJob) {
		j.Stage = publishStageStagingCreating
		j.StagingIntentKey = intentKey
		j.StagingIntentAt = intentAt
		j.FailedStage = ""
		j.Error = ""
	})
	if err != nil {
		return err
	}
	if !swapped {
		if intentJob.StagingIntentKey == "" {
			return errPublishContentSuperseded
		}
		return fmt.Errorf("%w：任务当前阶段 %s，已存在 intent %q", errStagingOutcomeUncertain, intentJob.Stage, intentJob.StagingIntentKey)
	}

	newsID, err = ft.CreateNews(ctx, news)
	if err != nil {
		msg := fmt.Sprintf("CreateNews 请求结果不确定，禁止自动重试；请在 FoodTalks 后台按标题核对后人工对账（intent=%s）：%v", intentKey, err)
		_, _, markErr := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.Stage == publishStageStagingCreating && current.StagingIntentKey == intentKey
		}, func(j *publishJob) {
			j.Stage = publishStageReconciliationRequired
			j.Error = msg
		})
		if markErr != nil {
			log.Printf("publish: job %d 无法落 reconciliation 状态，保留 staging_creating intent: %v", job.ID, markErr)
		}
		return fmt.Errorf("%w：%s", errStagingOutcomeUncertain, msg)
	}
	if newsID <= 0 {
		msg := fmt.Sprintf("CreateNews 返回非正资讯 id=%d，无法建立幂等锚点；请在 FoodTalks 后台人工核对（intent=%s）", newsID, intentKey)
		_, _, markErr := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.Stage == publishStageStagingCreating && current.StagingIntentKey == intentKey
		}, func(j *publishJob) {
			j.Stage = publishStageReconciliationRequired
			j.Error = msg
		})
		if markErr != nil {
			log.Printf("publish: job %d 无法落无效 id reconciliation 状态，保留 staging_creating intent: %v", job.ID, markErr)
		}
		return fmt.Errorf("%w：%s", errStagingOutcomeUncertain, msg)
	}
	createdTarget := currentFoodTalksTarget()
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageStagingCreating && current.StagingIntentKey == intentKey && current.FoodTalksNewsID == 0
	}, func(j *publishJob) {
		j.FoodTalksNewsID = newsID
		j.StagingTarget = createdTarget
		j.Stage = publishStageAwaitingReview
		j.FailedStage = ""
		j.Error = ""
		if j.PendingContentHash == "" {
			p.enqueueReviewNotification(j)
		}
	})
	if err != nil {
		return fmt.Errorf("%w：FoodTalks 已返回资讯 id=%d，但本地锚点落盘失败（intent=%s）：%v", errStagingOutcomeUncertain, newsID, intentKey, err)
	}
	if !swapped {
		return fmt.Errorf("%w：FoodTalks 已返回资讯 id=%d，但任务状态已变为 %s（intent=%s）", errStagingOutcomeUncertain, newsID, updated.Stage, intentKey)
	}
	p.schedulePublishNotification(updated.ID)
	return nil
}

// stagePublicize 翻公开：PUT 全量覆盖语义（契约 §7）——用与 staging 同一个 buildSiteNews
// 重建整份 News 再改 publishStatus=1，绝不能只传状态字段。
// ⚠️ 单向门（契约 §3）：发布过永远回不到草稿 0。本函数只能经 review(approve) 或
// 人工确认“上次未发布”后续跑到达。不可逆 sink 自身必须复核 approve +
// reviewed_at + 被审核的风险版本，不依赖上游调用者自律。
func (p *publishPipeline) stagePublicize(ctx context.Context, job publishJob) error {
	if job.FoodTalksNewsID == 0 {
		return errors.New("缺少 FoodTalks 资讯 id（草稿进站未完成），无法公开")
	}
	if !validPersistedPublishApproval(job) {
		return errors.New("公开授权无效：缺少 approve/reviewed_at，或审核的风险版本已变化")
	}
	if guardErr := publishStagingTargetMismatch(job); guardErr != nil {
		return guardErr
	}
	ft, err := p.foodTalks()
	if err != nil {
		return err
	}
	news, err := p.buildSiteNews(ctx, job, foodTalksPublishStatusPublished)
	if err != nil {
		return err
	}
	// 同题防重硬闸（2026-08-14 用户裁定）：手工发布优先，站上已有同名已发布稿时
	// 绝不重复公开；查询失败同样 fail closed。此时尚未持久化公开意图，纯失败可重试。
	if err := p.sitePublishDuplicateGate(ctx, job); err != nil {
		return err
	}
	news.ID = job.FoodTalksNewsID
	rulePackID, err := publishRulePackIDForJob(job)
	if err != nil {
		return err
	}
	hours, err := p.snapshotPublishLifecyclePolicy(rulePackID, publishResolvedRule{
		AccountID:  rulePackID,
		PresetID:   job.RulePresetID,
		Revision:   job.RuleRevision,
		ConfigHash: job.RuleConfigHash,
		Source:     "server",
	})
	if err != nil {
		return err
	}
	intentKey := fmt.Sprintf("foodtalks-publicize:job-%d:%s", job.ID, job.ApprovedRiskVersion)
	claimed, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStagePublicizing && current.PublicizeIntentKey == "" &&
			validPersistedPublishApproval(current)
	}, func(j *publishJob) {
		j.Stage = publishStagePublicizingUpdating
		j.PublicizeIntentKey = intentKey
		j.PublicizeIntentAt = p.now().UTC().Format(time.RFC3339)
		j.UnpublishWindowHours = hours
		j.UnpublishPolicySet = true
		j.PublicationAction = publishPublicationActionPublish
		j.PublicationActor = strings.TrimSpace(j.ReviewedBy)
		j.PublicationActionAt = j.ReviewedAt
		j.Error = ""
	})
	if err != nil {
		// writeLocked 可能已 rename 成功但在目录 fsync 返错；不能把“可能已落意图”
		// 当成普通 failed 覆写。此时尚未外调，重启后 publicizing 可安全再 claim，
		// publicizing_updating 则按不确定态人工核对。
		return fmt.Errorf("%w：公开意图持久化结果不确定: %v", errPublicizeOutcomeUncertain, err)
	}
	if !swapped {
		if claimed.Stage == publishStagePublicizing && claimed.PublicizeIntentKey == "" &&
			!validPersistedPublishApproval(claimed) {
			return errors.New("公开授权在外调前失效，未发出 UpdateNews")
		}
		return fmt.Errorf("%w：任务当前阶段 %s，公开意图 %q 已被其他 worker 认领或授权已失效",
			errPublicizeOutcomeUncertain, claimed.Stage, claimed.PublicizeIntentKey)
	}

	if err := ft.UpdateNews(ctx, news); err != nil {
		msg := fmt.Sprintf("UpdateNews 请求结果不确定，不得盲目重试；请在 FoodTalks 前台/后台核对文章是否已公开后人工对账（intent=%s）：%v", intentKey, err)
		p.markPublicizeReconciliation(job.ID, intentKey, msg)
		return fmt.Errorf("%w：%s", errPublicizeOutcomeUncertain, msg)
	}
	publishedAt := pipelineNow(p)
	articleURL := siteFrontURL() + "/news/" + strconv.FormatInt(job.FoodTalksNewsID, 10)
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStagePublicizingUpdating && current.PublicizeIntentKey == intentKey
	}, func(j *publishJob) {
		j.ArticleURL = articleURL
		j.Stage = publishStagePublished
		j.FailedStage = ""
		j.Error = ""
		j.PublishedAt = publishedAt.Format(time.RFC3339)
		j.UnpublishWindowUntil = publishWindowUntil(publishedAt, j.UnpublishWindowHours)
		enqueuePublishNotification(j, publishNotificationPublished)
	})
	if err != nil || !swapped {
		if latest, loadErr := p.store.byID(job.ID); loadErr == nil && latest.Stage == publishStagePublished {
			p.schedulePublishNotification(latest.ID)
			return nil
		}
		msg := fmt.Sprintf("FoodTalks 已返回公开成功，但本地终态落盘不确定；请人工核对（intent=%s）", intentKey)
		if err != nil {
			msg += "：" + err.Error()
		} else {
			msg += fmt.Sprintf("：任务已变为 %s", updated.Stage)
		}
		p.markPublicizeReconciliation(job.ID, intentKey, msg)
		return fmt.Errorf("%w：%s", errPublicizeOutcomeUncertain, msg)
	}
	p.schedulePublishNotification(job.ID)
	p.recordPublishedRepost(ctx, updated)
	return nil
}

func validPersistedPublishApproval(job publishJob) bool {
	riskAllows := publishRiskAllowsManualApprove(job.RiskLevel) ||
		(job.ReviewForced && job.RiskLevel == publishRiskBlock)
	if job.ReviewAction != publishReviewApprove || strings.TrimSpace(job.ReviewedAt) == "" ||
		!riskAllows || strings.TrimSpace(job.ApprovedRiskVersion) == "" ||
		!publishContentVersionReadyForReview(job) {
		return false
	}
	current := publishReviewCardVersion(job)
	return len(current) == len(job.ApprovedRiskVersion) &&
		subtle.ConstantTimeCompare([]byte(current), []byte(job.ApprovedRiskVersion)) == 1
}

func (p *publishPipeline) markPublicizeReconciliation(jobID int64, intentKey, reason string) {
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStagePublicizingUpdating && current.PublicizeIntentKey == intentKey
	}, func(j *publishJob) {
		j.Stage = publishStagePublicizeReconciliationRequired
		j.Error = reason
		enqueuePublishNotification(j, publishNotificationPublicizeUncertain)
	})
	if err != nil {
		log.Printf("publish: job %d 记录公开结果不确定状态失败: %v", jobID, err)
		return
	}
	if swapped {
		p.schedulePublishNotification(updated.ID)
	}
}

// review 人审动作核心：HTTP 端点（登录角色门）与飞书卡片回调（token+
// reviewer 白名单门）共用这一个函数——仅 awaiting_review 可操作；approve 只持久化
// publicizing intent 并排队后台 worker，不在请求内等待 FoodTalks。reviewedBy 是审核人留痕（卡片回调传
// operator.open_id，HTTP 端点传空），落 reviewed_by/reviewed_at 进终态卡与任务存储。
// reject 置终态 rejected——文章留在 FoodTalks 草稿态本就不可见，不额外动它，
// 且终态校验保证 reject 后不可再 approve。
func (p *publishPipeline) review(ctx context.Context, jobID int64, action, reviewedBy string) (publishJob, error) {
	return p.reviewWithRiskVersion(ctx, jobID, action, reviewedBy, "")
}

// reviewWithRiskVersion 把卡片上看到的风险版本纳入同一次存储 CAS，消除
// “先读版本、后审核”的 TOCTOU。expectedRiskVersion 为空表示后台人工操作
// 管理端明确审批 CAS 当刻的最新风险快照；两条路径都会持久化该版本。
func (p *publishPipeline) reviewWithRiskVersion(_ context.Context, jobID int64, action, reviewedBy, expectedRiskVersion string) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	if action != publishReviewApprove && action != publishReviewReject && action != publishReviewForceApprove {
		job, err := p.store.byID(jobID)
		if err != nil {
			return publishJob{}, err
		}
		return job, fmt.Errorf("未知审核动作 %q（支持 approve / reject / force_approve）", action)
	}
	// 强制发布归一为 approve 流程；force 只放宽「阻断级不可通过」这一条，
	// 版本比对、内容冻结、环境守卫等其余闸门全部保持不变。
	force := action == publishReviewForceApprove
	if force {
		action = publishReviewApprove
	}
	reviewedAt := p.now().UTC().Format(time.RFC3339)
	switch action {
	case publishReviewApprove:
		versionMismatch := false
		job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
			currentVersion := publishReviewCardVersion(current)
			if expectedRiskVersion != "" && (len(expectedRiskVersion) != len(currentVersion) ||
				subtle.ConstantTimeCompare([]byte(expectedRiskVersion), []byte(currentVersion)) != 1) {
				versionMismatch = true
				return false
			}
			riskAllows := publishRiskAllowsManualApprove(current.RiskLevel) ||
				(force && current.RiskLevel == publishRiskBlock)
			return current.Stage == publishStageAwaitingReview && current.FoodTalksNewsID > 0 &&
				publishStagingTargetMismatch(current) == nil &&
				riskAllows && currentVersion != "" &&
				publishContentVersionReadyForReview(current)
		}, func(j *publishJob) {
			j.Stage = publishStagePublicizing
			j.FailedStage = ""
			j.Error = ""
			j.ReviewedBy = reviewedBy
			j.ReviewedAt = reviewedAt
			j.ReviewAction = publishReviewApprove
			j.ReviewForced = force && j.RiskLevel == publishRiskBlock
			j.ApprovedRiskVersion = publishReviewCardVersion(*j)
			j.PublicizeIntentKey = ""
			j.PublicizeIntentAt = ""
			cancelPublishNotification(j)
		})
		if err != nil {
			return publishJob{}, err
		}
		if !swapped {
			if versionMismatch {
				return job, errors.New("审核卡已过期：风险结论在点击期间发生变化，请使用最新卡片")
			}
			if guardErr := publishStagingTargetMismatch(job); guardErr != nil {
				return job, guardErr
			}
			if !publishRiskLevelKnown(job.RiskLevel) {
				return job, fmt.Errorf("任务尚无有效风险结论（risk_level=%q），为防止漏审已禁止发布；请先完成风险检查", job.RiskLevel)
			}
			if job.RiskLevel == publishRiskBlock {
				return job, errors.New("任务风险级别为 block，普通「通过」不可用；确需发布请使用卡片上的「强制发布」，或驳回后人工处理风险")
			}
			if !publishContentVersionReadyForReview(job) {
				return job, errors.New("任务正文版本尚未冻结或有更高权威内容待处理，禁止按旧版本发布")
			}
			return job, fmt.Errorf("任务当前阶段（%s）无法通过：仅待审核且草稿已进站的任务可发布", job.Stage)
		}
		// 回调/HTTP 请求只负责持久化 approve intent；不可逆 PUT 由后台推进器执行，
		// 从而保证飞书 3 秒窗口内立即返回。PUT 结果不确定时进人工对账态。
		p.spawn(func() { p.runJob(jobID) })
		return job, nil
	case publishReviewReject:
		job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
			return current.Stage == publishStageAwaitingReview
		}, func(j *publishJob) {
			j.Stage = publishStageRejected
			j.FailedStage = ""
			j.Error = ""
			j.ReviewedBy = reviewedBy
			j.ReviewedAt = reviewedAt
			j.ReviewAction = publishReviewReject
			j.ApprovedRiskVersion = ""
			enqueuePublishNotification(j, publishNotificationRejected)
		})
		if err != nil {
			return publishJob{}, err
		}
		if !swapped {
			return job, fmt.Errorf("任务当前阶段（%s）无法驳回：仅待审核任务可驳回", job.Stage)
		}
		p.schedulePublishNotification(job.ID)
	}
	return p.store.byID(jobID)
}

// retry failed 任务从 failed_stage 续跑（异步推进）。publicizing 只有在尚未落
// PublicizeIntentKey（确定未外调）且审批风险版本仍匹配时可重试；已进入
// publicizing_updating / reconciliation 的任务不经本入口。
func (p *publishPipeline) retry(jobID int64) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	if current, loadErr := p.store.byID(jobID); loadErr == nil &&
		current.Stage == publishStageFailed &&
		current.FoodTalksNewsID > 0 &&
		publishStagingTargetMismatch(current) != nil &&
		publishContentRerenderable(current) {
		return p.restageCurrentAndRerender(jobID)
	}
	var target string
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		if current.Stage != publishStageFailed {
			return false
		}
		target = current.FailedStage
		if target == "" || target == publishStageCreated {
			target = publishStageFetching
		}
		// publicizing 重试也是公开动作，必须存在已持久化 approve 授权凭据。
		if target == publishStagePublicizing &&
			(!validPersistedPublishApproval(current) || current.PublicizeIntentKey != "") {
			return false
		}
		if (target == publishStageUnpublishing || target == publishStageRepublishing) &&
			(current.UnpublishIntentKey != "" || current.RepublishIntentKey != "") {
			return false
		}
		if target == publishStageUnpublishing &&
			(!publishUnpublishWindowOpen(current, pipelineNow(p))) {
			return false
		}
		if target == publishStageRepublishing && !validPersistedPublishApproval(current) {
			return false
		}
		// 旧数据若在 staging failed 上已有 intent，也必须人工对账，不可重发 POST。
		if target == publishStageStaging && current.StagingIntentKey != "" && current.FoodTalksNewsID == 0 {
			return false
		}
		return true
	}, func(j *publishJob) {
		j.Stage = target
		j.FailedStage = ""
		j.Error = ""
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return job, fmt.Errorf("任务当前阶段（%s）或授权状态无法重试：仅可安全续跑的 failed 任务可重试", job.Stage)
	}
	p.spawn(func() { p.runJob(jobID) })
	return job, nil
}

// recoverJobs 扫描进程重启时遗留的活跃阶段。纯计算/幂等 PUT 阶段可续跑；
// staging_creating 可能已在网站建文，必须转人工核对；publicizing 只有持久化了
// approve + reviewed_at + 匹配的风险版本且尚未落 PUT intent 才能恢复。
// publicizing_updating 表示崩溃可能发生在外调窗口，必须转人工对账。
func (p *publishPipeline) recoverJobs() {
	if publishWorkersPaused() {
		log.Printf("publish: workers paused，跳过启动恢复扫描")
		return
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 启动恢复扫描失败: %v", err)
		return
	}
	p.recoverRepostSourceSubscribe()
	for _, job := range jobs {
		p.recoverPublishNotification(job)
		p.recoverExternalDistribution(job)
		switch job.Stage {
		case publishStageCreated, publishStageFetching, publishStageRendering,
			publishStageMirroring, publishStageEnriching, publishStageAssessing, publishStageAssessingReview:
			p.spawn(func() { p.runJob(job.ID) })
		case publishStageStaging:
			if job.StagingIntentKey != "" && job.FoodTalksNewsID == 0 {
				p.markStagingReconciliation(job.ID, "任务在 staging 留有 CreateNews intent，无法确认外部结果，启动时禁止自动重发")
				continue
			}
			if !publishRiskLevelKnown(job.RiskLevel) {
				p.queueRiskBackfill(job.ID, publishStageStaging)
				continue
			}
			p.spawn(func() { p.runJob(job.ID) })
		case publishStageAwaitingReview:
			if !publishRiskLevelKnown(job.RiskLevel) {
				p.queueRiskBackfill(job.ID, publishStageAwaitingReview)
			}
		case publishStageStagingCreating:
			p.markStagingReconciliation(job.ID, "进程在 CreateNews 调用窗口中断，外部结果不确定，启动时禁止自动重发")
		case publishStageFailed:
			if job.FailedStage == publishStageStaging && job.StagingIntentKey != "" && job.FoodTalksNewsID == 0 {
				p.markStagingReconciliation(job.ID, "staging intent 已落盘但任务记录为 failed，无法确认外部结果，禁止自动重发")
			}
		case publishStagePublicizing:
			if validPersistedPublishApproval(job) && job.PublicizeIntentKey == "" {
				p.spawn(func() { p.runJob(job.ID) })
				continue
			}
			_, _, updateErr := p.store.updateIf(job.ID, func(current publishJob) bool {
				return current.Stage == publishStagePublicizing &&
					(!validPersistedPublishApproval(current) || current.PublicizeIntentKey != "")
			}, func(j *publishJob) {
				j.Error = "publicizing 缺少已持久化的 approve/reviewed_at/审批风险版本，或风险已变化，启动时拒绝自动公开"
			})
			if updateErr != nil {
				log.Printf("publish: job %d 记录不安全 publicizing 状态失败: %v", job.ID, updateErr)
			}
		case publishStagePublicizingUpdating:
			p.markPublicizeReconciliation(job.ID, job.PublicizeIntentKey,
				"进程在不可逆 UpdateNews 调用窗口中断，外部结果不确定，启动时禁止自动重放")
		case publishStageUnpublishing:
			if publishUnpublishWindowOpen(job, pipelineNow(p)) && job.UnpublishIntentKey == "" {
				p.spawn(func() { p.runJob(job.ID) })
			}
		case publishStageUnpublishingUpdating:
			p.markUnpublishReconciliation(job.ID, job.UnpublishIntentKey,
				"进程在下线 UpdateNews 调用窗口中断，外部结果不确定，启动时禁止自动重放")
		case publishStageRepublishing:
			if validPersistedPublishApproval(job) && job.RepublishIntentKey == "" {
				p.spawn(func() { p.runJob(job.ID) })
			}
		case publishStageRepublishingUpdating:
			p.markRepublishReconciliation(job.ID, job.RepublishIntentKey,
				"进程在重新发布 UpdateNews 调用窗口中断，外部结果不确定，启动时禁止自动重放")
		}
	}
}

func (p *publishPipeline) queueRiskBackfill(jobID int64, expectedStage string) {
	targetStage := publishStageAssessing
	if expectedStage == publishStageAwaitingReview {
		targetStage = publishStageAssessingReview
	}
	_, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == expectedStage && !publishRiskLevelKnown(current.RiskLevel) &&
			(current.StagingIntentKey == "" || current.FoodTalksNewsID > 0)
	}, func(j *publishJob) {
		j.Stage = targetStage
		j.Error = ""
		j.FailedStage = ""
	})
	if err != nil {
		log.Printf("publish: job %d 历史风险补检排队失败: %v", jobID, err)
		return
	}
	if swapped {
		p.spawn(func() { p.runJob(jobID) })
	}
}

func (p *publishPipeline) markStagingReconciliation(jobID int64, reason string) {
	_, _, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return (current.Stage == publishStageStagingCreating || current.Stage == publishStageStaging ||
			(current.Stage == publishStageFailed && current.FailedStage == publishStageStaging)) &&
			current.StagingIntentKey != "" && current.FoodTalksNewsID == 0
	}, func(j *publishJob) {
		j.Stage = publishStageReconciliationRequired
		j.Error = reason + fmt.Sprintf("（intent=%s）", j.StagingIntentKey)
	})
	if err != nil {
		log.Printf("publish: job %d 转人工 reconciliation 失败: %v", jobID, err)
	}
}

// reconcileStaging 是不确定 CreateNews 的人工收口：
//   - newsID>0：运营已在 FoodTalks 后台找到实际草稿，绑定锚点后进入待审；
//   - confirmNotCreated=true：运营确认后台没有该文，才允许清 intent 并重新 POST。
//
// 两种选择都走存储 CAS，重复/并发处理只有一个胜出。
func (p *publishPipeline) reconcileStaging(jobID, newsID int64, confirmNotCreated bool) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	if (newsID > 0) == confirmNotCreated {
		return publishJob{}, errors.New("必须且只能选择 foodtalks_news_id 或 confirm_not_created")
	}
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStageStagingCreating || current.Stage == publishStageReconciliationRequired
	}, func(j *publishJob) {
		j.Error = ""
		j.FailedStage = ""
		if newsID > 0 {
			j.FoodTalksNewsID = newsID
			if publishRiskLevelKnown(j.RiskLevel) {
				j.Stage = publishStageAwaitingReview
				p.enqueueReviewNotification(j)
			} else {
				j.Stage = publishStageAssessingReview
			}
			return
		}
		j.StagingIntentKey = ""
		j.StagingIntentAt = ""
		if publishRiskLevelKnown(j.RiskLevel) {
			j.Stage = publishStageStaging
		} else {
			j.Stage = publishStageAssessing
		}
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return job, fmt.Errorf("任务当前阶段（%s）不需要 CreateNews 对账", job.Stage)
	}
	if job.Stage == publishStageAssessing || job.Stage == publishStageAssessingReview {
		p.spawn(func() { p.runJob(jobID) })
		if latest, loadErr := p.store.byID(jobID); loadErr == nil {
			job = latest
		}
	} else if newsID > 0 {
		p.schedulePublishNotification(job.ID)
	} else {
		p.spawn(func() { p.runJob(jobID) })
	}
	return job, nil
}

// reconcilePublicize 是不可逆 PUT 结果不确定时的人工收口：运营必须先在
// FoodTalks 前台/后台核对，然后明确二选一——已公开则只补本地终态，
// 未公开才允许清掉旧 intent 并发起一次新 PUT。
func (p *publishPipeline) reconcilePublicize(jobID int64, confirmPublished, confirmNotPublished bool) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	if confirmPublished == confirmNotPublished {
		return publishJob{}, errors.New("必须且只能选择 confirm_published 或 confirm_not_published")
	}
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStagePublicizeReconciliationRequired &&
			current.PublicizeIntentKey != "" && validPersistedPublishApproval(current)
	}, func(j *publishJob) {
		j.Error = ""
		j.FailedStage = ""
		if confirmPublished {
			j.ArticleURL = siteFrontURL() + "/news/" + strconv.FormatInt(j.FoodTalksNewsID, 10)
			j.Stage = publishStagePublished
			enqueuePublishNotification(j, publishNotificationPublished)
			return
		}
		j.Stage = publishStagePublicizing
		j.PublicizeIntentKey = ""
		j.PublicizeIntentAt = ""
		cancelPublishNotification(j)
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return job, fmt.Errorf("任务当前阶段（%s）或审批版本不允许公开结果对账", job.Stage)
	}
	if confirmPublished {
		p.schedulePublishNotification(job.ID)
		return job, nil
	}
	p.spawn(func() { p.runJob(jobID) })
	if latest, loadErr := p.store.byID(jobID); loadErr == nil {
		job = latest
	}
	return job, nil
}

// submit source_key 永久幂等建单：任意阶段（含终态）同源任务都返回原单，绝不再建文。
func (p *publishPipeline) submit(sourceURL string) (publishJob, bool, error) {
	return p.submitWithContext(context.Background(), sourceURL)
}

func (p *publishPipeline) submitWithContext(ctx context.Context, sourceURL string) (publishJob, bool, error) {
	return p.submitWithRulePackAndMetadataContext(ctx, sourceURL, sitePublishRulePackID, "", "", "")
}

func (p *publishPipeline) submitWithRulePackAndMetadataContext(
	ctx context.Context, sourceURL, rulePackID, classification, classifier, submittedOriginalURL string,
) (publishJob, bool, error) {
	if publishWorkersPaused() {
		return publishJob{}, false, errPublishWorkersPaused
	}
	rulePackID, err := normalizeSitePublishRulePackID(rulePackID)
	if err != nil {
		return publishJob{}, false, err
	}
	classification = strings.TrimSpace(classification)
	classifier = strings.TrimSpace(classifier)
	ctx = withPublishRulePackID(ctx, rulePackID)
	sourceKey, err := normalizeSiteSourceKey(sourceURL)
	if err != nil {
		return publishJob{}, false, err
	}
	// /s/<slug> 只在单条分享链接内部稳定，两个不同 slug 仍可能指向同一篇文章。
	// 因此短链必须先抓取页面并解析 __biz/mid/idx canonical identity，再原子建单；
	// 绝不能先创建临时 job、事后把冲突输家打成 failed。
	if publishSourceKeyIsShort(sourceKey) {
		title, sourceName, byline, canonicalURL, fetchedOriginalURL, blocksJSON, fetchErr := p.fetch(ctx, sourceURL)
		if fetchErr != nil {
			return publishJob{}, false, fetchErr
		}
		canonicalKey, keyErr := normalizeSiteSourceKey(canonicalURL)
		if keyErr != nil || publishSourceKeyIsShort(canonicalKey) {
			if keyErr != nil {
				return publishJob{}, false, fmt.Errorf("短链未解析出可验证的文章规范身份: %w", keyErr)
			}
			return publishJob{}, false, errors.New("短链未解析出 __biz/mid/idx 文章规范身份，请使用公众号文章完整链接")
		}
		title = strings.TrimSpace(title)
		if title == "" || strings.TrimSpace(blocksJSON) == "" {
			return publishJob{}, false, errors.New("短链解析结果缺少标题或正文内容块，未创建发布任务")
		}
		seed := newPublishContentSeed(publishContentWebFetch, title, sourceName, byline, blocksJSON, "")
		job, created, createErr := p.store.createSeeded(canonicalKey, sourceURL, func(j *publishJob) {
			j.SourceAliases = []string{sourceKey}
			j.RulePackID = rulePackID
			j.Classification = classification
			j.Classifier = classifier
			applyPublishContentSeed(j, seed)
			j.OriginalSourceURL = preferOriginalSourceURL(fetchedOriginalURL, submittedOriginalURL)
		})
		if createErr != nil {
			return publishJob{}, false, createErr
		}
		if created {
			p.spawn(func() { p.runJob(job.ID) })
		}
		return job, created, nil
	}
	submittedOriginal := normalizeSubmittedOriginalSourceURL(submittedOriginalURL)
	job, created, err := p.store.createSeeded(sourceKey, sourceURL, func(j *publishJob) {
		j.RulePackID = rulePackID
		j.Classification = classification
		j.Classifier = classifier
		if submittedOriginal != "" {
			j.OriginalSourceURL = submittedOriginal
		}
	})
	if err != nil {
		return publishJob{}, false, err
	}
	if !created && submittedOriginal != "" && !isHTTPURL(job.OriginalSourceURL) {
		updated, updateErr := p.store.update(job.ID, func(current *publishJob) {
			if !isHTTPURL(current.OriginalSourceURL) {
				current.OriginalSourceURL = submittedOriginal
			}
		})
		if updateErr == nil {
			job = updated
		}
	}
	if created {
		p.spawn(func() { p.runJob(job.ID) })
	}
	return job, created, nil
}

func publishSourceKeyIsShort(sourceKey string) bool {
	if !strings.HasPrefix(sourceKey, "site:") {
		return false
	}
	u, err := url.Parse(strings.TrimPrefix(sourceKey, "site:"))
	if err != nil {
		return false
	}
	path := strings.TrimRight(u.Path, "/")
	return strings.HasPrefix(path, "/s/") && len(path) > len("/s/")
}

type officialPublishInput struct {
	URL               string
	Title             string
	SourceName        string
	Author            string
	ContentHTML       string
	CoverURL          string
	RulePackID        string
	Classification    string
	ClassifierVersion string
}

// ⚠️ 以下 submitOfficial* / submitSupplier* / submitStructuredContent 这条「外部
// 投递正文」通道**已无生产调用方**，仅存单元测试。2026-08-06 用户裁定的红线是
// 「对外发布接口只接收链接」，site-sync 与 official-sync 两个 HTTP 入口都已在
// rejectedPublishContentFields 处把正文字段 400 挡掉。
//
// 不要把它接回任何 HTTP 入口。用户报告的任务 #72 排版崩坏（长文零小标题、正文段落
// 被排成灰色图注）就出自这条通道：它用 extractArticleBlocks 解析外部投递的官方 API
// HTML，与抓取路径的 extractArticleBlocksWithOptions + articleBlockOptionsForAccount
// 不是同一条路；而 publishContentRank 里 official_api/supplier 的优先级又高于自抓的
// web_fetch，投什么就发什么。（#72 具体崩在哪一步未复现——没有留存的原始正文样本。）
//
// 保留而非立即删除，是为了把这一轮改动面收窄到「关入口」；整条通道连同
// officialPlainTextBlocks 的清理另行安排。
func (p *publishPipeline) submitOfficial(input officialPublishInput) (publishJob, bool, error) {
	return p.submitOfficialWithContext(context.Background(), input)
}

func (p *publishPipeline) submitSupplier(input officialPublishInput) (publishJob, bool, error) {
	return p.submitSupplierWithContext(context.Background(), input)
}

func (p *publishPipeline) submitOfficialWithContext(ctx context.Context, input officialPublishInput) (publishJob, bool, error) {
	return p.submitStructuredContent(ctx, input, publishContentOfficial, nil)
}

func (p *publishPipeline) submitSupplierWithContext(ctx context.Context, input officialPublishInput) (publishJob, bool, error) {
	return p.submitStructuredContent(ctx, input, publishContentSupplier, nil)
}

// submitSupplierWithRestartContext 随 site-sync 的正文入口一并下线：该函数唯一的
// 调用点是「外部提交的正文变了就重启同一条任务」，而发布接口已不再接收正文。

func (p *publishPipeline) submitStructuredContent(
	ctx context.Context,
	input officialPublishInput,
	contentSource string,
	restartedOut *bool,
) (publishJob, bool, error) {
	if restartedOut != nil {
		*restartedOut = false
	}
	if publishWorkersPaused() {
		return publishJob{}, false, errPublishWorkersPaused
	}
	rulePackID, err := normalizeSitePublishRulePackID(input.RulePackID)
	if err != nil {
		return publishJob{}, false, err
	}
	classification := strings.TrimSpace(input.Classification)
	classifier := strings.TrimSpace(input.ClassifierVersion)
	sourceKey, err := normalizeSiteSourceKey(input.URL)
	if err != nil {
		return publishJob{}, false, err
	}
	originalSourceKey := sourceKey
	if publishSourceKeyIsShort(sourceKey) {
		if p.fetch == nil {
			return publishJob{}, false, errors.New("短链缺少 canonical identity 解析器，未创建发布任务")
		}
		_, _, _, canonicalURL, _, _, fetchErr := p.fetch(withPublishRulePackID(ctx, rulePackID), input.URL)
		if fetchErr != nil {
			return publishJob{}, false, fmt.Errorf("短链规范身份解析失败，未创建发布任务: %w", fetchErr)
		}
		canonicalKey, keyErr := normalizeSiteSourceKey(canonicalURL)
		if keyErr != nil || publishSourceKeyIsShort(canonicalKey) {
			if keyErr != nil {
				return publishJob{}, false, fmt.Errorf("短链未解析出可验证的文章规范身份: %w", keyErr)
			}
			return publishJob{}, false, errors.New("短链未解析出 __biz/mid/idx 文章规范身份，未创建发布任务")
		}
		sourceKey = canonicalKey
	}
	input.Title = strings.TrimSpace(input.Title)
	input.ContentHTML = strings.TrimSpace(input.ContentHTML)
	if input.Title == "" {
		return publishJob{}, false, errors.New("title is required")
	}
	if input.ContentHTML == "" {
		return publishJob{}, false, errors.New("content_html is required")
	}
	_, blocks, parsedAuthor := extractArticleBlocks(input.ContentHTML)
	if len(blocks) == 0 {
		blocks = officialPlainTextBlocks(input.ContentHTML)
	}
	if len(blocks) == 0 {
		return publishJob{}, false, errors.New("官方 API 正文未解析出可排版内容块")
	}
	blocksJSON, err := json.Marshal(blocks)
	if err != nil {
		return publishJob{}, false, fmt.Errorf("序列化官方 API 正文内容块失败: %w", err)
	}
	input.Author = strings.TrimSpace(input.Author)
	if input.Author == "" {
		input.Author = strings.TrimSpace(parsedAuthor)
	}
	seed := newPublishContentSeed(contentSource, input.Title, input.SourceName, input.Author, string(blocksJSON), input.CoverURL)
	job, created, err := p.store.createSeeded(sourceKey, input.URL, func(j *publishJob) {
		j.RulePackID = rulePackID
		j.Classification = classification
		j.Classifier = classifier
		if originalSourceKey != sourceKey {
			j.SourceAliases = []string{originalSourceKey}
		}
		applyPublishContentSeed(j, seed)
	})
	if err != nil {
		return publishJob{}, false, err
	}
	if !created {
		if originalSourceKey != sourceKey {
			if _, conflictingID, aliasErr := p.store.claimSourceAlias(job.ID, originalSourceKey); aliasErr != nil {
				return publishJob{}, false, aliasErr
			} else if conflictingID > 0 && conflictingID != job.ID {
				return publishJob{}, false, fmt.Errorf("短链身份已属于发布任务 #%d", conflictingID)
			}
		}
		var restart bool
		job, restart, err = p.mergeIncomingContent(job.ID, seed)
		if err != nil {
			return publishJob{}, false, err
		}
		if restart {
			if restartedOut != nil {
				*restartedOut = true
			}
			p.spawn(func() { p.runJob(job.ID) })
		}
	} else {
		p.spawn(func() { p.runJob(job.ID) })
	}
	return job, created, nil
}

// officialPlainTextBlocks 兼容 freepublish 对快讯正文返回「根级纯文本 + 少量行内
// 链接」的真实形态。空行保留为下一段的 blankBefore；链接继续保留为 runs，
// 不做 Markdown 猜测，也不引入任何网页回源。
func officialPlainTextBlocks(content string) []Block {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	blocks := make([]Block, 0)
	pendingBlank := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(blocks) > 0 {
				pendingBlank = true
			}
			continue
		}
		block := Block{K: "p", BlankBefore: pendingBlank}
		if regexp.MustCompile(`(?i)</?[a-z][^>]*>`).MatchString(line) {
			block.Runs = officialInlineRuns(line)
			if !hasContent(block.Runs) {
				continue
			}
		} else {
			block.Text = line
		}
		blocks = append(blocks, block)
		pendingBlank = false
	}
	return blocks
}

func officialInlineRuns(line string) []Run {
	doc, err := html.Parse(strings.NewReader("<body>" + line + "</body>"))
	if err != nil {
		return nil
	}
	var runs []Run
	var walk func(*html.Node, string, bool)
	walk = func(node *html.Node, href string, bold bool) {
		if node.Type == html.TextNode {
			runType := "txt"
			if href != "" {
				runType = "link"
			}
			runs = append(runs, Run{Type: runType, Text: node.Data, Href: href, Bold: bold})
			return
		}
		if node.Type == html.ElementNode {
			tag := strings.ToLower(node.Data)
			switch tag {
			case "script", "style", "noscript", "iframe", "img":
				return
			case "br":
				runs = append(runs, Run{Type: "txt", Text: " "})
				return
			case "a":
				candidate := strings.TrimSpace(getAttr(node, "href"))
				if candidate != "" && !strings.HasPrefix(strings.ToLower(candidate), "javascript:") {
					href = candidate
				}
			case "strong", "b":
				bold = true
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, href, bold)
		}
	}
	walk(doc, "", false)
	return normalizeRuns(runs)
}

// ---- News 组装与可配项 ----

// buildSiteNews News 组装唯一入口：staging（草稿）与 publicizing（公开）都从这一个函数
// 拿同一份全量字段——PUT 是全量覆盖语义（契约 §7），两处各自组装一旦漂移，公开时缺的
// 字段会被覆盖成零值。可配项集中在此走 env，绝不散落多处硬编码。
func (p *publishPipeline) buildSiteNews(ctx context.Context, job publishJob, publishStatus int) (foodTalksNews, error) {
	// M7 来源判定（docs/editorial-norms-foodtalks.md §1/§2/§5，从库里 163 篇倒推）：
	// sourceType 是确定性规则不交模型——自家号清单命中=0（原创），否则=1（转载）；
	// 自家号转发他人文章时 AI 从正文声明抽出真实出处（job.AISource），此时按转载算。
	// author 不再回落 source_name：编辑部 132/163 篇作者为空、来源归 sourceName 字段。
	if leak := publishHTMLWechatTrailerLeak(job.ContentHTML); leak != "" {
		return foodTalksNews{}, fmt.Errorf("正文仍含公众号导流尾（%s），禁止写入 FoodTalks", leak)
	}
	sourceType, sourceName := siteSourceFor(job)
	// 分类按原创/转载分派（2026-08-14 用户裁定）：原创=独家专栏，转载=食品新闻。
	primary, categories, err := siteCategoryIDsFor(sourceType)
	if err != nil {
		return foodTalksNews{}, err
	}
	// 公司库关联（2026-08-17 用户裁定，规范 step2-(5)）：AI 标签词精确匹配公司库后，
	// 正文首次出现处注入 mc-company-link，relatedCompanyIds 同步写关联 id。
	// 检索按词幂等，重复组装对同一标签集产出一致结果（同一公司库状态下确定性）。
	companyMatches := p.siteCompanyMatchesFor(ctx, job)
	enriched := injectCompanyLinks(job.ContentHTML, companyMatches)
	relatedCompanyIDs := make([]int64, 0, len(companyMatches))
	for _, m := range companyMatches {
		relatedCompanyIDs = append(relatedCompanyIDs, m.Company.ID)
	}
	// 目录（2026-08-17 用户裁定：所有文章一视同仁，不分原创/转载——此前只给原创/Mote
	// 是误解，历史转载没目录多为人工漏掉）：字数与标题数达标即注入 h2 锚点并写 toc
	// 字段；锚点由内容哈希派生，重复组装字节一致。阈值见 publishSiteTocForContent。
	// 在公司链接注入之后计算，锚点基于最终正文。
	content, toc := publishSiteTocForContent(enriched)
	return foodTalksNews{
		Title:         job.Title,
		CoverImg:      job.CoverURL, // mirroring 后已是转存 OSS URL；enriching 可能换成 AI 选中图
		SourceType:    sourceType,
		SourceID:      p.articleSourceID(ctx, sourceName),
		SourceName:    sourceName,
		PublishStatus: publishStatus,
		IsPublic:      1, // ⚠️ 实测非可见性开关（契约 §4），恒 1；隐藏靠 publishStatus=0
		Author:        strings.TrimSpace(job.Author),
		Summary:       job.Summary, // M5 AI 摘要（兜底=正文前 100 字）
		// newsTags（2026-08-14 复测解除封印）：契约 §9 记载的「PUT 带标签必 500 且污染
		// 文章」已在后端修复——测试环境 7321/7322 实测 POST/PUT 带标签、重复 PUT、清空
		// 全部 code=0 且读写闭环；生产 62765 运营带标签多次编辑亦正常。所有文章解析
		// AI 标签，Mote/系列标签优先，总数最多 3 个且只解析不创建（见 siteNewsTagsFor）。
		NewsTags: p.siteNewsTagsFor(ctx, job),
		// 以下三项是发布/编辑接口必填。PUT 是全量覆盖，Go 零值会把 CMS 默认值盖成
		// 0/false。官方文档示例与字段表均为 isLatest=1 / imgPreviewEnable=true /
		// commentEnable=1；isLatest=0 时文章可以直链可见，但进不了首页「最新资讯」。
		IsLatest:          1,
		ImgPreviewEnable:  true,
		CommentEnable:     1,
		Content:           content, // mirroring 后成品（图片已全部 OSS 化）；组装层再注入公司链接与 h2 锚点 id
		TOC:               toc,
		RelatedCompanyIDs: relatedCompanyIDs,
		PublishTime:       sitePublishTime(job.CreatedAt, p.now()),
		PrimaryCategoryID: primary,
		CategoryIDs:       categories,
		Language:          "ZH", // 中文站固定
		SEOTitle:          job.Title + "-FoodTalks全球食品资讯",
	}, nil
}

const foodTalksSourceTagCacheTTL = 10 * time.Minute

func (p *publishPipeline) articleSourceID(ctx context.Context, sourceName string) int64 {
	if strings.TrimSpace(sourceName) == "" {
		return 0
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	if p != nil && p.now != nil {
		now = p.now()
	}
	norm := normalizeFoodTalksSourceName(sourceName)
	if p != nil {
		p.sourceTagsMu.Lock()
		if !p.sourceTagsLoaded.IsZero() && now.Sub(p.sourceTagsLoaded) < foodTalksSourceTagCacheTTL {
			tags := p.sourceTags
			p.sourceTagsMu.Unlock()
			return matchFoodTalksSourceID(tags, sourceName)
		}
		if !p.sourceIDCacheAt.IsZero() && now.Sub(p.sourceIDCacheAt) < foodTalksSourceTagCacheTTL {
			if id, ok := p.sourceIDByName[norm]; ok {
				p.sourceTagsMu.Unlock()
				return id
			}
		}
		p.sourceTagsMu.Unlock()
	}
	if p == nil || p.foodTalks == nil {
		return 0
	}
	ft, err := p.foodTalks()
	if err != nil {
		log.Printf("publish: 拉取来源出处列表失败，CMS 下来源无法自动选中（%s）：%v", sourceName, err)
		return 0
	}
	tags, err := ft.ListTagsByType(ctx, foodTalksTagTypeArticleSource, sourceName)
	if err != nil {
		log.Printf("publish: 拉取来源出处列表失败，CMS 下来源无法自动选中（%s）：%v", sourceName, err)
		return 0
	}
	id := matchFoodTalksSourceID(tags, sourceName)
	if id == 0 {
		created, err := ft.CreateTagByName(ctx, foodTalksTagTypeArticleSource, strings.TrimSpace(sourceName))
		if err != nil {
			log.Printf("publish: 来源出处 %q 不在 CMS 标签库，自动创建失败：%v", sourceName, err)
		} else {
			id = created
			log.Printf("publish: 来源出处 %q 已写入 CMS 标签库（id=%d）", sourceName, id)
		}
	}
	if id > 0 {
		p.sourceTagsMu.Lock()
		if p.sourceIDByName == nil || p.sourceIDCacheAt.IsZero() || now.Sub(p.sourceIDCacheAt) >= foodTalksSourceTagCacheTTL {
			p.sourceIDByName = map[string]int64{}
			p.sourceIDCacheAt = now
		}
		p.sourceIDByName[norm] = id
		p.sourceTagsMu.Unlock()
	}
	return id
}

// siteSourceFor 转载/原创判定（确定性规则，规范文档 §1：能用代码就不用模型）：
//  1. AI 从正文抽到出处声明且不是自家账号 → 转载，来源=声明出处（自家号转发他人文章）；
//  2. 公众号名在自家清单 → 原创，来源=公众号名；
//  3. 其余 → 转载，来源=公众号名（直接监控外部公众号的场景）。
//
// source_type 语义 0=原创/自有、1=转载，来自 mc-fb 库近一月 163 篇实测分布（规范 §1）。
func siteOwnAccounts() map[string]bool {
	own := map[string]bool{}
	raw := strings.TrimSpace(os.Getenv(siteOwnAccountsEnv))
	if raw == "" {
		raw = defaultSiteOwnAccounts
	}
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			own[name] = true
		}
	}
	return own
}

var explicitSiteRepostDisclosure = regexp.MustCompile(`^本文为转载文章(?:[，,：:].*)?[。.!！]?$`)

// 确定性出处声明模式（2026-08-17 运营反馈：任务 #101 天下网商转载稿 AI 漏抽出处、
// 被误判原创进独家专栏）。自家公众号转发他人文章时，正文头部/尾部惯例带这三类声明，
// 用代码直取兜底，不再全靠模型（规范 §1：能用代码就不用模型）：
//
//	「来源：天下网商（ID：xxx）」「本文来自微信公众号：小食代」「本文为新零售原创，作者：…」
var publishDeclaredSourcePatterns = []*regexp.Regexp{
	// 分隔符兼容中英文冒号与竖线/斜杠（红线 17 公众号渠道同口径：来源｜天下网商）
	regexp.MustCompile(`^(?:来源|出处|文章来源|本文来源)\s*[：:｜|/]\s*(.+)$`),
	regexp.MustCompile(`本文(?:来自|转自|转载自|经授权转载自|首发于)\s*(?:微信)?(?:公众号)?\s*[：:「"“]?\s*([^\s，。、；：:（()）「」"”]{2,30})`),
	regexp.MustCompile(`^本文(?:系|为)\s*([^\s，。、；：:（()）「」"”]{2,30}?)\s*(?:原创|首发|授权)`),
}

var publishDeclaredSourceIDSuffixRe = regexp.MustCompile(`[（(]\s*(?i:ID)[：:][^）)]*[）)]?\s*$`)

// publishDeclaredSource 从正文块里确定性抽取出处声明的机构名；抽不到返回空串。
// 只扫头部前 12 个文本块与尾部 35% 区域——转载声明的惯例位置；不扫正文中段，
// 避免把「来源：某数据库」之类的行内说明误当出处。图/图注/表格块跳过。
func publishDeclaredSource(blocksJSON string) string {
	var blocks []Block
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return ""
	}
	tailFrom := int(float64(len(blocks)) * 0.65)
	textSeen := 0
	for i, block := range blocks {
		if block.K == "img" || block.K == "cap" || block.K == "video" || block.K == "table" {
			continue
		}
		textSeen++
		if textSeen > 12 && i < tailFrom {
			continue
		}
		line := strings.TrimSpace(publishExternalDistributionBlockText(block))
		if line == "" || len([]rune(line)) > 80 {
			continue
		}
		for _, re := range publishDeclaredSourcePatterns {
			m := re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			name := strings.TrimSpace(publishDeclaredSourceIDSuffixRe.ReplaceAllString(m[1], ""))
			name = strings.Trim(name, `「」"“”《》`)
			// 声明行里常跟着「，作者：xxx」等后缀，截到第一个分隔符为止
			if idx := strings.IndexAny(name, "，,。；;："); idx >= 0 {
				name = name[:idx]
			}
			name = strings.TrimSpace(name)
			if n := len([]rune(name)); n >= 2 && n <= 30 {
				return name
			}
		}
	}
	return ""
}

// publishSiteIsRepost 只回答 CMS 来源类型：正文明确转载声明优先；否则自家公众号
// 按原创，其余公众号按转载。它不决定是否切除微信导流尾——进网站一律切尾。
func publishSiteIsRepost(sourceName, blocksJSON string) bool {
	var blocks []Block
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err == nil {
		for _, block := range blocks {
			text := block.Text
			if len(block.Runs) > 0 {
				var b strings.Builder
				for _, run := range block.Runs {
					if run.Type != "img" && run.Type != "br" {
						b.WriteString(run.Text)
					}
				}
				text = b.String()
			}
			text = strings.Map(func(r rune) rune {
				if r == '*' || r == '＊' || r == ' ' || r == '\t' || r == '\r' || r == '\n' {
					return -1
				}
				return r
			}, text)
			if explicitSiteRepostDisclosure.MatchString(text) {
				return true
			}
		}
	}
	return !siteOwnAccounts()[strings.TrimSpace(sourceName)]
}

// publishArticleProvenance 是所有渠道共用的文章来源真相：只负责回答“是否原创、
// 来源是谁”，不掺任何渠道发布策略。FoodTalks 可接收两类文章；云梭等外部渠道
// 在自己的资格函数中只接受 original=true，避免把平台名写进中立能力层。
func publishArticleProvenance(job publishJob) (original bool, source string) {
	own := siteOwnAccounts()
	if src := strings.TrimSpace(job.AISource); src != "" && !own[src] {
		return false, src
	}
	// AI 没抽到出处时用确定性声明抽取兜底（任务 #101 教训：自家号转发天下网商，
	// AI 把机构名填进 author、AISource 为空 → 误判原创进独家专栏）。抽到自家名
	//（如「本文为FBIF食品饮料创新原创」）时不在此返回，继续走自家清单原创判定。
	if src := publishDeclaredSource(job.BlocksJSON); src != "" && !own[src] {
		return false, src
	}
	name := strings.TrimSpace(job.SourceName)
	if publishSiteIsRepost(name, job.BlocksJSON) {
		return false, name
	}
	return true, name
}

func siteSourceFor(job publishJob) (int, string) {
	original, source := publishArticleProvenance(job)
	if !original {
		return 1, source
	}
	return 0, source
}

// siteFrontURL 前台地址（article_url 回填用），默认测试环境；生产切换只改 env。
func siteFrontURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv(siteFrontURLEnv)), "/"); v != "" {
		return v
	}
	return defaultSiteFrontURL
}

// siteCategoryIDs 解析分类配置：第一个为主分类（primaryCategoryId），全集进 categoryIds。
func siteCategoryIDs() (int64, []int64, error) {
	raw := strings.TrimSpace(os.Getenv(siteCategoryIDsEnv))
	if raw == "" {
		raw = defaultSiteCategoryIDs
	}
	return parseSiteCategoryIDs(raw, siteCategoryIDsEnv)
}

// siteCategoryIDsFor 按原创/转载分派分类（2026-08-14 用户裁定 + 7 月 256 篇实证：
// 独家专栏(52)是原创专属栏目，转载 108/120 挂食品新闻(53)；此前一刀切 52 把
// PackHorizon 等转载稿错标成独家专栏）。原创沿用 FOODTALKS_CATEGORY_IDS 现状，
// 转载走 FOODTALKS_REPOST_CATEGORY_IDS（默认 53 食品新闻）。
func siteCategoryIDsFor(sourceType int) (int64, []int64, error) {
	if sourceType != 0 {
		raw := strings.TrimSpace(os.Getenv(siteRepostCategoryIDsEnv))
		if raw == "" {
			raw = defaultSiteRepostCategoryIDs
		}
		return parseSiteCategoryIDs(raw, siteRepostCategoryIDsEnv)
	}
	return siteCategoryIDs()
}

func parseSiteCategoryIDs(raw, envName string) (int64, []int64, error) {
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return 0, nil, fmt.Errorf("分类配置 %s 非法（%q 不是数字）", envName, part)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return 0, nil, fmt.Errorf("分类配置 %s 为空：至少需要一个分类 id", envName)
	}
	return ids[0], ids, nil
}

// sitePublishTime 任务创建时间 → FoodTalks publishTime（YYYY-MM-DD HH:mm:ss，北京时间）。
// 存储串解析失败时回退当前时间——publishTime 只影响前台展示排序，不值得让任务失败。
func sitePublishTime(createdAt string, fallback time.Time) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		t = fallback
	}
	// FoodTalks 是中文站，publishTime 无时区字段，按运营口径固定北京时间
	//（FixedZone 不依赖系统 tzdata，容器内确定性）。
	return t.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")
}

// ---- 正文图片提取 / 重写（照抄资讯订阅仓 pipeline.go 同名函数）----

var publishImgSrcRe = regexp.MustCompile(`(?i)<img\b[^>]*?\bsrc="([^"]+)"`)

// extractImgSrcs 按出现顺序提取去重后的 img src。
func extractImgSrcs(rawHTML string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range publishImgSrcRe.FindAllStringSubmatch(rawHTML, -1) {
		src := html.UnescapeString(m[1])
		if src == "" || seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	return out
}

// rewriteImgSrcs 把已转存图片的 src 替换为 OSS URL（只替换 src 属性值，不动其他）。
func rewriteImgSrcs(rawHTML string, imageMap map[string]string) string {
	if len(imageMap) == 0 {
		return rawHTML
	}
	for src, ossURL := range imageMap {
		if ossURL == "" {
			continue
		}
		escaped := html.EscapeString(src)
		rawHTML = strings.ReplaceAll(rawHTML, `src="`+src+`"`, `src="`+ossURL+`"`)
		if escaped != src {
			rawHTML = strings.ReplaceAll(rawHTML, `src="`+escaped+`"`, `src="`+ossURL+`"`)
		}
	}
	return rawHTML
}
