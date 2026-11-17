package main

// publish_proofread.go — 确定错误的校对提醒。
//
// 2026-10-13 用户裁定：「像是早上的这个错误，其实是非常好的校对……他就是属于那种置信度几乎100%的
// 错误，比如还有什么错别字之类的……我觉得是有必要在卡片的话题下，@对应的人来查看的。但是不耽误同步。
// 但是需要提醒确认。注意，模棱两可的，这些可以不提醒」；同日「你可以先@郑梽煌」。
//
// 做法：风险检查（publish_risk.go）里标了 confirmed 的条目是候选——生产实测里它们大多是真错，但也混着
// 「建议补年份」「建议改绝对日期」这类建议，直接拿来 @ 人不够准。所以审核卡发出后，再用 Pro 档逐条
// 复核一次（editorial/proofread_prompt.md），只留下能写出「把 X 改成 Y」、确定无疑的错误；服务端再校验
// X 逐字在正文里、Y 与 X 不同、不是只改标点。有这样的错误，就在审核卡的话题里回复一条富文本、
// 真 @ 管理后台「发布链路 → 校对负责人」。
//
// 不挡任何东西：不改正文、不改风险结论与审核卡、不影响审核/发布/外发。同一组错误只提醒一次；
// 重跑风险检查后错误变了才再提醒。持久 outbox：失败按通知退避重试，超过次数进死信（监控计数，红线 30）。

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// 一篇最多提醒几处：确定的错误本来就少，超过说明复核在凑数。
const publishProofreadMaxFindings = 10

// publishProofreadFinding 一处确定的错误：正文里的 Wrong 应改为 Correct；Evidence 是所在原句。
type publishProofreadFinding struct {
	Wrong    string `json:"wrong"`
	Correct  string `json:"correct"`
	Evidence string `json:"evidence"`
}

// publishProofreadReminder 一篇文章的校对提醒（按审核群各一条回复）。
type publishProofreadReminder struct {
	// Want 要复核的那份风险结论（正文版本 + 候选条目的指纹）；Checked 已经复核过的那份。
	Want     string                    `json:"want,omitempty"`
	Checked  string                    `json:"checked,omitempty"`
	Findings []publishProofreadFinding `json:"findings,omitempty"`
	// Sent 已经提醒过的那组错误（同样的错不再 @ 第二次）；MessageIDs 是 Round 这组错误在各审核群的回复。
	Sent       string            `json:"sent,omitempty"`
	Round      string            `json:"round,omitempty"`
	MessageIDs map[string]string `json:"message_ids,omitempty"`
	State      string            `json:"state,omitempty"` // pending / sending / dead_letter
	Attempts   int               `json:"attempts,omitempty"`
	Error      string            `json:"error,omitempty"`
	SentAt     string            `json:"sent_at,omitempty"`
	Skipped    string            `json:"skipped,omitempty"` // 没发的原因（如没设校对负责人）
}

//go:embed editorial/proofread_prompt.md
var publishProofreadPrompt string

// publishProofreadCandidate 风险检查里可能是确定错误的条目：模型标了 confirmed（且 evidence
// 逐字在正文里，groundPublishRiskEvidence 已降级编造的），类别是事实、名称、数字或成稿事故。
// 合规族与 system 不是校对问题。
func publishProofreadCandidate(item publishRiskItem) bool {
	if item.Certainty != publishRiskCertaintyConfirmed || publishRiskComplianceFamily(item.Category) {
		return false
	}
	switch item.Category {
	case publishRiskCategoryFactual, publishRiskCategoryNaming, publishRiskCategoryNumber, publishRiskCategoryIntegrity:
		return strings.TrimSpace(item.Evidence) != ""
	}
	return false
}

func publishProofreadCandidates(items []publishRiskItem) []publishRiskItem {
	var out []publishRiskItem
	for _, item := range items {
		if publishProofreadCandidate(item) {
			out = append(out, item)
		}
	}
	return out
}

