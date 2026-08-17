package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ---- 匹配策略 ----

func TestMatchFoodTalksCompany(t *testing.T) {
	moutai := foodTalksCompany{ID: 105, CnName: "贵州茅台酒股份有限公司", ShortCnName: "贵州茅台,茅台股份,茅台", URLCode: "Kweichow_Moutai"}
	records := []foodTalksCompany{moutai}
	// 官方简称精确命中（2026-08-17 生产实测数据形态）
	if got := matchFoodTalksCompany(records, "茅台"); got == nil || got.ID != 105 {
		t.Fatalf("简称精确命中应返回茅台：%+v", got)
	}
	// 全称命中
	if got := matchFoodTalksCompany(records, "贵州茅台酒股份有限公司"); got == nil || got.ID != 105 {
		t.Fatalf("全称命中失败")
	}
	// 非精确词（部分包含）不命中——宁缺勿错
	if got := matchFoodTalksCompany(records, "茅"); got != nil {
		t.Fatalf("部分词不得命中：%+v", got)
	}
	if got := matchFoodTalksCompany(records, "飞天茅台酒"); got != nil {
		t.Fatalf("未收录的变体不得命中：%+v", got)
	}
	// 多条命中：优先中国子公司（规范：国内场景优先境内主体）
	sams := []foodTalksCompany{
		{ID: 900, CnName: "山姆会员商店", ShortCnName: "山姆", URLCode: "Sams_Club"},
		{ID: 901, CnName: "山姆会员商店中国", ShortCnName: "山姆,山姆中国", URLCode: "Sams_Club_China"},
	}
	if got := matchFoodTalksCompany(sams, "山姆"); got == nil || got.ID != 901 {
		t.Fatalf("同名多条应优先中国子公司：%+v", got)
	}
	// 多条且无中国主体区分 → 歧义放弃
	twins := []foodTalksCompany{
		{ID: 1, CnName: "同名公司A", ShortCnName: "同名", URLCode: "a"},
		{ID: 2, CnName: "同名公司B", ShortCnName: "同名", URLCode: "b"},
	}
	if got := matchFoodTalksCompany(twins, "同名"); got != nil {
		t.Fatalf("歧义命中应放弃交人工：%+v", got)
	}
	// 缺 urlCode 的记录构不成前台链接，跳过
	noCode := []foodTalksCompany{{ID: 3, CnName: "无代号公司", ShortCnName: "无代号", URLCode: ""}}
	if got := matchFoodTalksCompany(noCode, "无代号"); got != nil {
		t.Fatalf("缺 urlCode 不得命中：%+v", got)
	}
}

func TestSplitFoodTalksShortNames(t *testing.T) {
	got := splitFoodTalksShortNames("贵州茅台,茅台股份，茅台、老名")
	if len(got) != 4 || got[2] != "茅台" {
		t.Fatalf("简称表应兼容中英文逗号与顿号：%v", got)
	}
	if splitFoodTalksShortNames("  ") != nil {
		t.Fatalf("空表应返回 nil")
	}
}

// ---- 正文注入 ----

func testMoutaiMatch() siteCompanyMatch {
	return siteCompanyMatch{
		Company: foodTalksCompany{ID: 105, CnName: "贵州茅台酒股份有限公司", URLCode: "Kweichow_Moutai"},
		Anchor:  "茅台",
	}
}

func TestInjectCompanyLinksFirstOccurrenceOnly(t *testing.T) {
	html := `<p>第一段讲茅台的故事。</p><p>第二段又提到茅台。</p>`
	out := injectCompanyLinks(html, []siteCompanyMatch{testMoutaiMatch()})
	want := `<a class="mc-company-link" title="茅台" href="https://www.foodtalks.cn/company/Kweichow_Moutai" target="_blank" rel="noopener" data-id="105" data-name="贵州茅台酒股份有限公司" data-code="Kweichow_Moutai" data-type="company">茅台</a>`
	if strings.Count(out, "mc-company-link") != 1 {
		t.Fatalf("每家公司只链首次出现，实际：%s", out)
	}
	if !strings.Contains(out, `<p>第一段讲`+want+`的故事。</p>`) {
		t.Fatalf("链接形态/位置不对（须对齐小工具箱成品 DOM）：%s", out)
	}
	if !strings.Contains(out, `<p>第二段又提到茅台。</p>`) {
		t.Fatalf("第二次出现应保持纯文本：%s", out)
	}
}

