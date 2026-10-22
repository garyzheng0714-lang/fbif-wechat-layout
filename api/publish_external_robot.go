package main

// publish_external_robot.go — 经自己的分发服务（distribution_robot.go）送稿的 lane，目前只有百家号。
//
// 2026-10-22 用户裁定：审核卡「同步至百家号」改走自己的机器人，先只存草稿、不发布，用户看过
// 线上效果再加发布。与云梭 lane 共用 queued / pushing / failed / reconciliation_required，多一个
// 终态 draft_saved：
//
//	queued → pushing（提交前先落 intent，IntentKey 就是发给分发服务的幂等键）
//	  → draft_saved：草稿存好，ContentPK=草稿编号、DraftURL，分发服务当场读回的核对写进 Fidelity；
//	  → failed：点「存草稿」之前就被拒（参数、token、封面、没登录），没建稿，卡片可以重新存；
//	  → reconciliation_required：草稿箱里同名新稿不止一篇（要人去删），或结果不明。
//
// 结果不明的按同一个幂等键 GET：存好了 → draft_saved；分发服务记失败或从没收到 → failed；还在做或
// 要人对账 → 不动。系统只查不提交；再提交只走卡片重试与管理员重发，同一个键分发服务也只认一篇。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// publishExternalDistributionDraftSaved 平台上存好了草稿、没有发布。不算「已在平台上」：
// 人去平台把草稿发出去后，用人工登记接口记成人工已发。
const publishExternalDistributionDraftSaved = "draft_saved"

// robotDraftPayload 冻结在 lane 上的草稿内容（PayloadJSON）。Platform 是分发服务里的平台 id。
type robotDraftPayload struct {
	Platform string `json:"platform"`
	Title    string `json:"title"`
	HTML     string `json:"html"`
	CoverURL string `json:"cover_url"`
}

// buildRobotDraftPayload 与云梭同一套正文规则与导流尾拦截；标题原样（人给这个平台改过的优先），
// 封面单独上传、不进正文。
func buildRobotDraftPayload(job publishJob, profile yunsuoPlatformProfile, draft publishExternalDraft) (robotDraftPayload, error) {
	_, rendered, err := renderExternalBody(job, profile, draft)
	if err != nil {
		return robotDraftPayload{}, err
	}
	if leak := publishHTMLWechatTrailerLeak(rendered.HTML); leak != "" {
		return robotDraftPayload{}, fmt.Errorf("%s草稿正文仍含公众号导流尾（%s），禁止外发", profile.Name, leak)
	}
	title := externalEffectiveTitle(job, draft)
	if min, max := profile.titleCharRange(); runeLen(title) < min || runeLen(title) > max {
		return robotDraftPayload{}, fmt.Errorf("%s标题要 %d–%d 字，现在 %d 字", profile.Name, min, max, runeLen(title))
	}
	if rendered.Cover == "" {
		return robotDraftPayload{}, fmt.Errorf("%s草稿缺少公开可访问的封面", profile.Name)
	}
	if width, height, small := profile.coverTooSmall(rendered.Cover); small {
		return robotDraftPayload{}, fmt.Errorf("%s封面图尺寸过小（%dx%d），要求不小于 %dx%d，请换封面后再存草稿",
			profile.Name, width, height, profile.CoverMinWidth, profile.CoverMinHeight)
	}
	return robotDraftPayload{Platform: profile.Key, Title: title, HTML: rendered.HTML, CoverURL: rendered.Cover}, nil
}

// buildExternalLanePayload 按平台的送稿方式冻结这条 lane 的内容：返回 JSON 与推到平台上的标题。
func buildExternalLanePayload(job publishJob, target yunsuoAccountTarget, categories []string, draft publishExternalDraft) ([]byte, string, error) {
	profile := yunsuoProfileForTarget(target)
	if profile.Driver == externalDriverRobot {
		payload, err := buildRobotDraftPayload(job, profile, draft)
		if err != nil {
			return nil, "", err
		}
		encoded, err := json.Marshal(payload)
		return encoded, payload.Title, err
	}
	payload, err := buildYunsuoPushPayloadWithDraft(job, []yunsuoAccountTarget{target}, categories, draft)
	if err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(payload)
	return encoded, payload.titleField(profile.TitleField), err
}

