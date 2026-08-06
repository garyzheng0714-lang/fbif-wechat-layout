package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 审核群监控播报（2026-08-06 用户裁定：每天早上把链路状态用人话发进审核群）。

func TestPublishMonitoringReportHealthyText(t *testing.T) {
	t.Setenv("FOODTALKS_BASE_URL", "https://api-we.foodtalks.cn")
	status := publishMonitoringStatus{Ready: true, Health: "healthy", Failed: 1}
	text := buildPublishMonitoringReportText(status, 3, time.Date(2026, 8, 7, 0, 30, 0, 0, time.UTC))
	for _, want := range []string{
		"08-07 08:30",       // CST 时间
		"FoodTalks 正式站",     // 发布目标人话
		"链路状态：正常",           // 报平安
		"待审核 3 篇",           // 群成员最关心的数字
		"失败任务 1（近 24 小时 0）", // 历史失败不隐藏
		"「通过并发布」或「驳回」",      // 告诉新成员怎么操作
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("播报应包含 %q，实际：\n%s", want, text)
		}
	}
}

func TestPublishMonitoringReportTranslatesIssues(t *testing.T) {
	status := publishMonitoringStatus{
		Ready:  false,
		Issues: []string{"notification_dead_letters:2", "some_future_code:9"},
	}
	text := buildPublishMonitoringReportText(status, 0, time.Now())
	if !strings.Contains(text, "链路状态：有问题需要处理") {
		t.Fatalf("非 ready 播报必须醒目标注，实际：\n%s", text)
	}
	if !strings.Contains(text, "死信") || !strings.Contains(text, "（2）") {
		t.Fatalf("已收录机器码应翻译成人话并带数量，实际：\n%s", text)
	}
	// 未收录的机器码必须原样保留——不许静默吞掉一类问题。
	if !strings.Contains(text, "some_future_code:9") {
		t.Fatalf("未收录机器码应原样展示，实际：\n%s", text)
	}
}

func TestPublishMonitoringReportSendsToReviewChat(t *testing.T) {
	env := newPublishTestEnv(t)
	if _, _, err := env.pipeline.submit(testPublishSourceURL); err != nil {
		t.Fatalf("submit: %v", err)
	}
	before := len(env.notified)
	text, err := env.pipeline.sendMonitoringReport(context.Background())
	if err != nil {
		t.Fatalf("sendMonitoringReport: %v", err)
	}
	if len(env.notified) != before+1 || env.notified[before] != text {
		t.Fatalf("播报应经审核群通知器发送且与返回文本一致，实际 %d 条", len(env.notified))
	}
	if !strings.Contains(text, "待审核 1 篇") {
		t.Fatalf("播报应统计待审核任务数，实际：\n%s", text)
	}
}

func TestPublishMonitoringReportFailsWithoutNotifier(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.notify = nil
	if _, err := env.pipeline.sendMonitoringReport(context.Background()); err == nil {
		t.Fatalf("审核群通知未配置时播报必须显式失败，不得静默丢失")
	}
}
