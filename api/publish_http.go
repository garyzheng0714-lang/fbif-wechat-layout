package main

// publish_http.go — 发布流水线 HTTP 端点（施工图 S5）。权限全部复用规则后台
// ADMIN_PASSWORD 门（X-Admin-Password 头，与 /api/rules/admin 同机制；登录 M2 落地后
// 换角色权限）。既有 API 路径与前端零改动，本文件只新增 /api/publish/* 路由。

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func registerPublishRoutes(mux *http.ServeMux, p *publishPipeline) {
	// 提交同步任务：source_key 幂等（已有未终态 job 返回它）→ 建 job → goroutine 异步推进。
	mux.HandleFunc("POST /api/publish/site-sync", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
			return
		}
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.URL == "" {
			writeError(w, http.StatusBadRequest, "url is required")
			return
		}
		job, created, err := p.submit(req.URL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{"job": job, "existing": !created})
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

	// 人审：approve 触发唯一不可逆的翻公开动作（用户红线：除此之外任何路径不得触发）；
	// reject 置终态。仅 awaiting_review 可操作，非法阶段返回 400 人话。
	mux.HandleFunc("POST /api/publish/jobs/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
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
		// reviewedBy 传空：ADMIN_PASSWORD 门后没有个人身份；飞书卡片回调侧传 operator.open_id。
		job, err := p.review(r.Context(), id, req.Action, "")
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
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		var req struct {
			FoodTalksNewsID   int64 `json:"foodtalks_news_id"`
			ConfirmNotCreated bool  `json:"confirm_not_created"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		job, err := p.reconcileStaging(id, req.FoodTalksNewsID, req.ConfirmNotCreated)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": job})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
	})
}

func publishJobIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "任务 id 必须是正整数")
		return 0, false
	}
	return id, true
}
