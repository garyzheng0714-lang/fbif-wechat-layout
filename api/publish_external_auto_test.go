package main

import (
	"context"
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
	t.Setenv(yunsuoTargetAccountsEnv, `[{"id":"`+testYunsuoToutiaoID+`","name":"头条号","auto":true},{"id":"`+testYunsuoBaijiaID+`","name":"百家号"}]`)
}

func newToutiaoOnlyYunsuo() *fakeYunsuoAPI {
	return &fakeYunsuoAPI{
		contentPK: "content-pk-auto",
		statuses: []yunsuoAccountStatus{
			{Status: 1, Remark: "OK", AccountPool: yunsuoAccountTarget{ID: testYunsuoToutiaoID, Name: "头条号"}},
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
	if got.ExternalDistributionRequestedBy != publishExternalAutoActor ||
		!strings.HasPrefix(got.ExternalDistributionAttribution, "original_auto_confirmed: ") {
		t.Fatalf("应留下系统自动外发留痕：by=%q attribution=%q", got.ExternalDistributionRequestedBy, got.ExternalDistributionAttribution)
	}
	if got.ExternalDistributionState != publishExternalDistributionSucceeded || got.ExternalDistributionAutoPending != "" {
		t.Fatalf("应外发成功并清掉待办：state=%q pending=%q", got.ExternalDistributionState, got.ExternalDistributionAutoPending)
	}
	if len(got.ExternalDistributionTargets) != 1 || got.ExternalDistributionTargets[0].Name != "头条号" {
		t.Fatalf("任务只应记录头条号目标：%+v", got.ExternalDistributionTargets)
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
	if fake.pushCalls != 0 || got.ExternalDistributionAutoPending != "" || got.ExternalDistributionState != "" {
		t.Fatalf("转载稿不得自动外发：push=%d pending=%q state=%q", fake.pushCalls, got.ExternalDistributionAutoPending, got.ExternalDistributionState)
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
		ExternalDistributionState: publishExternalDistributionSucceeded}
	markExternalAutoDistributionPending(&distributed)
	if distributed.ExternalDistributionAutoPending != "" {
		t.Fatalf("已外发过的稿不得再记待办")
	}
}

func TestExternalAutoQuotaRules(t *testing.T) {
	auto := []yunsuoAccountTarget{{ID: testYunsuoToutiaoID, Name: "头条号", Auto: true}}
	clock := newCSTClock("2026-10-12 10:00")
	sent := func(at, by string, accountID string) publishJob {
		requested, err := time.ParseInLocation("2006-01-02 15:04", at, publishExternalAutoZone)
		if err != nil {
			t.Fatal(err)
		}
		return publishJob{
			ExternalDistributionState:       publishExternalDistributionSucceeded,
			ExternalDistributionContentPK:   "pk",
			ExternalDistributionRequestedAt: requested.UTC().Format(time.RFC3339),
			ExternalDistributionRequestedBy: by,
			ExternalDistributionTargets:     []publishExternalDistributionTarget{{AccountID: accountID}},
		}
	}

	quota := externalAutoQuotaFor(nil, auto, clock.now)
	if !quota.allows(publishExternalAutoKindPublish, clock.now) || !quota.allows(publishExternalAutoKindBackfill, clock.now) {
		t.Fatalf("空配额应放行")
	}
	// 昨天的、只发百家号的、受理前失败的都不占今天头条名额。
	preFail := sent("2026-10-12 09:00", "ou_x", testYunsuoToutiaoID)
	preFail.ExternalDistributionState = publishExternalDistributionFailed
	preFail.ExternalDistributionContentPK = ""
	quota = externalAutoQuotaFor([]publishJob{
		sent("2026-10-11 23:30", publishExternalAutoActor, testYunsuoToutiaoID),
		sent("2026-10-12 08:00", "ou_x", testYunsuoBaijiaID),
		preFail,
	}, auto, clock.now)
	if quota.today != 0 {
		t.Fatalf("不应计入今天的头条名额，today=%d", quota.today)
	}

	// 人工按钮发的也计入；满 5 篇后新稿也停。
	var five []publishJob
	for i := 0; i < publishExternalAutoDailyLimit; i++ {
		five = append(five, sent("2026-10-12 08:0"+fmt.Sprint(i), "ou_reviewer", testYunsuoToutiaoID))
	}
	quota = externalAutoQuotaFor(five, auto, clock.now)
	if quota.allows(publishExternalAutoKindPublish, clock.now) {
		t.Fatalf("当天已满 %d 篇应停", publishExternalAutoDailyLimit)
	}

	// 补发：40 分钟前刚推过 → 等满 1 小时；夜里不推；当天补发满 3 篇停。
	quota = externalAutoQuotaFor([]publishJob{sent("2026-10-12 09:20", publishExternalAutoActor, testYunsuoToutiaoID)}, auto, clock.now)
	if quota.allows(publishExternalAutoKindBackfill, clock.now) {
		t.Fatalf("补发与上一篇间隔不足 1 小时应等待")
	}
	if !quota.allows(publishExternalAutoKindPublish, clock.now) {
		t.Fatalf("新公开稿不受补发间隔限制")
	}
	night := newCSTClock("2026-10-12 21:30")
	if externalAutoQuotaFor(nil, auto, night.now).allows(publishExternalAutoKindBackfill, night.now) {
		t.Fatalf("补发只在白天推")
	}
	var threeBackfill []publishJob
	for _, at := range []string{"2026-10-12 06:00", "2026-10-12 07:00", "2026-10-12 08:00"} {
		threeBackfill = append(threeBackfill, sent(at, publishExternalAutoBackfillActor, testYunsuoToutiaoID))
	}
	quota = externalAutoQuotaFor(threeBackfill, auto, clock.now)
	if quota.allows(publishExternalAutoKindBackfill, clock.now) {
		t.Fatalf("当天补发满 %d 篇应停", publishExternalAutoBackfillDailyLimit)
	}
	if !quota.allows(publishExternalAutoKindPublish, clock.now) {
		t.Fatalf("补发配额用完不影响新稿")
	}
}

func TestExternalAutoBackfillPacesAcrossHoursAndDays(t *testing.T) {
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
		if got.ExternalDistributionState != publishExternalDistributionSucceeded || got.ExternalDistributionAutoPending != "" ||
			got.ExternalDistributionRequestedBy != publishExternalAutoBackfillActor {
			t.Fatalf("第 %d 篇补发未闭环：state=%q pending=%q by=%q", i, got.ExternalDistributionState, got.ExternalDistributionAutoPending, got.ExternalDistributionRequestedBy)
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
	if gotFresh.ExternalDistributionState == "" {
		t.Fatalf("新公开稿应优先外发")
	}
	if gotOld.ExternalDistributionState != "" || gotOld.ExternalDistributionAutoPending != publishExternalAutoKindBackfill {
		t.Fatalf("刚推过新稿，补发应等满间隔：state=%q pending=%q", gotOld.ExternalDistributionState, gotOld.ExternalDistributionAutoPending)
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
	if fake.pushCalls != 0 || got.ExternalDistributionState != "" {
		t.Fatalf("网站标为转载的稿不得外发：push=%d state=%q", fake.pushCalls, got.ExternalDistributionState)
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

func TestExternalDistributionSafeRetryKeepsPreviousTargets(t *testing.T) {
	setTestYunsuoAutoConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	fake := newToutiaoOnlyYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, err := env.store.update(job.ID, func(current *publishJob) {
		current.ExternalDistributionState = publishExternalDistributionFailed
		current.ExternalDistributionTargets = []publishExternalDistributionTarget{
			{AccountID: testYunsuoToutiaoID, Name: "头条号", State: publishExternalTargetFailed},
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("受理前失败应可安全重试：%v", err)
	}
	if len(fake.payload.AccountIDs) != 1 || fake.payload.AccountIDs[0] != testYunsuoToutiaoID {
		t.Fatalf("重试应沿用上次的账号，不得扩到其他平台：%v", fake.payload.AccountIDs)
	}
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
