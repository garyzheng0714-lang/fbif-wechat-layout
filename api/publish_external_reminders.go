package main

// publish_external_reminders.go — 外发待修改的提醒与后台准备。
//
// 2026-10-12 用户裁定：超限时「在群里 @ 对应的人」，@ 的是后台「发布链路」里设的固定分发
// 负责人；改在审核卡里完成。同日补充：「不应该直接在卡片里@人，因为在卡片里@人可能收不到。
// 它应该在这个卡片消息的话题下创建一个对话，@对应的人，告诉他下一步要做什么」「自动化不要让人
// 过多思考，简单直接地告诉他要干什么……给他选项」。所以提醒是审核卡话题里的普通富文本消息
// （post，@ 才提醒得到），不是卡片，也不在群里单独刷屏（红线 36 只管运维播报）。
//
// 一轮对话：有平台待修改 → 话题里发一条开场（@ 负责人，逐项写哪里不合要求、可以怎么改、改完
// 点哪里；审核卡快过或已过飞书 14 天修改期限时，卡片上的改稿按钮已不在，改指向侧边栏改稿页，#233）；全部改好 → 同一话题里说一句「都改好了，正在同步」。中途进度看卡片，不在话题里刷屏。
// 改好后又出新问题才开新一轮、重新 @。持久 outbox：发送失败按通知退避重试，超过次数进死信
// （监控计数，红线 30）。

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	publishExternalReminderOpen     = "open"
	publishExternalReminderResolved = "resolved"
	// publishReviewCardEditableFor 飞书只让更新 14 天内发出的消息；留一天给人去改。
	publishReviewCardEditableFor = 13 * 24 * time.Hour
	// publishReviewCardAgeLookup 读卡片发送时间最多等这么久，别把提醒本身的发送时间耗光。
	publishReviewCardAgeLookup = 5 * time.Second
)

// publishExternalReminder 一篇文章外发待修改的提醒（按审核群各一条回复）。
type publishExternalReminder struct {
	Round      int               `json:"round,omitempty"`
	Want       string            `json:"want,omitempty"` // open：还有平台要改；resolved：都改好了
	Signature  string            `json:"signature,omitempty"`
	MessageIDs map[string]string `json:"message_ids,omitempty"` // 审核群 → 这一轮开场消息的 message_id
	ClosedIDs  map[string]string `json:"closed_ids,omitempty"`  // 审核群 → 这一轮「都改好了」的 message_id
	State      string            `json:"state,omitempty"`       // pending / sending / dead_letter
	Attempts   int               `json:"attempts,omitempty"`
	Error      string            `json:"error,omitempty"`
	Seq        int64             `json:"seq,omitempty"`
	SentAt     string            `json:"sent_at,omitempty"`
	Skipped    string            `json:"skipped,omitempty"` // 没发的原因（如没设负责人）
}

// externalIssueShort 一句话说清是什么问题（卡片标题行、提醒、接口错误共用）。
func externalIssueShort(issue publishExternalIssue) string {
	switch issue.Field {
	case publishExternalIssueTitle:
		return fmt.Sprintf("标题超出 %d 字", issue.Length-issue.Max)
	case publishExternalIssueSummary:
		return fmt.Sprintf("摘要超出 %d 字", issue.Length-issue.Max)
	case publishExternalIssueCover:
		if issue.Missing {
			return "没有可用的封面"
		}
		return "封面太小"
	}
	return "内容不符合平台要求"
}

// externalIssueDetail 带上要求与现状：「标题超出 6 字（最多 20 字，现在 26 字）」。
func externalIssueDetail(issue publishExternalIssue) string {
	switch issue.Field {
	case publishExternalIssueTitle, publishExternalIssueSummary:
		return fmt.Sprintf("%s（最多 %d 字，现在 %d 字）", externalIssueShort(issue), issue.Max, issue.Length)
	case publishExternalIssueCover:
		if issue.Missing {
			return fmt.Sprintf("没有可用的封面（至少 %d×%d）", issue.MinWidth, issue.MinHeight)
		}
		return fmt.Sprintf("封面太小（至少 %d×%d，现在 %d×%d）", issue.MinWidth, issue.MinHeight, issue.Width, issue.Height)
	}
	return externalIssueShort(issue)
}

