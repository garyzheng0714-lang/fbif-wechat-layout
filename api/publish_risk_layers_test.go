package main

// publish_risk_layers_test.go — 审核卡两层信息结构（2026-08-08 用户裁定）：
// 首屏只放已证实的实质性问题 + 一句话结论；转载说明、证明不了的存疑项、排版说明
// 沉到折叠的「点此查看完整检查报告」。这层分流一旦退化，审核人会重新被每篇都长
// 一样的日常提示淹没，真正的事实错误反而被埋掉。

import (
	"fmt"
	"strings"
	"testing"
)

func riskLayersJob(level string, items ...publishRiskItem) publishJob {
	return publishJob{
		ID: 80, Stage: publishStageAwaitingReview, Title: "测试标题",
		SourceURL: testPublishSourceURL, RiskLevel: level, RiskItems: items,
	}
}

func cardText(v any) string { return fmt.Sprint(v) }

func TestRiskHeadlineBlockLiftsConfirmedFactualErrorToFirstScreen(t *testing.T) {
	job := riskLayersJob(publishRiskWarning,
		publishRiskItem{
			Category: publishRiskCategoryFactual, Level: publishRiskWarning,
			Certainty: publishRiskCertaintyConfirmed,
			Evidence:  "2018年的北京冬奥会带动了冰雪消费",
			Reason:    "北京冬奥会实际举办时间为2022年",
		},
		publishRiskItem{
			Category: publishRiskCategoryRepublication, Level: publishRiskWarning,
			Certainty: publishRiskCertaintyConfirmed, // 转载授权即使 confirmed 也不上首屏
			Evidence:  "转载自某媒体", Reason: "未确认转载授权",
		},
		publishRiskItem{
			Category: publishRiskCategoryDataSource, Level: publishRiskWarning,
			Certainty: publishRiskCertaintyUnverified,
			Evidence:  "市场规模达 3000 亿元", Reason: "未标注数据来源",
		},
	)
	job.RiskHeadline = "正文把北京冬奥会写成 2018 年举办，实际是 2022 年。"

	head := cardText(reviewCardRiskHeadlineBlock(job))
	if !strings.Contains(head, "需要确认") || !strings.Contains(head, "2022") {
		t.Fatalf("首屏必须直接说清已证实的事实错误：%s", head)
	}
	if !strings.Contains(head, "事实与常识") {
		t.Fatalf("首屏应逐条指认已证实问题：%s", head)
	}
	for _, deferred := range []string{"转载授权", "未确认转载授权", "未标注数据来源"} {
		if strings.Contains(head, deferred) {
			t.Fatalf("首屏不得出现 %q 这类每篇都一样的日常提示：%s", deferred, head)
		}
	}
	// 待核条数由折叠条标题承担，首屏不再复读一遍（同义信息重复是 2026-08-08
	// 真机截图上最刺眼的问题）。
	if strings.Contains(head, "待核提示") {
		t.Fatalf("首屏不得复读折叠条已经说过的待核条数：%s", head)
	}
	// 首屏每一行都必须是独立元素：飞书 markdown 吃掉单个 \n，同段内换行会把
	// 「原句」粘到上一句尾巴上。
	if strings.Contains(head, `\n`) || strings.Contains(head, "\n") {
		t.Fatalf("首屏块内不得用换行符排版，须靠元素间距：%s", head)
	}

	detail := cardText(reviewCardRiskDetailBlock(job)["content"])
	for _, want := range []string{"转载授权", "未确认转载授权", "数据来源", "未标注数据来源"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("完整检查报告缺少 %q：%s", want, detail)
		}
	}
	if title := reviewCardDetailsPanelTitle(job); !strings.Contains(title, "完整检查报告") || !strings.Contains(title, "2 条") {
		t.Fatalf("折叠条应自报里面有多少条待核提示，实际 %q", title)
	}
}

func TestRiskHeadlineNeverExceedsHundredRunes(t *testing.T) {
	long := strings.Repeat("这是一句很长的结论", 40)
	job := riskLayersJob(publishRiskWarning, publishRiskItem{
		Category: publishRiskCategoryFactual, Level: publishRiskWarning,
		Certainty: publishRiskCertaintyConfirmed, Evidence: "原句", Reason: "原因",
	})
	job.RiskHeadline = long
	if n := len([]rune(publishRiskHeadlineOrFallback(job))); n > publishRiskHeadlineMaxRunes {
		t.Fatalf("首屏一句话结论超过 %d 字：%d", publishRiskHeadlineMaxRunes, n)
	}
	parsed, err := parsePublishRiskAssessment(fmt.Sprintf(
		`{"level":"warning","headline":%q,"items":[{"category":"factual","level":"warning","certainty":"confirmed","evidence":"e","reason":"r"}]}`, long))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n := len([]rune(parsed.Headline)); n > publishRiskHeadlineMaxRunes {
		t.Fatalf("解析层未截断 headline：%d 字", n)
	}
}

