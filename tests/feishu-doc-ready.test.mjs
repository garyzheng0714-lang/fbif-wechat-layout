import test from 'node:test';
import assert from 'node:assert/strict';

import { prepareFeishuArticlePreview } from '../public/js/feishu-doc-ready.js';

test('Feishu readiness materializes, decodes, then returns one atomic preview', async () => {
  const calls = [];
  const progress = [];
  const image = { complete: true, naturalWidth: 800 };
  const result = await prepareFeishuArticlePreview(
    '<p><img src="/api/doc-cache/hash/image.png"></p>',
    {
      async materialize(articleHtml, footerHtml, options) {
        calls.push(['materialize', articleHtml, footerHtml, options.timeoutMs, options.concurrency]);
        options.onProgress(0, 1);
        options.onProgress(1, 1);
        return {
          articleCopy: '<p><img src="data:image/png;base64,AQID"></p>',
          footerCopy: '',
          failed: 0,
          failedSrcs: [],
          replacements: new Map([[
            '/api/doc-cache/hash/image.png',
            'data:image/png;base64,AQID',
          ]]),
        };
      },
      async preload(articleHtml, options) {
        calls.push(['preload', articleHtml, options.concurrency]);
        options.onProgress(1, 1);
        return {
          images: new Map([['data:image/png;base64,AQID', image]]),
          total: 1,
          failedSrcs: [],
        };
      },
      onProgress(update) { progress.push(update); },
    },
  );

  assert.equal(result.stale, false);
  assert.match(result.articleHtml, /data:image\/png;base64,AQID/);
  assert.equal(result.preparedImages.get('data:image/png;base64,AQID'), image);
  assert.equal(
    result.replacements.get('/api/doc-cache/hash/image.png'),
    'data:image/png;base64,AQID',
  );
  assert.deepEqual(calls.map(call => call[0]), ['materialize', 'preload']);
  assert.deepEqual(calls[0].slice(3), [90000, 6]);
  assert.deepEqual(progress.at(-1), { phase: 'decode', done: 1, total: 1 });
});

test('Feishu readiness fails closed before preview commit when an image is missing', async () => {
  let preloadCalls = 0;
  await assert.rejects(
    () => prepareFeishuArticlePreview('<img src="/api/doc-cache/hash/broken.png">', {
      async materialize() {
        return {
          articleCopy: '<img src="/api/doc-cache/hash/broken.png">',
          footerCopy: '',
          failed: 1,
          failedSrcs: ['/api/doc-cache/hash/broken.png'],
        };
      },
      async preload() {
        preloadCalls++;
        return { images: new Map(), total: 0, failedSrcs: [] };
      },
    }),
    /1 张图片下载失败.*保留当前预览/,
  );
  assert.equal(preloadCalls, 0);
});

test('a superseding article intent cancels Feishu readiness after await', async () => {
  let stale = false;
  let preloadCalls = 0;
  const result = await prepareFeishuArticlePreview('<p>old</p>', {
    async materialize() {
      stale = true;
      return { articleCopy: '<p>old-ready</p>', footerCopy: '', failed: 0, failedSrcs: [] };
    },
    async preload() {
      preloadCalls++;
      return { images: new Map(), total: 0, failedSrcs: [] };
    },
    isStale: () => stale,
  });

  assert.equal(result.stale, true);
  assert.equal(preloadCalls, 0);
});
