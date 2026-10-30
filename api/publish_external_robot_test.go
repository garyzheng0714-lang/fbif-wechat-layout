package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

const testRobotArticleID = "1846000000000000001"

// fakeDistributionRobot 自己的分发服务的测试替身：记下每次存草稿、发布与查询，按幂等键记着最新一份记录
// （存草稿、发布都更新），GET 默认回它，同一个键再存草稿回记着的那份（和分发服务一样只认一篇）。
type fakeDistributionRobot struct {
	saves         []robotDraftRequest
	platforms     []string
	gets          []string
	publishes     []string // 平台/键/actor
	saveRecord    *robotDraftRecord
	saveErr       error
	getRecord     robotDraftRecord // 设了就 GET 一律回它
	getErr        error
	publishRecord *robotDraftRecord
	publishErr    error
	duringSave    func(robotDraftRequest)
	duringPublish func(key string)
	records       map[string]robotDraftRecord
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
	if kept, ok := f.records[req.IdempotencyKey]; ok && kept.Status == robotDraftSaved {
		kept.Replayed, kept.Action = true, nil // 写草稿的响应不带 action
		return kept, nil
	}
	record := testRobotSavedRecord(testRobotArticleID)
	if f.saveRecord != nil {
		record = *f.saveRecord
	}
	f.remember(req.IdempotencyKey, record)
	return record, nil
}

func (f *fakeDistributionRobot) Draft(_ context.Context, platform, key string) (robotDraftRecord, error) {
	f.gets = append(f.gets, platform+"/"+key)
	if f.getErr != nil || f.getRecord.Status != "" {
		return f.getRecord, f.getErr
	}
	if kept, ok := f.records[key]; ok {
		return kept, nil
	}
	return robotDraftRecord{}, errRobotDraftNotFound
}

// Publish 默认当场发出去：平台上已发布、带文章链接。
func (f *fakeDistributionRobot) Publish(_ context.Context, platform, key, actor string) (robotDraftRecord, error) {
	f.publishes = append(f.publishes, platform+"/"+key+"/"+actor)
	if f.duringPublish != nil {
		f.duringPublish(key)
	}
	if f.publishErr != nil {
		return robotDraftRecord{}, f.publishErr
	}
	articleID := testRobotArticleID
	if kept, ok := f.records[key]; ok && kept.ArticleID != "" {
		articleID = kept.ArticleID
	}
	record := testRobotPublishRecord(articleID, robotStatePublished, testRobotArticleURL(platform, articleID))
	record.Action.ID = fmt.Sprintf("act-%d", len(f.publishes))
	if f.publishRecord != nil {
		record = *f.publishRecord
	}
	f.remember(key, record)
	return record, nil
}

func (f *fakeDistributionRobot) remember(key string, record robotDraftRecord) {
	if f.records == nil {
		f.records = map[string]robotDraftRecord{}
	}
	f.records[key] = record
}

func useFakeRobot(env *publishTestEnv) *fakeDistributionRobot {
	fake := &fakeDistributionRobot{}
	env.pipeline.distributionRobot = func() (distributionRobotAPI, error) { return fake, nil }
	return fake
}

func testRobotDraftURL(id string) string {
	return "https://baijiahao.baidu.com/builder/rc/edit?type=news&article_id=" + id
}

