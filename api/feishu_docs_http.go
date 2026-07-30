package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var feishuStatusRequestGuard = newRequestCostGuard(
	maxFeishuStatusRequestsPerWindow*2,
	feishuExportStatusRateWindow,
	maxFeishuStatusWaiters+32,
)

func registerFeishuDocumentRoutes(mux *http.ServeMux, service *authService) {
	mux.HandleFunc("GET /api/feishu/docs/search", noStoreHandler(service.handleFeishuDocumentSearch))
	mux.HandleFunc("POST /api/feishu/docs/export", noStoreHandler(service.handleFeishuDocumentExport))
	mux.HandleFunc("POST /api/feishu/docs/export/jobs", noStoreHandler(service.handleFeishuDocumentExportJobCreate))
	mux.HandleFunc("GET /api/feishu/docs/export/jobs/{job_id}", noStoreHandler(service.handleFeishuDocumentExportJobStatus))
	mux.HandleFunc("GET /api/feishu/docs/export/jobs/{job_id}/result", noStoreHandler(service.handleFeishuDocumentExportJobResult))
	mux.HandleFunc("DELETE /api/feishu/docs/export/jobs/{job_id}", noStoreHandler(service.handleFeishuDocumentExportJobCancel))
}

func feishuExportErrorResponse(err error) (int, string, string) {
	var taskErr *feishuExportTaskError
	switch {
	case errors.Is(err, errAuthenticationRequired):
		return http.StatusUnauthorized, "authentication_required", "请先登录"
	case errors.Is(err, errFeishuLoginRequired):
		return http.StatusForbidden, "feishu_login_required", "飞书云文档功能需要使用飞书登录"
	case errors.Is(err, errFeishuExportTimeout):
		return http.StatusGatewayTimeout, "feishu_export_timeout", "飞书生成 Word 超时，请稍后重试"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "feishu_export_timeout", "飞书生成 Word 超时，请稍后重试"
	case errors.Is(err, context.Canceled):
		return http.StatusConflict, "feishu_export_canceled", "导出已取消"
	case errors.As(err, &taskErr):
		switch taskErr.JobStatus {
		case 107, 6000:
			return http.StatusRequestEntityTooLarge, "feishu_export_too_large", "文档或图片过多，飞书无法导出，请精简后重试"
		case 109, 110, 122:
			return http.StatusForbidden, "feishu_document_permission_denied", "当前账号没有导出这篇文档的权限"
		case 111, 123:
			return http.StatusNotFound, "feishu_document_not_found", "文档已删除或不存在"
		case 108:
			return http.StatusGatewayTimeout, "feishu_export_timeout", "飞书生成 Word 超时，请稍后重试"
		default:
			return http.StatusBadGateway, "feishu_export_failed", "飞书生成 Word 失败，请稍后重试"
		}
	default:
		return http.StatusBadGateway, "feishu_api_failed", "飞书云文档服务暂不可用"
	}
}

func feishuExportPublicError(err error) (string, string) {
	_, code, message := feishuExportErrorResponse(err)
	return code, message
}

func writeFeishuCapabilityError(w http.ResponseWriter, err error) {
	status, code, message := feishuExportErrorResponse(err)
	writeAuthError(w, status, code, message)
}

func requireFeishuMutationRequest(w http.ResponseWriter, r *http.Request, requireJSON bool) bool {
	// X-Session-Token is the explicit WebView/server bearer transport. Browsers
	// using the cookie must prove same-origin; SameSite=Lax alone still permits
	// requests from another subdomain on the same registrable site.
	if strings.TrimSpace(r.Header.Get("X-Session-Token")) == "" && !sameOrigin(r) {
		writeAuthError(w, http.StatusForbidden, "invalid_request_origin", "请求来源无效，请刷新页面后重试")
		return false
	}
	if !requireJSON {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeAuthError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请求必须使用 application/json")
		return false
	}
	return true
}

