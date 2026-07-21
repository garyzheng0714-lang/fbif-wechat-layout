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
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	mux := http.NewServeMux()
	registerFeishuCardCallbackRoute(mux, env.pipeline)
	return mux
}

// TestFeishuCallbackMissingTokenFailClosed 回调来源凭证未配置时必须默认拒绝；
// challenge 也不能为了首次配置窗口而裸奔，否则同一路由上的审核动作会一起失守。
func TestFeishuCallbackMissingTokenFailClosed(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FEISHU_VERIFICATION_TOKEN", "")
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	mux := http.NewServeMux()
	registerFeishuCardCallbackRoute(mux, env.pipeline)
	rec := postCardCallback(mux, `{"type":"url_verification","challenge":"ch-unsafe","token":""}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("token 未配置时 challenge 必须 fail-closed，实际 %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestConfiguredFeishuCallbackRouteIsMutuallyExclusive(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		wantStatus int
	}{
		{name: "websocket 关闭公网端点", mode: "websocket", wantStatus: http.StatusNotFound},
		{name: "未配置安全关闭公网端点", mode: "", wantStatus: http.StatusNotFound},
		{name: "非法配置安全关闭公网端点", mode: "both", wantStatus: http.StatusNotFound},
		{name: "webhook 显式开放并校验 token", mode: "webhook", wantStatus: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newPublishTestEnv(t)
			t.Setenv(feishuCallbackModeEnv, tc.mode)
			mux := http.NewServeMux()
			registerConfiguredFeishuCallbackRoute(mux, env.pipeline)
			rec := postCardCallback(mux, `{}`)
			if rec.Code != tc.wantStatus {
				t.Fatalf("mode=%q status=%d want=%d body=%s", tc.mode, rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestFeishuReviewerAllowlistFailClosed Webhook 与长连接共用 reviewCardAction：
// 非白名单、空白名单与非法 open_id 均默认拒绝。
func TestFeishuReviewerAllowlistFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, allowlist, operator string
	}{
		{name: "非白名单成员", allowlist: "ou_allowed", operator: "ou_intruder"},
		{name: "空白名单", allowlist: "", operator: "ou_group_member"},
		{name: "非法 open_id 不能注入卡片", allowlist: `"><at id=all>`, operator: `"><at id=all>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newPublishTestEnv(t)
			t.Setenv("FEISHU_REVIEWER_OPEN_IDS", tc.allowlist)
			job, _, err := env.pipeline.submit(testPublishSourceURL)
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			raw := json.RawMessage(fmt.Sprintf(`{"action":"approve","job_id":%d}`, job.ID))
			out := env.pipeline.reviewCardAction(context.Background(), tc.operator, raw)
			if out.toastType != "error" || !strings.Contains(out.toastContent, "权限") {
				t.Fatalf("未授权审核应回 error 权限提示，实际 %+v", out)
			}
			got, _ := env.store.byID(job.ID)
			if got.Stage != publishStageAwaitingReview {
				t.Fatalf("未授权审核不得改状态，实际 %q", got.Stage)
			}
			if _, updateN := env.ft.counts(); updateN != 0 {
				t.Fatalf("未授权审核触发了公开请求：%d", updateN)
			}
		})
	}
}

func TestReviewVersionKeepsLegacyCardsCompatibleAndVersionsNewContent(t *testing.T) {
	legacy := publishJob{ID: 7, RiskLevel: publishRiskPass}
	if got := publishReviewCardVersion(legacy); !strings.HasPrefix(got, "risk-v2:7:") {
		t.Fatalf("legacy review version=%q", got)
	}
	versioned := legacy
	versioned.ContentHash = "abc"
	versioned.ContentVersion = 1
	versioned.ContentFrozenVersion = 1
	if got := publishReviewCardVersion(versioned); !strings.HasPrefix(got, "review-v3:7:") {
		t.Fatalf("versioned review version=%q", got)
	}
}

