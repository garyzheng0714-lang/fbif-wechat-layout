package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// 自家号转发缺「阅读原文」、Feed 又还没订这个公众号时，用 TikHub 广域搜补微信原文链接。
// 内部订阅列表先查；没有再按标题搜一搜（最大池子），作者和标题都对上才用。
// 失败只降级，不让发布任务失败。

const (
	tikhubSearchPath     = "/api/v1/wechat_search/v2/fetch_search"
	defaultTikhubAPIBase = "https://api.tikhub.dev"
	tikhubLookupTimeout  = 70 * time.Second
	tikhubRequestTimeout = 30 * time.Second
	tikhubSearchMaxPages = 2
	repostBodyMaxFetches = 4
	tikhubSuccessCode    = 200
)

var (
	lookupWechatOriginalArticle = defaultLookupWechatOriginalArticle
	htmlTagRe                   = regexp.MustCompile(`<[^>]*>`)
	ghUsernameRe                = regexp.MustCompile(`^gh_[A-Za-z0-9_]+$`)
)

func tikhubAPIBase() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("TIKHUB_API_BASE_URL")), "/")
	if base == "" {
		return defaultTikhubAPIBase
	}
	return base
}

func tikhubAPIKey() string {
	return strings.TrimSpace(os.Getenv("TIKHUB_API_KEY"))
}

// defaultLookupWechatOriginalArticle 先按本稿标题、再按正文第一句搜一搜。标题和作者都对上直接用；
// FBIF 转载常改标题（2026-10-14：#255/#274/#275/#277 编辑都在 FoodTalks 后台手改成原标题），
// 所以作者对上、标题不同的候选要抓回原文逐段比对正文，对上才用，并带回原文真标题。
func defaultLookupWechatOriginalArticle(ctx context.Context, title, sourceName string, body []string) repostOriginalArticle {
	title = strings.TrimSpace(title)
	sourceName = strings.TrimSpace(sourceName)
	if title == "" || sourceName == "" || tikhubAPIKey() == "" {
		return repostOriginalArticle{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	wantTitle := normalizeOriginalTitle(title)
	wantSource := normalizeFoodTalksSourceName(sourceName)
	if wantTitle == "" || wantSource == "" {
		return repostOriginalArticle{}
	}
	budget, cancel := context.WithTimeout(ctx, tikhubLookupTimeout)
	defer cancel()
	probes := repostBodyProbes(body)
	checked := map[string]bool{}
	pick := func(items []wechatWorkItem) repostOriginalArticle {
		if found := pickWechatWorkURL(items, wantTitle, wantSource); found != "" {
			return repostOriginalArticle{URL: found}
		}
		return verifyRetitledWechatOriginal(budget, items, sourceName, probes, checked)
	}
	if found := lookupTikhubArticleSearch(budget, title, pick); found.URL != "" {
		return found
	}
	// 标题搜不到时按正文第一句再搜一次（#275 标题搜不出原文，正文第一句能）；只在能比对正文时才搜。
	if lead := repostBodyLeadSentence(body); lead != "" && len(probes) >= repostBodyMinProbes {
		return lookupTikhubArticleSearch(budget, lead, pick)
	}
	return repostOriginalArticle{}
}

func lookupTikhubArticleSearch(ctx context.Context, keyword string, pick func([]wechatWorkItem) repostOriginalArticle) repostOriginalArticle {
	cursor := ""
	seenCursor := map[string]bool{}
	for page := 0; page < tikhubSearchMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return repostOriginalArticle{}
		}
		payload := map[string]any{
			"keyword":       keyword,
			"business_type": "article",
			"sort":          "latest",
			"publish_time":  "all",
			"raw":           false,
		}
		if cursor != "" {
			payload["cursor"] = cursor
		}
		data, err := tikhubPOST(ctx, tikhubSearchPath, payload)
		if err != nil {
			log.Printf("publish: TikHub 搜文章失败：%v", err)
			return repostOriginalArticle{}
		}
		if found := pick(collectWechatWorkItems(data)); found.URL != "" {
			return found
		}
		next := tikhubString(data, "cursor")
		if next == "" || seenCursor[next] || !tikhubFlag(data, "continue_flag", "continueFlag") {
			return repostOriginalArticle{}
		}
		seenCursor[next] = true
		cursor = next
	}
	return repostOriginalArticle{}
}

// verifyRetitledWechatOriginal 作者是来源号、标题与本稿不同的候选：抓回原文，正文探针过半对上
// （同一个号常有好几篇相关稿，只看作者会错配）、页面里有来源号名才用。每次查找最多抓 repostBodyMaxFetches 篇。
func verifyRetitledWechatOriginal(ctx context.Context, items []wechatWorkItem, sourceName string, probes []string, checked map[string]bool) repostOriginalArticle {
	if len(probes) < repostBodyMinProbes {
		return repostOriginalArticle{}
	}
	wantSource := normalizeFoodTalksSourceName(sourceName)
	for _, item := range items {
		if normalizeFoodTalksSourceName(item.Author) != wantSource {
			continue
		}
		articleURL, ok := normalizeWechatWorkURL(item.URL)
		if !ok {
			continue
		}
		key := canonicalOriginalURLKey(articleURL)
		if checked[key] {
			continue
		}
		if len(checked) >= repostBodyMaxFetches || ctx.Err() != nil {
			return repostOriginalArticle{}
		}
		checked[key] = true
		page, _, err := fetchOriginalArticlePage(articleURL)
		if err != nil || len(page) == 0 {
			continue
		}
		raw := string(page)
		if !repostBodyMatches(raw, probes) ||
			!strings.Contains(normalizeOriginalTitle(raw), normalizeOriginalTitle(sourceName)) {
			continue
		}
		return repostOriginalArticle{URL: articleURL, Title: originalArticlePageTitle(raw, item.Title)}
	}
	return repostOriginalArticle{}
}

