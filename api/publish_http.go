package main

// publish_http.go — 发布流水线 HTTP 端点（施工图 S5）。机器提交/紧凑状态查询使用
// 独立 PUBLISH_SYNC_SERVICE_TOKEN；完整详情、重试与对账仍在 ADMIN_PASSWORD 管理门后。
// HTTP 审核默认关闭，生产审批只接受飞书白名单审核人的持久化决策。

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func registerPublishRoutes(mux *http.ServeMux, p *publishPipeline) {
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
		if !requirePublishSubmitOrAdmin(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, currentPublishMonitoring(p.store, time.Now()))
	})

	mux.HandleFunc("GET /api/publish/readiness", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, currentPublishReadiness(p))
	})

	// 提交同步任务：source_key 幂等（已有未终态 job 返回它）→ 建 job → goroutine 异步推进。
	mux.HandleFunc("POST /api/publish/site-sync", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishSubmitOrAdmin(w, r) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		var req struct {
			URL               string `json:"url"`
			Title             string `json:"title"`
			SourceName        string `json:"source_name"`
			Author            string `json:"author"`
			ContentHTML       string `json:"content_html"`
			CoverURL          string `json:"cover_url"`
			ContentKind       string `json:"content_kind"`
			Classification    string `json:"classification"`
			ClassifierVersion string `json:"classifier_version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil || req.URL == "" {
			writeError(w, http.StatusBadRequest, "url is required")
			return
		}
		machineRequest := strings.TrimSpace(r.Header.Get(publishSyncServiceHeader)) != ""
		if machineRequest {
			if evidenceErr := validatePublishMachineEvidence(req.ContentKind, req.Classification, req.ClassifierVersion, req.ContentHTML); evidenceErr != nil {
				writeError(w, http.StatusUnprocessableEntity, evidenceErr.Error())
				return
			}
		}
		var job publishJob
		var created bool
		var err error
		if strings.TrimSpace(req.ContentHTML) != "" {
			job, created, err = p.submitSupplierWithContext(r.Context(), officialPublishInput{
				URL: req.URL, Title: req.Title, SourceName: req.SourceName,
				Author: req.Author, ContentHTML: req.ContentHTML, CoverURL: req.CoverURL,
			})
		} else {
			job, created, err = p.submitWithContext(r.Context(), req.URL)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
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
		writeJSON(w, status, map[string]any{"job": newPublishSyncJobSummary(job), "existing": !created})
	})

	// 微信官方 API 正文直入：不抓公众号网页，直接把官方返回的 HTML 转 blocks 后
	// 进入 ft-default 排版、图片转存、风险检查和待审核草稿流程。
	mux.HandleFunc("POST /api/publish/official-sync", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishSubmitOrAdmin(w, r) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		var req struct {
			URL               string `json:"url"`
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
		if !requirePublishSubmitOrAdmin(w, r) {
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
		if !requireAdminPassword(w, r) {
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
		if !requireAdminPassword(w, r) {
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
		if !requireAdminPassword(w, r) {
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

	// failed 任务从 failed_stage 续跑（异步）。
	mux.HandleFunc("POST /api/publish/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
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

	// CreateNews 结果不确定态的人工对账。运营在 FoodTalks 后台核对后，只能二选一：
	// 回填已存在 news id；或明确确认未创建，才重新进入 staging 发起新 POST。
	mux.HandleFunc("POST /api/publish/jobs/{id}/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
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
			FoodTalksNewsID     int64 `json:"foodtalks_news_id"`
			ConfirmNotCreated   bool  `json:"confirm_not_created"`
			ConfirmPublished    bool  `json:"confirm_published"`
			ConfirmNotPublished bool  `json:"confirm_not_published"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var job publishJob
		var err error
		if req.ConfirmPublished || req.ConfirmNotPublished {
			job, err = p.reconcilePublicize(id, req.ConfirmPublished, req.ConfirmNotPublished)
		} else {
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
	ID                int64    `json:"id"`
	SourceKey         string   `json:"source_key"`
	SourceURL         string   `json:"source_url"`
	Title             string   `json:"title,omitempty"`
	Stage             string   `json:"stage"`
	FailedStage       string   `json:"failed_stage,omitempty"`
	Error             string   `json:"error,omitempty"`
	FoodTalksNewsID   int64    `json:"foodtalks_news_id,omitempty"`
	ArticleURL        string   `json:"article_url,omitempty"`
	SupersededByJobID int64    `json:"superseded_by_job_id,omitempty"`
	ReviewAction      string   `json:"review_action,omitempty"`
	ReviewedBy        string   `json:"reviewed_by,omitempty"`
	ReviewedAt        string   `json:"reviewed_at,omitempty"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	Retryable         bool     `json:"retryable"`
	RetryBlockReason  string   `json:"retry_block_reason,omitempty"`
	ReconcileRequired bool     `json:"reconcile_required"`
	AvailableActions  []string `json:"available_actions"`
}

func newPublishSyncJobSummary(job publishJob) publishSyncJobSummary {
	retryable, retryBlockReason := publishJobRetryability(job)
	reconcileRequired := job.Stage == publishStageReconciliationRequired ||
		job.Stage == publishStagePublicizeReconciliationRequired ||
		job.Stage == publishStageStagingCreating || job.Stage == publishStagePublicizingUpdating
	actions := make([]string, 0, 1)
	switch {
	case retryable:
		actions = append(actions, "retry")
	case reconcileRequired:
		actions = append(actions, "reconcile")
	case job.Stage == publishStageAwaitingReview:
		actions = append(actions, "review_in_feishu")
	case job.Stage == publishStageSupersededDuplicate && job.SupersededByJobID > 0:
		actions = append(actions, "open_winner")
	}
	return publishSyncJobSummary{
		ID: job.ID, SourceKey: job.SourceKey, SourceURL: job.SourceURL, Title: job.Title,
		Stage: job.Stage, FailedStage: job.FailedStage, Error: truncateRunes(job.Error, 500),
		FoodTalksNewsID: job.FoodTalksNewsID, ArticleURL: job.ArticleURL,
		SupersededByJobID: job.SupersededByJobID,
		ReviewAction:      job.ReviewAction, ReviewedBy: job.ReviewedBy, ReviewedAt: job.ReviewedAt,
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
