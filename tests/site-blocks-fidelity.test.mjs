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
  return { schema_version: 2, enabled: true, active_preset_id: 'default', presets: [{ id: 'default', order: FOOTER_SEGMENT_ORDER_DEFAULT.slice(), blocks }] };
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

test('网站文字模块与图片区块共用底部语义（v2 单预设）', () => {
  // WHY：网站没有推荐文章系统，但文字/图片模块应与公众号共用同一 bottom_blocks 模型。
  // v2 无 blank_line 自动空段——块间距由 spacer 块承载（此处不测间距，只测块样式渲染）。
  const mod = {
    schema_version: 2, active_preset_id: 'default',
    presets: [{ id: 'default', order: ['tip', 'logo'], blocks: {
      tip: { type: 'text', enabled: true, fields: { content: '商务合作', font_size: 16, text_color: '#396FBC', italic: true, underline: true, align: 'center' } },
      logo: { type: 'image', enabled: true, fields: { image: 'https://static.foodtalks.cn/logo.png' } },
    } }],
  };
  const out = renderBottomBlocksHtml(
    mod,
    { enabled: true, overrides: {} },
    siteBlockRenderers(ASSET_LIBRARY_DEFAULT, 'ft-default')
  );
  assert.match(out, /商务合作/);
  assert.match(out, /font-size: 16px/);
  assert.match(out, /font-style: italic/);
  assert.match(out, /text-decoration: underline/);
  assert.match(out, /static\.foodtalks\.cn\/logo\.png/);
});

test('网站 rich_html 页脚支持 fields.html_lines 替换素材且默认输出不漂', () => {
  const assetLibrary = {
    ...ASSET_LIBRARY_DEFAULT,
    assets: [
      ...ASSET_LIBRARY_DEFAULT.assets,
      {
        id: 'ft_footer_replacement_logo',
        type: 'image',
        usage: 'custom',
        label: '替换图',
        url: 'https://static.foodtalks.cn/replacement-logo.png',
        scope: 'ft-default',
      },
    ],
  };
  const mod = {
    schema_version: 2,
    enabled: true,
    active_preset_id: 'default',
    presets: [{ id: 'default', order: ['logo'], blocks: {
      logo: {
        type: 'rich_html',
        enabled: true,
        fields: {
          html_lines: ['<p><img src="{{asset#ft_footer_logo}}" alt="old" /></p>'],
        },
      },
    } }],
  };

  const defaultOut = renderBottomBlocksHtml(
    mod,
    { enabled: true, overrides: {} },
    siteBlockRenderers(assetLibrary, 'ft-default')
  );
  const restoredOut = renderBottomBlocksHtml(
    mod,
    { enabled: true, overrides: { logo: { enabled: true } } },
    siteBlockRenderers(assetLibrary, 'ft-default')
  );
  const replacedOut = renderBottomBlocksHtml(
    mod,
    {
      enabled: true,
      overrides: {
        logo: {
          enabled: true,
          fields: {
            html_lines: ['<p><img src="{{asset#ft_footer_replacement_logo}}" alt="new" /></p>'],
          },
        },
      },
    },
    siteBlockRenderers(assetLibrary, 'ft-default')
  );

  // WHY: /ft 侧栏只写本篇 rich_html field diff。没有 diff 或恢复默认时必须回到
  // 后台默认 HTML；有 diff 时渲染应解析新素材，不保留旧素材 URL。
  assert.equal(restoredOut, defaultOut, 'no fields override should keep the existing default/golden path stable');
  assert.match(defaultOut, /e155b7852c10a72f7b73157fdf9c4bf0\.png/);
  assert.match(replacedOut, /replacement-logo\.png/);
  assert.doesNotMatch(replacedOut, /e155b7852c10a72f7b73157fdf9c4bf0\.png/);
});
