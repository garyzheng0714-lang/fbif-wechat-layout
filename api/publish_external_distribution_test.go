package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	testYunsuoToutiaoID = "11111111-1111-4111-8111-111111111111"
	testYunsuoBaijiaID  = "22222222-2222-4222-8222-222222222222"
)

type fakeYunsuoAPI struct {
	pushCalls   int
	statusCalls int
	payload     yunsuoPushPayload
	contentPK   string
	pushErr     error
	statusErr   error
	statuses    []yunsuoAccountStatus
}

func (f *fakeYunsuoAPI) Push(_ context.Context, payload yunsuoPushPayload) (string, error) {
	f.pushCalls++
	f.payload = payload
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

func createPublishedDistributionJob(t *testing.T, env *publishTestEnv, sourceName string) publishJob {
	t.Helper()
	job, _, err := env.store.create("site:distribution-test", testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.Title = "一款中国食品品牌如何用产品创新打开新的消费市场"
		current.SourceName = sourceName
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
	return job
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
	if !strings.Contains(card, "同步分发至其他平台") || !strings.Contains(card, "头条号") || !strings.Contains(card, "百家号") {
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
	if got.ExternalDistributionState != publishExternalDistributionSucceeded ||
		got.ExternalDistributionContentPK != "content-pk-1" || fake.pushCalls != 1 || fake.statusCalls != 1 {
		t.Fatalf("原创分发未闭环：state=%q pk=%q push=%d status=%d job=%+v",
			got.ExternalDistributionState, got.ExternalDistributionContentPK, fake.pushCalls, fake.statusCalls, got)
	}
	if len(fake.payload.AccountIDs) != 2 || strings.Contains(fake.payload.HTML, "FoodTalks") {
		t.Fatalf("云梭应收到两个固定目标和渠道中性 HTML：%+v", fake.payload)
	}
	if repeated := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", value); repeated.toastType != "info" || fake.pushCalls != 1 {
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
	if strings.Contains(card, "同步分发至其他平台") || !strings.Contains(card, "转载文章仅发布至 FoodTalks") {
		t.Fatalf("转载卡必须隐藏按钮并解释规则：%s", card)
	}
	_, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job))
	if err == nil || !strings.Contains(err.Error(), "转载文章") || fake.pushCalls != 0 {
		t.Fatalf("服务端门禁也必须拒绝转载：err=%v push=%d", err, fake.pushCalls)
	}
}

func TestExternalDistributionFailsClosedOnExplicitAttributionAndMissingCheck(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.BlocksJSON = `[{"k":"p","text":"转载自：小食代"},{"k":"p","text":"正文"}]`
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	card := cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(card, "同步分发至其他平台") || !strings.Contains(card, "转载文章仅发布至 FoodTalks") {
		t.Fatalf("自有号正文存在外部出处时必须隐藏按钮：%s", card)
	}
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err == nil || !strings.Contains(err.Error(), "转载文章") {
		t.Fatalf("服务端必须拒绝显式外部出处：%v", err)
	}
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.BlocksJSON = `[{"k":"p","text":"本文经小食代授权转载"},{"k":"p","text":"正文"}]`
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	if card = cardJSON(t, buildReviewDoneCard(job)); strings.Contains(card, "同步分发至其他平台") {
		t.Fatalf("授权转载同样不得开放外部分发：%s", card)
	}

	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.BlocksJSON = `[{"k":"p","text":"正文"}]`
		current.AINotes = "AI 预填未执行（上游不可用）"
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	card = cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(card, "同步分发至其他平台") || !strings.Contains(card, "原创状态未完成核验") {
		t.Fatalf("出处核验未执行时必须 fail closed：%s", card)
	}
}

func TestExternalDistributionSafeFailureRendersRetryButton(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.ExternalDistributionState = publishExternalDistributionFailed
		current.ExternalDistributionTargets = []publishExternalDistributionTarget{
			{AccountID: testYunsuoToutiaoID, Name: "头条号", State: publishExternalTargetFailed},
			{AccountID: testYunsuoBaijiaID, Name: "百家号", State: publishExternalTargetFailed},
		}
		current.ExternalDistributionContentPK = ""
	})
	card := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(card, "重新同步分发") || !strings.Contains(card, "不会重复发布") {
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
	if got.ExternalDistributionState != publishExternalDistributionReconciliationRequired || fake.statusCalls != 1 {
		t.Fatalf("明确拒绝的状态查询应立即转人工核对：state=%q calls=%d", got.ExternalDistributionState, fake.statusCalls)
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
	if got.ExternalDistributionState != publishExternalDistributionReconciliationRequired || fake.pushCalls != 1 {
		t.Fatalf("不确定结果必须进入人工核对：state=%q push=%d", got.ExternalDistributionState, fake.pushCalls)
	}
	env.pipeline.recoverExternalDistribution(got)
	if fake.pushCalls != 1 {
		t.Fatalf("恢复扫描不得重放不确定 push，实际 %d 次", fake.pushCalls)
	}
}

func TestReviewCardOutboxesSerializeAndLatestLifecycleWins(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.ReviewCardMsgID = "om_review_card"
		current.ExternalDistributionState = publishExternalDistributionSucceeded
		current.ExternalDistributionTargets = []publishExternalDistributionTarget{
			{AccountID: testYunsuoToutiaoID, Name: "头条号", State: publishExternalTargetSucceeded},
			{AccountID: testYunsuoBaijiaID, Name: "百家号", State: publishExternalTargetSucceeded},
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
	if len(cards) != 2 || !strings.Contains(cards[1], "已下线") || strings.Contains(cards[1], "同步分发至其他平台") {
		t.Fatalf("最后写入的卡必须反映最新 offline 生命周期：%v", cards)
	}
}
