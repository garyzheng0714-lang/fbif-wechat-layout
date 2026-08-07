package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// 阻断级风险的强制发布（2026-08-07 用户裁定：阻断只当警示不当锁，
// 但普通「通过」仍不可用，强制发布必须显式点、留痕、上红标）。

func setJobRiskBlock(t *testing.T, env *publishTestEnv, jobID int64) publishJob {
	t.Helper()
	job, _, err := env.store.updateIf(jobID, func(publishJob) bool { return true }, func(j *publishJob) {
		j.RiskLevel = publishRiskBlock
	})
	if err != nil {
		t.Fatalf("置 block 风险: %v", err)
	}
	return job
}

func TestBlockCardOffersForcePublishButton(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	blocked := setJobRiskBlock(t, env, job.ID)
	raw, _ := json.Marshal(buildReviewPendingCard(blocked, ""))
	card := string(raw)
	if !strings.Contains(card, "强制发布") || !strings.Contains(card, feishuCardActionForceApprove) {
		t.Fatalf("block 卡片应提供强制发布按钮：%s", card)
	}
	if strings.Contains(card, "通过并发布") {
		t.Fatalf("block 卡片不得出现普通通过按钮：%s", card)
	}
	if !strings.Contains(card, "驳回") {
		t.Fatalf("block 卡片仍需保留驳回：%s", card)
	}
}

func TestForceApprovePublishesBlockJobWithAuditTrail(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FOODTALKS_FRONT_URL", "")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	setJobRiskBlock(t, env, job.ID)
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	accepted, err := env.pipeline.review(context.Background(), job.ID, publishReviewForceApprove, "ou_boss")
	if err != nil {
		t.Fatalf("force_approve: %v", err)
	}
	if !accepted.ReviewForced {
		t.Fatalf("强制发布必须落 review_forced 留痕")
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStagePublished {
		t.Fatalf("强制发布后 stage = %q，应为 published", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 1 || env.ft.updateStatuses[0] != foodTalksPublishStatusPublished {
		t.Fatalf("应恰一次 publishStatus=1 的 UpdateNews，实际 %v", env.ft.updateStatuses)
	}
	doneCard, _ := json.Marshal(buildReviewDoneCard(got))
	if !strings.Contains(string(doneCard), "强制发布") {
		t.Fatalf("终态卡必须标注强制发布：%s", doneCard)
	}
}

func TestPlainApproveStillDeniedOnBlock(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	setJobRiskBlock(t, env, job.ID)
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, ""); err == nil ||
		!strings.Contains(err.Error(), "强制发布") {
		t.Fatalf("block 上普通 approve 仍应拒绝并指路强制发布，实际 err=%v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview || got.ReviewForced {
		t.Fatalf("普通 approve 不得改状态（stage=%q forced=%v）", got.Stage, got.ReviewForced)
	}
}

func TestForceApproveDeniedWhenRiskUnknown(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, _, err := env.store.updateIf(job.ID, func(publishJob) bool { return true }, func(j *publishJob) {
		j.RiskLevel = "" // 检查未完成：强制发布也不放行（fail closed 不变）
	}); err != nil {
		t.Fatalf("清空风险: %v", err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewForceApprove, "ou_boss"); err == nil {
		t.Fatalf("风险未知时强制发布也必须拒绝")
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("被拒后应停在 awaiting_review，实际 %q", got.Stage)
	}
}

func TestForceApproveViaCardCallback(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_boss")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	blocked := setJobRiskBlock(t, env, job.ID)
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	raw := json.RawMessage(fmt.Sprintf(`{"action":%q,"job_id":%d,"review_version":%q}`,
		feishuCardActionForceApprove, job.ID, publishReviewCardVersion(blocked)))
	out := env.pipeline.reviewCardAction(context.Background(), "ou_boss", "", raw)
	if out.toastType != "success" || !strings.Contains(out.toastContent, "强制发布") {
		t.Fatalf("卡片强制发布应受理并明说强制，实际 %+v", out)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStagePublished || !got.ReviewForced || got.ReviewedBy != "ou_boss" {
		t.Fatalf("强制发布应完成并留痕（stage=%q forced=%v by=%q）", got.Stage, got.ReviewForced, got.ReviewedBy)
	}
}

// 撤回重发（card-refresh ?resend=1）：旧卡撤回、message_id 清空、发出全新卡。
func TestRefreshReviewCardResendRecallsAndSendsNew(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, _, err := env.store.updateIf(job.ID, func(publishJob) bool { return true }, func(j *publishJob) {
		j.ReviewCardMsgID = "om_old_card"
	}); err != nil {
		t.Fatalf("预置旧卡 id: %v", err)
	}
	var recalled []string
	env.pipeline.recallCard = func(_ context.Context, messageID string) error {
		recalled = append(recalled, messageID)
		return nil
	}
	var sentCards int
	env.pipeline.notifyCardWithID = func(_ context.Context, _ map[string]any) (string, error) {
		sentCards++
		return "om_new_card", nil
	}
	if _, err := env.pipeline.refreshReviewCard(context.Background(), job.ID, true); err != nil {
		t.Fatalf("resend 刷新: %v", err)
	}
	if len(recalled) != 1 || recalled[0] != "om_old_card" {
		t.Fatalf("应撤回旧卡 om_old_card，实际 %v", recalled)
	}
	if sentCards != 1 {
		t.Fatalf("应发出 1 张新卡，实际 %d", sentCards)
	}
	got, _ := env.store.byID(job.ID)
	if got.ReviewCardMsgID != "om_new_card" {
		t.Fatalf("新卡 message_id 应登记为 om_new_card，实际 %q", got.ReviewCardMsgID)
	}
}
