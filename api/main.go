package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

func main() {
	port := flag.Int("port", 9000, "API server port")
	_ = flag.String("db", "", "legacy ignored SQLite database path")
	rulesPath := flag.String("rules", os.Getenv("RULES_STORE_PATH"), "rule preset JSON store path")
	publicFlag := flag.String("public", os.Getenv("PUBLIC_DIR"), "static files directory; if empty falls back to ../public then public relative to cwd")
	emitRegistry := flag.Bool("emit-registry", false, "print generated public/js/rule-packs/registry.js from accountCatalog and exit")
	emitBlocks := flag.Bool("emit-blocks", false, "print generated public/js/blocks/block-registry.gen.js from blockTypeCatalog and exit")
	cmsResyncDay := flag.String("cms-resync-day", "", "one-off: re-PUT published FoodTalks homepage/source fields for this CST day (YYYY-MM-DD) and exit")
	flag.Parse()

	// codegen：从账号单一真相源生成 registry.js（`npm run gen:registry`）。
	if *emitRegistry {
		fmt.Print(renderRegistryJS())
		return
	}
	// codegen：从区块类型单一真相源生成 block-registry.gen.js（`npm run gen:blocks`）。
	if *emitBlocks {
		fmt.Print(renderBlockRegistryJS())
		return
	}
	if day := strings.TrimSpace(*cmsResyncDay); day != "" {
		runCMSResyncAndExit(day, *rulesPath)
		return
	}

	if productionRuntime() {
		if err := validateRuntimeConfigSchema(); err != nil {
			log.Fatalf("运行配置 schema 不匹配：%v", err)
		}
	}

	mux := http.NewServeMux()
	publicDir := *publicFlag
	if publicDir == "" {
		publicDir = "../public"
		if _, err := os.Stat(publicDir); err != nil {
			publicDir = "public"
		}
	}

	// 生成物存在但漂移在所有环境都 fatal；ENV=production 时文件缺失/不可读也 fatal。
	// 本地 codegen/source-only 工作流仍允许缺文件告警，readiness 则始终如实标红。
	if err := validateGeneratedArtifactsForStartup(publicDir, productionRuntime()); err != nil {
		log.Fatalf("生成文件启动校验失败：%v", err)
	}

	// 启动期账号↔preset 挂链校验：DefaultPresetID 悬空则 fail loud（无文件依赖，纯内存比对）。
	if err := validateAccountPresetLinkage(); err != nil {
		log.Fatalf("账号 preset 挂链校验失败：%v", err)
	}

	ruleStore := newRulePresetStore(*rulesPath)
	// schema v3 起每条规则必须显式声明 baseline_id。v1/v2 存量库会按历史
	// 语义迁移；已是 v3 却缺失/指向未知基线则拒绝启动，避免运行期串台。
	if err := ruleStore.validate(); err != nil {
		log.Fatalf("规则库校验失败：%v", err)
	}

	authService, err := newRuntimeAuthService(*rulesPath)
	if err != nil {
		log.Fatalf("登录服务启动失败：%v", err)
	}
	registerAuthRoutes(mux, authService)
	registerFeishuDocumentRoutes(mux, authService)
	registerFeishuChatRoutes(mux, authService)

	// Image upload to OSS (base64 from DOCX)
	mux.HandleFunc("POST /api/oss-upload", handleImageUpload)
	mux.HandleFunc("POST /api/wechat-upload", handleImageUpload) // legacy alias

	// Article fetch (URL repost via x-reader)
	mux.HandleFunc("POST /api/fetch-article", handleFetchArticleWithStore(ruleStore))

	// Article meta fetch (lightweight: title + cover image for "更多文章" cards)
	mux.HandleFunc("POST /api/fetch-article-meta", handleFetchArticleMeta)

	// Image proxy (bypass CORS for Canvas compositing)
	mux.HandleFunc("GET /api/image-proxy", handleImageProxy)

	// Legacy .doc (OLE2) → stub .docx conversion via LibreOffice + image cache
	mux.HandleFunc("POST /api/doc-to-docx", handleDocToDocx)

	// Cached image payload for images stripped from converted docx
	mux.HandleFunc("GET /api/doc-cache/{hash}/{filename}", handleDocImageCache)

	registerRulePresetHandlers(mux, ruleStore, authService)
	registerRuleAdminAccessHandlers(mux, authService)

	// 程序化排版 API（/api/render 代理 + /api/html-to-blocks，Bearer RENDER_TOKEN）
	registerRenderRoutes(mux)

	// 发布流水线（公众号 → FoodTalks 网站）：JSON 任务存储 + 状态机 + 人审端点。
	// 机器提交使用最小权限同步凭证；人工管理使用登录会话角色，生产 HTTP 审核默认关闭。
	publishPath := publishJobsPath(*rulesPath)
	publishLock, err := acquirePublishProcessLock(publishPath)
	if err != nil {
		log.Fatalf("发布任务库进程锁获取失败：%v", err)
	}
	defer publishLock.Close()
	publishStore := newPublishJobStore(publishPath)
	publishPipe := newPublishPipeline(publishStore, ruleStore)
	publishPipe.deliverySettings = newPublishDeliverySettingsStore(publishDeliverySettingsPath(*rulesPath))
	publishPipe.publicDir = publicDir
	publishPipe.enableBoundedWorkers(4, 128)
	if !feishuReviewerAllowlistConfigured() {
		log.Printf("publish: FEISHU_REVIEWER_OPEN_IDS 未配置；卡片审核仅在群成员模式与审核群均已配置时可用，否则 fail closed")
	}
	if !publishSyncServiceTokenConfigured() {
		log.Printf("publish: PUBLISH_SYNC_SERVICE_TOKEN 未配置，机器提交与紧凑状态接口仅允许管理员调用，readiness=false")
	}
	// 候选部署先以 DEPLOYMENT_WRITES_PAUSED=1 启动，只开放只读/健康验收；验收通过并
	// 接受该 release 后再解除暂停。这样 rollback 窗口内不会自动恢复任务、发通知或
	// 续跑不可逆外调。正常启动仍先扫描再开放 HTTP，避免与人工 retry 争抢。
	if publishWorkersPaused() {
		log.Printf("publish: 候选部署暂停态，跳过任务恢复、通知 outbox 与飞书长连接")
	} else {
		publishPipe.recoverJobs()
		publishPipe.startCMSPublicationSync()
	}
	registerPublishRoutes(mux, publishPipe, authService)
	// 飞书回调传输层必须显式互斥；缺失/非法配置安全回落 websocket 行为，公网
	// Webhook 精确返回 404，但 readiness=false，防止部署在含糊配置下假绿。
	feishuMode, feishuModeExplicit := registerConfiguredFeishuCallbackRoute(mux, publishPipe)
	if !feishuModeExplicit {
		log.Printf("publish: %s 未显式配置为 websocket/webhook；安全禁用公网 Webhook，readiness=false", feishuCallbackModeEnv)
	}
	if !publishWorkersPaused() && feishuMode == feishuCallbackModeWebSocket {
		maybeStartFeishuWS(publishPipe)
	}
	// 未上架预览页（M4「预览链接」裁定）：免登录但 HMAC 签名门（key=AUTH_SESSION_SECRET 派生），
	// 签名错/不存在/阶段太早一律 404（见 publish_preview.go 安全模型）。
	registerPublishPreviewRoute(mux, publishStore)

	// Kick off the in-memory image cache GC loop
	startDocImageCacheGC()

	// Health check
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		cacheStats := docImageCache.stats(time.Now())
		writeJSON(w, http.StatusOK, map[string]any{
			"status":                "ok",
			"config_schema_version": runtimeConfigSchemaVersion,
			"doc_cache_entries":     cacheStats.Entries,
			"doc_cache_bytes":       cacheStats.Bytes,
			"doc_cache_evictions":   cacheStats.Evictions,
		})
	})
	// Caddy 将未命中的 /api/* 也交给 Go；必须在静态 FileServer
	// 前统一返回 JSON 404，避免新 API 漏登记时落到 SPA HTML 假 200。
	mux.HandleFunc("/api", handleUnknownAPI)
	mux.HandleFunc("/api/", handleUnknownAPI)

	registerChannelRoutes(mux, publicDir)

	// Root → /mp/fbif (portal removed; landing goes straight to the WeChat editor)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mp/fbif", http.StatusFound)
	})

	// Serve static files from ../public
	fs := http.FileServer(http.Dir(publicDir))
	// HTML/version stay fresh on every deploy; version-pinned top-level assets
	// can be reused without revalidation. Bare nested JS/CSS remains no-cache
	// because those imports do not carry ?v=<commit>.
	mux.Handle("/", cacheStatic(fs))

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("FBIF server listening on %s (API + static from %s)", addr, publicDir)
	srv := newHTTPServer(addr, withSecurityHeaders(mux))
	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := serveUntilShutdown(shutdownSignal, srv, publishPipe.shutdownWorkers); err != nil {
		log.Fatal(err)
	}
}

func handleUnknownAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{
		"error": "API endpoint not found",
		"code":  "api_not_found",
	})
}

const gracefulShutdownTimeout = 30 * time.Second

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      150 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func serveUntilShutdown(shutdownSignal context.Context, srv *http.Server, shutdownHook func(context.Context) error) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		ctx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		if shutdownHook != nil {
			if hookErr := shutdownHook(ctx); hookErr != nil {
				return errors.Join(err, hookErr)
			}
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-shutdownSignal.Done():
		log.Printf("FBIF server shutting down gracefully")
		ctx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		shutdownErr := srv.Shutdown(ctx)
		if shutdownHook != nil {
			shutdownErr = errors.Join(shutdownErr, shutdownHook(ctx))
		}
		if shutdownErr != nil {
			_ = srv.Close()
			return fmt.Errorf("graceful shutdown: %w", shutdownErr)
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

const (
	appShellCacheControl        = "no-cache"
	versionedAssetCacheControl  = "public, max-age=31536000, immutable"
	shortLivedAssetCacheControl = "public, max-age=86400"
)

// cacheStatic keeps the app shell deployment-sensitive while avoiding repeat
// revalidation for assets whose URL already carries the deploy version.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/" || p == "/version.json" || strings.HasSuffix(p, ".html"):
			w.Header().Set("Cache-Control", appShellCacheControl)
		case isVersionedStaticAsset(r):
			w.Header().Set("Cache-Control", versionedAssetCacheControl)
		case strings.HasPrefix(p, "/vendor/"):
			w.Header().Set("Cache-Control", versionedAssetCacheControl)
		case isImageAssetPath(p):
			w.Header().Set("Cache-Control", shortLivedAssetCacheControl)
		case isScriptOrStylePath(p):
			w.Header().Set("Cache-Control", appShellCacheControl)
		}
		next.ServeHTTP(w, r)
	})
}

func isVersionedStaticAsset(r *http.Request) bool {
	version := strings.TrimSpace(r.URL.Query().Get("v"))
	if version == "" || strings.EqualFold(version, "dev") || strings.HasPrefix(strings.ToLower(version), "dev-") {
		return false
	}
	return isScriptOrStylePath(r.URL.Path)
}

func isScriptOrStylePath(p string) bool {
	return strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".mjs") || strings.HasSuffix(p, ".css")
}

