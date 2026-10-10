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
	// 下线窗口已过：不再出置灰的「不可下线」；按钮先操作、后查看：重新同步至网站 → 网站 → 公众号。
	resyncAt := strings.Index(body, "重新同步至网站")
	siteAt := strings.Index(body, `"content":"网站"`)
	sourceAt := strings.Index(body, `"content":"公众号"`)
	if resyncAt < 0 || siteAt < resyncAt || sourceAt < siteAt || strings.Contains(body, "不可下线") {
		t.Fatalf("按钮应为重新同步至网站、网站、公众号，且不出不可下线: %s", body)
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
	// 同步结果在小字区另起一行（「转载 · #编号」下面、按钮上面；用户 2026-10-10）。
	body := cardJSON(t, buildReviewBatchCard([]publishJob{job}))
	metaAt := strings.Index(body, "#72")
	successAt := strings.Index(body, "同步成功")
	buttonAt := strings.Index(body, `"content":"重新同步"`)
	if metaAt < 0 || successAt < metaAt || buttonAt < successAt || !strings.Contains(body, "color='green'") {
		t.Fatalf("同步成功提示应以绿色显示在小字区、按钮上方: %s", body)
	}

	job.SourceResyncState = publishSourceResyncFailed
	job.SourceResyncError = "公众号正文抓取失败"
	body = cardJSON(t, buildReviewBatchCard([]publishJob{job}))
	buttonAt = strings.Index(body, `"content":"重新同步"`)
	failureAt := strings.Index(body, "同步失败")
	if buttonAt < 0 || failureAt < 0 || buttonAt < failureAt || !strings.Contains(body, "color='red'") ||
		!strings.Contains(body, job.SourceResyncError) {
		t.Fatalf("同步失败提示应以红色显示在小字区、按钮上方并给出原因: %s", body)
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
		"FoodTalks 内容审核", "8月20日 · 2篇文章 · 待审核1篇 · 已发布1篇",
		`"scale_type":"fit_horizontal"`, `"background_style":"grey-50"`, "#117", "#118",
		`\u003ctext_tag color='orange'\u003e待审核\u003c/text_tag\u003e`, `\u003ctext_tag color='green'\u003e已发布\u003c/text_tag\u003e`,
		`\u003ctext_tag color='blue'\u003e能量饮料\u003c/text_tag\u003e \u003ctext_tag color='blue'\u003e女性消费`,
		`"content":"公众号"`, `"content":"网站"`, `"content":"预览"`, `"action":"approve"`, `"action":"unpublish"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("聚合卡缺少 %q: %s", want, body)
		}
	}
	if strings.Count(body, `"schema":"2.0"`) != 1 {
		t.Fatalf("同批文章必须共用一张卡: %s", body)
	}
	// 2026-10-10 定稿 S2：每篇一个浅灰块（只此一层，不再灰块套灰块），篇间靠间距不画线；
	// 按钮一律最小号、按内容宽度，不撑满。
	for _, unwanted := range []string{`"grey-100"`, `"tag":"hr"`, `"width":"fill"`, `\u003ctext_tag color='neutral'\u003e转载`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("聚合卡不应再出现 %s: %s", unwanted, body)
		}
	}
	if strings.Count(body, `"background_style":"grey-50"`) != 2 {
		t.Fatalf("两篇各一个浅灰块: %s", body)
	}
	if strings.Count(body, `"tag":"collapsible_panel"`) != 1 ||
		strings.Count(body, "风险核查（1篇有提示）") != 1 ||
		strings.Index(body, `"action":"approve"`) > strings.Index(body, "风险核查") ||
		!strings.Contains(body, "已确认的事实错误") || strings.Contains(body, "需人工确认转载授权") {
		t.Fatalf("风险核查必须只在全部文章之后出现一次: %s", body)
	}
}

// TestReviewBatchCardKeepsOneLeftEdge 2026-10-10 定稿 S2（用户「这个感觉对了」）：每篇一个
// 浅灰块，左栏 1/4 宽是铺满栏宽的方形封面（随卡片宽度自适应），右栏 3/4 依次是「［状态］
// **标题**」、灰色摘要、「转载 · #任务号」+ 蓝色标签、一排最小号按钮。头部不带图标，
// 正文 markdown 不带前缀 icon，折叠条箭头跟在标题后（用户「极其极其讨厌对不齐」）。
func TestReviewBatchCardKeepsOneLeftEdge(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.RiskItems = []publishRiskItem{{Category: "factual", Reason: "已确认的事实错误", Certainty: publishRiskCertaintyConfirmed}}
	card := buildReviewBatchCard([]publishJob{job})
	if _, hasIcon := card["header"].(map[string]any)["icon"]; hasIcon {
		t.Fatalf("头部图标会把标题推离正文左边线: %#v", card["header"])
	}
	elements := card["body"].(map[string]any)["elements"].([]any)
	article := elements[0].(map[string]any)
	if article["background_style"] != "grey-50" {
		t.Fatalf("每篇是一个浅灰块: %#v", article)
	}
	inner := article["columns"].([]any)[0].(map[string]any)["elements"].([]any)[0].(map[string]any)
	columns := inner["columns"].([]any)
	if inner["flex_mode"] != "none" || len(columns) != 2 {
		t.Fatalf("块内左图右文: %#v", inner)
	}
	coverColumn, textColumn := columns[0].(map[string]any), columns[1].(map[string]any)
	if coverColumn["width"] != "weighted" || coverColumn["weight"] != 1 || textColumn["width"] != "weighted" || textColumn["weight"] != 3 {
		t.Fatalf("图文按 1:3 随卡片宽度分配: %#v | %#v", coverColumn, textColumn)
	}
	cover := coverColumn["elements"].([]any)[0].(map[string]any)
	if cover["tag"] != "img" || cover["scale_type"] != "fit_horizontal" || cover["size"] != nil {
		t.Fatalf("方形封面铺满左栏宽度、不裁切: %#v", cover)
	}
	text := textColumn["elements"].([]any)
	head := fmt.Sprint(text[0].(map[string]any)["content"])
	if !strings.HasPrefix(head, "<text_tag color='green'>已发布</text_tag> **[") || !strings.Contains(head, "首年销量破亿") ||
		!strings.Contains(head, "("+job.ArticleURL+")") {
		t.Fatalf("第一行是状态标签加链到网站文章的粗体标题: %q", head)
	}
	if meta := fmt.Sprint(text[2].(map[string]any)["content"]); !strings.Contains(meta, "原创") || !strings.Contains(meta, "#72") || !strings.Contains(meta, "能量饮料") {
		t.Fatalf("第三行是「原创/转载 · #任务号」加标签: %q", meta)
	}
	var buttons []map[string]any
	reviewCardButtons(textColumn, &buttons)
	for _, button := range buttons {
		if button["size"] != "tiny" {
			t.Fatalf("按钮一律最小号: %#v", button)
		}
	}
	for _, element := range text {
		if element.(map[string]any)["tag"] == "markdown" {
			if _, hasIcon := element.(map[string]any)["icon"]; hasIcon {
				t.Fatalf("正文行不得带前缀图标: %#v", element)
			}
		}
	}
	risk := elements[len(elements)-1].(map[string]any)
	if risk["tag"] != "collapsible_panel" || risk["header"].(map[string]any)["icon_position"] != "follow_text" {
		t.Fatalf("折叠条图标只能跟在标题文字后面: %#v", risk)
	}

	// 无封面旧稿只有文字栏。
	job.ReviewCoverImgKey = ""
	noCover := buildReviewBatchCard([]publishJob{job})["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)
	noCoverInner := noCover["columns"].([]any)[0].(map[string]any)["elements"].([]any)[0].(map[string]any)
	if cols := noCoverInner["columns"].([]any); len(cols) != 1 || strings.Contains(cardJSON(t, noCover), `"tag":"img"`) {
		t.Fatalf("无封面时只有文字栏: %#v", noCover)
	}
}

// TestReviewBatchButtonsSitOnCoverBottom 按钮行底边对齐封面底边（用户 2026-10-10）：
// 封面栏和文字栏都贴底，按钮是文字栏最后一行（原创稿的外部分发按钮除外）。
func TestReviewBatchButtonsSitOnCoverBottom(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.SourceURL = "https://mp.weixin.qq.com/s/abc"
	block := buildReviewBatchCard([]publishJob{job})["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)
	inner := block["columns"].([]any)[0].(map[string]any)["elements"].([]any)[0].(map[string]any)
	for _, col := range inner["columns"].([]any) {
		if col.(map[string]any)["vertical_align"] != "bottom" {
			t.Fatalf("封面栏和文字栏都要贴底: %#v", col)
		}
	}
	text := inner["columns"].([]any)[1].(map[string]any)["elements"].([]any)
	if last := cardJSON(t, text[len(text)-1].(map[string]any)); !strings.Contains(last, `"tag":"button"`) {
		t.Fatalf("按钮应是文字栏最后一行: %s", last)
	}
}

// TestReviewBatchViewButtonsSayWhereTheyGo「查看原文」分不清网站还是公众号（用户 2026-10-09）。
// 查看按钮名字说清去哪：网站文章已发布叫「网站」、未发布叫「预览」；来源是公众号叫「公众号」、
// 其他网页叫「原网页」、缺来源链接不出。图标用两个网站自己的网站图标（用户 2026-10-10）。
func TestReviewBatchViewButtonsSayWhereTheyGo(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	labels := func(job publishJob) string {
		out := []string{}
		for _, button := range reviewBatchViewButtons(job) {
			out = append(out, fmt.Sprintf("%v/%v", button["text"].(map[string]any)["content"], button[reviewCardIconField]))
		}
		return strings.Join(out, " ")
	}
	job := reviewCardTestJob(publishStagePublished)
	for _, tc := range []struct{ sourceURL, want string }{
		{"https://mp.weixin.qq.com/s/abc", "网站/foodtalks-favicon 公众号/wechat-favicon"},
		{"https://www.example.com/news/1", "网站/foodtalks-favicon 原网页/external-link"},
		{"", "网站/foodtalks-favicon"},
	} {
		job.SourceURL = tc.sourceURL
		if got := labels(job); got != tc.want {
			t.Fatalf("来源 %q 的查看按钮应为 %q，实际 %q", tc.sourceURL, tc.want, got)
		}
	}
	if got := labels(reviewCardTestJob(publishStageAwaitingReview)); got != "预览/foodtalks-favicon 公众号/wechat-favicon" {
		t.Fatalf("待审核稿应为预览、公众号，实际 %q", got)
	}
	if body := cardJSON(t, buildReviewBatchCard([]publishJob{reviewCardTestJob(publishStagePublished)})); strings.Contains(body, "查看原文") {
		t.Fatalf("聚合卡不应再出现有歧义的「查看原文」: %s", body)
	}
}

// TestReviewBatchCardHidesDistributionNotices 聚合卡不显示任何外部分发文字提示（用户
// 2026-10-09：「只去掉……类似于这种分发提示，因为我们的分发流程还没做太好」）：已同步、
// 失败原因等都不出现；可安全重试时仍留「重新同步分发」按钮。
func TestReviewBatchCardHidesDistributionNotices(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.ExternalDistributionState = publishExternalDistributionSucceeded
	job.ExternalDistributionTargets = []publishExternalDistributionTarget{{Name: "头条号", State: publishExternalTargetSucceeded}}
	if body := cardJSON(t, buildReviewBatchCard([]publishJob{job})); strings.Contains(body, "其他平台分发") || strings.Contains(body, "已同步") {
		t.Fatalf("分发成功不应在聚合卡出现提示: %s", body)
	}
	job.ExternalDistributionState = publishExternalDistributionFailed
	job.ExternalDistributionTargets = []publishExternalDistributionTarget{{Name: "头条号", State: publishExternalTargetFailed, Remark: "授权过期"}}
	if body := cardJSON(t, buildReviewBatchCard([]publishJob{job})); strings.Contains(body, "其他平台分发") || strings.Contains(body, "授权过期") {
		t.Fatalf("分发失败也不在聚合卡出现文字提示: %s", body)
	}
	// 单篇卡（降级路径）仍保留完整分发状态，不受聚合卡取舍影响。
	if body := cardJSON(t, buildReviewDoneCard(job)); !strings.Contains(body, "其他平台分发") {
		t.Fatalf("单篇卡仍应显示分发状态: %s", body)
	}
}

// TestReviewBatchCardLocksResyncAfterDistribution 已分发的稿子服务端不许覆盖；聚合卡
// 不再显示分发提示，所以按钮不能凭空消失，要置灰并说明原因。
func TestReviewBatchCardLocksResyncAfterDistribution(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.ExternalDistributionState = publishExternalDistributionSucceeded
	var buttons []map[string]any
	reviewCardButtons(buildReviewBatchCardWithIcons([]publishJob{job}, reviewCardTestIconKeys(t)), &buttons)
	for _, button := range buttons {
		if button["text"].(map[string]any)["content"] != "重新同步" {
			continue
		}
		if button["disabled"] != true || button["behaviors"] != nil || button["disabled_tips"] == nil ||
			button["icon"].(map[string]any)["img_key"] != "img_lock-disabled" {
			t.Fatalf("已分发稿的重新同步按钮应置灰加锁并说明原因: %#v", button)
		}
		if err := sourceResyncEligibility(job); err == nil || !strings.Contains(err.Error(), "外部平台分发") {
			t.Fatalf("服务端仍须以「已发起外部平台分发」拒绝覆盖，实际 %v", err)
		}
		return
	}
	t.Fatalf("已分发稿也要显示（置灰的）重新同步按钮: %#v", buttons)
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
	if strings.Join(order, " ") != "发布 预览 公众号" || all[0]["type"] != "primary_filled" {
		t.Fatalf("待审核稿按钮应为发布、预览、公众号，实际 %v", order)
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
	resyncAt := strings.Index(body, "重新同步至网站")
	unpublishAt := strings.Index(body, `"action":"unpublish"`)
	siteAt := strings.Index(body, `"content":"网站"`)
	if resyncAt < 0 || unpublishAt < resyncAt || siteAt < unpublishAt {
		t.Fatalf("已发布稿按钮应为重新同步、下线、网站、公众号: %s", body)
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

// TestReviewBatchButtonsWrapAsGroups 手机放不下时按钮整组换行（2026-10-10 用户手机截图：
// 「公众号」被单独挤到第二行）：操作一组、查看一组，组内不折行，两组之间才允许换行。
func TestReviewBatchButtonsWrapAsGroups(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	job := reviewCardTestJob(publishStagePublished)
	job.FoodTalksNewsID = 7318
	job.PublicationAction = publishPublicationActionPublish
	job.PublishedAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	job.UnpublishPolicySet = true
	job.UnpublishWindowHours = 8
	job.UnpublishWindowUntil = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	row := reviewBatchButtonRow(job)
	if row["flex_mode"] != "flow" {
		t.Fatalf("两组之间可换行: %#v", row)
	}
	groups := row["columns"].([]any)
	if len(groups) != 2 {
		t.Fatalf("按钮应分操作、查看两组: %#v", groups)
	}
	labels := func(group any) string {
		inner := group.(map[string]any)["elements"].([]any)[0].(map[string]any)
		if inner["flex_mode"] != "none" {
			t.Fatalf("组内不折行: %#v", inner)
		}
		var buttons []map[string]any
		reviewCardButtons(inner, &buttons)
		out := []string{}
		for _, button := range buttons {
			out = append(out, fmt.Sprint(button["text"].(map[string]any)["content"]))
		}
		return strings.Join(out, " ")
	}
	if got := labels(groups[0]); got != "重新同步 下线" {
		t.Fatalf("操作组应为重新同步、下线，实际 %q", got)
	}
	if got := labels(groups[1]); got != "网站 公众号" {
		t.Fatalf("查看组应为网站、公众号，实际 %q", got)
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
