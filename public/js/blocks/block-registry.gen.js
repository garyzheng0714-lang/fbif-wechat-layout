// blocks/block-registry.gen.js — 底部区块类型注册表。
// 【生成文件】由 `go run . -emit-blocks`（npm run gen:blocks）从 api/block_type_catalog.go 生成，请勿手改。
// 区块类型/字段/默认值唯一真相在 blockTypeCatalog()；改后跑 npm run gen:blocks，启动期一致性校验兜底。

export const BLOCK_TYPES = Object.freeze({
  // 文字模块：一段可编辑文字，支持字号、斜体、下划线、字色、背景色和对齐。作为底部通用文字块，公众号/网站共用。
  text: Object.freeze({
    type: 'text',
    kind: 'text',
    label: '文字',
    channels: Object.freeze(['mp', 'ft']),
    fields: Object.freeze([
      Object.freeze({ key: 'content', label: '文字内容', type: 'longText', default: '', tooltip: '支持换行；每行会作为一行文字输出。' }),
      Object.freeze({ key: 'html', label: '富文本', type: 'rich_text', default: '', tooltip: '富文本内容（选中片段设样式）；留空则用上面的纯文本字段（迁移期并存，P2）。' }),
      Object.freeze({ key: 'font_size', label: '字号', type: 'number', default: 15, tooltip: '文字字号（纯数字，无需单位）。', min: 10, max: 48 }),
      Object.freeze({ key: 'text_color', label: '字色', type: 'color', default: '#544545', tooltip: '文字颜色。' }),
      Object.freeze({ key: 'background_color', label: '背景色', type: 'color', default: '', tooltip: '可留空；填写后给文字行加背景色。' }),
      Object.freeze({ key: 'italic', label: '斜体', type: 'switch', default: false, tooltip: '是否使用斜体。' }),
      Object.freeze({ key: 'underline', label: '下划线', type: 'switch', default: false, tooltip: '是否显示下划线。' }),
      Object.freeze({ key: 'bold', label: '加粗', type: 'switch', default: false, tooltip: '整段是否加粗。' }),
      Object.freeze({ key: 'align', label: '对齐', type: 'select', default: 'left', tooltip: '文字对齐方式。', options: Object.freeze([{ value: 'left', label: '左对齐' }, { value: 'center', label: '居中' }, { value: 'right', label: '右对齐' }]) }),
    ]),
  }),
  // 原始 HTML 段（html_lines）。承接历史页脚段的无损迁移，也是高级用户兜底类型。
  rich_html: Object.freeze({
    type: 'rich_html',
    kind: 'text',
    label: '自定义内容',
    channels: Object.freeze(['ft']),
    fields: Object.freeze([
      Object.freeze({ key: 'html_lines', label: 'HTML 内容', type: 'html_lines', default: [], tooltip: '每行一个 HTML 段；属高级编辑，运营一般无需改动。' }),
    ]),
  }),
  // 栏目标题（如「/ 更多文章 /」，可改文字/字号/颜色/居中）+ 委托现有 more-articles.js 渲染推荐卡片；卡片在「更多文章」编辑器/侧栏管理。
  more_articles: Object.freeze({
    type: 'more_articles',
    kind: 'recommendations',
    label: '更多文章',
    channels: Object.freeze(['mp']),
    fields: Object.freeze([
      Object.freeze({ key: 'title', label: '标题文字', type: 'text', default: '/ 更多文章 /', tooltip: '栏目标题。' }),
      Object.freeze({ key: 'title_size', label: '标题字号', type: 'number', default: 17, tooltip: '标题字号。', min: 12, max: 28 }),
      Object.freeze({ key: 'title_color', label: '标题颜色', type: 'color', default: '#544545', tooltip: '标题颜色。' }),
      Object.freeze({ key: 'center', label: '标题居中', type: 'switch', default: true, tooltip: '标题是否居中。' }),
    ]),
  }),
  // 单张图片（素材库），可设宽度/居中。覆盖 FoodTalks logo 等纯图区块。
  image: Object.freeze({
    type: 'image',
    kind: 'image',
    label: '图片',
    channels: Object.freeze(['mp', 'ft']),
    fields: Object.freeze([
      Object.freeze({ key: 'image', label: '图片', type: 'image', default: '', tooltip: '从素材库选择图片。' }),
      Object.freeze({ key: 'width', label: '宽度', type: 'number', default: 0, tooltip: '图片宽度（px，0=自适应满宽）。', min: 0, max: 1080 }),
      Object.freeze({ key: 'center', label: '居中', type: 'switch', default: true, tooltip: '是否居中。' }),
    ]),
  }),
  // 间距模块：一段可调高度的留白，精确控制区块之间的间隔（取代自动空行）。公众号/网站共用。
  spacer: Object.freeze({
    type: 'spacer',
    kind: 'spacer',
    label: '间距',
    channels: Object.freeze(['mp', 'ft']),
    fields: Object.freeze([
      Object.freeze({ key: 'height', label: '高度', type: 'number', default: 16, tooltip: '留白高度（px，纯数字，无需单位）。', min: 0, max: 200 }),
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