// testRobotArticleURL 平台上已发布的文章链接（域名在 testdata 平台限制的 hosts 里）。
func testRobotArticleURL(platform, id string) string {
	if platform == "toutiao" {
		return "https://www.toutiao.com/article/" + id + "/"
	}
	return "https://baijiahao.baidu.com/s?id=" + id
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

// testRobotPublishRecord 发布做成之后的记录：平台上是 state，已发布的带文章链接。
func testRobotPublishRecord(id, state, articleURL string) robotDraftRecord {
	record := testRobotSavedRecord(id)
	record.State = &robotDraftState{Value: state, Raw: state, URL: articleURL}
	record.ArticleURL = articleURL
	record.Action = &robotDraftAction{ID: "act-1", Kind: robotActionPublish, Status: robotActionDone, Outcome: "by_us"}
	return record
}

// setTestRobotOnlyConfig 只配百家号一个号：卡片上只有百家号一行，断言简单。
func setTestRobotOnlyConfig(t *testing.T) {
	t.Helper()
	setTestExternalAccounts(t, testArticleAccount(testBaijiaAccountID, "baijiahao", false))
}

var robotKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// TestRobotAutoLaneSavesPublishesAndBringsBackLink 2026-10-30 用户：「我们的原创文章发出来就自动同步，不需要存草稿，
// 也不需要别的操作。自动同步之后，把文档链接返回到卡片上」。自动外发的 lane 存好草稿、读回对得上，先把草稿编号、
// 地址与读回核对落盘并转发布中，再按同一个幂等键调发布；平台审核中的每分钟跟一次，审核过了记下文章链接，
// 卡片给「查看文章」。定了就不再查。
func TestRobotAutoLaneSavesPublishesAndBringsBackLink(t *testing.T) {
	setTestExternalAccounts(t, testArticleAccount(testBaijiaAccountID, "baijiahao", true))
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-30 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	markAutoPending(t, env, job.ID, publishExternalAutoKindPublish)
	reviewing := testRobotPublishRecord(testRobotArticleID, robotStateReviewing, "")
	robot.publishRecord = &reviewing
	robot.duringPublish = func(key string) {
		lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
		if lane.State != publishExternalDistributionPublishing || lane.IntentKey != key || lane.ContentPK != testRobotArticleID ||
			lane.DraftURL != testRobotDraftURL(testRobotArticleID) || lane.Fidelity.CheckedAt == "" {
			t.Errorf("调发布之前，草稿编号、地址与读回核对要先落盘、转发布中：%+v", lane)
		}
	}

	env.pipeline.runExternalAutoDistribution()
	got := mustJob(t, env, job.ID)
	lane := mustLane(t, got, testBaijiaAccountID)
	if len(robot.saves) != 1 || fmt.Sprint(robot.publishes) != "[baijiahao/"+lane.IntentKey+"/"+publishExternalAutoActor+"]" {
		t.Fatalf("自动外发存一次草稿、按同一个幂等键调一次发布、记成自动外发：saves=%d publishes=%v", len(robot.saves), robot.publishes)
	}
	if lane.State != publishExternalDistributionReviewing || lane.ActionID != "act-1" || lane.URL != "" || got.ExternalDistributionAutoPending != "" {
		t.Fatalf("发布点下去、平台在审：lane=%+v pending=%q", lane, got.ExternalDistributionAutoPending)
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	if !strings.Contains(body, "<font color='blue'>平台审核中</font>") || strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("审核中写清楚、不给按钮: %s", body)
	}
	if status := currentPublishMonitoring(env.store, false, clock.now.Add(time.Hour)); status.Processing != 0 || status.OldestExternalReviewingAgeSeconds != 3600 ||
		publishMonitoringHasIssue(publishMonitoringStatus{Issues: status.Warnings}, "external_reviewing_slow:") || len(status.AlertIssues) != 0 {
		t.Fatalf("平台审核中不算处理中，一小时也不报：%+v", status)
	}

	// 跟进：还在审就不写盘；审核过了记下文章链接。
	env.pipeline.followRobotLanes()
	if lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID); lane.State != publishExternalDistributionReviewing || len(robot.gets) != 1 {
		t.Fatalf("还在审，留在审核中：%+v gets=%v", lane, robot.gets)
	}
	articleURL := testRobotArticleURL("baijiahao", testRobotArticleID)
	robot.getRecord = testRobotPublishRecord(testRobotArticleID, robotStatePublished, articleURL)
	env.pipeline.followRobotLanes()
	got = mustJob(t, env, job.ID)
	if lane := mustLane(t, got, testBaijiaAccountID); lane.State != publishExternalDistributionSucceeded || lane.URL != articleURL {
		t.Fatalf("审核过了记已发布与文章链接：%+v", lane)
	}
	body = distributionSectionJSON(t, buildReviewDoneCard(got))
	if !strings.Contains(body, "<font color='green'>已发布</font>") || !strings.Contains(body, "**百家号**　[查看文章]("+articleURL+")") {
		t.Fatalf("卡片给「查看文章」: %s", body)
	}
	env.pipeline.followRobotLanes()
	env.pipeline.recoverExternalDistribution(got)
	env.pipeline.refreshExternalDistributionResults()
	if len(robot.gets) != 2 || len(robot.publishes) != 1 || len(robot.saves) != 1 {
		t.Fatalf("已发布就不再查、不再发：gets=%v publishes=%v saves=%d", robot.gets, robot.publishes, len(robot.saves))
	}
	if status := currentPublishMonitoring(env.store, false, clock.now.Add(time.Hour)); status.Processing != 0 || status.Failed != 0 || status.ReconciliationRequired != 0 {
		t.Fatalf("已发布是终态：%+v", status)
	}
}

// TestRobotLaneFreezesDraftBeforeSavingThenPublishes 卡片上点的和自动外发一样：提交前 lane 已落 pushing + 冻结
// 内容 + 幂等键；存好就发布、记文章链接；两个平台各存各发，发布记下点的人。
func TestRobotLaneFreezesDraftBeforeSavingThenPublishes(t *testing.T) {
	setTestDistributionAccounts(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	robot.duringSave = func(req robotDraftRequest) {
		if len(robot.platforms) == 0 || robot.platforms[len(robot.platforms)-1] != "baijiahao" {
			return
		}
		lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
		var frozen robotDraftPayload
		if err := json.Unmarshal([]byte(lane.PayloadJSON), &frozen); err != nil {
			t.Errorf("提交前 lane 上应已冻结草稿内容：%v", err)
		}
		if lane.State != publishExternalDistributionPushing || lane.Driver != externalDriverRobot || lane.IntentKey != req.IdempotencyKey ||
			lane.IntentAt == "" || frozen.Platform != "baijiahao" || frozen.Title != req.Title || !reflect.DeepEqual(frozen.Blocks, req.Blocks) || frozen.CoverURL != req.CoverURL {
			t.Errorf("提交前必须先落 intent：lane=%+v frozen=%+v req=%+v", lane, frozen, req)
		}
	}

	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(robot.platforms) != "[toutiao baijiahao]" {
		t.Fatalf("头条号、百家号都经分发服务各存一次：%v", robot.platforms)
	}
	req := robot.saves[1]
	if robot.saves[0].Account != testToutiaoAccountID || req.Account != testBaijiaAccountID {
		t.Fatalf("存草稿要带上「平台账号」的编号，分发服务才知道存进哪个号：%q %q", robot.saves[0].Account, req.Account)
	}
	if !robotKeyPattern.MatchString(req.IdempotencyKey) || !strings.HasPrefix(req.IdempotencyKey, fmt.Sprintf("job-%d.", job.ID)) {
		t.Fatalf("幂等键只用分发服务认的字符：%q", req.IdempotencyKey)
	}
	// 标题原样（不补「｜摘要」），封面是公众号封面、单独上传。
	if req.Title != job.Title || req.CoverURL != job.CoverURL || !strings.Contains(distributionOutline(req.Blocks), "这是一篇用于验证外部分发流程的原创文章正文。") {
		t.Fatalf("草稿内容不对：%+v", req)
	}
	wantPublishes := "[toutiao/" + robot.saves[0].IdempotencyKey + "/ou_reviewer_1 baijiahao/" + req.IdempotencyKey + "/ou_reviewer_1]"
	if fmt.Sprint(robot.publishes) != wantPublishes {
		t.Fatalf("两个平台各按自己的键发一次，记下点的人：%v", robot.publishes)
	}
	got := mustJob(t, env, job.ID)
	for _, want := range []struct{ id, platform string }{{testToutiaoAccountID, "toutiao"}, {testBaijiaAccountID, "baijiahao"}} {
		lane := mustLane(t, got, want.id)
		if lane.Driver != externalDriverRobot || lane.State != publishExternalDistributionSucceeded || lane.ContentPK != testRobotArticleID ||
			lane.URL != testRobotArticleURL(want.platform, testRobotArticleID) || lane.Title != job.Title || lane.CompletedAt == "" {
			t.Fatalf("%s 应已发布并记下文章链接：%+v", want.platform, lane)
		}
		if f := lane.Fidelity; f.CheckedAt == "" || f.Note != "" || f.Expected.Headings != 2 || f.Online.CaptionedImages != 1 {
			t.Fatalf("读回核对应记在 lane 上：%+v", f)
		}
	}

	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	if !strings.Contains(body, "**百家号**　[查看文章]("+testRobotArticleURL("baijiahao", testRobotArticleID)+")") ||
		!strings.Contains(body, "**头条号**　[查看文章]("+testRobotArticleURL("toutiao", testRobotArticleID)+")") {
		t.Fatalf("卡片两个平台都给「查看文章」: %s", body)
	}
	status := currentPublishMonitoring(env.store, false, time.Now())
	if status.Processing != 0 || status.Failed != 0 || status.ReconciliationRequired != 0 || status.ExternalFidelityMismatches != 0 || status.ExternalFidelityUnavailable != 0 {
		t.Fatalf("已发布是终态，不算处理中或失败：%+v", status)
	}
	if out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, got, "")); len(robot.saves) != 2 || out.toastContent != "文章已同步至其他平台，无需重复操作" {
		t.Fatalf("发出去后再点不得再提交：out=%+v saves=%d", out, len(robot.saves))
	}
}

