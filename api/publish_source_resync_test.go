package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestPublishedSourceResyncUpdatesSameFoodTalksArticle(t *testing.T) {
	env, published := publishedSourceResyncTestJob(t)
	seedSourceResyncLiveNews(t, env, published, "")
	env.ft.mu.Lock()
	live := env.ft.newsByID[published.FoodTalksNewsID]
	live.CoverImg = "https://oss.example.com/cms-cover.png"
	live.Summary = "运营在 CMS 保留的摘要"
	live.NewsTags = []foodTalksNewsTag{{TagID: 114000, TagName: "茶叶系列"}}
	live.NewsCategories = []foodTalksNewsCategoryDetail{{
		CategoryTagID:   54,
		ChildCategories: []foodTalksNewsCategoryDetail{{IsPrimary: 1, CategoryTagID: 76}},
	}}
	env.ft.newsByID[published.FoodTalksNewsID] = live
	env.ft.mu.Unlock()

	latestBlocks := `[{"k":"p","text":"公众号刚刚更新的正文"}]`
	env.pipeline.fetch = func(context.Context, string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: published.Title, SourceName: published.SourceName, Byline: published.Author, CanonicalURL: published.SourceURL, BlocksJSON: latestBlocks}, nil
	}
	latestHTML := `<section><p>公众号刚刚更新的正文</p></section>`
	env.pipeline.render = func(context.Context, string, string, string, bool) (publishRenderResult, error) {
		return successfulPublishRenderResult(latestHTML, testPublishResolvedRule()), nil
	}

	_, beforeUpdate := env.ft.counts()
	version := publishSourceResyncCardVersion(published)
	updated, err := env.pipeline.requestSourceResync(published.ID, "ou_resync_editor", version)
	if err != nil {
		t.Fatalf("request source resync: %v", err)
	}
	assertSourceResyncResult(t, published, updated, latestBlocks)
	assertSingleFoodTalksUpdate(t, env.ft, beforeUpdate, published.FoodTalksNewsID)
	// 下一次尝试若在 PUT 前安全失败，当前线上版本上一次已完成的
	// 人工授权仍然有效；否则会连重试、下线与外发都一起锁死。
	failedRetry, err := env.pipeline.requestSourceResync(
		updated.ID, "ou_resync_editor", publishSourceResyncCardVersion(updated),
	)
	if err != nil || failedRetry.SourceResyncState != publishSourceResyncFailed || !validPersistedPublishApproval(failedRetry) {
		t.Fatalf("后续安全失败不得作废当前线上版本授权：%+v err=%v", failedRetry, err)
	}
}

func assertSourceResyncResult(t *testing.T, published, updated publishJob, latestBlocks string) {
	t.Helper()
	if updated.SourceResyncState != publishSourceResyncSucceeded {
		t.Fatalf("重新同步应完成，实际 %+v", updated)
	}
	if updated.FoodTalksNewsID != published.FoodTalksNewsID {
		t.Fatalf("必须覆盖同一资讯 ID：before=%d after=%d", published.FoodTalksNewsID, updated.FoodTalksNewsID)
	}
	if updated.BlocksJSON != latestBlocks || !strings.Contains(updated.ContentHTML, "刚刚更新") {
		t.Fatalf("最新正文未落库：blocks=%s html=%s", updated.BlocksJSON, updated.ContentHTML)
	}
	if updated.ContentVersion != published.ContentVersion+1 || updated.ContentFrozenVersion != updated.ContentVersion ||
		updated.SiteContentHash == "" || !validPersistedPublishApproval(updated) {
		t.Fatalf("新内容版本/手动授权未完整落库：%+v", updated)
	}
}

func assertSingleFoodTalksUpdate(t *testing.T, ft *ftStub, beforeUpdate int, wantNewsID int64) {
	t.Helper()
	_, afterUpdate := ft.counts()
	if afterUpdate-beforeUpdate != 1 {
		t.Fatalf("只应 PUT 一次同一篇文章，实际增量=%d", afterUpdate-beforeUpdate)
	}
	ft.mu.Lock()
	updatedNewsID := ft.updateIDs[len(ft.updateIDs)-1]
	ft.mu.Unlock()
	if updatedNewsID != wantNewsID {
		t.Fatalf("PUT 必须使用原资讯 ID：want=%d got=%d", wantNewsID, updatedNewsID)
	}
}

