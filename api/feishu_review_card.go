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
	"net/url"
	"strings"
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
// （标题/来源公众号/作者/标签/摘要/原文/任务号/目标环境/进站时间）+ AI 优化说明段
// + 按钮行（通过并发布｜驳回｜预览效果）。第三键 2026-07-10「预览链接」裁定后从
// 「查看原文」换成「预览效果」（审核不能盲审），原文链接降为信息色块一行。
// coverImgKey 是封面图经飞书图片素材接口转存后的 img_key（卡片 img 元素只认
// img_key）；上传失败或无封面时传空串 → 卡片不带图，其余不变（卡片永不阻塞流水线）。
func buildReviewPendingCard(job publishJob, coverImgKey string) map[string]any {
	approveValue := map[string]any{"action": feishuCardActionApprove, "job_id": job.ID, "review_version": publishReviewCardVersion(job)}
	rejectValue := map[string]any{"action": feishuCardActionReject, "job_id": job.ID, "review_version": publishReviewCardVersion(job)}
	approveButton := map[string]any{
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
	}
	rejectButton := map[string]any{
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
	}
	previewButton := map[string]any{
		"tag":   "button",
		"text":  feishuPlainText("预览效果"),
		"type":  "default",
		"size":  "medium",
		"width": "fill",
		"icon":  map[string]any{"tag": "standard_icon", "token": "preview_outlined"},
		"behaviors": []any{map[string]any{
			"type": "open_url", "default_url": publishPreviewURL(job.ID),
			"pc_url": "", "ios_url": "", "android_url": "",
		}},
	}
	buttons := feishuButtonRow(approveButton, rejectButton, previewButton)
	if !publishRiskAllowsManualApprove(job.RiskLevel) {
		// block/缺失/未知级别不能只靠回调端挡：卡片本身不呈现 approve，避免错误暗示。
		buttons = feishuButtonRow(rejectButton, previewButton)
	}
	elements := []any{}
	if coverImgKey != "" {
		elements = append(elements, map[string]any{
			"tag":           "img",
			"img_key":       coverImgKey,
			"alt":           feishuPlainText("封面图（AI 预选，可在管理后台更换）"),
			"corner_radius": "8px",
		})
	}
	elements = append(elements, reviewCardInfoBlock(job))
	if notes := reviewCardAINotesBlock(job); notes != nil {
		elements = append(elements, notes)
	}
	if risk := reviewCardRiskBlock(job); risk != nil {
		elements = append(elements, risk)
	}
	elements = append(elements, buttons)
	headerTemplate := "blue"
	headerSubtitle := "草稿已进站（外界不可见），等待人工裁决"
	headerTagColor := "yellow"
	headerTagText := "待审核"
	if job.RiskLevel == publishRiskBlock {
		headerTemplate = "red"
		headerSubtitle = "发现阻断级风险，不可一键发布"
		headerTagColor = "red"
		headerTagText = "风险阻断"
	} else if !publishRiskLevelKnown(job.RiskLevel) {
		headerTemplate = "orange"
		headerSubtitle = "风险检查尚未完成，不可一键发布"
		headerTagColor = "orange"
		headerTagText = "风险未完成"
	}
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true},
		"header": map[string]any{
			"template": headerTemplate,
			"icon":     map[string]any{"tag": "standard_icon", "token": "readinfo_outlined"},
			"title":    feishuPlainText(reviewCardHeaderTitle),
			"subtitle": feishuPlainText(headerSubtitle),
			"text_tag_list": []any{map[string]any{
				"tag": "text_tag", "text": feishuPlainText(headerTagText), "color": headerTagColor,
			}},
		},
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

