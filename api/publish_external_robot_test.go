package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

const testRobotArticleID = "1846000000000000001"

// fakeDistributionRobot 自己的分发服务（百家号存草稿）的测试替身：记下每次提交与查询。
type fakeDistributionRobot struct {
	saves      []robotDraftRequest
	platforms  []string
	gets       []string
	saveRecord *robotDraftRecord
	saveErr    error
	getRecord  robotDraftRecord
	getErr     error
	duringSave func(robotDraftRequest)
}

func (f *fakeDistributionRobot) SaveDraft(_ context.Context, platform string, req robotDraftRequest) (robotDraftRecord, error) {
	f.saves = append(f.saves, req)
	f.platforms = append(f.platforms, platform)
	if f.duringSave != nil {
		f.duringSave(req)
	}
	if f.saveErr != nil {
		return robotDraftRecord{}, f.saveErr
	}
	if f.saveRecord != nil {
		return *f.saveRecord, nil
	}
	return testRobotSavedRecord(testRobotArticleID), nil
}

func (f *fakeDistributionRobot) Draft(_ context.Context, platform, key string) (robotDraftRecord, error) {
	f.gets = append(f.gets, platform+"/"+key)
	return f.getRecord, f.getErr
}

func useFakeRobot(env *publishTestEnv) *fakeDistributionRobot {
	fake := &fakeDistributionRobot{}
	env.pipeline.distributionRobot = func() (distributionRobotAPI, error) { return fake, nil }
	return fake
}

func testRobotDraftURL(id string) string {
	return "https://baijiahao.baidu.com/builder/rc/edit?type=news&article_id=" + id
}

func testRobotSavedRecord(id string) robotDraftRecord {
	match := true
	uploaded := true
	return robotDraftRecord{
		Status: robotDraftSaved, ArticleID: id, DraftURL: testRobotDraftURL(id),
		Expected: robotDraftExpected{Headings: 2, Bold: 1, Images: 1, Captions: []string{"图注；图片来源：X"}},
		ReadBack: &robotDraftReadBack{Title: "标题", Headings: 2, Bold: 1, Images: 1, Captions: 1, CoverUploaded: &uploaded},
		Match:    &match, Attempts: 1,
	}
}

// setTestRobotOnlyConfig 只配百家号：整张卡都是分发服务的平台，便于断言不出现「云梭」。
// 也验证只有经分发服务的平台时不要求云梭分类。
func setTestRobotOnlyConfig(t *testing.T) {
	t.Helper()
	setTestYunsuoConfig(t)
	t.Setenv(yunsuoTargetAccountsEnv, `[{"id":"`+testYunsuoBaijiaID+`","name":"百家号"}]`)
	t.Setenv(yunsuoCategoriesEnv, "")
}

var robotKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// TestRobotLaneFreezesDraftBeforeSavingThenLandsDraftSaved 2026-10-22 用户裁定：百家号改走自己的
// 分发服务、只存草稿（头条号 2026-10-24 也切过来）。提交前 lane 已落 pushing + 冻结内容 + 幂等键；存好后
// 记 draft_saved、草稿编号、草稿地址与当场读回的核对；两个平台各存各的，云梭一次都不调。
func TestRobotLaneFreezesDraftBeforeSavingThenLandsDraftSaved(t *testing.T) {
	setTestYunsuoConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	yunsuo := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return yunsuo, nil }
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	robot.duringSave = func(req robotDraftRequest) {
		if len(robot.platforms) == 0 || robot.platforms[len(robot.platforms)-1] != "baijiahao" {
			return
		}
		lane := mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID)
		var frozen robotDraftPayload
		if err := json.Unmarshal([]byte(lane.PayloadJSON), &frozen); err != nil {
			t.Errorf("提交前 lane 上应已冻结草稿内容：%v", err)
		}
		if lane.State != publishExternalDistributionPushing || lane.Driver != externalDriverRobot || lane.IntentKey != req.IdempotencyKey ||
			lane.IntentAt == "" || frozen.Platform != "baijiahao" || frozen.Title != req.Title || frozen.HTML != req.HTML || frozen.CoverURL != req.CoverURL {
			t.Errorf("提交前必须先落 intent：lane=%+v frozen=%+v req=%+v", lane, frozen, req)
		}
	}

	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	if len(yunsuo.pushed) != 0 || fmt.Sprint(robot.platforms) != "[toutiao baijiahao]" {
		t.Fatalf("头条号、百家号都经分发服务各存一次：yunsuo=%v robot=%v", yunsuo.pushed, robot.platforms)
	}
	req := robot.saves[1]
	if !robotKeyPattern.MatchString(req.IdempotencyKey) || !strings.HasPrefix(req.IdempotencyKey, fmt.Sprintf("job-%d.", job.ID)) {
		t.Fatalf("幂等键只用分发服务认的字符：%q", req.IdempotencyKey)
	}
	// 标题原样（不补「｜摘要」），封面是公众号封面、单独上传。
	if req.Title != job.Title || req.CoverURL != job.CoverURL || !strings.Contains(req.HTML, "这是一篇用于验证外部分发流程的原创文章正文。") {
		t.Fatalf("草稿内容不对：%+v", req)
	}
	got := mustJob(t, env, job.ID)
	if lane := mustLane(t, got, testYunsuoToutiaoID); lane.Driver != externalDriverRobot || lane.State != publishExternalDistributionDraftSaved {
		t.Fatalf("头条号也经分发服务存草稿：%+v", lane)
	}
	lane := mustLane(t, got, testYunsuoBaijiaID)
	if lane.State != publishExternalDistributionDraftSaved || lane.ContentPK != testRobotArticleID || lane.DraftURL != testRobotDraftURL(testRobotArticleID) ||
		lane.CompletedAt == "" || lane.Title != job.Title || lane.URL != "" {
		t.Fatalf("存好草稿应记 draft_saved、草稿编号与地址：%+v", lane)
	}
	if f := lane.Fidelity; f.CheckedAt == "" || f.Note != "" || f.Expected.Headings != 2 || f.Online.CaptionedImages != 1 {
		t.Fatalf("读回核对应记在 lane 上：%+v", f)
	}

	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	if !strings.Contains(body, "<font color='green'>草稿已存（未发布）</font>") ||
		!strings.Contains(body, "**百家号**　[去百家号草稿箱查看]("+testRobotDraftURL(testRobotArticleID)+")") {
		t.Fatalf("卡片应写草稿已存并给草稿箱链接: %s", body)
	}
	status := currentPublishMonitoring(env.store, false, time.Now())
	if status.Processing != 0 || status.Failed != 0 || status.ReconciliationRequired != 0 || status.ExternalFidelityMismatches != 0 || status.ExternalFidelityUnavailable != 0 {
		t.Fatalf("存好的草稿是终态，不算处理中或失败：%+v", status)
	}
	if out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, got, nil)); len(robot.saves) != 2 || yunsuo.pushCalls != 0 || out.toastContent != "草稿都已存好，无需重复操作" {
		t.Fatalf("存好草稿后再点不得再提交：out=%+v saves=%d", out, len(robot.saves))
	}
}

