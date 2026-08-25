package main

import (
	"context"
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
	got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
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
	if got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所"); got != "" {
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
	if got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所"); got != "" {
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
	if got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所"); got != "" {
		t.Fatalf("作者对不上不得采用：%q", got)
	}
}

func TestResolveRepostOriginalURLPrefersWechatLookupBeforeSearch(t *testing.T) {
	lookupFeedOriginalArticleDetail = func(context.Context, string, string) feedOriginalArticleHit {
		return feedOriginalArticleHit{}
	}
	lookupWechatOriginalArticle = func(context.Context, string, string) string {
		return "https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1"
	}
	searchOriginalArticleURLs = func(context.Context, string, string) []string {
		t.Fatal("作品库已命中时不应再去网页检索")
		return nil
	}
	t.Cleanup(func() {
		lookupFeedOriginalArticleDetail = defaultLookupFeedOriginalArticleDetail
		lookupWechatOriginalArticle = defaultLookupWechatOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
	})
	got := resolveRepostOriginalURL(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所",
		"https://mp.weixin.qq.com/s/fbif-repost")
	if !strings.Contains(got, "__biz=MzkzNzI3NTc1Mg==") {
		t.Fatalf("got %q", got)
	}
}

func TestLiveTikhubWorkListLookup(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TIKHUB_LIVE")) != "1" || tikhubAPIKey() == "" {
		t.Skip("set TIKHUB_LIVE=1 and TIKHUB_API_KEY to hit the real API")
	}
	got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "mp.weixin.qq.com") {
		t.Fatalf("live lookup missed: %q", got)
	}
	t.Logf("live work url = %s", got)
}
