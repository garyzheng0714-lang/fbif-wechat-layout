package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func touchRegularFile(path string) error {
	return os.WriteFile(path, []byte("block mkdir"), 0600)
}

// directFoodTalksStub 不走 HTTP，用于精确模拟 CreateNews 已被对端执行但响应/本地落盘失败。
type directFoodTalksStub struct {
	create func() (int64, error)
	update func() error
}

func (s directFoodTalksStub) CreateNews(context.Context, foodTalksNews) (int64, error) {
	return s.create()
}

func (s directFoodTalksStub) UpdateNews(context.Context, foodTalksNews) error {
	if s.update != nil {
		return s.update()
	}
	return nil
}

// TestPublishReviewCASConcurrentApprovals 两个审核请求必须只有一个能从 awaiting_review
// 抢到 publicizing；旧实现的「先读再 update」会让两边都成功并发出两次不可逆 PUT。
func TestPublishReviewCASConcurrentApprovals(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()

	var arrived atomic.Int32
	release := make(chan struct{})
	env.pipeline.now = func() time.Time {
		if arrived.Add(1) == 2 {
			close(release)
		}
		<-release
		return time.Unix(1_700_000_000, 0)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "admin")
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	successes := 0
	for err := range errCh {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("并发 approve 只能一个成功，实际 %d", successes)
	}
	if _, updateN := env.ft.counts(); updateN != 1 {
		t.Fatalf("并发 approve 只能公开一次，实际 UpdateNews=%d", updateN)
	}
}