func externalIssueSummaryText(issues []publishExternalIssue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, externalIssueShort(issue))
	}
	return strings.Join(parts, "、")
}

// externalPendingFixes 还没推、卡在推送前检查的平台（按表里的顺序）。
func externalPendingFixes(job publishJob, cfg externalTargets) []publishExternalDraft {
	var out []publishExternalDraft
	for _, target := range cfg.forJob(job) {
		draft, ok := externalDraftFor(job, target.ID)
		if !ok || len(draft.Issues) == 0 {
			continue
		}
		if _, pushed := externalLaneFor(job, target.ID); pushed {
			continue
		}
		out = append(out, draft)
	}
	return out
}

func externalReminderSignature(job publishJob) string {
	r := job.ExternalDistributionReminder
	parts := []string{fmt.Sprint(r.Round), r.Want}
	for _, draft := range job.ExternalDistributionDrafts {
		if _, pushed := externalLaneFor(job, draft.AccountID); pushed || len(draft.Issues) == 0 {
			continue
		}
		for _, issue := range draft.Issues {
			parts = append(parts, draft.AccountID+"/"+externalIssueDetail(issue))
		}
	}
	material, _ := json.Marshal(parts)
	digest := sha256.Sum256(material)
	return fmt.Sprintf("%x", digest[:8])
}

// noteExternalReminderProgress 在改 draft/lane 的同一次写入里调用：决定提醒要不要发、改。
func noteExternalReminderProgress(job *publishJob) {
	r := &job.ExternalDistributionReminder
	switch {
	case externalJobNeedsFix(*job):
		if r.Want != publishExternalReminderOpen {
			if r.Want == publishExternalReminderResolved || len(r.MessageIDs) > 0 || r.Round == 0 {
				// 上一轮已经改好（或第一次）：开新一轮，新回复里重新 @。
				r.Round++
				r.MessageIDs, r.ClosedIDs = nil, nil
				r.SentAt, r.Skipped = "", ""
			}
			r.Want = publishExternalReminderOpen
		}
	case r.Want == publishExternalReminderOpen:
		r.Want = publishExternalReminderResolved
	default:
		return
	}
	if sig := externalReminderSignature(*job); sig != r.Signature {
		r.Signature = sig
		r.Seq++
		r.State = publishExternalCardPending
		r.Attempts = 0
		r.Error = ""
	}
}

func (p *publishPipeline) scheduleExternalReminder(jobID int64) {
	p.spawn(func() { p.dispatchExternalReminder(jobID) })
}

func (p *publishPipeline) dispatchExternalReminder(jobID int64) {
	// 串行发送；重排下一次发送放在锁外（测试里 spawn 是同步的）。
	if next := p.dispatchExternalReminderLocked(jobID); next != nil {
		next()
	}
}

func (p *publishPipeline) dispatchExternalReminderLocked(jobID int64) func() {
	p.externalReminderMu.Lock()
	defer p.externalReminderMu.Unlock()
	claimed, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionReminder.State == publishExternalCardPending
	}, func(current *publishJob) {
		current.ExternalDistributionReminder.State = publishExternalCardSending
		current.ExternalDistributionReminder.Attempts++
		current.ExternalDistributionReminder.Error = ""
	})
	if err != nil || !swapped {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, sendErr := p.sendExternalReminder(ctx, claimed)
	skipped := result.skipped
	claimedReminder := claimed.ExternalDistributionReminder
	failed, restored, persistErr := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionReminder.Round == claimedReminder.Round
	}, func(current *publishJob) {
		r := &current.ExternalDistributionReminder
		for chatID, messageID := range result.opened {
			if r.MessageIDs == nil {
				r.MessageIDs = map[string]string{}
			}
			if r.MessageIDs[chatID] == "" && r.SentAt == "" {
				r.SentAt = pipelineNow(p).Format(time.RFC3339)
			}
			r.MessageIDs[chatID] = messageID
		}
		for chatID, messageID := range result.closed {
			if r.ClosedIDs == nil {
				r.ClosedIDs = map[string]string{}
			}
			r.ClosedIDs[chatID] = messageID
		}
		if r.Seq != claimedReminder.Seq || r.State != publishExternalCardSending {
			// 发送期间又有新变化：保持 pending，交给已安排的下一次发送。
			return
		}
		if sendErr == nil {
			r.State, r.Error, r.Skipped = "", "", skipped
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
		log.Printf("publish: ack external reminder for job %d failed: %v", jobID, persistErr)
		return nil
	}
	if !restored {
		return nil
	}
	if sendErr != nil {
		log.Printf("publish: external reminder for job %d failed: %v", jobID, sendErr)
	}
	r := failed.ExternalDistributionReminder
	if r.State != publishExternalCardPending {
		return nil
	}
	if r.Seq != claimedReminder.Seq {
		return func() { p.scheduleExternalReminder(jobID) }
	}
	if p.retryNotification != nil {
		delay := publishNotificationRetryDelay(r.Attempts)
		return func() { p.retryNotification(delay, func() { p.scheduleExternalReminder(jobID) }) }
	}
	return nil
}

