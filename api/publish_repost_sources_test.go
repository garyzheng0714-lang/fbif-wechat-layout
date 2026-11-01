package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRepostSourceStoreCountsDistinctArticles(t *testing.T) {
	store := newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	first, added, err := store.record("惊蛰研究所", "https://mp.weixin.qq.com/s?__biz=aaa&mid=1&idx=1", "一", 1, now)
	if err != nil || !added || len(first.Articles) != 1 {
		t.Fatalf("first add: added=%v n=%d err=%v", added, len(first.Articles), err)
	}
	again, added, err := store.record("惊蛰研究所", "https://mp.weixin.qq.com/s?__biz=aaa&mid=1&idx=1", "一", 2, now)
	if err != nil || added || len(again.Articles) != 1 {
		t.Fatalf("same url should not recount: added=%v n=%d err=%v", added, len(again.Articles), err)
	}
	sameJob, added, err := store.record("惊蛰研究所", "https://mp.weixin.qq.com/s?__biz=aaa&mid=2&idx=1", "二", 1, now)
	if err != nil || added || len(sameJob.Articles) != 1 {
		t.Fatalf("same job should not recount: added=%v n=%d err=%v", added, len(sameJob.Articles), err)
	}
	second, added, err := store.record("惊蛰研究所", "https://mp.weixin.qq.com/s?__biz=aaa&mid=2&idx=1", "二", 2, now)
	if err != nil || !added || len(second.Articles) != 2 {
		t.Fatalf("second add: added=%v n=%d err=%v", added, len(second.Articles), err)
	}
	if second.readyToSubscribe() {
		t.Fatal("未满 3 次不得订阅")
	}
}