func mustDistributeValue(t *testing.T, job publishJob, account string) json.RawMessage {
	t.Helper()
	value := map[string]any{
		"action": feishuCardActionDistributeExternal, "job_id": job.ID,
		"distribution_version": publishExternalDistributionCardVersion(job),
	}
	if account != "" {
		value["account"] = account
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestRobotLaneHoldsDraftThatDoesNotMatch 分发服务存完读回与原文对不上：不发，停在草稿（未发布）；卡片写为什么、
// 给草稿链接、不给按钮；跟进、重启、补查、再点卡片都不会去发。
func TestRobotLaneHoldsDraftThatDoesNotMatch(t *testing.T) {
	setTestRobotOnlyConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	mismatched := testRobotSavedRecord(testRobotArticleID)
	match := false
	mismatched.Match, mismatched.Mismatches = &match, []string{"小标题：送去 2 处，读回 1 处"}
	robot.saveRecord = &mismatched
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	got := mustJob(t, env, job.ID)
	lane := mustLane(t, got, testBaijiaAccountID)
	if lane.State != publishExternalDistributionDraftSaved || lane.Error != "草稿与原文对不上，没有发布：小标题：送去 2 处，读回 1 处" ||
		lane.ContentPK != testRobotArticleID || lane.DraftURL != testRobotDraftURL(testRobotArticleID) || len(robot.publishes) != 0 {
		t.Fatalf("对不上的草稿不发、停在草稿：%+v publishes=%v", lane, robot.publishes)
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	for _, want := range []string{"<font color='orange'>未发布</font>", "**百家号**　[去百家号草稿箱查看](" + testRobotDraftURL(testRobotArticleID) + ")",
		"<font color='grey'>草稿与原文对不上，没有发布：小标题：送去 2 处，读回 1 处</font>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("卡片缺少 %q: %s", want, body)
		}
	}
	if strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("停在草稿不给按钮（内容已在那篇草稿里，要人去平台改）: %s", body)
	}
	if status := currentPublishMonitoring(env.store, false, time.Now()); status.ExternalFidelityMismatches != 1 || status.Processing != 0 {
		t.Fatalf("对不上照旧报 fidelity mismatch：%+v", status)
	}
	env.pipeline.recoverExternalDistribution(got)
	env.pipeline.followRobotLanes()
	env.pipeline.refreshExternalDistributionResults()
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, got, ""))
	if len(robot.publishes) != 0 || len(robot.saves) != 1 || len(robot.gets) != 0 || out.toastContent != "有平台停在草稿、没有发布，请看卡片" {
		t.Fatalf("停在草稿的不再发、不再查：out=%+v publishes=%v saves=%d gets=%v", out, robot.publishes, len(robot.saves), robot.gets)
	}
}

// TestRobotOldDraftSavedLaneNeverPublished 2026-10-30 之前只存草稿的 lane：不补发。重启、跟进、补查、自动外发、
// 卡片都不碰它；卡片写「未发布」、给草稿链接。
func TestRobotOldDraftSavedLaneNeverPublished(t *testing.T) {
	setTestExternalAccounts(t, testArticleAccount(testBaijiaAccountID, "baijiahao", true))
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-30 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	old := testRobotLaneIn(job.ID, publishExternalDistributionDraftSaved)
	robot.remember(old.IntentKey, testRobotSavedRecord(testRobotArticleID))
	job = setTestLanes(t, env, job.ID, old)
	markAutoPending(t, env, job.ID, publishExternalAutoKindBackfill)

	env.pipeline.recoverExternalDistribution(job)
	env.pipeline.followRobotLanes()
	env.pipeline.refreshExternalDistributionResults()
	env.pipeline.runExternalAutoDistribution()
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, mustJob(t, env, job.ID), testBaijiaAccountID))
	if len(robot.saves) != 0 || len(robot.publishes) != 0 || len(robot.gets) != 0 {
		t.Fatalf("旧草稿不补发、不查：out=%+v saves=%d publishes=%v gets=%v", out, len(robot.saves), robot.publishes, robot.gets)
	}
	got := mustJob(t, env, job.ID)
	if lane := mustLane(t, got, testBaijiaAccountID); !reflect.DeepEqual(lane, old) {
		t.Fatalf("旧草稿 lane 不得被改：%+v", lane)
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	for _, want := range []string{"<font color='orange'>未发布</font>", "[去百家号草稿箱查看](" + testRobotDraftURL(testRobotArticleID) + ")",
		"<font color='grey'>只存了草稿，没有发布</font>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("卡片缺少 %q: %s", want, body)
		}
	}
	if strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("旧草稿不给按钮: %s", body)
	}
}

