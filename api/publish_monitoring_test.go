package main

import (
	"context"
	"encoding/json"
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
	t.Setenv("ADMIN_PASSWORD", "admin-secret")
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

func TestPublishMonitorAlertRequiresMachineTokenAndDelivers(t *testing.T) {
	t.Setenv(publishSyncServiceTokenEnv, "monitor-secret")
	t.Setenv("ADMIN_PASSWORD", "admin-secret")
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
	adminOnly.Header.Set("X-Admin-Password", "admin-secret")
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
