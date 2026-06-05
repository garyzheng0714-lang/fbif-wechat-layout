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

// truthyRuleValue 把任意值判定为布尔（兼容 true / 1 / "1" / "true"）。
func truthyRuleValue(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		return s == "1" || s == "true" || s == "yes" || s == "on"
	default:
		return false
	}
}

// bottomBlocksFromFooterModule 把历史页脚库模块(fbif_footer)无损迁移成区块模块：
//   - 每个页脚段 → 一个 rich_html 区块（html_lines 原样保留）；
//   - segment_order → order（排除 tip——tip 在网站走正文流 buildSiteFooterTipElem，不属 buildFooterHtml 渲染范围）；
//   - presets 的 segment 开关 → 区块 enabled 开关（同样排除 tip）。
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

	presets := []any{}
	if rawPresets, ok := footer["presets"].([]any); ok {
		for _, rp := range rawPresets {
			p, ok := rp.(map[string]any)
			if !ok {
				continue
			}
			segs, _ := p["segments"].(map[string]any)
			enabled := map[string]any{}
			for _, raw := range order {
				id := fmt.Sprint(raw)
				enabled[id] = truthyRuleValue(segs[id])
			}
			presets = append(presets, map[string]any{
				"id":      p["id"],
				"name":    p["name"],
				"enabled": enabled,
			})
		}
	}

	enabled := true
	if v, ok := footer["enabled"].(bool); ok {
		enabled = v
	}
	return map[string]any{
		"enabled": enabled,
		"order":   order,
		"blocks":  blocks,
		"presets": presets,
	}
}

// defaultBottomBlocksModule 是区块模块的服务端默认种子：由页脚库默认无损迁移而来，
// 保留 enabled=false 等当前语义（页脚默认关，由运营在前台/后台开启）。
func defaultBottomBlocksModule() map[string]any {
	return bottomBlocksFromFooterModule(defaultFBIFFooterModule())
}

// defaultMpBottomBlocksModule 是公众号(FBIF)底部区块默认布局：由 footer.html 的 4 块逆向而来
// （提示声明 declaration / 波波名片 name_card / 更多文章 more_articles / 1000案例 titled_media）。
// 图字段引用素材库 {{asset#id}}。模块 enabled 暂为 false：待 engine footer 链路改由 bottom_blocks
// engine footer 链路已通过 template.renderBottomFooter 接管（替代 footer.html）；
// 渲染保真由 tests/mp-blocks-fidelity.test.mjs golden 守。
func defaultMpBottomBlocksModule() map[string]any {
	return map[string]any{
		"enabled": true,
		"gap":     "blank_line",
		"order":   []any{"tip", "byline", "more_articles", "promo"},
		"blocks": map[string]any{
			"tip": map[string]any{
				"type":         "declaration",
				"display_name": "提示声明",
				"enabled":      true,
				"fields": map[string]any{
					"title": "提示：",
					"lines": []any{
						`<!--REPOST_BULLET1_START-->`,
						`<p style="margin:0 8px;padding:0;"><span style="color:rgb(136, 136, 136);font-size:15px;font-family:mp-quote, &quot;PingFang SC&quot;, system-ui, -apple-system, BlinkMacSystemFont, &quot;Helvetica Neue&quot;, &quot;Hiragino Sans GB&quot;, &quot;Microsoft YaHei UI&quot;, &quot;Microsoft YaHei&quot;, Arial, sans-serif;line-height:1.75;letter-spacing:0.034em;font-style:normal;font-weight:normal;">* 本文为FBIF原创，欢迎转发朋友圈；</span></p>`,
						`<!--REPOST_BULLET1_END--><!--REPOST_BULLET2_START-->`,
						`<p style="margin:0 8px;padding:0;"><span style="color:rgb(136, 136, 136);font-size:15px;font-family:mp-quote, &quot;PingFang SC&quot;, system-ui, -apple-system, BlinkMacSystemFont, &quot;Helvetica Neue&quot;, &quot;Hiragino Sans GB&quot;, &quot;Microsoft YaHei UI&quot;, &quot;Microsoft YaHei&quot;, Arial, sans-serif;line-height:1.75;letter-spacing:0.034em;font-style:normal;font-weight:normal;">* 转载请在后台回复“转载”了解规则；</span></p>`,
						`<!--REPOST_BULLET2_END-->`,
						`<p style="margin:0 8px;padding:0;"><span style="color:rgb(136, 136, 136);font-size:15px;font-family:mp-quote, &quot;PingFang SC&quot;, system-ui, -apple-system, BlinkMacSystemFont, &quot;Helvetica Neue&quot;, &quot;Hiragino Sans GB&quot;, &quot;Microsoft YaHei UI&quot;, &quot;Microsoft YaHei&quot;, Arial, sans-serif;line-height:1.75;letter-spacing:0.034em;font-style:normal;font-weight:normal;">* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）</span></p>`,
					},
				},
			},
			"byline": map[string]any{
				"type":         "name_card",
				"display_name": "Bobo 名片",
				"enabled":      true,
				"fields": map[string]any{
					"intro_text": "",
					"image":      "{{asset#fbif_bobo_card}}",
				},
			},
			"more_articles": map[string]any{
				"type":         "more_articles",
				"display_name": "更多文章",
				"enabled":      true,
				"fields": map[string]any{
					"title": "/ 更多文章 /",
				},
			},
			"promo": map[string]any{
				"type":         "titled_media",
				"display_name": "1000 个食品创新案例",
				"enabled":      true,
				"fields": map[string]any{
					"title": "/ 1000个食品创新案例 /",
					"image": "{{asset#fbif_promo_1000}}",
				},
			},
		},
		"presets": []any{},
	}
}

