package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 测试用的「平台账号」record_id。
const (
	testToutiaoAccountID = "recTestToutiao01"
	testBaijiaAccountID  = "recTestBaijia001"
)

// setTestDistributionAccounts 多维表格「平台账号」里 FBIF食品饮料创新的头条号、百家号各一个在用号。
func setTestDistributionAccounts(t *testing.T) {
	t.Helper()
	setTestExternalAccounts(t, testArticleAccount(testToutiaoAccountID, "toutiao", false), testArticleAccount(testBaijiaAccountID, "baijiahao", false))
}

// yunsuoLegacyLane 云梭（2026-10-24 下线）留下的旧 lane：没有 driver，有云梭单号。
func yunsuoLegacyLane(accountID, name, state string) publishExternalLane {
	lane := testLane(accountID, name, state)
	lane.ContentPK = "pk-yunsuo-" + accountID
	lane.PayloadJSON = `{"type":0,"title":"旧标题","html":"<p>wrong-field</p>"}`
	lane.RequestedAt = "2026-10-12T02:00:00Z"
	return lane
}

const (
	testDistributionNewsID   int64 = 90001
	testDistributionLiveHTML       = `<p>这是一篇用于验证外部分发流程的原创文章正文。</p><p><img src="https://cdn.example.com/a.jpg" alt=""></p>`
)

func seedLivePublishedNews(t *testing.T, env *publishTestEnv, news foodTalksNewsDetail) {
	t.Helper()
	if news.ID == 0 {
		news.ID = testDistributionNewsID
	}
	env.ft.mu.Lock()
	if env.ft.newsByID == nil {
		env.ft.newsByID = map[int64]foodTalksNewsDetail{}
	}
	env.ft.newsByID[news.ID] = news
	env.ft.mu.Unlock()
}

func createPublishedDistributionJob(t *testing.T, env *publishTestEnv, sourceName string) publishJob {
	t.Helper()
	return createPublishedDistributionJobWithKey(t, env, "site:distribution-test", sourceName)
}

func createPublishedDistributionJobWithKey(t *testing.T, env *publishTestEnv, sourceKey, sourceName string) publishJob {
	t.Helper()
	job, _, err := env.store.create(sourceKey, testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.Title = "中国食品品牌如何用产品创新打开新市场"
		current.SourceName = sourceName
		current.FoodTalksNewsID = testDistributionNewsID
		current.BlocksJSON = `[{"k":"p","text":"这是一篇用于验证外部分发流程的原创文章正文。"},{"k":"img","src":"https://mmbiz.qpic.cn/a.jpg"}]`
		current.ImageMap = `{"https://mmbiz.qpic.cn/a.jpg":"https://cdn.example.com/a.jpg"}`
		current.CoverURL = "https://cdn.example.com/a.jpg"
		current.Summary = "食品品牌通过产品创新打开新的消费市场。"
		current.Tags = []string{"食品", "创新", "消费"}
		current.AINotes = "已核对正文出处，未发现外部转载声明；最终由审核人确认原创。"
		current.ContentHash = "sha256:distribution-content"
		current.ContentVersion = 1
		current.ContentFrozenVersion = 1
		current.ContentFrozenAt = "2026-08-11T08:00:00Z"
		current.RiskLevel = publishRiskPass
		current.ReviewAction = publishReviewApprove
		current.ReviewedAt = "2026-08-11T08:10:00Z"
		current.ReviewedBy = "ou_reviewer_1"
		current.PublishedAt = "2026-08-11T08:11:00Z"
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID:            testDistributionNewsID,
		Title:         job.Title,
		CoverImg:      job.CoverURL,
		Content:       testDistributionLiveHTML,
		Summary:       job.Summary,
		SourceName:    sourceName,
		PublishStatus: foodTalksPublishStatusPublished,
	})
	return job
}

