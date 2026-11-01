package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type capturedStageRetry struct {
	delay time.Duration
	run   func()
}

func captureStageRetries(env *publishTestEnv) *[]capturedStageRetry {
	var scheduled []capturedStageRetry
	env.pipeline.retryStageLater = func(delay time.Duration, f func()) {
		scheduled = append(scheduled, capturedStageRetry{delay, f})
	}
	return &scheduled
}

// TestPublishMirrorFailureRetriesLaterThenSucceeds #296：微信图这一下没取到，搬图不一次判死，
// 任务留在 mirroring、隔一会儿自己再试，好了就照常往下走。
func TestPublishMirrorFailureRetriesLaterThenSucceeds(t *testing.T) {
	env := newPublishTestEnv(t)
	scheduled := captureStageRetries(env)
	env.ossResults = []map[string]string{
		{"https://mmbiz.qpic.cn/a.jpg": ossURLFor("https://mmbiz.qpic.cn/a.jpg")},
		{},
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageMirroring || got.MirrorAttempts != 1 {
		t.Fatalf("搬图失败应留在 mirroring 等自动重试，实际 stage=%q attempts=%d error=%q", got.Stage, got.MirrorAttempts, got.Error)
	}
	if !strings.Contains(got.Error, "1 分钟后自动再试（第 1/5 次）") || !strings.Contains(got.Error, "b.jpg") {
		t.Fatalf("等重试时应写明多久后再试与哪张图没搬成：%q", got.Error)
	}
	if got.FailedBefore {
		t.Fatalf("自动重试中不算落过 failed")
	}
	if len(*scheduled) != 1 || (*scheduled)[0].delay != publishMirrorRetryDelays[0] {
		t.Fatalf("应排一次 %s 后的重试，实际 %+v", publishMirrorRetryDelays[0], *scheduled)
	}

	(*scheduled)[0].run()
	got, _ = env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("重试搬图成功后应照常进待审核，实际 %q error=%q", got.Stage, got.Error)
	}
	if got.MirrorAttempts != 0 || got.Error != "" {
		t.Fatalf("搬图成功后应清掉重试计数与原因，实际 attempts=%d error=%q", got.MirrorAttempts, got.Error)
	}
	if !got.AutoPublishEligible {
		t.Fatalf("自动重试搬图成功的新稿仍是新稿，可以自动发布")
	}
}

// TestPublishMirrorFailureGivesUpAfterRetries 五次都不行才落 failed，原因写明已自动重试过。
func TestPublishMirrorFailureGivesUpAfterRetries(t *testing.T) {
	env := newPublishTestEnv(t)
	scheduled := captureStageRetries(env)
	env.pipeline.ossUpload = func(ctx context.Context, urls []string) (map[string]string, error) {
		return map[string]string{}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	for i := 0; i < len(publishMirrorRetryDelays); i++ {
		if len(*scheduled) != i+1 {
			t.Fatalf("第 %d 次失败后应排第 %d 次重试，实际排了 %d 次", i+1, i+1, len(*scheduled))
		}
		if (*scheduled)[i].delay != publishMirrorRetryDelays[i] {
			t.Fatalf("第 %d 次重试间隔 %s，应为 %s", i+1, (*scheduled)[i].delay, publishMirrorRetryDelays[i])
		}
		(*scheduled)[i].run()
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageFailed || got.FailedStage != publishStageMirroring {
		t.Fatalf("重试用完应落 failed(mirroring)，实际 stage=%q failed=%q", got.Stage, got.FailedStage)
	}
	if !strings.Contains(got.Error, "已自动重试 5 次仍没成功") {
		t.Fatalf("失败原因应写明已自动重试过：%q", got.Error)
	}
	if !got.FailedBefore {
		t.Fatalf("落 failed 应记 failed_before")
	}
	if len(*scheduled) != len(publishMirrorRetryDelays) {
		t.Fatalf("用完后不应再排重试，实际 %d", len(*scheduled))
	}

	// 人工重试清零，再失败又能自动重试一轮。
	if _, err := env.pipeline.retry(job.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, _ = env.store.byID(job.ID)
	if got.Stage != publishStageMirroring || got.MirrorAttempts != 1 {
		t.Fatalf("人工重试后应重新开始自动重试计数，实际 stage=%q attempts=%d", got.Stage, got.MirrorAttempts)
	}
}

// TestPublishMirrorRetryStepsAsideAfterManualAction 等重试期间内容换了版本或任务被处理过，计时器到点不插手。
func TestPublishMirrorRetryStepsAsideAfterManualAction(t *testing.T) {
	env := newPublishTestEnv(t)
	scheduled := captureStageRetries(env)
	env.ossResults = []map[string]string{{}, {}}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = publishStageFailed; j.FailedStage = publishStageMirroring }); err != nil {
		t.Fatal(err)
	}
	calls := len(env.ossCalls)
	(*scheduled)[0].run()
	if len(env.ossCalls) != calls {
		t.Fatalf("任务已不在 mirroring，到点的重试不应再转存")
	}
}

// TestPublishMirrorMissingCoverFailsAtOnce 原文没有封面这类重试也没用的失败当场落 failed。
func TestPublishMirrorMissingCoverFailsAtOnce(t *testing.T) {
	env := newPublishTestEnv(t)
	scheduled := captureStageRetries(env)
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: "无图稿", SourceName: "测试公众号", CanonicalURL: articleURL,
			BlocksJSON: `[{"k":"p","text":"正文没有配图"}]`}, nil
	}
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		return successfulPublishRenderResult(`<p>正文没有配图</p>`, testPublishResolvedRule()), nil
	}
	job, _, _ := env.pipeline.submit(testPublishSourceURL)
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageFailed || len(*scheduled) != 0 {
		t.Fatalf("没有封面应当场失败、不排重试，实际 stage=%q scheduled=%d", got.Stage, len(*scheduled))
	}
}

