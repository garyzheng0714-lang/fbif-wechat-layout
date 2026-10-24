package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// setTestYunsuoAutoConfig 头条号标 auto、百家号仍只走按钮（2026-10-10 用户裁定）。
func setTestYunsuoAutoConfig(t *testing.T) {
	t.Helper()
	setTestYunsuoConfig(t)
	setTestExternalAccounts(t, testArticleAccount(testYunsuoToutiaoID, "toutiao", true), testArticleAccount(testYunsuoBaijiaID, "baijiahao", false))
}

func newToutiaoOnlyYunsuo() *fakeYunsuoAPI {
	return &fakeYunsuoAPI{
		contentPK: "content-pk-auto",
		statuses: []yunsuoAccountStatus{
			{Status: 1, Remark: "OK", AccountPool: yunsuoAccountPool{ID: testYunsuoToutiaoID, Name: "头条号"}},
		},
	}
}

// cstClock 可推进的北京时间时钟，配额按北京时间自然日与白天窗口计算。
type cstClock struct{ now time.Time }

func newCSTClock(value string) *cstClock {
	at, err := time.ParseInLocation("2006-01-02 15:04", value, publishExternalAutoZone)
	if err != nil {
		panic(err)
	}
	return &cstClock{now: at}
}

func (c *cstClock) set(value string) {
	at, err := time.ParseInLocation("2006-01-02 15:04", value, publishExternalAutoZone)
	if err != nil {
		panic(err)
	}
	c.now = at
}

func markAutoPending(t *testing.T, env *publishTestEnv, jobID int64, kind string) {
	t.Helper()
	if _, err := env.store.update(jobID, func(current *publishJob) {
		current.ExternalDistributionAutoPending = kind
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExternalAutoDistributionAfterAutoPublishPushesOnlyAutoTargets(t *testing.T) {
	useLegacyYunsuoToutiao(t)
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	enableAutoPublish(t, env)
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: "测试原创标题：一家食品品牌的产品创新", SourceName: "FBIF食品饮料创新", CanonicalURL: articleURL, BlocksJSON: testPublishBlocksJSON}, nil
	}
	// 草稿进站后的网站 id 固定为 7240（桩），预置成已公开的网站稿供外发读取。
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: 7240, Title: "测试原创标题：一家食品品牌的产品创新", CoverImg: "https://cdn.example.com/a.jpg",
		Content: testDistributionLiveHTML, Summary: "食品品牌的产品创新。", SourceName: "FBIF食品饮料创新",
		PublishStatus: foodTalksPublishStatusPublished,
	})
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != publishStagePublished {
		t.Fatalf("应先自动公开，实际 %s error=%q", got.Stage, got.Error)
	}
	if fake.pushCalls != 1 {
		t.Fatalf("原创公开后应自动推一次云梭，实际 %d", fake.pushCalls)
	}
	if len(fake.payload.AccountIDs) != 1 || fake.payload.AccountIDs[0] != testYunsuoToutiaoID {
		t.Fatalf("自动外发只发 auto 账号（头条号），实际 %v", fake.payload.AccountIDs)
	}
	lane := mustLane(t, got, testYunsuoToutiaoID)
	if lane.RequestedBy != publishExternalAutoActor || !strings.HasPrefix(lane.Attribution, "original_auto_confirmed: ") {
		t.Fatalf("应留下系统自动外发留痕：%+v", lane)
	}
	if lane.State != publishExternalDistributionSucceeded || got.ExternalDistributionAutoPending != "" {
		t.Fatalf("应外发成功并清掉待办：lane=%+v pending=%q", lane, got.ExternalDistributionAutoPending)
	}
	if len(got.ExternalDistributionLanes) != 1 {
		t.Fatalf("任务只应有头条号一条 lane：%+v", got.ExternalDistributionLanes)
	}
}

func TestExternalAutoDistributionSkipsRepost(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	enableAutoPublish(t, env)
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }

	// 默认 fetch 的公众号是「测试公众号」，不是自家号 → 转载。
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStagePublished {
		t.Fatalf("转载稿仍应在网站公开，实际 %s", got.Stage)
	}
	if fake.pushCalls != 0 || got.ExternalDistributionAutoPending != "" || len(got.ExternalDistributionLanes) != 0 {
		t.Fatalf("转载稿不得自动外发：push=%d pending=%q lanes=%+v", fake.pushCalls, got.ExternalDistributionAutoPending, got.ExternalDistributionLanes)
	}
}