// publishProofreadWant 这份风险结论的指纹；没有候选时为空（不用复核）。
func publishProofreadWant(job publishJob) string {
	candidates := publishProofreadCandidates(job.RiskItems)
	if len(candidates) == 0 {
		return ""
	}
	material, _ := json.Marshal(struct {
		Version int64             `json:"v"`
		Items   []publishRiskItem `json:"items"`
	}{job.ContentVersion, candidates})
	digest := sha256.Sum256(material)
	return fmt.Sprintf("%x", digest[:8])
}

func publishProofreadSignature(findings []publishProofreadFinding) string {
	if len(findings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.Wrong+"→"+finding.Correct)
	}
	material, _ := json.Marshal(parts)
	digest := sha256.Sum256(material)
	return fmt.Sprintf("%x", digest[:8])
}

// notePublishProofread 在写入风险结论的同一次写入里调用：结论变了就排一次复核。返回要不要调度。
func notePublishProofread(job *publishJob) bool {
	want := publishProofreadWant(*job)
	r := &job.ProofreadReminder
	if want == r.Want {
		return false
	}
	r.Want = want
	r.Attempts, r.Error = 0, ""
	if want == "" {
		// 新结论里没有候选：不用复核，也不再提醒（已发的回复留在话题里）。
		r.State, r.Checked, r.Findings = "", "", nil
		return false
	}
	r.State = publishExternalCardPending
	return true
}

// publishProofreadDropped 不再提醒的文章：驳回、放弃、重复稿。失败的稿可能重试后再发卡，先挂着。
func publishProofreadDropped(job publishJob) bool {
	switch job.Stage {
	case publishStageRejected, publishStageAbandoned, publishStageSupersededDuplicate:
		return true
	}
	return false
}

// publishProofreadAwaitingCard 提醒挂着、但审核卡还没发出：在等卡片，不算积压的通知（卡片自己的
// outbox 另有计数）。
func publishProofreadAwaitingCard(job publishJob) bool {
	return job.ProofreadReminder.State == publishExternalCardPending &&
		len(job.ReviewCardMsgIDs) == 0 && strings.TrimSpace(job.ReviewCardMsgID) == ""
}

func (p *publishPipeline) scheduleProofreadReminder(jobID int64) {
	p.spawnExternalWork(func() { p.dispatchProofreadReminder(jobID) })
}

func (p *publishPipeline) dispatchProofreadReminder(jobID int64) {
	if next := p.dispatchProofreadReminderLocked(jobID); next != nil {
		next()
	}
}