func isImageAssetPath(p string) bool {
	for _, suffix := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".svg", ".ico"} {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
		if comma := strings.IndexByte(proto, ','); comma >= 0 {
			proto = strings.TrimSpace(proto[:comma])
		}
		if r.TLS != nil || proto == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// ---- Helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ---- Image Upload (base64 from DOCX → OSS or legacy proxy) ----

const maxImageUploadBodyBytes = 50 << 20

var uploadProxyHTTPClient = &http.Client{Timeout: 65 * time.Second}

func handleImageUpload(w http.ResponseWriter, r *http.Request) {
	release, ok := authorizeExpensiveRequest(w, r, imageUploadCostGuard)
	if !ok {
		return
	}
	defer release()

	r.Body = http.MaxBytesReader(w, r.Body, maxImageUploadBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "上传内容过大（最大 50 MB）")
			return
		}
		writeError(w, 400, "failed to read body")
		return
	}

	// Proxy to upload endpoint if configured
	legacyURL := os.Getenv("WECHAT_UPLOAD_ENDPOINT")
	if legacyURL != "" {
		origin, referer := uploadProxySourceHeaders(r)
		proxyReq, err := newUploadProxyRequest(r.Context(), legacyURL, r.URL.Path, r.Header.Get("Content-Type"), origin, referer, body)
		if err != nil {
			writeError(w, 500, "proxy error: "+err.Error())
			return
		}
		resp, err := uploadProxyHTTPClient.Do(proxyReq)
		if err != nil {
			writeError(w, 502, "upload service unavailable")
			return
		}
		defer resp.Body.Close()
		respBody, readErr := readBoundedBody(resp.Body, 8<<20)
		if errors.Is(readErr, errOutboundResponseTooLarge) {
			writeError(w, 502, "upload service response too large")
			return
		}
		if readErr != nil {
			writeError(w, 502, "failed to read upload service response")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return
	}

	// No upload endpoint — return base64 as-is (preview works; WeChat re-hosts on paste)
	var req struct {
		Base64Images map[string]string `json:"base64_images"`
		URLs         []string          `json:"urls"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	// URL mode is used by the website channel and cannot safely fall back to
	// returning the original WeChat CDN URLs. Fail loudly so deployment smoke
	// tests and the browser retry state expose a missing upload service instead
	// of reporting a misleading 200 with an empty result map.
	if req.URLs != nil {
		writeError(w, http.StatusServiceUnavailable, "图片转存服务未配置")
		return
	}

	results := make(map[string]string)
	for key, val := range req.Base64Images {
		results[key] = val
	}
	writeJSON(w, 200, map[string]interface{}{"results": results})
}

func uploadProxyURL(configured, requestPath string) string {
	u, err := url.Parse(configured)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return configured
	}

	switch {
	case strings.HasSuffix(requestPath, "/api/oss-upload"):
		u.Path = uploadProxyEndpointPath(u.Path, "/api/oss-upload")
	case strings.HasSuffix(requestPath, "/api/wechat-upload"):
		u.Path = uploadProxyEndpointPath(u.Path, "/api/wechat-upload")
	}
	return u.String()
}

func uploadProxyEndpointPath(current, endpoint string) string {
	trimmed := strings.TrimRight(current, "/")
	for _, legacyEndpoint := range []string{"/api/oss-upload", "/api/wechat-upload"} {
		if strings.HasSuffix(trimmed, legacyEndpoint) {
			return strings.TrimSuffix(trimmed, legacyEndpoint) + endpoint
		}
	}
	if trimmed == "" {
		return endpoint
	}
	return current
}

// newUploadProxyRequest 上游图片上传代理的请求构造内核：/api/oss-upload（及 wechat 别名）
// handler 与发布流水线 mirroring 阶段共用这一份（ADR 四层模型 L1：能力代码只此一份），
// 不许出现第二套上游请求实现。origin/referer 传空则不设该头（与历史条件语义一致）。
func newUploadProxyRequest(ctx context.Context, endpoint, requestPath, contentType, origin, referer string, body []byte) (*http.Request, error) {
	proxyReq, err := http.NewRequestWithContext(ctx, "POST", uploadProxyURL(endpoint, requestPath), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	proxyReq.Header.Set("Content-Type", contentType)
	if origin != "" {
		proxyReq.Header.Set("Origin", origin)
	}
	if referer != "" {
		proxyReq.Header.Set("Referer", referer)
	}
	return proxyReq, nil
}

// uploadProxySourceHeaders 从原始浏览器请求推导转发用的 Origin/Referer 值。
func uploadProxySourceHeaders(original *http.Request) (origin, referer string) {
	origin = strings.TrimSpace(original.Header.Get("Origin"))
	if origin == "" {
		origin = uploadProxyOrigin(original)
	}
	referer = strings.TrimSpace(original.Header.Get("Referer"))
	if referer == "" && origin != "" {
		referer = strings.TrimRight(origin, "/") + "/"
	}
	return origin, referer
}

func uploadProxyOrigin(r *http.Request) string {
	if configured := strings.TrimSpace(os.Getenv("WECHAT_UPLOAD_ORIGIN")); configured != "" {
		return strings.TrimRight(configured, "/")
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	if comma := strings.IndexByte(host, ','); comma >= 0 {
		host = strings.TrimSpace(host[:comma])
	}

	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if comma := strings.IndexByte(proto, ','); comma >= 0 {
		proto = strings.TrimSpace(proto[:comma])
	}
	if proto != "http" && proto != "https" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto + "://" + host
}

// ---- Article Fetch (URL repost via x-reader) ----

func handleFetchArticleWithStore(store *rulePresetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL       string `json:"url"`
			Account   string `json:"account"`
			AccountID string `json:"account_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
			writeError(w, 400, "url is required")
			return
		}

		validatedURL, err := parsePublicWebURL(req.URL)
		if err != nil {
			writeError(w, 400, "invalid or unsafe public URL")
			return
		}
		req.URL = validatedURL.String()

		// Preferred path: direct HTTP → structured Blocks. Preserves visual
		// signals (font-size, color, align, bold) so heading/caption/body
		// classification is deterministic, not inferred from a lossy MD
		// intermediate. See wechat_blocks.go.
		accountID := firstNonEmpty(req.AccountID, req.Account)
		title, blocks, sourceName, byline, _, originalSourceURL, derr := fetchDirectBlocksWithOptions(req.URL, articleBlockOptionsForAccount(store, accountID))
		if derr == nil && len(blocks) > 0 {
			writeJSON(w, 200, map[string]interface{}{
				"title":       title,
				"blocks":      blocks,
				"source":      req.URL,
				"source_name": sourceName,
				"byline":      byline,
				// 转载文章「阅读原文」指向的原文地址；转载声明《标题》超链接优先用它
				"original_source_url": originalSourceURL,
			})
			return
		}
		// Do not hand attacker-controlled URLs to an external reader process:
		// its redirect/DNS behavior cannot share the Go transport's SSRF guard.
		if derr != nil {
			log.Printf("handleFetchArticle failed for %s: direct=%v", req.URL, derr)
		}
		writeError(w, 502, "文章抓取失败，请稍后重试（公众号可能临时限流）")
	}
}

// Retries with backoff on transport failure OR empty blocks: WeChat
// rate-limits/serves an anti-scrape interstitial intermittently, and a
// plain retry a moment later almost always succeeds — far more reliable
// than the x-reader fallback (which isn't installed in production). Only
// after all attempts come up empty do we let the caller fall through.
//
// Contract: returns a non-nil err only when every attempt hit a
// transport-level failure. An empty `blocks` slice with err==nil is a
// valid (non-error) outcome — a page genuinely without article content.
// 第 4/5 个返回值是页面结构化元数据（extractWechatArticleMeta，2026-07-10 M7 修正：
// 旧说法「extractArticleBlocks 一直在产出公众号名」与代码不符——那条链路恒返回空串，
// 生产 job #1/#2 的 source_name 为空即此暗伤）：sourceName=公众号名（var nickname），
// byline=发布者填的结构化作者署名（meta author / var author，没署名就是空）。
// 发布流水线 fetching 阶段消费；handler 行为不变。
func fetchDirectBlocksWithOptions(url string, opts articleBlockOptions) (title string, blocks []Block, sourceName, byline, canonicalURL, originalSourceURL string, err error) {
	opts = normalizeArticleBlockOptions(opts)
	// 3 attempts, exponential-ish backoff. WeChat 限流通常几百毫秒内恢复。
	backoffs := []time.Duration{0, 600 * time.Millisecond, 1500 * time.Millisecond}
	var transportErr error // 最后一次的传输错误；为 nil 表示最后一次拿到了页面但内容为空
	for i, wait := range backoffs {
		if wait > 0 {
			time.Sleep(wait)
		}
		bodyBytes, finalURL, ferr := fetchRawHTML(url)
		if ferr != nil {
			transportErr = ferr
			log.Printf("fetchDirectBlocks attempt %d/%d transport error: %v", i+1, len(backoffs), ferr)
			continue
		}
		t, bs, _ := extractArticleBlocksWithOptions(string(bodyBytes), opts)
		if len(bs) > 0 {
			nickname, by, msgSourceURL := extractWechatArticleMeta(string(bodyBytes))
			return t, bs, nickname, by, articleSourceURL(url, finalURL, string(bodyBytes)), msgSourceURL, nil
		}
		// 传输成功但无内容：可能是反爬中间页，重试一次可能拿到真正文。
		transportErr = nil
		log.Printf("fetchDirectBlocks attempt %d/%d: empty content (possible anti-scrape page)", i+1, len(backoffs))
	}
	// 全部尝试都没拿到内容：最后一次是传输失败则返回该 err；否则（拿到页面但空）
	// 返回空 blocks 无 err，让调用方决定是否走 x-reader。
	return "", nil, "", "", "", "", transportErr
}

func articleBlockOptionsForAccount(store *rulePresetStore, accountID string) articleBlockOptions {
	opts := defaultArticleBlockOptions()
	if store == nil {
		return opts
	}
	preset, err := store.activeForAccount(accountID)
	if err != nil {
		return opts
	}
	params, _ := preset.Behavior["params"].(map[string]any)
	opts.CaptionMaxChars = behaviorParamInt(params, "image_caption_max_chars", opts.CaptionMaxChars)
	opts.CaptionCenterMaxChars = behaviorParamInt(params, "image_caption_center_max_chars", opts.CaptionCenterMaxChars)
	opts.CaptionStrongMaxChars = behaviorParamInt(params, "image_caption_strong_max_chars", opts.CaptionStrongMaxChars)
	return normalizeArticleBlockOptions(opts)
}

