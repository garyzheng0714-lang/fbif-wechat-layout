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
	got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "mid=2247499622") || strings.Contains(got, "#rd") {
		t.Fatalf("got %q", got)
	}
	if strings.Join(paths, ",") != tikhubSearchPath {
		t.Fatalf("paths = %v", paths)
	}
}

func TestDefaultLookupWechatFallsBackToAccountList(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case tikhubSearchPath:
			if strings.Contains(string(raw), `"business_type":"article"`) {
				_, _ = w.Write([]byte(`{"code":200,"data":{"items":[
					{"title":"餐饮零食化，火了猪油渣","jumpInfo":{"nickName":"刺猬公社","url":"https://mp.weixin.qq.com/s?__biz=MzkxNzAwMDkwNQ==&mid=1&idx=3&sn=bbb"}}
				]}}`))
				return
			}
			if !strings.Contains(string(raw), `"keyword":"惊蛰研究所"`) ||
				!strings.Contains(string(raw), `"business_type":"account"`) {
				t.Fatalf("account search body = %s", raw)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"items":[
				{"title":"惊蛰研究所","jumpInfo":{"userName":"gh_0c90bd6e9b47","nickName":"惊蛰研究所"}},
				{"title":"惊蛰研究院","jumpInfo":{"userName":"gh_other","nickName":"惊蛰研究院"}}
			]}}`))
		case tikhubAccountArticlesPath:
			if !strings.Contains(string(raw), `"username":"gh_0c90bd6e9b47"`) {
				t.Fatalf("article list body = %s", raw)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"is_end":1,"articles":[
				{"title":"餐饮零食化，火了猪油渣","url":"https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1&sn=ddd22422243a86a2e72d2c3b107f4156#rd"}
			]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "mid=2247499622") || strings.Contains(got, "#rd") {
		t.Fatalf("got %q", got)
	}
	wantPaths := []string{tikhubSearchPath, tikhubSearchPath, tikhubAccountArticlesPath}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("paths = %v", paths)
	}
}

func TestDefaultLookupWechatPagesAccountArticles(t *testing.T) {
	var offsets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == tikhubSearchPath {
			if strings.Contains(string(raw), `"business_type":"article"`) {
				_, _ = w.Write([]byte(`{"code":200,"data":{"items":[]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"items":[
				{"jumpInfo":{"userName":"gh_0c90bd6e9b47","nickName":"惊蛰研究所"}}
			]}}`))
			return
		}
		if r.URL.Path != tikhubAccountArticlesPath {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var req struct {
			Offset string `json:"offset"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, req.Offset)
		if req.Offset == "" {
			_, _ = w.Write([]byte(`{"code":200,"data":{"is_end":0,"next_offset":"page2","articles":[
				{"title":"短剧换脸、选秀出道，AI开始“批量造星”了","url":"https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=1&idx=1&sn=aaa"}
			]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"is_end":1,"articles":[
			{"title":"餐饮零食化，火了猪油渣","url":"https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1&sn=ddd22422243a86a2e72d2c3b107f4156"}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	got := defaultLookupWechatOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "mid=2247499622") {
		t.Fatalf("got %q", got)
	}
	if len(offsets) != 2 || offsets[0] != "" || offsets[1] != "page2" {
		t.Fatalf("offsets = %v", offsets)
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

func TestResolveTikhubAccountRequiresExactUniqueName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"items":[
			{"jumpInfo":{"userName":"gh_1","nickName":"惊蛰研究所"}},
			{"jumpInfo":{"userName":"gh_2","nickName":"惊蛰研究所"}}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	if got := resolveTikhubAccount(context.Background(), "惊蛰研究所"); got != "" {
		t.Fatalf("重名应放弃：%q", got)
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
