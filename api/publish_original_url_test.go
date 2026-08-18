package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOriginalArticlePageMatchesTitleAndSource(t *testing.T) {
	page := `<html><head><meta property="og:title" content="餐饮零食化，火了猪油渣-钛媒体官方网站"></head>
<body>本文系作者 惊蛰研究所 授权钛媒体发表，作者：娅沁</body></html>`
	if !originalArticlePageMatches(page, "餐饮零食化，火了猪油渣", "惊蛰研究所") {
		t.Fatal("标题和出处都对得上时应判为原文")
	}
	if originalArticlePageMatches(page, "餐饮零食化，火了猪油渣", "小食代") {
		t.Fatal("出处对不上不得判为原文")
	}
	if originalArticlePageMatches(page, "另一篇完全不同的标题", "惊蛰研究所") {
		t.Fatal("标题对不上不得判为原文")
	}
}

func TestSanitizeOriginalURLCandidateDropsSearchHosts(t *testing.T) {
	if _, ok := sanitizeOriginalURLCandidate("https://cn.bing.com/search?q=x"); ok {
		t.Fatal("检索页自己不得当候选")
	}
	if _, ok := sanitizeOriginalURLCandidate("https://www.foodtalks.cn/news/1"); ok {
		t.Fatal("本站成品页不得当原文候选")
	}
	got, ok := sanitizeOriginalURLCandidate("https://www.tmtpost.com/8103277.html")
	if !ok || !strings.Contains(got, "tmtpost.com/8103277") {
		t.Fatalf("钛媒体原文应保留：%q ok=%v", got, ok)
	}
}

func TestResolveRepostOriginalURLPrefersFeedLookup(t *testing.T) {
	t.Cleanup(func() {
		lookupFeedOriginalArticle = defaultLookupFeedOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
		fetchOriginalArticlePage = fetchRawHTML
	})
	lookupFeedOriginalArticle = func(context.Context, string, string) string {
		return "http://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg&mid=1&idx=1&sn=abc"
	}
	searchOriginalArticleURLs = func(context.Context, string, string) []string {
		t.Fatal("Feed 已命中时不应再去网页检索")
		return nil
	}
	got := resolveRepostOriginalURL(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所",
		"https://mp.weixin.qq.com/s/fbif-repost")
	if !strings.Contains(got, "__biz=MzkzNzI3NTc1Mg") {
		t.Fatalf("应采用 Feed 返回的原号链接：%q", got)
	}
}

func TestDefaultLookupFeedOriginalArticle(t *testing.T) {
	var gotTitle, gotSource, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Publish-Sync-Token")
		if r.URL.Path != "/api/internal/original-article" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var req struct {
			Title      string `json:"title"`
			SourceName string `json:"source_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		gotTitle, gotSource = req.Title, req.SourceName
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"found","url":"http://mp.weixin.qq.com/s?__biz=feed"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FOODTALKS_FEED_BASE_URL", srv.URL)
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", "sync-secret")
	got := defaultLookupFeedOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if gotTitle != "餐饮零食化，火了猪油渣" || gotSource != "惊蛰研究所" || gotToken != "sync-secret" {
		t.Fatalf("request title=%q source=%q token=%q", gotTitle, gotSource, gotToken)
	}
	if !strings.Contains(got, "__biz=feed") {
		t.Fatalf("got %q", got)
	}
}

func TestResolveRepostOriginalURLUsesVerifiedCandidate(t *testing.T) {
	lookupFeedOriginalArticle = func(context.Context, string, string) string { return "" }
	t.Cleanup(func() {
		lookupFeedOriginalArticle = defaultLookupFeedOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
		fetchOriginalArticlePage = fetchRawHTML
	})
	searchOriginalArticleURLs = func(context.Context, string, string) []string {
		return []string{
			"https://cn.bing.com/search?q=x",
			"https://www.foodtalks.cn/news/1",
			"https://www.tmtpost.com/8103277.html",
		}
	}
	fetchOriginalArticlePage = func(raw string) ([]byte, string, error) {
		if !strings.Contains(raw, "tmtpost.com") {
			t.Fatalf("不应去核验被过滤的候选：%s", raw)
		}
		return []byte(`<meta property="og:title" content="餐饮零食化，火了猪油渣"><p>惊蛰研究所</p>`), raw, nil
	}
	got := resolveRepostOriginalURL(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所",
		"https://mp.weixin.qq.com/s/fbif-repost")
	if got != "https://www.tmtpost.com/8103277.html" {
		t.Fatalf("应采用核验通过的原文：%q", got)
	}
}

func TestResolveRepostOriginalURLRejectsUnverifiedCandidate(t *testing.T) {
	lookupFeedOriginalArticle = func(context.Context, string, string) string { return "" }
	t.Cleanup(func() {
		lookupFeedOriginalArticle = defaultLookupFeedOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
		fetchOriginalArticlePage = fetchRawHTML
	})
	searchOriginalArticleURLs = func(context.Context, string, string) []string {
		return []string{"https://example.com/unrelated"}
	}
	fetchOriginalArticlePage = func(raw string) ([]byte, string, error) {
		return []byte(`<title>无关文章</title><p>别的媒体</p>`), raw, nil
	}
	if got := resolveRepostOriginalURL(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所", ""); got != "" {
		t.Fatalf("核验不过应空着：%q", got)
	}
}

func TestNormalizeSubmittedOriginalSourceURL(t *testing.T) {
	if got := normalizeSubmittedOriginalSourceURL("https://www.tmtpost.com/8103277.html"); got != "https://www.tmtpost.com/8103277.html" {
		t.Fatalf("合法出处应保留：%q", got)
	}
	if got := normalizeSubmittedOriginalSourceURL("javascript:alert(1)"); got != "" {
		t.Fatalf("非 http(s) 应丢弃：%q", got)
	}
	if got := preferOriginalSourceURL("", "https://www.tmtpost.com/8103277.html"); got != "https://www.tmtpost.com/8103277.html" {
		t.Fatalf("网页为空时应保留提交的阅读原文：%q", got)
	}
	if got := preferOriginalSourceURL("https://mp.weixin.qq.com/s/from-page", "https://www.tmtpost.com/8103277.html"); got != "https://mp.weixin.qq.com/s/from-page" {
		t.Fatalf("网页抓到的阅读原文应优先：%q", got)
	}
}

func TestPublishJobIsOwnAccountRepost(t *testing.T) {
	own := publishJob{
		SourceName: "FBIF食品饮料创新",
		BlocksJSON: `[{"k":"txt","text":"来源：惊蛰研究所（ID：jingzheyanjiusuo）"}]`,
	}
	if !publishJobIsOwnAccountRepost(own) {
		t.Fatal("自家号转发应判定为自家转载")
	}
	external := publishJob{
		SourceName: "惊蛰研究所",
		BlocksJSON: `[{"k":"txt","text":"正文"}]`,
	}
	if publishJobIsOwnAccountRepost(external) {
		t.Fatal("外号原文不是自家转载")
	}
}
