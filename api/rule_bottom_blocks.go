package main

import (
	"fmt"
	"strings"
)

// rule_bottom_blocks.go — 底部区块模块（config.modules.bottom_blocks）的服务端默认 / 迁移 / 校验。
//
// 区块模块是「页脚库(fbif_footer)」的统一泛化：把"一段 HTML 段"升级为"带 type+字段的区块"。
// 历史页脚段无损迁移成 rich_html 区块（html_lines 原样保留），新类型(name_card 等)再叠加。
// 区块类型 schema 的单一真相在 block_type_catalog.go（blockTypeCatalog）。
//
// 边界：渲染（type→HTML）由各渠道渲染器在 P3 实现；本文件只产出/校验数据，不拼 HTML。

// bottomBlocksFromFooterModule 把历史页脚库模块(fbif_footer)迁移成 v2 单预设区块模块：
//   - 每个页脚段 → 一个 rich_html 区块（html_lines 原样保留）；
//   - segment_order → 单预设 default 的 order（排除 tip——tip 在网站走正文流
//     buildSiteFooterTipElem，不属 buildFooterHtml 渲染范围）；
//   - 页脚「4 套快捷预设」(fbif-original/repost-simple/tip-only/none) 是前端面板概念
//     （site.html FALLBACK_PRESETS / footerLib.presets，走 ft.footer.segments 勾选），
//     不进 bottom_blocks presets[]；段显隐由前端 override.overrides[id].enabled 控制。
//
// 同时作为运行时 fallback：旧规则只有 fbif_footer、缺 bottom_blocks 时现场迁移（spec §10）。
func bottomBlocksFromFooterModule(footer map[string]any) map[string]any {
	if footer == nil {
		footer = map[string]any{}
	}
	segments, _ := footer["segments"].(map[string]any)
	if segments == nil {
		segments = map[string]any{}
	}
	orderRaw, _ := footer["segment_order"].([]any)

	order := []any{}
	blocks := map[string]any{}
	for _, raw := range orderRaw {
		id := strings.TrimSpace(fmt.Sprint(raw))
		if id == "" || id == "tip" {
			continue
		}
		if _, ok := blocks[id]; ok {
			continue
		}
		seg, ok := segments[id]
		if !ok {
			continue
		}
		var lines []any
		switch s := seg.(type) {
		case map[string]any:
			if hl, ok := s["html_lines"].([]any); ok {
				lines = hl
			}
		case []any:
			lines = s
		}
		if lines == nil {
			lines = []any{}
		}
		blocks[id] = map[string]any{
			"type":    "rich_html",
			"enabled": true,
			"fields":  map[string]any{"html_lines": lines},
		}
		order = append(order, id)
	}

	enabled := true
	if v, ok := footer["enabled"].(bool); ok {
		enabled = v
	}
	return map[string]any{
		"schema_version":   2,
		"enabled":          enabled,
		"active_preset_id": "default",
		"presets": []any{
			map[string]any{
				"id":     "default",
				"name":   "页脚",
				"order":  order,
				"blocks": blocks,
			},
		},
	}
}

// defaultBottomBlocksModule 是区块模块的服务端默认种子：由页脚库默认无损迁移而来，
// 保留 enabled=false 等当前语义（页脚默认关，由运营在前台/后台开启）。
func defaultBottomBlocksModule() map[string]any {
	return bottomBlocksFromFooterModule(defaultFBIFFooterModule())
}

