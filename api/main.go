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
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

	// 启动期 fail loud：缺 ADMIN_PASSWORD 拒绝启动，杜绝正式端进入「规则后台未配置密码」状态
	// （CLAUDE.md Deploy 契约）。codegen 模式 -emit-* 已在上方 return，不受此限；本地开发设任意值即可。
	if strings.TrimSpace(os.Getenv("ADMIN_PASSWORD")) == "" {
		log.Fatal("ADMIN_PASSWORD 未设置：拒绝以无规则后台密码的状态启动（正式端必须配置；本地开发可临时设任意值）")
	}

	mux := http.NewServeMux()
	publicDir := *publicFlag
	if publicDir == "" {
		publicDir = "../public"
		if _, err := os.Stat(publicDir); err != nil {
			publicDir = "public"
		}
	}

	// 启动期账号一致性校验：registry.js 与 accountCatalog 漂移则 fail loud（消灭 D1/D3 静默降级）。
	// 文件缺失（部署路径差异）只告警，不 brick 生产。
	if err := validateRegistryConsistency(publicDir); err != nil {
		if strings.HasPrefix(err.Error(), "warn:") {
			log.Printf("账号一致性校验：%v", err)
		} else {
			log.Fatalf("账号一致性校验失败：%v", err)
		}
	}

	// 启动期区块注册表一致性校验：block-registry.gen.js 与 blockTypeCatalog 漂移则 fail loud。
	// 文件缺失（部署路径差异）只告警，不 brick 生产。
	if err := validateBlockRegistryConsistency(publicDir); err != nil {
		if strings.HasPrefix(err.Error(), "warn:") {
			log.Printf("区块注册表一致性校验：%v", err)
		} else {
			log.Fatalf("区块注册表一致性校验失败：%v", err)
		}
	}

	ruleStore := newRulePresetStore(*rulesPath)

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

	registerRulePresetHandlers(mux, ruleStore)

	// Kick off the in-memory image cache GC loop
	startDocImageCacheGC()

	// Health check
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

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
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
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
	if strings.TrimSpace(r.URL.Query().Get("v")) == "" {
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

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Admin-Password")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
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

func handleImageUpload(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 50<<20)) // 50MB limit
	if err != nil {
		writeError(w, 400, "failed to read body")
		return
	}

	// Proxy to upload endpoint if configured
	legacyURL := os.Getenv("WECHAT_UPLOAD_ENDPOINT")
	if legacyURL != "" {
		proxyReq, err := http.NewRequestWithContext(r.Context(), "POST", legacyURL, bytes.NewReader(body))
		if err != nil {
			writeError(w, 500, "proxy error: "+err.Error())
			return
		}
		proxyReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			writeError(w, 502, "upstream error: "+err.Error())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// No upload endpoint — return base64 as-is (preview works; WeChat re-hosts on paste)
	var req struct {
		Base64Images map[string]string `json:"base64_images"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}

	results := make(map[string]string)
	for key, val := range req.Base64Images {
		results[key] = val
	}
	writeJSON(w, 200, map[string]interface{}{"results": results})
}

// ---- Article Fetch (URL repost via x-reader) ----

func handleFetchArticle(w http.ResponseWriter, r *http.Request) {
	handleFetchArticleWithStore(nil)(w, r)
}

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

		// Validate URL format
		if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
			writeError(w, 400, "invalid URL: must start with http:// or https://")
			return
		}

		// Preferred path: direct HTTP → structured Blocks. Preserves visual
		// signals (font-size, color, align, bold) so heading/caption/body
		// classification is deterministic, not inferred from a lossy MD
		// intermediate. See wechat_blocks.go.
		accountID := firstNonEmpty(req.AccountID, req.Account)
		title, blocks, derr := fetchDirectBlocksWithOptions(req.URL, articleBlockOptionsForAccount(store, accountID))
		if derr == nil && len(blocks) > 0 {
			writeJSON(w, 200, map[string]interface{}{
				"title":  title,
				"blocks": blocks,
				"source": req.URL,
			})
			return
		}
		// Fall back to x-reader only if direct fetch failed. Ship its
		// Markdown to the frontend so the legacy classifyMd path can still
		// handle non-WeChat URLs that x-reader renders better.
		content, xrTitle, xrErr := fetchWithXReader(req.URL)
		if xrErr == nil {
			writeJSON(w, 200, map[string]interface{}{
				"title":   xrTitle,
				"content": content,
				"source":  req.URL,
			})
			return
		}
		// 两条路径都没拿到内容。内部错误（x-reader 未安装、传输细节等）只记日志，
		// 不抛给终端用户——给一句可操作的中文提示。
		internalErr := derr
		if internalErr == nil {
			internalErr = xrErr
		}
		if internalErr != nil {
			log.Printf("handleFetchArticle failed for %s: direct=%v xreader=%v", req.URL, derr, xrErr)
		}
		writeError(w, 502, "文章抓取失败，请稍后重试（公众号可能临时限流）")
	}
}

// fetchDirectBlocks is the new primary URL-import path: GET the raw HTML
// and walk it into a Block slice via extractArticleBlocks. Shares the
// HTTP boilerplate via fetchRawHTML so the paths can't drift on timeouts,
// UA, or body-size limits.
//
// Retries with backoff on transport failure OR empty blocks: WeChat
// rate-limits/serves an anti-scrape interstitial intermittently, and a
// plain retry a moment later almost always succeeds — far more reliable
// than the x-reader fallback (which isn't installed in production). Only
// after all attempts come up empty do we let the caller fall through.
//
// Contract: returns a non-nil err only when every attempt hit a
// transport-level failure. An empty `blocks` slice with err==nil is a
// valid (non-error) outcome — a page genuinely without article content.
// The caller at handleFetchArticle gates on `len(blocks) > 0`.
func fetchDirectBlocks(url string) (title string, blocks []Block, err error) {
	return fetchDirectBlocksWithOptions(url, defaultArticleBlockOptions())
}

func fetchDirectBlocksWithOptions(url string, opts articleBlockOptions) (title string, blocks []Block, err error) {
	opts = normalizeArticleBlockOptions(opts)
	// 3 attempts, exponential-ish backoff. WeChat 限流通常几百毫秒内恢复。
	backoffs := []time.Duration{0, 600 * time.Millisecond, 1500 * time.Millisecond}
	var transportErr error // 最后一次的传输错误；为 nil 表示最后一次拿到了页面但内容为空
	for i, wait := range backoffs {
		if wait > 0 {
			time.Sleep(wait)
		}
		bodyBytes, ferr := fetchRawHTML(url)
		if ferr != nil {
			transportErr = ferr
			log.Printf("fetchDirectBlocks attempt %d/%d transport error: %v", i+1, len(backoffs), ferr)
			continue
		}
		t, bs, _ := extractArticleBlocksWithOptions(string(bodyBytes), opts)
		if len(bs) > 0 {
			return t, bs, nil
		}
		// 传输成功但无内容：可能是反爬中间页，重试一次可能拿到真正文。
		transportErr = nil
		log.Printf("fetchDirectBlocks attempt %d/%d: empty content (possible anti-scrape page)", i+1, len(backoffs))
	}
	// 全部尝试都没拿到内容：最后一次是传输失败则返回该 err；否则（拿到页面但空）
	// 返回空 blocks 无 err，让调用方决定是否走 x-reader。
	return "", nil, transportErr
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

func fetchRawHTML(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

func fetchWithXReader(url string) (content, title string, err error) {
	// x-reader outputs markdown by default
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "x-reader", url, "--format", "markdown")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("x-reader failed: %s %v", stderr.String(), err)
	}

	output := stdout.String()
	joined, title := cleanXReaderMarkdown(output)

	// x-reader returns HTTP 200 even when WeChat serves a CAPTCHA/env-exception
	// stub. Surface those as errors so handleFetchArticle falls back to the
	// direct HTTP + extractArticle path (which preserves bold/refs).
	if looksLikeWeChatBlock(joined) {
		return "", "", fmt.Errorf("x-reader returned wechat block page")
	}

	return joined, title, nil
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
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		writeError(w, 400, "invalid URL: must start with http:// or https://")
		return
	}

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

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", resp.Request.URL.String(), fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024)) // 8MB cap
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
func handleImageProxy(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	if target == "" {
		writeError(w, 400, "url query param required")
		return
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeError(w, 400, "invalid URL")
		return
	}
	hostOK := false
	for _, h := range hostAllowlistCDN {
		if u.Host == h || strings.HasSuffix(u.Host, "."+h) {
			hostOK = true
			break
		}
	}
	if !hostOK {
		writeError(w, 403, "host not allowed: "+u.Host)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://mp.weixin.qq.com/")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, 502, "fetch failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		writeError(w, 502, fmt.Sprintf("upstream HTTP %d", resp.StatusCode))
		return
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	io.Copy(w, io.LimitReader(resp.Body, 10*1024*1024)) // 10MB cap
}

func init() {
	// Ensure data directory exists for persisted rule presets and caches.
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Printf("Warning: could not create data directory: %v", err)
	}
}

// ---- Doc image cache (strip from .docx, serve on demand) ----

type docCacheEntry struct {
	files   map[string][]byte
	expires time.Time
}

var (
	docImageCacheMu sync.Mutex
	docImageCache   = map[string]docCacheEntry{}
)

const docCacheTTL = 15 * time.Minute

func docCachePut(hash string, files map[string][]byte) {
	docImageCacheMu.Lock()
	defer docImageCacheMu.Unlock()
	docImageCache[hash] = docCacheEntry{files: files, expires: time.Now().Add(docCacheTTL)}
}

func docCacheGet(hash, filename string) ([]byte, bool) {
	docImageCacheMu.Lock()
	defer docImageCacheMu.Unlock()
	e, ok := docImageCache[hash]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(docImageCache, hash)
		return nil, false
	}
	data, ok := e.files[filename]
	return data, ok
}

func startDocImageCacheGC() {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			docImageCacheMu.Lock()
			now := time.Now()
			for k, e := range docImageCache {
				if now.After(e.expires) {
					delete(docImageCache, k)
				}
			}
			docImageCacheMu.Unlock()
		}
	}()
}

// stripImagesFromDocx opens the docx zip, extracts every word/media/* entry
// into the in-memory cache (keyed by a sha256-prefix hash of the original
// docx bytes), and rewrites the zip with those entries removed. It injects
// word/_fbif_imageUrls.json mapping filename → /api/doc-cache/<hash>/<filename>
// so the frontend parseDocx can wire <img src> without fetching bytes again.
func stripImagesFromDocx(docxBytes []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	sum := sha256.Sum256(docxBytes)
	hash := hex.EncodeToString(sum[:])[:16]

	images := map[string][]byte{}
	imageUrls := map[string]string{}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", f.Name, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", f.Name, err)
			}
			base := strings.TrimPrefix(f.Name, "word/media/")
			images[base] = data
			imageUrls[base] = "/api/doc-cache/" + hash + "/" + base
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
		_, err = io.Copy(w, rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("copy %s: %w", f.Name, err)
		}
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

	if len(images) > 0 {
		docCachePut(hash, images)
	}
	return out.Bytes(), nil
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

// sameOrigin enforces a CSRF-style check for endpoints that may spend
// paid backend quota (CloudConvert). A cross-site <form>/fetch from a
// third-party page always ships an Origin header; same-origin fetches
// from our own app match r.Host. curl / server-to-server traffic omits
// both headers and is allowed.
func sameOrigin(r *http.Request) bool {
	check := func(raw string) (matched, decided bool) {
		if raw == "" {
			return false, false
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return false, true
		}
		return u.Host == r.Host, true
	}
	if ok, decided := check(r.Header.Get("Origin")); decided {
		return ok
	}
	if ok, decided := check(r.Header.Get("Referer")); decided {
		return ok
	}
	return true
}

// handleDocToDocx accepts a multipart upload with field "file" containing an
// OLE2 (.doc) binary and returns real .docx bytes. Tries the self-hosted
// LibreOffice service first (fast, unlimited, private); falls back to
// CloudConvert if that's not configured or fails (network/5xx).
func handleDocToDocx(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, 403, "cross-origin request not allowed")
		return
	}
	// Hard cap the request body before ParseMultipartForm so Go doesn't
	// spill arbitrarily large uploads to temp files.
	r.Body = http.MaxBytesReader(w, r.Body, maxDocUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, 413, fmt.Sprintf("file too large (max %d MB)", maxDocUploadBytes>>20))
			return
		}
		writeError(w, 400, "failed to parse multipart: "+err.Error())
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
			writeError(w, 413, fmt.Sprintf("file too large (max %d MB)", maxDocUploadBytes>>20))
			return
		}
		writeError(w, 400, "read file: "+err.Error())
		return
	}
	if len(data) < 8 || !bytes.Equal(data[:8], ole2Magic) {
		writeError(w, 400, "not an OLE2 .doc file (missing magic bytes)")
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
			writeError(w, 500, "no converter available: LIBREOFFICE_URL/TOKEN and CLOUDCONVERT_API_KEY both unset")
			return
		}
		docx, err = cloudConvertDocToDocx(ctx, apiKey, data, header.Filename)
		if err != nil {
			log.Printf("cloudconvert error: %v", err)
			writeError(w, 502, "conversion failed: "+err.Error())
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("libreoffice status %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 100<<20))
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
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
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
	upBody, _ := io.ReadAll(upResp.Body)
	upResp.Body.Close()
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
		pollBody, _ := io.ReadAll(pollResp.Body)
		pollResp.Body.Close()
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
	return io.ReadAll(io.LimitReader(dlResp.Body, 100<<20))
}
