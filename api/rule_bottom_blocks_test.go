package main

import (
	"reflect"
	"testing"
)

func bbBlock(t *testing.T, mod map[string]any, id string) map[string]any {
	t.Helper()
	blocks, _ := mod["blocks"].(map[string]any)
	b, ok := blocks[id].(map[string]any)
	if !ok {
		t.Fatalf("block %q missing in bottom_blocks", id)
	}
	return b
}

// 迁移必须逐段无损：每个页脚段变成 rich_html 区块，html_lines 字节级保留。
// WHY：P3 渲染器据此复刻当前页脚——迁移丢字符就等于页脚保真被破坏（红线 #20）。
func TestBottomBlocksMigrationLossless(t *testing.T) {
	footer := defaultFBIFFooterModule()
	mod := bottomBlocksFromFooterModule(footer)

	wantOrder := []any{"biz_coop", "join_group", "follow_account", "logo", "more_info"}
	if !reflect.DeepEqual(mod["order"], wantOrder) {
		t.Fatalf("order = %v, want %v", mod["order"], wantOrder)
	}
	segments := footer["segments"].(map[string]any)
	for _, raw := range wantOrder {
		id := raw.(string)
		b := bbBlock(t, mod, id)
		if b["type"] != "rich_html" {
			t.Errorf("block %q type = %v, want rich_html", id, b["type"])
		}
		if b["enabled"] != true {
			t.Errorf("block %q enabled = %v, want true", id, b["enabled"])
		}
		gotLines := b["fields"].(map[string]any)["html_lines"]
		wantLines := segments[id].(map[string]any)["html_lines"]
		if !reflect.DeepEqual(gotLines, wantLines) {
			t.Errorf("block %q html_lines not preserved:\n got=%v\nwant=%v", id, gotLines, wantLines)
		}
	}
	// tip 不进区块模型（网站走正文流 buildSiteFooterTipElem），即使 presets 里引用了它。
	if _, ok := mod["blocks"].(map[string]any)["tip"]; ok {
		t.Error("tip must NOT migrate into bottom_blocks")
	}
}

func TestBottomBlocksMigrationPresets(t *testing.T) {
	mod := bottomBlocksFromFooterModule(defaultFBIFFooterModule())
	byID := map[string]map[string]any{}
	for _, rp := range mod["presets"].([]any) {
		p := rp.(map[string]any)
		byID[p["id"].(string)] = p["enabled"].(map[string]any)
	}
	orig := byID["fbif-original"]
	for _, id := range []string{"biz_coop", "join_group", "follow_account", "logo", "more_info"} {
		if orig[id] != true {
			t.Errorf("fbif-original preset %q = %v, want true", id, orig[id])
		}
	}
	if _, ok := orig["tip"]; ok {
		t.Error("preset enabled map must exclude tip")
	}
	repost := byID["repost-simple"]
	if repost["biz_coop"] != true || repost["join_group"] != false || repost["logo"] != false {
		t.Errorf("repost-simple mapping wrong: %v", repost)
	}
}

func TestDefaultBottomBlocksModuleValid(t *testing.T) {
	if err := validateBottomBlocksModule(defaultBottomBlocksModule()); err != nil {
		t.Fatalf("default bottom_blocks module should validate: %v", err)
	}
}

func TestValidateBottomBlocksRejectsBad(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown type":          {"blocks": map[string]any{"x": map[string]any{"type": "no_such_type"}}},
		"bad block id":          {"blocks": map[string]any{"Bad ID!": map[string]any{"type": "rich_html"}}},
		"order ghost ref":       {"order": []any{"ghost"}},
		"number above max":      {"blocks": map[string]any{"c": map[string]any{"type": "name_card", "fields": map[string]any{"intro_size": float64(999)}}}},
		"insecure image url":    {"blocks": map[string]any{"c": map[string]any{"type": "name_card", "fields": map[string]any{"image": "http://insecure.example/x.png"}}}},
		"undeclared field":      {"blocks": map[string]any{"c": map[string]any{"type": "name_card", "fields": map[string]any{"nope": "x"}}}},
		"enabled wrong type":    {"enabled": "yes"},
		"unknown module field":  {"weird": true},
		// 审计修复回归：重复 order、预设悬空引用、html_lines 非字符串元素均须拒绝。
		"duplicate order":       {"blocks": map[string]any{"a": map[string]any{"type": "rich_html"}, "b": map[string]any{"type": "rich_html"}}, "order": []any{"a", "a"}},
		"preset ghost key":      {"blocks": map[string]any{"a": map[string]any{"type": "rich_html"}}, "presets": []any{map[string]any{"id": "p1", "name": "x", "enabled": map[string]any{"ghost": true}}}},
		"html_lines non-string": {"blocks": map[string]any{"c": map[string]any{"type": "rich_html", "fields": map[string]any{"html_lines": []any{1, true}}}}},
	}
	for name, c := range cases {
		if err := validateBottomBlocksModule(c); err == nil {
			t.Errorf("case %q should be rejected", name)
		}
	}
}

// name_card 图片字段接受素材库占位符（{{asset#id}}）与站内路径，合法 number 通过。
func TestValidateBottomBlocksAcceptsValid(t *testing.T) {
	ok := map[string]any{"blocks": map[string]any{
		"c": map[string]any{"type": "name_card", "fields": map[string]any{
			"image":      "{{asset#ft_footer_logo}}",
			"intro_size": float64(16),
			"intro_text": "联系 Eileen",
		}},
	}}
	if err := validateBottomBlocksModule(ok); err != nil {
		t.Fatalf("valid name_card should pass: %v", err)
	}
}
