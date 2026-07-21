// site-blocks.js — FoodTalks 网站渠道的底部区块渲染器（type → HTML）。
//
// 红线 #19：网站渲染器与公众号渲染器物理隔离——本文件不 import fbif.js / mp-blocks.js，
// 只 import 中性的 asset-library（占位符解析）。网站口径与公众号不同：颜色用 hex 直出（不转 rgb）、
// 无 mp-quote 字体栈、署名走 #888888（见 docs/superpowers/specs/2026-05-28-unified-rule-engine-design.md §1.2）。
//
// rich_html：原样输出 html_lines（复刻现有页脚段，字节一致，网站默认仍走此型）。
// 语义化块（name_card/qr_card/titled_media/image/declaration）：字段化，网站口径样式，
// 供网站后续按语义化布局使用；图字段经选图器写 {{asset#id}}，由 resolveAssetRefs 解析。

import { resolveAssetRefs } from '../asset-library.js';
import { richTextToInline } from '../blocks/rich-text-render.js';
import { filterSafeFooterHTMLLines } from '../footer-html-policy.js';

function asLines(v) {
  if (Array.isArray(v)) return v;
  if (v == null || v === '') return [];
  return [String(v)];
}

function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function intOr(v, fallback) {
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? Math.round(n) : fallback;
}
function alignOr(v, fallback = 'left') {
  return ['left', 'center', 'right'].includes(v) ? v : fallback;
}

// 网站口径满宽图段。
function siteImage(url, extra = '') {
  return `<p style="text-align: center;"><img style="display: block; margin-left: auto; margin-right: auto; max-width: 100%;${extra}" src="${url}" alt="" /></p>`;
}
// siteBlockRenderers 返回网站渠道的「type → 渲染函数」表。
//   assetLibrary: config.modules.asset_library（用于解析 {{asset#id}} 占位符，兼容旧 {{asset:usage}}）
//   scope: 资产作用域，网站默认 'ft-default'
export function siteBlockRenderers(assetLibrary, scope = 'ft-default', opts = {}) {
  const resolve = (lines) => resolveAssetRefs(lines, assetLibrary, scope).join('\n');
  // P2 富文本：parseDoc 注入；siteCh 网站口径（hex 直出、无 mp-quote 字体栈）。
  const parseDoc = opts.parseDoc || ((html) => {
    if (typeof DOMParser === 'undefined') return null;
    const d = new DOMParser().parseFromString(html, 'text/html');
    return d.body || d; // 内容容器（childNodes 语义对齐测试侧 linkedom document）
  });
  const fsOk = (v) => {
    const m = /^(\d+(?:\.\d+)?)(px|em|rem|%|pt)$/.exec(String(v || ''));
    if (!m) return false;
    if (m[2] === 'px') { const n = Number(m[1]); return n >= 6 && n <= 100; } // px 范围防滥用（codex P2 review #3）
    return true;
  };
  const siteSpan = (s) => {
    const parts = [`color: ${esc(s.color || '#544545')}`];
    if (fsOk(s['font-size'])) parts.push(`font-size: ${s['font-size']}`);
    if (s['font-style'] === 'italic') parts.push('font-style: italic');
    if (s['font-weight'] === 'bold') parts.push('font-weight: bold');
    if (s['text-decoration'] === 'underline') parts.push('text-decoration: underline');
    if (s['background-color']) parts.push(`background-color: ${esc(s['background-color'])}`);
    return parts.join('; ');
  };
  const siteCh = {
    span: (s, inner) => `<span style="${siteSpan(s)};">${inner}</span>`,
    link: (href, s, inner) => `<a href="${esc(href)}" style="${siteSpan(s)};">${inner}</a>`,
    pWrap: (align, inner) => `<p style="text-align: ${align};">${inner}</p>`,
  };
  return {

    // 间距模块（v2 原子）：定高留白段。height<=0 不输出（=运营删掉该间距）。
    spacer: (f) => {
      let h = Math.round(Number(f.height));
      if (!Number.isFinite(h) || h <= 0) return '';
      if (h > 200) h = 200; // 与 catalog schema(0..200) 一致，防脏本地数据渲出超大留白（codex review）
      return `<section style="height:${h}px;line-height:0;font-size:0;">&nbsp;</section>`;
    },

    // 文字模块：富文本 html 非空 → 渲染期转网站口径 inline-style（P2）；否则散字段回落。
    text: (f) => {
      const richHtml = String(f.html == null ? '' : f.html).trim();
      if (richHtml && parseDoc) {
        const out = richTextToInline(richHtml, parseDoc, siteCh);
        if (out) return out;
      }
      const lines = String(f.content == null ? '' : f.content).split(/\r?\n/).map(s => s.trim()).filter(Boolean);
      if (!lines.length) return '';
      const bg = String(f.background_color == null ? '' : f.background_color).trim();
      const style = [
        `text-align: ${alignOr(f.align)}`,
        `font-size: ${intOr(f.font_size, 15)}px`,
        `color: ${esc(f.text_color || '#544545')}`,
        f.italic ? 'font-style: italic' : '',
        f.bold ? 'font-weight: bold' : '',
        f.underline ? 'text-decoration: underline' : '',
        bg ? `background-color: ${esc(bg)}` : '',
      ].filter(Boolean).join('; ');
      return lines.map(line => `<p style="${style};">${esc(line)}</p>`).join('\n');
    },

    // 自定义内容：原样输出（网站默认页脚段即此型，字节一致）。
    rich_html: (f) => resolve(filterSafeFooterHTMLLines(asLines(f.html_lines))),

    // 更多文章：网站渠道无公众号「更多文章」卡片系统，按栏目标题渲染（仅标题，无委托卡片）。
    more_articles: (f) => {
      const title = String(f.title == null ? '' : f.title).trim();
      if (!title) return '';
      const ta = f.center === false ? 'left' : 'center';
      return `<p style="text-align: ${ta}; font-size: ${intOr(f.title_size, 17)}px; color: ${esc(f.title_color || '#544545')}; font-weight: bold;">${esc(title)}</p>`;
    },

    // 单图：满宽图（宽度/居中可配）。
    image: (f) => {
      const img = String(f.image == null ? '' : f.image).trim();
      if (!img) return '';
      const w = intOr(f.width, 0);
      const extra = w > 0 ? ` width: ${w}px;` : '';
      return resolve([siteImage(img, extra)]);
    },
  };
}