func TestExternalAutoDistributionNeedsAutoAccount(t *testing.T) {
	setTestYunsuoConfig(t) // 两个账号都没标 auto
	job := publishJob{Stage: publishStagePublished, SourceName: "FBIF食品饮料创新", BlocksJSON: testPublishBlocksJSON}
	markExternalAutoDistributionPending(&job)
	if job.ExternalDistributionAutoPending != "" {
		t.Fatalf("没有 auto 账号时不应记自动外发待办")
	}
	setTestYunsuoAutoConfig(t)
	markExternalAutoDistributionPending(&job)
	if job.ExternalDistributionAutoPending != publishExternalAutoKindPublish {
		t.Fatalf("有 auto 账号的原创稿应记待办，实际 %q", job.ExternalDistributionAutoPending)
	}
	distributed := publishJob{Stage: publishStagePublished, SourceName: "FBIF食品饮料创新", BlocksJSON: testPublishBlocksJSON,
		ExternalDistributionLanes: []publishExternalLane{testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded)}}
	markExternalAutoDistributionPending(&distributed)
	if distributed.ExternalDistributionAutoPending != "" {
		t.Fatalf("已外发过的稿不得再记待办")
	}
}

// sentLane 某时刻（北京时间）由 by 推给 accountID 的一条已受理 lane。
func sentLane(t *testing.T, at, by, accountID string) publishExternalLane {
	t.Helper()
	requested, err := time.ParseInLocation("2006-01-02 15:04", at, publishExternalAutoZone)
	if err != nil {
		t.Fatal(err)
	}
	lane := testLane(accountID, "", publishExternalDistributionSucceeded)
	lane.ContentPK = "pk"
	lane.RequestedAt = requested.UTC().Format(time.RFC3339)
	lane.RequestedBy = by
	return lane
}

func jobsWithLanes(lanes ...publishExternalLane) []publishJob {
	jobs := make([]publishJob, 0, len(lanes))
	for _, lane := range lanes {
		jobs = append(jobs, publishJob{ExternalDistributionLanes: []publishExternalLane{lane}})
	}
	return jobs
}

// TestExternalAutoQuotaRules 名额按平台各算各的：每天上限取平台规则（头条号 5 篇），补发
// 每天 ≤3 篇、9–20 点、同一平台间隔 ≥1 小时；头条号满了不挡百家号。
func TestExternalAutoQuotaRules(t *testing.T) {
	auto := []externalAccount{{ID: testYunsuoToutiaoID, Name: "头条号", Auto: true}, {ID: testYunsuoBaijiaID, Name: "百家号", Auto: true}}
	clock := newCSTClock("2026-10-12 10:00")
	toutiaoLimit := yunsuoPlatformProfiles["toutiao"].DailyLimit
	if toutiaoLimit != 5 {
		t.Fatalf("头条号文章日上限应为 5，实际 %d", toutiaoLimit)
	}

	quota := externalAutoQuotaFor(nil, auto, clock.now)
	if !quota.allows(publishExternalAutoKindPublish, testYunsuoToutiaoID, clock.now) || !quota.allows(publishExternalAutoKindBackfill, testYunsuoBaijiaID, clock.now) {
		t.Fatalf("空配额应放行")
	}
	if quota.allows(publishExternalAutoKindPublish, "not-auto", clock.now) {
		t.Fatalf("非 auto 账号不得自动推")
	}
	// 昨天的、人工登记的、受理前失败的都不占今天头条名额；百家号的只占百家号的。
	preFail := sentLane(t, "2026-10-12 09:00", "ou_x", testYunsuoToutiaoID)
	preFail.State, preFail.ContentPK = publishExternalDistributionFailed, ""
	manual := sentLane(t, "2026-10-12 09:10", "ou_x", testYunsuoToutiaoID)
	manual.State, manual.ContentPK = publishExternalLaneManual, ""
	quota = externalAutoQuotaFor(jobsWithLanes(
		sentLane(t, "2026-10-11 23:30", publishExternalAutoActor, testYunsuoToutiaoID),
		sentLane(t, "2026-10-12 08:00", "ou_x", testYunsuoBaijiaID),
		preFail, manual,
	), auto, clock.now)
	if quota.today[testYunsuoToutiaoID] != 0 || quota.today[testYunsuoBaijiaID] != 1 {
		t.Fatalf("今天名额按平台各算：%+v", quota.today)
	}

	// 人工按钮发的也计入；头条号满 5 篇后头条停，百家号照推。
	var five []publishExternalLane
	for i := 0; i < toutiaoLimit; i++ {
		five = append(five, sentLane(t, "2026-10-12 08:0"+fmt.Sprint(i), "ou_reviewer", testYunsuoToutiaoID))
	}
	quota = externalAutoQuotaFor(jobsWithLanes(five...), auto, clock.now)
	if quota.allows(publishExternalAutoKindPublish, testYunsuoToutiaoID, clock.now) {
		t.Fatalf("头条号当天已满 %d 篇应停", toutiaoLimit)
	}
	if !quota.allows(publishExternalAutoKindPublish, testYunsuoBaijiaID, clock.now) {
		t.Fatalf("头条号满了不得挡百家号")
	}

	// 补发：40 分钟前刚推过头条 → 头条等满 1 小时，百家号不受影响；夜里不推；当天补发满 3 篇停。
	quota = externalAutoQuotaFor(jobsWithLanes(sentLane(t, "2026-10-12 09:20", publishExternalAutoActor, testYunsuoToutiaoID)), auto, clock.now)
	if quota.allows(publishExternalAutoKindBackfill, testYunsuoToutiaoID, clock.now) {
		t.Fatalf("补发与上一篇间隔不足 1 小时应等待")
	}
	if !quota.allows(publishExternalAutoKindBackfill, testYunsuoBaijiaID, clock.now) {
		t.Fatalf("间隔按平台算，头条刚推过不挡百家号补发")
	}
	if !quota.allows(publishExternalAutoKindPublish, testYunsuoToutiaoID, clock.now) {
		t.Fatalf("新公开稿不受补发间隔限制")
	}
	night := newCSTClock("2026-10-12 21:30")
	if externalAutoQuotaFor(nil, auto, night.now).allows(publishExternalAutoKindBackfill, testYunsuoToutiaoID, night.now) {
		t.Fatalf("补发只在白天推")
	}
	var threeBackfill []publishExternalLane
	for _, at := range []string{"2026-10-12 06:00", "2026-10-12 07:00", "2026-10-12 08:00"} {
		threeBackfill = append(threeBackfill, sentLane(t, at, publishExternalAutoBackfillActor, testYunsuoToutiaoID))
	}
	quota = externalAutoQuotaFor(jobsWithLanes(threeBackfill...), auto, clock.now)
	if quota.allows(publishExternalAutoKindBackfill, testYunsuoToutiaoID, clock.now) {
		t.Fatalf("当天补发满 %d 篇应停", publishExternalAutoBackfillDailyLimit)
	}
	if !quota.allows(publishExternalAutoKindPublish, testYunsuoToutiaoID, clock.now) {
		t.Fatalf("补发配额用完不影响新稿")
	}
	// 同一篇的两个平台各占各的名额。
	quota = externalAutoQuotaFor([]publishJob{{ExternalDistributionLanes: []publishExternalLane{
		sentLane(t, "2026-10-12 09:00", publishExternalAutoActor, testYunsuoToutiaoID),
		sentLane(t, "2026-10-12 09:40", publishExternalAutoBackfillActor, testYunsuoBaijiaID),
	}}}, auto, clock.now)
	if quota.today[testYunsuoToutiaoID] != 1 || quota.today[testYunsuoBaijiaID] != 1 || quota.backfill[testYunsuoBaijiaID] != 1 {
		t.Fatalf("同篇两平台应各计一次：today=%+v backfill=%+v", quota.today, quota.backfill)
	}
	if quota.allows(publishExternalAutoKindBackfill, testYunsuoBaijiaID, clock.now) || !quota.allows(publishExternalAutoKindBackfill, testYunsuoToutiaoID, clock.now) {
		t.Fatalf("百家号 20 分钟前刚补过应等，头条号 1 小时前推过可补")
	}
}

