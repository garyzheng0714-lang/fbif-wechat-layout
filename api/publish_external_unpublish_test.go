package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestUnpublishExternalLaneMarksCardOffline 2026-11-03 #340 读者投稿被误判原创、自动发到头条号和百家号，用户：「下，
// 然后卡片也要改」。管理员接口经分发服务按这条 lane 的键下线，做成了 lane 记「已下线」：卡片那一行写「已下线」和原因、
// 不给文章链接和重新同步，自动外发、管理员同步都不再推；再调一次不再找分发服务。
func TestUnpublishExternalLaneMarksCardOffline(t *testing.T) {
	setTestRobotOnlyConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	published := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	if published.State != publishExternalDistributionSucceeded {
		t.Fatalf("先发出去：%+v", published)
	}

	mux := newPublishTestMux(t, env)
	target := fmt.Sprintf("/api/publish/jobs/%d/external-distribution/unpublish", job.ID)
	body := func(extra string) string {
		return `{"account_id":"` + testBaijiaAccountID + `","reason":"读者投稿，误当原创发出，按要求下线"` + extra + `}`
	}
	if rec := doPublishReq(mux, http.MethodPost, target, `{"account_id":"`+testBaijiaAccountID+`"}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("不写原因不受理：%d %s", rec.Code, rec.Body.String())
	}
	if rec := doPublishReq(mux, http.MethodPost, target, body(`,"dry_run":true`), true); rec.Code != http.StatusOK || len(robot.unpublishes) != 0 {
		t.Fatalf("dry_run 只核对、不找分发服务：%d %s %v", rec.Code, rec.Body.String(), robot.unpublishes)
	}

	pending := robot.records[published.IntentKey]
	pending.Action = &robotDraftAction{ID: "unpub-1", Kind: robotActionUnpublish, Status: robotActionDoing}
	robot.unpublishRecord = &pending
	if rec := doPublishReq(mux, http.MethodPost, target, body(""), true); rec.Code != http.StatusAccepted ||
		mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID).State != publishExternalDistributionSucceeded {
		t.Fatalf("分发服务还在做：回 202、lane 不动：%d %s", rec.Code, rec.Body.String())
	}

	robot.unpublishRecord = nil
	rec := doPublishReq(mux, http.MethodPost, target, body(""), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("下线应做成：%d %s", rec.Code, rec.Body.String())
	}
	if want := "baijiahao/" + published.IntentKey + "/" + publishExternalAdminUnpublishActor; robot.unpublishes[len(robot.unpublishes)-1] != want {
		t.Fatalf("按这条 lane 的键找分发服务下线：%v", robot.unpublishes)
	}
	var out struct {
		Lane struct{ State string } `json:"lane"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Lane.State != publishExternalDistributionUnpublished {
		t.Fatalf("回的 lane 是已下线：%s", rec.Body.String())
	}

	got := mustJob(t, env, job.ID)
	lane := mustLane(t, got, testBaijiaAccountID)
	if lane.State != publishExternalDistributionUnpublished || lane.Error != "读者投稿，误当原创发出，按要求下线" || lane.URL != published.URL {
		t.Fatalf("lane 记已下线和原因，原链接留着备查：%+v", lane)
	}
	state := externalPlatformStatus(got, testArticleAccount(testBaijiaAccountID, "baijiahao", false))
	if state.Status != "已下线" || state.URL != "" || state.Syncable || state.ManageURL == "" {
		t.Fatalf("卡片：已下线、不给文章链接和同步按钮、给平台后台：%+v", state)
	}
	cardJSON, _ := json.Marshal(reviewCardExternalDistributionElements(got, true))
	if card := string(cardJSON); !strings.Contains(card, "已下线") || !strings.Contains(card, "按要求下线") || strings.Contains(card, published.URL) ||
		strings.Contains(card, feishuCardActionDistributeExternal) {
		t.Fatalf("卡片那一行写已下线和原因，没有文章链接和按钮：%s", card)
	}
	if externalDistributionSummaryState(got) != publishExternalDistributionSucceeded {
		t.Fatalf("已下线不算失败：%s", externalDistributionSummaryState(got))
	}

	if rec := doPublishReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/external-distribution/sync", job.ID), `{"account_id":"`+testBaijiaAccountID+`"}`, true); rec.Code != http.StatusConflict {
		t.Fatalf("已下线的不再同步：%d %s", rec.Code, rec.Body.String())
	}
	cfg, err := loadExternalTargets()
	if err != nil {
		t.Fatal(err)
	}
	if targets := externalDistributionTargetsFor(got, cfg, externalQueueAuto, nil); len(targets) != 0 {
		t.Fatalf("自动外发不再推：%+v", targets)
	}
	calls := len(robot.unpublishes)
	if lane, err := env.pipeline.unpublishExternalAccount(context.Background(), job.ID, testBaijiaAccountID, "再下一次", publishExternalAdminUnpublishActor, false); err != nil ||
		lane.State != publishExternalDistributionUnpublished || len(robot.unpublishes) != calls {
		t.Fatalf("已下线的再调不找分发服务：%v %+v %v", err, lane, robot.unpublishes)
	}
}

// 分发服务说做成了、平台上却还公开：不记已下线。
func TestUnpublishExternalLaneNeedsPlatformOffline(t *testing.T) {
	setTestRobotOnlyConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	lane := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID)
	still := robot.records[lane.IntentKey]
	still.Action = &robotDraftAction{ID: "unpub-1", Kind: robotActionUnpublish, Status: robotActionDone, Outcome: "by_us"}
	robot.unpublishRecord = &still
	if _, err := env.pipeline.unpublishExternalAccount(context.Background(), job.ID, testBaijiaAccountID, "误发", publishExternalAdminUnpublishActor, false); err == nil ||
		!strings.Contains(err.Error(), "现在是「published」") {
		t.Fatalf("平台上还公开要报出来：%v", err)
	}
	if got := mustLane(t, mustJob(t, env, job.ID), testBaijiaAccountID); got.State != publishExternalDistributionSucceeded {
		t.Fatalf("lane 不动：%+v", got)
	}
}
