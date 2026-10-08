package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const moteSeriesTazoWechat = "https://mp.weixin.qq.com/s/tazoGold"
const moteSeriesItoenWechat = "https://mp.weixin.qq.com/s/itoenGold"

func moteSeriesLinkHTML(href, text string) string {
	return `<p style="text-align: justify;">这是我茶叶系列的31篇，前面写过<a href="` + href + `" target="_blank" rel="noopener">` + text + `</a>。</p>`
}

func TestRewriteMoteSeriesLinksFromPublishedJobStore(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	tazoKey, err := normalizeSiteSourceKey(moteSeriesTazoWechat)
	if err != nil {
		t.Fatal(err)
	}
	sibling, _, err := store.create(tazoKey, moteSeriesTazoWechat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(sibling.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.FoodTalksNewsID = 62236
		j.Title = "茶叶巫师创建的品牌Tazo"
		j.SourceAliases = []string{tazoKey}
	}); err != nil {
		t.Fatal(err)
	}

	p := &publishPipeline{store: store, now: time.Now}
	job := publishJob{
		Author:      "Mote莫特",
		SourceKey:   "site:https://mp.weixin.qq.com/s/currentTeaSnack",
		SourceURL:   "https://mp.weixin.qq.com/s/currentTeaSnack",
		ContentHTML: moteSeriesLinkHTML(moteSeriesTazoWechat, "Tazo"),
	}
	got := p.rewriteMoteSeriesLinks(context.Background(), job, job.ContentHTML)
	want := `<a href="https://www.foodtalks.cn/news/62236" target="_blank" rel="noopener">Tazo</a>`
	if !strings.Contains(got, want) {
		t.Fatalf("任务库已发布稿应改成站内链（金标准 62285 形态），实际：%s", got)
	}
	if strings.Contains(got, "mp.weixin.qq.com/s/tazoGold") {
		t.Fatalf("原公众号链应被替换：%s", got)
	}
}

func TestRewriteMoteSeriesLinksMatchesShortHrefViaAlias(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	shortURL := "https://mp.weixin.qq.com/s/AbCdEf123"
	longURL := "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2247483700&idx=1&sn=deadbeef"
	shortKey, err := normalizeSiteSourceKey(shortURL)
	if err != nil {
		t.Fatal(err)
	}
	longKey, err := normalizeSiteSourceKey(longURL)
	if err != nil {
		t.Fatal(err)
	}
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	sibling, _, err := store.create(longKey, longURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(sibling.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.FoodTalksNewsID = 62001
		j.SourceAliases = []string{shortKey}
	}); err != nil {
		t.Fatal(err)
	}
	p := &publishPipeline{store: store, now: time.Now}
	job := publishJob{Author: "Mote 莫特", ContentHTML: moteSeriesLinkHTML(shortURL, "Lupicia（绿碧茶园）")}
	got := p.rewriteMoteSeriesLinks(context.Background(), job, job.ContentHTML)
	if !strings.Contains(got, `href="https://www.foodtalks.cn/news/62001"`) || !strings.Contains(got, "Lupicia（绿碧茶园）") {
		t.Fatalf("短链应对上任务库 alias：%s", got)
	}
}

func TestRewriteMoteSeriesLinksByWechatTitle(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	stub := &moteSeriesSearchStub{items: []foodTalksNewsListItem{
		{ID: 61880, Title: "伊藤园把茶卖成了国民饮料", PublishStatus: foodTalksPublishStatusPublished},
		{ID: 1, Title: "伊藤园把茶卖成了国民饮料（草稿）", PublishStatus: foodTalksPublishStatusDraft},
	}}
	var fetched []string
	p := &publishPipeline{
		now:       time.Now,
		foodTalks: func() (foodTalksAPI, error) { return stub, nil },
		fetchWechatTitle: func(_ context.Context, articleURL string) (wechatArticleRef, error) {
			fetched = append(fetched, articleURL)
			if articleURL == moteSeriesItoenWechat {
				return wechatArticleRef{Title: "伊藤园把茶卖成了国民饮料"}, nil
			}
			return wechatArticleRef{}, errors.New("未知链接")
		},
	}
	html := moteSeriesLinkHTML(moteSeriesItoenWechat, "伊藤园") + moteSeriesLinkHTML(moteSeriesItoenWechat, "伊藤园")
	job := publishJob{Author: "Mote莫特", ContentHTML: html}
	got := p.rewriteMoteSeriesLinks(context.Background(), job, html)
	if strings.Count(got, `href="https://www.foodtalks.cn/news/61880"`) != 2 {
		t.Fatalf("同链两处都应改写：%s", got)
	}
	if len(fetched) != 1 {
		t.Fatalf("同一公众号链只抓一次标题，实际 %d 次：%v", len(fetched), fetched)
	}
	if stub.searchN != 1 {
		t.Fatalf("标题检索应一次，实际 %d", stub.searchN)
	}
}

