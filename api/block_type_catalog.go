package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// block_type_catalog.go — 底部区块类型注册表【单一真相源】。
//
// 沿用 account_catalog.go 的「单源 + codegen + 启动字节校验」防漂移范式：
//   - blockTypeCatalog() 是区块类型/字段/默认值/约束/适用渠道的唯一真相；
//   - `go run . -emit-blocks`（npm run gen:blocks）从它生成 public/js/blocks/block-registry.gen.js；
//   - 启动期 validateBlockRegistryConsistency 读生成文件与它逐字节比对，漂移即 fail loud。
//
// 「schema 是数据、渲染是代码」：本表只声明字段 schema（中性、可序列化）；
// 把字段渲染成 HTML 的逻辑分别落在各渠道渲染器（mp-blocks.js / site-blocks.js），
// 互不 import，公众号与网站渲染器物理隔离（红线 #19）。
//
// 新增区块类型 = 在 blockTypeCatalog() 加一条 + 跑 npm run gen:blocks（+ 在各渠道渲染器补 type→html_lines），
// 启动校验兜底。运营在规则后台按 schema 自动生成的表单填字段，不写代码。

// blockFieldType 是字段控件类型的白名单。新增类型需同步前端控件映射与 validateBottomBlocksField。
func isAllowedBlockFieldType(t string) bool {
	switch t {
	case "text", "longText", "number", "color", "image", "select", "switch", "html_lines":
		return true
	default:
		return false
	}
}

type blockFieldOption struct {
	Value string
	Label string
}

type blockField struct {
	Key     string
	Label   string
	Type    string // 见 isAllowedBlockFieldType
	Default any
	Tooltip string
	Min     *int // 仅 number
	Max     *int // 仅 number
	Options []blockFieldOption
}

type blockType struct {
	Type     string
	Label    string
	Channels []string // mp | ft
	Fields   []blockField
	Note     string // 生成文件时写在条目前的注释
}

func intp(n int) *int { return &n }

// blockTypeCatalog 是底部区块类型的单一真相源。
// P1 初始三类，覆盖三种原型：原始 HTML（rich_html，历史段无损迁移目标）、
// 字段化区块（name_card，用户旗舰示例）、委托渲染（more_articles，委托 more-articles.js）。
// 后续阶段按需增补 qr_card / link_text / image / tip 等（仅需在此加条目 + 渠道渲染器）。
func blockTypeCatalog() []blockType {
	return []blockType{
		{
			Type:     "rich_html",
			Label:    "自定义内容",
			Channels: []string{"mp", "ft"},
			Note:     "原始 HTML 段（html_lines）。承接历史页脚段的无损迁移，也是高级用户兜底类型。",
			Fields: []blockField{
				{Key: "html_lines", Label: "HTML 内容", Type: "html_lines", Default: []any{}, Tooltip: "每行一个 HTML 段；属高级编辑，运营一般无需改动。"},
			},
		},
		{
			Type:     "name_card",
			Label:    "名片",
			Channels: []string{"mp", "ft"},
			Note:     "字段化名片：上方一行说明文字（可改文案/字号/颜色）+ 名片图。",
			Fields: []blockField{
				{Key: "intro_text", Label: "上方文字", Type: "text", Default: "找谁谁谁", Tooltip: "名片上方显示的一行说明文字。"},
				{Key: "intro_size", Label: "文字字号", Type: "number", Default: 14, Min: intp(12), Max: intp(28), Tooltip: "上方文字字号（纯数字，无需单位）。"},
				{Key: "intro_color", Label: "文字颜色", Type: "color", Default: "#888888", Tooltip: "上方文字颜色。"},
				{Key: "image", Label: "名片图", Type: "image", Default: "", Tooltip: "从素材库选择名片图片。"},
			},
		},
		{
			Type:     "more_articles",
			Label:    "更多文章",
			Channels: []string{"mp"},
			Note:     "委托现有 more-articles.js 渲染推荐卡片；卡片在「更多文章」编辑器/侧栏管理，本区块只控制是否启用与排序。",
			Fields:   []blockField{},
		},
	}
}

func blockTypeByName(name string) (blockType, bool) {
	for _, t := range blockTypeCatalog() {
		if t.Type == name {
			return t, true
		}
	}
	return blockType{}, false
}

// ─────────────────────────────────────────────────────────────────────────
// block-registry.gen.js 代码生成（`go run . -emit-blocks`）