func writeFeishuRateLimit(w http.ResponseWriter, code, message string, err error) {
	retryAfter := time.Second
	var rateErr *feishuExportRateLimitError
	if errors.As(err, &rateErr) {
		retryAfter = rateErr.RetryAfter()
	}
	seconds := int((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeAuthError(w, http.StatusTooManyRequests, code, message)
}

func acquireFeishuStatusRequest(w http.ResponseWriter, r *http.Request) (func(), bool) {
	release, reason := feishuStatusRequestGuard.acquire(expensiveClientKey(r), time.Now())
	switch reason {
	case costGuardRateLimited:
		w.Header().Set("Retry-After", "60")
		writeAuthError(w, http.StatusTooManyRequests, "feishu_export_status_rate_limited", "进度查询过于频繁，请稍后重试")
		return nil, false
	case costGuardBusy:
		w.Header().Set("Retry-After", "1")
		writeAuthError(w, http.StatusServiceUnavailable, "feishu_export_status_busy", "进度服务正忙，请稍后重试")
		return nil, false
	default:
		return release, true
	}
}

func feishuExportErrorLabel(err error) string {
	var taskErr *feishuExportTaskError
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, errAuthenticationRequired):
		return "authentication_required"
	case errors.Is(err, errFeishuLoginRequired):
		return "feishu_login_required"
	case errors.Is(err, errFeishuExportTimeout):
		return "export_timeout"
	case errors.As(err, &taskErr):
		return feishuExportStatusLabel(taskErr.JobStatus)
	default:
		return "upstream_error"
	}
}

func docxContainsEntry(docxBytes []byte, entryName string) bool {
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		return false
	}
	for _, file := range zr.File {
		if file.Name == entryName {
			return true
		}
	}
	return false
}

func (s *authService) handleFeishuDocumentSearch(w http.ResponseWriter, r *http.Request) {
	user, err := s.requireFeishuUser(r)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) > 80 {
		writeAuthError(w, http.StatusBadRequest, "invalid_search_query", "搜索关键词过长")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"documents": []feishuDocument{}})
		return
	}
	accessToken, err := s.userAccessToken(r.Context(), user)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	documents, err := s.feishu.SearchDocuments(r.Context(), accessToken, query, limit)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	if documents == nil {
		documents = []feishuDocument{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": documents})
}

func decodeFeishuDocumentRef(w http.ResponseWriter, r *http.Request) (feishuDocumentRef, error) {
	var ref feishuDocumentRef
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ref); err != nil {
		return feishuDocumentRef{}, errFeishuInvalidDocument
	}
	return validateFeishuDocumentRef(ref)
}

func (s *authService) prepareFeishuDocumentExport(
	ctx context.Context,
	accessToken string,
	ref feishuDocumentRef,
	requestID string,
) (preparedFeishuDocument, error) {
	exported, err := s.feishu.ExportDocument(ctx, accessToken, ref)
	if err != nil {
		return preparedFeishuDocument{}, err
	}
	if len(exported.Content) == 0 {
		return preparedFeishuDocument{}, errors.New("empty Feishu export")
	}
	originalBytes := len(exported.Content)
	stripStartedAt := time.Now()
	optimized := exported.Content
	imagesDeferred := false
	imagesMirrored := false
	imageCount := 0
	mirrorDuration := time.Duration(0)
	stub, stripErr := stripImagesFromDocxWithProgress(ctx, exported.Content)
	stripDuration := time.Since(stripStartedAt)
	if stripErr == nil {
		optimized = stub
		imagesDeferred = docxContainsEntry(stub, "word/_fbif_imageUrls.json")
		if imagesDeferred && docImageMirroringConfigured() {
			mirrorStartedAt := time.Now()
			mirrored, mirroredCount, mirrorErr := mirrorCachedDocxImagesToOSS(ctx, exported.Content, stub)
			mirrorDuration = time.Since(mirrorStartedAt)
			if mirrorErr != nil {
				if contextErr := ctx.Err(); contextErr != nil {
					return preparedFeishuDocument{}, contextErr
				}
				log.Printf(
					"feishu-doc-export image-mirror-fallback request_id=%s elapsed_ms=%d error=%q",
					requestID,
					mirrorDuration.Milliseconds(),
					mirrorErr.Error(),
				)
			} else {
				optimized = mirrored
				imageCount = mirroredCount
				imagesDeferred = false
				imagesMirrored = mirroredCount > 0
			}
		}
	} else {
		if contextErr := ctx.Err(); contextErr != nil {
			return preparedFeishuDocument{}, contextErr
		}
		// Import must remain available for uncommon DOCX variants. Falling back
		// to the full file is slower, but preferable to turning a successfully
		// exported document into a false failure.
		log.Printf(
			"feishu-doc-export optimize-skipped request_id=%s reason=%q",
			requestID,
			stripErr.Error(),
		)
	}
	return preparedFeishuDocument{
		Filename:         exported.Filename,
		Content:          optimized,
		Metrics:          exported.Metrics,
		OriginalBytes:    originalBytes,
		OptimizedBytes:   len(optimized),
		ImagesDeferred:   imagesDeferred,
		ImagesMirrored:   imagesMirrored,
		ImageCount:       imageCount,
		OptimizeDuration: stripDuration,
		MirrorDuration:   mirrorDuration,
	}, nil
}

