package main

// feishu_review_card.go — 飞书审核卡片 JSON 构建（施工图 M4，卡片规范以
// feishu-cardkit skill references 为权威，不凭记忆写字段）。
//
// 卡片红线（cardkit skill）：
//   - 一律禁 emoji；
//   - schema 2.0 严格模式：不用 note / action 容器（会报 200861 unsupported tag），
//     横排按钮用 column_set、小灰字用 markdown text_size=notation；
//   - 「通过并发布」必须带 confirm 二次确认（发布是唯一不可逆动作）；
//   - 按钮层级照规范 §9：primary_filled 主操作（带 done_outlined）/ danger 危险
//     （权威示例「驳回」不带 icon）/ default 工具（带 preview_outlined）；
//   - config.update_multi=true：回调响应带新卡时群内所有人同步看到终态。
//
// 状态色联动（规范 §4）：蓝=待审核（tag 黄）、绿=已发布（succeed_filled + tag 绿）、
// 红=已驳回（tag 红）。

import (
	"fmt"
	"time"
)

// feishuCardActionApprove / Reject 回调 value 的 action 取值（发送与回调两侧共用，
// 字面量漂移会让按钮点了没反应）。
const (
	feishuCardActionApprove = "approve"
	feishuCardActionReject  = "reject"
)

const reviewCardHeaderTitle = "网站同步 · 发布审核"

// buildReviewPendingCard 待审核卡：蓝 header + 「待审核」黄 tag + grey-100 信息色块
// （标题/来源公众号/任务号/目标环境/进站时间）+ 按钮行（通过并发布｜驳回｜查看原文）。
func buildReviewPendingCard(job publishJob) map[string]any {
	approveValue := map[string]any{"action": feishuCardActionApprove, "job_id": job.ID}
	rejectValue := map[string]any{"action": feishuCardActionReject, "job_id": job.ID}
	buttons := feishuButtonRow(
		map[string]any{
			"tag":   "button",
			"text":  feishuPlainText("通过并发布"),
			"type":  "primary_filled",
			"size":  "medium",
			"width": "fill",
			"icon":  map[string]any{"tag": "standard_icon", "token": "done_outlined"},
			"confirm": map[string]any{
				"title": feishuPlainText("通过并发布？"),
				"text":  feishuPlainText("发布后文章公开可见，且发布动作不可逆。"),
			},
			"behaviors": []any{map[string]any{"type": "callback", "value": approveValue}},
		},
		map[string]any{
			"tag":   "button",
			"text":  feishuPlainText("驳回"),
			"type":  "danger",
			"size":  "medium",
			"width": "fill",
			"confirm": map[string]any{
				"title": feishuPlainText("驳回该文章？"),
				"text":  feishuPlainText("驳回后任务终止，文章保留在 FoodTalks 草稿态，不对外可见。"),
			},
			"behaviors": []any{map[string]any{"type": "callback", "value": rejectValue}},
		},
		map[string]any{
			"tag":   "button",
			"text":  feishuPlainText("查看原文"),
			"type":  "default",
			"size":  "medium",
			"width": "fill",
			"icon":  map[string]any{"tag": "standard_icon", "token": "preview_outlined"},
			"behaviors": []any{map[string]any{
				"type": "open_url", "default_url": job.SourceURL,
				"pc_url": "", "ios_url": "", "android_url": "",
			}},
		},
	)
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true},
		"header": map[string]any{
			"template": "blue",
			"icon":     map[string]any{"tag": "standard_icon", "token": "readinfo_outlined"},
			"title":    feishuPlainText(reviewCardHeaderTitle),
			"subtitle": feishuPlainText("草稿已进站（外界不可见），等待人工裁决"),
			"text_tag_list": []any{map[string]any{
				"tag": "text_tag", "text": feishuPlainText("待审核"), "color": "yellow",
			}},
		},
		"body": map[string]any{
			"direction":        "vertical",
			"padding":          "12px 12px 12px 12px",
			"vertical_spacing": "8px",
			"horizontal_align": "left",
			"vertical_align":   "top",
			"elements": []any{
				reviewCardInfoBlock(job),
				buttons,
			},
		},
	}
}

