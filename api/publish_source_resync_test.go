package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPublishedSourceResyncUpdatesSameFoodTalksArticle(t *testing.T) {
	env, published := publishedSourceResyncTestJob(t)
	seedSourceResyncLiveNews(t, env, published, "")

	latestBlocks := `[{"k":"p","text":"公众号刚刚更新的正文"}]`
	env.pipeline.fetch = func(context.Context, string) (string, string, string, string, string, string, error) {
		return published.Title, published.SourceName, published.Author, published.SourceURL, "", latestBlocks, nil
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
	env.pipeline.fetch = func(context.Context, string) (string, string, string, string, string, string, error) {
		return published.Title, published.SourceName, published.Author, published.SourceURL, "", `[{"k":"p","text":"公众号新正文"}]`, nil
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
	env.pipeline.fetch = func(context.Context, string) (string, string, string, string, string, string, error) {
		return published.Title, published.SourceName, published.Author, published.SourceURL, "", published.BlocksJSON, nil
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

func TestSourceResyncManualGuardCoversFullPUTMetadata(t *testing.T) {
	expected := foodTalksNews{
		Title: "标题", CoverImg: "https://oss.example/cover.png", SourceType: 0, SourceID: 7,
		SourceName: "FoodTalks", Author: "FBIF", Summary: "摘要", Content: "<p>正文</p>",
		NewsTags: []foodTalksNewsTag{{TagID: 9, TagName: "茶饮"}}, PublishStatus: 1,
		IsPublic: 1, IsLatest: 1, ImgPreviewEnable: true, CommentEnable: 1,
		PublishTime: "2026-09-01 08:00:00", PrimaryCategoryID: 52, CategoryIDs: []int64{52, 58},
		Language: "ZH", SEOTitle: "标题-FoodTalks全球食品资讯",
	}
	live := foodTalksDetailFromExpected(62915, expected)
	if changes := foodTalksManualChangeFields(live, expected, ""); len(changes) != 0 {
		t.Fatalf("未修改的 CMS 现稿不应被拦截：%v", changes)
	}
	live.Summary = "运营手工改过的摘要"
	changes := foodTalksManualChangeFields(live, expected, "")
	if len(changes) != 1 || !strings.Contains(changes[0], "发布字段") {
		t.Fatalf("全量 PUT 会覆盖的元数据也必须拦截：%v", changes)
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
