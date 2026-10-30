package main

// publish_external_lanes.go — 外部分发按平台账号分道（lane）。
//
// 2026-10-12 用户裁定：各平台分开发布（「他们这个发布不应该是分开的吗？」）。每个账号一条 lane，
// 各自冻结 payload 与 intent、各自存草稿再发布、各自对账。红线 37 的安全规则逐条落在 lane 上：结果
// 不明的按幂等键向分发服务查（publish_external_robot.go），系统自己不再提交；只有失败的 lane 可以从
// 卡片重新同步；推过的账号（不论结果）不会被自动外发再推。
//
// 2026-10-24 云梭下线：它留下的 lane（Driver 为空）只显示；已发布的照旧显示已发布，失败的可以从卡片
// 重新同步（换成一条经分发服务的 lane）。

import "strings"

// publishExternalLaneManual 系统之外已经发过（人工发的头条稿等），只登记链接；
// 系统永不再推这个账号。
const publishExternalLaneManual = "manual"

type publishExternalLane struct {
	// AccountID 多维表格「平台账号」的 record_id。
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	// Driver 送稿方式：robot 是自己的分发服务；空是云梭留下的旧 lane，只显示。
	Driver externalDriver `json:"driver,omitempty"`
	// 经分发服务的：queued → pushing → publishing → reviewing → succeeded，旁支 draft_saved（草稿与原文
	// 对不上、没发）、rejected（平台没通过）、failed、reconciliation_required；人工登记的是 manual。
	// 云梭的旧 lane 只有 succeeded / failed / reconciliation_required。
	State          string `json:"state"`
	RequestedBy    string `json:"requested_by,omitempty"`
	RequestedAt    string `json:"requested_at,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
	ContentVersion int64  `json:"content_version,omitempty"`
	Attribution    string `json:"attribution,omitempty"`
	Source         string `json:"source,omitempty"`
	// Title 存到这个平台的标题，卡片列表显示。
	Title       string `json:"title,omitempty"`
	PayloadJSON string `json:"payload_json,omitempty"`
	PayloadHash string `json:"payload_hash,omitempty"`
	// IntentKey 外调前落盘；经分发服务的就是发给它的幂等键。
	IntentKey string `json:"intent_key,omitempty"`
	IntentAt  string `json:"intent_at,omitempty"`
	// ContentPK 平台上的草稿编号（云梭旧 lane 是云梭的单号）。
	ContentPK string `json:"content_pk,omitempty"`
	Error     string `json:"error,omitempty"`
	// CompletedAt 进入现在这个状态的时间（存好草稿转发布中、点完转审核中、发出去……每一步都记）；
	// monitoring 按它算发布中、审核中多久了，失败、没通过是不是近期的。
	CompletedAt  string `json:"completed_at,omitempty"`
	PollAttempts int    `json:"poll_attempts,omitempty"`
	LastPolledAt string `json:"last_polled_at,omitempty"`
	// Remark 失败原因的人话（卡片显示）；云梭旧 lane 是云梭回执里的说明。
	Remark string `json:"remark,omitempty"`
	// URL 文章在平台上的链接：分发服务发出去后查到的，或人工登记的（系统之外发过的）。
	URL string `json:"url,omitempty"`
	// DraftURL 平台上这篇草稿的编辑页（分发服务存好草稿后给的）。
	DraftURL string `json:"draft_url,omitempty"`
	// Fidelity 分发服务存完草稿当场读回的核对（publish_external_fidelity.go）。
	Fidelity publishExternalFidelity `json:"fidelity,omitzero"`
	// ActionID 已经落到 lane 上的那次发布动作（分发服务记的编号）。卡片重试、管理员重发后又看到同一个
	// 编号的失败或没看清，是上一次的结果，不是这次的：这次还没发，要调发布。
	ActionID string `json:"action_id,omitempty"`
}

func externalLaneIndex(job publishJob, accountID string) int {
	accountID = strings.TrimSpace(accountID)
	for i, lane := range job.ExternalDistributionLanes {
		if strings.TrimSpace(lane.AccountID) == accountID {
			return i
		}
	}
	return -1
}

func externalLaneFor(job publishJob, accountID string) (publishExternalLane, bool) {
	if i := externalLaneIndex(job, accountID); i >= 0 {
		return job.ExternalDistributionLanes[i], true
	}
	return publishExternalLane{}, false
}

// externalLaneCanSafeRetry 失败的平台可以从卡片重新同步：经分发服务的失败要么没存成草稿（改好后重新
// 冻结、重存），要么草稿存好了、发布明确没做成（同一个幂等键再交，分发服务认出存好的那篇，再发一次）；
// 结果不明的是待核对，平台没通过的是 rejected，都不能重推。云梭旧 lane 失败的是云梭没发出去。
func externalLaneCanSafeRetry(lane publishExternalLane) bool {
	return lane.State == publishExternalDistributionFailed
}

// externalLaneDraftSaved 经分发服务的、草稿已经存进平台的 lane：内容冻结在那篇草稿里，不能再改，
// 重试也只按原来的幂等键再交（robotResendFrozenLane）。
func externalLaneDraftSaved(lane publishExternalLane) bool {
	return lane.Driver == externalDriverRobot && strings.TrimSpace(lane.ContentPK) != "" && strings.TrimSpace(lane.IntentKey) != ""
}

// externalLaneEditable 推过的平台里还能改标题、摘要、封面的：失败了、草稿也还没存进平台。
func externalLaneEditable(lane publishExternalLane) bool {
	return externalLaneCanSafeRetry(lane) && !externalLaneDraftSaved(lane)
}

// externalLaneSettled 已在平台上：系统推送成功，或人工登记过已发。
func externalLaneSettled(lane publishExternalLane) bool {
	return lane.State == publishExternalDistributionSucceeded || lane.State == publishExternalLaneManual
}

func externalDistributionStarted(job publishJob) bool {
	return len(job.ExternalDistributionLanes) > 0
}

func externalDistributionAnyProcessing(job publishJob) bool {
	for _, lane := range job.ExternalDistributionLanes {
		if publishExternalDistributionProcessing(lane.State) {
			return true
		}
	}
	return false
}

// externalDistributionCoveredAccounts 有 lane 的账号（不论结果）：失败的也算，自动外发不再推，
// 只从卡片人工重存。账号为空的 lane（按平台拆开之前缺账号的旧数据）视为覆盖全部。
func externalDistributionCoveredAccounts(job publishJob) (map[string]bool, bool) {
	covered := map[string]bool{}
	for _, lane := range job.ExternalDistributionLanes {
		id := strings.TrimSpace(lane.AccountID)
		if id == "" {
			return covered, true
		}
		covered[id] = true
	}
	return covered, false
}

// externalDistributionMissingTargets 给定账号里还没有 lane 的。
func externalDistributionMissingTargets(job publishJob, targets []externalAccount) []externalAccount {
	covered, all := externalDistributionCoveredAccounts(job)
	if all {
		return nil
	}
	var missing []externalAccount
	for _, target := range targets {
		if !covered[strings.TrimSpace(target.ID)] {
			missing = append(missing, target)
		}
	}
	return missing
}

// externalDistributionMissingAutoTargets 这篇能发的号里还没推过的 auto 账号。
func externalDistributionMissingAutoTargets(job publishJob, cfg externalTargets) []externalAccount {
	var auto []externalAccount
	for _, target := range cfg.forJob(job) {
		if target.Auto {
			auto = append(auto, target)
		}
	}
	return externalDistributionMissingTargets(job, auto)
}

// externalDistributionSummaryState 各 lane 汇总成一个状态，给卡片版本、回调提示与旧口径用：
// 有在推、在发、平台在审核的 → polling；全部在平台上、或停在草稿（对不上没发、改版前存的）→ succeeded；
// 有待核对 → reconciliation_required；有失败或没通过：有平台已成功 → partial_failed，否则 failed。
func externalDistributionSummaryState(job publishJob) string {
	lanes := job.ExternalDistributionLanes
	if len(lanes) == 0 {
		return ""
	}
	settled, failed, reconcile := 0, 0, 0
	for _, lane := range lanes {
		switch {
		case publishExternalDistributionProcessing(lane.State) || lane.State == publishExternalDistributionReviewing:
			return publishExternalDistributionPolling
		case externalLaneSettled(lane) || lane.State == publishExternalDistributionDraftSaved:
			settled++
		case lane.State == publishExternalDistributionReconciliationRequired:
			reconcile++
		default:
			failed++
		}
	}
	switch {
	case settled == len(lanes):
		return publishExternalDistributionSucceeded
	case reconcile > 0:
		return publishExternalDistributionReconciliationRequired
	case settled > 0:
		return publishExternalDistributionPartialFailed
	default:
		_ = failed
		return publishExternalDistributionFailed
	}
}

// externalLegacyUnresolved 云梭旧 lane 待核对时卡片与侧边栏的说法（旧回执里可能写着云梭，不显示）。
const externalLegacyUnresolved = "旧的送稿方式已停用，没拿到这条的结果；请到平台后台看有没有这篇，系统不会再推"

// retireYunsuoLanes 读盘时把云梭旧 lane 里没走完的转待核对（2026-10-24 云梭下线时线上一条都没有，
// 兜底）：不会再有人推、查它们，留在处理中会让 monitoring 一直报处理中。幂等。
func retireYunsuoLanes(j *publishJob) {
	for i := range j.ExternalDistributionLanes {
		lane := &j.ExternalDistributionLanes[i]
		if lane.Driver == externalDriverRobot || !publishExternalDistributionProcessing(lane.State) {
			continue
		}
		lane.State = publishExternalDistributionReconciliationRequired
		lane.Error = externalLegacyUnresolved
		lane.Remark = lane.Error
	}
}

// externalDistributionLaneSignature 卡片版本里各平台的状态，平台状态一变旧卡按钮即失效。
func externalDistributionLaneSignature(job publishJob) []string {
	if len(job.ExternalDistributionLanes) == 0 {
		return nil
	}
	out := make([]string, 0, len(job.ExternalDistributionLanes))
	for _, lane := range job.ExternalDistributionLanes {
		out = append(out, strings.TrimSpace(lane.AccountID)+":"+lane.State)
	}
	return out
}
