package main

import (
	"context"
	"strings"
	"testing"
)

func TestRepairPublishedWechatTrailerRewritesLiveArticle(t *testing.T) {
	env := newPublishTestEnv(t)
	env.ft.forbidPublish = false
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_reviewer"); err != nil {
		t.Fatal(err)
	}
	published, err := env.store.byID(job.ID)
	if err != nil || published.Stage != publishStagePublished {
		t.Fatalf("应已发布：%+v err=%v", published, err)
	}
	leaked := published.ContentHTML + `<p>* 本文为FBIF原创，欢迎转发朋友圈；</p><p>/ 更多文章 /</p><p>预览时标签不可点</p>`
	if _, _, err := env.store.updateIf(job.ID, func(publishJob) bool { return true }, func(j *publishJob) {
		j.ContentHTML = leaked
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		if !isRepost {
			t.Fatal("已发布稿修复也必须按切尾渲染")
		}
		return successfulPublishRenderResult(testPublishRenderedHTML, testPublishResolvedRule()), nil
	}

	beforeCreate, beforeUpdate := env.ft.counts()
	got, err := env.pipeline.repairPublishedWechatTrailer(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != publishStagePublished {
		t.Fatalf("修复不得改变生命周期，实际 %s", got.Stage)
	}
	if got.ContentVersion != published.ContentVersion {
		t.Fatalf("修复不得改 content_version：%d → %d", published.ContentVersion, got.ContentVersion)
	}
	if leak := publishHTMLWechatTrailerLeak(got.ContentHTML); leak != "" {
		t.Fatalf("本地正文仍含导流尾 %s", leak)
	}
	_, afterUpdate := env.ft.counts()
	if afterUpdate-beforeUpdate != 1 {
		t.Fatalf("应 PUT 覆盖线上正文一次，create 基线=%d update 增量=%d", beforeCreate, afterUpdate-beforeUpdate)
	}

	report, err := env.pipeline.repairPublishedWechatTrailerLeaks(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Matched != 0 {
		t.Fatalf("切干净后批量修复不应再命中：%+v", report)
	}
}

func TestRepairPublishedWechatTrailerRejectsUnstrippedRender(t *testing.T) {
	env := newPublishTestEnv(t)
	env.ft.forbidPublish = false
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_reviewer"); err != nil {
		t.Fatal(err)
	}
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		leaked := testPublishRenderedHTML + `<p>欢迎转发朋友圈</p>`
		return successfulPublishRenderResult(leaked, testPublishResolvedRule()), nil
	}
	if _, err := env.pipeline.repairPublishedWechatTrailer(context.Background(), job.ID); err == nil || !strings.Contains(err.Error(), "导流尾") {
		t.Fatalf("重排仍带导流尾必须 fail closed：%v", err)
	}
}
