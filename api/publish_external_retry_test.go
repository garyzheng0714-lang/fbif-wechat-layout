package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// useRetryClock 流水线和卡片用同一个钟。
func useRetryClock(t *testing.T, env *publishTestEnv, value string) *cstClock {
	t.Helper()
	clock := newCSTClock(value)
	env.pipeline.now = func() time.Time { return clock.now }
	saved := externalCardNow
	externalCardNow = func() time.Time { return clock.now.UTC() }
	t.Cleanup(func() { externalCardNow = saved })
	return clock
}

// TestExternalAutoRetryRepushesFailedLane 2026-11-12 #367 三个平台没发出去，都是人一个个补的；用户：「这样每次都需要人介入的
// 流程不是好的流程，我非常讨厌这样」。自动外发没存成的平台不等人点：卡片写几点自己重试、不告警，到点按同一个键再交，
// 存好就发，lane 上照旧记原来的请求人和请求时间。
func TestExternalAutoRetryRepushesFailedLane(t *testing.T) {
	setTestExternalAccounts(t, testArticleAccount(testBaijiaAccountID, "baijiahao", true))
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	clock := useRetryClock(t, env, "2026-11-12 10:00")
	robot := useFakeRobot(env)
	robot.saveErr = &robotRejectedError{Status: 422, Code: "image_too_large", Message: "第 1 张图 5.6MB，超过 5MB"}
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	markAutoPending(t, env, job.ID, publishExternalAutoKindPublish)
	env.pipeline.runExternalAutoDistribution()

	failed := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if failed.State != publishExternalDistributionFailed || failed.AutoRetries != 0 {
		t.Fatalf("没存成记失败：%+v", failed)
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(mustJob(t, env, job.ID)))
	if !strings.Contains(body, "10:10 自动重试；分发工具没存草稿：第 1 张图 5.6MB，超过 5MB") {
		t.Fatalf("卡片写几点自己重试: %s", body)
	}
	if status := currentPublishMonitoring(env.store, false, clock.now); status.Failed != 0 || status.FailedRecent != 0 {
		t.Fatalf("系统还会自己重试的不告警：%+v", status)
	}

	clock.set("2026-11-12 10:09")
	env.pipeline.retryExternalLanes()
	if len(robot.saves) != 1 {
		t.Fatalf("没到点不推：%d", len(robot.saves))
	}
	clock.set("2026-11-12 10:10")
	robot.saveErr = nil
	env.pipeline.retryExternalLanes()
	lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if len(robot.saves) != 2 || robot.saves[1].IdempotencyKey != failed.IntentKey ||
		fmt.Sprint(robot.publishes) != "[baijiahao/"+failed.IntentKey+"/"+publishExternalAutoActor+"]" {
		t.Fatalf("到点按同一个键再交、存好就发：saves=%+v publishes=%v", robot.saves, robot.publishes)
	}
	if lane.State != publishExternalDistributionSucceeded || lane.URL != testRobotArticleURL("baijiahao", testRobotArticleID) ||
		lane.AutoRetries != 1 || lane.RequestedBy != publishExternalAutoActor || lane.RequestedAt != failed.RequestedAt {
		t.Fatalf("重推发出去，请求人和请求时间照旧：%+v", lane)
	}
	env.pipeline.retryExternalLanes()
	if len(robot.saves) != 2 {
		t.Fatalf("发出去了就不再推：%d", len(robot.saves))
	}
}

// TestExternalAutoRetryResavesHeldDraft 存好的草稿读回没对上（#367 头条号封面没换上）：那篇不能发，到点按现在的内容另存一篇，
// 键后面加 .r2；对上了就发，发布记的还是原来点的人。旧草稿留着不动。
func TestExternalAutoRetryResavesHeldDraft(t *testing.T) {
	setTestRobotOnlyConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	clock := useRetryClock(t, env, "2026-11-12 14:00")
	robot := useFakeRobot(env)
	mismatched := testRobotSavedRecord(testRobotArticleID)
	match := false
	mismatched.Match, mismatched.Mismatches = &match, []string{"封面：送去的没换上"}
	robot.saveRecord = &mismatched
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	held := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if held.State != publishExternalDistributionDraftSaved || len(robot.publishes) != 0 {
		t.Fatalf("对不上的草稿不发：%+v", held)
	}

	const second = "1846000000000000002"
	fresh := testRobotSavedRecord(second)
	robot.saveRecord = &fresh
	clock.set("2026-11-12 14:10")
	env.pipeline.retryExternalLanes()
	lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if len(robot.saves) != 2 || robot.saves[1].IdempotencyKey != held.IntentKey+".r2" {
		t.Fatalf("另存一篇用新键：%+v", robot.saves)
	}
	if fmt.Sprint(robot.publishes) != "[baijiahao/"+held.IntentKey+".r2/ou_reviewer_1]" || lane.State != publishExternalDistributionSucceeded ||
		lane.ContentPK != second || lane.DraftAttempt != 2 || lane.AutoRetries != 1 || lane.RequestedBy != "ou_reviewer_1" {
		t.Fatalf("新草稿对上了就发，记原来点的人：publishes=%v lane=%+v", robot.publishes, lane)
	}
	if kept := robot.records[held.IntentKey]; kept.ArticleID != testRobotArticleID || kept.Action != nil {
		t.Fatalf("旧草稿不动：%+v", kept)
	}
}