type externalReminderSendResult struct {
	opened, closed map[string]string // 审核群 → 本次发出的开场 / 收尾消息 id
	skipped        string
}

const (
	externalReminderKindOpen  = "open"
	externalReminderKindClose = "close"
)

// sendExternalReminder 往这篇文章的每个审核群话题里发开场或收尾。每个群每轮各只发一次：
// 部分群失败时已成功的记下来，重试只补没发成的，不重复 @。
func (p *publishPipeline) sendExternalReminder(ctx context.Context, job publishJob) (externalReminderSendResult, error) {
	r := job.ExternalDistributionReminder
	result := externalReminderSendResult{opened: map[string]string{}, closed: map[string]string{}}
	if r.Want != publishExternalReminderOpen && r.Want != publishExternalReminderResolved {
		return result, nil
	}
	if r.Want == publishExternalReminderResolved && len(r.MessageIDs) == 0 {
		return result, nil // 还没提醒过就改好了，不必再说
	}
	cfg, err := loadExternalTargets()
	if err != nil {
		return result, err
	}
	settings, err := p.externalReminderTargets(job)
	if err != nil {
		return result, err
	}
	owners := p.externalDistributionOwners(settings.AppProfileID)
	var failures []string
	for _, chatID := range settings.ChatIDs {
		chatID = strings.TrimSpace(chatID)
		if chatID == "" {
			continue
		}
		var kind string
		var post map[string]any
		switch {
		case r.Want == publishExternalReminderOpen && r.MessageIDs[chatID] == "":
			if len(owners) == 0 {
				result.skipped = "没有设置分发负责人，未发提醒"
				continue
			}
			inCard := p.externalReminderCardEditable(ctx, job, chatID)
			if !inCard && externalDistributionSidebarLink(job.ID) == "" {
				// 卡片里改不了、又给不出改稿页：发了也没法照做，不如不发。
				result.skipped = "审核卡里改不了，也没有改稿页链接，未发提醒"
				continue
			}
			kind, post = externalReminderKindOpen, buildExternalReminderOpening(job, cfg, owners, inCard)
		case r.Want == publishExternalReminderResolved && r.MessageIDs[chatID] != "" && r.ClosedIDs[chatID] == "":
			kind, post = externalReminderKindClose, buildExternalReminderClosing(job, cfg)
		default:
			continue
		}
		messageID, err := p.sendExternalReminderToChat(ctx, job, chatID, kind, post)
		if err != nil {
			failures = append(failures, chatID+"："+err.Error())
			continue
		}
		if kind == externalReminderKindOpen {
			result.opened[chatID] = messageID
		} else {
			result.closed[chatID] = messageID
		}
	}
	if len(failures) > 0 {
		return result, errors.New("发送外发提醒失败：" + strings.Join(failures, "；"))
	}
	return result, nil
}

// externalReminderTargets 提醒发到哪个应用、哪些群：跟这篇审核卡走（卡发在哪就回复在哪）。
func (p *publishPipeline) externalReminderTargets(job publishJob) (publishDeliverySettings, error) {
	if app := strings.TrimSpace(job.ReviewDeliveryAppID); app != "" && len(job.ReviewDeliveryChatIDs) > 0 {
		return publishDeliverySettings{AppProfileID: app, ChatIDs: append([]string(nil), job.ReviewDeliveryChatIDs...)}, nil
	}
	settings, _, err := p.reviewDeliveryForJob(job)
	return settings, err
}

