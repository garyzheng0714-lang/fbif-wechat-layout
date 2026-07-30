package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func registerFeishuDocumentRoutes(mux *http.ServeMux, service *authService) {
	mux.HandleFunc("GET /api/feishu/docs/search", noStoreHandler(service.handleFeishuDocumentSearch))
	mux.HandleFunc("POST /api/feishu/docs/export", noStoreHandler(service.handleFeishuDocumentExport))
}

func writeFeishuCapabilityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAuthenticationRequired):
		writeAuthError(w, http.StatusUnauthorized, "authentication_required", "请先登录")
	case errors.Is(err, errFeishuLoginRequired):
		writeAuthError(w, http.StatusForbidden, "feishu_login_required", "飞书云文档功能需要使用飞书登录")
	default:
		writeAuthError(w, http.StatusBadGateway, "feishu_api_failed", "飞书云文档服务暂不可用")
	}
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
	user, err := s.requireFeishuUser(r)
	if err != nil {
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
		writeFeishuCapabilityError(w, err)
		return
	}
	exported, err := s.feishu.ExportDocument(r.Context(), accessToken, ref)
	if err != nil {
		writeFeishuCapabilityError(w, err)
		return
	}
	if len(exported.Content) == 0 {
		writeFeishuCapabilityError(w, errors.New("empty Feishu export"))
		return
	}
	filename := sanitizeExportFilename(exported.Filename)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", "attachment; filename=\"feishu-document.docx\"; filename*=UTF-8''"+url.PathEscape(filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(exported.Content)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(exported.Content)
}