func behaviorParamInt(params map[string]any, key string, fallback int) int {
	spec, _ := params[key].(map[string]any)
	if spec == nil {
		return fallback
	}
	raw, ok := spec["default"]
	if !ok || raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

var articleFetchHTTPClient = newOutboundHTTPClient(20*time.Second, publicWebURLPolicy)

func fetchRawHTML(url string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	resp, err := doOutboundRequest(articleFetchHTTPClient, req, publicWebURLPolicy)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, resp.Request.URL.String(), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := readBoundedBody(resp.Body, 5*1024*1024)
	if errors.Is(err, errOutboundResponseTooLarge) {
		return nil, resp.Request.URL.String(), fmt.Errorf("article response exceeds 5 MB")
	}
	return body, resp.Request.URL.String(), err
}

// looksLikeWeChatBlock reports whether the x-reader output is a WeChat
// anti-scrape stub (CAPTCHA / 环境异常) rather than real article content.
// Heuristic: short output containing any of the known block markers.
func looksLikeWeChatBlock(s string) bool {
	if len(s) > 2000 {
		return false
	}
	markers := []string{
		"环境异常",
		"完成验证后即可继续访问",
		"requiring CAPTCHA",
		"CAPTCHA",
	}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// ---- Article Meta (lightweight: title + cover for "更多文章" cards) ----

var (
	reMsgCdnURL      = regexp.MustCompile(`var\s+msg_cdn_url\s*=\s*["']((?:[^"'\\]|\\.)*)["']`)
	reMsgTitle       = regexp.MustCompile(`var\s+msg_title\s*=\s*["']((?:[^"'\\]|\\.)*)["']`)
	reTitleTag       = regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	reImgDataSrc     = regexp.MustCompile(`(?is)<img\b[^>]+(?:data-src|data-original|data-url|src)\s*=\s*["']([^"']+)["']`)
	reCaptchaTarget  = regexp.MustCompile(`target_url\s*:\s*["']((?:[^"'\\]|\\.)*)["']`)
	hostAllowlistCDN = []string{
		"mmbiz.qpic.cn",
		"mmecoa.qpic.cn",
		"mmbiz.qlogo.cn",
		"wx.qlogo.cn",
		"mp.weixin.qq.com",
	}
)

// handleFetchArticleMeta fetches a WeChat (or generic) article URL and returns
// the title, cover image, and article body images for the "更多文章" card editor.
func handleFetchArticleMeta(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, 400, "url is required")
		return
	}
	req.URL = strings.Join(strings.Fields(req.URL), "")
	validatedURL, err := parsePublicWebURL(req.URL)
	if err != nil {
		writeError(w, 400, "invalid or unsafe public URL")
		return
	}
	req.URL = validatedURL.String()

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	raw, finalURL, err := fetchArticleHTML(ctx, req.URL)
	if err != nil {
		writeError(w, 502, "failed to fetch: "+err.Error())
		return
	}

	title, cover := extractArticleMeta(raw)
	imageURLs := extractArticleImageURLs(raw)
	sourceURL := articleSourceURL(req.URL, finalURL, raw)
	if (title == "" && cover == "") || len(imageURLs) == 0 {
		if fallbackURL := weChatMetaFallbackURL(req.URL, finalURL, raw); fallbackURL != "" && fallbackURL != req.URL && fallbackURL != finalURL {
			if fallbackRaw, fallbackFinalURL, fetchErr := fetchArticleHTML(ctx, fallbackURL); fetchErr == nil {
				fallbackTitle, fallbackCover := extractArticleMeta(fallbackRaw)
				fallbackImageURLs := extractArticleImageURLs(fallbackRaw)
				if fallbackTitle != "" || fallbackCover != "" || len(fallbackImageURLs) > 0 {
					if fallbackTitle != "" {
						title = fallbackTitle
					}
					if fallbackCover != "" {
						cover = fallbackCover
					}
					if len(fallbackImageURLs) > 0 {
						imageURLs = fallbackImageURLs
					}
					sourceURL = articleSourceURL(fallbackURL, fallbackFinalURL, fallbackRaw)
				}
			}
		}
	}
	title = strings.TrimSpace(title)
	if title == "" && cover == "" {
		writeError(w, 422, "未提取到标题或头图，请检查链接是否完整")
		return
	}

	writeJSON(w, 200, map[string]any{
		"title":      title,
		"cover_url":  cover,
		"source_url": sourceURL,
		"image_urls": imageURLs,
	})
}

func fetchArticleHTML(ctx context.Context, rawURL string) (string, string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", "", err
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	httpReq.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := doOutboundRequest(articleFetchHTTPClient, httpReq, publicWebURLPolicy)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", resp.Request.URL.String(), fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	body, err := readBoundedBody(resp.Body, 8*1024*1024)
	if errors.Is(err, errOutboundResponseTooLarge) {
		return "", resp.Request.URL.String(), fmt.Errorf("article response exceeds 8 MB")
	}
	if err != nil {
		return "", resp.Request.URL.String(), fmt.Errorf("read body: %w", err)
	}
	return string(body), resp.Request.URL.String(), nil
}

