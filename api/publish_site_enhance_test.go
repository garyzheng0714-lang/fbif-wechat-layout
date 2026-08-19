package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---- 目录注入 ----

// siteTocLongPara 长正文段：目录触发有 3000 字门槛（排版规范），fixture 每段 ~800 字，
// 5 段起总字数即过线，字数条件不干扰标题数条件的断言。
var siteTocLongPara = `<p>` + strings.Repeat("这是用于凑足排版规范目录字数门槛的正文内容。", 40) + `</p>`

func siteTocTestContent(n int) string {
	var b strings.Builder
	b.WriteString(`<p style="text-align: justify;">开头</p>`)
	titles := []string{"两个卖车的", "把茶叶装进袋子", "一台咖啡包装机", "买下荒茶", "去澳大利亚种茶", "59款茶", "喂，喝茶"}
	for i := 0; i < n; i++ {
		b.WriteString(`<h2 style="text-align: justify;">` + titles[i%len(titles)] + `</h2>` + siteTocLongPara)
	}
	return b.String()
}

func TestPublishSiteTocInjectsAnchorsAndBuildsJSON(t *testing.T) {
	content := siteTocTestContent(6)
	got, toc := publishSiteTocForContent(content)
	if toc == "" {
		t.Fatalf("6 个 h2 应生成目录")
	}
	var entries []publishSiteTocEntry
	if err := json.Unmarshal([]byte(toc), &entries); err != nil {
		t.Fatalf("toc 不是合法 JSON：%v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("目录应有 6 条，实际 %d", len(entries))
	}
	for i, e := range entries {
		if e.Level != 1 {
			t.Fatalf("level 应为 1，实际 %d", e.Level)
		}
		if !strings.HasPrefix(e.ID, "mctoc_") {
			t.Fatalf("锚点 id 应带 mctoc_ 前缀：%q", e.ID)
		}
		if !strings.Contains(got, `<h2 id="`+e.ID+`" style="text-align: justify;">`) {
			t.Fatalf("正文缺少与目录第 %d 条匹配的锚点（原样式属性必须保留）：%q", i, e.ID)
		}
	}
	if entries[0].Title != "两个卖车的" {
		t.Fatalf("目录标题应取 h2 文本：%q", entries[0].Title)
	}
	// 确定性：同一份正文重复组装必须字节一致（staging→publicize→repair 走 PUT 全量覆盖）
	got2, toc2 := publishSiteTocForContent(content)
	if got2 != got || toc2 != toc {
		t.Fatalf("同一正文重复注入结果必须一致")
	}
}

func TestPublishSiteTocSkipsShortAndAnchored(t *testing.T) {
	// 2026-08-17 对齐排版规范：字数超 3000 且一二级标题（h2+h3）合计 ≥5 即生成。
	content := siteTocTestContent(4)
	got, toc := publishSiteTocForContent(content)
	if toc != "" || got != content {
		t.Fatalf("4 个一二级标题（不足 5）不得生成目录")
	}
	// 5 个 h2 达标（旧口径要 >5，规范是 ≥5）
	if _, toc := publishSiteTocForContent(siteTocTestContent(5)); toc == "" {
		t.Fatalf("5 个 h2 且字数达标应生成目录（规范：5 个及以上）")
	}
	// h3 计入触发计数，但目录条目仍只收 h2
	mixed := siteTocTestContent(3) +
		`<h3>（一）小节</h3>` + siteTocLongPara + `<h3>（二）小节</h3>` + siteTocLongPara
	_, toc = publishSiteTocForContent(mixed)
	if toc == "" {
		t.Fatalf("3 个 h2 + 2 个 h3 合计 5 应生成目录")
	}
	var entries []publishSiteTocEntry
	if err := json.Unmarshal([]byte(toc), &entries); err != nil || len(entries) != 3 {
		t.Fatalf("目录条目应只收 3 个 h2（62765 金标准），实际 %s", toc)
	}
	// 字数不足 3000 不生成（规范：文章字数超 3000）
	short := strings.Repeat(`<h2>标题</h2><p>短段</p>`, 6)
	if got, toc := publishSiteTocForContent(short); toc != "" || got != short {
		t.Fatalf("字数不足 3000 不得生成目录")
	}
	anchored := strings.Repeat(`<h2 id="mctoc_x0" style="a">T</h2>`+siteTocLongPara, 6)
	got, toc = publishSiteTocForContent(anchored)
	if toc != "" || got != anchored {
		t.Fatalf("已带锚点的正文不得二次注入")
	}
}

func TestPublishSiteTocUnescapesTitle(t *testing.T) {
	content := strings.Repeat(`<h2>M&amp;A <span>与并购</span></h2>`+siteTocLongPara, 6)
	_, toc := publishSiteTocForContent(content)
	if !strings.Contains(toc, `M&A 与并购`) && !strings.Contains(toc, `"M&A 与并购"`) {
		var entries []publishSiteTocEntry
		_ = json.Unmarshal([]byte(toc), &entries)
		if len(entries) == 0 || entries[0].Title != "M&A 与并购" {
			t.Fatalf("目录标题应去标签并反转义实体：%s", toc)
		}
	}
}

// ---- Mote / 系列识别 ----

func TestPublishSiteIsMoteArticle(t *testing.T) {
	if !publishSiteIsMoteArticle(publishJob{Author: "Mote 莫特"}) {
		t.Fatalf("任务作者 Mote 莫特 应判 Mote 文章")
	}
	blocks := `[{"k":"txt","runs":[{"type":"txt","text":"作者：Mote莫特"}]},{"k":"txt","text":"正文"}]`
	if !publishSiteIsMoteArticle(publishJob{Author: "FBIF", BlocksJSON: blocks}) {
		t.Fatalf("正文作者行 作者：Mote莫特 应判 Mote 文章")
	}
	if publishSiteIsMoteArticle(publishJob{Author: "FBIF", BlocksJSON: `[{"k":"txt","text":"作者：张三"}]`}) {
		t.Fatalf("非 Mote 作者不得误判")
	}
	// 图注里的 Mote 不算署名
	if publishSiteIsMoteArticle(publishJob{BlocksJSON: `[{"k":"cap","text":"作者：Mote莫特"}]`}) {
		t.Fatalf("图注不参与作者行判定")
	}
}

func TestPublishSiteSeriesNames(t *testing.T) {
	blocks := `[
	 {"k":"txt","runs":[{"type":"txt","text":"这是我茶叶系列的第29篇。"}]},
	 {"k":"txt","text":"我在茶叶系列第6篇写韩国的故事"},
	 {"k":"txt","text":"新品系列上市，与目录无关"}
	]`
	got := publishSiteSeriesNames(blocks)
	if len(got) != 1 || got[0] != "茶叶系列" {
		t.Fatalf("应抽出去重后的 茶叶系列，实际 %v", got)
	}
	if names := publishSiteSeriesNames(`[{"k":"txt","text":"没有系列声明"}]`); len(names) != 0 {
		t.Fatalf("无声明不得抽出系列：%v", names)
	}
}

// ---- 新闻标签解析 ----

type newsTagStub struct {
	byKeyword map[string][]foodTalksTag
	created   []string
}

func (s *newsTagStub) CreateNews(context.Context, foodTalksNews) (int64, error) { return 0, nil }
func (s *newsTagStub) UpdateNews(context.Context, foodTalksNews) error          { return nil }
func (s *newsTagStub) ListTagsByType(_ context.Context, tagTypeID int64, keywords string) ([]foodTalksTag, error) {
	if tagTypeID != foodTalksTagTypeNews {
		return nil, nil
	}
	return s.byKeyword[keywords], nil
}
func (s *newsTagStub) CreateTagByName(_ context.Context, _ int64, name string) (int64, error) {
	s.created = append(s.created, name)
	return 999, nil
}

func (s *newsTagStub) SearchNewsByTitle(context.Context, string) ([]foodTalksNewsListItem, error) {
	return nil, nil
}

func (s *newsTagStub) SearchCompanies(context.Context, string) ([]foodTalksCompany, error) {
	return nil, nil
}

func (s *newsTagStub) GetNews(context.Context, int64) (foodTalksNewsDetail, error) {
	return foodTalksNewsDetail{}, errors.New("测试桩未提供资讯详情")
}

func TestSiteNewsTagsForMoteArticle(t *testing.T) {
	stub := &newsTagStub{byKeyword: map[string][]foodTalksTag{
		"Mote莫特": {{ID: 99913, Name: "Mote莫特", TagTypeID: 6}},
		"茶叶系列":   {{ID: 114000, Name: "茶叶系列", TagTypeID: 6}},
	}}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil }}
	job := publishJob{
		Author:     "FBIF",
		BlocksJSON: `[{"k":"txt","text":"作者：Mote莫特"},{"k":"txt","text":"这是我茶叶系列的第29篇。"}]`,
	}
	tags := p.siteNewsTagsFor(context.Background(), job)
	if len(tags) != 2 || tags[0].TagID != 99913 || tags[1].TagID != 114000 {
		t.Fatalf("Mote 文章应挂 Mote莫特 + 茶叶系列，实际 %+v", tags)
	}
	if len(stub.created) != 0 {
		t.Fatalf("新闻标签只解析不创建，实际创建了 %v", stub.created)
	}
	// 库里没有对应标签 → 跳过且不创建
	stub2 := &newsTagStub{byKeyword: map[string][]foodTalksTag{}}
	p2 := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub2, nil }}
	if tags := p2.siteNewsTagsFor(context.Background(), job); len(tags) != 0 {
		t.Fatalf("标签库缺失时应跳过：%+v", tags)
	}
	if len(stub2.created) != 0 {
		t.Fatalf("缺失也不得自动创建：%v", stub2.created)
	}
	// 非 Mote 文章：不挂 Mote/系列，但 AI 预填标签照常解析
	other := publishJob{Author: "张三", BlocksJSON: `[{"k":"txt","text":"这是我茶叶系列的第29篇。"}]`}
	if tags := p.siteNewsTagsFor(context.Background(), other); len(tags) != 0 {
		t.Fatalf("非 Mote 且无 AI 标签时应为空：%+v", tags)
	}
}