func (s *authService) handleFeishuDocumentExport(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	requestID := strconv.FormatInt(startedAt.UnixNano(), 36)
	w.Header().Set("X-Request-ID", requestID)
	user, err := s.requireFeishuUser(r)
	if err != nil {
		log.Printf(
			"feishu-doc-export failed request_id=%s phase=authentication elapsed_ms=%d class=%s",
			requestID,
			time.Since(startedAt).Milliseconds(),
			feishuExportErrorLabel(err),
		)
		writeFeishuCapabilityError(w, err)
		return
	}
	if !requireFeishuMutationRequest(w, r, true) {
		return
	}
	ref, err := decodeFeishuDocumentRef(w, r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_document", "飞书文档信息无效")
		return
	}
	if err := s.feishuExportJobs.reserveCreate(user.ID); err != nil {
		writeFeishuRateLimit(w, "feishu_export_rate_limited", "导出操作过于频繁，请稍后重试", err)
		return
	}
	runContext, cancel := context.WithTimeout(r.Context(), s.feishuExportJobs.runTimeout)
	defer cancel()
	release, err := s.feishuExportJobs.acquireExportSlot(runContext)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	defer release()
	accessToken, err := s.userAccessToken(runContext, user)
	if err != nil {
		log.Printf(
			"feishu-doc-export failed request_id=%s phase=access_token elapsed_ms=%d class=%s",
			requestID,
			time.Since(startedAt).Milliseconds(),
			feishuExportErrorLabel(err),
		)
		writeFeishuCapabilityError(w, err)
		return
	}
	prepared, err := s.prepareFeishuDocumentExport(runContext, accessToken, ref, requestID)
	if err != nil {
		log.Printf(
			"feishu-doc-export failed request_id=%s phase=export elapsed_ms=%d class=%s",
			requestID,
			time.Since(startedAt).Milliseconds(),
			feishuExportErrorLabel(err),
		)
		writeFeishuCapabilityError(w, err)
		return
	}
	if writeErr := writePreparedFeishuDocument(w, prepared); writeErr != nil {
		log.Printf(
			"feishu-doc-export response-write-failed request_id=%s elapsed_ms=%d error=%q",
			requestID,
			time.Since(startedAt).Milliseconds(),
			writeErr.Error(),
		)
		return
	}
	log.Printf(
		"feishu-doc-export success request_id=%s total_ms=%d resolve_ms=%d create_ms=%d poll_ms=%d download_ms=%d optimize_ms=%d mirror_ms=%d poll_attempts=%d original_bytes=%d optimized_bytes=%d images_deferred=%t images_mirrored=%t image_count=%d",
		requestID,
		time.Since(startedAt).Milliseconds(),
		prepared.Metrics.ResolveDuration.Milliseconds(),
		prepared.Metrics.CreateDuration.Milliseconds(),
		prepared.Metrics.PollDuration.Milliseconds(),
		prepared.Metrics.DownloadDuration.Milliseconds(),
		prepared.OptimizeDuration.Milliseconds(),
		prepared.MirrorDuration.Milliseconds(),
		prepared.Metrics.PollAttempts,
		prepared.OriginalBytes,
		prepared.OptimizedBytes,
		prepared.ImagesDeferred,
		prepared.ImagesMirrored,
		prepared.ImageCount,
	)
}

func (s *authService) handleFeishuDocumentExportJobCreate(w http.ResponseWriter, r *http.Request) {
	user, err := s.requireFeishuUser(r)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	if !requireFeishuMutationRequest(w, r, true) {
		return
	}
	ref, err := decodeFeishuDocumentRef(w, r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_document", "飞书文档信息无效")
		return
	}
	requestID := strconv.FormatInt(time.Now().UnixNano(), 36)
	job, err := s.feishuExportJobs.create(user.ID, func(ctx context.Context, _ feishuExportProgressReporter) (preparedFeishuDocument, error) {
		accessToken, tokenErr := s.userAccessToken(ctx, user)
		if tokenErr != nil {
			return preparedFeishuDocument{}, tokenErr
		}
		return s.prepareFeishuDocumentExport(ctx, accessToken, ref, requestID)
	})
	if err != nil {
		var rateErr *feishuExportRateLimitError
		if errors.As(err, &rateErr) {
			writeFeishuRateLimit(w, "feishu_export_rate_limited", "导出操作过于频繁，请稍后重试", err)
			return
		}
		writeAuthError(w, http.StatusServiceUnavailable, "feishu_export_busy", "当前导出任务较多，请稍后重试")
		return
	}
	w.Header().Set("Location", "/api/feishu/docs/export/jobs/"+job.id)
	status, _, _ := s.feishuExportJobs.status(user.ID, job.id)
	writeJSON(w, http.StatusAccepted, status)
}

