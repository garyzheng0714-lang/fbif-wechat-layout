package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testLongToutiaoTitle = "这家老字号把茶做成零食，166年老店只开6家，年营收1.4亿"

// createLongTitleDistributionJob 一篇已发布原创，标题超过头条号 20 字（网站最新稿同标题）。
func createLongTitleDistributionJob(t *testing.T, env *publishTestEnv, key string) publishJob {
	t.Helper()
	job := createPublishedDistributionJobWithKey(t, env, key, "FBIF食品饮料创新")
	job, err := env.store.update(job.ID, func(current *publishJob) {
		current.Title = testLongToutiaoTitle
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: testLongToutiaoTitle, CoverImg: job.CoverURL,
		Content: testDistributionLiveHTML, Summary: job.Summary, SourceName: "FBIF食品饮料创新",
		PublishStatus: foodTalksPublishStatusPublished,
	})
	return job
}

func fixActionValue(t *testing.T, job publishJob, slot int) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"action": feishuCardActionDistributeFix, "job_id": job.ID, "slot": slot,
		"distribution_version": publishExternalDistributionCardVersion(job),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestExternalPreflightHoldsLongTitleUntilFixedInCard 头条号标题超限（2026-10-12 用户：「标题超限……
// 应该支持在群里 @ 对应的人，提醒去更换标题」「能否直接在卡片里改好」）：头条号先不推、记下问题与
// 建议，百家号照推；卡片表单提交改好的标题后马上推，推出去的头条号标题就是人改的那一句。
func TestExternalPreflightHoldsLongTitleUntilFixedInCard(t *testing.T) {
	setTestYunsuoConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	stubYunsuoImageSize(t, map[string][2]int{})
	env := newPublishTestEnv(t)
	fake := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	env.pipeline.externalSuggest = func(_ context.Context, kind, current string, min, max int) (string, error) {
		return "老茶铺把茶做成零食，年营收1.4亿", nil
	}
	job := createLongTitleDistributionJob(t, env, "site:long-title")

	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("百家号能推，整体请求不应失败：%v", err)
	}
	if fmt.Sprint(fake.pushed) != fmt.Sprint([][]string{{testYunsuoBaijiaID}}) {
		t.Fatalf("头条号标题超限应先不推：%v", fake.pushed)
	}
	got := mustJob(t, env, job.ID)
	draft, _ := externalDraftFor(got, testYunsuoToutiaoID)
	title := externalIssueFields(draft.Issues)[publishExternalIssueTitle]
	if title.Max != 20 || title.Length != runeLen(testLongToutiaoTitle) || title.Current != testLongToutiaoTitle {
		t.Fatalf("应记下标题问题与现状：%+v", draft)
	}
	if draft.SuggestedTitle != "老茶铺把茶做成零食，年营收1.4亿" || draft.SuggestedBy != "ai" || draft.PreparedFor != draft.CheckedAt {
		t.Fatalf("后台应准备好 AI 建议：%+v", draft)
	}
	if !externalJobNeedsFix(got) {
		t.Fatalf("应标记为待修改")
	}

	// 超出上限的修改当场拒绝，并说清还要删几个字。
	tooLong := "老茶铺把茶做成零食，年营收一点四亿元人民币啊"
	out := env.pipeline.reviewCardActionWithForm(context.Background(), "ou_reviewer_1", "", fixActionValue(t, got, 0),
		map[string]any{fmt.Sprintf("t_%d_0", got.ID): tooLong})
	if out.toastType != "error" || !strings.Contains(out.toastContent, fmt.Sprintf("头条号标题最多 20 字，现在 %d 字，还要删 %d 字", runeLen(tooLong), runeLen(tooLong)-20)) {
		t.Fatalf("超限修改应拒绝并说明：%+v", out)
	}

	fixed := "老茶铺把茶做成零食，年营收过亿"
	out = env.pipeline.reviewCardActionWithForm(context.Background(), "ou_reviewer_1", "", fixActionValue(t, got, 0),
		map[string]any{fmt.Sprintf("t_%d_0", got.ID): fixed})
	if out.toastType != "success" || out.toastContent != "已保存，正在同步至头条号" {
		t.Fatalf("改好后应受理：%+v", out)
	}
	if fmt.Sprint(fake.pushed) != fmt.Sprint([][]string{{testYunsuoBaijiaID}, {testYunsuoToutiaoID}}) {
		t.Fatalf("改好后只推头条号：%v", fake.pushed)
	}
	if fake.payload.ShortTitle != fixed {
		t.Fatalf("头条号拿短标题，必须是人改的那一句：%+v", fake.payload)
	}
	got = mustJob(t, env, job.ID)
	lane := mustLane(t, got, testYunsuoToutiaoID)
	if lane.Title != fixed || lane.RequestedBy != "ou_reviewer_1" {
		t.Fatalf("lane 应记平台标题与操作人：%+v", lane)
	}
	if draft, _ := externalDraftFor(got, testYunsuoToutiaoID); len(draft.Issues) != 0 || draft.Title != fixed || draft.EditedBy != "ou_reviewer_1" {
		t.Fatalf("draft 应记下修改且没有待改项：%+v", draft)
	}
	if externalJobNeedsFix(got) {
		t.Fatalf("推出去后不再待修改")
	}
	// 推过以后表单失效：旧卡再提交不会重推。
	out = env.pipeline.reviewCardActionWithForm(context.Background(), "ou_reviewer_1", "", fixActionValue(t, got, 0),
		map[string]any{fmt.Sprintf("t_%d_0", got.ID): "再改一次标题也不会重推"})
	if out.toastType == "success" || fake.pushCalls != 2 {
		t.Fatalf("已推的平台不得再改再推：%+v pushes=%d", out, fake.pushCalls)
	}
}

