package main

import (
	"strings"
	"testing"
)

func TestBlockTypeCatalogIntegrity(t *testing.T) {
	seen := map[string]bool{}
	for _, bt := range blockTypeCatalog() {
		if bt.Type == "" {
			t.Fatal("block type with empty Type")
		}
		if seen[bt.Type] {
			t.Fatalf("duplicate block type: %s", bt.Type)
		}
		seen[bt.Type] = true
		if len(bt.Channels) == 0 {
			t.Errorf("%s: must declare at least one channel", bt.Type)
		}
		if bt.Kind != "text" && bt.Kind != "image" && bt.Kind != "recommendations" {
			t.Errorf("%s: invalid kind %q (only text|image|recommendations)", bt.Type, bt.Kind)
		}
		for _, ch := range bt.Channels {
			if ch != "mp" && ch != "ft" {
				t.Errorf("%s: invalid channel %q (only mp|ft)", bt.Type, ch)
			}
		}
		fkeys := map[string]bool{}
		for _, f := range bt.Fields {
			if !isAllowedBlockFieldType(f.Type) {
				t.Errorf("%s.%s: field type %q not in whitelist", bt.Type, f.Key, f.Type)
			}
			if fkeys[f.Key] {
				t.Errorf("%s: duplicate field key %s", bt.Type, f.Key)
			}
			fkeys[f.Key] = true
		}
	}
}

// WHY：底部产品模型收敛为「文字 / 图片 / 推荐文章」三类；文字模块必须是跨公众号/网站共用的基础能力。
func TestTextBlockSupportsBasicFormatting(t *testing.T) {
	bt, ok := blockTypeByName("text")
	if !ok {
		t.Fatal("text block type missing")
	}
	if bt.Kind != "text" {
		t.Fatalf("text kind = %q, want text", bt.Kind)
	}
	want := map[string]string{
		"content":          "longText",
		"font_size":        "number",
		"text_color":       "color",
		"background_color": "color",
		"italic":           "switch",
		"underline":        "switch",
		"align":            "select",
	}
	got := map[string]string{}
	for _, f := range bt.Fields {
		got[f.Key] = f.Type
	}
	for k, typ := range want {
		if got[k] != typ {
			t.Errorf("text.%s: want field type %s, got %q", k, typ, got[k])
		}
	}
}

// 用户旗舰示例：名片必须是「字段化」的——上方文字 + 字号(带范围) + 颜色 + 名片图。
// WHY：核心诉求是「可编辑/可自定义」，名片是其示范。若有人把名片退回成裸 HTML，本测试应失败。
func TestNameCardIsFieldBased(t *testing.T) {
	bt, ok := blockTypeByName("name_card")
	if !ok {
		t.Fatal("name_card block type missing")
	}
	want := map[string]string{"intro_text": "text", "intro_size": "number", "intro_color": "color", "image": "image"}
	got := map[string]string{}
	for _, f := range bt.Fields {
		got[f.Key] = f.Type
		if f.Key == "intro_size" && (f.Min == nil || f.Max == nil) {
			t.Error("name_card.intro_size must carry min/max to keep font size in a safe range")
		}
	}
	for k, typ := range want {
		if got[k] != typ {
			t.Errorf("name_card.%s: want field type %s, got %q", k, typ, got[k])
		}
	}
}

// 防漂移：committed 的 block-registry.gen.js 必须与 blockTypeCatalog 生成结果逐字节一致。
// WHY：与启动期 fail-loud 校验同源——改了 catalog 不跑 npm run gen:blocks，前端就会读到旧 schema。
func TestBlockRegistryGeneratedFileInSync(t *testing.T) {
	if err := validateBlockRegistryConsistency("../public"); err != nil {
		if strings.HasPrefix(err.Error(), "warn:") {
			t.Skipf("skip (generated file not found): %v", err)
		}
		t.Fatalf("block-registry.gen.js out of sync — run `npm run gen:blocks`: %v", err)
	}
}
