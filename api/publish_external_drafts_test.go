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

const testLongToutiaoTitle = "这家166年的京都老字号把抹茶做成零食，只开6家店，年营收就做到了1.4亿"

// toutiaoTitleLength 头条号的数法（英文字母、数字两个算一个字）：testLongToutiaoTitle 37 个字符算 33 字。
func toutiaoTitleLength(title string) int {
	return yunsuoProfileForTarget(externalAccount{Name: "头条号"}).titleLength(title)
}

// createLongTitleDistributionJob 一篇已发布原创，标题超过头条号 30 字（网站最新稿同标题）。
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

func fixActionValue(t *testing.T, job publishJob, account string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"action": feishuCardActionDistributeFix, "job_id": job.ID, "account": account,
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
	setTestDistributionAccounts(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	stubYunsuoImageSize(t, map[string][2]int{})
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	env.pipeline.externalSuggest = func(_ context.Context, kind, current string, min, max int) (string, error) {
		return "老茶铺把茶做成零食，年营收1.4亿", nil
	}
	job := createLongTitleDistributionJob(t, env, "site:long-title")

	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("百家号能推，整体请求不应失败：%v", err)
	}
	// 百家号标题上限 64 字，原样存草稿。
	if fmt.Sprint(robot.platforms) != "[baijiahao]" || robot.saves[0].Title != testLongToutiaoTitle {
		t.Fatalf("头条号标题超限应先不存：robot=%v %+v", robot.platforms, robot.saves)
	}
	got := mustJob(t, env, job.ID)
	draft, _ := externalDraftFor(got, testToutiaoAccountID)
	title := externalIssueFields(draft.Issues)[publishExternalIssueTitle]
	if title.Max != 30 || title.Length != toutiaoTitleLength(testLongToutiaoTitle) || title.Current != testLongToutiaoTitle {
		t.Fatalf("应记下标题问题与现状：%+v", draft)
	}
	if draft.SuggestedTitle != "老茶铺把茶做成零食，年营收1.4亿" || draft.SuggestedBy != "ai" || draft.PreparedFor != draft.CheckedAt {
		t.Fatalf("后台应准备好 AI 建议：%+v", draft)
	}
	if !externalJobNeedsFix(got) {
		t.Fatalf("应标记为待修改")
	}

	// 超出上限的修改当场拒绝，并说清还要删几个字。
	tooLong := "老茶铺把茶做成零食和甜品，只开六家店，年营收就做到了一点四亿元人民币啊"
	out := env.pipeline.reviewCardActionWithForm(context.Background(), "ou_reviewer_1", "", fixActionValue(t, got, testToutiaoAccountID),
		map[string]any{reviewCardFixFieldName("t", got.ID, testToutiaoAccountID): tooLong})
	if out.toastType != "error" || !strings.Contains(out.toastContent, fmt.Sprintf("头条号标题最多 30 字，现在 %d 字，还要删 %d 字", runeLen(tooLong), runeLen(tooLong)-30)) {
		t.Fatalf("超限修改应拒绝并说明：%+v", out)
	}

	fixed := "老茶铺把茶做成零食，年营收过亿"
	out = env.pipeline.reviewCardActionWithForm(context.Background(), "ou_reviewer_1", "", fixActionValue(t, got, testToutiaoAccountID),
		map[string]any{reviewCardFixFieldName("t", got.ID, testToutiaoAccountID): fixed})
	if out.toastType != "success" || out.toastContent != "已保存，正在存草稿至头条号" {
		t.Fatalf("改好后应受理：%+v", out)
	}
	if fmt.Sprint(robot.platforms) != "[baijiahao toutiao]" {
		t.Fatalf("改好后只存头条号：robot=%v", robot.platforms)
	}
	if robot.saves[1].Title != fixed {
		t.Fatalf("头条号草稿标题必须是人改的那一句：%+v", robot.saves[1])
	}
	got = mustJob(t, env, job.ID)
	lane := mustLane(t, got, testToutiaoAccountID)
	if lane.Title != fixed || lane.RequestedBy != "ou_reviewer_1" {
		t.Fatalf("lane 应记平台标题与操作人：%+v", lane)
	}
	if draft, _ := externalDraftFor(got, testToutiaoAccountID); len(draft.Issues) != 0 || draft.Title != fixed || draft.EditedBy != "ou_reviewer_1" {
		t.Fatalf("draft 应记下修改且没有待改项：%+v", draft)
	}
	if externalJobNeedsFix(got) {
		t.Fatalf("推出去后不再待修改")
	}
	// 推过以后表单失效：旧卡再提交不会重推。
	out = env.pipeline.reviewCardActionWithForm(context.Background(), "ou_reviewer_1", "", fixActionValue(t, got, testToutiaoAccountID),
		map[string]any{reviewCardFixFieldName("t", got.ID, testToutiaoAccountID): "再改一次标题也不会重推"})
	if out.toastType == "success" || len(robot.saves) != 2 {
		t.Fatalf("已存的平台不得再改再存：%+v robot=%v", out, robot.platforms)
	}
}

