// text-migrate.js — text 块散字段 → 中性富文本 html（P2：迁移/统一到 html 字段）。
//
// 用途：① 切 v2 时把存量散字段 text 烘焙成 html；② 后台 wangEditor 编辑 text 块时用散字段初始化。
// 产出语义化中性 HTML（hex 颜色、px 字号），经后端 validateRichTextHTML 白名单，被 rich-text-render 渲染。
// 中性：不 import 渲染器（红线#19）。

function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// textFieldsToRichHtml 把 text 块散字段（content/font_size/text_color/bold/italic/underline/background_color/align）
// 烘焙成中性富文本 html（多行 → 多个 <p>，空行跳过）。content 为空返回 ''。
export function textFieldsToRichHtml(f) {
  f = f || {};
  const content = String(f.content == null ? '' : f.content);
  if (!content.trim()) return '';
  const align = ['left', 'center', 'right'].includes(f.align) ? f.align : 'left';
  const sp = [];
  const color = String(f.text_color || '').trim();
  if (color) sp.push(`color: ${color}`);
  const size = Number(f.font_size);
  if (Number.isFinite(size) && size > 0) sp.push(`font-size: ${Math.round(size)}px`);
  if (f.bold) sp.push('font-weight: bold');
  if (f.italic) sp.push('font-style: italic');
  if (f.underline) sp.push('text-decoration: underline');
  const bg = String(f.background_color == null ? '' : f.background_color).trim();
  if (bg) sp.push(`background-color: ${bg}`);
  const spanStyle = sp.join('; ');
  return content.split(/\r?\n/).map((line) => {
    const t = line.trim();
    if (!t) return '';
    const inner = spanStyle ? `<span style="${spanStyle};">${esc(t)}</span>` : esc(t);
    return `<p style="text-align: ${align};">${inner}</p>`;
  }).filter(Boolean).join('');
}
