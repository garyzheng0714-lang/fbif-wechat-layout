package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// 「格式清理」（2026-10-27）：浏览器打开这个账号拿到的规则里，底部一律关着、没有 FBIF 的模块与 Mote 行为。
func TestWechatPlainActiveRuleHasNoFooterOrInheritedModules(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rules/active?account=wechat-plain", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("active code = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Preset struct {
			ID         string         `json:"id"`
			RuleFamily string         `json:"rule_family"`
			Config     map[string]any `json:"config"`
			Behavior   map[string]any `json:"behavior"`
		} `json:"preset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	p := payload.Preset
	if p.ID != "wechat-plain-default" || p.RuleFamily != "wechat-plain" {
		t.Fatalf("preset = %q family = %q, want wechat-plain-default / wechat-plain", p.ID, p.RuleFamily)
	}
	if p.Config["footer_enabled"] != false || p.Config["more_articles_enabled"] != false {
		t.Fatalf("footer_enabled=%v more_articles_enabled=%v, want both false", p.Config["footer_enabled"], p.Config["more_articles_enabled"])
	}
	if cards, _ := p.Config["more_articles_default_cards"].([]any); len(cards) != 0 {
		t.Fatalf("more_articles_default_cards = %v, want empty", p.Config["more_articles_default_cards"])
	}
	mods, _ := p.Config["modules"].(map[string]any)
	for _, id := range []string{"bottom_blocks", "article_footer_defaults"} {
		mod, _ := mods[id].(map[string]any)
		if mod == nil || mod["enabled"] != false {
			t.Fatalf("modules.%s = %v, want enabled=false", id, mods[id])
		}
	}
	if _, ok := mods["asset_library"]; ok {
		t.Fatalf("modules carries FBIF asset_library: %v", mods["asset_library"])
	}
	for _, key := range []string{"conditions", "actions"} {
		if list, _ := p.Behavior[key].([]any); len(list) != 0 {
			t.Fatalf("behavior.%s = %v, want empty (no Mote rules)", key, p.Behavior[key])
		}
	}
}

// 每个账号的 TemplateID 都得有渲染器文件：app.html 按 /js/templates/<templateId>.js 加载。
func TestAccountTemplatesExist(t *testing.T) {
	for _, m := range accountCatalog() {
		path := filepath.Join("..", "public", "js", "templates", m.TemplateID+".js")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("账号 %s 的模板 %s 不存在：%v", m.ID, path, err)
		}
	}
}
