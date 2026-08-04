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
	case publishNotificationPublicizeFailed:
		return p.sendTerminalCardNotification(ctx, job, buildReviewIssueCard(job, false), fmt.Sprintf(
			"【网站同步】发布失败\n标题：%s\n任务 #%d 在发出公开请求前已确认失败，网站仍保持草稿态；修复配置后可安全重试。\n原因：%s",
			job.Title, job.ID, job.Error))
	case publishNotificationPublicizeUncertain:
		return p.sendTerminalCardNotification(ctx, job, buildReviewIssueCard(job, true), fmt.Sprintf(
			"【网站同步】公开结果待核对\n标题：%s\nFoodTalks 资讯 ID：%d\n任务 #%d 不会自动重试，请先在网站前台/后台确认是否已公开，再调用人工对账。\n原因：%s",
			job.Title, job.FoodTalksNewsID, job.ID, job.Error))
	default:
		return fmt.Errorf("未知发布通知类型 %q", job.NotificationKind)
	}
}

// sendTerminalPublishNotification 更新 approve/reject 对应的终态卡，让回调
// 返回的「请求已受理」进度卡在同一条消息里落到最终结果。
func (p *publishPipeline) sendTerminalPublishNotification(ctx context.Context, job publishJob, fallbackText string) error {
	return p.sendTerminalCardNotification(ctx, job, buildReviewDoneCard(job), fallbackText)
}

// sendTerminalCardNotification 先尝试原位更新审核卡；旧任务没有 message_id、
// 或更新接口暂时不可用时，兼容发送一张新终态卡，再降级文本。新任务的正常路径
// 只会命中第一分支，因此不会在群里追加第二张结果卡。
func (p *publishPipeline) sendTerminalCardNotification(ctx context.Context, job publishJob, card map[string]any, fallbackText string) error {
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

func (p *publishPipeline) sendReviewPendingNotification(ctx context.Context, job publishJob) error {
	coverImgKey := ""
	if p.coverImgKey != nil && strings.TrimSpace(job.CoverURL) != "" {
		key, err := p.coverImgKey(ctx, job.CoverURL)
		if err != nil {
			log.Printf("publish: WARN 封面转存飞书失败（卡片将不带图）: %v", err)
		} else {
			coverImgKey = key
		}
	}
	card := buildReviewPendingCard(job, coverImgKey)
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
