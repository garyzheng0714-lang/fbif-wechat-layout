package main

// feishu_review_card_test.go — 审核卡信息完整性守护。
//
// 背景：终态卡曾经只渲染 reviewCardInfoBlock（来源/作者/时间/任务号两行），
// 审核通过后封面、标题、标签、摘要和原文链接全部消失。审核卡在群里长期留存，
// 是事后回看「到底发了什么」的唯一入口，退化成一个任务号等于信息归零。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func reviewCardTestJob(stage string) publishJob {
	return publishJob{
		ID: 72, Stage: stage,
		Title:      "首年销量破亿，冲进沃尔玛前五！这个女性能量饮料，靠青汁粉走红了",
		SourceName: "轻食online", Author: "FBIF食品饮料创新",
		Summary:           "靠青汁粉走红的女性能量饮料，凭什么首年销售额破亿冲进沃尔玛前五？",
		Tags:              []string{"能量饮料", "女性消费"},
		SourceURL:         "https://mp.weixin.qq.com/s?__biz=MzA5&mid=1&idx=1&sn=abc",
		ArticleURL:        "https://test.foodtalks.cn/news/7318",
		ReviewCoverImgKey: "img_v3_cover_key",
		RiskLevel:         publishRiskWarning,
		UpdatedAt:         "2026-08-06T00:57:00Z",
	}
}

func cardJSON(t *testing.T, card map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("卡片无法序列化: %v", err)
	}
	return string(raw)
}

// TestReviewCardsKeepArticleIdentity 待审核、已发布、已驳回和异常四种卡片都必须
// 让人一眼认出是哪篇文章：封面、标题、摘要、原文链接缺一不可。
func TestReviewCardsKeepArticleIdentity(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	cards := map[string]map[string]any{
		"待审核":  buildReviewPendingCard(reviewCardTestJob(publishStageAwaitingReview), job.ReviewCoverImgKey),
		"已发布":  buildReviewDoneCard(job),
		"已驳回":  buildReviewDoneCard(reviewCardTestJob(publishStageRejected)),
		"发布失败": buildReviewIssueCard(job, false),
	}
	for name, card := range cards {
		body := cardJSON(t, card)
		for _, want := range []struct {
			what     string
			fragment string
		}{
			{"封面 img_key", job.ReviewCoverImgKey},
			{"标题", "首年销量破亿"},
			{"摘要", "凭什么首年销售额破亿"},
			{"标签", "能量饮料"},
			{"公众号原文链接", "mp.weixin.qq.com"},
		} {
			if !strings.Contains(body, want.fragment) {
				t.Errorf("%s卡缺少%s（片段 %q）", name, want.what, want.fragment)
			}
		}
	}
}

// TestReviewDoneCardWithoutCoverStaysSingleColumn 旧任务没存过 img_key 时，
// 终态卡必须退化成无封面单列，而不是留一个空图位或直接报错。
func TestReviewDoneCardWithoutCoverStaysSingleColumn(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.ReviewCoverImgKey = ""
	body := cardJSON(t, buildReviewDoneCard(job))
	if strings.Contains(body, `"tag":"img"`) {
		t.Fatalf("无封面任务不应渲染 img 元素: %s", body)
	}
	if !strings.Contains(body, "首年销量破亿") {
		t.Fatalf("无封面时标题仍必须保留: %s", body)
	}
}

// TestReviewDoneCardKeepsPublishedArticleLink 已发布卡必须给出站内正式链接，
// 这是审核人核对发布结果的落点。
func TestReviewDoneCardKeepsPublishedArticleLink(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	body := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(body, job.ArticleURL) {
		t.Fatalf("已发布卡缺少站内文章链接 %q: %s", job.ArticleURL, body)
	}
}

func TestPublishedReviewCardShowsSourceResyncButtonInActionRow(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	body := cardJSON(t, buildReviewBatchCard([]publishJob{job}))
	for _, want := range []string{
		`"action":"resync_source"`, `"source_resync_version":`, "不会新建文章", "后台人工修改",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("已发布卡片缺少 %q: %s", want, body)
		}
	}
	// 下线窗口已过：不再出置灰的「不可下线」；按钮为公众号原文 → 重新同步至网站（最右）。
	sourceAt := strings.Index(body, `"content":"公众号原文"`)
	resyncAt := strings.Index(body, `"content":"重新同步至网站"`)
	if sourceAt < 0 || resyncAt < sourceAt || strings.Contains(body, "不可下线") || strings.Contains(body, `"content":"网站"`) {
		t.Fatalf("按钮应为公众号原文、重新同步，不出不可下线和网站: %s", body)
	}

	job.PublicationAction = publishPublicationActionManualCMS
	if manual := cardJSON(t, buildReviewBatchCard([]publishJob{job})); strings.Contains(manual, "resync_source") {
		t.Fatalf("后台人工发布稿不得显示覆盖按钮: %s", manual)
	}
}

