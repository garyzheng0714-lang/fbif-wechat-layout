package main

import (
	"context"
	"strings"
	"testing"
)

// TestPublishStagingTargetMismatchMatrix 环境守卫判定矩阵：无锚点不拦；同环境放行；
// 跨环境必拦；空环境记录（存量任务）只在正式环境 fail closed。
func TestPublishStagingTargetMismatchMatrix(t *testing.T) {
	for name, tc := range map[string]struct {
		base    string
		job     publishJob
		blocked bool
	}{
		"无锚点任务不拦": {
			base: "https://api-we.foodtalks.cn", job: publishJob{ID: 1}, blocked: false,
		},
		"同为正式环境放行": {
			base: "https://api-we.foodtalks.cn",
			job:  publishJob{ID: 1, FoodTalksNewsID: 9, StagingTarget: "production"}, blocked: false,
		},
		"测试草稿到正式环境必拦": {
			base: "https://api-we.foodtalks.cn",
			job:  publishJob{ID: 1, FoodTalksNewsID: 9, StagingTarget: "test"}, blocked: true,
		},
		"存量空记录在正式环境必拦": {
			base: "https://api-we.foodtalks.cn",
			job:  publishJob{ID: 1, FoodTalksNewsID: 9}, blocked: true,
		},
		"存量空记录在测试环境放行": {
			base: "https://api-we-test.foodtalks.cn",
			job:  publishJob{ID: 1, FoodTalksNewsID: 9}, blocked: false,
		},
		"正式草稿回到测试环境同样拦": {
			base: "https://api-we-test.foodtalks.cn",
			job:  publishJob{ID: 1, FoodTalksNewsID: 9, StagingTarget: "production"}, blocked: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("FOODTALKS_BASE_URL", tc.base)
			err := publishStagingTargetMismatch(tc.job)
			if tc.blocked && (err == nil || !strings.Contains(err.Error(), "环境守卫")) {
				t.Fatalf("应被环境守卫拦下，实际 err=%v", err)
			}
			if !tc.blocked && err != nil {
				t.Fatalf("不应被拦，实际 err=%v", err)
			}
		})
	}
}

// TestPublishStagingTargetRecordedAtStaging 草稿进站时必须记录建站环境，
// 之后的守卫判定才有依据。
func TestPublishStagingTargetRecordedAtStaging(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("stage = %q，应为 awaiting_review", got.Stage)
	}
	// httptest 桩的 127.0.0.1 地址按 readiness 口径归类为 custom。
	if got.StagingTarget != "custom" {
		t.Fatalf("staging_target = %q，进站时应记录当前环境", got.StagingTarget)
	}
}

// TestPublishApproveBlockedByTargetGuard 切换环境后，旧环境暂存的任务不能被
// approve 发到新环境：审核报环境守卫错误、任务停在 awaiting_review、外部零调用。
// 这是 2026-08-06 切正式端的核心保护：66 篇测试站暂存任务的卡片在群里仍可点，
// 点了必须被拦住而不是把测试站的资讯 id 发到正式站。
func TestPublishApproveBlockedByTargetGuard(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 模拟：任务在测试环境暂存，随后服务切到正式环境。
	if _, _, err := env.store.updateIf(job.ID, func(publishJob) bool { return true }, func(j *publishJob) {
		j.StagingTarget = "test"
	}); err != nil {
		t.Fatalf("回写 staging_target: %v", err)
	}
	t.Setenv("FOODTALKS_BASE_URL", "https://api-we.foodtalks.cn")
	env.ft.mu.Lock()
	env.ft.forbidPublish = false // 若守卫失效，桩不替本测试兜底
	env.ft.mu.Unlock()
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, ""); err == nil ||
		!strings.Contains(err.Error(), "环境守卫") {
		t.Fatalf("跨环境 approve 应报环境守卫错误，实际 err=%v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("被拦后任务应停在 awaiting_review，实际 %q", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("被拦后不得有任何 UpdateNews 外调，实际 %d 次", updateN)
	}
}

// TestPublishStagingUpdateBlockedByTargetGuard 重排/续跑走 UpdateNews 更新既有
// 草稿的路径同样受守卫保护：跨环境时 stageStage 直接失败，不发外部请求。
func TestPublishStagingUpdateBlockedByTargetGuard(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, _, err := env.store.updateIf(job.ID, func(publishJob) bool { return true }, func(j *publishJob) {
		j.Stage = publishStageStaging
		j.StagingTarget = "test"
	}); err != nil {
		t.Fatalf("回写任务状态: %v", err)
	}
	t.Setenv("FOODTALKS_BASE_URL", "https://api-we.foodtalks.cn")
	_, beforeUpdates := env.ft.counts()
	current, _ := env.store.byID(job.ID)
	if err := env.pipeline.stageStage(context.Background(), current); err == nil ||
		!strings.Contains(err.Error(), "环境守卫") {
		t.Fatalf("跨环境草稿更新应报环境守卫错误，实际 err=%v", err)
	}
	if _, afterUpdates := env.ft.counts(); afterUpdates != beforeUpdates {
		t.Fatalf("被拦后不得发出 UpdateNews，实际新增 %d 次", afterUpdates-beforeUpdates)
	}
}
