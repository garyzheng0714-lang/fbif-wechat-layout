import test from 'node:test';
import assert from 'node:assert/strict';

import {
  finalizeFeishuArticlePreview,
  prepareFeishuArticlePreview,
} from '../public/js/feishu-doc-ready.js';

test('Feishu preparation materializes every visible/copy asset before connected decode', async () => {
  const calls = [];
  const progress = [];
  const result = await prepareFeishuArticlePreview(
    '<p><img src="/api/doc-cache/hash/article.png"></p>',
    {
      previewFooterHtml: '<footer><img src="/api/doc-cache/hash/visible-footer.png"></footer>',
      footerHtml: '<footer><img src="/api/doc-cache/hash/copy-footer.png"></footer>',
      async materialize(articleHtml, footerHtml, options) {
        calls.push(['materialize', articleHtml, footerHtml, options.timeoutMs, options.concurrency]);
        options.onProgress(0, 3);
        options.onProgress(1, 3);
        options.onProgress(2, 3);
        options.onProgress(3, 3);
        const replacements = new Map([
          ['/api/doc-cache/hash/article.png', 'data:image/png;base64,ARTICLE'],
          ['/api/doc-cache/hash/visible-footer.png', 'data:image/png;base64,VISIBLE'],
          ['/api/doc-cache/hash/copy-footer.png', 'data:image/png;base64,COPY'],
        ]);
        const replace = html => String(html).replace(
          /\/api\/doc-cache\/hash\/(?:article|visible-footer|copy-footer)\.png/g,
          src => replacements.get(src),
        );
        return {
          articleCopy: replace(articleHtml),
          footerCopy: replace(footerHtml),
          failed: 0,
          failedSrcs: [],
          replacements,
        };
      },
      onProgress(update) { progress.push(update); },
    },
  );

  assert.equal(result.stale, false);
  assert.match(result.articleHtml, /base64,ARTICLE/);
  assert.match(result.previewFooterHtml, /base64,VISIBLE/);
  assert.match(result.footerCopy, /base64,COPY/);
  assert.equal(result.preparedImages.size, 0);
  assert.equal(
    result.replacements.get('/api/doc-cache/hash/article.png'),
    'data:image/png;base64,ARTICLE',
  );
  assert.deepEqual(result.imageRefs, [
    'data:image/png;base64,ARTICLE',
    'data:image/png;base64,VISIBLE',
  ]);
  assert.deepEqual(calls.map(call => call[0]), ['materialize']);
  assert.deepEqual(calls[0].slice(3), [90000, 6]);
  assert.deepEqual(progress, [
    { phase: 'materialize', done: 0, total: 3 },
    { phase: 'materialize', done: 1, total: 3 },
    { phase: 'materialize', done: 2, total: 3 },
    { phase: 'materialize', done: 3, total: 3 },
  ]);
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
  const result = await prepareFeishuArticlePreview('<p>old</p>', {
    async materialize() {
      stale = true;
      return { articleCopy: '<p>old-ready</p>', footerCopy: '', failed: 0, failedSrcs: [] };
    },
    isStale: () => stale,
  });

  assert.equal(result.stale, true);
});

test('Feishu preparation reports only real materialize counts before connected decode', async () => {
  const progress = [];

  await prepareFeishuArticlePreview('<img src="one"><img src="two">', {
    async materialize(articleHtml, footerHtml, options) {
      options.onProgress(0, 2);
      options.onProgress(1, 2);
      options.onProgress(2, 2);
      return {
        articleCopy: articleHtml,
        footerCopy: footerHtml,
        total: 2,
        failed: 0,
        failedSrcs: [],
        replacements: new Map(),
      };
    },
    onProgress(update) {
      progress.push(update);
    },
  });

  assert.deepEqual(progress, [
    { phase: 'materialize', done: 0, total: 2 },
    { phase: 'materialize', done: 1, total: 2 },
    { phase: 'materialize', done: 2, total: 2 },
  ]);
});