// 模型漏填 / 填错 certainty 时必须落回 unverified：猜测不能冒充已证实的错误
// 被推到审核人眼前。
func TestUnknownCertaintyFallsBackToUnverified(t *testing.T) {
	parsed, err := parsePublishRiskAssessment(
		`{"level":"warning","items":[{"category":"factual","level":"warning","evidence":"e","reason":"r"},` +
			`{"category":"efficacy","level":"warning","certainty":"maybe","evidence":"e2","reason":"r2"}]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for i, item := range parsed.Items {
		if item.Certainty != publishRiskCertaintyUnverified {
			t.Fatalf("条目 %d 的 certainty 应落回 unverified，实际 %q", i, item.Certainty)
		}
	}
	if confirmed, _ := splitPublishRiskItems(parsed.Items); len(confirmed) != 0 {
		t.Fatalf("未标 confirmed 的条目不得上首屏，实际 %d 条", len(confirmed))
	}
}

// 没有已证实条目时，首屏文案必须如实区分「干净」「有待核提示」「阻断」「没查完」，
// 不许一律显示成绿色通过。
func TestHeadlineBlockDoesNotClaimCleanWhenItIsNot(t *testing.T) {
	deferredItem := publishRiskItem{
		Category: publishRiskCategoryRepublication, Level: publishRiskWarning,
		Certainty: publishRiskCertaintyUnverified, Evidence: "转载自某媒体", Reason: "未确认转载授权",
	}
	cases := []struct {
		name    string
		job     publishJob
		want    string
		notWant []string
	}{
		// 正常态不挂状态标签：标签文字与紧随其后的结论是同一句话，而且和标题下的
		// 蓝色文章标签形状雷同。标签只留给阻断 / 检查未完成这两种真正的异常。
		{"pass 无条目", riskLayersJob(publishRiskPass), "未发现可证实的实质性错误", []string{"需要确认", "text_tag"}},
		{"warning 有待核", riskLayersJob(publishRiskWarning, deferredItem), "未发现可证实的实质性错误",
			// 待核条数由折叠条标题承担，这一行不得复读。
			[]string{"需要确认", "text_tag", "待核提示"}},
		{"block", riskLayersJob(publishRiskBlock, deferredItem), "风险阻断", []string{"未发现"}},
		{"级别未知", riskLayersJob(""), "", nil},
		{"级别非法", riskLayersJob("future_level"), "检查未完成", []string{"检查通过"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := reviewCardRiskHeadlineBlock(tc.job)
			if tc.want == "" {
				if block != nil {
					t.Fatalf("没有风险结论时不应渲染首屏块：%v", block)
				}
				return
			}
			got := cardText(block)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("首屏缺少 %q：%s", tc.want, got)
			}
			for _, bad := range tc.notWant {
				if strings.Contains(got, bad) {
					t.Fatalf("首屏不得出现 %q：%s", bad, got)
				}
			}
		})
	}
}

// 风险条目一律用飞书官方无序列表，且项内换行必须是 <br>。
// 官方文档明说单个 \n 是软换行、渲染时可能被忽略——实测就是被忽略的，
// 「原句」会直接粘到上一句尾巴上（2026-08-08 真机截图，同一个坑踩过两次）。
func TestRiskItemListUsesOfficialBulletsAndHardBreaks(t *testing.T) {
	list := reviewCardRiskItemList([]publishRiskItem{
		{Category: publishRiskCategoryFactual, Evidence: "原句甲", Reason: "原因甲"},
		{Category: publishRiskCategoryEfficacy, Evidence: "原句乙", Reason: "原因乙"},
	}, 120, 100)
	if !strings.HasPrefix(list, "- ") || strings.Count(list, "\n- ") != 1 {
		t.Fatalf("条目必须是官方无序列表、项间用 \\n 分隔：%q", list)
	}
	if strings.Count(list, "<br>") != 2 {
		t.Fatalf("每条的原句必须用 <br> 留在列表项内：%q", list)
	}
	for _, line := range strings.Split(list, "\n") {
		if idx := strings.Index(line, "原句"); idx >= 0 && !strings.Contains(line[:idx], "<br>") {
			t.Fatalf("原句与结论之间缺少 <br>，会粘成一句话：%q", line)
		}
	}
}

func TestPublishRiskPromptRequiresFactualCheckAndCertainty(t *testing.T) {
	prompt := publishRiskSystemPrompt()
	for _, want := range []string{
		"factual", "事实与常识", "北京冬奥会", "certainty", "confirmed", "unverified",
		"headline", "100 个字", "拿不准一律填 unverified",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("风险 prompt 缺少 %q", want)
		}
	}
}