func TestRewriteMoteSeriesLinksKeepsOriginalOnMissOrError(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	html := moteSeriesLinkHTML("https://mp.weixin.qq.com/s/notOnSite", "原田制茶")
	job := publishJob{Author: "Mote莫特", ContentHTML: html}

	p := &publishPipeline{
		now:       time.Now,
		foodTalks: func() (foodTalksAPI, error) { return &moteSeriesSearchStub{}, nil },
		fetchWechatTitle: func(context.Context, string) (wechatArticleRef, error) {
			return wechatArticleRef{Title: "一篇网站上没有的茶文"}, nil
		},
	}
	if got := p.rewriteMoteSeriesLinks(context.Background(), job, html); got != html {
		t.Fatalf("站内无稿应保留原链：%s", got)
	}

	p.fetchWechatTitle = func(context.Context, string) (wechatArticleRef, error) {
		return wechatArticleRef{}, errors.New("微信限流")
	}
	if got := p.rewriteMoteSeriesLinks(context.Background(), job, html); got != html {
		t.Fatalf("抓标题失败应保留原链：%s", got)
	}

	p.fetchWechatTitle = func(context.Context, string) (wechatArticleRef, error) {
		return wechatArticleRef{Title: "限流后的标题"}, nil
	}
	p.foodTalks = func() (foodTalksAPI, error) { return nil, errFoodTalksNotConfigured }
	if got := p.rewriteMoteSeriesLinks(context.Background(), job, html); got != html {
		t.Fatalf("CMS 不可用应保留原链：%s", got)
	}
}

func TestRewriteMoteSeriesLinksSkipsNonMoteAndSelfAndUnpublished(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	draftKey, _ := normalizeSiteSourceKey("https://mp.weixin.qq.com/s/draftOnly")
	draft, _, err := store.create(draftKey, "https://mp.weixin.qq.com/s/draftOnly")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(draft.ID, func(j *publishJob) {
		j.Stage = publishStageAwaitingReview
		j.FoodTalksNewsID = 70001
	}); err != nil {
		t.Fatal(err)
	}

	html := moteSeriesLinkHTML("https://mp.weixin.qq.com/s/draftOnly", "还没上线的系列") +
		moteSeriesLinkHTML("https://mp.weixin.qq.com/s/currentTeaSnack", "本文") +
		`<p>站内已改过的<a href="https://www.foodtalks.cn/news/62236" target="_blank" rel="noopener">Tazo</a></p>`
	p := &publishPipeline{store: store, now: time.Now}

	nonMote := publishJob{Author: "张三", ContentHTML: html}
	if got := p.rewriteMoteSeriesLinks(context.Background(), nonMote, html); got != html {
		t.Fatalf("非 Mote 不得改链：%s", got)
	}

	mote := publishJob{
		Author:      "Mote莫特",
		SourceKey:   "site:https://mp.weixin.qq.com/s/currentTeaSnack",
		SourceURL:   "https://mp.weixin.qq.com/s/currentTeaSnack",
		ContentHTML: html,
	}
	got := p.rewriteMoteSeriesLinks(context.Background(), mote, html)
	if !strings.Contains(got, `href="https://mp.weixin.qq.com/s/draftOnly"`) {
		t.Fatalf("未发布任务不得当站内目标：%s", got)
	}
	if !strings.Contains(got, `href="https://mp.weixin.qq.com/s/currentTeaSnack"`) {
		t.Fatalf("本稿自链应保留公众号地址（提示声明）：%s", got)
	}
	if !strings.Contains(got, `href="https://www.foodtalks.cn/news/62236"`) {
		t.Fatalf("已是站内链应原样保留：%s", got)
	}
}

func TestRewriteMoteSeriesLinksAmbiguousTitleKeepsOriginal(t *testing.T) {
	html := moteSeriesLinkHTML("https://mp.weixin.qq.com/s/dupTitle", "日东红茶")
	p := &publishPipeline{
		now: time.Now,
		foodTalks: func() (foodTalksAPI, error) {
			return &moteSeriesSearchStub{items: []foodTalksNewsListItem{
				{ID: 11, Title: "同题", PublishStatus: foodTalksPublishStatusPublished},
				{ID: 12, Title: "同题", PublishStatus: foodTalksPublishStatusPublished},
			}}, nil
		},
		fetchWechatTitle: func(context.Context, string) (wechatArticleRef, error) { return wechatArticleRef{Title: "同题"}, nil },
	}
	job := publishJob{Author: "Mote莫特", ContentHTML: html}
	if got := p.rewriteMoteSeriesLinks(context.Background(), job, html); got != html {
		t.Fatalf("标题命中多篇已发布稿应放弃：%s", got)
	}
}

