package main

// publish_pipeline.go — 「公众号 → FoodTalks 网站」发布状态机（施工图 S2，
// WORKLOG-publish-pipeline-in-layout-2026-07-10.md；状态机语义照抄 FBIF资讯订阅仓
// internal/publishing/site.go，阶段执行进程内化——这是架构反转的最大红利）：
//
//	created → fetching（进程内抓文章存 blocks）→ rendering（render ft-default）
//	        → mirroring（图片批量转存 OSS + 重写）→ enriching（AI 预填发布参数，M5）
//	        → staging（CreateNews 草稿进站）
//	        → awaiting_review（人审闸门，自动推进到此为止）
//	        → [人 approve] → publicizing（UpdateNews 翻 publishStatus=1）→ published
//	        → [人 reject]  → rejected（终态；文章留在 FoodTalks 草稿态，本就不可见）
//
// 🚨 用户红线（最高优先）：翻公开（UpdateNews publishStatus=1）是唯一不可逆动作
//（单向门 1↛0，契约 §3），只能由 review approve 端点触发；任何自动路径（site-sync、
// retry 到非 publicizing 阶段、进程重启）都不得触发——tests/publish_pipeline_test.go
// 的「无 approve 则 UpdateNews 永不被调」测试锁死这条。
//
// 依赖注入哲学：fetch/render/ossUpload 的默认实现**进程内复用**既有内核（不复制第二份，
// ADR 四层模型 L1）；FoodTalks 客户端与飞书通知每次现构造（凭证 env-only 运行期可变）；
// 测试注入 fake / httptest 桩。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// sitePublishRulePackID 网站渠道排版规则包（registry 的 ft-default，FoodTalks 视觉口径）。
const sitePublishRulePackID = "ft-default"

// 可配项 env（MVP 先 env/常量，绝不散落硬编码，后续进管理后台）。
const (
	siteCategoryIDsEnv     = "FOODTALKS_CATEGORY_IDS"
	siteFrontURLEnv        = "FOODTALKS_FRONT_URL"
	siteOwnAccountsEnv     = "SITE_OWN_ACCOUNTS"
	siteAdminURLEnv        = "FOODTALKS_ADMIN_ARTICLE_URL" // 管理后台文章编辑页模板，{id} 占位
	defaultSiteCategoryIDs = "52,58"                       // 与 2026-07-10 冒烟一致（契约 §9：线上分类表待后端给全集）
	defaultSiteFrontURL    = "https://test.foodtalks.cn"   // 默认测试环境前台；生产切换只改 env
	// 管理后台默认先落在首页（hash 路由 SPA）：编辑页路由从后台 JS 包挖到是
	// cms/:siteCode/news/edit/:id，但 siteCode 是运行时值、不猜（红线：不编造 URL）——
	// 拿到真实编辑页 URL 后配 env（含 {id} 占位）即得深链，无需改代码。
	defaultSiteAdminURL = "https://admin-we-test.foodtalks.cn/#/"
	// 自家账号清单（原创判定，规范文档 §1）：近一月库内 source_type=0 的全部来源名。
	defaultSiteOwnAccounts = "FBIF食品饮料创新,FBIF食品饮料创新论坛,FoodTalks,FBIF食品创新展"
)

// 人审动作。
const (
	publishReviewApprove = "approve"
	publishReviewReject  = "reject"
)

// errStagingOutcomeUncertain 表示 CreateNews 可能已被服务端执行，调用方绝不能把任务
// 降成普通 failed 后自动重试 POST。stageStage 会尽力落 reconciliation_required；若连
// 本地写也失败，则保留此前已持久化的 staging_creating intent，交给启动扫描转人工。
var errStagingOutcomeUncertain = errors.New("FoodTalks CreateNews 结果不确定")

// foodTalksAPI FoodTalks 发布客户端能力面（*foodTalksClient 满足；测试可注入 fake）。
type foodTalksAPI interface {
	CreateNews(ctx context.Context, n foodTalksNews) (int64, error)
	UpdateNews(ctx context.Context, n foodTalksNews) error
}