func TestExternalAutoBackfillPacesAcrossHoursAndDays(t *testing.T) {
	useLegacyYunsuoToutiao(t)
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	var ids []int64
	for i := 0; i < 4; i++ {
		job := createPublishedDistributionJobWithKey(t, env, fmt.Sprintf("site:backfill-%d", i), "FBIF食品饮料创新")
		ids = append(ids, job.ID)
	}
	since := time.Date(2026, 8, 1, 0, 0, 0, 0, publishExternalAutoZone)

	preview, err := env.pipeline.markExternalAutoBackfill(since, true)
	if err != nil || len(preview) != 4 || fake.pushCalls != 0 {
		t.Fatalf("dry run 应列出 4 篇且不推送：n=%d err=%v push=%d", len(preview), err, fake.pushCalls)
	}
	if got, _ := env.store.byID(ids[0]); got.ExternalDistributionAutoPending != "" {
		t.Fatalf("dry run 不得落待办")
	}

	if marked, err := env.pipeline.markExternalAutoBackfill(since, false); err != nil || len(marked) != 4 {
		t.Fatalf("应记 4 篇补发待办：n=%d err=%v", len(marked), err)
	}
	if fake.pushCalls != 1 {
		t.Fatalf("同一时刻只补发 1 篇，实际 %d", fake.pushCalls)
	}
	clock.set("2026-10-12 10:30")
	env.pipeline.runExternalAutoDistribution()
	if fake.pushCalls != 1 {
		t.Fatalf("不足 1 小时不应补发下一篇，实际 %d", fake.pushCalls)
	}
	clock.set("2026-10-12 11:00")
	env.pipeline.runExternalAutoDistribution()
	clock.set("2026-10-12 12:00")
	env.pipeline.runExternalAutoDistribution()
	if fake.pushCalls != 3 {
		t.Fatalf("到点应各补 1 篇，实际 %d", fake.pushCalls)
	}
	clock.set("2026-10-12 13:30")
	env.pipeline.runExternalAutoDistribution()
	if fake.pushCalls != 3 {
		t.Fatalf("当天补发满 3 篇后应停，实际 %d", fake.pushCalls)
	}
	clock.set("2026-10-13 08:30")
	env.pipeline.runExternalAutoDistribution()
	if fake.pushCalls != 3 {
		t.Fatalf("次日 9 点前不补发，实际 %d", fake.pushCalls)
	}
	clock.set("2026-10-13 09:00")
	env.pipeline.runExternalAutoDistribution()
	if fake.pushCalls != 4 {
		t.Fatalf("次日应补完最后 1 篇，实际 %d", fake.pushCalls)
	}
	for i, id := range ids {
		got, _ := env.store.byID(id)
		lane := mustLane(t, got, testYunsuoToutiaoID)
		if lane.State != publishExternalDistributionSucceeded || got.ExternalDistributionAutoPending != "" || lane.RequestedBy != publishExternalAutoBackfillActor {
			t.Fatalf("第 %d 篇补发未闭环：lane=%+v pending=%q", i, lane, got.ExternalDistributionAutoPending)
		}
	}
	if again, err := env.pipeline.markExternalAutoBackfill(since, false); err != nil || len(again) != 0 {
		t.Fatalf("已外发的稿不得再次补发：n=%d err=%v", len(again), err)
	}
}

