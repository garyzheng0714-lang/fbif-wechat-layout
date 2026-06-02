package main

import (
	"strings"
	"testing"
)

// 意图：素材库是规则数据的一部分，默认种子必须能通过服务端保存校验；
// 否则线上 BuiltinVersion 覆盖后会把默认规则写成不可保存状态。
func TestValidateDefaultAssetLibraryModule(t *testing.T) {
	if err := validateAssetLibraryModule(defaultAssetLibraryModule()); err != nil {
		t.Fatalf("默认 asset_library 应通过校验，却报错: %v", err)
	}
	cfg := defaultRuleConfig()
	if err := validateRuleConfig(cfg); err != nil {
		t.Fatalf("含默认 asset_library 的默认规则应通过校验，却报错: %v", err)
	}
}

// 意图：FoodTalks「什么是」系列已有 18 群/名片/序号图素材；阶段 1 的总素材库
// 必须把它们纳入同一资产池，同时用 foodtalks-series scope 保持账号隔离。
func TestDefaultAssetLibraryIncludesSeriesMaterials(t *testing.T) {
	lib := defaultAssetLibraryModule()
	assets, _ := lib["assets"].([]any)
	var sections, groups, cards int
	for _, raw := range assets {
		asset, _ := raw.(map[string]any)
		id := strings.TrimSpace(asset["id"].(string))
		scope := strings.TrimSpace(asset["scope"].(string))
		if !strings.HasPrefix(id, "ft_series_") {
			continue
		}
		if scope != foodTalksRuleAccountID {
			t.Fatalf("系列素材 %s scope=%q，期望 %s", id, scope, foodTalksRuleAccountID)
		}
		switch {
		case strings.HasPrefix(id, "ft_series_section_"):
			sections++
		case strings.HasPrefix(id, "ft_series_group_"):
			groups++
		case strings.HasPrefix(id, "ft_series_card_"):
			cards++
		}
	}
	if sections != 12 || groups != 18 || cards != 15 {
		t.Fatalf("系列素材数量不完整：sections=%d groups=%d cards=%d", sections, groups, cards)
	}
}

func TestAssetLibraryRejectsUnsafeOrAmbiguousAssets(t *testing.T) {
	cases := []struct {
		name string
		mod  map[string]any
	}{
		{
			name: "duplicate id",
			mod: map[string]any{"assets": []any{
				map[string]any{"id": "same", "type": "image", "url": "https://static.foodtalks.cn/a.png", "scope": "global", "usage": "logo"},
				map[string]any{"id": "same", "type": "image", "url": "https://static.foodtalks.cn/b.png", "scope": "global", "usage": "logo"},
			}},
		},
		{
			name: "unsafe url",
			mod: map[string]any{"assets": []any{
				map[string]any{"id": "bad_url", "type": "image", "url": `javascript:alert(1)`, "scope": "global", "usage": "logo"},
			}},
		},
		{
			name: "unknown scope",
			mod: map[string]any{"assets": []any{
				map[string]any{"id": "bad_scope", "type": "image", "url": "https://static.foodtalks.cn/a.png", "scope": "ghost", "usage": "logo"},
			}},
		},
		{
			name: "unknown usage",
			mod: map[string]any{"assets": []any{
				map[string]any{"id": "bad_usage", "type": "image", "url": "https://static.foodtalks.cn/a.png", "scope": "global", "usage": "surprise"},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateAssetLibraryModule(tc.mod); err == nil {
				t.Fatal("非法素材库应被拒绝")
			}
		})
	}
}

// 意图：页脚迁移为 {{asset:usage}} 引用后，默认 html_lines 不再内联 CDN URL；
// 保真由前端 resolveAssetRefs 在渲染期恢复。
func TestDefaultFooterUsesAssetReferences(t *testing.T) {
	footer := defaultFBIFFooterModule()
	segments := footer["segments"].(map[string]any)
	joinGroup := segments["join_group"].(map[string]any)
	lines := joinGroup["html_lines"].([]any)
	foundQR := false
	for _, line := range lines {
		s := line.(string)
		if s == `<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefUsage("qr_group")+`" alt="00" width="600" height="267" /></p>` {
			foundQR = true
		}
		if strings.Contains(s, ftFooterGroupQR) {
			t.Fatal("默认页脚不应再把二维码 URL 内联在 html_lines")
		}
	}
	if !foundQR {
		t.Fatal("默认页脚应通过 {{asset:qr_group}} 引用加群二维码")
	}
}
