package main

import (
	"fmt"
	"sort"
	"strings"
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
	FoodTalksCredentialInvalid     bool     `json:"foodtalks_credential_invalid"`
	Issues                         []string `json:"issues,omitempty"`
	Warnings                       []string `json:"warnings,omitempty"`
	// alert_issues 必须始终出现在 JSON 里。省略空数组时，外部看门狗会退回
	// 把 issues 当 P0，于是正常处理中的稿也会被喊成事故。
	AlertIssues []string `json:"alert_issues"`

	failedUnresolved int
}

type publishMonitoringLevel uint8

const (
	publishMonitoringIssue publishMonitoringLevel = 1 << iota
	publishMonitoringWarning
	publishMonitoringAlert
)

type publishMonitoringSignal struct {
	condition bool
	message   string
	levels    publishMonitoringLevel
}

// credentialInvalid 来自最近一次 FoodTalks 鉴权结论：它不依赖任务积压，
// 没有稿件在跑时凭证失效也要立刻告警。
func currentPublishMonitoring(store *publishJobStore, credentialInvalid bool, now time.Time) publishMonitoringStatus {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	status := publishMonitoringStatus{
		CheckedAt:     now.Format(time.RFC3339),
		StoreValid:    true,
		WorkersPaused: publishWorkersPaused(),
		AlertIssues:   []string{},

		FoodTalksCredentialInvalid: credentialInvalid,
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
		updatePublishMonitoringWork(&status, job, now, age)
		updatePublishMonitoringNotifications(&status, job, age)
		updatePublishMonitoringFailures(&status, job, now)
		updatePublishMonitoringReconciliation(&status, job, now, age)
		if job.ContentConflict {
			status.ContentConflicts++
		}
	}
	finalizePublishMonitoring(&status)
	return status
}

func updatePublishMonitoringWork(status *publishMonitoringStatus, job publishJob, now time.Time, age int64) {
	processingAge := int64(-1)
	if publishMonitoringProcessingStage(job.Stage) {
		processingAge = age
	}
	for _, lane := range job.ExternalDistributionLanes {
		if !publishExternalDistributionProcessing(lane.State) {
			continue
		}
		if externalAge := publishMonitoringAge(now, publishExternalLaneStartedAt(lane)); externalAge > processingAge {
			processingAge = externalAge
		}
	}
	if publishSourceResyncProcessing(job.SourceResyncState) {
		resyncAge := publishMonitoringAge(now, publishSourceResyncStartedAt(job))
		if resyncAge > processingAge {
			processingAge = resyncAge
		}
	}
	if processingAge >= 0 {
		status.Processing++
		if processingAge > status.OldestProcessingAgeSeconds {
			status.OldestProcessingAgeSeconds = processingAge
		}
	}
	if job.Stage == publishStageCreated {
		status.Queued++
		if age > status.OldestQueueAgeSeconds {
			status.OldestQueueAgeSeconds = age
		}
	}
}

