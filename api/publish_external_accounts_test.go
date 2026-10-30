package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestExternalCardButtonPicksAccountFromJobTargets 卡片按钮带「平台账号」的编号：服务端只在这篇能发的号里认，
// 不在里面的（别的公众号的号、停用的号、伪造的编号）一律拒；2026-10-24 前按位置认号的旧卡按钮也拒——
// 位置会随表里加号、停号错开，点了可能推到别的平台。
func TestExternalCardButtonPicksAccountFromJobTargets(t *testing.T) {
	setTestDistributionAccounts(t)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	expo := testArticleAccount("recExpoToutiao", "toutiao", false)
	expo.Source = "FBIF食品创新展"
	setTestExternalAccounts(t, testArticleAccount(testToutiaoAccountID, "toutiao", false), testArticleAccount(testBaijiaAccountID, "baijiahao", false), expo)
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
	if out := click(map[string]any{"account": testBaijiaAccountID}); out.toastType != "success" || out.toastContent != "已受理，正在同步至百家号" {
		t.Fatalf("这篇能发的号应受理：%+v", out)
	}
	if len(robot.saves) != 1 || robot.saves[0].Account != testBaijiaAccountID {
		t.Fatalf("只存这一个号：%+v", robot.saves)
	}
	if _, ok := externalLaneFor(mustJob(t, env, job.ID), testToutiaoAccountID); ok {
		t.Fatal("没点的平台不推")
	}
}