func TestSiteNewsTagsResolvesAITagsForAllArticles(t *testing.T) {
	stub := &newsTagStub{byKeyword: map[string][]foodTalksTag{
		"瑞幸咖啡":   {{ID: 111, Name: "瑞幸咖啡", TagTypeID: 6}},
		"新茶饮":    {{ID: 112, Name: "新茶饮", TagTypeID: 6}},
		"Mote莫特": {{ID: 99913, Name: "Mote莫特", TagTypeID: 6}},
		"茶叶系列":   {{ID: 114000, Name: "茶叶系列", TagTypeID: 6}},
		"零售":     {{ID: 113, Name: "零售", TagTypeID: 6}},
	}}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil }}
	// 普通转载/原创：AI 预填标签按库解析（7 月实证：编辑给每篇挂 1-2 个品牌/品类词）
	job := publishJob{Author: "张三", Tags: []string{"瑞幸咖啡", "新茶饮", "不存在的词"}}
	tags := p.siteNewsTagsFor(context.Background(), job)
	if len(tags) != 2 || tags[0].TagID != 111 || tags[1].TagID != 112 {
		t.Fatalf("AI 标签应解析入库且跳过未命中项：%+v", tags)
	}
	if len(stub.created) != 0 {
		t.Fatalf("不得自动创建：%v", stub.created)
	}
	// Mote 文章叠加 AI 标签时，Mote/系列优先且总数封顶 3
	mote := publishJob{
		Author:     "Mote莫特",
		BlocksJSON: `[{"k":"txt","text":"这是我茶叶系列的第29篇。"}]`,
		Tags:       []string{"瑞幸咖啡", "新茶饮", "零售"},
	}
	tags = p.siteNewsTagsFor(context.Background(), mote)
	if len(tags) != 3 || tags[0].TagName != "Mote莫特" || tags[1].TagName != "茶叶系列" {
		t.Fatalf("Mote/系列应优先且封顶 3 个：%+v", tags)
	}
	// 重名去重（AI 标签与系列名重复只算一次）
	dup := publishJob{Author: "Mote 莫特", BlocksJSON: `[{"k":"txt","text":"这是我茶叶系列的第29篇。"}]`, Tags: []string{"茶叶系列"}}
	tags = p.siteNewsTagsFor(context.Background(), dup)
	if len(tags) != 2 {
		t.Fatalf("重名标签应去重：%+v", tags)
	}
}