func mustDistributeValue(t *testing.T, job publishJob, slot *int) json.RawMessage {
	t.Helper()
	value := map[string]any{
		"action": feishuCardActionDistributeExternal, "job_id": job.ID,
		"distribution_version": publishExternalDistributionCardVersion(job),
	}
	if slot != nil {
		value["slot"] = *slot
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestRobotLaneOutcomes 分发服务每类回答落到 lane 上是什么：点存草稿之前被拒 → failed、卡片可重新存；
// 草稿箱要人删 → 待核对；结果不明 → 当场按同一个键查一次。系统自己从不再提交。
func TestRobotLaneOutcomes(t *testing.T) {
	rejected := func(status int, code, message string) error {
		return &robotRejectedError{Status: status, Code: code, Message: message}
	}
	uncertain := &robotUncertainError{Cause: "HTTP 502 platform_error：百家号编辑器没有出现"}
	platformFailed := "百家号编辑器没有出现"
	for _, tc := range []struct {
		name      string
		saveErr   error
		getRecord robotDraftRecord
		getErr    error
		state     string
		reason    string
		gets      int
		retry     bool
	}{
		{name: "参数不对", saveErr: rejected(400, "invalid_request", "title 要 2–64 字"), state: publishExternalDistributionFailed, reason: "分发工具没存草稿：title 要 2–64 字", retry: true},
		{name: "token 不对", saveErr: rejected(401, "unauthorized", "token 不对"), state: publishExternalDistributionFailed, reason: distributionAPITokenEnv, retry: true},
		{name: "键已用于别的内容", saveErr: rejected(409, "idempotency_key_reused", "这个键已用来存过别的内容"), state: publishExternalDistributionFailed, reason: "这个键已用来存过别的内容", retry: true},
		{name: "封面下不来", saveErr: rejected(422, "cover_unavailable", "封面下不下来"), state: publishExternalDistributionFailed, reason: "封面下不下来", retry: true},
		{name: "服务器没登录", saveErr: rejected(503, "login_required", "没登录"), state: publishExternalDistributionFailed, reason: "远程画面里登录", retry: true},
		{name: "草稿箱要人删", saveErr: &robotReconcileError{Message: "草稿箱里这次新建的同名草稿有 2 篇，请删到只剩一篇"}, state: publishExternalDistributionReconciliationRequired, reason: "同名草稿有 2 篇"},
		{name: "平台出错后查到已存", saveErr: uncertain, getRecord: testRobotSavedRecord("1846000000000000002"), state: publishExternalDistributionDraftSaved, gets: 1},
		{name: "平台出错后查到失败", saveErr: uncertain, getRecord: robotDraftRecord{Status: robotDraftFailed, Error: &platformFailed}, state: publishExternalDistributionFailed, reason: "百家号编辑器没有出现；可以重新存", gets: 1, retry: true},
		{name: "超时后查无此键", saveErr: &robotUncertainError{Cause: "网络请求失败或超时"}, getErr: errRobotDraftNotFound, state: publishExternalDistributionFailed, reason: "分发工具没收到这次请求", gets: 1, retry: true},
		{name: "超时后还在存", saveErr: &robotUncertainError{Cause: "网络请求失败或超时"}, getRecord: robotDraftRecord{Status: robotDraftSaving}, state: publishExternalDistributionReconciliationRequired, reason: "不会重复存稿", gets: 1},
		{name: "超时后要人核对", saveErr: &robotUncertainError{Cause: "网络请求失败或超时"}, getRecord: robotDraftRecord{Status: robotDraftUnknown}, state: publishExternalDistributionReconciliationRequired, reason: "草稿箱里的结果要人核对", gets: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setTestRobotOnlyConfig(t)
			t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
			env := newPublishTestEnv(t)
			robot := useFakeRobot(env)
			robot.saveErr, robot.getRecord, robot.getErr = tc.saveErr, tc.getRecord, tc.getErr
			job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
			if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
				t.Fatal(err)
			}
			got := mustJob(t, env, job.ID)
			lane := mustLane(t, got, testYunsuoBaijiaID)
			if lane.State != tc.state || !strings.Contains(lane.Error, tc.reason) || len(robot.gets) != tc.gets {
				t.Fatalf("应为 %s（%q），GET %d 次：lane=%+v gets=%v", tc.state, tc.reason, tc.gets, lane, robot.gets)
			}
			if tc.gets > 0 && robot.gets[0] != "baijiahao/"+lane.IntentKey {
				t.Fatalf("应按提交时的幂等键查：%v key=%q", robot.gets, lane.IntentKey)
			}
			if lane.State != publishExternalDistributionDraftSaved && (lane.ContentPK != "" || lane.DraftURL != "") {
				t.Fatalf("没存成不得记草稿编号：%+v", lane)
			}
			card := cardJSON(t, buildReviewDoneCard(got))
			if strings.Contains(card, "云梭") {
				t.Fatalf("经分发服务的平台不提云梭: %s", card)
			}
			if hasRetry := strings.Contains(card, `"content":"重新存草稿至百家号"`); hasRetry != tc.retry {
				t.Fatalf("重新存草稿按钮应为 %v: %s", tc.retry, card)
			}
			// 重启恢复与兜底补查都只查不提交。
			env.pipeline.recoverExternalDistribution(got)
			env.pipeline.refreshExternalDistributionResults()
			if len(robot.saves) != 1 {
				t.Fatalf("系统不得自己再提交，实际 %d 次", len(robot.saves))
			}
		})
	}
}

