package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// 自家号转发别人、公众号又没填「阅读原文」时，转载声明《标题》不能链回自家转载页。
// 按「标题 + 出处」检索候选页，抓回来核验标题和出处都对得上才采用；核验不过就空着。

const (
	originalURLSearchTimeout = 6 * time.Second
	originalURLVerifyTimeout = 8 * time.Second
	originalURLMaxCandidates = 6
)

type feedOriginalArticleHit struct {
	URL         string
	KnownSource bool
}

var (
	// 测试可替换；默认先问 Feed 订阅列表，再走公开网页检索。失败只降级，不让发布任务失败。
	lookupFeedOriginalArticleDetail = defaultLookupFeedOriginalArticleDetail
	searchOriginalArticleURLs       = defaultSearchOriginalArticleURLs
	fetchOriginalArticlePage        = fetchRawHTML
	hrefInHTMLRe                    = regexp.MustCompile(`(?i)(?:href|data-url|data-href)\s*=\s*["']([^"']+)["']`)
	absoluteHTTPURLRe               = regexp.MustCompile(`https?://[^\s"'<>\\]+`)
	htmlTitleRe                     = regexp.MustCompile(`(?is)<title[^>]*>([^<]+)</title>`)
)

const (
	feedOriginalArticlePath    = "/api/internal/original-article"
	defaultFoodTalksFeedBase   = "https://feed.foodtalks.cn"
	feedOriginalArticleTimeout = 8 * time.Second
)

func defaultLookupFeedOriginalArticle(ctx context.Context, title, sourceName string) string {
	return defaultLookupFeedOriginalArticleDetail(ctx, title, sourceName).URL
}