func TestExternalDistributionUsesLivePublishedNewsNotJobSnapshot(t *testing.T) {
	setTestDistributionAccounts(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, err := env.store.update(job.ID, func(current *publishJob) {
		current.BlocksJSON = `[{"k":"p","text":"这是历史错误正文，绝对不能发到外部平台。"},{"k":"img","src":"https://mmbiz.qpic.cn/a.jpg"}]`
		current.Title = "历史错误标题不该发出去"
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID:            testDistributionNewsID,
		Title:         "把茶做成零食，这家老茶铺年营收1.4亿",
		CoverImg:      "https://cdn.example.com/a.jpg",
		Content:       `<p>这是网站上最新已发布的正确正文。</p><p><img src="https://cdn.example.com/a.jpg" alt=""></p>`,
		Summary:       "祇园辻利把茶做成零食。",
		Author:        "FBIF",
		SourceName:    "FBIF食品饮料创新",
		PublishStatus: foodTalksPublishStatusPublished,
	})
	robot := useFakeRobot(env)
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("应用最新已发布稿分发：%v", err)
	}
	// 头条号、百家号都经分发服务，各存一次草稿。
	if fmt.Sprint(robot.platforms) != "[toutiao baijiahao]" {
		t.Fatalf("两个平台各存一次草稿：%v", robot.platforms)
	}
	for i, draft := range robot.saves {
		if !strings.Contains(distributionOutline(draft.Blocks), "最新已发布的正确正文") || strings.Contains(distributionOutline(draft.Blocks), "历史错误正文") || draft.Title != "把茶做成零食，这家老茶铺年营收1.4亿" {
			t.Fatalf("%s草稿必须用网站最新已发布稿，不得发任务里的历史错误稿：%+v", robot.platforms[i], draft)
		}
	}
}

func TestExternalDistributionFailsClosedIfCMSNotPublished(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL,
		Content: testDistributionLiveHTML, PublishStatus: foodTalksPublishStatusDraft,
		SourceName: "FBIF食品饮料创新",
	})
	robot := useFakeRobot(env)
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err == nil || !strings.Contains(err.Error(), "还不是已发布") {
		t.Fatalf("网站未公开时必须拒绝外发：%v", err)
	}
	if len(robot.saves) != 0 {
		t.Fatalf("未公开不得存草稿，实际 %d", len(robot.saves))
	}
}

func TestExternalDistributionFailsClosedIfLiveNewsMissing(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	env.ft.mu.Lock()
	env.ft.newsByID = map[int64]foodTalksNewsDetail{}
	env.ft.mu.Unlock()
	robot := useFakeRobot(env)
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err == nil {
		t.Fatal("读不到网站已发布稿时必须拒绝外发")
	}
	if len(robot.saves) != 0 {
		t.Fatalf("读失败不得存草稿，实际 %d", len(robot.saves))
	}
}

func TestExternalDistributionManualCMSUsesLiveNewsWithoutPipelineApprove(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, err := env.store.update(job.ID, func(current *publishJob) {
		current.ReviewAction = ""
		current.ReviewedAt = ""
		current.ReviewedBy = ""
		current.ApprovedRiskVersion = ""
		current.PublicationAction = publishPublicationActionManualCMS
		current.BlocksJSON = `[{"k":"p","text":"流水线进站时的旧稿，已被后台改过。"}]`
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID:            testDistributionNewsID,
		Title:         "把茶做成零食，这家老茶铺年营收1.4亿",
		CoverImg:      "https://cdn.example.com/a.jpg",
		Content:       `<p>后台公开后的最新正文。</p><p><img src="https://cdn.example.com/a.jpg" alt=""></p>`,
		Summary:       "祇园辻利把茶做成零食。",
		Author:        "FBIF",
		SourceName:    "FBIF食品饮料创新",
		PublishStatus: foodTalksPublishStatusPublished,
	})
	robot := useFakeRobot(env)
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("后台公开的原创稿应可分发：%v", err)
	}
	if len(robot.saves) != 2 {
		t.Fatalf("两个平台各存一次草稿：%v", robot.platforms)
	}
	for _, draft := range robot.saves {
		if !strings.Contains(distributionOutline(draft.Blocks), "后台公开后的最新正文") || strings.Contains(distributionOutline(draft.Blocks), "流水线进站时的旧稿") {
			t.Fatalf("人工后台公开后必须发最新网站稿：%s", distributionOutline(draft.Blocks))
		}
	}
}

