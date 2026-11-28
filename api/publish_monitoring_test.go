package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publishMonitoringHasIssue(status publishMonitoringStatus, prefix string) bool {
	for _, issue := range status.Issues {
		if strings.HasPrefix(issue, prefix) {
			return true
		}
	}
	return false
}

func TestPublishMonitoringReportsOnlyOperationalMetrics(t *testing.T) {
	t.Setenv(publishSyncServiceTokenEnv, "monitor-secret")
	t.Setenv("PUBLISH_ADMIN_SERVICE_TOKEN", "admin-service-token")
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/a", "https://mp.weixin.qq.com/s/a")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageRendering
		current.Title = "不得出现在监控响应中的标题"
		current.ContentHTML = "<p>secret body</p>"
		current.NotificationState = publishNotificationStatePending
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(20 * time.Minute)
	status := currentPublishMonitoring(store, false, 0, now)
	if status.Ready || status.Processing != 1 || status.NotificationPending != 1 {
		t.Fatalf("monitoring status = %+v", status)
	}
	for _, prefix := range []string{"business_processing:", "review_notification_pending:"} {
		if !publishMonitoringHasIssue(status, prefix) {
			t.Fatalf("business work in progress must never be reported green; missing %q in %+v", prefix, status.Issues)
		}
	}

	pipeline := &publishPipeline{store: store}
	mux := http.NewServeMux()
	registerPublishRoutes(mux, pipeline)
	req := httptest.NewRequest(http.MethodGet, "/api/publish/monitoring", nil)
	req.Header.Set(publishSyncServiceHeader, "monitor-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("monitoring status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "不得出现在") || strings.Contains(rec.Body.String(), "secret body") {
		t.Fatalf("monitoring response leaked content: %s", rec.Body.String())
	}
	var wire map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["alert_issues"]; !ok {
		t.Fatalf("监控 JSON 必须带 alert_issues 空数组，否则看门狗会把 issues 当 P0：%s", rec.Body.String())
	}

	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/publish/monitoring", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing service token status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
}