func (p *publishPipeline) sendExternalReminderToChat(ctx context.Context, job publishJob, chatID, kind string, post map[string]any) (string, error) {
	if p.externalReminderSender != nil {
		return p.externalReminderSender(ctx, job, chatID, kind, post)
	}
	settings, apps, err := p.reviewDeliveryForJob(job)
	if err != nil {
		return "", err
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, chatID)
	if err != nil {
		return "", err
	}
	parent := p.reviewCardMessageIDForChat(job, chatID)
	if parent == "" {
		// 这个群里找不到审核卡（极旧的稿）：直接发到群里。
		content, err := json.Marshal(post)
		if err != nil {
			return "", err
		}
		return notifier.sendMessageWithID(ctx, "post", string(content))
	}
	return notifier.replyPostWithID(ctx, parent, post, true)
}

// externalReminderCardEditable 这个群里的审核卡还能在卡片里改。#233：补发九月旧稿时卡片早已过期，
// 卡上没有改稿按钮，提醒却叫人去点。读不到发送时间时按改不了处理——改稿页卡片新旧都能用。
func (p *publishPipeline) externalReminderCardEditable(ctx context.Context, job publishJob, chatID string) bool {
	if job.NotificationState == publishNotificationStateExpired || job.ExternalDistributionCardState == publishExternalCardExpired {
		return false
	}
	parent := p.reviewCardMessageIDForChat(job, chatID)
	if parent == "" {
		return false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, publishReviewCardAgeLookup)
	defer cancel()
	sentAt, err := p.reviewCardSentTime(lookupCtx, job, chatID, parent)
	if err != nil {
		log.Printf("publish: 读 #%d 审核卡发送时间失败，提醒改指向改稿页：%v", job.ID, err)
		return false
	}
	return pipelineNow(p).Sub(sentAt) < publishReviewCardEditableFor
}

func (p *publishPipeline) reviewCardSentTime(ctx context.Context, job publishJob, chatID, messageID string) (time.Time, error) {
	if p.reviewCardSentAt != nil {
		return p.reviewCardSentAt(ctx, job, chatID, messageID)
	}
	settings, apps, err := p.reviewDeliveryForJob(job)
	if err != nil {
		return time.Time{}, err
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, chatID)
	if err != nil {
		return time.Time{}, err
	}
	return notifier.messageCreateTime(ctx, messageID)
}

// reviewCardMessageIDForChat 这篇文章在这个群里的审核卡（聚合卡里任一篇的都算同一张）。
func (p *publishPipeline) reviewCardMessageIDForChat(job publishJob, chatID string) string {
	if parent := reviewCardMessageID(job, chatID); parent != "" {
		return parent
	}
	if batch, err := p.reviewCardBatch(job); err == nil {
		for _, candidate := range batch {
			if parent := reviewCardMessageID(candidate, chatID); parent != "" {
				return parent
			}
		}
	}
	return ""
}

// externalDistributionOwners 分发负责人：open_id 按审核应用区分，换了应用就 @ 不到原来的人。
func (p *publishPipeline) externalDistributionOwners(appProfileID string) []publishDistributionOwner {
	if p == nil || p.deliverySettings == nil {
		return nil
	}
	settings, err := p.deliverySettings.read()
	if err != nil {
		return nil
	}
	if strings.TrimSpace(settings.DistributionOwnersApp) != strings.TrimSpace(appProfileID) {
		return nil
	}
	return settings.DistributionOwners
}

// 富文本（post）的几种元素。
func postText(text string, bold bool) map[string]any {
	el := map[string]any{"tag": "text", "text": text}
	if bold {
		el["style"] = []string{"bold"}
	}
	return el
}

func postLink(text, href string) map[string]any {
	return map[string]any{"tag": "a", "text": text, "href": href}
}

func postAt(openID string) map[string]any {
	return map[string]any{"tag": "at", "user_id": openID}
}

