// FoodTalks 网站文章底部页脚：守住 bottom_blocks 主路径与 fbif_footer 回退路径的字节级一致性。
//
// WHY：网站页脚曾从历史 footer library 迁到统一 bottom_blocks；两条路径都还在线上承担兼容职责。
// 如果未来有人只改其中一条路径，文章底部会悄悄漂移，而这类漂移在本仓库已经反复发生过。

import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  ASSET_LIBRARY_DEFAULT,
  FOOTER_SEGMENTS_DEFAULT,
  FOOTER_SEGMENT_ORDER_DEFAULT,
} from '../public/js/footer-segments-defaults.js';

const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };
globalThis.window = { __APP_BUILD_INFO: { build: 'test' }, localStorage: storage };
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });

const template = (await import('../public/js/templates/foodtalks-site.js?siteFooterParity=' + Date.now())).default;
const { FOOTER_PRESETS } = await import('../public/js/rule-behavior-foodtalks.js?siteFooterParity=' + Date.now());

const FOOTER_KEYS = ['tip', ...FOOTER_SEGMENT_ORDER_DEFAULT];
const GOLDEN_ALL_ON_FOOTER = new URL('./golden/site-footer-parity-all-on.footer.html', import.meta.url);

const ARTICLE = {
  title: '页脚一致性样例',
  blocks: [
    { k: 'txt', runs: [{ type: 'txt', text: '正文用于页脚一致性测试。' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '提示：这是页脚一致性测试里的版权提示。' }] },
  ],
};

function normalizeSegments(segments) {
  const out = {};
  for (const key of FOOTER_KEYS) out[key] = !!(segments && segments[key]);
  return out;
}

function footerLibraryForSegments(segments) {
  return {
    enabled: true,
    active_preset_id: 'case',
    segment_order: FOOTER_SEGMENT_ORDER_DEFAULT.slice(),
    segments: FOOTER_SEGMENTS_DEFAULT,
    presets: [{ id: 'case', name: '测试组合', segments: normalizeSegments(segments) }],
  };
}

function bottomBlocksFromFooterModule(footerCfg) {
  const blocks = {};
  const order = [];
  for (const id of footerCfg.segment_order || []) {
    if (id === 'tip') continue;
    const entry = footerCfg.segments && footerCfg.segments[id];
    const htmlLines = Array.isArray(entry)
      ? entry
      : (entry && Array.isArray(entry.html_lines) ? entry.html_lines : []);
    blocks[id] = { type: 'rich_html', enabled: true, fields: { html_lines: htmlLines.slice() } };
    order.push(id);
  }
  return {
    schema_version: 2,
    enabled: !!footerCfg.enabled,
    active_preset_id: 'default',
    presets: [{ id: 'default', name: '页脚', order, blocks }],
  };
}

function bottomBlocksOverrideForSegments(segments) {
  const overrides = {};
  for (const id of FOOTER_SEGMENT_ORDER_DEFAULT) {
    overrides[id] = { enabled: !!segments[id] };
  }
  return { enabled: true, overrides };
}

function configPairForSegments(segments) {
  const normalized = normalizeSegments(segments);
  const footerCfg = footerLibraryForSegments(normalized);
  const sharedModules = {
    fbif_footer: footerCfg,
    asset_library: ASSET_LIBRARY_DEFAULT,
  };
  return {
    main: {
      modules: {
        ...sharedModules,
        bottom_blocks: bottomBlocksFromFooterModule(footerCfg),
      },
      _bottomBlocksOverride: bottomBlocksOverrideForSegments(normalized),
    },
    fallback: { modules: sharedModules },
  };
}

function renderArticleHtml(config) {
  return template.processBlocks(ARTICLE, { config, isRepost: false }).lines.join('\n');
}

function extractFooterHtml(html) {
  const marker = '<p style="text-align: justify;">正文用于页脚一致性测试。</p>\n<p style="text-align: justify;"><br></p>\n';
  assert.ok(html.startsWith(marker), '测试文章正文锚点必须稳定，golden 才只锁页脚输出');
  return html.slice(marker.length);
}

async function readOrCreateGolden(url, value) {
  try {
    return await readFile(url, 'utf8');
  } catch (err) {
    if (!err || err.code !== 'ENOENT') throw err;
    // WHY：本仓库 golden 测试允许首次运行生成快照，便于新增守卫时先固定当前实际行为；
    // 提交时仍应带上生成后的文件，后续漂移才会被逐字节拦住。
    await mkdir(dirname(fileURLToPath(url)), { recursive: true });
    await writeFile(url, value, 'utf8');
    return value;
  }
}

const CASES = [
  {
    name: 'all-on',
    segments: Object.fromEntries(FOOTER_KEYS.map(key => [key, true])),
  },
  {
    name: 'all-off',
    segments: Object.fromEntries(FOOTER_KEYS.map(key => [key, false])),
  },
  ...FOOTER_PRESETS.map(preset => ({
    name: `preset-${preset.id}`,
    segments: preset.segments,
  })),
];

for (const c of CASES) {
  test(`FoodTalks 网站页脚两条路径字节一致：${c.name}`, () => {
    // WHY：运营可能从新规则拿到 bottom_blocks，也可能命中旧规则回退 fbif_footer；
    // 两份等价配置必须产出同一篇文章 HTML，避免同一文章因规则形态不同而变版。
    const pair = configPairForSegments(c.segments);
    const mainHtml = renderArticleHtml(pair.main);
    const fallbackHtml = renderArticleHtml(pair.fallback);
    assert.equal(mainHtml, fallbackHtml, `${c.name} 的 bottom_blocks 主路径与 fbif_footer 回退路径必须逐字节一致`);
  });
}

test('FoodTalks 网站页脚 all-on 输出锁定 golden', async () => {
  // WHY：相等断言只能证明两条路径同涨同跌；golden 额外锁住当前 all-on 页脚字节，
  // 防止未来同时改动主路径和回退路径时把已发布页脚一起悄悄漂移。
  const pair = configPairForSegments(Object.fromEntries(FOOTER_KEYS.map(key => [key, true])));
  const mainFooter = extractFooterHtml(renderArticleHtml(pair.main));
  const fallbackFooter = extractFooterHtml(renderArticleHtml(pair.fallback));
  assert.equal(mainFooter, fallbackFooter, 'golden 只在两条路径当前一致时锁一份页脚输出');
  const golden = await readOrCreateGolden(GOLDEN_ALL_ON_FOOTER, mainFooter);
  assert.equal(mainFooter, golden, 'all-on 页脚输出必须与 golden 逐字节一致');
});
