package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestExtractArticleDigest(t *testing.T) {
	raw := `<meta property="og:description" content="og 摘要" />` +
		`<script>var msg_desc = htmlDecode("FBIF时隔5年再探T9茶：年均增长50%，中国茶叶品牌的\"奢侈品\"打法。");</script>`
	if got := extractArticleDigest(raw); got != `FBIF时隔5年再探T9茶：年均增长50%，中国茶叶品牌的"奢侈品"打法。` {
		t.Fatalf("应取 msg_desc 原值并还原转义，实际 %q", got)
	}
	if got := extractArticleDigest(`<meta property="og:description" content="只有 og &amp; 摘要" />`); got != "只有 og & 摘要" {
		t.Fatalf("无 msg_desc 时回退 og:description，实际 %q", got)
	}
	// 图片消息（item_show_type=8）没有 msg_desc，og:description 是带 \x0a 的全文：不当摘要。
	full := `<meta property="og:description" content="7月10日，沃尔玛中国宣布推出新系列。\x0a\x0a第二段正文" />`
	if got := extractArticleDigest(full); got != "" {
		t.Fatalf("含换行的全文不是摘要，应为空，实际 %q", got)
	}
	long := `<script>var msg_desc = htmlDecode("` + strings.Repeat("长", 121) + `");</script>`
	if got := extractArticleDigest(long); got != "" {
		t.Fatalf("超过 120 字不是公众号摘要，应为空，实际 %d 字", len([]rune(got)))
	}
	if got := extractArticleDigest(`<p>无摘要</p>`); got != "" {
		t.Fatalf("没有摘要应为空，实际 %q", got)
	}
}

// TestPublishUsesWechatCoverAndDigest 与公众号保持一致（2026-10-07 用户裁定）：公众号原封面
// 与摘要直接成为网站封面/摘要，AI 改选的封面与自写摘要都不得覆盖；封面已定时不再发图给模型。
func TestPublishUsesWechatCoverAndDigest(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: "测试标题", SourceName: "测试公众号", CanonicalURL: articleURL,
			BlocksJSON: testPublishBlocksJSON, CoverURL: "https://mmbiz.qpic.cn/wechat-cover.jpg", Digest: "公众号后台填的摘要。"}, nil
	}
	gotReq := newAIStub(t, func() (int, string) {
		return http.StatusOK, fmt.Sprintf(`{"cover_url":%q,"summary":"AI 自己写的摘要。","tags":["食品"],"author":"","source_name":"","notes":"说明"}`,
			ossURLFor("https://mmbiz.qpic.cn/b.jpg"))
	})
	env.pipeline.enrich = aiEnrichArticle
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("stage=%q error=%q", got.Stage, got.Error)
	}
	if got.CoverURL != ossURLFor("https://mmbiz.qpic.cn/wechat-cover.jpg") {
		t.Fatalf("封面应为公众号原封面（AI 不得改选），实际 %q", got.CoverURL)
	}
	if got.Summary != "公众号后台填的摘要。" {
		t.Fatalf("摘要应与公众号一致，实际 %q", got.Summary)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "食品" {
		t.Fatalf("标签仍由 AI 预填：%v", got.Tags)
	}
	for _, c := range gotReq.Input[1].Content {
		if c.Type == "input_image" {
			t.Fatal("封面已沿用公众号原封面时不应再发图给模型")
		}
	}
}

func publishedJobForMetaSync(t *testing.T, env *publishTestEnv, cover, summary string) publishJob {
	t.Helper()
	submitted, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	job, err := env.store.update(submitted.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.CoverURL = cover
		j.Summary = summary
		j.ContentHTML = testPublishRenderedHTML + `<img src="https://mmbiz.qpic.cn/bobo-card.webp">`
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.FoodTalksNewsID == 0 {
		t.Fatal("测试任务应已有网站稿 id")
	}
	env.ft.forbidPublish = false
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: job.Title, BlocksJSON: testPublishBlocksJSON,
			CoverURL: "https://mmbiz.qpic.cn/wechat-cover.jpg", Digest: "公众号摘要。"}, nil
	}
	return job
}

