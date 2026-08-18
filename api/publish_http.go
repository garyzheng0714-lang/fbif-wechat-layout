package main

// publish_http.go — 发布流水线 HTTP 端点（施工图 S5）。机器提交/紧凑状态查询使用
// 独立 PUBLISH_SYNC_SERVICE_TOKEN；完整详情、重试与对账使用登录角色或 CI 专用操作令牌。
// HTTP 审核默认关闭，生产审批只接受飞书白名单审核人的持久化决策。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func requirePublishAdmin(w http.ResponseWriter, r *http.Request, auth *authService) bool {
	if validPublishAdminServiceToken(r.Header.Get(publishAdminServiceHeader)) {
		return true
	}
	if auth != nil {
		if _, err := auth.requireRuleAdmin(r); err != nil {
			writeRuleAdminAccessError(w, err)
			return false
		}
		return true
	}
	writeError(w, http.StatusUnauthorized, "authentication_required")
	return false
}

// rejectedPublishContentFields 返回请求里出现的、发布入口一律拒绝的内容类字段名。
//
// 红线（2026-08-06 用户裁定）：对外发布接口**只接收文章链接**，`site-sync` 与
// `official-sync` 同此一条，不留任何正文旁路。允许外部提交正文有两重代价：
//
//  1. 排版路径分叉。外部正文走 extractArticleBlocks（硬编码 defaultArticleBlockOptions），
//     自抓正文走 extractArticleBlocksWithOptions + articleBlockOptionsForAccount——
//     后者按账号规则包取图注阈值等参数，前者读不到。当前 rule_behavior.go 里这些
//     参数的默认值恰好等于硬编码默认值，所以差异要等有人在后台调过参数才显形；
//     但两条路径本身就不同，喂进来的还是官方 API 的二手 HTML（与网页版结构不同，
//     靠字号判定的 hasHeadingFontSize 小标题识别是否等价，无样本可证）。
//     用户报告的任务 #72 排版崩坏（长文零小标题、正文段落被排成灰色小字图注）
//     即出自这条正文路径；具体崩在哪一步未经复现，故此处只陈述机制不断言成因。
//  2. 内容可信度。外部可投递任意正文，而 publishContentRank 里 official_api/
//     supplier 的优先级高于自抓的 web_fetch，投什么就发什么。
//
// 因此两个入口共用这一份判定（ADR 四层模型 L1：能力代码只此一份，杜绝复制漂移）。
// 静默忽略比拒绝更危险：调用方会以为自己传的正文生效了。
func rejectedPublishContentFields(contentHTML, title, sourceName, author, coverURL string) []string {
	candidates := []struct {
		name  string
		value string
	}{
		{"content_html", contentHTML},
		{"title", title},
		{"source_name", sourceName},
		{"author", author},
		{"cover_url", coverURL},
	}
	var rejected []string
	for _, c := range candidates {
		if strings.TrimSpace(c.value) != "" {
			rejected = append(rejected, c.name)
		}
	}
	return rejected
}

// publishContentRejectionMessage 两个发布入口共用的拒绝文案：说清楚被挡的是哪些
// 字段，以及正文究竟由谁负责——让调用方能自己改对，而不是来回猜。
func publishContentRejectionMessage(rejected []string) string {
	return fmt.Sprintf(
		"本接口只接收文章链接，请移除 %s。正文、标题、来源、作者与封面一律由排版服务从原文现抓，"+
			"外部提交的内容不会被采用。", strings.Join(rejected, "、"))
}

func publishAdminActor(r *http.Request, auth *authService) string {
	if auth != nil {
		if user, err := auth.requireFeishuUser(r); err == nil && strings.TrimSpace(user.FeishuOpenID) != "" {
			return user.FeishuOpenID
		}
	}
	return "admin"
}

