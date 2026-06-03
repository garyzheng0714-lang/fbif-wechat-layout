// FoodTalks「什么是」系列【底部区块】渲染器保真测试（P1 统一底部区块）。
// 守护：系列把作者/BD/群二维码表达成 image 区块后，renderBottomFooter() 仍输出与旧
// buildSeriesFooterElems 同字节口径的满宽 banner（width:100%，非 FBIF 661px），
// 且不泄漏 FBIF 页脚（波波/1000/mp-quote）。more_articles 默认隐藏不渲染。
// 渲染器 top-level await import engine.js，需浏览器全局 shim（仿 foodtalks-series-render.test.mjs）。

import test from 'node:test';
import assert from 'node:assert/strict';

const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };
globalThis.window = { __APP_BUILD_INFO: { build: 'test' }, localStorage: storage };
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });

const template = (await import('../public/js/templates/foodtalks-series.js?test=' + Date.now())).default;

// 系列底部默认（与 api/rule_bottom_blocks.go defaultSeriesBottomBlocksModule 对齐）。
function seriesBottomBlocks() {
  return {
    enabled: true,
    order: ['author_card', 'bd_card', 'group_qr', 'more_articles'],
    blocks: {
      author_card: { type: 'image', enabled: true, fields: { image: '' } },
      bd_card: { type: 'image', enabled: true, fields: { image: '{{asset#ft_series_card_eileen}}' } },
      group_qr: { type: 'image', enabled: true, fields: { image: '{{asset#ft_series_group_packaging}}' } },
      more_articles: { type: 'more_articles', enabled: false, fields: { title: '/ 更多文章 /' } },
    },
    presets: [],
  };
}
const ASSET_LIB = {
  schema_version: 1,
  assets: [
    { id: 'ft_series_card_eileen', type: 'image', scope: 'foodtalks-series', url: '/assets/foodtalks-series/cards/eileen.jpg' },
    { id: 'ft_series_group_packaging', type: 'image', scope: 'foodtalks-series', url: '/assets/foodtalks-series/groups/packaging.png' },
    // 跨账号私有素材：必须解析不到（scope 隔离），不得泄漏进系列页脚
    { id: 'fbif_bobo_card', type: 'image', scope: 'fbif', url: '/assets/fbif/bobo-card.webp' },
  ],
};

function setActive(bb, override) {
  globalThis.window._activeRulePack = { id: 'foodtalks-series' };
  globalThis.window._activeConfig = {
    config: {},
    preset: { config: { modules: { bottom_blocks: bb, asset_library: ASSET_LIB } } },
  };
  globalThis.window._bottomBlocksOverride = override;
}

// 系列满宽图 section 字节口径（区别于 FBIF 的 661px）。
const SERIES_SECTION_STYLE = 'margin: 0 0 20px; text-align: center; line-height: 0; font-size: 0;';
const FULL_IMG_STYLE = 'width: 100%; max-width: 100%; height: auto; display: block; margin: 0 auto; vertical-align: top;';

test('renderBottomFooter: 系列默认 = Eileen 名片 + 包装群二维码 两张满宽 banner（author 空跳过、更多文章默认隐藏）', () => {
  setActive(seriesBottomBlocks(), undefined);
  const html = template.renderBottomFooter();
  assert.equal(typeof html, 'string', 'renderBottomFooter 返回字符串');
  assert.equal((html.match(/<section /g) || []).length, 2, '恰好 2 张图（author 空 + more_articles 禁用各跳过）');
  assert.match(html, /src="\/assets\/foodtalks-series\/cards\/eileen\.jpg"/, 'BD 名片=Eileen');
  assert.match(html, /src="\/assets\/foodtalks-series\/groups\/packaging\.png"/, '群二维码=包装');
  assert.ok(html.includes(SERIES_SECTION_STYLE), '系列 section 字节口径（满宽居中、line-height:0）');
  assert.ok(html.includes(FULL_IMG_STYLE), '图片满宽 width:100%（系列口径）');
});

test('renderBottomFooter: 不泄漏 FBIF 页脚口径（661px / mp-quote / 波波 / 1000）', () => {
  setActive(seriesBottomBlocks(), undefined);
  const html = template.renderBottomFooter();
  assert.ok(!html.includes('661px'), '非 FBIF 661px 满宽');
  assert.ok(!html.includes('mp-quote'), '非 FBIF mp-quote 字体栈');
  assert.ok(!/波波|1000个食品创新/.test(html), '无 FBIF 名片/案例引流内容');
  assert.ok(!html.includes('/assets/fbif/'), '跨账号 fbif 私有素材被 scope 隔离，不泄漏');
});

test('renderBottomFooter: 单篇 override 显隐生效（隐藏 bd_card → 只剩群二维码）', () => {
  setActive(seriesBottomBlocks(), { overrides: { bd_card: { enabled: false } } });
  const html = template.renderBottomFooter();
  assert.equal((html.match(/<section /g) || []).length, 1, '隐藏 BD 名片后只剩 1 张');
  assert.ok(!html.includes('eileen.jpg'), 'BD 名片已隐藏');
  assert.match(html, /packaging\.png/, '群二维码仍在');
});

test('renderBottomFooter: 模块禁用 → 返回 null（engine fallback footerUrl）', () => {
  const bb = seriesBottomBlocks(); bb.enabled = false;
  setActive(bb, undefined);
  assert.equal(template.renderBottomFooter(), null, 'enabled=false → null');
});