func TestExternalAutoDistributionNewPublishBeatsBackfill(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	old := createPublishedDistributionJobWithKey(t, env, "site:old", "FBIF食品饮料创新")
	fresh := createPublishedDistributionJobWithKey(t, env, "site:fresh", "FBIF食品饮料创新")
	markAutoPending(t, env, old.ID, publishExternalAutoKindBackfill)
	markAutoPending(t, env, fresh.ID, publishExternalAutoKindPublish)

	env.pipeline.runExternalAutoDistribution()
	gotFresh, _ := env.store.byID(fresh.ID)
	gotOld, _ := env.store.byID(old.ID)
	if len(gotFresh.ExternalDistributionLanes) == 0 {
		t.Fatalf("新公开稿应优先外发")
	}
	if len(gotOld.ExternalDistributionLanes) != 0 || gotOld.ExternalDistributionAutoPending != publishExternalAutoKindBackfill {
		t.Fatalf("刚推过新稿，补发应等满间隔：lanes=%+v pending=%q", gotOld.ExternalDistributionLanes, gotOld.ExternalDistributionAutoPending)
	}
}

func TestExternalAutoDistributionIneligibleClearsPending(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	// 网站上这篇已改成转载：本地口径说原创也不能外发，待办作废并留原因。
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL, Content: testDistributionLiveHTML,
		SourceName: "FBIF食品饮料创新", SourceType: 1, PublishStatus: foodTalksPublishStatusPublished,
	})
	markAutoPending(t, env, job.ID, publishExternalAutoKindBackfill)
	env.pipeline.now = func() time.Time { return newCSTClock("2026-10-12 10:00").now }

	env.pipeline.runExternalAutoDistribution()
	got, _ := env.store.byID(job.ID)
	if fake.pushCalls != 0 || len(got.ExternalDistributionLanes) != 0 {
		t.Fatalf("网站标为转载的稿不得外发：push=%d lanes=%+v", fake.pushCalls, got.ExternalDistributionLanes)
	}
	if got.ExternalDistributionAutoPending != "" || !strings.Contains(got.ExternalDistributionAutoError, "转载") {
		t.Fatalf("应清掉待办并留原因：pending=%q err=%q", got.ExternalDistributionAutoPending, got.ExternalDistributionAutoError)
	}
}

func TestExternalAutoDistributionTransientErrorKeepsPending(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	env.pipeline.foodTalks = func() (foodTalksAPI, error) { return nil, fmt.Errorf("网站凭证暂不可用") }
	markAutoPending(t, env, job.ID, publishExternalAutoKindPublish)

	env.pipeline.runExternalAutoDistribution()
	got, _ := env.store.byID(job.ID)
	if fake.pushCalls != 0 || got.ExternalDistributionAutoPending != publishExternalAutoKindPublish {
		t.Fatalf("读网站失败应保留待办下轮再试：push=%d pending=%q", fake.pushCalls, got.ExternalDistributionAutoPending)
	}
	if !strings.Contains(got.ExternalDistributionAutoError, "网站凭证暂不可用") {
		t.Fatalf("应记下最近一次失败原因：%q", got.ExternalDistributionAutoError)
	}
}

func TestExternalAutoDistributionTurnedOffDropsPending(t *testing.T) {
	setTestYunsuoConfig(t) // 没有 auto 账号
	env := newPublishTestEnv(t)
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	markAutoPending(t, env, job.ID, publishExternalAutoKindBackfill)

	env.pipeline.runExternalAutoDistribution()
	got, _ := env.store.byID(job.ID)
	if fake.pushCalls != 0 || got.ExternalDistributionAutoPending != "" {
		t.Fatalf("关闭自动外发后待办应作废：push=%d pending=%q", fake.pushCalls, got.ExternalDistributionAutoPending)
	}
}

