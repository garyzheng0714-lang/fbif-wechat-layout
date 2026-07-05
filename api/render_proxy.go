package main

// render_proxy.go — 程序化排版 API（供 FBIF 资讯订阅一键发布流水线调用）。
//   POST /api/render          → 薄代理到本机 Node 渲染服务（render-service/，生产渲染器 headless 复用）
//   POST /api/html-to-blocks  → 复用 extractArticleBlocks：文章 HTML → 中立 Block[]
// 两端点均要求 Authorization: Bearer $RENDER_TOKEN；缺配置时 fail loud 拒答。
// 背景与决策见 WORKLOG-render-api.md。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultRenderServiceURL = "http://127.0.0.1:19091"

func renderToken() string {
	return strings.TrimSpace(os.Getenv("RENDER_TOKEN"))
}

func renderServiceURL() string {
	if v := strings.TrimSpace(os.Getenv("RENDER_SERVICE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultRenderServiceURL
}

// requireRenderToken 校验 Bearer token；RENDER_TOKEN 未配置时拒绝服务而非裸奔
//（同 ADMIN_PASSWORD 的缺配置哲学）。
func requireRenderToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := renderToken()
		if token == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "RENDER_TOKEN 未配置：渲染 API 拒绝无鉴权提供服务"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func registerRenderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/render", requireRenderToken(handleRenderProxy))
	mux.HandleFunc("POST /api/html-to-blocks", requireRenderToken(handleHTMLToBlocks))
}

// handleRenderProxy 原样转发请求体到 Node 渲染服务。渲染服务未就绪时返回明确 502
//（调用方据此把发布任务置为 failed，提示「排版服务未就绪」，不得挂起）。
func handleRenderProxy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取请求体失败: " + err.Error()})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, renderServiceURL()+"/render", bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "渲染服务未就绪（render-service 未启动或不可达）: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleHTMLToBlocks 文章 HTML → 中立 Block[]，供无原文 URL（或抓取失败）的调用方降级使用。
// 输出形状对齐 /api/fetch-article 的主路径（title/blocks/author），可直接喂 /api/render。
func handleHTMLToBlocks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HTML string `json:"html"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.HTML) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "html 不能为空"})
		return
	}
	title, blocks, author := extractArticleBlocks(req.HTML)
	writeJSON(w, http.StatusOK, map[string]any{
		"title":  title,
		"blocks": blocks,
		"author": author,
	})
}
