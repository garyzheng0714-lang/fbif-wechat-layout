package main

// feishu_review_card_distribution.go — 审核卡底部的「分发到其他平台」区。
//
// 2026-10-12 用户裁定：「分发区域可以做成一个专门的区域，不用事先隐藏起来……相当于加一个分割线，
// 底部去排版分发的内容。每个分发平台对应一张卡片」；超限时「能否直接在卡片里改好？不过这里一定
// 要做好引导，提示清楚要把什么改成什么」。
//
// 只有原创文章有这一区。每个平台一个细外框块：图标、名字、靠右的状态（或同步按钮）；下面一行是
// 文章链接或原因。待修改的平台（publish_external_drafts.go）块里直接是表单：哪一项超了多少、
// 现在是什么，输入框里预填建议改成的内容，封面从正文大图里点选；保存后服务端重查，通过即推。
// 卡片里做不了的（上传封面）给侧边栏链接（publish_external_edit.go）。
//
// 飞书限制：form 只能放在卡片根上，所以有表单的平台块整个包在根级 form 里；字段名带文章编号与
// 平台位置，回调只带平台位置（slot），不带账号 id（红线 37）。

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const (
	feishuCardActionDistributeFix = "distribute_fix"

	reviewCardPlatformRadius  = 12
	reviewCardPlatformPadding = "10px 12px 10px 12px"
)

// reviewCardDistributionElements 根级元素：分割线、区标题，再是每篇文章的平台块。
// heading=true 时每篇先写「#编号 标题」（聚合卡里有多篇时）。
func reviewCardDistributionElements(jobs []publishJob, allowStart, aggregate bool, icons reviewCardIcons) []any {
	cfg, cfgErr := loadYunsuoConfig()
	var targets []yunsuoAccountTarget
	if cfgErr == nil {
		targets = cfg.Targets
	}
	heading := aggregate && len(jobs) > 1
	var body []any
	for _, job := range jobs {
		body = append(body, reviewCardDistributionGroup(job, targets, allowStart, aggregate, heading, icons)...)
	}
	if len(body) == 0 {
		return nil
	}
	return append([]any{
		map[string]any{"tag": "hr", "margin": "4px 0px 0px 0px"},
		map[string]any{"tag": "markdown", "content": "**分发到其他平台**"},
	}, body...)
}

// reviewCardExternalDistributionElements 单篇卡（降级路径）的分发区，与聚合卡同一写法。
func reviewCardExternalDistributionElements(job publishJob, allowStart bool) []any {
	return reviewCardDistributionElements([]publishJob{job}, allowStart, false, nil)
}

func reviewCardDistributionGroup(job publishJob, targets []yunsuoAccountTarget, allowStart, aggregate, heading bool, icons reviewCardIcons) []any {
	started := externalDistributionStarted(job)
	switch {
	case job.Stage == publishStagePublished:
	case job.Stage == publishStageOffline && started:
		// 外部平台与 FoodTalks 生命周期彼此独立：下线前发起的分发仍保留结果，但不再提供新入口。
		allowStart = false
	default:
		return nil
	}
	provenance, _, evidence := publishExternalDistributionProvenance(job)
	original := provenance == publishExternalProvenanceOriginal
	if !started && !original {
		// 聚合卡里转载稿没有这一区；单篇卡说一句为什么不外发。
		if !allowStart || aggregate {
			return nil
		}
		message := "转载文章仅发布至 FoodTalks，不同步外部平台。"
		if provenance == publishExternalProvenanceUnverified {
			message = "原创状态未完成核验，暂不开放外部分发（" + evidence + "）。"
		}
		return []any{map[string]any{
			"tag": "markdown", "text_size": "notation",
			"content": "<font color='grey'>" + reviewCardSafeText(message, 180) + "</font>",
		}}
	}
	if !started && (!allowStart || len(targets) == 0) {
		// 没开始分发、又不能从这张卡发起（进度卡、缺配置）：不渲染一个必失败的入口。
		return nil
	}
	canStart := allowStart && original
	var blocks []any
	seen := map[string]bool{}
	for slot, target := range targets {
		seen[strings.TrimSpace(target.ID)] = true
		if _, ok := externalLaneFor(job, target.ID); !ok && !original {
			continue
		}
		blocks = append(blocks, reviewCardPlatformBlock(job, target, slot, canStart, icons))
	}
	for _, lane := range job.ExternalDistributionLanes {
		if seen[strings.TrimSpace(lane.AccountID)] {
			continue
		}
		// 配置里已去掉的账号：只展示结果，不给操作。
		blocks = append(blocks, reviewCardPlatformBlock(job, yunsuoAccountTarget{ID: lane.AccountID, Name: lane.Name}, -1, false, icons))
	}
	if len(blocks) == 0 {
		return nil
	}
	if heading {
		blocks = append([]any{map[string]any{
			"tag": "markdown", "margin": "4px 0px 0px 0px",
			"content": fmt.Sprintf("#%d %s", job.ID, reviewCardSafeOrDash(job.Title, 80)),
		}}, blocks...)
	}
	return blocks
}

