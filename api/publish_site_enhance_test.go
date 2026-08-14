package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ---- 目录注入 ----

func siteTocTestContent(n int) string {
	var b strings.Builder
	b.WriteString(`<p style="text-align: justify;">开头</p>`)
	titles := []string{"两个卖车的", "把茶叶装进袋子", "一台咖啡包装机", "买下荒茶", "去澳大利亚种茶", "59款茶", "喂，喝茶"}
	for i := 0; i < n; i++ {
		b.WriteString(`<h2 style="text-align: justify;">` + titles[i%len(titles)] + `</h2><p>段落</p>`)
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
	content := siteTocTestContent(5)
	got, toc := publishSiteTocForContent(content)
	if toc != "" || got != content {
		t.Fatalf("5 个 h2（不足 >5）不得生成目录")
	}
	anchored := strings.Repeat(`<h2 id="mctoc_x0" style="a">T</h2>`, 6)
	got, toc = publishSiteTocForContent(anchored)
	if toc != "" || got != anchored {
		t.Fatalf("已带锚点的正文不得二次注入")
	}
}

func TestPublishSiteTocUnescapesTitle(t *testing.T) {
	content := strings.Repeat(`<h2>M&amp;A <span>与并购</span></h2>`, 6)
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
	// 非 Mote 文章不挂标签
	other := publishJob{Author: "张三", BlocksJSON: `[{"k":"txt","text":"这是我茶叶系列的第29篇。"}]`}
	if tags := p.siteNewsTagsFor(context.Background(), other); len(tags) != 0 {
		t.Fatalf("非 Mote 文章不得自动挂标签：%+v", tags)
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
	// 转载不生成目录
	repost := job
	repost.SourceName = "外部公众号"
	news, err = p.buildSiteNews(context.Background(), repost, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.TOC != "" || strings.Contains(news.Content, `id="mctoc_`) {
		t.Fatalf("转载不得生成目录：toc=%q", news.TOC)
	}
	// 标题少（≤5）不生成
	short := job
	short.ContentHTML = siteTocTestContent(5)
	news, _ = p.buildSiteNews(context.Background(), short, foodTalksPublishStatusDraft)
	if news.TOC != "" {
		t.Fatalf("h2 不足不得生成目录：toc=%q", news.TOC)
	}
}
