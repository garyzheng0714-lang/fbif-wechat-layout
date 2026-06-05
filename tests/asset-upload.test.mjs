// asset-upload.test.mjs — 上传入库公共逻辑的意图测试（P4 选图上传，codex 条11）。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { uploadAssetIntoLibrary } from '../public/js/blocks/asset-upload.js';

test('uploadAssetIntoLibrary：上传并构造 asset（注入 uploadFn/genID/nowISO）', async () => {
  const asset = await uploadAssetIntoLibrary({
    file: { name: 'bobo-card.png' },
    scope: 'fbif',
    genID: () => 'asset_x',
    nowISO: '2026-06-05T00:00:00Z',
    uploadFn: async () => 'https://oss/bobo-card.png',
  });
  assert.equal(asset.id, 'asset_x');
  assert.equal(asset.url, 'https://oss/bobo-card.png');
  assert.equal(asset.scope, 'fbif');
  assert.equal(asset.label, 'bobo-card'); // 去扩展名
  assert.equal(asset.type, 'image');
  assert.equal(asset.usage, 'custom');
  assert.deepEqual(asset.tags, ['上传']);
  assert.equal(asset.updated_at, '2026-06-05T00:00:00Z');
});

test('uploadAssetIntoLibrary：缺省 genID 按 url 派生确定性 id（同 url 同 id）', async () => {
  const opts = { file: { name: 'x.png' }, scope: 'global', nowISO: 'T', uploadFn: async () => 'https://oss/x.png' };
  const a = await uploadAssetIntoLibrary(opts);
  const b = await uploadAssetIntoLibrary(opts);
  assert.equal(a.id, b.id);
  assert.match(a.id, /^asset_/);
});

test('uploadAssetIntoLibrary：无 file / 无 uploadFn 抛错', async () => {
  await assert.rejects(() => uploadAssetIntoLibrary({ uploadFn: async () => 'x' }));
  await assert.rejects(() => uploadAssetIntoLibrary({ file: { name: 'x' } }));
});