// buildReviewPublishingCard approve 已被 CAS 接受后立即返回的无按钮进度卡。FoodTalks
// PUT 在后台执行，回调不等待外部网络；失败/成功都可由任务详情与终态通知追踪。
func buildReviewPublishingCard(job publishJob) map[string]any {
	elements := []any{reviewCardInfoBlock(job), map[string]any{
		"tag":       "markdown",
		"text_size": "notation",
		"content":   reviewCardAuditTrail(job, "已通过本文，后台正在发布"),
	}}
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true},
		"header": map[string]any{
			"template": "blue",
			"icon":     map[string]any{"tag": "standard_icon", "token": "readinfo_outlined"},
			"title":    feishuPlainText(reviewCardHeaderTitle),
			"subtitle": feishuPlainText("请求已受理，最终结果以随后终态卡为准"),
			"text_tag_list": []any{map[string]any{
				"tag": "text_tag", "text": feishuPlainText("发布中"), "color": "blue",
			}},
		},
		"body": map[string]any{
			"direction": "vertical", "padding": "12px 12px 12px 12px",
			"vertical_spacing": "8px", "elements": elements,
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

// reviewCardInfoBlock grey-100 信息色块：标题/来源公众号/作者/标签/摘要/原文链接/
// 任务号/目标环境/进站时间。作者/标签/摘要是 M5 AI 预填参数（或规则兜底值）；
// 原文链接自 2026-07-10「预览链接」裁定起从按钮降为此处一行。
func reviewCardInfoBlock(job publishJob) map[string]any {
	sourceName := job.SourceName
	if sourceName == "" {
		sourceName = "未知"
	}
	sourceURL := reviewCardSafeURL(job.SourceURL)
	if sourceURL == "" {
		sourceURL = "https://mp.weixin.qq.com/"
	}
	content := fmt.Sprintf(
		"**%s**\n<font color='grey'>来源公众号</font> %s\n<font color='grey'>作者</font> %s\n<font color='grey'>标签</font> %s\n<font color='grey'>摘要</font> %s\n<font color='grey'>原文</font> [打开公众号原文](%s)\n<font color='grey'>管理后台</font> [去后台编辑这篇文章](%s)\n<font color='grey'>任务号</font> #%d\n<font color='grey'>目标环境</font> %s\n<font color='grey'>进站时间</font> %s",
		reviewCardSafeText(job.Title, 200), reviewCardSafeText(sourceName, 100),
		reviewCardSafeOrDash(job.Author, 100),
		reviewCardSafeOrDash(strings.Join(job.Tags, "、"), 200),
		reviewCardSafeOrDash(job.Summary, 500),
		sourceURL, siteAdminArticleURL(job.FoodTalksNewsID), job.ID, siteFrontURL(), formatReviewCardTime(job.UpdatedAt))
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

// reviewCardSafeURL 专用于 Feishu markdown 的链接目标。url.Parse/String 先拒绝
// 非 HTTP(S) 与控制字符，再对 markdown 可闭合 `](...)` 的字符做百分号
// 编码；否则合法 mp.weixin.qq.com path 也可用 `)` 提前闭链并注入后续标记。
func reviewCardSafeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	safe := u.String()
	return strings.NewReplacer(
		"\\", "%5C", "(", "%28", ")", "%29", "[", "%5B", "]", "%5D",
		"<", "%3C", ">", "%3E", "\"", "%22", "'", "%27", " ", "%20",
	).Replace(safe)
}

// reviewCardAINotesBlock AI 优化说明段（M5）：grey-100 色块 + 小灰字标题，无 emoji。
// notes 为空（老任务没跑过 enriching）时不渲染该段，返回 nil。
func reviewCardAINotesBlock(job publishJob) map[string]any {
	notes := strings.TrimSpace(job.AINotes)
	if notes == "" {
		return nil
	}
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
				"content": fmt.Sprintf("<font color='grey'>AI 优化说明</font>\n%s", reviewCardSafeText(notes, 600)),
			}},
		}},
	}
}

// reviewCardRiskBlock 展示持久化的风险结论与证据。模型输出按纯文本收敛后再拼
// markdown，避免正文片段注入标签/链接；卡片最多展示 6 条，完整结果仍在任务详情。
func reviewCardRiskBlock(job publishJob) map[string]any {
	if strings.TrimSpace(job.RiskLevel) == "" {
		return nil
	}
	var content strings.Builder
	content.WriteString("<font color='grey'>风险审核</font>\n**")
	content.WriteString(reviewCardSafeText(publishRiskLevelLabel(job.RiskLevel), 40))
	content.WriteString("**")
	if job.RiskLevel == publishRiskBlock {
		content.WriteString(" · 禁止一键发布")
	}
	limit := len(job.RiskItems)
	if limit > 6 {
		limit = 6
	}
	for i := 0; i < limit; i++ {
		item := job.RiskItems[i]
		content.WriteString("\n")
		content.WriteString(fmt.Sprintf("%d. %s：%s", i+1,
			reviewCardSafeText(publishRiskCategoryLabel(item.Category), 40),
			reviewCardSafeText(item.Evidence, 140)))
		content.WriteString("\n   原因：")
		content.WriteString(reviewCardSafeText(item.Reason, 140))
	}
	if len(job.RiskItems) > limit {
		content.WriteString(fmt.Sprintf("\n另有 %d 条，请在任务详情中查看。", len(job.RiskItems)-limit))
	}
	if strings.TrimSpace(job.RiskNotes) != "" {
		content.WriteString("\n说明：")
		content.WriteString(reviewCardSafeText(job.RiskNotes, 180))
	}
	return map[string]any{
		"tag":              "column_set",
		"flex_mode":        "stretch",
		"background_style": "grey-100",
		"columns": []any{map[string]any{
			"tag":       "column",
			"width":     "weighted",
			"weight":    1,
			"direction": "vertical",
			"padding":   "12px 12px 12px 12px",
			"elements": []any{map[string]any{
				"tag": "markdown", "content": content.String(),
			}},
		}},
	}
}

// reviewCardSafeText 把文章/模型等外部文本收敛成 Feishu markdown 内的纯文本。
// 固定链接与格式标记由调用方代码拼接，外部值不能注入 at、链接、标题或强调语法。
func reviewCardSafeText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.NewReplacer(
		"https://", "https：／／", "http://", "http：／／",
		"\\", "＼", "<", "‹", ">", "›", "[", "［", "]", "］",
		"(", "（", ")", "）", "*", "＊", "_", "＿", "~", "～",
		"`", "'", "#", "＃", "!", "！", "|", "｜", "{", "｛", "}", "｝",
	).Replace(value)
	return truncateRunes(value, limit)
}

// reviewCardSafeOrDash 空值统一展示为「-」（卡片信息行不留空尾巴）。
func reviewCardSafeOrDash(v string, limit int) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return reviewCardSafeText(v, limit)
}

// reviewCardAuditTrail 底部操作留痕：卡片回调有 operator.open_id 时用 person capsule
// 嵌人；HTTP 审核（无 open_id）退化为「经管理后台」纯文本，谁都不冒充。
func reviewCardAuditTrail(job publishJob, actionLabel string) string {
	when := formatReviewCardTime(job.ReviewedAt)
	if feishuOpenIDSafe(job.ReviewedBy) {
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