// TestRobotLaneRetryReusesKeyAndSucceeds 卡片上「重新存草稿」：内容没变就是同一个幂等键，分发服务
// 对失败记录从头再做、对存好的直接返回，不会多建一篇。
func TestRobotLaneRetryReusesKeyAndSucceeds(t *testing.T) {
	setTestRobotOnlyConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	robot.saveErr = &robotRejectedError{Status: 503, Code: "login_required", Message: "没登录"}
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	failed := mustJob(t, env, job.ID)
	robot.saveErr = nil
	slot := 0
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, failed, &slot))
	if out.toastType != "success" || out.toastContent != "已受理，正在存草稿至百家号" {
		t.Fatalf("重新存草稿应受理：%+v", out)
	}
	if len(robot.saves) != 2 || robot.saves[0].IdempotencyKey != robot.saves[1].IdempotencyKey {
		t.Fatalf("同一份内容重新存用同一个键：%+v", robot.saves)
	}
	if lane := mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID); lane.State != publishExternalDistributionDraftSaved || lane.RequestedBy != "ou_reviewer_1" {
		t.Fatalf("重新存草稿应闭环：%+v", lane)
	}
}

// TestRobotLaneRestartRecoveryChecksByKey 进程在提交窗口里退出：分发服务可能存了也可能没存。恢复时
// 转待核对、按同一个键查；查到存好就记 draft_saved，还在存就留着等兜底补查，绝不重新提交。
func TestRobotLaneRestartRecoveryChecksByKey(t *testing.T) {
	setTestRobotOnlyConfig(t)
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	interrupted := testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionPushing)
	interrupted.Driver = externalDriverRobot
	interrupted.IntentKey = "job-1.22222222-2222-4222-8222-222222222222.abc"
	interrupted.PayloadJSON = `{"platform":"baijiahao","title":"中国食品品牌如何用产品创新打开新市场","html":"<p>正文</p>","cover_url":"https://cdn.example.com/a.jpg"}`
	interrupted.RequestedAt = time.Now().UTC().Format(time.RFC3339)
	job = setTestLanes(t, env, job.ID, interrupted)

	robot.getRecord = robotDraftRecord{Status: robotDraftSaving}
	env.pipeline.recoverExternalDistribution(job)
	lane := mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID)
	if lane.State != publishExternalDistributionReconciliationRequired || !strings.Contains(lane.Error, "进程在存草稿时中断") ||
		fmt.Sprint(robot.gets) != "[baijiahao/"+interrupted.IntentKey+"]" {
		t.Fatalf("恢复应转待核对并按同一个键查：%+v gets=%v", lane, robot.gets)
	}
	if state := externalPlatformStatus(mustJob(t, env, job.ID), yunsuoAccountTarget{ID: testYunsuoBaijiaID, Name: "百家号"}); state.Status != "待核对" || strings.Contains(state.Reason, "云梭") {
		t.Fatalf("待核对的说法不提云梭：%+v", state)
	}

	robot.getRecord = testRobotSavedRecord("1846000000000000003")
	env.pipeline.refreshExternalDistributionResults()
	lane = mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID)
	if lane.State != publishExternalDistributionDraftSaved || lane.ContentPK != "1846000000000000003" || len(robot.gets) != 2 || len(robot.saves) != 0 {
		t.Fatalf("兜底补查查到存好应记 draft_saved、不重新提交：%+v gets=%v saves=%d", lane, robot.gets, len(robot.saves))
	}
	env.pipeline.refreshExternalDistributionResults()
	if len(robot.gets) != 2 {
		t.Fatalf("定了的 lane 不再查：%v", robot.gets)
	}
}

