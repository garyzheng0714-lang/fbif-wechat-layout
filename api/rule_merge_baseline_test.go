package main

import (
	"regexp"
	"testing"
)

// 契约（架构体检 S3，2026-07-10）：mergeRuleDefaults / mergeRuleBehavior 的合并基线
// 必须按 preset 归属选择——内置 preset 用【自己账号族】的代码内默认，而不是一律
// FBIF 基线。为什么重要：旧实现下任何账号的存量持久化 preset 缺键时都会补进 FBIF
// 的值（页脚、名片、Mote 行为串到别的账号），这是近 3 个月约 8 个渠道串台 fix 的
// 结构性根因；若本测试被改弱，串台会以「莫名其妙的 bug」形态回归。

// WowFood 内置 preset 的存量数据缺 footer_enabled / bottom_blocks 时，
// 必须补 WowFood 自己的默认（底部无内容），而不是 FBIF 的（页脚开、mp v2 区块）。
func TestMergeBaselineWowFoodNotFBIF(t *testing.T) {
	merged := mergeRuleDefaults(wowFoodDefaultRulePresetID, map[string]any{})
	if got := merged["footer_enabled"]; got != false {
		t.Fatalf("WowFood 缺键 footer_enabled 应补自己基线 false，实际 %v（=串到 FBIF）", got)
	}
	mods, _ := merged["modules"].(map[string]any)
	bb, _ := mods["bottom_blocks"].(map[string]any)
	if bb == nil {
		t.Fatal("WowFood merge 后应有显式 bottom_blocks 模块")
	}
	if got := bb["enabled"]; got != false {
		t.Fatalf("WowFood bottom_blocks.enabled 应为自己基线 false，实际 %v（=错渲 FBIF 页脚）", got)
	}
}

// 网站 ft-default 内置 preset 的存量数据 behavior 为空时，必须补网站自己的
// behavior（识别正则/lists），绝不能把 FBIF 的 Mote conditions/actions 深合并进来。
func TestMergeBehaviorBaselineSiteNotFBIF(t *testing.T) {
	merged := mergeRuleBehavior(foodTalksSiteDefaultRulePresetID, nil)
	if _, hasActions := merged["actions"]; hasActions {
		t.Fatalf("ft-default behavior 混入了 FBIF 的 actions（Mote 署名图动作）：%v", merged["actions"])
	}
	site := defaultFoodTalksSiteRuleBehavior()
	if aw, ok := site["author_regex"]; ok {
		if merged["author_regex"] != aw {
			t.Fatalf("ft-default behavior 缺省时应补网站自己的 author_regex，实际 %v", merged["author_regex"])
		}
	}
}

// 自建 preset 必须明确携带 baseline_id，不再根据「非内置 id」猜成 FBIF。
// 缺失/未知基线要在保存边界给人话错误，不能带着不完整配置进存储。
func TestCustomRuleRequiresExplicitKnownBaseline(t *testing.T) {
	for _, tc := range []struct {
		name       string
		baselineID string
	}{
		{name: "missing"},
		{name: "unknown", baselineID: "mystery-channel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRulePresetPayload(rulePreset{
				ID:         "user-custom-123",
				Name:       "自建规则",
				BaselineID: tc.baselineID,
				Config:     map[string]any{"font_size": "16"},
			})
			if err == nil {
				t.Fatal("缺失/未知 baseline_id 必须拒绝保存")
			}
			if !regexp.MustCompile(`(?i)baseline_id|规则基线`).MatchString(err.Error()) {
				t.Fatalf("错误应指明规则基线，实际：%v", err)
			}
		})
	}
}

func TestCustomRuleCanExplicitlyInheritWebsiteBaseline(t *testing.T) {
	merged := mergeRuleDefaults(foodTalksSiteDefaultRulePresetID, map[string]any{"author_font_size": "16"})
	if got := merged["author_font_size"]; got != "16" {
		t.Fatalf("显式覆盖应保留，实际 %v", got)
	}
	behavior := mergeRuleBehavior(foodTalksSiteDefaultRulePresetID, nil)
	if _, hasActions := behavior["actions"]; hasActions {
		t.Fatalf("自建网站规则继承网站基线时不得混入公众号 actions：%v", behavior["actions"])
	}
}

// 内置 preset 自身的完整默认 config 过 merge 必须是恒等操作（关键模块不漂移）。
func TestMergeBaselineIdempotentForBuiltins(t *testing.T) {
	for id, def := range defaultRuleDefinitions("") {
		if len(def.Versions) == 0 {
			continue
		}
		src := def.Versions[0].Config
		merged := mergeRuleDefaults(id, src)
		for _, key := range []string{"footer_enabled", "more_articles_enabled", "skip_upload"} {
			want, inSrc := src[key]
			if !inSrc {
				continue
			}
			if merged[key] != want {
				t.Fatalf("内置 %s 的 %s 过 merge 后漂移：%v -> %v", id, key, want, merged[key])
			}
		}
	}
}
