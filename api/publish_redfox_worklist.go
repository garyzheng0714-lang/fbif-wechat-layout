package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// 自家号转发缺「阅读原文」、Feed 又还没订这个公众号时，今天看啥拉不到文章列表。
// 先按标题搜作品（优质库再广域库），作者和标题都对上才用；搜不到再按号拉作品列表。
// 失败只降级，不让发布任务失败。

const (
	redfoxPremiumSearchArticlePath = "/story/api/gzhData/searchArticle"
	redfoxWideSearchArticlePath    = "/story/api/gzh/data/searchArticle"
	redfoxPremiumWorkListPath      = "/story/api/gzhData/queryWorkList"
	redfoxWideWorkListPath         = "/story/api/gzh/data/queryWorkList"
	redfoxWideSearchUserPath       = "/story/api/gzh/data/searchUser"
	defaultRedfoxAPIBase           = "https://redfox.hk"
	redfoxWorkListTimeout          = 16 * time.Second
	redfoxRequestTimeout           = 5 * time.Second
	redfoxSearchMaxPages           = 2
	redfoxWorkListMaxPages         = 3
	redfoxWorkListPageSize         = 20
	redfoxSuccessCode              = 2000
	redfoxNotInCatalogCode         = 3203
)

var lookupRedfoxOriginalArticle = defaultLookupRedfoxOriginalArticle

func redfoxAPIBase() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("REDFOX_API_BASE_URL")), "/")
	if base == "" {
		return defaultRedfoxAPIBase
	}
	return base
}

func redfoxAPIKey() string {
	return strings.TrimSpace(os.Getenv("REDFOX_API_KEY"))
}

func defaultLookupRedfoxOriginalArticle(ctx context.Context, title, sourceName string) string {
	title = strings.TrimSpace(title)
	sourceName = strings.TrimSpace(sourceName)
	if title == "" || sourceName == "" || redfoxAPIKey() == "" {
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
	budget, cancel := context.WithTimeout(ctx, redfoxWorkListTimeout)
	defer cancel()
	if found := lookupRedfoxWorkPages(budget, wantTitle, wantSource, redfoxPremiumSearchQuery(title), redfoxSearchMaxPages, true); found != "" {
		return found
	}
	if found := lookupRedfoxWorkPages(budget, wantTitle, wantSource, redfoxWideSearchQuery(title), redfoxSearchMaxPages, true); found != "" {
		return found
	}
	if found := lookupRedfoxWorkPages(budget, wantTitle, wantSource, redfoxPremiumWorkQuery(sourceName), redfoxWorkListMaxPages, false); found != "" {
		return found
	}
	account := resolveRedfoxWideAccount(budget, sourceName)
	if account.Empty() {
		return ""
	}
	return lookupRedfoxWorkPages(budget, wantTitle, wantSource, account.wideWorkQuery, redfoxWorkListMaxPages, false)
}

type redfoxWorkQuery func(offset int) (path string, payload map[string]any)

func redfoxPremiumSearchQuery(title string) redfoxWorkQuery {
	return func(offset int) (string, map[string]any) {
		return redfoxPremiumSearchArticlePath, map[string]any{
			"keyword":  title,
			"offset":   offset,
			"sortType": "_0",
		}
	}
}

func redfoxWideSearchQuery(title string) redfoxWorkQuery {
	return func(offset int) (string, map[string]any) {
		return redfoxWideSearchArticlePath, map[string]any{
			"keyword":  title,
			"offset":   offset,
			"sortType": "0",
		}
	}
}

func redfoxPremiumWorkQuery(accountName string) redfoxWorkQuery {
	return func(offset int) (string, map[string]any) {
		return redfoxPremiumWorkListPath, map[string]any{
			"accountName": accountName,
			"offset":      offset,
			"sortType":    "_2",
		}
	}
}

func lookupRedfoxWorkPages(ctx context.Context, wantTitle, wantSource string, query redfoxWorkQuery, maxPages int, requireAuthor bool) string {
	if query == nil {
		return ""
	}
	if maxPages <= 0 {
		maxPages = redfoxWorkListMaxPages
	}
	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return ""
		}
		offset := page * redfoxWorkListPageSize
		path, payload := query(offset)
		items, hasMore, err := queryRedfoxWorkList(ctx, path, payload)
		if err != nil {
			log.Printf("publish: 公众号作品库查询失败：%v", err)
			return ""
		}
		if found := pickRedfoxWorkURL(items, wantTitle, wantSource, requireAuthor); found != "" {
			return found
		}
		if !hasMore || len(items) == 0 {
			return ""
		}
	}
	return ""
}

type redfoxAccountID struct {
	Account string
	WxID    string
	BizInfo string
}

func (id redfoxAccountID) Empty() bool {
	return strings.TrimSpace(id.WxID) == "" && strings.TrimSpace(id.BizInfo) == "" && strings.TrimSpace(id.Account) == ""
}