test('final Feishu refresh runs after other work and validates every visible occurrence', async () => {
  const progress = [];
  const repeated = [
    { complete: true, naturalWidth: 640 },
    { complete: true, naturalWidth: 640 },
  ];
  const footer = { complete: true, naturalWidth: 320 };
  const prepared = {
    stale: false,
    articleHtml: '<img src="oss:a"><img src="oss:a">',
    previewFooterHtml: '<footer><img src="oss:footer"></footer>',
    displayFooterHtml: '<footer><img src="oss:stale-footer"></footer>',
    footerCopy: '',
    preparedImages: new Map([
      ['oss:a', repeated],
      ['oss:footer', footer],
    ]),
    replacements: new Map(),
    imageRefs: ['oss:stale'],
  };
  let refreshOptions;
  const result = await finalizeFeishuArticlePreview(prepared, {
    async refresh(images, options) {
      refreshOptions = options;
      options.onProgress(0, 3);
      options.onProgress(1, 3);
      options.onProgress(2, 3);
      options.onProgress(3, 3);
      return { images, total: 3, failedSrcs: [] };
    },
    onProgress(update) { progress.push(update); },
  });

  assert.equal(result.stale, false);
  assert.equal(result.preparedImages, prepared.preparedImages);
  assert.deepEqual(refreshOptions.refs, ['oss:a', 'oss:a', 'oss:footer']);
  assert.equal(refreshOptions.concurrency, 6);
  assert.deepEqual(progress, [
    { phase: 'refresh', done: 0, total: 3 },
    { phase: 'refresh', done: 1, total: 3 },
    { phase: 'refresh', done: 2, total: 3 },
    { phase: 'refresh', done: 3, total: 3 },
  ]);
});

test('a final refresh failure rejects before the atomic preview commit', async () => {
  const prepared = {
    stale: false,
    articleHtml: '<img src="data:image/png;base64,AQID">',
    previewFooterHtml: '',
    footerCopy: '',
    preparedImages: new Map([[
      'data:image/png;base64,AQID',
      { complete: true, naturalWidth: 640 },
    ]]),
    replacements: new Map(),
    imageRefs: ['data:image/png;base64,AQID'],
  };
  await assert.rejects(
    () => finalizeFeishuArticlePreview(prepared, {
      async refresh(images, options) {
        options.onProgress(1, 1);
        return {
          images,
          total: 1,
          failedSrcs: ['data:image/png;base64,AQID'],
        };
      },
    }),
    /1 张图片.*最终校验失败.*保留当前预览/,
  );
});

test('final refresh restores an exact detached node that restarted after final attributes', async () => {
  const image = { complete: false, naturalWidth: 0 };
  const prepared = {
    stale: false,
    articleHtml: '<img src="oss:mote" referrerpolicy="no-referrer">',
    previewFooterHtml: '',
    footerCopy: '',
    preparedImages: new Map([['oss:mote', image]]),
    replacements: new Map(),
  };
  let refreshCalled = false;

  const result = await finalizeFeishuArticlePreview(prepared, {
    async refresh(images, options) {
      refreshCalled = true;
      assert.equal(images.get('oss:mote'), image);
      assert.deepEqual(options.refs, ['oss:mote']);
      image.complete = true;
      image.naturalWidth = 1001;
      options.onProgress(1, 1);
      return { images, total: 1, failedSrcs: [] };
    },
  });

  assert.equal(refreshCalled, true);
  assert.equal(result.preparedImages.get('oss:mote').naturalWidth, 1001);
});

test('an aborted final refresh returns stale without allowing a later commit', async () => {
  const controller = new AbortController();
  const image = { complete: true, naturalWidth: 640 };
  const prepared = {
    stale: false,
    articleHtml: '<img src="oss:old">',
    previewFooterHtml: '',
    footerCopy: '',
    preparedImages: new Map([['oss:old', image]]),
    imageRefs: ['oss:old'],
  };
  const pending = finalizeFeishuArticlePreview(prepared, {
    signal: controller.signal,
    async refresh() {
      return new Promise(resolve => {
        controller.signal.addEventListener('abort', () => resolve({
          images: prepared.preparedImages,
          total: 1,
          failedSrcs: [],
        }), { once: true });
      });
    },
  });
  controller.abort();
  const result = await pending;

  assert.equal(result.stale, true);
});
