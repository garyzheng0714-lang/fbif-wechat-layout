// block-render.js — 底部区块「编排层」中性渲染器：合并两层 → 按序调各渠道渲染器 → 拼 HTML。
//
// 中性：不 import 任何渠道渲染器（fbif.js / foodtalks-site.js），红线 #19 安全。
// 渠道把「type → HTML」的渲染器表传进来（mp-blocks.js / site-blocks.js 各自提供）。

import { resolveBlocks, flattenFields } from './block-merge.js';

function blockSeparatorHtml(renderers, ctx) {
  if (ctx && typeof ctx.blockSeparatorHtml === 'string') return ctx.blockSeparatorHtml;
  if (renderers && typeof renderers.__separator === 'function') return renderers.__separator(ctx);
  return '<section style="height:1em;line-height:1em;font-size:0;">&nbsp;</section>';
}

// renderBottomBlocksHtml 合并后台默认与单篇覆盖，按解析顺序对每个启用区块调用其类型渲染器，
// 返回拼好的 HTML（模块禁用或全部为空时返回 ''）。
//   module:   config.modules.bottom_blocks（后台默认）
//   override: 单篇覆盖 { enabled?, order?, overrides:{ id:{enabled?,fields?} } }
//   renderers: { <type>: (fields, ctx) => htmlStringOrEmpty }；缺该 type 的渲染器则跳过该块（该渠道不支持）。
//   ctx: 透传给渲染器（含 block 元信息）；ctx.presetId 在 v2 双预设下选 active 预设（v1 忽略）。
export function renderBottomBlocksHtml(module, override, renderers, ctx = {}) {
  const resolved = resolveBlocks(module, override, { presetId: ctx && ctx.presetId });
  if (!resolved.enabled) return '';
  const parts = [];
  for (const block of resolved.blocks) {
    if (!block.enabled) continue;
    const render = renderers && renderers[block.type];
    if (typeof render !== 'function') continue; // 该渠道未实现此区块类型 → 跳过（不报错，保持鲁棒）
    const html = render(flattenFields(block), { ...ctx, block });
    if (html) {
      if (parts.length && resolved.gap === 'blank_line') parts.push(blockSeparatorHtml(renderers, ctx));
      parts.push(html);
    }
  }
  return parts.join('\n');
}