func (p *publishPipeline) dispatchProofreadReminderLocked(jobID int64) func() {
	if publishWorkersPaused() {
		return nil
	}
	p.proofreadReminderMu.Lock()
	defer p.proofreadReminderMu.Unlock()
	job, err := p.store.byID(jobID)
	if err != nil || job.ProofreadReminder.State != publishExternalCardPending {
		return nil
	}
	if publishProofreadDropped(job) {
		_, _, _ = p.store.updateIf(jobID, func(current publishJob) bool {
			return current.ProofreadReminder.State == publishExternalCardPending
		}, func(current *publishJob) {
			current.ProofreadReminder.State = ""
			current.ProofreadReminder.Skipped = "文章没有发布，不再提醒"
		})
		return nil
	}
	settings, err := p.externalReminderTargets(job)
	if err != nil || !p.proofreadCardSent(job, settings) {
		// 审核卡还没发出：等卡片发出后（dispatchPublishNotification）再来。
		return nil
	}
	want := job.ProofreadReminder.Want
	claimed, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ProofreadReminder.State == publishExternalCardPending && current.ProofreadReminder.Want == want
	}, func(current *publishJob) {
		current.ProofreadReminder.State = publishExternalCardSending
		current.ProofreadReminder.Attempts++
		current.ProofreadReminder.Error = ""
	})
	if err != nil || !swapped {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	round, opened, skipped, sendErr := p.runProofreadReminder(ctx, claimed)

	failed, restored, persistErr := p.store.updateIf(jobID, func(publishJob) bool { return true }, func(current *publishJob) {
		r := &current.ProofreadReminder
		if len(opened) > 0 {
			if r.Round != round {
				r.Round, r.MessageIDs = round, nil
			}
			if r.MessageIDs == nil {
				r.MessageIDs = map[string]string{}
			}
			for chatID, messageID := range opened {
				r.MessageIDs[chatID] = messageID
			}
			r.SentAt = pipelineNow(p).Format(time.RFC3339)
		}
		if r.Want != want || r.State != publishExternalCardSending {
			// 发送期间风险结论又变了：保持 pending，交给已安排的下一次。
			return
		}
		if sendErr == nil {
			r.State, r.Error, r.Skipped = "", "", skipped
			if skipped == "" && round != "" {
				r.Sent = round
			}
			return
		}
		if r.Attempts >= publishExternalCardMaxAttempts {
			r.State = publishExternalCardDeadLetter
		} else {
			r.State = publishExternalCardPending
		}
		r.Error = truncateRunes(strings.Join(strings.Fields(sendErr.Error()), " "), 500)
	})
	if persistErr != nil {
		log.Printf("publish: ack proofread reminder for job %d failed: %v", jobID, persistErr)
		return nil
	}
	if !restored {
		return nil
	}
	if sendErr != nil {
		log.Printf("publish: proofread reminder for job %d failed: %v", jobID, sendErr)
	}
	r := failed.ProofreadReminder
	if r.State != publishExternalCardPending {
		return nil
	}
	if r.Want != want {
		return func() { p.scheduleProofreadReminder(jobID) }
	}
	if p.retryNotification != nil {
		delay := publishNotificationRetryDelay(r.Attempts)
		return func() { p.retryNotification(delay, func() { p.scheduleProofreadReminder(jobID) }) }
	}
	return nil
}

// proofreadCardSent 这篇的审核卡至少在一个审核群里发出去了（提醒要回复在卡片话题下）。
func (p *publishPipeline) proofreadCardSent(job publishJob, settings publishDeliverySettings) bool {
	for _, chatID := range settings.ChatIDs {
		if p.reviewCardMessageIDForChat(job, strings.TrimSpace(chatID)) != "" {
			return true
		}
	}
	return false
}

// runProofreadReminder 没复核过就先复核（结果写回任务），再把确定的错误发到各审核群的卡片话题里。
// 返回这组错误的指纹（一轮）、本次发出的回复（审核群 → message_id）与没发的原因。
func (p *publishPipeline) runProofreadReminder(ctx context.Context, claimed publishJob) (string, map[string]string, string, error) {
	r := &claimed.ProofreadReminder
	if r.Checked != r.Want {
		findings, err := p.verifyProofread(ctx, claimed)
		if err != nil {
			return "", nil, "", err
		}
		want := r.Want
		if _, swapped, err := p.store.updateIf(claimed.ID, func(current publishJob) bool {
			return current.ProofreadReminder.Want == want
		}, func(current *publishJob) {
			current.ProofreadReminder.Checked = want
			current.ProofreadReminder.Findings = findings
		}); err != nil {
			return "", nil, "", err
		} else if !swapped {
			return "", nil, "", nil // 复核期间结论变了，交给下一次
		}
		r.Checked, r.Findings = want, findings
	}
	round := publishProofreadSignature(r.Findings)
	if round == "" || round == r.Sent {
		return "", nil, "", nil
	}
	settings, err := p.externalReminderTargets(claimed)
	if err != nil {
		return round, nil, "", err
	}
	owners, missing, err := p.bylineRecipients(ctx, claimed, settings, p.publishProofreadOwners(settings.AppProfileID))
	if err != nil {
		return round, nil, "", err
	}
	if len(owners) == 0 {
		return round, nil, "没有设置校对负责人，未发提醒", nil
	}
	post := buildProofreadReminder(claimed, owners, missing)
	opened := map[string]string{}
	var failures []string
	for _, chatID := range settings.ChatIDs {
		chatID = strings.TrimSpace(chatID)
		if chatID == "" || (r.Round == round && r.MessageIDs[chatID] != "") {
			continue
		}
		if p.reviewCardMessageIDForChat(claimed, chatID) == "" {
			continue // 这个群里没有这篇的卡：不单独发到群里刷屏
		}
		messageID, err := p.sendProofreadReminderToChat(ctx, claimed, chatID, post)
		if err != nil {
			failures = append(failures, chatID+"："+err.Error())
			continue
		}
		opened[chatID] = messageID
	}
	if len(failures) > 0 {
		return round, opened, "", errors.New("发送校对提醒失败：" + strings.Join(failures, "；"))
	}
	return round, opened, "", nil
}

