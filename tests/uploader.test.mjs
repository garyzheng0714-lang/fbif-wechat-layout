import test from 'node:test';
import assert from 'node:assert/strict';
import { isMmbizUrl, materializeLazyImages } from '../public/js/uploader.js';

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

// Note: uploadNonCdnImages requires fetch() which is only available in
// Node 18+ with --experimental-fetch or Node 21+. The core logic is
// tested via isMmbizUrl; integration tests for upload need a browser or
// a mock server environment.