func TestPublishedReviewCardShowsSourceResyncResultBelowButton(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.SourceResyncState = publishSourceResyncSucceeded
	job.SourceResyncCompletedAt = "2026-09-01T06:30:00Z"
	// 同步结果跟在按钮行末尾（它就是「重新同步」的结果），不单占一行，右栏高度才固定。
	body := cardJSON(t, buildReviewBatchCard([]publishJob{job}))
	buttonAt := strings.Index(body, `"content":"重新同步至网站"`)
	successAt := strings.Index(body, "同步成功")
	if buttonAt < 0 || successAt < buttonAt || !strings.Contains(body, "color='green'") {
		t.Fatalf("同步成功提示应以绿色跟在按钮后面: %s", body)
	}

	job.SourceResyncState = publishSourceResyncFailed
	job.SourceResyncError = "公众号正文抓取失败"
	body = cardJSON(t, buildReviewBatchCard([]publishJob{job}))
	buttonAt = strings.Index(body, `"content":"重新同步至网站"`)
	failureAt := strings.Index(body, "同步失败")
	if buttonAt < 0 || failureAt < buttonAt || !strings.Contains(body, "color='red'") ||
		!strings.Contains(body, job.SourceResyncError) {
		t.Fatalf("同步失败提示应以红色跟在按钮后面并给出原因: %s", body)
	}
}

