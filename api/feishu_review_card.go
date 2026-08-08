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
// 红=已驳回/失败、橙=公开结果待核对。

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// feishuCardActionApprove / Reject 回调 value 的 action 取值（发送与回调两侧共用，
// 字面量漂移会让按钮点了没反应）。
const (
	feishuCardActionApprove   = "approve"
	feishuCardActionReject    = "reject"
	feishuCardActionUnpublish = "unpublish"
	feishuCardActionRepublish = "republish"
	// 阻断级风险卡上的强制发布（2026-08-07 用户裁定：阻断只当警示不当锁）。
	feishuCardActionForceApprove = "force_approve"
)

const reviewCardHeaderTitle = "网站发布审核"

// reviewCardTagsWithForced 在 header 标签末尾追加「强制发布」红标（仅 ReviewForced
// 任务）：群里长期留存的卡必须一眼看出这篇是无视阻断结论发出去的。
func reviewCardTagsWithForced(job publishJob, tags ...any) []any {
	if !job.ReviewForced {
		return tags
	}
	return append(tags, map[string]any{
		"tag": "text_tag", "text": feishuPlainText("强制发布"), "color": "red",
	})
}

// buildReviewPendingCard 待审核卡：蓝 header + 「待审核」黄 tag + grey-100 信息色块
// （封面/标题/标签/摘要/预览）+ 基础信息 + AI 优化说明段 + 审核按钮行。
// 预览键使用稳定的本站签名入口，每次点击现铸 FoodTalks 短期 token 后跳到官方预览页；
// 原文链接降为基础信息一行。
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
			"text":  feishuPlainText("发布公开动作不可逆；如需纠正内容，可在配置的窗口内下线，之后也能重新发布。"),
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
	buttons := feishuButtonRow(approveButton, rejectButton)
	if !publishRiskAllowsManualApprove(job.RiskLevel) {
		// 缺失/未知级别（检查没跑完）仍只呈现驳回；阻断级给「强制发布」显式出口
		//（2026-08-07 用户裁定），确认文案明说是在无视阻断结论。
		buttons = feishuButtonRow(rejectButton)
		if job.RiskLevel == publishRiskBlock {
			forceValue := map[string]any{"action": feishuCardActionForceApprove, "job_id": job.ID, "review_version": publishReviewCardVersion(job)}
			forceButton := map[string]any{
				"tag":   "button",
				"text":  feishuPlainText("强制发布"),
				"type":  "danger",
				"size":  "medium",
				"width": "fill",
				"confirm": map[string]any{
					"title": feishuPlainText("无视风险阻断，强制发布？"),
					"text":  feishuPlainText("风险检查判定为阻断级。强制发布将忽略该结论，文章公开可见且不可逆，操作以你的名义留痕。"),
				},
				"behaviors": []any{map[string]any{"type": "callback", "value": forceValue}},
			}
			buttons = feishuButtonRow(forceButton, rejectButton)
		}
	}
	elements := []any{reviewCardHeroBlock(job, coverImgKey), reviewCardInfoBlock(job, true)}
	if headline := reviewCardRiskHeadlineBlock(job); headline != nil {
		elements = append(elements, headline)
	}
	if details := reviewCardDetailsPanel(job); details != nil {
		elements = append(elements, details)
	}
	elements = append(elements, buttons)
	headerTemplate := "blue"
	headerTagColor := "yellow"
	headerTagText := "待审核"
	if job.RiskLevel == publishRiskBlock {
		headerTemplate = "red"
		headerTagColor = "red"
		headerTagText = "风险阻断"
	} else if !publishRiskLevelKnown(job.RiskLevel) {
		headerTemplate = "orange"
		headerTagColor = "orange"
		headerTagText = "风险未完成"
	}
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true, "width_mode": "default"},
		"header": map[string]any{
			"template": headerTemplate,
			"icon":     map[string]any{"tag": "standard_icon", "token": "readinfo_outlined"},
			"title":    feishuPlainText(reviewCardHeaderTitle),
			"subtitle": feishuPlainText(fmt.Sprintf("%s · 任务 #%d", reviewCardEnvironmentLabel(), job.ID)),
			"text_tag_list": []any{map[string]any{
				"tag": "text_tag", "text": feishuPlainText(headerTagText), "color": headerTagColor,
			}},
		},
		"body": map[string]any{
			"direction":        "vertical",
			"padding":          "12px 12px 16px 12px",
			"vertical_spacing": "12px",
			"horizontal_align": "left",
			"vertical_align":   "top",
			"elements":         elements,
		},
	}
}

