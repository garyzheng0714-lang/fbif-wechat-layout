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
	// 第三方平台发布中：分发服务点发布、再查几分钟才定，过 30 分钟还没定才告警。
	publishExternalPublishingCriticalAfter = 30 * time.Minute
	// 第三方平台审核中：要等平台，多久说不准；过 24 小时提醒一次，不算处理中、不告警。
	publishExternalReviewingWarningAfter = 24 * time.Hour
)

type publishMonitoringStatus struct {
	Ready                      bool   `json:"ready"`
	CheckedAt                  string `json:"checked_at"`
	StoreValid                 bool   `json:"store_valid"`
	WorkersPaused              bool   `json:"workers_paused"`
	Health                     string `json:"health"`
	Busy                       bool   `json:"busy"`
	Processing                 int    `json:"processing"`
	OldestProcessingAgeSeconds int64  `json:"oldest_processing_age_seconds"`
	// 第三方平台发布中、审核中的最久一条（只看分发服务的 lane，不进 oldest_processing_age_seconds）。
	OldestExternalPublishingAgeSeconds int64 `json:"oldest_external_publishing_age_seconds"`
	OldestExternalReviewingAgeSeconds  int64 `json:"oldest_external_reviewing_age_seconds"`
	Queued                             int   `json:"queued"`
	OldestQueueAgeSeconds              int64 `json:"oldest_queue_age_seconds"`
	NotificationPending                int   `json:"notification_pending"`
	OldestNotificationAgeSeconds       int64 `json:"oldest_notification_age_seconds"`
	NotificationErrors                 int   `json:"notification_errors"`
	NotificationDeadLetters            int   `json:"notification_dead_letters"`
	ReviewCardsMissing                 int   `json:"review_cards_missing"`
	DuplicateSourceKeys                int   `json:"duplicate_source_keys"`
	Failed                             int   `json:"failed"`
	FailedRecent                       int   `json:"failed_recent"`
	ReconciliationRequired             int   `json:"reconciliation_required"`
	OldestReconciliationAgeSeconds     int64 `json:"oldest_reconciliation_age_seconds"`
	ContentConflicts                   int   `json:"content_conflicts"`
	FoodTalksCredentialInvalid         bool  `json:"foodtalks_credential_invalid"`
	ExternalFidelityMismatches         int   `json:"external_fidelity_mismatches"`
	ExternalFidelityUnavailable        int   `json:"external_fidelity_unavailable"`
	// 7 天内连续推不出去、已放弃的任务状态推送（资讯订阅还有兜底回查，不是 P0）。
	StatusCallbackDeadLetters int      `json:"status_callback_dead_letters"`
	Issues                    []string `json:"issues,omitempty"`
	Warnings                  []string `json:"warnings,omitempty"`
	// alert_issues 必须始终出现在 JSON 里。省略空数组时，外部看门狗会退回
	// 把 issues 当 P0，于是正常处理中的稿也会被喊成事故。
	AlertIssues []string `json:"alert_issues"`
	// alert_hints 每条 alert_issues 出了什么事、怎么办（键就是 alert_issues 里那一条）。人话只写在这里，
	// 看门狗照着发，不在那边另配一张对照表（2026-11-20：看门狗表里没有 failed_jobs_recent，私信里只有机器码）。
	AlertHints map[string]publishMonitoringHint `json:"alert_hints"`
	// 分发服务器上没登录的号，每个号一条；等它登录的篇不算失败（publish_external_retry.go）。
	ExternalLoginRequired []publishMonitoringLogin `json:"external_login_required,omitempty"`

	failedUnresolved int
	// nextChange 下一个只靠时间流逝就可能让告警翻转的时刻（处理中满 20 分钟、近期失败满 24 小时……）；
	// 零值表示没有。叫醒看门狗（publish_monitor_wake.go）到这一刻重算一次，不按固定间隔去问。
	nextChange time.Time
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
	// hint 告警级的必须有（TestPublishMonitoringAlertsSayWhatToDo）。
	hint publishMonitoringHint
}

// publishMonitoringHint 一条告警给人看的：出了什么事、怎么办。
type publishMonitoringHint struct {
	Meaning string `json:"meaning"`
	Action  string `json:"action"`
}

// publishMonitoringLogin 一个在分发服务器上没登录的号，和等它的几篇。
type publishMonitoringLogin struct {
	AccountID string  `json:"account_id"`
	Platform  string  `json:"platform"`
	Label     string  `json:"label"`
	Jobs      []int64 `json:"jobs"`
}