// defaultMpBottomBlocksModuleV2 是 FBIF 公众号底部的 v2 原子双预设版本（原创/转载各一套）：
// 把 v1 的 declaration/name_card/titled_media 重写成「文字(text)+图片(image)+间距(spacer)+推荐(more_articles)」
// 四原子，视觉复刻 footer.html。原创/转载差异仅在提示正文(tip_body)文案，由 isRepost→presetId 自动选。
// 间距复刻 footer.html：提示标题↓8、提示正文↓16(名片图上)；
// more_articles 标题间距内化进其渲染器，名片图后不放独立 spacer。
// 注意：此为 v2 样板，切换生产（BuiltinVersion）前须在公众号后台复制确认视觉 1:1（CLAUDE.md UI 协议）。
func defaultMpBottomBlocksModuleV2() map[string]any {
	textBlock := func(name, content string, bold bool, size int, color, align string) map[string]any {
		return map[string]any{
			"type": "text", "display_name": name, "enabled": true,
			"fields": map[string]any{"content": content, "bold": bold, "font_size": size, "text_color": color, "align": align},
		}
	}
	spacer := func(h int) map[string]any {
		return map[string]any{"type": "spacer", "display_name": "间距", "enabled": true, "fields": map[string]any{"height": h}}
	}
	imageBlock := func(name, asset string, width int) map[string]any {
		return map[string]any{"type": "image", "display_name": name, "enabled": true, "fields": map[string]any{"image": asset, "width": width}}
	}
	// 原创/转载共用块（除 tip_body 文案外完全一致）。图宽 661 对齐 footer.html 的满宽图（codex review #3）。
	sharedBlocks := func(tipBody map[string]any) map[string]any {
		return map[string]any{
			"tip_title":     textBlock("提示标题", "提示：", true, 17, "#544545", "left"),
			"sp1":           spacer(8),
			"tip_body":      tipBody,
			"sp2":           spacer(16),
			"byline":        imageBlock("Bobo 名片", "{{asset#fbif_bobo_card}}", 661),
			"more_articles": map[string]any{"type": "more_articles", "display_name": "更多文章", "enabled": true, "fields": map[string]any{"title": "/ 更多文章 /"}},
		}
	}
	// 顺序对齐当前 FBIF 底部契约：提示 → 名片 → 更多文章。
	// 旧 1000 案例推广块已下线，不再作为默认排版输出。
	order := []any{"tip_title", "sp1", "tip_body", "sp2", "byline", "more_articles"}
	// 原创三条（含「转载请在后台回复」，codex review #2），转载两条。
	originalBody := textBlock("提示正文", "* 本文为FBIF原创，欢迎转发朋友圈；\n* 转载请在后台回复“转载”了解规则；\n* 投稿与商务合作请在后台回复“商务合作”。", false, 15, "#888888", "left")
	repostBody := textBlock("提示正文", "* 本文为转载文章。\n* 投稿与商务合作请在后台回复“商务合作”。", false, 15, "#888888", "left")
	return map[string]any{
		"schema_version":   2,
		"enabled":          true,
		"active_preset_id": "original",
		"presets": []any{
			map[string]any{"id": "original", "name": "原创", "order": order, "blocks": sharedBlocks(originalBody)},
			map[string]any{"id": "repost", "name": "转载", "order": order, "blocks": sharedBlocks(repostBody)},
		},
	}
}