// buildReviewPublishingCard approve 已被 CAS 接受后立即返回的无按钮进度卡。FoodTalks
// PUT 在后台执行，回调不等待外部网络；失败/成功都可由任务详情与终态通知追踪。
func buildReviewPublishingCard(job publishJob) map[string]any {
	elements := []any{reviewCardInfoBlock(job, false), map[string]any{
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
			"text_tag_list": reviewCardTagsWithForced(job, map[string]any{
				"tag": "text_tag", "text": feishuPlainText("发布中"), "color": "blue",
			}),
		},
		"body": map[string]any{
			"direction": "vertical", "padding": "12px 12px 12px 12px",
			"vertical_spacing": "8px", "elements": elements,
		},
	}
}

// buildReviewLifecycleProgressCard 保留发布、下线、重新发布的“已受理”中间态，
// 但始终由同一张卡片更新到最终状态。
func buildReviewLifecycleProgressCard(job publishJob, action string) map[string]any {
	label := "处理中"
	content := "后台正在处理这篇文章，最终结果会更新到同一张卡片。"
	switch action {
	case feishuCardActionUnpublish:
		label = "下线中"
		content = "已受理下线请求，正在等待 FoodTalks 返回最终结果。"
	case feishuCardActionRepublish:
		label = "重新发布中"
		content = "已受理重新发布请求，正在等待 FoodTalks 返回最终结果。"
	}
	elements := []any{reviewCardInfoBlock(job, false), map[string]any{
		"tag": "markdown", "text_size": "notation",
		"content": content + "\n\n" + reviewCardAuditTrail(job, content),
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
				"tag": "text_tag", "text": feishuPlainText(label), "color": "blue",
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
//
// 终态卡与待审核卡共用 reviewCardHeroBlock：审核前能看到的封面、标题、标签、摘要、
// 预览与原文链接，审核后必须一个不少。这张卡在群里会长期留存，是事后回看「发了什么」
// 的唯一入口；只留来源/作者/时间和任务号，等于把一篇文章压成一个查不动的编号。
// 封面用待审核时存下的 img_key（卡片回调要同步返回新卡，不能在这里现传图片）。
func buildReviewDoneCard(job publishJob) map[string]any {
	published := job.Stage == publishStagePublished
	offline := job.Stage == publishStageOffline
	header := map[string]any{
		"title": feishuPlainText(reviewCardHeaderTitle),
	}
	var actionLabel string
	elements := []any{
		reviewCardHeroBlock(job, strings.TrimSpace(job.ReviewCoverImgKey)),
		reviewCardInfoBlock(job, false),
	}
	if published {
		header["template"] = "green"
		header["icon"] = map[string]any{"tag": "standard_icon", "token": "succeed_filled"}
		header["subtitle"] = feishuPlainText("文章已在 FoodTalks 公开可见 · " + publishUnpublishWindowLabel(job, time.Now().UTC()))
		header["text_tag_list"] = reviewCardTagsWithForced(job, map[string]any{
			"tag": "text_tag", "text": feishuPlainText("已发布"), "color": "green",
		})
		actionLabel = "通过并发布了本文"
		if job.ReviewForced {
			actionLabel = "强制发布了本文（当时风险结论为阻断级）"
		}
		if job.PublicationAction == publishPublicationActionRepublish {
			actionLabel = "重新发布了本文"
		}
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
		if publishUnpublishWindowOpen(job, time.Now().UTC()) {
			unpublishValue := map[string]any{
				"action": feishuCardActionUnpublish, "job_id": job.ID,
				"lifecycle_version": publishLifecycleCardVersion(job),
			}
			elements = append(elements, feishuButtonRow(map[string]any{
				"tag": "button", "text": feishuPlainText("下线文章"), "type": "danger",
				"size": "medium", "width": "fill",
				"confirm": map[string]any{
					"title": feishuPlainText("下线这篇文章？"),
					"text":  feishuPlainText("文章会暂时从 FoodTalks 前台下线，仍可在窗口内重新发布。"),
				},
				"behaviors": []any{map[string]any{"type": "callback", "value": unpublishValue}},
			}))
		}
	} else if offline {
		header["template"] = "orange"
		header["icon"] = map[string]any{"tag": "standard_icon", "token": "readinfo_outlined"}
		header["subtitle"] = feishuPlainText("文章已下线，可重新发布")
		header["text_tag_list"] = []any{map[string]any{
			"tag": "text_tag", "text": feishuPlainText("已下线"), "color": "orange",
		}}
		actionLabel = "下线了本文"
		republishValue := map[string]any{
			"action": feishuCardActionRepublish, "job_id": job.ID,
			"lifecycle_version": publishLifecycleCardVersion(job),
		}
		elements = append(elements, feishuButtonRow(map[string]any{
			"tag": "button", "text": feishuPlainText("重新发布"), "type": "primary_filled",
			"size": "medium", "width": "fill",
			"confirm": map[string]any{
				"title": feishuPlainText("重新发布这篇文章？"),
				"text":  feishuPlainText("将使用原审核通过的版本恢复为公开状态。"),
			},
			"behaviors": []any{map[string]any{"type": "callback", "value": republishValue}},
		}))
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

// buildReviewIssueCard 是发布窗口的异常终态：仍更新原审核卡，不另起一张通知卡。
// uncertain=true 表示外部 PUT 的结果无法确认，不能误报为失败，也不能自动重试。
func buildReviewIssueCard(job publishJob, uncertain bool) map[string]any {
	header := map[string]any{
		"title":    feishuPlainText(reviewCardHeaderTitle),
		"icon":     map[string]any{"tag": "standard_icon", "token": "readinfo_outlined"},
		"template": "red",
	}
	status := "发布失败"
	subtitle := "公开请求未完成，文章仍保留在 FoodTalks 草稿态"
	actionLabel := "发布失败"
	color := "red"
	if job.PublicationAction == publishPublicationActionUnpublish {
		status = "下线失败"
		subtitle = "下线请求未完成，文章仍保持公开状态"
		actionLabel = "下线失败"
	} else if job.PublicationAction == publishPublicationActionRepublish {
		status = "重新发布失败"
		subtitle = "重新发布请求未完成，文章仍保持下线状态"
		actionLabel = "重新发布失败"
	}
	if uncertain {
		header["template"] = "orange"
		status = "待人工核对"
		subtitle = "操作结果暂时无法确认，请先核对网站状态"
		actionLabel = "操作结果待核对"
		color = "orange"
	}
	header["subtitle"] = feishuPlainText(subtitle)
	header["text_tag_list"] = []any{map[string]any{
		"tag": "text_tag", "text": feishuPlainText(status), "color": color,
	}}
	// 异常卡同样长期留在群里，且往往需要有人接手排查——不能只留一个任务号，
	// 让人无从判断是哪篇文章出的问题（与终态卡同口径）。
	elements := []any{
		reviewCardHeroBlock(job, strings.TrimSpace(job.ReviewCoverImgKey)),
		reviewCardInfoBlock(job, false),
		map[string]any{
			"tag":       "markdown",
			"text_size": "notation",
			"content":   "<font color='grey'>处理说明</font>\n" + reviewCardSafeOrDash(job.Error, 600),
		}}
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

// reviewCardHeroBlock 把封面、标题、标签、摘要和预览键收进同一个横向媒体容器：
// 封面素材已在上传前生成方形画布，在紧凑左栏内等比显示；文案列占据剩余宽度，
// 原图不被裁切且竖图不再主导整卡高度。无封面时自动退化为单列，不留空白占位。
func reviewCardHeroBlock(job publishJob, coverImgKey string) map[string]any {
	columns := []any{}
	if coverImgKey != "" {
		columns = append(columns, map[string]any{
			"tag":            "column",
			"width":          "104px",
			"direction":      "vertical",
			"vertical_align": "top",
			"elements": []any{map[string]any{
				"tag":           "img",
				"img_key":       coverImgKey,
				"alt":           feishuPlainText("文章封面图"),
				"scale_type":    "fit_horizontal",
				"corner_radius": "8px",
			}},
		})
	}
	copyElements := []any{map[string]any{
		"tag": "markdown", "content": fmt.Sprintf("**%s**", reviewCardSafeText(job.Title, 200)),
	}}
	if tags := reviewCardTagMarkup(job.Tags); tags != "" {
		copyElements = append(copyElements, map[string]any{"tag": "markdown", "content": tags})
	}
	copyElements = append(copyElements, map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag": "plain_text", "content": reviewCardSafeOrDash(job.Summary, 500), "lines": 3,
		},
	})
	copyElements = append(copyElements, map[string]any{
		"tag": "markdown", "content": reviewCardArticleLinks(job), "text_size": "notation",
	})
	columns = append(columns, map[string]any{
		"tag":              "column",
		"width":            "weighted",
		"weight":           1,
		"direction":        "vertical",
		"vertical_align":   "top",
		"vertical_spacing": "6px",
		"elements":         copyElements,
	})
	return map[string]any{
		"tag":                "column_set",
		"flex_mode":          "none",
		"horizontal_spacing": "12px",
		"columns":            columns,
	}
}

func reviewCardArticleLinks(job publishJob) string {
	links := []string{fmt.Sprintf("[预览效果](%s)", reviewCardSafeURL(publishPreviewURL(job.ID)))}
	if sourceURL := reviewCardSafeURL(job.SourceURL); sourceURL != "" {
		links = append(links, fmt.Sprintf("[公众号原文](%s)", sourceURL))
	}
	return strings.Join(links, "　·　")
}

// reviewCardTagMarkup 把 AI/规则生成的文章标签渲染成飞书原生 text_tag，而不是逗号文本。
func reviewCardTagMarkup(tags []string) string {
	items := make([]string, 0, len(tags))
	for _, tag := range tags {
		if safe := reviewCardSafeText(tag, 40); safe != "" {
			items = append(items, fmt.Sprintf("<text_tag color='blue'>%s</text_tag>", safe))
		}
	}
	return strings.Join(items, " ")
}

// reviewCardInfoBlock 把来源、作者、进站时间、目标环境和风险结论压成两行轻量上下文，
// 不再用第二层灰色卡片与主内容争夺视觉焦点。
//
// compact=true 用于待审核卡：那里 header 副标题已经写了「环境 · 任务 #N」，首屏也
// 已有独立的检查结论块，再重复一遍环境、任务号和风险标签就是同义信息刷三遍
// （真机截图实证：副标题与信息行第二行一字不差）。其余卡片的 header 副标题是状态
// 文案、不带任务号，仍需这一行兜住身份与当时的风险结论。
func reviewCardInfoBlock(job publishJob, compact bool) map[string]any {
	sourceName := job.SourceName
	if sourceName == "" {
		sourceName = "未知"
	}
	content := fmt.Sprintf(
		"<font color='grey'>来源</font> %s　·　<font color='grey'>作者</font> %s　·　<font color='grey'>进站</font> %s",
		reviewCardSafeText(sourceName, 100),
		reviewCardSafeOrDash(job.Author, 100),
		formatReviewCardTime(job.UpdatedAt))
	if !compact {
		content += fmt.Sprintf("\n<font color='grey'>%s　·　任务 #%d</font>　%s",
			reviewCardEnvironmentLabel(), job.ID, reviewCardRiskTag(job.RiskLevel))
	}
	return map[string]any{
		"tag": "markdown", "content": content, "text_size": "notation",
	}
}

func reviewCardEnvironmentLabel() string {
	u, err := url.Parse(siteFrontURL())
	if err != nil {
		return "FoodTalks"
	}
	host := strings.ToLower(u.Hostname())
	if host == "foodtalks.cn" || host == "www.foodtalks.cn" {
		return "FoodTalks 正式环境"
	}
	if host == "localhost" || host == "127.0.0.1" || strings.Contains(host, "test") {
		return "FoodTalks 测试环境"
	}
	return "FoodTalks"
}

func reviewCardRiskTag(level string) string {
	switch level {
	case publishRiskPass:
		return "<text_tag color='neutral'>检查通过</text_tag>"
	case publishRiskWarning:
		return "<text_tag color='yellow'>风险提醒</text_tag>"
	case publishRiskBlock:
		return "<text_tag color='red'>风险阻断</text_tag>"
	default:
		return "<text_tag color='orange'>检查未完成</text_tag>"
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

// reviewCardRiskHeadlineBlock 审核卡首屏唯一的检查结论（2026-08-08 用户裁定）。
//
// 首屏只放**已证实的实质性问题**：一句话结论（≤100 字）+ 至多 3 条逐条指认。
// 转载授权、证明不了的存疑项、排版说明这些每篇都长一个样的内容全部沉到折叠的
// 完整检查报告里——审核人扫一眼就要下判断，重复内容占满首屏等于什么都没说。
// 没有已证实问题时这里只剩一行小字，明确区分「干净」与「有几条待核」，
// 绝不因为模型没标 certainty 就谎称检查通过。
func reviewCardRiskHeadlineBlock(job publishJob) map[string]any {
	if strings.TrimSpace(job.RiskLevel) == "" && strings.TrimSpace(job.RiskHeadline) == "" {
		return nil
	}
	confirmed, _ := splitPublishRiskItems(job.RiskItems)
	headline := reviewCardSafeText(publishRiskHeadlineOrFallback(job), publishRiskHeadlineMaxRunes)
	if len(confirmed) > 0 {
		return reviewCardConfirmedRiskBlock(confirmed, headline)
	}
	// 无已证实问题：一行小字，不与标题摘要争视觉焦点。
	//
	// 正常态**不挂状态标签**：标签写「无实质性错误」、后面紧跟「未发现可证实的
	// 实质性错误」，是同一句话说两遍（2026-08-08 真机截图）；而且它和标题下方的
	// 蓝色文章标签形状一模一样、语义完全不同，扫一眼容易混。标签只留给阻断和
	// 检查未完成这两种真正的异常——那时它才携带信息。
	var content strings.Builder
	switch {
	case !publishRiskLevelKnown(job.RiskLevel):
		content.WriteString("<text_tag color='orange'>检查未完成</text_tag> ")
	case job.RiskLevel == publishRiskBlock:
		// 阻断级即使没有已证实条目也绝不能显示成绿色「无实质性错误」——
		// header 是红的，首屏说没事，等于教审核人无视阻断结论。
		content.WriteString("<text_tag color='red'>风险阻断</text_tag> ")
	default:
		content.WriteString("<font color='grey'>检查结论　</font>")
	}
	content.WriteString("<font color='grey'>")
	content.WriteString(headline)
	content.WriteString("</font>")
	return map[string]any{
		"tag": "markdown", "content": content.String(), "text_size": "notation",
		// 与上方来源行贴成一组「这篇文章的背景信息」，同折叠条、按钮拉开距离：
		// 四行等距平铺会让卡片下半身散掉。
		"margin": "-6px 0px 0px 0px",
	}
}

// reviewCardConfirmedRiskBlock 已证实问题的红色块——审核人唯一必须停下来看的东西。
//
// 四条排版纪律（2026-08-08 真机截图 + 用户反馈后定的）：
//   - **层级靠元素间距，不靠换行符**：飞书 markdown 吃掉单个 \n，同一段里的
//     「原句」会直接粘在上一句尾巴上（`…是平昌冬奥会原句 2018年的…`）。每一行
//     都必须是独立元素，由 column 的 vertical_spacing 分隔。
//   - **状态标签不进正文首**：块顶只放一个灰色小标题，与折叠区的「检查结论」
//     「排版说明」同一套语法；状态色由 header 的 template 与 text_tag 承担，
//     红底本身已经在喊「这里有问题」，正文再插一个红标签就是第三遍编码同一件事
//     （cardkit alert-form 脚手架同样是第一行直接给结论，不内联标签）。
//   - **不重复说同一件事**：只有一条问题时省掉 headline——它和该条 reason 本来
//     就是同一句话的两种写法，并排放着像复读。多条时 headline 才有概括价值，
//     此时条目改用无序列表，让「概括 + 几条明细」的从属关系一眼可见。
//   - **不重复折叠条已经说的话**：「另有 N 条待核提示」由折叠条标题承担，这里不再说。
func reviewCardConfirmedRiskBlock(confirmed []publishRiskItem, headline string) map[string]any {
	limit := len(confirmed)
	if limit > 3 {
		limit = 3
	}
	// 首行把「需要确认」和一句话总括合成一句（用户 2026-08-08 手写的格式）：
	// 单条时下面那条本身就是结论，重复一遍等于复读，只留标签词。
	lead := "**需要确认**"
	if limit > 1 {
		lead += "：" + headline
	}
	elements := []any{map[string]any{"tag": "markdown", "content": lead}}
	elements = append(elements, map[string]any{
		"tag":     "markdown",
		"content": reviewCardRiskItemList(confirmed[:limit], 120, 100),
	})
	if len(confirmed) > limit {
		elements = append(elements, map[string]any{
			"tag":       "markdown",
			"text_size": "notation",
			"content": fmt.Sprintf("<font color='grey'>另有 %d 条已证实问题，见下方完整检查报告。</font>",
				len(confirmed)-limit),
		})
	}
	// 背景色块把这一组从卡片里"抬"出来（规范 §3）：平铺成文字会和摘要、来源行混成一片。
	return map[string]any{
		"tag":       "column_set",
		"flex_mode": "none",
		"columns": []any{map[string]any{
			"tag":              "column",
			"width":            "weighted",
			"weight":           1,
			"direction":        "vertical",
			"background_style": "red-100",
			"padding":          "12px 12px 12px 12px",
			"vertical_spacing": "8px",
			"elements":         elements,
		}},
	}
}

// reviewCardRiskItemList 风险条目 → 飞书官方无序列表（首屏红块与折叠报告共用一份，
// 语法只维护一处）。
//
// 两条来自官方文档的硬约束（open.feishu.cn 卡片 markdown 语法，2026-08-08 查证）：
//   - 列表项之间用 `\n` 分隔；
//   - **列表项内换行必须用 `<br>`**——单个 `\n` 是软换行，官方明说「渲染时可能被
//     忽略」，实测就是被忽略的（原句会粘到上一句尾巴上）。用 `<br>` 原句才留在
//     列表项内部，自动跟着圆点缩进对齐，不需要手画点号或用空格凑缩进。
//
// `<br>` 由本函数拼接，外部文本仍先经 reviewCardSafeText 收敛（其中 `<` 会被换成
// 全角，注入不进标签）。
func reviewCardRiskItemList(items []publishRiskItem, reasonLimit, evidenceLimit int) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		label := reviewCardSafeText(publishRiskCategoryLabel(item.Category), 40)
		if item.Category == publishRiskCategorySystem {
			lines = append(lines, fmt.Sprintf(
				"- **%s**：风险模型未返回可验证的结构化结果。<br><font color='grey'>请人工核对事实常识、功效表达、转载授权、竞品评价和数据来源后再决定。</font>",
				label))
			continue
		}
		lines = append(lines, fmt.Sprintf("- **%s**：%s<br><font color='grey'>原句：“%s”</font>",
			label,
			reviewCardSafeText(item.Reason, reasonLimit),
			reviewCardSafeText(item.Evidence, evidenceLimit)))
	}
	return strings.Join(lines, "\n")
}

func reviewCardDetailsPanel(job publishJob) map[string]any {
	elements := []any{}
	// 顺序即重要性：待核提示（人还得看）→ 排版说明（知道 AI 干了什么就行）。
	if risk := reviewCardRiskDetailBlock(job); risk != nil {
		elements = append(elements, risk)
	}
	if notes := reviewCardAINotesBlock(job); notes != nil {
		elements = append(elements, notes)
	}
	if len(elements) == 0 {
		return nil
	}
	return map[string]any{
		"tag": "collapsible_panel",
		// 首屏只留一句话结论；转载说明、存疑项与排版说明由审核人主动展开。
		"expanded": false,
		"header": map[string]any{
			"title":         feishuPlainText(reviewCardDetailsPanelTitle(job)),
			"width":         "auto_when_fold",
			"icon":          map[string]any{"tag": "standard_icon", "token": "info_outlined", "color": "grey"},
			"icon_position": "left",
		},
		"vertical_spacing": "4px",
		"elements":         elements,
	}
}

// reviewCardDetailsPanelTitle 折叠条自己说清里面有多少东西，否则审核人无从判断值不值得展开。
func reviewCardDetailsPanelTitle(job publishJob) string {
	_, deferred := splitPublishRiskItems(job.RiskItems)
	if len(deferred) > 0 {
		return fmt.Sprintf("点此查看完整检查报告（%d 条待核提示）", len(deferred))
	}
	return "点此查看完整检查报告"
}

// reviewCardAINotesBlock AI 优化说明段（M5）。notes 为空时不渲染。
func reviewCardAINotesBlock(job publishJob) map[string]any {
	notes := strings.TrimSpace(job.AINotes)
	if notes == "" {
		return nil
	}
	status, items := reviewCardAINoteParts(notes)
	var content strings.Builder
	content.WriteString("<font color='grey'>排版说明</font>")
	if status != "" {
		content.WriteString("\n")
		content.WriteString(status)
	}
	for i, item := range items {
		label, detail := reviewCardAINoteLabel(item)
		content.WriteString(fmt.Sprintf("\n\n%d. ", i+1))
		if label != "" {
			content.WriteString("**")
			content.WriteString(label)
			content.WriteString("**：")
		}
		content.WriteString(reviewCardSafeText(detail, 180))
	}
	return map[string]any{
		"tag": "markdown", "text_size": "notation",
		"content": content.String(),
	}
}

// reviewCardAINoteParts 保留模型说明里的真实语义分组：模型状态单列，其余说明按
// 显式换行和中文句末拆成最多 6 个检查项。拆分发生在安全转义之前，每一项最终仍由
// reviewCardSafeText 收敛，旧任务无需迁移即可获得编号排版。
func reviewCardAINoteParts(notes string) (string, []string) {
	var status string
	items := make([]string, 0, 6)
	for _, line := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "【双模型】") || strings.HasPrefix(line, "【双模型判决】") {
			if rendered, ok := reviewCardModelStatus(line); ok {
				status = rendered
				continue
			}
		}
		for _, sentence := range reviewCardSplitSentences(line) {
			if len(items) == 6 {
				break
			}
			items = append(items, sentence)
		}
	}
	return status, items
}

func reviewCardSplitSentences(value string) []string {
	var out []string
	var sentence strings.Builder
	flush := func() {
		if item := strings.TrimSpace(sentence.String()); item != "" {
			out = append(out, item)
		}
		sentence.Reset()
	}
	for _, r := range value {
		sentence.WriteRune(r)
		if r == '。' || r == '！' || r == '？' {
			flush()
		}
	}
	flush()
	return out
}

func reviewCardModelStatus(raw string) (string, bool) {
	switch {
	case strings.Contains(raw, "对比模型失败"):
		if strings.Contains(raw, "image_url") {
			return "<text_tag color='orange'>双模型降级</text_tag> 对比模型不支持图片输入，已采用主模型结果。", true
		}
		return "<text_tag color='orange'>双模型降级</text_tag> 对比模型本次未完成，已采用主模型结果。", true
	case strings.Contains(raw, "主模型失败"):
		return "<text_tag color='orange'>双模型降级</text_tag> 主模型本次未完成，已采用对比模型结果。", true
	case strings.Contains(raw, "判决节点失败"):
		return "<text_tag color='orange'>双模型降级</text_tag> 双模型判决本次未完成，已采用主模型结果。", true
	case strings.HasPrefix(raw, "【双模型判决】"):
		detail := strings.TrimSpace(strings.TrimPrefix(raw, "【双模型判决】"))
		return "**双模型判决**：" + reviewCardSafeText(detail, 180), true
	default:
		return "", false
	}
}

func reviewCardAINoteLabel(item string) (string, string) {
	labels := []struct {
		prefix string
		label  string
	}{
		{"封面", "封面"},
		{"摘要", "摘要"},
		{"标签", "标签"},
		{"作者", "作者"},
		{"来源", "来源"},
		{"原文", "原文核对"},
		{"正文", "正文"},
		{"排版", "排版"},
	}
	item = strings.TrimSpace(item)
	for _, candidate := range labels {
		if !strings.HasPrefix(item, candidate.prefix) {
			continue
		}
		detail := strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(item, candidate.prefix), "：:，, "))
		if detail != "" {
			return candidate.label, detail
		}
	}
	return "", item
}

