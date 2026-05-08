import test from 'node:test';
import assert from 'node:assert/strict';
import { isMmbizUrl, materializeLazyImages, uploadNonCdnImages } from '../public/js/uploader.js';

// ---- isMmbizUrl ----

test('isMmbizUrl detects mmbiz CDN URLs', () => {
  assert.equal(isMmbizUrl('https://mmbiz.qpic.cn/mmbiz_jpg/abc/640'), true);
  assert.equal(isMmbizUrl('http://mmbiz.qpic.cn/sz_mmbiz_gif/abc/640'), true);
});

test('isMmbizUrl rejects non-mmbiz URLs', () => {
  assert.equal(isMmbizUrl('https://example.com/image.jpg'), false);
  assert.equal(isMmbizUrl('https://cdn.example.com/mmbiz.jpg'), false);
  assert.equal(isMmbizUrl(''), false);
});

test('isMmbizUrl is case insensitive', () => {
  assert.equal(isMmbizUrl('HTTPS://MMBIZ.QPIC.CN/test'), true);
});

test('materializeLazyImages replaces DOCX blob/cache refs with data URLs', async (t) => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    const bytes = String(url).startsWith('blob:')
      ? new Uint8Array([1, 2, 3])
      : new Uint8Array([4, 5, 6]);
    return new Response(new Blob([bytes], { type: 'image/png' }));
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  const result = await materializeLazyImages(
    '<p><img src="blob:local-image"></p>',
    '<p><img src="/api/doc-cache/hash/image.png"></p>'
  );

  assert.equal(result.failed, 0);
  assert.doesNotMatch(result.articleCopy, /blob:/);
  assert.doesNotMatch(result.footerCopy, /\/api\/doc-cache\//);
  assert.match(result.articleCopy, /src="data:image\/png;base64,AQID"/);
  assert.match(result.footerCopy, /src="data:image\/png;base64,BAUG"/);
});

test('uploadNonCdnImages uses refreshed HTML after lazy image materialization', async (t) => {
  const originalFetch = globalThis.fetch;
  const uploads = [];
  globalThis.fetch = async (url, opts = {}) => {
    if (String(url) === '/api/oss-upload') {
      uploads.push(JSON.parse(opts.body));
      return new Response(JSON.stringify({ results: { img_0: 'https://oss.example/img_0.png' } }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }
    return new Response(new Blob([new Uint8Array([7, 8, 9])], { type: 'image/png' }));
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  let materialized = null;
  const result = await uploadNonCdnImages(
    '<p><img src="blob:local-image"></p><p><img src="https://mmbiz.qpic.cn/a.png"></p>',
    '',
    { onMaterialized: r => { materialized = r; } }
  );

  assert.equal(materialized.failed, 0);
  assert.equal(uploads.length, 1);
  assert.doesNotMatch(result.articleCopy, /blob:/);
  assert.match(result.articleCopy, /https:\/\/oss\.example\/img_0\.png/);
});