// testRobotLaneIn 已经交给分发服务、草稿存进百家号的 lane（冻结内容、幂等键、草稿编号都在）。
func testRobotLaneIn(jobID int64, state string) publishExternalLane {
	lane := testLane(testBaijiaAccountID, "百家号", state)
	lane.Driver = externalDriverRobot
	lane.Title = "中国食品品牌如何用产品创新打开新市场"
	lane.PayloadJSON = `{"platform":"baijiahao","title":"` + lane.Title + `","blocks":[{"k":"p","text":"正文"}],"cover_url":"https://cdn.example.com/a.jpg"}`
	lane.PayloadHash = "sha256:frozen"
	lane.IntentKey = robotIdempotencyKey(jobID, testBaijiaAccountID, lane.PayloadHash)
	lane.IntentAt = "2026-10-10T01:00:00Z"
	lane.ContentPK, lane.DraftURL = testRobotArticleID, testRobotDraftURL(testRobotArticleID)
	lane.CompletedAt = "2026-10-10T01:02:00Z"
	return lane
}

// TestRobotPublishOutcomes 分发服务对发布的每类回答落到 lane 上是什么（发布的回答与跟进时 GET 的记录同一套）：
// 还在点 → 发布中；平台审核中、定时 → 审核中；已发布、链接在平台域名内 → 已发布；链接不对、没链接、平台上是
// 别的样子、点了没看清、草稿被人改过 → 待核对；平台没通过 → 未通过（终态）；明确没发出去、没登录 → 失败、
// 卡片可重新同步；说不准 → 留在发布中，下一轮再查。系统自己从不再存草稿。
func TestRobotPublishOutcomes(t *testing.T) {
	articleURL := testRobotArticleURL("baijiahao", testRobotArticleID)
	published := func(url string) *robotDraftRecord {
		record := testRobotPublishRecord(testRobotArticleID, robotStatePublished, url)
		return &record
	}
	onPlatform := func(value, raw, reason string) *robotDraftRecord {
		record := testRobotPublishRecord(testRobotArticleID, value, "")
		record.State.Raw, record.State.Reason = raw, reason
		return &record
	}
	acting := func(status, message string) *robotDraftRecord {
		record := testRobotSavedRecord(testRobotArticleID)
		record.Action = &robotDraftAction{ID: "act-1", Kind: robotActionPublish, Status: status, Error: message}
		return &record
	}
	rejected := func(status int, code, message string) error {
		return &robotRejectedError{Status: status, Code: code, Message: message}
	}
	for _, tc := range []struct {
		name   string
		record *robotDraftRecord
		err    error
		state  string
		reason string
		url    string
		status string
		retry  bool
	}{
		{name: "还没点（202）", record: acting(robotActionPending, ""), state: publishExternalDistributionPublishing, status: "发布中"},
		{name: "点下去了还在看（202）", record: acting(robotActionDoing, ""), state: publishExternalDistributionPublishing, status: "发布中"},
		{name: "平台审核中", record: onPlatform(robotStateReviewing, "审核中", ""), state: publishExternalDistributionReviewing, status: "平台审核中"},
		{name: "定时待发", record: onPlatform(robotStateScheduled, "定时发布", ""), state: publishExternalDistributionReviewing, status: "平台审核中"},
		{name: "已发布", record: published(articleURL), state: publishExternalDistributionSucceeded, url: articleURL, status: "已发布"},
		{name: "链接不在平台域名", record: published("https://evil.example.com/s?id=1"), state: publishExternalDistributionReconciliationRequired, reason: "给的文章链接不对", status: "待核对"},
		{name: "已发布没给链接", record: published(""), state: publishExternalDistributionReconciliationRequired, reason: "百家号上已发布，但分发工具没拿到文章链接", status: "待核对"},
		{name: "平台没通过", record: onPlatform(robotStateRejected, "未通过", "标题涉嫌夸大"), state: publishExternalDistributionRejected, reason: "标题涉嫌夸大", status: "未通过"},
		{name: "平台上是别的样子", record: onPlatform("withdrawn", "已撤回", ""), state: publishExternalDistributionReconciliationRequired, reason: "百家号上这篇现在是「已撤回」", status: "待核对"},
		{name: "平台上查不到样子", record: onPlatform("", "", ""), state: publishExternalDistributionReconciliationRequired, reason: "分发工具没查到百家号上这篇现在的样子", status: "待核对"},
		{name: "明确没发出去", record: acting(robotActionFailed, "发布按钮是灰的"), state: publishExternalDistributionFailed, reason: "分发工具没发出去：发布按钮是灰的；可以重新同步", status: "失败", retry: true},
		{name: "点了没看清", record: acting(robotActionUnknown, ""), state: publishExternalDistributionReconciliationRequired, reason: "点了发布没看清发没发出去", status: "待核对"},
		{name: "要人核对（409 needs_human）", err: &robotReconcileError{Code: "needs_human", Message: "上次点了发布没看清，请到后台看一眼"}, state: publishExternalDistributionReconciliationRequired, reason: "上次点了发布没看清", status: "待核对"},
		{name: "草稿被人改过", err: rejected(409, "draft_changed", "草稿被人改过"), state: publishExternalDistributionReconciliationRequired, reason: "分发工具没发布：草稿被人改过；请到百家号后台核对", status: "待核对"},
		{name: "已不是草稿", err: rejected(409, "wrong_state", "这篇现在是已撤回"), state: publishExternalDistributionReconciliationRequired, reason: "请到百家号后台核对", status: "待核对"},
		{name: "服务器没登录", err: rejected(503, "login_required", "没登录"), state: publishExternalDistributionFailed, reason: "远程画面里登录后重新同步", status: "失败", retry: true},
		{name: "找不到发布按钮", err: rejected(422, "button_not_found", "找不到「发布」"), state: publishExternalDistributionFailed, reason: "分发工具没发布：找不到「发布」", status: "失败", retry: true},
		{name: "这篇正在做别的", err: rejected(409, "busy", "这篇正在做别的"), state: publishExternalDistributionPublishing, status: "发布中"},
		{name: "说不准", err: &robotUncertainError{Cause: "网络请求失败或超时"}, state: publishExternalDistributionPublishing, status: "发布中"},
		{name: "说不准但带着记录", err: &robotUncertainError{Cause: "HTTP 502 platform_error", Record: published(articleURL)}, state: publishExternalDistributionSucceeded, url: articleURL, status: "已发布"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setTestRobotOnlyConfig(t)
			t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
			env := newPublishTestEnv(t)
			robot := useFakeRobot(env)
			robot.publishRecord, robot.publishErr = tc.record, tc.err
			job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
			if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
				t.Fatal(err)
			}
			got := mustJob(t, env, job.ID)
			lane := mustLane(t, got, testBaijiaAccountID)
			if lane.State != tc.state || !strings.Contains(lane.Error, tc.reason) || lane.URL != tc.url || len(robot.saves) != 1 || len(robot.publishes) != 1 {
				t.Fatalf("应为 %s（%q，链接 %q）：lane=%+v saves=%d publishes=%v", tc.state, tc.reason, tc.url, lane, len(robot.saves), robot.publishes)
			}
			if lane.ContentPK != testRobotArticleID || lane.DraftURL != testRobotDraftURL(testRobotArticleID) {
				t.Fatalf("草稿存好了，编号与地址照记：%+v", lane)
			}
			baijia := externalAccount{ID: testBaijiaAccountID, Platform: "baijiahao", Name: "百家号"}
			if state := externalPlatformStatus(got, baijia); state.Status != tc.status || state.Editable {
				t.Fatalf("卡片状态应为 %s、内容不能再改：%+v", tc.status, state)
			}
			card := cardJSON(t, buildReviewDoneCard(got))
			if hasRetry := strings.Contains(card, `"content":"重新同步至百家号"`); hasRetry != tc.retry {
				t.Fatalf("重新同步按钮应为 %v: %s", tc.retry, card)
			}
			if strings.Contains(card, "存草稿至") {
				t.Fatalf("卡片不再说存草稿: %s", card)
			}

			// 之后的跟进、重启恢复、兜底补查：发布中的只查（分发服务那边已有这次发布，正在做），定了的不再碰。
			robot.getRecord = *acting(robotActionDoing, "")
			if tc.state == publishExternalDistributionReviewing {
				robot.getRecord = *tc.record
			}
			gets := 0
			if robotLaneFollowed(lane) {
				gets = 2
			}
			env.pipeline.followRobotLanes()
			env.pipeline.recoverExternalDistribution(mustJob(t, env, job.ID))
			env.pipeline.refreshExternalDistributionResults()
			if after := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID); after.State != tc.state || len(robot.saves) != 1 ||
				len(robot.publishes) != 1 || len(robot.gets) != gets {
				t.Fatalf("系统不得自己再存、再发：lane=%+v saves=%d publishes=%v gets=%v", after, len(robot.saves), robot.publishes, robot.gets)
			}
		})
	}
}

