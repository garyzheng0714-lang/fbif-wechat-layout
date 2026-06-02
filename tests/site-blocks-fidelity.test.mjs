// site-blocks-fidelity.test.mjs — 证明「区块渲染路径」与历史 buildFooterHtml 字节一致。
// WHY：P3 把网站底部从 fbif_footer 段切到 bottom_blocks 区块；rich_html 区块必须复刻
// 原页脚到一个字符不差（红线 #20 页脚完整输出/保真），否则等于破坏已发布成品保真。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildFooterHtml } from '../public/js/rule-behavior-foodtalks.js';
import { FOOTER_SEGMENTS_DEFAULT, FOOTER_SEGMENT_ORDER_DEFAULT, ASSET_LIBRARY_DEFAULT } from '../public/js/footer-segments-defaults.js';
import { renderBottomBlocksHtml } from '../public/js/blocks/block-render.js';
import { siteBlockRenderers } from '../public/js/templates/site-blocks.js';

// 用默认页脚段构造等价的 bottom_blocks 模块（每段 → 一个 rich_html 区块，html_lines 原样）。
function bottomBlocksFromDefaults() {
  const blocks = {};
  for (const id of FOOTER_SEGMENT_ORDER_DEFAULT) {
    blocks[id] = { type: 'rich_html', enabled: true, fields: { html_lines: FOOTER_SEGMENTS_DEFAULT[id].html_lines } };
  }
  return { enabled: true, order: FOOTER_SEGMENT_ORDER_DEFAULT.slice(), blocks };
}

const LIB = { segments: FOOTER_SEGMENTS_DEFAULT, segment_order: FOOTER_SEGMENT_ORDER_DEFAULT };

test('全部启用：区块渲染 == buildFooterHtml（字节一致）', () => {
  const allOn = Object.fromEntries(FOOTER_SEGMENT_ORDER_DEFAULT.map(k => [k, 1]));
  const viaFooter = buildFooterHtml(allOn, LIB, ASSET_LIBRARY_DEFAULT, 'ft-default');
  const viaBlocks = renderBottomBlocksHtml(
    bottomBlocksFromDefaults(),
    { enabled: true, overrides: {} },
    siteBlockRenderers(ASSET_LIBRARY_DEFAULT, 'ft-default')
  );
  assert.equal(viaBlocks, viaFooter);
});

test('子集启用（仅 biz_coop + logo）：区块渲染 == buildFooterHtml（字节一致）', () => {
  const subset = { biz_coop: 1, join_group: 0, follow_account: 0, logo: 1, more_info: 0 };
  const viaFooter = buildFooterHtml(subset, LIB, ASSET_LIBRARY_DEFAULT, 'ft-default');

  const overrides = {};
  for (const id of FOOTER_SEGMENT_ORDER_DEFAULT) overrides[id] = { enabled: !!subset[id] };
  const viaBlocks = renderBottomBlocksHtml(
    bottomBlocksFromDefaults(),
    { enabled: true, overrides },
    siteBlockRenderers(ASSET_LIBRARY_DEFAULT, 'ft-default')
  );
  assert.equal(viaBlocks, viaFooter);
});

test('模块禁用（enabled=false）：区块渲染输出空串', () => {
  const out = renderBottomBlocksHtml(
    bottomBlocksFromDefaults(),
    { enabled: false, overrides: {} },
    siteBlockRenderers(ASSET_LIBRARY_DEFAULT, 'ft-default')
  );
  assert.equal(out, '');
});

test('name_card 字段化区块：上方文字 + 名片图按字段渲染', () => {
  const mod = { enabled: true, order: ['card'], blocks: { card: { type: 'name_card', enabled: true, fields: {} } } };
  const out = renderBottomBlocksHtml(
    mod,
    { overrides: { card: { fields: { intro_text: '联系 Eileen', intro_size: 16, intro_color: '#396FBC', image: 'https://static.foodtalks.cn/x.png' } } } },
    siteBlockRenderers(ASSET_LIBRARY_DEFAULT, 'ft-default')
  );
  assert.match(out, /联系 Eileen/);
  assert.match(out, /font-size: 16px/);
  assert.match(out, /#396FBC/);
  assert.match(out, /static\.foodtalks\.cn\/x\.png/);
});
