package main

// feishu_card_callback.go — 飞书卡片回调端点（施工图 M4）：POST /api/feishu/card-callback。
//
// FEISHU_CALLBACK_MODE 明确选择 Webhook（本文件端点）或长连接 WebSocket
//（feishu_ws.go）；两个传输入口不再同时开放。事件解析后的业务段抽在
// reviewCardAction，两种模式共用一份。
//
// 安全模型（与其他 /api/publish/* 端点不同，本端点由飞书开放平台服务器调用，
// **不能**依赖后台登录角色门）：
//  1. verification token 校验：env FEISHU_VERIFICATION_TOKEN 与请求携带的 token 比对，
//     不匹配或未配置一律 401；challenge 同样 fail-closed，须先配置 token 再验证 URL。
//     （长连接模式无此项：SDK 建连时内置鉴权。）
//  2. 审核人权限：operator.open_id 命中 FEISHU_REVIEWER_OPEN_IDS 时不检查群；否则只有
//     启用群成员模式且 chat_id 匹配当前配置审核群（环境变量或投递设置）才放行，其他群拒绝。
//  3. 业务态校验：审核动作走与 HTTP review 端点同一个 review 核心（publishPipeline.review），
//     仅 awaiting_review 可操作、approve 仍是翻公开的唯一路径——按钮不是新的权限入口。
//
// 协议（cardkit 规范 04）：
//   - url_verification（配置回调地址时的 challenge 握手）→ 原样回 {"challenge": ...}
//   - card.action.trigger → 3 秒内只回 {toast}；整张日批次卡由后台原位更新，
//     避免连点时在回调里读整库超时（飞书 108002）。审核人取 operator.open_id。
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
	"time"
)

const (
	feishuCallbackModeEnv       = "FEISHU_CALLBACK_MODE"
	feishuCallbackModeWebSocket = "websocket"
	feishuCallbackModeWebhook   = "webhook"
)

// 飞书 card.action.trigger 限 3 秒回包，超时即 108002。
// 2.5 秒仍未做完就先回 toast，审核 CAS 继续在后台跑。测试可缩短。
var feishuCardActionSLA = 2500 * time.Millisecond

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
		Context struct {
			OpenChatID string `json:"open_chat_id"`
		} `json:"context"`
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

// feishuReviewerChatModeEnabled 群成员审核模式（2026-08-06 用户裁定：「只要在群里
// 面，就有权限点这个卡片」）。显式 env 开关，默认关闭保持旧白名单行为。
func feishuReviewerChatModeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// reviewChatAllowed 判定卡片动作发生的群是否是配置过的审核群：FEISHU_REVIEW_CHAT_ID
// 或规则后台投递设置里的任一群。只有这些群里才会出现审核卡；卡片被转发到其他
// 会话后群 id 不同，一律拒绝。
func (p *publishPipeline) reviewChatAllowed(chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false
	}
	if envChat := strings.TrimSpace(os.Getenv("FEISHU_REVIEW_CHAT_ID")); envChat != "" && envChat == chatID {
		return true
	}
	if p != nil && p.deliverySettings != nil {
		if settings, _, err := p.deliverySettings.resolve(); err == nil {
			for _, configured := range settings.ChatIDs {
				if strings.TrimSpace(configured) == chatID {
					return true
				}
			}
		}
	}
	return false
}

// feishuReviewActionAllowed 卡片审核动作的权限总闸：静态审核人白名单，或（群成员
// 模式开启时）动作来自配置的审核群。群成员语义依赖飞书事实：只有群成员能看到并
// 点击群消息里的按钮，事件 context.open_chat_id 由飞书服务端注入、不可由客户端伪造。
func (p *publishPipeline) feishuReviewActionAllowed(operatorOpenID, actionChatID string) bool {
	if feishuReviewerAllowed(operatorOpenID) {
		return true
	}
	if !feishuReviewerChatModeEnabled() {
		return false
	}
	// 群成员模式仍要求 operator open_id 形态合法：审核留痕（reviewed_by）必须可信。
	if !feishuOpenIDSafe(strings.TrimSpace(operatorOpenID)) {
		return false
	}
	return p.reviewChatAllowed(actionChatID)
}