func (p *publishPipeline) sendProofreadReminderToChat(ctx context.Context, job publishJob, chatID string, post map[string]any) (string, error) {
	if p.proofreadReminderSender != nil {
		return p.proofreadReminderSender(ctx, job, chatID, post)
	}
	settings, apps, err := p.reviewDeliveryForJob(job)
	if err != nil {
		return "", err
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, chatID)
	if err != nil {
		return "", err
	}
	return notifier.replyPostWithID(ctx, p.reviewCardMessageIDForChat(job, chatID), post, true)
}

// publishProofreadOwners 校对负责人：open_id 按审核应用区分，换了应用就 @ 不到原来的人。
func (p *publishPipeline) publishProofreadOwners(appProfileID string) []publishDistributionOwner {
	if p == nil || p.deliverySettings == nil {
		return nil
	}
	settings, err := p.deliverySettings.read()
	if err != nil || strings.TrimSpace(settings.ProofreadOwnersApp) != strings.TrimSpace(appProfileID) {
		return nil
	}
	return settings.ProofreadOwners
}

// buildProofreadReminder 话题里的一条：@ 作者编辑或校对负责人，哪篇、几处；每处一行粗体「X」应为「Y」，下一行原句；
// 原创稿署名没对上群里的人时说一句没找到谁；最后一句说不影响同步、请核对。
func buildProofreadReminder(job publishJob, owners []publishDistributionOwner, missing []string) map[string]any {
	findings := job.ProofreadReminder.Findings
	var head []any
	for _, owner := range owners {
		if id := strings.TrimSpace(owner.OpenID); id != "" {
			head = append(head, postAt(id), postText(" ", false))
		}
	}
	head = append(head, postText(fmt.Sprintf("#%d《%s》有 %d 处确定的错误，请核对：",
		job.ID, reviewCardSafeOrDash(job.Title, 80), len(findings)), false))
	lines := [][]any{head}
	for i, finding := range findings {
		fix := fmt.Sprintf("%d. 「%s」应为「%s」", i+1, finding.Wrong, finding.Correct)
		lines = append(lines, []any{postText(fix, true)})
		if evidence := proofreadPlain(finding.Evidence, 80); evidence != "" {
			lines = append(lines, []any{postText("原句："+evidence, false)})
		}
	}
	if len(missing) > 0 {
		lines = append(lines, bylineMissingLine(missing))
	}
	lines = append(lines, []any{postText("不影响同步和发布，确认后改正即可。", false)})
	return postMessage(lines)
}

func proofreadPlain(value string, limit int) string {
	return truncateRunes(strings.Join(strings.Fields(value), " "), limit)
}

// verifyProofread 复核候选条目，只留确定无疑、改法唯一的错误。
func (p *publishPipeline) verifyProofread(ctx context.Context, job publishJob) ([]publishProofreadFinding, error) {
	candidates := publishProofreadCandidates(job.RiskItems)
	if len(candidates) == 0 {
		return nil, nil
	}
	verify := p.proofreadVerify
	if verify == nil {
		verify = aiVerifyProofread
	}
	bodyText, raw, err := verify(ctx, job, candidates)
	if err != nil {
		return nil, err
	}
	return parsePublishProofread(raw, candidates, job.Title+"\n"+bodyText)
}

