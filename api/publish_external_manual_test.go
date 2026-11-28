package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testManualAccountID = "recTencentManual"
	testManualHomepage  = "https://news.qq.com/omn/author/8QMc3X9Y6YIavT7d"
)

// testManualAccount 2026-11-21 腾讯号改「人工发」：系统不推，人在后台发，系统按「主页」查人发了哪篇。
func testManualAccount(homepage string) externalAccount {
	account := testArticleAccount(testManualAccountID, "tencent", true)
	account.Status, account.Homepage = externalAccountManual, homepage
	return account
}

// TestExternalManualAccountsAreNeverPushed 人工发的号不进推送名单（自动外发、重推、卡片按钮都只看 Targets），
// 只进 Manual；按编号仍找得到（人工登记、监控认它）。
func TestExternalManualAccountsAreNeverPushed(t *testing.T) {
	setTestExternalAccounts(t, testArticleAccount(testBaijiaAccountID, "baijiahao", true), testManualAccount(testManualHomepage))
	cfg, err := loadExternalTargets()
	if err != nil {
		t.Fatal(err)
	}
	job := publishJob{SourceName: "FBIF食品饮料创新"}
	if got := cfg.forJob(job); len(got) != 1 || got[0].ID != testBaijiaAccountID {
		t.Fatalf("人工发的号不能进推送名单：%+v", got)
	}
	if got := cfg.manualForJob(job); len(got) != 1 || got[0].ID != testManualAccountID || got[0].Homepage != testManualHomepage {
		t.Fatalf("人工发的号要进 Manual：%+v", got)
	}
	if _, ok := cfg.jobTarget(job, testManualAccountID); ok {
		t.Fatal("人工发的号不能从按钮推")
	}
	if account, ok := cfg.account(testManualAccountID); !ok || !account.manual() {
		t.Fatalf("按编号要找得到人工发的号：%+v %v", account, ok)
	}
	if !externalManualTrackable(testManualAccount(testManualHomepage)) || externalManualTrackable(testManualAccount("")) {
		t.Fatal("平台能读公开文章、填了主页才查得到")
	}
}

// TestExternalManualLookupPutsLinkOnCard 等人发的稿：卡片写「待人工发」；人在平台上发了，经分发服务读公开文章按标题认出来，
// 记成人工已发、卡片带上链接；之后不再问。过了 7 天还没发的、查不到的号（没填主页）卡片上不列。
func TestExternalManualLookupPutsLinkOnCard(t *testing.T) {
	setTestExternalAccounts(t, testManualAccount(testManualHomepage))
	env := newPublishTestEnv(t)
	clock := useRetryClock(t, env, "2026-08-12 16:00") // 网站 08-11 16:11（北京时间）发布的第二天下午
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	// 以前推过、没登录失败的 lane 留着：改成人工发以后不算失败、不显示失败。
	setTestLanes(t, env, job.ID, publishExternalLane{
		AccountID: testManualAccountID, Name: "腾讯号", State: publishExternalDistributionFailed, Driver: externalDriverRobot,
		Error: "服务器上的腾讯号没登录", LoginRequired: true, CompletedAt: "2026-08-11T09:00:00Z",
	})

	body := distributionSectionJSON(t, buildReviewDoneCard(mustJob(t, env, job.ID)))
	if !strings.Contains(body, "待人工发") || strings.Contains(body, "没登录") || strings.Contains(body, "同步至腾讯号") {
		t.Fatalf("等人发的要写待人工发、不给按钮、不报失败：%s", body)
	}

	articleURL := "https://news.qq.com/rain/a/20260812A0000100"
	robot.publicArticles = []externalArticleRef{
		{URL: "https://news.qq.com/rain/a/20260812A0000200", Title: "别的一篇", PublishedAt: time.Date(2026, 8, 12, 3, 0, 0, 0, time.UTC)},
		{URL: articleURL, Title: job.Title, PublishedAt: time.Date(2026, 8, 12, 2, 0, 0, 0, time.UTC)},
	}
	env.pipeline.followExternalManualLanes(context.Background(), clock.now.UTC())
	lane := mustLane(t, mustJob(t, env, job.ID), testManualAccountID)
	if lane.State != publishExternalLaneManual || lane.URL != articleURL || lane.RequestedBy != publishExternalManualLookupActor || lane.LoginRequired || lane.Error != "" {
		t.Fatalf("查到了要记成人工已发、带链接：%+v", lane)
	}
	if fmt.Sprint(robot.publicAsks) != "[tencent/"+testManualHomepage+"]" || len(robot.saves) != 0 || len(robot.publishes) != 0 {
		t.Fatalf("只问一次公开文章、不推不发：asks=%v saves=%d publishes=%v", robot.publicAsks, len(robot.saves), robot.publishes)
	}
	body = distributionSectionJSON(t, buildReviewDoneCard(mustJob(t, env, job.ID)))
	if !strings.Contains(body, "人工已发") || !strings.Contains(body, articleURL) {
		t.Fatalf("卡片要显示人工已发和链接：%s", body)
	}
	env.pipeline.followExternalManualLanes(context.Background(), clock.now.UTC())
	if len(robot.publicAsks) != 1 {
		t.Fatalf("没有等人发的稿就不问：%v", robot.publicAsks)
	}

	// 别的稿：时段外不问；过了 7 天没发的不列、不问。
	other := createPublishedDistributionJobWithKey(t, env, "site:manual-other", "FBIF食品饮料创新")
	clock.set("2026-08-12 22:00")
	env.pipeline.followExternalManualLanes(context.Background(), clock.now.UTC())
	if len(robot.publicAsks) != 1 {
		t.Fatalf("时段外不问：%v", robot.publicAsks)
	}
	clock.set("2026-08-18 16:00") // 网站发布后 6 天 23 小时多
	if body := distributionSectionJSON(t, buildReviewDoneCard(mustJob(t, env, other.ID))); !strings.Contains(body, "待人工发") {
		t.Fatalf("7 天内还列着：%s", body)
	}
	clock.set("2026-08-18 16:30")
	if card := buildReviewDoneCard(mustJob(t, env, other.ID)); strings.Contains(cardJSON(t, card), "待人工发") {
		t.Fatalf("过了 7 天没发的不列：%s", cardJSON(t, card))
	}
	env.pipeline.followExternalManualLanes(context.Background(), clock.now.UTC())
	if len(robot.publicAsks) != 1 {
		t.Fatalf("过了 7 天不问：%v", robot.publicAsks)
	}

	// 查不到的号（没填主页）：等人发的也不列。
	setTestExternalAccounts(t, testManualAccount(""))
	clock.set("2026-08-12 16:00")
	if card := buildReviewDoneCard(mustJob(t, env, other.ID)); strings.Contains(cardJSON(t, card), "待人工发") {
		t.Fatalf("查不到的号不列：%s", cardJSON(t, card))
	}
}