// externalLaneFrozenHTML 冻结在 lane 上的正文，按这条 lane 的送稿方式解。
func externalLaneFrozenHTML(lane publishExternalLane) (string, error) {
	if lane.Driver == externalDriverRobot {
		var payload robotDraftPayload
		err := json.Unmarshal([]byte(lane.PayloadJSON), &payload)
		return payload.HTML, err
	}
	var payload yunsuoPushPayload
	err := json.Unmarshal([]byte(lane.PayloadJSON), &payload)
	return payload.HTML, err
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

func (p *publishPipeline) robotClient() (distributionRobotAPI, error) {
	if p.distributionRobot != nil {
		return p.distributionRobot()
	}
	return newDistributionRobotClientFromEnv()
}

// robotPayloadProfile 冻结内容里记的平台；认不出（配置改过）就用通用规则，只影响提示里的平台名。
func robotPayloadProfile(payload robotDraftPayload) yunsuoPlatformProfile {
	if profile, ok := yunsuoPlatformProfiles[payload.Platform]; ok {
		return profile
	}
	return yunsuoGenericProfile
}

func (p *publishPipeline) pushRobotLane(job publishJob, lane publishExternalLane) {
	var payload robotDraftPayload
	if err := json.Unmarshal([]byte(lane.PayloadJSON), &payload); err != nil || strings.TrimSpace(payload.Platform) == "" {
		p.failExternalLaneBeforePush(job.ID, lane.AccountID, "已保存的草稿内容无法解析，未提交")
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
		IdempotencyKey: intentKey, Title: payload.Title, HTML: payload.HTML, CoverURL: payload.CoverURL,
	})
	profile := robotPayloadProfile(payload)
	result := robotLaneResultOf(record, saveErr, profile)
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
	if result.Uncertain {
		// 平台出错这类结果，分发服务其实已经定了（记成失败或已对账认领）：当场查一次，不用等兜底循环。
		p.resolveRobotLane(job.ID, lane.AccountID)
	}
}

// robotLaneResult 分发服务的一次回答落到 lane 上是什么。State 为空：还没定，lane 不动。
type robotLaneResult struct {
	State     string
	Reason    string
	Record    robotDraftRecord
	Uncertain bool
}

func robotLaneResultOf(record robotDraftRecord, err error, profile yunsuoPlatformProfile) robotLaneResult {
	var rejected *robotRejectedError
	var reconcile *robotReconcileError
	switch {
	case err == nil && record.Status == robotDraftSaved && record.ArticleID != "":
		return robotLaneResult{State: publishExternalDistributionDraftSaved, Record: record}
	case errors.As(err, &rejected):
		return robotLaneResult{State: publishExternalDistributionFailed, Reason: robotRejectedReason(rejected, profile)}
	case errors.As(err, &reconcile):
		return robotLaneResult{State: publishExternalDistributionReconciliationRequired, Reason: reconcile.Message}
	case errors.Is(err, errRobotDraftNotFound):
		return robotLaneResult{State: publishExternalDistributionFailed, Reason: "分发工具没收到这次请求，没有存草稿，可以重新存"}
	case err != nil:
		return robotLaneResult{Reason: err.Error(), Uncertain: true}
	case record.Status == robotDraftFailed:
		return robotLaneResult{State: publishExternalDistributionFailed, Reason: robotRecordError(record, "分发工具记为没存成") + "；可以重新存"}
	case record.Status == robotDraftUnknown:
		return robotLaneResult{State: publishExternalDistributionReconciliationRequired, Reason: robotRecordError(record, "草稿箱里的结果要人核对")}
	default:
		return robotLaneResult{Reason: "分发工具还在存草稿（" + firstNonEmpty(record.Status, "没有状态") + "）"}
	}
}

func robotRecordError(record robotDraftRecord, fallback string) string {
	if record.Error != nil && strings.TrimSpace(*record.Error) != "" {
		return safeYunsuoMessage(*record.Error)
	}
	return fallback
}

func robotRejectedReason(err *robotRejectedError, profile yunsuoPlatformProfile) string {
	switch {
	case err.Code == "login_required":
		return "服务器上的" + profile.Name + "没登录，请在分发工具的远程画面里登录后重新存草稿"
	case err.Status == 401:
		return "分发工具不认这个接口 token（" + distributionAPITokenEnv + "），请查配置后重新存草稿"
	default:
		return "分发工具没存草稿：" + err.Message
	}
}