// publishPipeline 状态机执行器。函数字段的默认值在 newPublishPipeline 装配为
// 进程内复用实现，测试逐项替换。
type publishPipeline struct {
	store *publishJobStore
	rules *rulePresetStore

	// fetch 公众号 URL → 标题 + 公众号名 + 结构化作者署名 + 中立 Block[] JSON
	//（默认复用 fetchDirectBlocksWithOptions；byline 是发布者填的结构化字段，
	// 比 AI 从正文猜准确——AI 只在它为空时兜底，M7）
	fetch func(ctx context.Context, articleURL string) (title, sourceName, byline, blocksJSON string, err error)
	// render Block[] → ft-default 规则包内联 HTML（默认走 render_proxy 转发内核）
	render func(ctx context.Context, title, source, blocksJSON string) (string, error)
	// ossUpload 整批图片 URL → OSS 转存（默认走 /api/oss-upload 的上游代理内核，urls 模式）
	ossUpload func(ctx context.Context, urls []string) (map[string]string, error)
	// enrich AI 预填发布参数（M5，默认 arkEnrichArticle）；nil / 失败都走规则兜底，
	// WARN 不 fail job——AI 是加速器不是闸门
	enrich func(ctx context.Context, job publishJob) (publishEnrichment, error)
	// foodTalks 每次任务执行现构造（凭证 env-only 运行期可变；缺配置的错误落到具体任务上）
	foodTalks func() (foodTalksAPI, error)
	// notify 人审群通知；nil 或返回错误都不阻断流水线——人可以查任务列表，通知只是加速器
	notify func(ctx context.Context, text string) error
	// notifyCard 审核交互卡片发送（M4）；nil 或失败降级回 notify 文本，同样不阻断流水线
	notifyCard func(ctx context.Context, card map[string]any) error
	// coverImgKey 封面 URL → 飞书图片素材 img_key（卡片内呈现封面用）；nil 或失败
	// 降级为卡片不带图，同样不阻断流水线
	coverImgKey func(ctx context.Context, imgURL string) (string, error)
	// spawn 异步推进入口（生产 go routine；测试注入同步执行保证确定性）
	spawn func(func())
	now   func() time.Time
}

// newPublishPipeline 生产装配：全部依赖用进程内复用的默认实现。
func newPublishPipeline(store *publishJobStore, rules *rulePresetStore) *publishPipeline {
	p := &publishPipeline{
		store:     store,
		rules:     rules,
		render:    renderSiteHTML,
		ossUpload: ossUploadByURLs,
		enrich:    arkEnrichArticle,
		foodTalks: func() (foodTalksAPI, error) { return newFoodTalksClientFromEnv() },
		spawn:     func(f func()) { go f() },
		now:       time.Now,
	}
	p.fetch = func(ctx context.Context, articleURL string) (string, string, string, string, error) {
		return fetchSiteArticle(rules, articleURL)
	}
	if n := newFeishuNotifierFromEnv(); n != nil {
		p.notify = n.send
		p.notifyCard = n.sendCard
		p.coverImgKey = n.uploadImageByURL
	} else {
		log.Printf("publish: 飞书通知未配置（需 FEISHU_APP_ID / FEISHU_APP_SECRET / FEISHU_REVIEW_CHAT_ID），审核群通知禁用")
	}
	return p
}

// ---- source_key 归一化（幂等键，语义照抄资讯订阅仓 NormalizeSiteSourceKey）----

// normalizeSiteSourceKey 公众号文章 URL → 幂等键。
// 规范化：强制 https + 小写 host、去 fragment；长链只保留稳定定位参数 __biz/mid/idx/sn
// （chksm 等是会话性易变参数，同一篇文章每次分享都不同，保留会让同文重复建单）；
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
	u.Scheme = "https"
	u.Host = host
	u.Fragment = ""
	q := u.Query()
	kept := url.Values{}
	for _, k := range []string{"__biz", "mid", "idx", "sn"} {
		if v := q.Get(k); v != "" {
			kept.Set(k, v)
		}
	}
	u.RawQuery = kept.Encode() // Encode 按 key 排序，同文不同参数顺序也归一
	// 长链形态（path=/s）却一个稳定参数都没有：规范化后会退化成裸 /s，
	// 不同文章互相撞 key——显式拒绝，让用户贴完整链接。
	if strings.TrimSuffix(u.Path, "/") == "/s" && u.RawQuery == "" {
		return "", errors.New("链接缺少文章定位参数（__biz/mid/idx/sn），请从公众号文章页复制完整链接")
	}
	return "site:" + u.String(), nil
}