func extractArticleMeta(raw string) (string, string) {
	cover := extractArticleCover(raw)

	// Title priority: msg_title → og:title → <title>
	title := ""
	if m := reMsgTitle.FindStringSubmatch(raw); len(m) > 1 {
		title = decodeJSString(m[1])
	}
	if title == "" {
		title = extractMeta(raw, "og:title")
	}
	if title == "" {
		if m := reTitleTag.FindStringSubmatch(raw); len(m) > 1 {
			title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}
	return strings.TrimSpace(title), cover
}

func articleSourceURL(requestURL, finalURL, raw string) string {
	if v := extractMeta(raw, "og:url"); v != "" {
		return v
	}
	if v := extractWeChatCaptchaTargetURL(raw); v != "" {
		return v
	}
	if finalURL != "" {
		return finalURL
	}
	return requestURL
}

func extractWeChatCaptchaTargetURL(raw string) string {
	if m := reCaptchaTarget.FindStringSubmatch(raw); len(m) > 1 {
		return strings.TrimSpace(html.UnescapeString(decodeJSString(m[1])))
	}
	return ""
}

func weChatMetaFallbackURL(requestURL, finalURL, raw string) string {
	for _, candidate := range []string{extractWeChatCaptchaTargetURL(raw), finalURL, requestURL} {
		if fallback := addWeChatMetaFallbackParams(candidate); fallback != "" {
			return fallback
		}
	}
	return ""
}

func addWeChatMetaFallbackParams(rawURL string) string {
	rawURL = strings.TrimSpace(html.UnescapeString(decodeJSString(rawURL)))
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if u.Host != "mp.weixin.qq.com" || u.Path != "/s" {
		return ""
	}
	q := u.Query()
	if q.Get("__biz") == "" || q.Get("mid") == "" || q.Get("sn") == "" {
		return ""
	}
	q.Del("poc_token")
	if q.Get("chksm") == "" {
		q.Set("chksm", strings.Repeat("0", 32))
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String()
}

// extractArticleCover accepts the common shapes seen in WeChat and generic
// article pages: JS vars, Open Graph/Twitter cards, and lazy-loaded images.
func extractArticleCover(raw string) string {
	if m := reMsgCdnURL.FindStringSubmatch(raw); len(m) > 1 {
		if cover := normalizeArticleImageURL(m[1]); cover != "" {
			return cover
		}
	}
	for _, key := range []string{
		"og:image",
		"og:image:url",
		"og:image:secure_url",
		"twitter:image",
		"twitter:image:src",
	} {
		if cover := normalizeArticleImageURL(extractMeta(raw, key)); cover != "" {
			return cover
		}
	}
	return extractFirstArticleImageURL(raw)
}

func extractFirstArticleImageURL(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err == nil {
		var walk func(*html.Node) string
		walk = func(n *html.Node) string {
			if n.Type == html.ElementNode && strings.EqualFold(n.Data, "img") {
				for _, name := range []string{"data-src", "data-original", "data-url", "src"} {
					for _, a := range n.Attr {
						if strings.EqualFold(a.Key, name) {
							if src := normalizeArticleImageURL(a.Val); src != "" {
								return src
							}
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if v := walk(c); v != "" {
					return v
				}
			}
			return ""
		}
		if src := walk(doc); src != "" {
			return src
		}
	}

	if m := reImgDataSrc.FindStringSubmatch(raw); len(m) > 1 {
		return normalizeArticleImageURL(m[1])
	}
	return ""
}

func extractArticleImageURLs(raw string) []string {
	content := extractJsContent(raw)
	if content == "" {
		content = raw
	}

	const maxImages = 80
	out := make([]string, 0, 16)
	seen := map[string]bool{}
	add := func(src string) {
		src = normalizeArticleImageURL(src)
		if src == "" || isIgnoredArticleImageURL(src) || seen[src] {
			return
		}
		seen[src] = true
		out = append(out, src)
	}

	if doc, err := html.Parse(strings.NewReader(content)); err == nil {
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if len(out) >= maxImages {
				return
			}
			if n.Type == html.ElementNode && strings.EqualFold(n.Data, "img") {
				for _, name := range []string{"data-src", "data-original", "data-url", "src"} {
					for _, a := range n.Attr {
						if strings.EqualFold(a.Key, name) {
							add(a.Val)
							return
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
				if len(out) >= maxImages {
					return
				}
			}
		}
		walk(doc)
	}

	if len(out) < maxImages {
		for _, m := range reImgDataSrc.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				add(m[1])
			}
			if len(out) >= maxImages {
				break
			}
		}
	}

	return out
}

func isIgnoredArticleImageURL(src string) bool {
	lower := strings.ToLower(src)
	return strings.Contains(lower, "/pic_blank.gif") ||
		strings.Contains(lower, "res.wx.qq.com/mmbizappmsg") ||
		strings.HasPrefix(lower, "data:image")
}

func normalizeArticleImageURL(raw string) string {
	s := strings.TrimSpace(html.UnescapeString(decodeJSString(raw)))
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return ""
}

// decodeJSString handles simple backslash escapes found in WeChat's var msg_title
// (e.g. \", \\, \n, 中).
func decodeJSString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		nc := s[i+1]
		switch nc {
		case '"', '\\', '/':
			b.WriteByte(nc)
			i++
		case 'n':
			b.WriteByte('\n')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case 'r':
			b.WriteByte('\r')
			i++
		case 'u':
			if i+5 < len(s) {
				if code, err := strconv.ParseInt(s[i+2:i+6], 16, 32); err == nil {
					b.WriteRune(rune(code))
					i += 5
				} else {
					b.WriteByte(c)
				}
			} else {
				b.WriteByte(c)
			}
		case 'x':
			if i+3 < len(s) {
				if code, err := strconv.ParseInt(s[i+2:i+4], 16, 8); err == nil {
					b.WriteByte(byte(code))
					i += 3
				} else {
					b.WriteByte(c)
				}
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ---- Image Proxy (bypass CORS for Canvas compositing) ----

// handleImageProxy streams an allowlisted image URL so the frontend Canvas can
// read its pixels without tainting. Only WeChat CDN hosts are allowed so this
// cannot be abused as an open proxy.
var imageProxyHTTPClient = newOutboundHTTPClient(20*time.Second, imageProxyURLPolicy)

func handleImageProxy(w http.ResponseWriter, r *http.Request) {
	handleImageProxyWithClient(imageProxyHTTPClient)(w, r)
}

func handleImageProxyWithClient(client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("url")
		if target == "" {
			writeError(w, 400, "url query param required")
			return
		}
		u, err := parsePublicWebURL(target)
		if err != nil {
			writeError(w, 400, "invalid or unsafe public URL")
			return
		}
		if err := imageProxyURLPolicy(u); err != nil {
			writeError(w, 403, "image host not allowed")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			writeError(w, 400, "invalid URL")
			return
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Referer", "https://mp.weixin.qq.com/")
		resp, err := doOutboundRequest(client, req, imageProxyURLPolicy)
		if err != nil {
			writeError(w, 502, "image fetch failed")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			writeError(w, 502, fmt.Sprintf("upstream HTTP %d", resp.StatusCode))
			return
		}

		body, err := readBoundedBody(resp.Body, 10*1024*1024)
		if errors.Is(err, errOutboundResponseTooLarge) {
			writeError(w, 413, "image exceeds 10 MB")
			return
		}
		if err != nil {
			writeError(w, 502, "failed to read image")
			return
		}
		ct := detectedRasterImageType(body)
		if ct == "" {
			writeError(w, http.StatusUnsupportedMediaType, "upstream response is not a supported raster image")
			return
		}

		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func detectedRasterImageType(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	detected, _, err := mime.ParseMediaType(http.DetectContentType(body))
	if err != nil {
		return ""
	}
	switch detected {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/x-icon":
		return detected
	default:
		return ""
	}
}

func init() {
	// Ensure data directory exists for persisted rule presets and caches.
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Printf("Warning: could not create data directory: %v", err)
	}
}

// ---- Doc image cache (strip from .docx, serve on demand) ----

type docCacheEntry struct {
	files        map[string][]byte
	mirroredURLs map[string]string
	expires      time.Time
	lastAccess   time.Time
	bytes        int64
}

const (
	docCacheTTL              = 2 * time.Hour
	maxDocCacheBytes   int64 = 512 << 20
	maxDocCacheEntries       = 128
	maxDocMediaBytes   int64 = 100 << 20
	maxDocImageBytes   int64 = 15 << 20
	maxDocMediaFiles         = 100
	maxDocxEntries           = 5000
	maxDocxEntryBytes  int64 = 100 << 20
	maxDocxTotalBytes  int64 = 200 << 20
)

type docCacheStats struct {
	Entries   int
	Bytes     int64
	Evictions uint64
}

type docImageMemoryCache struct {
	mu         sync.Mutex
	entries    map[string]docCacheEntry
	totalBytes int64
	evictions  uint64
	maxBytes   int64
	maxEntries int
	ttl        time.Duration
}

func newDocImageMemoryCache(maxBytes int64, maxEntries int, ttl time.Duration) *docImageMemoryCache {
	return &docImageMemoryCache{
		entries:    make(map[string]docCacheEntry),
		maxBytes:   maxBytes,
		maxEntries: maxEntries,
		ttl:        ttl,
	}
}

var docImageCache = newDocImageMemoryCache(maxDocCacheBytes, maxDocCacheEntries, docCacheTTL)

func validateDocCacheFiles(files map[string][]byte) (int64, error) {
	if len(files) > maxDocMediaFiles {
		return 0, fmt.Errorf("too many document images: %d > %d", len(files), maxDocMediaFiles)
	}
	var total int64
	for name, data := range files {
		if int64(len(data)) > maxDocImageBytes {
			return 0, fmt.Errorf("document image %s exceeds %d MB", name, maxDocImageBytes>>20)
		}
		if int64(len(data)) > maxDocMediaBytes-total {
			return 0, fmt.Errorf("document media exceeds %d MB", maxDocMediaBytes>>20)
		}
		total += int64(len(data))
	}
	return total, nil
}

func (c *docImageMemoryCache) put(hash string, files map[string][]byte, now time.Time) error {
	size, err := validateDocCacheFiles(files)
	if err != nil {
		return err
	}
	if size > c.maxBytes || c.maxEntries < 1 {
		return fmt.Errorf("document image cache entry exceeds global capacity")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeExpiredLocked(now)
	var mirroredURLs map[string]string
	if existing, exists := c.entries[hash]; exists {
		mirroredURLs = cloneStringMap(existing.mirroredURLs)
		c.removeLocked(hash, false)
	}
	for len(c.entries) >= c.maxEntries || c.totalBytes > c.maxBytes-size {
		oldestKey := ""
		var oldest time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.lastAccess.Before(oldest) {
				oldestKey = key
				oldest = entry.lastAccess
			}
		}
		if oldestKey == "" {
			return fmt.Errorf("document image cache cannot free enough capacity")
		}
		c.removeLocked(oldestKey, true)
	}
	c.entries[hash] = docCacheEntry{
		files:        files,
		mirroredURLs: mirroredURLs,
		expires:      now.Add(c.ttl),
		lastAccess:   now,
		bytes:        size,
	}
	c.totalBytes += size
	return nil
}

func (c *docImageMemoryCache) mirroredURLs(hash string, now time.Time) (map[string]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[hash]
	if !ok {
		return nil, false
	}
	if !now.Before(entry.expires) {
		c.removeLocked(hash, true)
		return nil, false
	}
	if len(entry.mirroredURLs) == 0 {
		return nil, false
	}
	entry.lastAccess = now
	c.entries[hash] = entry
	return cloneStringMap(entry.mirroredURLs), true
}

func (c *docImageMemoryCache) setMirroredURLs(hash string, urls map[string]string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[hash]
	if !ok || !now.Before(entry.expires) {
		if ok {
			c.removeLocked(hash, true)
		}
		return false
	}
	entry.mirroredURLs = cloneStringMap(urls)
	entry.lastAccess = now
	c.entries[hash] = entry
	return true
}

func (c *docImageMemoryCache) get(hash, filename string, now time.Time) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[hash]
	if !ok {
		return nil, false
	}
	if !now.Before(entry.expires) {
		c.removeLocked(hash, true)
		return nil, false
	}
	data, ok := entry.files[filename]
	if !ok {
		return nil, false
	}
	entry.lastAccess = now
	c.entries[hash] = entry
	return data, true
}

func (c *docImageMemoryCache) stats(now time.Time) docCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeExpiredLocked(now)
	return docCacheStats{Entries: len(c.entries), Bytes: c.totalBytes, Evictions: c.evictions}
}

func (c *docImageMemoryCache) removeExpiredLocked(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			c.removeLocked(key, true)
		}
	}
}

func (c *docImageMemoryCache) removeLocked(hash string, evicted bool) {
	entry, ok := c.entries[hash]
	if !ok {
		return
	}
	delete(c.entries, hash)
	c.totalBytes -= entry.bytes
	if evicted {
		c.evictions++
	}
}

func docCachePut(hash string, files map[string][]byte) error {
	return docImageCache.put(hash, files, time.Now())
}

func docCacheGet(hash, filename string) ([]byte, bool) {
	return docImageCache.get(hash, filename, time.Now())
}

func docCacheMirroredURLs(hash string) (map[string]string, bool) {
	return docImageCache.mirroredURLs(hash, time.Now())
}

func docCacheSetMirroredURLs(hash string, urls map[string]string) bool {
	return docImageCache.setMirroredURLs(hash, urls, time.Now())
}

func startDocImageCacheGC() {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			docImageCache.stats(time.Now())
		}
	}()
}

// stripImagesFromDocx opens the docx zip, extracts every word/media/* entry
// into the in-memory cache (keyed by a sha256-prefix hash of the original
// docx bytes), and rewrites the zip with those entries removed. It injects
// word/_fbif_imageUrls.json mapping filename → /api/doc-cache/<hash>/<filename>
// so the frontend parseDocx can wire <img src> without fetching bytes again.
func stripImagesFromDocx(docxBytes []byte) ([]byte, error) {
	return stripImagesFromDocxWithProgress(context.Background(), docxBytes)
}

type docxContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r docxContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	read, err := r.reader.Read(buffer)
	if err == nil {
		if contextErr := r.ctx.Err(); contextErr != nil {
			return read, contextErr
		}
	}
	return read, err
}

func stripImagesFromDocxWithProgress(ctx context.Context, docxBytes []byte) ([]byte, error) {
	reportFeishuExportProgress(ctx, "optimize", 0, 0, "operations")
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	if len(zr.File) > maxDocxEntries {
		return nil, fmt.Errorf("docx has too many zip entries: %d > %d", len(zr.File), maxDocxEntries)
	}
	totalOperations := int64(len(zr.File) + 2)
	reportFeishuExportProgress(ctx, "optimize", 0, totalOperations, "operations")
	var declaredTotal uint64
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f.UncompressedSize64 > uint64(maxDocxEntryBytes) {
			return nil, fmt.Errorf("docx entry %s exceeds %d MB", f.Name, maxDocxEntryBytes>>20)
		}
		if f.UncompressedSize64 > uint64(maxDocxTotalBytes)-declaredTotal {
			return nil, fmt.Errorf("docx uncompressed content exceeds %d MB", maxDocxTotalBytes>>20)
		}
		declaredTotal += f.UncompressedSize64
		if suspiciousZipExpansion(f, 1000) {
			return nil, fmt.Errorf("docx entry %s has an unsafe compression ratio", f.Name)
		}
	}

	sum := sha256.Sum256(docxBytes)
	hash := hex.EncodeToString(sum[:])[:16]

	images := map[string][]byte{}
	imageUrls := map[string]string{}
	var mediaBytes int64
	var actualTotal int64

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for index, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.HasPrefix(f.Name, "word/media/") && !f.FileInfo().IsDir() {
			if len(images) >= maxDocMediaFiles {
				return nil, fmt.Errorf("docx has too many media files: more than %d", maxDocMediaFiles)
			}
			base := strings.TrimPrefix(f.Name, "word/media/")
			if base == "" || path.Base(base) != base || strings.ContainsAny(base, "/\\") || strings.HasPrefix(base, ".") {
				return nil, fmt.Errorf("unsafe media path: %s", f.Name)
			}
			if f.UncompressedSize64 > uint64(maxDocImageBytes) || suspiciousZipExpansion(f, 200) {
				return nil, fmt.Errorf("document image %s exceeds safe size or compression ratio", base)
			}
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", f.Name, err)
			}
			data, err := readBoundedBody(docxContextReader{ctx: ctx, reader: rc}, maxDocImageBytes)
			closeErr := rc.Close()
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", f.Name, err)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("close %s: %w", f.Name, closeErr)
			}
			if int64(len(data)) > maxDocMediaBytes-mediaBytes || int64(len(data)) > maxDocxTotalBytes-actualTotal {
				return nil, fmt.Errorf("document media exceeds safe uncompressed size")
			}
			mediaBytes += int64(len(data))
			actualTotal += int64(len(data))
			images[base] = data
			imageUrls[base] = "/api/doc-cache/" + hash + "/" + base
			reportFeishuExportProgress(ctx, "optimize", int64(index+1), totalOperations, "operations")
			continue
		}
		// Copy every other entry verbatim
		hdr := f.FileHeader
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", f.Name, err)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", f.Name, err)
		}
		remaining := maxDocxTotalBytes - actualTotal
		entryLimit := maxDocxEntryBytes
		if remaining < entryLimit {
			entryLimit = remaining
		}
		written, err := copyBounded(w, docxContextReader{ctx: ctx, reader: rc}, entryLimit)
		closeErr := rc.Close()
		if err != nil {
			return nil, fmt.Errorf("copy %s: %w", f.Name, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close %s: %w", f.Name, closeErr)
		}
		actualTotal += written
		reportFeishuExportProgress(ctx, "optimize", int64(index+1), totalOperations, "operations")
	}

	if len(imageUrls) > 0 {
		urlsJSON, err := json.Marshal(imageUrls)
		if err != nil {
			return nil, fmt.Errorf("encode imageUrls: %w", err)
		}
		uw, err := zw.Create("word/_fbif_imageUrls.json")
		if err != nil {
			return nil, fmt.Errorf("create imageUrls file: %w", err)
		}
		if _, err := uw.Write(urlsJSON); err != nil {
			return nil, fmt.Errorf("write imageUrls: %w", err)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	reportFeishuExportProgress(ctx, "optimize", int64(len(zr.File)+1), totalOperations, "operations")

	if len(images) > 0 {
		if err := docCachePut(hash, images); err != nil {
			return nil, fmt.Errorf("cache document images: %w", err)
		}
	}
	reportFeishuExportProgress(ctx, "optimize", totalOperations, totalOperations, "operations")
	return out.Bytes(), nil
}

func suspiciousZipExpansion(f *zip.File, maxRatio uint64) bool {
	if f == nil || f.UncompressedSize64 == 0 {
		return false
	}
	if f.CompressedSize64 == 0 {
		return true
	}
	return f.UncompressedSize64/f.CompressedSize64 > maxRatio
}

func copyBounded(dst io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	if maxBytes < 0 {
		return 0, errOutboundResponseTooLarge
	}
	limited := &io.LimitedReader{R: src, N: maxBytes + 1}
	written, err := io.Copy(dst, limited)
	if err != nil {
		return written, err
	}
	if written > maxBytes {
		return written, errOutboundResponseTooLarge
	}
	return written, nil
}

func handleDocImageCache(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	filename := r.PathValue("filename")
	// Sanitize: filename must be plain (no slashes, no parent refs).
	if filename == "" || strings.ContainsAny(filename, "/\\") || strings.HasPrefix(filename, ".") {
		http.NotFound(w, r)
		return
	}
	data, ok := docCacheGet(hash, filename)
	if !ok {
		http.NotFound(w, r)
		return
	}
	mime := docImageMimeFromExt(strings.ToLower(path.Ext(filename)))
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=900")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

func docImageMimeFromExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".svg":
		return "image/svg+xml"
	case ".emf":
		return "image/x-emf"
	case ".wmf":
		return "image/x-wmf"
	}
	return ""
}