// TestExternalAutoSkipsPlatformWaitingForFix auto 账号卡在待修改：自动外发不反复去网站取稿、不占名额；
// 改好后挂回自动外发并推出。
func TestExternalAutoSkipsPlatformWaitingForFix(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	stubYunsuoImageSize(t, map[string][2]int{})
	env := newPublishTestEnv(t)
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	job := createLongTitleDistributionJob(t, env, "site:auto-long-title")
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		current.ExternalDistributionAutoPending = publishExternalAutoKindPublish
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.runExternalAutoDistribution()
	if fake.pushCalls != 0 {
		t.Fatalf("待修改不得推送：%v", fake.pushed)
	}
	got := mustJob(t, env, job.ID)
	if got.ExternalDistributionAutoPending == "" || !externalDraftBlocked(got, testYunsuoToutiaoID) {
		t.Fatalf("待办应保留、头条号应记为待修改：pending=%q drafts=%+v", got.ExternalDistributionAutoPending, got.ExternalDistributionDrafts)
	}
	checkedAt := mustDraft(t, got, testYunsuoToutiaoID).CheckedAt
	clock.set("2026-10-12 10:30")
	env.pipeline.runExternalAutoDistribution()
	if again := mustDraft(t, mustJob(t, env, job.ID), testYunsuoToutiaoID); again.CheckedAt != checkedAt || fake.pushCalls != 0 {
		t.Fatalf("文章没变时不重查、不推：%+v", again)
	}
	if used := externalDistributionQuotaUsed(t, env, testYunsuoToutiaoID); used != 0 {
		t.Fatalf("待修改不占名额，实际 %d", used)
	}

	fixed := "老茶铺把茶做成零食，年营收过亿"
	if _, err := env.pipeline.submitExternalDraftEdit(job.ID, 0, externalDraftEdit{Title: &fixed}, "ou_editor"); err != nil {
		t.Fatal(err)
	}
	if fake.pushCalls != 1 || fake.payload.ShortTitle != fixed {
		t.Fatalf("改好后应按自动外发推出：calls=%d payload=%+v", fake.pushCalls, fake.payload)
	}
	if lane := mustLane(t, mustJob(t, env, job.ID), testYunsuoToutiaoID); lane.RequestedBy != publishExternalAutoActor {
		t.Fatalf("auto 账号仍记系统发起：%+v", lane)
	}
}

func mustDraft(t *testing.T, job publishJob, accountID string) publishExternalDraft {
	t.Helper()
	draft, ok := externalDraftFor(job, accountID)
	if !ok {
		t.Fatalf("任务 #%d 缺少账号 %s 的 draft：%+v", job.ID, accountID, job.ExternalDistributionDrafts)
	}
	return draft
}

