package main

// feishu_review_card_distribution.go — 审核卡底部的「分发到其他平台」区。
//
// 2026-10-12 用户裁定：「分发区域可以做成一个专门的区域，不用事先隐藏起来……相当于加一个分割线，
// 底部去排版分发的内容」；同日补充：「整个卡片应该分两个区域」「上半部分这三个是不变的」「一般只有
// 一篇是原创……得用一个单独的区域呈现出来；当然也可以用容器循环组件」「同一篇文章可能会同时发到
// 百家号、头条号、大鱼号、知乎等五六家平台……做一个循环容器组件」。
//
// 版式（照 Square「Channels」、Shopify「Sales channels」的渠道列表）：每篇原创一个与上半部分文章块
// 同款的细外框块（同色、同圆角、同内边距）；块里先是这篇的小封面与「#编号 标题」（卡里只有这一篇时
// 不重复），下面每个平台一行：图标、名字、文章链接（平台标题与本文不同就写平台标题）、靠右的纯色
// 状态字或「同步至X」按钮；失败原因在下一行灰字。平台多了只是行多，不换版式。
//
// 待修改的平台（publish_external_drafts.go）在它那一行下面直接展开表单：哪一项超了多少、现在是什么，
// 输入框里预填建议，封面从正文大图里点选；保存后服务端重查，通过即推。卡片里做不了的上传：话题发图
// 开通后让人把图发到这张卡的话题里（publish_review_thread.go），没开通时给侧边栏链接。
//
// 飞书限制：form 只能放在卡片根上，所以有表单的文章块整个包在根级 form 里；字段名带文章编号与
// 平台位置，回调只带平台位置（slot），不带账号 id（红线 37）。

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const (
	feishuCardActionDistributeFix = "distribute_fix"

	// 平台行之间、表头与平台行之间的间距；文章块外框沿用上半部分文章块的样式。
	reviewCardPlatformSpacing = "12px"
	reviewCardGroupCoverSize  = 36
)

// reviewCardDistributionElements 根级元素：分割线、区标题，再是每篇原创一块。
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
	var rows []any
	fixing := false
	seen := map[string]bool{}
	for slot, target := range targets {
		seen[strings.TrimSpace(target.ID)] = true
		if _, ok := externalLaneFor(job, target.ID); !ok && !original {
			continue
		}
		row, rowFixing := reviewCardPlatformRow(job, target, slot, canStart, icons)
		rows = append(rows, row)
		fixing = fixing || rowFixing
	}
	for _, lane := range job.ExternalDistributionLanes {
		if seen[strings.TrimSpace(lane.AccountID)] {
			continue
		}
		// 配置里已去掉的账号：只展示结果，不给操作。
		row, _ := reviewCardPlatformRow(job, yunsuoAccountTarget{ID: lane.AccountID, Name: lane.Name}, -1, false, icons)
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	if heading {
		rows = append([]any{reviewCardDistributionHeader(job)}, rows...)
	}
	link := firstNonEmpty(externalDistributionSidebarLink(job.ID), externalEditSiteURL(job), reviewCardSafeURL(siteFrontURL()))
	box := map[string]any{
		// 与上半部分文章块同一外框。飞书只有交互容器能画外框，它必须带点击行为：点块里空白处打开
		// 侧边栏里这篇的分发页；块内链接、输入框与按钮优先响应自己的交互。
		"tag": "interactive_container", "width": "fill",
		"has_border": true, "border_color": reviewBatchBorderColor,
		"corner_radius":    fmt.Sprintf("%dpx", reviewBatchBlockRadius),
		"padding":          fmt.Sprintf("%[1]dpx %[1]dpx %[1]dpx %[1]dpx", reviewBatchBlockPadding),
		"vertical_spacing": reviewCardPlatformSpacing,
		"behaviors":        []any{map[string]any{"type": "open_url", "default_url": link, "pc_url": "", "ios_url": "", "android_url": ""}},
		"elements":         rows,
	}
	if !fixing {
		return []any{box}
	}
	return []any{map[string]any{
		"tag": "form", "name": fmt.Sprintf("fix_%d", job.ID),
		"elements": []any{box},
	}}
}

