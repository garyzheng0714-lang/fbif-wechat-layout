package main

import (
	"reflect"
	"strings"
	"testing"
)

func bbBlock(t *testing.T, mod map[string]any, id string) map[string]any {
	t.Helper()
	b, ok := bbActiveBlocks(t, mod)[id].(map[string]any)
	if !ok {
		t.Fatalf("block %q missing in bottom_blocks", id)
	}
	return b
}

// bbActiveBlocks 取 v2 单预设的 blocks（网站/系列默认已转 v2 presets[]）。
func bbActiveBlocks(t *testing.T, mod map[string]any) map[string]any {
	t.Helper()
	presets, _ := mod["presets"].([]any)
	if len(presets) == 0 {
		t.Fatal("bottom_blocks 应为 v2 presets 结构")
	}
	p, _ := presets[0].(map[string]any)
	blocks, _ := p["blocks"].(map[string]any)
	return blocks
}

// 迁移必须逐段无损：每个页脚段变成 rich_html 区块，html_lines 字节级保留。
// WHY：P3 渲染器据此复刻当前页脚——迁移丢字符就等于页脚保真被破坏（红线 #20）。
func TestBottomBlocksMigrationLossless(t *testing.T) {
	footer := defaultFBIFFooterModule()
	mod := bottomBlocksFromFooterModule(footer)

	presets, _ := mod["presets"].([]any)
	if len(presets) == 0 {
		t.Fatal("迁移后应为 v2 presets 结构")
	}
	preset := presets[0].(map[string]any)
	wantOrder := []any{"biz_coop", "join_group", "follow_account", "logo", "more_info"}
	if !reflect.DeepEqual(preset["order"], wantOrder) {
		t.Fatalf("order = %v, want %v", preset["order"], wantOrder)
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
	if _, ok := bbActiveBlocks(t, mod)["tip"]; ok {
		t.Error("tip must NOT migrate into bottom_blocks")
	}
}

// 网站页脚迁移后是 v2 单预设（default）：4 套快捷预设（fbif-original/repost-simple/
// tip-only/none）是前端面板概念（site.html FALLBACK_PRESETS，走 ft.footer.segments 勾选），
// 不进 bottom_blocks presets[]；段显隐由前端 override.overrides[id].enabled 控制。
func TestBottomBlocksMigrationPresets(t *testing.T) {
	mod := bottomBlocksFromFooterModule(defaultFBIFFooterModule())
	if mod["schema_version"] != 2 {
		t.Fatalf("schema_version = %v, want 2", mod["schema_version"])
	}
	if mod["active_preset_id"] != "default" {
		t.Errorf("active_preset_id = %v, want default", mod["active_preset_id"])
	}
	presets, _ := mod["presets"].([]any)
	if len(presets) != 1 {
		t.Fatalf("网站 v2 应为单预设，got %d 个", len(presets))
	}
	p := presets[0].(map[string]any)
	if p["id"] != "default" {
		t.Errorf("单预设 id = %v, want default", p["id"])
	}
	// 顶层不得残留 v1 的 blocks/order（v2 数据全在 presets[] 内）。
	if _, ok := mod["blocks"]; ok {
		t.Error("v2 不应有顶层 blocks")
	}
	if _, ok := mod["order"]; ok {
		t.Error("v2 不应有顶层 order")
	}
}

// FBIF v2 原子双预设样板必须通过 v2 校验（结构合法）。视觉 1:1 由 JS 渲染 golden + 公众号真机确认。
func TestDefaultMpBottomBlocksModuleV2Valid(t *testing.T) {
	if err := validateBottomBlocksModule(defaultMpBottomBlocksModuleV2()); err != nil {
		t.Fatalf("FBIF v2 bottom_blocks module should validate: %v", err)
	}
}

// Go 侧防漂移：锁住 v2 样板的顺序/三条文案/图宽（codex review #1/#2/#3/#5）。
// 与 tests/mp-blocks-fidelity.test.mjs 的 JS 渲染断言双侧守，避免 Go 默认与 JS 测试漂移。
func TestDefaultMpBottomBlocksModuleV2Shape(t *testing.T) {
	mod := defaultMpBottomBlocksModuleV2()
	presets := mod["presets"].([]any)
	if len(presets) != 2 {
		t.Fatalf("v2 应有 2 个预设(原创/转载), got %d", len(presets))
	}
	original := presets[0].(map[string]any)
	if original["id"] != "original" {
		t.Fatalf("首个预设应为 original, got %v", original["id"])
	}
	// 顺序对齐 footer.html：提示 → 名片 → 更多文章 → 1000标题 → 1000图
	wantOrder := []any{"tip_title", "sp1", "tip_body", "sp2", "byline", "more_articles", "sp_ma", "promo_title", "sp4", "promo_img"}
	if !reflect.DeepEqual(original["order"], wantOrder) {
		t.Errorf("original order 漂移:\n got=%v\nwant=%v", original["order"], wantOrder)
	}
	blocks := original["blocks"].(map[string]any)
	tipBody := blocks["tip_body"].(map[string]any)["fields"].(map[string]any)["content"].(string)
	for _, frag := range []string{"本文为FBIF原创", "转载请在后台回复", "FBIF投稿与商务合作"} {
		if !strings.Contains(tipBody, frag) {
			t.Errorf("original tip_body 缺文案 %q（codex #2）", frag)
		}
	}
	byline := blocks["byline"].(map[string]any)["fields"].(map[string]any)
	if byline["width"] != 661 {
		t.Errorf("byline 图宽应 661（codex #3）, got %v", byline["width"])
	}
	// 转载预设两条、不含原创
	repostBody := presets[1].(map[string]any)["blocks"].(map[string]any)["tip_body"].(map[string]any)["fields"].(map[string]any)["content"].(string)
	if strings.Contains(repostBody, "本文为FBIF原创") {
		t.Error("repost tip_body 不应含原创文案")
	}
	if !strings.Contains(repostBody, "本文为转载") {
		t.Error("repost tip_body 应含转载文案")
	}
}

func TestDefaultBottomBlocksModuleValid(t *testing.T) {
	if err := validateBottomBlocksModule(defaultBottomBlocksModule()); err != nil {
		t.Fatalf("default bottom_blocks module should validate: %v", err)
	}
	if err := validateBottomBlocksModule(defaultMpBottomBlocksModuleV2()); err != nil {
		t.Fatalf("mp v2 bottom_blocks module should validate: %v", err)
	}
	if err := validateBottomBlocksModule(defaultSeriesBottomBlocksModule()); err != nil {
		t.Fatalf("series bottom_blocks module should validate: %v", err)
	}
}

func TestSeriesBottomBlocksExposeFrontPanelImageSlots(t *testing.T) {
	// WHY：FoodTalks 系列文章每篇都可能需要替换作者名片、BD 名片和入群二维码；
	// 若默认块未标 frontend_editable，左侧内容面板不会出现换图入口，运营无法完成单篇替换。
	blocks := bbActiveBlocks(t, defaultSeriesBottomBlocksModule())
	for _, id := range []string{"author_card", "bd_card", "group_qr"} {
		block, ok := blocks[id].(map[string]any)
		if !ok {
			t.Fatalf("series block %q missing", id)
		}
		if block["frontend_editable"] != true {
			t.Fatalf("series block %q frontend_editable = %v, want true", id, block["frontend_editable"])
		}
	}
}

// 系列底部默认只能用 foodtalks-series.js 的 seriesBlockRenderers 真正实现的块类型
// （image / more_articles / text / spacer）。守护「校验通过但渲染器没实现 → block-render 静默跳过」
// 这类回归：曾经系列默认放了 spacer 块但渲染器无 spacer renderer，导致图间距整段静默丢失
// （已于 b0bf5ee 补 spacer renderer）。系列已转 v2 单预设：从 presets[0].blocks 取块类型校验。
// 新增系列块类型前，必须先在 seriesBlockRenderers 实现对应 renderer 并更新此白名单。
func TestSeriesBottomBlocksOnlyRenderableTypes(t *testing.T) {
	renderable := map[string]bool{"image": true, "more_articles": true, "text": true, "spacer": true}
	mod := defaultSeriesBottomBlocksModule()
	presets, ok := mod["presets"].([]any)
	if !ok || len(presets) == 0 {
		t.Fatal("系列默认应为 v2 presets 结构（schema_version=2，单预设 default）")
	}
	preset, _ := presets[0].(map[string]any)
	blocks, ok := preset["blocks"].(map[string]any)
	if !ok {
		t.Fatal("系列 v2 预设应含 blocks")
	}
	for id, raw := range blocks {
		b, _ := raw.(map[string]any)
		typ, _ := b["type"].(string)
		if !renderable[typ] {
			t.Errorf("系列底部块 %q 类型 %q 无 seriesBlockRenderers 实现，会被 block-render 静默跳过", id, typ)
		}
	}
}

func TestValidateBottomBlocksRejectsBad(t *testing.T) {
	// 包进 v2 单预设的「块/order 坏」——触发 preset 内 validateBlocksAndOrderMaps/Block 细粒度校验
	// （删 v1 分支后，坏数据须在 v2 preset 内被拒，而非靠缺 schema_version 一刀切拦下）。
	inPreset := func(blocks map[string]any, order []any) map[string]any {
		return map[string]any{
			"schema_version": float64(2), "enabled": true, "active_preset_id": "p",
			"presets": []any{map[string]any{"id": "p", "order": order, "blocks": blocks}},
		}
	}
	richBlk := map[string]any{"a": map[string]any{"type": "rich_html"}}
	withPreset := []any{map[string]any{"id": "p", "order": []any{"a"}, "blocks": richBlk}}
	cases := map[string]map[string]any{
		// 模块级坏
		"missing schema_version":   {"enabled": true, "presets": withPreset},
		"wrong schema_version":     {"schema_version": float64(1), "presets": []any{}},
		"enabled wrong type":       {"schema_version": float64(2), "enabled": "yes", "presets": withPreset},
		"top-level gap retired":    {"schema_version": float64(2), "gap": "huge", "presets": []any{}},
		"top-level blocks retired": {"schema_version": float64(2), "blocks": richBlk},
		"unknown module field":     {"schema_version": float64(2), "weird": true},
		"empty presets enabled":    {"schema_version": float64(2), "enabled": true, "presets": []any{}},
		"active_preset ghost":      {"schema_version": float64(2), "active_preset_id": "ghost", "presets": withPreset},
		// preset 内块/order 坏（细粒度校验）
		"unknown type":          inPreset(map[string]any{"x": map[string]any{"type": "no_such_type"}}, []any{"x"}),
		"bad block id":          inPreset(map[string]any{"Bad ID!": map[string]any{"type": "rich_html"}}, []any{"Bad ID!"}),
		"order ghost ref":       inPreset(richBlk, []any{"ghost"}),
		"number above max":      inPreset(map[string]any{"c": map[string]any{"type": "image", "fields": map[string]any{"width": float64(9999)}}}, []any{"c"}),
		"insecure image url":    inPreset(map[string]any{"c": map[string]any{"type": "image", "fields": map[string]any{"image": "http://insecure.example/x.png"}}}, []any{"c"}),
		"undeclared field":      inPreset(map[string]any{"c": map[string]any{"type": "image", "fields": map[string]any{"nope": "x"}}}, []any{"c"}),
		"display non-string":    inPreset(map[string]any{"c": map[string]any{"type": "image", "display_name": 123, "fields": map[string]any{"image": ""}}}, []any{"c"}),
		"display too long":      inPreset(map[string]any{"c": map[string]any{"type": "image", "display_name": string(make([]rune, 81)), "fields": map[string]any{"image": ""}}}, []any{"c"}),
		"duplicate order":       inPreset(richBlk, []any{"a", "a"}),
		"html_lines non-string": inPreset(map[string]any{"c": map[string]any{"type": "rich_html", "fields": map[string]any{"html_lines": []any{1, true}}}}, []any{"c"}),
	}
	for name, c := range cases {
		if err := validateBottomBlocksModule(c); err == nil {
			t.Errorf("case %q should be rejected", name)
		}
	}
}

// 图片块的图片字段接受素材库占位符（{{asset#id}}）与站内路径，合法 number 通过。
func TestValidateBottomBlocksAcceptsValid(t *testing.T) {
	ok := map[string]any{
		"schema_version":   float64(2),
		"enabled":          true,
		"active_preset_id": "default",
		"presets": []any{map[string]any{
			"id":    "default",
			"order": []any{"c", "t"},
			"blocks": map[string]any{
				"c": map[string]any{"type": "image", "display_name": "商务图", "fields": map[string]any{
					"image":  "{{asset#ft_footer_logo}}",
					"width":  float64(160),
					"center": true,
				}},
				"t": map[string]any{"type": "text", "display_name": "提示文字", "fields": map[string]any{
					"content":          "欢迎入群",
					"font_size":        float64(18),
					"text_color":       "#396FBC",
					"background_color": "",
					"italic":           true,
					"underline":        true,
					"align":            "center",
				}},
			},
		}},
	}
	if err := validateBottomBlocksModule(ok); err != nil {
		t.Fatalf("valid bottom blocks should pass: %v", err)
	}
}

// v2 双预设：schema_version=2 时 presets[] 各带完整 order/blocks（原创/转载各一套）。
func TestValidateBottomBlocksAcceptsV2(t *testing.T) {
	mod := map[string]any{
		"schema_version":   float64(2),
		"enabled":          true,
		"active_preset_id": "original",
		"presets": []any{
			map[string]any{
				"id": "original", "name": "原创",
				"order": []any{"note", "card"},
				"blocks": map[string]any{
					"note": map[string]any{"type": "text", "fields": map[string]any{"content": "原创声明"}},
					"card": map[string]any{"type": "image", "display_name": "名片", "fields": map[string]any{"image": "{{asset#y}}"}},
				},
			},
			map[string]any{
				"id": "repost", "name": "转载",
				"order": []any{"note"},
				"blocks": map[string]any{
					"note": map[string]any{"type": "text", "fields": map[string]any{"content": "转载声明"}},
				},
			},
		},
	}
	if err := validateBottomBlocksModule(mod); err != nil {
		t.Fatalf("v2 bottom_blocks module should validate: %v", err)
	}
}

// v2 拒绝：未知预设字段 / 预设内坏块类型 / 预设内 order 悬空 / 非法 schema_version。
func TestValidateBottomBlocksV2RejectsBad(t *testing.T) {
	cases := map[string]map[string]any{
		"v2 preset unknown field": {
			"schema_version": float64(2),
			"presets":        []any{map[string]any{"id": "p1", "weird": true}},
		},
		"v2 preset bad block type": {
			"schema_version": float64(2),
			"presets":        []any{map[string]any{"id": "p1", "blocks": map[string]any{"a": map[string]any{"type": "no_such"}}}},
		},
		"v2 preset order ghost": {
			"schema_version": float64(2),
			"presets":        []any{map[string]any{"id": "p1", "blocks": map[string]any{"a": map[string]any{"type": "text"}}, "order": []any{"ghost"}}},
		},
		"bad schema_version":      {"schema_version": float64(3)},
		"v2 empty presets":        {"schema_version": float64(2), "presets": []any{}},
		"v2 missing presets":      {"schema_version": float64(2)},
		"v2 preset empty blocks":  {"schema_version": float64(2), "presets": []any{map[string]any{"id": "p1", "order": []any{"a"}, "blocks": map[string]any{}}}},
		"v2 preset empty order":   {"schema_version": float64(2), "presets": []any{map[string]any{"id": "p1", "order": []any{}, "blocks": map[string]any{"a": map[string]any{"type": "text"}}}}},
		"v2 top-level blocks":     {"schema_version": float64(2), "blocks": map[string]any{}, "presets": []any{map[string]any{"id": "p1", "order": []any{"a"}, "blocks": map[string]any{"a": map[string]any{"type": "text"}}}}},
		"v2 top-level gap":        {"schema_version": float64(2), "gap": "blank_line", "presets": []any{map[string]any{"id": "p1", "order": []any{"a"}, "blocks": map[string]any{"a": map[string]any{"type": "text"}}}}},
		"v2 bad active_preset_id": {"schema_version": float64(2), "active_preset_id": "ghost", "presets": []any{map[string]any{"id": "p1", "order": []any{"a"}, "blocks": map[string]any{"a": map[string]any{"type": "text"}}}}},
	}
	for name, c := range cases {
		if err := validateBottomBlocksModule(c); err == nil {
			t.Errorf("v2 case %q should be rejected", name)
		}
	}
}

// v2 容错：schema_version 为字符串 "2" 也应被识别并接受（前后端一致）。
func TestValidateBottomBlocksV2AcceptsStringSchemaVersion(t *testing.T) {
	mod := map[string]any{
		"schema_version":   "2",
		"active_preset_id": "original",
		"presets": []any{
			map[string]any{"id": "original", "order": []any{"a"}, "blocks": map[string]any{"a": map[string]any{"type": "text"}}},
		},
	}
	if err := validateBottomBlocksModule(mod); err != nil {
		t.Fatalf("string schema_version '2' should validate: %v", err)
	}
}