// TestExternalDistributionCardPushesRetryAndMissingSeparately 卡片按钮推「受理前失败的」和
// 「还没推过的」平台，每个平台单独一次推送；已受理的平台不碰。
func TestExternalDistributionCardPushesRetryAndMissingSeparately(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	fake := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job = setTestLanes(t, env, job.ID, testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionFailed))
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("受理前失败应可安全重试：%v", err)
	}
	// 切换前经云梭、受理前就失败的头条号，重试改经分发服务存草稿；百家号首推。云梭一次都不调。
	if len(fake.pushed) != 0 || fmt.Sprint(robot.platforms) != "[toutiao baijiahao]" {
		t.Fatalf("应按平台各存一次（头条重试 + 百家号首推），不经云梭：%v robot=%v", fake.pushed, robot.platforms)
	}
	got, _ := env.store.byID(job.ID)
	for _, id := range []string{testYunsuoToutiaoID, testYunsuoBaijiaID} {
		if lane := mustLane(t, got, id); lane.State != publishExternalDistributionDraftSaved || lane.Driver != externalDriverRobot || lane.RequestedBy != "ou_reviewer_1" {
			t.Fatalf("两个平台都应由审核人存好草稿：%+v", lane)
		}
	}

	// 头条号已受理（有单号）但失败：卡片不再给它重推，只剩没推过的平台。
	second := createPublishedDistributionJobWithKey(t, env, "site:accepted-failed", "FBIF食品饮料创新")
	accepted := testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionFailed)
	accepted.ContentPK = "pk-accepted"
	second = setTestLanes(t, env, second.ID, accepted)
	fake.pushed = nil
	if _, err := env.pipeline.requestExternalDistribution(second.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(second)); err != nil {
		t.Fatal(err)
	}
	if len(fake.pushed) != 0 || len(robot.saves) != 3 || robot.platforms[2] != "baijiahao" {
		t.Fatalf("云梭已受理的头条号不得重推，只存百家号：%v robot=%v", fake.pushed, robot.platforms)
	}
	if lane := mustLane(t, mustJob(t, env, second.ID), testYunsuoToutiaoID); lane.ContentPK != "pk-accepted" || lane.State != publishExternalDistributionFailed {
		t.Fatalf("头条号 lane 不得被改：%+v", lane)
	}
}

// TestExternalDistributionLanesFailIndependently 一个平台的推送前检查不过（封面不够头条号
// 452x352）只让这个平台停下来等人改：不推、不建 lane、不占名额；其他平台照推。
func TestExternalDistributionLanesFailIndependently(t *testing.T) {
	setTestYunsuoConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	stubYunsuoImageSize(t, map[string][2]int{"https://cdn.example.com/a.jpg": {300, 200}})
	env := newPublishTestEnv(t)
	fake := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("百家号能推，整体请求不应失败：%v", err)
	}
	if len(fake.pushed) != 0 || fmt.Sprint(robot.platforms) != "[baijiahao]" {
		t.Fatalf("只应存百家号：%v robot=%v", fake.pushed, robot.platforms)
	}
	got := mustJob(t, env, job.ID)
	if _, ok := externalLaneFor(got, testYunsuoToutiaoID); ok {
		t.Fatalf("头条号检查不过，不得建 lane（不占名额、不算推过）：%+v", got.ExternalDistributionLanes)
	}
	draft, _ := externalDraftFor(got, testYunsuoToutiaoID)
	cover := externalIssueFields(draft.Issues)[publishExternalIssueCover]
	if cover.Width != 300 || cover.Height != 200 || cover.MinWidth != 452 || cover.MinHeight != 352 {
		t.Fatalf("头条号应记下封面问题与现状：%+v", draft)
	}
	if lane := mustLane(t, got, testYunsuoBaijiaID); lane.State != publishExternalDistributionDraftSaved {
		t.Fatalf("百家号应照常存好草稿：%+v", lane)
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	if !strings.Contains(body, "<font color='orange'>封面太小</font>：头条号封面至少 452×352，现在 300×200") || !strings.Contains(body, "保存并存草稿至头条号") {
		t.Fatalf("卡片应在头条号块里写清封面问题并给修改表单: %s", body)
	}

	// 所有平台都要改：整体报「要先改好」，不落任何推送。
	setTestExternalAccounts(t, testArticleAccount(testYunsuoToutiaoID, "toutiao", false))
	other := createPublishedDistributionJobWithKey(t, env, "site:all-fail", "FBIF食品饮料创新")
	fake.pushed = nil
	if _, err := env.pipeline.requestExternalDistribution(other.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(other)); !errors.Is(err, errExternalLanesNeedFix) {
		t.Fatalf("没有任何平台能推时应报要先改好：%v", err)
	}
	if len(fake.pushed) != 0 {
		t.Fatalf("不得推送：%v", fake.pushed)
	}
}