func TestExternalDistributionCardAndWorkerPublishOriginalOnly(t *testing.T) {
	setTestDistributionAccounts(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	robot := useFakeRobot(env)

	card := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(card, "存草稿至头条号") || !strings.Contains(card, "存草稿至百家号") || !strings.Contains(card, feishuCardActionDistributeExternal) {
		t.Fatalf("已发布原创卡应显示固定目标的一键分发按钮：%s", card)
	}
	value, _ := json.Marshal(map[string]any{
		"action": feishuCardActionDistributeExternal, "job_id": job.ID,
		"distribution_version": publishExternalDistributionCardVersion(job),
	})
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", value)
	if out.toastType != "success" || !strings.Contains(out.toastContent, "同步分发") {
		t.Fatalf("卡片动作应立即受理：%+v", out)
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{testToutiaoAccountID, testBaijiaAccountID} {
		if lane := mustLane(t, got, id); lane.State != publishExternalDistributionDraftSaved || lane.Driver != externalDriverRobot || lane.ContentPK != testRobotArticleID {
			t.Fatalf("两个平台都应存好草稿：%+v", lane)
		}
	}
	// 各平台分开存：每个平台一次，正文渠道中性。
	if fmt.Sprint(robot.platforms) != "[toutiao baijiahao]" {
		t.Fatalf("两个平台应各存一次：%v", robot.platforms)
	}
	for _, draft := range robot.saves {
		if strings.Contains(distributionOutline(draft.Blocks), "FoodTalks") {
			t.Fatalf("正文渠道中性：%s", distributionOutline(draft.Blocks))
		}
	}
	if repeated := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", value); repeated.toastType != "info" || len(robot.saves) != 2 {
		t.Fatalf("重复点击不得再次存草稿：out=%+v robot=%v", repeated, robot.platforms)
	}
}

func TestExternalDistributionRejectsRepostInCardAndServer(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "外部行业媒体")
	robot := useFakeRobot(env)

	card := cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(card, feishuCardActionDistributeExternal) || !strings.Contains(card, "转载文章仅发布至 FoodTalks") {
		t.Fatalf("转载卡必须隐藏按钮并解释规则：%s", card)
	}
	_, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job))
	if err == nil || !strings.Contains(err.Error(), "转载文章") || len(robot.saves) != 0 {
		t.Fatalf("服务端门禁也必须拒绝转载：err=%v saves=%d", err, len(robot.saves))
	}
}

func TestExternalDistributionFailsClosedOnExplicitAttribution(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.BlocksJSON = `[{"k":"p","text":"转载自：小食代"},{"k":"p","text":"正文"}]`
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL,
		Content: `<p>转载自：小食代</p><p>正文</p><p><img src="https://cdn.example.com/a.jpg"></p>`,
		Summary: job.Summary, SourceName: "FBIF食品饮料创新",
		PublishStatus: foodTalksPublishStatusPublished,
	})
	card := cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(card, feishuCardActionDistributeExternal) || !strings.Contains(card, "转载文章仅发布至 FoodTalks") {
		t.Fatalf("自有号正文存在外部出处时必须隐藏按钮：%s", card)
	}
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err == nil || !strings.Contains(err.Error(), "转载文章") {
		t.Fatalf("服务端必须拒绝显式外部出处：%v", err)
	}
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.BlocksJSON = `[{"k":"p","text":"本文经小食代授权转载"},{"k":"p","text":"正文"}]`
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL,
		Content: `<p>本文经小食代授权转载</p><p>正文</p>`,
		Summary: job.Summary, SourceName: "FBIF食品饮料创新",
		PublishStatus: foodTalksPublishStatusPublished,
	})
	if card = cardJSON(t, buildReviewDoneCard(job)); strings.Contains(card, feishuCardActionDistributeExternal) {
		t.Fatalf("授权转载同样不得开放外部分发：%s", card)
	}
}

func TestExternalDistributionOwnAccountDoesNotWaitForArk(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.Author = "FBIF"
		current.BlocksJSON = `[{"k":"p","text":"这是一篇自有号原创正文。"}]`
		current.AINotes = "AI 预填未执行（上游不可用）"
		current.RiskLevel = publishRiskWarning
		current.RiskItems = []publishRiskItem{{
			Category:  publishRiskCategorySystem,
			Level:     publishRiskWarning,
			Certainty: publishRiskCertaintyUnverified,
			Evidence:  "风险模型未能产出可验证的结构化结果",
			Reason:    "模型不可达",
		}}
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	card := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(card, feishuCardActionDistributeExternal) {
		t.Fatalf("自有号原创不得因火山检查未完成而隐藏外发按钮：%s", card)
	}
	if strings.Contains(card, "原创状态未完成核验") {
		t.Fatalf("自有号原创不应再提示等待火山核验：%s", card)
	}
}