// ---- Legacy .doc → .docx via CloudConvert ----

var ole2Magic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

const maxDocUploadBytes = 50 << 20 // 50 MB cap for /api/doc-to-docx

// sameOrigin is the browser half of the expensive-endpoint boundary. Missing
// Origin/Referer fails closed; non-browser callers must use EXPENSIVE_API_TOKEN
// (or the existing admin credential) instead of being treated as trusted.
func sameOrigin(r *http.Request) bool {
	expectedScheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if comma := strings.IndexByte(expectedScheme, ','); comma >= 0 {
		expectedScheme = strings.TrimSpace(expectedScheme[:comma])
	}
	if expectedScheme != "http" && expectedScheme != "https" {
		if r.TLS != nil {
			expectedScheme = "https"
		} else {
			expectedScheme = "http"
		}
	}
	check := func(raw string) (matched, decided bool) {
		if raw == "" {
			return false, false
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return false, true
		}
		return u.Scheme == expectedScheme && strings.EqualFold(u.Host, r.Host), true
	}
	if ok, decided := check(r.Header.Get("Origin")); decided {
		return ok
	}
	if ok, decided := check(r.Header.Get("Referer")); decided {
		return ok
	}
	return false
}

// handleDocToDocx accepts a multipart upload with field "file" containing an
// OLE2 (.doc) binary and returns real .docx bytes. Tries the self-hosted
// LibreOffice service first (fast, unlimited, private); falls back to
// CloudConvert if that's not configured or fails (network/5xx).
func handleDocToDocx(w http.ResponseWriter, r *http.Request) {
	release, ok := authorizeExpensiveRequest(w, r, docConvertCostGuard)
	if !ok {
		return
	}
	defer release()
	// Hard cap the request body before ParseMultipartForm so Go doesn't
	// spill arbitrarily large uploads to temp files.
	r.Body = http.MaxBytesReader(w, r.Body, maxDocUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, 413, fmt.Sprintf("文件过大（最大 %d MB），请压缩后重试", maxDocUploadBytes>>20))
			return
		}
		log.Printf("doc-to-docx: parse multipart failed: %v", err)
		writeError(w, 400, "上传内容解析失败，请重试")
		return
	}
	upload, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "file field required")
		return
	}
	defer upload.Close()

	data, err := io.ReadAll(upload)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, 413, fmt.Sprintf("文件过大（最大 %d MB），请压缩后重试", maxDocUploadBytes>>20))
			return
		}
		log.Printf("doc-to-docx: read upload failed: %v", err)
		writeError(w, 400, "文件读取失败，请重试")
		return
	}
	if len(data) < 8 || !bytes.Equal(data[:8], ole2Magic) {
		writeError(w, 400, "文件不是有效的 Word .doc 格式，请用 Word 另存为 .docx 后重试")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	var docx []byte
	var usedPath string
	loURL := os.Getenv("LIBREOFFICE_URL")
	loToken := os.Getenv("LIBREOFFICE_TOKEN")
	// Token is optional: when LibreOffice is reached via localhost (same-box
	// deploy), no auth layer is needed. A public URL still needs one.
	if loURL != "" {
		docx, err = convertViaLibreOffice(ctx, loURL, loToken, data, header.Filename)
		if err == nil {
			usedPath = "libreoffice"
		} else {
			log.Printf("libreoffice failed (%v), falling back to cloudconvert", err)
		}
	}
	if docx == nil {
		apiKey := os.Getenv("CLOUDCONVERT_API_KEY")
		if apiKey == "" {
			log.Printf("doc-to-docx: no converter available (LIBREOFFICE_URL/TOKEN and CLOUDCONVERT_API_KEY all unset)")
			writeError(w, 500, "文件转换服务暂不可用，请将文件另存为 .docx 后重新上传")
			return
		}
		if !validExpensiveCredential(r) {
			log.Printf("doc-to-docx: blocked anonymous CloudConvert fallback")
			writeError(w, http.StatusForbidden, "自动付费转换未授权，请将文件另存为 .docx 后重试")
			return
		}
		docx, err = cloudConvertDocToDocx(ctx, apiKey, data, header.Filename)
		if err != nil {
			log.Printf("cloudconvert error: %v", err)
			writeError(w, 502, "文件转换失败，请将文件另存为 .docx 后重新上传")
			return
		}
		usedPath = "cloudconvert"
	}

	// Strip embedded images out of the docx and replace them with URL refs
	// served from an in-memory cache. This shaves the client download from
	// ~14 MB to ~200 KB for the stub, lets the browser render text before
	// images finish transferring, and preserves image bytes verbatim.
	if stub, stripErr := stripImagesFromDocx(docx); stripErr == nil {
		docx = stub
	} else {
		log.Printf("strip images failed (%v), returning full docx", stripErr)
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", `inline; filename="converted.docx"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Conversion-Path", usedPath)
	w.Write(docx)
}

