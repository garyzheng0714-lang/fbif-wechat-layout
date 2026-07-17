package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
