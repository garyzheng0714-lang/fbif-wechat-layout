package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// 群成员审核模式（2026-08-06 用户裁定：只要在审核群里，就有权限点卡片）。
// 语义依据：只有群成员能看到并点击群消息里的按钮，事件 context.open_chat_id 由
// 飞书服务端注入。模式必须显式开启；开启后动作只认来自配置的审核群，转发出去
// 的卡片（群 id 不同）与空群 id（私聊/未知宿主）一律拒绝。

func TestFeishuReviewChatMemberModeAllowsGroupAction(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "")
	t.Setenv("FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS", "1")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_review_chat")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	current, _ := env.store.byID(job.ID)
	raw := json.RawMessage(fmt.Sprintf(`{"action":"reject","job_id":%d,"review_version":%q}`,
		job.ID, publishReviewCardVersion(current)))
	out := env.pipeline.reviewCardAction(context.Background(), "ou_group_member", "oc_review_chat", raw)
	if out.toastType != "success" {
		t.Fatalf("群内成员操作应放行，实际 %+v", out)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageRejected || got.ReviewedBy != "ou_group_member" {
		t.Fatalf("审核应生效并留痕（stage=%q reviewed_by=%q）", got.Stage, got.ReviewedBy)
	}
}

func TestFeishuReviewChatMemberModeRejectsOtherChats(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "")
	t.Setenv("FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS", "1")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_review_chat")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	raw := json.RawMessage(fmt.Sprintf(`{"action":"approve","job_id":%d}`, job.ID))
	for name, chatID := range map[string]string{
		"卡片被转发到别的群": "oc_other_chat",
		"事件缺群上下文":   "",
	} {
		out := env.pipeline.reviewCardAction(context.Background(), "ou_group_member", chatID, raw)
		if out.toastType != "error" || !strings.Contains(out.toastContent, "权限") {
			t.Fatalf("%s：应拒绝并提示权限，实际 %+v", name, out)
		}
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("被拒动作不得改状态，实际 %q", got.Stage)
	}
}

func TestFeishuReviewChatMemberModeDefaultOff(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "")
	t.Setenv("FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS", "")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_review_chat")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	raw := json.RawMessage(fmt.Sprintf(`{"action":"approve","job_id":%d}`, job.ID))
	out := env.pipeline.reviewCardAction(context.Background(), "ou_group_member", "oc_review_chat", raw)
	if out.toastType != "error" || !strings.Contains(out.toastContent, "权限") {
		t.Fatalf("模式未开启时必须维持白名单行为（拒绝），实际 %+v", out)
	}
}

func TestFeishuReviewerAuthConfiguredSemantics(t *testing.T) {
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "")
	t.Setenv("FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS", "")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "")
	if feishuReviewerAuthConfigured() {
		t.Fatalf("无白名单且无群成员模式时应视为未配置")
	}
	t.Setenv("FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS", "1")
	if feishuReviewerAuthConfigured() {
		t.Fatalf("群成员模式开启但没有审核群配置，仍应视为未配置（等于零权限来源）")
	}
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_review_chat")
	if !feishuReviewerAuthConfigured() {
		t.Fatalf("群成员模式 + 审核群齐备应视为已配置")
	}
	t.Setenv("FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS", "")
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	if !feishuReviewerAuthConfigured() {
		t.Fatalf("静态白名单应继续视为已配置")
	}
}
