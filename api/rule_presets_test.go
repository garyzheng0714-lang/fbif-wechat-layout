package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validMoreArticlesDefaultCards() []any {
	return []any{
		map[string]any{
			"href":   "https://mp.weixin.qq.com/s/custom-a",
			"title":  "自定义 A",
			"imgurl": "https://mmbiz.qpic.cn/custom-a.jpg",
		},
	}
}

func ruleRevisionPointer(value int) *int {
	return &value
}

func articleFooterDefaultsWithProfiles(n int) map[string]any {
	aliases := map[string]any{}
	profiles := []any{}
	for i := 0; i < n; i++ {
		groupID := fmt.Sprintf("author_%03d", i)
		aliases[groupID] = []any{fmt.Sprintf("作者%d", i)}
		profiles = append(profiles, map[string]any{
			"id":   groupID,
			"name": fmt.Sprintf("作者%d 默认", i),
			"match": map[string]any{
				"author_alias_group": groupID,
			},
			"block_visibility": map[string]any{
				"byline":        false,
				"more_articles": true,
			},
			"artifact_visibility": map[string]any{
				"mote_ref_author_card": false,
			},
		})
	}
	return map[string]any{
		"schema_version": float64(1),
		"enabled":        true,
		"author_aliases": aliases,
		"profiles":       profiles,
	}
}

func TestRulePresetStorePersistsActivePreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	store := newRulePresetStore(path)

	doc, err := store.upsert(rulePreset{
		ID:         "campaign",
		Name:       "Campaign Rule",
		BaselineID: defaultRulePresetID,
		Config: map[string]any{
			"font_size": "16",
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if len(doc.Presets) != 6 {
		t.Fatalf("expected five account defaults + custom preset, got %d", len(doc.Presets))
	}

	if _, err := store.setActive("campaign"); err != nil {
		t.Fatalf("setActive: %v", err)
	}

	reloaded := newRulePresetStore(path)
	active, err := reloaded.active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active.ID != "campaign" {
		t.Fatalf("active ID = %q, want campaign", active.ID)
	}
	if active.Config["font_size"] != "16" {
		t.Fatalf("font_size = %v, want 16", active.Config["font_size"])
	}
	if active.Config["heading_size"] == "" {
		t.Fatalf("expected default fields to be merged")
	}
}

func TestRulePresetStoreRejectsUnknownAccountInsteadOfFallingBackToFBIF(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	if _, err := store.activeForAccount("ft-defaut"); err == nil || !strings.Contains(err.Error(), "未知规则账号") {
		t.Fatalf("未知账号读取必须 fail loud，实际 err=%v", err)
	}
	if _, err := store.setActiveForAccount("ft-defaut", defaultRulePresetID); err == nil || !strings.Contains(err.Error(), "未知规则账号") {
		t.Fatalf("未知账号绑定必须 fail loud，实际 err=%v", err)
	}
}

func TestRulePresetStoreRejectsCrossFamilyRuleBinding(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	if _, err := store.setActiveForAccount(foodTalksSiteRuleAccountID, defaultRulePresetID); err == nil || !strings.Contains(err.Error(), "规则族") {
		t.Fatalf("网站账号不得绑定公众号 baseline，实际 err=%v", err)
	}
	if _, err := store.setActiveForAccount(foodTalksRuleAccountID, defaultRulePresetID); err == nil || !strings.Contains(err.Error(), "规则族") {
		t.Fatalf("FBIF 规则不得绑定独立 FoodTalks 系列渲染器，实际 err=%v", err)
	}
	if _, err := store.setActiveForAccount(defaultRuleAccountID, foodTalksDefaultRulePresetID); err == nil || !strings.Contains(err.Error(), "规则族") {
		t.Fatalf("FoodTalks 系列规则不得反向绑定 FBIF，实际 err=%v", err)
	}
	if _, err := store.setActiveForAccount(wowFoodRuleAccountID, defaultRulePresetID); err != nil {
		t.Fatalf("同为公众号渠道的规则应允许复用：%v", err)
	}
	if _, err := store.setActiveForAccount(foodTalksSeriesSiteRuleAccountID, foodTalksSiteDefaultRulePresetID); err != nil {
		t.Fatalf("同为 foodtalks-site 规则族应允许复用：%v", err)
	}
}

func TestRulePresetUpsertCannotChangeBaselineAcrossBoundRuleFamily(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store := newRulePresetStore(path)
	if _, err := store.upsert(rulePreset{ID: "bound-custom", Name: "绑定规则", BaselineID: defaultRulePresetID, Config: map[string]any{"font_size": "16"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.setActiveForAccount(defaultRuleAccountID, "bound-custom"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.upsert(rulePreset{ID: "bound-custom", Name: "错误改族", BaselineID: foodTalksDefaultRulePresetID, Config: map[string]any{"font_size": "17"}}); err == nil || !strings.Contains(err.Error(), "规则族") {
		t.Fatalf("已绑定规则跨族改 baseline 必须在落盘前拒绝，实际 err=%v", err)
	}
	reloaded := newRulePresetStore(path)
	active, err := reloaded.activeForAccount(defaultRuleAccountID)
	if err != nil {
		t.Fatalf("拒绝写入后旧库仍应可读：%v", err)
	}
	if active.ID != "bound-custom" || active.BaselineID != defaultRulePresetID || active.Config["font_size"] != "16" {
		t.Fatalf("拒绝写入后旧版本必须原样保留：%#v", active)
	}
}

func TestRuleStoreV2MarksUnboundCustomRuleAsMigrationRequired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	raw := `{
		"schema_version": 2,
		"accounts": {},
		"rules": {
			"legacy-custom": {
				"id": "legacy-custom",
				"name": "历史自建规则",
				"versions": [{"version": 1, "config": {"font_size": "16"}, "behavior": {}}]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store := newRulePresetStore(path)
	doc, err := store.readStoreLocked()
	if err != nil {
		t.Fatalf("v2 存量规则库应兼容迁移：%v", err)
	}
	legacy := doc.Rules["legacy-custom"]
	if legacy.BaselineID != defaultRulePresetID {
		t.Fatalf("无绑定历史规则应使用确定性占位基线，实际 %q", legacy.BaselineID)
	}
	if !legacy.MigrationRequired || !strings.Contains(legacy.MigrationNote, "无法推断") {
		t.Fatalf("零绑定不得静默当作 FBIF 规则：%#v", legacy)
	}
	listed := legacyDocumentFromStore(doc)
	for _, preset := range listed.Presets {
		if preset.ID != "legacy-custom" {
			continue
		}
		if preset.Config["font_size"] != "16" || preset.Config["heading_size"] != nil {
			t.Fatalf("待迁移 preset 只能暴露原始 overrides，不得展开占位基线：%#v", preset.Config)
		}
	}
	if err := store.writeStoreLocked(doc); err != nil {
		t.Fatalf("回写迁移后规则库：%v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(fmt.Sprintf(`"schema_version": %d`, currentRuleStoreSchemaVersion))) || !bytes.Contains(b, []byte(`"migration_required": true`)) {
		t.Fatalf("回写后必须持久化待确认标记：%s", b)
	}
}

func TestRuleStoreV2InfersCustomRuleBaselineFromUniqueAccountBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	raw := `{
		"schema_version": 2,
		"accounts": {
			"ft-default": {"name":"网站默认","active_rule_id":"website-custom","active_rule_version":1}
		},
		"rules": {
			"website-custom": {
				"id":"website-custom","name":"历史网站规则",
				"versions":[{"version":1,"config":{"author_font_size":"16"},"behavior":{"author_regex":"^署名[：:]"}}]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := newRulePresetStore(path).readStoreLocked()
	if err != nil {
		t.Fatalf("v2 网站规则迁移失败：%v", err)
	}
	got := doc.Rules["website-custom"]
	if got.BaselineID != foodTalksSiteDefaultRulePresetID {
		t.Fatalf("唯一绑定 ft-default 的历史规则应迁移到网站基线，实际 %q", got.BaselineID)
	}
	if got.MigrationRequired {
		t.Fatalf("唯一账号绑定可确定推断，不应要求人工迁移：%#v", got)
	}
	behavior := latestRuleVersion(got).Behavior
	if _, leaked := behavior["actions"]; leaked {
		t.Fatalf("网站历史规则迁移后不得注入公众号 actions：%#v", behavior["actions"])
	}
}

func TestRuleStoreV2AmbiguousBindingsAreMarkedInsteadOfSilentlyDefaulted(t *testing.T) {
	doc := ruleStoreDocumentV2{
		SchemaVersion: 2,
		Accounts: map[string]ruleAccount{
			"fbif":       {ActiveRuleID: "shared-custom"},
			"ft-default": {ActiveRuleID: "shared-custom"},
		},
		Rules: map[string]ruleDefinition{
			"shared-custom": {ID: "shared-custom"},
		},
	}
	migrateLegacyRuleBaselines(&doc)
	if got := doc.Rules["shared-custom"].BaselineID; got != defaultRulePresetID {
		t.Fatalf("歧义规则占位基线应按 catalog 顺序确定，实际 %q", got)
	}
	if !doc.Rules["shared-custom"].MigrationRequired {
		t.Fatal("多基线绑定必须显式标记待迁移")
	}
}

func TestRuleStoreV2AmbiguousCrossFamilyBindingsStayReadableButCannotRebind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	raw := `{
		"schema_version": 2,
		"accounts": {
			"fbif": {"active_rule_id":"shared-custom","active_rule_version":1},
			"ft-default": {"active_rule_id":"shared-custom","active_rule_version":1}
		},
		"rules": {
			"shared-custom": {
				"id":"shared-custom","name":"历史跨族共享规则",
				"versions":[{"version":1,"config":{"font_size":"16"}}]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store := newRulePresetStore(path)
	if _, err := store.readStoreLocked(); err != nil {
		t.Fatalf("历史歧义不得直接阻断生产升级：%v", err)
	}
	fbif, err := store.activeForAccount(defaultRuleAccountID)
	if err != nil {
		t.Fatal(err)
	}
	website, err := store.activeForAccount(foodTalksSiteRuleAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if !fbif.MigrationRequired || !website.MigrationRequired {
		t.Fatalf("两个现有绑定都必须暴露迁移状态：fbif=%#v website=%#v", fbif, website)
	}
	if fbif.RuleFamily != "fbif" || website.RuleFamily != "foodtalks-site" {
		t.Fatalf("待迁移规则应按各账号自己基线读取：fbif=%q website=%q", fbif.RuleFamily, website.RuleFamily)
	}
	if _, err := store.setActiveForAccount(wowFoodRuleAccountID, "shared-custom"); err == nil || !strings.Contains(err.Error(), "先确认历史基线") {
		t.Fatalf("待迁移规则禁止新绑定，实际 err=%v", err)
	}
}

func TestRuleStoreV2UnboundRuleCanBindOnlyAfterExplicitBaselineSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	raw := `{
		"schema_version":2,
		"accounts":{},
		"rules":{"legacy-custom":{"id":"legacy-custom","name":"旧规则","versions":[{"version":1,"config":{"text_color":"#123456"},"behavior":{"author_regex":"^署名[：:]"}}]}}
	}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store := newRulePresetStore(path)
	if _, err := store.setActiveForAccount(foodTalksSiteRuleAccountID, "legacy-custom"); err == nil || !strings.Contains(err.Error(), "先确认历史基线") {
		t.Fatalf("未确认基线前不得绑定，实际 err=%v", err)
	}
	if _, err := store.upsert(rulePreset{
		ID: "legacy-custom", Name: "旧规则", BaselineID: foodTalksSiteDefaultRulePresetID,
		Config: map[string]any{"text_color": "#deadbe"},
	}); err == nil || !strings.Contains(err.Error(), "尚未确认历史基线") {
		t.Fatalf("仅传 baseline_id 不算人工确认，实际 err=%v", err)
	}
	doc, err := store.upsert(rulePreset{
		ID: "legacy-custom", Name: "旧规则", BaselineID: foodTalksSiteDefaultRulePresetID,
		MigrationConfirmed: true,
		Config:             map[string]any{"text_color": "#deadbe"},
		Behavior:           map[string]any{"author_regex": "^CLIENT-WRONG$"},
	})
	if err != nil {
		t.Fatalf("显式选择网站 baseline 应能完成迁移：%v", err)
	}
	var migrated rulePreset
	for _, preset := range doc.Presets {
		if preset.ID == "legacy-custom" {
			migrated = preset
			break
		}
	}
	if migrated.MigrationRequired || migrated.BaselineID != foodTalksSiteDefaultRulePresetID {
		t.Fatalf("显式保存后应解除迁移标记：%#v", migrated)
	}
	if migrated.Config["text_color"] != "#123456" || migrated.Behavior["author_regex"] != "^署名[：:]" {
		t.Fatalf("首次确认必须用原始 overrides 重建，不得信任客户端已展开值：%#v", migrated)
	}
	if _, err := store.setActiveForAccount(foodTalksSiteRuleAccountID, "legacy-custom"); err != nil {
		t.Fatalf("完成迁移后应允许绑定：%v", err)
	}
}

func TestRuleStoreV3MissingBaselineFailsValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	raw := `{
		"schema_version": 3,
		"accounts": {},
		"rules": {
			"broken-custom": {
				"id": "broken-custom",
				"name": "缺基线规则",
				"versions": [{"version": 1, "config": {"font_size": "16"}, "behavior": {}}]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	err := newRulePresetStore(path).validate()
	if err == nil {
		t.Fatal("schema v3 缺 baseline_id 必须在启动校验期失败")
	}
	if !strings.Contains(err.Error(), "broken-custom") || !strings.Contains(err.Error(), "baseline_id") {
		t.Fatalf("启动错误应指明规则和 baseline_id，实际：%v", err)
	}
}

func TestArticleFooterDefaultsAllowsManyAuthorProfiles(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	cfg := defaultRuleConfig()
	mods, ok := cfg["modules"].(map[string]any)
	if !ok {
		t.Fatal("default config modules missing")
	}
	mods["article_footer_defaults"] = articleFooterDefaultsWithProfiles(500)

	if _, err := store.upsert(rulePreset{ID: "many-authors", Name: "Many Authors", BaselineID: defaultRulePresetID, Config: cfg}); err != nil {
		t.Fatalf("upsert many article footer profiles: %v", err)
	}
}

func TestArticleFooterDefaultsAllowsArtifactVisibility(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	cfg := defaultRuleConfig()
	mods, ok := cfg["modules"].(map[string]any)
	if !ok {
		t.Fatal("default config modules missing")
	}
	mods["article_footer_defaults"] = map[string]any{
		"schema_version": float64(1),
		"enabled":        true,
		"author_aliases": map[string]any{"mote": []any{"Mote莫特"}},
		"profiles": []any{
			map[string]any{
				"id":    "mote_author",
				"name":  "Mote 作者默认",
				"match": map[string]any{"author_alias_group": "mote"},
				"block_visibility": map[string]any{
					"byline": false,
				},
				"artifact_visibility": map[string]any{
					"mote_ref_author_card": false,
				},
			},
		},
	}

	if _, err := store.upsert(rulePreset{ID: "mote-artifact-default", Name: "Mote", BaselineID: defaultRulePresetID, Config: cfg}); err != nil {
		t.Fatalf("upsert article footer artifact defaults: %v", err)
	}
}

func TestArticleFooterDefaultsRejectsInvalidProfiles(t *testing.T) {
	tests := []struct {
		name   string
		module map[string]any
	}{
		{
			name:   "too many profiles",
			module: articleFooterDefaultsWithProfiles(501),
		},
		{
			name: "profile references missing alias group",
			module: map[string]any{
				"schema_version": float64(1),
				"enabled":        true,
				"author_aliases": map[string]any{},
				"profiles": []any{
					map[string]any{
						"id":    "missing_group_profile",
						"name":  "缺失作者组",
						"match": map[string]any{"author_alias_group": "missing_group"},
						"block_visibility": map[string]any{
							"byline": false,
						},
					},
				},
			},
		},
		{
			name: "profile name must be string",
			module: map[string]any{
				"schema_version": float64(1),
				"enabled":        true,
				"author_aliases": map[string]any{"author_a": []any{"作者A"}},
				"profiles": []any{
					map[string]any{
						"id":    "author_a",
						"name":  []any{"作者A 默认"},
						"match": map[string]any{"author_alias_group": "author_a"},
						"block_visibility": map[string]any{
							"byline": false,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
			cfg := defaultRuleConfig()
			mods, ok := cfg["modules"].(map[string]any)
			if !ok {
				t.Fatal("default config modules missing")
			}
			mods["article_footer_defaults"] = tt.module
			if _, err := store.upsert(rulePreset{ID: "bad-author-default", Name: "Bad", BaselineID: defaultRulePresetID, Config: cfg}); err == nil {
				t.Fatal("expected invalid article_footer_defaults profile to be rejected")
			}
		})
	}
}

func TestUpsertBuiltinVersionUsesPresetSpecificTarget(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))

	if _, err := store.upsert(rulePreset{
		ID:       defaultRulePresetID,
		Name:     "FBIF默认规则",
		Config:   defaultRuleConfig(),
		Behavior: defaultRuleBehavior(),
	}); err != nil {
		t.Fatalf("upsert fbif default: %v", err)
	}
	if _, err := store.upsert(rulePreset{
		ID:       wowFoodDefaultRulePresetID,
		Name:     "WowFood默认规则",
		Config:   defaultWowFoodRuleConfig(),
		Behavior: defaultWowFoodRuleBehavior(),
	}); err != nil {
		t.Fatalf("upsert wowfood default: %v", err)
	}
	if _, err := store.upsert(rulePreset{
		ID:         "my-custom-rule",
		Name:       "自建规则",
		BaselineID: defaultRulePresetID,
		Config:     defaultRuleConfig(),
		Behavior:   defaultRuleBehavior(),
	}); err != nil {
		t.Fatalf("upsert custom rule: %v", err)
	}

	store.mu.Lock()
	doc, err := store.readStoreLocked()
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if got := doc.Rules[defaultRulePresetID].BuiltinVersion; got != fbifBuiltinRuleVersion {
		t.Fatalf("FBIF builtin version = %d, want %d", got, fbifBuiltinRuleVersion)
	}
	if got := doc.Rules[wowFoodDefaultRulePresetID].BuiltinVersion; got != defaultBuiltinRuleVersion {
		t.Fatalf("WowFood builtin version = %d, want %d", got, defaultBuiltinRuleVersion)
	}
	if got := doc.Rules["my-custom-rule"].BuiltinVersion; got != 0 {
		t.Fatalf("custom rule builtin version = %d, want 0", got)
	}
}

func TestDefaultRuleStoreIncludesAccountBindings(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))

	doc, err := store.list()
	if err != nil {
		t.Fatalf("list default store: %v", err)
	}

	// 5 个账号，顺序由 accountCatalog（单一真相源）派生，与前端账号菜单一致：
	// fbif, foodtalks-series, wowfood, ft-default, ft-series。
	if len(doc.Accounts) != 5 {
		t.Fatalf("accounts = %d, want 5: %#v", len(doc.Accounts), doc.Accounts)
	}
	wantOrder := []struct{ id, name string }{
		{"fbif", "FBIF食品饮料创新"},
		{"foodtalks-series", "FoodTalks什么是系列"},
		{"wowfood", "WowFood爆品超市"},
		{"ft-default", "网站默认"},
		{"ft-series", "网站·什么是系列"},
	}
	for i, want := range wantOrder {
		if doc.Accounts[i].ID != want.id {
			t.Fatalf("account[%d] id = %q, want %q", i, doc.Accounts[i].ID, want.id)
		}
		if doc.Accounts[i].Name != want.name {
			t.Fatalf("account[%d] name = %q, want %q", i, doc.Accounts[i].Name, want.name)
		}
	}
	// WowFood 账号绑定自己的独立 preset（wowfood-default），不复用 FBIF default。
	if doc.Accounts[2].ActiveID != "wowfood-default" {
		t.Fatalf("wowfood active rule = %q, want wowfood-default", doc.Accounts[2].ActiveID)
	}
	for _, a := range doc.Accounts {
		if a.ActiveID == "" {
			t.Fatalf("account %q must expose active rule id: %#v", a.ID, doc.Accounts)
		}
	}
	// All four accounts must have distinct active rule IDs
	seen := map[string]bool{}
	for _, a := range doc.Accounts {
		if seen[a.ActiveID] {
			t.Fatalf("default accounts should start with independent rule ids: %#v", doc.Accounts)
		}
		seen[a.ActiveID] = true
	}

	// WowFood 与 FBIF 唯一的默认差异：WowFood 不带 FBIF 的「更多文章」推荐位
	// （红线 #14 + 品牌隔离）。锁住这条意图，防有人把 defaultWowFoodRuleConfig
	// 退化成直接 return defaultRuleConfig() 而 CI 仍绿。
	presetByID := func(id string) rulePreset {
		for _, p := range doc.Presets {
			if p.ID == id {
				return p
			}
		}
		t.Fatalf("preset %q not found in default store", id)
		return rulePreset{}
	}
	if v := presetByID("wowfood-default").Config["more_articles_enabled"]; v != false {
		t.Fatalf("wowfood-default more_articles_enabled = %#v, want false", v)
	}
	if v := presetByID("default").Config["more_articles_enabled"]; v != true {
		t.Fatalf("fbif default more_articles_enabled = %#v, want true（差异即意图）", v)
	}
	// 用户裁定：WowFood 底部默认「无任何排版规则」。锁住三条——更多文章关、
	// 不继承 FBIF 的默认推荐卡（清空）、页脚开关关。防有人把 WowFood 退回 FBIF 基线。
	wow := presetByID("wowfood-default").Config
	if cards, ok := wow["more_articles_default_cards"].([]any); !ok || len(cards) != 0 {
		t.Fatalf("wowfood-default more_articles_default_cards = %#v, want empty []", wow["more_articles_default_cards"])
	}
	if v := wow["footer_enabled"]; v != false {
		t.Fatalf("wowfood-default footer_enabled = %#v, want false（底部无排版规则）", v)
	}
	// 锁住 bottom_blocks 也关：WowFood 复用 templateId=fbif，其 renderBottomFooter 会直接渲染
	// bottom_blocks；若继承 FBIF 基线的 enabled=true，WowFood 文末会错渲 FBIF 提示/名片/更多文章。
	wowMods, ok := wow["modules"].(map[string]any)
	if !ok {
		t.Fatalf("wowfood-default modules missing")
	}
	wowBB, ok := wowMods["bottom_blocks"].(map[string]any)
	if !ok {
		t.Fatalf("wowfood-default modules.bottom_blocks missing")
	}
	if wowBB["enabled"] != false {
		t.Fatalf("wowfood-default bottom_blocks.enabled = %#v, want false（否则错渲 FBIF 底部+破图）", wowBB["enabled"])
	}
	if aft, ok := wowMods["article_footer_defaults"].(map[string]any); ok && aft["enabled"] == true {
		t.Fatalf("wowfood-default article_footer_defaults.enabled = %#v, want absent/disabled", aft["enabled"])
	}
	// 对照：FBIF 基线仍带 3 张默认卡，证明 WowFood 的空是显式覆盖、非全局退化。
	if cards, ok := presetByID("default").Config["more_articles_default_cards"].([]any); !ok || len(cards) == 0 {
		t.Fatalf("fbif default more_articles_default_cards 应保留默认卡（差异即意图），got %#v", presetByID("default").Config["more_articles_default_cards"])
	}
	fbifMods, ok := presetByID("default").Config["modules"].(map[string]any)
	if !ok {
		t.Fatalf("fbif default modules missing")
	}
	aft, ok := fbifMods["article_footer_defaults"].(map[string]any)
	if !ok || aft["enabled"] != true {
		t.Fatalf("fbif default article_footer_defaults = %#v, want enabled module", fbifMods["article_footer_defaults"])
	}
	aliases, _ := aft["author_aliases"].(map[string]any)
	if _, ok := aliases["mote"]; !ok {
		t.Fatalf("fbif default article_footer_defaults.author_aliases.mote missing: %#v", aft)
	}
	profiles, _ := aft["profiles"].([]any)
	if len(profiles) == 0 {
		t.Fatalf("fbif default article_footer_defaults.profiles missing: %#v", aft)
	}
	moteProfile, _ := profiles[0].(map[string]any)
	// 用户裁定（2026-07-05）：Mote 作者默认【显示】参考文献前署名图与 Bobo 名片。
	artifactVisibility, _ := moteProfile["artifact_visibility"].(map[string]any)
	if artifactVisibility["mote_ref_author_card"] != true {
		t.Fatalf("mote artifact default = %#v, want mote_ref_author_card true", artifactVisibility)
	}
	blockVisibility, _ := moteProfile["block_visibility"].(map[string]any)
	if blockVisibility["byline"] != true {
		t.Fatalf("mote block default = %#v, want byline true", blockVisibility)
	}
	for _, id := range []string{"ft-default-v1", "ft-series-v1", "foodtalks-series-default"} {
		cfg := presetByID(id).Config
		mods, _ := cfg["modules"].(map[string]any)
		if mod, ok := mods["article_footer_defaults"].(map[string]any); ok && mod["enabled"] == true {
			t.Fatalf("%s article_footer_defaults.enabled = %#v, want absent/disabled", id, mod["enabled"])
		}
	}
}

func TestMergeRuleDefaultsDoesNotInjectArticleFooterDefaults(t *testing.T) {
	cfg := mergeRuleDefaults(defaultRulePresetID, map[string]any{
		"font_size": "16",
		"modules": map[string]any{
			"bottom_blocks": map[string]any{"schema_version": 2, "enabled": false, "presets": []any{}},
		},
	})
	mods, _ := cfg["modules"].(map[string]any)
	aft, _ := mods["article_footer_defaults"].(map[string]any)
	if aft["enabled"] == true {
		t.Fatalf("article_footer_defaults.enabled = %#v, want disabled when source did not declare it", aft["enabled"])
	}

	explicit := mergeRuleDefaults(defaultRulePresetID, map[string]any{
		"modules": map[string]any{
			"article_footer_defaults": defaultArticleFooterDefaultsModule(),
		},
	})
	explicitMods, _ := explicit["modules"].(map[string]any)
	explicitAFT, _ := explicitMods["article_footer_defaults"].(map[string]any)
	if explicitAFT["enabled"] != true {
		t.Fatalf("explicit article_footer_defaults.enabled = %#v, want true preserved", explicitAFT["enabled"])
	}
}

func TestDefaultRulePresetIncludesBehavior(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))

	active, err := store.activeForAccount("fbif")
	if err != nil {
		t.Fatalf("active default rule: %v", err)
	}
	if active.Behavior == nil {
		t.Fatal("default active rule should include behavior")
	}
	templates, _ := active.Behavior["templates"].(map[string]any)
	if templates["author_line"] == "" {
		t.Fatalf("default behavior missing author_line template: %#v", active.Behavior)
	}
	params, _ := active.Behavior["params"].(map[string]any)
	moteParam, _ := params["mote_ref_image_url"].(map[string]any)
	if moteParam["default"] == "" {
		t.Fatalf("default behavior missing Mote image param: %#v", active.Behavior)
	}
}

func TestRulePresetRejectsInvalidBehavior(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))

	_, err := store.upsert(rulePreset{
		ID:         "bad-behavior",
		Name:       "Bad behavior",
		BaselineID: defaultRulePresetID,
		Config:     defaultRuleConfig(),
		Behavior: map[string]any{
			"schema_version": float64(1),
			"params": map[string]any{
				"mote_ref_image_url": map[string]any{"type": "url", "required": true, "default": "http://example.com/a.png"},
			},
			"templates": map[string]any{
				"author_line": `<span onclick="alert(1)">作者：{{ article.author }}</span>`,
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid behavior to be rejected")
	}

	_, err = store.upsert(rulePreset{
		ID:         "bad-behavior-url",
		Name:       "Bad behavior URL",
		BaselineID: defaultRulePresetID,
		Config:     defaultRuleConfig(),
		Behavior: map[string]any{
			"params": map[string]any{
				"mote_ref_image_url": map[string]any{"type": "url", "required": true, "default": `https://example.com/a.png" onerror="alert(1)`},
			},
		},
	})
	if err == nil {
		t.Fatal("expected attribute-breaking behavior URL to be rejected")
	}

	_, err = store.upsert(rulePreset{
		ID:         "bad-behavior-action",
		Name:       "Bad behavior action",
		BaselineID: defaultRulePresetID,
		Config:     defaultRuleConfig(),
		Behavior: map[string]any{
			"actions": []any{
				map[string]any{
					"when":      "is_mote_author",
					"type":      "insert_image_before",
					"target":    "first_ref_heading",
					"src_param": "missing_image",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected unknown behavior action param to be rejected")
	}

	_, err = store.upsert(rulePreset{
		ID:         "bad-behavior-shape",
		Name:       "Bad behavior shape",
		BaselineID: defaultRulePresetID,
		Config:     defaultRuleConfig(),
		Behavior: map[string]any{
			"params": []any{"not-object"},
		},
	})
	if err == nil {
		t.Fatal("expected malformed behavior sections to be rejected")
	}
}

func TestRuleActiveCanBeSelectedPerAccount(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	custom := rulePreset{ID: "foodtalks-custom", Name: "FoodTalks Custom", BaseRevision: ruleRevisionPointer(0), BaselineID: foodTalksDefaultRulePresetID, Config: defaultRuleConfig()}
	body, _ := json.Marshal(custom)
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("X-Admin-Password", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save custom code = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/rules/admin/active", bytes.NewBufferString(`{"account_id":"foodtalks-series","id":"foodtalks-custom","base_revision":1}`))
	req.Header.Set("X-Admin-Password", "secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set foodtalks active code = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/rules/active?account=foodtalks-series", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("foodtalks active code = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("active rule Cache-Control = %q, want no-store", got)
	}
	var payload struct {
		Preset rulePreset `json:"preset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal active payload: %v", err)
	}
	if payload.Preset.ID != "foodtalks-custom" {
		t.Fatalf("foodtalks active preset = %q, want foodtalks-custom", payload.Preset.ID)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/rules/active?account=fbif", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fbif active code = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal fbif active payload: %v", err)
	}
	if payload.Preset.ID == "foodtalks-custom" {
		t.Fatalf("fbif active preset changed when only FoodTalks was updated")
	}
}

func TestRulePresetPublicCatalogOmitsRuleBodies(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	req := httptest.NewRequest(http.MethodGet, "/api/rules/presets", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog code = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Presets  []map[string]any     `json:"presets"`
		Accounts []ruleAccountBinding `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal catalog payload: %v", err)
	}
	if len(payload.Presets) < 2 {
		t.Fatalf("catalog presets = %d, want account defaults", len(payload.Presets))
	}
	if payload.Presets[0]["id"] == "" || payload.Presets[0]["name"] == "" {
		t.Fatalf("catalog should expose rule id and name: %#v", payload.Presets[0])
	}
	for _, preset := range payload.Presets {
		if preset["channel"] != "mp" && preset["channel"] != "ft" {
			t.Fatalf("catalog preset 必须携带可过滤的 channel：%#v", preset)
		}
		if preset["rule_family"] == "" {
			t.Fatalf("catalog preset 必须携带可过滤的 rule_family：%#v", preset)
		}
	}
	if _, ok := payload.Presets[0]["config"]; ok {
		t.Fatalf("catalog must not expose editable config body: %#v", payload.Presets[0])
	}
	if _, ok := payload.Presets[0]["behavior"]; ok {
		t.Fatalf("catalog must not expose behavior code body: %#v", payload.Presets[0])
	}
	// Now 5 accounts: fbif, wowfood, foodtalks-series, ft-default, ft-series (FoodTalks website)
	if len(payload.Accounts) != 5 {
		t.Fatalf("catalog accounts = %d, want 5", len(payload.Accounts))
	}
	for _, binding := range payload.Accounts {
		if binding.ActiveRevision <= 0 {
			t.Fatalf("catalog account %q missing active_revision: %+v", binding.ID, binding)
		}
	}
}

func TestRulePresetAdminHandlersRequirePassword(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	body, _ := json.Marshal(rulePreset{ID: "test", Name: "Test", BaseRevision: ruleRevisionPointer(0), BaselineID: defaultRulePresetID, Config: defaultRuleConfig()})
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without password code = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Password", "secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with password code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestRulePresetLoginValidatesPassword(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/login", bytes.NewBufferString(`{"password":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password code = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/rules/admin/login", bytes.NewBufferString(`{"password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("good password code = %d, want 200", rec.Code)
	}
}

func TestAdminHandlersRequireConfiguredPassword(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/login", bytes.NewBufferString(`{"password":"FBIF2026"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("login without configured password code = %d, want 503", rec.Code)
	}
}

func TestRulePresetStoreMigratesLegacyV1FileToV2OnWriteKeepingLegacyFacade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	legacy := `{
  "active_id": "campaign",
  "presets": [
    {
      "id": "campaign",
      "name": "Campaign Rule",
      "config": { "font_size": "16" },
      "updated_at": "2026-05-22T00:00:00Z"
    }
  ],
  "updated_at": "2026-05-22T00:00:00Z"
}`
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy fixture: %v", err)
	}

	store := newRulePresetStore(path)
	active, err := store.active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active.ID != "campaign" {
		t.Fatalf("active ID = %q, want campaign", active.ID)
	}
	if active.Config["font_size"] != "16" {
		t.Fatalf("font_size = %v, want 16", active.Config["font_size"])
	}

	if _, err := store.upsert(rulePreset{
		ID:         "campaign",
		Name:       "Campaign Rule",
		BaselineID: defaultRulePresetID,
		Config:     map[string]any{"font_size": "17"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var raw map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated file: %v", err)
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal migrated file: %v", err)
	}
	if raw["schema_version"] != float64(currentRuleStoreSchemaVersion) {
		t.Fatalf("schema_version = %v, want %d", raw["schema_version"], currentRuleStoreSchemaVersion)
	}
	if _, ok := raw["accounts"]; !ok {
		t.Fatalf("migrated file missing accounts")
	}
	if _, ok := raw["rules"]; !ok {
		t.Fatalf("migrated file missing rules")
	}

	reloaded := newRulePresetStore(path)
	doc, err := reloaded.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if doc.ActiveID != "campaign" {
		t.Fatalf("legacy facade active_id = %q, want campaign", doc.ActiveID)
	}
	if len(doc.Presets) != 6 {
		t.Fatalf("legacy facade presets = %d, want account defaults + campaign", len(doc.Presets))
	}
	active, err = reloaded.active()
	if err != nil {
		t.Fatalf("active after reload: %v", err)
	}
	if active.Config["font_size"] != "17" {
		t.Fatalf("active font_size after reload = %v, want 17", active.Config["font_size"])
	}
}

func TestRulePresetStoreWritesRollbackReadableLegacyShadowFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	store := newRulePresetStore(path)
	if _, err := store.upsert(rulePreset{
		ID:         "campaign",
		Name:       "Campaign Rule",
		BaselineID: defaultRulePresetID,
		Config:     map[string]any{"font_size": "17"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.setActive("campaign"); err != nil {
		t.Fatalf("set active: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read v2 file: %v", err)
	}
	var rollbackDoc rulePresetDocument
	if err := json.Unmarshal(b, &rollbackDoc); err != nil {
		t.Fatalf("old backend unmarshal shape: %v", err)
	}
	if rollbackDoc.ActiveID != "campaign" {
		t.Fatalf("rollback active_id = %q, want campaign", rollbackDoc.ActiveID)
	}
	var found *rulePreset
	for i := range rollbackDoc.Presets {
		if rollbackDoc.Presets[i].ID == "campaign" {
			found = &rollbackDoc.Presets[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("rollback presets missing campaign: %#v", rollbackDoc.Presets)
	}
	if found.Config["font_size"] != "17" {
		t.Fatalf("rollback font_size = %v, want 17", found.Config["font_size"])
	}
}

func TestRulePresetHandlersKeepLegacyResponseShapeOnV2Store(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	path := filepath.Join(t.TempDir(), "rules.json")
	store := newRulePresetStore(path)
	if _, err := store.upsert(rulePreset{
		ID:         "campaign",
		Name:       "Campaign Rule",
		BaselineID: defaultRulePresetID,
		Config:     map[string]any{"font_size": "16"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	req := httptest.NewRequest(http.MethodGet, "/api/rules/admin/presets", nil)
	req.Header.Set("X-Admin-Password", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var docPayload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &docPayload); err != nil {
		t.Fatalf("unmarshal admin list: %v", err)
	}
	if _, ok := docPayload["active_id"]; !ok {
		t.Fatalf("admin list missing active_id")
	}
	if _, ok := docPayload["presets"]; !ok {
		t.Fatalf("admin list missing presets")
	}
	accounts, ok := docPayload["accounts"].([]any)
	// Now 5 accounts: fbif, wowfood, foodtalks-series, ft-default, ft-series (FoodTalks website)
	if !ok || len(accounts) != 5 {
		t.Fatalf("admin list accounts = %#v, want four account bindings", docPayload["accounts"])
	}
	if _, ok := docPayload["rules"]; ok {
		t.Fatalf("admin list leaked v2 rules")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/rules/active", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("active code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var activePayload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &activePayload); err != nil {
		t.Fatalf("unmarshal active: %v", err)
	}
	if _, ok := activePayload["preset"]; !ok {
		t.Fatalf("active response missing preset")
	}
	if _, ok := activePayload["account"]; ok {
		t.Fatalf("active response leaked account")
	}
	if _, ok := activePayload["rules"]; ok {
		t.Fatalf("active response leaked rules")
	}
}

func TestRulePresetAdminRejectsInvalidConfig(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	cases := []struct {
		name   string
		config map[string]any
	}{
		{
			name: "bad color",
			config: map[string]any{
				"text_color": "red",
			},
		},
		{
			name: "NaN number",
			config: map[string]any{
				"font_size": "NaN",
			},
		},
		{
			name: "font injection",
			config: map[string]any{
				"font_family": "PingFang';<img src=x>",
			},
		},
		{
			name: "unknown field",
			config: map[string]any{
				"font_size": "15",
				"html":      "<img src=x>",
			},
		},
		{
			name: "unknown module",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"surprise": map[string]any{"font_size": "16"},
				},
			},
		},
		{
			name: "field in wrong module",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"workflow": map[string]any{"font_size": "16"},
				},
			},
		},
		{
			name: "duplicate field in modules",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"theme":      map[string]any{"font_size": "16"},
					"supporting": map[string]any{"font_size": "17"},
				},
			},
		},
		{
			name: "bad more articles card href",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"more_articles": map[string]any{
						"default_cards": []any{
							map[string]any{
								"href": "javascript:alert(1)",
							},
						},
					},
				},
			},
		},
		{
			name: "bad more articles article path",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"more_articles": map[string]any{
						"default_cards": []any{
							map[string]any{
								"href": "https://mp.weixin.qq.com/not-an-article",
							},
						},
					},
				},
			},
		},
		{
			name: "bad more articles imgurl",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"more_articles": map[string]any{
						"default_cards": []any{
							map[string]any{
								"href":   "https://mp.weixin.qq.com/s/custom-a",
								"imgurl": "javascript:alert(1)",
							},
						},
					},
				},
			},
		},
		{
			name: "bad more articles imgurl host",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"more_articles": map[string]any{
						"default_cards": []any{
							map[string]any{
								"href":   "https://mp.weixin.qq.com/s/custom-a",
								"imgurl": "https://",
							},
						},
					},
				},
			},
		},
		{
			name: "bad more articles data image",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"more_articles": map[string]any{
						"default_cards": []any{
							map[string]any{
								"href":   "https://mp.weixin.qq.com/s/custom-a",
								"imgurl": "data:image/",
							},
						},
					},
				},
			},
		},
		{
			name: "bad more articles image_urls",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"more_articles": map[string]any{
						"default_cards": []any{
							map[string]any{
								"href":       "https://mp.weixin.qq.com/s/custom-a",
								"image_urls": []any{"https://mmbiz.qpic.cn/a.jpg", "javascript:alert(1)"},
							},
						},
					},
				},
			},
		},
		{
			name: "bad more articles protocol relative image_urls",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"more_articles": map[string]any{
						"default_cards": []any{
							map[string]any{
								"href":       "https://mp.weixin.qq.com/s/custom-a",
								"image_urls": []any{"//"},
							},
						},
					},
				},
			},
		},
		{
			name: "bad more articles title type",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"more_articles": map[string]any{
						"default_cards": []any{
							map[string]any{
								"href":  "https://mp.weixin.qq.com/s/custom-a",
								"title": map[string]any{"bad": "value"},
							},
						},
					},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(rulePreset{
				ID:         "bad-config",
				Name:       "Bad Config",
				BaselineID: defaultRulePresetID,
				Config:     tc.config,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Admin-Password", "secret")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid config code = %d, want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRulePresetAdminAcceptsThemeJustifyParagraphs(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	body, _ := json.Marshal(rulePreset{
		ID:           "theme-justify",
		Name:         "Theme Justify",
		BaseRevision: ruleRevisionPointer(0),
		BaselineID:   defaultRulePresetID,
		Config: map[string]any{
			"schema_version": float64(2),
			"modules": map[string]any{
				"theme": map[string]any{
					"font_size":          "15",
					"justify_paragraphs": true,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Password", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestRulePresetAcceptsMoreArticlesDefaultCards(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	path := filepath.Join(t.TempDir(), "rules.json")
	store := newRulePresetStore(path)
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	body, _ := json.Marshal(rulePreset{
		ID:           "custom-more-articles",
		Name:         "Custom More Articles",
		BaseRevision: ruleRevisionPointer(0),
		BaselineID:   defaultRulePresetID,
		Config: map[string]any{
			"schema_version": float64(2),
			"modules": map[string]any{
				"more_articles": map[string]any{
					"enabled":       false,
					"default_cards": validMoreArticlesDefaultCards(),
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Password", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("save code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	var doc rulePresetDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var found *rulePreset
	for i := range doc.Presets {
		if doc.Presets[i].ID == "custom-more-articles" {
			found = &doc.Presets[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("saved preset not found: %#v", doc.Presets)
	}
	if found.Config["more_articles_enabled"] != false {
		t.Fatalf("more_articles_enabled = %#v, want false", found.Config["more_articles_enabled"])
	}
	cards, ok := found.Config["more_articles_default_cards"].([]any)
	if !ok || len(cards) != 1 {
		t.Fatalf("default cards = %#v, want one card", found.Config["more_articles_default_cards"])
	}

	reloaded := newRulePresetStore(path)
	reloadedDoc, err := reloaded.list()
	if err != nil {
		t.Fatalf("reload list: %v", err)
	}
	found = nil
	for i := range reloadedDoc.Presets {
		if reloadedDoc.Presets[i].ID == "custom-more-articles" {
			found = &reloadedDoc.Presets[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("reloaded preset not found: %#v", reloadedDoc.Presets)
	}
	cards, ok = found.Config["more_articles_default_cards"].([]any)
	if !ok || len(cards) != 1 {
		t.Fatalf("reloaded default cards = %#v, want one card", found.Config["more_articles_default_cards"])
	}
	card, ok := cards[0].(map[string]any)
	if !ok {
		t.Fatalf("reloaded default card = %#v, want object", cards[0])
	}
	if card["href"] != "https://mp.weixin.qq.com/s/custom-a" {
		t.Fatalf("reloaded default card href = %#v, want custom-a", card["href"])
	}
}

func TestRulePresetStoreCapsSavedVersionsPerRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store := newRulePresetStore(path)
	for i := 0; i < maxRulePresetVersions+5; i++ {
		if _, err := store.upsert(rulePreset{
			ID:         "campaign",
			Name:       "Campaign Rule",
			BaselineID: defaultRulePresetID,
			Config:     map[string]any{"font_size": "16"},
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	var raw ruleStoreDocumentV2
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal store: %v", err)
	}
	if got := len(raw.Rules["campaign"].Versions); got != maxRulePresetVersions {
		t.Fatalf("versions = %d, want %d", got, maxRulePresetVersions)
	}
}

func TestDefaultRuleStoreDocumentIncludesFoodTalksSite(t *testing.T) {
	doc := defaultRuleStoreDocumentV2()

	siteAccount, ok := doc.Accounts[foodTalksSiteRuleAccountID]
	if !ok {
		t.Fatalf("expected FoodTalks site account %q in default doc", foodTalksSiteRuleAccountID)
	}
	if siteAccount.ActiveRuleID != foodTalksSiteDefaultRulePresetID {
		t.Errorf("FoodTalks site account active rule = %q, want %q",
			siteAccount.ActiveRuleID, foodTalksSiteDefaultRulePresetID)
	}
	if siteAccount.ActiveRuleVersion != 1 {
		t.Errorf("FoodTalks site account active version = %d, want 1", siteAccount.ActiveRuleVersion)
	}

	rule, ok := doc.Rules[foodTalksSiteDefaultRulePresetID]
	if !ok {
		t.Fatalf("expected rule definition %q in default doc", foodTalksSiteDefaultRulePresetID)
	}
	if len(rule.Versions) == 0 {
		t.Fatal("FoodTalks site rule has no versions")
	}
	v := rule.Versions[0]

	// 必须的 behavior key
	for _, k := range []string{"author_regex", "tip_regex", "ref_header_regex", "ref_item_regex"} {
		if _, ok := v.Behavior[k]; !ok {
			t.Errorf("missing behavior.%s", k)
		}
	}

	// 必须的 config key
	if _, ok := v.Config["modules"]; !ok {
		t.Error("missing config.modules")
	}
	modules, _ := v.Config["modules"].(map[string]any)
	if modules == nil {
		t.Fatal("config.modules is not a map")
	}
	if mctoc, ok := modules["mctoc"].(map[string]any); !ok {
		t.Error("modules.mctoc not present or not a map")
	} else if mctoc["enabled"] != true {
		t.Errorf("modules.mctoc.enabled = %v, want true", mctoc["enabled"])
	}
	if footer, ok := modules["fbif_footer"].(map[string]any); !ok {
		t.Error("modules.fbif_footer not present or not a map")
	} else if footer["enabled"] != false {
		t.Errorf("modules.fbif_footer.enabled = %v, want false", footer["enabled"])
	}
}

func TestRulePresetAdminAcceptsModularConfigAndProjectsFlatLegacyPreset(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	body, _ := json.Marshal(rulePreset{
		ID:           "modular",
		Name:         "Modular",
		BaseRevision: ruleRevisionPointer(0),
		BaselineID:   defaultRulePresetID,
		Config: map[string]any{
			"schema_version": float64(2),
			"font_size":      "15",
			"modules": map[string]any{
				"theme": map[string]any{
					"font_size": "16",
				},
				"workflow": map[string]any{
					"footer_enabled": false,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Password", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("modular config code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	if _, err := store.setActive("modular"); err != nil {
		t.Fatalf("setActive: %v", err)
	}
	active, err := store.active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active.Config["font_size"] != "16" {
		t.Fatalf("font_size = %v, want modular override 16", active.Config["font_size"])
	}
	if active.Config["footer_enabled"] != false {
		t.Fatalf("footer_enabled = %v, want false", active.Config["footer_enabled"])
	}
}

// 删除某账号当前激活的自定义 preset 后，该账号必须回落到【自己的】默认 preset，
// 不能悬空、不能串到别的账号默认（store.delete 的 per-account fallback 分支）。
// WHY：delete 是用户可触发的破坏性操作；fallback id 写错（如复制粘贴漏改）会让账号
// 静默绑到错误规则且 go test 全过——本测试钉死「删激活规则=回落到自己的默认」意图。
func TestRulePresetStoreDeleteFallsBackToAccountDefault(t *testing.T) {
	cases := []struct{ account, wantDefault string }{
		{"fbif", "default"},
		{"wowfood", "wowfood-default"},
		{"foodtalks-series", "foodtalks-series-default"},
		{"ft-default", "ft-default-v1"},
	}
	for _, c := range cases {
		t.Run(c.account, func(t *testing.T) {
			store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
			customID := c.account + "-custom"
			if _, err := store.upsert(rulePreset{
				ID:         customID,
				Name:       "临时自定义",
				BaselineID: c.wantDefault,
				Config:     map[string]any{"font_size": "16"},
			}); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if _, err := store.setActiveForAccount(c.account, customID); err != nil {
				t.Fatalf("setActiveForAccount: %v", err)
			}
			if got, err := store.activeForAccount(c.account); err != nil || got.ID != customID {
				t.Fatalf("active before delete = %q (err %v), want %q", got.ID, err, customID)
			}
			if _, err := store.delete(customID); err != nil {
				t.Fatalf("delete: %v", err)
			}
			got, err := store.activeForAccount(c.account)
			if err != nil {
				t.Fatalf("activeForAccount after delete: %v", err)
			}
			if got.ID != c.wantDefault {
				t.Fatalf("%s fallback = %q, want %q（删激活规则后必须回落到自己的默认，不得串号/悬空）",
					c.account, got.ID, c.wantDefault)
			}
		})
	}
}