func TestPublishedSourceResyncStopsBeforePUTWhenCMSWasEdited(t *testing.T) {
	env, published := publishedSourceResyncTestJob(t)
	seedSourceResyncLiveNews(t, env, published, `<p>运营在 CMS 手工修改的正文</p>`)
	env.pipeline.fetch = func(context.Context, string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: published.Title, SourceName: published.SourceName, Byline: published.Author, CanonicalURL: published.SourceURL, BlocksJSON: `[{"k":"p","text":"公众号新正文"}]`}, nil
	}
	env.pipeline.render = func(context.Context, string, string, string, bool) (publishRenderResult, error) {
		return successfulPublishRenderResult(`<p>公众号新正文</p>`, testPublishResolvedRule()), nil
	}

	_, beforeUpdate := env.ft.counts()
	updated, err := env.pipeline.requestSourceResync(
		published.ID, "ou_resync_editor", publishSourceResyncCardVersion(published),
	)
	if err != nil {
		t.Fatalf("异步受理本身不应失败: %v", err)
	}
	if updated.SourceResyncState != publishSourceResyncFailed ||
		!strings.Contains(updated.SourceResyncError, "后台已有人工修改") {
		t.Fatalf("应在 PUT 前标记人工改稿冲突：%+v", updated)
	}
	_, afterUpdate := env.ft.counts()
	if afterUpdate != beforeUpdate {
		t.Fatalf("命中人工改稿时不得 PUT：before=%d after=%d", beforeUpdate, afterUpdate)
	}
	if updated.ContentVersion != published.ContentVersion || updated.BlocksJSON != published.BlocksJSON {
		t.Fatalf("被拦截时不得改本地已发布版本")
	}
}

func TestSourceResyncCardActionUsesVersionedButton(t *testing.T) {
	env, published := publishedSourceResyncTestJob(t)
	seedSourceResyncLiveNews(t, env, published, "")
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_resync_editor")
	env.pipeline.fetch = func(context.Context, string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: published.Title, SourceName: published.SourceName, Byline: published.Author, CanonicalURL: published.SourceURL, BlocksJSON: published.BlocksJSON}, nil
	}

	raw, err := json.Marshal(map[string]any{
		"action": feishuCardActionResyncSource, "job_id": published.ID,
		"source_resync_version": publishSourceResyncCardVersion(published),
	})
	if err != nil {
		t.Fatal(err)
	}
	out := env.pipeline.reviewCardAction(context.Background(), "ou_resync_editor", "", raw)
	if out.toastType != "success" || !strings.Contains(out.toastContent, "已受理") {
		t.Fatalf("卡片动作未走重新同步入口：%+v", out)
	}
}

func TestSourceResyncLegacyGuardAcceptsEquivalentCMSMarkup(t *testing.T) {
	stored := `<p>茶叶<a href="https://mp.weixin.qq.com/s/old">系列</a></p><p><img src="https://oss.example.com/a.jpg"></p>`
	live := `<p id="mctoc_1"><span>茶叶</span><a href="https://www.foodtalks.cn/news/1">系列</a></p><p><img width="600" src="https://oss.example.com/a.jpg" /></p>`
	if foodTalksManualContentChanged(live, stored, "") {
		t.Fatal("旧稿缺字节基线时，CMS 序列化与站内链接差异不应误判为人工改正文")
	}
	if !foodTalksManualContentChanged(strings.Replace(live, "系列", "新系列", 1), stored, "") {
		t.Fatal("旧稿的可见文字变化必须拦截")
	}
	if !foodTalksManualContentChanged(strings.Replace(live, "a.jpg", "b.jpg", 1), stored, "") {
		t.Fatal("旧稿的正文图片变化必须拦截")
	}
	if !foodTalksManualContentChanged(live, stored, publishRenderOutputHash(stored)) {
		t.Fatal("已有字节基线的新稿必须继续严格拦截任意 HTML 变化")
	}
}

func TestSourceResyncPreservesCurrentCMSMetadata(t *testing.T) {
	next := foodTalksNews{Content: "<p>公众号新正文</p>", TOC: `[{"id":"new"}]`}
	live := foodTalksNewsDetail{
		Title: "网站标题", CoverImg: "https://oss.example.com/cms-cover.png",
		SourceType: 0, SourceID: 7632, SourceName: "FBIF食品饮料创新", Author: "Mote莫特",
		Summary: "网站摘要", PublishStatus: 1, IsPublic: true, IsLatest: 1,
		ImgPreviewEnable: true, CommentEnable: 1, PublishTime: "2026-09-01 08:43:55",
		NewsCategories: []foodTalksNewsCategoryDetail{{
			CategoryTagID:   54,
			ChildCategories: []foodTalksNewsCategoryDetail{{IsPrimary: 1, CategoryTagID: 76}},
		}},
		NewsTags:           []foodTalksNewsTag{{TagID: 114000, TagName: "茶叶系列"}},
		NewsRelation:       []foodTalksNewsRelationDetail{{RelationNewsID: 20}},
		TopicSectionIDList: []int64{11},
		RelatedCompanies:   []foodTalksRelatedCompanyDetail{{CompanyID: 30}},
		RelatedFoodpedias:  []foodTalksRelatedFoodpedia{{FoodpediaID: 40}},
		Language:           "ZH", SEOTitle: "网站 SEO", SEOKeywords: "茶叶", SEODescription: "网站 SEO 摘要",
	}
	preserveFoodTalksLiveMetadata(&next, live)
	if next.Content != "<p>公众号新正文</p>" || next.TOC != `[{"id":"new"}]` {
		t.Fatal("重新同步只能替换正文与随正文生成的目录")
	}
	if next.Title != live.Title || next.CoverImg != live.CoverImg || next.Summary != live.Summary ||
		next.SourceID != live.SourceID || next.PublishTime != live.PublishTime || next.PrimaryCategoryID != 76 {
		t.Fatalf("网站现有标量发布字段未完整保留：%+v", next)
	}
	if !slices.Equal(next.CategoryIDs, []int64{54, 76}) ||
		!slices.Equal(next.NewsTags, live.NewsTags) ||
		!slices.Equal(next.TopicSectionIDList, live.TopicSectionIDList) ||
		!slices.Equal(next.NewsRelationIDs, []int64{20}) ||
		!slices.Equal(next.RelatedCompanyIDs, []int64{30}) ||
		!slices.Equal(next.RelatedFoodpediaIDs, []int64{40}) {
		t.Fatalf("网站现有关联发布字段未完整保留：%+v", next)
	}
}