// reviewCardDistributionHeader 文章块表头：小封面 + 「#编号 标题」，与上半部分同一张封面。
func reviewCardDistributionHeader(job publishJob) map[string]any {
	title := map[string]any{"tag": "div", "text": map[string]any{
		"tag": "lark_md", "lines": 2,
		"content": fmt.Sprintf("**#%d %s**", job.ID, reviewCardSafeOrDash(job.Title, 120)),
	}}
	columns := []any{}
	if key := strings.TrimSpace(job.ReviewCoverImgKey); key != "" {
		columns = append(columns, map[string]any{
			"tag": "column", "width": "auto", "vertical_align": "center",
			"elements": []any{map[string]any{
				"tag": "img", "img_key": key, "alt": feishuPlainText("封面"), "preview": false,
				"scale_type": "crop_center", "size": fmt.Sprintf("%[1]dpx %[1]dpx", reviewCardGroupCoverSize),
				"corner_radius": fmt.Sprintf("%dpx", reviewBatchCoverRadius),
			}},
		})
	}
	columns = append(columns, map[string]any{
		"tag": "column", "width": "weighted", "weight": 1, "vertical_align": "center", "elements": []any{title},
	})
	return map[string]any{"tag": "column_set", "flex_mode": "none", "horizontal_spacing": "10px", "columns": columns}
}

// reviewCardPlatformRow 一个平台一行（一个竖排分栏，行内间距比平台之间小）。待修改且能从这张卡
// 改的，下面接着放表单字段，返回 fixing=true，由文章块包进根级 form。
func reviewCardPlatformRow(job publishJob, target yunsuoAccountTarget, slot int, canStart bool, icons reviewCardIcons) (map[string]any, bool) {
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
	label := "**" + name + "**"
	if link := reviewCardPlatformArticleLink(job, state); link != "" {
		label += "　" + link
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
			"elements": []any{map[string]any{"tag": "markdown", "content": label}},
		},
		map[string]any{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []any{right}},
	)
	elements := []any{map[string]any{
		"tag": "column_set", "flex_mode": "none", "horizontal_spacing": "8px", "columns": columns,
	}}
	if line := reviewCardPlatformReason(state, fixing, buttoned, editLink); line != "" {
		elements = append(elements, map[string]any{"tag": "markdown", "text_size": "notation", "content": line})
	}
	if fixing {
		draft, _ := externalDraftFor(job, target.ID)
		elements = append(elements, reviewCardFixFields(job, draft, profile, name, slot, editLink)...)
	}
	return map[string]any{
		"tag": "column_set", "flex_mode": "none",
		"columns": []any{map[string]any{
			"tag": "column", "width": "weighted", "weight": 1, "direction": "vertical",
			"vertical_spacing": "6px", "elements": elements,
		}},
	}, fixing
}

// reviewCardPlatformArticleLink 名字后面的文章链接：平台改过标题就写平台上的标题，否则「查看文章」。
func reviewCardPlatformArticleLink(job publishJob, state externalPlatformState) string {
	if state.URL == "" {
		return ""
	}
	label := "查看文章"
	if state.Title != "" && normalizeSiteTitle(state.Title) != normalizeSiteTitle(job.Title) {
		label = reviewCardSafeText(state.Title, 40)
	}
	return "[" + label + "](" + state.URL + ")"
}

// reviewCardPlatformReason 名字下面一行：失败/待核对写原因，待修改但卡片里改不了的指去侧边栏。
// 右侧是重新同步按钮时状态字不显示，失败就写在这一行开头，免得只看到一句灰字原因。
func reviewCardPlatformReason(state externalPlatformState, fixing, buttoned bool, editLink string) string {
	if state.URL != "" || fixing {
		return "" // 有文章链接就不再写原因；表单里逐项写清楚
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
		// 卡片里没有上传控件：给侧边栏改稿页的链接，在那里点选、拖入或粘贴图片上传。
		upload := ""
		if editLink != "" {
			upload = "[上传一张](" + editLink + ")"
		}
		switch {
		case len(options) > 0:
			text := "<font color='grey'>从正文里选一张作封面</font>"
			if upload != "" {
				text += "<font color='grey'>，或</font>" + upload
			}
			note(text)
			out = append(out, map[string]any{
				"tag": "select_img", "name": reviewCardFixFieldName("c", job.ID, slot),
				"multi_select": false, "layout": "trisect", "aspect_ratio": "16:9",
				"style": "laser", "can_preview": false, "required": false, "options": options,
			})
		case upload != "":
			note("<font color='grey'>正文里没有够大的图，请</font>" + upload)
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
