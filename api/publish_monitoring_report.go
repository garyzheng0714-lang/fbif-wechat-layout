package main

// publish_monitoring_report.go — 审核群监控播报（2026-08-06 用户裁定：链路上线后，
// 每天早上把监控情况用人话发到审核群，群成员据此开始一天的值班监控）。
//
// 与 POST /api/publish/monitor-alert 的分工：monitor-alert 是外部看门狗的异常
// relay，只发监控负责人私聊（见 publish_pipeline.go 对 monitorNotify 的边界注释，
// 该边界不变）；本文件是面向审核群的定时状态播报，正常也发（报平安），异常时
// 附人话问题清单。触发方式：部署机 systemd timer 每天 08:30 CST 调
// POST /api/publish/monitoring-report（管理员会话或 PUBLISH_ADMIN_SERVICE_TOKEN）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// publishMonitoringIssueLabel 把监控机器码翻译成群成员看得懂的一句话。未收录的
// 机器码原样保留——宁可显示生硬代码，不可静默吞掉一类问题。
func publishMonitoringIssueLabel(issue string) string {
	code, detail, _ := strings.Cut(issue, ":")
	labels := map[string]string{
		"publish_store_invalid":       "任务存储读取失败，所有数字不可信，需要立即处理",
		"deployment_writes_paused":    "服务处于部署暂停态，新文章暂不处理",
		"business_processing":         "有任务长时间停在处理中",
		"review_notification_pending": "有文章已就绪但审核卡还没发出来",
		"processing_stale":            "处理中的任务已卡住过久",
		"queue_stale":                 "排队任务等待过久",
		"notification_errors":         "通知投递出现错误",
		"notification_dead_letters":   "有通知彻底投递失败（死信），需要人工重放",
		"review_cards_missing":        "有待审核任务缺少审核卡",
		"duplicate_source_keys":       "发现重复任务（同一篇文章建了多单）",
		"failed_jobs_recent":          "近 24 小时有任务失败",
		"reconciliation_required":     "有任务需要人工对账（外部调用结果不确定）",
		"reconciliation_stale":        "待人工对账的任务搁置过久",
		"content_conflicts":           "有任务存在内容版本冲突",
	}
	label, ok := labels[code]
	if !ok {
		return issue
	}
	if detail != "" {
		return fmt.Sprintf("%s（%s）", label, detail)
	}
	return label
}

// publishFoodTalksTargetLabel 播报里当前发布目标的人话名称。
func publishFoodTalksTargetLabel() string {
	switch currentFoodTalksTarget() {
	case "production":
		return "FoodTalks 正式站"
	case "test":
		return "FoodTalks 测试站"
	case "unconfigured":
		return "未配置"
	default:
		return "自定义环境"
	}
}

// buildPublishMonitoringReportText 组装播报正文（纯文本消息，不是卡片）。
func buildPublishMonitoringReportText(status publishMonitoringStatus, awaitingReview int, now time.Time) string {
	cst := now.In(time.FixedZone("CST", 8*3600))
	var b strings.Builder
	fmt.Fprintf(&b, "FBIF 文章发布链路播报 · %s\n", cst.Format("01-02 15:04"))
	fmt.Fprintf(&b, "发布目标：%s\n", publishFoodTalksTargetLabel())
	if status.Ready {
		b.WriteString("链路状态：正常，可以放心审核发布\n")
	} else {
		b.WriteString("链路状态：有问题需要处理！\n")
	}
	fmt.Fprintf(&b, "待审核 %d 篇 · 处理中 %d · 排队 %d\n", awaitingReview, status.Processing, status.Queued)
	fmt.Fprintf(&b, "失败任务 %d（近 24 小时 %d） · 待人工对账 %d\n", status.Failed, status.FailedRecent, status.ReconciliationRequired)
	if status.NotificationPending > 0 || status.NotificationDeadLetters > 0 || status.ReviewCardsMissing > 0 {
		fmt.Fprintf(&b, "通知积压 %d · 通知死信 %d · 缺审核卡 %d\n", status.NotificationPending, status.NotificationDeadLetters, status.ReviewCardsMissing)
	}
	if len(status.Issues) > 0 {
		b.WriteString("问题清单：\n")
		for _, issue := range status.Issues {
			fmt.Fprintf(&b, "· %s\n", publishMonitoringIssueLabel(issue))
		}
	}
	if len(status.Warnings) > 0 {
		b.WriteString("提醒（不阻塞）：\n")
		for _, warning := range status.Warnings {
			fmt.Fprintf(&b, "· %s\n", publishMonitoringIssueLabel(warning))
		}
	}
	b.WriteString("审核方式：点击群里对应文章卡片上的「通过并发布」或「驳回」")
	return b.String()
}

// sendMonitoringReport 采集当前监控状态并发送到审核群。返回发送的正文，供端点
// 响应与测试断言使用。审核群通知器未配置时显式失败——播报静默丢失比不配置更糟。
func (p *publishPipeline) sendMonitoringReport(ctx context.Context) (string, error) {
	if p == nil || p.store == nil {
		return "", errors.New("发布流水线未初始化")
	}
	if p.notify == nil {
		return "", errors.New("审核群通知未配置（FEISHU_APP_ID/FEISHU_APP_SECRET/FEISHU_REVIEW_CHAT_ID），无法发送播报")
	}
	now := p.now()
	status := currentPublishMonitoring(p.store, now)
	awaiting := 0
	if jobs, err := p.store.all(); err == nil {
		for _, job := range jobs {
			if job.Stage == publishStageAwaitingReview {
				awaiting++
			}
		}
	}
	text := buildPublishMonitoringReportText(status, awaiting, now)
	if err := p.notify(ctx, text); err != nil {
		return "", fmt.Errorf("播报发送失败：%w", err)
	}
	return text, nil
}