// jsValue 把任意默认值安全编码成 JS 字面量。复用 account_catalog.go 的 jsStr（字符串转义）。
func jsValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return jsStr(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = jsValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		// fail loud：未支持的默认值类型（如 map）若静默走 fmt.Sprint 会产出破损 JS。
		// codegen 在 build 期跑，panic 能在加新字段类型时立刻暴露，而非上线后前端炸。
		panic(fmt.Sprintf("blockTypeCatalog: unsupported field default type %T (%v); 扩展 jsValue 以支持它", x, x))
	}
}

func joinJsStr(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = jsStr(s)
	}
	return strings.Join(parts, ", ")
}

// renderBlockRegistryJS 从 blockTypeCatalog 生成 block-registry.gen.js 的完整内容。
func renderBlockRegistryJS() string {
	cat := blockTypeCatalog()
	var b strings.Builder
	b.WriteString("// blocks/block-registry.gen.js — 底部区块类型注册表。\n")
	b.WriteString("// 【生成文件】由 `go run . -emit-blocks`（npm run gen:blocks）从 api/block_type_catalog.go 生成，请勿手改。\n")
	b.WriteString("// 区块类型/字段/默认值唯一真相在 blockTypeCatalog()；改后跑 npm run gen:blocks，启动期一致性校验兜底。\n\n")
	b.WriteString("export const BLOCK_TYPES = Object.freeze({\n")
	for _, t := range cat {
		if t.Note != "" {
			b.WriteString("  // " + t.Note + "\n")
		}
		b.WriteString("  " + jsKey(t.Type) + ": Object.freeze({\n")
		b.WriteString("    type: " + jsStr(t.Type) + ",\n")
		b.WriteString("    label: " + jsStr(t.Label) + ",\n")
		b.WriteString("    channels: Object.freeze([" + joinJsStr(t.Channels) + "]),\n")
		b.WriteString("    fields: Object.freeze([\n")
		for _, f := range t.Fields {
			b.WriteString("      Object.freeze({ key: " + jsStr(f.Key) + ", label: " + jsStr(f.Label) + ", type: " + jsStr(f.Type) + ", default: " + jsValue(f.Default))
			if f.Tooltip != "" {
				b.WriteString(", tooltip: " + jsStr(f.Tooltip))
			}
			if f.Min != nil {
				b.WriteString(", min: " + strconv.Itoa(*f.Min))
			}
			if f.Max != nil {
				b.WriteString(", max: " + strconv.Itoa(*f.Max))
			}
			if len(f.Options) > 0 {
				opts := make([]string, len(f.Options))
				for i, o := range f.Options {
					opts[i] = "{ value: " + jsStr(o.Value) + ", label: " + jsStr(o.Label) + " }"
				}
				b.WriteString(", options: Object.freeze([" + strings.Join(opts, ", ") + "])")
			}
			b.WriteString(" }),\n")
		}
		b.WriteString("    ]),\n")
		b.WriteString("  }),\n")
	}
	b.WriteString("});\n\n")
	b.WriteString(`export function getBlockType(type) {
  return BLOCK_TYPES[type] || null;
}

export function blockTypesForChannel(channel) {
  return Object.values(BLOCK_TYPES).filter(t => t.channels.includes(channel));
}

// 返回某类型的字段默认值映射 { key: default }（数组默认值做浅拷贝，避免共享引用）。
export function defaultFieldsForType(type) {
  const t = BLOCK_TYPES[type];
  if (!t) return {};
  const out = {};
  for (const f of t.fields) out[f.key] = Array.isArray(f.default) ? f.default.slice() : f.default;
  return out;
}
`)
	return b.String()
}

// validateBlockRegistryConsistency：committed 的 block-registry.gen.js 必须与 blockTypeCatalog
// 生成结果逐字节一致。文件缺失只告警（不 brick 生产），存在但不一致则返回错误（由 main 决定 fatal）。
func validateBlockRegistryConsistency(publicDir string) error {
	path := filepath.Join(publicDir, "js", "blocks", "block-registry.gen.js")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("warn: 无法读取 %s 做区块注册表一致性校验（跳过）: %w", path, err)
	}
	if string(raw) != renderBlockRegistryJS() {
		return fmt.Errorf("block-registry.gen.js 与 blockTypeCatalog 不一致（改了 api/block_type_catalog.go 后请跑 `npm run gen:blocks`；该文件是生成文件，勿手改）")
	}
	return nil
}