// TestPublishRetryCASConcurrent 双 retry 必须只让一个调用者认领 failed 任务。
func TestPublishRetryCASConcurrent(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.store.create("site:retry-cas", testPublishSourceURL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := env.store.setFailed(job.ID, publishStageFetching, "boom"); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	// 阻断 worker，避免首个 retry 很快跑完后干扰「认领」断言。
	env.pipeline.spawn = func(func()) {}
	const n = 32
	start := make(chan struct{})
	errCh := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := env.pipeline.retry(job.ID)
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	successes := 0
	for err := range errCh {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("并发 retry 只能一个认领成功，实际 %d", successes)
	}
}

// TestPublishCreateResponseLossNeedsReconciliation CreateNews 返回传输错误时不能假定
// 对端没创建；必须进入人工 reconciliation，retry/runJob 都不得再次 POST。
func TestPublishCreateResponseLossNeedsReconciliation(t *testing.T) {
	env := newPublishTestEnv(t)
	var creates atomic.Int32
	env.pipeline.foodTalks = func() (foodTalksAPI, error) {
		return directFoodTalksStub{create: func() (int64, error) {
			creates.Add(1)
			return 0, errors.New("response lost after server accepted request")
		}}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageReconciliationRequired || got.StagingIntentKey == "" {
		t.Fatalf("响应丢失后应进入 reconciliation 且保留 intent，实际 stage=%q intent=%q error=%q", got.Stage, got.StagingIntentKey, got.Error)
	}
	if _, err := env.pipeline.retry(job.ID); err == nil {
		t.Fatalf("不确定态不得 retry CreateNews")
	}
	env.pipeline.runJob(job.ID)
	if creates.Load() != 1 {
		t.Fatalf("不确定态不得二次 CreateNews，实际 %d", creates.Load())
	}
}

// TestPublishCreateSucceededButAnchorWriteFailed 模拟网站已返回 news id，随后本地锚点
// 落盘失败。磁盘上必须已先有 staging intent；恢复后转人工核对，绝不能再次 CreateNews。
func TestPublishCreateSucceededButAnchorWriteFailed(t *testing.T) {
	env := newPublishTestEnv(t)
	originalPath := env.store.path
	badPath := filepath.Join(t.TempDir(), "not-a-directory", "publish-jobs.json")
	if err := touchRegularFile(filepath.Dir(badPath)); err != nil {
		t.Fatalf("准备坏路径: %v", err)
	}
	var creates atomic.Int32
	env.pipeline.foodTalks = func() (foodTalksAPI, error) {
		return directFoodTalksStub{create: func() (int64, error) {
			creates.Add(1)
			// intent 已落盘后才破坏后续写路径，精确命中「外部成功、本地锚点失败」窗口。
			env.store.path = badPath
			return 9911, nil
		}}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.store.path = originalPath
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatalf("read durable intent: %v", err)
	}
	if got.Stage != publishStageStagingCreating || got.StagingIntentKey == "" {
		t.Fatalf("锚点落盘失败后磁盘应保留 creating intent，实际 stage=%q intent=%q", got.Stage, got.StagingIntentKey)
	}
	env.pipeline.recoverJobs()
	got, _ = env.store.byID(job.ID)
	if got.Stage != publishStageReconciliationRequired {
		t.Fatalf("重启扫描应把 creating 转人工核对，实际 %q", got.Stage)
	}
	if creates.Load() != 1 {
		t.Fatalf("恢复不得二次 CreateNews，实际 %d", creates.Load())
	}
}

// TestPublishStartupRecoverySafeStages 启动只恢复可安全重放的自动阶段；每个阶段都应
// 继续到 awaiting_review，且仍只 CreateNews 一次、绝不自动公开。
func TestPublishStartupRecoverySafeStages(t *testing.T) {
	for _, stage := range []string{
		publishStageCreated,
		publishStageFetching,
		publishStageRendering,
		publishStageMirroring,
		publishStageEnriching,
		publishStageStaging,
	} {
		t.Run(stage, func(t *testing.T) {
			env := newPublishTestEnv(t)
			job, _, err := env.store.create("site:recover:"+stage, testPublishSourceURL)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			_, err = env.store.update(job.ID, func(j *publishJob) {
				j.Stage = stage
				j.Title = "恢复测试"
				j.SourceName = "测试公众号"
				j.BlocksJSON = testPublishBlocksJSON
				j.ContentHTML = testPublishRenderedHTML
				j.CoverURL = "https://mmbiz.qpic.cn/a.jpg"
				if stage == publishStageEnriching || stage == publishStageStaging {
					j.ContentHTML = `<p>正文</p><img src="https://oss.example.com/a.jpg">`
					j.CoverURL = "https://oss.example.com/a.jpg"
				}
			})
			if err != nil {
				t.Fatalf("seed stage: %v", err)
			}
			env.pipeline.recoverJobs()
			got, err := env.store.byID(job.ID)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.Stage != publishStageAwaitingReview {
				t.Fatalf("%s 恢复后应 awaiting_review，实际 stage=%q error=%q", stage, got.Stage, got.Error)
			}
			if createN, updateN := env.ft.counts(); createN != 1 || updateN != 0 {
				t.Fatalf("%s 恢复应 CreateNews=1/UpdateNews=0，实际 %d/%d", stage, createN, updateN)
			}
		})
	}
}

// TestPublishStartupRecoveryPublicizingRequiresApprovalIntent publicizing 只有同时持久化
// approve 动作与 reviewed_at 才能恢复 PUT；缺任一授权凭据必须原地冻结并写明错误。
func TestPublishStartupRecoveryPublicizingRequiresApprovalIntent(t *testing.T) {
	t.Run("有 approve intent 可恢复", func(t *testing.T) {
		env := newPublishTestEnv(t)
		job, _, err := env.store.create("site:recover-publicizing-ok", testPublishSourceURL)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		_, err = env.store.update(job.ID, func(j *publishJob) {
			j.Stage = publishStagePublicizing
			j.Title = "已审核"
			j.SourceName = "测试公众号"
			j.ContentHTML = "<p>正文</p>"
			j.CoverURL = "https://oss.example.com/a.jpg"
			j.FoodTalksNewsID = 7240
			j.ReviewAction = publishReviewApprove
			j.ReviewedAt = "2026-07-10T00:00:00Z"
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		env.ft.mu.Lock()
		env.ft.forbidPublish = false
		env.ft.mu.Unlock()
		env.pipeline.recoverJobs()
		got, _ := env.store.byID(job.ID)
		if got.Stage != publishStagePublished {
			t.Fatalf("合法 approve intent 应恢复 published，实际 %q error=%q", got.Stage, got.Error)
		}
		if _, updateN := env.ft.counts(); updateN != 1 {
			t.Fatalf("应恰一次恢复公开 PUT，实际 %d", updateN)
		}
	})

	t.Run("缺 approve intent 不恢复", func(t *testing.T) {
		env := newPublishTestEnv(t)
		job, _, err := env.store.create("site:recover-publicizing-deny", testPublishSourceURL)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		_, err = env.store.update(job.ID, func(j *publishJob) {
			j.Stage = publishStagePublicizing
			j.Title = "未授权"
			j.ContentHTML = "<p>正文</p>"
			j.CoverURL = "https://oss.example.com/a.jpg"
			j.FoodTalksNewsID = 7240
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		env.pipeline.recoverJobs()
		got, _ := env.store.byID(job.ID)
		if got.Stage != publishStagePublicizing || got.Error == "" {
			t.Fatalf("无授权应冻结 publicizing 并写错误，实际 stage=%q error=%q", got.Stage, got.Error)
		}
		if _, updateN := env.ft.counts(); updateN != 0 {
			t.Fatalf("无授权不得公开，实际 PUT=%d", updateN)
		}
	})
}

// TestPublishReconcileStaging 人工对账必须可闭环：找到已建草稿就绑定 ID 进入待审；
// 明确确认未创建才允许清除旧 intent 后重新 Create。
func TestPublishReconcileStaging(t *testing.T) {
	t.Run("绑定已存在草稿", func(t *testing.T) {
		env := newPublishTestEnv(t)
		job, _, _ := env.store.create("site:reconcile-existing", testPublishSourceURL)
		_, _ = env.store.update(job.ID, func(j *publishJob) {
			j.Stage = publishStageReconciliationRequired
			j.StagingIntentKey = "foodtalks-create:job-1"
			j.Title = "人工找到的草稿"
		})
		got, err := env.pipeline.reconcileStaging(job.ID, 9911, false)
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if got.Stage != publishStageAwaitingReview || got.FoodTalksNewsID != 9911 {
			t.Fatalf("绑定后应待审 + id，实际 stage=%q id=%d", got.Stage, got.FoodTalksNewsID)
		}
		if createN, _ := env.ft.counts(); createN != 0 {
			t.Fatalf("绑定已有草稿不得 CreateNews，实际 %d", createN)
		}
	})

	t.Run("人工确认未创建后重试", func(t *testing.T) {
		env := newPublishTestEnv(t)
		job, _, _ := env.store.create("site:reconcile-absent", testPublishSourceURL)
		_, _ = env.store.update(job.ID, func(j *publishJob) {
			j.Stage = publishStageReconciliationRequired
			j.StagingIntentKey = "foodtalks-create:job-1"
			j.Title = "确认不存在"
			j.SourceName = "测试公众号"
			j.ContentHTML = "<p>正文</p>"
			j.CoverURL = "https://oss.example.com/a.jpg"
		})
		got, err := env.pipeline.reconcileStaging(job.ID, 0, true)
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if got.Stage != publishStageStaging {
			t.Fatalf("返回快照应是已认领的 staging，实际 %q", got.Stage)
		}
		latest, _ := env.store.byID(job.ID)
		if latest.Stage != publishStageAwaitingReview || latest.FoodTalksNewsID == 0 {
			t.Fatalf("确认不存在后应重新创建并待审，实际 stage=%q id=%d error=%q", latest.Stage, latest.FoodTalksNewsID, latest.Error)
		}
		if createN, _ := env.ft.counts(); createN != 1 {
			t.Fatalf("人工确认后应恰一次 CreateNews，实际 %d", createN)
		}
	})
}
