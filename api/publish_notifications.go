package main

// publish_notifications.go — 发布通知持久 outbox。任务状态与 notification_kind 在
// 同一次 JSON 原子写中提交；发送前 CAS claim，崩溃遗留 sending 在启动时重置，失败
// 按退避重试。语义是 at-least-once：极端「飞书成功、本地 ack 失败」可能重复，但不会漏。

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"
)

const (
	publishNotificationReviewPending      = "review_pending"
	publishNotificationPublished          = "published"
	publishNotificationRejected           = "rejected"
	publishNotificationPublicizeFailed    = "publicize_failed"
	publishNotificationPublicizeUncertain = "publicize_uncertain"
	publishNotificationOffline            = "offline"
	publishNotificationRepublished        = "republished"
	publishNotificationUnpublishFailed    = "unpublish_failed"
	publishNotificationUnpublishUncertain = "unpublish_uncertain"
	publishNotificationRepublishFailed    = "republish_failed"
	publishNotificationRepublishUncertain = "republish_uncertain"

	publishNotificationStatePending    = "pending"
	publishNotificationStateSending    = "sending"
	publishNotificationStateDeadLetter = "dead_letter"

	publishNotificationMaxAttempts = 10
	publishNotificationMaxDelay    = 30 * time.Minute
)

func enqueuePublishNotification(job *publishJob, kind string) {
	job.NotificationSeq++
	job.NotificationKind = kind
	job.NotificationState = publishNotificationStatePending
	job.NotificationAttempts = 0
	job.NotificationError = ""
	job.NotificationSentAt = ""
}

func cancelPublishNotification(job *publishJob) {
	job.NotificationSeq++ // 使任何在途旧发送结果无法 ack/回写当前 outbox。
	job.NotificationKind = ""
	job.NotificationState = ""
	job.NotificationAttempts = 0
	job.NotificationError = ""
}

func (p *publishPipeline) schedulePublishNotification(jobID int64) {
	p.spawn(func() { p.dispatchPublishNotification(jobID) })
}

func (p *publishPipeline) dispatchPublishNotification(jobID int64) {
	claimed, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.NotificationKind != "" && current.NotificationState == publishNotificationStatePending
	}, func(j *publishJob) {
		j.NotificationState = publishNotificationStateSending
		j.NotificationAttempts++
		j.NotificationError = ""
	})
	if err != nil {
		log.Printf("publish: claim job %d notification failed: %v", jobID, err)
		return
	}
	if !swapped {
		return
	}

	sendErr := p.sendPublishNotification(context.Background(), claimed)
	if sendErr == nil {
		_, _, ackErr := p.store.updateIf(jobID, func(current publishJob) bool {
			return current.NotificationSeq == claimed.NotificationSeq &&
				current.NotificationKind == claimed.NotificationKind &&
				current.NotificationState == publishNotificationStateSending
		}, func(j *publishJob) {
			j.NotificationKind = ""
			j.NotificationState = ""
			j.NotificationError = ""
			j.NotificationSentAt = p.now().UTC().Format(time.RFC3339)
		})
		if ackErr != nil {
			log.Printf("publish: ack job %d notification failed（启动恢复会重发）: %v", jobID, ackErr)
		}
		return
	}

	failed, restored, persistErr := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.NotificationSeq == claimed.NotificationSeq &&
			current.NotificationKind == claimed.NotificationKind &&
			current.NotificationState == publishNotificationStateSending
	}, func(j *publishJob) {
		if j.NotificationAttempts >= publishNotificationMaxAttempts {
			j.NotificationState = publishNotificationStateDeadLetter
		} else {
			j.NotificationState = publishNotificationStatePending
		}
		j.NotificationError = truncateRunes(strings.Join(strings.Fields(sendErr.Error()), " "), 500)
	})
	if persistErr != nil {
		log.Printf("publish: persist job %d notification failure failed: %v", jobID, persistErr)
		return
	}
	if restored && failed.NotificationState == publishNotificationStatePending && p.retryNotification != nil {
		delay := publishNotificationRetryDelay(failed.NotificationAttempts)
		delay += randomNotificationJitter(delay / 4)
		p.retryNotification(delay, func() { p.schedulePublishNotification(jobID) })
	}
}

func publishNotificationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > publishNotificationMaxAttempts {
		attempt = publishNotificationMaxAttempts
	}
	delay := time.Duration(1<<uint(attempt)) * time.Second
	if delay > publishNotificationMaxDelay {
		return publishNotificationMaxDelay
	}
	return delay
}

func randomNotificationJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)+1))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}

func (p *publishPipeline) sendPublishNotification(ctx context.Context, job publishJob) error {
	switch job.NotificationKind {
	case publishNotificationReviewPending:
		return p.sendReviewPendingNotification(ctx, job)
	case publishNotificationPublished:
		return p.sendTerminalPublishNotification(ctx, job, fmt.Sprintf(
			"【网站同步】已发布\n标题：%s\n链接：%s", job.Title, job.ArticleURL))
	case publishNotificationRejected:
		return p.sendTerminalPublishNotification(ctx, job, fmt.Sprintf(
			"【网站同步】已驳回\n标题：%s\n文章留在 FoodTalks 草稿态，不对外可见", job.Title))
	case publishNotificationOffline:
		return p.sendTerminalPublishNotification(ctx, job, fmt.Sprintf(
			"【网站同步】已下线\n标题：%s\n文章已从 FoodTalks 前台下线，仍可在窗口内重新发布。", job.Title))
	case publishNotificationRepublished:
		return p.sendTerminalPublishNotification(ctx, job, fmt.Sprintf(
			"【网站同步】已重新发布\n标题：%s\n文章已恢复为 FoodTalks 公开状态。", job.Title))
	case publishNotificationPublicizeFailed:
		return p.sendTerminalCardNotification(ctx, job, fmt.Sprintf(
			"【网站同步】发布失败\n标题：%s\n任务 #%d 在发出公开请求前已确认失败，网站仍保持草稿态；修复配置后可安全重试。\n原因：%s",
			job.Title, job.ID, job.Error))
	case publishNotificationPublicizeUncertain:
		return p.sendTerminalCardNotification(ctx, job, fmt.Sprintf(
			"【网站同步】公开结果待核对\n标题：%s\nFoodTalks 资讯 ID：%d\n任务 #%d 不会自动重试，请先在网站前台/后台确认是否已公开，再调用人工对账。\n原因：%s",
			job.Title, job.FoodTalksNewsID, job.ID, job.Error))
	case publishNotificationUnpublishFailed:
		return p.sendTerminalCardNotification(ctx, job, fmt.Sprintf(
			"【网站同步】下线失败\n标题：%s\n任务 #%d 尚未确认下线，修复配置后可重试。\n原因：%s",
			job.Title, job.ID, job.Error))
	case publishNotificationUnpublishUncertain:
		return p.sendTerminalCardNotification(ctx, job, fmt.Sprintf(
			"【网站同步】下线结果待核对\n标题：%s\nFoodTalks 资讯 ID：%d\n任务 #%d 不会自动重试，请先确认文章当前是否已下线，再进行对账。\n原因：%s",
			job.Title, job.FoodTalksNewsID, job.ID, job.Error))
	case publishNotificationRepublishFailed:
		return p.sendTerminalCardNotification(ctx, job, fmt.Sprintf(
			"【网站同步】重新发布失败\n标题：%s\n任务 #%d 仍保持下线状态，修复配置后可重试。\n原因：%s",
			job.Title, job.ID, job.Error))
	case publishNotificationRepublishUncertain:
		return p.sendTerminalCardNotification(ctx, job, fmt.Sprintf(
			"【网站同步】重新发布结果待核对\n标题：%s\nFoodTalks 资讯 ID：%d\n任务 #%d 不会自动重试，请先确认文章当前是否已公开，再进行对账。\n原因：%s",
			job.Title, job.FoodTalksNewsID, job.ID, job.Error))
	default:
		return fmt.Errorf("未知发布通知类型 %q", job.NotificationKind)
	}
}

