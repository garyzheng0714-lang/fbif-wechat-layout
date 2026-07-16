package main

import (
	"fmt"
	"sort"
	"time"
)

const (
	publishProcessingStaleAfter   = 15 * time.Minute
	publishNotificationStaleAfter = 10 * time.Minute
)

type publishMonitoringStatus struct {
	Ready                        bool     `json:"ready"`
	CheckedAt                    string   `json:"checked_at"`
	StoreValid                   bool     `json:"store_valid"`
	WorkersPaused                bool     `json:"workers_paused"`
	Processing                   int      `json:"processing"`
	OldestProcessingAgeSeconds   int64    `json:"oldest_processing_age_seconds"`
	NotificationPending          int      `json:"notification_pending"`
	OldestNotificationAgeSeconds int64    `json:"oldest_notification_age_seconds"`
	Failed                       int      `json:"failed"`
	ReconciliationRequired       int      `json:"reconciliation_required"`
	ContentConflicts             int      `json:"content_conflicts"`
	Issues                       []string `json:"issues,omitempty"`
}

func currentPublishMonitoring(store *publishJobStore, now time.Time) publishMonitoringStatus {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	status := publishMonitoringStatus{
		CheckedAt:     now.Format(time.RFC3339),
		StoreValid:    true,
		WorkersPaused: publishWorkersPaused(),
	}
	jobs, err := store.all()
	if err != nil {
		status.StoreValid = false
		status.Issues = []string{"publish_store_invalid"}
		return status
	}
	for _, job := range jobs {
		age := publishMonitoringAge(now, job.UpdatedAt)
		if publishMonitoringProcessingStage(job.Stage) {
			status.Processing++
			if age > status.OldestProcessingAgeSeconds {
				status.OldestProcessingAgeSeconds = age
			}
		}
		if job.NotificationState == publishNotificationStatePending || job.NotificationState == publishNotificationStateSending {
			status.NotificationPending++
			if age > status.OldestNotificationAgeSeconds {
				status.OldestNotificationAgeSeconds = age
			}
		}
		if job.Stage == publishStageFailed {
			status.Failed++
		}
		if job.Stage == publishStageReconciliationRequired ||
			job.Stage == publishStagePublicizeReconciliationRequired ||
			job.Stage == publishStageStagingCreating ||
			job.Stage == publishStagePublicizingUpdating {
			status.ReconciliationRequired++
		}
		if job.ContentConflict {
			status.ContentConflicts++
		}
	}
	if status.WorkersPaused {
		status.Issues = append(status.Issues, "deployment_writes_paused")
	}
	if status.OldestProcessingAgeSeconds > int64(publishProcessingStaleAfter/time.Second) {
		status.Issues = append(status.Issues, fmt.Sprintf("processing_stale:%ds", status.OldestProcessingAgeSeconds))
	}
	if status.OldestNotificationAgeSeconds > int64(publishNotificationStaleAfter/time.Second) {
		status.Issues = append(status.Issues, fmt.Sprintf("notification_outbox_stale:%ds", status.OldestNotificationAgeSeconds))
	}
	if status.Failed > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("failed_jobs:%d", status.Failed))
	}
	if status.ReconciliationRequired > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("reconciliation_backlog:%d", status.ReconciliationRequired))
	}
	if status.ContentConflicts > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("content_conflicts:%d", status.ContentConflicts))
	}
	sort.Strings(status.Issues)
	status.Ready = status.StoreValid && len(status.Issues) == 0
	return status
}

func publishMonitoringProcessingStage(stage string) bool {
	switch stage {
	case publishStageCreated, publishStageFetching, publishStageRendering,
		publishStageMirroring, publishStageEnriching, publishStageAssessing,
		publishStageAssessingReview, publishStageStaging, publishStagePublicizing:
		return true
	default:
		return false
	}
}

func publishMonitoringAge(now time.Time, value string) int64 {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.After(now) {
		return 0
	}
	return int64(now.Sub(parsed) / time.Second)
}