func postCardCallback(mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/feishu/card-callback", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// cardActionBody card.action.trigger 事件体（形态照 cardkit 规范 04 §4.1：
// verification token 在 header.token，审核人在 event.operator.open_id）。
func cardActionBody(token, action string, jobID int64, reviewVersions ...string) string {
	reviewVersion := "obsolete-test-version"
	if len(reviewVersions) > 0 {
		reviewVersion = reviewVersions[0]
	}
	return fmt.Sprintf(`{
		"schema": "2.0",
		"header": {"event_type": "card.action.trigger", "token": %q},
		"event": {
			"operator": {"open_id": "ou_reviewer_1"},
			"token": "c-cardtoken",
			"action": {"tag": "button", "value": {"action": %q, "job_id": %d, "review_version": %q}},
			"context": {"open_chat_id": "oc_review_chat"}
		}
	}`, token, action, jobID, reviewVersion)
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
// 核心：3 秒响应只做 CAS 接受并回「发布中」卡，不等待 FoodTalks；后台 worker
// 随后落 published，UpdateNews 收 publishStatus=1，审核人留痕保持。
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
	var queued []func()
	env.pipeline.spawn = func(f func()) { queued = append(queued, f) }
	ready, _ := env.store.byID(job.ID)

	rec := postCardCallback(mux, cardActionBody(testFeishuVerificationToken, feishuCardActionApprove, job.ID, publishReviewCardVersion(ready)))
	if rec.Code != http.StatusOK {
		t.Fatalf("approve 回调应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStagePublicizing || len(queued) != 1 {
		t.Fatalf("回调返回时应 publicizing + 1 个后台 worker，实际 stage=%q queued=%d", got.Stage, len(queued))
	}
	if got.ReviewedBy != "ou_reviewer_1" || got.ReviewedAt == "" {
		t.Fatalf("审核留痕未落库：reviewed_by=%q reviewed_at=%q", got.ReviewedBy, got.ReviewedAt)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("回调返回前不得等待/调用 FoodTalks，实际 UpdateNews=%d", updateN)
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
	if resp.Toast.Type != "success" || resp.Toast.Content != "已受理，正在发布" {
		t.Fatalf("toast 不符：%+v", resp.Toast)
	}
	if resp.Card.Type != "raw" || resp.Card.Data["schema"] != "2.0" {
		t.Fatalf("响应应带 raw 进度卡：%+v", resp.Card)
	}
	header, _ := resp.Card.Data["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Fatalf("approve 进度卡 header 应为 blue，实际 %v", header["template"])
	}
	for _, want := range []string{"发布中", "ou_reviewer_1", "capsule", "后台正在发布", "最终结果以随后终态卡为准"} {
		if !strings.Contains(body, want) {
			t.Fatalf("终态卡应包含 %q，实际响应：%s", want, body)
		}
	}
	if containsEmoji(body) {
		t.Fatalf("进度卡含 emoji（cardkit 红线）：%s", body)
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

// TestFeishuCallbackRejectTerminates 卡片「驳回」置终态 rejected：不触发任何 FoodTalks
// 请求（forbidPublish 守卫全程在场），响应带红终态卡 + 留痕。
func TestFeishuCallbackRejectTerminates(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newCardCallbackMux(t, env)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	ready, _ := env.store.byID(job.ID)
	rec := postCardCallback(mux, cardActionBody(testFeishuVerificationToken, feishuCardActionReject, job.ID, publishReviewCardVersion(ready)))
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
	ready, _ := env.store.byID(job.ID)
	if rec := postCardCallback(mux, cardActionBody(testFeishuVerificationToken, feishuCardActionApprove, job.ID, publishReviewCardVersion(ready))); rec.Code != http.StatusOK {
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

func TestReviewCardRiskChangeExpiresOldVersion(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	job, _, err := env.store.create("site:risk-version", testPublishSourceURL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	oldJob, err := env.store.update(job.ID, func(j *publishJob) {
		j.Stage = publishStageAwaitingReview
		j.RiskLevel = publishRiskWarning
		j.RiskItems = []publishRiskItem{{
			Category: publishRiskCategoryDataSource, Level: publishRiskWarning,
			Evidence: "旧证据", Reason: "旧说明",
		}}
		j.RiskNotes = "旧风险结论"
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	oldVersion := publishReviewCardVersion(oldJob)
	if _, err := env.store.update(job.ID, func(j *publishJob) {
		j.RiskLevel = publishRiskBlock
		j.RiskItems = []publishRiskItem{{
			Category: publishRiskCategoryRepublication, Level: publishRiskBlock,
			Evidence: "新证据", Reason: "授权硬缺口",
		}}
		j.RiskNotes = "新风险结论"
	}); err != nil {
		t.Fatalf("update risk: %v", err)
	}
	raw := json.RawMessage(fmt.Sprintf(
		`{"action":"approve","job_id":%d,"review_version":%q}`, job.ID, oldVersion))
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", raw)
	if out.toastType != "error" || !strings.Contains(out.toastContent, "过期") {
		t.Fatalf("风险变化后旧卡必须失效，实际 %+v", out)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview || got.ReviewAction != "" {
		t.Fatalf("过期卡不得改变任务：stage=%q action=%q", got.Stage, got.ReviewAction)
	}
}

// TestReviewPendingCardContract 待审核卡 JSON 契约（cardkit 红线，字符串断言）：
// 无 emoji、「通过并发布」「驳回」都带 confirm 二次确认、schema 2.0 不含 action/note
// 容器、callback value 带 action+job_id、update_multi 开启（群内同步刷新的前提）。
// 2026-07-10「预览链接」+ M5 扩展：「预览效果」使用 HMAC 签名链接。
// 2026-07-15 收紧首屏：封面/标题/原生标签/摘要/预览链接归入同一个白底横向
// 媒体容器；元信息压成两行，排版与风险说明默认折叠，不再叠灰色卡片。
func TestReviewPendingCardContract(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	t.Setenv("WECHAT_UPLOAD_ORIGIN", "https://preview.test")
	job := publishJob{
		ID: 7, Title: "示例标题", SourceName: "示例公众号",
		SourceURL: "https://mp.weixin.qq.com/s/AbC", Stage: publishStageAwaitingReview,
		FoodTalksNewsID: 7240, UpdatedAt: "2026-07-10T07:00:00Z",
		Summary: "AI 摘要一段。", Tags: []string{"沃集鲜", "沃尔玛", "药食同源"}, Author: "Mote莫特",
		AINotes: "封面选第一张图。正文格式规范。摘要取自导语。", RiskLevel: publishRiskPass,
	}
	card := buildReviewPendingCard(job, "img_v3_test_cover_key")
	raw, err := json.Marshal(card)
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
		"通过并发布", "驳回", "预览效果", "待审核",
		"不可逆", // confirm 文案必须明说发布不可逆
		`"action":"approve"`, `"action":"reject"`, `"job_id":7`,
		`"primary_filled"`, `"danger"`, "done_outlined",
		"示例标题", "示例公众号", "#7",
		// 预览键 open_url 指向带 HMAC 签名的预览链接（域名走 WECHAT_UPLOAD_ORIGIN）。
		"https://preview.test/preview/publish/7?t=" + publishPreviewSig(7),
		// 原文与预览都降为主内容里的文字链接。
		"公众号原文", job.SourceURL,
		// M5 AI 预填参数 + 优化说明段。
		"Mote莫特", "沃集鲜", "沃尔玛", "药食同源", "AI 摘要一段。",
		"排版说明", "1. **封面**：选第一张图。",
		// 2026-07-10 用户追加：封面图直接在卡片呈现（img 元素只认 img_key）+
		// 2026-07-14 收进横向媒体容器，左侧封面、右侧标题/标签/摘要/预览键。
		`"tag":"img"`, `"img_key":"img_v3_test_cover_key"`,
		`"scale_type":"fit_horizontal"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("审核卡缺少 %q：%s", want, s)
		}
	}
	body, ok := card["body"].(map[string]any)
	if !ok {
		t.Fatalf("审核卡 body 结构错误：%#v", card["body"])
	}
	elements, ok := body["elements"].([]any)
	if !ok || len(elements) < 2 {
		t.Fatalf("审核卡 body.elements 结构错误：%#v", body["elements"])
	}
	hero, ok := elements[0].(map[string]any)
	if !ok || hero["tag"] != "column_set" || hero["flex_mode"] != "none" {
		t.Fatalf("封面、标题、摘要必须位于同一个横向媒体容器：%#v", elements[0])
	}
	if _, hasBackground := hero["background_style"]; hasBackground {
		t.Fatalf("主内容应直接落在白底连续阅读流，不再叠灰色卡片：%#v", hero)
	}
	columns, ok := hero["columns"].([]any)
	if !ok || len(columns) != 2 {
		t.Fatalf("有封面时媒体容器必须是图片列 + 文案列：%#v", hero["columns"])
	}
	coverColumn, _ := columns[0].(map[string]any)
	coverElements, _ := coverColumn["elements"].([]any)
	if coverColumn["width"] != "104px" || len(coverElements) != 1 {
		t.Fatalf("封面列尺寸或结构错误：%#v", coverColumn)
	}
	cover, _ := coverElements[0].(map[string]any)
	if cover["tag"] != "img" || cover["scale_type"] != "fit_horizontal" {
		t.Fatalf("封面必须按左栏宽度等比缩放并完整显示：%#v", cover)
	}
	if _, fixedSize := cover["size"]; fixedSize {
		t.Fatalf("fit_horizontal 封面不得再带固定 size，否则客户端可能继续裁切：%#v", cover)
	}
	copyColumn, _ := columns[1].(map[string]any)
	copyElements, _ := copyColumn["elements"].([]any)
	if len(copyElements) != 4 {
		t.Fatalf("文案列必须依次包含标题、标签、摘要、预览键：%#v", copyColumn)
	}
	title, _ := copyElements[0].(map[string]any)
	tags, _ := copyElements[1].(map[string]any)
	summary, _ := copyElements[2].(map[string]any)
	links, _ := copyElements[3].(map[string]any)
	wantTags := "<text_tag color='blue'>沃集鲜</text_tag> <text_tag color='blue'>沃尔玛</text_tag> <text_tag color='blue'>药食同源</text_tag>"
	summaryText, _ := summary["text"].(map[string]any)
	if title["content"] != "**示例标题**" || tags["content"] != wantTags || summaryText["content"] != "AI 摘要一段。" || summaryText["lines"] != 3 {
		t.Fatalf("媒体容器文案顺序应为标题→标签→摘要：title=%#v tags=%#v summary=%#v", title, tags, summary)
	}
	if links["tag"] != "markdown" || links["text_size"] != "notation" || !strings.Contains(fmt.Sprint(links["content"]), publishPreviewURL(job.ID)) {
		t.Fatalf("媒体容器必须带紧凑的系统签名预览链接：%#v", links)
	}
	infoRaw, _ := json.Marshal(elements[1])
	for _, duplicate := range []string{"示例标题", "AI 摘要一段。", "沃集鲜", "沃尔玛", "药食同源"} {
		if strings.Contains(string(infoRaw), duplicate) {
			t.Fatalf("标题、标签、摘要只应在媒体容器出现一次（重复 %q）：%s", duplicate, infoRaw)
		}
	}
	if strings.Contains(s, "去后台编辑这篇文章") || strings.Contains(s, "admin-we") {
		t.Fatalf("审核卡不得暴露管理后台入口：%s", s)
	}
	if n := strings.Count(s, "预览效果"); n != 1 {
		t.Fatalf("预览键应只在媒体容器出现一次（实际 %d）：%s", n, s)
	}
	if strings.Contains(s, `"background_style":"grey-50"`) || strings.Contains(s, `"background_style":"grey-100"`) {
		t.Fatalf("待审核卡不得再堆叠灰色背景卡片：%s", s)
	}
	if !strings.Contains(s, `"tag":"collapsible_panel"`) || !strings.Contains(s, `"expanded":false`) {
		t.Fatalf("排版与风险说明应默认折叠：%s", s)
	}
	// 无封面 img_key 时卡片不得残留空 img 元素。
	rawNoCover, _ := json.Marshal(buildReviewPendingCard(job, ""))
	if strings.Contains(string(rawNoCover), `"tag":"img"`) {
		t.Fatalf("无 img_key 不得渲染 img 元素：%s", rawNoCover)
	}
	if strings.Contains(s, "查看原文") {
		t.Fatalf("「查看原文」按钮应已被「预览效果」取代：%s", s)
	}
	for _, banned := range []string{`"tag":"action"`, `"tag":"note"`} {
		if strings.Contains(s, banned) {
			t.Fatalf("schema 2.0 禁用容器 %s（会报 200861）：%s", banned, s)
		}
	}
}

func TestReviewCardEscapesExternalMarkdownFields(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	malicious := `<at id=all></at> [恶链](https://evil.example) **粗体** # 标题`
	job := publishJob{
		ID: 9, Stage: publishStageAwaitingReview, FoodTalksNewsID: 7240,
		SourceURL: "https://mp.weixin.qq.com/s/SafeSlug", RiskLevel: publishRiskWarning,
		Title: malicious, SourceName: malicious, Author: malicious, Summary: malicious,
		Tags: []string{malicious}, AINotes: malicious, RiskNotes: malicious,
		RiskItems: []publishRiskItem{{
			Category: publishRiskCategoryDataSource, Level: publishRiskWarning,
			Evidence: malicious, Reason: malicious,
		}},
	}
	card := fmt.Sprint(buildReviewPendingCard(job, ""))
	for _, banned := range []string{"<at id=all>", "https://evil.example", "[恶链]", "**粗体**", "# 标题"} {
		if strings.Contains(card, banned) {
			t.Fatalf("外部文本不得注入 Feishu markdown %q：%s", banned, card)
		}
	}
	for _, fixed := range []string{"公众号原文", job.SourceURL, "预览效果", "通过并发布"} {
		if !strings.Contains(card, fixed) {
			t.Fatalf("安全转义不得破坏代码生成的固定内容 %q：%s", fixed, card)
		}
	}
}

func TestReviewCardFormatsAINotesAsNumberedChecks(t *testing.T) {
	job := publishJob{AINotes: "【双模型】对比模型失败（ARK 调用失败（HTTP 400）：The parameter messages.content.image_url），采用主模型方案。\n" +
		"封面选首张 HBAF 上海门店实景图，有清晰品牌标识。摘要采用提问式悬念写法，紧扣文章核心设问。原文存在两处问题：HABF 应改为 HBAF，文末引语未完结。"}
	block := reviewCardAINotesBlock(job)
	content := fmt.Sprint(block["content"])
	for _, want := range []string{
		"双模型降级", "对比模型不支持图片输入", "已采用主模型结果",
		"1. **封面**：", "2. **摘要**：", "3. **原文核对**：",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("编号排版缺少 %q：%s", want, content)
		}
	}
	for _, internal := range []string{"HTTP 400", "messages.content.image_url", "ARK 调用失败"} {
		if strings.Contains(content, internal) {
			t.Fatalf("审核卡不得暴露内部模型错误 %q：%s", internal, content)
		}
	}
}

func TestReviewCardHumanizesSystemRisk(t *testing.T) {
	job := publishJob{
		RiskLevel: publishRiskWarning,
		RiskItems: []publishRiskItem{{
			Category: publishRiskCategorySystem,
			Level:    publishRiskWarning,
			Evidence: "风险模型未能产出可验证的结构化结果",
			Reason:   `风险审核接口不可达: Post "https://ark.example/api/v3/responses": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
		}},
		RiskNotes: `风险检查未完成，必须人工核对。原因：https://ark.example Client.Timeout`,
	}
	block := reviewCardRiskBlock(job)
	content := fmt.Sprint(block["content"])
	for _, want := range []string{
		"1. **风险检查异常**", "发现：风险模型未返回可验证的结构化结果。",
		"处理建议：请人工核对功效表达、转载授权、竞品评价和数据来源后再决定。",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("风险说明缺少 %q：%s", want, content)
		}
	}
	for _, internal := range []string{"https://ark.example", "Client.Timeout", "context deadline exceeded"} {
		if strings.Contains(content, internal) {
			t.Fatalf("审核卡不得暴露内部风险错误 %q：%s", internal, content)
		}
	}
}

func TestReviewCardEscapesSourceURLMarkdownTarget(t *testing.T) {
	rawURL := "https://mp.weixin.qq.com/s/SafeSlug)[恶链](mailto:attacker@example.com)"
	safeURL := reviewCardSafeURL(rawURL)
	if safeURL == "" {
		t.Fatalf("合法 HTTP URL 应可安全编码")
	}
	if strings.Contains(safeURL, ")[") || strings.Contains(safeURL, "](mailto:") ||
		!strings.Contains(safeURL, "%29%5B") {
		t.Fatalf("markdown 链接目标未阻止闭链注入：raw=%q safe=%q", rawURL, safeURL)
	}
	job := publishJob{
		ID: 10, Stage: publishStageAwaitingReview, FoodTalksNewsID: 7240,
		SourceURL: rawURL, RiskLevel: publishRiskPass, Title: "URL 注入测试",
	}
	card := fmt.Sprint(buildReviewPendingCard(job, ""))
	if strings.Contains(card, ")[恶链](mailto:") || strings.Contains(card, rawURL) {
		t.Fatalf("原始 SourceURL 不得直接拼入 Feishu markdown：%s", card)
	}
	if !strings.Contains(card, safeURL) {
		t.Fatalf("卡片应使用安全编码后的 SourceURL：%s", card)
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

// TestFeishuReviewerClosedWhenUnset 锁死当前发布安全合同：空白名单不得把审核权
// 扩散到整个群，任务必须保持 awaiting_review，且不能触发 FoodTalks。
func TestFeishuReviewerClosedWhenUnset(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	current, _ := env.store.byID(job.ID)
	raw := json.RawMessage(fmt.Sprintf(`{"action":"approve","job_id":%d,"review_version":%q}`, job.ID, publishReviewCardVersion(current)))
	out := env.pipeline.reviewCardAction(context.Background(), "ou_any_group_member", raw)
	if out.toastType != "error" || !strings.Contains(out.toastContent, "权限") {
		t.Fatalf("未配置白名单时必须拒绝审核，实际 %+v", out)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview || got.ReviewedBy != "" {
		t.Fatalf("空白名单不得改变任务：stage=%q by=%q", got.Stage, got.ReviewedBy)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("空白名单审核不得触发公开请求：%d", updateN)
	}
}
