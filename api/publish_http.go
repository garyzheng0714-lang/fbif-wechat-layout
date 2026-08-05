package main

// publish_http.go — 发布流水线 HTTP 端点（施工图 S5）。机器提交/紧凑状态查询使用
// 独立 PUBLISH_SYNC_SERVICE_TOKEN；完整详情、重试与对账使用登录角色或 CI 专用操作令牌。
// HTTP 审核默认关闭，生产审批只接受飞书白名单审核人的持久化决策。

import (
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
			Title             string `json:"title"`
			SourceName        string `json:"source_name"`
			Author            string `json:"author"`
			ContentHTML       string `json:"content_html"`
			CoverURL          string `json:"cover_url"`
			ContentKind       string `json:"content_kind"`
			Classification    string `json:"classification"`
			ClassifierVersion string `json:"classifier_version"`
			RerenderLatest    bool   `json:"rerender_latest"`
			RefetchLatest     bool   `json:"refetch_latest"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil || req.URL == "" {
			writeError(w, http.StatusBadRequest, "url is required")
			return
		}
		rulePackID, ruleErr := normalizeSitePublishRulePackID(firstNonEmpty(req.RulePackID, req.RuleID))
		if ruleErr != nil {
			writeError(w, http.StatusBadRequest, ruleErr.Error())
			return
		}
		machineRequest := strings.TrimSpace(r.Header.Get(publishSyncServiceHeader)) != ""
		if machineRequest {
			if evidenceErr := validatePublishMachineEvidence(req.ContentKind, req.Classification, req.ClassifierVersion, req.ContentHTML); evidenceErr != nil {
				writeError(w, http.StatusUnprocessableEntity, evidenceErr.Error())
				return
			}
		}
		if req.RefetchLatest && strings.TrimSpace(req.ContentHTML) != "" {
			writeError(w, http.StatusBadRequest, "refetch_latest 只能与 URL-only 提交一起使用")
			return
		}
		var job publishJob
		var created bool
		var restarted bool
		var err error
		if strings.TrimSpace(req.ContentHTML) != "" {
			job, created, restarted, err = p.submitSupplierWithRestartContext(r.Context(), officialPublishInput{
				URL: req.URL, Title: req.Title, SourceName: req.SourceName,
				Author: req.Author, ContentHTML: req.ContentHTML, CoverURL: req.CoverURL,
				RulePackID: rulePackID, Classification: req.Classification, ClassifierVersion: req.ClassifierVersion,
			})
		} else {
			job, created, err = p.submitWithRulePackAndMetadataContext(r.Context(), req.URL, rulePackID, req.Classification, req.ClassifierVersion)
		}
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
			if restarted {
				// A changed structured payload already restarted this same job.
				rerendered = true
			} else {
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

	// 微信官方 API 正文直入：不抓公众号网页，直接把官方返回的 HTML 转 blocks 后
	// 进入 ft-default 排版、图片转存、风险检查和待审核草稿流程。
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
			Title             string `json:"title"`
			SourceName        string `json:"source_name"`
			Author            string `json:"author"`
			ContentHTML       string `json:"content_html"`
			CoverURL          string `json:"cover_url"`
			ContentKind       string `json:"content_kind"`
			Classification    string `json:"classification"`
			ClassifierVersion string `json:"classifier_version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		rulePackID, ruleErr := normalizeSitePublishRulePackID(firstNonEmpty(req.RulePackID, req.RuleID))
		if ruleErr != nil {
			writeError(w, http.StatusBadRequest, ruleErr.Error())
			return
		}
		machineRequest := strings.TrimSpace(r.Header.Get(publishSyncServiceHeader)) != ""
		if machineRequest {
			if evidenceErr := validatePublishMachineEvidence(req.ContentKind, req.Classification, req.ClassifierVersion, req.ContentHTML); evidenceErr != nil {
				writeError(w, http.StatusUnprocessableEntity, evidenceErr.Error())
				return
			}
		}
		job, created, err := p.submitOfficialWithContext(r.Context(), officialPublishInput{
			URL: req.URL, Title: req.Title, SourceName: req.SourceName,
			Author: req.Author, ContentHTML: req.ContentHTML, CoverURL: req.CoverURL,
			RulePackID: rulePackID, Classification: req.Classification, ClassifierVersion: req.ClassifierVersion,
		})
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