func mustJob(t *testing.T, env *publishTestEnv, id int64) publishJob {
	t.Helper()
	job, err := env.store.byID(id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestExternalAutoBackfillHTTP(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	body := `{"since":"2026-08-01T00:00:00+08:00","dry_run":true}`
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/external-distribution/auto-backfill", body, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无管理员令牌应 401，实际 %d", rec.Code)
	}
	createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	createPublishedDistributionJobWithKey(t, env, "site:repost", "别人家公众号")
	rec := doPublishReq(mux, http.MethodPost, "/api/publish/external-distribution/auto-backfill", body, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":1`) || !strings.Contains(rec.Body.String(), `"dry_run":true`) {
		t.Fatalf("dry run 应只列出 1 篇原创，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/external-distribution/auto-backfill", `{"since":"bad"}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("since 非法应 400，实际 %d", rec.Code)
	}
}

func TestExternalDistributionLateRefreshResolvesTimedOutAndRetried(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	fake := &fakeYunsuoAPI{statuses: []yunsuoAccountStatus{
		{Status: 0, Remark: "等待发布", AccountPool: yunsuoAccountPool{ID: testYunsuoToutiaoID, Name: "头条号"}},
	}}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	requested := clock.now.Add(-20 * time.Minute).UTC().Format(time.RFC3339)
	timedOut := createPublishedDistributionJobWithKey(t, env, "site:late-timeout", "FBIF食品饮料创新")
	lane := testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionReconciliationRequired)
	lane.ContentPK, lane.RequestedAt = "pk-timeout", requested
	lane.Error = "云梭在轮询时限内未返回终态；系统不会重复发布，请人工核对"
	setTestLanes(t, env, timedOut.ID, lane)
	noPK := createPublishedDistributionJobWithKey(t, env, "site:late-nopk", "FBIF食品饮料创新")
	failedNoPK := testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionFailed)
	failedNoPK.RequestedAt = requested
	setTestLanes(t, env, noPK.ID, failedNoPK)

	// 云梭仍在排队：留在对账，只记查询时间；30 分钟内不重复查。
	env.pipeline.refreshExternalDistributionResults()
	got := mustLane(t, mustJob(t, env, timedOut.ID), testYunsuoToutiaoID)
	if fake.statusCalls != 1 || got.State != publishExternalDistributionReconciliationRequired || got.LastPolledAt == "" {
		t.Fatalf("排队中应只记查询：calls=%d lane=%+v", fake.statusCalls, got)
	}
	clock.set("2026-10-12 10:20")
	env.pipeline.refreshExternalDistributionResults()
	if fake.statusCalls != 1 {
		t.Fatalf("30 分钟内不应重复补查，calls=%d", fake.statusCalls)
	}

	// 云梭发布完成：补成成功并刷卡；全程只查询、从不重推。
	fake.statuses[0] = yunsuoAccountStatus{Status: 1, Remark: "OK", AccountPool: yunsuoAccountPool{ID: testYunsuoToutiaoID, Name: "头条号"}}
	clock.set("2026-10-12 10:40")
	env.pipeline.refreshExternalDistributionResults()
	gotJob := mustJob(t, env, timedOut.ID)
	got = mustLane(t, gotJob, testYunsuoToutiaoID)
	if got.State != publishExternalDistributionSucceeded || got.Error != "" || got.CompletedAt == "" {
		t.Fatalf("云梭迟到的成功应补回：lane=%+v", got)
	}
	if fake.pushCalls != 0 || fake.statusCalls != 2 {
		t.Fatalf("补查只能查询：push=%d status=%d", fake.pushCalls, fake.statusCalls)
	}
	if l := mustLane(t, mustJob(t, env, noPK.ID), testYunsuoToutiaoID); l.State != publishExternalDistributionFailed || l.LastPolledAt != "" {
		t.Fatalf("没有单号的失败不得补查：%+v", l)
	}
}

func TestExternalDistributionLateRefreshPicksUpProviderRetry(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	// 首次头条报异常、百家号登录失效；云梭自动重试后头条成功、百家号仍失败。
	fake := &fakeYunsuoAPI{statuses: []yunsuoAccountStatus{
		{Status: 1, Remark: "OK", AccountPool: yunsuoAccountPool{ID: testYunsuoToutiaoID, Name: "头条号"}},
		{Status: 2, Remark: "账号多次登录失败,等待用户更新账号", AccountPool: yunsuoAccountPool{ID: testYunsuoBaijiaID, Name: "百家号"}},
	}}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	failed := func(id, name, requestedAt string) publishExternalLane {
		lane := testLane(id, name, publishExternalDistributionFailed)
		lane.ContentPK, lane.RequestedAt = "pk-retry", requestedAt
		return lane
	}
	old := clock.now.Add(-50 * 24 * time.Hour).UTC().Format(time.RFC3339)
	setTestLanes(t, env, job.ID, failed(testYunsuoToutiaoID, "头条号", old), failed(testYunsuoBaijiaID, "百家号", old))
	env.pipeline.refreshExternalDistributionResults()
	if fake.statusCalls != 0 {
		t.Fatalf("超过 45 天的外发不再补查")
	}
	recent := clock.now.Add(-50 * time.Hour).UTC().Format(time.RFC3339)
	setTestLanes(t, env, job.ID, failed(testYunsuoToutiaoID, "头条号", recent), failed(testYunsuoBaijiaID, "百家号", recent))
	env.pipeline.refreshExternalDistributionResults()
	got := mustJob(t, env, job.ID)
	if mustLane(t, got, testYunsuoToutiaoID).State != publishExternalDistributionSucceeded ||
		mustLane(t, got, testYunsuoBaijiaID).State != publishExternalDistributionFailed || fake.pushCalls != 0 {
		t.Fatalf("头条重试成功、百家号仍失败：lanes=%+v push=%d", got.ExternalDistributionLanes, fake.pushCalls)
	}
	if externalDistributionSummaryState(got) != publishExternalDistributionPartialFailed {
		t.Fatalf("汇总应为部分失败：%q", externalDistributionSummaryState(got))
	}
	if fake.statusCalls != 1 {
		t.Fatalf("迁移来的共用单号同一轮只查一次：%d", fake.statusCalls)
	}
}

// setTestYunsuoBothAutoConfig 头条号、百家号都标 auto（2026-10-12 百家号恢复后补发）。
func setTestYunsuoBothAutoConfig(t *testing.T) {
	t.Helper()
	setTestYunsuoConfig(t)
	setTestExternalAccounts(t, testArticleAccount(testYunsuoToutiaoID, "toutiao", true), testArticleAccount(testYunsuoBaijiaID, "baijiahao", true))
}

func newBothAccountsYunsuo() *fakeYunsuoAPI {
	return &fakeYunsuoAPI{
		contentPK: "content-pk-both",
		statuses: []yunsuoAccountStatus{
			{Status: 1, Remark: "OK", AccountPool: yunsuoAccountPool{ID: testYunsuoToutiaoID, Name: "头条号"}},
			{Status: 1, Remark: "OK", AccountPool: yunsuoAccountPool{ID: testYunsuoBaijiaID, Name: "百家号"}},
		},
	}
}

func doneLane(id, name, state, pk string) publishExternalLane {
	lane := testLane(id, name, state)
	lane.ContentPK = pk
	lane.PayloadHash = "sha256:first-round"
	lane.CompletedAt = "2026-10-10T01:05:00Z"
	return lane
}

// TestExternalAutoSupplementAddsOnlyMissingAccount 新加 auto 的百家号只补推百家号：头条号
// 不论成败（有单号的失败由云梭自己重试）都不碰；两个平台都有记录的不再进补发名单。
func TestExternalAutoSupplementAddsOnlyMissingAccount(t *testing.T) {
	setTestYunsuoBothAutoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	fake := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	robot := useFakeRobot(env)

	toutiaoDone := createPublishedDistributionJobWithKey(t, env, "site:toutiao-done", "FBIF食品饮料创新")
	setTestLanes(t, env, toutiaoDone.ID, doneLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded, "pk-toutiao"))
	bothDone := createPublishedDistributionJobWithKey(t, env, "site:both-done", "FBIF食品饮料创新")
	setTestLanes(t, env, bothDone.ID,
		doneLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded, "pk-both"),
		doneLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionSucceeded, "pk-both"))
	// 百家号受理后失败、云梭自己会重试：不得再推一次。
	baijiaRetrying := createPublishedDistributionJobWithKey(t, env, "site:baijia-retrying", "FBIF食品饮料创新")
	setTestLanes(t, env, baijiaRetrying.ID,
		doneLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded, "pk-retrying"),
		doneLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionFailed, "pk-retrying"))
	// 头条号受理后失败、百家号没推过：各平台分开后，百家号照补，头条号单号原样留着补查。
	toutiaoFailed := createPublishedDistributionJobWithKey(t, env, "site:toutiao-failed", "FBIF食品饮料创新")
	setTestLanes(t, env, toutiaoFailed.ID, doneLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionFailed, "pk-failed"))
	since := time.Date(2026, 8, 1, 0, 0, 0, 0, publishExternalAutoZone)

	preview, err := env.pipeline.markExternalAutoBackfill(since, true)
	if err != nil || len(preview) != 2 {
		t.Fatalf("只应补百家号没推过的 2 篇：n=%d err=%v", len(preview), err)
	}
	if _, err := env.pipeline.markExternalAutoBackfill(since, false); err != nil {
		t.Fatal(err)
	}
	clock.set("2026-10-12 11:00")
	env.pipeline.runExternalAutoDistribution()
	// 百家号现在经分发服务存草稿（2026-10-22）；头条号云梭一次也不推。
	if len(fake.pushed) != 0 || fmt.Sprint(robot.platforms) != "[baijiahao baijiahao]" {
		t.Fatalf("补发只推百家号、间隔 1 小时各 1 篇，头条号不得重发：%v robot=%v", fake.pushed, robot.platforms)
	}
	for _, id := range []int64{toutiaoDone.ID, toutiaoFailed.ID} {
		got := mustJob(t, env, id)
		if lane := mustLane(t, got, testYunsuoBaijiaID); lane.State != publishExternalDistributionDraftSaved || lane.RequestedBy != publishExternalAutoBackfillActor {
			t.Fatalf("任务 #%d 百家号应补发成功：%+v", id, lane)
		}
		if got.ExternalDistributionAutoPending != "" {
			t.Fatalf("任务 #%d 补完应清待办", id)
		}
	}
	if lane := mustLane(t, mustJob(t, env, toutiaoDone.ID), testYunsuoToutiaoID); lane.ContentPK != "pk-toutiao" || lane.PayloadHash != "sha256:first-round" ||
		lane.RequestedBy != publishExternalAutoActor || lane.State != publishExternalDistributionSucceeded {
		t.Fatalf("头条号 lane 应原样保留：%+v", lane)
	}
	if lane := mustLane(t, mustJob(t, env, toutiaoFailed.ID), testYunsuoToutiaoID); lane.ContentPK != "pk-failed" || lane.State != publishExternalDistributionFailed {
		t.Fatalf("头条号受理后失败的单号不得被覆盖：%+v", lane)
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(mustJob(t, env, toutiaoDone.ID)))
	if strings.Count(body, "<font color='green'>已发布</font>") != 1 || !strings.Contains(body, "<font color='green'>草稿已存（未发布）</font>") {
		t.Fatalf("单篇卡应把头条号列为已发布、百家号草稿已存: %s", body)
	}
	for _, id := range []int64{bothDone.ID, baijiaRetrying.ID} {
		other := mustJob(t, env, id)
		if other.ExternalDistributionAutoPending != "" || len(other.ExternalDistributionLanes) != 2 {
			t.Fatalf("任务 #%d 不应补发：lanes=%+v pending=%q", id, other.ExternalDistributionLanes, other.ExternalDistributionAutoPending)
		}
		if _, err := env.pipeline.queueAutoExternalDistribution(id, publishExternalAutoActor, nil); !isExternalDistributionIneligible(err) || fake.pushCalls != 0 || len(robot.saves) != 2 {
			t.Fatalf("任务 #%d 两个平台都有记录，直接提交也应被拒：err=%v push=%d robot=%d", id, err, fake.pushCalls, len(robot.saves))
		}
	}

	clock.set("2026-10-12 12:00")
	if again, err := env.pipeline.markExternalAutoBackfill(since, false); err != nil || len(again) != 0 {
		t.Fatalf("两个平台都推过后不得再补：n=%d err=%v", len(again), err)
	}
	env.pipeline.runExternalAutoDistribution()
	if fake.pushCalls != 0 || len(robot.saves) != 2 {
		t.Fatalf("不应再有推送，实际云梭 %d、分发服务 %d", fake.pushCalls, len(robot.saves))
	}
}

// TestExternalAutoSupplementSafeRetryStaysOnCard 补发的百家号在云梭受理前失败：自动外发
// 不接手（防止反复失败刷名额），只走审核卡重试，且只推百家号；头条号 lane 不动。
func TestExternalAutoSupplementSafeRetryStaysOnCard(t *testing.T) {
	setTestYunsuoBothAutoConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	fake := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job = setTestLanes(t, env, job.ID,
		doneLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded, "pk-toutiao"),
		testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionFailed))
	markAutoPending(t, env, job.ID, publishExternalAutoKindBackfill)
	env.pipeline.runExternalAutoDistribution()
	if got := mustJob(t, env, job.ID); fake.pushCalls != 0 || len(robot.saves) != 0 || got.ExternalDistributionAutoPending != "" {
		t.Fatalf("受理前失败只走卡片重试，自动外发不接手：push=%d robot=%d pending=%q", fake.pushCalls, len(robot.saves), got.ExternalDistributionAutoPending)
	}
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("受理前失败应可安全重试：%v", err)
	}
	if len(fake.pushed) != 0 || len(robot.saves) != 1 {
		t.Fatalf("重试只推百家号：%v robot=%d", fake.pushed, len(robot.saves))
	}
	got := mustJob(t, env, job.ID)
	if lane := mustLane(t, got, testYunsuoToutiaoID); lane.ContentPK != "pk-toutiao" || lane.PayloadHash != "sha256:first-round" {
		t.Fatalf("头条号 lane 不得改动：%+v", lane)
	}
	if body := distributionSectionJSON(t, buildReviewDoneCard(got)); strings.Count(body, "<font color='green'>已发布</font>") != 1 ||
		!strings.Contains(body, "<font color='green'>草稿已存（未发布）</font>") {
		t.Fatalf("重试成功后卡片应头条号已发布、百家号草稿已存: %s", body)
	}
}