// feishuReviewerAuthConfigured readiness 口径：白名单与群成员模式二者有其一即算
// 配置完备；群成员模式必须同时有明确的审核群配置，否则等于没有任何权限来源。
func feishuReviewerAuthConfigured() bool {
	if feishuReviewerAllowlistConfigured() {
		return true
	}
	return feishuReviewerChatModeEnabled() && strings.TrimSpace(os.Getenv("FEISHU_REVIEW_CHAT_ID")) != ""
}

// handleReviewCardAction Webhook 模式薄壳：事件字段 → 共用业务段 → 3 秒内只回 {toast}。
func (p *publishPipeline) handleReviewCardAction(r *http.Request, payload feishuCardCallbackPayload) map[string]any {
	out := p.reviewCardActionWithinSLA(r.Context(), payload.Event.Operator.OpenID, payload.Event.Context.OpenChatID, payload.Event.Action.Value)
	return feishuCardToast(out.toastType, out.toastContent, nil)
}

// reviewCardActionWithinSLA 保证飞书 3 秒窗口内必有 toast。
// 连点会串行写整份任务库；超过 SLA 仍把审核做完，只是先告诉飞书「正在处理」。
func (p *publishPipeline) reviewCardActionWithinSLA(parent context.Context, openID, chatID string, rawValue json.RawMessage) reviewCardOutcome {
	ctx := context.WithoutCancel(parent)
	done := make(chan reviewCardOutcome, 1)
	go func() {
		done <- p.reviewCardAction(ctx, openID, chatID, rawValue)
	}()
	timer := time.NewTimer(feishuCardActionSLA)
	defer timer.Stop()
	select {
	case out := <-done:
		return out
	case <-timer.C:
		return reviewCardOutcome{toastType: "info", toastContent: "已受理，正在处理"}
	}
}

// reviewCardOutcome 卡片按钮业务处理结果——Webhook 与长连接两模式共用的中立形态，
// 由各自的薄壳包装成协议响应（Webhook: {toast,card} JSON；长连接: SDK CardActionTriggerResponse）。
type reviewCardOutcome struct {
	toastType    string // success / info / error
	toastContent string
	card         map[string]any // 非 nil 时以 raw 形态刷新当前卡片（update_multi 群内同步）
}

