package main

import (
	"fmt"
	"sort"
	"time"
)

const (
	publishProcessingStaleAfter   = 15 * time.Minute
	publishNotificationStaleAfter = 10 * time.Minute
	publishRecentFailureWindow    = 24 * time.Hour
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
	NotificationErrors           int      `json:"notification_errors"`
	ReviewCardsMissing           int      `json:"review_cards_missing"`
	DuplicateSourceKeys          int      `json:"duplicate_source_keys"`
	Failed                       int      `json:"failed"`
	FailedRecent                 int      `json:"failed_recent"`
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
	for index, job := range jobs {
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
		if job.NotificationError != "" {
			status.NotificationErrors++
		}
		if job.Stage == publishStageAwaitingReview && job.NotificationKind == "" && job.NotificationSentAt == "" {
			status.ReviewCardsMissing++
		}
		if job.Stage == publishStageFailed {
			status.Failed++
			if publishMonitoringIsRecent(now, job.UpdatedAt) {
				status.FailedRecent++
			}
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
		if job.Stage != publishStageSupersededDuplicate {
			for previous := 0; previous < index; previous++ {
				if jobs[previous].Stage != publishStageSupersededDuplicate && publishJobsShareSourceIdentity(job, jobs[previous]) {
					status.DuplicateSourceKeys++
					break
				}
			}
		}
	}
	if status.WorkersPaused {
		status.Issues = append(status.Issues, "deployment_writes_paused")
	}
	// 健康的含义是业务闭环已经完成，不是“进程还活着”。只要仍有任务在排版，
	// 或审核卡仍在 outbox 中，就绝不能向外部看门狗返回绿色。
	if status.Processing > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("business_processing:%d", status.Processing))
	}
	if status.NotificationPending > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("review_notification_pending:%d", status.NotificationPending))
	}
	if status.OldestProcessingAgeSeconds > int64(publishProcessingStaleAfter/time.Second) {
		status.Issues = append(status.Issues, fmt.Sprintf("processing_stale:%ds", status.OldestProcessingAgeSeconds))
	}
	if status.OldestNotificationAgeSeconds > int64(publishNotificationStaleAfter/time.Second) {
		status.Issues = append(status.Issues, fmt.Sprintf("notification_outbox_stale:%ds", status.OldestNotificationAgeSeconds))
	}
	if status.NotificationErrors > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("notification_errors:%d", status.NotificationErrors))
	}
	if status.ReviewCardsMissing > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("review_cards_missing:%d", status.ReviewCardsMissing))
	}
	if status.DuplicateSourceKeys > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("duplicate_source_keys:%d", status.DuplicateSourceKeys))
	}
	if status.FailedRecent > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("failed_jobs_recent:%d", status.FailedRecent))
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

func publishJobsShareSourceIdentity(left, right publishJob) bool {
	leftKeys := append([]string{left.SourceKey}, left.SourceAliases...)
	rightKeys := append([]string{right.SourceKey}, right.SourceAliases...)
	for _, leftKey := range leftKeys {
		for _, rightKey := range rightKeys {
			if leftKey != "" && rightKey != "" && publishSourceKeysEqual(leftKey, rightKey) {
				return true
			}
		}
	}
	return false
}

func publishMonitoringIsRecent(now time.Time, value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.After(now) {
		return true
	}
	return now.Sub(parsed) <= publishRecentFailureWindow
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