// TestExternalAutoRetryBacksOffThenDailyForAWeek 一直不成：10 分钟、1 小时、4 小时各一次，之后每天一次，最初请求后 7 天为止；
// 只在北京时间 9–21 点推。前 3 次退避用完才算失败、告警。
func TestExternalAutoRetryBacksOffThenDailyForAWeek(t *testing.T) {
	setTestExternalAccounts(t, testArticleAccount(testBaijiaAccountID, "baijiahao", true))
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	clock := useRetryClock(t, env, "2026-11-12 18:00")
	robot := useFakeRobot(env)
	robot.saveErr = &robotRejectedError{Status: 422, Code: "cover_too_small", Message: "封面太小"}
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	markAutoPending(t, env, job.ID, publishExternalAutoKindPublish)
	env.pipeline.runExternalAutoDistribution()
	target := testArticleAccount(testBaijiaAccountID, "baijiahao", true)

	var pushed []string
	for range 20 {
		lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
		at, ok := externalLaneNextAutoRetry(lane, target, clock.now)
		if !ok {
			break
		}
		if status := currentPublishMonitoring(env.store, false, clock.now); (status.Failed != 0) != (lane.AutoRetries >= 3) {
			t.Fatalf("第 %d 次重推之后：前 3 次不告警、用完才告警：%+v", lane.AutoRetries, status)
		}
		clock.now = at
		env.pipeline.retryExternalLanes()
		pushed = append(pushed, at.In(publishExternalAutoZone).Format("01-02 15:04"))
	}
	want := "[11-12 18:10 11-12 19:10 11-13 09:00 11-14 09:00 11-15 09:00 11-16 09:00 11-17 09:00 11-18 09:00 11-19 09:00]"
	if fmt.Sprint(pushed) != want || len(robot.saves) != 1+len(pushed) {
		t.Fatalf("重推的时间：%v（saves=%d）\n要的是 %s", pushed, len(robot.saves), want)
	}
	clock.set("2026-11-21 10:00")
	env.pipeline.retryExternalLanes()
	if len(robot.saves) != 1+len(pushed) {
		t.Fatalf("过了 7 天不再推：%d", len(robot.saves))
	}
	if status := currentPublishMonitoring(env.store, false, clock.now); status.Failed != 1 {
		t.Fatalf("不再推的照常算失败：%+v", status)
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(mustJob(t, env, job.ID)))
	if strings.Contains(body, "自动重试") || !strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("不再自己推的卡片不写自动重试、留着重新同步按钮: %s", body)
	}
}