func TestSiteNewsTagsBlocksColumnAndEventTags(t *testing.T) {
	stub := &newsTagStub{byKeyword: map[string][]foodTalksTag{
		"瑞幸咖啡":         {{ID: 111, Name: "瑞幸咖啡", TagTypeID: 6}},
		"创新周报":         {{ID: 200, Name: "创新周报", TagTypeID: 6}},
		"FBIF2026会后报告": {{ID: 201, Name: "FBIF2026会后报告", TagTypeID: 6}},
		"Wow食品创新奖":     {{ID: 202, Name: "Wow食品创新奖", TagTypeID: 6}},
	}}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil }}
	// AI 输出混入栏目/活动标签时只保留内容标签（排版规范：不添加首发栏目为标签；
	// 编辑规范 §4：栏目·活动标签由编辑手工体系管理，AI 不打）
	job := publishJob{Author: "张三", Tags: []string{"创新周报", "FBIF2026会后报告", "Wow食品创新奖", "瑞幸咖啡"}}
	tags := p.siteNewsTagsFor(context.Background(), job)
	if len(tags) != 1 || tags[0].TagID != 111 {
		t.Fatalf("栏目/活动标签应被拦截，只留内容标签：%+v", tags)
	}
}

func TestPublishSiteTagBlocked(t *testing.T) {
	for _, name := range []string{"独家专栏", "创新周报", "有料", "FBIF2026", "FBIF 2026", "FBIF2026会后报告", "FBIF2026参展企业", "Wow Food Awards", "Wow食品创新奖", "vfood", "Vfood"} {
		if !publishSiteTagBlocked(name) {
			t.Fatalf("%q 是栏目/活动名，应被拦截", name)
		}
	}
	// 品类词与品牌名是合法标签，绝不拦截
	for _, name := range []string{"饮料", "乳品", "咖啡", "瑞幸咖啡", "盒马", "FBIF", "Mote莫特", "茶叶系列", "新品牌"} {
		if publishSiteTagBlocked(name) {
			t.Fatalf("%q 是合法内容标签，不得拦截", name)
		}
	}
}