func TestRepostSourceReadyAfterThreeArticles(t *testing.T) {
	store := newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	now := time.Now()
	var src publishRepostSource
	for i := 1; i <= repostSubscribeThreshold; i++ {
		var err error
		src, _, err = store.record("惊蛰研究所",
			"https://mp.weixin.qq.com/s?__biz=aaa&mid="+itoaRepost(i)+"&idx=1",
			"标题", int64(i), now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !src.readyToSubscribe() {
		t.Fatalf("满 3 次应可订阅：n=%d", len(src.Articles))
	}
}

func TestRecordPublishedRepostSubscribesAfterThree(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("SITE_OWN_ACCOUNTS", "FBIF食品饮料创新")
	env.pipeline.repostSources = newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	var subscribed []string
	env.pipeline.subscribeFeedWechatArticle = func(_ context.Context, sourceName, articleURL string) error {
		subscribed = append(subscribed, sourceName+"|"+articleURL)
		return nil
	}
	for i := 1; i < repostSubscribeThreshold; i++ {
		env.pipeline.recordPublishedRepost(context.Background(), publishJob{
			ID:                int64(i),
			Title:             "标题",
			SourceName:        "FBIF食品饮料创新",
			BlocksJSON:        `[{"k":"txt","text":"来源：惊蛰研究所"}]`,
			OriginalSourceURL: "https://mp.weixin.qq.com/s?__biz=aaa&mid=" + itoaRepost(i) + "&idx=1",
		})
		if len(subscribed) != 0 {
			t.Fatalf("第 %d 次不应订阅", i)
		}
	}
	env.pipeline.recordPublishedRepost(context.Background(), publishJob{
		ID:                int64(repostSubscribeThreshold),
		Title:             "标题",
		SourceName:        "FBIF食品饮料创新",
		BlocksJSON:        `[{"k":"txt","text":"来源：惊蛰研究所"}]`,
		OriginalSourceURL: "https://mp.weixin.qq.com/s?__biz=aaa&mid=" + itoaRepost(repostSubscribeThreshold) + "&idx=1",
	})
	if len(subscribed) != 1 || !strings.Contains(subscribed[0], "惊蛰研究所") {
		t.Fatalf("满 3 次应订阅一次：%v", subscribed)
	}
	src, ok, err := env.pipeline.repostSources.byKey("惊蛰研究所")
	if err != nil || !ok || src.SubscribeState != repostSubscribeSuccess {
		t.Fatalf("订阅状态应成功：ok=%v state=%q err=%v", ok, src.SubscribeState, err)
	}
}

// TestRecoverRepostSubscribeCatchesUpSourcesAlreadyOverThreshold 阈值从 5 降到 3（2026-11-01）、
// 满 3 次要开快速订阅（2026-11-02）时，已转满的号没有新转载就不会触发；启动恢复要把已满、还没订上的
// （含停在 subscribing 的）和订上了但快速订阅没开成的都补交，订阅与快速订阅都成了的、没满的不碰。
func TestRecoverRepostSubscribeCatchesUpSourcesAlreadyOverThreshold(t *testing.T) {
	env := newPublishTestEnv(t)
	store := newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	env.pipeline.repostSources = store
	now := time.Now()
	add := func(name string, n int) {
		for i := 1; i <= n; i++ {
			if _, _, err := store.record(name, "https://mp.weixin.qq.com/s?__biz="+name+"&mid="+itoaRepost(i)+"&idx=1", "标题", 0, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("投资界", 3)
	add("全球烘焙指南", 4)
	add("盐财经", 2)
	add("咖门", 5)
	add("龙商网超市周刊", 3)
	add("联商网", 7)
	_ = store.markSubscribe("咖门", repostSubscribeSuccess, repostSubscribeSuccess, "", "", now)
	_ = store.markSubscribe("联商网", repostSubscribeSuccess, "", "", "", now)
	_ = store.markSubscribe("龙商网超市周刊", repostSubscribeSubscribing, "", "", "", now)
	var subscribed []string
	env.pipeline.subscribeFeedWechatArticle = func(_ context.Context, sourceName, _ string) error {
		subscribed = append(subscribed, sourceName)
		return nil
	}
	env.pipeline.recoverRepostSourceSubscribe()
	if strings.Join(subscribed, ",") != "投资界,全球烘焙指南,龙商网超市周刊,联商网" {
		t.Fatalf("应补订已满 3 篇且没订上、或快速订阅没开的号：%v", subscribed)
	}
	for _, name := range []string{"投资界", "全球烘焙指南", "龙商网超市周刊", "咖门", "联商网"} {
		src, _, _ := store.byKey(name)
		if src.SubscribeState != repostSubscribeSuccess || src.FastState != repostSubscribeSuccess {
			t.Fatalf("%s 应订上并开快速订阅：state=%q fast=%q", name, src.SubscribeState, src.FastState)
		}
	}
	if src, _, _ := store.byKey("盐财经"); src.SubscribeState != repostSubscribeIdle {
		t.Fatalf("没满 3 篇不应订阅：state=%q", src.SubscribeState)
	}
}

// TestRepostSubscribeSendsHTTPSToFeed 阅读原文记下的是 http:// 链接，Feed 只收 https://mp.weixin.qq.com，
// 2026-10-19 起联商网、咖门、中国企业家杂志每次都 400「source_name and wechat article url are required」。
func TestRepostSubscribeSendsHTTPSToFeed(t *testing.T) {
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", "sync-secret")
	var got map[string]string
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != feedOriginalArticleSubscribePath || r.Header.Get("X-Publish-Sync-Token") != "sync-secret" {
			t.Fatalf("path=%s token=%q", r.URL.Path, r.Header.Get("X-Publish-Sync-Token"))
		}
		got = nil
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		if raw["fast"] != true {
			t.Fatalf("订阅要带 fast=true 请 Feed 开快速订阅：%v", raw)
		}
		got = map[string]string{}
		for k, v := range raw {
			if str, ok := v.(string); ok {
				got[k] = str
			}
		}
		w.Header().Set("Content-Type", "application/json")
		// 照 Feed 的校验：要 source_name，链接必须 https 且是 mp.weixin.qq.com。
		u, err := url.Parse(got["url"])
		if err != nil || u.Scheme != "https" || u.Hostname() != "mp.weixin.qq.com" || strings.TrimSpace(got["source_name"]) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"source_name and wechat article url are required"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"subscribed","source_name":"联商网","fast":true}`))
	}))
	t.Cleanup(feed.Close)
	t.Setenv("FOODTALKS_FEED_BASE_URL", feed.URL)

	env := newPublishTestEnv(t)
	t.Setenv("SITE_OWN_ACCOUNTS", "FBIF食品饮料创新")
	env.pipeline.repostSources = newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	env.pipeline.subscribeFeedWechatArticle = defaultSubscribeFeedWechatArticle
	for i := 1; i <= repostSubscribeThreshold; i++ {
		env.pipeline.recordPublishedRepost(context.Background(), publishJob{
			ID:                int64(i),
			Title:             "标题",
			SourceName:        "FBIF食品饮料创新",
			BlocksJSON:        `[{"k":"txt","text":"来源：联商网"}]`,
			OriginalSourceURL: "http://mp.weixin.qq.com/s?__biz=MjM5ODM1MzM3NA==&mid=" + itoaRepost(i) + "&idx=1&sn=sn" + itoaRepost(i) + "&chksm=y#rd",
		})
	}
	n := itoaRepost(repostSubscribeThreshold)
	want := "https://mp.weixin.qq.com/s?__biz=MjM5ODM1MzM3NA==&mid=" + n + "&idx=1&sn=sn" + n + "&chksm=y"
	if got["source_name"] != "联商网" || got["url"] != want {
		t.Fatalf("发给 Feed 的应是 https 链接：%v", got)
	}
	src, _, _ := env.pipeline.repostSources.byKey("联商网")
	if src.SubscribeState != repostSubscribeSuccess || src.LastSubscribeURL != want {
		t.Fatalf("订阅应成功并记下 https 链接：state=%q url=%q err=%q", src.SubscribeState, src.LastSubscribeURL, src.SubscribeError)
	}
}

// TestRepostSubscribeFastPendingKeepsSubscribedAndRetries Feed 订上了但快速订阅没开成（积分不够等）：
// 订阅记成功、快速订阅记失败，下回再交；早先已订上的号补开失败也不把订阅改成失败。
func TestRepostSubscribeFastPendingKeepsSubscribedAndRetries(t *testing.T) {
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", "sync-secret")
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"subscribed","source_name":"投资界","fast":false,"fast_error":"供应商没有开通极速订阅：积分不足"}`))
	}))
	t.Cleanup(feed.Close)
	t.Setenv("FOODTALKS_FEED_BASE_URL", feed.URL)
	err := defaultSubscribeFeedWechatArticle(context.Background(), "投资界", "https://mp.weixin.qq.com/s?__biz=a&mid=1&idx=1")
	if !errors.Is(err, errFeedFastPending) || !strings.Contains(err.Error(), "积分不足") {
		t.Fatalf("200 但没开成快速订阅应认作 fast pending：%v", err)
	}

	env := newPublishTestEnv(t)
	env.pipeline.repostSources = newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	for i := 1; i <= repostSubscribeThreshold; i++ {
		if _, _, err := env.pipeline.repostSources.record("投资界", "https://mp.weixin.qq.com/s?__biz=a&mid="+itoaRepost(i)+"&idx=1", "标题", int64(i), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	reply := err
	env.pipeline.subscribeFeedWechatArticle = func(context.Context, string, string) error { return reply }
	env.pipeline.completeRepostSourceSubscribe("投资界")
	src, _, _ := env.pipeline.repostSources.byKey("投资界")
	if src.SubscribeState != repostSubscribeSuccess || src.FastState != repostSubscribeFailed || !src.readyToSubscribe() {
		t.Fatalf("应记订阅成功、快速订阅失败并下回再交：state=%q fast=%q", src.SubscribeState, src.FastState)
	}
	reply = errors.New("订阅接口不可达")
	env.pipeline.completeRepostSourceSubscribe("投资界")
	if src, _, _ = env.pipeline.repostSources.byKey("投资界"); src.SubscribeState != repostSubscribeSuccess {
		t.Fatalf("早先已订上，补开失败不应把订阅改成失败：state=%q", src.SubscribeState)
	}
	reply = nil
	env.pipeline.completeRepostSourceSubscribe("投资界")
	if src, _, _ = env.pipeline.repostSources.byKey("投资界"); src.FastState != repostSubscribeSuccess || src.readyToSubscribe() {
		t.Fatalf("开成后不再交：fast=%q", src.FastState)
	}
}

// TestRepostSubscribeAcceptedIsNotFailure Feed 回 202（今天看啥还没确认）不算失败，下回照样再提交，回 200 才算订上。
func TestRepostSubscribeAcceptedIsNotFailure(t *testing.T) {
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", "sync-secret")
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted","message":"订阅已提交，等待今天看啥确认"}`))
	}))
	t.Cleanup(feed.Close)
	t.Setenv("FOODTALKS_FEED_BASE_URL", feed.URL)
	err := defaultSubscribeFeedWechatArticle(context.Background(), "咖门", "https://mp.weixin.qq.com/s?__biz=a&mid=1&idx=1")
	if !errors.Is(err, errFeedSubscribeAccepted) {
		t.Fatalf("202 应认作已受理：%v", err)
	}

	env := newPublishTestEnv(t)
	env.pipeline.repostSources = newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	for i := 1; i <= repostSubscribeThreshold; i++ {
		if _, _, err := env.pipeline.repostSources.record("咖门", "https://mp.weixin.qq.com/s?__biz=a&mid="+itoaRepost(i)+"&idx=1", "标题", int64(i), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	reply := err
	env.pipeline.subscribeFeedWechatArticle = func(context.Context, string, string) error { return reply }
	env.pipeline.completeRepostSourceSubscribe("咖门")
	src, _, _ := env.pipeline.repostSources.byKey("咖门")
	if src.SubscribeState != repostSubscribeAccepted || !src.readyToSubscribe() {
		t.Fatalf("已受理应记 accepted 且下回再提交：state=%q ready=%v", src.SubscribeState, src.readyToSubscribe())
	}
	reply = nil
	env.pipeline.completeRepostSourceSubscribe("咖门")
	src, _, _ = env.pipeline.repostSources.byKey("咖门")
	if src.SubscribeState != repostSubscribeSuccess || src.readyToSubscribe() {
		t.Fatalf("确认后应记成功、不再提交：state=%q", src.SubscribeState)
	}
}

// TestResolveRepostOriginalSearchesWechatEvenWhenFeedHasSource Feed 只按标题对，订了这个号也对不上
// FBIF 改过标题的转载（2026-10-14 #255 财经杂志、#274 窄播）：Feed 没命中就照样搜一搜，并把正文带过去核验。
func TestResolveRepostOriginalSearchesWechatEvenWhenFeedHasSource(t *testing.T) {
	lookupFeedOriginalArticleDetail = func(context.Context, string, string) feedOriginalArticleHit {
		return feedOriginalArticleHit{}
	}
	var gotBody []string
	lookupWechatOriginalArticle = func(_ context.Context, _, _ string, body []string) repostOriginalArticle {
		gotBody = body
		return repostOriginalArticle{URL: "https://mp.weixin.qq.com/s?__biz=MzkxMzMwNTkxOA==&mid=1&idx=1&sn=a", Title: "社区烘焙第一品牌，是怎么跑出来的｜对话"}
	}
	searchOriginalArticleURLs = func(context.Context, string, string) []string {
		t.Fatal("搜一搜已命中时不应再去网页检索")
		return nil
	}
	t.Cleanup(func() {
		lookupFeedOriginalArticleDetail = defaultLookupFeedOriginalArticleDetail
		lookupWechatOriginalArticle = defaultLookupWechatOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
	})
	got := resolveRepostOriginal(context.Background(), "3年开出300家门店，社区烘焙第一品牌是怎么跑出来的", "窄播", "", []string{"正文第一段"})
	if got.Title != "社区烘焙第一品牌，是怎么跑出来的｜对话" || len(gotBody) != 1 {
		t.Fatalf("Feed 没命中应搜一搜并带回原文标题：%+v body=%v", got, gotBody)
	}
}

func itoaRepost(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
