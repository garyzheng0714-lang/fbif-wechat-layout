package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// 守护「内置默认 preset 版本化覆盖」根治逻辑（2026-06-02）：
// 历史问题——内置默认 preset 一旦持久化，代码更新永不覆盖（normalizeRuleStoreDocument 旧逻辑
// 只在缺失时填默认），导致 5/26 旧 foodtalks-series-default 一直挡住新规则（#888888 边框、
// 18 群 modules、bd_card）。现以 BuiltinVersion 比较，落后则用代码默认覆盖。

func builtinVersionStoreDoc(rule ruleDefinition) ruleStoreDocumentV2 {
	return ruleStoreDocumentV2{
		SchemaVersion: 2,
		Rules:         map[string]ruleDefinition{rule.ID: rule},
		Accounts:      map[string]ruleAccount{},
	}
}

func TestNormalizeOverridesStaleBuiltinPreset(t *testing.T) {
	// 历史持久化的内置 series preset：BuiltinVersion=0（旧）、config 过时、无 modules。
	stale := ruleDefinition{
		ID:             foodTalksDefaultRulePresetID,
		Name:           "旧持久化",
		BuiltinVersion: 0,
		Versions: []rulePresetVersion{{
			Version: 1,
			Config:  map[string]any{"table_border_color": "#e5e7eb"},
		}},
	}
	doc := normalizeRuleStoreDocument(builtinVersionStoreDoc(stale))
	got := doc.Rules[foodTalksDefaultRulePresetID]
	wantVersion := builtinVersionForDefaultRule(foodTalksDefaultRulePresetID)
	if got.BuiltinVersion != wantVersion {
		t.Fatalf("BuiltinVersion 应提升到 %d，实际 %d", wantVersion, got.BuiltinVersion)
	}
	cfg := latestRuleVersion(got).Config
	if cfg["table_border_color"] != "#888888" {
		t.Fatalf("旧内置 preset 应被代码默认覆盖：table_border_color=%v，期望 #888888", cfg["table_border_color"])
	}
	mods, _ := cfg["modules"].(map[string]any)
	if _, ok := mods["ft_series_materials"]; !ok {
		t.Fatalf("覆盖后应含 modules.ft_series_materials（18 群/BD 名片），实际 modules=%v", cfg["modules"])
	}
}

func TestNormalizeKeepsCurrentBuiltinPreset(t *testing.T) {
	// 已是当前代码版本的内置 preset（用户在后台改过并保存）→ 不被覆盖，保留自定义。
	custom := ruleDefinition{
		ID:             foodTalksDefaultRulePresetID,
		Name:           "用户自定义",
		BuiltinVersion: builtinVersionForDefaultRule(foodTalksDefaultRulePresetID),
		Versions: []rulePresetVersion{{
			Version: 1,
			Config:  map[string]any{"table_border_color": "#123456"},
		}},
	}
	doc := normalizeRuleStoreDocument(builtinVersionStoreDoc(custom))
	cfg := latestRuleVersion(doc.Rules[foodTalksDefaultRulePresetID]).Config
	if cfg["table_border_color"] != "#123456" {
		t.Fatalf("当前版本内置 preset 不应被覆盖：table_border_color=%v，期望保留 #123456", cfg["table_border_color"])
	}
}

