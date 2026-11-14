package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
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
		NewsRelation:       []foodTalksNewsRelationDetail{{RelatedNewsID: 20}},
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
		relations = append(relations, foodTalksNewsRelationDetail{RelatedNewsID: relationID})
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

// TestFoodTalksNewsRelationIDsUsesRelatedNewsID 生产详情的相关文章用 relatedNewsId；读错字段会
// 回落成本稿 id，把 43083 的三篇相关文章写成了自己（2026-10-08）。
func TestFoodTalksNewsRelationIDsUsesRelatedNewsID(t *testing.T) {
	var live foodTalksNewsDetail
	raw := `{"id":43083,"newsRelation":[
		{"id":329654,"newsId":43083,"relatedNewsId":43102,"title":"a"},
		{"id":329655,"newsId":43083,"relatedNewsId":42674,"title":"b"},
		{"id":485897,"newsId":43083,"relatedNewsId":43083,"title":"self"}]}`
	if err := json.Unmarshal([]byte(raw), &live); err != nil {
		t.Fatal(err)
	}
	got := foodTalksDetailNewsRelationIDs(live.NewsRelation)
	if len(got) != 2 || got[0] != 43102 || got[1] != 42674 {
		t.Fatalf("应只保留真实相关文章 id、丢弃自引用：%v", got)
	}
}

// TestSourceResyncAdminEndpoint 卡片过了 14 天点不了的旧稿，管理员用接口补同步：与卡片同一条路径，
// 只认管理员；dry_run 只报告不动稿；不能同步的稿（未发布等）如实拒绝。
func TestSourceResyncAdminEndpoint(t *testing.T) {
	env, published := publishedSourceResyncTestJob(t)
	seedSourceResyncLiveNews(t, env, published, "")
	latestBlocks := `[{"k":"p","text":"本文为某媒体原创，作者：张三，转载请联系出处。"}]`
	env.pipeline.fetch = func(context.Context, string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: published.Title, SourceName: published.SourceName, Byline: published.Author, CanonicalURL: published.SourceURL, BlocksJSON: latestBlocks}, nil
	}
	env.pipeline.render = func(context.Context, string, string, string, bool) (publishRenderResult, error) {
		return successfulPublishRenderResult(`<section><p>本文为某媒体原创，作者：张三，转载请联系出处。</p><p>刚刚更新</p></section>`, testPublishResolvedRule()), nil
	}
	mux := newPublishTestMux(t, env)
	target := fmt.Sprintf("/api/publish/jobs/%d/source-resync", published.ID)
	_, beforeUpdate := env.ft.counts()

	if rec := doPublishReq(mux, http.MethodPost, target, "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无管理员令牌必须 401：%d %s", rec.Code, rec.Body.String())
	}
	dry := doPublishReq(mux, http.MethodPost, target+"?dry_run=1", "", true)
	if dry.Code != http.StatusOK || !strings.Contains(dry.Body.String(), `"eligible":true`) {
		t.Fatalf("dry_run 应报告可同步：%d %s", dry.Code, dry.Body.String())
	}
	if got := mustJob(t, env, published.ID); got.SourceResyncState != "" || got.ContentVersion != published.ContentVersion {
		t.Fatalf("dry_run 不得动稿：%+v", got)
	}
	if _, afterDry := env.ft.counts(); afterDry != beforeUpdate {
		t.Fatalf("dry_run 不得 PUT FoodTalks")
	}

	rec := doPublishReq(mux, http.MethodPost, target, "", true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("补同步应受理：%d %s", rec.Code, rec.Body.String())
	}
	updated := mustJob(t, env, published.ID)
	assertSourceResyncResult(t, published, updated, latestBlocks)
	assertSingleFoodTalksUpdate(t, env.ft, beforeUpdate, published.FoodTalksNewsID)
	if updated.SourceResyncRequestedBy == "" {
		t.Fatalf("补同步必须记发起人：%+v", updated)
	}

	pending, _, err := env.store.createSeeded("site:awaiting-review", "https://mp.weixin.qq.com/s/awaiting-review", func(j *publishJob) {
		j.Stage = publishStageAwaitingReview
	})
	if err != nil {
		t.Fatal(err)
	}
	pendingTarget := fmt.Sprintf("/api/publish/jobs/%d/source-resync", pending.ID)
	if dry := doPublishReq(mux, http.MethodPost, pendingTarget+"?dry_run=1", "", true); !strings.Contains(dry.Body.String(), `"eligible":false`) ||
		!strings.Contains(dry.Body.String(), "只有已在 FoodTalks 发布的文章") {
		t.Fatalf("未发布稿 dry_run 应说明不能同步：%s", dry.Body.String())
	}
	if rec := doPublishReq(mux, http.MethodPost, pendingTarget, "", true); rec.Code != http.StatusConflict {
		t.Fatalf("未发布稿补同步必须拒绝：%d %s", rec.Code, rec.Body.String())
	}
}

