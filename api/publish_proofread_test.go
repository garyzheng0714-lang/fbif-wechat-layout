package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const testProofreadBody = "近期推出的“不使用西柚的西柚味100%果汁”，也是类似的出发点：农业水产省数据显示，西柚进口量持续下降。"

// testProofreadRiskItems 生产 #280 的形状：一条确定的名称错误、一条「建议补年份」的 confirmed
// （复核会否掉）、一条待核、一条合规——后两条根本不是候选。
func testProofreadRiskItems() []publishRiskItem {
	return []publishRiskItem{
		{Category: publishRiskCategoryNaming, Level: publishRiskWarning, Certainty: publishRiskCertaintyConfirmed,
			Evidence: "也是类似的出发点：农业水产省数据显示", Reason: "前文写「日本农林水产省」，此处写「农业水产省」，应改为「农林水产省」。"},
		{Category: publishRiskCategoryFactual, Level: publishRiskWarning, Certainty: publishRiskCertaintyConfirmed,
			Evidence: "西柚进口量持续下降", Reason: "未标注年份，需补充报告期。"},
		{Category: publishRiskCategoryNumber, Level: publishRiskWarning, Certainty: publishRiskCertaintyUnverified,
			Evidence: "西柚进口量持续下降", Reason: "按我了解到的情况可能已变化。"},
		{Category: publishRiskCategoryCompliance, Level: publishRiskWarning, Certainty: publishRiskCertaintyUnverified,
			Evidence: "不使用西柚的西柚味100%果汁", Reason: "转载授权待核。"},
	}
}

func setTestProofreadOwners(t *testing.T, env *publishTestEnv, owners ...publishDistributionOwner) {
	t.Helper()
	env.pipeline.deliverySettings = newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "delivery.json"))
	settings := publishDeliverySettings{AppProfileID: "review-app", ChatIDs: []string{"oc_review"}}
	if len(owners) > 0 {
		settings.ProofreadOwners, settings.ProofreadOwnersApp = owners, "review-app"
	}
	if err := env.pipeline.deliverySettings.write(settings); err != nil {
		t.Fatal(err)
	}
}

