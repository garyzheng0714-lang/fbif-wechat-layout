package main

// feishu_card_callback_test.go — 飞书卡片审核交互测试（施工图 M4）。
// 复用 publish_pipeline_test.go 的 publishTestEnv（FoodTalks httptest 桩 + fake 注入），
// 不触真实飞书 / FoodTalks。
//
// 🚨 锁死两条红线：
//   1. 卡片回调与 HTTP review 共用同一个 review 核心——仅 awaiting_review 可操作，
//      approve 仍是翻公开的唯一路径（ftStub forbidPublish 守卫全程在场）；
//   2. 卡片 JSON 遵守 cardkit 红线：无 emoji、必带 confirm 二次确认、schema 2.0
//      不用 action/note 容器。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testFeishuVerificationToken = "vt-secret"

func newCardCallbackMux(t *testing.T, env *publishTestEnv) *http.ServeMux {
	t.Helper()
	t.Setenv("FEISHU_VERIFICATION_TOKEN", testFeishuVerificationToken)
	mux := http.NewServeMux()
	registerFeishuCardCallbackRoute(mux, env.pipeline)
	return mux
}

func postCardCallback(mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/feishu/card-callback", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// cardActionBody card.action.trigger 事件体（形态照 cardkit 规范 04 §4.1：
// verification token 在 header.token，审核人在 event.operator.open_id）。
func cardActionBody(token, action string, jobID int64) string {
	return fmt.Sprintf(`{
		"schema": "2.0",
		"header": {"event_type": "card.action.trigger", "token": %q},
		"event": {
			"operator": {"open_id": "ou_reviewer_1"},
			"token": "c-cardtoken",
			"action": {"tag": "button", "value": {"action": %q, "job_id": %d}},
			"context": {"open_chat_id": "oc_review_chat"}
		}
	}`, token, action, jobID)
}

// containsEmoji 粗粒度 emoji 扫描：卡片文案只允许中文/ASCII/常规标点（cardkit 红线）。
func containsEmoji(s string) bool {
	for _, r := range s {
		if r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF) || (r >= 0xFE00 && r <= 0xFE0F) {
			return true
		}
	}
	return false
}