// aiVerifyProofread 复核走 MiMo Pro（ai_models.go）：结论会直接 @ 人，是高价值判断。
// 返回模型实际看到的正文（服务端校验 wrong 用）与模型原文。
func aiVerifyProofread(ctx context.Context, job publishJob, candidates []publishRiskItem) (string, string, error) {
	bodyText := htmlToPlainText(job.ContentHTML)
	text, err := aiVerifyProofreadText(ctx, job.Title, bodyText, candidates)
	return bodyText, text, err
}

// aiVerifyProofreadText 对一份标题和纯文字正文复核初检挑出的条目，返回模型原文；发布流水线和网页校对共用。
func aiVerifyProofreadText(ctx context.Context, title, bodyText string, candidates []publishRiskItem) (string, error) {
	var b strings.Builder
	b.WriteString(riskUserInput(title, bodyText, time.Now()))
	b.WriteString("\n\n初检列出的疑似错误：\n")
	for i, item := range candidates {
		fmt.Fprintf(&b, "%d. 原句：%s\n   初检说明：%s\n", i+1,
			strings.Join(strings.Fields(item.Evidence), " "), strings.Join(strings.Fields(item.Reason), " "))
	}
	return aiResponsesCall(ctx, aiModel(aiTierPro), publishProofreadPrompt,
		[]map[string]any{{"type": "input_text", "text": b.String()}})
}

// parsePublishProofread 只收 definite=true、wrong 逐字在正文和原句里、correct 与 wrong 不同且不只差标点、
// 而且是小改动的条目（proofreadSmallEdit）。
func parsePublishProofread(text string, candidates []publishRiskItem, sourceText string) ([]publishProofreadFinding, error) {
	findings, _, err := parsePublishProofreadIndexed(text, candidates, sourceText)
	return findings, err
}