// TestRobotSweepNotBlockedByYunsuoOutage 兜底补查时云梭客户端建不起来（配置坏了）：经云梭的 lane 这轮
// 不查，经分发服务的 lane 照查。
func TestRobotSweepNotBlockedByYunsuoOutage(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return nil, errors.New("云梭发布未配置") }
	robot := useFakeRobot(env)
	robot.getRecord = testRobotSavedRecord("1846000000000000004")
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	first := createPublishedDistributionJobWithKey(t, env, "site:sweep-yunsuo", "FBIF食品饮料创新")
	toutiao := testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionReconciliationRequired)
	toutiao.ContentPK, toutiao.RequestedAt = "pk-toutiao", recent
	setTestLanes(t, env, first.ID, toutiao)
	second := createPublishedDistributionJobWithKey(t, env, "site:sweep-robot", "FBIF食品饮料创新")
	waiting := testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionReconciliationRequired)
	waiting.Driver, waiting.IntentKey, waiting.RequestedAt = externalDriverRobot, "job-2.baijiahao.abc", recent
	waiting.PayloadJSON = `{"platform":"baijiahao","title":"t","html":"<p>x</p>","cover_url":"https://cdn.example.com/a.jpg"}`
	setTestLanes(t, env, second.ID, waiting)

	env.pipeline.refreshExternalDistributionResults()
	if lane := mustLane(t, mustJob(t, env, second.ID), testYunsuoBaijiaID); lane.State != publishExternalDistributionDraftSaved || len(robot.gets) != 1 {
		t.Fatalf("云梭坏了也要查分发服务的 lane：%+v gets=%v", lane, robot.gets)
	}
}