// TestRobotLaneSaveOutcomes 存草稿这一步每类回答落到 lane 上是什么：点存草稿之前被拒 → failed、卡片可重新同步；
// 草稿箱要人删 → 待核对；结果不明 → 当场按同一个键查一次，查到存好了照样发布。系统自己从不再存。
func TestRobotLaneSaveOutcomes(t *testing.T) {
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
		publishes int
		retry     bool
	}{
		{name: "参数不对", saveErr: rejected(400, "invalid_request", "title 要 2–64 字"), state: publishExternalDistributionFailed, reason: "分发工具没存草稿：title 要 2–64 字", retry: true},
		{name: "token 不对", saveErr: rejected(401, "unauthorized", "token 不对"), state: publishExternalDistributionFailed, reason: distributionAPITokenEnv, retry: true},
		{name: "键已用于别的内容", saveErr: rejected(409, "idempotency_key_reused", "这个键已用来存过别的内容"), state: publishExternalDistributionFailed, reason: "这个键已用来存过别的内容", retry: true},
		{name: "封面下不来", saveErr: rejected(422, "cover_unavailable", "封面下不下来"), state: publishExternalDistributionFailed, reason: "封面下不下来", retry: true},
		{name: "服务器没登录", saveErr: rejected(503, "login_required", "没登录"), state: publishExternalDistributionFailed, reason: "远程画面里登录后重新同步", retry: true},
		{name: "草稿箱要人删", saveErr: &robotReconcileError{Message: "草稿箱里这次新建的同名草稿有 2 篇，请删到只剩一篇"}, state: publishExternalDistributionReconciliationRequired, reason: "同名草稿有 2 篇"},
		{name: "平台出错后查到已存", saveErr: uncertain, getRecord: testRobotSavedRecord(testRobotArticleID), state: publishExternalDistributionSucceeded, gets: 1, publishes: 1},
		{name: "平台出错后查到失败", saveErr: uncertain, getRecord: robotDraftRecord{Status: robotDraftFailed, Error: &platformFailed}, state: publishExternalDistributionFailed, reason: "百家号编辑器没有出现；可以重新同步", gets: 1, retry: true},
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
			lane := mustLane(t, got, testBaijiaAccountID)
			if lane.State != tc.state || !strings.Contains(lane.Error, tc.reason) || len(robot.gets) != tc.gets || len(robot.publishes) != tc.publishes {
				t.Fatalf("应为 %s（%q），GET %d 次、发布 %d 次：lane=%+v gets=%v publishes=%v", tc.state, tc.reason, tc.gets, tc.publishes, lane, robot.gets, robot.publishes)
			}
			if tc.gets > 0 && robot.gets[0] != "baijiahao/"+lane.IntentKey {
				t.Fatalf("应按提交时的幂等键查：%v key=%q", robot.gets, lane.IntentKey)
			}
			if tc.publishes == 0 && (lane.ContentPK != "" || lane.DraftURL != "") {
				t.Fatalf("没存成不得记草稿编号：%+v", lane)
			}
			if tc.retry && !externalLaneEditable(lane) {
				t.Fatalf("没存成草稿的失败可以改了再同步：%+v", lane)
			}
			card := cardJSON(t, buildReviewDoneCard(got))
			if strings.Contains(card, "云梭") {
				t.Fatalf("卡片不提云梭: %s", card)
			}
			if hasRetry := strings.Contains(card, `"content":"重新同步至百家号"`); hasRetry != tc.retry {
				t.Fatalf("重新同步按钮应为 %v: %s", tc.retry, card)
			}
			// 重启恢复与兜底补查都只查不提交。
			env.pipeline.recoverExternalDistribution(got)
			env.pipeline.refreshExternalDistributionResults()
			if len(robot.saves) != 1 || len(robot.publishes) != tc.publishes {
				t.Fatalf("系统不得自己再存、再发：saves=%d publishes=%v", len(robot.saves), robot.publishes)
			}
		})
	}
}