func TestInjectCompanyLinksSkipsForbiddenRegions(t *testing.T) {
	m := testMoutaiMatch()
	// 标题里的公司名不加（规范：正文标题处不添加公司库）
	html := `<h2>茅台七年来首登第一</h2><p>正文里茅台再次出现。</p>`
	out := injectCompanyLinks(html, []siteCompanyMatch{m})
	if !strings.Contains(out, `<h2>茅台七年来首登第一</h2>`) {
		t.Fatalf("标题内不得注入：%s", out)
	}
	if !strings.Contains(out, `正文里<a class="mc-company-link"`) {
		t.Fatalf("应落在标题后的正文首次出现处：%s", out)
	}
	// 图注与已有链接内不加
	html = `<figure><figcaption>图片来源：茅台官网</figcaption></figure><p><a href="https://x.com">茅台官网</a>入口，正文茅台说明。</p>`
	out = injectCompanyLinks(html, []siteCompanyMatch{m})
	if !strings.Contains(out, `<figcaption>图片来源：茅台官网</figcaption>`) {
		t.Fatalf("图注内不得注入：%s", out)
	}
	if !strings.Contains(out, `<a href="https://x.com">茅台官网</a>`) {
		t.Fatalf("已有链接内不得嵌套注入：%s", out)
	}
	if !strings.Contains(out, `正文<a class="mc-company-link"`) {
		t.Fatalf("应落在链接外的正文出现处：%s", out)
	}
	// 正文找不到该词 → 原样返回
	if got := injectCompanyLinks(`<p>没有该词。</p>`, []siteCompanyMatch{m}); got != `<p>没有该词。</p>` {
		t.Fatalf("找不到词应原样返回：%s", got)
	}
	// 属性值里的词不受影响（标签内部整体跳过）
	html = `<p><img src="https://cdn/x.png" alt="茅台酒瓶">正文茅台。</p>`
	out = injectCompanyLinks(html, []siteCompanyMatch{m})
	if !strings.Contains(out, `alt="茅台酒瓶"`) {
		t.Fatalf("属性值不得被改写：%s", out)
	}
	if !strings.Contains(out, `正文<a class="mc-company-link"`) {
		t.Fatalf("文本节点应注入：%s", out)
	}
}

// ---- 解析（stub 检索）----

type companySearchStub struct {
	newsTagStub
	byKeyword map[string][]foodTalksCompany
	calls     []string
}

func (s *companySearchStub) SearchCompanies(_ context.Context, keyword string) ([]foodTalksCompany, error) {
	s.calls = append(s.calls, keyword)
	return s.byKeyword[keyword], nil
}

func TestSiteCompanyMatchesFor(t *testing.T) {
	stub := &companySearchStub{byKeyword: map[string][]foodTalksCompany{
		"茅台": {{ID: 105, CnName: "贵州茅台酒股份有限公司", ShortCnName: "贵州茅台,茅台股份,茅台", URLCode: "Kweichow_Moutai"}},
		"饮料": {{ID: 7, CnName: "某饮料集团股份有限公司", ShortCnName: "某饮集团", URLCode: "yinliao"}},
	}}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil }}
	job := publishJob{ID: 1, Tags: []string{"茅台", "饮料", "创新周报"}}
	got := p.siteCompanyMatchesFor(context.Background(), job)
	if len(got) != 1 || got[0].Company.ID != 105 || got[0].Anchor != "茅台" {
		t.Fatalf("应只命中精确匹配的茅台（品类词「饮料」非该公司官方名、栏目词被拦）：%+v", got)
	}
	for _, c := range stub.calls {
		if c == "创新周报" {
			t.Fatalf("栏目黑名单词不得发起公司检索")
		}
	}
	// 无标签 → 不检索
	stub.calls = nil
	if got := p.siteCompanyMatchesFor(context.Background(), publishJob{ID: 2}); len(got) != 0 || len(stub.calls) != 0 {
		t.Fatalf("无候选词不得检索")
	}
}

// ---- buildSiteNews 集成 ----

func TestBuildSiteNewsInjectsCompanyLinksAndRelatedIDs(t *testing.T) {
	t.Setenv("FOODTALKS_CATEGORY_IDS", "")
	stub := &companySearchStub{byKeyword: map[string][]foodTalksCompany{
		"茅台": {{ID: 105, CnName: "贵州茅台酒股份有限公司", ShortCnName: "茅台", URLCode: "Kweichow_Moutai"}},
	}}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil }}
	job := publishJob{
		Title:       "标题",
		SourceName:  "FBIF食品饮料创新",
		Tags:        []string{"茅台"},
		ContentHTML: `<p>茅台发布业绩公告。</p>`,
		CreatedAt:   "2026-08-17T00:00:00Z",
	}
	news, err := p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if !strings.Contains(news.Content, `class="mc-company-link"`) || !strings.Contains(news.Content, `data-id="105"`) {
		t.Fatalf("正文应注入公司链接：%s", news.Content)
	}
	if len(news.RelatedCompanyIDs) != 1 || news.RelatedCompanyIDs[0] != 105 {
		t.Fatalf("relatedCompanyIds 应写入命中公司：%v", news.RelatedCompanyIDs)
	}
	// 检索不可用 → 降级为无关联，不 fail
	pDown := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return nil, errFoodTalksNotConfigured }}
	news, err = pDown.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("公司检索不可用不得 fail job: %v", err)
	}
	if strings.Contains(news.Content, "mc-company-link") || len(news.RelatedCompanyIDs) != 0 {
		t.Fatalf("降级时不得注入：%v", news.RelatedCompanyIDs)
	}
}