// sendTerminalPublishNotification 更新 approve/reject 对应的终态卡，让回调
// 返回的「请求已受理」进度卡在同一条消息里落到最终结果。
func (p *publishPipeline) sendTerminalPublishNotification(ctx context.Context, job publishJob, fallbackText string) error {
	return p.sendTerminalCardNotification(ctx, job, fallbackText)
}

// sendTerminalCardNotification 统一串行所有终态/后续状态卡更新，并在取得发送锁后
// 重读任务。网站生命周期 outbox 与外部分发 outbox 虽然独立持久化，但不能拿各自
// claim 时的旧快照覆盖同一张飞书卡。
func (p *publishPipeline) sendTerminalCardNotification(ctx context.Context, job publishJob, fallbackText string) error {
	p.reviewCardUpdateMu.Lock()
	defer p.reviewCardUpdateMu.Unlock()
	latest, err := p.store.byID(job.ID)
	if err != nil {
		return fmt.Errorf("发送卡片前重读任务 #%d 失败: %w", job.ID, err)
	}
	return p.sendTerminalCardNotificationUnlocked(ctx, latest, buildReviewCardForCurrentState(latest), fallbackText)
}

// sendTerminalCardNotificationUnlocked 先尝试原位更新审核卡；旧任务没有
// message_id、或更新接口暂时不可用时，兼容发送一张新终态卡，再降级文本。
// 调用方必须持有 reviewCardUpdateMu。
func (p *publishPipeline) sendTerminalCardNotificationUnlocked(ctx context.Context, job publishJob, card map[string]any, fallbackText string) error {
	if p != nil && p.deliverySettings != nil {
		if err := p.sendConfiguredReviewCardToTargets(ctx, job, card); err == nil {
			return nil
		} else {
			// 配置过应用/多群后，不能退回旧的单群 notifier；否则用户会看到
			// 两张卡，且终态可能落到错误的群里。让 outbox 按原配置重试。
			return err
		}
	}
	if messageID := strings.TrimSpace(job.ReviewCardMsgID); messageID != "" && p.updateCard != nil {
		if err := p.updateCard(ctx, messageID, card); err == nil {
			return nil
		} else {
			log.Printf("publish: 原位更新审核卡失败，兼容发送新终态卡: %v", err)
		}
	}
	if p.notifyCard != nil {
		if err := p.notifyCard(ctx, card); err == nil {
			return nil
		} else {
			log.Printf("publish: 终态卡片发送失败，降级文本通知: %v", err)
		}
	}
	if p.notify == nil {
		return errors.New("飞书终态卡片与文本通知均未配置")
	}
	return p.notify(ctx, fallbackText)
}

// buildReviewCardForCurrentState 是两个卡片 outbox 的共同渲染入口。外部分发状态
// 会附着在网站当前真实生命周期上，而不是反过来把网站阶段强行还原为 published。
func buildReviewCardForCurrentState(job publishJob) map[string]any {
	switch job.Stage {
	case publishStageAwaitingReview:
		return buildReviewPendingCard(job, strings.TrimSpace(job.ReviewCoverImgKey))
	case publishStagePublicizing, publishStagePublicizingUpdating:
		return buildReviewPublishingCard(job)
	case publishStagePublished, publishStageOffline, publishStageRejected, publishStageSupersededDuplicate:
		return buildReviewDoneCard(job)
	case publishStageUnpublishing, publishStageUnpublishingUpdating:
		return buildReviewLifecycleProgressCard(job, feishuCardActionUnpublish)
	case publishStageRepublishing, publishStageRepublishingUpdating:
		return buildReviewLifecycleProgressCard(job, feishuCardActionRepublish)
	case publishStageReconciliationRequired, publishStagePublicizeReconciliationRequired,
		publishStageUnpublishReconciliationRequired, publishStageRepublishReconciliationRequired,
		publishStageStagingCreating:
		return buildReviewIssueCard(job, true)
	case publishStageFailed:
		return buildReviewIssueCard(job, false)
	default:
		// 只有已经存在审核卡的任务才会走到这里；未知新阶段宁可展示异常卡，
		// 也不能用旧的“已发布”卡片掩盖当前状态。
		return buildReviewIssueCard(job, true)
	}
}