// reviewCardPlatformBlock 一个平台一块。待修改且能从这张卡改的，返回包着它的根级 form。
func reviewCardPlatformBlock(job publishJob, target yunsuoAccountTarget, slot int, canStart bool, icons reviewCardIcons) map[string]any {
	profile := yunsuoProfileForTarget(target)
	name := reviewCardSafeText(firstNonEmpty(target.Name, profile.Name), 20)
	if name == "" {
		name = "未命名平台"
	}
	state := externalPlatformStatus(job, target)
	actionable := canStart && slot >= 0
	fixing := actionable && state.NeedsFix
	editLink := externalDistributionSidebarLink(job.ID)

	var right map[string]any
	if actionable && state.Editable && !state.NeedsFix {
		right = reviewCardPlatformSyncButton(job, slot, name, state)
	}
	buttoned := right != nil
	if right == nil {
		right = map[string]any{
			"tag": "markdown", "text_align": "right",
			"content": "<font color='" + state.Tone + "'>" + state.Status + "</font>",
		}
	}
	var columns []any
	if key := icons[profile.Icon]; key != "" {
		columns = append(columns, map[string]any{
			"tag": "column", "width": "auto", "vertical_align": "center",
			"elements": []any{map[string]any{
				"tag": "img", "img_key": key, "alt": feishuPlainText(name),
				"scale_type": "crop_center", "size": "16px 16px", "corner_radius": "3px",
				"transparent": true, "preview": false,
			}},
		})
	}
	columns = append(columns,
		map[string]any{
			"tag": "column", "width": "weighted", "weight": 1, "vertical_align": "center",
			"elements": []any{map[string]any{"tag": "markdown", "content": "**" + name + "**"}},
		},
		map[string]any{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []any{right}},
	)
	elements := []any{map[string]any{
		"tag": "column_set", "flex_mode": "none", "horizontal_spacing": "8px", "columns": columns,
	}}
	if line := reviewCardPlatformSecondLine(job, state, fixing, buttoned, editLink); line != "" {
		elements = append(elements, map[string]any{"tag": "markdown", "text_size": "notation", "content": line})
	}
	if fixing {
		draft, _ := externalDraftFor(job, target.ID)
		elements = append(elements, reviewCardFixFields(job, draft, profile, name, slot, editLink)...)
	}
	link := firstNonEmpty(state.URL, editLink, externalEditSiteURL(job), reviewCardSafeURL(siteFrontURL()))
	box := map[string]any{
		// 飞书只有交互容器能画外框，它必须带点击行为：点块里空白处打开平台上的文章，还没有文章时
		// 打开侧边栏改稿页；块内输入框与按钮优先响应自己的交互（与文章块同一写法）。
		"tag": "interactive_container", "width": "fill",
		"has_border": true, "border_color": reviewBatchBorderColor,
		"corner_radius": fmt.Sprintf("%dpx", reviewCardPlatformRadius),
		"padding":       reviewCardPlatformPadding, "vertical_spacing": "6px",
		"behaviors": []any{map[string]any{"type": "open_url", "default_url": link, "pc_url": "", "ios_url": "", "android_url": ""}},
		"elements":  elements,
	}
	if !fixing {
		return box
	}
	return map[string]any{
		"tag": "form", "name": fmt.Sprintf("fix_%d_%d", job.ID, slot),
		"elements": []any{box},
	}
}