// TestFeishuCallbackChallenge url_verification 握手原样回 challenge——开放平台配置
// 回调地址的第一步，这一步不通过整个交互链路都上不了线。token 错误必须 401：
// challenge 阶段同样不给伪造来源留门。
func TestFeishuCallbackChallenge(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newCardCallbackMux(t, env)
	rec := postCardCallback(mux, fmt.Sprintf(
		`{"type":"url_verification","challenge":"ch-123","token":%q}`, testFeishuVerificationToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge 应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Challenge != "ch-123" {
		t.Fatalf("challenge 应原样回传，实际 body=%s err=%v", rec.Body.String(), err)
	}
	if rec := postCardCallback(mux, `{"type":"url_verification","challenge":"ch-123","token":"wrong"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token 错误的 challenge 应 401，实际 %d", rec.Code)
	}
}

// TestFeishuCallbackTokenMismatch401 verification token 是回调端点唯一的来源门
// （无 ADMIN_PASSWORD）：不匹配必须 401，且不得触发任何审核动作与 FoodTalks 调用。
func TestFeishuCallbackTokenMismatch401(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newCardCallbackMux(t, env)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	rec := postCardCallback(mux, cardActionBody("wrong-token", feishuCardActionApprove, job.ID))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("token 不匹配应 401，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("401 请求不得改任务状态，实际 %q", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("401 请求触发了 UpdateNews（%d 次）——回调门失效", updateN)
	}
}

// TestFeishuCallbackApprovePublishes 卡片「通过并发布」走与 HTTP 端点同一个 review
// 核心：job 经 publicizing 落 published、UpdateNews 收 publishStatus=1、审核人
// operator.open_id 落 reviewed_by/reviewed_at，3 秒响应带 success toast + 绿终态卡
// （含 person 留痕，群内 update_multi 同步刷新）。
func TestFeishuCallbackApprovePublishes(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newCardCallbackMux(t, env)
	t.Setenv("FOODTALKS_FRONT_URL", "")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false // 本场景 approve 合法，解除红线守卫
	env.ft.mu.Unlock()

	rec := postCardCallback(mux, cardActionBody(testFeishuVerificationToken, feishuCardActionApprove, job.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("approve 回调应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStagePublished {
		t.Fatalf("stage = %q（error=%q），应为 published", got.Stage, got.Error)
	}
	if got.ReviewedBy != "ou_reviewer_1" || got.ReviewedAt == "" {
		t.Fatalf("审核留痕未落库：reviewed_by=%q reviewed_at=%q", got.ReviewedBy, got.ReviewedAt)
	}
	if _, updateN := env.ft.counts(); updateN != 1 || env.ft.updateStatuses[0] != foodTalksPublishStatusPublished {
		t.Fatalf("UpdateNews 应恰一次 publishStatus=1，实际 statuses=%v", env.ft.updateStatuses)
	}
	body := rec.Body.String()
	var resp struct {
		Toast struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"toast"`
		Card struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"card"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode 回调响应: %v", err)
	}
	if resp.Toast.Type != "success" || resp.Toast.Content != "已发布" {
		t.Fatalf("toast 不符：%+v", resp.Toast)
	}
	if resp.Card.Type != "raw" || resp.Card.Data["schema"] != "2.0" {
		t.Fatalf("响应应带 raw 终态卡：%+v", resp.Card)
	}
	header, _ := resp.Card.Data["header"].(map[string]any)
	if header["template"] != "green" {
		t.Fatalf("approve 终态卡 header 应为 green，实际 %v", header["template"])
	}
	for _, want := range []string{"已发布", "ou_reviewer_1", "capsule", "查看文章", got.ArticleURL} {
		if !strings.Contains(body, want) {
			t.Fatalf("终态卡应包含 %q，实际响应：%s", want, body)
		}
	}
	if containsEmoji(body) {
		t.Fatalf("终态卡含 emoji（cardkit 红线）：%s", body)
	}
}

// TestFeishuCallbackRejectTerminates 卡片「驳回」置终态 rejected：不触发任何 FoodTalks
// 请求（forbidPublish 守卫全程在场），响应带红终态卡 + 留痕。
func TestFeishuCallbackRejectTerminates(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newCardCallbackMux(t, env)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	rec := postCardCallback(mux, cardActionBody(testFeishuVerificationToken, feishuCardActionReject, job.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("reject 回调应 200，实际 %d", rec.Code)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageRejected || got.ReviewedBy != "ou_reviewer_1" {
		t.Fatalf("应落 rejected + 审核留痕，实际 stage=%q reviewed_by=%q", got.Stage, got.ReviewedBy)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("reject 不应触发 UpdateNews，实际 %d 次", updateN)
	}
	body := rec.Body.String()
	for _, want := range []string{"已驳回", `"template":"red"`, "驳回了本文", "ou_reviewer_1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("驳回终态卡应包含 %q，实际响应：%s", want, body)
		}
	}
}

// TestFeishuCallbackRepeatedClickNoOp 防重复点击：任务已进终态后再点旧卡按钮，
// 回「已被处理」+ 当前终态卡，不改状态、不重复触发翻公开——update_multi 会把旧卡
// 刷成终态，第二个人手快点了也不会二次发布。
func TestFeishuCallbackRepeatedClickNoOp(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newCardCallbackMux(t, env)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	if rec := postCardCallback(mux, cardActionBody(testFeishuVerificationToken, feishuCardActionApprove, job.ID)); rec.Code != http.StatusOK {
		t.Fatalf("首次 approve 应 200，实际 %d", rec.Code)
	}
	// 二次点击（approve 与 reject 各试一次）：状态不变、UpdateNews 不再发。
	for _, action := range []string{feishuCardActionApprove, feishuCardActionReject} {
		rec := postCardCallback(mux, cardActionBody(testFeishuVerificationToken, action, job.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("重复点击应 200 + toast，实际 %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "已被处理") {
			t.Fatalf("重复点击应提示已被处理，实际 %s", rec.Body.String())
		}
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStagePublished {
		t.Fatalf("重复点击不得改状态，实际 %q", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 1 {
		t.Fatalf("翻公开应恰一次，实际 UpdateNews %d 次", updateN)
	}
	// 任务不存在：200 + error toast（非 2xx 会被飞书重试轰炸）。
	rec := postCardCallback(mux, cardActionBody(testFeishuVerificationToken, feishuCardActionApprove, 9999))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "不存在") {
		t.Fatalf("任务不存在应 200 + 人话 toast，实际 %d %s", rec.Code, rec.Body.String())
	}
}

// TestReviewPendingCardContract 待审核卡 JSON 契约（cardkit 红线，字符串断言）：
// 无 emoji、「通过并发布」「驳回」都带 confirm 二次确认、schema 2.0 不含 action/note
// 容器、callback value 带 action+job_id、update_multi 开启（群内同步刷新的前提）。
func TestReviewPendingCardContract(t *testing.T) {
	job := publishJob{
		ID: 7, Title: "示例标题", SourceName: "示例公众号",
		SourceURL: "https://mp.weixin.qq.com/s/AbC", Stage: publishStageAwaitingReview,
		FoodTalksNewsID: 7240, UpdatedAt: "2026-07-10T07:00:00Z",
	}
	raw, err := json.Marshal(buildReviewPendingCard(job))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if containsEmoji(s) {
		t.Fatalf("审核卡含 emoji（cardkit 红线）：%s", s)
	}
	if n := strings.Count(s, `"confirm":`); n != 2 {
		t.Fatalf("通过并发布 + 驳回都必须带 confirm 二次确认（应 2 处，实际 %d）：%s", n, s)
	}
	for _, want := range []string{
		`"schema":"2.0"`, `"update_multi":true`,
		"通过并发布", "驳回", "查看原文", "待审核",
		"不可逆", // confirm 文案必须明说发布不可逆
		`"action":"approve"`, `"action":"reject"`, `"job_id":7`,
		`"primary_filled"`, `"danger"`, "done_outlined",
		"示例标题", "示例公众号", "#7",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("审核卡缺少 %q：%s", want, s)
		}
	}
	for _, banned := range []string{`"tag":"action"`, `"tag":"note"`} {
		if strings.Contains(s, banned) {
			t.Fatalf("schema 2.0 禁用容器 %s（会报 200861）：%s", banned, s)
		}
	}
}

// TestNotifyPendingCardFallbackToText 卡片链路失败不影响审核推进：sendCard 报错时
// 降级发既有文本通知（人仍能收到待审核提醒），任务照常停在 awaiting_review。
func TestNotifyPendingCardFallbackToText(t *testing.T) {
	env := newPublishTestEnv(t)
	cardCalls := 0
	env.pipeline.notifyCard = func(ctx context.Context, card map[string]any) error {
		cardCalls++
		return errors.New("飞书卡片接口不可达")
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("卡片失败不得影响任务，实际 stage=%q", got.Stage)
	}
	if cardCalls != 1 {
		t.Fatalf("应先尝试发卡片（1 次），实际 %d", cardCalls)
	}
	if len(env.notified) != 1 || !strings.Contains(env.notified[0], "待审核") {
		t.Fatalf("卡片失败应降级文本待审核通知，实际 %v", env.notified)
	}

	// 卡片成功时不再重复发文本（避免同一事件双通知刷屏）。
	env2 := newPublishTestEnv(t)
	var sentCard map[string]any
	env2.pipeline.notifyCard = func(ctx context.Context, card map[string]any) error {
		sentCard = card
		return nil
	}
	if _, _, err := env2.pipeline.submit(testPublishSourceURL); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if sentCard == nil {
		t.Fatalf("staging 成功应发审核卡片")
	}
	if len(env2.notified) != 0 {
		t.Fatalf("卡片成功不应再发文本，实际 %v", env2.notified)
	}
}