func TestNormalizeOnlyOverridesStaleTargetBuiltinPreset(t *testing.T) {
	// 本次 v17 只更新网站 ft-default-v1。其他内置默认即便低于 currentBuiltinRuleVersion，
	// 只要已达各自目标版本，就不能被覆盖，避免跨账号规则被重置。
	now := "2026-06-14T00:00:00Z"
	doc := ruleStoreDocumentV2{
		SchemaVersion: 2,
		Rules: map[string]ruleDefinition{
			defaultRulePresetID: {
				ID:             defaultRulePresetID,
				Name:           "FBIF 用户改过的默认",
				BuiltinVersion: builtinVersionForDefaultRule(defaultRulePresetID),
				Versions: []rulePresetVersion{{
					Version:   1,
					Config:    map[string]any{"text_color": "#abcdef"},
					Behavior:  defaultRuleBehavior(),
					CreatedAt: now,
				}},
				UpdatedAt: now,
			},
			foodTalksSiteDefaultRulePresetID: {
				ID:             foodTalksSiteDefaultRulePresetID,
				Name:           "旧 ft-default 网站默认",
				BuiltinVersion: defaultBuiltinRuleVersion,
				Versions: []rulePresetVersion{{
					Version:   1,
					Config:    map[string]any{"modules": map[string]any{"fbif_footer": map[string]any{"segments": map[string]any{"biz_coop": map[string]any{"html_lines": []any{"旧商务合作"}}}}}},
					Behavior:  defaultFoodTalksSiteRuleBehavior(),
					CreatedAt: now,
				}},
				UpdatedAt: now,
			},
			foodTalksSeriesSiteDefaultRulePresetID: {
				ID:             foodTalksSeriesSiteDefaultRulePresetID,
				Name:           "当前 ft-series 网站默认",
				BuiltinVersion: builtinVersionForDefaultRule(foodTalksSeriesSiteDefaultRulePresetID),
				Versions: []rulePresetVersion{{
					Version:   1,
					Config:    defaultFoodTalksSeriesSiteRuleConfig(),
					Behavior:  defaultFoodTalksSeriesSiteRuleBehavior(),
					CreatedAt: now,
				}},
				UpdatedAt: now,
			},
		},
		Accounts: map[string]ruleAccount{},
	}
	got := normalizeRuleStoreDocument(doc)
	fbifCfg := latestRuleVersion(got.Rules[defaultRulePresetID]).Config
	if fbifCfg["text_color"] != "#abcdef" {
		t.Fatalf("非目标内置默认不应被 v15 覆盖：text_color=%v", fbifCfg["text_color"])
	}
	ftDefault := got.Rules[foodTalksSiteDefaultRulePresetID]
	if ftDefault.BuiltinVersion != currentBuiltinRuleVersion {
		t.Fatalf("ft-default-v1 应升级到 v17，实际 %d", ftDefault.BuiltinVersion)
	}
	ftDefaultModules, _ := latestRuleVersion(ftDefault).Config["modules"].(map[string]any)
	ftDefaultFooter, _ := ftDefaultModules["fbif_footer"].(map[string]any)
	ftDefaultSegments, _ := ftDefaultFooter["segments"].(map[string]any)
	biz, _ := ftDefaultSegments["biz_coop"].(map[string]any)
	if !strings.Contains(strings.Join(anyStringSlice(biz["html_lines"]), "\n"), assetRefID("ft_footer_bobo_card")) {
		t.Fatalf("ft-default-v1 升级后商务合作应含 Bobo 二维码")
	}
	ftSeries := got.Rules[foodTalksSeriesSiteDefaultRulePresetID]
	if ftSeries.BuiltinVersion != ftSeriesSiteBuiltinRuleVersion {
		t.Fatalf("ft-series-v1 不应被 v17 覆盖，实际 %d", ftSeries.BuiltinVersion)
	}
}

func TestNormalizeLeavesUserPresetUntouched(t *testing.T) {
	// 用户自建 preset（id 不在 defaultRuleDefinitions）→ 永不被内置覆盖逻辑波及。
	user := ruleDefinition{
		ID:             "my-custom-rule",
		Name:           "我的规则",
		BuiltinVersion: 0,
		Versions: []rulePresetVersion{{
			Version: 1,
			Config:  map[string]any{"text_color": "#abcdef"},
		}},
	}
	doc := normalizeRuleStoreDocument(builtinVersionStoreDoc(user))
	got, ok := doc.Rules["my-custom-rule"]
	if !ok {
		t.Fatal("用户自建 preset 不应消失")
	}
	if latestRuleVersion(got).Config["text_color"] != "#abcdef" {
		t.Fatalf("用户自建 preset config 不应被覆盖，实际 %v", latestRuleVersion(got).Config["text_color"])
	}
}

func TestUpsertBuiltinPresetSurvivesReload(t *testing.T) {
	// 用户在后台编辑内置 series preset 并保存 → 重载（模拟重启）后自定义不被内置默认覆盖。
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	if _, err := store.upsert(rulePreset{
		ID:     foodTalksDefaultRulePresetID,
		Name:   "系列-用户改",
		Config: map[string]any{"table_border_color": "#abcabc"},
	}); err != nil {
		t.Fatalf("upsert 失败: %v", err)
	}
	got, err := store.activeForAccount("foodtalks-series")
	if err != nil {
		t.Fatalf("activeForAccount 失败: %v", err)
	}
	if got.Config["table_border_color"] != "#abcabc" {
		t.Fatalf("用户保存的内置 preset 重载后应保留：table_border_color=%v，期望 #abcabc", got.Config["table_border_color"])
	}
}
