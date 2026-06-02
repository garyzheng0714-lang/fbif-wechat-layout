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

// defaultMpBottomBlocksModule 是公众号渠道的追加式区块默认种子。
// 模块默认关（enabled=false）→ 对现有公众号输出零改动（追加段整体不渲染）；
// 预置一个禁用的 name_card 作为运营起点（在后台/前台开启并改字段即可用）。
func defaultMpBottomBlocksModule() map[string]any {
	return map[string]any{
		"enabled": false,
		"order":   []any{"name_card"},
		"blocks": map[string]any{
			"name_card": map[string]any{
				"type":    "name_card",
				"enabled": false,
				"fields": map[string]any{
					"intro_text":  "找谁谁谁",
					"intro_size":  14,
					"intro_color": "#888888",
					"image":       "",
				},
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
	allowed := map[string]bool{"type": true, "enabled": true, "fields": true}
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
	if v, ok := block["enabled"]; ok {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("bottom_blocks block %q enabled must be boolean", id)
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

// validateBottomBlocksModule 校验区块模块结构（config.modules.bottom_blocks）。
func validateBottomBlocksModule(raw any) error {
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("bottom_blocks module must be an object")
	}
	allowed := map[string]bool{"enabled": true, "order": true, "blocks": true, "presets": true, "schema_version": true}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("unknown bottom_blocks field: %s", k)
		}
	}
	if v, ok := m["enabled"]; ok {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("bottom_blocks enabled must be boolean")
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
		for i, rp := range presets {
			p, ok := rp.(map[string]any)
			if !ok {
				return fmt.Errorf("bottom_blocks presets[%d] must be an object", i)
			}
			for k := range p {
				if k != "id" && k != "name" && k != "enabled" {
					return fmt.Errorf("bottom_blocks presets[%d] has unknown field: %s", i, k)
				}
			}
			if id := strings.TrimSpace(fmt.Sprint(p["id"])); id != "" && !assetLibraryID.MatchString(id) {
				return fmt.Errorf("bottom_blocks presets[%d].id is invalid", i)
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
	return nil
}
