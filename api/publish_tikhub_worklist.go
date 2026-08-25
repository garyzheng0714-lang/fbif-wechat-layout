package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// 自家号转发缺「阅读原文」、Feed 又还没订这个公众号时，用 TikHub 补微信原文链接。
// 先按标题搜文章，作者和标题都对上才用；搜不到再按号名搜唯一公众号、拉文章列表。
// 失败只降级，不让发布任务失败。

const (
	tikhubSearchPath          = "/api/v1/wechat_search/v2/fetch_search"
	tikhubAccountArticlesPath = "/api/v1/wechat_mp/v2/fetch_account_articles"
	defaultTikhubAPIBase      = "https://api.tikhub.dev"
	tikhubLookupTimeout       = 70 * time.Second
	tikhubRequestTimeout      = 30 * time.Second
	tikhubSearchMaxPages      = 2
	tikhubWorkListMaxPages    = 3
	tikhubWorkListPageSize    = 20
	tikhubSuccessCode         = 200
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

func defaultLookupWechatOriginalArticle(ctx context.Context, title, sourceName string) string {
	title = strings.TrimSpace(title)
	sourceName = strings.TrimSpace(sourceName)
	if title == "" || sourceName == "" || tikhubAPIKey() == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	wantTitle := normalizeOriginalTitle(title)
	wantSource := normalizeFoodTalksSourceName(sourceName)
	if wantTitle == "" || wantSource == "" {
		return ""
	}
	budget, cancel := context.WithTimeout(ctx, tikhubLookupTimeout)
	defer cancel()
	if found := lookupTikhubArticleSearch(budget, title, wantTitle, wantSource); found != "" {
		return found
	}
	account := resolveTikhubAccount(budget, sourceName)
	if account == "" {
		return ""
	}
	return lookupTikhubAccountArticles(budget, account, wantTitle, wantSource)
}

func lookupTikhubArticleSearch(ctx context.Context, keyword, wantTitle, wantSource string) string {
	cursor := ""
	seenCursor := map[string]bool{}
	for page := 0; page < tikhubSearchMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return ""
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
			return ""
		}
		if found := pickWechatWorkURL(collectWechatWorkItems(data), wantTitle, wantSource, true); found != "" {
			return found
		}
		next := tikhubString(data, "cursor")
		if next == "" || seenCursor[next] || !tikhubFlag(data, "continue_flag", "continueFlag") {
			return ""
		}
		seenCursor[next] = true
		cursor = next
	}
	return ""
}

func resolveTikhubAccount(ctx context.Context, sourceName string) string {
	data, err := tikhubPOST(ctx, tikhubSearchPath, map[string]any{
		"keyword":       sourceName,
		"business_type": "account",
		"raw":           false,
	})
	if err != nil {
		log.Printf("publish: TikHub 搜公众号失败（%s）：%v", sourceName, err)
		return ""
	}
	want := normalizeFoodTalksSourceName(sourceName)
	var exact []string
	seen := map[string]bool{}
	for _, account := range collectTikhubAccounts(data) {
		if normalizeFoodTalksSourceName(account.Name) != want {
			continue
		}
		if account.Username == "" || seen[account.Username] {
			continue
		}
		seen[account.Username] = true
		exact = append(exact, account.Username)
	}
	if len(exact) != 1 {
		return ""
	}
	return exact[0]
}

func lookupTikhubAccountArticles(ctx context.Context, username, wantTitle, wantSource string) string {
	offset := ""
	seenOffset := map[string]bool{}
	for page := 0; page < tikhubWorkListMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return ""
		}
		payload := map[string]any{
			"username":  username,
			"page_size": tikhubWorkListPageSize,
			"raw":       false,
		}
		if offset != "" {
			payload["offset"] = offset
		}
		data, err := tikhubPOST(ctx, tikhubAccountArticlesPath, payload)
		if err != nil {
			log.Printf("publish: TikHub 拉公众号文章失败：%v", err)
			return ""
		}
		items := collectWechatWorkItems(data)
		if found := pickWechatWorkURL(items, wantTitle, wantSource, false); found != "" {
			return found
		}
		if tikhubFlag(data, "is_end", "isEnd") {
			return ""
		}
		next := tikhubString(data, "next_offset", "nextOffset")
		if next == "" || seenOffset[next] {
			return ""
		}
		seenOffset[next] = true
		offset = next
	}
	return ""
}

type wechatWorkItem struct {
	Title  string
	URL    string
	Author string
}

type tikhubAccount struct {
	Username string
	Name     string
}

func tikhubPOST(ctx context.Context, path string, payload any) (any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, tikhubRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tikhubAPIBase()+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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

func collectTikhubAccounts(v any) []tikhubAccount {
	var out []tikhubAccount
	seen := map[string]bool{}
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if account, ok := extractTikhubAccount(n); ok {
				if !seen[account.Username] {
					seen[account.Username] = true
					out = append(out, account)
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

func extractTikhubAccount(m map[string]any) (tikhubAccount, bool) {
	username := firstGHUsername(m)
	name := stripSearchHighlight(firstString(m, "nickName", "nick_name", "title", "name", "accountName", "account_name"))
	if jump, ok := nestedMap(m, "jumpInfo", "jump_info"); ok {
		if username == "" {
			username = firstGHUsername(jump)
		}
		if name == "" || ghUsernameRe.MatchString(name) {
			name = stripSearchHighlight(firstString(jump, "nickName", "nick_name", "title", "name"))
		}
	}
	if username == "" {
		return tikhubAccount{}, false
	}
	return tikhubAccount{Username: username, Name: name}, true
}

func firstGHUsername(m map[string]any) string {
	for _, key := range []string{"userName", "username", "user_name", "biz_username", "wxId", "wx_id"} {
		s := strings.TrimSpace(fmt.Sprint(m[key]))
		if ghUsernameRe.MatchString(s) {
			return s
		}
	}
	return ""
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
	if _, err := normalizeSiteSourceKey(cleaned); err != nil {
		return "", false
	}
	return cleaned, true
}

func pickWechatWorkURL(items []wechatWorkItem, wantTitle, wantSource string, requireAuthor bool) string {
	for _, item := range items {
		if normalizeOriginalTitle(item.Title) != wantTitle {
			continue
		}
		author := normalizeFoodTalksSourceName(item.Author)
		if requireAuthor {
			if author != wantSource {
				continue
			}
		} else if author != "" && author != wantSource {
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