// TestExternalAutoSkipsPlatformWaitingForFix auto 账号卡在待修改：自动外发不反复去网站取稿、不占名额；
// 改好后挂回自动外发并推出。
func TestExternalAutoSkipsPlatformWaitingForFix(t *testing.T) {
	setTestAutoAccounts(t)
	stubYunsuoImageSize(t, map[string][2]int{})
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	job := createLongTitleDistributionJob(t, env, "site:auto-long-title")
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		current.ExternalDistributionAutoPending = publishExternalAutoKindPublish
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.runExternalAutoDistribution()
	if len(robot.saves) != 0 {
		t.Fatalf("待修改不得推送：%v", robot.platforms)
	}
	got := mustJob(t, env, job.ID)
	if got.ExternalDistributionAutoPending == "" || !externalDraftBlocked(got, externalAccount{ID: testToutiaoAccountID, Name: "头条号"}) {
		t.Fatalf("待办应保留、头条号应记为待修改：pending=%q drafts=%+v", got.ExternalDistributionAutoPending, got.ExternalDistributionDrafts)
	}
	checkedAt := mustDraft(t, got, testToutiaoAccountID).CheckedAt
	clock.set("2026-10-12 10:30")
	env.pipeline.runExternalAutoDistribution()
	if again := mustDraft(t, mustJob(t, env, job.ID), testToutiaoAccountID); again.CheckedAt != checkedAt || len(robot.saves) != 0 {
		t.Fatalf("文章没变时不重查、不推：%+v", again)
	}
	if used := externalDistributionQuotaUsed(t, env, testToutiaoAccountID); used != 0 {
		t.Fatalf("待修改不占名额，实际 %d", used)
	}

	fixed := "老茶铺把茶做成零食，年营收过亿"
	if _, err := env.pipeline.submitExternalDraftEdit(job.ID, testToutiaoAccountID, externalDraftEdit{Title: &fixed}, "ou_editor"); err != nil {
		t.Fatal(err)
	}
	if len(robot.saves) != 1 || robot.saves[0].Title != fixed {
		t.Fatalf("改好后应按自动外发存草稿：%+v", robot.saves)
	}
	if lane := mustLane(t, mustJob(t, env, job.ID), testToutiaoAccountID); lane.RequestedBy != publishExternalAutoActor {
		t.Fatalf("auto 账号仍记系统发起：%+v", lane)
	}
}