// buildReviewDoneCard 终态卡：按 job 当前终态渲染。approve → 绿「已发布」+ 查看文章；
// reject → 红「已驳回」。底部一行操作留痕（person capsule + 动作 + 时间，规范 §6.4）。
func buildReviewDoneCard(job publishJob) map[string]any {
	published := job.Stage == publishStagePublished
	header := map[string]any{
		"title": feishuPlainText(reviewCardHeaderTitle),
	}
	var actionLabel string
	elements := []any{reviewCardInfoBlock(job)}
	if published {
		header["template"] = "green"
		header["icon"] = map[string]any{"tag": "standard_icon", "token": "succeed_filled"}
		header["subtitle"] = feishuPlainText("文章已在 FoodTalks 公开可见")
		header["text_tag_list"] = []any{map[string]any{
			"tag": "text_tag", "text": feishuPlainText("已发布"), "color": "green",
		}}
		actionLabel = "通过并发布了本文"
		if job.ArticleURL != "" {
			elements = append(elements, feishuButtonRow(map[string]any{
				"tag":   "button",
				"text":  feishuPlainText("查看文章"),
				"type":  "default",
				"size":  "medium",
				"width": "fill",
				"icon":  map[string]any{"tag": "standard_icon", "token": "preview_outlined"},
				"behaviors": []any{map[string]any{
					"type": "open_url", "default_url": job.ArticleURL,
					"pc_url": "", "ios_url": "", "android_url": "",
				}},
			}))
		}
	} else {
		header["template"] = "red"
		header["subtitle"] = feishuPlainText("任务终止，文章保留在 FoodTalks 草稿态")
		header["text_tag_list"] = []any{map[string]any{
			"tag": "text_tag", "text": feishuPlainText("已驳回"), "color": "red",
		}}
		actionLabel = "驳回了本文"
	}
	elements = append(elements, map[string]any{
		"tag":       "markdown",
		"text_size": "notation",
		"content":   reviewCardAuditTrail(job, actionLabel),
		"margin":    "4px 0px 0px 0px",
	})
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true},
		"header": header,
		"body": map[string]any{
			"direction":        "vertical",
			"padding":          "12px 12px 12px 12px",
			"vertical_spacing": "8px",
			"horizontal_align": "left",
			"vertical_align":   "top",
			"elements":         elements,
		},
	}
}

// reviewCardInfoBlock grey-100 信息色块：标题/来源公众号/任务号/目标环境/进站时间。
func reviewCardInfoBlock(job publishJob) map[string]any {
	sourceName := job.SourceName
	if sourceName == "" {
		sourceName = "未知"
	}
	content := fmt.Sprintf(
		"**%s**\n<font color='grey'>来源公众号</font> %s\n<font color='grey'>任务号</font> #%d\n<font color='grey'>目标环境</font> %s\n<font color='grey'>进站时间</font> %s",
		job.Title, sourceName, job.ID, siteFrontURL(), formatReviewCardTime(job.UpdatedAt))
	return map[string]any{
		"tag":              "column_set",
		"flex_mode":        "stretch",
		"background_style": "grey-100",
		"columns": []any{map[string]any{
			"tag":              "column",
			"width":            "weighted",
			"weight":           1,
			"direction":        "vertical",
			"padding":          "12px 12px 12px 12px",
			"vertical_spacing": "4px",
			"elements": []any{map[string]any{
				"tag":     "markdown",
				"content": content,
			}},
		}},
	}
}

// reviewCardAuditTrail 底部操作留痕：卡片回调有 operator.open_id 时用 person capsule
// 嵌人；HTTP 审核（无 open_id）退化为「经管理后台」纯文本，谁都不冒充。
func reviewCardAuditTrail(job publishJob, actionLabel string) string {
	when := formatReviewCardTime(job.ReviewedAt)
	if job.ReviewedBy != "" {
		return fmt.Sprintf(
			"<person id=\"%s\" show_avatar=\"true\" show_name=\"true\" style=\"capsule\"></person> %s <font color='grey'>%s</font>",
			job.ReviewedBy, actionLabel, when)
	}
	return fmt.Sprintf("经管理后台%s <font color='grey'>%s</font>", actionLabel, when)
}

// feishuButtonRow schema 2.0 横排按钮：column_set 等宽列（action 容器在 2.0 会报
// 200861，规范坑 1）。
func feishuButtonRow(buttons ...map[string]any) map[string]any {
	columns := make([]any, 0, len(buttons))
	for _, b := range buttons {
		columns = append(columns, map[string]any{
			"tag":            "column",
			"width":          "weighted",
			"weight":         1,
			"direction":      "vertical",
			"vertical_align": "center",
			"elements":       []any{b},
		})
	}
	return map[string]any{
		"tag":                "column_set",
		"flex_mode":          "stretch",
		"horizontal_spacing": "8px",
		"columns":            columns,
	}
}

func feishuPlainText(s string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": s}
}

// formatReviewCardTime 存储时间（RFC3339 UTC）→ 北京时间人话展示；解析失败原样返回
// （宁可展示原始串也不空着——卡片是给人看的审计信息）。
func formatReviewCardTime(rfc3339 string) string {
	if rfc3339 == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04")
}
