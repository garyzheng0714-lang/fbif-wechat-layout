// blocks/block-registry.gen.js — 底部区块类型注册表。
// 【生成文件】由 `go run . -emit-blocks`（npm run gen:blocks）从 api/block_type_catalog.go 生成，请勿手改。
// 区块类型/字段/默认值唯一真相在 blockTypeCatalog()；改后跑 npm run gen:blocks，启动期一致性校验兜底。

export const BLOCK_TYPES = Object.freeze({
  // 原始 HTML 段（html_lines）。承接历史页脚段的无损迁移，也是高级用户兜底类型。
  rich_html: Object.freeze({
    type: 'rich_html',
    label: '自定义内容',
    channels: Object.freeze(['mp', 'ft']),
    fields: Object.freeze([
      Object.freeze({ key: 'html_lines', label: 'HTML 内容', type: 'html_lines', default: [], tooltip: '每行一个 HTML 段；属高级编辑，运营一般无需改动。' }),
    ]),
  }),
  // 字段化名片：上方一行说明文字（可改文案/字号/颜色）+ 名片图。
  name_card: Object.freeze({
    type: 'name_card',
    label: '名片',
    channels: Object.freeze(['mp', 'ft']),
    fields: Object.freeze([
      Object.freeze({ key: 'intro_text', label: '上方文字', type: 'text', default: '找谁谁谁', tooltip: '名片上方显示的一行说明文字。' }),
      Object.freeze({ key: 'intro_size', label: '文字字号', type: 'number', default: 14, tooltip: '上方文字字号（纯数字，无需单位）。', min: 12, max: 28 }),
      Object.freeze({ key: 'intro_color', label: '文字颜色', type: 'color', default: '#888888', tooltip: '上方文字颜色。' }),
      Object.freeze({ key: 'image', label: '名片图', type: 'image', default: '', tooltip: '从素材库选择名片图片。' }),
    ]),
  }),
  // 委托现有 more-articles.js 渲染推荐卡片；卡片在「更多文章」编辑器/侧栏管理，本区块只控制是否启用与排序。
  more_articles: Object.freeze({
    type: 'more_articles',
    label: '更多文章',
    channels: Object.freeze(['mp']),
    fields: Object.freeze([
    ]),
  }),
});

export function getBlockType(type) {
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
