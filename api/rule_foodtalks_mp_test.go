package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 「FoodTalks食品资讯」公众号账号（rule_foodtalks_mp.go，2026-10-27）。

func TestFoodTalksMpAccountRegistered(t *testing.T) {
	meta, ok := accountMetaByID(foodTalksMpRuleAccountID)
	if !ok || meta.Channel != "mp" || meta.TemplateID != "foodtalks-series" || meta.AccountName != "FoodTalks食品资讯" || meta.DefaultPresetID != foodTalksMpDefaultRulePresetID {
		t.Fatalf("catalog entry = %+v", meta)
	}
	def, ok := defaultRuleDefinitions("")[foodTalksMpDefaultRulePresetID]
	if !ok {
		t.Fatal("没有 foodtalks-mp-default 内置规则")
	}
	preset := rulePreset{ID: def.ID, Name: def.Name, Description: def.Description, BaselineID: def.ID, Config: def.Versions[0].Config, Behavior: def.Versions[0].Behavior}
	if err := validateRulePresetPayload(preset); err != nil {
		t.Fatalf("内置规则过不了规则后台的校验（存不回去）：%v", err)
	}
	// 什么是系列不动：没有排网站稿开关。
	series := defaultFoodTalksSeriesRuleBehavior()["params"].(map[string]any)
	if _, ok := series["site_article_layout"]; ok {
		t.Fatal("什么是系列不应打开 site_article_layout")
	}
}

func TestFoodTalksMpFooterAndSectionsResolveToFiles(t *testing.T) {
	cfg := defaultFoodTalksMpRuleConfig()
	mods := cfg["modules"].(map[string]any)
	if cfg["h1_font_size"] != "18" {
		t.Fatalf("一级标题字号 = %v，成品是 18px", cfg["h1_font_size"])
	}
	assets := map[string]map[string]any{}
	for _, raw := range mods["asset_library"].(map[string]any)["assets"].([]any) {
		a := raw.(map[string]any)
		assets[a["id"].(string)] = a
	}
	fileExists := func(url string) {
		t.Helper()
		if !strings.HasPrefix(url, "/assets/") {
			t.Fatalf("素材不是站内图：%s", url)
		}
		if _, err := os.Stat(filepath.Join("..", "public", url)); err != nil {
			t.Fatalf("素材文件不存在：%s", url)
		}
	}
	for i := 1; i <= foodTalksMpSectionCount; i++ {
		fileExists(mods["ft_series_materials"].(map[string]any)["sections"].(map[string]any)[fmt.Sprint(i)].(string))
	}
	preset := mods["bottom_blocks"].(map[string]any)["presets"].([]any)[0].(map[string]any)
	blocks := preset["blocks"].(map[string]any)
	for _, id := range preset["order"].([]any) {
		block := blocks[id.(string)].(map[string]any)
		if block["type"] != "image" {
			continue
		}
		ref := block["fields"].(map[string]any)["image"].(string)
		asset := assets[strings.TrimSuffix(strings.TrimPrefix(ref, "{{asset#"), "}}")]
		if asset == nil || asset["scope"] != foodTalksMpRuleAccountID {
			t.Fatalf("页脚 %s 引用的素材 %s 在本账号素材库里找不到", id, ref)
		}
		fileExists(asset["url"].(string))
	}
	groups := 0
	for id, a := range assets {
		if strings.HasPrefix(id, "ft_mp_group_") {
			groups++
			fileExists(a["url"].(string))
		}
	}
	if groups != 19 { // 食品资讯群（线上助手）+ 什么是系列 18 张品类群码
		t.Fatalf("群二维码 %d 张，want 19", groups)
	}
}

// 前端离线降级常量（foodtalks-series-defaults.js SITE_ARTICLE_*）须与这里的默认一字不差。
func TestFoodTalksMpBehaviorMatchesFrontendDefaults(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "public", "js", "foodtalks-series-defaults.js"))
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	beh := defaultFoodTalksMpRuleBehavior()
	params := beh["params"].(map[string]any)
	for _, key := range []string{"credit_line_regex", "site_byline_regex", "site_footer_start_regex", "site_tip_source_regex", "own_source_regex"} {
		want := fmt.Sprintf("%s: '%s'", key, strings.ReplaceAll(params[key].(map[string]any)["default"].(string), `\`, `\\`))
		if !strings.Contains(js, want) {
			t.Errorf("前端缺或不同：%s", want)
		}
	}
	templates := beh["templates"].(map[string]any)
	for _, key := range []string{"source_credit", "repost_tip", "original_tip", "contact_tip"} {
		want := fmt.Sprintf("%s: '%s'", key, templates[key])
		if !strings.Contains(js, want) {
			t.Errorf("前端缺或不同：%s", want)
		}
	}
}

// testdata/foodtalks-mp/rules.json：服务端给这个账号的默认规则（合并基线后的 config + behavior），
// 前端排版测试（tests/foodtalks-mp.test.mjs）照它排样稿。规则一变就失败，GOLDEN_UPDATE=1 重写。
func TestFoodTalksMpRulesFixture(t *testing.T) {
	got, err := json.MarshalIndent(map[string]any{
		"config":   mergeRuleDefaults(foodTalksMpDefaultRulePresetID, defaultFoodTalksMpRuleConfig()),
		"behavior": mergeRuleBehavior(foodTalksMpDefaultRulePresetID, defaultFoodTalksMpRuleBehavior()),
	}, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "foodtalks-mp", "rules.json")
	if os.Getenv("GOLDEN_UPDATE") != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("%s 和默认规则对不上：GOLDEN_UPDATE=1 go test -run TestFoodTalksMpRulesFixture 重写", path)
	}
}
