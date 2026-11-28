package main

// flash_http.go — 快讯的接口（约定写在 docs/publish-intake-contract.md「快讯」、docs/publish-status-callback.md）：
//
//	POST /api/publish/flash-sync              资讯订阅交一篇：{"article_id", "url", "status_callback_url"}
//	GET  /api/publish/flash-jobs/{id}          看一条（稿子、检查结果、发布结果）
//	POST /api/publish/flash-jobs/{id}/retry    失败的从失败那一步接着做（管理员）
//
// 机器入口只收文章编号（红线 33 的同一个道理：正文、标题一概不收，有就 400），事实一律回资讯订阅取。

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// newFlashPipelineFromEnv 中台 FLASH_ENABLED 没开就不装（入口回 503）。卡片只发 FLASH_REVIEW_CHAT_ID，没配就不发卡。
func newFlashPipelineFromEnv(p *publishPipeline, auth *authService, publishPath string) *flashPipeline {
	if !flashEnabled() {
		log.Printf("flash: %s 没开，快讯这条线不装", flashEnabledEnv)
		return nil
	}
	store := newFlashJobStore(flashJobsPath(publishPath))
	fp := newFlashPipeline(store)
	fp.spec = newFlashSpecSourceFromEnv(auth, flashSpecCachePath(store.path)).load
	if chat := strings.TrimSpace(os.Getenv(flashReviewChatEnv)); chat != "" {
		fp.cards = flashCardSender{chat: chat, notifier: func() (*feishuNotifier, error) {
			settings, apps, err := p.deliverySettings.resolve()
			if err != nil {
				return nil, err
			}
			return p.configuredReviewNotifier(settings, apps, chat)
		}}
	} else {
		log.Printf("flash: 没配 %s，快讯不发审核卡，稿子停在待审核", flashReviewChatEnv)
	}
	log.Printf("flash: 快讯这条线已开，任务库 %s", store.path)
	return fp
}

// flashJobView 接口回的一条：摘要加上稿子和检查结果。
type flashJobView struct {
	flashJobSummary
	Version      int          `json:"version,omitempty"`
	Draft        *flashDraft  `json:"draft,omitempty"`
	Checks       *flashChecks `json:"checks,omitempty"`
	SpecRevision int64        `json:"spec_revision,omitempty"`
	SpecNote     string       `json:"spec_note,omitempty"`
	Usage        aiTokenUsage `json:"usage"`
	RetryAt      string       `json:"retry_at,omitempty"`
	Failures     int          `json:"failures,omitempty"`
}

func newFlashJobView(j flashJob) flashJobView {
	return flashJobView{flashJobSummary: newFlashJobSummary(j), Version: j.Version, Draft: j.Draft, Checks: j.Checks,
		SpecRevision: j.SpecRevision, SpecNote: j.SpecNote, Usage: j.Usage, RetryAt: j.RetryAt, Failures: j.Failures}
}

var errFlashDisabled = errors.New("快讯这条线还没开（中台 " + flashEnabledEnv + "）")

func registerFlashRoutes(mux *http.ServeMux, p *publishPipeline, auth *authService) {
	mux.HandleFunc("POST /api/publish/flash-sync", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishSubmitOrAdmin(w, r, auth) {
			return
		}
		if p.flash == nil {
			writeError(w, http.StatusServiceUnavailable, errFlashDisabled.Error())
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		var req struct {
			ArticleID         string `json:"article_id"`
			URL               string `json:"url"`
			StatusCallbackURL string `json:"status_callback_url"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields() // 标题、正文之类一概不收：事实只从资讯订阅取
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "请求体只认 article_id、url、status_callback_url："+err.Error())
			return
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			writeError(w, http.StatusBadRequest, "请求体只能有一个 JSON 对象")
			return
		}
		req.ArticleID = strings.TrimSpace(req.ArticleID)
		if req.ArticleID == "" || len(req.ArticleID) > 128 {
			writeError(w, http.StatusBadRequest, "article_id is required")
			return
		}
		callbackURL, err := publishStatusCallbackTarget(req.StatusCallbackURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		job, existing, err := p.flash.submit(req.ArticleID, truncateRunes(strings.TrimSpace(req.URL), 2000), callbackURL)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		status := http.StatusCreated
		if existing {
			status = http.StatusOK
		}
		resp := map[string]any{"existing": existing, "job": newFlashJobSummary(job)}
		if callbackURL != "" { // 回显：调用方据此确认这边认了这个字段（同文章入口）
			resp["status_callback_url"] = callbackURL
		}
		writeJSON(w, status, resp)
	})

	mux.HandleFunc("GET /api/publish/flash-jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishSubmitOrAdmin(w, r, auth) {
			return
		}
		if p.flash == nil {
			writeError(w, http.StatusServiceUnavailable, errFlashDisabled.Error())
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "编号不对")
			return
		}
		job, err := p.flash.store.byID(id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": newFlashJobView(job)})
	})

	mux.HandleFunc("POST /api/publish/flash-jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if p.flash == nil {
			writeError(w, http.StatusServiceUnavailable, errFlashDisabled.Error())
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "编号不对")
			return
		}
		job, err := p.flash.retry(id)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": newFlashJobView(job)})
	})
}
