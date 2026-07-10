package main

// feishu_card_callback.go — 飞书卡片回调端点（施工图 M4）：POST /api/feishu/card-callback。
//
// 安全模型（与其他 /api/publish/* 端点不同，本端点由飞书开放平台服务器调用，
// **不能**上 ADMIN_PASSWORD 门）：
//  1. verification token 校验：env FEISHU_VERIFICATION_TOKEN 与请求携带的 token 比对，
//     不匹配一律 401。未配置时 WARN 放行——仅为首次配置窗口（开放平台后台要先配回调
//     地址并通过 challenge 验证才发得下 token）；⚠️ 首次配置后必须把 token 回填
//     publish.env，长期空置等于回调裸奔。
//  2. 业务态校验：审核动作走与 HTTP review 端点同一个 review 核心（publishPipeline.review），
//     仅 awaiting_review 可操作、approve 仍是翻公开的唯一路径——按钮不是新的权限入口。
//
// 协议（cardkit 规范 04）：
//   - url_verification（配置回调地址时的 challenge 握手）→ 原样回 {"challenge": ...}
//   - card.action.trigger → 3 秒内回 {toast, card}；card 为终态卡（update_multi 使群内
//     所有人同步看到），审核人取 operator.open_id（唯一可信的「谁」）。
//   - 业务性失败（任务不存在 / 已被处理）回 200 + toast，不回非 2xx——否则飞书会重试
//     轰炸端点；只有鉴权失败才 401。

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// feishuCardCallbackPayload 回调请求的联合形态：url_verification 的字段在顶层，
// card.action.trigger（事件 v2）的 verification token 在 header.token。
type feishuCardCallbackPayload struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
	Header    struct {
		EventType string `json:"event_type"`
		Token     string `json:"token"`
	} `json:"header"`
	Event struct {
		Operator struct {
			OpenID string `json:"open_id"`
		} `json:"operator"`
		Action struct {
			Value json.RawMessage `json:"value"`
		} `json:"action"`
	} `json:"event"`
}

func registerFeishuCardCallbackRoute(mux *http.ServeMux, p *publishPipeline) {
	mux.HandleFunc("POST /api/feishu/card-callback", func(w http.ResponseWriter, r *http.Request) {
		var payload feishuCardCallbackPayload
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		token := payload.Token
		if token == "" {
			token = payload.Header.Token
		}
		if !feishuCallbackTokenOK(token) {
			writeError(w, http.StatusUnauthorized, "verification token 不匹配")
			return
		}
		// challenge 握手：开放平台配置回调地址时的 URL 校验，原样回 challenge。
		if payload.Type == "url_verification" {
			writeJSON(w, http.StatusOK, map[string]string{"challenge": payload.Challenge})
			return
		}
		if payload.Header.EventType != "card.action.trigger" {
			// 未订阅关注的事件类型：回 200 空体让飞书别重试，不做任何业务动作。
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		writeJSON(w, http.StatusOK, p.handleReviewCardAction(r, payload))
	})
}

// feishuCallbackTokenOK verification token 校验；env 未配置 WARN 放行（首次配置窗口，
// 之后必须回填——见文件头安全模型）。
func feishuCallbackTokenOK(got string) bool {
	want := strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_TOKEN"))
	if want == "" {
		log.Printf("publish: WARN FEISHU_VERIFICATION_TOKEN 未配置，卡片回调未做来源校验（首次配置后必须回填 publish.env）")
		return true
	}
	return got == want
}

// handleReviewCardAction 卡片按钮动作 → review 核心。返回 3 秒响应体 {toast, card}。
func (p *publishPipeline) handleReviewCardAction(r *http.Request, payload feishuCardCallbackPayload) map[string]any {
	var value struct {
		Action string `json:"action"`
		JobID  any    `json:"job_id"`
	}
	if len(payload.Event.Action.Value) > 0 {
		if err := json.Unmarshal(payload.Event.Action.Value, &value); err != nil {
			return feishuCardToast("error", "按钮数据无法解析", nil)
		}
	}
	jobID := coerceFeishuJobID(value.JobID)
	if jobID <= 0 || (value.Action != feishuCardActionApprove && value.Action != feishuCardActionReject) {
		return feishuCardToast("error", "按钮数据不完整，无法定位发布任务", nil)
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return feishuCardToast("error", err.Error(), nil)
	}
	// 防重复点击：任务已进终态 → 提示已被处理，并把卡片刷成当前终态（update_multi
	// 让群里其他人手里的旧「待审核」卡也一起变终态）。
	if job.terminal() {
		return feishuCardToast("info", "该任务已被处理", buildReviewDoneCard(job))
	}
	if job.Stage != publishStageAwaitingReview {
		// 非终态也非待审（failed / publicizing 半途）：不动卡片，指路管理后台。
		return feishuCardToast("info", fmt.Sprintf("任务当前阶段（%s）无法在卡片上操作，请到管理后台处理", job.Stage), nil)
	}
	// 审核人身份唯一可信来源是 operator.open_id（cardkit 规范 04），不信 value 自带字段。
	reviewed, err := p.review(r.Context(), jobID, value.Action, payload.Event.Operator.OpenID)
	if err != nil {
		return feishuCardToast("error", err.Error(), nil)
	}
	toast := "已发布"
	if value.Action == feishuCardActionReject {
		toast = "已驳回"
	}
	return feishuCardToast("success", toast, buildReviewDoneCard(reviewed))
}

// coerceFeishuJobID 回调 value 里的 job_id 兼容数字与字符串两种 JSON 形态。
func coerceFeishuJobID(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		id, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		if err != nil {
			return 0
		}
		return id
	case json.Number:
		id, err := n.Int64()
		if err != nil {
			return 0
		}
		return id
	default:
		return 0
	}
}

// feishuCardToast 组装 3 秒响应体：toast 必带；card 非 nil 时以 raw 形态同步刷新卡片。
func feishuCardToast(typ, content string, card map[string]any) map[string]any {
	resp := map[string]any{
		"toast": map[string]any{"type": typ, "content": content},
	}
	if card != nil {
		resp["card"] = map[string]any{"type": "raw", "data": card}
	}
	return resp
}