func TestExternalDistributionSafeFailureRendersRetryButton(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	baijia := testLane(testBaijiaAccountID, "百家号", publishExternalDistributionFailed)
	baijia.Driver = externalDriverRobot
	// 头条号是云梭留下的失败旧 lane（云梭收下了、没发出去）：也能重新存草稿。
	job = setTestLanes(t, env, job.ID, yunsuoLegacyLane(testToutiaoAccountID, "头条号", publishExternalDistributionFailed), baijia)
	card := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(card, "重新存草稿至头条号") || !strings.Contains(card, "重新存草稿至百家号") || !strings.Contains(card, "不会重复存稿") ||
		strings.Contains(card, "云梭") {
		t.Fatalf("失败的平台都给重新存草稿入口、不提云梭：%s", card)
	}
}

// TestExternalDistributionUncertainSaveNeverRepeats 结果不明：按幂等键查，查不出结果就留在待核对，
// 重启恢复与兜底补查都不再提交。
func TestExternalDistributionUncertainSaveNeverRepeats(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	robot := useFakeRobot(env)
	robot.saveErr = &robotUncertainError{Cause: "网络请求失败或超时"}
	robot.getRecord = robotDraftRecord{Status: robotDraftPending}

	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	got, _ := env.store.byID(job.ID)
	if externalDistributionSummaryState(got) != publishExternalDistributionReconciliationRequired || len(robot.saves) != 2 {
		t.Fatalf("不确定结果必须进入待核对：lanes=%+v saves=%d", got.ExternalDistributionLanes, len(robot.saves))
	}
	env.pipeline.recoverExternalDistribution(got)
	env.pipeline.refreshExternalDistributionResults()
	if len(robot.saves) != 2 {
		t.Fatalf("恢复扫描不得重放不确定的提交，实际 %d 次", len(robot.saves))
	}
}

func TestReviewCardOutboxesSerializeAndLatestLifecycleWins(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.ReviewCardMsgID = "om_review_card"
		current.ExternalDistributionLanes = []publishExternalLane{
			testLane(testToutiaoAccountID, "头条号", publishExternalDistributionSucceeded),
			testLane(testBaijiaAccountID, "百家号", publishExternalDistributionSucceeded),
		}
		current.ExternalDistributionCardSeq = 1
		current.ExternalDistributionCardState = publishExternalCardPending
	})
	firstSendEntered := make(chan struct{})
	releaseFirstSend := make(chan struct{})
	var cards []string
	calls := 0
	env.pipeline.updateCard = func(_ context.Context, _ string, card map[string]any) error {
		calls++
		raw, _ := json.Marshal(card)
		if calls == 1 {
			close(firstSendEntered)
			<-releaseFirstSend
		}
		cards = append(cards, string(raw))
		return nil
	}
	externalDone := make(chan struct{})
	go func() {
		env.pipeline.dispatchExternalDistributionCardUpdate(job.ID)
		close(externalDone)
	}()
	<-firstSendEntered
	offline, err := env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageOffline
		current.UnpublishedAt = "2026-08-11T09:00:00Z"
		current.PublicationAction = publishPublicationActionUnpublish
	})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleDone := make(chan error, 1)
	go func() {
		lifecycleDone <- env.pipeline.sendTerminalCardNotification(context.Background(), offline, "offline")
	}()
	close(releaseFirstSend)
	<-externalDone
	if err := <-lifecycleDone; err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 || !strings.Contains(cards[1], "已下线") || strings.Contains(cards[1], feishuCardActionDistributeExternal) {
		t.Fatalf("最后写入的卡必须反映最新 offline 生命周期：%v", cards)
	}
}

