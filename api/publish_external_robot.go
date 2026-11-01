package main

// publish_external_robot.go — 经自己的分发服务（distribution_robot.go）送稿的 lane：百家号、头条号（2026-10-24 起）。
//
// 2026-10-30 用户：「整个流程可以开始做自动同步了：我们的原创文章发出来就自动同步，不需要存草稿，也不需要
// 别的操作。自动同步之后，把文档链接返回到卡片上，方便点击查看啊」。经分发服务的 lane 一律存草稿、发布、
// 跟到出结果，不论是自动外发还是卡片上点的（「自动外发」只管要不要自己开始）：
//
//	queued → pushing（提交前先落 intent，IntentKey 就是发给分发服务的幂等键）
//	  → publishing：草稿存好、读回对得上，同一次写入记下草稿编号、地址、读回核对并转发布中，写完再调发布；
//	  → draft_saved：读回与原文对不上，故意不发，卡片说清楚为什么、给草稿链接；
//	  → failed：点「存草稿」之前就被拒（参数、token、封面、没登录），没建稿，卡片可以重新同步；
//	  → reconciliation_required：草稿箱里同名新稿不止一篇（要人去删），或结果不明（按同一个键查）。
//	publishing → reviewing（平台审核中、定时待发）→ succeeded（URL 是文章链接，卡片给「查看文章」）
//	  → rejected：平台没通过，终态，卡片写平台给的原因；
//	  → failed：发布明确没做成，卡片重试按原来的幂等键再交（分发服务认出存好的那篇），再发一次；
//	  → reconciliation_required：点下去没看清、草稿被人改过、平台上这篇不能发了，要人去后台看。
//
// 存草稿结果不明的按同一个键 GET，存好了照样转发布中再发。发布中、审核中的每分钟 GET 一次（只读分发服务的
// 记录，不碰平台；平台由分发服务自己接着查），一变就写回 lane、刷新审核卡；说不准（断网、超时、平台出错）
// 的留在发布中下一轮再查。发布中而分发服务那边没有这次的发布动作（进程在落盘与调发布之间退出、卡片重试后）
// 才再调一次发布：分发服务点下去以后绝不自动再点，同一篇再调只会等正在做的那次，所以不会发两遍。
// 2026-10-30 之前只存了草稿的 lane 停在 draft_saved，不补发。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// publishExternalDistributionDraftSaved 平台上存好了草稿、故意没发：分发服务读回与原文对不上，或是
// 2026-10-30 之前只存草稿时存的。不算「已在平台上」：人去平台改好发出去后，用人工登记接口记成人工已发。
const publishExternalDistributionDraftSaved = "draft_saved"

// robotDraftPayload 冻结在 lane 上的草稿内容（PayloadJSON）。Platform 是分发服务里的平台 id，
// Blocks 是外发通用版正文块（distribution_blocks.go），分发服务按平台写成编辑器的标签。
// HTML 只在 2026-10-24 之前冻结的旧 lane 上有（那时排版工具自己写好网页代码）：只按原来的幂等键查，不再提交。
type robotDraftPayload struct {
	Platform string  `json:"platform"`
	Title    string  `json:"title"`
	Blocks   []Block `json:"blocks,omitempty"`
	HTML     string  `json:"html,omitempty"`
	CoverURL string  `json:"cover_url"`
}

// buildRobotDraftPayload 通用正文规则与导流尾拦截；标题原样（人给这个平台改过的优先），封面单独上传、不进正文。
// 平台限制还没从分发服务拿到的不推：没检查过不算通过。
func buildRobotDraftPayload(job publishJob, profile distributionPlatform, draft publishExternalDraft) (robotDraftPayload, error) {
	if !profile.Known {
		return robotDraftPayload{}, errors.New(profile.unknownNote())
	}
	content, err := buildDistributionContent(job, profile, draft)
	if err != nil {
		return robotDraftPayload{}, err
	}
	if leak := publishHTMLWechatTrailerLeak(distributionBlocksText(content.Blocks)); leak != "" {
		return robotDraftPayload{}, fmt.Errorf("%s草稿正文仍含公众号导流尾（%s），禁止外发", profile.Name, leak)
	}
	title := externalEffectiveTitle(job, draft)
	if min, max := profile.titleLimits(); profile.titleLength(title) < min || profile.titleLength(title) > max {
		note := profile.titleCountNote()
		if note != "" {
			note = "（" + note + "）"
		}
		return robotDraftPayload{}, fmt.Errorf("%s标题要 %d–%d 字%s，现在 %d 字", profile.Name, min, max, note, profile.titleLength(title))
	}
	if content.Cover == "" {
		return robotDraftPayload{}, fmt.Errorf("%s草稿缺少公开可访问的封面", profile.Name)
	}
	if width, height, small := profile.coverTooSmall(content.Cover); small {
		return robotDraftPayload{}, fmt.Errorf("%s封面图尺寸过小（%dx%d），要求不小于 %dx%d，请换封面后再同步",
			profile.Name, width, height, profile.CoverMinWidth, profile.CoverMinHeight)
	}
	return robotDraftPayload{Platform: profile.Key, Title: title, Blocks: content.Blocks, CoverURL: content.Cover}, nil
}

