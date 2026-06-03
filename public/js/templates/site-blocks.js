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

// 网站口径满宽图段。
function siteImage(url, extra = '') {
  return `<p style="text-align: center;"><img style="display: block; margin-left: auto; margin-right: auto; max-width: 100%;${extra}" src="${url}" alt="" /></p>`;
}
// 网站口径居中文字段。
function siteText(text, size, color) {
  return `<p style="text-align: center; font-size: ${size}px; color: ${esc(color)};">${esc(text)}</p>`;
}

// siteBlockRenderers 返回网站渠道的「type → 渲染函数」表。
//   assetLibrary: config.modules.asset_library（用于解析 {{asset#id}} 占位符，兼容旧 {{asset:usage}}）
//   scope: 资产作用域，网站默认 'ft-default'
export function siteBlockRenderers(assetLibrary, scope = 'ft-default') {
  const resolve = (lines) => resolveAssetRefs(lines, assetLibrary, scope).join('\n');
  return {
    // 自定义内容：原样输出（网站默认页脚段即此型，字节一致）。
    rich_html: (f) => resolve(asLines(f.html_lines)),

    // 声明/提示：粗体标题 + 条目行（原样）。
    declaration: (f) => {
      const lines = [];
      const title = String(f.title == null ? '' : f.title).trim();
      if (title) lines.push(`<p style="font-weight: bold;">${esc(title)}</p>`);
      for (const ln of asLines(f.lines)) lines.push(String(ln == null ? '' : ln));
      return resolve(lines);
    },

    // 名片：上方文字（可空）+ 名片图（网站口径 #888888 14px）。
    name_card: (f) => {
      const lines = [];
      const text = String(f.intro_text == null ? '' : f.intro_text).trim();
      if (text) lines.push(siteText(text, intOr(f.intro_size, 14), f.intro_color || '#888888'));
      const img = String(f.image == null ? '' : f.image).trim();
      if (img) lines.push(siteImage(img));
      return resolve(lines);
    },

    // 二维码卡：说明（可空）+ 二维码图。
    qr_card: (f) => {
      const lines = [];
      const cap = String(f.caption == null ? '' : f.caption).trim();
      if (cap) lines.push(siteText(cap, intOr(f.caption_size, 14), f.caption_color || '#888888'));
      const img = String(f.image == null ? '' : f.image).trim();
      if (img) lines.push(siteImage(img));
      return resolve(lines);
    },

    // 标题+图片：栏目标题（可配文字/字号/颜色/居中）+ 图。
    titled_media: (f) => {
      const parts = [];
      const title = String(f.title == null ? '' : f.title).trim();
      if (title) {
        const ta = f.center === false ? 'left' : 'center';
        parts.push(`<p style="text-align: ${ta}; font-size: ${intOr(f.title_size, 17)}px; color: ${esc(f.title_color || '#544545')}; font-weight: bold;">${esc(title)}</p>`);
      }
      const img = String(f.image == null ? '' : f.image).trim();
      if (img) parts.push(siteImage(img));
      return resolve(parts);
    },

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