func TestPublishDeclaredSource(t *testing.T) {
	// 任务 #101 形态：自家号转发天下网商，正文头部「来源：天下网商（ID：xxx）」
	blocks := `[{"k":"txt","text":"奶奶辈土味饮料翻红"},{"k":"txt","text":"来源：天下网商（ID：txws2013）"},{"k":"txt","text":"正文开始"}]`
	if got := publishDeclaredSource(blocks); got != "天下网商" {
		t.Fatalf("应抽出声明出处 天下网商，实际 %q", got)
	}
	// 「本文来自微信公众号：小食代」形态（编辑规范 §1 实例）
	blocks = `[{"k":"txt","text":"本文来自微信公众号：小食代，作者：何丹琳"}]`
	if got := publishDeclaredSource(blocks); got != "小食代" {
		t.Fatalf("应抽出 小食代，实际 %q", got)
	}
	// 「本文为新零售原创」形态
	blocks = `[{"k":"txt","text":"本文为新零售原创，作者：陈新生，转载请联系出处。"}]`
	if got := publishDeclaredSource(blocks); got != "新零售" {
		t.Fatalf("应抽出 新零售，实际 %q", got)
	}
	// 图注「来源：pexels」不参与（k=cap 跳过）
	blocks = `[{"k":"txt","text":"正文"},{"k":"cap","text":"来源：pexels"}]`
	if got := publishDeclaredSource(blocks); got != "" {
		t.Fatalf("图注不得参与出处判定：%q", got)
	}
	// 正文中段的「来源：xxx」不算声明（只扫头部前 12 个文本块与尾部区域）
	var mid strings.Builder
	mid.WriteString(`[`)
	for i := 0; i < 20; i++ {
		mid.WriteString(`{"k":"txt","text":"正文段落"},`)
	}
	mid.WriteString(`{"k":"txt","text":"来源：某数据库"},`)
	for i := 0; i < 30; i++ {
		mid.WriteString(`{"k":"txt","text":"正文段落"},`)
	}
	mid.WriteString(`{"k":"txt","text":"结尾"}]`)
	if got := publishDeclaredSource(mid.String()); got != "" {
		t.Fatalf("正文中段的来源行不得当出处声明：%q", got)
	}
	// 尾部区域的「来源：xxx」要认（转载稿模板尾部形态，运营截图案例）
	var tail strings.Builder
	tail.WriteString(`[`)
	for i := 0; i < 30; i++ {
		tail.WriteString(`{"k":"txt","text":"正文段落"},`)
	}
	tail.WriteString(`{"k":"txt","text":"来源：天下网商"},{"k":"txt","text":"作者：叶晨"}]`)
	if got := publishDeclaredSource(tail.String()); got != "天下网商" {
		t.Fatalf("尾部声明应被识别：%q", got)
	}
}