func TestReviewBatchCardKeepsOneResponsiveCardAndPerArticleActions(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	now := time.Now().UTC()
	pending := reviewCardTestJob(publishStageAwaitingReview)
	pending.ID = 118
	pending.CreatedAt = "2026-08-20T01:00:00Z"
	pending.ContentHash = "pending"
	pending.ContentVersion = 1
	pending.RiskLevel = publishRiskPass
	pending.RiskItems = []publishRiskItem{{Category: "authorization", Reason: "需人工确认转载授权", Certainty: publishRiskCertaintyUnverified}}
	quietBody := cardJSON(t, buildReviewBatchCard([]publishJob{pending}))
	if strings.Contains(quietBody, "风险核查") || strings.Contains(quietBody, "需人工确认转载授权") {
		t.Fatalf("正常、待核或检查失败不得作为内容告警打扰审核人: %s", quietBody)
	}
	pending.RiskItems = append(pending.RiskItems, publishRiskItem{
		Category: "factual", Reason: "已确认的事实错误", Certainty: publishRiskCertaintyConfirmed,
	})
	published := reviewCardTestJob(publishStagePublished)
	published.ID = 117
	published.CreatedAt = pending.CreatedAt
	published.PublishedAt = now.Add(-time.Hour).Format(time.RFC3339)
	published.UnpublishPolicySet = true
	published.UnpublishWindowHours = 8
	published.UnpublishWindowUntil = now.Add(time.Hour).Format(time.RFC3339)

	body := cardJSON(t, buildReviewBatchCard([]publishJob{published, pending}))
	for _, want := range []string{
		"FBIF文章多平台自动同步", "8月20日 · 2篇文章 · 待审核1篇 · 已发布1篇",
		`"scale_type":"fit_horizontal"`, `"border_color":"grey-100"`, "#117", "#118",
		`\u003ctext_tag color='orange'\u003e待审核\u003c/text_tag\u003e`, `\u003ctext_tag color='green'\u003e已发布\u003c/text_tag\u003e`,
		`\u003ctext_tag color='blue'\u003e能量饮料\u003c/text_tag\u003e \u003ctext_tag color='blue'\u003e女性消费`,
		`"content":"公众号原文"`, `"action":"approve"`, `"action":"unpublish"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("聚合卡缺少 %q: %s", want, body)
		}
	}
	if strings.Count(body, `"schema":"2.0"`) != 1 {
		t.Fatalf("同批文章必须共用一张卡: %s", body)
	}
	// 2026-10-10 定稿：每篇一个白底细外框块（grey-50 线，不再灰底），篇间靠间距不画线；
	// 按钮一律最小号、按内容宽度，不撑满。
	for _, unwanted := range []string{`"background_style":"grey-50"`, `"background_style":"grey-100"`, `"tag":"hr"`, `\u003ctext_tag color='neutral'\u003e转载`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("聚合卡不应再出现 %s: %s", unwanted, body)
		}
	}
	if strings.Count(body, `"tag":"interactive_container"`) != 2 {
		t.Fatalf("两篇各一个外框块: %s", body)
	}
	if strings.Count(body, `"tag":"collapsible_panel"`) != 1 ||
		strings.Count(body, "风险核查（1篇有提示）") != 1 ||
		strings.Index(body, `"action":"approve"`) > strings.Index(body, "风险核查") ||
		!strings.Contains(body, "已确认的事实错误") || strings.Contains(body, "需人工确认转载授权") {
		t.Fatalf("风险核查必须只在全部文章之后出现一次: %s", body)
	}
}

// TestReviewBatchCardKeepsOneLeftEdge 2026-10-10 定稿（用户「现在是对的」）：S2 + 「每个应该是
// 独立的块，这样才好微调」。每篇一个浅灰块，左边固定边长方形封面，右边四个独立的单行块——
// 标题行（［状态］+ 链到网站的粗体标题，1 行截断）、摘要行（1 行截断）、信息行（原创/转载 ·
// #任务号 + 蓝色标签）、按钮行。两栏都贴顶，块间距是具名常量。头部不带图标，折叠条箭头跟在标题后。
func TestReviewBatchCardKeepsOneLeftEdge(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.RiskItems = []publishRiskItem{{Category: "factual", Reason: "已确认的事实错误", Certainty: publishRiskCertaintyConfirmed}}
	card := buildReviewBatchCard([]publishJob{job})
	if _, hasIcon := card["header"].(map[string]any)["icon"]; hasIcon {
		t.Fatalf("没有图标 img_key 时头部不带图标: %#v", card["header"])
	}
	withIcons := buildReviewBatchCardWithIcons([]publishJob{job}, reviewCardTestIconKeys(t))
	if icon, _ := withIcons["header"].(map[string]any)["icon"].(map[string]any); icon["tag"] != "custom_icon" || icon["img_key"] != "img_network-wathet" {
		t.Fatalf("头部图标应为浅蓝 network（用户 2026-10-10 选定）: %#v", withIcons["header"])
	}
	if card["header"].(map[string]any)["template"] != "wathet" {
		t.Fatalf("头部用浅蓝 wathet: %#v", card["header"])
	}
	elements := card["body"].(map[string]any)["elements"].([]any)
	article := elements[0].(map[string]any)
	if article["tag"] != "interactive_container" || article["has_border"] != true || article["border_color"] != reviewBatchBorderColor ||
		article["corner_radius"] != fmt.Sprintf("%dpx", reviewBatchBlockRadius) || article["background_style"] != nil {
		t.Fatalf("每篇是一个白底细外框块: %#v", article)
	}
	if !strings.Contains(fmt.Sprint(article["behaviors"]), job.ArticleURL) {
		t.Fatalf("点块里空白处打开网站文章: %#v", article["behaviors"])
	}
	inner := article["elements"].([]any)[0].(map[string]any)
	columns := inner["columns"].([]any)
	if len(columns) != 2 || inner["flex_mode"] != "none" {
		t.Fatalf("块内左图右文，手机上也不堆叠（图片按比例缩）: %#v", inner)
	}
	coverColumn, textColumn := columns[0].(map[string]any), columns[1].(map[string]any)
	if coverColumn["weight"] != reviewBatchCoverWeight || textColumn["weight"] != reviewBatchTextWeight ||
		coverColumn["vertical_align"] != "top" || textColumn["vertical_align"] != "top" {
		t.Fatalf("图文按比例分宽、两栏贴顶: %#v | %#v", coverColumn, textColumn)
	}
	if cover := coverColumn["elements"].([]any)[0].(map[string]any); cover["scale_type"] != "fit_horizontal" || cover["size"] != nil {
		t.Fatalf("封面铺满封面栏、随栏宽缩放: %#v", cover)
	}
	rows := textColumn["elements"].([]any)
	if len(rows) != 4 {
		t.Fatalf("右栏是标题、摘要、信息、按钮四个独立块: %#v", rows)
	}
	titleRow := cardJSON(t, rows[0].(map[string]any))
	if !strings.Contains(titleRow, `\u003ctext_tag color='green'\u003e已发布`) || !strings.Contains(titleRow, `"lines":1`) ||
		!strings.Contains(titleRow, "("+job.ArticleURL+")") || !strings.Contains(titleRow, "首年销量破亿") {
		t.Fatalf("标题行：状态标签 + 链到网站的单行粗体标题: %s", titleRow)
	}
	summary := rows[1].(map[string]any)
	if summary["tag"] != "div" || summary["text"].(map[string]any)["lines"] != 1 || summary["margin"] != reviewBatchSummaryMargin {
		t.Fatalf("摘要行单行截断，间距可调: %#v", summary)
	}
	meta := rows[2].(map[string]any)
	if body := fmt.Sprint(meta["content"]); !strings.Contains(body, "原创") || !strings.Contains(body, "#72") || !strings.Contains(body, "能量饮料") || meta["margin"] != reviewBatchMetaMargin {
		t.Fatalf("信息行：原创/转载 · #任务号 + 标签，间距可调: %#v", meta)
	}
	buttonRow := rows[3].(map[string]any)
	if buttonRow["margin"] != reviewBatchButtonMargin {
		t.Fatalf("按钮行间距可调: %#v", buttonRow)
	}
	var buttons []map[string]any
	reviewCardButtons(buttonRow, &buttons)
	for _, button := range buttons {
		if button["size"] != "tiny" {
			t.Fatalf("按钮一律最小号: %#v", button)
		}
	}
	risk := elements[len(elements)-1].(map[string]any)
	if risk["tag"] != "collapsible_panel" || risk["header"].(map[string]any)["icon_position"] != "follow_text" {
		t.Fatalf("折叠条图标只能跟在标题文字后面: %#v", risk)
	}

	// 无封面旧稿只有文字栏。
	job.ReviewCoverImgKey = ""
	noCover := buildReviewBatchCard([]publishJob{job})["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)
	noCoverInner := noCover["elements"].([]any)[0].(map[string]any)
	if cols := noCoverInner["columns"].([]any); len(cols) != 1 || strings.Contains(cardJSON(t, noCover), `"tag":"img"`) {
		t.Fatalf("无封面时只有文字栏: %#v", noCover)
	}
}

// TestReviewBatchCoverRadiusIsConcentric 同心圆角（用户 2026-10-10）：封面圆角 = 外框圆角 − 内边距，
// 两条弧线共用圆心，间距处处均匀。
func TestReviewBatchCoverRadiusIsConcentric(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	block := reviewBatchArticleBlock(job)
	wantPadding := fmt.Sprintf("%[1]dpx %[1]dpx %[1]dpx %[1]dpx", reviewBatchBlockPadding)
	if block["padding"] != wantPadding || block["corner_radius"] != fmt.Sprintf("%dpx", reviewBatchBlockRadius) {
		t.Fatalf("外框圆角与内边距应取常量: %#v", block)
	}
	cover := reviewBatchCover(job)
	if cover["corner_radius"] != fmt.Sprintf("%dpx", reviewBatchBlockRadius-reviewBatchBlockPadding) {
		t.Fatalf("封面圆角应为外框圆角减内边距: %#v", cover)
	}
}

// TestReviewBatchRightColumnHasFixedHeight 右栏高度固定才能上下都对齐封面：会变的内容不另起
// 一行——重新同步结果跟在按钮后面，原创稿的外部分发按钮并入操作组。
func TestReviewBatchRightColumnHasFixedHeight(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.SourceResyncState = publishSourceResyncSucceeded
	job.SourceResyncCompletedAt = "2026-09-01T06:30:00Z"
	t.Setenv(yunsuoTargetAccountsEnv, `[{"account_id":"a1","name":"头条号"}]`)
	block := buildReviewBatchCard([]publishJob{job})["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)
	inner := block["elements"].([]any)[0].(map[string]any)
	rows := inner["columns"].([]any)[1].(map[string]any)["elements"].([]any)
	if len(rows) != 4 {
		t.Fatalf("有同步结果、可外部分发时右栏仍只有四行: %s", cardJSON(t, inner))
	}
	if last := cardJSON(t, rows[3].(map[string]any)); !strings.Contains(last, "同步成功") {
		t.Fatalf("同步结果应在按钮行里: %s", last)
	}
}

// TestReviewBatchSourceButtonSaysWhichOriginal 只留一个查看按钮（用户 2026-10-10：「网站这个
// 按钮去掉，保留微信的，改成公众号原文」）：公众号链接叫「公众号原文」并用微信网站图标，其他网页
// 叫「原网页」，缺来源链接不出；网站文章由标题链接打开。
func TestReviewBatchSourceButtonSaysWhichOriginal(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	for _, tc := range []struct{ sourceURL, want string }{
		{"https://mp.weixin.qq.com/s/abc", "公众号原文/wechat-favicon"},
		{"https://www.example.com/news/1", "原网页/external-link"},
		{"", ""},
	} {
		job.SourceURL = tc.sourceURL
		got := ""
		if button := reviewBatchSourceButton(job); button != nil {
			got = fmt.Sprintf("%v/%v", button["text"].(map[string]any)["content"], button[reviewCardIconField])
		}
		if got != tc.want {
			t.Fatalf("来源 %q 的按钮应为 %q，实际 %q", tc.sourceURL, tc.want, got)
		}
	}
	body := cardJSON(t, buildReviewBatchCard([]publishJob{reviewCardTestJob(publishStagePublished)}))
	if strings.Contains(body, "查看原文") || strings.Contains(body, `"content":"网站"`) || strings.Contains(body, `"content":"预览"`) {
		t.Fatalf("聚合卡不应再有「查看原文」「网站」「预览」按钮: %s", body)
	}
}

// panelJSON 折叠区序列化后把 json 转义的 < > & 还原，断言可以直接写 text_tag 标记。
func panelJSON(t *testing.T, panel map[string]any) string {
	t.Helper()
	return strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(cardJSON(t, panel))
}

// reviewCardTitleRowHasButton 折叠区里有一行：左栏是以 titlePrefix 开头的组标题，右栏是 label 按钮。
func reviewCardTitleRowHasButton(panel map[string]any, titlePrefix, label string) bool {
	found := false
	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if columns, ok := value["columns"].([]any); ok && len(columns) == 2 {
				left := fmt.Sprint(columns[0])
				right, _ := columns[1].(map[string]any)
				if strings.Contains(left, titlePrefix) && strings.Contains(fmt.Sprint(right["elements"]), label) {
					found = true
				}
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(panel)
	return found
}

// reviewCardFindPanel 取卡片里标题以 prefix 开头的折叠面板。
func reviewCardFindPanel(card map[string]any, prefix string) map[string]any {
	var found map[string]any
	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if value["tag"] == "collapsible_panel" && found == nil {
				header, _ := value["header"].(map[string]any)
				title, _ := header["title"].(map[string]any)
				if content, _ := title["content"].(string); strings.HasPrefix(content, prefix) {
					found = value
					return
				}
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(card)
	return found
}

// TestReviewCardDistributionPanelFoldsPlatforms 多平台分发收进一个折叠区（2026-10-12 用户裁定：
// 「按钮可能太多……应该要把这些按钮折叠起来……点上去展开一个列表，显示它的标题那些东西」）：
// 折叠标题带汇总；展开后各平台一行，状态 + 平台上的标题链接；分发按钮只在折叠区里，
// 文章按钮行不再放；转载稿聚合卡不出折叠区。
func TestReviewCardDistributionPanelFoldsPlatforms(t *testing.T) {
	setTestYunsuoConfig(t)
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish

	// 还没分发：两个平台都「未同步」，按钮在折叠区里。
	card := buildReviewBatchCard([]publishJob{job})
	panel := reviewCardFindPanel(card, "多平台分发")
	if panel == nil || panel["expanded"] != false {
		t.Fatalf("原创已发布稿应有收起的多平台分发折叠区: %s", cardJSON(t, card))
	}
	body := panelJSON(t, panel)
	if !strings.Contains(body, "多平台分发（头条号未同步 · 百家号未同步）") || strings.Count(body, "<font color='grey'>未同步</font>") != 2 || !strings.Contains(body, `"content":"同步至头条号、百家号"`) {
		t.Fatalf("未分发时应列出两个平台并给分发按钮: %s", body)
	}
	// 版式照渠道列表先例：状态是靠右的彩色文字，不用胶囊标签、不用分隔线；按钮在组标题右侧。
	if strings.Contains(body, "text_tag") || strings.Contains(body, `"tag":"hr"`) || strings.Count(body, `"text_align":"right"`) != 2 {
		t.Fatalf("状态应为靠右文字，不用胶囊和分隔线: %s", body)
	}
	if !reviewCardTitleRowHasButton(panel, "**#72 ", "同步至头条号、百家号") {
		t.Fatalf("分发按钮应在「#72 标题」同一行右侧: %s", body)
	}
	var outside []map[string]any
	reviewCardButtons(map[string]any{"elements": card["body"].(map[string]any)["elements"].([]any)[0]}, &outside)
	for _, button := range outside {
		if strings.Contains(fmt.Sprint(button["text"]), "分发") {
			t.Fatalf("文章按钮行不应再放分发按钮: %#v", button)
		}
	}

	// 头条号已发、查到链接；百家号云梭受理后失败。
	toutiao := testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded)
	toutiao.Title, toutiao.URL = "这个女性能量饮料，靠青汁粉走红了", "https://www.toutiao.com/article/7560000000000000000/"
	baijia := testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionFailed)
	baijia.ContentPK, baijia.Remark = "pk-1", "授权过期"
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, baijia}
	body = panelJSON(t, reviewCardFindPanel(buildReviewBatchCard([]publishJob{job}), "多平台分发"))
	for _, want := range []string{"多平台分发（头条号已发布 · 百家号失败）", "<font color='green'>已发布</font>", "头条号　[这个女性能量饮料，靠青汁粉走红了](https://www.toutiao.com/article/7560000000000000000/)", "<font color='red'>失败</font>", "<font color='grey'>账号登录失效，在云梭插件更新登录后云梭会自动重试</font>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("折叠区缺少 %q: %s", want, body)
		}
	}
	if strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("云梭已受理的失败不得给重推按钮: %s", body)
	}
	// 平台标题与本文相同：不重复整篇标题，链接文字用「查看文章」。
	toutiao.Title = job.Title
	baijia.Title = job.Title
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, baijia}
	body = panelJSON(t, reviewCardFindPanel(buildReviewBatchCard([]publishJob{job}), "多平台分发"))
	if !strings.Contains(body, "头条号　[查看文章](https://www.toutiao.com/article/7560000000000000000/)") || strings.Count(body, job.Title) != 1 {
		t.Fatalf("同标题时平台行不应重复标题（只在组标题出现一次）: %s", body)
	}
	// 多篇：每节先写「#编号 标题」，与风险核查区一致。
	second := reviewCardTestJob(publishStagePublished)
	second.ID, second.Title = 73, "第二篇原创文章"
	body = panelJSON(t, reviewCardFindPanel(buildReviewBatchCard([]publishJob{job, second}), "多平台分发"))
	if !strings.Contains(body, "**#72 ") || !strings.Contains(body, "**#73 第二篇原创文章**") || !strings.Contains(body, "多平台分发（头条号 1/2 · 百家号 0/2）") {
		t.Fatalf("多篇时每节应有编号标题、汇总按平台行数: %s", body)
	}

	// 百家号受理前失败：可安全重推，只重推它。
	baijia = testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionFailed)
	baijia.Error = "封面图尺寸过小"
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, baijia}
	body = panelJSON(t, reviewCardFindPanel(buildReviewBatchCard([]publishJob{job}), "多平台分发"))
	if !strings.Contains(body, "<font color='grey'>封面图太小，换封面后重新同步</font>") || !strings.Contains(body, `"content":"重新同步至百家号"`) || !strings.Contains(body, "重新同步至百家号？") {
		t.Fatalf("受理前失败应给只推百家号的重试按钮: %s", body)
	}

	// 同步中：折叠标题直接说「同步中」，不给按钮。
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionPolling)}
	body = panelJSON(t, reviewCardFindPanel(buildReviewBatchCard([]publishJob{job}), "多平台分发"))
	if !strings.Contains(body, "多平台分发（同步中）") || strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("同步中不给按钮: %s", body)
	}

	// 转载稿：聚合卡不出折叠区；单篇卡留一句说明。
	repost := reviewCardTestJob(publishStagePublished)
	repost.Author, repost.SourceName = "某外部作者", "第一财经"
	repost.BlocksJSON = `[{"k":"p","t":"本文转载自第一财经"}]`
	if panel := reviewCardFindPanel(buildReviewBatchCard([]publishJob{repost}), "多平台分发"); panel != nil {
		t.Fatalf("转载稿聚合卡不应出现分发折叠区: %s", cardJSON(t, panel))
	}
	if body := cardJSON(t, buildReviewDoneCard(repost)); !strings.Contains(body, "仅发布至 FoodTalks") {
		t.Fatalf("单篇卡应说明转载稿不外发: %s", body)
	}

	// 有图标时平台行前是平台自己的网站图标，按钮带分享图标；单篇卡不走图标替换，绝不能留内部标记。
	job.ExternalDistributionLanes = []publishExternalLane{toutiao}
	icons := reviewCardIcons{"platform-toutiao": "img_toutiao", "platform-baijiahao": "img_baijia", "share-2-default": "img_share"}
	body = panelJSON(t, reviewCardFindPanel(buildReviewBatchCardWithIcons([]publishJob{job}, icons), "多平台分发"))
	for _, want := range []string{`"img_key":"img_toutiao"`, `"img_key":"img_baijia"`, `"img_key":"img_share"`, `"size":"16px 16px"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少 %s: %s", want, body)
		}
	}
	if body := cardJSON(t, buildReviewDoneCard(job)); strings.Contains(body, reviewCardIconField) || !strings.Contains(body, "同步至百家号") {
		t.Fatalf("单篇卡应有分发按钮且不得残留图标标记: %s", body)
	}
	if body := panelJSON(t, reviewCardFindPanel(buildReviewBatchCard([]publishJob{job}), "多平台分发")); strings.Contains(body, `"tag":"img"`) || strings.Contains(body, reviewCardIconField) {
		t.Fatalf("没有图标时不放空图、不留标记: %s", body)
	}
}

