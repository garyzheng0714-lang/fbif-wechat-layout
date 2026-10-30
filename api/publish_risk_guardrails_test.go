package main

// publish_risk_guardrails_test.go — 风险检查的服务端硬约束（2026-08-08 三方评审后新增）。
//
// 这些约束在 prompt 里都写过一遍，但 prompt 是「请求」，模型不照做时没有兜底。
// 每一条都对应评审指出的一个具体失效模式，全部是纯离线单测、进 CI。

import (
	"strings"
	"testing"
	"time"
)

// 新分类不进解析白名单，模型一输出 naming/number 就会让整份结果解析失败、
// 降级成「风险检查未完成」——比不改还糟。这是改 prompt 的前置代码改动。
func TestParseAcceptsNewAndLegacyCategories(t *testing.T) {
	for _, category := range []string{
		publishRiskCategoryFactual, publishRiskCategoryNaming, publishRiskCategoryNumber,
		publishRiskCategoryIntegrity, publishRiskCategoryCompliance,
		publishRiskCategoryEfficacy, publishRiskCategoryRepublication,
		publishRiskCategoryCompetitor, publishRiskCategoryDataSource,
	} {
		raw := `{"level":"warning","items":[{"category":"` + category +
			`","level":"warning","certainty":"confirmed","evidence":"e","reason":"r"}]}`
		if _, err := parsePublishRiskAssessment(raw); err != nil {
			t.Fatalf("category=%q 应被接受，实际报错：%v", category, err)
		}
	}
	if _, err := parsePublishRiskAssessment(
		`{"level":"warning","items":[{"category":"marketing","level":"warning","evidence":"e","reason":"r"}]}`,
	); err == nil {
		t.Fatal("未知 category 仍应被拒绝，否则白名单形同虚设")
	}
}

// 合规族（含旧四类）永不上首屏：编辑部事前会确认，不占审核人唯一会停下来看的位置。
// 成稿事故同理——确定度高但严重度低。
func TestComplianceAndIntegrityNeverReachFirstScreen(t *testing.T) {
	for _, category := range []string{
		publishRiskCategoryCompliance, publishRiskCategoryEfficacy,
		publishRiskCategoryRepublication, publishRiskCategoryCompetitor,
		publishRiskCategoryDataSource, publishRiskCategoryIntegrity,
		publishRiskCategorySystem,
	} {
		item := publishRiskItem{Category: category, Certainty: publishRiskCertaintyConfirmed}
		if publishRiskItemConfirmed(item) {
			t.Fatalf("category=%q 即使标 confirmed 也不得上首屏", category)
		}
	}
	for _, category := range []string{
		publishRiskCategoryFactual, publishRiskCategoryNaming, publishRiskCategoryNumber,
	} {
		item := publishRiskItem{Category: category, Certainty: publishRiskCertaintyConfirmed}
		if !publishRiskItemConfirmed(item) {
			t.Fatalf("category=%q 的已证实错误必须上首屏", category)
		}
	}
}

func TestCategoryLabelCoversEveryCategory(t *testing.T) {
	for _, category := range []string{
		publishRiskCategoryFactual, publishRiskCategoryNaming, publishRiskCategoryNumber,
		publishRiskCategoryIntegrity, publishRiskCategoryCompliance,
	} {
		if label := publishRiskCategoryLabel(category); label == "其他风险" || label == "" {
			t.Fatalf("category=%q 缺中文标签，卡片会显示成「其他风险」", category)
		}
	}
}

// 模型输出多条合规提示时合并成一条，而不是报错——报错会把整篇打成
// 「风险检查未完成」，代价远大于收益。合并后一律 unverified，但 block 级别保留。
func TestMergeComplianceItemsKeepsAtMostOneAndForcesUnverified(t *testing.T) {
	items := []publishRiskItem{
		{Category: publishRiskCategoryFactual, Certainty: publishRiskCertaintyConfirmed, Reason: "事实错误"},
		{Category: publishRiskCategoryEfficacy, Level: publishRiskWarning, Certainty: publishRiskCertaintyConfirmed, Reason: "功效表述存疑"},
		{Category: publishRiskCategoryDataSource, Level: publishRiskBlock, Certainty: publishRiskCertaintyConfirmed, Reason: "数据无来源"},
		{Category: publishRiskCategoryRepublication, Level: publishRiskWarning, Reason: "授权未确认"},
	}
	merged := mergeComplianceItems(items)
	var compliance []publishRiskItem
	for _, item := range merged {
		if publishRiskComplianceFamily(item.Category) {
			compliance = append(compliance, item)
		}
	}
	if len(compliance) != 1 {
		t.Fatalf("合规族必须收敛成 1 条，实际 %d 条", len(compliance))
	}
	got := compliance[0]
	if got.Certainty != publishRiskCertaintyUnverified {
		t.Fatalf("合并后的合规条目必须是 unverified，实际 %q", got.Certainty)
	}
	if got.Level != publishRiskBlock {
		t.Fatalf("合并后应保留最高级别 block（硬违法要让整张卡变红），实际 %q", got.Level)
	}
	for _, want := range []string{"功效表述存疑", "数据无来源", "授权未确认"} {
		if !strings.Contains(got.Reason, want) {
			t.Fatalf("合并后的理由丢了 %q：%s", want, got.Reason)
		}
	}
	// 非合规条目必须原样保留，不能被合并逻辑吃掉。
	if len(merged) != 2 {
		t.Fatalf("非合规条目应原样保留，合并结果应为 2 条，实际 %d", len(merged))
	}
}