// TestRobotLaneRetryReusesKeyAndPublishes 卡片上「重新同步」，草稿还没存成的：内容没变就是同一个幂等键，分发服务
// 对失败记录从头再做，存好就发布，不会多建一篇。
func TestRobotLaneRetryReusesKeyAndPublishes(t *testing.T) {
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
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, failed, testBaijiaAccountID))
	if out.toastType != "success" || out.toastContent != "已受理，正在同步至百家号" {
		t.Fatalf("重新同步应受理：%+v", out)
	}
	if len(robot.saves) != 2 || robot.saves[0].IdempotencyKey != robot.saves[1].IdempotencyKey {
		t.Fatalf("同一份内容重新存用同一个键：%+v", robot.saves)
	}
	if lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID); lane.State != publishExternalDistributionSucceeded || lane.RequestedBy != "ou_reviewer_1" ||
		lane.URL != testRobotArticleURL("baijiahao", testRobotArticleID) || len(robot.publishes) != 1 {
		t.Fatalf("重新同步应发出去并记下链接：%+v publishes=%v", lane, robot.publishes)
	}
}

// TestRobotLaneRetryAfterFailedPublishKeepsDraft 草稿存好了、发布明确没做成：内容锁在那篇草稿里（侧边栏不能改），
// 卡片「重新同步」按原来的冻结内容和幂等键再交，分发服务认出存好的那篇，再发一次；上次那次失败认得出是旧的。
func TestRobotLaneRetryAfterFailedPublishKeepsDraft(t *testing.T) {
	setTestRobotOnlyConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	failedPublish := testRobotSavedRecord(testRobotArticleID)
	failedPublish.Action = &robotDraftAction{ID: "act-1", Kind: robotActionPublish, Status: robotActionFailed, Error: "发布按钮是灰的"}
	robot.publishRecord = &failedPublish
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	failed := mustJob(t, env, job.ID)
	lane := mustLane(t, failed, testBaijiaAccountID)
	if lane.State != publishExternalDistributionFailed || lane.ActionID != "act-1" || !externalLaneCanSafeRetry(lane) || externalLaneEditable(lane) {
		t.Fatalf("发布没做成：可以重新同步，但内容不能再改：%+v", lane)
	}
	edited := "改过的标题：中国食品品牌如何打开新市场"
	if _, err := env.pipeline.submitExternalDraftEdit(job.ID, testBaijiaAccountID, externalDraftEdit{Title: &edited}, "ou_reviewer_1"); err == nil ||
		!strings.Contains(err.Error(), "内容不能再改") {
		t.Fatalf("草稿已存进平台，侧边栏不得再改：%v", err)
	}

	robot.publishRecord = nil
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, failed, testBaijiaAccountID))
	if out.toastType != "success" || out.toastContent != "已受理，正在同步至百家号" {
		t.Fatalf("重新同步应受理：%+v", out)
	}
	if len(robot.saves) != 2 || robot.saves[1].IdempotencyKey != lane.IntentKey ||
		fmt.Sprint(robot.publishes) != "[baijiahao/"+lane.IntentKey+"/ou_reviewer_1 baijiahao/"+lane.IntentKey+"/ou_reviewer_1]" {
		t.Fatalf("按原来的键再交、再发一次：saves=%+v publishes=%v", robot.saves, robot.publishes)
	}
	retried := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if retried.State != publishExternalDistributionSucceeded || retried.ContentPK != testRobotArticleID || retried.ActionID != "act-2" ||
		retried.URL != testRobotArticleURL("baijiahao", testRobotArticleID) {
		t.Fatalf("再发一次应发出去：%+v", retried)
	}
}

// TestRobotLaneRestartRecoveryPublishesOnce 进程在「转发布中」落盘之后、调发布之前退出：重启时按同一个键查，分发服务
// 那边没有这次的发布就调一次（上次那次的失败不算这次）；正在发的只跟着查。再重启一次也不多发。
func TestRobotLaneRestartRecoveryPublishesOnce(t *testing.T) {
	for _, tc := range []struct {
		name      string
		laneActed string
		kept      *robotDraftAction
		publishes int
		state     string
	}{
		{name: "还没调过发布", publishes: 1, state: publishExternalDistributionSucceeded},
		{name: "卡片重试后还没调", laneActed: "act-1", kept: &robotDraftAction{ID: "act-1", Kind: robotActionPublish, Status: robotActionFailed}, publishes: 1, state: publishExternalDistributionSucceeded},
		{name: "分发服务正在发", kept: &robotDraftAction{ID: "act-1", Kind: robotActionPublish, Status: robotActionDoing}, state: publishExternalDistributionPublishing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setTestRobotOnlyConfig(t)
			env := newPublishTestEnv(t)
			robot := useFakeRobot(env)
			job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
			interrupted := testRobotLaneIn(job.ID, publishExternalDistributionPublishing)
			interrupted.ActionID = tc.laneActed
			kept := testRobotSavedRecord(testRobotArticleID)
			kept.Action = tc.kept
			robot.remember(interrupted.IntentKey, kept)
			job = setTestLanes(t, env, job.ID, interrupted)

			env.pipeline.recoverExternalDistribution(job)
			if lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID); lane.State != tc.state || len(robot.publishes) != tc.publishes {
				t.Fatalf("重启当场就该定：lane=%+v publishes=%v", lane, robot.publishes)
			}
			env.pipeline.recoverExternalDistribution(mustJob(t, env, job.ID))
			env.pipeline.followRobotLanes()
			lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
			if lane.State != tc.state || len(robot.publishes) != tc.publishes || len(robot.saves) != 0 {
				t.Fatalf("应为 %s、调发布 %d 次：lane=%+v publishes=%v saves=%d", tc.state, tc.publishes, lane, robot.publishes, len(robot.saves))
			}
			if tc.publishes == 1 && robot.publishes[0] != "baijiahao/"+interrupted.IntentKey+"/"+publishExternalAutoActor {
				t.Fatalf("按同一个键发布：%v", robot.publishes)
			}
		})
	}
}