// TestReviewBatchCardAllowsResyncAfterDistribution 已外部分发的稿子照样能重新同步到网站：重新
// 同步只影响 FoodTalks 网站稿，外部平台对不上就对不上（2026-10-10 用户裁定，取消原先的限制）。
func TestReviewBatchCardAllowsResyncAfterDistribution(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.ExternalDistributionLanes = []publishExternalLane{testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded)}
	var buttons []map[string]any
	reviewCardButtons(buildReviewBatchCard([]publishJob{job}), &buttons)
	for _, button := range buttons {
		if button["text"].(map[string]any)["content"] != "重新同步至网站" {
			continue
		}
		if button["disabled"] == true || !strings.Contains(fmt.Sprint(button["behaviors"]), feishuCardActionResyncSource) {
			t.Fatalf("已分发稿的重新同步按钮应可点: %#v", button)
		}
		if err := sourceResyncEligibility(job); err != nil && strings.Contains(err.Error(), "外部平台") {
			t.Fatalf("服务端不得再以外部分发为由拒绝重新同步: %v", err)
		}
		return
	}
	t.Fatalf("已分发稿也要有重新同步按钮: %#v", buttons)
}

// reviewCardActionButtons 只取状态操作按钮，不含图片下方打开链接的查看按钮。
func reviewCardActionButtons(card map[string]any) []map[string]any {
	var all, out []map[string]any
	reviewCardButtons(card, &all)
	for _, button := range all {
		if !strings.Contains(fmt.Sprint(button["behaviors"]), "open_url") {
			out = append(out, button)
		}
	}
	return out
}

