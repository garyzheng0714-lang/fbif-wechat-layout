// block-panel.test.mjs — 前台底部区块内容面板。
// WHY：系列公众号每篇文章需要在左侧替换作者名片、BD 名片和入群二维码。
// 只要这些 image 块被默认标记 frontend_editable，面板就必须显示对应换图入口。

import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

const storage = new Map();
const { document, window } = parseHTML('<!doctype html><html><head></head><body><div id="mount"></div></body></html>');
globalThis.window = window;
globalThis.document = document;
globalThis.localStorage = {
  getItem(key) { return storage.has(key) ? storage.get(key) : null; },
  setItem(key, value) { storage.set(key, String(value)); },
  removeItem(key) { storage.delete(key); },
};

const { mountBlockPanel } = await import('../public/js/blocks/block-panel.js?panel=' + Date.now());

function seriesBottomBlocks() {
  return {
    schema_version: 2,
    enabled: true,
    active_preset_id: 'default',
    presets: [{
      id: 'default',
      name: '系列页脚',
      order: ['author_card', 'bd_card', 'group_qr', 'more_articles'],
      blocks: {
        author_card: {
          type: 'image',
          display_name: '作者名片',
          enabled: true,
          frontend_editable: true,
          fields: { image: '' },
        },
        bd_card: {
          type: 'image',
          display_name: 'BD 名片',
          enabled: true,
          frontend_editable: true,
          fields: { image: '{{asset#ft_series_card_eileen}}' },
        },
        group_qr: {
          type: 'image',
          display_name: '包装群二维码',
          enabled: true,
          frontend_editable: true,
          fields: { image: '{{asset#ft_series_group_packaging}}' },
        },
        more_articles: {
          type: 'more_articles',
          display_name: '推荐文章',
          enabled: false,
          fields: { title: '/ 更多文章 /' },
        },
      },
    }],
  };
}

const assetLibrary = {
  assets: [
    { id: 'ft_series_card_eileen', label: 'Eileen 名片', scope: 'foodtalks-series', url: '/assets/foodtalks-series/cards/eileen.jpg' },
    { id: 'ft_series_group_packaging', label: '包装群二维码', scope: 'foodtalks-series', url: '/assets/foodtalks-series/groups/packaging.png' },
  ],
};

test('系列公众号左侧面板显示作者名片、BD 名片和入群二维码换图入口', () => {
  storage.clear();
  const mount = document.getElementById('mount');
  const inst = mountBlockPanel({
    mount,
    accountId: 'foodtalks-series',
    getModule: seriesBottomBlocks,
    getAssetLibrary: () => assetLibrary,
    getBlockExtra: () => null,
    onChange: () => {},
    doc: document,
  });

  const titles = Array.from(mount.querySelectorAll('.bp-sec-title')).map(el => el.textContent.trim());
  assert.deepEqual(titles, ['作者名片', 'BD 名片', '包装群二维码']);
  assert.equal(mount.querySelectorAll('.bp-image-btn').length, 3, '三个图片区块都应有换图按钮');
  assert.equal(inst.hasEditableContent(), true);
});