func TestRewriteMoteSeriesLinksUnescapesLongWechatHref(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	raw := "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2247483700&idx=1&sn=abc"
	escaped := strings.ReplaceAll(raw, "&", "&amp;")
	htmlBody := moteSeriesLinkHTML(escaped, "绿碧茶园")
	key, err := normalizeSiteSourceKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	sibling, _, err := store.create(key, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(sibling.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.FoodTalksNewsID = 61500
	}); err != nil {
		t.Fatal(err)
	}
	p := &publishPipeline{store: store, now: time.Now}
	got := p.rewriteMoteSeriesLinks(context.Background(), publishJob{Author: "Mote莫特", ContentHTML: htmlBody}, htmlBody)
	if !strings.Contains(got, `href="https://www.foodtalks.cn/news/61500"`) {
		t.Fatalf("&amp; 长链应能对上任务库：%s", got)
	}
}

func TestRewriteMoteSeriesLinksIgnoresDataHref(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	wechat := "https://mp.weixin.qq.com/s/tazoGold"
	key, err := normalizeSiteSourceKey(wechat)
	if err != nil {
		t.Fatal(err)
	}
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	sibling, _, err := store.create(key, wechat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(sibling.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.FoodTalksNewsID = 62236
	}); err != nil {
		t.Fatal(err)
	}
	html := `<p><a data-href="` + wechat + `" href="https://example.com/keep">文字</a></p>`
	p := &publishPipeline{store: store, now: time.Now}
	got := p.rewriteMoteSeriesLinks(context.Background(), publishJob{Author: "Mote莫特", ContentHTML: html}, html)
	if !strings.Contains(got, `data-href="`+wechat+`"`) {
		t.Fatalf("data-href 不得被改：%s", got)
	}
	if !strings.Contains(got, `href="https://example.com/keep"`) {
		t.Fatalf("非公众号 href 应保留：%s", got)
	}
}

func TestRewriteMoteSeriesLinksGold62285TazoParagraph(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	wechat := "https://mp.weixin.qq.com/s/tazoFromWechat"
	gold := `<p style="text-align: justify;">我上一篇写茶叶巫师创建的品牌<a href="https://www.foodtalks.cn/news/62236" target="_blank" rel="noopener">Tazo</a>，星巴克1999年花810万美元`
	input := strings.Replace(gold, "https://www.foodtalks.cn/news/62236", wechat, 1)
	key, err := normalizeSiteSourceKey(wechat)
	if err != nil {
		t.Fatal(err)
	}
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	sibling, _, err := store.create(key, wechat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(sibling.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.FoodTalksNewsID = 62236
	}); err != nil {
		t.Fatal(err)
	}
	p := &publishPipeline{store: store, now: time.Now}
	got := p.rewriteMoteSeriesLinks(context.Background(), publishJob{Author: "Mote莫特", ContentHTML: input}, input)
	if got != gold {
		t.Fatalf("应还原成 62285 金标准段落：\n得到 %s\n期望 %s", got, gold)
	}
}

func TestBuildSiteNewsRewritesMoteSeriesLinks(t *testing.T) {
	t.Setenv("FOODTALKS_CATEGORY_IDS", "")
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	tazoKey, _ := normalizeSiteSourceKey(moteSeriesTazoWechat)
	sibling, _, err := store.create(tazoKey, moteSeriesTazoWechat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(sibling.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.FoodTalksNewsID = 62236
	}); err != nil {
		t.Fatal(err)
	}
	p := &publishPipeline{store: store, now: time.Now}
	job := publishJob{
		Title:       "把茶做成零食",
		Author:      "Mote莫特",
		SourceName:  "FBIF食品饮料创新",
		ContentHTML: moteSeriesLinkHTML(moteSeriesTazoWechat, "Tazo"),
		CreatedAt:   "2026-08-19T00:00:00Z",
	}
	news, err := p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	want := `<a href="https://www.foodtalks.cn/news/62236" target="_blank" rel="noopener">Tazo</a>`
	if !strings.Contains(news.Content, want) {
		t.Fatalf("组装层应写出站内系列链：%s", news.Content)
	}
}

type moteSeriesSearchStub struct {
	newsTagStub
	items   []foodTalksNewsListItem
	searchN int
}

func (s *moteSeriesSearchStub) SearchNewsByTitle(context.Context, string) ([]foodTalksNewsListItem, error) {
	s.searchN++
	return s.items, nil
}
