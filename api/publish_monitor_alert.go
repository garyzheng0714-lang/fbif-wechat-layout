package main

import (
	"fmt"
	"strings"
)

type publishMonitorAlertRequest struct {
	Source  string   `json:"source"`
	Status  string   `json:"status"`
	Summary string   `json:"summary"`
	Details []string `json:"details,omitempty"`
}

var publishMonitorAlertSources = map[string]string{
	"watchdog":        "独立看门狗",
	"wechat-official": "微信官方数据服务",
	"github-watchdog": "GitHub 外部看门狗",
}

var publishMonitorAlertStatuses = map[string]string{
	"critical":  "异常",
	"recovery":  "恢复",
	"heartbeat": "每日心跳",
	"drill":     "故障演练",
}

func formatPublishMonitorAlert(input publishMonitorAlertRequest) (string, error) {
	input.Source = cleanPublishMonitorAlertField(input.Source, 64)
	input.Status = cleanPublishMonitorAlertField(input.Status, 64)
	input.Summary = cleanPublishMonitorAlertField(input.Summary, 500)
	sourceLabel, sourceOK := publishMonitorAlertSources[input.Source]
	statusLabel, statusOK := publishMonitorAlertStatuses[input.Status]
	if !sourceOK || !statusOK || input.Summary == "" {
		return "", fmt.Errorf("invalid monitoring alert")
	}
	lines := []string{
		"[FBIF 公众号监控 P0] " + statusLabel,
		"来源：" + sourceLabel,
		"摘要：" + input.Summary,
	}
	for index, detail := range input.Details {
		if index >= 20 {
			break
		}
		if cleaned := cleanPublishMonitorAlertField(detail, 300); cleaned != "" {
			lines = append(lines, "- "+cleaned)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func cleanPublishMonitorAlertField(value string, maxRunes int) string {
	return truncateRunes(strings.Join(strings.Fields(value), " "), maxRunes)
}