// TestLegacyBaijiahaoYunsuoLanesStayOnYunsuo 改走分发服务之前经云梭推的百家号（lane 没有 driver）：
// 照旧按云梭单号补查、按云梭的说法显示，进程中断也按云梭处理；分发服务一概不碰。
func TestLegacyBaijiahaoYunsuoLanesStayOnYunsuo(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	yunsuo := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return yunsuo, nil }
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	legacy := testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionReconciliationRequired)
	legacy.ContentPK = "pk-legacy-baijia"
	legacy.IntentKey = "legacy-intent"
	legacy.RequestedAt = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	job = setTestLanes(t, env, job.ID, legacy)
	target := yunsuoAccountTarget{ID: testYunsuoBaijiaID, Name: "百家号"}
	if state := externalPlatformStatus(job, target); state.Status != "待核对" || !strings.Contains(state.Reason, "云梭后台") {
		t.Fatalf("旧的云梭百家号 lane 照旧按云梭说：%+v", state)
	}

	env.pipeline.refreshExternalDistributionResults()
	lane := mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID)
	if yunsuo.statusCalls != 1 || len(robot.gets) != 0 || lane.State != publishExternalDistributionSucceeded || lane.Driver != externalDriverYunsuo {
		t.Fatalf("旧 lane 按云梭单号补查：status=%d robotGets=%d lane=%+v", yunsuo.statusCalls, len(robot.gets), lane)
	}
	if state := externalPlatformStatus(mustJob(t, env, job.ID), target); state.Status != "已发布" {
		t.Fatalf("云梭发出去的百家号照旧是已发布：%+v", state)
	}

	pushing := testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionPushing)
	pushing.IntentKey = "legacy-pushing"
	job = setTestLanes(t, env, job.ID, pushing)
	env.pipeline.recoverExternalDistribution(job)
	lane = mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID)
	if lane.State != publishExternalDistributionReconciliationRequired || !strings.Contains(lane.Error, "云梭推送窗口中断") || len(robot.gets) != 0 {
		t.Fatalf("旧 lane 中断按云梭处理：%+v robotGets=%v", lane, robot.gets)
	}

	// 头条号的线上正文核对只取经云梭发出去的 lane。
	robotLane := testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionSucceeded)
	robotLane.Driver, robotLane.URL, robotLane.PayloadJSON = externalDriverRobot, "https://www.toutiao.com/article/1/", `{"platform":"baijiahao"}`
	robotLane.RequestedAt = time.Now().UTC().Format(time.RFC3339)
	if externalLaneNeedsFidelity(robotLane, yunsuoPlatformProfiles["toutiao"], time.Now()) {
		t.Fatal("经分发服务的 lane 不走头条线上正文核对")
	}
}

// TestRobotLaneCardWording 百家号一行的按钮、确认框、状态与链接照实说「存草稿」「不会发布」，不提云梭。
func TestRobotLaneCardWording(t *testing.T) {
	setTestRobotOnlyConfig(t)
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	section := func(lanes ...publishExternalLane) string {
		t.Helper()
		job.ExternalDistributionLanes = lanes
		card := buildReviewBatchCard([]publishJob{job})
		if strings.Contains(cardJSON(t, card), "云梭") {
			t.Fatalf("经分发服务的平台不提云梭: %s", cardJSON(t, card))
		}
		return distributionSectionJSON(t, card)
	}
	robotLane := func(state string) publishExternalLane {
		lane := testLane(testYunsuoBaijiaID, "百家号", state)
		lane.Driver = externalDriverRobot
		return lane
	}

	body := section()
	for _, want := range []string{`"content":"存草稿至百家号"`, `"content":"存草稿至百家号？"`, "不会发布", "草稿箱确认版式"} {
		if !strings.Contains(body, want) {
			t.Fatalf("没存过时缺少 %q: %s", want, body)
		}
	}
	failed := robotLane(publishExternalDistributionFailed)
	failed.Error = "服务器上的百家号没登录，请在分发工具的远程画面里登录后重新存草稿"
	body = section(failed)
	for _, want := range []string{`"content":"重新存草稿至百家号"`, "<font color='red'>上次存草稿失败</font>", "远程画面里登录", "不会重复存稿，也不会发布"} {
		if !strings.Contains(body, want) {
			t.Fatalf("失败时缺少 %q: %s", want, body)
		}
	}
	if body = section(robotLane(publishExternalDistributionPushing)); !strings.Contains(body, "<font color='blue'>存草稿中</font>") || strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("存草稿中不给按钮: %s", body)
	}
	saved := robotLane(publishExternalDistributionDraftSaved)
	saved.ContentPK, saved.DraftURL = testRobotArticleID, testRobotDraftURL(testRobotArticleID)
	saved.Fidelity = robotDraftFidelity(robotDraftRecord{
		Expected: robotDraftExpected{Headings: 3}, ReadBack: &robotDraftReadBack{Headings: 2},
		Mismatches: []string{"小标题：送去 3 处，读回 2 处"},
	}, "百家号", "2026-10-22T02:00:00Z")
	body = section(saved)
	for _, want := range []string{"<font color='green'>草稿已存（未发布）</font>", "[去百家号草稿箱查看](" + testRobotDraftURL(testRobotArticleID) + ")",
		"<font color='grey'>草稿读回对不上：小标题：送去 3 处，读回 2 处</font>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("草稿已存时缺少 %q: %s", want, body)
		}
	}
	if strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("草稿已存不再给按钮: %s", body)
	}
	// 草稿地址不在百家号域名内就不给，退回百家号后台。
	saved.DraftURL = ""
	if body = section(saved); !strings.Contains(body, "[去百家号后台查看](https://baijiahao.baidu.com/builder/rc/content)") {
		t.Fatalf("没有草稿地址时给百家号后台: %s", body)
	}

	// 侧边栏同一套说法：按钮后半句、草稿地址。
	cfg, err := loadYunsuoConfig()
	if err != nil {
		t.Fatalf("只有经分发服务的平台时不要求云梭分类：%v", err)
	}
	saved.DraftURL = testRobotDraftURL(testRobotArticleID)
	job.ExternalDistributionLanes = []publishExternalLane{saved}
	platforms := externalEditView(job, cfg)["platforms"].([]externalEditPlatform)
	if len(platforms) != 1 || platforms[0].Status != "草稿已存（未发布）" || platforms[0].DraftURL != testRobotDraftURL(testRobotArticleID) ||
		platforms[0].Action != "存草稿至百家号" || platforms[0].Editable || platforms[0].TitleMin != 2 || platforms[0].TitleMax != 64 {
		t.Fatalf("侧边栏百家号状态不对：%+v", platforms)
	}
}

