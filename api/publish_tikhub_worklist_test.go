package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDefaultLookupWechatPrefersTitleSearch(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer ak_test" {
			t.Fatalf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != tikhubSearchPath {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if !strings.Contains(string(raw), `"keyword":"餐饮零食化，火了猪油渣"`) ||
			!strings.Contains(string(raw), `"business_type":"article"`) {
			t.Fatalf("search body = %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"items":[
			{"title":"<em class=\"highlight\">餐饮零食化，火了猪油渣</em>","doc_url":"http://mp.weixin.qq.com/s?__biz=MzA5MjMwNTgyNw==&mid=1&idx=4&sn=aaa#rd","source":{"title":"中经视野"}},
			{"title":"<em class=\"highlight\">餐饮零食化，火了猪油渣</em>","doc_url":"http://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1&sn=ddd22422243a86a2e72d2c3b107f4156#rd","source":{"title":"惊蛰研究所"}},
			{"title":"餐饮零食化，火了猪油渣","doc_url":"https://mp.weixin.qq.com/s?__biz=MzkxNzAwMDkwNQ==&mid=1&idx=3&sn=bbb","source":{"title":"36氪"}}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所", nil).URL
	if !strings.Contains(got, "mid=2247499622") || strings.Contains(got, "#rd") {
		t.Fatalf("got %q", got)
	}
	if strings.Join(paths, ",") != tikhubSearchPath {
		t.Fatalf("paths = %v", paths)
	}
}

func TestDefaultLookupWechatWideSearchDoesNotCallAccountAPIs(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"business_type":"account"`) {
			t.Fatal("广域搜不到不得再搜公众号")
		}
		if r.URL.Path != tikhubSearchPath {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"items":[
			{"title":"餐饮零食化，火了猪油渣","doc_url":"https://mp.weixin.qq.com/s?__biz=MzkxNzAwMDkwNQ==&mid=1&idx=3&sn=bbb","source":{"title":"刺猬公社"}}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	if got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所", nil).URL; got != "" {
		t.Fatalf("作者对不上不得采用：%q", got)
	}
	if strings.Join(paths, ",") != tikhubSearchPath {
		t.Fatalf("paths = %v", paths)
	}
}

func TestDefaultLookupWechatOriginalArticleSkipsWithoutKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("没有密钥时不得发请求")
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "")
	if got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所", nil).URL; got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultLookupWechatOriginalArticleIgnoresWrongAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"items":[]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	if got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所", nil).URL; got != "" {
		t.Fatalf("作者对不上不得采用：%q", got)
	}
}

func TestResolveRepostOriginalURLPrefersWechatLookupBeforeSearch(t *testing.T) {
	lookupFeedOriginalArticleDetail = func(context.Context, string, string) feedOriginalArticleHit {
		return feedOriginalArticleHit{}
	}
	lookupWechatOriginalArticle = func(context.Context, string, string, []string) repostOriginalArticle {
		return repostOriginalArticle{URL: "https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1"}
	}
	searchOriginalArticleURLs = func(context.Context, string, string) []string {
		t.Fatal("广域搜已命中时不应再去网页检索")
		return nil
	}
	t.Cleanup(func() {
		lookupFeedOriginalArticleDetail = defaultLookupFeedOriginalArticleDetail
		lookupWechatOriginalArticle = defaultLookupWechatOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
	})
	got := resolveRepostOriginal(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所",
		"https://mp.weixin.qq.com/s/fbif-repost", nil).URL
	if !strings.Contains(got, "__biz=MzkzNzI3NTc1Mg==") {
		t.Fatalf("got %q", got)
	}
}

func TestLiveTikhubWorkListLookup(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TIKHUB_LIVE")) != "1" || tikhubAPIKey() == "" {
		t.Skip("set TIKHUB_LIVE=1 and TIKHUB_API_KEY to hit the real API")
	}
	got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所", nil).URL
	if !strings.Contains(got, "mp.weixin.qq.com") {
		t.Fatalf("live lookup missed: %q", got)
	}
	t.Logf("live work url = %s", got)
}

// retitledOriginalFixture 2026-10-14 #274 实例：FBIF 把窄播的《社区烘焙第一品牌，是怎么跑出来的｜对话》
// 改成《3年开出300家门店，社区烘焙第一品牌是怎么跑出来的》转载，阅读原文为空。
var retitledOriginalBody = []string{
	"作者：窄播",
	"孙北北的效率答案不是省成本，是改结构：把生产搬到工厂、把门店做小、把品类收窄到社区刚需。",
	"三年时间，孙北北在深圳、广州、东莞开出近300家门店，9.9元的水牛乳吐司成了社区里的固定早餐。",
	"它没有请高薪面包师傅，而是把中央工厂做成了真正的核心资产，门店只负责最后一道烘烤与陈列。",
	"会员超过240万，女性占比超过七成，复购率是商场店的两倍多，这是社区烘焙最值钱的一组数字。",
}

func retitledOriginalPage(title, nickname string, paras []string) string {
	var b strings.Builder
	b.WriteString(`<html><head><meta property="og:title" content="` + title + `"></head><body>`)
	b.WriteString(`<strong class="profile_nickname">` + nickname + `</strong><div id="js_content">`)
	for _, p := range paras {
		b.WriteString(`<p><span style="color:#333">` + p + `</span></p>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

func TestDefaultLookupWechatFindsRetitledOriginalByBody(t *testing.T) {
	const originalURL = "http://mp.weixin.qq.com/s?__biz=MzkxMzMwNTkxOA==&mid=2247500001&idx=1&sn=orig&chksm=x#rd"
	const sameAuthorOtherURL = "http://mp.weixin.qq.com/s?__biz=MzkxMzMwNTkxOA==&mid=2247499000&idx=1&sn=other&chksm=y#rd"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"items":[
			{"title":"3年开出300家门店，社区烘焙第一品牌是怎么跑出来的","doc_url":"http://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=1&idx=1&sn=fbif","source":{"title":"FBIF食品饮料创新"}},
			{"title":"另一篇：烘焙赛道的价格战","doc_url":"` + sameAuthorOtherURL + `","source":{"title":"窄播"}},
			{"title":"社区烘焙第一品牌，是怎么跑出来的｜对话","doc_url":"` + originalURL + `","source":{"title":"窄播"}},
			{"title":"社区烘焙第一品牌，是怎么跑出来的","doc_url":"http://mp.weixin.qq.com/s?__biz=MjM5NzMyODk4MA==&mid=1&idx=1&sn=mba","source":{"title":"MBA"}}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	var fetched []string
	fetchOriginalArticlePage = func(raw string) ([]byte, string, error) {
		fetched = append(fetched, raw)
		if strings.Contains(raw, "sn=orig") {
			return []byte(retitledOriginalPage("社区烘焙第一品牌，是怎么跑出来的｜对话", "窄播", retitledOriginalBody[1:])), raw, nil
		}
		return []byte(retitledOriginalPage("另一篇：烘焙赛道的价格战", "窄播", []string{"同一个号的另一篇稿子，讲的是烘焙价格战，正文和本稿完全不同。"})), raw, nil
	}
	t.Cleanup(func() { fetchOriginalArticlePage = fetchRawHTML })

	got := defaultLookupWechatOriginalArticle(context.Background(), "3年开出300家门店，社区烘焙第一品牌是怎么跑出来的", "窄播", retitledOriginalBody)
	if !strings.HasPrefix(got.URL, "https://mp.weixin.qq.com/s?") || !strings.Contains(got.URL, "sn=orig") || strings.Contains(got.URL, "#rd") ||
		got.Title != "社区烘焙第一品牌，是怎么跑出来的｜对话" {
		t.Fatalf("应按正文找到改过标题的原文并带回原标题：%+v", got)
	}
	if len(fetched) != 2 || !strings.Contains(fetched[0], "sn=other") {
		t.Fatalf("只抓作者是来源号的候选，同号别篇正文对不上要跳过：%v", fetched)
	}
	for _, raw := range fetched {
		if strings.Contains(raw, "sn=fbif") || strings.Contains(raw, "sn=mba") {
			t.Fatalf("自家转载页与别的号不得去抓：%s", raw)
		}
	}

	// 没有正文可比对时，作者对上、标题不同也不采用（不抓页）。
	fetched = nil
	if got := defaultLookupWechatOriginalArticle(context.Background(), "3年开出300家门店，社区烘焙第一品牌是怎么跑出来的", "窄播", nil); got.URL != "" || len(fetched) != 0 {
		t.Fatalf("没有正文核验不得采用改过标题的候选：%+v fetched=%v", got, fetched)
	}
}

func TestDefaultLookupWechatFallsBackToLeadSentence(t *testing.T) {
	const originalURL = "http://mp.weixin.qq.com/s?__biz=Mzk0OTUzNDUzNg==&mid=2247500002&idx=1&sn=liangcha&chksm=z"
	var keywords []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Keyword string `json:"keyword"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		keywords = append(keywords, req.Keyword)
		w.Header().Set("Content-Type", "application/json")
		if len(keywords) == 1 {
			_, _ = w.Write([]byte(`{"code":200,"data":{"items":[{"title":"无关","doc_url":"http://mp.weixin.qq.com/s?__biz=MzE5&mid=1&idx=1&sn=x","source":{"title":"笃定老梁"}}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"items":[{"title":"去潮苦水：把传统非遗凉茶带进高奢场","doc_url":"` + originalURL + `","source":{"title":"品牌数读"}}]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	body := []string{
		"当凉茶在岭南街边还是几块钱一碗的“苦药”，去潮苦水把它带进了高奢商场。这是一次关于文化溢价的尝试。",
		"38元一杯的凉茶，卖的不只是草本，而是一整套关于岭南生活方式的叙事，门店设计也走了极简路线。",
		"品牌创始人说，凉茶的问题从来不是苦，而是没有人认真讲过它的故事，年轻人其实愿意为故事买单。",
	}
	fetchOriginalArticlePage = func(raw string) ([]byte, string, error) {
		return []byte(retitledOriginalPage("去潮苦水：把传统非遗凉茶带进高奢场，如何做“东方草本”的文化溢价？｜对话新势力", "品牌数读", body)), raw, nil
	}
	t.Cleanup(func() { fetchOriginalArticlePage = fetchRawHTML })

	got := defaultLookupWechatOriginalArticle(context.Background(), "把传统凉茶带进高奢商场，靠什么把单价拉到38元？", "品牌数读", body)
	if !strings.Contains(got.URL, "sn=liangcha") || !strings.HasPrefix(got.Title, "去潮苦水") {
		t.Fatalf("标题搜不到时应按正文第一句搜到原文：%+v", got)
	}
	if len(keywords) != 2 || keywords[1] != "当凉茶在岭南街边还是几块钱一碗的“苦药”，去潮苦水把它带" {
		t.Fatalf("第二次应按正文第一句搜：%q", keywords)
	}
}