// changesWhenOlderThan 一件事现在 age 秒、满 limit 才算告警：记下它跨过去的那一刻。已经跨过去的不用记。
func (s *publishMonitoringStatus) changesWhenOlderThan(now time.Time, age int64, limit time.Duration) {
	limitSeconds := int64(limit / time.Second)
	if age > limitSeconds {
		return
	}
	// 判定是 age > limit（按整秒），所以到 limit 再过一秒才翻。
	at := now.Add(time.Duration(limitSeconds-age+1) * time.Second)
	if s.nextChange.IsZero() || at.Before(s.nextChange) {
		s.nextChange = at
	}
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
		AlertHints:    map[string]publishMonitoringHint{},

		FoodTalksCredentialInvalid: credentialInvalid,
	}
	jobs, err := store.all()
	if err != nil {
		status.StoreValid = false
		status.Issues = []string{"publish_store_invalid"}
		status.AlertIssues = []string{"publish_store_invalid"}
		status.AlertHints["publish_store_invalid"] = publishMonitoringHint{
			Meaning: "排版工具读不了自己的任务库", Action: "查排版工具日志里读 data/publish-jobs.json 的报错",
		}
		status.Health = "unhealthy"
		return status
	}
	status.DuplicateSourceKeys = countDuplicatePublishSourceKeys(jobs)
	for _, job := range jobs {
		age := publishMonitoringAge(now, job.UpdatedAt)
		updatePublishMonitoringWork(&status, job, now, age)
		updatePublishMonitoringNotifications(&status, job, now, age)
		updatePublishMonitoringFailures(&status, job, now)
		updatePublishMonitoringReconciliation(&status, job, now, age)
		mismatched, unavailable := externalFidelityMonitoring(job, now)
		status.ExternalFidelityMismatches += mismatched
		status.ExternalFidelityUnavailable += unavailable
		if job.ContentConflict {
			status.ContentConflicts++
		}
		if statusCallbackDeadLetterRecent(job, now) {
			status.StatusCallbackDeadLetters++
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
	publishing := false
	for _, lane := range job.ExternalDistributionLanes {
		switch {
		case lane.State == publishExternalDistributionPublishing:
			publishing = true
			age := publishMonitoringAge(now, publishExternalLaneEnteredAt(lane))
			status.changesWhenOlderThan(now, age, publishExternalPublishingCriticalAfter)
			if age > status.OldestExternalPublishingAgeSeconds {
				status.OldestExternalPublishingAgeSeconds = age
			}
		case lane.State == publishExternalDistributionReviewing:
			if age := publishMonitoringAge(now, publishExternalLaneEnteredAt(lane)); age > status.OldestExternalReviewingAgeSeconds {
				status.OldestExternalReviewingAgeSeconds = age
			}
		case publishExternalDistributionProcessing(lane.State):
			if externalAge := publishMonitoringAge(now, publishExternalLaneStartedAt(lane)); externalAge > processingAge {
				processingAge = externalAge
			}
		}
	}
	if publishSourceResyncProcessing(job.SourceResyncState) {
		resyncAge := publishMonitoringAge(now, publishSourceResyncStartedAt(job))
		if resyncAge > processingAge {
			processingAge = resyncAge
		}
	}
	if processingAge >= 0 {
		status.changesWhenOlderThan(now, processingAge, publishProcessingCriticalAfter)
	}
	if processingAge >= 0 || publishing {
		status.Processing++
		if processingAge > status.OldestProcessingAgeSeconds {
			status.OldestProcessingAgeSeconds = processingAge
		}
	}
	if job.Stage == publishStageCreated {
		status.changesWhenOlderThan(now, age, publishQueueCriticalAfter)
		status.Queued++
		if age > status.OldestQueueAgeSeconds {
			status.OldestQueueAgeSeconds = age
		}
	}
}

func updatePublishMonitoringNotifications(status *publishMonitoringStatus, job publishJob, now time.Time, age int64) {
	pending := boolInt(job.NotificationState == publishNotificationStatePending || job.NotificationState == publishNotificationStateSending)
	pending += boolInt(job.ExternalDistributionCardState == publishExternalCardPending || job.ExternalDistributionCardState == publishExternalCardSending)
	// 外发待修改提醒（回复在审核卡下、@ 作者或分发负责人）同样是持久通知。
	reminder := job.ExternalDistributionReminder
	pending += boolInt(reminder.State == publishExternalCardPending || reminder.State == publishExternalCardSending)
	// 校对提醒（回复在审核卡下、@ 校对负责人）同上；还在等审核卡发出的不算。
	proofread := job.ProofreadReminder
	pending += boolInt((proofread.State == publishExternalCardPending && !publishProofreadAwaitingCard(job)) ||
		proofread.State == publishExternalCardSending)
	status.NotificationPending += pending
	if pending > 0 {
		status.changesWhenOlderThan(now, age, publishNotificationCriticalAfter)
	}
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
	// 第三方平台的失败按 lane 失败那一刻算近期：补查、刷卡都会刷新 job.UpdatedAt，
	// 用它算就会把几周前的失败当新失败一直告警（2026-10-14 #153/#260/#183）。
	// 草稿存好却读回对不上、没发的（draft_saved）也是这篇没发出去：系统按退避另存重推，3 次都没对上多半是写法错了、
	// 重推也一样，和失败一样告警（2026-11-26 #400 头条号、知乎号对不上卡了 2 天，#369 知乎号卡了 12 天，都只记了一条不提醒的警告，
	// 是人在群里问了才发现）。
	externalFailed, externalFailedAt := false, time.Time{}
	for _, lane := range job.ExternalDistributionLanes {
		held := lane.State == publishExternalDistributionDraftSaved && externalLaneAutoRetryState(lane)
		if lane.State != publishExternalDistributionFailed && lane.State != publishExternalDistributionRejected && !held {
			continue
		}
		outlook := externalLaneOutlookOf(job, lane, now)
		// 号改成人工发以后，它上面以前没推成的归人去发，不算失败（publish_external_manual.go）。
		if outlook.Manual {
			continue
		}
		// 号没登录停着的不算这一篇失败：按号报一条，等到 30 天。
		if outlook.Login == externalLoginWaiting {
			status.noteLoginWait(outlook.Target, job.ID)
			status.changesWhenOlderThan(now, publishMonitoringAge(now, lane.RequestedAt), externalLoginWaitWindow)
			continue
		}
		// 系统还会自己重推的（前 3 次退避没用完、号刚登好）先不算失败：连续 3 次没成才找人。
		if outlook.Retrying {
			// 过了 7 天系统就不再自己重推，那一刻起算失败。
			status.changesWhenOlderThan(now, publishMonitoringAge(now, lane.RequestedAt), externalAutoRetryWindow)
			continue
		}
		externalFailed = true
		if at, err := time.Parse(time.RFC3339, lane.CompletedAt); err == nil && at.After(externalFailedAt) {
			externalFailedAt = at
		}
	}
	externalFailedWhen := job.UpdatedAt
	if !externalFailedAt.IsZero() {
		externalFailedWhen = externalFailedAt.Format(time.RFC3339Nano)
	}
	resyncFailed := job.SourceResyncState == publishSourceResyncFailed
	status.Failed += boolInt(jobFailed) + boolInt(externalFailed) + boolInt(resyncFailed)
	// 主发布仍停在 failed 就尚未闭环，不得因超过近期窗口而停止告警。
	status.failedUnresolved += boolInt(jobFailed)
	for _, failure := range []struct {
		failed bool
		at     string
	}{
		{jobFailed, job.UpdatedAt},
		{externalFailed, externalFailedWhen},
		{resyncFailed, firstNonEmpty(job.SourceResyncCompletedAt, job.UpdatedAt)},
	} {
		if failure.failed && publishMonitoringIsRecent(now, failure.at) {
			status.FailedRecent++
			// 过了近期窗口就不再算近期失败：那一刻告警可能少一类。
			status.changesWhenOlderThan(now, publishMonitoringAge(now, failure.at), publishRecentFailureWindow)
		}
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
	if stageRequired || externalRequired {
		status.changesWhenOlderThan(now, age, publishReconciliationCriticalAfter)
	}
	if stageRequired && age > status.OldestReconciliationAgeSeconds {
		status.OldestReconciliationAgeSeconds = age
	}
	if externalRequired && age > status.OldestReconciliationAgeSeconds {
		status.OldestReconciliationAgeSeconds = age
	}
	if resyncRequired {
		resyncAge := publishMonitoringAge(now, publishSourceResyncStartedAt(job))
		status.changesWhenOlderThan(now, resyncAge, publishReconciliationCriticalAfter)
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
		{status.WorkersPaused, "deployment_writes_paused", publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{"排版工具暂停了写入（部署时会暂停）", "部署完会自己恢复；部署早结束了还在报，查部署日志和 " + deploymentWritesPausedEnv}},
		{status.Processing > 0, fmt.Sprintf("business_processing:%d", status.Processing), publishMonitoringIssue, publishMonitoringHint{}},
		{status.NotificationPending > 0, fmt.Sprintf("review_notification_pending:%d", status.NotificationPending), publishMonitoringIssue, publishMonitoringHint{}},
		{status.OldestProcessingAgeSeconds > int64(publishProcessingWarningAfter/time.Second), fmt.Sprintf("processing_slow:%ds", status.OldestProcessingAgeSeconds), publishMonitoringWarning, publishMonitoringHint{}},
		{status.OldestProcessingAgeSeconds > int64(publishProcessingCriticalAfter/time.Second), fmt.Sprintf("processing_stale:%ds", status.OldestProcessingAgeSeconds), publishMonitoringAlert,
			publishMonitoringHint{"有文章处理了 20 分钟还没完（排版、发 FoodTalks、同步第三方平台或重抓原文）", "在管理后台找处理中的那篇，看排版工具日志卡在哪一步"}},
		{status.OldestExternalPublishingAgeSeconds > int64(publishExternalPublishingCriticalAfter/time.Second), fmt.Sprintf("external_publishing_stale:%ds", status.OldestExternalPublishingAgeSeconds), publishMonitoringAlert,
			publishMonitoringHint{"有文章在第三方平台「发布中」30 分钟还没出结果", "看审核卡上那个平台的状态和分发工具日志"}},
		{status.OldestExternalReviewingAgeSeconds > int64(publishExternalReviewingWarningAfter/time.Second), fmt.Sprintf("external_reviewing_slow:%ds", status.OldestExternalReviewingAgeSeconds), publishMonitoringWarning, publishMonitoringHint{}},
		{status.OldestQueueAgeSeconds > int64(publishQueueCriticalAfter/time.Second), fmt.Sprintf("queue_stale:%ds", status.OldestQueueAgeSeconds), publishMonitoringAlert,
			publishMonitoringHint{"有文章排队 15 分钟还没开始处理", "看排版工具的处理进程在不在跑、日志里有没有报错"}},
		{status.OldestNotificationAgeSeconds > int64(publishNotificationCriticalAfter/time.Second), fmt.Sprintf("notification_outbox_stale:%ds", status.OldestNotificationAgeSeconds), publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{"有审核卡或提醒 10 分钟还没发出去", "查排版工具日志里发飞书消息的报错"}},
		{status.NotificationErrors > 0, fmt.Sprintf("notification_errors:%d", status.NotificationErrors), publishMonitoringIssue | publishMonitoringWarning, publishMonitoringHint{}},
		{status.NotificationDeadLetters > 0, fmt.Sprintf("notification_dead_letters:%d", status.NotificationDeadLetters), publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{"有审核卡或提醒发了多次都没发出去，已不再重试", "查排版工具日志里发飞书消息失败的原因，修好后在管理后台重发"}},
		{status.ReviewCardsMissing > 0, fmt.Sprintf("review_cards_missing:%d", status.ReviewCardsMissing), publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{"有文章在等审核，却没发出审核卡", "查排版工具日志，在管理后台补发审核卡"}},
		{status.DuplicateSourceKeys > 0, fmt.Sprintf("duplicate_source_keys:%d", status.DuplicateSourceKeys), publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{"同一篇原文进了两个任务", "在管理后台找出重复的任务，只留一个"}},
		{status.FailedRecent > 0, fmt.Sprintf("failed_jobs_recent:%d", status.FailedRecent), publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{fmt.Sprintf("最近 24 小时有 %d 篇没做成（排版、发布、重抓原文，或同步第三方平台自己重推 3 次都没成、草稿读回 3 次都对不上）", status.FailedRecent),
				"在管理后台看失败原因；第三方平台的看审核卡上那一行"}},
		{status.failedUnresolved > 0, fmt.Sprintf("failed_jobs_unresolved:%d", status.failedUnresolved), publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{"有文章发布失败、还没处理", "在管理后台看原因并重试"}},
		{status.ReconciliationRequired > 0, fmt.Sprintf("reconciliation_backlog:%d", status.ReconciliationRequired), publishMonitoringIssue | publishMonitoringWarning, publishMonitoringHint{}},
		{status.ReconciliationRequired > 0 && status.OldestReconciliationAgeSeconds > int64(publishReconciliationCriticalAfter/time.Second), fmt.Sprintf("reconciliation_stale:%ds", status.OldestReconciliationAgeSeconds), publishMonitoringAlert,
			publishMonitoringHint{"有任务「待核对」超过 30 分钟", "看审核卡上写「待核对」的地方，到平台后台核对后处理"}},
		{status.ContentConflicts > 0, fmt.Sprintf("content_conflicts:%d", status.ContentConflicts), publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{"有文章审核或发布后又来了更新的原文，没有自动替换", "在管理后台看这篇的冲突说明，决定用不用新内容"}},
		{status.FoodTalksCredentialInvalid, "foodtalks_credential_invalid", publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{"FoodTalks 凭证失效（最近一次鉴权回 401），文章发不到 FoodTalks", "在中台换 FoodTalks 的凭证"}},
		// 草稿读回对不上（平台编辑器改了），或分发服务没读回：要有人看，但不是 P0、不挡 ready。
		{status.ExternalFidelityMismatches > 0, fmt.Sprintf("external_fidelity_mismatch:%d", status.ExternalFidelityMismatches), publishMonitoringWarning, publishMonitoringHint{}},
		{status.ExternalFidelityUnavailable > 0, fmt.Sprintf("external_fidelity_unavailable:%d", status.ExternalFidelityUnavailable), publishMonitoringWarning, publishMonitoringHint{}},
		{status.StatusCallbackDeadLetters > 0, fmt.Sprintf("status_callback_dead_letters:%d", status.StatusCallbackDeadLetters), publishMonitoringWarning, publishMonitoringHint{}},
	}
	signals = append(signals, status.loginSignals()...)
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
			status.AlertHints[signal.message] = signal.hint
		}
	}
}