func (id redfoxAccountID) wideWorkQuery(offset int) (string, map[string]any) {
	payload := map[string]any{"offset": offset, "sortType": "2"}
	switch {
	case strings.TrimSpace(id.WxID) != "":
		payload["wxId"] = strings.TrimSpace(id.WxID)
	case strings.TrimSpace(id.BizInfo) != "":
		payload["bizInfo"] = strings.TrimSpace(id.BizInfo)
	default:
		payload["account"] = strings.TrimSpace(id.Account)
	}
	return redfoxWideWorkListPath, payload
}

func resolveRedfoxWideAccount(ctx context.Context, sourceName string) redfoxAccountID {
	payload := map[string]any{
		"keyword":  sourceName,
		"pageNum":  1,
		"pageSize": 20,
	}
	body, err := redfoxPOST(ctx, redfoxWideSearchUserPath, payload)
	if err != nil {
		log.Printf("publish: 公众号作品库解析微信号失败（%s）：%v", sourceName, err)
		return redfoxAccountID{}
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			List []struct {
				Account     string `json:"account"`
				AccountName string `json:"accountName"`
				WxID        string `json:"wxId"`
				BizInfo     string `json:"bizInfo"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		log.Printf("publish: 公众号作品库解析微信号失败（%s）：%v", sourceName, err)
		return redfoxAccountID{}
	}
	if parsed.Code != redfoxSuccessCode {
		return redfoxAccountID{}
	}
	want := normalizeFoodTalksSourceName(sourceName)
	var exact []redfoxAccountID
	for _, item := range parsed.Data.List {
		if normalizeFoodTalksSourceName(item.AccountName) != want {
			continue
		}
		exact = append(exact, redfoxAccountID{
			Account: strings.TrimSpace(item.Account),
			WxID:    strings.TrimSpace(item.WxID),
			BizInfo: strings.TrimSpace(item.BizInfo),
		})
	}
	if len(exact) != 1 || exact[0].Empty() {
		return redfoxAccountID{}
	}
	return exact[0]
}

type redfoxWorkItem struct {
	Title      string `json:"title"`
	WorkURL    string `json:"workUrl"`
	Author     string `json:"author"`
	IsOriginal int    `json:"isOriginal"`
}

func queryRedfoxWorkList(ctx context.Context, path string, payload map[string]any) ([]redfoxWorkItem, bool, error) {
	body, err := redfoxPOST(ctx, path, payload)
	if err != nil {
		return nil, false, err
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			HasMore json.RawMessage `json:"hasMore"`
			Total   int             `json:"total"`
			List    []redfoxWorkItem
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false, err
	}
	if parsed.Code != redfoxSuccessCode {
		if parsed.Code == redfoxNotInCatalogCode {
			return nil, false, nil
		}
		return nil, false, errRedfoxWorkList(parsed.Code, parsed.Msg)
	}
	offset := 0
	if payload != nil {
		if n, ok := payload["offset"].(int); ok {
			offset = n
		}
	}
	return parsed.Data.List, redfoxListHasMore(parsed.Data.HasMore, len(parsed.Data.List), offset, parsed.Data.Total), nil
}

func redfoxPOST(ctx context.Context, path string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, redfoxRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, redfoxAPIBase()+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("REDFOX_API_KEY", redfoxAPIKey())
	req.Header.Set("X-API-KEY", redfoxAPIKey())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readBoundedBody(resp.Body, 1<<20)
}

type redfoxWorkListError struct {
	code int
	msg  string
}

func (e redfoxWorkListError) Error() string {
	if strings.TrimSpace(e.msg) != "" {
		return e.msg
	}
	return "公众号作品库返回异常"
}

func errRedfoxWorkList(code int, msg string) error {
	return redfoxWorkListError{code: code, msg: strings.TrimSpace(msg)}
}

func redfoxHasMore(raw json.RawMessage, listLen int) bool {
	s := strings.ToLower(strings.Trim(strings.TrimSpace(string(raw)), `"`))
	switch s {
	case "true", "1":
		return true
	case "false", "0", "null":
		return false
	}
	return listLen >= redfoxWorkListPageSize
}

func redfoxListHasMore(raw json.RawMessage, listLen, offset, total int) bool {
	if total > 0 {
		return offset+listLen < total
	}
	return redfoxHasMore(raw, listLen)
}

func normalizeRedfoxWorkURL(raw string) (string, bool) {
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

func pickRedfoxWorkURL(items []redfoxWorkItem, wantTitle, wantSource string, requireAuthor bool) string {
	var fallback string
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
		articleURL, ok := normalizeRedfoxWorkURL(item.WorkURL)
		if !ok {
			continue
		}
		if item.IsOriginal == 1 {
			return articleURL
		}
		if fallback == "" {
			fallback = articleURL
		}
	}
	return fallback
}