// TestExternalAutoRetryLeavesAlone 不自己重推的：号后来取消了「自动外发」的自动外发 lane；待核对、没通过的；最初请求在 7 天前的旧 lane。
func TestExternalAutoRetryLeavesAlone(t *testing.T) {
	setTestExternalAccounts(t, testArticleAccount(testBaijiaAccountID, "baijiahao", true))
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	clock := useRetryClock(t, env, "2026-11-12 10:00")
	robot := useFakeRobot(env)
	robot.saveErr = &robotRejectedError{Status: 422, Code: "cover_too_small", Message: "封面太小"}
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	markAutoPending(t, env, job.ID, publishExternalAutoKindPublish)
	env.pipeline.runExternalAutoDistribution()

	setTestExternalAccounts(t, testArticleAccount(testBaijiaAccountID, "baijiahao", false))
	clock.set("2026-11-12 11:00")
	env.pipeline.retryExternalLanes()
	if len(robot.saves) != 1 {
		t.Fatalf("取消了自动外发的号不再自己推：%d", len(robot.saves))
	}
	if status := currentPublishMonitoring(env.store, false, clock.now); status.Failed != 1 {
		t.Fatalf("不会再自己推的照常算失败：%+v", status)
	}

	now := newCSTClock("2026-11-12 10:00").now
	human := testArticleAccount(testBaijiaAccountID, "baijiahao", false)
	for _, state := range []string{publishExternalDistributionReconciliationRequired, publishExternalDistributionRejected,
		publishExternalDistributionSucceeded, publishExternalLaneManual, publishExternalDistributionPublishing} {
		lane := testRobotLaneIn(job.ID, state)
		lane.RequestedBy, lane.RequestedAt, lane.CompletedAt = "ou_reviewer_1", "2026-11-12T01:00:00Z", "2026-11-12T01:00:00Z"
		if _, ok := externalLaneNextAutoRetry(lane, human, now); ok {
			t.Fatalf("%s 不自己重推", state)
		}
	}
	old := testRobotLaneIn(job.ID, publishExternalDistributionDraftSaved)
	old.RequestedBy = "ou_reviewer_1"
	if _, ok := externalLaneNextAutoRetry(old, human, now); ok {
		t.Fatal("最初请求在 7 天前的旧 lane（2026-10-30 之前只存草稿的）不自己重推")
	}
}

// TestExternalLoginRequiredWaitsForLogin 2026-11-20 腾讯号在分发服务器上掉登录一周没人扫码：每天还去开一遍登录页，
// 告警按篇一天天往上加（failed_jobs_recent 2→5），过了 7 天的两篇悄悄不推了。号没登录是号的事：等着的篇不按退避去碰平台、
// 不算失败，每个号只报一条 external_login_required（带人话和怎么办）；过了 7 天也不丢；中台送来这次失败之后查到的「已登录」
// 就马上接着推（晚上等到 9 点），不用人点。
func TestExternalLoginRequiredWaitsForLogin(t *testing.T) {
	account := testArticleAccount(testBaijiaAccountID, "baijiahao", true)
	account.Login, account.LoginCheckedAt = externalAccountLoggedIn, newCSTClock("2026-11-12 09:00").now.UTC()
	setTestExternalAccounts(t, account)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	clock := useRetryClock(t, env, "2026-11-12 10:00")
	robot := useFakeRobot(env)
	robot.saveErr = &robotRejectedError{Status: 503, Code: robotLoginRequired, Message: "没登录"}
	var jobs []publishJob
	for _, key := range []string{"site:login-a", "site:login-b"} {
		job := createPublishedDistributionJobWithKey(t, env, key, "FBIF食品饮料创新")
		markAutoPending(t, env, job.ID, publishExternalAutoKindPublish)
		jobs = append(jobs, job)
	}
	env.pipeline.runExternalAutoDistribution()
	for _, job := range jobs {
		if lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID); lane.State != publishExternalDistributionFailed || !lane.LoginRequired {
			t.Fatalf("没登录记下来：%+v", lane)
		}
	}
	// 分发服务把「要登录」写回表格，中台送过来。
	account.Login, account.LoginCheckedAt = "要登录", clock.now.UTC()
	setTestExternalAccounts(t, account)

	wantAlert := "external_login_required:baijiahao:2"
	checkWaiting := func(when string) {
		t.Helper()
		status := currentPublishMonitoring(env.store, false, clock.now)
		hint := status.AlertHints[wantAlert]
		if fmt.Sprint(status.AlertIssues) != "["+wantAlert+"]" || status.FailedRecent != 0 || status.Failed != 0 ||
			!strings.Contains(hint.Meaning, fmt.Sprintf("2 篇原创停着没同步（#%d、#%d）", jobs[0].ID, jobs[1].ID)) ||
			!strings.Contains(hint.Action, "cli.mjs login "+testBaijiaAccountID) {
			t.Fatalf("%s：每个号只报一条、带人话和怎么办，不算失败：%+v", when, status)
		}
		body := distributionSectionJSON(t, buildReviewDoneCard(mustJob(t, env, jobs[0].ID)))
		if strings.Contains(body, "自动重试") || !strings.Contains(body, "没登录，登好后系统自己重新同步") {
			t.Fatalf("%s：卡片说登好后自己同步，不写几点重试: %s", when, body)
		}
	}
	checkWaiting("刚失败")
	for _, when := range []string{"2026-11-12 10:10", "2026-11-12 14:00", "2026-11-13 09:00", "2026-11-20 10:00"} {
		clock.set(when)
		env.pipeline.retryExternalLanes()
		if len(robot.saves) != 2 {
			t.Fatalf("%s：没登好不去碰平台：%d", when, len(robot.saves))
		}
	}
	checkWaiting("过了 7 天")

	// 晚上有人扫码登好：等到 9 点再推。
	clock.set("2026-11-20 22:00")
	account.Login, account.LoginCheckedAt = externalAccountLoggedIn, clock.now.UTC()
	setTestExternalAccounts(t, account)
	robot.saveErr = nil
	env.pipeline.retryExternalLanes()
	if len(robot.saves) != 2 {
		t.Fatalf("晚上不推：%d", len(robot.saves))
	}
	if status := currentPublishMonitoring(env.store, false, clock.now); len(status.AlertIssues) != 0 || status.Failed != 0 {
		t.Fatalf("登好了、等着推的不告警：%+v", status)
	}
	clock.set("2026-11-21 09:00")
	env.pipeline.retryExternalLanes()
	for _, job := range jobs {
		lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
		if lane.State != publishExternalDistributionSucceeded || lane.LoginRequired || lane.RequestedBy != publishExternalAutoActor {
			t.Fatalf("登好后接着推出去：%+v", lane)
		}
	}
	if len(robot.saves) != 4 {
		t.Fatalf("两篇各推一次：%d", len(robot.saves))
	}
	if status := currentPublishMonitoring(env.store, false, clock.now); len(status.AlertIssues) != 0 {
		t.Fatalf("推出去了告警清掉：%+v", status)
	}
}