// reviewCardAction 卡片按钮动作的业务核心（两模式共用唯一入口）：解析 value
// （action + job_id，兼容数字/字符串）→ 读取最新任务状态 → 业务态校验 → review 核心
// → 发布中/终态卡。审核语义与权限红线只在 publishPipeline.review 定义一次，此处不复制。
func (p *publishPipeline) reviewCardAction(ctx context.Context, operatorOpenID, actionChatID string, rawValue json.RawMessage) (out reviewCardOutcome) {
	var value struct {
		Action              string `json:"action"`
		JobID               any    `json:"job_id"`
		ReviewVersion       string `json:"review_version"`
		LifecycleVersion    string `json:"lifecycle_version"`
		DistributionVersion string `json:"distribution_version"`
	}
	if len(rawValue) > 0 {
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return reviewCardOutcome{toastType: "error", toastContent: "按钮数据无法解析"}
		}
	}
	jobID := coerceFeishuJobID(value.JobID)
	if jobID <= 0 || (value.Action != feishuCardActionApprove && value.Action != feishuCardActionForceApprove &&
		value.Action != feishuCardActionReject &&
		value.Action != feishuCardActionUnpublish && value.Action != feishuCardActionRepublish &&
		value.Action != feishuCardActionDistributeExternal) {
		return reviewCardOutcome{toastType: "error", toastContent: "按钮数据不完整，无法定位发布任务"}
	}
	defer func() {
		if out.card == nil {
			return
		}
		p.scheduleReviewBatchCardRefresh(jobID)
	}()
	if !p.feishuReviewActionAllowed(operatorOpenID, actionChatID) {
		return reviewCardOutcome{toastType: "error", toastContent: "你没有发布操作权限：请在审核群内操作卡片，或联系管理员配置审核人白名单"}
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return reviewCardOutcome{toastType: "error", toastContent: err.Error()}
	}
	// 外部分发属于 published 任务的独立后续动作。published 在主状态机里是终态，
	// 因此必须像下线/重发一样，在 terminal 判断之前分流。
	if value.Action == feishuCardActionDistributeExternal {
		if outcome, handled := externalDistributionCardStateOutcome(job); handled {
			return outcome
		}
		if !externalDistributionVersionMatches(value.DistributionVersion, job) {
			return reviewCardOutcome{
				toastType: "error", toastContent: "分发卡片已过期，请使用最新卡片",
			}
		}
		_, distributionErr := p.requestExternalDistribution(jobID, operatorOpenID, value.DistributionVersion)
		if distributionErr != nil {
			if latest, loadErr := p.store.byID(jobID); loadErr == nil {
				if outcome, handled := externalDistributionCardStateOutcome(latest); handled {
					return outcome
				}
			}
			return reviewCardOutcome{toastType: "error", toastContent: distributionErr.Error()}
		}
		return reviewCardOutcome{
			toastType: "success", toastContent: "已受理，正在同步分发",
		}
	}
	// 下线 / 重新发布是已发布任务的后续生命周期动作，必须在 terminal 判断前
	// 分流；offline / published 虽是业务终态，卡片仍允许显示对应的下一步按钮。
	if value.Action == feishuCardActionUnpublish || value.Action == feishuCardActionRepublish {
		// 管理后台和飞书卡片可能几乎同时操作同一篇文章。先按持久化状态做一次
		// 幂等判断，避免把“已经下线/正在下线”误报成卡片过期，也避免再次发出
		// FoodTalks PUT；返回当前状态卡，让群内旧卡一起收敛到同一张卡片。
		if outcome, handled := lifecycleCardStateOutcome(value.Action, job); handled {
			return outcome
		}
		if !lifecycleVersionMatches(value.LifecycleVersion, job) {
			return lifecycleCardStaleOutcome(value.Action, job)
		}
		var next publishJob
		var lifecycleErr error
		if value.Action == feishuCardActionUnpublish {
			next, lifecycleErr = p.requestUnpublish(jobID, operatorOpenID, value.LifecycleVersion)
		} else {
			next, lifecycleErr = p.requestRepublish(jobID, operatorOpenID, value.LifecycleVersion)
		}
		if lifecycleErr != nil {
			// 状态可能在第一次读取之后被管理后台抢先更新。重新读取一次，
			// 将竞态收敛为“已处理/处理中”提示，而不是暴露内部 CAS 错误。
			if latest, loadErr := p.store.byID(jobID); loadErr == nil {
				if outcome, handled := lifecycleCardStateOutcome(value.Action, latest); handled {
					return outcome
				}
			}
			return reviewCardOutcome{toastType: "error", toastContent: lifecycleErr.Error()}
		}
		return reviewCardOutcome{
			toastType: "success", toastContent: map[string]string{
				feishuCardActionUnpublish: "已受理下线请求",
				feishuCardActionRepublish: "已受理重新发布请求",
			}[value.Action],
			card: buildReviewLifecycleProgressCard(next, value.Action),
		}
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
	if value.Action == feishuCardActionApprove || value.Action == feishuCardActionForceApprove {
		toast := "已受理，正在发布"
		if value.Action == feishuCardActionForceApprove {
			toast = "已受理，正在强制发布"
		}
		return reviewCardOutcome{toastType: "success", toastContent: toast, card: buildReviewPublishingCard(reviewed)}
	}
	return reviewCardOutcome{toastType: "success", toastContent: "已驳回", card: buildReviewDoneCard(reviewed)}
}