func (p *publishPipeline) sendReviewPendingNotification(ctx context.Context, job publishJob) error {
	p.reviewCardUpdateMu.Lock()
	defer p.reviewCardUpdateMu.Unlock()
	return p.sendReviewPendingNotificationUnlocked(ctx, job)
}

func (p *publishPipeline) sendReviewPendingNotificationUnlocked(ctx context.Context, job publishJob) error {
	coverImgKey := ""
	if strings.TrimSpace(job.CoverURL) != "" {
		var key string
		var err error
		if p != nil && p.deliverySettings != nil {
			key, err = p.uploadReviewCoverForJob(ctx, job)
		} else if p.coverImgKey != nil {
			key, err = p.coverImgKey(ctx, job.CoverURL)
		}
		if err != nil {
			log.Printf("publish: WARN 封面转存飞书失败（卡片将不带图）: %v", err)
		} else {
			coverImgKey = key
			// 记住 img_key，终态卡直接复用；存不下也只记日志，绝不阻塞发卡。
			if updated, updateErr := p.store.update(job.ID, func(current *publishJob) {
				current.ReviewCoverImgKey = key
			}); updateErr != nil {
				log.Printf("publish: WARN 保存审核卡封面 img_key 失败（终态卡将不带图）: %v", updateErr)
			} else {
				job = updated
			}
		}
	}
	// 上传封面期间任务可能已经被审核并推进到发布/下线。发送前必须在同一张
	// 卡的串行锁内重读；旧 review_pending 绝不能最后覆盖终态卡。
	latest, err := p.store.byID(job.ID)
	if err != nil {
		return fmt.Errorf("发送待审核卡前重读任务 #%d 失败: %w", job.ID, err)
	}
	job = latest
	if job.Stage != publishStageAwaitingReview {
		return p.sendTerminalCardNotificationUnlocked(ctx, job, buildReviewCardForCurrentState(job), fmt.Sprintf(
			"【网站同步】任务 #%d 状态已变化\n标题：%s", job.ID, job.Title))
	}
	coverImgKey = strings.TrimSpace(job.ReviewCoverImgKey)
	// 同题防重提醒（best effort）：发卡/刷卡前查一次 CMS，命中即在卡上标红。
	job = p.refreshSiteDuplicateFlag(ctx, job)
	card := buildReviewPendingCard(job, coverImgKey)
	if p != nil && p.deliverySettings != nil {
		return p.sendConfiguredReviewCardToTargets(ctx, job, card)
	}
	if messageID := strings.TrimSpace(job.ReviewCardMsgID); messageID != "" && p.updateCard != nil {
		if err := p.updateCard(ctx, messageID, card); err == nil {
			return nil
		} else {
			log.Printf("publish: 原位恢复待审核卡失败，兼容发送新审核卡: %v", err)
		}
	}
	if p.notifyCardWithID != nil {
		messageID, err := p.notifyCardWithID(ctx, card)
		if err == nil {
			if rememberErr := p.rememberReviewCardMessageID(job, messageID); rememberErr != nil {
				return rememberErr
			}
			return nil
		}
		log.Printf("publish: 审核卡片发送失败，降级文本通知: %v", err)
	}
	if p.notifyCard != nil {
		if err := p.notifyCard(ctx, card); err == nil {
			return nil
		} else {
			log.Printf("publish: 审核卡片发送失败，降级文本通知: %v", err)
		}
	}
	if p.notify == nil {
		return errors.New("飞书审核卡片与文本通知均未配置")
	}
	instruction := fmt.Sprintf("请对发布任务 #%d 操作通过或驳回", job.ID)
	if job.RiskLevel == publishRiskBlock {
		instruction = fmt.Sprintf("发布任务 #%d 为阻断级，仅可驳回或人工处理风险后重新评估", job.ID)
	}
	return p.notify(ctx, fmt.Sprintf(
		"【网站同步】待审核\n标题：%s\n原文：%s\n%s\n草稿已进站（外界不可见），FoodTalks 资讯 ID：%d\n%s",
		job.Title, job.SourceURL, formatPublishRiskText(job), job.FoodTalksNewsID, instruction))
}

