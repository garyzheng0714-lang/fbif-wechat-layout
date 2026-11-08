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
	URL string
}

// repostOriginalArticle 找到的转载原文。Title 只在抓回原文核验过正文时才填（原文真标题），
// 空则转载声明沿用本稿标题。
type repostOriginalArticle struct {
	URL   string
	Title string
}

const (
	repostBodyProbeRunes = 24
	repostBodyProbeSkip  = 4 // 跳过段首几个字：转载时常在段首加「导语：」之类
	repostBodyMinProbes  = 3
	repostBodyMaxProbes  = 8
	repostBodyLeadRunes  = 28
)

var (
	repostBodyCreditLineRe = regexp.MustCompile(`^(?:作者|编辑|来源|出处|撰文|文|图|翻译|编译|采访|统筹|审校|责编|主编|美编|本文)[：:|｜\s]`)
	repostBodySentenceEnd  = regexp.MustCompile(`[。！？!?]`)
	wechatMsgTitleRe       = regexp.MustCompile(`var msg_title = '([^']+)'`)
)

// publishJobBodyParagraphs 本稿正文段落（图注、图片、表格除外），供找原文时比对正文。
func publishJobBodyParagraphs(blocksJSON string) []string {
	var blocks []Block
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return nil
	}
	var out []string
	for _, block := range blocks {
		if block.K == "cap" || block.K == "img" || block.K == "video" || block.K == "table" {
			continue
		}
		if text := publishExternalDistributionBlockText(block); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// repostBodyProbes 从正文前几段够长的段落各截一段做逐字探针（只留字母数字，忽略标点空格）；署名行不算。
func repostBodyProbes(paras []string) []string {
	var probes []string
	for _, para := range paras {
		para = strings.TrimSpace(para)
		if repostBodyCreditLineRe.MatchString(para) {
			continue
		}
		runes := []rune(normalizeOriginalTitle(para))
		if len(runes) < repostBodyProbeSkip+repostBodyProbeRunes+4 {
			continue
		}
		probes = append(probes, string(runes[repostBodyProbeSkip:repostBodyProbeSkip+repostBodyProbeRunes]))
		if len(probes) == repostBodyMaxProbes {
			break
		}
	}
	return probes
}

// repostBodyMatches 候选页正文命中至少六成探针才算同一篇。
func repostBodyMatches(rawHTML string, probes []string) bool {
	if len(probes) < repostBodyMinProbes {
		return false
	}
	text := normalizeOriginalTitle(html.UnescapeString(htmlTagRe.ReplaceAllString(rawHTML, "")))
	hits := 0
	for _, probe := range probes {
		if strings.Contains(text, probe) {
			hits++
		}
	}
	return hits*5 >= len(probes)*3
}

// repostBodyLeadSentence 正文第一句（截到 repostBodyLeadRunes 字），标题搜不到原文时拿它再搜。
func repostBodyLeadSentence(paras []string) string {
	for _, para := range paras {
		para = strings.TrimSpace(para)
		if repostBodyCreditLineRe.MatchString(para) || len([]rune(normalizeOriginalTitle(para))) < 20 {
			continue
		}
		if loc := repostBodySentenceEnd.FindStringIndex(para); loc != nil {
			para = para[:loc[0]]
		}
		if runes := []rune(para); len(runes) > repostBodyLeadRunes {
			para = string(runes[:repostBodyLeadRunes])
		}
		return strings.TrimSpace(para)
	}
	return ""
}

// originalArticlePageTitle 原文页的真标题：og:title，其次公众号页的 msg_title，最后用搜索结果标题。
func originalArticlePageTitle(rawHTML, fallback string) string {
	if title := strings.TrimSpace(extractMeta(rawHTML, "og:title")); title != "" {
		return title
	}
	if m := wechatMsgTitleRe.FindStringSubmatch(rawHTML); len(m) > 1 {
		if title := strings.TrimSpace(html.UnescapeString(m[1])); title != "" {
			return title
		}
	}
	return stripSearchHighlight(fallback)
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
	base := foodTalksFeedBaseURL()
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
	hit := feedOriginalArticleHit{}
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
	original, _, _ := publishArticleProvenance(job)
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
	if p == nil || job == nil {
		return
	}
	next, changed := repostOriginalFor(ctx, *job)
	if !changed {
		return
	}
	setOriginal := func(j *publishJob) {
		j.OriginalSourceURL = next.OriginalSourceURL
		j.OriginalSourceTitle, j.OriginalSourceTitleURL = next.OriginalSourceTitle, next.OriginalSourceTitleURL
	}
	setOriginal(job)
	if p.store == nil || job.ID <= 0 {
		return
	}
	if _, _, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return !isHTTPURL(current.OriginalSourceURL) ||
			canonicalOriginalURLKey(current.OriginalSourceURL) == canonicalOriginalURLKey(next.OriginalSourceURL)
	}, setOriginal); err != nil {
		log.Printf("publish: 写入原文链接失败（任务 #%d）：%v", job.ID, err)
	}
}