func TestPublishArticleProvenanceDeclaredSourceFallback(t *testing.T) {
	// AI 漏抽出处（AISource 空）时确定性抽取兜底 → 判转载、来源=声明出处
	job := publishJob{
		SourceName: "FBIF食品饮料创新",
		BlocksJSON: `[{"k":"txt","text":"来源：天下网商（ID：txws2013）"},{"k":"txt","text":"正文"}]`,
	}
	original, source := publishArticleProvenance(job)
	if original || source != "天下网商" {
		t.Fatalf("声明出处兜底应判转载且来源=天下网商，实际 original=%v source=%q", original, source)
	}
	// 自家原创声明（本文为FBIF食品饮料创新原创）不改判：仍按自家清单走原创
	own := publishJob{
		SourceName: "FBIF食品饮料创新",
		BlocksJSON: `[{"k":"txt","text":"本文为FBIF食品饮料创新原创，转载请联系授权。"},{"k":"txt","text":"正文"}]`,
	}
	original, source = publishArticleProvenance(own)
	if !original || source != "FBIF食品饮料创新" {
		t.Fatalf("自家原创声明不得误判转载，实际 original=%v source=%q", original, source)
	}
	// AISource 仍然最优先
	ai := job
	ai.AISource = "小食代"
	if _, source := publishArticleProvenance(ai); source != "小食代" {
		t.Fatalf("AISource 应优先于确定性抽取：%q", source)
	}
}