// reviewCardPlatformSecondLine 名字下面一行：已发布给文章链接（平台改过标题就写平台上的标题），
// 失败/待核对写原因，待修改但卡片里改不了的指去侧边栏。右侧是重新同步按钮时状态字不显示，
// 失败就写在这一行开头，免得只看到一句灰字原因。
func reviewCardPlatformSecondLine(job publishJob, state externalPlatformState, fixing, buttoned bool, editLink string) string {
	if state.URL != "" {
		label := "查看文章"
		if state.Title != "" && normalizeSiteTitle(state.Title) != normalizeSiteTitle(job.Title) {
			label = reviewCardSafeText(state.Title, 40)
		}
		return "[" + label + "](" + state.URL + ")"
	}
	if fixing {
		return "" // 表单里逐项写清楚
	}
	reason := reviewCardSafeText(state.Reason, 80)
	if reason == "" {
		return ""
	}
	if state.NeedsFix && editLink != "" {
		return "<font color='grey'>" + reason + "</font>　[去修改](" + editLink + ")"
	}
	if buttoned && state.Tone == "red" {
		return "<font color='red'>上次同步失败</font>　<font color='grey'>" + reason + "</font>"
	}
	return "<font color='grey'>" + reason + "</font>"
}

// reviewCardPlatformSyncButton 没推过、或受理前失败可安全重推的平台：按钮推这一个平台。
func reviewCardPlatformSyncButton(job publishJob, slot int, name string, state externalPlatformState) map[string]any {
	retry := state.Status == "失败"
	text := "同步至" + name
	confirm := "请确认本文为原创文章。外部发布独立于 FoodTalks；下线本站文章不会自动下线外部平台。确认后将立即提交发布。"
	if retry {
		text = "重新同步至" + name
		confirm = "上次请求已确认未被云梭受理，不会重复发布。本次会重新提交；请同时确认本文为原创文章。"
	}
	return map[string]any{
		"tag": "button", "text": feishuPlainText(text), "type": "default",
		"size": "tiny", "width": "default",
		"confirm": map[string]any{"title": feishuPlainText(text + "？"), "text": feishuPlainText(confirm)},
		"behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{
			"action":               feishuCardActionDistributeExternal,
			"job_id":               job.ID,
			"slot":                 slot,
			"distribution_version": publishExternalDistributionCardVersion(job),
		}}},
	}
}