// TestExternalAutoRechecksDraftWhenPlatformLimitChanges #280（2026-10-13）：头条号标题上限按 20 字
// 记成待修改，用户裁定上限是 30 字。文章没变也要按新规则重查一次，过了就推，标题原样。
func TestExternalAutoRechecksDraftWhenPlatformLimitChanges(t *testing.T) {
	setTestAutoAccounts(t)
	stubYunsuoImageSize(t, map[string][2]int{})
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	clock := newCSTClock("2026-10-13 09:00")
	env.pipeline.now = func() time.Time { return clock.now }
	title := "西柚汁原料价格翻倍，可果美开始用胡萝卜做“西柚汁”了"
	job := createPublishedDistributionJobWithKey(t, env, "site:limit-change", "FBIF食品饮料创新")
	job, err := env.store.update(job.ID, func(current *publishJob) {
		current.Title = title
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
		current.ExternalDistributionAutoPending = publishExternalAutoKindPublish
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: title, CoverImg: job.CoverURL,
		Content: testDistributionLiveHTML, Summary: job.Summary, SourceName: "FBIF食品饮料创新",
		PublishStatus: foodTalksPublishStatusPublished,
	})
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		setExternalDraft(current, publishExternalDraft{
			AccountID: testToutiaoAccountID, CheckedMaterial: externalDraftMaterial(*current),
			Issues: []publishExternalIssue{{Field: publishExternalIssueTitle, Current: title, Length: runeLen(title), Min: 8, Max: 20}},
		})
	}); err != nil {
		t.Fatal(err)
	}
	toutiao := externalAccount{ID: testToutiaoAccountID, Name: "头条号"}
	if externalDraftBlocked(mustJob(t, env, job.ID), toutiao) {
		t.Fatalf("按旧上限记的待修改不应继续挡住")
	}
	env.pipeline.runExternalAutoDistribution()
	if len(robot.saves) != 1 || robot.saves[0].Title != title {
		t.Fatalf("26 字标题在 30 字上限内，应重查后存原标题：%+v", robot.saves)
	}
	got := mustJob(t, env, job.ID)
	if externalJobNeedsFix(got) {
		t.Fatalf("重查通过后不应再待修改：%+v", got.ExternalDistributionDrafts)
	}
	if lane := mustLane(t, got, testToutiaoAccountID); lane.Title != title {
		t.Fatalf("头条号记下的标题应是推出去的原标题：%+v", lane)
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
	chatID, kind string
	post         string
}

