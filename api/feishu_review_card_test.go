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
	// 下线窗口已过：不再出置灰的「不可下线」；按钮为原文 → 重新同步至FoodTalks（最右）。
	sourceAt := strings.Index(body, `"content":"原文"`)
	resyncAt := strings.Index(body, `"content":"重新同步至FoodTalks"`)
	if sourceAt < 0 || resyncAt < sourceAt || strings.Contains(body, "不可下线") || strings.Contains(body, `"content":"网站"`) {
		t.Fatalf("按钮应为原文、重新同步至FoodTalks，不出不可下线和网站: %s", body)
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
	// 同步结果在文章块底部单独一行（在按钮之后），见 TestReviewBatchRightColumnHasFixedHeight。
	body := cardJSON(t, buildReviewBatchCard([]publishJob{job}))
	buttonAt := strings.Index(body, `"content":"重新同步至FoodTalks"`)
	successAt := strings.Index(body, "同步成功")
	if buttonAt < 0 || successAt < buttonAt || !strings.Contains(body, "color='green'") {
		t.Fatalf("同步成功提示应以绿色排在按钮之后: %s", body)
	}

	job.SourceResyncState = publishSourceResyncFailed
	job.SourceResyncError = "公众号正文抓取失败"
	body = cardJSON(t, buildReviewBatchCard([]publishJob{job}))
	buttonAt = strings.Index(body, `"content":"重新同步至FoodTalks"`)
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
		`"content":"原文"`, `"action":"approve"`, `"action":"unpublish"`,
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

// TestReviewBatchRightColumnHasFixedHeight 右栏高度固定才能上下都对齐封面：右栏只有四行。重新同步
// 结果在两栏下面单独一行、横跨整个文章块（2026-10-13 用户：「单独一行，覆盖整个卡片宽度的一行，
// 不是图片下的一行、也不是按钮下的一行」）。
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
	if strings.Contains(cardJSON(t, inner), "同步成功") {
		t.Fatalf("同步结果不在两栏里（不在图片下、也不在按钮下）: %s", cardJSON(t, inner))
	}
	children := block["elements"].([]any)
	if len(children) != 2 {
		t.Fatalf("文章块应为两栏 + 同步结果一行: %s", cardJSON(t, block))
	}
	status := children[1].(map[string]any)
	if status["tag"] != "markdown" || !strings.Contains(fmt.Sprint(status["content"]), "同步成功") || status["margin"] != reviewBatchResyncStatusMargin {
		t.Fatalf("同步结果应是文章块里横跨整宽的单独一行: %#v", status)
	}
	job.SourceResyncState = ""
	plain := buildReviewBatchCard([]publishJob{job})["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)
	if len(plain["elements"].([]any)) != 1 {
		t.Fatalf("没同步过就没有这一行: %s", cardJSON(t, plain))
	}
}

// TestReviewBatchSourceButtonSaysWhichOriginal 只留一个查看按钮（用户 2026-10-10：「网站这个
// 按钮去掉，保留微信的，改成公众号原文」；2026-10-13 再改短为「原文」）：公众号链接叫「原文」并用
// 微信网站图标，其他网页叫「原网页」，缺来源链接不出；网站文章由标题链接打开。
func TestReviewBatchSourceButtonSaysWhichOriginal(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	for _, tc := range []struct{ sourceURL, want string }{
		{"https://mp.weixin.qq.com/s/abc", "原文/wechat-favicon"},
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

// distributionSectionJSON 卡片根上「分发到其他平台」区（标题之后的所有根级元素）序列化，
// 把 json 转义的 < > & 还原；没有这一区返回空串。
func distributionSectionJSON(t *testing.T, card map[string]any) string {
	t.Helper()
	var elements []any
	if body, ok := card["body"].(map[string]any); ok {
		elements, _ = body["elements"].([]any)
	}
	for i, element := range elements {
		node, _ := element.(map[string]any)
		if node["tag"] == "markdown" && node["content"] == "**分发到其他平台**" {
			// 2026-10-13 用户：「这根线可以不要」——区标题上方不再有分割线。
			if i > 0 && elements[i-1].(map[string]any)["tag"] == "hr" {
				t.Fatalf("分发区前不应再有分割线: %s", cardJSON(t, card))
			}
			return panelJSON(t, map[string]any{"elements": elements[i+1:]})
		}
	}
	return ""
}

// TestReviewCardDistributionSectionShowsPlatformBlocks 分发区常显在卡片最底下（2026-10-12 用户裁定：
// 「分发区域可以做成一个专门的区域，不用事先隐藏起来……加一个分割线，底部去排版分发的内容」；同日
// 「一般只有一篇是原创……得用一个单独的区域呈现出来」「同一篇文章可能会同时发到……五六家平台……做一个
// 循环容器组件」）：每篇原创一个与文章块同款的细外框块，块里每个平台一行——名字、文章链接、靠右状态，
// 下一行原因；每个平台自己的按钮，只带平台位置不带账号 id；转载稿聚合卡没有这一区。
func TestReviewCardDistributionSectionShowsPlatformBlocks(t *testing.T) {
	setTestYunsuoConfig(t)
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish

	// 还没分发：两个平台各一块，各自一枚按钮；不再有折叠区。
	card := buildReviewBatchCard([]publishJob{job})
	body := distributionSectionJSON(t, card)
	if body == "" || strings.Contains(cardJSON(t, card), "多平台分发") {
		t.Fatalf("原创已发布稿应有常显的分发区: %s", cardJSON(t, card))
	}
	// 两个平台都经自己的分发服务只存草稿（百家号 2026-10-22、头条号 2026-10-24），按钮照实说。
	if strings.Count(body, `"has_border":true`) != 1 || !strings.Contains(body, `"content":"存草稿至头条号"`) || !strings.Contains(body, `"content":"存草稿至百家号"`) {
		t.Fatalf("未分发时一篇一个外框块，块里每个平台一行、各自一枚按钮: %s", body)
	}
	if !strings.Contains(body, `"corner_radius":"16px"`) || !strings.Contains(body, `"border_color":"grey-100"`) {
		t.Fatalf("分发块应与上半部分文章块同款外框: %s", body)
	}
	if !strings.Contains(body, `"slot":0`) || !strings.Contains(body, `"slot":1`) ||
		strings.Contains(cardJSON(t, card), testYunsuoToutiaoID) || strings.Contains(cardJSON(t, card), testYunsuoBaijiaID) {
		t.Fatalf("按钮只带平台位置，卡片里不得出现账号 id: %s", body)
	}
	elements := card["body"].(map[string]any)["elements"].([]any)
	if last := cardJSON(t, elements[len(elements)-1].(map[string]any)); !strings.Contains(last, "存草稿至百家号") {
		t.Fatalf("分发区应在卡片最底下: %s", last)
	}
	var outside []map[string]any
	reviewCardButtons(map[string]any{"elements": elements[0]}, &outside)
	for _, button := range outside {
		if strings.Contains(fmt.Sprint(button["text"]), "存草稿至头条号") {
			t.Fatalf("文章按钮行不应放分发按钮: %#v", button)
		}
	}

	// 头条号已发、查到链接；百家号（改走分发服务之前的云梭 lane，没有 driver）云梭受理后失败。
	toutiao := testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionSucceeded)
	toutiao.Title, toutiao.URL = "这个女性能量饮料，靠青汁粉走红了", "https://www.toutiao.com/article/7560000000000000000/"
	baijia := testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionFailed)
	baijia.ContentPK, baijia.Remark = "pk-1", "授权过期"
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, baijia}
	body = distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job}))
	for _, want := range []string{"<font color='green'>已发布</font>", "[这个女性能量饮料，靠青汁粉走红了](https://www.toutiao.com/article/7560000000000000000/)",
		"**头条号**　[", "<font color='red'>失败</font>",
		"<font color='grey'>账号登录失效，在云梭插件更新登录后云梭会自动重试</font>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("分发区缺少 %q: %s", want, body)
		}
	}
	if strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("云梭已受理的失败不得给重推按钮: %s", body)
	}
	// 平台标题与本文相同：链接文字用「查看文章」。
	toutiao.Title = job.Title
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, baijia}
	body = distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job}))
	if !strings.Contains(body, "[查看文章](https://www.toutiao.com/article/7560000000000000000/)") || strings.Contains(body, job.Title) {
		t.Fatalf("同标题时不重复标题: %s", body)
	}
	// 百家号已发布但还没拿到文章链接（不自动找）：给百家号后台看发布情况（2026-10-13 用户：「百家号的
	// 已发布，我没看到任何的链接。我也查看不了发布情况」）。头条号有链接就不给后台。
	baijia = testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionSucceeded)
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, baijia}
	body = distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job}))
	if !strings.Contains(body, "**百家号**　[去百家号后台查看](https://baijiahao.baidu.com/builder/rc/content)") ||
		strings.Contains(body, "去头条号后台查看") {
		t.Fatalf("已发布没链接的平台应给后台链接，有链接的不给: %s", body)
	}
	// 多篇：每篇原创各一块，块头是这篇的小封面与「#编号 标题」；转载稿不占块。
	second := reviewCardTestJob(publishStagePublished)
	second.ID, second.Title = 73, "第二篇原创文章"
	third := reviewCardTestJob(publishStagePublished)
	third.ID, third.Author, third.SourceName = 74, "某外部作者", "第一财经"
	third.BlocksJSON = `[{"k":"p","t":"本文转载自第一财经"}]`
	body = distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job, second, third}))
	if !strings.Contains(body, `"content":"**#72 `) || !strings.Contains(body, `"content":"**#73 第二篇原创文章**"`) || strings.Contains(body, "#74") {
		t.Fatalf("多篇时每篇原创应有编号标题，转载稿不出现: %s", body)
	}
	if strings.Count(body, `"has_border":true`) != 2 || !strings.Contains(body, `"img_key":"img_v3_cover_key"`) || !strings.Contains(body, `"size":"36px 36px"`) {
		t.Fatalf("两篇原创各一块，块头带同一张封面: %s", body)
	}

	// 百家号（旧的云梭 lane）受理前失败：可安全重推，只重推它；再推走的是现在的分发服务、存草稿。
	baijia = testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionFailed)
	baijia.Error = "封面图尺寸过小"
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, baijia}
	body = distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job}))
	if !strings.Contains(body, "<font color='grey'>封面图太小，换封面后重新同步</font>") || !strings.Contains(body, `"content":"重新存草稿至百家号"`) ||
		!strings.Contains(body, "重新存草稿至百家号？") || strings.Contains(body, `"content":"同步至头条号"`) {
		t.Fatalf("受理前失败应给只推百家号的重试按钮: %s", body)
	}

	// 同步中：不给按钮。
	job.ExternalDistributionLanes = []publishExternalLane{toutiao, testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionPolling)}
	body = distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job}))
	if !strings.Contains(body, "<font color='blue'>同步中</font>") || strings.Contains(body, feishuCardActionDistributeExternal) {
		t.Fatalf("同步中不给按钮: %s", body)
	}

	// 转载稿：聚合卡没有这一区；单篇卡留一句说明。
	repost := reviewCardTestJob(publishStagePublished)
	repost.Author, repost.SourceName = "某外部作者", "第一财经"
	repost.BlocksJSON = `[{"k":"p","t":"本文转载自第一财经"}]`
	if body := distributionSectionJSON(t, buildReviewBatchCard([]publishJob{repost})); body != "" {
		t.Fatalf("转载稿聚合卡不应出现分发区: %s", body)
	}
	if body := cardJSON(t, buildReviewDoneCard(repost)); !strings.Contains(body, "仅发布至 FoodTalks") {
		t.Fatalf("单篇卡应说明转载稿不外发: %s", body)
	}

	// 有图标时平台名前是平台自己的网站图标；单篇卡不走图标替换，绝不能留内部标记。
	job.ExternalDistributionLanes = []publishExternalLane{toutiao}
	icons := reviewCardIcons{"platform-toutiao": "img_toutiao", "platform-baijiahao": "img_baijia"}
	body = distributionSectionJSON(t, buildReviewBatchCardWithIcons([]publishJob{job}, icons))
	for _, want := range []string{`"img_key":"img_toutiao"`, `"img_key":"img_baijia"`, `"size":"16px 16px"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少 %s: %s", want, body)
		}
	}
	if body := cardJSON(t, buildReviewDoneCard(job)); strings.Contains(body, reviewCardIconField) || !strings.Contains(body, "存草稿至百家号") {
		t.Fatalf("单篇卡应有分发按钮且不得残留图标标记: %s", body)
	}
	if body := distributionSectionJSON(t, buildReviewBatchCard([]publishJob{job})); strings.Contains(body, `"tag":"img"`) || strings.Contains(body, reviewCardIconField) {
		t.Fatalf("没有图标时不放空图、不留标记: %s", body)
	}
}

// TestReviewCardDistributionFixForm 待修改的平台直接在卡片里改（2026-10-12 用户裁定：「能否直接在卡片里
// 改好？不过这里一定要做好引导，提示清楚要把什么改成什么」）：写清超了多少、要求与现状，输入框预填
// 建议，封面从正文大图里点选；表单在卡片根上，字段名唯一，回调只带平台位置。
func TestReviewCardDistributionFixForm(t *testing.T) {
	setTestYunsuoConfig(t)
	t.Setenv("AUTH_SESSION_SECRET", "test-session-secret")
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.ReviewDeliveryAppID = "review-app"
	long := "这是一条明显超过了头条号三十个字上限的文章标题，需要编辑缩短以后再同步"
	job.ExternalDistributionDrafts = []publishExternalDraft{{
		AccountID: testYunsuoToutiaoID,
		Issues: []publishExternalIssue{
			{Field: publishExternalIssueTitle, Current: long, Length: runeLen(long), Min: 15, Max: 30},
			{Field: publishExternalIssueCover, Current: "https://cdn.example.com/small.jpg", Width: 300, Height: 200, MinWidth: 452, MinHeight: 352},
		},
		SuggestedTitle: "头条号标题缩短后的建议", SuggestedBy: "ai",
		Candidates: []publishExternalCoverCandidate{
			{URL: "https://cdn.example.com/big.jpg", Width: 1080, Height: 720, ImgKey: "img_big", ImgApp: "review-app"},
			{URL: "https://cdn.example.com/other-app.jpg", Width: 1080, Height: 720, ImgKey: "img_other", ImgApp: "old-app"},
		},
	}}
	card := buildReviewBatchCard([]publishJob{job})
	var form map[string]any
	for _, element := range card["body"].(map[string]any)["elements"].([]any) {
		if node := element.(map[string]any); node["tag"] == "form" {
			form = node
		}
	}
	if form == nil || form["name"] != "fix_72" {
		t.Fatalf("有待修改平台的文章块应包在根级表单 fix_72 里: %s", cardJSON(t, card))
	}
	body := panelJSON(t, form)
	for _, want := range []string{
		"<font color='orange'>待修改</font>",
		fmt.Sprintf("<font color='orange'>标题超出 %d 字</font>：头条号最多 30 字（英文字母、数字两个算一个字），现在 %d 字", runeLen(long)-30, runeLen(long)),
		"现在：" + long,
		// 输入框按字符数限长：全是英文数字时可到 60 个字符；按头条数法超没超由服务端判。
		`"name":"t_72_0"`, `"default_value":"头条号标题缩短后的建议"`, `"max_length":60`, `"placeholder":{"content":"30 字以内"`, `"content":"改成"`,
		"<font color='orange'>封面太小</font>：头条号封面至少 452×352，现在 300×200",
		`"tag":"select_img"`, `"name":"c_72_0"`, `"img_key":"img_big"`, `"value":"` + reviewCardCoverValue("https://cdn.example.com/big.jpg") + `"`,
		`"content":"保存并存草稿至头条号"`, `"form_action_type":"submit"`, `"action":"` + feishuCardActionDistributeFix + `"`, `"slot":0`,
		"在侧边栏修改", "applink.feishu.cn/client/web_url/open?mode=sidebar-semi",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("表单缺少 %q: %s", want, body)
		}
	}
	if strings.Contains(body, "img_other") || strings.Contains(cardJSON(t, card), testYunsuoToutiaoID) {
		t.Fatalf("别的应用传的图不能用，卡片里不得有账号 id: %s", body)
	}
	// 选一张封面，或在侧边栏上传（2026-10-12 用户：「就是“选一张封面”或者“上传”」）。
	if !strings.Contains(body, "从正文里选一张作封面</font><font color='grey'>，或</font>[上传一张](") || strings.Contains(body, "话题") {
		t.Fatalf("封面应是选一张或上传一张: %s", body)
	}
	// 百家号没问题：仍是普通的存草稿按钮。
	if section := distributionSectionJSON(t, card); !strings.Contains(section, `"content":"存草稿至百家号"`) {
		t.Fatalf("百家号应照常给存草稿按钮: %s", section)
	}
	// 下线后不再给改稿表单，只说要改什么。
	job.Stage = publishStageOffline
	job.ExternalDistributionLanes = []publishExternalLane{testLane(testYunsuoBaijiaID, "百家号", publishExternalDistributionSucceeded)}
	if body := cardJSON(t, buildReviewDoneCard(job)); strings.Contains(body, `"tag":"form"`) || !strings.Contains(body, "待修改") {
		t.Fatalf("下线后不得给改稿表单: %s", body)
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
		if button["text"].(map[string]any)["content"] != "重新同步至FoodTalks" {
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

// TestReviewBatchCardButtonOrderAndSubtitle 待审核：发布 → 原文；已发布：原文 →
// 重新同步至FoodTalks → 下线（最右）。全批同一状态时副标题只说一次数；无封面旧稿只剩文字栏。
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
	if strings.Join(order, " ") != "发布 原文" || all[0]["type"] != "primary_filled" {
		t.Fatalf("待审核稿按钮应为发布、原文，实际 %v", order)
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
	if strings.Join(publishedOrder, " ") != "原文 重新同步至FoodTalks 下线" {
		t.Fatalf("已发布稿按钮应为原文、重新同步至FoodTalks、下线（最右），实际 %v", publishedOrder)
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