// noteLoginWait 这篇在等这个号登录。
func (s *publishMonitoringStatus) noteLoginWait(target externalAccount, jobID int64) {
	for i := range s.ExternalLoginRequired {
		if entry := &s.ExternalLoginRequired[i]; entry.AccountID == target.ID {
			entry.Jobs = append(entry.Jobs, jobID)
			return
		}
	}
	s.ExternalLoginRequired = append(s.ExternalLoginRequired, publishMonitoringLogin{
		AccountID: target.ID, Platform: target.Platform, Label: target.label(), Jobs: []int64{jobID},
	})
}

// loginSignals 每个没登录的号一条告警：external_login_required:<平台>:<篇数>（一个平台不止一个号时再带上号的编号）。
func (s *publishMonitoringStatus) loginSignals() []publishMonitoringSignal {
	sort.Slice(s.ExternalLoginRequired, func(i, j int) bool {
		return s.ExternalLoginRequired[i].AccountID < s.ExternalLoginRequired[j].AccountID
	})
	perPlatform := map[string]int{}
	for _, entry := range s.ExternalLoginRequired {
		perPlatform[entry.Platform]++
	}
	out := make([]publishMonitoringSignal, 0, len(s.ExternalLoginRequired))
	for _, entry := range s.ExternalLoginRequired {
		sort.Slice(entry.Jobs, func(i, j int) bool { return entry.Jobs[i] < entry.Jobs[j] })
		where := entry.Platform
		if perPlatform[entry.Platform] > 1 {
			where += ":" + entry.AccountID
		}
		ids := make([]string, len(entry.Jobs))
		for i, id := range entry.Jobs {
			ids[i] = fmt.Sprintf("#%d", id)
		}
		out = append(out, publishMonitoringSignal{
			true, fmt.Sprintf("external_login_required:%s:%d", where, len(entry.Jobs)), publishMonitoringIssue | publishMonitoringAlert,
			publishMonitoringHint{
				Meaning: fmt.Sprintf("%s在分发服务器上没登录，%d 篇原创停着没同步（%s）", entry.Label, len(entry.Jobs), strings.Join(ids, "、")),
				Action: fmt.Sprintf("在内部工具服务器上跑 docker exec fbif-distribution-browser node service/cli.mjs login %s，"+
					"把打出的远程画面链接发给能扫码的人登录；登好后这几篇系统自己重新同步，不用点卡片", entry.AccountID),
			},
		})
	}
	return out
}

// publishExternalLaneEnteredAt lane 进入现在这个状态的时间（发布中、审核中按它算多久了）。
func publishExternalLaneEnteredAt(lane publishExternalLane) string {
	return firstNonEmpty(strings.TrimSpace(lane.CompletedAt), publishExternalLaneStartedAt(lane))
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