// sendConfiguredReviewCardToTargets 是应用 + 多群配置的唯一卡片投递路径。
// 每个群各自持久化 message_id：部分群发送成功后，重试只更新已成功的卡，
// 不会因为某一个群暂时失败而在其它群追加重复卡片。
func (p *publishPipeline) sendConfiguredReviewCardToTargets(ctx context.Context, job publishJob, card map[string]any) error {
	settings, _, err := p.reviewDeliveryForJob(job)
	if err != nil {
		return err
	}
	if len(settings.ChatIDs) == 0 {
		return errors.New("发布审核通知尚未选择审核群")
	}
	for _, chatID := range settings.ChatIDs {
		chatID = strings.TrimSpace(chatID)
		if chatID == "" {
			continue
		}
		messageID := reviewCardMessageID(job, chatID)
		if messageID != "" {
			if err := p.updateConfiguredReviewCard(ctx, job, chatID, messageID, card); err != nil {
				return fmt.Errorf("更新审核群 %s 的卡片失败：%w", chatID, err)
			}
			continue
		}
		messageID, err := p.sendConfiguredReviewCard(ctx, job, chatID, card)
		if err != nil {
			return fmt.Errorf("向审核群 %s 发送卡片失败：%w", chatID, err)
		}
		if err := p.rememberReviewCardMessageIDForChat(job, chatID, messageID); err != nil {
			return err
		}
		job.ReviewCardMsgIDs = cloneStringMap(job.ReviewCardMsgIDs)
		if job.ReviewCardMsgIDs == nil {
			job.ReviewCardMsgIDs = map[string]string{}
		}
		job.ReviewCardMsgIDs[chatID] = messageID
		if job.ReviewCardMsgID == "" {
			job.ReviewCardMsgID = messageID
		}
	}
	return nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

// rememberReviewCardMessageID 把发送成功得到的卡片消息 ID 写回任务。允许旧 ID
// 等于发送前快照时被新卡替换（例如原卡已被撤回），但不会覆盖并发重试已经登记的
// 更新 ID。这样审核回调取消 review_pending 后，发送线程仍能把 ID 留下来供终态使用。
func (p *publishPipeline) rememberReviewCardMessageID(job publishJob, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return errors.New("飞书审核卡发送成功但没有 message_id")
	}
	expected := strings.TrimSpace(job.ReviewCardMsgID)
	_, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		currentID := strings.TrimSpace(current.ReviewCardMsgID)
		return currentID == "" || currentID == expected || currentID == messageID
	}, func(current *publishJob) {
		current.ReviewCardMsgID = messageID
	})
	if err != nil {
		return fmt.Errorf("保存审核卡 message_id 失败: %w", err)
	}
	if swapped {
		return nil
	}
	// 另一个并发发送已经登记了 canonical 卡片 ID；不因重复通知把 outbox
	// 重新打回失败，终态会更新已经登记的那一张。
	current, loadErr := p.store.byID(job.ID)
	if loadErr != nil {
		return fmt.Errorf("确认审核卡 message_id 失败: %w", loadErr)
	}
	if strings.TrimSpace(current.ReviewCardMsgID) != "" {
		return nil
	}
	return errors.New("保存审核卡 message_id 未生效")
}