func postMessage(lines [][]any) map[string]any {
	return map[string]any{"zh_cn": map[string]any{"title": "", "content": lines}}
}

// externalReminderPlatformNames 待修改的平台名，按表里的顺序。
func externalReminderPlatformNames(cfg externalTargets, drafts []publishExternalDraft) []string {
	names := make([]string, 0, len(drafts))
	for _, draft := range drafts {
		if target, ok := cfg.target(draft.AccountID); ok {
			names = append(names, target.label())
		}
	}
	return names
}

// buildExternalReminderOpening 话题开场：@ 负责人，一句话说清哪篇、要同步到哪、差几处；
// 每处一行粗体写不合要求的地方，下一行给能选的做法；最后一行说改完点哪里。
// inCard=false（审核卡改不了）时每一步都在改稿页里做，最后一行给改稿页链接。
func buildExternalReminderOpening(job publishJob, cfg externalTargets, owners []publishDistributionOwner, inCard bool) map[string]any {
	drafts := externalPendingFixes(job, cfg)
	names := externalReminderPlatformNames(cfg, drafts)
	total := 0
	for _, draft := range drafts {
		total += len(draft.Issues)
	}
	var head []any
	for _, owner := range owners {
		if id := strings.TrimSpace(owner.OpenID); id != "" {
			head = append(head, postAt(id), postText(" ", false))
		}
	}
	head = append(head, postText(fmt.Sprintf("#%d《%s》同步到%s前，还差 %d 处要改：",
		job.ID, reviewCardSafeOrDash(job.Title, 80), strings.Join(names, "、"), total), false))
	lines := [][]any{head}
	link := externalDistributionSidebarLink(job.ID)
	place := "卡片"
	if !inCard {
		place = "改稿页"
	}
	multi := len(drafts) > 1
	n := 0
	buttons := ""
	for i, draft := range drafts {
		name := names[i]
		profile := yunsuoGenericProfile
		if target, ok := cfg.target(draft.AccountID); ok {
			profile = yunsuoProfileForTarget(target)
		}
		buttons += "「保存并" + profile.syncAction(name) + "」"
		prefix := ""
		if multi {
			prefix = name
		}
		for _, issue := range draft.Issues {
			n++
			var problem string
			var options []any
			switch issue.Field {
			case publishExternalIssueTitle, publishExternalIssueSummary:
				what := "标题"
				suggested := draft.SuggestedTitle
				how := profile.titleCountNote()
				if issue.Field == publishExternalIssueSummary {
					what, suggested, how = "摘要", draft.SuggestedSummary, ""
				}
				if how != "" {
					how = "，" + how
				}
				problem = fmt.Sprintf("%d. %s%s超了 %d 字（%s最多 %d 字%s）", n, prefix, what, issue.Length-issue.Max, name, issue.Max, how)
				if suggested != "" {
					options = append(options, postText(fmt.Sprintf("%s里已填好建议「%s」，可以直接用，也可以改成自己的", place, reviewCardSafeText(suggested, 120)), false))
				} else if inCard {
					options = append(options, postText(fmt.Sprintf("在卡片输入框里改到 %d 字以内", issue.Max), false))
				} else {
					options = append(options, postText(fmt.Sprintf("在改稿页里改到 %d 字以内", issue.Max), false))
				}
			case publishExternalIssueCover:
				if issue.Missing {
					problem = fmt.Sprintf("%d. %s没有可用的封面（%s至少 %d×%d）", n, prefix, name, issue.MinWidth, issue.MinHeight)
				} else {
					problem = fmt.Sprintf("%d. %s封面太小（%s至少 %d×%d，现在 %d×%d）", n, prefix, name, issue.MinWidth, issue.MinHeight, issue.Width, issue.Height)
				}
				// 上传在侧边栏改稿页：点选、拖入或粘贴图片，传上去就换上（2026-10-12 用户裁定）。
				if !inCard {
					options = append(options, postText("在改稿页里点选一张正文大图，或者上传一张（传上去就换上）", false))
				} else if link != "" {
					options = append(options, postText("在卡片里点选一张正文大图，或者", false), postLink("上传一张", link), postText("（传上去就换上）", false))
				} else {
					options = append(options, postText("在卡片里点选一张正文大图", false))
				}
			default:
				continue
			}
			lines = append(lines, []any{postText(problem, true)}, options)
		}
	}
	if inCard {
		lines = append(lines, []any{postText("改好后点上面卡片里的"+buttons+"，就会同步。", false)})
	} else {
		// 卡片过期、群里没有卡、读不到卡片时间都走这里，只说去哪改，不猜原因。
		lines = append(lines, []any{postText("请", false), postLink("打开改稿页", link),
			postText("，改好后点"+buttons+"，就会同步。", false)})
	}
	return postMessage(lines)
}