// externalDistributionQuotaUsed 这个平台今天已用的自动外发名额（建了 lane 的才算）。
func externalDistributionQuotaUsed(t *testing.T, env *publishTestEnv, accountID string) int {
	t.Helper()
	jobs, err := env.store.list(100)
	if err != nil {
		t.Fatal(err)
	}
	used := 0
	for _, job := range jobs {
		if _, ok := externalLaneFor(job, accountID); ok {
			used++
		}
	}
	return used
}

type sentReminder struct {
	chatID, existing string
	card             string
}

// TestExternalReminderMentionsOwnersInThread 待修改时在审核卡下回复一条提醒并 @ 分发负责人
// （2026-10-12 用户裁定：@ 固定负责人）；问题变了原位更新、不再 @；改好后原位改成「已改好」；
// 改好后又出问题才开新一轮、重新 @。
func TestExternalReminderMentionsOwnersInThread(t *testing.T) {
	setTestYunsuoConfig(t)
	// 不配应用凭证：审核卡 outbox 在本地就失败，不会打到真飞书；提醒走注入的发送器。
	t.Setenv(publishNotificationAppsEnv, "")
	t.Setenv("AUTH_SESSION_SECRET", "test-session-secret")
	stubYunsuoImageSize(t, map[string][2]int{})
	env := newPublishTestEnv(t)
	env.pipeline.deliverySettings = newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "delivery.json"))
	if err := env.pipeline.deliverySettings.write(publishDeliverySettings{
		AppProfileID: "review-app", ChatIDs: []string{"oc_review"},
		DistributionOwners:    []publishDistributionOwner{{OpenID: "ou_owner_1", Name: "王编辑"}},
		DistributionOwnersApp: "review-app",
	}); err != nil {
		t.Fatal(err)
	}
	var sent []sentReminder
	env.pipeline.externalReminderSender = func(_ context.Context, job publishJob, chatID, existing string, card map[string]any) (string, error) {
		sent = append(sent, sentReminder{chatID: chatID, existing: existing, card: panelJSON(t, card)})
		if existing != "" {
			return existing, nil
		}
		return fmt.Sprintf("om_reminder_%d", len(sent)), nil
	}
	fake := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	job := createLongTitleDistributionJob(t, env, "site:reminder")
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		current.ReviewDeliveryAppID = "review-app"
		current.ReviewDeliveryChatIDs = []string{"oc_review"}
		current.ReviewCardMsgIDs = map[string]string{"oc_review": "om_review_card"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(mustJob(t, env, job.ID))); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].existing != "" || sent[0].chatID != "oc_review" {
		t.Fatalf("应在审核群回复一条新提醒：%+v", sent)
	}
	for _, want := range []string{"<at id=ou_owner_1></at>", fmt.Sprintf("**#%d ", job.ID), "同步到其他平台前要改：",
		fmt.Sprintf("- 头条号标题超出 %d 字（最多 20 字，现在 %d 字）", runeLen(testLongToutiaoTitle)-20, runeLen(testLongToutiaoTitle)),
		"在审核卡的「分发到其他平台」里改，改好自动同步。", "[在侧边栏改](https://applink.feishu.cn/client/web_url/open?mode=sidebar-semi"} {
		if !strings.Contains(sent[0].card, want) {
			t.Fatalf("提醒缺少 %q: %s", want, sent[0].card)
		}
	}
	got := mustJob(t, env, job.ID)
	if r := got.ExternalDistributionReminder; r.State != "" || r.MessageIDs["oc_review"] != "om_reminder_1" || r.Want != publishExternalReminderOpen {
		t.Fatalf("发完应记下消息 id：%+v", r)
	}

	// 改好：同一条提醒原位改成「已改好」，不新发、不再 @。
	fixed := "老茶铺把茶做成零食，年营收过亿"
	if _, err := env.pipeline.submitExternalDraftEdit(job.ID, 0, externalDraftEdit{Title: &fixed}, "ou_owner_1"); err != nil {
		t.Fatal(err)
	}
	last := sent[len(sent)-1]
	if last.existing != "om_reminder_1" || !strings.Contains(last.card, "已改好，正在同步到其他平台。") || strings.Contains(last.card, "<at id=") {
		t.Fatalf("改好后应原位更新提醒：%+v", sent)
	}
	if r := mustJob(t, env, job.ID).ExternalDistributionReminder; r.State != "" || r.Want != publishExternalReminderResolved {
		t.Fatalf("提醒应结束：%+v", r)
	}

	// 没设负责人：不发提醒，也不算错误。
	if err := env.pipeline.deliverySettings.write(publishDeliverySettings{AppProfileID: "review-app", ChatIDs: []string{"oc_review"}}); err != nil {
		t.Fatal(err)
	}
	before := len(sent)
	other := createLongTitleDistributionJob(t, env, "site:reminder-no-owner")
	if _, err := env.store.update(other.ID, func(current *publishJob) {
		current.ReviewDeliveryAppID = "review-app"
		current.ReviewDeliveryChatIDs = []string{"oc_review"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestExternalDistribution(other.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(mustJob(t, env, other.ID))); err != nil &&
		!errors.Is(err, errExternalLanesNeedFix) {
		t.Fatal(err)
	}
	if r := mustJob(t, env, other.ID).ExternalDistributionReminder; len(sent) != before || r.State != "" || r.Error != "" || r.Skipped == "" {
		t.Fatalf("没设负责人不发提醒：sent=%d reminder=%+v", len(sent)-before, r)
	}
}

// TestExternalReminderFailureCountsInMonitoring 提醒发不出去：按通知退避重试，超过次数进死信并计入监控。
func TestExternalReminderFailureCountsInMonitoring(t *testing.T) {
	job := publishJob{ExternalDistributionReminder: publishExternalReminder{State: publishExternalCardDeadLetter, Error: "飞书回复消息失败"}}
	var status publishMonitoringStatus
	updatePublishMonitoringNotifications(&status, job, 60)
	if status.NotificationDeadLetters != 1 || status.NotificationErrors != 1 {
		t.Fatalf("提醒死信应计入监控：%+v", status)
	}
	job.ExternalDistributionReminder = publishExternalReminder{State: publishExternalCardPending}
	status = publishMonitoringStatus{}
	updatePublishMonitoringNotifications(&status, job, 60)
	if status.NotificationPending != 1 {
		t.Fatalf("待发提醒应计入监控：%+v", status)
	}
}

func newDistributionEditMux(t *testing.T, env *publishTestEnv) (*http.ServeMux, *authService) {
	t.Helper()
	t.Setenv("AUTH_SESSION_SECRET", "test-session-secret")
	service := newTestAuthService(t, &fakeFeishuProvider{})
	mux := http.NewServeMux()
	registerPublishRoutes(mux, env.pipeline, service)
	return mux, service
}

func doEditReq(mux *http.ServeMux, method, target, token string, body []byte, contentType string) *httptest.ResponseRecorder {
	req := requestWithSession(method, target, token, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestExternalDistributionSidebarEditAPI 侧边栏改稿页（2026-10-12 用户：「做一个链接……在飞书应用内
// 打开，是侧边栏……就可以上传图片，或者改东西……修改后状态再同步回卡片」）：必须飞书登录且持有这篇
// 的签名链接（或是规则管理员）；改标题、上传封面都走同一个修改入口，改好即推。
func TestExternalDistributionSidebarEditAPI(t *testing.T) {
	setTestYunsuoConfig(t)
	stubYunsuoImageSize(t, map[string][2]int{"https://cdn.example.com/a.jpg": {300, 200}})
	env := newPublishTestEnv(t)
	fake := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	var stored [][]byte
	env.pipeline.externalCoverStore = func(_ context.Context, name string, body []byte) (string, error) {
		stored = append(stored, body)
		return "https://cdn.example.com/uploaded/" + name, nil
	}
	mux, service := newDistributionEditMux(t, env)
	job := createLongTitleDistributionJob(t, env, "site:sidebar")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/publish/jobs/%d/external-distribution/", job.ID)
	signed := "?t=" + externalDistributionEditSig(job.ID)

	editor := mustSessionForUser(t, service, authUser{ID: "feishu:ou_editor", Name: "王编辑", AuthMode: authModeFeishu, Status: authUserActive, FeishuOpenID: "ou_editor"})
	if rec := doEditReq(mux, http.MethodGet, base+"draft"+signed, "", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，实际 %d", rec.Code)
	}
	if rec := doEditReq(mux, http.MethodGet, base+"draft", editor, nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("没有签名链接的普通成员应 403，实际 %d", rec.Code)
	}
	if rec := doEditReq(mux, http.MethodGet, base+"draft?t="+externalDistributionEditSig(job.ID+1), editor, nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("别篇的签名不能用，实际 %d", rec.Code)
	}
	rec := doEditReq(mux, http.MethodGet, base+"draft"+signed, editor, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("签名链接 + 飞书登录应 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	var view struct {
		Platforms []externalEditPlatform `json:"platforms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Platforms) != 2 {
		t.Fatalf("应列出两个平台：%s", rec.Body.String())
	}
	toutiao, baijia := view.Platforms[0], view.Platforms[1]
	if toutiao.Status != "待修改" || !toutiao.Editable || !toutiao.TitleIssue || toutiao.TitleMax != 20 || toutiao.CoverIssue == "" ||
		toutiao.CoverMinWidth != 452 || toutiao.Title != testLongToutiaoTitle {
		t.Fatalf("头条号应待修改并给出上限：%+v", toutiao)
	}
	if baijia.Status != "已发布" || baijia.Editable {
		t.Fatalf("百家号已推，不能再改：%+v", baijia)
	}

	// 只改标题：封面问题还在，仍待修改、不推。
	put := func(body string) *httptest.ResponseRecorder {
		return doEditReq(mux, http.MethodPut, base+"draft"+signed, editor, []byte(body), "application/json")
	}
	if rec := put(`{"slot":0,"title":"老茶铺把茶做成零食，年营收过亿"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"待修改"`) || fake.pushCalls != 1 {
		t.Fatalf("改完标题封面仍要改：%d %s pushes=%d", rec.Code, rec.Body.String(), fake.pushCalls)
	}
	if rec := put(`{"slot":0,"title":"这个标题足足有二十五个字远远超过头条号上限"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "头条号标题最多 20 字") {
		t.Fatalf("超限标题应拒绝：%d %s", rec.Code, rec.Body.String())
	}

	// 上传封面：太小的拒绝；够大的转存后作为头条号封面，问题清空即推。
	upload := func(data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("slot", "0")
		part, _ := form.CreateFormFile("file", "cover.png")
		_, _ = part.Write(data)
		_ = form.Close()
		return doEditReq(mux, http.MethodPost, base+"cover"+signed, editor, body.Bytes(), form.FormDataContentType())
	}
	if rec := upload(testPNG(t, 400, 300)); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "头条号封面至少 452×352") || len(stored) != 0 {
		t.Fatalf("太小的图应拒绝且不转存：%d %s", rec.Code, rec.Body.String())
	}
	if rec := upload([]byte("not an image")); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "只支持 JPG、PNG") {
		t.Fatalf("非图片应拒绝：%d %s", rec.Code, rec.Body.String())
	}
	rec = upload(testPNG(t, 900, 600))
	var uploaded struct {
		Uploaded externalEditImage `json:"uploaded"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &uploaded)
	if rec.Code != http.StatusOK || len(stored) != 1 || uploaded.Uploaded.Width != 900 || !strings.HasPrefix(uploaded.Uploaded.URL, "https://cdn.example.com/uploaded/distribution-cover-") {
		t.Fatalf("够大的图应转存并作为可选封面：%d %s", rec.Code, rec.Body.String())
	}
	if fake.pushCalls != 1 {
		t.Fatalf("上传只加可选封面，点保存才推：%d", fake.pushCalls)
	}
	if rec := put(`{"slot":0,"cover_url":"` + uploaded.Uploaded.URL + `"}`); rec.Code != http.StatusOK {
		t.Fatalf("选上传的图作封面应成功：%d %s", rec.Code, rec.Body.String())
	}
	if fake.pushCalls != 2 || fake.payload.Cover != uploaded.Uploaded.URL {
		t.Fatalf("封面改好后应推头条号并用上传的图：calls=%d payload=%+v", fake.pushCalls, fake.payload)
	}
	got := mustJob(t, env, job.ID)
	draft := mustDraft(t, got, testYunsuoToutiaoID)
	if !strings.HasPrefix(draft.EditedBy, "王编辑（ou_editor）") || len(draft.Issues) != 0 {
		t.Fatalf("应记下谁改的：%+v", draft)
	}
	// 推过以后不能再改。
	if rec := put(`{"slot":0,"title":"再改一次"}`); rec.Code != http.StatusConflict {
		t.Fatalf("已推的平台不能再改：%d %s", rec.Code, rec.Body.String())
	}
}

// TestDistributionOwnersFromReviewChatMembers 分发负责人只能从审核群成员里选，按审核应用存。
func TestDistributionOwnersFromReviewChatMembers(t *testing.T) {
	t.Setenv(publishNotificationAppsEnv, `[{"id":"review-app","name":"审核应用","app_id":"cli_review","app_secret":"secret"}]`)
	env := newPublishTestEnv(t)
	env.pipeline.deliverySettings = newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "delivery.json"))
	if err := env.pipeline.deliverySettings.write(publishDeliverySettings{AppProfileID: "review-app", ChatIDs: []string{"oc_review"}}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.distributionOwnerLister = func(_ context.Context, app, chat string) ([]publishDistributionOwner, error) {
		if app != "review-app" || chat != "oc_review" {
			return nil, fmt.Errorf("unexpected %s/%s", app, chat)
		}
		return []publishDistributionOwner{{OpenID: "ou_b", Name: "乙"}, {OpenID: "ou_a", Name: "甲"}}, nil
	}
	mux := newPublishTestMux(t, env)
	path := "/api/publish/delivery-settings/distribution-owners"
	if rec := doPublishReq(mux, http.MethodGet, path, "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("需要管理员：%d", rec.Code)
	}
	rec := doPublishReq(mux, http.MethodGet, path, "", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"candidates":[{"open_id":"ou_b","name":"乙"},{"open_id":"ou_a","name":"甲"}]`) &&
		!strings.Contains(rec.Body.String(), `"open_id":"ou_a"`) {
		t.Fatalf("应列出审核群成员：%d %s", rec.Code, rec.Body.String())
	}
	if rec := doPublishReq(mux, http.MethodPut, path, `{"open_ids":["ou_stranger"]}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("不在群里的人不能选：%d %s", rec.Code, rec.Body.String())
	}
	if rec := doPublishReq(mux, http.MethodPut, path, `{"open_ids":["ou_a"]}`, true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"owners":[{"open_id":"ou_a","name":"甲"}]`) {
		t.Fatalf("应保存负责人：%d %s", rec.Code, rec.Body.String())
	}
	if owners := env.pipeline.externalDistributionOwners("review-app"); len(owners) != 1 || owners[0].OpenID != "ou_a" {
		t.Fatalf("提醒应 @ 这位负责人：%+v", owners)
	}
	if owners := env.pipeline.externalDistributionOwners("another-app"); len(owners) != 0 {
		t.Fatalf("换了审核应用 open_id 就 @ 不到人，不能用：%+v", owners)
	}
	// 改审核群配置时，同一个应用下负责人保留。
	settings, _ := env.pipeline.deliverySettings.read()
	kept, err := publishDeliverySelectionFromRequest(publishDeliverySelectionRequest{AppProfileID: "review-app", ChatIDs: []string{"oc_review"}},
		settings, []feishuNotificationApp{{ID: "review-app"}}, []feishuChat{{ChatID: "oc_review"}}, "admin")
	if err != nil || len(kept.DistributionOwners) != 1 {
		t.Fatalf("同应用改群应保留负责人：%+v %v", kept, err)
	}
	// 读不到群成员（飞书暂时不通、还没配应用）不算接口失败：后台照常打开，原因单独给出。
	env.pipeline.distributionOwnerLister = func(context.Context, string, string) ([]publishDistributionOwner, error) {
		return nil, errors.New("飞书暂时不可用")
	}
	rec = doPublishReq(mux, http.MethodGet, path, "", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"candidates_error":"飞书暂时不可用"`) ||
		!strings.Contains(rec.Body.String(), `"candidates":[]`) || !strings.Contains(rec.Body.String(), `"open_id":"ou_a"`) {
		t.Fatalf("读不到成员应 200 并带原因、保留已选的人：%d %s", rec.Code, rec.Body.String())
	}
}