func publishedSourceResyncTestJob(t *testing.T) (*publishTestEnv, publishJob) {
	t.Helper()
	env := newPublishTestEnv(t)
	env.ft.forbidPublish = false
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_original_reviewer"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	published, err := env.store.byID(job.ID)
	if err != nil || published.Stage != publishStagePublished {
		t.Fatalf("准备已发布稿失败：%+v err=%v", published, err)
	}
	return env, published
}

func seedSourceResyncLiveNews(t *testing.T, env *publishTestEnv, job publishJob, contentOverride string) {
	t.Helper()
	expected, err := env.pipeline.buildSiteNews(context.Background(), job, foodTalksPublishStatusPublished)
	if err != nil {
		t.Fatalf("build current news: %v", err)
	}
	if contentOverride != "" {
		expected.Content = contentOverride
	}
	detail := foodTalksDetailFromExpected(job.FoodTalksNewsID, expected)
	env.ft.mu.Lock()
	if env.ft.newsByID == nil {
		env.ft.newsByID = map[int64]foodTalksNewsDetail{}
	}
	env.ft.newsByID[job.FoodTalksNewsID] = detail
	env.ft.mu.Unlock()
}

func foodTalksDetailFromExpected(id int64, news foodTalksNews) foodTalksNewsDetail {
	categories := make([]foodTalksNewsCategoryDetail, 0, len(news.CategoryIDs))
	for _, categoryID := range news.CategoryIDs {
		primary := 0
		if categoryID == news.PrimaryCategoryID {
			primary = 1
		}
		categories = append(categories, foodTalksNewsCategoryDetail{IsPrimary: primary, CategoryTagID: categoryID})
	}
	companies := make([]foodTalksRelatedCompanyDetail, 0, len(news.RelatedCompanyIDs))
	for _, companyID := range news.RelatedCompanyIDs {
		companies = append(companies, foodTalksRelatedCompanyDetail{CompanyID: companyID})
	}
	foodpedias := make([]foodTalksRelatedFoodpedia, 0, len(news.RelatedFoodpediaIDs))
	for _, foodpediaID := range news.RelatedFoodpediaIDs {
		foodpedias = append(foodpedias, foodTalksRelatedFoodpedia{FoodpediaID: foodpediaID})
	}
	relations := make([]foodTalksNewsRelationDetail, 0, len(news.NewsRelationIDs))
	for _, relationID := range news.NewsRelationIDs {
		relations = append(relations, foodTalksNewsRelationDetail{RelationNewsID: relationID})
	}
	return foodTalksNewsDetail{
		ID: id, Title: news.Title, Language: news.Language, CoverImg: news.CoverImg,
		SourceType: news.SourceType, SourceID: news.SourceID, SourceName: news.SourceName,
		Author: news.Author, Summary: news.Summary, Content: news.Content, TOC: news.TOC,
		NewsCategories: categories, NewsTags: append([]foodTalksNewsTag{}, news.NewsTags...),
		NewsRelation: relations, RelatedCompanies: companies, RelatedFoodpedias: foodpedias,
		TopicSectionIDList: append([]int64{}, news.TopicSectionIDList...),
		PublishStatus:      news.PublishStatus, IsPublic: news.IsPublic == 1, IsLatest: news.IsLatest,
		ImgPreviewEnable: news.ImgPreviewEnable, CommentEnable: news.CommentEnable,
		SEOTitle: news.SEOTitle, SEOKeywords: news.SEOKeywords, SEODescription: news.SEODescription,
		PublishTime: news.PublishTime,
	}
}