// buildExternalReminderClosing 收尾：都改好了，正在同步（不再 @）。
func buildExternalReminderClosing(job publishJob, cfg externalTargets) map[string]any {
	var actions []string
	for _, target := range cfg.forJob(job) {
		if draft, ok := externalDraftFor(job, target.ID); ok && draft.EditedAt != "" {
			actions = append(actions, yunsuoProfileForTarget(target).syncAction(target.label()))
		}
	}
	text := "都改好了，正在同步到其他平台。"
	if len(actions) > 0 {
		text = "都改好了，正在" + strings.Join(actions, "、") + "。"
	}
	return postMessage([][]any{{postText(text, false)}})
}

// prepareExternalDraftAssets 待修改的平台：用 AI 缩写出更好的建议标题/摘要（不可用就保留
// 标点处截短），并把封面候选图传到审核应用，卡片里才能直接点选。只在后台跑，同一次检查只跑一回。
func (p *publishPipeline) prepareExternalDraftAssets(jobID int64) {
	job, err := p.store.byID(jobID)
	if err != nil {
		return
	}
	cfg, err := loadExternalTargets()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	type prepared struct {
		checkedAt      string
		title, summary string
		keys, keyApps  map[string]string
	}
	results := map[string]prepared{}
	for _, draft := range externalPendingFixes(job, cfg) {
		if draft.PreparedFor == draft.CheckedAt {
			continue
		}
		target, ok := cfg.target(draft.AccountID)
		if !ok {
			continue
		}
		profile := yunsuoProfileForTarget(target)
		out := prepared{checkedAt: draft.CheckedAt, keys: map[string]string{}, keyApps: map[string]string{}}
		for _, issue := range draft.Issues {
			switch issue.Field {
			case publishExternalIssueTitle:
				if text, err := p.suggestExternalText(ctx, "标题", issue.Current, issue.Min, issue.Max); err == nil {
					out.title = text
				} else {
					log.Printf("publish: AI 缩写 #%d %s标题失败，保留截短建议: %v", jobID, profile.Name, err)
				}
			case publishExternalIssueSummary:
				if text, err := p.suggestExternalText(ctx, "摘要", issue.Current, 1, issue.Max); err == nil {
					out.summary = text
				} else {
					log.Printf("publish: AI 缩写 #%d %s摘要失败，保留截短建议: %v", jobID, profile.Name, err)
				}
			}
		}
		for _, candidate := range draft.Candidates {
			if candidate.ImgKey != "" && candidate.ImgApp == job.ReviewDeliveryAppID {
				continue
			}
			key, app, err := p.externalCoverImgKey(ctx, job, candidate.URL)
			if err != nil {
				log.Printf("publish: 上传 #%d 封面候选图失败: %v", jobID, err)
				continue
			}
			out.keys[candidate.URL], out.keyApps[candidate.URL] = key, app
		}
		results[draft.AccountID] = out
	}
	if len(results) == 0 {
		return
	}
	_, swapped, err := p.store.updateIf(jobID, func(publishJob) bool { return true }, func(current *publishJob) {
		for accountID, out := range results {
			draft, ok := externalDraftFor(*current, accountID)
			if !ok || draft.CheckedAt != out.checkedAt {
				continue // 准备期间又重查过，这份结果作废
			}
			fields := externalIssueFields(draft.Issues)
			if issue, ok := fields[publishExternalIssueTitle]; ok && out.title != "" && runeLen(out.title) <= issue.Max {
				draft.SuggestedTitle, draft.SuggestedBy = out.title, "ai"
			}
			if issue, ok := fields[publishExternalIssueSummary]; ok && out.summary != "" && runeLen(out.summary) <= issue.Max {
				draft.SuggestedSummary, draft.SuggestedBy = out.summary, "ai"
			}
			for i := range draft.Candidates {
				if key := out.keys[draft.Candidates[i].URL]; key != "" {
					draft.Candidates[i].ImgKey, draft.Candidates[i].ImgApp = key, out.keyApps[draft.Candidates[i].URL]
				}
			}
			draft.PreparedFor = out.checkedAt
			setExternalDraft(current, draft)
		}
		enqueueExternalDistributionCardUpdate(current)
	})
	if err == nil && swapped {
		p.scheduleExternalDistributionCardUpdate(jobID)
	}
}

