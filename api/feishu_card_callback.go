package main

// feishu_card_callback.go — 飞书卡片回调端点（施工图 M4）：POST /api/feishu/card-callback。
//
// FEISHU_CALLBACK_MODE 明确选择 Webhook（本文件端点）或长连接 WebSocket
//（feishu_ws.go）；两个传输入口不再同时开放。事件解析后的业务段抽在
// reviewCardAction，两种模式共用一份。
//
// 安全模型（与其他 /api/publish/* 端点不同，本端点由飞书开放平台服务器调用，
// **不能**上 ADMIN_PASSWORD 门）：
//  1. verification token 校验：env FEISHU_VERIFICATION_TOKEN 与请求携带的 token 比对，
//     不匹配或未配置一律 401；challenge 同样 fail-closed，须先配置 token 再验证 URL。
//     （长连接模式无此项：SDK 建连时内置鉴权。）
//  2. 审核人权限：operator.open_id 必须命中 FEISHU_REVIEWER_OPEN_IDS；空白名单默认拒绝，
//     Webhook 与 WebSocket 共用同一检查。
//  3. 业务态校验：审核动作走与 HTTP review 端点同一个 review 核心（publishPipeline.review），
//     仅 awaiting_review 可操作、approve 仍是翻公开的唯一路径——按钮不是新的权限入口。
//
// 协议（cardkit 规范 04）：
//   - url_verification（配置回调地址时的 challenge 握手）→ 原样回 {"challenge": ...}
//   - card.action.trigger → 3 秒内回 {toast, card}；approve 先把同一条消息刷新为
//     发布中（update_multi 使群内所有人同步看到），后台完成后再由 outbox 原位更新终态。
//     审核人取 operator.open_id（唯一可信的「谁」）。
//   - 业务性失败（任务不存在 / 已被处理）回 200 + toast，不回非 2xx——否则飞书会重试
//     轰炸端点；只有鉴权失败才 401。

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	feishuCallbackModeEnv       = "FEISHU_CALLBACK_MODE"
	feishuCallbackModeWebSocket = "websocket"
	feishuCallbackModeWebhook   = "webhook"
)

// configuredFeishuCallbackMode 强制两种传输层互斥。未配置或配置非法时采用更安全的
// websocket 行为（公网 Webhook 返回 404），同时由 readiness 标记为未显式配置。
func configuredFeishuCallbackMode() (mode string, explicit bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(feishuCallbackModeEnv))) {
	case feishuCallbackModeWebSocket:
		return feishuCallbackModeWebSocket, true
	case feishuCallbackModeWebhook:
		return feishuCallbackModeWebhook, true
	default:
		return feishuCallbackModeWebSocket, false
	}
}

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