// defaultSeriesBottomBlocksModule 是 FoodTalks「什么是」系列底部默认布局（v2 单预设）：把
// 作者名片 → BD 名片 → 群二维码 3 张满宽 banner 表达成 image 区块（引用素材库 {{asset#id}}），
// 外加默认隐藏、可显隐的「更多文章」。系列与 FBIF 共用同一 bottom_blocks 结构，差异只在默认值
// （红线#2：规则是可复用资产）。系列无原创/转载之分，单预设 id=default；renderBottomFooter 传
// presetId=original 在 activeLayer 找不到时 fallback 到首个预设（default）。块间距走 image 块自带
// margin（SERIES_LAYOUT.gap，golden 20px）——v2 无 blank_line 自动空段，对齐 series-blocks-fidelity
// golden 的设计口径。系列必须显式覆盖此模块，否则经 mergeRuleDefaults 继承 FBIF 基线
// defaultMpBottomBlocksModuleV2 → 错渲 FBIF 页脚。渲染走系列专属 renderBottomFooter
// （foodtalks-series.js），满宽 100% 区别于 FBIF 661px。
func defaultSeriesBottomBlocksModule() map[string]any {
	return map[string]any{
		"schema_version":   2,
		"enabled":          true,
		"active_preset_id": "default",
		"presets": []any{
			map[string]any{
				"id":    "default",
				"name":  "系列页脚",
				"order": []any{"author_card", "bd_card", "group_qr", "more_articles"},
				"blocks": map[string]any{
					"author_card": map[string]any{
						"type":              "image",
						"display_name":      "作者名片",
						"enabled":           true,
						"frontend_editable": true,
						"fields":            map[string]any{"image": ""},
					},
					"bd_card": map[string]any{
						"type":              "image",
						"display_name":      "BD 名片",
						"enabled":           true,
						"frontend_editable": true,
						"fields":            map[string]any{"image": "{{asset#ft_series_card_eileen}}"},
					},
					"group_qr": map[string]any{
						"type":              "image",
						"display_name":      "包装群二维码",
						"enabled":           true,
						"frontend_editable": true,
						"fields":            map[string]any{"image": "{{asset#ft_series_group_packaging}}"},
					},
					"more_articles": map[string]any{
						"type":         "more_articles",
						"display_name": "推荐文章",
						"enabled":      false,
						"fields":       map[string]any{"title": "/ 更多文章 /"},
					},
				},
			},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 校验

func isSafeBlockImageValue(v string) bool {
	s := strings.TrimSpace(v)
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "{{asset") && strings.HasSuffix(s, "}}") {
		return true // 素材库占位符，由 resolveAssetRefs 解析
	}
	return isSafeRuleBehaviorURL(s)
}

func validateBottomBlocksField(blockID string, bt blockType, key string, value any) error {
	var field *blockField
	for i := range bt.Fields {
		if bt.Fields[i].Key == key {
			field = &bt.Fields[i]
			break
		}
	}
	if field == nil {
		return fmt.Errorf("bottom_blocks block %q: field %q not declared for type %q", blockID, key, bt.Type)
	}
	switch field.Type {
	case "number":
		n := ruleBehaviorNumber(value)
		if field.Min != nil && n < float64(*field.Min) {
			return fmt.Errorf("bottom_blocks block %q field %q below min", blockID, key)
		}
		if field.Max != nil && n > float64(*field.Max) {
			return fmt.Errorf("bottom_blocks block %q field %q above max", blockID, key)
		}
	case "switch":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("bottom_blocks block %q field %q must be boolean", blockID, key)
		}
	case "color":
		s := strings.TrimSpace(fmt.Sprint(value))
		if len([]rune(s)) > 32 {
			return fmt.Errorf("bottom_blocks block %q field %q color too long", blockID, key)
		}
	case "image":
		if !isSafeBlockImageValue(fmt.Sprint(value)) {
			return fmt.Errorf("bottom_blocks block %q field %q must be empty, an asset placeholder, or a safe URL", blockID, key)
		}
	case "text", "select":
		if len([]rune(fmt.Sprint(value))) > 500 {
			return fmt.Errorf("bottom_blocks block %q field %q too long", blockID, key)
		}
	case "longText":
		if len([]rune(fmt.Sprint(value))) > 5000 {
			return fmt.Errorf("bottom_blocks block %q field %q too long", blockID, key)
		}
	case "rich_text":
		if len([]rune(fmt.Sprint(value))) > 20000 {
			return fmt.Errorf("bottom_blocks block %q field %q too long", blockID, key)
		}
		if err := validateRichTextHTML(fmt.Sprint(value)); err != nil {
			return fmt.Errorf("bottom_blocks block %q field %q: %w", blockID, key, err)
		}
	case "html_lines":
		lines, ok := value.([]any)
		if !ok {
			return fmt.Errorf("bottom_blocks block %q field %q must be an array of HTML lines", blockID, key)
		}
		if len(lines) > 200 {
			return fmt.Errorf("bottom_blocks block %q field %q has too many lines", blockID, key)
		}
		combinedLines := make([]string, 0, len(lines))
		for i, ln := range lines {
			s, ok := ln.(string)
			if !ok {
				return fmt.Errorf("bottom_blocks block %q field %q line %d must be a string", blockID, key, i)
			}
			if len([]rune(s)) > 4000 {
				return fmt.Errorf("bottom_blocks block %q field %q line %d too long", blockID, key, i)
			}
			if err := validateFooterHTML(s); err != nil {
				return fmt.Errorf("bottom_blocks block %q field %q line %d: %w", blockID, key, i, err)
			}
			combinedLines = append(combinedLines, s)
		}
		if err := validateFooterHTML(strings.Join(combinedLines, "\n")); err != nil {
			return fmt.Errorf("bottom_blocks block %q field %q: %w", blockID, key, err)
		}
	}
	return nil
}

func validateBottomBlocksBlock(id string, raw any) error {
	if !assetLibraryID.MatchString(id) {
		return fmt.Errorf("bottom_blocks block id %q is invalid", id)
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("bottom_blocks block %q must be an object", id)
	}
	allowed := map[string]bool{"type": true, "display_name": true, "enabled": true, "fields": true, "frontend_editable": true}
	for k := range block {
		if !allowed[k] {
			return fmt.Errorf("bottom_blocks block %q has unknown field: %s", id, k)
		}
	}
	typ := strings.TrimSpace(fmt.Sprint(block["type"]))
	bt, ok := blockTypeByName(typ)
	if !ok {
		return fmt.Errorf("bottom_blocks block %q has unknown type: %s", id, typ)
	}
	if v, ok := block["display_name"]; ok {
		name, ok := v.(string)
		if !ok {
			return fmt.Errorf("bottom_blocks block %q display_name must be string", id)
		}
		if len([]rune(name)) > 80 {
			return fmt.Errorf("bottom_blocks block %q display_name too long", id)
		}
	}
	if v, ok := block["enabled"]; ok {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("bottom_blocks block %q enabled must be boolean", id)
		}
	}
	// frontend_editable：块实例级「允许前端编辑此块」（缺省 false=前端只读）。按账号差异化，
	// 落服务端默认（rule_defaults.go），不进全账号一致的 catalog（红线#22）。
	if v, ok := block["frontend_editable"]; ok {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("bottom_blocks block %q frontend_editable must be boolean", id)
		}
	}
	if rawFields, ok := block["fields"]; ok && rawFields != nil {
		fields, ok := rawFields.(map[string]any)
		if !ok {
			return fmt.Errorf("bottom_blocks block %q fields must be an object", id)
		}
		for key, value := range fields {
			if err := validateBottomBlocksField(id, bt, key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateBlocksAndOrderMaps 校验一组 blocks(map) + order(array)——顶层(v1)与 preset 内(v2)共用。
// 块级校验复用 validateBottomBlocksBlock；order 引用必须指向 blocks 内存在且不重复的 id。
func validateBlocksAndOrderMaps(blocksRaw, orderRaw any) error {
	blocks := map[string]any{}
	if blocksRaw != nil {
		b, ok := blocksRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("bottom_blocks blocks must be an object")
		}
		blocks = b
		if len(blocks) > 100 {
			return fmt.Errorf("bottom_blocks has too many blocks")
		}
		for id, bl := range blocks {
			if err := validateBottomBlocksBlock(id, bl); err != nil {
				return err
			}
		}
	}
	if orderRaw != nil {
		order, ok := orderRaw.([]any)
		if !ok {
			return fmt.Errorf("bottom_blocks order must be an array")
		}
		if len(order) > 100 {
			return fmt.Errorf("bottom_blocks order has too many entries")
		}
		seen := map[string]bool{}
		for _, raw := range order {
			id := strings.TrimSpace(fmt.Sprint(raw))
			if _, ok := blocks[id]; !ok {
				return fmt.Errorf("bottom_blocks order references unknown block: %s", id)
			}
			if seen[id] {
				return fmt.Errorf("bottom_blocks order has duplicate block: %s", id)
			}
			seen[id] = true
		}
	}
	return nil
}

// validateBottomBlocksModule 校验区块模块结构（config.modules.bottom_blocks）。
func validateBottomBlocksModule(raw any) error {
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("bottom_blocks module must be an object")
	}
	allowed := map[string]bool{"enabled": true, "presets": true, "schema_version": true, "active_preset_id": true}
	for k := range m {
		if !allowed[k] {
			// 顶层 blocks/order/gap 是已退役的 v1 单布局格式（数据全在 presets[] 内）。
			if k == "blocks" || k == "order" || k == "gap" {
				return fmt.Errorf("bottom_blocks 顶层 %q 已退役（v1 单布局），数据须放 presets[]", k)
			}
			return fmt.Errorf("unknown bottom_blocks field: %s", k)
		}
	}
	// 统一 v2 打包：schema_version 必须为 2（v1 顶层 order/blocks 单布局已退役删除）。
	if v, ok := m["schema_version"]; !ok || ruleBehaviorNumber(v) != 2 {
		return fmt.Errorf("bottom_blocks requires schema_version 2")
	}
	moduleEnabled := true
	if v, ok := m["enabled"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return fmt.Errorf("bottom_blocks enabled must be boolean")
		}
		moduleEnabled = b
	}
	rawPresets, hasPresets := m["presets"]
	if !hasPresets || rawPresets == nil {
		if moduleEnabled {
			return fmt.Errorf("bottom_blocks schema_version 2 requires non-empty presets")
		}
		return nil // 关闭的空模块合法（如 WowFood 显式关页脚）
	}
	presets, ok := rawPresets.([]any)
	if !ok {
		return fmt.Errorf("bottom_blocks presets must be an array")
	}
	if moduleEnabled && len(presets) == 0 {
		return fmt.Errorf("bottom_blocks schema_version 2 requires non-empty presets")
	}
	if len(presets) > 50 {
		return fmt.Errorf("bottom_blocks has too many presets")
	}
	presetIDs := map[string]bool{}
	for i, rp := range presets {
		p, ok := rp.(map[string]any)
		if !ok {
			return fmt.Errorf("bottom_blocks presets[%d] must be an object", i)
		}
		pid := strings.TrimSpace(fmt.Sprint(p["id"]))
		if pid != "" && !assetLibraryID.MatchString(pid) {
			return fmt.Errorf("bottom_blocks presets[%d].id is invalid", i)
		}
		presetIDs[pid] = true
		for k := range p {
			if k != "id" && k != "name" && k != "order" && k != "blocks" {
				return fmt.Errorf("bottom_blocks presets[%d] has unknown field: %s", i, k)
			}
		}
		// enabled 模块的预设必须有非空 blocks/order（否则渲染空底部，codex review）；
		// 关闭模块（moduleEnabled=false）豁免——反正不渲染。
		if moduleEnabled {
			if bm, _ := p["blocks"].(map[string]any); len(bm) == 0 {
				return fmt.Errorf("bottom_blocks presets[%d] requires non-empty blocks", i)
			}
			if om, _ := p["order"].([]any); len(om) == 0 {
				return fmt.Errorf("bottom_blocks presets[%d] requires non-empty order", i)
			}
		}
		if err := validateBlocksAndOrderMaps(p["blocks"], p["order"]); err != nil {
			return fmt.Errorf("bottom_blocks presets[%d]: %w", i, err)
		}
	}
	if apid, ok := m["active_preset_id"]; ok {
		s := strings.TrimSpace(fmt.Sprint(apid))
		if s != "" && !presetIDs[s] {
			return fmt.Errorf("bottom_blocks active_preset_id references unknown preset: %s", s)
		}
	}
	return nil
}