// buildExternalLanePayload 冻结这条 lane 的草稿内容：返回 JSON 与存到平台上的标题。
func buildExternalLanePayload(job publishJob, target externalAccount, draft publishExternalDraft) ([]byte, string, error) {
	payload, err := buildRobotDraftPayload(job, platformForAccount(target), draft)
	if err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(payload)
	return encoded, payload.Title, err
}

// robotIdempotencyKey 同一份冻结内容同一个键：卡片重试、管理员重发、进程重启后再提交，分发服务都只认
// 一篇；内容变了 PayloadHash 就变、换键。只用分发服务认的字符（A-Za-z0-9._:-），不超过 128 位。
func robotIdempotencyKey(jobID int64, accountID, payloadHash string) string {
	raw := fmt.Sprintf("job-%d.%s.%s", jobID, strings.TrimSpace(accountID), strings.TrimPrefix(payloadHash, "sha256:"))
	key := []rune(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == ':', r == '-':
			return r
		default:
			return '-'
		}
	}, raw))
	if len(key) > 128 {
		key = key[:128]
	}
	return string(key)
}

// robotResendFrozenLane 同一份冻结内容、同一个幂等键再交一次：管理员重发交过分发服务的待核对 lane（分发
// 服务先去草稿箱对账，剩一篇就认它，全删了才重存），或卡片重试草稿已存好、发布没做成的 lane（分发服务认出
// 存好的那篇，再发一次）。按现在的代码重建会换键，平台上就会多出一篇。ActionID 留着：再看到上次那次发布
// 的失败或没看清，认得出是旧结果。
func robotResendFrozenLane(jobID int64, old publishExternalLane, actor, requestedAt string) (publishExternalLane, error) {
	var payload robotDraftPayload
	if err := json.Unmarshal([]byte(old.PayloadJSON), &payload); err != nil || len(payload.Blocks) == 0 ||
		robotIdempotencyKey(jobID, old.AccountID, old.PayloadHash) != old.IntentKey {
		// 2026-10-24 前按旧格式交的：分发服务已不收旧格式，没法按原来的键再问，也不能换键重存。
		return old, errors.New("上次是按旧格式交给分发服务的，没法按原来的幂等键再核对；请到" + old.Name + "草稿箱核对后人工处理")
	}
	lane := old
	lane.State = publishExternalDistributionQueued
	lane.RequestedBy, lane.RequestedAt = strings.TrimSpace(actor), requestedAt
	lane.IntentKey, lane.IntentAt = "", ""
	lane.Error, lane.Remark, lane.CompletedAt = "", "", ""
	lane.PollAttempts, lane.LastPolledAt = 0, ""
	return lane, nil
}

func (p *publishPipeline) robotClient() (distributionRobotAPI, error) {
	if p.distributionRobot != nil {
		return p.distributionRobot()
	}
	return newDistributionRobotClientFromEnv()
}

// robotPayloadProfile 冻结内容里记的平台；平台限制没拿到时名字退回平台编号，只影响提示里的平台名。
func robotPayloadProfile(payload robotDraftPayload) distributionPlatform {
	return platformByKey(payload.Platform)
}

