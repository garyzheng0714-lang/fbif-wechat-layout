// block-migrate.js — 旧语义类型块 → 4 原子的结构迁移（切生产 v6→v7 前置，spec §17 切生产批 #4）。
//
// 旧 type（rich_html/declaration/name_card/qr_card/titled_media）→ text/image 原子。用于用户自建规则
// 与 localStorage 快照的存量数据（内置规则走 BuiltinVersion 覆盖、不经此）。
// 本函数保证「结构 + 内容无损」：name_card/titled_media 的说明/标题文字不丢（拆成独立 text 原子）。
// 视觉等价（原子渲染器样式 vs 旧 type 渲染器）待公众号成品对照——切生产时验证。

import { textFieldsToRichHtml } from './text-migrate.js';

function esc(s) {
  return String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// migrateLegacyBlock 把一个旧 type 块转成一个或多个原子块（一对多时 order 由 migrateLegacyModule 重建）。
export function migrateLegacyBlock(block) {
  const t = block && block.type;
  const f = (block && block.fields) || {};
  const on = block && block.enabled !== false;
  if (t === 'rich_html') {
    return [{ type: 'text', enabled: on, fields: { html: (f.html_lines || []).join('\n') } }];
  }
  if (t === 'declaration') {
    const head = f.title ? `<p><strong>${esc(f.title)}</strong></p>` : '';
    const html = [head, ...(f.lines || [])].filter(Boolean).join('\n');
    return [{ type: 'text', enabled: on, fields: { html } }];
  }
  if (t === 'name_card' || t === 'qr_card') {
    const intro = t === 'name_card' ? f.intro_text : f.caption;
    const size = t === 'name_card' ? f.intro_size : f.caption_size;
    const color = t === 'name_card' ? f.intro_color : f.caption_color;
    const out = [];
    if (intro && String(intro).trim()) {
      out.push({ type: 'text', enabled: on, fields: { html: textFieldsToRichHtml({ content: intro, font_size: size, text_color: color, align: 'center' }) } });
    }
    out.push({ type: 'image', enabled: on, fields: { image: f.image || '', width: 0, center: true } });
    return out;
  }
  if (t === 'titled_media') {
    const out = [];
    if (f.title && String(f.title).trim()) {
      out.push({ type: 'text', enabled: on, fields: { html: textFieldsToRichHtml({ content: f.title, font_size: f.title_size, text_color: f.title_color, align: f.center ? 'center' : 'left', bold: true }) } });
    }
    out.push({ type: 'image', enabled: on, fields: { image: f.image || '', width: 0, center: true } });
    return out;
  }
  return [block]; // 已是原子（text/image/more_articles/spacer），原样保留
}

// migrateLegacyModule 迁移整个 bottom_blocks 模块：遍历 order，旧 type 块转原子并重建 order/blocks。
// 一对多块的衍生 id 用 `${id}_m${i}` 避免碰撞；保留其余模块字段（enabled/schema_version/presets…）。
export function migrateLegacyModule(module) {
  if (!module || typeof module !== 'object') return module;
  const order = Array.isArray(module.order) ? module.order : [];
  const blocks = module.blocks || {};
  const newOrder = [];
  const newBlocks = {};
  for (const id of order) {
    const block = blocks[id];
    if (!block) continue;
    migrateLegacyBlock(block).forEach((atom, i) => {
      const newId = i === 0 ? id : `${id}_m${i}`;
      newOrder.push(newId);
      newBlocks[newId] = atom;
    });
  }
  return { ...module, order: newOrder, blocks: newBlocks };
}