// TestPublishNotificationExpiredCardIsTerminal 审核卡过了飞书 14 天可更新期限（230031）：
// 生命周期通知一次就停，记 expired，不重试、不补发新卡、不算监控告警；启动恢复也不重排；
// 改之前进了死信的旧数据读盘即迁为 expired（2026-10-14 生产 30 条）。
func TestPublishNotificationExpiredCardIsTerminal(t *testing.T) {
	env, published := publishedSourceResyncTestJob(t)
	job, _ := env.store.update(published.ID, func(current *publishJob) {
		current.ReviewCardMsgID = "om_old_card"
		enqueuePublishNotification(current, publishNotificationSourceResyncSucceeded)
	})
	calls, newCards, retried, texts := 0, 0, false, 0
	env.pipeline.updateCard = func(context.Context, string, map[string]any) error {
		calls++
		return fmt.Errorf("飞书更新卡片失败 code=230031 msg=Message has expired: %w", errFeishuMessageExpired)
	}
	env.pipeline.notifyCard = func(context.Context, map[string]any) error { newCards++; return nil }
	env.pipeline.notify = func(context.Context, string) error { texts++; return nil }
	env.pipeline.retryNotification = func(time.Duration, func()) { retried = true }

	env.pipeline.dispatchPublishNotification(job.ID)
	got := mustJob(t, env, job.ID)
	if got.NotificationState != publishNotificationStateExpired || got.NotificationError != "" ||
		calls != 1 || newCards != 0 || texts != 0 || retried {
		t.Fatalf("过期卡应一次就停：state=%q err=%q calls=%d new=%d text=%d retried=%v",
			got.NotificationState, got.NotificationError, calls, newCards, texts, retried)
	}
	var status publishMonitoringStatus
	updatePublishMonitoringNotifications(&status, got, time.Now(), 60)
	if status.NotificationDeadLetters != 0 || status.NotificationErrors != 0 || status.NotificationPending != 0 {
		t.Fatalf("过期卡不算告警：%+v", status)
	}
	env.pipeline.recoverPublishNotification(got)
	if calls != 1 || mustJob(t, env, job.ID).NotificationState != publishNotificationStateExpired {
		t.Fatalf("启动恢复不得重排过期通知：calls=%d", calls)
	}

	old := publishJob{NotificationKind: publishNotificationPublished, NotificationState: publishNotificationStateDeadLetter,
		NotificationError: "更新审核群 oc_x 的卡片失败：飞书更新卡片失败 code=230031 msg=Message has expired when updating message, ext="}
	migrateExpiredPublishNotification(&old)
	if old.NotificationState != publishNotificationStateExpired || old.NotificationError != "" {
		t.Fatalf("过期死信应迁为 expired：%+v", old)
	}
	other := publishJob{NotificationKind: publishNotificationPublished, NotificationState: publishNotificationStateDeadLetter, NotificationError: "飞书更新卡片接口不可达"}
	migrateExpiredPublishNotification(&other)
	if other.NotificationState != publishNotificationStateDeadLetter {
		t.Fatalf("别的死信不动：%+v", other)
	}
}

// #337：公众号「转载」功能转来的稿，旧判定把网站来源记成了发文的号。重新同步照页面记的原号改来源、
// 正文开头和文末跟着新判定排；有人在后台改过来源的不动。
func TestSourceResyncFollowsRepostCardSource(t *testing.T) {
	for _, edited := range []bool{false, true} {
		env, published := publishedSourceResyncTestJob(t)
		seedSourceResyncLiveNews(t, env, published, "")
		if edited {
			env.ft.mu.Lock()
			live := env.ft.newsByID[published.FoodTalksNewsID]
			live.SourceName, live.SourceID = "编辑手填的来源", 999
			env.ft.newsByID[published.FoodTalksNewsID] = live
			env.ft.mu.Unlock()
		}
		const reprintURL = "http://mp.weixin.qq.com/s?__biz=Mzk2NDM4OTQ1NA%3D%3D&mid=2247503223&idx=1&sn=86950d343e0859f03537768183e79478"
		env.pipeline.fetch = func(context.Context, string) (publishFetchedArticle, error) {
			return publishFetchedArticle{Title: published.Title, SourceName: published.SourceName, CanonicalURL: published.SourceURL,
				BlocksJSON: published.BlocksJSON, RepostedFrom: "轻食online", OriginalSourceURL: reprintURL}, nil
		}
		var renderedSource string
		env.pipeline.render = func(ctx context.Context, _ string, source string, _ string, _ bool) (publishRenderResult, error) {
			provenance, _ := ctx.Value(publishRenderProvenanceContextKey{}).(articleProvenance)
			renderedSource = provenance.Source + "|" + source
			return successfulPublishRenderResult(`<section><p>本文为轻食online原创</p></section>`, testPublishResolvedRule()), nil
		}
		updated, err := env.pipeline.requestSourceResync(published.ID, "ou_resync_editor", publishSourceResyncCardVersion(published))
		if err != nil || updated.SourceResyncState != publishSourceResyncSucceeded {
			t.Fatalf("重新同步应完成：%+v err=%v", updated, err)
		}
		if renderedSource != "轻食online|"+reprintURL {
			t.Fatalf("排版拿到的来源|原文 = %q", renderedSource)
		}
		if updated.SourceRepostedFrom != "轻食online" || updated.OriginalSourceURL != reprintURL {
			t.Fatalf("原号和原文要落库：%q %q", updated.SourceRepostedFrom, updated.OriginalSourceURL)
		}
		env.ft.mu.Lock()
		put := env.ft.lastPut
		env.ft.mu.Unlock()
		want := "轻食online"
		if edited {
			want = "编辑手填的来源"
		}
		if put.SourceType != 1 || put.SourceName != want {
			t.Fatalf("edited=%v：网站来源 = %d %q，应为转载 %q", edited, put.SourceType, put.SourceName, want)
		}
	}
}