// TestReviewBatchCardButtonOrderAndSubtitle 按钮先操作、后查看：发布 → 预览 → 公众号；
// 重新同步至网站 → 下线 → 网站 → 公众号。全批同一状态时副标题只说一次数；无封面旧稿只剩文字栏。
func TestReviewBatchCardButtonOrderAndSubtitle(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	now := time.Now().UTC()
	pending := reviewCardTestJob(publishStageAwaitingReview)
	pending.CreatedAt = "2026-08-20T01:00:00Z"
	pending.RiskLevel = publishRiskPass
	body := cardJSON(t, buildReviewBatchCard([]publishJob{pending}))
	if !strings.Contains(body, "8月20日 · 待审核1篇") || strings.Contains(body, "1篇文章") {
		t.Fatalf("全批待审核时副标题不应重复总数: %s", body)
	}
	var all []map[string]any
	reviewCardButtons(buildReviewBatchCard([]publishJob{pending}), &all)
	order := []string{}
	for _, button := range all {
		order = append(order, fmt.Sprint(button["text"].(map[string]any)["content"]))
	}
	if strings.Join(order, " ") != "发布 公众号原文" || all[0]["type"] != "primary_filled" {
		t.Fatalf("待审核稿按钮应为发布、公众号原文，实际 %v", order)
	}
	buttons := reviewCardActionButtons(buildReviewBatchCard([]publishJob{pending}))
	if len(buttons) != 1 {
		t.Fatalf("待审核稿只有一枚操作按钮: %#v", buttons)
	}

	published := reviewCardTestJob(publishStagePublished)
	published.CreatedAt = pending.CreatedAt
	published.FoodTalksNewsID = 7318
	published.PublicationAction = publishPublicationActionPublish
	published.PublishedAt = now.Add(-time.Hour).Format(time.RFC3339)
	published.UnpublishPolicySet = true
	published.UnpublishWindowHours = 8
	published.UnpublishWindowUntil = now.Add(time.Hour).Format(time.RFC3339)
	published.ReviewCoverImgKey = ""
	body = cardJSON(t, buildReviewBatchCard([]publishJob{published}))
	var publishedButtons []map[string]any
	reviewCardButtons(buildReviewBatchCard([]publishJob{published}), &publishedButtons)
	publishedOrder := []string{}
	for _, button := range publishedButtons {
		publishedOrder = append(publishedOrder, fmt.Sprint(button["text"].(map[string]any)["content"]))
	}
	if strings.Join(publishedOrder, " ") != "公众号原文 下线 重新同步至网站" {
		t.Fatalf("已发布稿按钮应为公众号原文、下线、重新同步至网站（最右），实际 %v", publishedOrder)
	}
	if !strings.Contains(body, "8月20日 · 已发布1篇") {
		t.Fatalf("全批已发布时副标题应为「已发布N篇」: %s", body)
	}
	if strings.Contains(body, `"tag":"img"`) || !strings.Contains(body, "首年销量破亿") {
		t.Fatalf("无封面旧稿应只渲染文字列: %s", body)
	}

	// 只表示状态、点不了的按钮不出（状态标签已说明）；没有可点操作就没有按钮行。
	for _, stage := range []string{publishStagePublicizing, publishStageUnpublishing, publishStageRejected, publishStageReconciliationRequired} {
		job := reviewCardTestJob(stage)
		buttons = reviewCardActionButtons(buildReviewBatchCard([]publishJob{job}))
		if len(buttons) != 0 {
			t.Fatalf("%s 阶段不应出不可点的状态按钮: %#v", stage, buttons)
		}
	}
	unchecked := reviewCardTestJob(publishStageAwaitingReview)
	unchecked.RiskLevel = ""
	buttons = reviewCardActionButtons(buildReviewBatchCard([]publishJob{unchecked}))
	if len(buttons) != 1 || buttons[0]["disabled"] != true || buttons[0]["text"].(map[string]any)["content"] != "检查未完成" {
		t.Fatalf("风险检查没跑完时保留置灰的「检查未完成」说明为什么不能发布: %#v", buttons)
	}
}

