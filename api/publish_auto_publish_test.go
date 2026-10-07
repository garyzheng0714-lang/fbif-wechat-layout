package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func enableAutoPublish(t *testing.T, env *publishTestEnv) {
	t.Helper()
	store := newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "delivery.json"))
	if err := store.write(publishDeliverySettings{
		AppProfileID: "default",
		ChatIDs:      []string{"oc_review"},
		AutoPublish:  true,
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.deliverySettings = store
}

func TestAutoPublishPublishesWithoutHumanApprove(t *testing.T) {
	env := newPublishTestEnv(t)
	enableAutoPublish(t, env)
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != publishStagePublished {
		t.Fatalf("自动发布应落到 published，实际 %s error=%q", got.Stage, got.Error)
	}
	if got.ReviewedBy != publishAutoPublishActor || got.ReviewAction != publishReviewApprove {
		t.Fatalf("应留下系统自动发布授权：reviewed_by=%q action=%q", got.ReviewedBy, got.ReviewAction)
	}
	_, updateN := env.ft.counts()
	if updateN == 0 || env.ft.updateStatuses[len(env.ft.updateStatuses)-1] != foodTalksPublishStatusPublished {
		t.Fatalf("应把 publishStatus=1 写进 FoodTalks，statuses=%v", env.ft.updateStatuses)
	}
	card := buildReviewDoneCard(got)
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "自动发布了本文") {
		t.Fatalf("终态卡应标明自动发布：%s", raw)
	}
	if !strings.Contains(string(raw), "下线文章") {
		t.Fatalf("终态卡应保留下线按钮：%s", raw)
	}
}

func TestAutoPublishSkipsBlockRisk(t *testing.T) {
	env := newPublishTestEnv(t)
	enableAutoPublish(t, env)
	env.pipeline.assessRisk = func(_ context.Context, _ publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{
			Level:    publishRiskBlock,
			Headline: "有已证实的实质性错误",
			Items: []publishRiskItem{{
				Category:  publishRiskCategoryFactual,
				Level:     publishRiskBlock,
				Certainty: publishRiskCertaintyConfirmed,
				Evidence:  "原文写了与公开财报冲突的营收",
				Reason:    "营收数字与可核验来源不一致",
			}},
		}, nil
	}

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("阻断级风险应停在待审核等人强制发布，实际 %s", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("阻断级不得自动翻公开，UpdateNews=%d", updateN)
	}
}

func TestAutoPublishSkipsDuplicateTitle(t *testing.T) {
	env := newPublishTestEnv(t)
	enableAutoPublish(t, env)
	env.ft.mu.Lock()
	env.ft.searchRecords = []foodTalksNewsListItem{{
		ID: 88001, Title: "测试标题", PublishStatus: foodTalksPublishStatusPublished, SourceName: "手工后台",
	}}
	env.ft.mu.Unlock()

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("同题已发布稿应停在待审核，实际 %s error=%q", got.Stage, got.Error)
	}
	if got.SiteDuplicateNewsID != 88001 {
		t.Fatalf("应记下同题稿 id，实际 %d", got.SiteDuplicateNewsID)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("同题不得自动翻公开，UpdateNews=%d", updateN)
	}
}

func TestAutoPublishOffStillStopsForReview(t *testing.T) {
	env := newPublishTestEnv(t)
	store := newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "delivery.json"))
	if err := store.write(publishDeliverySettings{
		AppProfileID: "default",
		ChatIDs:      []string{"oc_review"},
		AutoPublish:  false,
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.deliverySettings = store

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("开关关闭应停在待审核，实际 %s", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("开关关闭不得翻公开，UpdateNews=%d", updateN)
	}
}

// TestAutoPublishSkipsRestagedLegacyReview 2026-10-08 事故回归：早已在审核群挂卡的老待审稿，
// 回源重排后再次暂存，即使自动发布已开也必须停在待审核，不得被系统公开。
func TestAutoPublishSkipsRestagedLegacyReview(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.store.update(job.ID, func(j *publishJob) {
		j.ReviewCardMsgIDs = map[string]string{"oc_review": "om_legacy_card"}
		j.NotificationSentAt = "2026-07-17T08:00:00Z"
	}); err != nil {
		t.Fatal(err)
	}
	enableAutoPublish(t, env)
	env.ft.mu.Lock()
	env.ft.forbidPublish = true // 红线守卫：任何 publishStatus=1 都算失败
	env.ft.mu.Unlock()

	if _, err := env.pipeline.refetchLatestWithRulePack(context.Background(), job.ID, ""); err != nil {
		t.Fatalf("refetch: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview || got.ReviewedBy != "" || got.AutoPublishEligible {
		t.Fatalf("老待审稿重排后必须停在待审核：stage=%s reviewed_by=%q eligible=%v", got.Stage, got.ReviewedBy, got.AutoPublishEligible)
	}
}
