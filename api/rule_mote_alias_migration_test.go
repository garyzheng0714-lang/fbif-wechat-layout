package main

import "testing"

// 一次性迁移（2026-07-11 用户裁定放宽 Mote 识别）：旧内置默认的 is_mote_author 条件
// （精确等于 mote莫特）和 mote 别名（["Mote莫特","Mote 莫特"]，只能整体匹配）在加载时
// 就地放宽为 mote/莫特 包含匹配；后台自定义过的值不动。守护 WORKLOG-mote-alias-broaden-2026-07-11.md。

func TestMergeMigratesLegacyMoteConditions(t *testing.T) {
	legacy := map[string]any{
		"conditions": []any{
			map[string]any{"id": "is_mote_author", "all": []any{
				map[string]any{"path": "article.author_normalized", "op": "eq", "value": "mote莫特"},
			}},
		},
	}
	merged := mergeRuleBehavior(defaultRulePresetID, legacy)
	conds, _ := merged["conditions"].([]any)
	if len(conds) != 1 {
		t.Fatalf("conditions 数量 = %d，期望 1", len(conds))
	}
	c, _ := conds[0].(map[string]any)
	if _, ok := c["any"]; !ok {
		t.Fatalf("旧默认 conditions 应迁移为 any/includes，实际 %+v", c)
	}
	if _, ok := c["all"]; ok {
		t.Fatalf("迁移后不应保留旧 all 结构，实际 %+v", c)
	}
}

func TestMergeKeepsCustomMoteConditions(t *testing.T) {
	custom := map[string]any{
		"conditions": []any{
			map[string]any{"id": "is_mote_author", "all": []any{
				map[string]any{"path": "article.author_normalized", "op": "eq", "value": "自定义值"},
			}},
		},
	}
	merged := mergeRuleBehavior(defaultRulePresetID, custom)
	conds, _ := merged["conditions"].([]any)
	c, _ := conds[0].(map[string]any)
	all, ok := c["all"].([]any)
	if !ok || len(all) != 1 {
		t.Fatalf("自定义 conditions 不应被迁移覆盖，实际 %+v", c)
	}
	e, _ := all[0].(map[string]any)
	if e["value"] != "自定义值" {
		t.Fatalf("自定义 conditions 值应保留，实际 %v", e["value"])
	}
}

func TestMergeMigratesLegacyMoteAliases(t *testing.T) {
	cfg := map[string]any{
		"modules": map[string]any{
			"article_footer_defaults": map[string]any{
				"enabled":        true,
				"author_aliases": map[string]any{"mote": []any{"Mote莫特", "Mote 莫特"}},
				"profiles":       []any{},
			},
		},
	}
	merged := mergeRuleDefaults(defaultRulePresetID, cfg)
	mods, _ := merged["modules"].(map[string]any)
	afd, _ := mods["article_footer_defaults"].(map[string]any)
	aliases, _ := afd["author_aliases"].(map[string]any)
	mote, _ := aliases["mote"].([]any)
	if len(mote) != 2 || mote[0] != "mote" || mote[1] != "莫特" {
		t.Fatalf("旧 mote 别名应迁移为 [mote 莫特]，实际 %v", mote)
	}
}

func TestMergeKeepsCustomMoteAliases(t *testing.T) {
	cfg := map[string]any{
		"modules": map[string]any{
			"article_footer_defaults": map[string]any{
				"enabled":        true,
				"author_aliases": map[string]any{"mote": []any{"莫特先生", "特别别名"}},
				"profiles":       []any{},
			},
		},
	}
	merged := mergeRuleDefaults(defaultRulePresetID, cfg)
	mods, _ := merged["modules"].(map[string]any)
	afd, _ := mods["article_footer_defaults"].(map[string]any)
	aliases, _ := afd["author_aliases"].(map[string]any)
	mote, _ := aliases["mote"].([]any)
	if len(mote) != 2 || mote[0] != "莫特先生" {
		t.Fatalf("自定义别名应保留，实际 %v", mote)
	}
}

// 内置默认本身就应是放宽后的新值（新建 preset 走这里）。
func TestDefaultMoteAliasesAreBroadened(t *testing.T) {
	afd := defaultArticleFooterDefaultsModule()
	aliases, _ := afd["author_aliases"].(map[string]any)
	mote, _ := aliases["mote"].([]any)
	if len(mote) != 2 || mote[0] != "mote" || mote[1] != "莫特" {
		t.Fatalf("内置默认 mote 别名应为 [mote 莫特]，实际 %v", mote)
	}
	beh := defaultRuleBehavior()
	conds, _ := beh["conditions"].([]any)
	c, _ := conds[0].(map[string]any)
	if _, ok := c["any"]; !ok {
		t.Fatalf("内置默认 is_mote_author 应为 any/includes，实际 %+v", c)
	}
}