// TestExternalLoginWaitEdges 中台没送登录状态的照普通退避；「已登录」是失败之前查的不算登好；超过 30 天的不再等；
// 2026-11-20 之前没记 LoginRequired 的按卡片上那句认。
func TestExternalLoginWaitEdges(t *testing.T) {
	now := newCSTClock("2026-11-20 10:00").now
	lane := testRobotLaneIn(1, publishExternalDistributionFailed)
	lane.RequestedBy, lane.RequestedAt, lane.CompletedAt = "ou_reviewer_1", "2026-11-13T01:00:00Z", "2026-11-19T07:00:00Z"
	lane.Error = "服务器上的腾讯号没登录，请在分发工具的远程画面里登录后重新同步"
	account := testArticleAccount(testBaijiaAccountID, "baijiahao", false)
	if got := externalLaneLogin(lane, account, now); got != externalLoginNone {
		t.Fatalf("中台没送登录状态照普通退避：%v", got)
	}
	account.Login, account.LoginCheckedAt = externalAccountLoggedIn, time.Date(2026, 11, 19, 6, 0, 0, 0, time.UTC)
	if got := externalLaneLogin(lane, account, now); got != externalLoginWaiting {
		t.Fatalf("旧 lane 按那句认；「已登录」是失败之前查的，还在等：%v", got)
	}
	account.LoginCheckedAt = time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	if got := externalLaneLogin(lane, account, now); got != externalLoginBack {
		t.Fatalf("失败之后查到登着：%v", got)
	}
	if at, ok := externalLaneNextAutoRetry(lane, account, now); !ok || !at.Equal(now.UTC()) {
		t.Fatalf("登好了马上推，不管 7 天：%v %v", at, ok)
	}
	if got := externalLaneLogin(lane, account, newCSTClock("2026-12-14 10:00").now); got != externalLoginNone {
		t.Fatalf("超过 30 天不再等：%v", got)
	}
	other := lane
	other.Error = "分发工具没存草稿：封面太小"
	if got := externalLaneLogin(other, account, now); got != externalLoginNone {
		t.Fatalf("别的原因照普通退避：%v", got)
	}
}

func TestExternalAutoRetryDaytime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-12 12:30": "11-12 12:30",
		"2026-11-12 07:15": "11-12 09:00",
		"2026-11-12 20:59": "11-12 20:59",
		"2026-11-12 21:00": "11-13 09:00",
		"2026-11-12 23:40": "11-13 09:00",
	} {
		if got := externalAutoRetryDaytime(newCSTClock(in).now).In(publishExternalAutoZone).Format("01-02 15:04"); got != want {
			t.Errorf("%s → %s，要 %s", in, got, want)
		}
	}
}