// recallReviewCards 撤回任务在各审核群里已发出的卡片。撤回是尽力而为：任一失败
// 只记 WARN（如超出撤回窗口），调用方随后重发新卡即可收敛。
func (p *publishPipeline) recallReviewCards(ctx context.Context, job publishJob) {
	recallOne := func(chatID, messageID string) {
		messageID = strings.TrimSpace(messageID)
		if messageID == "" {
			return
		}
		var err error
		if p.deliverySettings != nil {
			settings, apps, resolveErr := p.reviewDeliveryForJob(job)
			if resolveErr != nil {
				err = resolveErr
			} else {
				target := chatID
				if target == "" && len(settings.ChatIDs) > 0 {
					target = settings.ChatIDs[0]
				}
				notifier, notifierErr := p.configuredReviewNotifier(settings, apps, target)
				if notifierErr != nil {
					err = notifierErr
				} else {
					err = notifier.recallMessage(ctx, messageID)
				}
			}
		} else if p.recallCard != nil {
			err = p.recallCard(ctx, messageID)
		} else {
			err = errors.New("撤回能力未配置")
		}
		if err != nil {
			log.Printf("publish: WARN 任务 %d 旧审核卡撤回失败（将直接重发新卡）message_id=%s: %v", job.ID, messageID, err)
		}
	}
	seen := map[string]bool{}
	for chatID, messageID := range job.ReviewCardMsgIDs {
		recallOne(chatID, messageID)
		seen[strings.TrimSpace(messageID)] = true
	}
	if legacy := strings.TrimSpace(job.ReviewCardMsgID); legacy != "" && !seen[legacy] {
		recallOne("", legacy)
	}
}

// recoverPublishNotification 将崩溃遗留 sending 重新置 pending，再调度 drain。
func (p *publishPipeline) recoverPublishNotification(job publishJob) {
	if job.NotificationKind == "" {
		return
	}
	if job.NotificationState == publishNotificationStateDeadLetter {
		return
	}
	if job.NotificationState != publishNotificationStatePending {
		updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.NotificationSeq == job.NotificationSeq && current.NotificationKind != ""
		}, func(j *publishJob) {
			j.NotificationState = publishNotificationStatePending
			j.NotificationError = "进程在通知发送中断，已于启动时重新排队"
		})
		if err != nil {
			log.Printf("publish: recover job %d notification claim failed: %v", job.ID, err)
			return
		}
		if !swapped {
			return
		}
		job = updated
	}
	p.schedulePublishNotification(job.ID)
}

func (p *publishPipeline) replayPublishNotification(jobID int64) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.NotificationKind != "" && current.NotificationState == publishNotificationStateDeadLetter
	}, func(j *publishJob) {
		j.NotificationState = publishNotificationStatePending
		j.NotificationAttempts = 0
		j.NotificationError = ""
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return job, fmt.Errorf("任务没有可重放的通知 dead letter")
	}
	p.schedulePublishNotification(jobID)
	return job, nil
}

// recheckPublishRisk 用当前 prompt 重跑一次风险检查，并把结论刷进审核卡。
//
// 与 refreshReviewCard 的分工：那个只换卡片样式，用的还是当初存下来的风险结论；
// 这个真的再调一次模型。prompt 改版后，历史任务的结论仍是旧口径出的——想让新
// 检查能力作用在存量文章上，只能重跑。
//
// 三条边界：
//   - **只允许 awaiting_review**。已发布/已驳回的任务重跑没有意义，正在跑流水线的
//     任务重跑会与阶段推进抢同一条记录。
//   - **只碰 Risk* 字段**，不改正文、不改阶段、不触发任何对外发布。
//   - 重跑必然改变 publishReviewCardVersion，旧卡上的按钮随之失效——这是**正确**的：
//     风险结论变了，基于旧结论的审批意图本就该作废。任务尚未审批，不存在丢失授权。
func (p *publishPipeline) recheckPublishRisk(ctx context.Context, jobID int64) (publishJob, error) {
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	if job.Stage != publishStageAwaitingReview {
		return job, fmt.Errorf("只有待审核任务可以重跑风险检查，当前阶段为 %s", job.Stage)
	}
	result := assessPublishRiskWithFallback(ctx, job, p.assessRisk)
	// 重跑失败**绝不能**覆盖已有结论。fallback 会产出一条 system「检查未完成」，
	// 写进去等于用一次失败的重试，把这篇文章原本已经查出来的问题全部抹掉——
	// 比不重跑糟得多。失败就保持原样并如实报错（2026-08-08 首次真机重跑踩到）。
	if publishRiskHasSystemFailure(result.Items) {
		reason := strings.TrimSpace(result.Notes)
		if reason == "" {
			reason = "模型未产出可验证的结构化结果"
		}
		return job, fmt.Errorf("重跑未完成，已保留原有结论：%s", reason)
	}
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		// 重跑期间任务被审批或正文被换掉，结论就作废——不能把旧正文的结论盖到新内容上。
		return current.Stage == publishStageAwaitingReview &&
			current.ContentVersion == job.ContentVersion &&
			strings.TrimSpace(current.ReviewAction) == ""
	}, func(j *publishJob) {
		j.RiskLevel = result.Level
		j.RiskItems = append([]publishRiskItem(nil), result.Items...)
		j.RiskHeadline = result.Headline
		j.RiskNotes = result.Notes
		j.RiskRaw = result.Raw
	})
	if err != nil {
		return job, err
	}
	if !swapped {
		return job, errors.New("任务在重跑期间已被审核或内容已变化，结论未写入")
	}
	return updated, p.sendReviewPendingNotification(ctx, updated)
}