// reviewCardFixFields 待修改平台的表单：逐项写「哪里超了：要求，现状」，再给改的地方。
func reviewCardFixFields(job publishJob, draft publishExternalDraft, profile yunsuoPlatformProfile, name string, slot int, editLink string) []any {
	var out []any
	note := func(content string) {
		out = append(out, map[string]any{"tag": "markdown", "text_size": "notation", "content": content})
	}
	fields := externalIssueFields(draft.Issues)
	if issue, ok := fields[publishExternalIssueTitle]; ok {
		note(fmt.Sprintf("<font color='orange'>标题超出 %d 字</font>：%s最多 %d 字，现在 %d 字",
			issue.Length-issue.Max, name, issue.Max, issue.Length))
		note("<font color='grey'>现在：" + reviewCardSafeText(issue.Current, 120) + "</font>")
		out = append(out, reviewCardFixInput(reviewCardFixFieldName("t", job.ID, slot), "改成",
			firstNonEmpty(draft.SuggestedTitle, issue.Current), issue.Max, false))
	}
	if issue, ok := fields[publishExternalIssueSummary]; ok {
		note(fmt.Sprintf("<font color='orange'>摘要超出 %d 字</font>：%s最多 %d 字，现在 %d 字",
			issue.Length-issue.Max, name, issue.Max, issue.Length))
		note("<font color='grey'>现在：" + reviewCardSafeText(issue.Current, 200) + "</font>")
		out = append(out, reviewCardFixInput(reviewCardFixFieldName("s", job.ID, slot), "改成",
			firstNonEmpty(draft.SuggestedSummary, issue.Current), issue.Max, true))
	}
	if issue, ok := fields[publishExternalIssueCover]; ok {
		if issue.Missing {
			note(fmt.Sprintf("<font color='orange'>没有可用的封面</font>：%s封面至少 %d×%d", name, issue.MinWidth, issue.MinHeight))
		} else {
			note(fmt.Sprintf("<font color='orange'>封面太小</font>：%s封面至少 %d×%d，现在 %d×%d",
				name, issue.MinWidth, issue.MinHeight, issue.Width, issue.Height))
		}
		var options []any
		for _, candidate := range draft.Candidates {
			if candidate.ImgKey == "" || (job.ReviewDeliveryAppID != "" && candidate.ImgApp != job.ReviewDeliveryAppID) {
				continue
			}
			options = append(options, map[string]any{"img_key": candidate.ImgKey, "value": reviewCardCoverValue(candidate.URL)})
		}
		upload := ""
		if editLink != "" {
			upload = "[上传一张](" + editLink + ")"
		}
		switch {
		case len(options) > 0:
			text := "<font color='grey'>从正文里选一张作封面</font>"
			if upload != "" {
				text += "，或" + upload
			}
			note(text)
			out = append(out, map[string]any{
				"tag": "select_img", "name": reviewCardFixFieldName("c", job.ID, slot),
				"multi_select": false, "layout": "trisect", "aspect_ratio": "16:9",
				"style": "laser", "can_preview": false, "required": false, "options": options,
			})
		case upload != "":
			note("<font color='grey'>正文里没有够大的图，</font>" + upload)
		}
	}
	buttons := []map[string]any{{
		"tag": "button", "text": feishuPlainText("保存并同步至" + name), "type": "primary_filled",
		"size": "small", "width": "default",
		"name": reviewCardFixFieldName("submit", job.ID, slot), "form_action_type": "submit",
		"behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{
			"action":               feishuCardActionDistributeFix,
			"job_id":               job.ID,
			"slot":                 slot,
			"distribution_version": publishExternalDistributionCardVersion(job),
		}}},
	}}
	row := reviewBatchInlineButtons(buttons...)
	if editLink != "" {
		row["columns"] = append(row["columns"].([]any), map[string]any{
			"tag": "column", "width": "auto", "vertical_align": "center",
			"elements": []any{map[string]any{"tag": "markdown", "text_size": "notation", "content": "[在侧边栏修改](" + editLink + ")"}},
		})
	}
	row["margin"] = "4px 0px 0px 0px"
	return append(out, row)
}

func reviewCardFixInput(name, label, value string, max int, multiline bool) map[string]any {
	input := map[string]any{
		"tag": "input", "name": name, "width": "fill", "required": false,
		"label":          feishuPlainText(label),
		"label_position": "left",
		"default_value":  value,
		"placeholder":    feishuPlainText(fmt.Sprintf("%d 字以内", max)),
		"max_length":     max,
	}
	if multiline {
		input["input_type"] = "multiline_text"
		input["rows"] = 2
		input["auto_resize"] = true
		input["max_rows"] = 5
	}
	return input
}

// reviewCardFixFieldName 表单字段名在整张卡里唯一（聚合卡里可能有几篇、几个平台同时待修改）。
func reviewCardFixFieldName(kind string, jobID int64, slot int) string {
	return fmt.Sprintf("%s_%d_%d", kind, jobID, slot)
}

// reviewCardCoverValue 封面选项的值：图片地址的短摘要（回调里再按候选列表换回地址）。
func reviewCardCoverValue(url string) string {
	digest := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%x", digest[:6])
}