type wechatWorkItem struct {
	Title  string
	URL    string
	Author string
}

func tikhubPOST(ctx context.Context, path string, payload any) (any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return tikhubRequest(ctx, http.MethodPost, path, bytes.NewReader(raw))
}

func tikhubGET(ctx context.Context, path string, query url.Values) (any, error) {
	return tikhubRequest(ctx, http.MethodGet, path+"?"+query.Encode(), nil)
}

func tikhubRequest(ctx context.Context, method, path string, reqBody io.Reader) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, tikhubRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, tikhubAPIBase()+path, reqBody)
	if err != nil {
		return nil, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FBIF-layout/1.0)")
	req.Header.Set("Authorization", "Bearer "+tikhubAPIKey())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Code int             `json:"code"`
		Msg  string          `json:"message_zh"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Code != tikhubSuccessCode {
		if strings.TrimSpace(parsed.Msg) != "" {
			return nil, fmt.Errorf("%s", parsed.Msg)
		}
		return nil, fmt.Errorf("TikHub 返回 %d", parsed.Code)
	}
	return decodeTikhubData(parsed.Data), nil
}

func decodeTikhubData(raw json.RawMessage) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		var inner string
		if json.Unmarshal(raw, &inner) == nil {
			return decodeTikhubData([]byte(inner))
		}
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

func collectWechatWorkItems(v any) []wechatWorkItem {
	var out []wechatWorkItem
	seen := map[string]bool{}
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if item, ok := extractWechatWorkItem(n); ok {
				key := canonicalOriginalURLKey(item.URL)
				if key != "" && !seen[key] {
					seen[key] = true
					out = append(out, item)
				}
			}
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(v)
	return out
}

func extractWechatWorkItem(m map[string]any) (wechatWorkItem, bool) {
	title := stripSearchHighlight(firstString(m, "title", "name"))
	author := firstString(m, "author", "nickName", "nick_name", "accountName", "account_name", "sourceName", "source_name")
	articleURL := firstWechatURL(m)
	if src, ok := nestedMap(m, "source"); ok {
		if author == "" {
			author = stripSearchHighlight(firstString(src, "title", "nickName", "nick_name", "name", "author"))
		}
	}
	if jump, ok := nestedMap(m, "jumpInfo", "jump_info"); ok {
		if author == "" {
			author = stripSearchHighlight(firstString(jump, "nickName", "nick_name"))
		}
		if articleURL == "" {
			articleURL = firstWechatURL(jump)
		}
	}
	if title == "" || articleURL == "" {
		return wechatWorkItem{}, false
	}
	if ghUsernameRe.MatchString(author) {
		author = ""
	}
	return wechatWorkItem{Title: title, URL: articleURL, Author: author}, true
}

func firstWechatURL(v any) string {
	switch n := v.(type) {
	case string:
		if cleaned, ok := normalizeWechatWorkURL(n); ok {
			return cleaned
		}
	case map[string]any:
		for _, key := range []string{"url", "workUrl", "work_url", "link", "href", "docUrl", "doc_url", "article_url", "articleUrl"} {
			if s, ok := n[key].(string); ok {
				if cleaned, ok := normalizeWechatWorkURL(s); ok {
					return cleaned
				}
			}
		}
		for _, child := range n {
			if s := firstWechatURL(child); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range n {
			if s := firstWechatURL(child); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		s, ok := m[key].(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

func nestedMap(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		child, ok := m[key].(map[string]any)
		if ok {
			return child, true
		}
	}
	return nil, false
}

func tikhubString(v any, keys ...string) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	return firstString(m, keys...)
}

func tikhubFlag(v any, keys ...string) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range keys {
		if _, exists := m[key]; exists {
			return jsonFlagTrue(m[key])
		}
	}
	return false
}

func jsonFlagTrue(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case float64:
		return n != 0
	case json.Number:
		i, err := n.Int64()
		return err == nil && i != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(n))
		return s == "true" || s == "1"
	default:
		return false
	}
}

func stripSearchHighlight(v string) string {
	v = html.UnescapeString(strings.TrimSpace(v))
	v = htmlTagRe.ReplaceAllString(v, "")
	return strings.TrimSpace(v)
}

func normalizeWechatWorkURL(raw string) (string, bool) {
	cleaned, ok := sanitizeOriginalURLCandidate(raw)
	if !ok {
		return "", false
	}
	cleaned, _, _ = strings.Cut(cleaned, "#")
	if !isWechatArticleURL(cleaned) {
		return "", false
	}
	// 搜一搜给的是 http 链接；FoodTalks 页面里一律链 https（微信本来就会跳过去）。
	if rest, ok := strings.CutPrefix(cleaned, "http://mp.weixin.qq.com/"); ok {
		cleaned = "https://mp.weixin.qq.com/" + rest
	}
	if _, err := normalizeSiteSourceKey(cleaned); err != nil {
		return "", false
	}
	return cleaned, true
}

func pickWechatWorkURL(items []wechatWorkItem, wantTitle, wantSource string) string {
	for _, item := range items {
		if normalizeOriginalTitle(item.Title) != wantTitle {
			continue
		}
		if normalizeFoodTalksSourceName(item.Author) != wantSource {
			continue
		}
		articleURL, ok := normalizeWechatWorkURL(item.URL)
		if !ok {
			continue
		}
		return articleURL
	}
	return ""
}