// TestRobotLaneRestartRecoveryChecksByKey 进程在存草稿的提交窗口里退出：分发服务可能存了也可能没存。恢复时
// 转待核对、按同一个键查；还在存就留着等兜底补查，查到存好就转发布中、发出去，绝不重新存。
func TestRobotLaneRestartRecoveryChecksByKey(t *testing.T) {
	setTestRobotOnlyConfig(t)
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	interrupted := testRobotLaneIn(job.ID, publishExternalDistributionPushing)
	interrupted.ContentPK, interrupted.DraftURL, interrupted.CompletedAt = "", "", ""
	interrupted.RequestedAt = time.Now().UTC().Format(time.RFC3339)
	job = setTestLanes(t, env, job.ID, interrupted)

	robot.getRecord = robotDraftRecord{Status: robotDraftSaving}
	env.pipeline.recoverExternalDistribution(job)
	lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if lane.State != publishExternalDistributionReconciliationRequired || !strings.Contains(lane.Error, "进程在存草稿时中断") ||
		fmt.Sprint(robot.gets) != "[baijiahao/"+interrupted.IntentKey+"]" {
		t.Fatalf("恢复应转待核对并按同一个键查：%+v gets=%v", lane, robot.gets)
	}
	if state := externalPlatformStatus(mustJob(t, env, job.ID), externalAccount{ID: testBaijiaAccountID, Platform: "baijiahao", Name: "百家号"}); state.Status != "待核对" || strings.Contains(state.Reason, "云梭") {
		t.Fatalf("待核对的说法不提云梭：%+v", state)
	}

	robot.getRecord = testRobotSavedRecord("1846000000000000003")
	env.pipeline.refreshExternalDistributionResults()
	lane = mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if lane.State != publishExternalDistributionSucceeded || lane.ContentPK != "1846000000000000003" || len(robot.gets) != 2 || len(robot.saves) != 0 ||
		fmt.Sprint(robot.publishes) != "[baijiahao/"+interrupted.IntentKey+"/"+publishExternalAutoActor+"]" {
		t.Fatalf("兜底补查查到存好应发出去、不重新存：%+v gets=%v saves=%d publishes=%v", lane, robot.gets, len(robot.saves), robot.publishes)
	}
	env.pipeline.refreshExternalDistributionResults()
	env.pipeline.followRobotLanes()
	if len(robot.gets) != 2 || len(robot.publishes) != 1 {
		t.Fatalf("定了的 lane 不再查：gets=%v publishes=%v", robot.gets, robot.publishes)
	}
}

