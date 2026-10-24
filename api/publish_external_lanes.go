package main

// publish_external_lanes.go — 外部分发按平台账号分道（lane）。
//
// 2026-10-12 用户裁定：各平台分开发布（「他们这个发布不应该是分开的吗？」）。此前一篇稿
// 一次推送带上所有账号、共用一个状态：一个平台失败会拖住另一个，人工发过头条的稿也没法
// 只补百家号。现在每个账号一条 lane，各自冻结 payload 与 intent、各自拿 content_pk、
// 各自轮询与对账。红线 37 的安全规则逐条落在 lane 上：没拿到 content_pk 的不确定结果
// 只人工对账（经分发服务的按幂等键查，publish_external_robot.go）；有单号只查询；只有明确
// 受理前失败的 lane 可以重推；推过的账号（不论结果）不会被自动外发再推。

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// publishExternalLaneManual 系统之外已经发过（人工发的头条稿等），只登记链接；
// 系统永不再推这个账号。
const publishExternalLaneManual = "manual"

type publishExternalLane struct {
	AccountID string `json:"account_id"`
	// YunsuoAccountID 2026-10-24 改挂到「平台账号」record_id 前的云梭账号编号：云梭回执按它对。
	YunsuoAccountID string `json:"yunsuo_account_id,omitempty"`
	Name            string `json:"name"`
	// Driver 建 lane 时这个平台的送稿方式；空是云梭（含改这个字段之前的所有 lane）。
	Driver externalDriver `json:"driver,omitempty"`
	// queued / pushing / polling / succeeded / failed / reconciliation_required / manual；
	// 经分发服务的不轮询，多一个终态 draft_saved。
	State          string `json:"state"`
	RequestedBy    string `json:"requested_by,omitempty"`
	RequestedAt    string `json:"requested_at,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
	ContentVersion int64  `json:"content_version,omitempty"`
	Attribution    string `json:"attribution,omitempty"`
	Source         string `json:"source,omitempty"`
	// Title 推到这个平台的标题（云梭按平台字数在三个长度里挑，这里记中间档），卡片列表显示。
	Title       string `json:"title,omitempty"`
	PayloadJSON string `json:"payload_json,omitempty"`
	PayloadHash string `json:"payload_hash,omitempty"`
	// IntentKey 外调前落盘；经分发服务的就是发给它的幂等键。
	IntentKey string `json:"intent_key,omitempty"`
	IntentAt  string `json:"intent_at,omitempty"`
	// ContentPK 云梭的单号；经分发服务的是平台上的草稿编号。
	ContentPK    string `json:"content_pk,omitempty"`
	Error        string `json:"error,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
	PollAttempts int    `json:"poll_attempts,omitempty"`
	LastPolledAt string `json:"last_polled_at,omitempty"`
	// 云梭对这个账号的最新回执。
	ProviderRecordID  string `json:"provider_record_id,omitempty"`
	PlatformName      string `json:"platform_name,omitempty"`
	PublishTime       string `json:"publish_time,omitempty"`
	RawStatus         int    `json:"raw_status,omitempty"`
	Remark            string `json:"remark,omitempty"`
	FailedCount       int    `json:"failed_count,omitempty"`
	ProviderUpdatedAt string `json:"provider_updated_at,omitempty"`
	// 文章在平台上的链接（云梭不回链接，成功后另行查找或人工登记）。
	URL string `json:"url,omitempty"`
	// DraftURL 平台上这篇草稿的编辑页（分发服务存好草稿后给的）。
	DraftURL     string `json:"draft_url,omitempty"`
	URLCheckedAt string `json:"url_checked_at,omitempty"`
	URLAttempts  int    `json:"url_attempts,omitempty"`
	// 拿到链接后取一次平台上的正文，与 PayloadJSON 比结构（publish_external_fidelity.go）。
	Fidelity publishExternalFidelity `json:"fidelity,omitzero"`
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

// yunsuoAccountID 云梭认的账号编号：改挂过的用改挂前那个。
func (l publishExternalLane) yunsuoAccountID() string {
	return strings.TrimSpace(firstNonEmpty(l.YunsuoAccountID, l.AccountID))
}

func externalLaneFor(job publishJob, accountID string) (publishExternalLane, bool) {
	if i := externalLaneIndex(job, accountID); i >= 0 {
		return job.ExternalDistributionLanes[i], true
	}
	return publishExternalLane{}, false
}

// externalLaneCanSafeRetry 失败且没拿到 content_pk：POST 之前失败或云梭明确拒绝，
// 修正后可安全重推。拿到单号的平台失败不走这里，避免重复稿。
func externalLaneCanSafeRetry(lane publishExternalLane) bool {
	return lane.State == publishExternalDistributionFailed && strings.TrimSpace(lane.ContentPK) == ""
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

// externalDistributionCoveredAccounts 有 lane 的账号（不论结果）。失败的也算：云梭对受理后
// 的失败会自己重试，再推一次就会重复。账号为空的 lane（迁移时缺账号的旧数据）视为覆盖全部。
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
// 有在推的 → polling；全部在平台上或已存草稿 → succeeded；有待核对 → reconciliation_required；
// 有失败：有平台已成功 → partial_failed，否则 failed。
func externalDistributionSummaryState(job publishJob) string {
	lanes := job.ExternalDistributionLanes
	if len(lanes) == 0 {
		return ""
	}
	settled, failed, reconcile := 0, 0, 0
	for _, lane := range lanes {
		switch {
		case publishExternalDistributionProcessing(lane.State):
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

// migrateLegacyExternalDistribution 把按平台拆开之前的单轮字段（含当天加的补发存档）
// 迁成 lanes，读盘时调用，幂等。共用一个 content_pk 的旧轮次拆成多条 lane 后各查各的账号；
// 还没推的旧 payload 按账号拆成单账号 payload，不会带着别的账号再推一次。
func migrateLegacyExternalDistribution(j *publishJob) {
	if j == nil || (j.LegacyExternalState == "" && len(j.LegacyExternalHistory) == 0) {
		return
	}
	if len(j.ExternalDistributionLanes) == 0 {
		var lanes []publishExternalLane
		for _, round := range j.LegacyExternalHistory {
			for _, target := range round.Targets {
				lanes = append(lanes, legacyExternalLane(target, round.State, publishExternalLane{
					RequestedBy: round.RequestedBy, RequestedAt: round.RequestedAt,
					CompletedAt: round.CompletedAt, ContentPK: round.ContentPK, PayloadHash: round.PayloadHash,
				}))
			}
		}
		if state := strings.TrimSpace(j.LegacyExternalState); state != "" {
			base := publishExternalLane{
				RequestedBy: j.LegacyExternalRequestedBy, RequestedAt: j.LegacyExternalRequestedAt,
				ContentHash: j.LegacyExternalContentHash, ContentVersion: j.LegacyExternalContentVersion,
				Attribution: j.LegacyExternalAttribution, Source: j.LegacyExternalSource,
				PayloadJSON: j.LegacyExternalPayloadJSON, PayloadHash: j.LegacyExternalPayloadHash,
				IntentKey: j.LegacyExternalIntentKey, IntentAt: j.LegacyExternalIntentAt,
				ContentPK: j.LegacyExternalContentPK, Error: j.LegacyExternalError,
				Title:       legacyExternalPayloadTitle(j.LegacyExternalPayloadJSON),
				CompletedAt: j.LegacyExternalCompletedAt, PollAttempts: j.LegacyExternalPollAttempts,
				LastPolledAt: j.LegacyExternalLastPolledAt,
			}
			if len(j.LegacyExternalTargets) == 0 {
				// 没有账号的旧轮次：记一条空账号 lane，所有账号都算推过，绝不自动再推。
				lane := base
				lane.State = legacyExternalLaneState(state, "", base.ContentPK)
				lane.Name = "未知平台"
				lanes = append(lanes, lane)
			}
			for _, target := range j.LegacyExternalTargets {
				lane := legacyExternalLane(target, state, base)
				if lane.State == publishExternalDistributionQueued && base.PayloadJSON != "" {
					lane.PayloadJSON, lane.PayloadHash = legacyExternalSingleAccountPayload(base.PayloadJSON, target.AccountID)
				}
				lanes = append(lanes, lane)
			}
		}
		j.ExternalDistributionLanes = lanes
	}
	j.LegacyExternalState = ""
	j.LegacyExternalTargets = nil
	j.LegacyExternalRequestedBy, j.LegacyExternalRequestedAt = "", ""
	j.LegacyExternalContentHash, j.LegacyExternalContentVersion = "", 0
	j.LegacyExternalAttribution, j.LegacyExternalSource = "", ""
	j.LegacyExternalPayloadJSON, j.LegacyExternalPayloadHash = "", ""
	j.LegacyExternalIntentKey, j.LegacyExternalIntentAt = "", ""
	j.LegacyExternalContentPK, j.LegacyExternalError, j.LegacyExternalCompletedAt = "", "", ""
	j.LegacyExternalPollAttempts, j.LegacyExternalLastPolledAt = 0, ""
	j.LegacyExternalHistory = nil
}

func legacyExternalLane(target publishExternalDistributionTarget, roundState string, base publishExternalLane) publishExternalLane {
	lane := base
	lane.AccountID = strings.TrimSpace(target.AccountID)
	lane.Name = target.Name
	lane.ProviderRecordID = target.ProviderRecordID
	lane.PlatformName = target.PlatformName
	lane.PublishTime = target.PublishTime
	lane.RawStatus = target.RawStatus
	lane.Remark = target.Remark
	lane.FailedCount = target.FailedCount
	lane.ProviderUpdatedAt = target.UpdatedAt
	lane.State = legacyExternalLaneState(roundState, target.State, base.ContentPK)
	return lane
}

// legacyExternalLaneState 旧轮次状态 + 该账号的回执 → lane 状态。账号回执已是终态的以回执为准；
// 其余沿用整轮状态（推送中、对账中都原样保留，恢复与补查照旧处理）。
func legacyExternalLaneState(roundState, targetState, contentPK string) string {
	switch targetState {
	case publishExternalTargetSucceeded:
		return publishExternalDistributionSucceeded
	case publishExternalTargetFailed, publishExternalTargetUnsupported:
		if roundState == publishExternalDistributionReconciliationRequired && strings.TrimSpace(contentPK) == "" {
			return publishExternalDistributionReconciliationRequired
		}
		if roundState == publishExternalDistributionPushing || roundState == publishExternalDistributionQueued {
			return roundState
		}
		return publishExternalDistributionFailed
	}
	switch roundState {
	case publishExternalDistributionPartialFailed:
		// 部分失败里没有终态回执的账号：按失败处理（有单号会继续补查）。
		return publishExternalDistributionFailed
	case publishExternalDistributionSucceeded:
		return publishExternalDistributionSucceeded
	}
	return roundState
}

func legacyExternalPayloadTitle(payloadJSON string) string {
	var payload yunsuoPushPayload
	if json.Unmarshal([]byte(payloadJSON), &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Title)
}

func legacyExternalSingleAccountPayload(payloadJSON, accountID string) (string, string) {
	var payload yunsuoPushPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		// 解析不了就原样保留，推送时会以「无法解析」安全失败，不会外调。
		return payloadJSON, ""
	}
	payload.AccountIDs = []string{strings.TrimSpace(accountID)}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return payloadJSON, ""
	}
	digest := sha256.Sum256(encoded)
	return string(encoded), fmt.Sprintf("sha256:%x", digest[:])
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