// TestRobotDraftFidelityFromReadBack 分发服务存完当场读回：对不上写进 Note（monitoring 报 mismatch），
// 读回失败按取满次数记（monitoring 报 unavailable）。
func TestRobotDraftFidelityFromReadBack(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	match := false
	mismatch := robotDraftFidelity(robotDraftRecord{
		Expected: robotDraftExpected{Headings: 3, Images: 2, Captions: []string{"a", "b"}},
		ReadBack: &robotDraftReadBack{Headings: 3, Images: 2, Captions: 1}, Match: &match,
		Mismatches: []string{"图注：送去 2 条，读回 1 条", "封面不是上传的那张"},
	}, "百家号", now)
	if mismatch.CheckedAt != now || mismatch.Note != "草稿读回对不上：图注：送去 2 条，读回 1 条；封面不是上传的那张" ||
		mismatch.Expected.CaptionedImages != 2 || mismatch.Online.CaptionedImages != 1 {
		t.Fatalf("对不上应逐条写进 Note：%+v", mismatch)
	}
	readBackError := "编辑页没打开"
	failed := robotDraftFidelity(robotDraftRecord{ReadBackError: &readBackError}, "百家号", now)
	if failed.CheckedAt != "" || failed.Attempts != externalFidelityMaxAttempts || failed.Error != "读回草稿失败：编辑页没打开" {
		t.Fatalf("读回失败按取满次数记：%+v", failed)
	}
	job := publishJob{ExternalDistributionLanes: []publishExternalLane{{Fidelity: mismatch}, {Fidelity: failed}}}
	if mismatched, unavailable := externalFidelityMonitoring(job, time.Now()); mismatched != 1 || unavailable != 1 {
		t.Fatalf("monitoring 应各报一条：mismatched=%d unavailable=%d", mismatched, unavailable)
	}
}

