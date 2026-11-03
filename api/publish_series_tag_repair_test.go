package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

type seriesRepairStub struct {
	newsTagStub
	live    foodTalksNewsDetail
	puts    []foodTalksNews
	byTitle map[string][]foodTalksNewsListItem
}

func (s *seriesRepairStub) GetNews(context.Context, int64) (foodTalksNewsDetail, error) {
	return s.live, nil
}

func (s *seriesRepairStub) UpdateNews(_ context.Context, n foodTalksNews) error {
	s.puts = append(s.puts, n)
	return nil
}

func (s *seriesRepairStub) SearchNewsByTitle(_ context.Context, title string) ([]foodTalksNewsListItem, error) {
	return s.byTitle[title], nil
}

// TestRepairNewsLinks 63149：已发布稿文末系列声明仍链公众号合集、Mote 互推链仍是公众号长链。
// 按资讯 id 直接改网站现稿（编辑手工稿也一样）：只换这两类链接，找不到站内稿的公众号链保留；
// 其他正文与字段原样写回；dry_run 不写；重复执行不再写。
func TestRepairNewsLinks(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	album := `https://mp.weixin.qq.com/mp/appmsgalbum?album_id=4500314305401487362`
	twg := `https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&amp;mid=2652931174&amp;idx=1&amp;sn=e2949c&amp;scene=21#wechat_redirect`
	lost := `https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&amp;mid=2650000001&amp;idx=1&amp;sn=zzz`
	tagLink := `<a href="https://www.foodtalks.cn/news/tag/114000" target="_blank" rel="noopener">茶叶系列</a>`
	liveContent := `<p>这是我` + tagLink + `的第38篇。</p>` +
		`<p>海外品牌可以与T9竞争的应是<a href="` + twg + `" target="_blank" rel="noopener">TWG</a>和<a href="` + lost + `">某品牌</a>。</p>` +
		`<p>这是我<a href="` + album + `" target="_blank" rel="noopener">茶叶系列</a>的第38篇。</p>`
	stub := &seriesRepairStub{
		newsTagStub: newsTagStub{byKeyword: map[string][]foodTalksTag{
			"茶叶系列": {{ID: 114000, Name: "茶叶系列", TagTypeID: 6}},
		}},
		live: foodTalksNewsDetail{ID: 63149, Title: "编辑改过的标题", Author: "Mote莫特", Content: liveContent,
			PublishStatus: foodTalksPublishStatusPublished, Language: "ZH", NewsTags: []foodTalksNewsTag{{TagID: 114000}}},
		byTitle: map[string][]foodTalksNewsListItem{
			"新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？": {{ID: 62980, Title: "新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？", PublishStatus: foodTalksPublishStatusPublished}},
		},
	}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil },
		fetchWechatTitle: func(_ context.Context, u string) (wechatArticleRef, error) {
			if strings.Contains(u, "mid=2652931174") {
				return wechatArticleRef{Title: "新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？"}, nil
			}
			return wechatArticleRef{Title: "站内没有的文章"}, nil
		}}

	res, err := p.repairNewsLinks(context.Background(), 63149, true)
	if err != nil || !res.Changed || !res.Mote || len(stub.puts) != 0 {
		t.Fatalf("dry_run 应报告需修且不写：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	res, err = p.repairNewsLinks(context.Background(), 63149, false)
	if err != nil || !res.Changed || len(stub.puts) != 1 {
		t.Fatalf("应写回一次：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	put := stub.puts[0]
	want := `<p>这是我` + tagLink + `的第38篇。</p>` +
		`<p>海外品牌可以与T9竞争的应是<a href="https://www.foodtalks.cn/news/62980" target="_blank" rel="noopener">TWG</a>和<a href="` + lost + `">某品牌</a>。</p>` +
		`<p>这是我` + tagLink + `的第38篇。</p>`
	if put.Content != want {
		t.Fatalf("只应换系列声明链和找得到的公众号链：\n got %s\nwant %s", put.Content, want)
	}
	if put.ID != 63149 || put.Title != "编辑改过的标题" || put.PublishStatus != foodTalksPublishStatusPublished || len(put.NewsTags) != 1 {
		t.Fatalf("其余字段必须按网站现稿原样写回：%+v", put)
	}

	stub.live.Content = put.Content
	res, err = p.repairNewsLinks(context.Background(), 63149, false)
	if err != nil || res.Changed || len(stub.puts) != 1 {
		t.Fatalf("已修好的稿应幂等不写：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}

	// 非 Mote 稿只补系列标签链，不碰公众号链
	stub.live.Author = "张三"
	stub.live.Content = liveContent
	stub.puts = nil
	if _, err := p.repairNewsLinks(context.Background(), 63149, false); err != nil {
		t.Fatal(err)
	}
	if len(stub.puts) != 1 || !strings.Contains(stub.puts[0].Content, twg) {
		t.Fatalf("非 Mote 稿的公众号链不得改：%+v", stub.puts)
	}
}

// TestFetchWechatTitleTikhub 直连只拿到验证页时走 TikHub 文章详情取标题；同一链接进程内只查一次。
func TestFetchWechatTitleTikhub(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if r.URL.Path != tikhubArticleDetailPath || !strings.Contains(body["url"].(string), "mid=2652931174") || r.Header.Get("Authorization") != "Bearer ak_test" {
			t.Fatalf("path=%s body=%s auth=%q", r.URL.Path, raw, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"url":"x","content":{"title":"新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？","nick_name":"FBIF食品饮料创新","create_timestamp":1779496200}}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	u := "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652931174&idx=1&sn=e2949c&scene=21#wechat_redirect"
	ref, err := fetchWechatTitleTikhub(context.Background(), u)
	if err != nil || ref.Title != "新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？" || ref.PublishedAt.Unix() != 1779496200 {
		t.Fatalf("ref=%+v err=%v", ref, err)
	}

	key, err := normalizeSiteSourceKey(u)
	if err != nil {
		t.Fatal(err)
	}
	wechatTitleCache.Store(key, wechatArticleRef{Title: "缓存里的标题"})
	t.Cleanup(func() { wechatTitleCache.Delete(key) })
	if got, err := defaultFetchWechatTitle(context.Background(), u); err != nil || got.Title != "缓存里的标题" || calls != 1 {
		t.Fatalf("命中缓存不应再请求：got=%q err=%v calls=%d", got, err, calls)
	}
}

// TestPickMoteSeriesNews 真实三种情况：唯一同题直接用（不看日期）；网站重复稿取离公众号发文最近的；
// 编辑往标题里加了品牌名（Tea Forté）且 7 天内也算；超出 7 天或不知道发文时间就保留公众号链。
func TestPickMoteSeriesNews(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04:05", s, publishSiteTimeZone)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	pub := foodTalksPublishStatusPublished
	unique := []foodTalksNewsListItem{{ID: 1419, Title: "老文章", PublishStatus: pub, PublishTime: "2021-01-01 08:00:00"}}
	if got := pickMoteSeriesNews(wechatArticleRef{Title: "老文章", PublishedAt: at("2019-06-01 08:00:00")}, unique, 0); got != 1419 {
		t.Errorf("唯一同题不看日期：got %d", got)
	}
	dups := []foodTalksNewsListItem{
		{ID: 54237, Title: "FBIF 航海书发布", PublishStatus: pub, PublishTime: "2024-10-25 00:00:00"},
		{ID: 54254, Title: "FBIF 航海书发布", PublishStatus: pub, PublishTime: "2024-11-05 17:36:39"},
		{ID: 54252, Title: "FBIF 航海书发布", PublishStatus: pub, PublishTime: "2024-11-05 17:37:28"},
		{ID: 54242, Title: "FBIF 航海书发布", PublishStatus: foodTalksPublishStatusDraft},
	}
	if got := pickMoteSeriesNews(wechatArticleRef{Title: "FBIF 航海书发布", PublishedAt: at("2024-11-05 12:00:00")}, dups, 0); got != 54254 {
		t.Errorf("重复稿取发布时间最近的一篇：got %d", got)
	}
	if got := pickMoteSeriesNews(wechatArticleRef{Title: "FBIF 航海书发布"}, dups, 0); got != 0 {
		t.Errorf("不知道发文时间时重复稿应放弃：got %d", got)
	}
	if got := pickMoteSeriesNews(wechatArticleRef{Title: "FBIF 航海书发布", PublishedAt: at("2025-03-01 12:00:00")}, dups, 0); got != 0 {
		t.Errorf("重复稿都超出 7 天应放弃：got %d", got)
	}
	edited := []foodTalksNewsListItem{{ID: 62231, Title: "把茶包做成金字塔，Tea Forté进入35个国家高端酒店，年收1540万美元", PublishStatus: pub, PublishTime: "2026-05-25 11:30:04"}}
	ref := wechatArticleRef{Title: "把茶包做成金字塔，进入35个国家高端酒店，年收1540万美元", PublishedAt: time.Unix(1779496200, 0)}
	if got := pickMoteSeriesNews(ref, edited, 0); got != 62231 {
		t.Errorf("编辑加了品牌名、7 天内应命中：got %d", got)
	}
	ref.PublishedAt = ref.PublishedAt.AddDate(0, -2, 0)
	if got := pickMoteSeriesNews(ref, edited, 0); got != 0 {
		t.Errorf("近题但相差两个月不得命中：got %d", got)
	}
	if got := pickMoteSeriesNews(wechatArticleRef{Title: "老文章", PublishedAt: at("2021-01-01 08:00:00")}, unique, 1419); got != 0 {
		t.Errorf("不得链到本稿：got %d", got)
	}
	if got := longestTitleClause("把茶包做成金字塔，进入35个国家高端酒店，年收1540万美元"); got != "进入35个国家高端酒店" {
		t.Errorf("最长分句：got %q", got)
	}
}

// TestRepairNewsLinksRepostNoticeOriginalTitle 2026-10-14：已发布转载稿的声明还是 FBIF 改过的标题、没链接。
// 找到核验过的原文后只换这一句（链到原文、用原文真标题）；编辑已改过这句、自家原创、找不到原文都不动；
// dry_run 只查不记，正式修复把原文记进任务库。
func TestRepairNewsLinksRepostNoticeOriginalTitle(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.store.createSeeded("site:retitled", "https://mp.weixin.qq.com/s/fbif-repost", func(j *publishJob) {
		j.Stage = publishStagePublished
		j.Title = "3年开出300家门店，社区烘焙第一品牌是怎么跑出来的"
		j.SourceName = "FBIF食品饮料创新"
		j.AISource = "窄播"
		j.FoodTalksNewsID = 63177
		j.BlocksJSON = `[{"k":"txt","runs":[{"type":"txt","text":"正文"}]}]`
	})
	if err != nil {
		t.Fatal(err)
	}
	const originalURL = "https://mp.weixin.qq.com/s?__biz=MzkxMzMwNTkxOA==&mid=2247504481&idx=1&sn=f328"
	lookups := 0
	lookupFeedOriginalArticleDetail = func(context.Context, string, string) feedOriginalArticleHit { return feedOriginalArticleHit{} }
	lookupWechatOriginalArticle = func(context.Context, string, string, []string) repostOriginalArticle {
		lookups++
		return repostOriginalArticle{URL: originalURL, Title: "社区烘焙第一品牌，是怎么跑出来的｜对话"}
	}
	t.Cleanup(func() {
		lookupFeedOriginalArticleDetail = defaultLookupFeedOriginalArticleDetail
		lookupWechatOriginalArticle = defaultLookupWechatOriginalArticle
	})
	other := `<p>正文里<a href="https://mp.weixin.qq.com/s/other">别的链接</a>不动。</p>`
	notice := `<p style="text-align: justify;"><em><span style="font-size: 14px; color: #7f7f7f;">*本文为窄播原创《3年开出300家门店，社区烘焙第一品牌是怎么跑出来的》，不代表FoodTalks立场，转载请联系出处。</span></em></p>`
	stub := &seriesRepairStub{live: foodTalksNewsDetail{ID: 63177, Title: job.Title, Author: "FBIF", Content: other + notice,
		PublishStatus: foodTalksPublishStatusPublished, Language: "ZH"}}
	p := env.pipeline
	p.foodTalks = func() (foodTalksAPI, error) { return stub, nil }

	res, err := p.repairNewsLinks(context.Background(), 63177, true)
	if err != nil || !res.Changed || res.RepostNoticeTitle != "社区烘焙第一品牌，是怎么跑出来的｜对话" || len(stub.puts) != 0 {
		t.Fatalf("dry_run 应报告要换的原文标题且不写：%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	if got := mustJob(t, env, job.ID); got.OriginalSourceURL != "" {
		t.Fatalf("dry_run 不得把原文记进任务库：%+v", got.OriginalSourceURL)
	}
	if _, err := p.repairNewsLinks(context.Background(), 63177, false); err != nil || len(stub.puts) != 1 {
		t.Fatalf("应写回一次：err=%v puts=%d", err, len(stub.puts))
	}
	want := other + `<p style="text-align: justify;"><em><span style="font-size: 14px; color: #7f7f7f;">*本文为窄播原创` +
		`<a href="https://mp.weixin.qq.com/s?__biz=MzkxMzMwNTkxOA==&amp;mid=2247504481&amp;idx=1&amp;sn=f328" target="_blank" rel="noopener">` +
		`《社区烘焙第一品牌，是怎么跑出来的｜对话》</a>，不代表FoodTalks立场，转载请联系出处。</span></em></p>`
	if stub.puts[0].Content != want {
		t.Fatalf("只换声明这一句：\n got %s\nwant %s", stub.puts[0].Content, want)
	}
	if got := mustJob(t, env, job.ID); got.OriginalSourceURL != originalURL || publishOriginalTitleForRender(got) == "" {
		t.Fatalf("正式修复应把原文记进任务库：%+v", got)
	}

	// 已修好的：幂等不写、不再查。
	stub.live.Content = stub.puts[0].Content
	if res, _ := p.repairNewsLinks(context.Background(), 63177, false); res.Changed || len(stub.puts) != 1 || lookups != 2 {
		t.Fatalf("已修好的稿不得再写或再查：%+v puts=%d lookups=%d", res, len(stub.puts), lookups)
	}
	// 编辑已把声明改成别的标题：不动。
	stub.live.Content = strings.Replace(notice, "《3年开出300家门店，社区烘焙第一品牌是怎么跑出来的》", "《编辑自己写的标题》", 1)
	if res, _ := p.repairNewsLinks(context.Background(), 63177, false); res.Changed || len(stub.puts) != 1 {
		t.Fatalf("编辑改过的声明不得覆盖：%+v", res)
	}
	// 判定是转载（窄播）却写成 FBIF 原创：先改回窄播，再换原文标题。
	stub.live.Content = strings.Replace(notice, "*本文为窄播原创", "*本文为FBIF食品饮料创新（ID：FoodInnovation）原创", 1)
	if res, _ := p.repairNewsLinks(context.Background(), 63177, false); res.RepostNoticeSource != "*本文为窄播原创" ||
		len(stub.puts) != 2 || stub.puts[1].Content != strings.TrimPrefix(want, other) {
		t.Fatalf("写成 FBIF 原创的转载稿应改回窄播：%+v", res)
	}
}

// 2026-10-30 #309：AI 没抽到出处、正文开头写「本文转载自公众号新周刊」，网站文末却写成 FBIF 原创；编辑在网站上
// 改过错字，整篇重新同步会被拦。只换文末声明的开头，别处（编辑改的字）原样写回；判定是原创、编辑手工稿、
// 声明不止一处都不动；说不出原作者的转载写「本文为转载文章」。
func TestRepairNewsLinksRepostNoticeSource(t *testing.T) {
	env := newPublishTestEnv(t)
	lookupFeedOriginalArticleDetail = func(context.Context, string, string) feedOriginalArticleHit { return feedOriginalArticleHit{} }
	lookupWechatOriginalArticle = func(context.Context, string, string, []string) repostOriginalArticle { return repostOriginalArticle{} }
	searchOriginalArticleURLs = func(context.Context, string, string) []string { return nil }
	t.Cleanup(func() {
		lookupFeedOriginalArticleDetail = defaultLookupFeedOriginalArticleDetail
		lookupWechatOriginalArticle = defaultLookupWechatOriginalArticle
		searchOriginalArticleURLs = defaultSearchOriginalArticleURLs
	})
	seed := func(key string, newsID int64, blocks string) {
		t.Helper()
		if _, _, err := env.store.createSeeded(key, "https://mp.weixin.qq.com/s/"+key, func(j *publishJob) {
			j.Stage = publishStagePublished
			j.Title = "19.9元复刻餐厅招牌菜，预制菜进入打工人餐桌"
			j.SourceName = "FBIF食品饮料创新"
			j.FoodTalksNewsID = newsID
			j.BlocksJSON = blocks
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("site:309", 63280, `[{"k":"txt","runs":[{"type":"txt","text":"本文转载自公众号新周刊（ID：new-weekly）"}]},{"k":"txt","runs":[{"type":"txt","text":"正文"}]}]`)
	seed("site:own", 63281, `[{"k":"txt","runs":[{"type":"txt","text":"正文"}]},{"k":"txt","runs":[{"type":"txt","text":"* 本文为FBIF原创，欢迎转发朋友圈；"}]}]`)
	seed("site:unnamed", 63282, `[{"k":"txt","runs":[{"type":"txt","text":"正文"}]},{"k":"txt","runs":[{"type":"txt","text":"*本文为转载文章。"}]}]`)
	notice := func(prefix string) string {
		return `<p><em><span style="font-size: 14px; color: #7f7f7f;">` + prefix +
			`《19.9元复刻餐厅招牌菜，预制菜进入打工人餐桌》，不代表FoodTalks立场，转载请联系出处。</span></em></p>`
	}
	body := `<p>图片来源：盒马</p>`
	stub := &seriesRepairStub{}
	p := env.pipeline
	p.foodTalks = func() (foodTalksAPI, error) { return stub, nil }
	run := func(newsID int64, content string, dryRun bool) newsLinkRepairResult {
		t.Helper()
		stub.live = foodTalksNewsDetail{ID: newsID, Author: "FBIF", Content: content, PublishStatus: foodTalksPublishStatusPublished, Language: "ZH"}
		res, err := p.repairNewsLinks(context.Background(), newsID, dryRun)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	live := body + notice(ownRepostNoticePrefix)
	if res := run(63280, live, true); !res.Changed || res.RepostNoticeSource != "*本文为新周刊原创" || len(stub.puts) != 0 {
		t.Fatalf("dry_run 报告要改成新周刊、不写：%+v puts=%d", res, len(stub.puts))
	}
	if res := run(63280, live, false); !res.Changed || len(stub.puts) != 1 || stub.puts[0].Content != body+notice("*本文为新周刊原创") {
		t.Fatalf("只换声明开头：%+v\n got %s", res, stub.puts[0].Content)
	}
	if res := run(63280, stub.puts[0].Content, false); res.Changed || len(stub.puts) != 1 {
		t.Fatalf("已改好的不再写：%+v", res)
	}
	if res := run(63282, live, false); res.RepostNoticeSource != "*本文为转载文章" || stub.puts[1].Content != body+notice("*本文为转载文章") {
		t.Fatalf("说不出原作者写「本文为转载文章」：%+v", res)
	}
	for _, c := range []struct {
		name    string
		newsID  int64
		content string
	}{
		{"判定是自家原创", 63281, live},
		{"编辑手工稿（任务库里没有）", 63999, live},
		{"声明不止一处", 63280, live + notice(ownRepostNoticePrefix)},
	} {
		if res := run(c.newsID, c.content, false); res.Changed || len(stub.puts) != 2 {
			t.Fatalf("%s 不得改：%+v", c.name, res)
		}
	}
}

// 2026-10-23 #289：旧排版把「Salt Co.和」改成「Salt Co。和」。存量稿只把缩写后的「。」改回半角，句末不动。
func TestRepairNewsLinksAbbreviationPeriods(t *testing.T) {
	stub := &seriesRepairStub{live: foodTalksNewsDetail{ID: 63225, Author: "FBIF", PublishStatus: foodTalksPublishStatusPublished,
		Content: `<p>Jacobsen Salt Co。和Counter Culture Coffee等品牌，而Row 7 Seed Co。的罐装农产品；现制茶 vs。 包装茶，U.S。，并联系Bobo。</p>`}}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil }}
	res, err := p.repairNewsLinks(context.Background(), 63225, false)
	if err != nil || !res.Changed || res.AbbreviationPeriods != 4 || len(stub.puts) != 1 {
		t.Fatalf("应改回 4 处并写回一次：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	want := `<p>Jacobsen Salt Co.和Counter Culture Coffee等品牌，而Row 7 Seed Co.的罐装农产品；现制茶 vs. 包装茶，U.S.，并联系Bobo。</p>`
	if got := stub.puts[0].Content; got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestSiteAbbreviationWordsMatchRenderer(t *testing.T) {
	js, err := os.ReadFile("../public/js/punctuation.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`ABBREVIATION_TAIL_RE = /\(\?:\^\|\[\^A-Za-z0-9\.\]\)\(\?:\(\?:([A-Za-z|]+)\)`).FindSubmatch(js)
	if m == nil {
		t.Fatal("punctuation.js 里找不到 ABBREVIATION_TAIL_RE 的缩写表")
	}
	if got, want := strings.Join(siteAbbreviationWords, "|"), string(m[1]); got != want {
		t.Fatalf("存量修复的缩写表要和排版的一样：\n Go %s\n JS %s", got, want)
	}
}
