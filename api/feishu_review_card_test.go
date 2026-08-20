package main

// feishu_review_card_test.go — 审核卡信息完整性守护。
//
// 背景：终态卡曾经只渲染 reviewCardInfoBlock（来源/作者/时间/任务号两行），
// 审核通过后封面、标题、标签、摘要和原文链接全部消失。审核卡在群里长期留存，
// 是事后回看「到底发了什么」的唯一入口，退化成一个任务号等于信息归零。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func reviewCardTestJob(stage string) publishJob {
	return publishJob{
		ID: 72, Stage: stage,
		Title:      "首年销量破亿，冲进沃尔玛前五！这个女性能量饮料，靠青汁粉走红了",
		SourceName: "轻食online", Author: "FBIF食品饮料创新",
		Summary:           "靠青汁粉走红的女性能量饮料，凭什么首年销售额破亿冲进沃尔玛前五？",
		Tags:              []string{"能量饮料", "女性消费"},
		SourceURL:         "https://mp.weixin.qq.com/s?__biz=MzA5&mid=1&idx=1&sn=abc",
		ArticleURL:        "https://test.foodtalks.cn/news/7318",
		ReviewCoverImgKey: "img_v3_cover_key",
		RiskLevel:         publishRiskWarning,
		UpdatedAt:         "2026-08-06T00:57:00Z",
	}
}

func cardJSON(t *testing.T, card map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("卡片无法序列化: %v", err)
	}
	return string(raw)
}

// TestReviewCardsKeepArticleIdentity 待审核、已发布、已驳回和异常四种卡片都必须
// 让人一眼认出是哪篇文章：封面、标题、摘要、原文链接缺一不可。
func TestReviewCardsKeepArticleIdentity(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	cards := map[string]map[string]any{
		"待审核":  buildReviewPendingCard(reviewCardTestJob(publishStageAwaitingReview), job.ReviewCoverImgKey),
		"已发布":  buildReviewDoneCard(job),
		"已驳回":  buildReviewDoneCard(reviewCardTestJob(publishStageRejected)),
		"发布失败": buildReviewIssueCard(job, false),
	}
	for name, card := range cards {
		body := cardJSON(t, card)
		for _, want := range []struct {
			what     string
			fragment string
		}{
			{"封面 img_key", job.ReviewCoverImgKey},
			{"标题", "首年销量破亿"},
			{"摘要", "凭什么首年销售额破亿"},
			{"标签", "能量饮料"},
			{"公众号原文链接", "mp.weixin.qq.com"},
		} {
			if !strings.Contains(body, want.fragment) {
				t.Errorf("%s卡缺少%s（片段 %q）", name, want.what, want.fragment)
			}
		}
	}
}

// TestReviewDoneCardWithoutCoverStaysSingleColumn 旧任务没存过 img_key 时，
// 终态卡必须退化成无封面单列，而不是留一个空图位或直接报错。
func TestReviewDoneCardWithoutCoverStaysSingleColumn(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.ReviewCoverImgKey = ""
	body := cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(body, `"tag":"img"`) {
		t.Fatalf("无封面任务不应渲染 img 元素: %s", body)
	}
	if !strings.Contains(body, "首年销量破亿") {
		t.Fatalf("无封面时标题仍必须保留: %s", body)
	}
}

// TestReviewDoneCardKeepsPublishedArticleLink 已发布卡必须给出站内正式链接，
// 这是审核人核对发布结果的落点。
func TestReviewDoneCardKeepsPublishedArticleLink(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	body := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(body, job.ArticleURL) {
		t.Fatalf("已发布卡缺少站内文章链接 %q: %s", job.ArticleURL, body)
	}
}

func TestReviewBatchCardKeepsOneResponsiveCardAndPerArticleActions(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	now := time.Now().UTC()
	pending := reviewCardTestJob(publishStageAwaitingReview)
	pending.ID = 118
	pending.CreatedAt = "2026-08-20T01:00:00Z"
	pending.ContentHash = "pending"
	pending.ContentVersion = 1
	pending.RiskLevel = publishRiskPass
	published := reviewCardTestJob(publishStagePublished)
	published.ID = 117
	published.CreatedAt = pending.CreatedAt
	published.PublishedAt = now.Add(-time.Hour).Format(time.RFC3339)
	published.UnpublishPolicySet = true
	published.UnpublishWindowHours = 8
	published.UnpublishWindowUntil = now.Add(time.Hour).Format(time.RFC3339)

	body := cardJSON(t, buildReviewBatchCard([]publishJob{published, pending}))
	for _, want := range []string{
		"FoodTalks 内容审核", "8月20日 · 2篇文章 · 待审核1篇 · 已发布1篇",
		`"flex_mode":"flow"`, `"scale_type":"fit_horizontal"`, "#117", "#118",
		"查看原文", `"action":"approve"`, `"action":"unpublish"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("聚合卡缺少 %q: %s", want, body)
		}
	}
	if strings.Count(body, `"schema":"2.0"`) != 1 {
		t.Fatalf("同批文章必须共用一张卡: %s", body)
	}
}

// TestPublishCardRefreshReissuesCurrentCard 卡片改版后要能把历史任务的卡重出一遍：
// 终态任务可刷新（走已配置的投递通道，有 message_id 的群原位更新，不刷屏），
// 尚未产生审核卡的阶段必须明确拒绝，而不是发一张语义不明的卡。
func TestPublishCardRefreshReissuesCurrentCard(t *testing.T) {
	env := newPublishTestEnv(t)
	env.ft.forbidPublish = false
	mux := newPublishTestMux(t, env)

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_reviewer"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := len(env.notified)

	rec := doPublishReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/card-refresh", job.ID), "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("已发布任务应可刷新卡片，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if len(env.notified) <= before {
		t.Fatalf("刷新应重新投递一次卡片（此环境降级为文本），notified=%d→%d", before, len(env.notified))
	}

	missing := doPublishReq(mux, http.MethodPost, "/api/publish/jobs/999999/card-refresh", "", true)
	if missing.Code == http.StatusOK {
		t.Fatalf("不存在的任务不应返回成功: body=%s", missing.Body.String())
	}
}

// TestPublishCardRefreshRequiresAdmin 刷新会往审核群发消息，必须在管理员门后。
func TestPublishCardRefreshRequiresAdmin(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/jobs/1/card-refresh", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无凭证应 401，实际 %d", rec.Code)
	}
	if rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/jobs/1/card-refresh", ""); rec.Code == http.StatusOK {
		t.Fatalf("同步服务凭证不得刷新卡片，实际 %d", rec.Code)
	}
}