// TestExternalManualRegistrationLooksUpPublicArticles 管理员给人工发的号登记、让系统自己找链接：经分发服务读公开文章按标题找。
func TestExternalManualRegistrationLooksUpPublicArticles(t *testing.T) {
	setTestExternalAccounts(t, testManualAccount(testManualHomepage))
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	useRetryClock(t, env, "2026-08-12 10:00")
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	articleURL := "https://news.qq.com/rain/a/20260811A0000300"
	robot.publicArticles = []externalArticleRef{{URL: articleURL, Title: job.Title, PublishedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)}}
	path := fmt.Sprintf("/api/publish/jobs/%d/external-distribution/manual", job.ID)
	if rec := doPublishReq(mux, "POST", path, `{"account_id":"`+testManualAccountID+`","lookup":true}`, true); rec.Code != 200 {
		t.Fatalf("登记失败：%d %s", rec.Code, rec.Body.String())
	}
	if lane := mustLane(t, mustJob(t, env, job.ID), testManualAccountID); lane.State != publishExternalLaneManual || lane.URL != articleURL || lane.RequestedBy != publishExternalManualActor {
		t.Fatalf("要登记成人工已发、带找到的链接：%+v", lane)
	}
}

// TestPublishMonitoringSkipsManualAccounts 号改成人工发以后，它上面以前没推成的不算失败、不再报「号没登录」。
func TestPublishMonitoringSkipsManualAccounts(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/manual", "https://mp.weixin.qq.com/s/manual")
	if err != nil {
		t.Fatal(err)
	}
	failedAt := time.Date(2026, 11, 20, 6, 43, 0, 0, time.UTC)
	if _, err := store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.SourceName = "FBIF食品饮料创新"
		current.ExternalDistributionLanes = []publishExternalLane{{
			AccountID: testManualAccountID, State: publishExternalDistributionFailed, ContentPK: "pk", LoginRequired: true,
			AutoRetries: len(externalAutoRetryDelays), CompletedAt: failedAt.Format(time.RFC3339),
		}}
	}); err != nil {
		t.Fatal(err)
	}
	live := testManualAccount(testManualHomepage)
	live.Status = externalAccountLive
	setTestExternalAccounts(t, live)
	if before := currentPublishMonitoring(store, false, 0, failedAt.Add(time.Hour)); before.Ready {
		t.Fatalf("号还在用时没推成要报：%+v", before)
	}
	setTestExternalAccounts(t, testManualAccount(testManualHomepage))
	after := currentPublishMonitoring(store, false, 0, failedAt.Add(time.Hour))
	if !after.Ready || after.FailedRecent != 0 || publishMonitoringHasIssue(after, "external_login_required") {
		t.Fatalf("改成人工发以后不算失败、不报没登录：%+v", after)
	}
}