// TestReplaceExternalDistributionAfterReconcile 待核对的平台：卡片不给按钮、卡片路径也拒；管理员核对后重发。
// 交过分发服务的按原来的冻结内容和幂等键再交（分发服务先去草稿箱对账，剩一篇就认它）；换键会在人删到只剩一篇时多存一篇。
// 云梭旧 lane 没交过分发服务，用 FoodTalks 现在的正文重建。
func TestReplaceExternalDistributionAfterReconcile(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	frozen := robotDraftPayload{Platform: "baijiahao", Title: "上次冻结的标题", CoverURL: "https://cdn.example.com/a.jpg",
		Blocks: []Block{{K: "txt", Runs: []Run{{Type: "txt", Text: "上次冻结的正文"}}}}}
	frozenJSON, _ := json.Marshal(frozen)
	baijia := testLane(testBaijiaAccountID, "百家号", publishExternalDistributionReconciliationRequired)
	baijia.Driver, baijia.PayloadJSON, baijia.PayloadHash = externalDriverRobot, string(frozenJSON), "sha256:frozen"
	baijia.IntentKey = robotIdempotencyKey(job.ID, testBaijiaAccountID, baijia.PayloadHash)
	baijia.Error = "草稿箱里这次新建的同名草稿有 2 篇，请删到只剩一篇"
	job = setTestLanes(t, env, job.ID, yunsuoLegacyLane(testToutiaoAccountID, "头条号", publishExternalDistributionReconciliationRequired), baijia)
	card := cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(card, feishuCardActionDistributeExternal) {
		t.Fatalf("待核对的任务不得出现卡片重试按钮：%s", card)
	}
	robot := useFakeRobot(env)
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err == nil {
		t.Fatal("卡片路径不得对待核对的平台再存一次")
	}

	queued, err := env.pipeline.replaceExternalDistributionAfterReconcile(job.ID, publishExternalAdminRetryActor)
	if err != nil {
		t.Fatal(err)
	}
	if lane := mustLane(t, queued, testToutiaoAccountID); lane.State != publishExternalDistributionQueued || lane.Driver != externalDriverRobot {
		t.Fatalf("管理员重发应先落盘 queued 再异步存草稿：%+v", lane)
	}
	got := mustJob(t, env, job.ID)
	for _, id := range []string{testToutiaoAccountID, testBaijiaAccountID} {
		if lane := mustLane(t, got, id); lane.Driver != externalDriverRobot || lane.State != publishExternalDistributionDraftSaved || lane.RequestedBy != publishExternalAdminRetryActor {
			t.Fatalf("重发应经分发服务存好草稿：%+v", lane)
		}
	}
	if len(robot.saves) != 2 {
		t.Fatalf("待核对的两个平台各交一次，实际 %d", len(robot.saves))
	}
	for i, draft := range robot.saves {
		switch robot.platforms[i] {
		case "baijiahao":
			if draft.IdempotencyKey != baijia.IntentKey || draft.Title != frozen.Title || !reflect.DeepEqual(draft.Blocks, frozen.Blocks) {
				t.Fatalf("交过分发服务的要按原来的键和冻结内容再交：%+v", draft)
			}
		default:
			if !strings.Contains(distributionOutline(draft.Blocks), "这是一篇用于验证外部分发流程的原创文章正文。") {
				t.Fatalf("云梭旧 lane 应按现在的正文重建：%s", distributionOutline(draft.Blocks))
			}
		}
	}
}

// TestReplaceExternalDistributionRefusesOldFormatRobotLane 10-24 前按旧格式（网页代码）交给分发服务的：
// 分发服务已不收旧格式，没法按原来的键再问；也不换键重存，免得多存一篇。
func TestReplaceExternalDistributionRefusesOldFormatRobotLane(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	baijia := testLane(testBaijiaAccountID, "百家号", publishExternalDistributionReconciliationRequired)
	baijia.Driver, baijia.PayloadHash = externalDriverRobot, "sha256:old"
	baijia.PayloadJSON = `{"platform":"baijiahao","title":"旧标题","html":"<p>旧格式</p>","cover_url":"https://cdn.example.com/a.jpg"}`
	baijia.IntentKey = robotIdempotencyKey(job.ID, testBaijiaAccountID, baijia.PayloadHash)
	job = setTestLanes(t, env, job.ID, baijia)
	robot := useFakeRobot(env)
	_, err := env.pipeline.replaceExternalDistributionAfterReconcile(job.ID, publishExternalAdminRetryActor)
	if err == nil || !strings.Contains(err.Error(), "旧格式") || len(robot.saves) != 0 {
		t.Fatalf("旧格式的待核对 lane 不得换键重存：err=%v saves=%d", err, len(robot.saves))
	}
	if lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID); lane.State != publishExternalDistributionReconciliationRequired || lane.IntentKey != baijia.IntentKey {
		t.Fatalf("lane 应原样留在待核对：%+v", lane)
	}
}

