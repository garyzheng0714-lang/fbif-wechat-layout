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
	status := currentPublishMonitoring(store, now)
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

	busy := currentPublishMonitoring(store, updatedAt.Add(5*time.Minute))
	if busy.Ready || !busy.Busy || busy.Health != "busy" || len(busy.AlertIssues) != 0 {
		t.Fatalf("normal processing should be busy without P0 alert: %+v", busy)
	}

	degraded := currentPublishMonitoring(store, updatedAt.Add(11*time.Minute))
	if degraded.Health != "degraded" || len(degraded.AlertIssues) != 0 ||
		!publishMonitoringHasIssue(publishMonitoringStatus{Issues: degraded.Warnings}, "processing_slow:") {
		t.Fatalf("near-SLO processing should be warning-only: %+v", degraded)
	}

	unhealthy := currentPublishMonitoring(store, updatedAt.Add(21*time.Minute))
	if unhealthy.Health != "unhealthy" || !publishMonitoringHasIssue(publishMonitoringStatus{Issues: unhealthy.AlertIssues}, "processing_stale:") {
		t.Fatalf("over-SLO processing should alert: %+v", unhealthy)
	}
}

func TestPublishMonitoringUsesExternalIntentAgeAcrossPollUpdates(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/external-slow", "https://mp.weixin.qq.com/s/external-slow")
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-11 * time.Minute).Truncate(time.Second)
	if _, err := store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.ExternalDistributionState = publishExternalDistributionPolling
		current.ExternalDistributionRequestedAt = startedAt.Add(-time.Minute).Format(time.RFC3339)
		current.ExternalDistributionIntentAt = startedAt.Format(time.RFC3339)
		current.ExternalDistributionLastPolledAt = time.Now().UTC().Format(time.RFC3339)
	}); err != nil {
		t.Fatal(err)
	}
	status := currentPublishMonitoring(store, time.Now().UTC())
	if status.OldestProcessingAgeSeconds < 10*60 ||
		!publishMonitoringHasIssue(publishMonitoringStatus{Issues: status.Warnings}, "processing_slow:") {
		t.Fatalf("轮询更新 UpdatedAt 后仍应按外部分发 intent 计龄：%+v", status)
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
	queueStatus := currentPublishMonitoring(store, queuedAt.Add(16*time.Minute))
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
	warning := currentPublishMonitoring(store, reconciliationAt.Add(5*time.Minute))
	if warning.Health != "degraded" || len(warning.AlertIssues) != 0 {
		t.Fatalf("fresh reconciliation should be warning-only: %+v", warning)
	}
	critical := currentPublishMonitoring(store, reconciliationAt.Add(31*time.Minute))
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

	status := currentPublishMonitoring(store, time.Now().UTC())
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

	status := currentPublishMonitoring(store, time.Now())
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

func TestPublishMonitoringOldFailureRemainsVisibleWithoutPermanentOutage(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/old-failure", "https://mp.weixin.qq.com/s/old-failure")
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageFailed
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedAt, err := time.Parse(time.RFC3339, job.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	recent := currentPublishMonitoring(store, updatedAt.Add(time.Minute))
	if recent.Ready || recent.Failed != 1 || recent.FailedRecent != 1 || !publishMonitoringHasIssue(recent, "failed_jobs_recent:") {
		t.Fatalf("recent failure = %+v", recent)
	}
	old := currentPublishMonitoring(store, updatedAt.Add(publishRecentFailureWindow+time.Minute))
	if !old.Ready || old.Failed != 1 || old.FailedRecent != 0 || publishMonitoringHasIssue(old, "failed_jobs") {
		t.Fatalf("historical failure should remain metric-only = %+v", old)
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
	status := currentPublishMonitoring(env.store, time.Now().UTC())
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