// convertViaLibreOffice POSTs the .doc to the self-hosted unoserver HTTP API
// (reached via Caddy reverse proxy with Bearer auth) and returns .docx bytes.
func convertViaLibreOffice(ctx context.Context, baseURL, token string, docBytes []byte, filename string) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("convert-to", "docx"); err != nil {
		return nil, err
	}
	safe := filename
	if safe == "" {
		safe = "input.doc"
	}
	fw, err := mw.CreateFormFile("file", safe)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(docBytes); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(baseURL, "/")+"/request", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, readErr := readBoundedBody(resp.Body, 4<<10)
		if readErr != nil {
			return nil, fmt.Errorf("libreoffice status %d（响应读取失败或超过 4 KB）", resp.StatusCode)
		}
		return nil, fmt.Errorf("libreoffice status %d: %s", resp.StatusCode, string(body))
	}
	body, err := readBoundedBody(resp.Body, 100<<20)
	if err != nil {
		return nil, fmt.Errorf("libreoffice 响应读取失败或超过 100 MB: %w", err)
	}
	return body, nil
}

// registerChannelRoutes 注册渠道路由：
//
//	/mp 系列 serve app.html（公众号工具）
//	/ft 系列 serve site.html（网站工具）
//	/app, /app.html 兼容 serve app.html
//
// 路径下的 channel/packId 解析由前端 JS 完成（rule-packs/url.js）。
func registerChannelRoutes(mux *http.ServeMux, publicDir string) {
	serveFile := func(filename string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(publicDir, filename))
		}
	}
	serveApp := serveFile("app.html")
	serveSite := serveFile("site.html")

	// 兼容旧 URL
	mux.HandleFunc("GET /app", serveApp)
	mux.HandleFunc("GET /app.html", serveApp)

	// 公众号渠道
	mux.HandleFunc("GET /mp", serveApp)
	mux.HandleFunc("GET /mp/{packId}", serveApp)

	// 网站渠道
	mux.HandleFunc("GET /ft", serveSite)
	mux.HandleFunc("GET /ft/{packId}", serveSite)
}