const externalShortenPrompt = `你是中文新媒体编辑。把用户给的文章%s缩写到不超过 %d 个字（含标点和空格）%s。
要求：保留品牌名、产品名和核心信息；不编造原文没有的内容；不加引号、书名号包裹或解释。
只输出改好的%s本身。`

// suggestExternalText 让 AI 把标题/摘要缩到平台上限；结果不合要求就报错，由调用方保留截短建议。
func (p *publishPipeline) suggestExternalText(ctx context.Context, kind, current string, min, max int) (string, error) {
	if p.externalSuggest != nil {
		return p.externalSuggest(ctx, kind, current, min, max)
	}
	floor := ""
	if min > 1 {
		floor = fmt.Sprintf("、不少于 %d 个字", min)
	}
	prompt := fmt.Sprintf(externalShortenPrompt, kind, max, floor, kind)
	text, err := aiResponsesCall(ctx, aiModel(aiTierFlash), prompt, []map[string]any{{"type": "input_text", "text": current}})
	if err != nil {
		return "", err
	}
	return validExternalSuggestion(text, min, max)
}

func validExternalSuggestion(text string, min, max int) (string, error) {
	text = strings.TrimSpace(strings.Split(strings.TrimSpace(text), "\n")[0])
	text = strings.Trim(text, "\"“”'‘’「」")
	text = compactYunsuoText(text)
	switch n := runeLen(text); {
	case text == "":
		return "", errors.New("AI 没有给出结果")
	case n > max || n < min:
		return "", fmt.Errorf("AI 给出 %d 字，不在 %d–%d 字之间", n, min, max)
	}
	return text, nil
}

// externalCoverImgKey 把封面候选图传到这篇稿的审核应用（img_key 与应用绑定）。
func (p *publishPipeline) externalCoverImgKey(ctx context.Context, job publishJob, imgURL string) (string, string, error) {
	if p.externalCoverUploader != nil {
		return p.externalCoverUploader(ctx, job, imgURL)
	}
	settings, apps, err := p.reviewDeliveryForJob(job)
	if err != nil {
		return "", "", err
	}
	if len(settings.ChatIDs) == 0 {
		return "", "", errors.New("没有配置审核群")
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, settings.ChatIDs[0])
	if err != nil {
		return "", "", err
	}
	key, err := notifier.uploadImageByURL(ctx, imgURL)
	return key, settings.AppProfileID, err
}

// recoverExternalReminder 启动时把中断的提醒与未准备完的待修改项接着做完。
func (p *publishPipeline) recoverExternalReminder(job publishJob) {
	switch job.ExternalDistributionReminder.State {
	case publishExternalCardSending:
		reset, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.ExternalDistributionReminder.State == publishExternalCardSending
		}, func(current *publishJob) {
			current.ExternalDistributionReminder.State = publishExternalCardPending
		})
		if err == nil && swapped {
			p.scheduleExternalReminder(reset.ID)
		}
	case publishExternalCardPending:
		p.scheduleExternalReminder(job.ID)
	}
	for _, draft := range job.ExternalDistributionDrafts {
		if len(draft.Issues) > 0 && draft.PreparedFor != draft.CheckedAt {
			if _, pushed := externalLaneFor(job, draft.AccountID); !pushed {
				p.spawnExternalWork(func() { p.prepareExternalDraftAssets(job.ID) })
				break
			}
		}
	}
}