func TestReplaceExternalDistributionRejectedWhenSettled(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	saved := testLane(testBaijiaAccountID, "百家号", publishExternalDistributionDraftSaved)
	saved.Driver, saved.ContentPK = externalDriverRobot, testRobotArticleID
	job = setTestLanes(t, env, job.ID, yunsuoLegacyLane(testToutiaoAccountID, "头条号", publishExternalDistributionSucceeded), saved)
	robot := useFakeRobot(env)
	if _, err := env.pipeline.replaceExternalDistributionAfterReconcile(job.ID, publishExternalAdminRetryActor); err == nil || len(robot.saves) != 0 {
		t.Fatalf("已发布、已存草稿的平台不得管理员重发：err=%v saves=%d", err, len(robot.saves))
	}
}

func TestRetryExternalDistributionHTTPRequiresAdminAndRebuilds(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/jobs/1/retry-external-distribution", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无管理员令牌应 401，实际 %d", rec.Code)
	}
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	setTestLanes(t, env, job.ID, yunsuoLegacyLane(testToutiaoAccountID, "头条号", publishExternalDistributionReconciliationRequired))
	robot := useFakeRobot(env)
	rec := doPublishReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/retry-external-distribution", job.ID), "", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"retried":true`) {
		t.Fatalf("待核对的已发布任务应可管理员重发，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got := mustJob(t, env, job.ID)
	if lane := mustLane(t, got, testToutiaoAccountID); lane.State != publishExternalDistributionDraftSaved || lane.ContentPK != testRobotArticleID || len(robot.saves) != 1 {
		t.Fatalf("HTTP 重发未闭环：%+v saves=%d", lane, len(robot.saves))
	}
	if _, ok := externalLaneFor(got, testBaijiaAccountID); ok {
		t.Fatalf("管理员重发只推待核对的平台，不得顺带推别的平台：%+v", got.ExternalDistributionLanes)
	}
}

// TestExternalCardExpiredIsTerminal 补发九月旧稿时，原审核卡已过飞书 14 天可更新期限（230031）：
// 不重试 10 次进死信告警，也不在群里补发新卡；记为 expired，状态照常在 lane 上。
func TestExternalCardExpiredIsTerminal(t *testing.T) {
	setTestDistributionAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.ReviewCardMsgID = "om_old_card"
		current.ExternalDistributionLanes = []publishExternalLane{testLane(testToutiaoAccountID, "头条号", publishExternalDistributionSucceeded)}
		enqueueExternalDistributionCardUpdate(current)
	})
	calls, newCards, retried := 0, 0, false
	env.pipeline.updateCard = func(context.Context, string, map[string]any) error {
		calls++
		return fmt.Errorf("飞书更新卡片失败 code=230031 msg=Message has expired: %w", errFeishuMessageExpired)
	}
	env.pipeline.notifyCard = func(context.Context, map[string]any) error { newCards++; return nil }
	env.pipeline.retryNotification = func(time.Duration, func()) { retried = true }

	env.pipeline.dispatchExternalDistributionCardUpdate(job.ID)
	got := mustJob(t, env, job.ID)
	if got.ExternalDistributionCardState != publishExternalCardExpired || got.ExternalDistributionCardError != "" || calls != 1 || newCards != 0 || retried {
		t.Fatalf("过期卡应一次就停：state=%q err=%q calls=%d new=%d retried=%v", got.ExternalDistributionCardState, got.ExternalDistributionCardError, calls, newCards, retried)
	}
	var status publishMonitoringStatus
	updatePublishMonitoringNotifications(&status, got, 60)
	if status.NotificationDeadLetters != 0 || status.NotificationErrors != 0 || status.NotificationPending != 0 {
		t.Fatalf("过期卡不算告警：%+v", status)
	}

	// 改之前进了死信的旧数据：读盘即改记 expired；别的死信不动。
	old := publishJob{ExternalDistributionCardState: publishExternalCardDeadLetter,
		ExternalDistributionCardError: "更新审核群 oc_x 的卡片失败：飞书更新卡片失败 code=230031 msg=Message has expired when updating message, ext="}
	migrateExpiredExternalCard(&old)
	if old.ExternalDistributionCardState != publishExternalCardExpired || old.ExternalDistributionCardError != "" {
		t.Fatalf("过期死信应迁为 expired：%+v", old)
	}
	other := publishJob{ExternalDistributionCardState: publishExternalCardDeadLetter, ExternalDistributionCardError: "飞书更新卡片接口不可达"}
	migrateExpiredExternalCard(&other)
	if other.ExternalDistributionCardState != publishExternalCardDeadLetter {
		t.Fatalf("别的死信不动：%+v", other)
	}
}