// seedProofreadJob 已发到审核群的稿：卡片 message_id 已记、风险结论刚写入（同一次写入里排上复核）。
func seedProofreadJob(t *testing.T, env *publishTestEnv, stage string, cardSent bool) publishJob {
	t.Helper()
	job, _, err := env.store.create("site:proofread:"+stage, "https://mp.weixin.qq.com/s/proofread-280")
	if err != nil {
		t.Fatal(err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = stage
		current.Title = "西柚汁原料价格翻倍，可果美开始用胡萝卜做“西柚汁”了"
		current.ContentHTML = "<p>" + testProofreadBody + "</p>"
		current.ContentVersion = 1
		current.ReviewDeliveryAppID = "review-app"
		current.ReviewDeliveryChatIDs = []string{"oc_review"}
		if cardSent {
			current.ReviewCardMsgIDs = map[string]string{"oc_review": "om_review_card"}
		}
		current.RiskLevel = publishRiskWarning
		current.RiskItems = testProofreadRiskItems()
		notePublishProofread(current)
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

type proofreadVerifyCall struct {
	candidates []publishRiskItem
	cardMsgID  string
}

// stubProofreadVerify 复核桩：按顺序回放模型输出，记下每次看到的候选与当时审核卡是否已发。
func stubProofreadVerify(t *testing.T, env *publishTestEnv, outputs ...string) *[]proofreadVerifyCall {
	t.Helper()
	calls := &[]proofreadVerifyCall{}
	env.pipeline.proofreadVerify = func(_ context.Context, job publishJob, candidates []publishRiskItem) (string, string, error) {
		stored, _ := env.store.byID(job.ID)
		*calls = append(*calls, proofreadVerifyCall{candidates: candidates, cardMsgID: reviewCardMessageID(stored, "oc_review")})
		out := outputs[len(outputs)-1]
		if n := len(*calls); n <= len(outputs) {
			out = outputs[n-1]
		}
		return testProofreadBody, out, nil
	}
	return calls
}

type sentProofread struct {
	chatID, post string
	parent       string
}

func captureProofreadReminders(t *testing.T, env *publishTestEnv) *[]sentProofread {
	t.Helper()
	sent := &[]sentProofread{}
	env.pipeline.proofreadReminderSender = func(_ context.Context, job publishJob, chatID string, post map[string]any) (string, error) {
		*sent = append(*sent, sentProofread{chatID: chatID, post: panelJSON(t, post), parent: env.pipeline.reviewCardMessageIDForChat(job, chatID)})
		return fmt.Sprintf("om_proofread_%d", len(*sent)), nil
	}
	return sent
}

const testProofreadDefinite = `{"items":[{"index":1,"definite":true,"wrong":"农业水产省","correct":"农林水产省"},{"index":2,"definite":false}]}`

// TestProofreadReminderMentionsOwnerInCardThread 2026-10-13 用户裁定：「置信度几乎100%的错误……在卡片的
// 话题下，@对应的人来查看……不耽误同步……模棱两可的，这些可以不提醒」。确定的错误回复在审核卡话题里、
// 真 @ 校对负责人，写清「X」应为「Y」与原句；复核否掉的不提；同一组错误只提醒一次。
func TestProofreadReminderMentionsOwnerInCardThread(t *testing.T) {
	env := newPublishTestEnv(t)
	setTestProofreadOwners(t, env, publishDistributionOwner{OpenID: "ou_proof", Name: "郑梽煌"})
	calls := stubProofreadVerify(t, env, testProofreadDefinite)
	sent := captureProofreadReminders(t, env)
	job := seedProofreadJob(t, env, publishStagePublished, true)

	env.pipeline.dispatchProofreadReminder(job.ID)
	if len(*calls) != 1 || len((*calls)[0].candidates) != 2 {
		t.Fatalf("只有 confirmed 的事实/名称/数字/成稿条目是候选（待核、合规不送复核）：%+v", *calls)
	}
	if len(*sent) != 1 || (*sent)[0].chatID != "oc_review" || (*sent)[0].parent != "om_review_card" {
		t.Fatalf("应在审核卡话题里回复一条：%+v", *sent)
	}
	post := (*sent)[0].post
	for _, want := range []string{
		`{"tag":"at","user_id":"ou_proof"}`,
		fmt.Sprintf("#%d《西柚汁原料价格翻倍，可果美开始用胡萝卜做“西柚汁”了》有 1 处确定的错误，请核对：", job.ID),
		`"style":["bold"],"tag":"text","text":"1. 「农业水产省」应为「农林水产省」"`,
		"原句：也是类似的出发点：农业水产省数据显示",
		"不影响同步和发布，确认后改正即可。",
	} {
		if !strings.Contains(post, want) {
			t.Fatalf("提醒缺少 %q: %s", want, post)
		}
	}
	if strings.Contains(post, "补充报告期") || strings.Contains(post, "西柚进口量") {
		t.Fatalf("复核否掉的建议不应出现在提醒里: %s", post)
	}
	got := mustJob(t, env, job.ID)
	r := got.ProofreadReminder
	if r.State != "" || r.Sent == "" || r.MessageIDs["oc_review"] != "om_proofread_1" || len(r.Findings) != 1 || r.Checked != r.Want {
		t.Fatalf("发完应记下这组错误与回复 id：%+v", r)
	}
	if got.Stage != publishStagePublished || got.ContentHTML != job.ContentHTML || len(got.RiskItems) != 4 {
		t.Fatalf("提醒不改阶段、正文与风险结论：%+v", got)
	}

	// 同一组错误不再 @：重新调度什么都不做；风险结论重跑后措辞变了，复核出同样的错也不再提醒。
	env.pipeline.dispatchProofreadReminder(job.ID)
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		current.RiskItems[0].Reason += "（重跑）"
		if !notePublishProofread(current) {
			t.Fatal("风险结论变了应重新复核")
		}
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.dispatchProofreadReminder(job.ID)
	if len(*calls) != 2 || len(*sent) != 1 {
		t.Fatalf("同样的错误只提醒一次：verify=%d sent=%d", len(*calls), len(*sent))
	}

	// 新出现的错误才再提醒一次。
	*calls = nil
	env.pipeline.proofreadVerify = func(context.Context, publishJob, []publishRiskItem) (string, string, error) {
		return testProofreadBody, `{"items":[{"index":1,"definite":true,"wrong":"农业水产省","correct":"农林水产省"},{"index":2,"definite":true,"wrong":"持续下降","correct":"持续下滑"}]}`, nil
	}
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		current.RiskItems[1].Reason = "另一处错误。"
		notePublishProofread(current)
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.dispatchProofreadReminder(job.ID)
	if len(*sent) != 2 || !strings.Contains((*sent)[1].post, "有 2 处确定的错误") || !strings.Contains((*sent)[1].post, "2. 「持续下降」应为「持续下滑」") {
		t.Fatalf("新的一组错误应再提醒：%+v", *sent)
	}
}

// TestProofreadReminderWaitsForReviewCard 风险检查写入时卡片还没发：先挂着，不复核、不发；审核卡发出后
// （通知 outbox 确认成功）再复核并提醒。不挡流水线：风险检查照常推进到下一阶段。
func TestProofreadReminderWaitsForReviewCard(t *testing.T) {
	env := newPublishTestEnv(t)
	calls := stubProofreadVerify(t, env, testProofreadDefinite)
	captureProofreadReminders(t, env)
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{Level: publishRiskWarning, Headline: "名称写错", Items: testProofreadRiskItems()}, nil
	}
	job, _, err := env.store.create("site:proofread-wait", "https://mp.weixin.qq.com/s/proofread-wait")
	if err != nil {
		t.Fatal(err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageAssessing
		current.Title = "西柚汁"
		current.ContentHTML = "<p>" + testProofreadBody + "</p>"
		current.ContentVersion = 1
		current.ReviewDeliveryAppID = "review-app"
		current.ReviewDeliveryChatIDs = []string{"oc_review"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.pipeline.stageAssessRisk(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	got := mustJob(t, env, job.ID)
	if got.Stage != publishStageStaging || got.ProofreadReminder.State != publishExternalCardPending || got.ProofreadReminder.Want == "" {
		t.Fatalf("风险检查照常推进，校对复核挂起等卡片：stage=%s %+v", got.Stage, got.ProofreadReminder)
	}
	if len(*calls) != 0 {
		t.Fatalf("卡片没发之前不复核：%+v", *calls)
	}
	var waiting publishMonitoringStatus
	updatePublishMonitoringNotifications(&waiting, got, 0)
	if waiting.NotificationPending != 0 {
		t.Fatalf("等审核卡的提醒不算积压通知：%+v", waiting)
	}

	// 审核卡发出：通知 outbox 确认成功后接着复核（没设校对负责人：只记不发）。
	env.pipeline.notifyCardWithID = func(context.Context, map[string]any) (string, error) { return "om_review_card", nil }
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageAwaitingReview
		enqueuePublishNotification(current, publishNotificationReviewPending)
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.dispatchPublishNotification(job.ID)
	if len(*calls) != 1 || (*calls)[0].cardMsgID != "om_review_card" {
		t.Fatalf("卡片发出后才复核：%+v", *calls)
	}
	got = mustJob(t, env, job.ID)
	if r := got.ProofreadReminder; r.State != "" || r.Skipped != "没有设置校对负责人，未发提醒" || len(r.Findings) != 1 || r.Sent != "" {
		t.Fatalf("没设负责人只记不发：%+v", r)
	}
}

// TestProofreadReminderSkipsRejectedAndNoCandidates 驳回的稿不提醒；没有确定条目的稿不复核。
func TestProofreadReminderSkipsRejectedAndNoCandidates(t *testing.T) {
	env := newPublishTestEnv(t)
	setTestProofreadOwners(t, env, publishDistributionOwner{OpenID: "ou_proof"})
	calls := stubProofreadVerify(t, env, testProofreadDefinite)
	sent := captureProofreadReminders(t, env)
	job := seedProofreadJob(t, env, publishStageRejected, true)
	env.pipeline.dispatchProofreadReminder(job.ID)
	if len(*calls) != 0 || len(*sent) != 0 || mustJob(t, env, job.ID).ProofreadReminder.State != "" {
		t.Fatalf("驳回的稿不复核、不提醒：%+v", mustJob(t, env, job.ID).ProofreadReminder)
	}

	plain := publishJob{RiskItems: []publishRiskItem{{Category: publishRiskCategoryNumber, Certainty: publishRiskCertaintyUnverified, Evidence: "x"}}}
	if notePublishProofread(&plain) || plain.ProofreadReminder.State != "" {
		t.Fatalf("没有确定条目不排复核：%+v", plain.ProofreadReminder)
	}
}

// TestProofreadReminderFailureRetriesAndCountsInMonitoring 发送失败：保留 pending 退避重试，错误进监控；
// 部分群发成功的不重发。
func TestProofreadReminderFailureRetriesAndCountsInMonitoring(t *testing.T) {
	env := newPublishTestEnv(t)
	setTestProofreadOwners(t, env, publishDistributionOwner{OpenID: "ou_proof"})
	stubProofreadVerify(t, env, testProofreadDefinite)
	fail := true
	var sends int
	env.pipeline.proofreadReminderSender = func(context.Context, publishJob, string, map[string]any) (string, error) {
		sends++
		if fail {
			return "", errors.New("飞书 500")
		}
		return "om_ok", nil
	}
	job := seedProofreadJob(t, env, publishStageAwaitingReview, true)
	env.pipeline.dispatchProofreadReminder(job.ID)
	got := mustJob(t, env, job.ID)
	if r := got.ProofreadReminder; r.State != publishExternalCardPending || !strings.Contains(r.Error, "飞书 500") || r.Checked != r.Want {
		t.Fatalf("失败应保留 pending 并记错误（复核结果留着，重试不再调模型）：%+v", r)
	}
	var status publishMonitoringStatus
	updatePublishMonitoringNotifications(&status, got, 0)
	if status.NotificationErrors != 1 || status.NotificationPending != 1 {
		t.Fatalf("失败要进监控：%+v", status)
	}
	fail = false
	env.pipeline.dispatchProofreadReminder(job.ID)
	if r := mustJob(t, env, job.ID).ProofreadReminder; r.State != "" || r.Error != "" || r.MessageIDs["oc_review"] != "om_ok" || sends != 2 {
		t.Fatalf("重试成功后清掉错误：%+v sends=%d", r, sends)
	}
}

// TestParsePublishProofreadKeepsOnlyDefiniteGroundedFixes 服务端只收：definite、wrong 逐字在正文与原句里、
// correct 与 wrong 不同且不只差标点。
func TestParsePublishProofreadKeepsOnlyDefiniteGroundedFixes(t *testing.T) {
	candidates := []publishRiskItem{
		{Evidence: "也是类似的出发点：农业水产省数据显示"},
		{Evidence: "螺蛳粉月饼、韭菜月饼、火鸡面月饼层不出穷"},
		{Evidence: "门店选址、门店运营、出品标准化，供应链"},
		{Evidence: "西柚进口量持续下降"},
	}
	source := "也是类似的出发点：农业水产省数据显示。螺蛳粉月饼、韭菜月饼、火鸡面月饼层不出穷。门店选址、门店运营、出品标准化，供应链。西柚进口量持续下降。"
	raw := "```json\n" + `{"items":[
		{"index":1,"definite":true,"wrong":"农业水产省","correct":"农林水产省"},
		{"index":2,"definite":true,"wrong":"层不出穷","correct":"层出不穷"},
		{"index":3,"definite":true,"wrong":"出品标准化，供应链","correct":"出品标准化、供应链"},
		{"index":4,"definite":false,"wrong":"持续下降","correct":"持续下滑"},
		{"index":4,"definite":true,"wrong":"进口量大增","correct":"进口量下降"},
		{"index":1,"definite":true,"wrong":"农业水产省","correct":"农林水产省"},
		{"index":9,"definite":true,"wrong":"农业","correct":"农林"}
	]}` + "\n```"
	findings, err := parsePublishProofread(raw, candidates, source)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(findings))
	for _, finding := range findings {
		got = append(got, finding.Wrong+"→"+finding.Correct)
	}
	if strings.Join(got, " ") != "农业水产省→农林水产省 层不出穷→层出不穷" {
		t.Fatalf("只留确定、有据、不只是标点的改法（去重、越界丢弃）：%v", got)
	}
	if _, err := parsePublishProofread("不是 JSON", candidates, source); err == nil {
		t.Fatal("模型输出不可解析要报错重试，不能当作没有错误")
	}
}

// TestProofreadOwnersSavedSeparately 校对负责人与分发负责人分开存：同一份审核群成员里选，按应用区分。
func TestProofreadOwnersSavedSeparately(t *testing.T) {
	t.Setenv(publishNotificationAppsEnv, `[{"id":"review-app","name":"审核应用","app_id":"cli_review","app_secret":"secret"}]`)
	env := newPublishTestEnv(t)
	setTestProofreadOwners(t, env)
	env.pipeline.distributionOwnerLister = func(context.Context, string, string) ([]publishDistributionOwner, error) {
		return []publishDistributionOwner{{OpenID: "ou_a", Name: "甲"}, {OpenID: "ou_b", Name: "乙"}}, nil
	}
	mux := newPublishTestMux(t, env)
	path := "/api/publish/delivery-settings/proofread-owners"
	if rec := doPublishReq(mux, http.MethodPut, path, `{"open_ids":["ou_b"]}`, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("需要管理员：%d", rec.Code)
	}
	if rec := doPublishReq(mux, http.MethodPut, path, `{"open_ids":["ou_stranger"]}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("不在群里的人不能选：%d %s", rec.Code, rec.Body.String())
	}
	rec := doPublishReq(mux, http.MethodPut, path, `{"open_ids":["ou_b"]}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"proofread_owners":{"owners":[{"open_id":"ou_b","name":"乙"}],"stale":false}`) {
		t.Fatalf("应保存校对负责人：%d %s", rec.Code, rec.Body.String())
	}
	if owners := env.pipeline.publishProofreadOwners("review-app"); len(owners) != 1 || owners[0].OpenID != "ou_b" {
		t.Fatalf("校对提醒应 @ 这位：%+v", owners)
	}
	if owners := env.pipeline.externalDistributionOwners("review-app"); len(owners) != 0 {
		t.Fatalf("分发负责人不受影响：%+v", owners)
	}
	rec = doPublishReq(mux, http.MethodGet, "/api/publish/delivery-settings/distribution-owners", "", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"proofread":{"owners":[{"open_id":"ou_b","name":"乙"}],"stale":false}`) {
		t.Fatalf("成员目录接口应带上校对负责人：%d %s", rec.Code, rec.Body.String())
	}
	settings, _ := env.pipeline.deliverySettings.read()
	kept, err := publishDeliverySelectionFromRequest(publishDeliverySelectionRequest{AppProfileID: "review-app", ChatIDs: []string{"oc_review"}},
		settings, []feishuNotificationApp{{ID: "review-app"}}, []feishuChat{{ChatID: "oc_review"}}, "admin")
	if err != nil || len(kept.ProofreadOwners) != 1 {
		t.Fatalf("同应用改群应保留校对负责人：%+v %v", kept, err)
	}
	moved, err := publishDeliverySelectionFromRequest(publishDeliverySelectionRequest{AppProfileID: "other-app", ChatIDs: []string{"oc_review"}},
		settings, []feishuNotificationApp{{ID: "other-app"}}, []feishuChat{{ChatID: "oc_review"}}, "admin")
	if err != nil || len(moved.ProofreadOwners) != 0 {
		t.Fatalf("换应用后 open_id @ 不到人，应清掉：%+v %v", moved, err)
	}
}

// TestProofreadReminderEndpoint 运维入口：dry_run 只复核不写不发；正式调用重新复核并发到卡片话题；要管理员。
func TestProofreadReminderEndpoint(t *testing.T) {
	env := newPublishTestEnv(t)
	setTestProofreadOwners(t, env, publishDistributionOwner{OpenID: "ou_proof"})
	calls := stubProofreadVerify(t, env, testProofreadDefinite)
	sent := captureProofreadReminders(t, env)
	job := seedProofreadJob(t, env, publishStagePublished, true)
	mux := newPublishTestMux(t, env)
	path := fmt.Sprintf("/api/publish/jobs/%d/proofread-reminder", job.ID)
	if rec := doPublishReq(mux, http.MethodPost, path+"?dry_run=1", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("需要管理员：%d", rec.Code)
	}
	rec := doPublishReq(mux, http.MethodPost, path+"?dry_run=1", "", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"findings":[{"wrong":"农业水产省","correct":"农林水产省"`) || len(*sent) != 0 {
		t.Fatalf("dry_run 只返回复核结果：%d %s sent=%d", rec.Code, rec.Body.String(), len(*sent))
	}
	if !strings.Contains(rec.Body.String(), `"mentions":[{"open_id":"ou_proof"`) {
		t.Fatalf("dry_run 要说清会 @ 谁：%s", rec.Body.String())
	}
	if r := mustJob(t, env, job.ID).ProofreadReminder; r.Checked != "" || len(r.Findings) != 0 {
		t.Fatalf("dry_run 不写库：%+v", r)
	}
	rec = doPublishReq(mux, http.MethodPost, path, "", true)
	if rec.Code != http.StatusOK || len(*sent) != 1 || len(*calls) != 2 {
		t.Fatalf("正式调用应复核并发出：%d %s sent=%d verify=%d", rec.Code, rec.Body.String(), len(*sent), len(*calls))
	}
	if rec := doPublishReq(mux, http.MethodPost, path, "", true); rec.Code != http.StatusOK || len(*sent) != 1 {
		t.Fatalf("同一组错误再调用也不重复 @：%d sent=%d", rec.Code, len(*sent))
	}
}

// TestProofreadSmallEditMatchesProductionDryRun 2026-10-13 生产 dry_run 的 22 处复核结果：改一两个字的
// 真错保留；整块换内容（模型在猜原本该写什么）、跨行的排版残留都不提醒。
func TestProofreadSmallEditMatchesProductionDryRun(t *testing.T) {
	keep := [][2]string{
		{"农业水产省", "农林水产省"}, {"黄黄精粉", "黄精粉"}, {"也会公司创造", "也会为公司创造"}, {"Tiktok", "TikTok"},
		{"2018年的北京冬奥会", "2022年的北京冬奥会"}, {"看看是市面上", "看看市面上"}, {"断长出", "不断长出"},
		{"Vogue Bussiness", "Vogue Business"}, {"腌采", "腌菜"}, {"层不出穷", "层出不穷"}, {"它莫小仙供了", "莫小仙提供了"},
		{"全家“⼀⽇五餐”的全时段⽤餐概念", "全家“一日五餐”的全时段用餐概念"}, {"是品牌是抱着", "是品牌抱着"},
		{"食德乐", "食得乐"}, {"发展美国第一豆类品牌", "发展为美国第一豆类品牌"}, {"馋烤肉烤肉小蛋糕", "馋烤肉小蛋糕"}, {"他们", "它们"},
	}
	for _, pair := range keep {
		if !proofreadSmallEdit(pair[0], pair[1]) {
			t.Errorf("「%s」→「%s」是小改动，应提醒", pair[0], pair[1])
		}
	}
	drop := [][2]string{
		{"伊利伊刻活泉现泡茶", "彩虹糖"}, {"深海海水", "深层地下水"}, {"近500亿元", "约46亿元"}, {"史上过期最快海报", "史上最快过期海报"},
	}
	for _, pair := range drop {
		if proofreadSmallEdit(pair[0], pair[1]) {
			t.Errorf("「%s」→「%s」是在猜原本该写什么，不应提醒", pair[0], pair[1])
		}
	}
	findings, err := parsePublishProofread(`{"items":[{"index":1,"definite":true,"wrong":"提示：\n提示：","correct":"提示："}]}`,
		[]publishRiskItem{{Evidence: "提示：\n提示：本文产品案例为概念作品"}}, "提示：\n提示：本文产品案例为概念作品")
	if err != nil || len(findings) != 0 {
		t.Fatalf("跨行的排版残留不提醒：%+v %v", findings, err)
	}
}

// testProofreadMembers 审核群：真名来自群成员接口，笔名来自各人的飞书资料（英文名或别名）。
func testProofreadMembers(env *publishTestEnv, members ...proofreadMember) {
	env.pipeline.proofreadMemberLister = func(context.Context, string, string) ([]proofreadMember, error) {
		return members, nil
	}
}

func setProofreadByline(t *testing.T, env *publishTestEnv, jobID int64, sourceName, byline string) {
	t.Helper()
	if _, err := env.store.update(jobID, func(current *publishJob) {
		current.SourceName = sourceName
		current.BlocksJSON = `[{"k":"txt","text":"` + byline + `"},{"k":"txt","text":"` + testProofreadBody + `"}]`
	}); err != nil {
		t.Fatal(err)
	}
}

// 2026-10-30 用户裁定：「这篇文章如果是我们的原创文章（作者是编辑的，或者说是我们自己的），有错误的地方你就
// 直接在底下@他，别@我」。原创稿按署名的笔名对上群里的人去 @，不 @ 校对负责人；对不上照旧 @ 校对负责人。
func TestProofreadReminderMentionsOriginalAuthorsByPenName(t *testing.T) {
	members := []proofreadMember{
		{OpenID: "ou_sage", Names: []string{"冷思真", "Sage"}},
		{OpenID: "ou_bobo", Names: []string{"朱未", "Bobo"}},
		{OpenID: "ou_mote", Names: []string{"陈实", "Mote"}},
		{OpenID: "ou_designer", Names: []string{"胖兔"}},
		{OpenID: "ou_proof", Names: []string{"郑梽煌"}},
	}
	cases := []struct {
		name, source, byline string
		want, notWant        []string
	}{
		{"原创稿 @ 作者和编辑", "FBIF食品饮料创新", "作者：Sage，编辑：Bobo，设计：胖兔",
			[]string{`"user_id":"ou_sage"`, `"user_id":"ou_bobo"`},
			[]string{`"user_id":"ou_proof"`, `"user_id":"ou_designer"`, "没在审核群里找到"}},
		{"署名英文名连着中文名、资料只写了英文", "FBIF食品饮料创新", "作者：Mote莫特",
			[]string{`"user_id":"ou_mote"`}, []string{`"user_id":"ou_proof"`}},
		{"原创稿署名对不上群里的人：照旧 @ 校对负责人并说没找到谁", "FBIF食品饮料创新", "作者：Nova",
			[]string{`"user_id":"ou_proof"`, "没在审核群里找到署名里的「Nova」"}, []string{`"user_id":"ou_sage"`}},
		{"转载稿：照旧 @ 校对负责人，不提署名", "观潮新消费", "作者：Sage，编辑：Bobo",
			[]string{`"user_id":"ou_proof"`}, []string{`"user_id":"ou_sage"`, "没在审核群里找到"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newPublishTestEnv(t)
			setTestProofreadOwners(t, env, publishDistributionOwner{OpenID: "ou_proof", Name: "郑梽煌"})
			testProofreadMembers(env, members...)
			stubProofreadVerify(t, env, testProofreadDefinite)
			sent := captureProofreadReminders(t, env)
			job := seedProofreadJob(t, env, publishStagePublished, true)
			setProofreadByline(t, env, job.ID, tc.source, tc.byline)

			env.pipeline.dispatchProofreadReminder(job.ID)
			if len(*sent) != 1 {
				t.Fatalf("应发一条提醒：%+v", *sent)
			}
			post := (*sent)[0].post
			for _, want := range tc.want {
				if !strings.Contains(post, want) {
					t.Fatalf("提醒缺少 %q：%s", want, post)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(post, notWant) {
					t.Fatalf("提醒不该有 %q：%s", notWant, post)
				}
			}
		})
	}
}

// 群成员读不到时不 @ 错人：这一轮记失败、退避重试（与发送失败同一条路）。
func TestProofreadReminderRetriesWhenMembersUnavailable(t *testing.T) {
	env := newPublishTestEnv(t)
	setTestProofreadOwners(t, env, publishDistributionOwner{OpenID: "ou_proof", Name: "郑梽煌"})
	env.pipeline.proofreadMemberLister = func(context.Context, string, string) ([]proofreadMember, error) {
		return nil, errors.New("飞书群成员接口不可达")
	}
	stubProofreadVerify(t, env, testProofreadDefinite)
	sent := captureProofreadReminders(t, env)
	job := seedProofreadJob(t, env, publishStagePublished, true)
	setProofreadByline(t, env, job.ID, "FBIF食品饮料创新", "作者：Sage")

	env.pipeline.dispatchProofreadReminder(job.ID)
	if len(*sent) != 0 {
		t.Fatalf("读不到群成员时不该发：%+v", *sent)
	}
	if r := mustJob(t, env, job.ID).ProofreadReminder; r.Sent != "" || !strings.Contains(r.Error, "飞书群成员接口不可达") {
		t.Fatalf("应记失败等重试：%+v", r)
	}
}

// 同一个笔名对上不止一个人就当没找到：宁可 @ 校对负责人，也不 @ 错人。
func TestMatchProofreadMembersRefusesAmbiguousPenName(t *testing.T) {
	members := []proofreadMember{
		{OpenID: "ou_a", Names: []string{"杜青", "Kiki"}},
		{OpenID: "ou_b", Names: []string{"李琪", "kiki"}},
		{OpenID: "ou_c", Names: []string{"甘甜", "Riri"}},
	}
	matched, missing := matchProofreadMembers([]string{"kiki", "Riri"}, members)
	if len(matched) != 1 || matched[0].OpenID != "ou_c" || len(missing) != 1 || missing[0] != "kiki" {
		t.Fatalf("matched=%+v missing=%v", matched, missing)
	}
}

// 笔名在飞书资料的英文名或别名里；审核应用没开通读通讯录时报错（调用方只用真名、照旧 @ 校对负责人）。
func TestFeishuUserProfileNamesReadsEnNameAndNickname(t *testing.T) {
	denied := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok","expire":7200}`)
		case denied:
			fmt.Fprint(w, `{"code":99991672,"msg":"Access denied. One of the following scopes is required: [contact:contact.base:readonly]"}`)
		case r.URL.Path == "/open-apis/contact/v3/users/ou_mote" && r.URL.Query().Get("user_id_type") == "open_id":
			fmt.Fprint(w, `{"code":0,"data":{"user":{"name":"陈实","en_name":"Mote","nickname":"Mote莫特"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	n := &feishuNotifier{baseURL: srv.URL, appID: "cli_test", appSecret: "secret_test", httpClient: srv.Client()}
	names, err := n.userProfileNames(context.Background(), "ou_mote")
	if err != nil || strings.Join(names, ",") != "Mote,Mote莫特" {
		t.Fatalf("names=%v err=%v", names, err)
	}
	denied = true
	if _, err := n.userProfileNames(context.Background(), "ou_mote"); err == nil || !strings.Contains(err.Error(), "99991672") {
		t.Fatalf("没权限应报错：%v", err)
	}
}
