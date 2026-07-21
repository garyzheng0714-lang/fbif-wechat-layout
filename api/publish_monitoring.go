package main

import (
	"fmt"
	"sort"
	"time"
)

const (
	publishProcessingWarningAfter      = 10 * time.Minute
	publishProcessingCriticalAfter     = 20 * time.Minute
	publishQueueCriticalAfter          = 15 * time.Minute
	publishNotificationCriticalAfter   = 10 * time.Minute
	publishReconciliationCriticalAfter = 30 * time.Minute
	publishRecentFailureWindow         = 24 * time.Hour
)

type publishMonitoringStatus struct {
	Ready                          bool     `json:"ready"`
	CheckedAt                      string   `json:"checked_at"`
	StoreValid                     bool     `json:"store_valid"`
	WorkersPaused                  bool     `json:"workers_paused"`
	Health                         string   `json:"health"`
	Busy                           bool     `json:"busy"`
	Processing                     int      `json:"processing"`
	OldestProcessingAgeSeconds     int64    `json:"oldest_processing_age_seconds"`
	Queued                         int      `json:"queued"`
	OldestQueueAgeSeconds          int64    `json:"oldest_queue_age_seconds"`
	NotificationPending            int      `json:"notification_pending"`
	OldestNotificationAgeSeconds   int64    `json:"oldest_notification_age_seconds"`
	NotificationErrors             int      `json:"notification_errors"`
	NotificationDeadLetters        int      `json:"notification_dead_letters"`
	ReviewCardsMissing             int      `json:"review_cards_missing"`
	DuplicateSourceKeys            int      `json:"duplicate_source_keys"`
	Failed                         int      `json:"failed"`
	FailedRecent                   int      `json:"failed_recent"`
	ReconciliationRequired         int      `json:"reconciliation_required"`
	OldestReconciliationAgeSeconds int64    `json:"oldest_reconciliation_age_seconds"`
	ContentConflicts               int      `json:"content_conflicts"`
	Issues                         []string `json:"issues,omitempty"`
	Warnings                       []string `json:"warnings,omitempty"`
	AlertIssues                    []string `json:"alert_issues,omitempty"`
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
		status.AlertIssues = []string{"publish_store_invalid"}
		status.Health = "unhealthy"
		return status
	}
	status.DuplicateSourceKeys = countDuplicatePublishSourceKeys(jobs)
	for _, job := range jobs {
		age := publishMonitoringAge(now, job.UpdatedAt)
		if publishMonitoringProcessingStage(job.Stage) {
			status.Processing++
			if age > status.OldestProcessingAgeSeconds {
				status.OldestProcessingAgeSeconds = age
			}
		}
		if job.Stage == publishStageCreated {
			status.Queued++
			if age > status.OldestQueueAgeSeconds {
				status.OldestQueueAgeSeconds = age
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
		if job.NotificationState == publishNotificationStateDeadLetter {
			status.NotificationDeadLetters++
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
			if age > status.OldestReconciliationAgeSeconds {
				status.OldestReconciliationAgeSeconds = age
			}
		}
		if job.ContentConflict {
			status.ContentConflicts++
		}
	}
	if status.WorkersPaused {
		status.Issues = append(status.Issues, "deployment_writes_paused")
		status.AlertIssues = append(status.AlertIssues, "deployment_writes_paused")
	}
	// 健康的含义是业务闭环已经完成，不是“进程还活着”。只要仍有任务在排版，
	// 或审核卡仍在 outbox 中，就绝不能向外部看门狗返回绿色。
	if status.Processing > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("business_processing:%d", status.Processing))
	}
	if status.NotificationPending > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("review_notification_pending:%d", status.NotificationPending))
	}
	if status.OldestProcessingAgeSeconds > int64(publishProcessingWarningAfter/time.Second) {
		status.Warnings = append(status.Warnings, fmt.Sprintf("processing_slow:%ds", status.OldestProcessingAgeSeconds))
	}
	if status.OldestProcessingAgeSeconds > int64(publishProcessingCriticalAfter/time.Second) {
		status.AlertIssues = append(status.AlertIssues, fmt.Sprintf("processing_stale:%ds", status.OldestProcessingAgeSeconds))
	}
	if status.OldestQueueAgeSeconds > int64(publishQueueCriticalAfter/time.Second) {
		status.AlertIssues = append(status.AlertIssues, fmt.Sprintf("queue_stale:%ds", status.OldestQueueAgeSeconds))
	}
	if status.OldestNotificationAgeSeconds > int64(publishNotificationCriticalAfter/time.Second) {
		issue := fmt.Sprintf("notification_outbox_stale:%ds", status.OldestNotificationAgeSeconds)
		status.Issues = append(status.Issues, issue)
		status.AlertIssues = append(status.AlertIssues, issue)
	}
	if status.NotificationErrors > 0 {
		status.Issues = append(status.Issues, fmt.Sprintf("notification_errors:%d", status.NotificationErrors))
		status.Warnings = append(status.Warnings, fmt.Sprintf("notification_errors:%d", status.NotificationErrors))
	}
	if status.NotificationDeadLetters > 0 {
		issue := fmt.Sprintf("notification_dead_letters:%d", status.NotificationDeadLetters)
		status.Issues = append(status.Issues, issue)
		status.AlertIssues = append(status.AlertIssues, issue)
	}
	if status.ReviewCardsMissing > 0 {
		issue := fmt.Sprintf("review_cards_missing:%d", status.ReviewCardsMissing)
		status.Issues = append(status.Issues, issue)
		status.AlertIssues = append(status.AlertIssues, issue)
	}
	if status.DuplicateSourceKeys > 0 {
		issue := fmt.Sprintf("duplicate_source_keys:%d", status.DuplicateSourceKeys)
		status.Issues = append(status.Issues, issue)
		status.AlertIssues = append(status.AlertIssues, issue)
	}
	if status.FailedRecent > 0 {
		issue := fmt.Sprintf("failed_jobs_recent:%d", status.FailedRecent)
		status.Issues = append(status.Issues, issue)
		status.AlertIssues = append(status.AlertIssues, issue)
	}
	if status.ReconciliationRequired > 0 {
		issue := fmt.Sprintf("reconciliation_backlog:%d", status.ReconciliationRequired)
		status.Issues = append(status.Issues, issue)
		status.Warnings = append(status.Warnings, issue)
		if status.OldestReconciliationAgeSeconds > int64(publishReconciliationCriticalAfter/time.Second) {
			status.AlertIssues = append(status.AlertIssues, fmt.Sprintf("reconciliation_stale:%ds", status.OldestReconciliationAgeSeconds))
		}
	}
	if status.ContentConflicts > 0 {
		issue := fmt.Sprintf("content_conflicts:%d", status.ContentConflicts)
		status.Issues = append(status.Issues, issue)
		status.AlertIssues = append(status.AlertIssues, issue)
	}
	sort.Strings(status.Issues)
	sort.Strings(status.Warnings)
	sort.Strings(status.AlertIssues)
	status.Ready = status.StoreValid && len(status.Issues) == 0
	status.Busy = status.Processing > 0 || status.NotificationPending > 0
	switch {
	case len(status.AlertIssues) > 0:
		status.Health = "unhealthy"
	case len(status.Warnings) > 0:
		status.Health = "degraded"
	case status.Busy:
		status.Health = "busy"
	default:
		status.Health = "healthy"
	}
	return status
}

func countDuplicatePublishSourceKeys(jobs []publishJob) int {
	seen := make(map[string]int64, len(jobs))
	duplicates := 0
	for index, job := range jobs {
		if job.Stage == publishStageSupersededDuplicate {
			continue
		}
		identity := job.ID
		if identity == 0 {
			identity = -int64(index + 1)
		}
		duplicate := false
		keys := append([]string{job.SourceKey}, job.SourceAliases...)
		for _, rawKey := range keys {
			key := canonicalPublishSourceKey(rawKey)
			if key == "" {
				continue
			}
			if previous, ok := seen[key]; ok {
				if previous != identity {
					duplicate = true
				}
				continue
			}
			seen[key] = identity
		}
		if duplicate {
			duplicates++
		}
	}
	return duplicates
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