// TestYunsuoLegacyLanesOnlyDisplay 云梭 2026-10-24 下线：它留下的 lane 只显示。已发布的照旧是已发布、给平台后台；
// 失败的给「重新存草稿」，点了换成一条经分发服务的 lane；没走完的读盘即转待核对，重启也不会再推。
// 卡片与侧边栏不提云梭。
func TestYunsuoLegacyLanesOnlyDisplay(t *testing.T) {
	setTestDistributionAccounts(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	failed := yunsuoLegacyLane(testBaijiaAccountID, "百家号", publishExternalDistributionFailed)
	failed.Remark = "发布任务执行失败(封面图尺寸过小(780x332)，要求不小于 452x352)"
	job = setTestLanes(t, env, job.ID, yunsuoLegacyLane(testToutiaoAccountID, "头条号", publishExternalDistributionSucceeded), failed)

	toutiao := externalPlatformStatus(job, externalAccount{ID: testToutiaoAccountID, Platform: "toutiao", Name: "头条号"})
	if toutiao.Status != "已发布" || toutiao.ManageURL != testPlatform(t, "toutiao").ManageURL || toutiao.Editable {
		t.Fatalf("云梭发出去的照旧是已发布、给平台后台：%+v", toutiao)
	}
	baijia := externalPlatformStatus(job, externalAccount{ID: testBaijiaAccountID, Platform: "baijiahao", Name: "百家号"})
	if baijia.Status != "失败" || !baijia.Editable || baijia.Reason != "封面图太小（780×332，要求至少 452×352）" {
		t.Fatalf("云梭没发出去的可以重新存草稿：%+v", baijia)
	}
	card := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(card, "重新存草稿至百家号") || strings.Contains(card, "重新存草稿至头条号") || strings.Contains(card, "云梭") {
		t.Fatalf("只有失败的给重新存草稿、不提云梭：%s", card)
	}
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, job, testBaijiaAccountID))
	if out.toastType != "success" || len(robot.saves) != 1 || robot.platforms[0] != "baijiahao" {
		t.Fatalf("点重新存草稿应只给百家号存一篇：%+v platforms=%v", out, robot.platforms)
	}
	got := mustJob(t, env, job.ID)
	if lane := mustLane(t, got, testBaijiaAccountID); lane.Driver != externalDriverRobot || lane.State != publishExternalDistributionDraftSaved {
		t.Fatalf("重存后换成经分发服务的 lane：%+v", lane)
	}
	if lane := mustLane(t, got, testToutiaoAccountID); lane.State != publishExternalDistributionSucceeded || lane.Driver != "" {
		t.Fatalf("已发布的旧 lane 不动：%+v", lane)
	}

	// 没走完的旧 lane（下线时线上一条都没有）：读盘转待核对，恢复不推。
	for _, state := range []string{publishExternalDistributionQueued, publishExternalDistributionPushing, publishExternalDistributionPolling} {
		stuck := yunsuoLegacyLane(testToutiaoAccountID, "头条号", state)
		j := publishJob{ExternalDistributionLanes: []publishExternalLane{stuck}}
		retireYunsuoLanes(&j)
		if lane := j.ExternalDistributionLanes[0]; lane.State != publishExternalDistributionReconciliationRequired || !strings.Contains(lane.Error, "系统不会再推") {
			t.Fatalf("%s 的云梭旧 lane 应转待核对：%+v", state, lane)
		}
		saved := testLane(testBaijiaAccountID, "百家号", state)
		saved.Driver = externalDriverRobot
		j = publishJob{ExternalDistributionLanes: []publishExternalLane{saved}}
		if retireYunsuoLanes(&j); j.ExternalDistributionLanes[0].State != state {
			t.Fatalf("经分发服务的 lane 不动：%+v", j.ExternalDistributionLanes[0])
		}
	}
	stuck := setTestLanes(t, env, job.ID, yunsuoLegacyLane(testToutiaoAccountID, "头条号", publishExternalDistributionQueued))
	env.pipeline.recoverExternalDistribution(stuck)
	if len(robot.saves) != 1 {
		t.Fatalf("云梭旧 lane 恢复时不推：%d", len(robot.saves))
	}
}
