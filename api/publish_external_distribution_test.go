package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	testYunsuoToutiaoID = "11111111-1111-4111-8111-111111111111"
	testYunsuoBaijiaID  = "22222222-2222-4222-8222-222222222222"
)

type fakeYunsuoAPI struct {
	pushCalls   int
	statusCalls int
	payload     yunsuoPushPayload
	pushed      [][]string // 每次推送带的账号，按顺序
	contentPK   string
	pushErr     error
	statusErr   error
	statuses    []yunsuoAccountStatus
}

func (f *fakeYunsuoAPI) Push(_ context.Context, payload yunsuoPushPayload) (string, error) {
	f.pushCalls++
	f.payload = payload
	f.pushed = append(f.pushed, append([]string(nil), payload.AccountIDs...))
	return f.contentPK, f.pushErr
}

func (f *fakeYunsuoAPI) Status(_ context.Context, _ string) ([]yunsuoAccountStatus, error) {
	f.statusCalls++
	return append([]yunsuoAccountStatus(nil), f.statuses...), f.statusErr
}

func setTestYunsuoConfig(t *testing.T) {
	t.Helper()
	t.Setenv(yunsuoBaseURLEnv, "https://www.yunsuofabu.com/v1")
	t.Setenv(yunsuoUserCodeEnv, "test-user")
	t.Setenv(yunsuoAPIKeyEnv, "test-api-key")
	t.Setenv(yunsuoTargetAccountsEnv, `[{"id":"`+testYunsuoToutiaoID+`","name":"头条号"},{"id":"`+testYunsuoBaijiaID+`","name":"百家号"}]`)
	t.Setenv(yunsuoCategoriesEnv, "美食,饮食文化,财经,消费,公司")
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
	setTestYunsuoConfig(t)
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
	fake := &fakeYunsuoAPI{
		contentPK: "content-pk-live",
		statuses: []yunsuoAccountStatus{
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoToutiaoID, Name: "头条号"}},
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoBaijiaID, Name: "百家号"}},
		},
	}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("应用最新已发布稿分发：%v", err)
	}
	if fake.pushCalls != 2 {
		t.Fatalf("两个平台各推一次，实际 %d", fake.pushCalls)
	}
	if !strings.Contains(fake.payload.HTML, "最新已发布的正确正文") {
		t.Fatalf("外发必须用网站最新已发布正文：%s", fake.payload.HTML)
	}
	if strings.Contains(fake.payload.HTML, "历史错误正文") || strings.Contains(fake.payload.Title, "历史错误标题") {
		t.Fatalf("不得把任务里的历史错误稿发出去：title=%q html=%s", fake.payload.Title, fake.payload.HTML)
	}
	if !strings.Contains(fake.payload.Title, "把茶做成零食") {
		t.Fatalf("标题也必须用网站最新已发布稿：%q", fake.payload.Title)
	}
}

func TestExternalDistributionFailsClosedIfCMSNotPublished(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL,
		Content: testDistributionLiveHTML, PublishStatus: foodTalksPublishStatusDraft,
		SourceName: "FBIF食品饮料创新",
	})
	fake := &fakeYunsuoAPI{contentPK: "must-not-be-used"}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err == nil || !strings.Contains(err.Error(), "还不是已发布") {
		t.Fatalf("网站未公开时必须拒绝外发：%v", err)
	}
	if fake.pushCalls != 0 {
		t.Fatalf("未公开不得 push，实际 %d", fake.pushCalls)
	}
}

func TestExternalDistributionFailsClosedIfLiveNewsMissing(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	env.ft.mu.Lock()
	env.ft.newsByID = map[int64]foodTalksNewsDetail{}
	env.ft.mu.Unlock()
	fake := &fakeYunsuoAPI{contentPK: "must-not-be-used"}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err == nil {
		t.Fatal("读不到网站已发布稿时必须拒绝外发")
	}
	if fake.pushCalls != 0 {
		t.Fatalf("读失败不得 push，实际 %d", fake.pushCalls)
	}
}

func TestExternalDistributionManualCMSUsesLiveNewsWithoutPipelineApprove(t *testing.T) {
	setTestYunsuoConfig(t)
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
	fake := &fakeYunsuoAPI{
		contentPK: "content-pk-manual",
		statuses: []yunsuoAccountStatus{
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoToutiaoID, Name: "头条号"}},
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoBaijiaID, Name: "百家号"}},
		},
	}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatalf("后台公开的原创稿应可分发：%v", err)
	}
	if !strings.Contains(fake.payload.HTML, "后台公开后的最新正文") || strings.Contains(fake.payload.HTML, "流水线进站时的旧稿") {
		t.Fatalf("人工后台公开后必须发最新网站稿：%s", fake.payload.HTML)
	}
}