func (p *publishPipeline) pushRobotLane(job publishJob, lane publishExternalLane) {
	var payload robotDraftPayload
	if err := json.Unmarshal([]byte(lane.PayloadJSON), &payload); err != nil || strings.TrimSpace(payload.Platform) == "" {
		p.failExternalLaneBeforePush(job.ID, lane.AccountID, "已保存的草稿内容无法解析，未提交")
		return
	}
	if len(payload.Blocks) == 0 {
		// 2026-10-24 之前冻结的旧格式（排版工具写好的网页代码），分发服务已不收；还没提交过，重新存一次就按新格式冻结。
		p.failExternalLaneBeforePush(job.ID, lane.AccountID, "草稿内容是旧格式，未提交；请重新"+robotPayloadProfile(payload).syncAction(lane.Name))
		return
	}
	client, err := p.robotClient()
	if err != nil {
		p.failExternalLaneBeforePush(job.ID, lane.AccountID, err.Error())
		return
	}
	intentKey := robotIdempotencyKey(job.ID, lane.AccountID, lane.PayloadHash)
	if !p.claimExternalLanePush(job.ID, lane, intentKey) {
		return
	}
	record, saveErr := client.SaveDraft(context.Background(), payload.Platform, robotDraftRequest{
		IdempotencyKey: intentKey, Account: lane.AccountID, Title: payload.Title, Blocks: payload.Blocks, CoverURL: payload.CoverURL,
	})
	profile := robotPayloadProfile(payload)
	result := robotLaneResultOf(lane, record, saveErr, profile)
	if result.State == "" {
		result.State = publishExternalDistributionReconciliationRequired
		result.Reason = "分发工具没有给出确定结果（" + result.Reason + "），系统会按同一个幂等键向分发工具核对，不会重复存稿"
	}
	stamp := pipelineNow(p).Format(time.RFC3339)
	_, swapped, err := p.updateExternalLane(job.ID, lane.AccountID, func(current publishExternalLane) bool {
		return current.State == publishExternalDistributionPushing && current.IntentKey == intentKey
	}, func(current *publishJob, l *publishExternalLane) {
		applyRobotLaneResult(l, result, profile, stamp)
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil || !swapped {
		// 结果没落盘：lane 留在 pushing，启动恢复会按同一个键去查。
		log.Printf("publish: record %s draft result for job %d failed: %v", profile.Name, job.ID, err)
		return
	}
	p.scheduleExternalDistributionCardUpdate(job.ID)
	switch {
	case result.Publish:
		p.publishRobotLane(job.ID, lane.AccountID, client)
	case result.Uncertain:
		// 平台出错这类结果，分发服务其实已经定了（记成失败或已对账认领）：当场查一次，不用等兜底循环。
		p.resolveRobotLane(job.ID, lane.AccountID)
	}
}

// robotLaneResult 分发服务的一次回答落到 lane 上是什么。State 为空：还没定，lane 不动。
type robotLaneResult struct {
	State  string
	Reason string
	Record robotDraftRecord
	// Saved 这次回答带着刚存好的草稿：记草稿编号、地址与读回核对。
	Saved bool
	// Publish 草稿存好了、分发服务那边还没有这次的发布：要调一次发布。
	Publish bool
	// URL 已发布的文章链接（按平台域名校验过）。
	URL string
	// ActionID 这个结论出自分发服务的哪一次发布动作。
	ActionID  string
	Uncertain bool
}

// robotLaneResultOf 分发服务的一次回答（存草稿、发布或 GET）落到这条 lane 上是什么：发布中、审核中的看
// 发布动作与平台现状，其余看草稿存没存成。
func robotLaneResultOf(lane publishExternalLane, record robotDraftRecord, err error, profile distributionPlatform) robotLaneResult {
	if lane.State == publishExternalDistributionPublishing || lane.State == publishExternalDistributionReviewing {
		return robotPublishResultOf(lane, record, err, profile)
	}
	return robotSaveResultOf(record, err, profile)
}

func robotSaveResultOf(record robotDraftRecord, err error, profile distributionPlatform) robotLaneResult {
	var rejected *robotRejectedError
	var reconcile *robotReconcileError
	switch {
	case err == nil && record.Status == robotDraftSaved && record.ArticleID != "":
		if robotDraftMismatched(record) {
			return robotLaneResult{State: publishExternalDistributionDraftSaved, Reason: robotDraftHeldReason(record), Record: record, Saved: true}
		}
		return robotLaneResult{State: publishExternalDistributionPublishing, Record: record, Saved: true, Publish: true}
	case errors.As(err, &rejected):
		return robotLaneResult{State: publishExternalDistributionFailed, Reason: robotRejectedReason(rejected, profile, "没存草稿")}
	case errors.As(err, &reconcile):
		return robotLaneResult{State: publishExternalDistributionReconciliationRequired, Reason: reconcile.Message}
	case errors.Is(err, errRobotDraftNotFound):
		return robotLaneResult{State: publishExternalDistributionFailed, Reason: "分发工具没收到这次请求，没有存草稿，可以重新同步"}
	case err != nil:
		return robotLaneResult{Reason: err.Error(), Uncertain: true}
	case record.Status == robotDraftFailed:
		return robotLaneResult{State: publishExternalDistributionFailed, Reason: robotRecordError(record, "分发工具记为没存成") + "；可以重新同步"}
	case record.Status == robotDraftUnknown:
		return robotLaneResult{State: publishExternalDistributionReconciliationRequired, Reason: robotRecordError(record, "草稿箱里的结果要人核对")}
	default:
		return robotLaneResult{Reason: "分发工具还在存草稿（" + firstNonEmpty(record.Status, "没有状态") + "）"}
	}
}

// robotPublishResultOf 发布中、审核中的 lane：先看分发服务回的错，再看这次的发布动作，动作做成了看平台现状。
func robotPublishResultOf(lane publishExternalLane, record robotDraftRecord, err error, profile distributionPlatform) robotLaneResult {
	if err != nil {
		var rejected *robotRejectedError
		var reconcile *robotReconcileError
		var uncertain *robotUncertainError
		switch {
		case errors.As(err, &reconcile):
			// 分发服务说要人看（outcome human）。
			return robotLaneResult{State: publishExternalDistributionReconciliationRequired, Reason: reconcile.Message, ActionID: robotRecordActionID(reconcile.Record)}
		case errors.As(err, &rejected):
			switch rejected.Code {
			case "busy":
				// 这篇正在做别的动作：留在发布中，下一轮再看。
				return robotLaneResult{Reason: rejected.Message, Uncertain: true}
			case "draft_changed", "wrong_state", "not_saved", "not_found":
				// 平台上这篇被人改过、已不是草稿，或分发服务里对不上：不能自己再发，要人去后台看。
				return robotLaneResult{
					State:    publishExternalDistributionReconciliationRequired,
					Reason:   "分发工具没发布：" + rejected.Message + "；请到" + profile.Name + "后台核对",
					ActionID: robotRecordActionID(rejected.Record),
				}
			}
			return robotLaneResult{State: publishExternalDistributionFailed, Reason: robotRejectedReason(rejected, profile, "没发布"), ActionID: robotRecordActionID(rejected.Record)}
		case errors.Is(err, errRobotDraftNotFound):
			return robotLaneResult{State: publishExternalDistributionReconciliationRequired, Reason: "分发工具里查不到这篇草稿的记录；请到" + profile.Name + "后台核对"}
		case errors.As(err, &uncertain) && uncertain.Record != nil:
			record = *uncertain.Record
		default:
			return robotLaneResult{Reason: err.Error(), Uncertain: true}
		}
	}
	action := record.Action
	if !robotActionCurrent(lane, action) {
		if lane.State == publishExternalDistributionReviewing {
			return robotPlatformResultOf(record, profile, "")
		}
		return robotLaneResult{State: publishExternalDistributionPublishing, Publish: true}
	}
	switch action.Status {
	case robotActionPending, robotActionDoing:
		return robotLaneResult{State: publishExternalDistributionPublishing, ActionID: action.ID}
	case robotActionFailed:
		return robotLaneResult{
			State:    publishExternalDistributionFailed,
			Reason:   "分发工具没发出去：" + firstNonEmpty(safeActionText(action.Error), "没说原因") + "；可以重新同步",
			ActionID: action.ID,
		}
	case robotActionDone:
		return robotPlatformResultOf(record, profile, action.ID)
	default:
		return robotLaneResult{
			State:    publishExternalDistributionReconciliationRequired,
			Reason:   "分发工具记的发布状态「" + action.Status + "」排版工具不认得；请到" + profile.Name + "后台核对",
			ActionID: action.ID,
		}
	}
}

// robotActionCurrent 记录里最近一次按的是这次的发布：上次那次（同一个编号）的失败不算，这次还没发。
func robotActionCurrent(lane publishExternalLane, action *robotDraftAction) bool {
	if action == nil || action.Kind != robotActionPublish {
		return false
	}
	stale := action.ID != "" && action.ID == lane.ActionID && action.Status == robotActionFailed
	return !stale
}

// robotPlatformResultOf 发布做成之后，平台上这篇现在的样子。
func robotPlatformResultOf(record robotDraftRecord, profile distributionPlatform, actionID string) robotLaneResult {
	state := robotDraftState{}
	if record.State != nil {
		state = *record.State
	}
	switch state.Value {
	case robotStatePublished:
		raw := firstNonEmpty(record.ArticleURL, state.URL)
		if raw == "" {
			return robotLaneResult{
				State:    publishExternalDistributionReconciliationRequired,
				Reason:   profile.Name + "上已发布，但分发工具没拿到文章链接；请到" + profile.Name + "后台找到这篇、登记链接",
				ActionID: actionID,
			}
		}
		if !profile.Known {
			// 平台限制（含文章域名）还没从分发服务拿到：认不了链接，留着下一轮再认。
			return robotLaneResult{Reason: profile.unknownNote(), Uncertain: true}
		}
		articleURL, err := validateExternalArticleURL(raw, profile)
		if err != nil {
			return robotLaneResult{
				State:    publishExternalDistributionReconciliationRequired,
				Reason:   profile.Name + "上已发布，但分发工具给的文章链接不对（" + err.Error() + "）；请到" + profile.Name + "后台核对后登记链接",
				ActionID: actionID,
			}
		}
		return robotLaneResult{State: publishExternalDistributionSucceeded, URL: articleURL, ActionID: actionID}
	case robotStateReviewing, robotStateScheduled:
		return robotLaneResult{State: publishExternalDistributionReviewing, ActionID: actionID}
	case robotStateRejected:
		return robotLaneResult{State: publishExternalDistributionRejected, Reason: firstNonEmpty(safeActionText(state.Reason), "平台没给原因"), ActionID: actionID}
	case "":
		return robotLaneResult{
			State:    publishExternalDistributionReconciliationRequired,
			Reason:   "分发工具没查到" + profile.Name + "上这篇现在的样子；请到" + profile.Name + "后台核对",
			ActionID: actionID,
		}
	default:
		return robotLaneResult{
			State:    publishExternalDistributionReconciliationRequired,
			Reason:   profile.Name + "上这篇现在是「" + firstNonEmpty(safeActionText(state.Raw), state.Value) + "」；请到" + profile.Name + "后台核对",
			ActionID: actionID,
		}
	}
}

func robotRecordActionID(record *robotDraftRecord) string {
	if record == nil || record.Action == nil || record.Action.Kind != robotActionPublish {
		return ""
	}
	return record.Action.ID
}

// safeActionText 分发服务记下的原因（来自平台页面）压成一行；空就是空。
func safeActionText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return safeDistributionMessage(text)
}

// robotDraftMismatched 分发服务存完当场读回、全对上才发：对不上的不发，读回本身失败（没核对过）的也不发。
func robotDraftMismatched(record robotDraftRecord) bool {
	return len(record.Mismatches) > 0 || record.Match == nil || !*record.Match
}

// robotDraftHeldReason 读回对不上或没读成、没发时卡片上那句。
func robotDraftHeldReason(record robotDraftRecord) string {
	if record.Match == nil && len(record.Mismatches) == 0 {
		reason := "草稿存好后没读回来核对，没有发布"
		if record.ReadBackError != nil && strings.TrimSpace(*record.ReadBackError) != "" {
			reason += "：" + safeDistributionMessage(*record.ReadBackError)
		}
		return reason
	}
	reason := "草稿与原文对不上，没有发布"
	if len(record.Mismatches) > 0 {
		reason += "：" + strings.Join(record.Mismatches, "；")
	}
	return reason
}

func robotRecordError(record robotDraftRecord, fallback string) string {
	if record.Error != nil && strings.TrimSpace(*record.Error) != "" {
		return safeDistributionMessage(*record.Error)
	}
	return fallback
}

// robotRejectedReason 分发服务没动手时卡片上那句；what 是「没存草稿」「没发布」。
func robotRejectedReason(err *robotRejectedError, profile distributionPlatform, what string) string {
	switch {
	case err.Code == "login_required":
		return "服务器上的" + profile.Name + "没登录，请在分发工具的远程画面里登录后重新同步"
	case err.Status == 401:
		return "分发工具不认这个接口 token（" + distributionAPITokenEnv + "），请查配置后重新同步"
	default:
		return "分发工具" + what + "：" + err.Message
	}
}

// applyRobotLaneResult 锁内把定了的结果写进 lane；State 为空不调用。
func applyRobotLaneResult(l *publishExternalLane, result robotLaneResult, profile distributionPlatform, now string) {
	l.State = result.State
	l.Error = robotLaneReason(result.Reason)
	l.Remark = l.Error
	l.CompletedAt = now
	if result.ActionID != "" {
		l.ActionID = result.ActionID
	}
	if result.URL != "" {
		l.URL = result.URL
	}
	if !result.Saved {
		return
	}
	l.ContentPK = result.Record.ArticleID
	l.DraftURL = ""
	if draftURL, err := validateExternalArticleURL(result.Record.DraftURL, profile); err == nil {
		l.DraftURL = draftURL
	} else if !profile.Known && strings.HasPrefix(result.Record.DraftURL, "https://") {
		// 平台限制没拿到（不知道域名）也留着：地址是自己的分发服务给的，丢了人就找不到这篇草稿。
		l.DraftURL = strings.TrimSpace(result.Record.DraftURL)
	} else if result.Record.DraftURL != "" {
		log.Printf("publish: 分发工具返回的草稿地址不在%s域名内，不写进卡片：%v", profile.Name, err)
	}
	l.Fidelity = robotDraftFidelity(result.Record, profile.Name, now)
}

func robotLaneReason(reason string) string {
	return truncateRunes(strings.Join(strings.Fields(reason), " "), 500)
}

// robotLaneResultSame 结论和 lane 上记的一样（发布中还在做、审核中还在审）：不写盘、不刷卡。
func robotLaneResultSame(lane publishExternalLane, result robotLaneResult) bool {
	return lane.State == result.State && lane.Error == robotLaneReason(result.Reason) && (result.URL == "" || result.URL == lane.URL)
}

// robotDraftFidelity 分发服务存完草稿当场重新打开读回，与送去的正文逐项比；这就是这条 lane 的保真核对。
// 读回失败的分发服务不会再读，按取满次数记，monitoring 报「取不到」。
// 有哪步改点了模型挑的元素，也写进 Note（读回失败时一样写），卡片上那行灰字提醒去改分发服务的找法。
func robotDraftFidelity(record robotDraftRecord, platform, now string) publishExternalFidelity {
	check := publishExternalFidelity{
		Attempts: 1, LastTriedAt: now,
		Expected: externalFidelityShape{Headings: record.Expected.Headings, Images: record.Expected.Images, CaptionedImages: len(record.Expected.Captions)},
	}
	notes := []string{}
	if assisted := robotDraftAssistNote(record.Assists, platform); assisted != "" {
		notes = append(notes, assisted)
	}
	readBackError := ""
	if record.ReadBackError != nil {
		readBackError = strings.TrimSpace(*record.ReadBackError)
	}
	if record.ReadBack == nil || readBackError != "" {
		check.Attempts = externalFidelityMaxAttempts
		check.Error = "分发工具没有读回这篇草稿"
		if readBackError != "" {
			check.Error = "读回草稿失败：" + safeDistributionMessage(readBackError)
		}
		check.Note = strings.Join(notes, "；")
		return check
	}
	check.CheckedAt = now
	check.Online = externalFidelityShape{Headings: record.ReadBack.Headings, Images: record.ReadBack.Images, CaptionedImages: record.ReadBack.Captions}
	if len(record.Mismatches) > 0 || (record.Match != nil && !*record.Match) {
		mismatch := "草稿读回对不上"
		if len(record.Mismatches) > 0 {
			mismatch += "：" + strings.Join(record.Mismatches, "；")
		}
		notes = append(notes, mismatch)
	}
	check.Note = strings.Join(notes, "；")
	return check
}

// robotDraftAssistNote 「百家号改版：找不到「更换」，模型改点了「替换封面」」，几步就几句。
func robotDraftAssistNote(assists []robotDraftAssist, platform string) string {
	steps := []string{}
	for _, a := range assists {
		wanted := []string{}
		for _, w := range a.Wanted {
			if w = strings.TrimSpace(w); w != "" {
				wanted = append(wanted, "「"+w+"」")
			}
		}
		picked := strings.TrimSpace(a.Picked)
		if len(wanted) == 0 || picked == "" {
			continue
		}
		steps = append(steps, "找不到"+strings.Join(wanted, "或")+"，模型改点了「"+picked+"」")
	}
	if len(steps) == 0 {
		return ""
	}
	return safeDistributionMessage(platform + "改版：" + strings.Join(steps, "；"))
}

// robotResolveWithin 存草稿结果不明的 lane 推送后多久内还按幂等键去查。
const robotResolveWithin = 45 * 24 * time.Hour

// robotLaneAwaitingResult 存草稿结果不明、还在补查期内的分发服务 lane（只查不提交，可以每轮都查）。
func robotLaneAwaitingResult(lane publishExternalLane, now time.Time) bool {
	if lane.Driver != externalDriverRobot || lane.State != publishExternalDistributionReconciliationRequired ||
		strings.TrimSpace(lane.IntentKey) == "" || strings.TrimSpace(lane.ContentPK) != "" {
		return false
	}
	requestedAt, err := time.Parse(time.RFC3339, lane.RequestedAt)
	return err == nil && now.Sub(requestedAt) <= robotResolveWithin
}

// robotLanePayload 冻结在 lane 上的草稿内容与它的平台。
func robotLanePayload(lane publishExternalLane) (robotDraftPayload, distributionPlatform, bool) {
	var payload robotDraftPayload
	if err := json.Unmarshal([]byte(lane.PayloadJSON), &payload); err != nil || strings.TrimSpace(payload.Platform) == "" {
		return payload, distributionPlatform{}, false
	}
	return payload, robotPayloadProfile(payload), true
}

// resolveRobotLane 按同一个幂等键问分发服务这条 lane 的草稿存没存成。
func (p *publishPipeline) resolveRobotLane(jobID int64, accountID string) {
	job, err := p.store.byID(jobID)
	if err != nil {
		return
	}
	lane, ok := externalLaneFor(job, accountID)
	if !ok || !robotLaneAwaitingResult(lane, pipelineNow(p)) {
		return
	}
	client, err := p.robotClient()
	if err != nil {
		log.Printf("publish: 无法向分发工具核对任务 #%d %s：%v", jobID, lane.Name, err)
		return
	}
	p.resolveRobotLaneWithClient(jobID, lane, client)
}

func (p *publishPipeline) resolveRobotLaneWithClient(jobID int64, lane publishExternalLane, client distributionRobotAPI) {
	payload, profile, ok := robotLanePayload(lane)
	if !ok {
		log.Printf("publish: 任务 #%d %s 的草稿内容无法解析，不能向分发工具核对", jobID, lane.Name)
		return
	}
	record, getErr := client.Draft(context.Background(), payload.Platform, lane.IntentKey)
	result := robotLaneResultOf(lane, record, getErr, profile)
	if result.Uncertain {
		log.Printf("publish: 向分发工具核对任务 #%d %s 未果：%s", jobID, lane.Name, result.Reason)
	}
	stamp := pipelineNow(p).Format(time.RFC3339)
	changed := false
	_, swapped, err := p.updateExternalLane(jobID, lane.AccountID, func(current publishExternalLane) bool {
		return current.Driver == externalDriverRobot && current.State == publishExternalDistributionReconciliationRequired &&
			current.IntentKey == lane.IntentKey
	}, func(current *publishJob, l *publishExternalLane) {
		l.PollAttempts++
		l.LastPolledAt = stamp
		switch {
		case result.State == "":
		case result.State == publishExternalDistributionReconciliationRequired && (result.Reason == "" || result.Reason == l.Error):
		default:
			applyRobotLaneResult(l, result, profile, stamp)
			changed = true
			enqueueExternalDistributionCardUpdate(current)
		}
	})
	if err != nil {
		log.Printf("publish: 分发工具核对结果落盘失败（任务 #%d %s）：%v", jobID, lane.Name, err)
		return
	}
	if swapped && changed {
		log.Printf("publish: 分发工具核对更新（任务 #%d %s）：→ %s", jobID, lane.Name, result.State)
		p.scheduleExternalDistributionCardUpdate(jobID)
		if result.Publish {
			p.publishRobotLane(jobID, lane.AccountID, client)
		}
	}
}

// recoverRobotLane 进程在提交窗口里退出：分发服务可能存了也可能没存，转待核对后按同一个键去查。
func (p *publishPipeline) recoverRobotLane(job publishJob, lane publishExternalLane) {
	p.finishExternalLanePushFailure(job.ID, lane.AccountID, lane.IntentKey, publishExternalDistributionReconciliationRequired,
		"进程在存草稿时中断，系统会按同一个幂等键向分发工具核对，不会重复存稿")
	p.spawnExternalWork(func() { p.resolveRobotLane(job.ID, lane.AccountID) })
}

func robotPublishFlight(jobID int64, accountID string) string {
	return fmt.Sprintf("%d/%s", jobID, strings.TrimSpace(accountID))
}

// robotPublishActorOpenID 飞书 open_id（侧边栏记的是「名字（open_id）」）。
var robotPublishActorOpenID = regexp.MustCompile(`ou_[A-Za-z0-9_-]+`)

// robotPublishActor 记进分发服务这次发布动作的人：人点的（卡片、侧边栏）是他的 open_id，系统开始的
// （自动外发、补发、管理员重发）记成自动外发。
func robotPublishActor(lane publishExternalLane) string {
	requestedBy := strings.TrimSpace(lane.RequestedBy)
	if id := robotPublishActorOpenID.FindString(requestedBy); id != "" {
		return id
	}
	if requestedBy == "" || strings.HasPrefix(requestedBy, "system:") {
		return publishExternalAutoActor
	}
	return requestedBy
}

// publishRobotLane 发布中的 lane 调一次分发服务发布，回答写回 lane。同一条 lane 同一时间只调一次；
// 说不准的留在发布中，跟进时再查。
func (p *publishPipeline) publishRobotLane(jobID int64, accountID string, client distributionRobotAPI) {
	flight := robotPublishFlight(jobID, accountID)
	if _, busy := p.robotPublishing.LoadOrStore(flight, struct{}{}); busy {
		return
	}
	defer p.robotPublishing.Delete(flight)
	job, err := p.store.byID(jobID)
	if err != nil {
		return
	}
	lane, ok := externalLaneFor(job, accountID)
	if !ok || !robotLaneFollowed(lane) || lane.State != publishExternalDistributionPublishing {
		return
	}
	payload, profile, ok := robotLanePayload(lane)
	if !ok {
		log.Printf("publish: 任务 #%d %s 的草稿内容无法解析，不能发布", jobID, lane.Name)
		return
	}
	record, publishErr := client.Publish(context.Background(), payload.Platform, lane.IntentKey, robotPublishActor(lane))
	result := robotLaneResultOf(lane, record, publishErr, profile)
	if result.State == "" || result.Publish {
		log.Printf("publish: 任务 #%d %s 发布的结果还没定，跟进时再查：%s", jobID, lane.Name, firstNonEmpty(result.Reason, "分发工具那边还没有这次发布"))
		return
	}
	p.applyRobotFollowResult(jobID, lane, result, profile)
}

// robotLaneFollowed 发布中、平台审核中的分发服务 lane：每分钟按同一个键 GET 一次，直到有结果。
func robotLaneFollowed(lane publishExternalLane) bool {
	return lane.Driver == externalDriverRobot && strings.TrimSpace(lane.IntentKey) != "" &&
		(lane.State == publishExternalDistributionPublishing || lane.State == publishExternalDistributionReviewing)
}

// robotLanePublishedUnsettled 发布做成了、平台上的结果排版工具却认不了（没拿到链接、链接不对、平台上是别的样子）
// 的待核对 lane：分发服务会接着查、改，每分钟照样按同一个键 GET 一次，这次发布的结论变了就改过来，只读、不再发
// （2026-11-01 百家号第一次真发，列表给的链接是 http，卡片卡在待核对，分发服务改成 https 后没人再去问）。
func robotLanePublishedUnsettled(lane publishExternalLane, now time.Time) bool {
	if lane.Driver != externalDriverRobot || lane.State != publishExternalDistributionReconciliationRequired ||
		strings.TrimSpace(lane.IntentKey) == "" || strings.TrimSpace(lane.ContentPK) == "" || strings.TrimSpace(lane.ActionID) == "" {
		return false
	}
	requestedAt, err := time.Parse(time.RFC3339, lane.RequestedAt)
	return err == nil && now.Sub(requestedAt) <= robotResolveWithin
}

// recheckRobotPublishedLane 只认这条 lane 记的那次发布、而且做成了：按平台现状重新下结论。
func (p *publishPipeline) recheckRobotPublishedLane(jobID int64, lane publishExternalLane, client distributionRobotAPI) {
	payload, profile, ok := robotLanePayload(lane)
	if !ok {
		return
	}
	record, err := client.Draft(context.Background(), payload.Platform, lane.IntentKey)
	if err != nil || record.Action == nil || record.Action.ID != lane.ActionID || record.Action.Status != robotActionDone {
		return
	}
	if result := robotPlatformResultOf(record, profile, lane.ActionID); result.State != "" {
		p.applyRobotFollowResult(jobID, lane, result, profile)
	}
}

// followRobotLanes 发布中、平台审核中的 lane，和发布做成了却待核对的 lane，各 GET 一次（只读分发服务的记录，不碰平台）。
func (p *publishPipeline) followRobotLanes() {
	if p == nil || p.store == nil || publishWorkersPaused() {
		return
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 跟进外发发布读取任务失败：%v", err)
		return
	}
	now := pipelineNow(p)
	var client distributionRobotAPI
	for _, job := range jobs {
		for _, lane := range job.ExternalDistributionLanes {
			followed, unsettled := robotLaneFollowed(lane), robotLanePublishedUnsettled(lane, now)
			if !followed && !unsettled {
				continue
			}
			if client == nil {
				if client, err = p.robotClient(); err != nil {
					log.Printf("publish: 跟进外发发布无法创建分发工具客户端：%v", err)
					return
				}
			}
			if followed {
				p.followRobotLane(job.ID, lane, client)
			} else {
				p.recheckRobotPublishedLane(job.ID, lane, client)
			}
		}
	}
}

// followRobotLaneNow 启动恢复时当场跟进一条（发布中而分发服务那边没有这次发布的，就调一次发布）。
func (p *publishPipeline) followRobotLaneNow(jobID int64, accountID string) {
	job, err := p.store.byID(jobID)
	if err != nil {
		return
	}
	lane, ok := externalLaneFor(job, accountID)
	if !ok || !robotLaneFollowed(lane) {
		return
	}
	client, err := p.robotClient()
	if err != nil {
		log.Printf("publish: 无法跟进任务 #%d %s 的发布：%v", jobID, lane.Name, err)
		return
	}
	p.followRobotLane(jobID, lane, client)
}

func (p *publishPipeline) followRobotLane(jobID int64, lane publishExternalLane, client distributionRobotAPI) {
	if _, busy := p.robotPublishing.Load(robotPublishFlight(jobID, lane.AccountID)); busy {
		return // 正在调发布，它回来自己写结果
	}
	payload, profile, ok := robotLanePayload(lane)
	if !ok {
		log.Printf("publish: 任务 #%d %s 的草稿内容无法解析，不能跟进发布", jobID, lane.Name)
		return
	}
	record, getErr := client.Draft(context.Background(), payload.Platform, lane.IntentKey)
	result := robotLaneResultOf(lane, record, getErr, profile)
	switch {
	case result.Publish:
		p.publishRobotLane(jobID, lane.AccountID, client)
	case result.State == "":
		if result.Uncertain {
			log.Printf("publish: 跟进任务 #%d %s 的发布未果：%s", jobID, lane.Name, result.Reason)
		}
	default:
		p.applyRobotFollowResult(jobID, lane, result, profile)
	}
}

// applyRobotFollowResult 发布中、审核中的 lane 有了新结论：还是读到它的那个状态才写，写了就刷审核卡。
func (p *publishPipeline) applyRobotFollowResult(jobID int64, lane publishExternalLane, result robotLaneResult, profile distributionPlatform) {
	if robotLaneResultSame(lane, result) {
		return
	}
	stamp := pipelineNow(p).Format(time.RFC3339)
	_, swapped, err := p.updateExternalLane(jobID, lane.AccountID, func(current publishExternalLane) bool {
		return current.Driver == externalDriverRobot && current.State == lane.State && current.IntentKey == lane.IntentKey
	}, func(current *publishJob, l *publishExternalLane) {
		applyRobotLaneResult(l, result, profile, stamp)
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		log.Printf("publish: 外发发布结果落盘失败（任务 #%d %s）：%v", jobID, lane.Name, err)
		return
	}
	if swapped {
		log.Printf("publish: 外发发布更新（任务 #%d %s）：%s → %s", jobID, lane.Name, lane.State, result.State)
		p.scheduleExternalDistributionCardUpdate(jobID)
	}
}

// robotPlatformState 经分发服务的平台现在的状态（卡片与侧边栏）。
func robotPlatformState(lane publishExternalLane, profile distributionPlatform) externalPlatformState {
	state := externalPlatformState{URL: reviewCardSafeURL(lane.URL), Title: strings.TrimSpace(lane.Title)}
	switch {
	case lane.State == publishExternalDistributionSucceeded:
		state.Status, state.Tone = "已发布", "green"
		state.Fidelity = lane.Fidelity.Note
		if state.URL == "" {
			state.ManageURL = profile.ManageURL
		}
	case lane.State == publishExternalDistributionReviewing:
		state.Status, state.Tone = "平台审核中", "blue"
		state.Fidelity = lane.Fidelity.Note
	case lane.State == publishExternalDistributionPublishing:
		state.Status, state.Tone = "发布中", "blue"
		state.Fidelity = lane.Fidelity.Note
	case publishExternalDistributionProcessing(lane.State):
		state.Status, state.Tone = "同步中", "blue"
	case lane.State == publishExternalDistributionRejected:
		state.Status, state.Tone = "未通过", "red"
		state.Reason = firstNonEmpty(lane.Error, "平台没给原因")
		state.ManageURL = profile.ManageURL
	case lane.State == publishExternalDistributionDraftSaved:
		state.Status, state.Tone = "未发布", "orange"
		state.Reason = firstNonEmpty(lane.Error, "只存了草稿，没有发布")
		state.DraftURL = reviewCardSafeURL(lane.DraftURL)
		if state.DraftURL == "" {
			state.ManageURL = profile.ManageURL
		}
	case lane.State == publishExternalDistributionReconciliationRequired:
		state.Status, state.Tone = "待核对", "orange"
		state.Reason = firstNonEmpty(lane.Error, "结果未确认，系统按同一个幂等键向分发工具核对，不会重复存稿")
		if externalLaneDraftSaved(lane) && state.URL == "" {
			state.ManageURL = profile.ManageURL
		}
	default:
		state.Status, state.Tone, state.FailedLabel = "失败", "red", "上次同步失败"
		state.Reason = firstNonEmpty(externalLaneFailureReason(lane), "分发工具没同步成")
		state.Syncable = externalLaneCanSafeRetry(lane)
		state.Editable = externalLaneEditable(lane)
		if externalLaneDraftSaved(lane) {
			state.DraftURL = reviewCardSafeURL(lane.DraftURL)
		}
	}
	return state
}
