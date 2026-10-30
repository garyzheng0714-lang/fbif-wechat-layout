package main

// FoodTalks 网站文章导入（foodtalks_news_import.go）。样稿在 testdata/foodtalks-mp/：
// FoodTalks 网站上已发布的 3 篇转载（63296 懂经、63278 零售圈、63234 职业零售网），
// 同一篇在「FoodTalks食品资讯」公众号上编辑手排的样子是规范对照（2026-10-26 用户给的 9 篇）。
// <编号>.blocks.json 是导入后交给前端的正文块，前端排版测试（tests/foodtalks-mp.test.mjs）读它；
// 块一变就失败，看过确认后 GOLDEN_UPDATE=1 重写。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var foodTalksMpSampleIDs = []string{"63296", "63278", "63234"}

func loadFoodTalksMpSample(t *testing.T, id string) foodTalksNewsDetail {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "foodtalks-mp", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var news foodTalksNewsDetail
	if err := json.Unmarshal(raw, &news); err != nil {
		t.Fatal(err)
	}
	return news
}

func stubFoodTalksNews(t *testing.T, fn func(context.Context, int64) (foodTalksNewsDetail, error)) {
	t.Helper()
	prev := fetchFoodTalksNews
	fetchFoodTalksNews = fn
	t.Cleanup(func() { fetchFoodTalksNews = prev })
}

func TestFoodTalksNewsIDFromURLOnlyCurrentFront(t *testing.T) {
	t.Setenv(siteFrontURLEnv, "https://www.foodtalks.cn")
	cases := map[string]int64{
		"https://www.foodtalks.cn/news/63296":         63296,
		"https://foodtalks.cn/news/63296/":            63296,
		"http://www.foodtalks.cn/news/63296?from=mp":  63296,
		"https://m.foodtalks.cn/news/63296":           63296,
		"https://test.foodtalks.cn/news/63296":        0, // 测试站 id 与正式站对不上
		"https://www.foodtalks.cn/news/tag/1234":      0,
		"https://www.foodtalks.cn/company/abc":        0,
		"https://mp.weixin.qq.com/s/-JpQgfPwmtx1XQK1": 0,
	}
	for raw, want := range cases {
		got, ok := foodTalksNewsIDFromURL(raw)
		if (want > 0) != ok || got != want {
			t.Errorf("%s → %d,%v want %d", raw, got, ok, want)
		}
	}
}

func TestFetchArticleReadsFoodTalksNewsThroughAdminAPI(t *testing.T) {
	t.Setenv(siteFrontURLEnv, "https://www.foodtalks.cn")
	news := loadFoodTalksMpSample(t, "63296")
	var asked int64
	stubFoodTalksNews(t, func(_ context.Context, id int64) (foodTalksNewsDetail, error) {
		asked = id
		return news, nil
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fetch-article", strings.NewReader(`{"url":"https://www.foodtalks.cn/news/63296","account_id":"foodtalks-mp"}`))
	handleFetchArticleWithStore(nil)(rec, req)
	if rec.Code != 200 || asked != 63296 {
		t.Fatalf("status=%d asked=%d body=%.200s", rec.Code, asked, rec.Body.String())
	}
	var got articleBlocksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != news.Title || got.SourceName != "懂经" || got.Byline != "叶宇" || len(got.Blocks) < 20 {
		t.Fatalf("title=%q source=%q byline=%q blocks=%d", got.Title, got.SourceName, got.Byline, len(got.Blocks))
	}
	if got.Provenance == nil || *got.Provenance != (articleProvenance{Original: false, Source: "懂经"}) {
		t.Fatalf("和发布流程同一个原创/转载判定：%+v", got.Provenance)
	}
}

func TestFetchArticleFoodTalksNewsFailureIsLoud(t *testing.T) {
	t.Setenv(siteFrontURLEnv, "https://www.foodtalks.cn")
	stubFoodTalksNews(t, func(context.Context, int64) (foodTalksNewsDetail, error) {
		return foodTalksNewsDetail{}, errors.New("FoodTalks 接口不可达")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fetch-article", strings.NewReader(`{"url":"https://www.foodtalks.cn/news/63296"}`))
	handleFetchArticleWithStore(nil)(rec, req)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "FoodTalks 文章读取失败") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFoodTalksMpSampleBlocksGolden(t *testing.T) {
	update := os.Getenv("GOLDEN_UPDATE") != ""
	for _, id := range foodTalksMpSampleIDs {
		news := loadFoodTalksMpSample(t, id)
		_, blocks, _ := extractArticleBlocks(news.Content)
		got, err := json.MarshalIndent(articleBlocksResponse{
			Title: news.Title, Blocks: blocks, Source: "https://www.foodtalks.cn/news/" + id,
			SourceName: news.SourceName, Byline: news.Author,
		}, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("testdata", "foodtalks-mp", id+".blocks.json")
		if update {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("没有 %s：GOLDEN_UPDATE=1 go test -run TestFoodTalksMpSampleBlocksGolden 生成后逐块看过再提交", path)
		}
		if string(got) != string(want) {
			t.Errorf("%s 的正文块变了：看过确认后 GOLDEN_UPDATE=1 重写", id)
		}
	}
}

// 排版接口交 FoodTalks 文章链接，和网页导入同一条路：管理接口取正文，不去抓前端渲染的网页。
func TestLayoutFetcherReadsFoodTalksNewsThroughAdminAPI(t *testing.T) {
	t.Setenv(siteFrontURLEnv, "https://www.foodtalks.cn")
	news := loadFoodTalksMpSample(t, "63278")
	stubFoodTalksNews(t, func(_ context.Context, id int64) (foodTalksNewsDetail, error) {
		if id != 63278 {
			t.Fatalf("asked %d", id)
		}
		return news, nil
	})
	got, err := articleLayoutFetcher{}.FetchArticle(context.Background(), "https://www.foodtalks.cn/news/63278", articleBlockOptions{})
	if err != nil || got.Title != strings.TrimSpace(news.Title) || got.SourceName != "零售圈" || len(got.Blocks) == 0 {
		t.Fatalf("err=%v title=%q source=%q blocks=%d", err, got.Title, got.SourceName, len(got.Blocks))
	}
}

func TestLayoutFoodTalksNewsFailureIsLoud(t *testing.T) {
	t.Setenv(siteFrontURLEnv, "https://www.foodtalks.cn")
	env := newLayoutTestEnv(t)
	env.fetcher.err = errors.New("foodtalks 401")
	env.fetcher.readerTitle, env.fetcher.readerText = "FoodTalks", "# 导航\n\n页脚"
	body := map[string]any{"account": "foodtalks-mp", "url": "https://www.foodtalks.cn/news/63234"}
	expectLayoutError(t, env.postJSON(env.keys.active, body), http.StatusBadGateway, layoutErrFetchFailed)
	if env.fetcher.readerCalls != 0 || env.node.count() != 0 {
		t.Fatal("FoodTalks 文章读不到不退到阅读模式，也不排")
	}
}