// applyRobotLaneResult 锁内把定了的结果写进 lane；State 为空不调用。
func applyRobotLaneResult(l *publishExternalLane, result robotLaneResult, profile yunsuoPlatformProfile, now string) {
	l.State = result.State
	l.Error = truncateRunes(strings.Join(strings.Fields(result.Reason), " "), 500)
	l.Remark = l.Error
	l.CompletedAt = now
	if result.State != publishExternalDistributionDraftSaved {
		return
	}
	l.ContentPK = result.Record.ArticleID
	l.DraftURL = ""
	if draftURL, err := validateExternalArticleURL(result.Record.DraftURL, profile); err == nil {
		l.DraftURL = draftURL
	} else if result.Record.DraftURL != "" {
		log.Printf("publish: 分发工具返回的草稿地址不在%s域名内，不写进卡片：%v", profile.Name, err)
	}
	l.Fidelity = robotDraftFidelity(result.Record, profile.Name, now)
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
			check.Error = "读回草稿失败：" + safeYunsuoMessage(readBackError)
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
	return safeYunsuoMessage(platform + "改版：" + strings.Join(steps, "；"))
}

// robotLaneAwaitingResult 结果不明、还在补查期内的分发服务 lane（只查不提交，可以每轮都查）。
func robotLaneAwaitingResult(lane publishExternalLane, now time.Time) bool {
	if lane.Driver != externalDriverRobot || lane.State != publishExternalDistributionReconciliationRequired ||
		strings.TrimSpace(lane.IntentKey) == "" || strings.TrimSpace(lane.ContentPK) != "" {
		return false
	}
	requestedAt, err := time.Parse(time.RFC3339, lane.RequestedAt)
	return err == nil && now.Sub(requestedAt) <= publishExternalLateRefreshWithin
}

// resolveRobotLane 按同一个幂等键问分发服务这条 lane 的结果。
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
	var payload robotDraftPayload
	if err := json.Unmarshal([]byte(lane.PayloadJSON), &payload); err != nil || strings.TrimSpace(payload.Platform) == "" {
		log.Printf("publish: 任务 #%d %s 的草稿内容无法解析，不能向分发工具核对", jobID, lane.Name)
		return
	}
	profile := robotPayloadProfile(payload)
	record, getErr := client.Draft(context.Background(), payload.Platform, lane.IntentKey)
	result := robotLaneResultOf(record, getErr, profile)
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
	}
}

// recoverRobotLane 进程在提交窗口里退出：分发服务可能存了也可能没存，转待核对后按同一个键去查。
func (p *publishPipeline) recoverRobotLane(job publishJob, lane publishExternalLane) {
	p.finishExternalLanePushFailure(job.ID, lane.AccountID, lane.IntentKey, publishExternalDistributionReconciliationRequired,
		"进程在存草稿时中断，系统会按同一个幂等键向分发工具核对，不会重复存稿")
	p.spawnExternalWork(func() { p.resolveRobotLane(job.ID, lane.AccountID) })
}

// robotPlatformState 经分发服务的平台现在的状态（卡片与侧边栏）；不出现云梭的排队、养号这些说法。
func robotPlatformState(lane publishExternalLane, profile yunsuoPlatformProfile) externalPlatformState {
	state := externalPlatformState{URL: reviewCardSafeURL(lane.URL), Title: strings.TrimSpace(lane.Title)}
	switch {
	case lane.State == publishExternalDistributionDraftSaved:
		state.Status, state.Tone = "草稿已存（未发布）", "green"
		state.DraftURL = reviewCardSafeURL(lane.DraftURL)
		if state.DraftURL == "" {
			state.ManageURL = profile.ManageURL
		}
		state.Fidelity = lane.Fidelity.Note
	case publishExternalDistributionProcessing(lane.State):
		state.Status, state.Tone = "存草稿中", "blue"
	case lane.State == publishExternalDistributionReconciliationRequired:
		state.Status, state.Tone = "待核对", "orange"
		state.Reason = firstNonEmpty(lane.Error, "结果未确认，系统按同一个幂等键向分发工具核对，不会重复存稿")
	default:
		state.Status, state.Tone, state.FailedLabel = "失败", "red", "上次存草稿失败"
		state.Reason = firstNonEmpty(externalLaneFailureReason(lane), "分发工具没存草稿")
		state.Editable = externalLaneCanSafeRetry(lane)
	}
	return state
}