// reviewCardRiskDetailBlock 折叠区里的完整检查报告。已证实的实质性问题已在首屏
// 逐条指认过，这里只承载「证明不了、但请你留意」的那一层：存疑项、转载授权说明、
// 检查异常。模型输出按纯文本收敛后再拼 markdown，避免正文片段注入标签/链接；
// 卡片最多展示 6 条，完整结果仍在任务详情。
func reviewCardRiskDetailBlock(job publishJob) map[string]any {
	if strings.TrimSpace(job.RiskLevel) == "" {
		return nil
	}
	confirmed, deferred := splitPublishRiskItems(job.RiskItems)
	var content strings.Builder
	content.WriteString("<font color='grey'>检查结论</font>\n**")
	content.WriteString(reviewCardSafeText(publishRiskLevelLabel(job.RiskLevel), 40))
	content.WriteString("**")
	if job.RiskLevel == publishRiskBlock {
		content.WriteString(" · 禁止一键发布")
	}
	if len(confirmed) > 0 {
		content.WriteString(fmt.Sprintf(" · <font color='grey'>%d 条已证实问题见上方</font>", len(confirmed)))
	}
	if len(deferred) == 0 {
		content.WriteString("\n\n<font color='grey'>没有其他待核提示。</font>")
		return map[string]any{
			"tag": "markdown", "content": content.String(), "text_size": "notation",
		}
	}
	content.WriteString("\n\n<font color='grey'>以下是证明不了、但请你留意的提示：</font>")
	limit := len(deferred)
	if limit > 6 {
		limit = 6
	}
	// 与首屏红块同一套无序列表语法（条数由折叠条标题承担，这里不再编号）。
	content.WriteString("\n\n")
	content.WriteString(reviewCardRiskItemList(deferred[:limit], 140, 140))
	if len(deferred) > limit {
		content.WriteString(fmt.Sprintf("\n\n另有 %d 条，请在任务详情中查看。", len(deferred)-limit))
	}
	if strings.TrimSpace(job.RiskNotes) != "" && !reviewCardHasSystemRisk(job.RiskItems) {
		content.WriteString("\n\n**补充说明**：")
		content.WriteString(reviewCardSafeText(job.RiskNotes, 180))
	}
	return map[string]any{
		"tag": "markdown", "content": content.String(), "text_size": "notation",
	}
}

func reviewCardHasSystemRisk(items []publishRiskItem) bool {
	for _, item := range items {
		if item.Category == publishRiskCategorySystem {
			return true
		}
	}
	return false
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
	actor := strings.TrimSpace(job.PublicationActor)
	whenRaw := strings.TrimSpace(job.PublicationActionAt)
	if actor == "" {
		actor = strings.TrimSpace(job.ReviewedBy)
	}
	if whenRaw == "" {
		whenRaw = job.ReviewedAt
	}
	when := formatReviewCardTime(whenRaw)
	if feishuOpenIDSafe(actor) {
		return fmt.Sprintf(
			"<person id=\"%s\" show_avatar=\"true\" show_name=\"true\" style=\"capsule\"></person> %s <font color='grey'>%s</font>",
			actor, actionLabel, when)
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
		"flex_mode":          "none",
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