// repostOriginalFor 算出自家号转载稿该记的原文链接与真标题（不落库）。没有阅读原文就检索核验；
// 已有阅读原文且是公众号文章、还没记过它的标题，就抓页取真标题（FBIF 转载常改标题）。
func repostOriginalFor(ctx context.Context, job publishJob) (publishJob, bool) {
	_, source, _ := publishArticleProvenance(job)
	if publishJobNeedsOriginalURLSearch(job) {
		found := resolveRepostOriginal(ctx, job.Title, source, job.SourceURL, publishJobBodyParagraphs(job.BlocksJSON))
		if !isHTTPURL(found.URL) {
			return job, false
		}
		job.OriginalSourceURL = found.URL
		if title := strings.TrimSpace(found.Title); title != "" {
			job.OriginalSourceTitle, job.OriginalSourceTitleURL = title, found.URL
		}
		return job, true
	}
	url := strings.TrimSpace(job.OriginalSourceURL)
	if !publishJobIsOwnAccountRepost(job) ||
		canonicalOriginalURLKey(job.OriginalSourceTitleURL) == canonicalOriginalURLKey(url) {
		return job, false
	}
	title := wechatOriginalPageTitle(url, job.Title, source)
	if title == "" {
		return job, false
	}
	job.OriginalSourceTitle, job.OriginalSourceTitleURL = title, url
	return job, true
}

// publishOriginalTitleForRender 转载声明《》里用的原文真标题：只在它属于当前原文链接时才用
// （链接后来被阅读原文或人工改掉，旧标题就作废），否则沿用本稿标题。
func publishOriginalTitleForRender(job publishJob) string {
	title := strings.TrimSpace(job.OriginalSourceTitle)
	if title == "" || !isHTTPURL(job.OriginalSourceURL) ||
		canonicalOriginalURLKey(job.OriginalSourceTitleURL) != canonicalOriginalURLKey(job.OriginalSourceURL) {
		return ""
	}
	return title
}

// resolveRepostOriginal 检索并核验转载稿的真正原文。excludeURL 是自家转载页，必须排除；
// body 是本稿正文段落，用来核验改过标题的原文。
func resolveRepostOriginal(ctx context.Context, title, sourceName, excludeURL string, body []string) repostOriginalArticle {
	title = strings.TrimSpace(title)
	sourceName = strings.TrimSpace(sourceName)
	if title == "" || sourceName == "" {
		return repostOriginalArticle{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	excludeKey := canonicalOriginalURLKey(excludeURL)
	feed := lookupFeedOriginalArticleDetail(ctx, title, sourceName)
	if isHTTPURL(feed.URL) && canonicalOriginalURLKey(feed.URL) != excludeKey {
		return repostOriginalArticle{URL: feed.URL, Title: wechatOriginalPageTitle(feed.URL, title, sourceName)}
	}
	// Feed 订了这个号也照样搜一搜：Feed 只按标题对，FBIF 改过标题的转载在 Feed 里永远对不上
	// （2026-10-14 #255 财经杂志、#274 窄播都已订阅却 not_found）。
	if found := lookupWechatOriginalArticle(ctx, title, sourceName, body); isHTTPURL(found.URL) &&
		canonicalOriginalURLKey(found.URL) != excludeKey {
		return found
	}
	found := resolveRepostOriginalURLFromSearch(ctx, title, sourceName, excludeURL)
	return repostOriginalArticle{URL: found, Title: wechatOriginalPageTitle(found, title, sourceName)}
}

// wechatOriginalPageTitle Feed / 网页检索 / 阅读原文给出的公众号原文，标题仍可能被 FBIF 改过
// （#114 原文《笑死！假瑞幸败诉…》）：抓原文页取真标题。页面里没有来源号名、与本稿只差标点空格、
// 或抓不到时不填。
func wechatOriginalPageTitle(rawURL, ourTitle, sourceName string) string {
	if !isWechatArticleURL(rawURL) || strings.TrimSpace(sourceName) == "" {
		return ""
	}
	page, _, err := fetchOriginalArticlePage(rawURL)
	if err != nil || len(page) == 0 ||
		!strings.Contains(normalizeOriginalTitle(string(page)), normalizeOriginalTitle(sourceName)) {
		return ""
	}
	title := originalArticlePageTitle(string(page), "")
	if title == "" || normalizeOriginalTitle(title) == normalizeOriginalTitle(ourTitle) {
		return ""
	}
	return title
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
