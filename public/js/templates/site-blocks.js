// site-blocks.js — FoodTalks 网站渠道的底部区块渲染器（type → HTML）。
//
// 红线 #19：网站渲染器与公众号渲染器物理隔离——本文件不 import fbif.js / mp-blocks.js。
// 只 import 中性的 asset-library（占位符解析），与 foodtalks-site.js 同源隔离约束。
//
// rich_html：原样输出 html_lines（解析 {{asset:…}}）——复刻现有页脚段，字节一致。
// name_card：字段化名片（上方文字 + 名片图），网站口径样式。

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

// siteBlockRenderers 返回网站渠道的「type → 渲染函数」表。
//   assetLibrary: config.modules.asset_library（用于解析 {{asset:…}} 占位符）
//   scope: 资产作用域，网站默认 'ft-default'
export function siteBlockRenderers(assetLibrary, scope = 'ft-default') {
  return {
    rich_html: (f) => resolveAssetRefs(asLines(f.html_lines), assetLibrary, scope).join('\n'),
    name_card: (f) => {
      const lines = [];
      const text = String(f.intro_text == null ? '' : f.intro_text).trim();
      if (text) {
        const size = Number(f.intro_size) > 0 ? Math.round(Number(f.intro_size)) : 14;
        const color = String(f.intro_color || '#888888');
        lines.push(`<p style="text-align: center; font-size: ${size}px; color: ${esc(color)};">${esc(text)}</p>`);
      }
      const img = String(f.image == null ? '' : f.image).trim();
      if (img) {
        lines.push(`<p style="text-align: center;"><img style="display: block; margin-left: auto; margin-right: auto; max-width: 100%;" src="${img}" alt="" /></p>`);
      }
      return resolveAssetRefs(lines, assetLibrary, scope).join('\n');
    },
  };
}