func TestExternalDistributionCardAndWorkerPublishOriginalOnly(t *testing.T) {
	setTestYunsuoConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	fake := &fakeYunsuoAPI{
		contentPK: "content-pk-1",
		statuses: []yunsuoAccountStatus{
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoToutiaoID, Name: "头条号"}},
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoBaijiaID, Name: "百家号"}},
		},
	}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }

	card := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(card, "同步至头条号") || !strings.Contains(card, "同步至百家号") || !strings.Contains(card, feishuCardActionDistributeExternal) {
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
	for _, id := range []string{testYunsuoToutiaoID, testYunsuoBaijiaID} {
		if lane := mustLane(t, got, id); lane.State != publishExternalDistributionSucceeded || lane.ContentPK != "content-pk-1" {
			t.Fatalf("原创分发未闭环：%+v", lane)
		}
	}
	// 各平台分开推：每个平台一次 push、一次查询，payload 只带本平台账号。
	if fake.pushCalls != 2 || fake.statusCalls != 2 {
		t.Fatalf("两个平台应各推一次、各查一次：push=%d status=%d", fake.pushCalls, fake.statusCalls)
	}
	if len(fake.payload.AccountIDs) != 1 || strings.Contains(fake.payload.HTML, "FoodTalks") {
		t.Fatalf("每次推送只带一个账号，正文渠道中性：%+v", fake.payload)
	}
	if repeated := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", value); repeated.toastType != "info" || fake.pushCalls != 2 {
		t.Fatalf("重复点击不得再次 push：out=%+v push=%d", repeated, fake.pushCalls)
	}
}

func TestExternalDistributionRejectsRepostInCardAndServer(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "外部行业媒体")
	fake := &fakeYunsuoAPI{contentPK: "must-not-be-used"}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }

	card := cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(card, feishuCardActionDistributeExternal) || !strings.Contains(card, "转载文章仅发布至 FoodTalks") {
		t.Fatalf("转载卡必须隐藏按钮并解释规则：%s", card)
	}
	_, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job))
	if err == nil || !strings.Contains(err.Error(), "转载文章") || fake.pushCalls != 0 {
		t.Fatalf("服务端门禁也必须拒绝转载：err=%v push=%d", err, fake.pushCalls)
	}
}

func TestExternalDistributionFailsClosedOnExplicitAttribution(t *testing.T) {
	setTestYunsuoConfig(t)
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
	setTestYunsuoConfig(t)
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
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job = setTestLanes(t, env, job.ID,
		testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionFailed),
		testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionFailed))
	card := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(card, "重新同步至头条号") || !strings.Contains(card, "重新同步至百家号") || !strings.Contains(card, "不会重复发布") {
		t.Fatalf("明确未受理的失败必须提供安全重试入口：%s", card)
	}
}

func TestExternalDistributionRejectedStatusStopsPolling(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	fake := &fakeYunsuoAPI{
		contentPK: "content-pk-rejected-status",
		statusErr: &yunsuoRejectedError{Operation: "状态查询", Code: -1, Message: "content_pk 不存在"},
	}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	got, _ := env.store.byID(job.ID)
	if externalDistributionSummaryState(got) != publishExternalDistributionReconciliationRequired || fake.statusCalls != 2 {
		t.Fatalf("明确拒绝的状态查询应立即转人工核对：lanes=%+v calls=%d", got.ExternalDistributionLanes, fake.statusCalls)
	}
}

func TestExternalDistributionUncertainPushNeverRepeats(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	fake := &fakeYunsuoAPI{pushErr: &yunsuoUncertainError{Operation: "推送", Cause: errors.New("response lost")}}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }

	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	got, _ := env.store.byID(job.ID)
	if externalDistributionSummaryState(got) != publishExternalDistributionReconciliationRequired || fake.pushCalls != 2 {
		t.Fatalf("不确定结果必须进入人工核对：lanes=%+v push=%d", got.ExternalDistributionLanes, fake.pushCalls)
	}
	env.pipeline.recoverExternalDistribution(got)
	if fake.pushCalls != 2 {
		t.Fatalf("恢复扫描不得重放不确定 push，实际 %d 次", fake.pushCalls)
	}
}

func TestReviewCardOutboxesSerializeAndLatestLifecycleWins(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.ReviewCardMsgID = "om_review_card"
		current.ExternalDistributionLanes = []publishExternalLane{
			testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded),
			testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionSucceeded),
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

