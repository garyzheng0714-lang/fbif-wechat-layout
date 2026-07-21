package main

// feishu_ws_test.go — 飞书长连接模式测试（2026-07-10 升级）。
// 长连接客户端本身（SDK 建连/重连）薄到无需测；这里锁的是本仓自己的两段：
//   1. 事件体 → 业务函数参数的解析（feishuWSEventParams）：open_id 取位、
//      value map 重编码、job_id 数字/字符串两形态、空事件不 panic；
//   2. 长连接事件驱动共用业务段（feishuWSCardActionHandler → reviewCardAction）
//      与 Webhook 走同一个 review 核心——审核语义不因传输层换轨而漂移。
// 全程 httptest 桩（publishTestEnv），不连真实飞书。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func wsCardEvent(openID string, value map[string]interface{}) *callback.CardActionTriggerEvent {
	return &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Operator: &callback.Operator{OpenID: openID},
			Action:   &callback.CallBackAction{Value: value},
		},
	}
}

// TestFeishuWSEventParams 事件体 → 业务函数参数：open_id 与 action.value 必须
// 原样到位（value 经 map 重编码后仍能被 Webhook 同款解析消费——数字 job_id 是
// float64、字符串 job_id 原样，两形态都由 coerceFeishuJobID 兜住）。
func TestFeishuWSEventParams(t *testing.T) {
	event := wsCardEvent("ou_ws_reviewer", map[string]interface{}{
		"action": "approve", "job_id": float64(7),
	})
	openID, rawValue := feishuWSEventParams(event)
	if openID != "ou_ws_reviewer" {
		t.Fatalf("open_id 应为 ou_ws_reviewer，实际 %q", openID)
	}
	var value struct {
		Action string `json:"action"`
		JobID  any    `json:"job_id"`
	}
	if err := json.Unmarshal(rawValue, &value); err != nil {
		t.Fatalf("重编码后的 value 应可解析：%v（raw=%s）", err, rawValue)
	}
	if value.Action != feishuCardActionApprove {
		t.Fatalf("action 应为 approve，实际 %q", value.Action)
	}
	if got := coerceFeishuJobID(value.JobID); got != 7 {
		t.Fatalf("数字 job_id 应解析为 7，实际 %d", got)
	}

	// 字符串形态 job_id（飞书回调两形态之一）同样要通。
	_, rawStr := feishuWSEventParams(wsCardEvent("ou_x", map[string]interface{}{
		"action": "reject", "job_id": "42",
	}))
	if err := json.Unmarshal(rawStr, &value); err != nil {
		t.Fatalf("字符串 job_id 的 value 应可解析：%v", err)
	}
	if got := coerceFeishuJobID(value.JobID); got != 42 {
		t.Fatalf("字符串 job_id 应解析为 42，实际 %d", got)
	}
}

// TestFeishuWSEventParamsEmpty 空/残缺事件不 panic，返回空参——业务段随后回
// 「按钮数据不完整」toast，而不是长连接协程崩掉。
func TestFeishuWSEventParamsEmpty(t *testing.T) {
	for name, event := range map[string]*callback.CardActionTriggerEvent{
		"nil 事件":     nil,
		"nil Event":  {},
		"nil Action": {Event: &callback.CardActionTriggerRequest{Operator: &callback.Operator{OpenID: "ou_a"}}},
		"nil Value":  {Event: &callback.CardActionTriggerRequest{Action: &callback.CallBackAction{}}},
		"全空 Request": {Event: &callback.CardActionTriggerRequest{}},
	} {
		openID, rawValue := feishuWSEventParams(event)
		if len(rawValue) > 0 {
			t.Fatalf("%s：应返回空 value，实际 %s", name, rawValue)
		}
		if name == "nil Action" && openID != "ou_a" {
			t.Fatalf("operator 在场时 open_id 仍应取到，实际 %q", openID)
		}
	}
	// 空参进业务段 → error toast「按钮数据不完整」，不触发任何审核动作。
	env := newPublishTestEnv(t)
	out := env.pipeline.reviewCardAction(context.Background(), "", nil)
	if out.toastType != "error" || out.card != nil {
		t.Fatalf("空参应回 error toast 且不带卡片，实际 %+v", out)
	}
}

// TestFeishuWSCardActionApprove 长连接事件驱动与 Webhook 同一个 review 核心：
// approve 事件 → CAS 落 publicizing 后立即回 success + raw 蓝进度卡；后台 worker
// 再执行 UpdateNews，避免长连接 3 秒响应等待外部 API。
func TestFeishuWSCardActionApprove(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FOODTALKS_FRONT_URL", "")
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_ws_reviewer")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false // 本场景 approve 合法，解除红线守卫
	env.ft.mu.Unlock()
	ready, _ := env.store.byID(job.ID)
	var queued []func()
	env.pipeline.spawn = func(f func()) { queued = append(queued, f) }

	handler := feishuWSCardActionHandler(env.pipeline)
	resp, err := handler(context.Background(), wsCardEvent("ou_ws_reviewer", map[string]interface{}{
		"action": feishuCardActionApprove, "job_id": float64(job.ID), "review_version": publishReviewCardVersion(ready),
	}))
	if err != nil {
		t.Fatalf("handler 不应返回 error（业务失败走 toast）：%v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStagePublicizing || len(queued) != 1 {
		t.Fatalf("响应时应 publicizing + 后台 worker，实际 stage=%q queued=%d", got.Stage, len(queued))
	}
	if got.ReviewedBy != "ou_ws_reviewer" || got.ReviewedAt == "" {
		t.Fatalf("审核留痕未落库：reviewed_by=%q reviewed_at=%q", got.ReviewedBy, got.ReviewedAt)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("响应前不得调用 FoodTalks，实际 UpdateNews=%d", updateN)
	}
	if resp.Toast == nil || resp.Toast.Type != "success" || resp.Toast.Content != "已受理，正在发布" {
		t.Fatalf("toast 不符：%+v", resp.Toast)
	}
	if resp.Card == nil || resp.Card.Type != "raw" {
		t.Fatalf("响应应带 raw 进度卡：%+v", resp.Card)
	}
	card, ok := resp.Card.Data.(map[string]any)
	if !ok || card["schema"] != "2.0" {
		t.Fatalf("进度卡应为 schema 2.0 卡片对象：%+v", resp.Card.Data)
	}
	header, _ := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Fatalf("approve 进度卡 header 应为 blue，实际 %v", header["template"])
	}
	queued[0]()
	got, _ = env.store.byID(job.ID)
	if got.Stage != publishStagePublished {
		t.Fatalf("后台 worker 后应 published，实际 stage=%q error=%q", got.Stage, got.Error)
	}
	if _, updateN := env.ft.counts(); updateN != 1 || env.ft.updateStatuses[0] != foodTalksPublishStatusPublished {
		t.Fatalf("后台 UpdateNews 应恰一次 publishStatus=1，实际 statuses=%v", env.ft.updateStatuses)
	}
}