// TestPublishCardRefreshReissuesCurrentCard 卡片改版后要能把历史任务的卡重出一遍：
// 终态任务可刷新（走已配置的投递通道，有 message_id 的群原位更新，不刷屏），
// 尚未产生审核卡的阶段必须明确拒绝，而不是发一张语义不明的卡。
func TestPublishCardRefreshReissuesCurrentCard(t *testing.T) {
	env := newPublishTestEnv(t)
	env.ft.forbidPublish = false
	mux := newPublishTestMux(t, env)

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_reviewer"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := len(env.notified)

	rec := doPublishReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/card-refresh", job.ID), "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("已发布任务应可刷新卡片，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if len(env.notified) <= before {
		t.Fatalf("刷新应重新投递一次卡片（此环境降级为文本），notified=%d→%d", before, len(env.notified))
	}

	missing := doPublishReq(mux, http.MethodPost, "/api/publish/jobs/999999/card-refresh", "", true)
	if missing.Code == http.StatusOK {
		t.Fatalf("不存在的任务不应返回成功: body=%s", missing.Body.String())
	}
}

// TestPublishCardRefreshRequiresAdmin 刷新会往审核群发消息，必须在管理员门后。
func TestPublishCardRefreshRequiresAdmin(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/jobs/1/card-refresh", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无凭证应 401，实际 %d", rec.Code)
	}
	if rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/jobs/1/card-refresh", ""); rec.Code == http.StatusOK {
		t.Fatalf("同步服务凭证不得刷新卡片，实际 %d", rec.Code)
	}
}
