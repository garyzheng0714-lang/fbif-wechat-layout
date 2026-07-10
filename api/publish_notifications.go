package main

// publish_notifications.go — 发布通知持久 outbox。任务状态与 notification_kind 在
// 同一次 JSON 原子写中提交；发送前 CAS claim，崩溃遗留 sending 在启动时重置，失败
// 按退避重试。语义是 at-least-once：极端「飞书成功、本地 ack 失败」可能重复，但不会漏。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	publishNotificationReviewPending      = "review_pending"
	publishNotificationPublished          = "published"
	publishNotificationRejected           = "rejected"
	publishNotificationPublicizeUncertain = "publicize_uncertain"

	publishNotificationStatePending = "pending"
	publishNotificationStateSending = "sending"
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
		j.NotificationState = publishNotificationStatePending
		j.NotificationError = truncateRunes(strings.Join(strings.Fields(sendErr.Error()), " "), 500)
	})
	if persistErr != nil {
		log.Printf("publish: persist job %d notification failure failed: %v", jobID, persistErr)
		return
	}
	if restored && p.retryNotification != nil {
		delay := publishNotificationRetryDelay(failed.NotificationAttempts)
		p.retryNotification(delay, func() { p.dispatchPublishNotification(jobID) })
	}
}

func publishNotificationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		attempt = 5
	}
	return time.Duration(1<<uint(attempt)) * time.Second
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
	case publishNotificationPublicizeUncertain:
		if p.notify == nil {
			return errors.New("飞书公开结果异常通知未配置")
		}
		return p.notify(ctx, fmt.Sprintf(
			"【网站同步】公开结果待核对\n标题：%s\nFoodTalks 资讯 ID：%d\n任务 #%d 不会自动重试，请先在网站前台/后台确认是否已公开，再调用人工对账。\n原因：%s",
			job.Title, job.FoodTalksNewsID, job.ID, job.Error))
	default:
		return fmt.Errorf("未知发布通知类型 %q", job.NotificationKind)
	}
}

// sendTerminalPublishNotification 优先发不可审核的终态卡，让 approve 回调
// 返回的「请求已受理」进度卡之后一定有明确结果。现有发卡接口不返回
// message_id，暂时无法原位 PATCH；卡片失败时仍降级文本，交由 outbox 持久重试。
func (p *publishPipeline) sendTerminalPublishNotification(ctx context.Context, job publishJob, fallbackText string) error {
	if p.notifyCard != nil {
		if err := p.notifyCard(ctx, buildReviewDoneCard(job)); err == nil {
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
	if p.notifyCard != nil {
		coverImgKey := ""
		if p.coverImgKey != nil && strings.TrimSpace(job.CoverURL) != "" {
			key, err := p.coverImgKey(ctx, job.CoverURL)
			if err != nil {
				log.Printf("publish: WARN 封面转存飞书失败（卡片将不带图）: %v", err)
			} else {
				coverImgKey = key
			}
		}
		if err := p.notifyCard(ctx, buildReviewPendingCard(job, coverImgKey)); err == nil {
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

// recoverPublishNotification 将崩溃遗留 sending 重新置 pending，再调度 drain。
func (p *publishPipeline) recoverPublishNotification(job publishJob) {
	if job.NotificationKind == "" {
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