func TestPublishRenderSourceURLPrefersOriginalForRepost(t *testing.T) {
	repost := publishJob{
		SourceURL:         "https://mp.weixin.qq.com/s/fbif-repost",
		OriginalSourceURL: "https://mp.weixin.qq.com/s/original",
		SourceName:        "FBIF食品饮料创新",
		BlocksJSON:        `[{"k":"txt","text":"来源：天下网商"}]`,
	}
	if got := publishRenderSourceURL(repost); got != "https://mp.weixin.qq.com/s/original" {
		t.Fatalf("转载稿应优先用阅读原文链接：%q", got)
	}
	// 原创稿：即使有阅读原文链接也用提交链接（阅读原文可能是活动页）
	original := publishJob{
		SourceURL:         "https://mp.weixin.qq.com/s/fbif-own",
		OriginalSourceURL: "https://mp.weixin.qq.com/s/event-page",
		SourceName:        "FBIF食品饮料创新",
		BlocksJSON:        `[{"k":"txt","text":"正文"}]`,
	}
	if got := publishRenderSourceURL(original); got != "https://mp.weixin.qq.com/s/fbif-own" {
		t.Fatalf("原创稿应用提交链接：%q", got)
	}
	// 自家号转发且没抓到阅读原文 → 不得回退自家转载页
	repost.OriginalSourceURL = ""
	if got := publishRenderSourceURL(repost); got != "" {
		t.Fatalf("自家转载且无阅读原文时不得回退提交链接：%q", got)
	}
	// 提交的就是原账号自己的文章 → 提交链接就是原文
	external := publishJob{
		SourceURL:  "https://mp.weixin.qq.com/s/jingzhe-original",
		SourceName: "惊蛰研究所",
		BlocksJSON: `[{"k":"txt","text":"正文"}]`,
	}
	if got := publishRenderSourceURL(external); got != "https://mp.weixin.qq.com/s/jingzhe-original" {
		t.Fatalf("外号原文应保留提交链接：%q", got)
	}
}

func TestBuildSiteNewsCategoryBySourceType(t *testing.T) {
	t.Setenv("FOODTALKS_CATEGORY_IDS", "")
	t.Setenv("FOODTALKS_REPOST_CATEGORY_IDS", "")
	p := &publishPipeline{now: time.Now}
	base := publishJob{Title: "标题", ContentHTML: "<p>正文</p>", CreatedAt: "2026-08-14T00:00:00Z"}
	// 原创（自有号）→ 独家专栏（沿用 FOODTALKS_CATEGORY_IDS 默认 52,58）
	orig := base
	orig.SourceName = "FBIF食品饮料创新"
	news, err := p.buildSiteNews(context.Background(), orig, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatal(err)
	}
	if news.PrimaryCategoryID != 52 {
		t.Fatalf("原创主分类应为独家专栏(52)，实际 %d", news.PrimaryCategoryID)
	}
	// 转载 → 食品新闻(53)（2026-08-14 用户裁定：独家专栏是原创专属；7 月转载 108/120 挂食品新闻）
	repost := base
	repost.SourceName = "PackHorizon"
	news, err = p.buildSiteNews(context.Background(), repost, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatal(err)
	}
	if news.PrimaryCategoryID != 53 || len(news.CategoryIDs) != 1 || news.CategoryIDs[0] != 53 {
		t.Fatalf("转载主分类应为食品新闻(53)，实际 primary=%d cats=%v", news.PrimaryCategoryID, news.CategoryIDs)
	}
}

// ---- buildSiteNews 集成 ----

func TestBuildSiteNewsInjectsTocForOriginalLongArticle(t *testing.T) {
	t.Setenv("FOODTALKS_CATEGORY_IDS", "")
	p := &publishPipeline{now: time.Now}
	job := publishJob{
		Title:       "标题",
		SourceName:  "FBIF食品饮料创新", // 自有号 → 原创
		ContentHTML: siteTocTestContent(6),
		CreatedAt:   "2026-08-14T00:00:00Z",
	}
	news, err := p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.TOC == "" || !strings.Contains(news.Content, `id="mctoc_`) {
		t.Fatalf("原创长文应注入目录：toc=%q", news.TOC)
	}
	// 转载同样生成目录（2026-08-17 用户裁定：所有文章一视同仁，历史没加多为人工漏掉）
	repost := job
	repost.SourceName = "外部公众号"
	news, err = p.buildSiteNews(context.Background(), repost, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.TOC == "" || !strings.Contains(news.Content, `id="mctoc_`) {
		t.Fatalf("转载长文也应生成目录：toc=%q", news.TOC)
	}
	// 标题少（<5）不生成
	short := job
	short.ContentHTML = siteTocTestContent(4)
	news, _ = p.buildSiteNews(context.Background(), short, foodTalksPublishStatusDraft)
	if news.TOC != "" {
		t.Fatalf("h2 不足不得生成目录：toc=%q", news.TOC)
	}
}