// TestRobotLaneCardWording 2026-10-30 起卡片照实说「同步」「公开发布」：按钮「同步至百家号」、确认框写公开发布；
// 失败了「重新同步至百家号」；状态依次是同步中、发布中、平台审核中、已发布（给「查看文章」）、未通过、待核对、
// 未发布（停在草稿，给草稿链接）。
func TestRobotLaneCardWording(t *testing.T) {
	setTestRobotOnlyConfig(t)
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	section := func(lanes ...publishExternalLane) string {
		t.Helper()
		job.ExternalDistributionLanes = lanes
		card := buildReviewBatchCard([]publishJob{job})
		if body := cardJSON(t, card); strings.Contains(body, "云梭") || strings.Contains(body, "存草稿至") {
			t.Fatalf("卡片不提云梭、不再说存草稿: %s", body)
		}
		return distributionSectionJSON(t, card)
	}
	robotLane := func(state string) publishExternalLane {
		lane := testLane(testBaijiaAccountID, "百家号", state)
		lane.Driver = externalDriverRobot
		return lane
	}
	contains := func(body string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Fatalf("缺少 %q: %s", want, body)
			}
		}
	}
	noButton := func(body string) {
		t.Helper()
		if strings.Contains(body, feishuCardActionDistributeExternal) {
			t.Fatalf("不该给按钮: %s", body)
		}
	}

	contains(section(), `"content":"同步至百家号"`, `"content":"同步至百家号？"`, "将由分发工具把本文公开发布到百家号。请确认本文为原创文章。")
	failed := robotLane(publishExternalDistributionFailed)
	failed.Error = "服务器上的百家号没登录，请在分发工具的远程画面里登录后重新同步"
	contains(section(failed), `"content":"重新同步至百家号"`, "<font color='red'>上次同步失败</font>", "远程画面里登录",
		"上次没有发到百家号，这次由分发工具重新同步、公开发布到百家号，不会重复存稿")

	for state, status := range map[string]string{
		publishExternalDistributionPushing:    "<font color='blue'>同步中</font>",
		publishExternalDistributionPublishing: "<font color='blue'>发布中</font>",
		publishExternalDistributionReviewing:  "<font color='blue'>平台审核中</font>",
	} {
		body := section(robotLane(state))
		contains(body, status)
		noButton(body)
	}

	articleURL := testRobotArticleURL("baijiahao", testRobotArticleID)
	published := robotLane(publishExternalDistributionSucceeded)
	published.ContentPK, published.URL = testRobotArticleID, articleURL
	body := section(published)
	contains(body, "<font color='green'>已发布</font>", "**百家号**　[查看文章]("+articleURL+")")
	noButton(body)

	rejected := robotLane(publishExternalDistributionRejected)
	rejected.ContentPK, rejected.Error = testRobotArticleID, "标题涉嫌夸大"
	body = section(rejected)
	contains(body, "<font color='red'>未通过</font>", "<font color='grey'>标题涉嫌夸大</font>", "[去百家号后台查看](https://baijiahao.baidu.com/builder/rc/content)")
	noButton(body)

	reconcile := testRobotLaneIn(job.ID, publishExternalDistributionReconciliationRequired)
	reconcile.Error = "点了发布没看清发没发出去，请到百家号后台看一眼"
	body = section(reconcile)
	contains(body, "<font color='orange'>待核对</font>", "<font color='grey'>点了发布没看清发没发出去，请到百家号后台看一眼</font>", "[去百家号后台查看](")
	noButton(body)

	held := testRobotLaneIn(job.ID, publishExternalDistributionDraftSaved)
	held.Error = "草稿与原文对不上，没有发布：小标题：送去 3 处，读回 2 处"
	body = section(held)
	contains(body, "<font color='orange'>未发布</font>", "[去百家号草稿箱查看]("+testRobotDraftURL(testRobotArticleID)+")",
		"<font color='grey'>草稿与原文对不上，没有发布：小标题：送去 3 处，读回 2 处</font>")
	noButton(body)
	// 草稿地址不在百家号域名内就不给，退回百家号后台。
	held.DraftURL = ""
	contains(section(held), "[去百家号后台查看](https://baijiahao.baidu.com/builder/rc/content)")

	// 侧边栏同一套说法：按钮、状态、草稿地址。
	cfg, err := loadExternalTargets()
	if err != nil {
		t.Fatal(err)
	}
	held.DraftURL = testRobotDraftURL(testRobotArticleID)
	job.ExternalDistributionLanes = []publishExternalLane{held}
	platforms := externalEditView(job, cfg)["platforms"].([]externalEditPlatform)
	if len(platforms) != 1 || platforms[0].Status != "未发布" || platforms[0].DraftURL != testRobotDraftURL(testRobotArticleID) ||
		platforms[0].Action != "同步至百家号" || platforms[0].Editable || platforms[0].TitleMin != 2 || platforms[0].TitleMax != 64 {
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
// 卡片百家号那行灰字写出来（已发布、有文章链接也写），与读回对不上用「；」接；读回失败时也写。
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
	lane := testLane(testBaijiaAccountID, "百家号", publishExternalDistributionSucceeded)
	lane.Driver, lane.ContentPK, lane.URL, lane.Fidelity = externalDriverRobot, testRobotArticleID, testRobotArticleURL("baijiahao", testRobotArticleID), unread
	job.ExternalDistributionLanes = []publishExternalLane{lane}
	if body := distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job})); !strings.Contains(body, "<font color='grey'>百家号改版：找不到「存草稿」，模型改点了「保存」</font>") {
		t.Fatalf("卡片百家号那行应写出模型改点了什么: %s", body)
	}
}

// TestRobotIdempotencyKeyFollowsFrozenContent 同一份冻结内容同一个键；内容变了换键；只用分发服务认的字符。
func TestRobotIdempotencyKeyFollowsFrozenContent(t *testing.T) {
	first := robotIdempotencyKey(282, testBaijiaAccountID, "sha256:aaaa")
	if first != robotIdempotencyKey(282, testBaijiaAccountID, "sha256:aaaa") || first == robotIdempotencyKey(282, testBaijiaAccountID, "sha256:bbbb") {
		t.Fatalf("键应跟着冻结内容走：%q", first)
	}
	long := robotIdempotencyKey(282, "账号 id/带空格"+strings.Repeat("x", 200), "sha256:"+strings.Repeat("f", 64))
	if !robotKeyPattern.MatchString(first) || !robotKeyPattern.MatchString(long) {
		t.Fatalf("键只能用 A-Za-z0-9._:- 且不超过 128 位：%q %q", first, long)
	}
}

// TestRobotLaneStopsBeforeSubmitWhenUnconfigured 没配分发服务：提交前就失败、不落 intent，配好后可重新同步。
func TestRobotLaneStopsBeforeSubmitWhenUnconfigured(t *testing.T) {
	setTestRobotOnlyConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if lane.State != publishExternalDistributionFailed || !strings.Contains(lane.Error, "缺少 "+distributionAPIURLEnv) || lane.IntentKey != "" || !externalLaneCanSafeRetry(lane) {
		t.Fatalf("没配分发服务应在提交前失败、可重新存：%+v", lane)
	}
}

// TestRobotLaneOldFormatPayloadFailsBeforeSubmit 2026-10-24 之前冻结、还没提交过的旧格式（排版工具写好的网页代码）：
// 分发服务已不收，提交前就失败、不落 intent，可重新同步（按新格式重新冻结）；分发服务一次都不叫。
func TestRobotLaneOldFormatPayloadFailsBeforeSubmit(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	old := publishExternalLane{AccountID: testBaijiaAccountID, Name: "百家号", Driver: externalDriverRobot, State: publishExternalDistributionQueued,
		PayloadJSON: `{"platform":"baijiahao","title":"旧稿","html":"<p>正文</p>","cover_url":"https://cdn.example/a.jpg"}`, PayloadHash: "sha256:old"}
	job, err := env.store.update(job.ID, func(current *publishJob) { current.ExternalDistributionLanes = []publishExternalLane{old} })
	if err != nil {
		t.Fatal(err)
	}
	env.pipeline.pushRobotLane(job, old)
	lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if lane.State != publishExternalDistributionFailed || lane.Error != "草稿内容是旧格式，未提交；请重新同步至百家号" || lane.IntentKey != "" || !externalLaneCanSafeRetry(lane) {
		t.Fatalf("旧格式应在提交前失败、可重新存：%+v", lane)
	}
	if len(robot.saves) != 0 {
		t.Fatalf("旧格式不该送去分发服务：%+v", robot.saves)
	}
}