// ---- 默认阶段实现（进程内复用既有内核，不复制第二份）----

// fetchSiteArticle 进程内复用 main.go 的抓取主路径 fetchDirectBlocksWithOptions
// （含限流重试）；x-reader Markdown 回退喂不进渲染器，此处按失败处理（fail loud）。
// caption 参数取网站渠道账号（ft-default）的规则配置，与人工导入同口径。
func fetchSiteArticle(rules *rulePresetStore, articleURL string) (string, string, string, string, error) {
	title, blocks, sourceName, byline, err := fetchDirectBlocksWithOptions(articleURL, articleBlockOptionsForAccount(rules, foodTalksSiteRuleAccountID))
	if err != nil {
		return "", "", "", "", fmt.Errorf("文章抓取失败: %w", err)
	}
	if len(blocks) == 0 {
		return "", "", "", "", errors.New("未能从文章解析出内容块（链接可能已过期或被限流，请稍后重试）")
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return "", "", "", "", fmt.Errorf("序列化内容块失败: %w", err)
	}
	return title, sourceName, byline, string(raw), nil
}

// renderSiteHTML 进程内复用 render_proxy.go 的转发内核，packId=ft-default。
func renderSiteHTML(ctx context.Context, title, source, blocksJSON string) (string, error) {
	token := renderToken()
	if token == "" {
		return "", errors.New("RENDER_TOKEN 未配置：无法调用渲染服务")
	}
	payload, err := json.Marshal(map[string]any{
		"title":    title,
		"source":   source,
		"blocks":   json.RawMessage(blocksJSON),
		"isRepost": true,
		"packId":   sitePublishRulePackID,
	})
	if err != nil {
		return "", fmt.Errorf("序列化渲染请求失败: %w", err)
	}
	req, err := newRenderServiceRequest(ctx, payload, "Bearer "+token)
	if err != nil {
		return "", err
	}
	resp, err := renderServiceHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("渲染服务未就绪（render-service 未启动或不可达）: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", fmt.Errorf("读渲染服务响应失败: %w", err)
	}
	var out struct {
		HTML  string `json:"html"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("渲染服务响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return "", fmt.Errorf("渲染服务错误（HTTP %d）: %s", resp.StatusCode, out.Error)
		}
		return "", fmt.Errorf("渲染服务错误（HTTP %d）", resp.StatusCode)
	}
	if strings.TrimSpace(out.HTML) == "" {
		return "", errors.New("渲染服务返回空 HTML")
	}
	return out.HTML, nil
}

// ossUploadByURLs 进程内复用 handleImageUpload 的上游代理内核（urls 模式：上游服务器
// 内网并发「取图+传 OSS」）。返回 {原url: oss_url}；上游取图失败的 URL 不在响应里，
// 缺失项的重试与 fail loud 由 stageMirror 负责。
func ossUploadByURLs(ctx context.Context, urls []string) (map[string]string, error) {
	endpoint := strings.TrimSpace(os.Getenv("WECHAT_UPLOAD_ENDPOINT"))
	if endpoint == "" {
		return nil, errors.New("WECHAT_UPLOAD_ENDPOINT 未配置：图片转存不可用")
	}
	body, err := json.Marshal(map[string]any{"urls": urls})
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
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
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

// runJob 从当前阶段推进任务直至 awaiting_review / 终态 / failed。任一阶段出错即显式
// 落败（stage=failed + failed_stage + error），retry 端点续跑；publicizing 也在 switch 内：
// 人 approve 后若公开动作失败，retry 从 publicizing 补完（方向已由人裁定，续跑不越权）。
func (p *publishPipeline) runJob(jobID int64) {
	ctx := context.Background()
	for {
		job, err := p.store.byID(jobID)
		if err != nil {
			log.Printf("publish: load job %d failed: %v", jobID, err)
			return
		}
		if job.terminal() || job.Stage == publishStageAwaitingReview || job.Stage == publishStageFailed ||
			job.Stage == publishStageStagingCreating || job.Stage == publishStageReconciliationRequired {
			return
		}
		var stageErr error
		switch job.Stage {
		case publishStageCreated:
			_, stageErr = p.store.setStage(jobID, publishStageFetching)
		case publishStageFetching:
			stageErr = p.stageFetch(ctx, job)
		case publishStageRendering:
			stageErr = p.stageRender(ctx, job)
		case publishStageMirroring:
			stageErr = p.stageMirror(ctx, job)
		case publishStageEnriching:
			stageErr = p.stageEnrich(ctx, job)
		case publishStageStaging:
			stageErr = p.stageStage(ctx, job)
		case publishStagePublicizing:
			stageErr = p.stagePublicize(ctx, job)
		default:
			stageErr = fmt.Errorf("未知任务阶段 %q", job.Stage)
		}
		if stageErr != nil {
			log.Printf("publish: job %d stage %s failed: %v", jobID, job.Stage, stageErr)
			if errors.Is(stageErr, errStagingOutcomeUncertain) {
				// 已经存在持久化 intent；普通 failed/retry 会制造第二篇网站文章。
				return
			}
			if _, err := p.store.setFailed(jobID, job.Stage, stageErr.Error()); err != nil {
				log.Printf("publish: record job %d failure failed: %v", jobID, err)
			}
			return
		}
	}
}

// stageFetch 抓公众号文章 → 标题 + 公众号名 + 结构化作者署名 + Block[] 落库。
// blocks 必须先落库再推进：公众号临时链接（含 chksm）会过期，后续阶段失败重试不再回源。
func (p *publishPipeline) stageFetch(ctx context.Context, job publishJob) error {
	title, sourceName, byline, blocksJSON, err := p.fetch(ctx, job.SourceURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(title) == "" {
		title = job.Title // 抓取无标题时保留任务上已有标题（人工补填路径）
	}
	if strings.TrimSpace(title) == "" {
		return errors.New("未能从文章解析出标题：请确认链接是公众号文章页，或稍后重试")
	}
	_, err = p.store.update(job.ID, func(j *publishJob) {
		j.Title = title
		j.SourceName = sourceName
		j.Author = byline // 页面结构化署名（可为空）；enriching 只在它为空时用 AI 兜底
		j.BlocksJSON = blocksJSON
		j.Stage = publishStageRendering
		j.FailedStage = ""
		j.Error = ""
	})
	return err
}

// stageRender blocks → ft-default 规则包内联 HTML（与网站人工排版同源）。
func (p *publishPipeline) stageRender(ctx context.Context, job publishJob) error {
	if strings.TrimSpace(job.BlocksJSON) == "" {
		return errors.New("任务缺少已抓取的内容块（blocks），无法排版；请重试从抓取阶段续跑")
	}
	rendered, err := p.render(ctx, job.Title, job.SourceURL, job.BlocksJSON)
	if err != nil {
		return err
	}
	_, err = p.store.update(job.ID, func(j *publishJob) {
		j.ContentHTML = rendered
		j.Stage = publishStageMirroring
		j.FailedStage = ""
		j.Error = ""
	})
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
		if _, err := p.store.update(job.ID, func(j *publishJob) { j.ImageMap = string(mapJSON) }); err != nil {
			return err
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
	_, err := p.store.update(job.ID, func(j *publishJob) {
		j.ContentHTML = finalHTML
		j.CoverURL = ossCover
		j.Stage = publishStageEnriching
		j.FailedStage = ""
		j.Error = ""
	})
	return err
}

// stageEnrich AI 预填发布参数（M5）：只产元数据（summary/tags/author/ai_notes +
// 可能覆盖 cover_url），**绝不碰 ContentHTML**。AI 未装配 / 调用失败 / 解析失败 →
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
	_, uerr := p.store.update(job.ID, func(j *publishJob) {
		if result.CoverURL != "" {
			j.CoverURL = result.CoverURL // AI 从候选里选中的封面覆盖默认第一图
		}
		j.Summary = result.Summary
		j.Tags = result.Tags
		if strings.TrimSpace(j.Author) == "" {
			// 结构化署名（fetching 从页面 meta/var author 直取）优先；AI 从正文
			// 识别只做兜底——发布者自己填的字段永远比模型猜的准（M7 用户裁定）。
			j.Author = result.Author
		}
		j.AISource = result.SourceName
		j.AINotes = result.Notes
		j.Stage = publishStageStaging
		j.FailedStage = ""
		j.Error = ""
	})
	return uerr
}

// stageStage 草稿进站：已有 news id 走幂等 PUT；无 id 时必须先持久化
// staging_creating intent 再 POST。FoodTalks 没有服务端幂等键，因此 POST 返回错误、
// 或成功后本地 id 落盘失败，都属于结果不确定态，只能人工核对，绝不自动 POST 第二次。
func (p *publishPipeline) stageStage(ctx context.Context, job publishJob) error {
	ft, err := p.foodTalks()
	if err != nil {
		return err
	}
	news, err := p.buildSiteNews(job, foodTalksPublishStatusDraft)
	if err != nil {
		return err
	}
	newsID := job.FoodTalksNewsID
	if newsID > 0 {
		// 幂等锚点已回填：全量覆盖更新既有草稿，绝不重复 CreateNews
		//（网站无删除接口，重复创建即垃圾数据）。
		news.ID = newsID
		if err := ft.UpdateNews(ctx, news); err != nil {
			return err
		}
		updated, err := p.store.update(job.ID, func(j *publishJob) {
			j.Stage = publishStageAwaitingReview
			j.FailedStage = ""
			j.Error = ""
		})
		if err != nil {
			return err
		}
		p.notifyReviewPending(ctx, updated)
		return nil
	}

	intentKey := fmt.Sprintf("foodtalks-create:job-%d", job.ID)
	intentAt := p.now().UTC().Format(time.RFC3339)
	intentJob, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageStaging && current.FoodTalksNewsID == 0 && current.StagingIntentKey == ""
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
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageStagingCreating && current.StagingIntentKey == intentKey && current.FoodTalksNewsID == 0
	}, func(j *publishJob) {
		j.FoodTalksNewsID = newsID
		j.Stage = publishStageAwaitingReview
		j.FailedStage = ""
		j.Error = ""
	})
	if err != nil {
		return fmt.Errorf("%w：FoodTalks 已返回资讯 id=%d，但本地锚点落盘失败（intent=%s）：%v", errStagingOutcomeUncertain, newsID, intentKey, err)
	}
	if !swapped {
		return fmt.Errorf("%w：FoodTalks 已返回资讯 id=%d，但任务状态已变为 %s（intent=%s）", errStagingOutcomeUncertain, newsID, updated.Stage, intentKey)
	}
	p.notifyReviewPending(ctx, updated)
	return nil
}

// notifyReviewPending 待审核通知：优先发交互审核卡（点按钮即 approve/reject），
// 卡片未装配或发送失败降级回原有文本通知（M4 铁律：降级保底，通知永不阻断流水线）。
func (p *publishPipeline) notifyReviewPending(ctx context.Context, job publishJob) {
	if p.notifyCard != nil {
		// 封面转存飞书图片素材拿 img_key（2026-07-10 用户要求封面直接在卡片呈现）；
		// 失败只降级为「卡片不带图」并 WARN，绝不因此丢整张卡。
		coverImgKey := ""
		if p.coverImgKey != nil && strings.TrimSpace(job.CoverURL) != "" {
			key, err := p.coverImgKey(ctx, job.CoverURL)
			if err != nil {
				log.Printf("publish: WARN 封面转存飞书失败（卡片将不带图）: %v", err)
			} else {
				coverImgKey = key
			}
		}
		err := p.notifyCard(ctx, buildReviewPendingCard(job, coverImgKey))
		if err == nil {
			return
		}
		log.Printf("publish: 审核卡片发送失败，降级文本通知: %v", err)
	}
	p.notifyReview(ctx, fmt.Sprintf(
		"【网站同步】待审核\n标题：%s\n原文：%s\n草稿已进站（外界不可见），FoodTalks 资讯 ID：%d\n请对发布任务 #%d 操作通过或驳回",
		job.Title, job.SourceURL, job.FoodTalksNewsID, job.ID))
}

// stagePublicize 翻公开：PUT 全量覆盖语义（契约 §7）——用与 staging 同一个 buildSiteNews
// 重建整份 News 再改 publishStatus=1，绝不能只传状态字段。
// ⚠️ 单向门（契约 §3）：发布过永远回不到草稿 0。本函数只能经 review(approve) 或
// approve 后的 failed 续跑（failed_stage=publicizing 的 retry）到达——用户红线。
func (p *publishPipeline) stagePublicize(ctx context.Context, job publishJob) error {
	if job.FoodTalksNewsID == 0 {
		return errors.New("缺少 FoodTalks 资讯 id（草稿进站未完成），无法公开")
	}
	ft, err := p.foodTalks()
	if err != nil {
		return err
	}
	news, err := p.buildSiteNews(job, foodTalksPublishStatusPublished)
	if err != nil {
		return err
	}
	news.ID = job.FoodTalksNewsID
	if err := ft.UpdateNews(ctx, news); err != nil {
		return err
	}
	articleURL := siteFrontURL() + "/news/" + strconv.FormatInt(job.FoodTalksNewsID, 10)
	if _, err := p.store.update(job.ID, func(j *publishJob) {
		j.ArticleURL = articleURL
		j.Stage = publishStagePublished
		j.FailedStage = ""
		j.Error = ""
	}); err != nil {
		return err
	}
	p.notifyReview(ctx, fmt.Sprintf("【网站同步】已发布\n标题：%s\n链接：%s", job.Title, articleURL))
	return nil
}

// review 人审动作核心：HTTP 端点（ADMIN_PASSWORD 门）与飞书卡片回调（verification
// token 门）共用这一个函数——审核语义与权限红线只在这里定义一次：仅 awaiting_review
// 可操作，approve 是触发翻公开的唯一路径。reviewedBy 是审核人留痕（卡片回调传
// operator.open_id，HTTP 端点传空），落 reviewed_by/reviewed_at 进终态卡与任务存储。
// reject 置终态 rejected——文章留在 FoodTalks 草稿态本就不可见，不额外动它，
// 且终态校验保证 reject 后不可再 approve。
func (p *publishPipeline) review(ctx context.Context, jobID int64, action, reviewedBy string) (publishJob, error) {
	if action != publishReviewApprove && action != publishReviewReject {
		job, err := p.store.byID(jobID)
		if err != nil {
			return publishJob{}, err
		}
		return job, fmt.Errorf("未知审核动作 %q（支持 approve / reject）", action)
	}
	reviewedAt := p.now().UTC().Format(time.RFC3339)
	switch action {
	case publishReviewApprove:
		job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
			return current.Stage == publishStageAwaitingReview && current.FoodTalksNewsID > 0
		}, func(j *publishJob) {
			j.Stage = publishStagePublicizing
			j.FailedStage = ""
			j.Error = ""
			j.ReviewedBy = reviewedBy
			j.ReviewedAt = reviewedAt
			j.ReviewAction = publishReviewApprove
		})
		if err != nil {
			return publishJob{}, err
		}
		if !swapped {
			return job, fmt.Errorf("任务当前阶段（%s）无法通过：仅待审核且草稿已进站的任务可发布", job.Stage)
		}
		if err := p.stagePublicize(ctx, job); err != nil {
			// 公开动作失败：显式落败（failed_stage=publicizing），retry 续跑补完（方向已裁定）。
			if _, _, ferr := p.store.updateIf(jobID, func(current publishJob) bool {
				return current.Stage == publishStagePublicizing && current.ReviewAction == publishReviewApprove && current.ReviewedAt != ""
			}, func(j *publishJob) {
				j.Stage = publishStageFailed
				j.FailedStage = publishStagePublicizing
				j.Error = err.Error()
			}); ferr != nil {
				log.Printf("publish: record publicize failure failed: job=%d err=%v", jobID, ferr)
			}
			latest, lerr := p.store.byID(jobID)
			if lerr != nil {
				latest = job
			}
			return latest, err
		}
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
		})
		if err != nil {
			return publishJob{}, err
		}
		if !swapped {
			return job, fmt.Errorf("任务当前阶段（%s）无法驳回：仅待审核任务可驳回", job.Stage)
		}
		p.notifyReview(ctx, fmt.Sprintf("【网站同步】已驳回\n标题：%s\n文章留在 FoodTalks 草稿态，不对外可见", job.Title))
	}
	return p.store.byID(jobID)
}

// retry failed 任务从 failed_stage 续跑（异步推进）。publicizing 的续跑不越权：
// failed_stage=publicizing 只可能在 approve 之后被记录（方向已由人裁定）。
func (p *publishPipeline) retry(jobID int64) (publishJob, error) {
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
		if target == publishStagePublicizing && (current.ReviewAction != publishReviewApprove || current.ReviewedAt == "") {
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
// approve + reviewed_at 才能恢复，绝不能仅凭 stage 字符串触发公开。
func (p *publishPipeline) recoverJobs() {
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 启动恢复扫描失败: %v", err)
		return
	}
	for _, job := range jobs {
		switch job.Stage {
		case publishStageCreated, publishStageFetching, publishStageRendering,
			publishStageMirroring, publishStageEnriching:
			p.spawn(func() { p.runJob(job.ID) })
		case publishStageStaging:
			if job.StagingIntentKey == "" || job.FoodTalksNewsID > 0 {
				p.spawn(func() { p.runJob(job.ID) })
				continue
			}
			p.markStagingReconciliation(job.ID, "任务在 staging 留有 CreateNews intent，无法确认外部结果，启动时禁止自动重发")
		case publishStageStagingCreating:
			p.markStagingReconciliation(job.ID, "进程在 CreateNews 调用窗口中断，外部结果不确定，启动时禁止自动重发")
		case publishStagePublicizing:
			if job.ReviewAction == publishReviewApprove && job.ReviewedAt != "" {
				p.spawn(func() { p.runJob(job.ID) })
				continue
			}
			_, _, updateErr := p.store.updateIf(job.ID, func(current publishJob) bool {
				return current.Stage == publishStagePublicizing && (current.ReviewAction != publishReviewApprove || current.ReviewedAt == "")
			}, func(j *publishJob) {
				j.Error = "publicizing 缺少已持久化的 approve/reviewed_at 授权凭据，启动时拒绝自动公开，请人工核对"
			})
			if updateErr != nil {
				log.Printf("publish: job %d 记录不安全 publicizing 状态失败: %v", job.ID, updateErr)
			}
		}
	}
}

func (p *publishPipeline) markStagingReconciliation(jobID int64, reason string) {
	_, _, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return (current.Stage == publishStageStagingCreating || current.Stage == publishStageStaging) &&
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
			j.Stage = publishStageAwaitingReview
			return
		}
		j.StagingIntentKey = ""
		j.StagingIntentAt = ""
		j.Stage = publishStageStaging
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return job, fmt.Errorf("任务当前阶段（%s）不需要 CreateNews 对账", job.Stage)
	}
	if newsID > 0 {
		p.notifyReviewPending(context.Background(), job)
	} else {
		p.spawn(func() { p.runJob(jobID) })
	}
	return job, nil
}

// submit source_key 永久幂等建单：任意阶段（含终态）同源任务都返回原单，绝不再建文。
func (p *publishPipeline) submit(sourceURL string) (publishJob, bool, error) {
	sourceKey, err := normalizeSiteSourceKey(sourceURL)
	if err != nil {
		return publishJob{}, false, err
	}
	job, created, err := p.store.create(sourceKey, sourceURL)
	if err != nil {
		return publishJob{}, false, err
	}
	if created {
		p.spawn(func() { p.runJob(job.ID) })
	}
	return job, created, nil
}

// notifyReview 发人审群通知；未装配或发送失败只降级记日志，流水线照常。
func (p *publishPipeline) notifyReview(ctx context.Context, text string) {
	if p.notify == nil {
		return
	}
	if err := p.notify(ctx, text); err != nil {
		log.Printf("publish: review notify failed: %v", err)
	}
}

// ---- News 组装与可配项 ----

// buildSiteNews News 组装唯一入口：staging（草稿）与 publicizing（公开）都从这一个函数
// 拿同一份全量字段——PUT 是全量覆盖语义（契约 §7），两处各自组装一旦漂移，公开时缺的
// 字段会被覆盖成零值。可配项集中在此走 env，绝不散落多处硬编码。
func (p *publishPipeline) buildSiteNews(job publishJob, publishStatus int) (foodTalksNews, error) {
	primary, categories, err := siteCategoryIDs()
	if err != nil {
		return foodTalksNews{}, err
	}
	// M7 来源判定（docs/editorial-norms-foodtalks.md §1/§2/§5，从库里 163 篇倒推）：
	// sourceType 是确定性规则不交模型——自家号清单命中=0（原创），否则=1（转载）；
	// 自家号转发他人文章时 AI 从正文声明抽出真实出处（job.AISource），此时按转载算。
	// author 不再回落 source_name：编辑部 132/163 篇作者为空、来源归 sourceName 字段。
	sourceType, sourceName := siteSourceFor(job)
	return foodTalksNews{
		Title:         job.Title,
		CoverImg:      job.CoverURL, // mirroring 后已是转存 OSS URL；enriching 可能换成 AI 选中图
		SourceType:    sourceType,
		SourceName:    sourceName,
		PublishStatus: publishStatus,
		IsPublic:      1, // ⚠️ 实测非可见性开关（契约 §4），恒 1；隐藏靠 publishStatus=0
		Author:        strings.TrimSpace(job.Author),
		Summary:       job.Summary, // M5 AI 摘要（兜底=正文前 100 字）
		// newsTags 恒空（后端裁定 2026-07-10）：标签走**单独接口**，news payload 本就
		// 不该带标签（带了必 500 且污染文章，实测矩阵见契约 §9）；发布先不标注。
		// AI 标签（job.Tags）只在审核卡片与预览页展示，词表（含 tagId）备在
		// api/editorial/tag_vocab.json，待标签接口文档到手再接。
		NewsTags:          []string{},
		Content:           job.ContentHTML, // mirroring 后成品（图片已全部 OSS 化；enriching 绝不改它）
		PublishTime:       sitePublishTime(job.CreatedAt, p.now()),
		PrimaryCategoryID: primary,
		CategoryIDs:       categories,
		Language:          "ZH", // 中文站固定
		SEOTitle:          job.Title + "-FoodTalks全球食品资讯",
	}, nil
}

// siteSourceFor 转载/原创判定（确定性规则，规范文档 §1：能用代码就不用模型）：
//  1. AI 从正文抽到出处声明且不是自家账号 → 转载，来源=声明出处（自家号转发他人文章）；
//  2. 公众号名在自家清单 → 原创，来源=公众号名；
//  3. 其余 → 转载，来源=公众号名（直接监控外部公众号的场景）。
//
// source_type 语义 0=原创/自有、1=转载，来自 mc-fb 库近一月 163 篇实测分布（规范 §1）。
func siteSourceFor(job publishJob) (int, string) {
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
	if src := strings.TrimSpace(job.AISource); src != "" && !own[src] {
		return 1, src
	}
	name := strings.TrimSpace(job.SourceName)
	if own[name] {
		return 0, name
	}
	return 1, name
}

// siteAdminArticleURL 管理后台文章编辑页链接（审核卡片「去后台编辑」用，2026-07-10
// 用户要求）：env 模板含 {id} 时替换为 FoodTalks newsId；未配置回落后台首页。
func siteAdminArticleURL(newsID int64) string {
	tpl := strings.TrimSpace(os.Getenv(siteAdminURLEnv))
	if tpl == "" {
		return defaultSiteAdminURL
	}
	if newsID > 0 {
		return strings.ReplaceAll(tpl, "{id}", strconv.FormatInt(newsID, 10))
	}
	return tpl
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
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return 0, nil, fmt.Errorf("分类配置 %s 非法（%q 不是数字）", siteCategoryIDsEnv, part)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return 0, nil, fmt.Errorf("分类配置 %s 为空：至少需要一个分类 id", siteCategoryIDsEnv)
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