func updatePublishMonitoringNotifications(status *publishMonitoringStatus, job publishJob, age int64) {
	pending := boolInt(job.NotificationState == publishNotificationStatePending || job.NotificationState == publishNotificationStateSending)
	pending += boolInt(job.ExternalDistributionCardState == publishExternalCardPending || job.ExternalDistributionCardState == publishExternalCardSending)
	// 外发待修改提醒（回复在审核卡下、@ 分发负责人）同样是持久通知。
	reminder := job.ExternalDistributionReminder
	pending += boolInt(reminder.State == publishExternalCardPending || reminder.State == publishExternalCardSending)
	// 校对提醒（回复在审核卡下、@ 校对负责人）同上；还在等审核卡发出的不算。
	proofread := job.ProofreadReminder
	pending += boolInt((proofread.State == publishExternalCardPending && !publishProofreadAwaitingCard(job)) ||
		proofread.State == publishExternalCardSending)
	status.NotificationPending += pending
	if pending > 0 && age > status.OldestNotificationAgeSeconds {
		status.OldestNotificationAgeSeconds = age
	}
	status.NotificationErrors += boolInt(job.NotificationError != "") + boolInt(job.ExternalDistributionCardError != "") +
		boolInt(reminder.Error != "") + boolInt(proofread.Error != "")
	status.NotificationDeadLetters += boolInt(job.NotificationState == publishNotificationStateDeadLetter) +
		boolInt(job.ExternalDistributionCardState == publishExternalCardDeadLetter) +
		boolInt(reminder.State == publishExternalCardDeadLetter) +
		boolInt(proofread.State == publishExternalCardDeadLetter)
	if job.Stage == publishStageAwaitingReview && job.NotificationKind == "" && job.NotificationSentAt == "" {
		status.ReviewCardsMissing++
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func updatePublishMonitoringFailures(status *publishMonitoringStatus, job publishJob, now time.Time) {
	jobFailed := job.Stage == publishStageFailed
	externalFailed := false
	for _, lane := range job.ExternalDistributionLanes {
		externalFailed = externalFailed || lane.State == publishExternalDistributionFailed
	}
	resyncFailed := job.SourceResyncState == publishSourceResyncFailed
	status.Failed += boolInt(jobFailed) + boolInt(externalFailed) + boolInt(resyncFailed)
	// 主发布仍停在 failed 就尚未闭环，不得因超过近期窗口而停止告警。
	status.failedUnresolved += boolInt(jobFailed)
	if jobFailed && publishMonitoringIsRecent(now, job.UpdatedAt) {
		status.FailedRecent++
	}
	if externalFailed && publishMonitoringIsRecent(now, job.UpdatedAt) {
		status.FailedRecent++
	}
	if resyncFailed && publishMonitoringIsRecent(now, firstNonEmpty(job.SourceResyncCompletedAt, job.UpdatedAt)) {
		status.FailedRecent++
	}
}

func updatePublishMonitoringReconciliation(status *publishMonitoringStatus, job publishJob, now time.Time, age int64) {
	stageRequired := publishMonitoringReconciliationStage(job.Stage)
	externalRequired := false
	for _, lane := range job.ExternalDistributionLanes {
		externalRequired = externalRequired || lane.State == publishExternalDistributionReconciliationRequired
	}
	resyncRequired := job.SourceResyncState == publishSourceResyncReconciliationRequired
	status.ReconciliationRequired += boolInt(stageRequired) + boolInt(externalRequired) + boolInt(resyncRequired)
	if stageRequired && age > status.OldestReconciliationAgeSeconds {
		status.OldestReconciliationAgeSeconds = age
	}
	if externalRequired && age > status.OldestReconciliationAgeSeconds {
		status.OldestReconciliationAgeSeconds = age
	}
	if resyncRequired {
		resyncAge := publishMonitoringAge(now, publishSourceResyncStartedAt(job))
		if resyncAge > status.OldestReconciliationAgeSeconds {
			status.OldestReconciliationAgeSeconds = resyncAge
		}
	}
}

func publishMonitoringReconciliationStage(stage string) bool {
	switch stage {
	case publishStageReconciliationRequired, publishStagePublicizeReconciliationRequired,
		publishStageStagingCreating, publishStagePublicizingUpdating:
		return true
	default:
		return false
	}
}

func finalizePublishMonitoring(status *publishMonitoringStatus) {
	// 健康的含义是业务闭环已完成，不是“进程还活着”。仍在处理或
	// 卡在 outbox 的任务必须进 issues，超时后才升级为 alert。
	signals := []publishMonitoringSignal{
		{status.WorkersPaused, "deployment_writes_paused", publishMonitoringIssue | publishMonitoringAlert},
		{status.Processing > 0, fmt.Sprintf("business_processing:%d", status.Processing), publishMonitoringIssue},
		{status.NotificationPending > 0, fmt.Sprintf("review_notification_pending:%d", status.NotificationPending), publishMonitoringIssue},
		{status.OldestProcessingAgeSeconds > int64(publishProcessingWarningAfter/time.Second), fmt.Sprintf("processing_slow:%ds", status.OldestProcessingAgeSeconds), publishMonitoringWarning},
		{status.OldestProcessingAgeSeconds > int64(publishProcessingCriticalAfter/time.Second), fmt.Sprintf("processing_stale:%ds", status.OldestProcessingAgeSeconds), publishMonitoringAlert},
		{status.OldestQueueAgeSeconds > int64(publishQueueCriticalAfter/time.Second), fmt.Sprintf("queue_stale:%ds", status.OldestQueueAgeSeconds), publishMonitoringAlert},
		{status.OldestNotificationAgeSeconds > int64(publishNotificationCriticalAfter/time.Second), fmt.Sprintf("notification_outbox_stale:%ds", status.OldestNotificationAgeSeconds), publishMonitoringIssue | publishMonitoringAlert},
		{status.NotificationErrors > 0, fmt.Sprintf("notification_errors:%d", status.NotificationErrors), publishMonitoringIssue | publishMonitoringWarning},
		{status.NotificationDeadLetters > 0, fmt.Sprintf("notification_dead_letters:%d", status.NotificationDeadLetters), publishMonitoringIssue | publishMonitoringAlert},
		{status.ReviewCardsMissing > 0, fmt.Sprintf("review_cards_missing:%d", status.ReviewCardsMissing), publishMonitoringIssue | publishMonitoringAlert},
		{status.DuplicateSourceKeys > 0, fmt.Sprintf("duplicate_source_keys:%d", status.DuplicateSourceKeys), publishMonitoringIssue | publishMonitoringAlert},
		{status.FailedRecent > 0, fmt.Sprintf("failed_jobs_recent:%d", status.FailedRecent), publishMonitoringIssue | publishMonitoringAlert},
		{status.failedUnresolved > 0, fmt.Sprintf("failed_jobs_unresolved:%d", status.failedUnresolved), publishMonitoringIssue | publishMonitoringAlert},
		{status.ReconciliationRequired > 0, fmt.Sprintf("reconciliation_backlog:%d", status.ReconciliationRequired), publishMonitoringIssue | publishMonitoringWarning},
		{status.ReconciliationRequired > 0 && status.OldestReconciliationAgeSeconds > int64(publishReconciliationCriticalAfter/time.Second), fmt.Sprintf("reconciliation_stale:%ds", status.OldestReconciliationAgeSeconds), publishMonitoringAlert},
		{status.ContentConflicts > 0, fmt.Sprintf("content_conflicts:%d", status.ContentConflicts), publishMonitoringIssue | publishMonitoringAlert},
		{status.FoodTalksCredentialInvalid, "foodtalks_credential_invalid", publishMonitoringIssue | publishMonitoringAlert},
	}
	appendPublishMonitoringSignals(status, signals)
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
}

func appendPublishMonitoringSignals(status *publishMonitoringStatus, signals []publishMonitoringSignal) {
	for _, signal := range signals {
		if !signal.condition {
			continue
		}
		if signal.levels&publishMonitoringIssue != 0 {
			status.Issues = append(status.Issues, signal.message)
		}
		if signal.levels&publishMonitoringWarning != 0 {
			status.Warnings = append(status.Warnings, signal.message)
		}
		if signal.levels&publishMonitoringAlert != 0 {
			status.AlertIssues = append(status.AlertIssues, signal.message)
		}
	}
}

func publishExternalLaneStartedAt(lane publishExternalLane) string {
	if value := strings.TrimSpace(lane.IntentAt); value != "" {
		return value
	}
	return strings.TrimSpace(lane.RequestedAt)
}

func publishSourceResyncStartedAt(job publishJob) string {
	if value := strings.TrimSpace(job.SourceResyncIntentAt); value != "" {
		return value
	}
	return strings.TrimSpace(job.SourceResyncRequestedAt)
}

func countDuplicatePublishSourceKeys(jobs []publishJob) int {
	seen := make(map[string]int64, len(jobs))
	duplicates := 0
	for index, job := range jobs {
		if job.Stage == publishStageSupersededDuplicate {
			continue
		}
		if publishJobHasDuplicateSourceKey(job, index, seen) {
			duplicates++
		}
	}
	return duplicates
}

func publishJobHasDuplicateSourceKey(job publishJob, index int, seen map[string]int64) bool {
	identity := job.ID
	if identity == 0 {
		identity = -int64(index + 1)
	}
	duplicate := false
	for _, rawKey := range append([]string{job.SourceKey}, job.SourceAliases...) {
		key := canonicalPublishSourceKey(rawKey)
		if key == "" {
			continue
		}
		if previous, ok := seen[key]; ok {
			duplicate = duplicate || previous != identity
			continue
		}
		seen[key] = identity
	}
	return duplicate
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