// lifecycleCardStateOutcome 在卡片回调调用外部 FoodTalks 之前读取的本地持久状态。
// 本地状态是发布意图、worker 和 outbox 的共同真相源：卡片回调不能在 3 秒响应窗口内
// 依赖一个慢的外部查询，更不能在已经进入外部调用窗口后盲目重试。返回 handled=true
// 表示当前动作已经被处理、正在处理，或必须人工对账，调用方不得再发 PUT。
func lifecycleCardStateOutcome(action string, job publishJob) (reviewCardOutcome, bool) {
	progress := func(progressAction, toast string) (reviewCardOutcome, bool) {
		return reviewCardOutcome{
			toastType:    "info",
			toastContent: toast,
			card:         buildReviewLifecycleProgressCard(job, progressAction),
		}, true
	}
	issue := func(toast string) (reviewCardOutcome, bool) {
		return reviewCardOutcome{
			toastType:    "error",
			toastContent: toast,
			card:         buildReviewIssueCard(job, true),
		}, true
	}

	switch action {
	case feishuCardActionUnpublish:
		switch job.Stage {
		case publishStageOffline:
			return reviewCardOutcome{
				toastType:    "info",
				toastContent: "文章已下线，无需重复操作",
				card:         buildReviewDoneCard(job),
			}, true
		case publishStageUnpublishing, publishStageUnpublishingUpdating:
			return progress(feishuCardActionUnpublish, "文章正在下线处理中，请稍候")
		case publishStageUnpublishReconciliationRequired:
			return issue("下线结果待人工核对，请先到管理后台确认文章状态")
		case publishStageRepublishing, publishStageRepublishingUpdating:
			return progress(feishuCardActionRepublish, "文章正在重新发布，暂时不能下线")
		case publishStageRepublishReconciliationRequired:
			return issue("重新发布结果待人工核对，暂时不能下线")
		}
	case feishuCardActionRepublish:
		switch job.Stage {
		case publishStagePublished:
			return reviewCardOutcome{
				toastType:    "info",
				toastContent: "文章已重新发布，无需重复操作",
				card:         buildReviewDoneCard(job),
			}, true
		case publishStageRepublishing, publishStageRepublishingUpdating:
			return progress(feishuCardActionRepublish, "文章正在重新发布处理中，请稍候")
		case publishStageRepublishReconciliationRequired:
			return issue("重新发布结果待人工核对，请先到管理后台确认文章状态")
		case publishStageUnpublishing, publishStageUnpublishingUpdating:
			return progress(feishuCardActionUnpublish, "文章正在下线，暂时不能重新发布")
		case publishStageUnpublishReconciliationRequired:
			return issue("下线结果待人工核对，暂时不能重新发布")
		}
	}
	return reviewCardOutcome{}, false
}

// lifecycleCardStaleOutcome 处理仍处于可操作状态、但按钮版本已落后的情况。
// 给出最新终态卡而不是只吐错误 toast，用户可以直接看到新的窗口/按钮状态。
func lifecycleCardStaleOutcome(action string, job publishJob) reviewCardOutcome {
	if outcome, handled := lifecycleCardStateOutcome(action, job); handled {
		return outcome
	}
	outcome := reviewCardOutcome{
		toastType:    "error",
		toastContent: "生命周期卡片已过期，请使用最新一张卡片",
	}
	if job.Stage == publishStagePublished || job.Stage == publishStageOffline {
		outcome.toastType = "info"
		outcome.toastContent = "文章状态已更新，请使用最新卡片上的操作"
		outcome.card = buildReviewDoneCard(job)
	}
	return outcome
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

// feishuCardToast 组装 3 秒响应体：toast 必带。回调热路径不再带整张卡，
// 避免连点超时；card 参数保留给单测直接断言业务段产物。
func feishuCardToast(typ, content string, card map[string]any) map[string]any {
	resp := map[string]any{
		"toast": map[string]any{"type": typ, "content": content},
	}
	if card != nil {
		resp["card"] = map[string]any{"type": "raw", "data": card}
	}
	return resp
}
