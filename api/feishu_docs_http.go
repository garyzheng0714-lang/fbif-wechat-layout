package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func registerFeishuDocumentRoutes(mux *http.ServeMux, service *authService) {
	mux.HandleFunc("GET /api/feishu/docs/search", noStoreHandler(service.handleFeishuDocumentSearch))
	mux.HandleFunc("POST /api/feishu/docs/export", noStoreHandler(service.handleFeishuDocumentExport))
}

func writeFeishuCapabilityError(w http.ResponseWriter, err error) {
	var taskErr *feishuExportTaskError
	switch {
	case errors.Is(err, errAuthenticationRequired):
		writeAuthError(w, http.StatusUnauthorized, "authentication_required", "请先登录")
	case errors.Is(err, errFeishuLoginRequired):
		writeAuthError(w, http.StatusForbidden, "feishu_login_required", "飞书云文档功能需要使用飞书登录")
	case errors.Is(err, errFeishuExportTimeout):
		writeAuthError(w, http.StatusGatewayTimeout, "feishu_export_timeout", "飞书生成 Word 超时，请稍后重试")
	case errors.As(err, &taskErr):
		switch taskErr.JobStatus {
		case 107, 6000:
			writeAuthError(w, http.StatusRequestEntityTooLarge, "feishu_export_too_large", "文档或图片过多，飞书无法导出，请精简后重试")
		case 109, 110, 122:
			writeAuthError(w, http.StatusForbidden, "feishu_document_permission_denied", "当前账号没有导出这篇文档的权限")
		case 111, 123:
			writeAuthError(w, http.StatusNotFound, "feishu_document_not_found", "文档已删除或不存在")
		case 108:
			writeAuthError(w, http.StatusGatewayTimeout, "feishu_export_timeout", "飞书生成 Word 超时，请稍后重试")
		default:
			writeAuthError(w, http.StatusBadGateway, "feishu_export_failed", "飞书生成 Word 失败，请稍后重试")
		}
	default:
		writeAuthError(w, http.StatusBadGateway, "feishu_api_failed", "飞书云文档服务暂不可用")
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
	var ref feishuDocumentRef
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ref); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_document", "飞书文档信息无效")
		return
	}
	ref, err = validateFeishuDocumentRef(ref)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_document", "飞书文档信息无效")
		return
	}
	accessToken, err := s.userAccessToken(r.Context(), user)
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
	exported, err := s.feishu.ExportDocument(r.Context(), accessToken, ref)
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
	if len(exported.Content) == 0 {
		writeFeishuCapabilityError(w, errors.New("empty Feishu export"))
		return
	}
	originalBytes := len(exported.Content)
	stripStartedAt := time.Now()
	optimized := exported.Content
	imagesDeferred := false
	imagesMirrored := false
	imageCount := 0
	mirrorDuration := time.Duration(0)
	if stub, stripErr := stripImagesFromDocx(exported.Content); stripErr == nil {
		optimized = stub
		imagesDeferred = docxContainsEntry(stub, "word/_fbif_imageUrls.json")
		if imagesDeferred && docImageMirroringConfigured() {
			mirrorStartedAt := time.Now()
			mirrored, mirroredCount, mirrorErr := mirrorCachedDocxImagesToOSS(r.Context(), exported.Content, stub)
			mirrorDuration = time.Since(mirrorStartedAt)
			if mirrorErr != nil {
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
		// Import must remain available for uncommon DOCX variants. Falling back
		// to the full file is slower, but preferable to turning a successfully
		// exported document into a false failure.
		log.Printf(
			"feishu-doc-export optimize-skipped request_id=%s reason=%q",
			requestID,
			stripErr.Error(),
		)
	}
	stripDuration := time.Since(stripStartedAt)
	filename := sanitizeExportFilename(exported.Filename)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", "attachment; filename=\"feishu-document.docx\"; filename*=UTF-8''"+url.PathEscape(filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(optimized)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-FBIF-Docx-Original-Bytes", strconv.Itoa(originalBytes))
	w.Header().Set("X-FBIF-Docx-Optimized-Bytes", strconv.Itoa(len(optimized)))
	w.Header().Set("X-FBIF-Images-Deferred", strconv.FormatBool(imagesDeferred))
	w.Header().Set("X-FBIF-Images-Mirrored", strconv.FormatBool(imagesMirrored))
	w.Header().Set("X-FBIF-Image-Count", strconv.Itoa(imageCount))
	w.Header().Set("Server-Timing", fmt.Sprintf(
		"feishu_resolve;dur=%.1f, feishu_create;dur=%.1f, feishu_poll;dur=%.1f, feishu_download;dur=%.1f, docx_optimize;dur=%.1f, image_mirror;dur=%.1f",
		float64(exported.Metrics.ResolveDuration.Microseconds())/1000,
		float64(exported.Metrics.CreateDuration.Microseconds())/1000,
		float64(exported.Metrics.PollDuration.Microseconds())/1000,
		float64(exported.Metrics.DownloadDuration.Microseconds())/1000,
		float64(stripDuration.Microseconds())/1000,
		float64(mirrorDuration.Microseconds())/1000,
	))
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write(optimized); writeErr != nil {
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
		exported.Metrics.ResolveDuration.Milliseconds(),
		exported.Metrics.CreateDuration.Milliseconds(),
		exported.Metrics.PollDuration.Milliseconds(),
		exported.Metrics.DownloadDuration.Milliseconds(),
		stripDuration.Milliseconds(),
		mirrorDuration.Milliseconds(),
		exported.Metrics.PollAttempts,
		originalBytes,
		len(optimized),
		imagesDeferred,
		imagesMirrored,
		imageCount,
	)
}