func defaultLookupFeedOriginalArticleDetail(ctx context.Context, title, sourceName string) feedOriginalArticleHit {
	token := strings.TrimSpace(os.Getenv("PUBLISH_SYNC_SERVICE_TOKEN"))
	if token == "" {
		return feedOriginalArticleHit{}
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("FOODTALKS_FEED_BASE_URL")), "/")
	if base == "" {
		base = defaultFoodTalksFeedBase
	}
	payload, err := json.Marshal(map[string]string{
		"title":       title,
		"source_name": sourceName,
	})
	if err != nil {
		return feedOriginalArticleHit{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, feedOriginalArticleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+feedOriginalArticlePath, strings.NewReader(string(payload)))
	if err != nil {
		return feedOriginalArticleHit{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Publish-Sync-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("publish: 向 Feed 查询原文链接失败：%v", err)
		return feedOriginalArticleHit{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("publish: Feed 查询原文链接 HTTP %d", resp.StatusCode)
		return feedOriginalArticleHit{}
	}
	var body struct {
		URL      string `json:"url"`
		SourceID string `json:"source_id"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return feedOriginalArticleHit{}
	}
	hit := feedOriginalArticleHit{
		KnownSource: strings.TrimSpace(body.SourceID) != "" || body.Status == "found",
	}
	if body.Status == "found" {
		if cleaned, ok := sanitizeOriginalURLCandidate(body.URL); ok {
			hit.URL = cleaned
		}
	}
	return hit
}

var originalURLSearchSkipHosts = map[string]bool{
	"bing.com": true, "cn.bing.com": true, "www.bing.com": true,
	"sogou.com": true, "www.sogou.com": true, "weixin.sogou.com": true,
	"baidu.com": true, "www.baidu.com": true, "m.baidu.com": true,
	"google.com": true, "www.google.com": true,
	"foodtalks.cn": true, "www.foodtalks.cn": true,
	"test.foodtalks.cn": true, "admin-we.foodtalks.cn": true,
	"fbifmp-layout.foodtalks.cn": true,
}

func isHTTPURL(raw string) bool {
	u := strings.TrimSpace(raw)
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// normalizeSubmittedOriginalSourceURL 接收官方接口的 content_source_url /
// 排版字段 original_source_url。只收可公网访问的 http(s)，不当正文旁路。
func normalizeSubmittedOriginalSourceURL(raw string) string {
	parsed, err := parsePublicWebURL(raw)
	if err != nil {
		return ""
	}
	return parsed.String()
}

func preferOriginalSourceURL(fetched, existing string) string {
	if isHTTPURL(fetched) {
		return strings.TrimSpace(fetched)
	}
	if isHTTPURL(existing) {
		return strings.TrimSpace(existing)
	}
	return ""
}

// publishJobIsOwnAccountRepost 自家公众号转发外部稿。提交链接是自家转载页，不是原文。
func publishJobIsOwnAccountRepost(job publishJob) bool {
	original, _ := publishArticleProvenance(job)
	if original {
		return false
	}
	return siteOwnAccounts()[strings.TrimSpace(job.SourceName)]
}

func publishJobNeedsOriginalURLSearch(job publishJob) bool {
	if !publishJobIsOwnAccountRepost(job) {
		return false
	}
	return !isHTTPURL(job.OriginalSourceURL)
}

func (p *publishPipeline) ensureRepostOriginalURL(ctx context.Context, job *publishJob) {
	if p == nil || job == nil || !publishJobNeedsOriginalURLSearch(*job) {
		return
	}
	_, source := publishArticleProvenance(*job)
	found := resolveRepostOriginalURL(ctx, job.Title, source, job.SourceURL)
	if !isHTTPURL(found) {
		return
	}
	job.OriginalSourceURL = found
	if p.store == nil || job.ID <= 0 {
		return
	}
	if _, _, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return !isHTTPURL(current.OriginalSourceURL)
	}, func(current *publishJob) {
		current.OriginalSourceURL = found
	}); err != nil {
		log.Printf("publish: 写入原文链接失败（任务 #%d）：%v", job.ID, err)
	}
}

// resolveRepostOriginalURL 检索并核验转载稿的真正原文。excludeURL 是自家转载页，必须排除。
func resolveRepostOriginalURL(ctx context.Context, title, sourceName, excludeURL string) string {
	title = strings.TrimSpace(title)
	sourceName = strings.TrimSpace(sourceName)
	if title == "" || sourceName == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	feed := lookupFeedOriginalArticleDetail(ctx, title, sourceName)
	if isHTTPURL(feed.URL) && canonicalOriginalURLKey(feed.URL) != canonicalOriginalURLKey(excludeURL) {
		return feed.URL
	}
	if !feed.KnownSource {
		if found := lookupWechatOriginalArticle(ctx, title, sourceName); isHTTPURL(found) &&
			canonicalOriginalURLKey(found) != canonicalOriginalURLKey(excludeURL) {
			return found
		}
	}
	return resolveRepostOriginalURLFromSearch(ctx, title, sourceName, excludeURL)
}

func resolveRepostOriginalURLFromSearch(ctx context.Context, title, sourceName, excludeURL string) string {
	title = strings.TrimSpace(title)
	sourceName = strings.TrimSpace(sourceName)
	if title == "" || sourceName == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	searchCtx, cancel := context.WithTimeout(ctx, originalURLSearchTimeout)
	defer cancel()
	candidates := searchOriginalArticleURLs(searchCtx, title, sourceName)
	excludeKey := canonicalOriginalURLKey(excludeURL)
	seen := map[string]bool{}
	if excludeKey != "" {
		seen[excludeKey] = true
	}
	var ordered []string
	var wechat []string
	for _, raw := range candidates {
		cleaned, ok := sanitizeOriginalURLCandidate(raw)
		if !ok {
			continue
		}
		key := canonicalOriginalURLKey(cleaned)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if isWechatArticleURL(cleaned) {
			wechat = append(wechat, cleaned)
			continue
		}
		ordered = append(ordered, cleaned)
	}
	ordered = append(ordered, wechat...)
	if len(ordered) > originalURLMaxCandidates {
		ordered = ordered[:originalURLMaxCandidates]
	}
	for _, candidate := range ordered {
		if originalArticleURLMatches(candidate, title, sourceName) {
			return candidate
		}
	}
	return ""
}

func defaultSearchOriginalArticleURLs(ctx context.Context, title, sourceName string) []string {
	query := `"` + title + `" ` + sourceName
	var out []string
	for _, searchURL := range []string{
		"https://cn.bing.com/search?q=" + url.QueryEscape(query) + "&setlang=zh-hans",
		"https://www.sogou.com/web?query=" + url.QueryEscape(query),
	} {
		body, _, err := fetchSearchResultPage(ctx, searchURL)
		if err != nil || len(body) == 0 {
			continue
		}
		out = append(out, extractHTTPURLsFromSearchHTML(string(body))...)
		if len(out) >= originalURLMaxCandidates*2 {
			break
		}
	}
	return out
}

func fetchSearchResultPage(ctx context.Context, rawURL string) ([]byte, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, originalURLSearchTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	resp, err := doOutboundRequest(articleFetchHTTPClient, req, publicWebURLPolicy)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("search HTTP %d", resp.StatusCode)
	}
	body, err := readBoundedBody(resp.Body, 2*1024*1024)
	if err != nil {
		return nil, "", err
	}
	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return body, final, nil
}

func extractHTTPURLsFromSearchHTML(raw string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(rawURL string) {
		cleaned, ok := sanitizeOriginalURLCandidate(rawURL)
		if !ok || seen[cleaned] {
			return
		}
		seen[cleaned] = true
		out = append(out, cleaned)
	}
	for _, m := range hrefInHTMLRe.FindAllStringSubmatch(raw, -1) {
		if len(m) > 1 {
			add(html.UnescapeString(m[1]))
		}
	}
	for _, m := range absoluteHTTPURLRe.FindAllString(raw, -1) {
		add(html.UnescapeString(m))
	}
	return out
}

func sanitizeOriginalURLCandidate(raw string) (string, bool) {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	if raw == "" {
		return "", false
	}
	if strings.Contains(raw, "uddg=") {
		if u, err := url.Parse(raw); err == nil {
			if v := strings.TrimSpace(u.Query().Get("uddg")); v != "" {
				raw = v
			}
		}
	}
	if i := strings.Index(raw, "http://"); i > 0 {
		raw = raw[i:]
	} else if i := strings.Index(raw, "https://"); i > 0 {
		raw = raw[i:]
	}
	raw = strings.TrimRight(raw, `.,);]}>`)
	parsed, err := parsePublicWebURL(raw)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if originalURLSearchSkipHosts[host] {
		return "", false
	}
	for suffix := range originalURLSearchSkipHosts {
		if strings.HasSuffix(host, "."+suffix) {
			return "", false
		}
	}
	if strings.Contains(host, "bing.com") || strings.Contains(host, "sogou.com") ||
		strings.Contains(host, "baidu.com") || strings.Contains(host, "google.") {
		return "", false
	}
	return parsed.String(), true
}

func canonicalOriginalURLKey(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return strings.TrimSpace(raw)
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.TrimRight(parsed.Path, "/")
	if isWechatArticleURL(raw) {
		q := parsed.Query()
		if sn := q.Get("sn"); sn != "" {
			return "weixin:" + strings.ToLower(sn)
		}
		if biz, mid, idx := q.Get("__biz"), q.Get("mid"), q.Get("idx"); biz != "" && mid != "" {
			return "weixin:" + strings.ToLower(biz) + ":" + mid + ":" + idx
		}
		if path != "" && path != "/s" {
			return "weixin:" + strings.ToLower(path)
		}
	}
	return host + path
}

func isWechatArticleURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "mp.weixin.qq.com" || strings.HasSuffix(host, ".weixin.qq.com")
}

func originalArticleURLMatches(rawURL, title, sourceName string) bool {
	body, _, err := fetchOriginalArticlePage(rawURL)
	if err != nil || len(body) == 0 {
		return false
	}
	return originalArticlePageMatches(string(body), title, sourceName)
}

func originalArticlePageMatches(rawHTML, title, sourceName string) bool {
	pageTitle := strings.TrimSpace(extractMeta(rawHTML, "og:title"))
	if pageTitle == "" {
		if m := htmlTitleRe.FindStringSubmatch(rawHTML); len(m) > 1 {
			pageTitle = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}
	want := normalizeOriginalTitle(title)
	got := normalizeOriginalTitle(pageTitle)
	if want == "" || got == "" {
		return false
	}
	if !strings.Contains(got, want) && !strings.Contains(want, got) {
		return false
	}
	source := strings.TrimSpace(sourceName)
	if source == "" {
		return false
	}
	if strings.Contains(rawHTML, source) {
		return true
	}
	return strings.Contains(normalizeOriginalTitle(rawHTML), normalizeOriginalTitle(source))
}

func normalizeOriginalTitle(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(v)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