func registerPublishRoutes(mux *http.ServeMux, p *publishPipeline, authServices ...*authService) {
	var auth *authService
	if len(authServices) > 0 {
		auth = authServices[0]
	}
	mux.HandleFunc("POST /api/publish/monitor-alert", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishServiceToken(w, r) {
			return
		}
		var req publishMonitorAlertRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid monitoring alert")
			return
		}
		message, err := formatPublishMonitorAlert(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if p.monitorNotify == nil {
			writeError(w, http.StatusServiceUnavailable, "monitoring private reporter is not configured")
			return
		}
		if err := p.monitorNotify(r.Context(), message); err != nil {
			writeError(w, http.StatusBadGateway, "monitoring alert delivery failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"delivered": true})
	})

	mux.HandleFunc("GET /api/publish/monitoring", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishSubmitOrAdmin(w, r, auth) {
			return
		}
		writeJSON(w, http.StatusOK, currentPublishMonitoring(p.store, time.Now()))
	})

	mux.HandleFunc("GET /api/publish/readiness", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		writeJSON(w, http.StatusOK, currentPublishReadiness(p))
	})

	// 发布链路配置只返回应用名称和当前登录用户可选的群 ID；应用密钥永不
	// 出现在响应里，群名称也只来自 /api/feishu/chats 的实时目录。
	mux.HandleFunc("GET /api/publish/delivery-settings", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		store := p.deliverySettings
		if store == nil {
			store = newPublishDeliverySettingsStore("")
		}
		settings, apps, err := store.resolve()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, publishDeliverySettingsView(settings, apps))
	})

	mux.HandleFunc("PUT /api/publish/delivery-settings", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		if auth == nil {
			writeError(w, http.StatusServiceUnavailable, "发布链路配置需要飞书登录服务")
			return
		}
		user, err := auth.requireFeishuUser(r)
		if err != nil {
			writeFeishuCapabilityError(w, err)
			return
		}
		chats, err := auth.listFeishuChats(r.Context(), user)
		if err != nil {
			writeFeishuCapabilityError(w, err)
			return
		}
		var body struct {
			AppProfileID string   `json:"app_profile_id"`
			ChatIDs      []string `json:"chat_ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if p.deliverySettings == nil {
			writeError(w, http.StatusServiceUnavailable, "发布通知配置存储未初始化")
			return
		}
		current, apps, err := p.deliverySettings.resolve()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		settings, err := publishDeliverySelectionFromRequest(body, current, apps, chats, user.FeishuOpenID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := p.deliverySettings.write(settings); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, publishDeliverySettingsView(settings, apps))
	})

	// 提交同步任务：source_key 幂等（已有未终态 job 返回它）→ 建 job → goroutine 异步推进。
	mux.HandleFunc("POST /api/publish/site-sync", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishSubmitOrAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		var req struct {
			URL               string `json:"url"`
			RulePackID        string `json:"rule_pack_id"`
			RuleID            string `json:"rule_id,omitempty"` // 兼容监控方更短的字段名；内部统一为 rule_pack_id
			ContentKind       string `json:"content_kind"`
			Classification    string `json:"classification"`
			ClassifierVersion string `json:"classifier_version"`
			RerenderLatest    bool   `json:"rerender_latest"`
			RefetchLatest     bool   `json:"refetch_latest"`
			// 官方接口「阅读原文」：不是正文旁路，只补出处链接。
			ContentSourceURL  string `json:"content_source_url"`
			OriginalSourceURL string `json:"original_source_url"`

			// 以下字段只为「显式拒绝」而保留，绝不参与排版（红线，见函数头注释）。
			// 静默忽略比拒绝更危险：调用方会以为自己传的正文生效了。
			ContentHTML string `json:"content_html"`
			Title       string `json:"title"`
			SourceName  string `json:"source_name"`
			Author      string `json:"author"`
			CoverURL    string `json:"cover_url"`
		}
		// 只收链接，正文入口关闭 → body 上限从 8MB 收到 64KB，超限即 400。
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.URL == "" {
			writeError(w, http.StatusBadRequest, "url is required")
			return
		}
		if rejected := rejectedPublishContentFields(
			req.ContentHTML, req.Title, req.SourceName, req.Author, req.CoverURL,
		); len(rejected) > 0 {
			writeError(w, http.StatusBadRequest, publishContentRejectionMessage(rejected))
			return
		}
		rulePackID, ruleErr := normalizeSitePublishRulePackID(firstNonEmpty(req.RulePackID, req.RuleID))
		if ruleErr != nil {
			writeError(w, http.StatusBadRequest, ruleErr.Error())
			return
		}
		machineRequest := strings.TrimSpace(r.Header.Get(publishSyncServiceHeader)) != ""
		if machineRequest {
			// 正文入口已关闭，机器提交恒为 URL-only，证据校验不再看正文。
			if evidenceErr := validatePublishMachineEvidence(req.ContentKind, req.Classification, req.ClassifierVersion, ""); evidenceErr != nil {
				writeError(w, http.StatusUnprocessableEntity, evidenceErr.Error())
				return
			}
		}
		job, created, err := p.submitWithRulePackAndMetadataContext(
			r.Context(), req.URL, rulePackID, req.Classification, req.ClassifierVersion,
			firstNonEmpty(req.ContentSourceURL, req.OriginalSourceURL))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		explicitRulePack := strings.TrimSpace(req.RulePackID) != "" || strings.TrimSpace(req.RuleID) != ""
		if !created && explicitRulePack && firstNonEmpty(job.RulePackID, sitePublishRulePackID) != rulePackID && !req.RerenderLatest {
			writeError(w, http.StatusConflict, fmt.Sprintf("同一原文已绑定规则包 %q；如需切换规则，请同时设置 rerender_latest=true", firstNonEmpty(job.RulePackID, sitePublishRulePackID)))
			return
		}
		rerendered := false
		if (req.RerenderLatest || req.RefetchLatest) && !created {
			rerenderPackID := rulePackID
			if !explicitRulePack {
				rerenderPackID = firstNonEmpty(job.RulePackID, sitePublishRulePackID)
			}
			if req.RefetchLatest {
				job, err = p.refetchLatestWithRulePack(r.Context(), job.ID, rerenderPackID)
			} else if !explicitRulePack {
				job, err = p.rerenderLatest(job.ID)
			} else {
				job, err = p.rerenderLatestWithRulePack(job.ID, rerenderPackID)
			}
			if err != nil {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			rerendered = true
		}
		if req.Classification != "" {
			job, err = p.store.update(job.ID, func(current *publishJob) {
				if current.Classification == "" || current.Classification == "unknown" {
					current.Classification = req.Classification
					current.Classifier = req.ClassifierVersion
				}
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{
			"job": newPublishSyncJobSummary(job), "existing": !created, "rerendered": rerendered,
		})
	})

	// 微信官方来源的已发布文章提交口。**只接收链接**（红线，见
	// rejectedPublishContentFields）：正文由排版服务按 rule_pack_id 现抓现排，与
	// 用户在网页工具粘链接完全同一条解析路径，杜绝二手 HTML 造成的排版失真。
	//
	// 与 site-sync 的唯一区别是身份证据：调用方已用官方草稿类型确认这是普通图文，
	// 因此这里把 classification 钉成 ordinary_confirmed，防止小绿书/图集误投
	// （红线 29 fail closed）。除此之外两个入口的排版行为必须一致。
	mux.HandleFunc("POST /api/publish/official-sync", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishSubmitOrAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		var req struct {
			URL               string `json:"url"`
			RulePackID        string `json:"rule_pack_id"`
			RuleID            string `json:"rule_id,omitempty"`
			ContentKind       string `json:"content_kind"`
			Classification    string `json:"classification"`
			ClassifierVersion string `json:"classifier_version"`
			ContentSourceURL  string `json:"content_source_url"`
			OriginalSourceURL string `json:"original_source_url"`

			// 以下字段只为「显式拒绝」而保留，绝不参与排版（红线）。
			ContentHTML string `json:"content_html"`
			Title       string `json:"title"`
			SourceName  string `json:"source_name"`
			Author      string `json:"author"`
			CoverURL    string `json:"cover_url"`
		}
		// 只收链接，正文入口关闭 → body 上限从 8MB 收到 64KB，超限即 400。
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if strings.TrimSpace(req.URL) == "" {
			writeError(w, http.StatusBadRequest, "url is required")
			return
		}
		if rejected := rejectedPublishContentFields(
			req.ContentHTML, req.Title, req.SourceName, req.Author, req.CoverURL,
		); len(rejected) > 0 {
			writeError(w, http.StatusBadRequest, publishContentRejectionMessage(rejected))
			return
		}
		rulePackID, ruleErr := normalizeSitePublishRulePackID(firstNonEmpty(req.RulePackID, req.RuleID))
		if ruleErr != nil {
			writeError(w, http.StatusBadRequest, ruleErr.Error())
			return
		}
		machineRequest := strings.TrimSpace(r.Header.Get(publishSyncServiceHeader)) != ""
		if machineRequest {
			// 正文入口已关闭，机器提交恒为 URL-only，证据校验不再看正文。
			if evidenceErr := validatePublishMachineEvidence(req.ContentKind, req.Classification, req.ClassifierVersion, ""); evidenceErr != nil {
				writeError(w, http.StatusUnprocessableEntity, evidenceErr.Error())
				return
			}
		}
		job, created, err := p.submitWithRulePackAndMetadataContext(
			r.Context(), req.URL, rulePackID, req.Classification, req.ClassifierVersion,
			firstNonEmpty(req.ContentSourceURL, req.OriginalSourceURL))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		classifier := strings.TrimSpace(req.ClassifierVersion)
		if classifier == "" {
			classifier = "wechat-official-api-v1"
		}
		job, err = p.store.update(job.ID, func(current *publishJob) {
			current.Classification = "ordinary_confirmed"
			current.Classifier = classifier
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{"job": newPublishSyncJobSummary(job), "existing": !created})
	})

	// 同步中心批量读取紧凑状态。只返回跨系统编排所需字段，不返回正文、blocks、
	// 图片映射、风险模型原文或通知载荷。
	mux.HandleFunc("POST /api/publish/sync-status", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishSubmitOrAdmin(w, r, auth) {
			return
		}
		var req struct {
			JobIDs []int64 `json:"job_ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if len(req.JobIDs) == 0 || len(req.JobIDs) > 200 {
			writeError(w, http.StatusBadRequest, "job_ids must contain 1 to 200 ids")
			return
		}
		wanted := make(map[int64]struct{}, len(req.JobIDs))
		for _, id := range req.JobIDs {
			if id <= 0 {
				writeError(w, http.StatusBadRequest, "job_ids must be positive integers")
				return
			}
			wanted[id] = struct{}{}
		}
		jobs, err := p.store.all()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		summaries := make([]publishSyncJobSummary, 0, len(wanted))
		found := make(map[int64]struct{}, len(wanted))
		for _, job := range jobs {
			if _, ok := wanted[job.ID]; !ok {
				continue
			}
			summaries = append(summaries, newPublishSyncJobSummary(job))
			found[job.ID] = struct{}{}
		}
		missing := make([]int64, 0)
		for _, id := range req.JobIDs {
			if _, ok := found[id]; !ok {
				missing = append(missing, id)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": summaries, "missing_ids": missing})
	})

	mux.HandleFunc("GET /api/publish/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		jobs, err := p.store.list(limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
	})

	mux.HandleFunc("GET /api/publish/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		job, err := p.store.byID(id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
	})

	// 人审：approve 只原子落 publicizing/授权凭据并立即返回，后台 worker 执行唯一
	// 不可逆的翻公开动作；reject 置终态。仅 awaiting_review 可操作。
	mux.HandleFunc("POST /api/publish/jobs/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		if !publishHTTPReviewEnabled() {
			http.NotFound(w, r)
			return
		}
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		reviewer := publishHTTPReviewerOpenID()
		if !feishuReviewerAllowed(reviewer) {
			writeError(w, http.StatusServiceUnavailable, "HTTP review reviewer is not configured in the reviewer allowlist")
			return
		}
		job, err := p.review(r.Context(), id, req.Action, reviewer)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": job})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
	})

	mux.HandleFunc("POST /api/publish/jobs/{id}/unpublish", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		var req struct {
			LifecycleVersion string `json:"lifecycle_version"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
		}
		job, err := p.requestUnpublish(id, publishAdminActor(r, auth), req.LifecycleVersion)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": job})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
	})

	mux.HandleFunc("POST /api/publish/jobs/{id}/republish", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		var req struct {
			LifecycleVersion string `json:"lifecycle_version"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
		}
		job, err := p.requestRepublish(id, publishAdminActor(r, auth), req.LifecycleVersion)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": job})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
	})

	// failed 任务从 failed_stage 续跑（异步）。
	mux.HandleFunc("POST /api/publish/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		job, err := p.retry(id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": job})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
	})

	// 按任务编号用已存 blocks 走最新规则重排。短链原文过期时 site-sync
	// 会在建单前去解析 __biz/mid/idx 并 400，这条入口不回源，专给待审/可重排失败单续跑。
	mux.HandleFunc("POST /api/publish/jobs/{id}/rerender-latest", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		job, err := p.rerenderLatest(id)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "job": newPublishSyncJobSummary(job)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": newPublishSyncJobSummary(job), "rerendered": true})
	})

	// 测试站时期的存量待审单：正式站上同一资讯 id 已是另一篇文章。
	// 先摘掉旧锚点，再按已存正文块重排并在当前环境新建隐藏草稿。
	mux.HandleFunc("POST /api/publish/jobs/{id}/restage-current", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		job, err := p.restageCurrentAndRerender(id)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "job": newPublishSyncJobSummary(job)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": newPublishSyncJobSummary(job), "restaged": true, "rerendered": true})
	})

	// 无法继续的失败单结案：原文失效或测试站旧锚点不再处理。结案后不再点亮 24 小时失败铃。
	mux.HandleFunc("POST /api/publish/jobs/{id}/abandon", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
		}
		job, err := p.abandonFailedJob(id, req.Reason)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "job": newPublishSyncJobSummary(job)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": newPublishSyncJobSummary(job), "abandoned": true})
	})

	// 用当前代码重出审核卡。卡片改版后，历史任务的卡还停在旧样式上，群里回看
	// 到的仍是旧信息；已记过 message_id 的群走原位更新，不会刷屏。
	// 与 notification-replay 的分工见 refreshReviewCard 注释。
	mux.HandleFunc("POST /api/publish/jobs/{id}/card-refresh", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		// ?resend=1：撤回群里旧卡并发一张全新卡（默认仍是原位更新不刷屏）。
		resend := r.URL.Query().Get("resend") == "1"
		job, err := p.refreshReviewCard(r.Context(), id, resend)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": newPublishSyncJobSummary(job)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": newPublishSyncJobSummary(job), "refreshed": true})
	})

	// 对已公开任务再 PUT 一次 FoodTalks：补 isLatest（首页）和 sourceId（来源出处）。
	// 不改本地状态机、不发飞书通知。day 为北京时间 YYYY-MM-DD。
	mux.HandleFunc("POST /api/publish/cms-resync", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		var req struct {
			Day string `json:"day"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
		}
		if strings.TrimSpace(req.Day) == "" {
			req.Day = r.URL.Query().Get("day")
		}
		day, err := parseCMSResyncDayCST(req.Day)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		report, err := p.resyncPublishedCMSDay(r.Context(), day)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, report)
	})

	// 已公开稿若带着微信导流尾，按当前规则重排后覆盖 FoodTalks。不改审核状态。
	mux.HandleFunc("POST /api/publish/jobs/{id}/repair-wechat-trailer", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		job, err := p.repairPublishedWechatTrailer(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": newPublishSyncJobSummary(job)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": newPublishSyncJobSummary(job), "repaired": true})
	})

	mux.HandleFunc("POST /api/publish/repair-wechat-trailer", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		report, err := p.repairPublishedWechatTrailerLeaks(r.Context(), 0)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "report": report})
			return
		}
		writeJSON(w, http.StatusOK, report)
	})

	// 对账中的外发：管理员确认旧云梭稿作废后，用当前 payload 再推一次。
	// 飞书卡片没有这个按钮，避免误点造成重复稿。
	mux.HandleFunc("POST /api/publish/jobs/{id}/retry-external-distribution", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		job, err := p.replaceExternalDistributionAfterReconcile(id, publishExternalAdminRetryActor)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": newPublishSyncJobSummary(job)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": newPublishSyncJobSummary(job), "retried": true})
	})

	// 用当前 prompt 重跑风险检查（只对待审核任务）。card-refresh 只换样式、用的还是
	// 旧结论；prompt 改版后想让新检查能力作用在存量文章上，只能走这里真调一次模型。
	mux.HandleFunc("POST /api/publish/jobs/{id}/risk-recheck", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		// 不用 r.Context()：模型通读全文要一到几分钟，而网关会在 60 秒左右切断连接，
		// 请求 context 一取消，ARK 调用就跟着 context canceled、落进 fallback。
		// 用独立 context 让检查跑完，客户端拿不到响应也没关系——结论已落库，
		// 卡片也已刷新（2026-08-08 首次真机重跑踩到）。
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 6*time.Minute)
		defer cancel()
		job, err := p.recheckPublishRisk(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": newPublishSyncJobSummary(job)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"job": newPublishSyncJobSummary(job), "rechecked": true,
			"risk_level": job.RiskLevel, "risk_headline": job.RiskHeadline,
			"risk_items": job.RiskItems,
		})
	})

	mux.HandleFunc("POST /api/publish/jobs/{id}/notification-replay", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		job, err := p.replayPublishNotification(id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": job})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
	})

	// CreateNews 结果不确定态的人工对账。运营在 FoodTalks 后台核对后，只能二选一：
	// 回填已存在 news id；或明确确认未创建，才重新进入 staging 发起新 POST。
	mux.HandleFunc("POST /api/publish/jobs/{id}/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		var req struct {
			FoodTalksNewsID       int64 `json:"foodtalks_news_id"`
			ConfirmNotCreated     bool  `json:"confirm_not_created"`
			ConfirmPublished      bool  `json:"confirm_published"`
			ConfirmNotPublished   bool  `json:"confirm_not_published"`
			ConfirmOffline        bool  `json:"confirm_offline"`
			ConfirmStillPublished bool  `json:"confirm_still_published"`
			ConfirmStillOffline   bool  `json:"confirm_still_offline"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var job publishJob
		var err error
		current, loadErr := p.store.byID(id)
		if loadErr != nil {
			writeError(w, http.StatusNotFound, loadErr.Error())
			return
		}
		switch current.Stage {
		case publishStageUnpublishReconciliationRequired:
			job, err = p.reconcileUnpublish(id, req.ConfirmOffline, req.ConfirmStillPublished)
		case publishStageRepublishReconciliationRequired:
			job, err = p.reconcileRepublish(id, req.ConfirmPublished, req.ConfirmStillOffline)
		case publishStagePublicizeReconciliationRequired:
			job, err = p.reconcilePublicize(id, req.ConfirmPublished, req.ConfirmNotPublished)
		default:
			job, err = p.reconcileStaging(id, req.FoodTalksNewsID, req.ConfirmNotCreated)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": job})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
	})
}