// defaultMpBottomBlocksModuleV2 是 FBIF 公众号底部的 v2 原子双预设版本（原创/转载各一套）：
// 把 v1 的 declaration/name_card/titled_media 重写成「文字(text)+图片(image)+间距(spacer)+推荐(more_articles)」
// 四原子，视觉复刻 footer.html。原创/转载差异仅在提示正文(tip_body)文案，由 isRepost→presetId 自动选。
// 间距复刻 footer.html：提示标题↓8、提示正文↓16(名片图上)、名片图↓20(1000标题上)、1000标题↓20(图上)；
// more_articles 标题间距内化进其渲染器，故 1000图与 more_articles 之间不放独立 spacer（spec §5）。
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
			"sp_ma":         spacer(20),
			"promo_title":   textBlock("1000 案例标题", "/ 1000个食品创新案例 /", true, 17, "#544545", "center"),
			"sp4":           spacer(20),
			"promo_img":     imageBlock("1000 案例图", "{{asset#fbif_promo_1000}}", 661),
		}
	}
	// 顺序对齐 footer.html：提示 → 名片 → 更多文章 → 1000标题 → 1000图（codex review #1；
	// more_articles 标题间距内化，byline 后不放 spacer）。
	order := []any{"tip_title", "sp1", "tip_body", "sp2", "byline", "more_articles", "sp_ma", "promo_title", "sp4", "promo_img"}
	// 原创三条（含「转载请在后台回复」，codex review #2），转载两条。
	originalBody := textBlock("提示正文", "* 本文为FBIF原创，欢迎转发朋友圈；\n* 转载请在后台回复“转载”了解规则；\n* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）", false, 15, "#888888", "left")
	repostBody := textBlock("提示正文", "* 本文为转载，不代表FBIF立场。\n* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）", false, 15, "#888888", "left")
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

// defaultSeriesBottomBlocksModuleV2 是 FoodTalks「什么是」系列底部的 v2 原子版本：系列底部全是满宽
// 图片块（作者名片/BD名片/群二维码）+ 可显隐的更多文章，无原创/转载声明差异，故双预设同内容（保持
// 4 原子模型一致、presetId 映射直接命中）。作者名片 frontend_editable（红线#14 例外：系列作者随篇不同，
// 运营单篇换图）。间距 spacer 高度为占位值，切生产前须公众号后台复制确认（CLAUDE.md UI 协议）。
func defaultSeriesBottomBlocksModuleV2() map[string]any {
	spacer := func(h int) map[string]any {
		return map[string]any{"type": "spacer", "display_name": "间距", "enabled": true, "fields": map[string]any{"height": h}}
	}
	imageBlock := func(name, asset string, frontendEditable bool) map[string]any {
		b := map[string]any{"type": "image", "display_name": name, "enabled": true, "fields": map[string]any{"image": asset}}
		if frontendEditable {
			b["frontend_editable"] = true
		}
		return b
	}
	blocks := func() map[string]any {
		return map[string]any{
			"author_card":   imageBlock("作者名片", "", true), // 作者默认空，运营单篇从素材库点选（frontend_editable）
			"sp1":           spacer(16),
			"bd_card":       imageBlock("BD 名片", "{{asset#ft_series_card_eileen}}", false),
			"sp2":           spacer(16),
			"group_qr":      imageBlock("包装群二维码", "{{asset#ft_series_group_packaging}}", false),
			"sp3":           spacer(16),
			"more_articles": map[string]any{"type": "more_articles", "display_name": "推荐文章", "enabled": false, "fields": map[string]any{"title": "/ 更多文章 /"}},
		}
	}
	order := []any{"author_card", "sp1", "bd_card", "sp2", "group_qr", "sp3", "more_articles"}
	return map[string]any{
		"schema_version":   2,
		"enabled":          true,
		"active_preset_id": "original",
		"presets": []any{
			map[string]any{"id": "original", "name": "原创", "order": order, "blocks": blocks()},
			map[string]any{"id": "repost", "name": "转载", "order": order, "blocks": blocks()},
		},
	}
}

