package main

// feishu_ws.go — 飞书卡片回调·长连接（WebSocket）模式（2026-07-10 升级）。
//
// FEISHU_CALLBACK_MODE 明确选择 websocket/webhook，两个入口不再同时开放。
//
// 与 Webhook 的差异只有传输层：
//   - 鉴权：长连接由官方 SDK 建连时用 app_id/app_secret 内置鉴权，
//     无需 FEISHU_VERIFICATION_TOKEN（那是 Webhook 验证请求来源用的）。
//   - 断线重连：SDK 自带（WithAutoReconnect），进程内无需自己重试。
//   - 业务段：与 Webhook 共用同一个 reviewCardAction（feishu_card_callback.go），
//     审核语义/权限红线仍只在 publishPipeline.review 定义一次。
//
// SDK：github.com/larksuite/oapi-sdk-go/v3（larkws + dispatcher + callback）。

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// maybeStartFeishuWS FEISHU_APP_ID/FEISHU_APP_SECRET 齐了就 goroutine 启动长连接
// 客户端；缺任一则 log 一行跳过并由 readiness fail closed。
// 长连接失败不 fatal——它是审核交互的加速器，不是发布流水线的闸门。
func maybeStartFeishuWS(p *publishPipeline) {
	if publishWorkersPaused() {
		log.Printf("publish: workers paused，跳过飞书长连接")
		return
	}
	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	if appID == "" || appSecret == "" {
		log.Printf("publish: FEISHU_APP_ID/FEISHU_APP_SECRET 未配置，跳过飞书长连接")
		return
	}
	// 长连接模式无需 verification token / encrypt key（SDK 建连鉴权），两参传空。
	handler := dispatcher.NewEventDispatcher("", "").
		OnP2CardActionTrigger(feishuWSCardActionHandler(p))
	cli := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(handler),
		larkws.WithAutoReconnect(true),
		larkws.WithOnReady(func() {
			log.Printf("publish: 飞书长连接已建立")
		}),
		larkws.WithOnReconnecting(func() {
			log.Printf("publish: 飞书长连接断开，SDK 正在自动重连……")
		}),
		larkws.WithOnReconnected(func() {
			log.Printf("publish: 飞书长连接已恢复")
		}),
		larkws.WithOnError(func(err error) {
			log.Printf("publish: 飞书长连接出错：%v", err)
		}),
	)
	go func() {
		// Start 阻塞运行（内部含 SDK 自动重连循环）；返回错误说明连不上且不再重试
		// （如凭证错误），只记日志；readiness 配置检查仍保持 fail closed。
		if err := cli.Start(context.Background()); err != nil {
			log.Printf("publish: 飞书长连接启动失败：%v", err)
		}
	}()
}

// feishuWSCardActionHandler card.action.trigger 事件 → 共用业务段 → SDK 响应
// （3 秒窗口只回 toast；日批次卡后台原位更新）。SDK 负责把返回值在 3 秒窗口内回给飞书。
func feishuWSCardActionHandler(p *publishPipeline) func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	return func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		openID, chatID, rawValue := feishuWSEventParams(event)
		var formValue map[string]any
		if event != nil && event.Event != nil && event.Event.Action != nil {
			formValue = event.Event.Action.FormValue
		}
		out := p.reviewCardActionWithinSLA(ctx, openID, chatID, rawValue, formValue)
		log.Printf("publish: 飞书长连接收到卡片动作 operator=%s → %s（%s）", openID, out.toastType, out.toastContent)
		resp := &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: out.toastType, Content: out.toastContent},
		}
		// 3 秒窗口只回 toast。日批次卡由 scheduleReviewBatchCardRefresh 原位更新，
		// 避免连点时在回调里重建整卡导致飞书 108002。
		return resp, nil
	}
}

// feishuWSEventParams 长连接事件体 → 业务函数参数：operator.open_id +
// action.value（SDK 解出的 map 重编码为 JSON，交给与 Webhook 同一套解析——
// job_id 数字/字符串两形态由 coerceFeishuJobID 兼容）。空事件不 panic，
// 返回空参让业务段回「按钮数据不完整」toast。
func feishuWSEventParams(event *callback.CardActionTriggerEvent) (openID, chatID string, rawValue json.RawMessage) {
	if event == nil || event.Event == nil {
		return "", "", nil
	}
	if op := event.Event.Operator; op != nil {
		openID = op.OpenID
	}
	if evtCtx := event.Event.Context; evtCtx != nil {
		chatID = evtCtx.OpenChatID
	}
	if action := event.Event.Action; action != nil && action.Value != nil {
		if b, err := json.Marshal(action.Value); err == nil {
			rawValue = b
		}
	}
	return openID, chatID, rawValue
}