// TestRobotDraftFidelityNotesModelAssists 分发服务找不到按钮、改点了模型挑的元素（百家号改版）：
// 卡片百家号那行灰字写出来，与读回对不上用「；」接；读回失败时也写。
func TestRobotDraftFidelityNotesModelAssists(t *testing.T) {
	setTestRobotOnlyConfig(t)
	now := time.Now().UTC().Format(time.RFC3339)
	coverOpen := robotDraftAssist{Step: "cover.open", Wanted: []string{"选择封面", "更换"}, Picked: "替换封面"}
	save := robotDraftAssist{Step: "saveDraft", Wanted: []string{"存草稿"}, Picked: "保存"}
	match, mismatch := true, false
	readBack := &robotDraftReadBack{Headings: 1}

	for _, tc := range []struct {
		name   string
		record robotDraftRecord
		want   string
	}{
		{"读回对得上", robotDraftRecord{ReadBack: readBack, Match: &match, Assists: []robotDraftAssist{coverOpen, save}},
			"百家号改版：找不到「选择封面」或「更换」，模型改点了「替换封面」；找不到「存草稿」，模型改点了「保存」"},
		{"读回也对不上", robotDraftRecord{ReadBack: readBack, Match: &mismatch, Mismatches: []string{"封面不是送去的那张"}, Assists: []robotDraftAssist{coverOpen}},
			"百家号改版：找不到「选择封面」或「更换」，模型改点了「替换封面」；草稿读回对不上：封面不是送去的那张"},
		{"没用模型", robotDraftRecord{ReadBack: readBack, Match: &match}, ""},
	} {
		if got := robotDraftFidelity(tc.record, "百家号", now); got.Note != tc.want || got.CheckedAt != now {
			t.Fatalf("%s：Note=%q，应为 %q", tc.name, got.Note, tc.want)
		}
	}

	readBackError := "编辑页没打开"
	unread := robotDraftFidelity(robotDraftRecord{ReadBackError: &readBackError, Assists: []robotDraftAssist{save}}, "百家号", now)
	if unread.CheckedAt != "" || unread.Note != "百家号改版：找不到「存草稿」，模型改点了「保存」" {
		t.Fatalf("读回失败也写模型改点了什么：%+v", unread)
	}
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	lane := testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionDraftSaved)
	lane.Driver, lane.ContentPK, lane.DraftURL, lane.Fidelity = externalDriverRobot, testRobotArticleID, testRobotDraftURL(testRobotArticleID), unread
	job.ExternalDistributionLanes = []publishExternalLane{lane}
	if body := distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job})); !strings.Contains(body, "<font color='grey'>百家号改版：找不到「存草稿」，模型改点了「保存」</font>") {
		t.Fatalf("卡片百家号那行应写出模型改点了什么: %s", body)
	}
}

// TestRobotIdempotencyKeyFollowsFrozenContent 同一份冻结内容同一个键；内容变了换键；只用分发服务认的字符。
func TestRobotIdempotencyKeyFollowsFrozenContent(t *testing.T) {
	first := robotIdempotencyKey(282, testYunsuoBaijiaID, "sha256:aaaa")
	if first != robotIdempotencyKey(282, testYunsuoBaijiaID, "sha256:aaaa") || first == robotIdempotencyKey(282, testYunsuoBaijiaID, "sha256:bbbb") {
		t.Fatalf("键应跟着冻结内容走：%q", first)
	}
	long := robotIdempotencyKey(282, "账号 id/带空格"+strings.Repeat("x", 200), "sha256:"+strings.Repeat("f", 64))
	if !robotKeyPattern.MatchString(first) || !robotKeyPattern.MatchString(long) {
		t.Fatalf("键只能用 A-Za-z0-9._:- 且不超过 128 位：%q %q", first, long)
	}
}

// TestRobotLaneStopsBeforeSubmitWhenUnconfigured 没配分发服务：提交前就失败、不落 intent，配好后可重新存。
func TestRobotLaneStopsBeforeSubmitWhenUnconfigured(t *testing.T) {
	setTestRobotOnlyConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	lane := mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID)
	if lane.State != publishExternalDistributionFailed || !strings.Contains(lane.Error, "缺少 "+distributionAPIURLEnv) || lane.IntentKey != "" || !externalLaneCanSafeRetry(lane) {
		t.Fatalf("没配分发服务应在提交前失败、可重新存：%+v", lane)
	}
}