func (s *authService) handleFeishuDocumentExportJobStatus(w http.ResponseWriter, r *http.Request) {
	releaseRequest, ok := acquireFeishuStatusRequest(w, r)
	if !ok {
		return
	}
	defer releaseRequest()
	user, err := s.requireFeishuUser(r)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	after, wait, err := parseFeishuStatusWait(r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_export_status_request", "进度查询参数无效")
		return
	}
	jobID := r.PathValue("job_id")
	status, changed, ok, statusErr := s.feishuExportJobs.statusForRequest(user.ID, jobID)
	if statusErr != nil {
		writeFeishuRateLimit(w, "feishu_export_status_rate_limited", "进度查询过于频繁，请稍后重试", statusErr)
		return
	}
	if !ok {
		writeAuthError(w, http.StatusNotFound, "feishu_export_job_not_found", "导出任务不存在或已过期")
		return
	}
	if wait > 0 && status.Version <= after && status.State != "completed" && status.State != "failed" && status.State != "canceled" {
		release, waitErr := s.feishuExportJobs.acquireStatusWaiter(user.ID, jobID)
		if waitErr != nil {
			w.Header().Set("Retry-After", "1")
			writeAuthError(w, http.StatusTooManyRequests, "feishu_export_status_busy", "同时等待进度的连接过多，请稍后重试")
			return
		}
		defer release()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-timer.C:
		}
		status, _, ok = s.feishuExportJobs.status(user.ID, jobID)
		if !ok {
			writeAuthError(w, http.StatusNotFound, "feishu_export_job_not_found", "导出任务不存在或已过期")
			return
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *authService) handleFeishuDocumentExportJobResult(w http.ResponseWriter, r *http.Request) {
	user, err := s.requireFeishuUser(r)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	status, result, release, ok, resultErr := s.feishuExportJobs.resultForRequest(user.ID, r.PathValue("job_id"))
	if resultErr != nil {
		var rateErr *feishuExportRateLimitError
		switch {
		case errors.As(resultErr, &rateErr):
			writeFeishuRateLimit(
				w,
				"feishu_export_result_rate_limited",
				"导出结果下载过于频繁，请稍后重试",
				resultErr,
			)
		case errors.Is(resultErr, errFeishuResultBusy):
			w.Header().Set("Retry-After", "1")
			writeAuthError(
				w,
				http.StatusServiceUnavailable,
				"feishu_export_result_busy",
				"导出结果下载服务正忙，请稍后重试",
			)
		default:
			writeAuthError(
				w,
				http.StatusServiceUnavailable,
				"feishu_export_result_unavailable",
				"导出结果暂时无法下载，请稍后重试",
			)
		}
		return
	}
	if !ok {
		writeAuthError(w, http.StatusNotFound, "feishu_export_job_not_found", "导出任务不存在或已过期")
		return
	}
	if result == nil {
		if status.Code == "" {
			status.Code = "feishu_export_not_ready"
			status.Error = "Word 文档仍在生成"
		}
		writeJSON(w, http.StatusConflict, status)
		return
	}
	defer release()
	if err := writePreparedFeishuDocument(w, *result); err != nil {
		log.Printf("feishu-doc-export job-result-write-failed job_id=%s error=%q", status.JobID, err.Error())
		return
	}
	s.feishuExportJobs.markResultConsumed(user.ID, status.JobID)
}

func (s *authService) handleFeishuDocumentExportJobCancel(w http.ResponseWriter, r *http.Request) {
	user, err := s.requireFeishuUser(r)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	if !requireFeishuMutationRequest(w, r, false) {
		return
	}
	status, ok := s.feishuExportJobs.cancel(user.ID, r.PathValue("job_id"))
	if !ok {
		writeAuthError(w, http.StatusNotFound, "feishu_export_job_not_found", "导出任务不存在或已过期")
		return
	}
	writeJSON(w, http.StatusOK, status)
}
