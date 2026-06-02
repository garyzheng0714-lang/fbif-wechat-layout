import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveAssetRefs } from '../public/js/asset-library.js';

test('resolveAssetRefs: usage 引用命中后替换 src，保留已有尺寸保证默认页脚字节稳定', () => {
  const lines = [
    '<p><img class="mc-img" src="{{asset:qr_group}}" alt="00" width="600" height="267" /></p>',
  ];
  const out = resolveAssetRefs(lines, {
    assets: [{
      id: 'ft_footer_group_qr',
      type: 'image',
      usage: 'qr_group',
      scope: 'global',
      url: 'https://static.foodtalks.cn/image/news/qr.png',
      width: 320,
      height: 120,
    }],
  }, 'ft-default');

  assert.deepEqual(out, [
    '<p><img class="mc-img" src="https://static.foodtalks.cn/image/news/qr.png" alt="00" width="600" height="267" /></p>',
  ]);
});

test('resolveAssetRefs: id 精确引用可为缺尺寸 img 回填 width/height', () => {
  const out = resolveAssetRefs([
    '<p><img src="{{asset#hero_banner}}" alt="" /></p>',
  ], {
    assets: [{
      id: 'hero_banner',
      type: 'image',
      usage: 'banner',
      scope: 'global',
      url: 'https://static.foodtalks.cn/image/news/hero.jpg',
      width: 300,
      height: 120,
    }],
  }, 'ft-default');

  assert.equal(out[0], '<p><img src="https://static.foodtalks.cn/image/news/hero.jpg" alt="" width="300" height="120" /></p>');
});

test('resolveAssetRefs: scope 过滤阻止网站渠道读取公众号专属素材', () => {
  const originalWarn = console.warn;
  console.warn = () => {};
  try {
    const out = resolveAssetRefs([
      '<p><img src="{{asset:qr_group}}" /></p>',
    ], {
      assets: [{
        id: 'mp_qr',
        type: 'image',
        usage: 'qr_group',
        scope: 'fbif',
        url: 'https://static.foodtalks.cn/image/news/mp-only.png',
      }],
    }, 'ft-default');

    assert.doesNotMatch(out[0], /mp-only/, '网站渠道不能解析 fbif scope 的素材');
    assert.match(out[0], /data:image\/gif/, '未命中时降级透明占位，避免输出原始占位符');
  } finally {
    console.warn = originalWarn;
  }
});

test('resolveAssetRefs: 默认素材库含系列素材且仅 foodtalks-series scope 可见', async () => {
  const { ASSET_LIBRARY_DEFAULT } = await import('../public/js/asset-library-defaults.js');
  const seriesAssets = ASSET_LIBRARY_DEFAULT.assets.filter(asset => asset.id.startsWith('ft_series_'));
  assert.equal(seriesAssets.filter(asset => asset.id.startsWith('ft_series_group_')).length, 18);
  assert.equal(seriesAssets.every(asset => asset.scope === 'foodtalks-series'), true);
});
