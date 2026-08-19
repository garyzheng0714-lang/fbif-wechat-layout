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

func TestDefaultLookupRedfoxPrefersTitleSearch(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != redfoxPremiumSearchArticlePath {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if !strings.Contains(string(raw), `"keyword":"餐饮零食化，火了猪油渣"`) || !strings.Contains(string(raw), `"sortType":"_0"`) {
			t.Fatalf("search body = %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":2000,"msg":"成功","data":{"total":3,"hasMore":0,"list":[
			{"title":"餐饮零食化，火了猪油渣","author":"惊蛰研究所","isOriginal":1,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1&sn=ddd22422243a86a2e72d2c3b107f4156#rd"},
			{"title":"餐饮零食化，火了猪油渣","author":"刺猬公社","isOriginal":0,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzkxNzAwMDkwNQ==&mid=1&idx=3&sn=bbb#rd"},
			{"title":"餐饮零食化，火了猪油渣","author":"FBIF食品饮料创新","isOriginal":0,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=1&idx=3&sn=ccc#rd"}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "ak_test")
	got := defaultLookupRedfoxOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "mid=2247499622") || strings.Contains(got, "#rd") {
		t.Fatalf("got %q", got)
	}
	if strings.Join(paths, ",") != redfoxPremiumSearchArticlePath {
		t.Fatalf("paths = %v", paths)
	}
}

func TestDefaultLookupRedfoxUsesWideSearchWhenPremiumSearchMisses(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case redfoxPremiumSearchArticlePath:
			_, _ = w.Write([]byte(`{"code":2000,"data":{"hasMore":0,"list":[
				{"title":"餐饮零食化，火了猪油渣","author":"刺猬公社","isOriginal":0,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzkxNzAwMDkwNQ==&mid=1&idx=3&sn=bbb"}
			]}}`))
		case redfoxWideSearchArticlePath:
			_, _ = w.Write([]byte(`{"code":2000,"data":{"total":6,"list":[
				{"title":"餐饮零食化，火了猪油渣","author":"惊蛰研究所","isOriginal":1,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1&sn=ddd22422243a86a2e72d2c3b107f4156"}
			]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "ak_test")
	got := defaultLookupRedfoxOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "mid=2247499622") {
		t.Fatalf("got %q", got)
	}
	if strings.Join(paths, ",") != redfoxPremiumSearchArticlePath+","+redfoxWideSearchArticlePath {
		t.Fatalf("paths = %v", paths)
	}
}

func TestDefaultLookupRedfoxOriginalArticleMatchesTitle(t *testing.T) {
	var offsets []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == redfoxPremiumSearchArticlePath || r.URL.Path == redfoxWideSearchArticlePath {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":3203,"msg":"未收录"}`))
			return
		}
		if r.URL.Path != redfoxPremiumWorkListPath {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("REDFOX_API_KEY") != "ak_test" {
			t.Fatalf("missing api key")
		}
		var req struct {
			Account     string `json:"account"`
			AccountName string `json:"accountName"`
			Offset      int    `json:"offset"`
			SortType    string `json:"sortType"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Account != "" || req.AccountName != "惊蛰研究所" || req.SortType != "_2" {
			t.Fatalf("request = %+v", req)
		}
		offsets = append(offsets, req.Offset)
		w.Header().Set("Content-Type", "application/json")
		if req.Offset == 0 {
			_, _ = w.Write([]byte(`{"code":2000,"msg":"成功","data":{"total":39,"hasMore":1,"list":[
				{"title":"短剧换脸、选秀出道，AI开始“批量造星”了","author":"惊蛰研究所","isOriginal":1,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=1&idx=1&sn=aaa#rd"}
			]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":2000,"msg":"成功","data":{"total":39,"hasMore":0,"list":[
			{"title":"餐饮零食化，火了猪油渣","author":"惊蛰研究所","isOriginal":1,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1&sn=ddd22422243a86a2e72d2c3b107f4156#rd"}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "ak_test")
	got := defaultLookupRedfoxOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "__biz=MzkzNzI3NTc1Mg==") || !strings.Contains(got, "mid=2247499622") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "#rd") {
		t.Fatalf("应去掉微信链接锚点：%q", got)
	}
	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 20 {
		t.Fatalf("offsets = %v", offsets)
	}
}

func TestDefaultLookupRedfoxOriginalArticleSkipsWithoutKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("没有密钥时不得发请求")
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "")
	if got := defaultLookupRedfoxOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultLookupRedfoxOriginalArticleIgnoresWrongAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":2000,"msg":"成功","data":{"hasMore":0,"list":[
			{"title":"餐饮零食化，火了猪油渣","author":"刺猬公社","isOriginal":0,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzkxNzAwMDkwNQ==&mid=1&idx=1&sn=bbb"}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "ak_test")
	if got := defaultLookupRedfoxOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所"); got != "" {
		t.Fatalf("作者对不上不得采用：%q", got)
	}
}

func TestDefaultLookupRedfoxOriginalArticleTreatsMissingCatalogAsMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":3203,"msg":"优质库暂未收录该内容"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "ak_test")
	if got := defaultLookupRedfoxOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所"); got != "" {
		t.Fatalf("未收录应空着：%q", got)
	}
}

func TestResolveRepostOriginalURLPrefersRedfoxBeforeSearch(t *testing.T) {
	lookupFeedOriginalArticleDetail = func(context.Context, string, string) feedOriginalArticleHit {
		return feedOriginalArticleHit{}
	}
	lookupRedfoxOriginalArticle = func(context.Context, string, string) string {
		return "https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1"
	}
	searchOriginalArticleURLs = func(context.Context, string, string) []string {
		t.Fatal("作品库已命中时不应再去网页检索")
		return nil
	}
	t.Cleanup(func() {
		lookupFeedOriginalArticleDetail = defaultLookupFeedOriginalArticleDetail
		lookupRedfoxOriginalArticle = defaultLookupRedfoxOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
	})
	got := resolveRepostOriginalURL(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所",
		"https://mp.weixin.qq.com/s/fbif-repost")
	if !strings.Contains(got, "__biz=MzkzNzI3NTc1Mg==") {
		t.Fatalf("got %q", got)
	}
}

func TestLiveRedfoxWorkListLookup(t *testing.T) {
	if strings.TrimSpace(os.Getenv("REDFOX_LIVE")) != "1" || redfoxAPIKey() == "" {
		t.Skip("set REDFOX_LIVE=1 and REDFOX_API_KEY to hit the real work list")
	}
	got := defaultLookupRedfoxOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "mp.weixin.qq.com") || !strings.Contains(got, "__biz=") {
		t.Fatalf("live lookup missed: %q", got)
	}
	t.Logf("live work url = %s", got)
}

func TestDefaultLookupRedfoxFallsBackToWideWorkList(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case redfoxPremiumSearchArticlePath, redfoxWideSearchArticlePath, redfoxPremiumWorkListPath:
			_, _ = w.Write([]byte(`{"code":3203,"msg":"优质库暂未收录该内容"}`))
		case redfoxWideSearchUserPath:
			if !strings.Contains(string(raw), `"keyword":"惊蛰研究所"`) {
				t.Fatalf("search body = %s", raw)
			}
			_, _ = w.Write([]byte(`{"code":2000,"msg":"成功","data":{"total":3,"list":[
				{"account":"jingzheyanjiusuo","accountName":"惊蛰研究所","wxId":"gh_0c90bd6e9b47","bizInfo":"MzkzNzI3NTc1Mg=="},
				{"account":"gh_other","accountName":"惊蛰研究院","wxId":"gh_other"}
			]}}`))
		case redfoxWideWorkListPath:
			if !strings.Contains(string(raw), `"wxId":"gh_0c90bd6e9b47"`) {
				t.Fatalf("wide list should prefer wxId: %s", raw)
			}
			if strings.Contains(string(raw), `"sortType":"_2"`) {
				t.Fatalf("广域库 sortType 应为 2：%s", raw)
			}
			_, _ = w.Write([]byte(`{"code":2000,"msg":"成功","data":{"total":249,"list":[
				{"title":"餐饮零食化，火了猪油渣","author":"惊蛰研究所","isOriginal":1,"workUrl":"https://mp.weixin.qq.com/s?__biz=MzkzNzI3NTc1Mg==&mid=2247499622&idx=1&sn=ddd22422243a86a2e72d2c3b107f4156#rd"}
			]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "ak_test")
	got := defaultLookupRedfoxOriginalArticle(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所")
	if !strings.Contains(got, "mid=2247499622") {
		t.Fatalf("got %q", got)
	}
	wantPaths := []string{
		redfoxPremiumSearchArticlePath, redfoxWideSearchArticlePath,
		redfoxPremiumWorkListPath, redfoxWideSearchUserPath, redfoxWideWorkListPath,
	}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("paths = %v", paths)
	}
}

func TestResolveRedfoxWideAccountRequiresExactUniqueName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":2000,"data":{"list":[
			{"account":"a1","accountName":"惊蛰研究所","wxId":"gh_1"},
			{"account":"a2","accountName":"惊蛰研究所","wxId":"gh_2"}
		]}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "ak_test")
	if got := resolveRedfoxWideAccount(context.Background(), "惊蛰研究所"); !got.Empty() {
		t.Fatalf("重名应放弃：%+v", got)
	}
}

func TestQueryRedfoxWorkListDoesNotReadBodyOnSuccessPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case redfoxPremiumSearchArticlePath, redfoxWideSearchArticlePath, redfoxPremiumWorkListPath:
			_, _ = io.WriteString(w, `{"code":2000,"msg":"成功","data":{"hasMore":false,"list":[]}}`)
		case redfoxWideSearchUserPath:
			_, _ = io.WriteString(w, `{"code":2000,"data":{"list":[]}}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REDFOX_API_BASE_URL", srv.URL)
	t.Setenv("REDFOX_API_KEY", "ak_test")
	if got := defaultLookupRedfoxOriginalArticle(context.Background(), "零食大王卖不动了", "天下网商"); got != "" {
		t.Fatalf("空列表应 miss：%q", got)
	}
}
