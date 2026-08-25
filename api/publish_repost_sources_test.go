package main

import (
	"context"
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
		t.Fatal("未满 5 次不得订阅")
	}
}

func TestRepostSourceReadyAfterFiveArticles(t *testing.T) {
	store := newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	now := time.Now()
	var src publishRepostSource
	for i := 1; i <= 5; i++ {
		var err error
		src, _, err = store.record("惊蛰研究所",
			"https://mp.weixin.qq.com/s?__biz=aaa&mid="+itoaRepost(i)+"&idx=1",
			"标题", int64(i), now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !src.readyToSubscribe() {
		t.Fatalf("满 5 次应可订阅：n=%d", len(src.Articles))
	}
}

func TestRecordPublishedRepostSubscribesAfterFive(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("SITE_OWN_ACCOUNTS", "FBIF食品饮料创新")
	env.pipeline.repostSources = newPublishRepostSourceStore(filepath.Join(t.TempDir(), "repost.json"))
	var subscribed []string
	env.pipeline.subscribeFeedWechatArticle = func(_ context.Context, sourceName, articleURL string) error {
		subscribed = append(subscribed, sourceName+"|"+articleURL)
		return nil
	}
	for i := 1; i <= 4; i++ {
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
		ID:                5,
		Title:             "标题",
		SourceName:        "FBIF食品饮料创新",
		BlocksJSON:        `[{"k":"txt","text":"来源：惊蛰研究所"}]`,
		OriginalSourceURL: "https://mp.weixin.qq.com/s?__biz=aaa&mid=5&idx=1",
	})
	if len(subscribed) != 1 || !strings.Contains(subscribed[0], "惊蛰研究所") {
		t.Fatalf("满 5 次应订阅一次：%v", subscribed)
	}
	src, ok, err := env.pipeline.repostSources.byKey("惊蛰研究所")
	if err != nil || !ok || src.SubscribeState != repostSubscribeSuccess {
		t.Fatalf("订阅状态应成功：ok=%v state=%q err=%v", ok, src.SubscribeState, err)
	}
}

func TestResolveRepostOriginalURLSkipsWechatLookupWhenFeedHasSource(t *testing.T) {
	lookupFeedOriginalArticleDetail = func(context.Context, string, string) feedOriginalArticleHit {
		return feedOriginalArticleHit{KnownSource: true}
	}
	lookupWechatOriginalArticle = func(context.Context, string, string) string {
		t.Fatal("订阅源已有这个公众号时不应再走付费接口")
		return ""
	}
	searchOriginalArticleURLs = func(context.Context, string, string) []string { return nil }
	t.Cleanup(func() {
		lookupFeedOriginalArticleDetail = defaultLookupFeedOriginalArticleDetail
		lookupWechatOriginalArticle = defaultLookupWechatOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
	})
	if got := resolveRepostOriginalURL(context.Background(), "餐饮零食化，火了猪油渣", "惊蛰研究所", ""); got != "" {
		t.Fatalf("订阅源没有这篇、也没网页候选时应空着：%q", got)
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