// TestSyncWechatMetaReplacesFooterCoverAndAISummary #263 修复：页脚名片封面与流水线写的
// AI 摘要都换成公众号原值；正文、标题、标签按网站现稿原样写回。
func TestSyncWechatMetaReplacesFooterCoverAndAISummary(t *testing.T) {
	env := newPublishTestEnv(t)
	job := publishedJobForMetaSync(t, env, "https://mmbiz.qpic.cn/bobo-card.webp", "AI 摘要。")
	env.ft.mu.Lock()
	env.ft.newsByID = map[int64]foodTalksNewsDetail{job.FoodTalksNewsID: {
		ID: job.FoodTalksNewsID, Title: "编辑改过的标题", CoverImg: "https://mmbiz.qpic.cn/bobo-card.webp", Summary: "AI 摘要。",
		Content: "<p>网站现稿正文</p>", PublishStatus: foodTalksPublishStatusPublished, Language: "ZH",
		NewsTags: []foodTalksNewsTag{{TagID: 1}},
	}}
	env.ft.mu.Unlock()

	res, err := env.pipeline.syncPublishedWechatMeta(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !res.CoverChanged || !res.SummaryChanged {
		t.Fatalf("封面和摘要都应替换：%+v", res)
	}
	env.ft.mu.Lock()
	put := env.ft.lastPut
	env.ft.mu.Unlock()
	if put.CoverImg != ossURLFor("https://mmbiz.qpic.cn/wechat-cover.jpg") || put.Summary != "公众号摘要。" {
		t.Fatalf("写回的封面/摘要不对：cover=%q summary=%q", put.CoverImg, put.Summary)
	}
	if put.Content != "<p>网站现稿正文</p>" || put.Title != "编辑改过的标题" || len(put.NewsTags) != 1 || put.PublishStatus != foodTalksPublishStatusPublished {
		t.Fatalf("其余字段必须按网站现稿原样写回：%+v", put)
	}
	got, _ := env.store.byID(job.ID)
	if got.CoverURL != put.CoverImg || got.Summary != "公众号摘要。" || got.SourceDigest != "公众号摘要。" || got.Stage != publishStagePublished {
		t.Fatalf("本地任务应同步更新：cover=%q summary=%q digest=%q stage=%q", got.CoverURL, got.Summary, got.SourceDigest, got.Stage)
	}
}

// TestSyncWechatMetaKeepsManualEdits 编辑在 CMS 手工改过的封面/摘要不覆盖，也不发 PUT。
func TestSyncWechatMetaKeepsManualEdits(t *testing.T) {
	env := newPublishTestEnv(t)
	job := publishedJobForMetaSync(t, env, ossURLFor("https://mmbiz.qpic.cn/a.jpg"), "AI 摘要。")
	env.ft.mu.Lock()
	env.ft.newsByID = map[int64]foodTalksNewsDetail{job.FoodTalksNewsID: {
		ID: job.FoodTalksNewsID, Title: job.Title, CoverImg: "https://oss.example.com/manual.jpg", Summary: "编辑手改的摘要",
		Content: "<p>正文</p>", PublishStatus: foodTalksPublishStatusPublished,
	}}
	_, updatesBefore := len(env.ft.createStatuses), len(env.ft.updateStatuses)
	env.ft.mu.Unlock()

	res, err := env.pipeline.syncPublishedWechatMeta(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.CoverChanged || res.SummaryChanged || res.CoverKept == "" || res.SummaryKept == "" {
		t.Fatalf("人工修改必须保留并说明原因：%+v", res)
	}
	if _, updates := env.ft.counts(); updates != updatesBefore {
		t.Fatalf("无字段可替换时不得 PUT，PUT 次数 %d→%d", updatesBefore, updates)
	}
}

func TestSyncWechatMetaHTTPRequiresAdmin(t *testing.T) {
	env := newPublishTestEnv(t)
	job := publishedJobForMetaSync(t, env, "https://mmbiz.qpic.cn/bobo-card.webp", "AI 摘要。")
	env.ft.mu.Lock()
	env.ft.newsByID = map[int64]foodTalksNewsDetail{job.FoodTalksNewsID: {
		ID: job.FoodTalksNewsID, Title: job.Title, CoverImg: "https://mmbiz.qpic.cn/bobo-card.webp", Summary: "AI 摘要。", Content: "<p>正文</p>", PublishStatus: 1,
	}}
	env.ft.mu.Unlock()
	mux := newPublishTestMux(t, env)
	path := fmt.Sprintf("/api/publish/jobs/%d/sync-wechat-meta", job.ID)
	if rec := doPublishReq(mux, http.MethodPost, path, "", false); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("无管理员凭证应拒绝，实际 %d", rec.Code)
	}
	rec := doPublishReq(mux, http.MethodPost, path, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("管理员调用应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Result wechatMetaSyncResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Result.CoverChanged || !strings.Contains(body.Result.Summary, "公众号摘要") {
		t.Fatalf("响应应报告替换结果：%s", rec.Body.String())
	}
}