// registerConfiguredFeishuCallbackRoute 只在显式 webhook 模式下开放公网审核端点。
// websocket（以及缺失/非法配置）模式仍注册精确 404，避免 POST 落入静态站兜底。
func registerConfiguredFeishuCallbackRoute(mux *http.ServeMux, p *publishPipeline) (string, bool) {
	mode, explicit := configuredFeishuCallbackMode()
	if mode == feishuCallbackModeWebhook && explicit {
		registerFeishuCardCallbackRoute(mux, p)
		return mode, explicit
	}
	mux.HandleFunc("POST /api/feishu/card-callback", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return mode, explicit
}

// feishuCallbackTokenOK verification token 校验。配置缺失同样拒绝（fail-closed）；
// challenge 首次配置也必须先把 token 写入环境，不能用「配置窗口」换裸奔审核入口。
func feishuCallbackTokenOK(got string) bool {
	want := strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_TOKEN"))
	got = strings.TrimSpace(got)
	if want == "" || got == "" || len(want) != len(got) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// feishuReviewerAllowed 是 Webhook / WebSocket 共用的审核 RBAC。白名单以逗号、分号或
// 空白分隔；未配置、全部非法或 operator 不在清单内均 fail closed。
func feishuReviewerAllowed(operatorOpenID string) bool {
	operatorOpenID = strings.TrimSpace(operatorOpenID)
	if !feishuOpenIDSafe(operatorOpenID) {
		return false
	}
	items := strings.FieldsFunc(os.Getenv("FEISHU_REVIEWER_OPEN_IDS"), func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		candidate := strings.TrimSpace(item)
		if len(candidate) == len(operatorOpenID) && subtle.ConstantTimeCompare([]byte(candidate), []byte(operatorOpenID)) == 1 {
			return true
		}
	}
	return false
}

func feishuOpenIDSafe(openID string) bool {
	if openID == "" || len(openID) > 128 {
		return false
	}
	for _, r := range openID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func feishuReviewerAllowlistConfigured() bool {
	items := strings.FieldsFunc(os.Getenv("FEISHU_REVIEWER_OPEN_IDS"), func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if feishuOpenIDSafe(strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}

// handleReviewCardAction Webhook 模式薄壳：事件字段 → 共用业务段 → 3 秒响应体 {toast, card}。
func (p *publishPipeline) handleReviewCardAction(r *http.Request, payload feishuCardCallbackPayload) map[string]any {
	out := p.reviewCardAction(r.Context(), payload.Event.Operator.OpenID, payload.Event.Action.Value)
	return feishuCardToast(out.toastType, out.toastContent, out.card)
}

// reviewCardOutcome 卡片按钮业务处理结果——Webhook 与长连接两模式共用的中立形态，
// 由各自的薄壳包装成协议响应（Webhook: {toast,card} JSON；长连接: SDK CardActionTriggerResponse）。
type reviewCardOutcome struct {
	toastType    string // success / info / error
	toastContent string
	card         map[string]any // 非 nil 时以 raw 形态刷新当前卡片（update_multi 群内同步）
}

// reviewCardAction 卡片按钮动作的业务核心（两模式共用唯一入口）：解析 value
// （action + job_id，兼容数字/字符串）→ 业务态校验 → review 核心 → 发布中/终态卡。
// 审核语义与权限红线只在 publishPipeline.review 定义一次，此处不复制。
func (p *publishPipeline) reviewCardAction(ctx context.Context, operatorOpenID string, rawValue json.RawMessage) reviewCardOutcome {
	var value struct {
		Action        string `json:"action"`
		JobID         any    `json:"job_id"`
		ReviewVersion string `json:"review_version"`
	}
	if len(rawValue) > 0 {
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return reviewCardOutcome{toastType: "error", toastContent: "按钮数据无法解析"}
		}
	}
	jobID := coerceFeishuJobID(value.JobID)
	if jobID <= 0 || (value.Action != feishuCardActionApprove && value.Action != feishuCardActionReject) {
		return reviewCardOutcome{toastType: "error", toastContent: "按钮数据不完整，无法定位发布任务"}
	}
	if !feishuReviewerAllowed(operatorOpenID) {
		return reviewCardOutcome{toastType: "error", toastContent: "你没有网站发布审核权限，请联系管理员配置审核人白名单"}
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return reviewCardOutcome{toastType: "error", toastContent: err.Error()}
	}
	// 防重复点击：任务已进终态 → 提示已被处理，并把卡片刷成当前终态（update_multi
	// 让群里其他人手里的旧「待审核」卡也一起变终态）。
	if job.terminal() {
		return reviewCardOutcome{toastType: "info", toastContent: "该任务已被处理", card: buildReviewDoneCard(job)}
	}
	wantVersion := publishReviewCardVersion(job)
	if wantVersion != "" && (len(value.ReviewVersion) != len(wantVersion) ||
		subtle.ConstantTimeCompare([]byte(value.ReviewVersion), []byte(wantVersion)) != 1) {
		return reviewCardOutcome{toastType: "error", toastContent: "审核卡已过期，请使用最新一张包含风险结论的卡片"}
	}
	if job.Stage != publishStageAwaitingReview {
		// 非终态也非待审（failed / publicizing 半途）：不动卡片，指路管理后台。
		return reviewCardOutcome{toastType: "info", toastContent: fmt.Sprintf("任务当前阶段（%s）无法在卡片上操作，请到管理后台处理", job.Stage)}
	}
	// 审核人身份唯一可信来源是 operator.open_id（cardkit 规范 04），不信 value 自带字段。
	reviewed, err := p.reviewWithRiskVersion(ctx, jobID, value.Action, operatorOpenID, value.ReviewVersion)
	if err != nil {
		return reviewCardOutcome{toastType: "error", toastContent: err.Error()}
	}
	if value.Action == feishuCardActionApprove {
		return reviewCardOutcome{toastType: "success", toastContent: "已受理，正在发布", card: buildReviewPublishingCard(reviewed)}
	}
	return reviewCardOutcome{toastType: "success", toastContent: "已驳回", card: buildReviewDoneCard(reviewed)}
}

// publishReviewCardVersion 将风险结论、内容哈希和冻结版本绑定到卡片按钮：补检、
// 重评或高权威正文晚到都会使旧卡失效，避免审核人批准已过期内容。
// HTTP admin 审核不使用卡片版本。
func publishReviewCardVersion(job publishJob) string {
	if !publishRiskLevelKnown(job.RiskLevel) {
		return ""
	}
	// 上线前已经发出的待审卡仍携带 risk-v2。完全没有内容版本元数据的存量任务
	// 保持旧摘要算法，避免部署后所有旧卡无替代卡可点；任何新任务或正文变更都走 v3。
	if job.ContentVersion == 0 && job.ContentFrozenVersion == 0 && job.ContentHash == "" {
		payload, _ := json.Marshal(struct {
			Level string            `json:"level"`
			Items []publishRiskItem `json:"items"`
			Notes string            `json:"notes"`
		}{Level: job.RiskLevel, Items: job.RiskItems, Notes: job.RiskNotes})
		digest := sha256.Sum256(payload)
		return fmt.Sprintf("risk-v2:%d:%x", job.ID, digest[:8])
	}
	payload, _ := json.Marshal(struct {
		Level         string            `json:"level"`
		Items         []publishRiskItem `json:"items"`
		Notes         string            `json:"notes"`
		ContentHash   string            `json:"content_hash"`
		Content       int64             `json:"content_version"`
		FrozenContent int64             `json:"frozen_content_version"`
	}{
		Level: job.RiskLevel, Items: job.RiskItems, Notes: job.RiskNotes,
		ContentHash: job.ContentHash, Content: job.ContentVersion,
		FrozenContent: job.ContentFrozenVersion,
	})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("review-v3:%d:%x", job.ID, digest[:8])
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