func TestPublishMonitoringSeparatesBusyDegradedAndUnhealthy(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/slow", "https://mp.weixin.qq.com/s/slow")
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageRendering
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedAt, err := time.Parse(time.RFC3339, job.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}

	busy := currentPublishMonitoring(store, false, 0, updatedAt.Add(5*time.Minute))
	if busy.Ready || !busy.Busy || busy.Health != "busy" || len(busy.AlertIssues) != 0 {
		t.Fatalf("normal processing should be busy without P0 alert: %+v", busy)
	}
	raw, err := json.Marshal(busy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"alert_issues":[]`) && !strings.Contains(string(raw), `"alert_issues": []`) {
		t.Fatalf("busy 状态必须写出空的 alert_issues，实际 %s", raw)
	}

	degraded := currentPublishMonitoring(store, false, 0, updatedAt.Add(11*time.Minute))
	if degraded.Health != "degraded" || len(degraded.AlertIssues) != 0 ||
		!publishMonitoringHasIssue(publishMonitoringStatus{Issues: degraded.Warnings}, "processing_slow:") {
		t.Fatalf("near-SLO processing should be warning-only: %+v", degraded)
	}

	unhealthy := currentPublishMonitoring(store, false, 0, updatedAt.Add(21*time.Minute))
	if unhealthy.Health != "unhealthy" || !publishMonitoringHasIssue(publishMonitoringStatus{Issues: unhealthy.AlertIssues}, "processing_stale:") {
		t.Fatalf("over-SLO processing should alert: %+v", unhealthy)
	}
}

// TestPublishMonitoringAlertsSayWhatToDo 每条告警都带人话和怎么办（alert_hints），看门狗照着发：2026-11-20 看门狗自己那张
// 对照表里没有 failed_jobs_recent，私信里一周都只有机器码。人话只写在排版工具这一处，加告警忘了写这里就过不了。
func TestPublishMonitoringAlertsSayWhatToDo(t *testing.T) {
	hour := int64(time.Hour / time.Second)
	status := publishMonitoringStatus{
		StoreValid: true, WorkersPaused: true, AlertIssues: []string{}, AlertHints: map[string]publishMonitoringHint{},
		Processing: 1, OldestProcessingAgeSeconds: hour, OldestExternalPublishingAgeSeconds: hour, OldestExternalReviewingAgeSeconds: 48 * hour,
		OldestQueueAgeSeconds: hour, NotificationPending: 1, OldestNotificationAgeSeconds: hour, NotificationErrors: 1, NotificationDeadLetters: 1,
		ReviewCardsMissing: 1, DuplicateSourceKeys: 1, FailedRecent: 1, failedUnresolved: 1, ReconciliationRequired: 1,
		OldestReconciliationAgeSeconds: hour, ContentConflicts: 1, FoodTalksCredentialInvalid: true, ExternalFidelityMismatches: 1,
		ExternalFidelityUnavailable: 1, StatusCallbackDeadLetters: 1,
	}
	status.noteLoginWait(externalAccount{ID: "recTencent", Platform: "tencent", Label: "腾讯号"}, 383)
	finalizePublishMonitoring(&status)
	if len(status.AlertIssues) < 14 {
		t.Fatalf("每类告警都该出现：%v", status.AlertIssues)
	}
	for _, issue := range status.AlertIssues {
		if hint := status.AlertHints[issue]; strings.TrimSpace(hint.Meaning) == "" || strings.TrimSpace(hint.Action) == "" {
			t.Errorf("告警 %s 没写人话或怎么办：%+v", issue, hint)
		}
	}
	if len(status.AlertHints) != len(status.AlertIssues) {
		t.Fatalf("人话只给告警：%d 条人话、%d 条告警", len(status.AlertHints), len(status.AlertIssues))
	}
	quiet := currentPublishMonitoring(newPublishJobStore(t.TempDir()+"/jobs.json"), false, 0, time.Now())
	raw, err := json.Marshal(quiet)
	if err != nil || !strings.Contains(string(raw), `"alert_hints":{}`) {
		t.Fatalf("没告警时 alert_hints 也写出空的：%s %v", raw, err)
	}
}

func TestPublishMonitoringUsesExternalIntentAgeAcrossResultChecks(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/external-slow", "https://mp.weixin.qq.com/s/external-slow")
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-11 * time.Minute).Truncate(time.Second)
	if _, err := store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.ExternalDistributionLanes = []publishExternalLane{{
			AccountID: "acct-1", Name: "头条号", Driver: externalDriverRobot, State: publishExternalDistributionPushing,
			RequestedAt:  startedAt.Add(-time.Minute).Format(time.RFC3339),
			IntentAt:     startedAt.Format(time.RFC3339),
			LastPolledAt: time.Now().UTC().Format(time.RFC3339),
		}}
	}); err != nil {
		t.Fatal(err)
	}
	status := currentPublishMonitoring(store, false, 0, time.Now().UTC())
	if status.OldestProcessingAgeSeconds < 10*60 ||
		!publishMonitoringHasIssue(publishMonitoringStatus{Issues: status.Warnings}, "processing_slow:") {
		t.Fatalf("查结果更新 UpdatedAt 后仍应按外部分发 intent 计龄：%+v", status)
	}
}

func TestPublishMonitoringAlertsOnStaleQueueAndAgesReconciliation(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	queued, _, err := store.create("site:https://mp.weixin.qq.com/s/queued", "https://mp.weixin.qq.com/s/queued")
	if err != nil {
		t.Fatal(err)
	}
	queuedAt, _ := time.Parse(time.RFC3339, queued.UpdatedAt)
	queueStatus := currentPublishMonitoring(store, false, 0, queuedAt.Add(16*time.Minute))
	if !publishMonitoringHasIssue(publishMonitoringStatus{Issues: queueStatus.AlertIssues}, "queue_stale:") {
		t.Fatalf("stale queue must alert: %+v", queueStatus)
	}

	reconciliation, err := store.update(queued.ID, func(current *publishJob) {
		current.Stage = publishStageReconciliationRequired
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciliationAt, _ := time.Parse(time.RFC3339, reconciliation.UpdatedAt)
	warning := currentPublishMonitoring(store, false, 0, reconciliationAt.Add(5*time.Minute))
	if warning.Health != "degraded" || len(warning.AlertIssues) != 0 {
		t.Fatalf("fresh reconciliation should be warning-only: %+v", warning)
	}
	critical := currentPublishMonitoring(store, false, 0, reconciliationAt.Add(31*time.Minute))
	if !publishMonitoringHasIssue(publishMonitoringStatus{Issues: critical.AlertIssues}, "reconciliation_stale:") {
		t.Fatalf("stale reconciliation must alert: %+v", critical)
	}
}

func TestPublishMonitoringGuardsReviewNotificationAndDeduplication(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	first, _, err := store.create("site:https://mp.weixin.qq.com/s/first", "https://mp.weixin.qq.com/s/first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(first.ID, func(job *publishJob) {
		job.Stage = publishStageAwaitingReview
		job.NotificationKind = publishNotificationReviewPending
		job.NotificationState = publishNotificationStatePending
		job.NotificationError = "feishu unavailable"
	}); err != nil {
		t.Fatal(err)
	}
	missing, _, err := store.create("site:https://mp.weixin.qq.com/s/missing", "https://mp.weixin.qq.com/s/missing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(missing.ID, func(job *publishJob) {
		job.Stage = publishStageAwaitingReview
	}); err != nil {
		t.Fatal(err)
	}
	duplicate, _, err := store.create("site:https://mp.weixin.qq.com/s/duplicate", "https://mp.weixin.qq.com/s/duplicate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(duplicate.ID, func(job *publishJob) {
		job.SourceKey = first.SourceKey
	}); err != nil {
		t.Fatal(err)
	}

	status := currentPublishMonitoring(store, false, 0, time.Now().UTC())
	if status.Ready || status.NotificationErrors != 1 || status.ReviewCardsMissing != 1 || status.DuplicateSourceKeys != 1 {
		t.Fatalf("monitoring invariants = %+v", status)
	}
	for _, prefix := range []string{"notification_errors:", "review_cards_missing:", "duplicate_source_keys:"} {
		if !publishMonitoringHasIssue(status, prefix) {
			t.Fatalf("missing issue %q in %+v", prefix, status.Issues)
		}
	}
}

func TestPublishMonitoringReportsNotificationDeadLetters(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/dead-letter", "https://mp.weixin.qq.com/s/dead-letter")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageAwaitingReview
		current.NotificationKind = publishNotificationReviewPending
		current.NotificationState = publishNotificationStateDeadLetter
		current.NotificationError = "delivery exhausted"
	}); err != nil {
		t.Fatal(err)
	}

	status := currentPublishMonitoring(store, false, 0, time.Now())
	if status.Ready || status.NotificationDeadLetters != 1 || !publishMonitoringHasIssue(status, "notification_dead_letters:") {
		t.Fatalf("monitoring status = %+v", status)
	}
}

func TestCountDuplicatePublishSourceKeysHandlesAliasesOncePerJob(t *testing.T) {
	jobs := []publishJob{
		{ID: 1, SourceKey: "site:https://mp.weixin.qq.com/s/first", Stage: publishStagePublished},
		{ID: 2, SourceKey: "site:https://mp.weixin.qq.com/s/second", SourceAliases: []string{"site:https://mp.weixin.qq.com/s/first"}, Stage: publishStagePublished},
		{ID: 3, SourceKey: "site:https://mp.weixin.qq.com/s/third", SourceAliases: []string{
			"site:https://mp.weixin.qq.com/s/first",
			"site:https://mp.weixin.qq.com/s/second",
		}, Stage: publishStagePublished},
		{ID: 4, SourceKey: "site:https://mp.weixin.qq.com/s/first", Stage: publishStageSupersededDuplicate},
	}

	if got := countDuplicatePublishSourceKeys(jobs); got != 2 {
		t.Fatalf("duplicate jobs = %d, want 2", got)
	}
}

func TestCountDuplicatePublishSourceKeysScalesLinearly(t *testing.T) {
	const jobCount = 50_000
	jobs := make([]publishJob, jobCount)
	for i := range jobs {
		jobs[i] = publishJob{
			ID:        int64(i + 1),
			SourceKey: fmt.Sprintf("site:https://mp.weixin.qq.com/s/article-%d", i),
			Stage:     publishStagePublished,
		}
	}
	jobs[jobCount-1].SourceAliases = []string{jobs[123].SourceKey}

	started := time.Now()
	if got := countDuplicatePublishSourceKeys(jobs); got != 1 {
		t.Fatalf("duplicate jobs = %d, want 1", got)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("50k duplicate scan took %s; expected indexed linear scan", elapsed)
	}
}

func TestPublishMonitoringUnresolvedFailureDoesNotAgeOut(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/old-failure", "https://mp.weixin.qq.com/s/old-failure")
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageFailed
		current.FailedStage = publishStageRendering
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedAt, err := time.Parse(time.RFC3339, job.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	recent := currentPublishMonitoring(store, false, 0, updatedAt.Add(time.Minute))
	if recent.Ready || recent.Failed != 1 || recent.FailedRecent != 1 || !publishMonitoringHasIssue(recent, "failed_jobs_recent:") {
		t.Fatalf("recent failure = %+v", recent)
	}
	old := currentPublishMonitoring(store, false, 0, updatedAt.Add(publishRecentFailureWindow+time.Minute))
	if old.Ready || old.Health != "unhealthy" || old.Failed != 1 || old.FailedRecent != 0 ||
		!publishMonitoringHasIssue(old, "failed_jobs_unresolved:") ||
		!publishMonitoringHasIssue(publishMonitoringStatus{Issues: old.AlertIssues}, "failed_jobs_unresolved:") {
		t.Fatalf("unresolved publishing failure must keep alerting after 24 hours = %+v", old)
	}
	if _, err := store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageAbandoned
	}); err != nil {
		t.Fatal(err)
	}
	resolved := currentPublishMonitoring(store, false, 0, updatedAt.Add(publishRecentFailureWindow+time.Minute))
	if !resolved.Ready || resolved.Failed != 0 || len(resolved.AlertIssues) != 0 {
		t.Fatalf("resolved publishing failure must stop alerting = %+v", resolved)
	}
}

func TestPublishMonitoringPublishedOldSubflowFailuresRemainMetricOnly(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	for _, state := range []string{publishExternalDistributionFailed, publishExternalDistributionPartialFailed, "source_resync"} {
		t.Run(state, func(t *testing.T) {
			store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
			job, _, err := store.create("site:https://mp.weixin.qq.com/s/published", "https://mp.weixin.qq.com/s/published")
			if err != nil {
				t.Fatal(err)
			}
			job, err = store.update(job.ID, func(current *publishJob) {
				current.Stage = publishStagePublished
				if state == "source_resync" {
					current.SourceResyncState = publishSourceResyncFailed
				} else {
					// 汇总为部分失败：一个平台成功、一个受理后失败。
					current.ExternalDistributionLanes = []publishExternalLane{{AccountID: "acct-1", State: publishExternalDistributionFailed, ContentPK: "pk"}}
					if state == publishExternalDistributionPartialFailed {
						current.ExternalDistributionLanes = append(current.ExternalDistributionLanes,
							publishExternalLane{AccountID: "acct-2", State: publishExternalDistributionSucceeded, ContentPK: "pk"})
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			updatedAt, err := time.Parse(time.RFC3339, job.UpdatedAt)
			if err != nil {
				t.Fatal(err)
			}
			old := currentPublishMonitoring(store, false, 0, updatedAt.Add(publishRecentFailureWindow+time.Minute))
			if !old.Ready || old.Failed != 1 || old.FailedRecent != 0 || len(old.AlertIssues) != 0 {
				t.Fatalf("published article's old subflow failure must remain metric-only = %+v", old)
			}
		})
	}
}

// TestPublishMonitoringExternalFailureRecencyUsesLaneFailureTime 查结果、改卡都会刷新 job.UpdatedAt；
// 第三方平台失败是否「近期」按 lane 失败时间算，几周前的失败不能因为补查一直告警。
func TestPublishMonitoringExternalFailureRecencyUsesLaneFailureTime(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/old-lane", "https://mp.weixin.qq.com/s/old-lane")
	if err != nil {
		t.Fatal(err)
	}
	failedAt := time.Date(2026, 9, 3, 1, 38, 0, 0, time.UTC)
	job, err = store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.ExternalDistributionLanes = []publishExternalLane{
			{AccountID: "acct-1", State: publishExternalDistributionSucceeded, ContentPK: "pk", CompletedAt: failedAt.Format(time.RFC3339)},
			{AccountID: "acct-2", State: publishExternalDistributionFailed, ContentPK: "pk", CompletedAt: failedAt.Format(time.RFC3339)},
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedAt, err := time.Parse(time.RFC3339, job.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	polled := currentPublishMonitoring(store, false, 0, updatedAt.Add(time.Minute))
	if !polled.Ready || polled.Failed != 1 || polled.FailedRecent != 0 {
		t.Fatalf("几周前的第三方失败被补查刷新了任务时间，不得算近期告警 = %+v", polled)
	}
	fresh := currentPublishMonitoring(store, false, 0, failedAt.Add(time.Hour))
	if fresh.Ready || fresh.FailedRecent != 1 || !publishMonitoringHasIssue(fresh, "failed_jobs_recent:") {
		t.Fatalf("刚失败的第三方平台仍须近期告警 = %+v", fresh)
	}
}

func TestPublishMonitoringIncludesSourceResyncSubstate(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/resync", "https://mp.weixin.qq.com/s/resync")
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.SourceResyncState = publishSourceResyncPreparing
		current.SourceResyncRequestedAt = startedAt
	}); err != nil {
		t.Fatal(err)
	}
	processing := currentPublishMonitoring(store, false, 0, time.Now().UTC())
	if processing.Ready || processing.Processing != 1 || !publishMonitoringHasIssue(processing, "business_processing:") {
		t.Fatalf("重新同步进行中不得报绿：%+v", processing)
	}
	if _, err := store.update(job.ID, func(current *publishJob) {
		current.SourceResyncState = publishSourceResyncReconciliationRequired
	}); err != nil {
		t.Fatal(err)
	}
	reconciliation := currentPublishMonitoring(store, false, 0, time.Now().UTC())
	if reconciliation.ReconciliationRequired != 1 || !publishMonitoringHasIssue(reconciliation, "reconciliation_backlog:") {
		t.Fatalf("重新同步待对账必须进入监控积压：%+v", reconciliation)
	}
}

func TestAbandonFailedJobClearsRecentFailureAlert(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	env := newPublishTestEnv(t)
	job, _, err := env.store.create("site:https://mp.weixin.qq.com/s/dead-url", "https://mp.weixin.qq.com/s/dead-url")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageFailed
		current.FailedStage = publishStageFetching
		current.Error = "未能从文章解析出标题"
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.abandonFailedJob(job.ID, "公众号原文已失效"); err != nil {
		t.Fatalf("结案失败: %v", err)
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != publishStageAbandoned || !got.terminal() {
		t.Fatalf("结案后应为 abandoned 终态，实际 %+v", got)
	}
	status := currentPublishMonitoring(env.store, false, 0, time.Now().UTC())
	if !status.Ready || status.FailedRecent != 0 || publishMonitoringHasIssue(status, "failed_jobs_recent:") {
		t.Fatalf("结案后监控应变绿，实际 %+v", status)
	}
}

func TestPublishMonitorAlertRequiresMachineTokenAndDelivers(t *testing.T) {
	t.Setenv(publishSyncServiceTokenEnv, "monitor-secret")
	t.Setenv("PUBLISH_ADMIN_SERVICE_TOKEN", "admin-service-token")
	var delivered string
	pipeline := &publishPipeline{
		store: newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json")),
		monitorNotify: func(_ context.Context, message string) error {
			delivered = message
			return nil
		},
	}
	mux := http.NewServeMux()
	registerPublishRoutes(mux, pipeline)
	payload, err := json.Marshal(publishMonitorAlertRequest{
		Source: "wechat-official", Status: "critical", Summary: "freepublish 轮询停止",
		Details: []string{"failed_endpoints:2", "  notification   reporter  "},
	})
	if err != nil {
		t.Fatal(err)
	}

	adminOnly := httptest.NewRequest(http.MethodPost, "/api/publish/monitor-alert", strings.NewReader(string(payload)))
	adminOnly.Header.Set("X-Publish-Admin-Token", "admin-service-token")
	adminResult := httptest.NewRecorder()
	mux.ServeHTTP(adminResult, adminOnly)
	if adminResult.Code != http.StatusUnauthorized {
		t.Fatalf("admin must not authorize relay: %d %s", adminResult.Code, adminResult.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/publish/monitor-alert", strings.NewReader(string(payload)))
	req.Header.Set(publishSyncServiceHeader, "monitor-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(delivered, "[FBIF 公众号监控 P0] 异常") ||
		!strings.Contains(delivered, "failed_endpoints:2") || strings.Contains(delivered, "  ") {
		t.Fatalf("relay status=%d body=%s delivered=%q", rec.Code, rec.Body.String(), delivered)
	}
}

func TestPublishMonitorAlertNeverFallsBackToReviewGroup(t *testing.T) {
	t.Setenv(publishSyncServiceTokenEnv, "monitor-secret")
	groupMessages := 0
	pipeline := &publishPipeline{
		store: newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json")),
		notify: func(_ context.Context, _ string) error {
			groupMessages++
			return nil
		},
	}
	mux := http.NewServeMux()
	registerPublishRoutes(mux, pipeline)
	req := httptest.NewRequest(http.MethodPost, "/api/publish/monitor-alert", strings.NewReader(
		`{"source":"wechat-official","status":"heartbeat","summary":"日报完成"}`,
	))
	req.Header.Set(publishSyncServiceHeader, "monitor-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || groupMessages != 0 {
		t.Fatalf("私聊未配置时必须失败且不得回退审核群：status=%d group_messages=%d body=%s", rec.Code, groupMessages, rec.Body.String())
	}
}

// TestPublishMonitoringExternalPublishingAndReviewing 2026-10-30 起第三方平台存完草稿就发布：发布中算处理中，
// 过 30 分钟告警；平台审核中要等平台，不算处理中，24 小时内不报，过了只提醒；平台没通过和失败一样算失败。
func TestPublishMonitoringExternalPublishingAndReviewing(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	enteredAt := time.Date(2026, 10, 30, 2, 0, 0, 0, time.UTC)
	status := func(t *testing.T, state string, after time.Duration) publishMonitoringStatus {
		t.Helper()
		store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
		job, _, err := store.create("site:https://mp.weixin.qq.com/s/external-"+state, "https://mp.weixin.qq.com/s/external-"+state)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.update(job.ID, func(current *publishJob) {
			current.Stage = publishStagePublished
			current.ExternalDistributionLanes = []publishExternalLane{{
				AccountID: "acct-1", Name: "百家号", Driver: externalDriverRobot, State: state, ContentPK: "pk",
				IntentAt: enteredAt.Add(-5 * time.Minute).Format(time.RFC3339), CompletedAt: enteredAt.Format(time.RFC3339),
			}}
		}); err != nil {
			t.Fatal(err)
		}
		return currentPublishMonitoring(store, false, 0, enteredAt.Add(after))
	}
	has := func(issues []string, prefix string) bool {
		return publishMonitoringHasIssue(publishMonitoringStatus{Issues: issues}, prefix)
	}

	reviewing := status(t, publishExternalDistributionReviewing, 23*time.Hour)
	if reviewing.Processing != 0 || reviewing.OldestProcessingAgeSeconds != 0 || reviewing.OldestExternalReviewingAgeSeconds != 23*3600 ||
		has(reviewing.Warnings, "external_reviewing_slow:") || len(reviewing.AlertIssues) != 0 || reviewing.Failed != 0 {
		t.Fatalf("审核中 24 小时内不算处理中、不报：%+v", reviewing)
	}
	slow := status(t, publishExternalDistributionReviewing, 25*time.Hour)
	if !has(slow.Warnings, "external_reviewing_slow:") || len(slow.AlertIssues) != 0 {
		t.Fatalf("审核中过 24 小时只提醒、不告警：%+v", slow)
	}
	publishing := status(t, publishExternalDistributionPublishing, 20*time.Minute)
	if publishing.Processing != 1 || publishing.OldestExternalPublishingAgeSeconds != 20*60 || has(publishing.AlertIssues, "external_publishing_stale:") {
		t.Fatalf("发布中算处理中，30 分钟内不告警：%+v", publishing)
	}
	stale := status(t, publishExternalDistributionPublishing, 31*time.Minute)
	if !has(stale.AlertIssues, "external_publishing_stale:") {
		t.Fatalf("发布中过 30 分钟告警：%+v", stale)
	}
	rejected := status(t, publishExternalDistributionRejected, time.Hour)
	if rejected.Failed != 1 || rejected.FailedRecent != 1 || rejected.Processing != 0 {
		t.Fatalf("平台没通过和失败一样算：%+v", rejected)
	}
}
