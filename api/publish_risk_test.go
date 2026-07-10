package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePublishRiskAssessmentNormalizesHighestLevel(t *testing.T) {
	raw := `{
		"level":"warning",
		"items":[
			{"category":"data_source","level":"warning","evidence":"销量增长 80%","reason":"正文未注明数据出处"},
			{"category":"efficacy","level":"block","evidence":"三天改善肠胃","reason":"食品功效承诺需要人工确认"}
		],
		"notes":"请核对原始材料"
	}`
	got, err := parsePublishRiskAssessment(raw)
	if err != nil {
		t.Fatalf("parse risk: %v", err)
	}
	if got.Level != publishRiskBlock {
		t.Fatalf("level=%q，应按最高条目提升为 block", got.Level)
	}
	if len(got.Items) != 2 || got.Items[1].Category != publishRiskCategoryEfficacy {
		t.Fatalf("items 未保真：%+v", got.Items)
	}
}

func TestParsePublishRiskAssessmentRejectsUnknownShape(t *testing.T) {
	for _, raw := range []string{
		`{"level":"safe","items":[]}`,
		`{"level":"warning","items":[{"category":"marketing","level":"warning","evidence":"x","reason":"y"}]}`,
		`{"level":"block","items":[{"category":"efficacy","level":"pass","evidence":"x","reason":"y"}]}`,
	} {
		if _, err := parsePublishRiskAssessment(raw); err == nil {
			t.Fatalf("非法风险输出应拒绝：%s", raw)
		}
	}
}

func TestFallbackPublishRiskIsWarningAndKeepsRawEvidence(t *testing.T) {
	got := fallbackPublishRisk("模型返回的原始片段", errors.New("invalid JSON"))
	if got.Level != publishRiskWarning {
		t.Fatalf("fallback level=%q，应为 warning", got.Level)
	}
	if !strings.Contains(got.Raw, "模型返回的原始片段") || !strings.Contains(got.Notes, "invalid JSON") {
		t.Fatalf("fallback 必须保留原始返回和失败原因：%+v", got)
	}
	if len(got.Items) != 1 || got.Items[0].Category != publishRiskCategorySystem {
		t.Fatalf("fallback 应有系统风险条目：%+v", got.Items)
	}
}

func TestPublishRiskPromptCoversFourEditorialRedLines(t *testing.T) {
	prompt := publishRiskSystemPrompt()
	for _, want := range []string{"功效表达", "转载授权", "竞品评价", "数据来源", "只做提示"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("风险提示词缺 %q", want)
		}
	}
}