// parsePublishProofreadIndexed 同 parsePublishProofread，另外说出每处错误来自第几条候选（从 0 数）。
func parsePublishProofreadIndexed(text string, candidates []publishRiskItem, sourceText string) ([]publishProofreadFinding, []int, error) {
	cleaned := stripMarkdownFence(text)
	if start, end := strings.Index(cleaned, "{"), strings.LastIndex(cleaned, "}"); start >= 0 && end > start {
		cleaned = cleaned[start : end+1]
	}
	var out struct {
		Items []struct {
			Index    int    `json:"index"`
			Definite bool   `json:"definite"`
			Wrong    string `json:"wrong"`
			Correct  string `json:"correct"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		return nil, nil, fmt.Errorf("校对复核输出不是合法 JSON: %w", err)
	}
	haystack := riskEvidenceNormalize(sourceText)
	seen := map[string]bool{}
	var findings []publishProofreadFinding
	var from []int
	for _, item := range out.Items {
		if !item.Definite || item.Index < 1 || item.Index > len(candidates) {
			continue
		}
		wrong := strings.TrimSpace(item.Wrong)
		correct := strings.TrimSpace(item.Correct)
		evidence := candidates[item.Index-1].Evidence
		switch {
		case wrong == "" || correct == "" || wrong == correct,
			runeLen(wrong) > 30 || runeLen(correct) > 40,
			strings.ContainsAny(wrong, "\n\r"),
			!proofreadSmallEdit(wrong, correct),
			!strings.Contains(haystack, riskEvidenceNormalize(wrong)),
			!strings.Contains(riskEvidenceNormalize(evidence), riskEvidenceNormalize(wrong)),
			proofreadOnlyPunctuation(wrong, correct),
			seen[wrong+"→"+correct]:
			continue
		}
		seen[wrong+"→"+correct] = true
		findings = append(findings, publishProofreadFinding{Wrong: wrong, Correct: correct, Evidence: proofreadPlain(evidence, 200)})
		from = append(from, item.Index-1)
		if len(findings) >= publishProofreadMaxFindings {
			break
		}
	}
	return findings, from, nil
}

// proofreadSmallEdit 改动够小：编辑距离不超过 2，或不超过原片段长度的三分之一。
//
// 生产 dry_run（2026-10-13，43 篇历史稿）实测：真错别字、漏字、叠字、名称写错都是改一两个字
// （「腌采→腌菜」「层不出穷→层出不穷」「农业水产省→农林水产省」）；整块换掉的都是模型在猜原本该写
// 什么（「伊利伊刻活泉现泡茶→彩虹糖」「深海海水→深层地下水」「近500亿元→约46亿元」），达不到
// 「99% 确定」，不提醒。
func proofreadSmallEdit(wrong, correct string) bool {
	limit := runeLen(wrong) / 3
	if limit < 2 {
		limit = 2
	}
	return proofreadEditDistance([]rune(wrong), []rune(correct)) <= limit
}

// proofreadEditDistance 按字计的编辑距离（插入、删除、替换各算 1）。
func proofreadEditDistance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// proofreadOnlyPunctuation 两边去掉标点与空白后一样：只是标点问题，不提醒。
func proofreadOnlyPunctuation(wrong, correct string) bool {
	strip := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsPunct(r) || unicode.IsSpace(r) || unicode.IsSymbol(r) {
				return -1
			}
			return r
		}, s)
	}
	return strip(wrong) == strip(correct)
}

// registerPublishProofreadRoutes 运维入口：对一篇稿按当前风险结论复核并提醒（管理员或服务令牌）。
// ?dry_run=1 只复核、返回候选、确定的错误与会 @ 谁，不写库、不发；否则重新复核并发到审核卡话题（同一组错误
// 已经提醒过的不再发）。只在风险检查写入时自动触发，存量稿不会自己补发，需要时走这里。
func registerPublishProofreadRoutes(mux *http.ServeMux, p *publishPipeline, auth *authService) {
	mux.HandleFunc("POST /api/publish/jobs/{id}/proofread-reminder", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		dryRun := r.URL.Query().Get("dry_run") == "1"
		if !dryRun && !requireDeploymentWritesActive(w) {
			return
		}
		id, ok := publishJobIDFromPath(w, r)
		if !ok {
			return
		}
		job, err := p.store.byID(id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		// 复核要通读全文，可能超过网关时限；用独立 context 跑完，结论照样落库。
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 4*time.Minute)
		defer cancel()
		if dryRun {
			findings, err := p.verifyProofread(ctx, job)
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			settings, err := p.externalReminderTargets(job)
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			mentions, missing, err := p.bylineRecipients(ctx, job, settings, p.publishProofreadOwners(settings.AppProfileID))
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"job_id": job.ID, "dry_run": true,
				"candidates": publishProofreadCandidates(job.RiskItems), "findings": findings,
				"bylines": proofreadBylineNames(job), "mentions": mentions, "missing_bylines": missing,
			})
			return
		}
		if publishProofreadDropped(job) || publishProofreadWant(job) == "" {
			writeError(w, http.StatusBadRequest, "这篇没有可复核的确定条目，或文章没有发布")
			return
		}
		if _, err := p.store.update(id, func(current *publishJob) {
			reminder := &current.ProofreadReminder
			reminder.Want, reminder.Checked = publishProofreadWant(*current), ""
			reminder.State, reminder.Attempts, reminder.Error, reminder.Skipped = publishExternalCardPending, 0, "", ""
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		p.dispatchProofreadReminder(id)
		updated, err := p.store.byID(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "proofread_reminder": updated.ProofreadReminder})
	})
}

// recoverProofreadReminder 启动时把中断的提醒接着做完。
func (p *publishPipeline) recoverProofreadReminder(job publishJob) {
	switch job.ProofreadReminder.State {
	case publishExternalCardSending:
		reset, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.ProofreadReminder.State == publishExternalCardSending
		}, func(current *publishJob) {
			current.ProofreadReminder.State = publishExternalCardPending
		})
		if err == nil && swapped {
			p.scheduleProofreadReminder(reset.ID)
		}
	case publishExternalCardPending:
		p.scheduleProofreadReminder(job.ID)
	}
}