// defaultSeriesBottomBlocksModule 是 FoodTalks「什么是」系列底部默认布局：把原 buildSeriesFooterElems
// 的「作者名片 → BD 名片 → 群二维码」3 张满宽 banner 表达成 image 区块（引用素材库 {{asset#id}}），
// 外加一个默认隐藏、可显隐的「更多文章」区块——让系列与 FBIF 共用同一 bottom_blocks 结构，
// 差异只在默认值（红线#2：规则是可复用资产）。默认选择对齐 defaultFtSeriesMaterialsModule 的
// footer_default：author 留空（运营单篇点选）、bd=Eileen、group=包装。系列必须显式覆盖此模块，
// 否则经 mergeRuleDefaults 继承 FBIF 基线的 defaultMpBottomBlocksModule → 错渲 FBIF 页脚。
// 渲染走系列专属 renderBottomFooter（foodtalks-series.js），字节口径区别于 FBIF（满宽 100% 而非 661px）。
func defaultSeriesBottomBlocksModule() map[string]any {
	return map[string]any{
		"enabled": true,
		"gap":     "blank_line",
		"order":   []any{"author_card", "bd_card", "group_qr", "more_articles"},
		"blocks": map[string]any{
			"author_card": map[string]any{
				"type":              "image",
				"display_name":      "作者名片",
				"enabled":           true,
				"frontend_editable": true,                        // 系列作者名片随篇不同，前端放开换图（运营单篇从素材库点选）
				"fields":            map[string]any{"image": ""}, // 默认空：运营单篇从素材库点选作者名片
			},
			"bd_card": map[string]any{
				"type":         "image",
				"display_name": "BD 名片",
				"enabled":      true,
				"fields":       map[string]any{"image": "{{asset#ft_series_card_eileen}}"},
			},
			"group_qr": map[string]any{
				"type":         "image",
				"display_name": "包装群二维码",
				"enabled":      true,
				"fields":       map[string]any{"image": "{{asset#ft_series_group_packaging}}"},
			},
			"more_articles": map[string]any{
				"type":         "more_articles",
				"display_name": "推荐文章",
				"enabled":      false, // 系列默认不出更多文章；结构里保留，运营可显隐（用户诉求：结构统一）
				"fields":       map[string]any{"title": "/ 更多文章 /"},
			},
		},
		"presets": []any{},
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
		for i, ln := range lines {
			s, ok := ln.(string)
			if !ok {
				return fmt.Errorf("bottom_blocks block %q field %q line %d must be a string", blockID, key, i)
			}
			if len([]rune(s)) > 4000 {
				return fmt.Errorf("bottom_blocks block %q field %q line %d too long", blockID, key, i)
			}
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
	allowed := map[string]bool{"enabled": true, "gap": true, "order": true, "blocks": true, "presets": true, "schema_version": true, "active_preset_id": true}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("unknown bottom_blocks field: %s", k)
		}
	}
	// schema_version：缺省 1（顶层 order/blocks，gap 控空行）；2=双预设（presets[] 各带 order/blocks，间距走 spacer）。
	schemaVersion := 1
	if v, ok := m["schema_version"]; ok {
		n := ruleBehaviorNumber(v)
		if n != 1 && n != 2 {
			return fmt.Errorf("bottom_blocks schema_version must be 1 or 2")
		}
		schemaVersion = int(n)
	}
	if schemaVersion >= 2 {
		// v2：顶层 blocks/order/gap 无意义（数据全在 presets[] 内），存在即拒——强制干净，迁移负责清理顶层。
		for _, k := range []string{"blocks", "order", "gap"} {
			if _, ok := m[k]; ok {
				return fmt.Errorf("bottom_blocks schema_version 2 must not carry top-level %q (use presets[])", k)
			}
		}
		// presets 必须非空（否则前端 activeLayer 落空、底部静默消失）。
		if ps, _ := m["presets"].([]any); len(ps) == 0 {
			return fmt.Errorf("bottom_blocks schema_version 2 requires non-empty presets")
		}
	}
	if v, ok := m["enabled"]; ok {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("bottom_blocks enabled must be boolean")
		}
	}
	if v, ok := m["gap"]; ok {
		gap := strings.TrimSpace(fmt.Sprint(v))
		if gap != "none" && gap != "blank_line" {
			return fmt.Errorf("bottom_blocks gap must be none or blank_line")
		}
	}

	blocks := map[string]any{}
	if rawBlocks, ok := m["blocks"]; ok && rawBlocks != nil {
		blocks, ok = rawBlocks.(map[string]any)
		if !ok {
			return fmt.Errorf("bottom_blocks blocks must be an object")
		}
		if len(blocks) > 100 {
			return fmt.Errorf("bottom_blocks has too many blocks")
		}
		for id, b := range blocks {
			if err := validateBottomBlocksBlock(id, b); err != nil {
				return err
			}
		}
	}

	if rawOrder, ok := m["order"]; ok && rawOrder != nil {
		order, ok := rawOrder.([]any)
		if !ok {
			return fmt.Errorf("bottom_blocks order must be an array")
		}
		if len(order) > 100 {
			return fmt.Errorf("bottom_blocks order has too many entries")
		}
		seenOrder := map[string]bool{}
		for _, raw := range order {
			id := strings.TrimSpace(fmt.Sprint(raw))
			if _, ok := blocks[id]; !ok {
				return fmt.Errorf("bottom_blocks order references unknown block: %s", id)
			}
			if seenOrder[id] {
				return fmt.Errorf("bottom_blocks order has duplicate block: %s", id)
			}
			seenOrder[id] = true
		}
	}

	if rawPresets, ok := m["presets"]; ok && rawPresets != nil {
		presets, ok := rawPresets.([]any)
		if !ok {
			return fmt.Errorf("bottom_blocks presets must be an array")
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
			if schemaVersion >= 2 {
				// v2：每个预设是一套完整 blocks+order（原创/转载各一套，间距走 spacer）。
				for k := range p {
					if k != "id" && k != "name" && k != "order" && k != "blocks" {
						return fmt.Errorf("bottom_blocks presets[%d] has unknown field: %s", i, k)
					}
				}
				// blocks/order 必须非空，否则该预设渲染出空底部（codex review）。
				if bm, _ := p["blocks"].(map[string]any); len(bm) == 0 {
					return fmt.Errorf("bottom_blocks presets[%d] requires non-empty blocks", i)
				}
				if om, _ := p["order"].([]any); len(om) == 0 {
					return fmt.Errorf("bottom_blocks presets[%d] requires non-empty order", i)
				}
				if err := validateBlocksAndOrderMaps(p["blocks"], p["order"]); err != nil {
					return fmt.Errorf("bottom_blocks presets[%d]: %w", i, err)
				}
			} else {
				// v1：enabled map 预设（页脚库迁移而来，开关引用顶层 blocks）。
				for k := range p {
					if k != "id" && k != "name" && k != "enabled" {
						return fmt.Errorf("bottom_blocks presets[%d] has unknown field: %s", i, k)
					}
				}
				if en, ok := p["enabled"]; ok && en != nil {
					enMap, ok := en.(map[string]any)
					if !ok {
						return fmt.Errorf("bottom_blocks presets[%d].enabled must be an object", i)
					}
					// preset 里引用的区块必须真实存在（与 order 校验对齐，防悬空引用）。
					for blockID := range enMap {
						if _, ok := blocks[blockID]; !ok {
							return fmt.Errorf("bottom_blocks presets[%d].enabled references unknown block: %s", i, blockID)
						}
					}
				}
			}
		}
		// v2：active_preset_id 必须引用存在的预设，否则 fallback 首个会把转载渲成原创（codex review）。
		if schemaVersion >= 2 {
			if apid, ok := m["active_preset_id"]; ok {
				s := strings.TrimSpace(fmt.Sprint(apid))
				if s != "" && !presetIDs[s] {
					return fmt.Errorf("bottom_blocks active_preset_id references unknown preset: %s", s)
				}
			}
		}
	}
	return nil
}