// TestExternalReminderMentionsOwnersInThread 待修改时在审核卡的话题里发一条普通消息并 @ 分发负责人
// （2026-10-12 用户：「不应该直接在卡片里@人，因为在卡片里@人可能收不到。它应该在这个卡片消息的
// 话题下创建一个对话，@对应的人，告诉他下一步要做什么」「给他选项」）：开场逐项写哪里不合要求、能怎么
// 改、改完点哪里；改好后同一话题里说一句「都改好了」，不再 @；同一轮不重复发；没设负责人不发。
func TestExternalReminderMentionsOwnersInThread(t *testing.T) {
	setTestDistributionAccounts(t)
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
	env.pipeline.externalReminderSender = func(_ context.Context, job publishJob, chatID, kind string, post map[string]any) (string, error) {
		sent = append(sent, sentReminder{chatID: chatID, kind: kind, post: panelJSON(t, post)})
		return fmt.Sprintf("om_reminder_%d", len(sent)), nil
	}
	cardSentAt := map[string]time.Time{"om_review_card": pipelineNow(env.pipeline).Add(-time.Hour)}
	env.pipeline.reviewCardSentAt = func(_ context.Context, _ publishJob, _ string, messageID string) (time.Time, error) {
		if at, ok := cardSentAt[messageID]; ok {
			return at, nil
		}
		return time.Time{}, errors.New("没有这条消息")
	}
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
	if len(sent) != 1 || sent[0].kind != externalReminderKindOpen || sent[0].chatID != "oc_review" {
		t.Fatalf("应在审核卡话题里发一条开场：%+v", sent)
	}
	over := toutiaoTitleLength(testLongToutiaoTitle) - 30
	for _, want := range []string{`{"tag":"at","user_id":"ou_owner_1"}`, fmt.Sprintf("#%d《%s》同步到头条号前，还差 1 处要改：", job.ID, testLongToutiaoTitle),
		fmt.Sprintf(`"style":["bold"],"tag":"text","text":"1. 标题超了 %d 字（头条号最多 30 字，英文字母、数字两个算一个字）"`, over),
		"卡片里已填好建议「", "可以直接用，也可以改成自己的", "改好后点上面卡片里的「保存并存草稿至头条号」，就会同步。"} {
		if !strings.Contains(sent[0].post, want) {
			t.Fatalf("开场缺少 %q: %s", want, sent[0].post)
		}
	}
	got := mustJob(t, env, job.ID)
	if r := got.ExternalDistributionReminder; r.State != "" || r.MessageIDs["oc_review"] != "om_reminder_1" || r.Want != publishExternalReminderOpen {
		t.Fatalf("发完应记下开场消息 id：%+v", r)
	}
	// 同一轮再调度不重复发。
	env.pipeline.dispatchExternalReminder(job.ID)
	if len(sent) != 1 {
		t.Fatalf("同一轮不重复发：%+v", sent)
	}

	// 改好：同一话题里说一句都改好了，不再 @。
	fixed := "老茶铺把茶做成零食，年营收过亿"
	if _, err := env.pipeline.submitExternalDraftEdit(job.ID, testToutiaoAccountID, externalDraftEdit{Title: &fixed}, "ou_owner_1"); err != nil {
		t.Fatal(err)
	}
	last := sent[len(sent)-1]
	if len(sent) != 2 || last.kind != externalReminderKindClose || !strings.Contains(last.post, "都改好了，正在存草稿至头条号。") || strings.Contains(last.post, `"tag":"at"`) {
		t.Fatalf("改好后应收尾一句且不 @：%+v", sent)
	}
	if r := mustJob(t, env, job.ID).ExternalDistributionReminder; r.State != "" || r.Want != publishExternalReminderResolved || r.ClosedIDs["oc_review"] != "om_reminder_2" {
		t.Fatalf("提醒应结束并记下收尾：%+v", r)
	}
	env.pipeline.dispatchExternalReminder(job.ID)
	if len(sent) != 2 {
		t.Fatalf("收尾不重复发：%+v", sent)
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

// TestExternalReminderCoverOptions 封面太小时给的做法：话题发图开通了就请人直接发图到话题里，
// 没开通时给侧边栏上传链接——不给点了没反应的选项。
func TestExternalReminderCoverOptions(t *testing.T) {
	setTestDistributionAccounts(t)
	t.Setenv("AUTH_SESSION_SECRET", "test-session-secret")
	cfg, err := loadExternalTargets()
	if err != nil {
		t.Fatal(err)
	}
	job := publishJob{ID: 196, Title: "把茶做成零食", SourceName: "FBIF食品饮料创新", ExternalDistributionDrafts: []publishExternalDraft{{
		AccountID: testToutiaoAccountID,
		Issues:    []publishExternalIssue{{Field: publishExternalIssueCover, Width: 300, Height: 200, MinWidth: 452, MinHeight: 352}},
	}, {
		AccountID: testBaijiaAccountID,
		Issues:    []publishExternalIssue{{Field: publishExternalIssueTitle, Current: "一个很长的标题", Length: 45, Min: 15, Max: 40}},
	}}}
	owners := []publishDistributionOwner{{OpenID: "ou_a"}}
	plain := panelJSON(t, buildExternalReminderOpening(job, cfg, owners, true))
	for _, want := range []string{"同步到头条号、百家号前，还差 2 处要改：", "1. 头条号封面太小（头条号至少 452×352，现在 300×200）",
		"在卡片里点选一张正文大图，或者", `"tag":"a","text":"上传一张"`, "（传上去就换上）", "2. 百家号标题超了 5 字（百家号最多 40 字）",
		"在卡片输入框里改到 40 字以内", "「保存并存草稿至头条号」「保存并存草稿至百家号」"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("开场缺少 %q: %s", want, plain)
		}
	}
	if strings.Contains(plain, "话题里") {
		t.Fatalf("不再请人把图发到话题里: %s", plain)
	}
}

// TestExternalReminderExpiredCardPointsToSidebar 审核卡快过或已过飞书 14 天修改期限时，卡上已没有改稿按钮
// （#233：补发九月旧稿，提醒却叫人去点卡片里的「保存并存草稿至头条号」），每一步都改到侧边栏改稿页里做；
// 连改稿页链接都给不出就不发。
func TestExternalReminderExpiredCardPointsToSidebar(t *testing.T) {
	setTestDistributionAccounts(t)
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
	env.pipeline.externalReminderSender = func(_ context.Context, job publishJob, chatID, kind string, post map[string]any) (string, error) {
		sent = append(sent, sentReminder{chatID: chatID, kind: kind, post: panelJSON(t, post)})
		return fmt.Sprintf("om_reminder_%d", len(sent)), nil
	}
	cardAge := 13*24*time.Hour + time.Hour
	var cardErr error
	env.pipeline.reviewCardSentAt = func(context.Context, publishJob, string, string) (time.Time, error) {
		return pipelineNow(env.pipeline).Add(-cardAge), cardErr
	}
	request := func(key string) publishJob {
		t.Helper()
		job := createLongTitleDistributionJob(t, env, key)
		if _, err := env.store.update(job.ID, func(current *publishJob) {
			current.ReviewDeliveryAppID = "review-app"
			current.ReviewDeliveryChatIDs = []string{"oc_review"}
			current.ReviewCardMsgIDs = map[string]string{"oc_review": "om_old_card"}
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(mustJob(t, env, job.ID))); err != nil &&
			!errors.Is(err, errExternalLanesNeedFix) {
			t.Fatal(err)
		}
		return mustJob(t, env, job.ID)
	}

	job := request("site:reminder-expired")
	if len(sent) != 1 || sent[0].kind != externalReminderKindOpen {
		t.Fatalf("卡片过期也要提醒：%+v", sent)
	}
	link := externalDistributionSidebarLink(job.ID)
	for _, want := range []string{`{"tag":"at","user_id":"ou_owner_1"}`, "改稿页里已填好建议「", `[{"tag":"text","text":"请"},`,
		`"href":"` + link + `","tag":"a","text":"打开改稿页"`, "，改好后点「保存并存草稿至头条号」，就会同步。"} {
		if !strings.Contains(sent[0].post, want) {
			t.Fatalf("过期卡提醒缺少 %q: %s", want, sent[0].post)
		}
	}
	if strings.Contains(sent[0].post, "卡片里") || strings.Contains(sent[0].post, "上面卡片") {
		t.Fatalf("过期卡提醒不能再叫人去卡片里改: %s", sent[0].post)
	}

	// 读不到发送时间（飞书接口失败或慢）：改稿页卡片新旧都能用，按改不了处理；最多等 5 秒，
	// 不占用提醒本身的发送时间。
	cardAge, cardErr = time.Hour, errors.New("飞书读消息失败")
	env.pipeline.reviewCardSentAt = func(ctx context.Context, _ publishJob, _ string, _ string) (time.Time, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > publishReviewCardAgeLookup {
			t.Errorf("读卡片时间要有 5 秒以内的单独时限：%v %v", deadline, ok)
		}
		return pipelineNow(env.pipeline).Add(-cardAge), cardErr
	}
	request("site:reminder-unknown-age")
	if len(sent) != 2 || !strings.Contains(sent[1].post, "打开改稿页") || strings.Contains(sent[1].post, "上面卡片") {
		t.Fatalf("读不到卡片时间时指向改稿页：%+v", sent)
	}

	// 已知卡片过期（更新时飞书回过 230031）：不必再问飞书。
	cardErr = nil
	expired := createLongTitleDistributionJob(t, env, "site:reminder-known-expired")
	if _, err := env.store.update(expired.ID, func(current *publishJob) {
		current.NotificationState = publishNotificationStateExpired
	}); err != nil {
		t.Fatal(err)
	}
	if editable := env.pipeline.externalReminderCardEditable(context.Background(), mustJob(t, env, expired.ID), "oc_review"); editable {
		t.Fatal("已记过期的卡不能再按可改处理")
	}

	// 没有改稿页链接（缺签名密钥）：卡片里改不了又给不出去处，不发。
	t.Setenv("AUTH_SESSION_SECRET", "")
	cardAge = 20 * 24 * time.Hour
	noLink := request("site:reminder-no-link")
	if r := noLink.ExternalDistributionReminder; len(sent) != 2 || r.State != "" || r.Error != "" || !strings.Contains(r.Skipped, "改稿页") {
		t.Fatalf("给不出改稿页就不发：sent=%d reminder=%+v", len(sent), r)
	}
}

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
	setTestDistributionAccounts(t)
	stubYunsuoImageSize(t, map[string][2]int{"https://cdn.example.com/a.jpg": {300, 200}})
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
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
	if toutiao.Status != "待修改" || !toutiao.Editable || !toutiao.TitleIssue || toutiao.TitleMax != 30 || toutiao.CoverIssue == "" ||
		toutiao.CoverMinWidth != 452 || toutiao.Title != testLongToutiaoTitle {
		t.Fatalf("头条号应待修改并给出上限：%+v", toutiao)
	}
	if baijia.Status != "草稿已存（未发布）" || baijia.Editable || baijia.DraftURL != testRobotDraftURL(testRobotArticleID) || baijia.Action != "存草稿至百家号" {
		t.Fatalf("百家号已存草稿，不能再改：%+v", baijia)
	}
	if toutiao.Action != "存草稿至头条号" {
		t.Fatalf("头条号按钮写存草稿：%+v", toutiao)
	}

	// 只改标题：封面问题还在，仍待修改、不推。
	put := func(body string) *httptest.ResponseRecorder {
		return doEditReq(mux, http.MethodPut, base+"draft"+signed, editor, []byte(body), "application/json")
	}
	if rec := put(`{"account":"` + testToutiaoAccountID + `","title":"老茶铺把茶做成零食，年营收过亿"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"待修改"`) || len(robot.saves) != 1 {
		t.Fatalf("改完标题封面仍要改：%d %s robot=%v", rec.Code, rec.Body.String(), robot.platforms)
	}
	if rec := put(`{"account":"` + testToutiaoAccountID + `","title":"这个标题足足有三十多个字，远远超过了头条号三十个字的标题上限啊"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "头条号标题最多 30 字") {
		t.Fatalf("超限标题应拒绝：%d %s", rec.Code, rec.Body.String())
	}

	// 上传封面：太小的拒绝；够大的转存后就是头条号封面，问题清空即推（2026-10-12 用户：「传上去之后
	// 直接绑定、自动同步过去了」）。
	upload := func(data []byte, fields ...string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("account", testToutiaoAccountID)
		for i := 0; i+1 < len(fields); i += 2 {
			_ = form.WriteField(fields[i], fields[i+1])
		}
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
		t.Fatalf("够大的图应转存：%d %s", rec.Code, rec.Body.String())
	}
	if fmt.Sprint(robot.platforms) != "[baijiahao toutiao]" || robot.saves[1].CoverURL != uploaded.Uploaded.URL {
		t.Fatalf("封面改好后应存头条号草稿并用上传的图：robot=%v saves=%+v", robot.platforms, robot.saves)
	}
	got := mustJob(t, env, job.ID)
	draft := mustDraft(t, got, testToutiaoAccountID)
	if !strings.HasPrefix(draft.EditedBy, "王编辑（ou_editor）") || len(draft.Issues) != 0 {
		t.Fatalf("应记下谁改的：%+v", draft)
	}
	// 推过以后不能再改，也不能再传。
	if rec := put(`{"account":"` + testToutiaoAccountID + `","title":"再改一次"}`); rec.Code != http.StatusConflict {
		t.Fatalf("已推的平台不能再改：%d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(testPNG(t, 900, 600)); rec.Code != http.StatusConflict || len(stored) != 1 {
		t.Fatalf("已推的平台不能再传封面：%d %s", rec.Code, rec.Body.String())
	}
}

// TestExternalSidebarUploadCarriesTypedTitle 标题和封面都要改：页面里改好的标题随图一起提交，传上去
// 就推；标题还超着就只换封面，等人改完标题再点保存。
func TestExternalSidebarUploadCarriesTypedTitle(t *testing.T) {
	setTestDistributionAccounts(t)
	stubYunsuoImageSize(t, map[string][2]int{"https://cdn.example.com/a.jpg": {300, 200}})
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	env.pipeline.externalCoverStore = func(_ context.Context, name string, body []byte) (string, error) {
		return "https://cdn.example.com/uploaded/" + name, nil
	}
	mux, service := newDistributionEditMux(t, env)
	editor := mustSessionForUser(t, service, authUser{ID: "feishu:ou_editor", Name: "王编辑", AuthMode: authModeFeishu, Status: authUserActive, FeishuOpenID: "ou_editor"})
	upload := func(job publishJob, data []byte, title string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("account", testToutiaoAccountID)
		_ = form.WriteField("title", title)
		part, _ := form.CreateFormFile("file", "cover.png")
		_, _ = part.Write(data)
		_ = form.Close()
		target := fmt.Sprintf("/api/publish/jobs/%d/external-distribution/cover?t=%s", job.ID, externalDistributionEditSig(job.ID))
		return doEditReq(mux, http.MethodPost, target, editor, body.Bytes(), form.FormDataContentType())
	}

	job := createLongTitleDistributionJob(t, env, "site:upload-title")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	// 标题仍超：封面换上，标题问题留着，不推。
	if rec := upload(job, testPNG(t, 900, 600), testLongToutiaoTitle+"再长一点"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"待修改"`) || len(robot.saves) != 1 {
		t.Fatalf("标题没改好时只换封面：%d %s saves=%d", rec.Code, rec.Body.String(), len(robot.saves))
	}
	draft := mustDraft(t, mustJob(t, env, job.ID), testToutiaoAccountID)
	if _, cover := externalIssueFields(draft.Issues)[publishExternalIssueCover]; cover || !strings.HasPrefix(draft.CoverURL, "https://cdn.example.com/uploaded/") {
		t.Fatalf("封面应已换上：%+v", draft)
	}
	if _, title := externalIssueFields(draft.Issues)[publishExternalIssueTitle]; !title {
		t.Fatalf("标题问题还在：%+v", draft)
	}
	// 再传一张，带上改好的标题：一起提交，马上推。
	fixed := "老茶铺把茶做成零食，年营收过亿"
	if rec := upload(job, testPNG(t, 1200, 800), fixed); rec.Code != http.StatusOK || len(robot.saves) != 2 {
		t.Fatalf("标题改好、封面传上就存草稿：%d %s robot=%v", rec.Code, rec.Body.String(), robot.platforms)
	}
	if saved := robot.saves[1]; saved.Title != fixed || !strings.HasPrefix(saved.CoverURL, "https://cdn.example.com/uploaded/") {
		t.Fatalf("存进头条号的是改好的标题和刚传的图：%+v", saved)
	}
}

// TestExternalCoverAutoUpscalesNearMiss 原封面只差一点（#183：780×332，头条号要 452×352）：
// 自动放大一份换上直接推，不记待修改、不 @ 人；差太多（300×200）的仍等人换。
func TestExternalCoverAutoUpscalesNearMiss(t *testing.T) {
	setTestDistributionAccounts(t)
	sizes := map[string][2]int{"https://cdn.example.com/a.jpg": {780, 332}}
	stubYunsuoImageSize(t, sizes)
	original := externalCoverFetch
	t.Cleanup(func() { externalCoverFetch = original })
	var fetched []string
	externalCoverFetch = func(_ context.Context, src string) ([]byte, error) {
		fetched = append(fetched, src)
		return testPNG(t, sizes[src][0], sizes[src][1]), nil
	}
	env := newPublishTestEnv(t)
	useFakeRobot(env)
	var stored [][]byte
	env.pipeline.externalCoverStore = func(_ context.Context, name string, body []byte) (string, error) {
		stored = append(stored, body)
		return "https://cdn.example.com/uploaded/" + name, nil
	}
	job := createPublishedDistributionJobWithKey(t, env, "site:near-miss", "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("放大后两个平台都能推：%v", err)
	}
	if len(fetched) == 0 || fetched[0] != "https://cdn.example.com/a.jpg" || len(stored) == 0 {
		t.Fatalf("应取原封面放大并转存：fetched=%v stored=%d", fetched, len(stored))
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(stored[0]))
	if err != nil || format != "jpeg" || config.Width < 452 || config.Height < 352 || config.Height != 352 {
		t.Fatalf("放大到刚好够下限：%v %s %+v", err, format, config)
	}
	got := mustJob(t, env, job.ID)
	toutiao := mustLane(t, got, testToutiaoAccountID)
	if toutiao.State == "" || externalJobNeedsFix(got) || !strings.Contains(toutiao.PayloadJSON, "https://cdn.example.com/uploaded/distribution-cover-") {
		t.Fatalf("头条号应直接用放大后的封面推：%+v", toutiao)
	}
	draft := mustDraft(t, got, testToutiaoAccountID)
	if !strings.HasPrefix(draft.CoverURL, "https://cdn.example.com/uploaded/") || len(draft.Issues) != 0 || draft.EditedBy != "" {
		t.Fatalf("自动换上的封面不算人改：%+v", draft)
	}

	// 差太多的不放大，照旧待修改。
	if factor := externalCoverUpscaleFactor(300, 200, 452, 352); factor != 0 {
		t.Fatalf("300×200 要放大 %.2f 倍，太糊，不自动放大", factor)
	}
	if factor := externalCoverUpscaleFactor(780, 332, 452, 352); factor < 1.05 || factor > 1.07 {
		t.Fatalf("780×332 只需放大约 1.06 倍：%.3f", factor)
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

// TestFeishuMessageCreateTime 读审核卡发送时间（毫秒时间戳）；读不到就报错，由提醒改指向改稿页。
func TestFeishuMessageCreateTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok","expire":7200}`)
		case r.Method == http.MethodGet && r.URL.Path == "/open-apis/im/v1/messages/om_card":
			fmt.Fprint(w, `{"code":0,"msg":"ok","data":{"items":[{"message_id":"om_card","create_time":"1759395600000"}]}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/open-apis/im/v1/messages/om_gone":
			fmt.Fprint(w, `{"code":230011,"msg":"message recalled"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	notifier := newFeishuNotifierForOpenID(feishuNotificationApp{ID: "review-app", AppID: "cli", AppSecret: "secret"}, "ou_gary")
	notifier.baseURL = srv.URL
	at, err := notifier.messageCreateTime(context.Background(), "om_card")
	if err != nil || !at.Equal(time.UnixMilli(1759395600000)) {
		t.Fatalf("应读出发送时间：%v %v", at, err)
	}
	if _, err := notifier.messageCreateTime(context.Background(), "om_gone"); err == nil || !strings.Contains(err.Error(), "230011") {
		t.Fatalf("飞书报错要带出来：%v", err)
	}
}