type publishSyncJobSummary struct {
	ID                   int64    `json:"id"`
	SourceKey            string   `json:"source_key"`
	SourceURL            string   `json:"source_url"`
	Title                string   `json:"title,omitempty"`
	RulePackID           string   `json:"rule_pack_id"`
	Stage                string   `json:"stage"`
	FailedStage          string   `json:"failed_stage,omitempty"`
	Error                string   `json:"error,omitempty"`
	FoodTalksNewsID      int64    `json:"foodtalks_news_id,omitempty"`
	ArticleURL           string   `json:"article_url,omitempty"`
	PublishedAt          string   `json:"published_at,omitempty"`
	UnpublishedAt        string   `json:"unpublished_at,omitempty"`
	UnpublishWindowHours int      `json:"unpublish_window_hours,omitempty"`
	UnpublishWindowUntil string   `json:"unpublish_window_until,omitempty"`
	PublicationAction    string   `json:"publication_action,omitempty"`
	PublicationActor     string   `json:"publication_actor,omitempty"`
	PublicationActionAt  string   `json:"publication_action_at,omitempty"`
	SupersededByJobID    int64    `json:"superseded_by_job_id,omitempty"`
	ReviewAction         string   `json:"review_action,omitempty"`
	ReviewedBy           string   `json:"reviewed_by,omitempty"`
	ReviewedAt           string   `json:"reviewed_at,omitempty"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
	Retryable            bool     `json:"retryable"`
	RetryBlockReason     string   `json:"retry_block_reason,omitempty"`
	ReconcileRequired    bool     `json:"reconcile_required"`
	AvailableActions     []string `json:"available_actions"`
}

func newPublishSyncJobSummary(job publishJob) publishSyncJobSummary {
	retryable, retryBlockReason := publishJobRetryability(job)
	reconcileRequired := job.Stage == publishStageReconciliationRequired ||
		job.Stage == publishStagePublicizeReconciliationRequired ||
		job.Stage == publishStageStagingCreating || job.Stage == publishStagePublicizingUpdating ||
		job.Stage == publishStageUnpublishReconciliationRequired || job.Stage == publishStageRepublishReconciliationRequired ||
		job.Stage == publishStageUnpublishingUpdating || job.Stage == publishStageRepublishingUpdating
	actions := make([]string, 0, 1)
	switch {
	case retryable:
		actions = append(actions, "retry")
	case reconcileRequired:
		actions = append(actions, "reconcile")
	case job.Stage == publishStageAwaitingReview:
		actions = append(actions, "review_in_feishu")
	case job.Stage == publishStagePublished && publishUnpublishWindowOpen(job, time.Now().UTC()):
		actions = append(actions, "unpublish")
	case job.Stage == publishStageOffline && validPersistedPublishApproval(job):
		actions = append(actions, "republish")
	case job.Stage == publishStageSupersededDuplicate && job.SupersededByJobID > 0:
		actions = append(actions, "open_winner")
	}
	return publishSyncJobSummary{
		ID: job.ID, SourceKey: job.SourceKey, SourceURL: job.SourceURL, Title: job.Title,
		RulePackID: firstNonEmpty(job.RulePackID, sitePublishRulePackID),
		Stage:      job.Stage, FailedStage: job.FailedStage, Error: truncateRunes(job.Error, 500),
		FoodTalksNewsID: job.FoodTalksNewsID, ArticleURL: job.ArticleURL,
		PublishedAt: job.PublishedAt, UnpublishedAt: job.UnpublishedAt,
		UnpublishWindowHours: job.UnpublishWindowHours, UnpublishWindowUntil: job.UnpublishWindowUntil,
		PublicationAction: job.PublicationAction, PublicationActor: job.PublicationActor,
		PublicationActionAt: job.PublicationActionAt,
		SupersededByJobID:   job.SupersededByJobID,
		ReviewAction:        job.ReviewAction, ReviewedBy: job.ReviewedBy, ReviewedAt: job.ReviewedAt,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, Retryable: retryable,
		RetryBlockReason: retryBlockReason, ReconcileRequired: reconcileRequired,
		AvailableActions: actions,
	}
}

func publishJobRetryability(job publishJob) (bool, string) {
	if job.Stage != publishStageFailed {
		return false, "job is not in failed stage"
	}
	target := job.FailedStage
	if target == "" || target == publishStageCreated {
		target = publishStageFetching
	}
	if target == publishStagePublicizing &&
		(!validPersistedPublishApproval(job) || job.PublicizeIntentKey != "") {
		return false, "publicize approval is missing, stale, or already entered the external-call window"
	}
	if target == publishStageUnpublishing {
		if job.UnpublishIntentKey != "" || job.RepublishIntentKey != "" {
			return false, "unpublish intent is already in the external-call window"
		}
		if !publishUnpublishWindowOpen(job, time.Now().UTC()) {
			return false, "unpublish window has expired"
		}
	}
	if target == publishStageRepublishing {
		if job.UnpublishIntentKey != "" || job.RepublishIntentKey != "" || !validPersistedPublishApproval(job) {
			return false, "republish approval is missing, stale, or already entered the external-call window"
		}
	}
	if target == publishStageStaging && job.StagingIntentKey != "" && job.FoodTalksNewsID == 0 {
		return false, "CreateNews may already have been executed; reconciliation is required"
	}
	return true, ""
}

func publishJobIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "任务 id 必须是正整数")
		return 0, false
	}
	return id, true
}