// evidence 编造是最伤信任的失效：审核人按着一句正文里根本没有的话去核对，
// 会当场对整个工具失去信任。匹配不上就降级（fail-soft），不整篇丢弃。
func TestEvidenceGroundingDowngradesFabricatedQuotes(t *testing.T) {
	source := "2008年百利面包糠就成为了北京奥运会指定产品，在2018年的北京冬奥会中也成为了指定食品供应品牌之一。"
	items := []publishRiskItem{
		{Category: publishRiskCategoryFactual, Certainty: publishRiskCertaintyConfirmed,
			Evidence: "在2018年的北京冬奥会中也成为了指定食品供应品牌之一"},
		{Category: publishRiskCategoryNumber, Certainty: publishRiskCertaintyConfirmed,
			Evidence: "公司2025年营收达到惊人的一百二十三亿元人民币"}, // 正文里没有
		{Category: publishRiskCategoryNaming, Certainty: publishRiskCertaintyUnverified,
			Evidence: "本来就不是 confirmed 的条目不受影响"},
	}
	got, downgraded := groundPublishRiskEvidence(items, source)
	if downgraded != 1 {
		t.Fatalf("应降级 1 条编造证据，实际 %d", downgraded)
	}
	if got[0].Certainty != publishRiskCertaintyConfirmed {
		t.Fatal("原文里真实存在的证据不得被误伤")
	}
	if got[1].Certainty != publishRiskCertaintyUnverified {
		t.Fatal("编造的证据必须降级，否则会占据首屏")
	}
}

// 归一要能吃掉模型常见的无害改写：全角数字、中英引号、markdown 强调符、空白。
// 否则真实证据会被误判成编造，比不校验还糟。
func TestEvidenceGroundingToleratesHarmlessRewrites(t *testing.T) {
	source := "入场费约4045.35港元，照此估算本次IPO最高募资约为2亿港元。"
	for _, evidence := range []string{
		"入场费约4045.35港元，照此估算本次IPO最高募资约为2亿港元。",
		"“入场费约4045.35港元，照此估算本次IPO最高募资约为2亿港元。”",
		"入场费约 4045.35 港元，照此估算本次 IPO 最高募资约为 2 亿港元。",
		"**入场费约4045.35港元**，照此估算本次IPO最高募资约为2亿港元。",
	} {
		items := []publishRiskItem{{
			Category:  publishRiskCategoryNumber,
			Certainty: publishRiskCertaintyConfirmed, Evidence: evidence,
		}}
		if _, downgraded := groundPublishRiskEvidence(items, source); downgraded != 0 {
			t.Fatalf("无害改写不该被判成编造：%q", evidence)
		}
	}
}

// 喂给模型的输入必须带日期、分隔标记和全文，缺一个都对应一类已知误报/漏报。
func TestBuildRiskUserInputCarriesDateFullBodyAndFence(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	short := publishJob{Title: "标题", ContentHTML: "<p>短正文</p>"}
	body, msg := buildRiskUserInput(short, now)
	if !strings.Contains(msg, "2026年8月8日") {
		t.Fatalf("必须告知当前日期，否则模型会拿过时知识纠正正确的新事实：%s", msg)
	}
	if !strings.Contains(msg, "<正文开始>") || !strings.Contains(msg, "<正文结束>") {
		t.Fatalf("正文必须有分隔标记（注入防线）：%s", msg)
	}
	if body != "短正文" {
		t.Fatalf("返回的正文应是模型实际看到的那份，实际 %q", body)
	}

	// 线上最长稿 3.8 万字（#321 世界茶叶简史）；原先只送前 8000 字，后半篇的错从没查过。
	tail := "文末这句里有错别字腌采"
	long := publishJob{Title: "标题", ContentHTML: "<p>" + strings.Repeat("正", 40000) + "</p><p>" + tail + "</p>"}
	longBody, longMsg := buildRiskUserInput(long, now)
	if !strings.Contains(longBody, tail) || !strings.Contains(longMsg, tail) {
		t.Fatal("长稿必须全文送给模型，最后一段也要在")
	}
	if strings.Contains(longMsg, "截断") {
		t.Fatalf("正文不截断，不该提截断：%s", longMsg[:200])
	}
}
