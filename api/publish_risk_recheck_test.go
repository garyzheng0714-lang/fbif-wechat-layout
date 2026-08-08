package main

// publish_risk_recheck_test.go — 「用当前 prompt 重跑风险检查」的边界守护。
// 这个入口会真调模型并覆盖已有结论，边界错了就会把旧正文的结论盖到新内容上、
// 或者让已审批的任务凭空换掉审批依据。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRiskRecheckOnlyRunsForAwaitingReview(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{Level: publishRiskPass}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, stage := range []string{
		publishStagePublished, publishStageRejected, publishStageStaging, publishStageFailed,
	} {
		if _, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = stage }); err != nil {
			t.Fatalf("set stage: %v", err)
		}
		if _, err := env.pipeline.recheckPublishRisk(context.Background(), job.ID); err == nil ||
			!strings.Contains(err.Error(), "只有待审核任务") {
			t.Fatalf("阶段 %s 不该允许重跑，实际 err=%v", stage, err)
		}
	}
}

// 重跑期间任务被审批：结论必须作废，不能盖到已授权的快照上——否则审批依据被
// 悄悄换掉，approve 时校验的风险版本与人当初看到的不是同一份。
func TestRiskRecheckAbortsWhenJobGetsApprovedMidway(t *testing.T) {
	env := newPublishTestEnv(t)
	// submit 自身会跑一次风险检查，先用无害桩，避免提交阶段就写进结论。
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{Level: publishRiskPass}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		// 模型返回期间模拟有人点了通过。
		if _, err := env.store.update(job.ID, func(j *publishJob) {
			j.ReviewAction = publishReviewApprove
		}); err != nil {
			t.Fatalf("mid-flight approve: %v", err)
		}
		return publishRiskAssessment{
			Level:    publishRiskWarning,
			Headline: "重跑发现了新问题",
			Items: []publishRiskItem{{
				Category: publishRiskCategoryFactual, Level: publishRiskWarning,
				Certainty: publishRiskCertaintyConfirmed, Evidence: "e", Reason: "r",
			}},
		}, nil
	}
	if _, err := env.pipeline.recheckPublishRisk(context.Background(), job.ID); err == nil ||
		!strings.Contains(err.Error(), "已被审核") {
		t.Fatalf("重跑期间被审批应中止写入，实际 err=%v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.RiskHeadline == "重跑发现了新问题" {
		t.Fatal("已审批任务的风险结论不得被重跑结果覆盖")
	}
}

// 重跑失败绝不能覆盖已有结论（2026-08-08 首次真机重跑踩到）：
// 模型调用被取消时 fallback 会产出一条 system「检查未完成」，写进去等于用一次
// 失败的重试把这篇文章原本已经查出来的问题全部抹掉——比不重跑糟得多。
func TestRiskRecheckFailureKeepsExistingAssessment(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{
			Level:    publishRiskWarning,
			Headline: "原有结论：正文把北京冬奥会写成 2018 年。",
			Items: []publishRiskItem{{
				Category: publishRiskCategoryFactual, Level: publishRiskWarning,
				Certainty: publishRiskCertaintyConfirmed,
				Evidence:  "在2018年的北京冬奥会中", Reason: "实际举办时间为2022年",
			}},
		}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	before, _ := env.store.byID(job.ID)
	if len(before.RiskItems) != 1 {
		t.Fatalf("前置条件：应先有一条结论，实际 %d", len(before.RiskItems))
	}

	// 重跑时模型不可达 → assessPublishRiskWithFallback 产出 system 条目。
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{}, errors.New("风险审核接口不可达: context canceled")
	}
	if _, err := env.pipeline.recheckPublishRisk(context.Background(), job.ID); err == nil ||
		!strings.Contains(err.Error(), "已保留原有结论") {
		t.Fatalf("重跑失败应报错并保留原结论，实际 err=%v", err)
	}
	after, _ := env.store.byID(job.ID)
	if len(after.RiskItems) != 1 || after.RiskItems[0].Category != publishRiskCategoryFactual {
		t.Fatalf("原有结论被失败的重跑抹掉了：%+v", after.RiskItems)
	}
	if after.RiskHeadline != before.RiskHeadline {
		t.Fatalf("原有 headline 被覆盖：%q", after.RiskHeadline)
	}
	if publishRiskHasSystemFailure(after.RiskItems) {
		t.Fatal("失败的重跑不得把任务打成「检查未完成」")
	}
}

// 正常路径：重跑覆盖 Risk* 字段，但绝不碰正文与阶段。
func TestRiskRecheckReplacesRiskWithoutTouchingContent(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{Level: publishRiskPass}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	before, _ := env.store.byID(job.ID)

	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{
			Level:    publishRiskWarning,
			Headline: "正文把北京冬奥会写成 2018 年举办，实际是 2022 年。",
			Items: []publishRiskItem{{
				Category: publishRiskCategoryFactual, Level: publishRiskWarning,
				Certainty: publishRiskCertaintyConfirmed,
				Evidence:  "在2018年的北京冬奥会中也成为了指定食品供应品牌之一",
				Reason:    "北京冬奥会实际举办时间为2022年",
			}},
		}, nil
	}
	after, err := env.pipeline.recheckPublishRisk(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	if after.RiskLevel != publishRiskWarning || !strings.Contains(after.RiskHeadline, "2022") {
		t.Fatalf("重跑结论未写入：level=%q headline=%q", after.RiskLevel, after.RiskHeadline)
	}
	if confirmed, _ := splitPublishRiskItems(after.RiskItems); len(confirmed) != 1 {
		t.Fatalf("重跑后已证实的事实错误应上首屏，实际 %d 条", len(confirmed))
	}
	if after.Stage != publishStageAwaitingReview {
		t.Fatalf("重跑不得改变阶段，实际 %q", after.Stage)
	}
	if after.ContentHTML != before.ContentHTML || after.ContentVersion != before.ContentVersion {
		t.Fatal("重跑不得触碰正文")
	}
}