// cloudConvertDocToDocx drives a CloudConvert job: create → upload → poll → download.
// Returns the converted .docx bytes.
func cloudConvertDocToDocx(ctx context.Context, apiKey string, docBytes []byte, filename string) ([]byte, error) {
	const base = "https://api.cloudconvert.com/v2"

	// 1) Create job
	jobBody := `{"tasks":{"import-1":{"operation":"import/upload"},"convert-1":{"operation":"convert","input":"import-1","input_format":"doc","output_format":"docx"},"export-1":{"operation":"export/url","input":"convert-1"}}}`
	req, _ := http.NewRequestWithContext(ctx, "POST", base+"/jobs", strings.NewReader(jobBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	body, err := readBoundedBody(resp.Body, 1<<20)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("create job response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("create job status %d: %s", resp.StatusCode, string(body))
	}

	var jobResp struct {
		Data struct {
			ID    string `json:"id"`
			Tasks []struct {
				Name      string `json:"name"`
				Operation string `json:"operation"`
				Status    string `json:"status"`
				Result    struct {
					Form *struct {
						URL        string            `json:"url"`
						Parameters map[string]string `json:"parameters"`
					} `json:"form"`
				} `json:"result"`
			} `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &jobResp); err != nil {
		return nil, fmt.Errorf("decode job: %w", err)
	}
	jobID := jobResp.Data.ID
	var form *struct {
		URL        string            `json:"url"`
		Parameters map[string]string `json:"parameters"`
	}
	for _, t := range jobResp.Data.Tasks {
		if t.Operation == "import/upload" && t.Result.Form != nil {
			form = t.Result.Form
			break
		}
	}
	if form == nil {
		return nil, fmt.Errorf("no import/upload form in job response")
	}

	// 2) Upload file as multipart to CloudConvert's storage (order matters: file last)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range form.Parameters {
		if err := mw.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("write field %s: %w", k, err)
		}
	}
	safeName := filename
	if safeName == "" {
		safeName = "input.doc"
	}
	fw, err := mw.CreateFormFile("file", safeName)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(docBytes); err != nil {
		return nil, fmt.Errorf("write file part: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	upReq, _ := http.NewRequestWithContext(ctx, "POST", form.URL, &buf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	upBody, readErr := readBoundedBody(upResp.Body, 1<<20)
	upResp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("upload response: %w", readErr)
	}
	if upResp.StatusCode >= 300 {
		return nil, fmt.Errorf("upload status %d: %s", upResp.StatusCode, string(upBody))
	}

	// 3) Poll job until finished, errored, or the request context expires.
	var exportURL string
pollLoop:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		pollReq, _ := http.NewRequestWithContext(ctx, "GET", base+"/jobs/"+jobID, nil)
		pollReq.Header.Set("Authorization", "Bearer "+apiKey)
		pollResp, err := http.DefaultClient.Do(pollReq)
		if err != nil {
			return nil, fmt.Errorf("poll: %w", err)
		}
		pollBody, readErr := readBoundedBody(pollResp.Body, 1<<20)
		pollResp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("poll response: %w", readErr)
		}
		if pollResp.StatusCode >= 300 {
			return nil, fmt.Errorf("poll status %d: %s", pollResp.StatusCode, string(pollBody))
		}

		var pr struct {
			Data struct {
				Status string `json:"status"`
				Tasks  []struct {
					Operation string `json:"operation"`
					Status    string `json:"status"`
					Message   string `json:"message"`
					Code      string `json:"code"`
					Result    struct {
						Files []struct {
							Filename string `json:"filename"`
							URL      string `json:"url"`
						} `json:"files"`
					} `json:"result"`
				} `json:"tasks"`
			} `json:"data"`
		}
		if err := json.Unmarshal(pollBody, &pr); err != nil {
			return nil, fmt.Errorf("decode poll: %w", err)
		}

		switch pr.Data.Status {
		case "finished":
			for _, t := range pr.Data.Tasks {
				if t.Operation == "export/url" && len(t.Result.Files) > 0 {
					exportURL = t.Result.Files[0].URL
					break
				}
			}
			if exportURL == "" {
				return nil, fmt.Errorf("job finished but no export URL")
			}
			break pollLoop
		case "error":
			msg := "job failed"
			for _, t := range pr.Data.Tasks {
				if t.Status == "error" && t.Message != "" {
					msg = t.Operation + ": " + t.Message
					break
				}
			}
			return nil, fmt.Errorf("%s", msg)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	if exportURL == "" {
		return nil, fmt.Errorf("timed out waiting for conversion")
	}

	// 4) Download converted .docx
	dlReq, _ := http.NewRequestWithContext(ctx, "GET", exportURL, nil)
	dlResp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode >= 300 {
		return nil, fmt.Errorf("download status %d", dlResp.StatusCode)
	}
	body, err = readBoundedBody(dlResp.Body, 100<<20)
	if err != nil {
		return nil, fmt.Errorf("download response: %w", err)
	}
	return body, nil
}
