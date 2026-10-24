package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestExternalCardButtonPicksAccountFromJobTargets 卡片按钮带「平台账号」的编号：服务端只在这篇能发的号里认，
// 不在里面的（别的公众号的号、停用的号、伪造的编号）一律拒；2026-10-24 前按位置认号的旧卡按钮也拒——
// 位置会随表里加号、停号错开，点了可能推到别的平台。
func TestExternalCardButtonPicksAccountFromJobTargets(t *testing.T) {
	setTestYunsuoConfig(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	expo := testArticleAccount("recExpoToutiao", "toutiao", false)
	expo.Source = "FBIF食品创新展"
	setTestExternalAccounts(t, testArticleAccount(testYunsuoToutiaoID, "toutiao", false), testArticleAccount(testYunsuoBaijiaID, "baijiahao", false), expo)
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")

	click := func(value map[string]any) reviewCardOutcome {
		t.Helper()
		value["action"], value["job_id"] = feishuCardActionDistributeExternal, job.ID
		value["distribution_version"] = publishExternalDistributionCardVersion(mustJob(t, env, job.ID))
		raw, _ := json.Marshal(value)
		return env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", raw)
	}
	for name, value := range map[string]map[string]any{
		"旧卡按位置":   {"slot": 0},
		"别的公众号的号": {"account": "recExpoToutiao"},
		"伪造的编号":   {"account": "recNotInTable"},
	} {
		if out := click(value); out.toastType != "error" || !strings.Contains(out.toastContent, "请使用最新卡片") {
			t.Fatalf("%s的按钮应拒：%+v", name, out)
		}
	}
	if len(robot.saves) != 0 {
		t.Fatalf("被拒的按钮不得存稿：%v", robot.platforms)
	}
	if out := click(map[string]any{"account": testYunsuoBaijiaID}); out.toastType != "success" || out.toastContent != "已受理，正在存草稿至百家号" {
		t.Fatalf("这篇能发的号应受理：%+v", out)
	}
	if len(robot.saves) != 1 || robot.saves[0].Account != testYunsuoBaijiaID {
		t.Fatalf("只存这一个号：%+v", robot.saves)
	}
	if _, ok := externalLaneFor(mustJob(t, env, job.ID), testYunsuoToutiaoID); ok {
		t.Fatal("没点的平台不推")
	}
}

// TestExternalAccountAdoptionMovesYunsuoRecordsBeforeAccountsGoLive 2026-10-24 前的 lane、改稿记录挂着云梭 UUID：
// 启动时（任务恢复、worker 起来之前）改挂到这篇能发的同平台号上——只换键，intent、幂等键、状态原样，云梭回执
// 照样对得上，提醒签名跟着换算不算新的一轮；改完已推过的号不会再被挑上。
func TestExternalAccountAdoptionMovesYunsuoRecordsBeforeAccountsGoLive(t *testing.T) {
	setTestYunsuoConfig(t)
	setTestExternalAccounts(t)
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	const oldToutiao, oldBaijia = "906b1212-64a7-4910-8b65-bdcb5a9e1304", "281a3244-8858-48c4-a931-a31fc1d1003a"
	payload, _ := json.Marshal(robotDraftPayload{Platform: "baijiahao"})
	job, _ = env.store.update(job.ID, func(current *publishJob) {
		current.ExternalDistributionLanes = []publishExternalLane{
			{AccountID: oldBaijia, Name: "百家号", State: publishExternalDistributionDraftSaved, Driver: externalDriverRobot,
				PayloadJSON: string(payload), IntentKey: "job-1." + oldBaijia + ".abc", ContentPK: "draft-1"},
			{AccountID: oldToutiao, Name: "头条号", State: publishExternalDistributionReconciliationRequired, ContentPK: "yunsuo-9", IntentKey: "yunsuo-push:1:" + oldToutiao + ":x"},
		}
		current.ExternalDistributionDrafts = []publishExternalDraft{{
			AccountID: oldToutiao, Issues: []publishExternalIssue{{Field: publishExternalIssueTitle, Current: "长标题", Length: 40, Max: 30}},
		}}
		current.ExternalDistributionReminder = publishExternalReminder{Round: 1, Want: publishExternalReminderOpen, Seq: 3, MessageIDs: map[string]string{"oc_review": "om_1"}}
		current.ExternalDistributionReminder.Signature = externalReminderSignature(*current)
	})

	accounts := []externalAccount{testArticleAccount("recToutiao", "toutiao", false), testArticleAccount("recBaijia", "baijiahao", false)}
	d := &externalAccountDirectory{path: filepath.Join(t.TempDir(), externalAccountsFile), fetch: func(context.Context) ([]externalAccount, error) { return accounts, nil }}
	d.prime(env.pipeline.adoptExternalAccountIDs)
	got := mustJob(t, env, job.ID)
	baijia, toutiao := mustLane(t, got, "recBaijia"), mustLane(t, got, "recToutiao")
	if baijia.IntentKey != "job-1."+oldBaijia+".abc" || baijia.State != publishExternalDistributionDraftSaved || baijia.ContentPK != "draft-1" || baijia.YunsuoAccountID != "" {
		t.Fatalf("存草稿的 lane 只换键：%+v", baijia)
	}
	if toutiao.YunsuoAccountID != oldToutiao || toutiao.IntentKey != "yunsuo-push:1:"+oldToutiao+":x" {
		t.Fatalf("经云梭的 lane 留着云梭编号：%+v", toutiao)
	}
	if status, ok := externalLaneProviderStatus(toutiao, []yunsuoAccountStatus{
		{AccountPool: yunsuoAccountPool{ID: "33333333-3333-4333-8333-333333333333", Name: "别的号"}, Status: 2},
		{AccountPool: yunsuoAccountPool{ID: oldToutiao, Name: "头条号"}, Status: 1},
	}); !ok || status.Status != 1 {
		t.Fatalf("改挂后云梭回执按原编号对：%+v %v", status, ok)
	}
	if draft, ok := externalDraftFor(got, "recToutiao"); !ok || len(draft.Issues) != 1 {
		t.Fatalf("改稿记录跟着改挂：%+v", got.ExternalDistributionDrafts)
	}
	if r := got.ExternalDistributionReminder; r.Seq != 3 || r.Signature != externalReminderSignature(got) {
		t.Fatalf("提醒签名换算、不算新的一轮：%+v", r)
	}
	setTestExternalAccounts(t, accounts...)
	cfg, _ := loadExternalTargets()
	if chosen := externalDistributionTargetsFor(got, cfg, externalQueueCard, nil); len(chosen) != 0 {
		t.Fatalf("推过的号不再挑：%+v", chosen)
	}
	if err := env.pipeline.adoptExternalAccountIDs(accounts); err != nil || mustJob(t, env, job.ID).UpdatedAt != got.UpdatedAt {
		t.Fatalf("改挂过的不再写：%v", err)
	}
}

// TestExternalAccountAdoptionSkipsWhatItCannotPin 认不准就不改：同平台不止一个能发的号、这篇两个编号下都有记录、
// 两个云梭编号落到同一个号、部署候选暂停态（回滚回去的旧版本认不得新键）。没改挂的旧 lane 在，这篇在那个平台上
// 新建、重试都不走，免得给同一个号再存一篇；正在查的 lane 启动时照改（worker 还没起来），云梭编号留着对回执。
func TestExternalAccountAdoptionSkipsWhatItCannotPin(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	const oldA, oldB = "906b1212-64a7-4910-8b65-bdcb5a9e1304", "aaaaaaaa-64a7-4910-8b65-bdcb5a9e1304"
	seed := func(key string, lanes []publishExternalLane, drafts []publishExternalDraft) int64 {
		job := createPublishedDistributionJobWithKey(t, env, key, "FBIF食品饮料创新")
		env.store.update(job.ID, func(current *publishJob) {
			current.ExternalDistributionLanes, current.ExternalDistributionDrafts = lanes, drafts
		})
		return job.ID
	}
	toutiaoLane := func(id string) publishExternalLane {
		return publishExternalLane{AccountID: id, Name: "头条号", State: publishExternalDistributionSucceeded}
	}
	both := seed("site:adopt-both", []publishExternalLane{toutiaoLane(oldA)}, []publishExternalDraft{{AccountID: "recToutiao"}})
	twoOld := seed("site:adopt-two-old", []publishExternalLane{toutiaoLane(oldA), toutiaoLane(oldB)}, nil)
	single := seed("site:adopt-single", []publishExternalLane{toutiaoLane(oldA)}, nil)
	polling := toutiaoLane(oldA)
	polling.State = publishExternalDistributionPolling
	busy := seed("site:adopt-busy", []publishExternalLane{polling}, nil)
	retry := seed("site:adopt-retry", []publishExternalLane{toutiaoLane(oldA), {AccountID: "recToutiao", Name: "头条号", State: publishExternalDistributionFailed}}, nil)
	unchanged := func(id int64, want ...string) {
		t.Helper()
		var got []string
		for _, lane := range mustJob(t, env, id).ExternalDistributionLanes {
			got = append(got, lane.AccountID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("#%d 不该改：%v", id, got)
		}
	}

	t.Setenv(deploymentWritesPausedEnv, "1")
	one := []externalAccount{testArticleAccount("recToutiao", "toutiao", false)}
	if err := env.pipeline.adoptExternalAccountIDs(one); err != nil {
		t.Fatal(err)
	}
	unchanged(single, oldA)
	t.Setenv(deploymentWritesPausedEnv, "")

	second := testArticleAccount("recToutiao2", "toutiao", false)
	if err := env.pipeline.adoptExternalAccountIDs(append(one, second)); err != nil {
		t.Fatal(err)
	}
	unchanged(single, oldA)

	if err := env.pipeline.adoptExternalAccountIDs(one); err != nil {
		t.Fatal(err)
	}
	unchanged(both, oldA)
	unchanged(twoOld, oldA, oldB)
	unchanged(retry, oldA, "recToutiao")
	unchanged(single, "recToutiao")
	if lane := mustLane(t, mustJob(t, env, busy), "recToutiao"); lane.State != publishExternalDistributionPolling || lane.YunsuoAccountID != oldA {
		t.Fatalf("正在查的照改、留着云梭编号：%+v", lane)
	}
	cfg := externalTargetsFrom(one)
	for _, id := range []int64{both, twoOld, retry} {
		if chosen := externalDistributionTargetsFor(mustJob(t, env, id), cfg, externalQueueCard, nil); len(chosen) != 0 {
			t.Fatalf("#%d 头条号有没改挂的旧 lane，不得新建或重试：%+v", id, chosen)
		}
	}
	withBaijia := externalTargetsFrom(append(one, testArticleAccount("recBaijia", "baijiahao", false)))
	if chosen := externalDistributionTargetsFor(mustJob(t, env, twoOld), withBaijia, externalQueueCard, nil); len(chosen) != 1 || chosen[0].ID != "recBaijia" {
		t.Fatalf("别的平台照推：%+v", chosen)
	}
}