// TestAutoPublishSkipsJobThatFailedBefore #296：失败过的稿重试成功后停在待审核等人确认。
func TestAutoPublishSkipsJobThatFailedBefore(t *testing.T) {
	env := newPublishTestEnv(t)
	enableAutoPublish(t, env)
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	env.ossResults = []map[string]string{{}, {}}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageFailed {
		t.Fatalf("没装自动重试调度时搬图失败应落 failed，实际 %q", got.Stage)
	}
	if _, err := env.pipeline.retry(job.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, _ = env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("失败过的稿重试成功后应停在待审核，实际 %q error=%q", got.Stage, got.Error)
	}
	if got.AutoPublishEligible {
		t.Fatalf("失败过的稿不得算自动发布新稿")
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("失败过的稿不得自动翻公开，UpdateNews=%d", updateN)
	}
}

// TestAutoPublishSkipsJobOlderThanMaxAge 建单超过一天才进 FoodTalks 的稿停在待审核，免得旧闻直接上站。
func TestAutoPublishSkipsJobOlderThanMaxAge(t *testing.T) {
	env := newPublishTestEnv(t)
	enableAutoPublish(t, env)
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	env.pipeline.now = func() time.Time { return time.Now().Add(publishAutoPublishMaxAge + time.Hour) }
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview || got.AutoPublishEligible {
		t.Fatalf("离建单超过 %s 的稿应停在待审核，实际 stage=%q eligible=%v", publishAutoPublishMaxAge, got.Stage, got.AutoPublishEligible)
	}
}

// TestFailedBeforeMarkedWhenLeavingLegacyFailedJob 上线前就停在 failed 的老单（没有 failed_before）
// 一离开 failed 也要补记，重试后同样不自动公开。
func TestFailedBeforeMarkedWhenLeavingLegacyFailedJob(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.store.create(testPublishSourceURL, testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(publishJob{Stage: publishStageFailed})
	if strings.Contains(string(raw), "failed_before") {
		t.Fatalf("没失败过的任务不应写 failed_before")
	}
	if _, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = publishStageFailed }); err != nil {
		t.Fatal(err)
	}
	got, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = publishStageMirroring })
	if err != nil {
		t.Fatal(err)
	}
	if !got.FailedBefore {
		t.Fatalf("离开 failed 时应补记 failed_before")
	}
}

// TestPublishFailedJobsEndpointListsRetryAndAbandon 管理后台「失败任务」列表：只列 failed，
// 带能否重试、能否放弃；下线失败的已上过站，不给放弃。
func TestPublishFailedJobsEndpointListsRetryAndAbandon(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	mirror, _, _ := env.store.create("site:https://mp.weixin.qq.com/s/a", "https://mp.weixin.qq.com/s/a")
	offline, _, _ := env.store.create("site:https://mp.weixin.qq.com/s/b", "https://mp.weixin.qq.com/s/b")
	waiting, _, _ := env.store.create("site:https://mp.weixin.qq.com/s/c", "https://mp.weixin.qq.com/s/c")
	env.store.update(mirror.ID, func(j *publishJob) {
		j.Stage, j.FailedStage, j.Title, j.Error = publishStageFailed, publishStageMirroring, "搬图失败稿", "图片转存失败"
	})
	env.store.update(offline.ID, func(j *publishJob) {
		j.Stage, j.FailedStage, j.Title = publishStageFailed, publishStageUnpublishing, "下线失败稿"
	})
	env.store.update(waiting.ID, func(j *publishJob) { j.Stage = publishStageAwaitingReview })

	rec := doPublishReq(mux, http.MethodGet, "/api/publish/failed-jobs", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Jobs []struct {
			ID          int64  `json:"id"`
			Title       string `json:"title"`
			FailedStage string `json:"failed_stage"`
			Retryable   bool   `json:"retryable"`
			Abandonable bool   `json:"abandonable"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 2 {
		t.Fatalf("只应列出 2 个失败任务，实际 %+v", body.Jobs)
	}
	byID := map[int64]bool{}
	for _, j := range body.Jobs {
		byID[j.ID] = j.Abandonable
		if j.ID == mirror.ID && (!j.Retryable || !j.Abandonable || j.Title != "搬图失败稿") {
			t.Fatalf("搬图失败稿应能重试、能放弃：%+v", j)
		}
	}
	if byID[offline.ID] {
		t.Fatalf("下线失败的稿已上过站，不应给放弃")
	}
	rec = doPublishReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/abandon", offline.ID), "", true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("下线失败的稿结案应被拒，实际 %d", rec.Code)
	}
}
