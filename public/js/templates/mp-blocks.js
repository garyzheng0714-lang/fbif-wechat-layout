// mp-blocks.js — 微信公众号渠道的底部区块渲染器（type → HTML）。
//
// 红线 #19：公众号渲染器与网站渲染器物理隔离——本文件不 import foodtalks-site.js / site-blocks.js。
// 只 import 中性的 asset-library（占位符解析）。输出自包含 inline 样式 section，不依赖外壳 CSS class。
//
// 追加式：这些区块渲染在正文（含参考来源）之后、footer.html（提示/更多文章）之前——
// 正合「参考来源下方插名片」。默认 bottom_blocks 禁用/为空时整段不输出，对现有公众号输出零改动。

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

// mpBlockRenderers 返回公众号渠道的「type → 渲染函数」表。
//   assetLibrary: config.modules.asset_library（解析 {{asset:…}}）
//   scope: 资产作用域，公众号用账号 id（如 'fbif'）；缺省 'global'
export function mpBlockRenderers(assetLibrary, scope = 'global') {
  return {
    rich_html: (f) => resolveAssetRefs(asLines(f.html_lines), assetLibrary, scope).join('\n'),
    name_card: (f) => {
      const lines = [];
      const text = String(f.intro_text == null ? '' : f.intro_text).trim();
      if (text) {
        const size = Number(f.intro_size) > 0 ? Math.round(Number(f.intro_size)) : 14;
        const color = String(f.intro_color || '#888888');
        lines.push(`<section style="margin: 0 8px 12px; text-align: center;"><span style="font-size: ${size}px; color: ${esc(color)}; line-height: 1.75;">${esc(text)}</span></section>`);
      }
      const img = String(f.image == null ? '' : f.image).trim();
      if (img) {
        lines.push(`<section style="margin: 0 0 12px; text-align: center; line-height: 0; font-size: 0;"><img src="${img}" style="max-width: 100%; height: auto; display: block; margin: 0 auto;" /></section>`);
      }
      return resolveAssetRefs(lines, assetLibrary, scope).join('');
    },
  };
}