// refreshReviewCard 用当前代码重建审核卡并推到已配置的审核群。
//
// 与 replayPublishNotification 的分工：那个只救「发送失败躺在 dead letter 里」的
// 通知；这个管「已经发成功、但卡片样式或内容需要按新版本重出」的情况——卡片改版
// 后，历史任务的卡还停在旧样式上，群里回看到的仍是旧信息。
//
// 已记过 message_id 的群走原位更新（sendConfiguredReviewCardToTargets 内部判定），
// 所以刷新不会在群里刷屏，看到的是同一张卡变成新样子。
// 通知状态机不参与：这是人工触发的一次性运维动作，不改 NotificationState，
// 免得与正在跑的通知 worker 抢同一条记录。
func (p *publishPipeline) refreshReviewCard(ctx context.Context, jobID int64, resend bool) (publishJob, error) {
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	// resend：先撤回群里已有的卡，再清掉 message_id 记录，让下面的发送路径发一张
	// 全新卡（而不是原位更新）。撤回失败（超窗等）只记日志继续——新卡必须发出去。
	if resend {
		p.recallReviewCards(ctx, job)
		cleared, clearErr := p.store.update(job.ID, func(current *publishJob) {
			current.ReviewCardMsgIDs = nil
			current.ReviewCardMsgID = ""
		})
		if clearErr != nil {
			return job, fmt.Errorf("清除旧卡 message_id 失败，未重发：%w", clearErr)
		}
		job = cleared
	}
	// 旧任务没存过封面 img_key（该字段晚于它们加入），这里补传一次，
	// 否则刷新出来的卡片仍是无封面单列。失败只降级为不带图，不阻断刷新。
	if strings.TrimSpace(job.ReviewCoverImgKey) == "" && strings.TrimSpace(job.CoverURL) != "" {
		if key, upErr := p.uploadReviewCoverForJob(ctx, job); upErr != nil {
			log.Printf("publish: WARN 刷新卡片时封面转存失败（卡片将不带图）: %v", upErr)
		} else if updated, updErr := p.store.update(job.ID, func(current *publishJob) {
			current.ReviewCoverImgKey = key
		}); updErr != nil {
			log.Printf("publish: WARN 刷新卡片时保存封面 img_key 失败: %v", updErr)
		} else {
			job = updated
		}
	}
	switch job.Stage {
	case publishStageAwaitingReview:
		return job, p.sendReviewPendingNotification(ctx, job)
	case publishStagePublished, publishStageOffline, publishStageRejected:
		return job, p.sendTerminalCardNotification(ctx, job, fmt.Sprintf(
			"【网站同步】任务 #%d 审核卡已刷新\n标题：%s", job.ID, job.Title))
	case publishStageFailed:
		return job, p.sendTerminalCardNotification(ctx, job, fmt.Sprintf(
			"【网站同步】任务 #%d 审核卡已刷新\n标题：%s", job.ID, job.Title))
	default:
		return job, fmt.Errorf("任务当前阶段（%s）没有对应的审核卡，无法刷新", job.Stage)
	}
}