func TestReplaceExternalDistributionAfterReconcileRebuildsContentHTML(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	stale := func(id, name string) publishExternalLane {
		lane := testLane(id, name, publishExternalDistributionReconciliationRequired)
		lane.ContentPK = "old-wrong-field-pk"
		lane.PayloadJSON = `{"type":0,"html":"<p>wrong-field</p>"}`
		lane.Error = "云梭在轮询时限内未返回终态"
		return lane
	}
	job = setTestLanes(t, env, job.ID, stale(testYunsuoToutiaoID, "头条号"), stale(testYunsuoBaijiaID, "百家号"))
	card := cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(card, feishuCardActionDistributeExternal) {
		t.Fatalf("对账中的任务不得出现卡片安全重试按钮：%s", card)
	}
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err == nil {
		t.Fatal("卡片路径不得对已有 content_pk 的对账任务再推一次")
	}

	fake := &fakeYunsuoAPI{
		contentPK: "content-pk-replaced",
		statuses: []yunsuoAccountStatus{
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoToutiaoID, Name: "头条号"}},
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoBaijiaID, Name: "百家号"}},
		},
	}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	queued, err := env.pipeline.replaceExternalDistributionAfterReconcile(job.ID, publishExternalAdminRetryActor)
	if err != nil {
		t.Fatal(err)
	}
	if lane := mustLane(t, queued, testYunsuoToutiaoID); lane.State != publishExternalDistributionQueued {
		t.Fatalf("管理员重发应先落盘 queued 再异步推送，实际 %q", lane.State)
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{testYunsuoToutiaoID, testYunsuoBaijiaID} {
		if lane := mustLane(t, got, id); lane.State != publishExternalDistributionSucceeded || lane.ContentPK != "content-pk-replaced" {
			t.Fatalf("管理员重发应对账闭环：%+v", lane)
		}
	}
	if fake.pushCalls != 2 {
		t.Fatalf("对账中的两个平台各重推一次，实际 %d", fake.pushCalls)
	}
	encoded, err := json.Marshal(fake.payload)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["content_html"]; ok {
		t.Fatalf("重发必须使用官方 html 字段，不得再发 content_html：%s", encoded)
	}
	if strings.TrimSpace(fmt.Sprint(wire["html"])) == "" {
		t.Fatalf("重发缺少 html：%s", encoded)
	}
	if strings.Contains(fake.payload.HTML, "wrong-field") {
		t.Fatalf("不得把上一份错误 payload 再发出去：%s", fake.payload.HTML)
	}
}

func TestReplaceExternalDistributionRejectedWhenSucceeded(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	succeeded := testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded)
	succeeded.ContentPK = "already-published-pk"
	job = setTestLanes(t, env, job.ID, succeeded)
	fake := &fakeYunsuoAPI{contentPK: "must-not-be-used"}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	if _, err := env.pipeline.replaceExternalDistributionAfterReconcile(job.ID, publishExternalAdminRetryActor); err == nil || fake.pushCalls != 0 {
		t.Fatalf("已成功的外发不得管理员重发：err=%v push=%d", err, fake.pushCalls)
	}
}

func TestRetryExternalDistributionHTTPRequiresAdminAndRebuilds(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/jobs/1/retry-external-distribution", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无管理员令牌应 401，实际 %d", rec.Code)
	}
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	stale := testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionReconciliationRequired)
	stale.ContentPK = "old-wrong-field-pk"
	setTestLanes(t, env, job.ID, stale)
	fake := &fakeYunsuoAPI{
		contentPK: "content-pk-http-retry",
		statuses: []yunsuoAccountStatus{
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoToutiaoID, Name: "头条号"}},
			{Status: 1, AccountPool: yunsuoAccountTarget{ID: testYunsuoBaijiaID, Name: "百家号"}},
		},
	}
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	rec := doPublishReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/retry-external-distribution", job.ID), "", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"retried":true`) {
		t.Fatalf("对账中的已发布任务应可管理员重发，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lane := mustLane(t, got, testYunsuoToutiaoID); lane.State != publishExternalDistributionSucceeded ||
		lane.ContentPK != "content-pk-http-retry" || fake.pushCalls != 1 {
		t.Fatalf("HTTP 重发未闭环：%+v push=%d", lane, fake.pushCalls)
	}
	if _, ok := externalLaneFor(got, testYunsuoBaijiaID); ok {
		t.Fatalf("管理员对账重发只推对账中的平台，不得顺带推别的平台：%+v", got.ExternalDistributionLanes)
	}
}

// TestExternalCardExpiredIsTerminal 补发九月旧稿时，原审核卡已过飞书 14 天可更新期限（230031）：
// 不重试 10 次进死信告警，也不在群里补发新卡；记为 expired，状态照常在 lane 上。
func TestExternalCardExpiredIsTerminal(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.ReviewCardMsgID = "om_old_card"
		current.ExternalDistributionLanes = []publishExternalLane{testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded)}
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
