import test from 'node:test';
import assert from 'node:assert/strict';
import { isMmbizUrl, materializeLazyImages, uploadAssetFile, uploadNonCdnImages } from '../public/js/uploader.js';

// ---- isMmbizUrl ----

test('isMmbizUrl detects mmbiz CDN URLs', () => {
  assert.equal(isMmbizUrl('https://mmbiz.qpic.cn/mmbiz_jpg/abc/640'), true);
  assert.equal(isMmbizUrl('http://mmbiz.qpic.cn/sz_mmbiz_gif/abc/640'), true);
  assert.equal(isMmbizUrl('https://mmecoa.qpic.cn/mmecoa_jpg/abc/640'), true);
});

test('isMmbizUrl rejects non-mmbiz URLs', () => {
  assert.equal(isMmbizUrl('https://example.com/image.jpg'), false);
  assert.equal(isMmbizUrl('https://cdn.example.com/mmbiz.jpg'), false);
  assert.equal(isMmbizUrl('https://other.qpic.cn/image.jpg'), false);
  assert.equal(isMmbizUrl(''), false);
});

test('isMmbizUrl is case insensitive', () => {
  assert.equal(isMmbizUrl('HTTPS://MMBIZ.QPIC.CN/test'), true);
});

test('uploadAssetFile rejects non-image files before hitting upload endpoint', async () => {
  await assert.rejects(
    () => uploadAssetFile(new Blob(['txt'], { type: 'text/plain' })),
    /请选择图片文件/
  );
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

test('materializeLazyImages replaces local asset and image proxy refs with data URLs', async (t) => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (url) => {
    calls.push(String(url));
    return new Response(new Blob([new Uint8Array([10, 11, 12])], { type: 'image/webp' }));
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  const result = await materializeLazyImages(
    '<p><img src="/assets/fbif/bobo-card.webp"></p>',
    '<p><img src="/api/image-proxy?url=https%3A%2F%2Fmmbiz.qpic.cn%2Fa.jpg"></p>'
  );

  assert.equal(result.failed, 0);
  assert.deepEqual(calls, [
    '/assets/fbif/bobo-card.webp',
    '/api/image-proxy?url=https%3A%2F%2Fmmbiz.qpic.cn%2Fa.jpg',
  ]);
  assert.doesNotMatch(result.articleCopy, /\/assets\/fbif/);
  assert.doesNotMatch(result.footerCopy, /\/api\/image-proxy/);
  assert.match(result.articleCopy, /src="data:image\/webp;base64,CgsM"/);
  assert.match(result.footerCopy, /src="data:image\/webp;base64,CgsM"/);
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

test('uploadNonCdnImages uploads local asset refs before copy', async (t) => {
  const originalFetch = globalThis.fetch;
  const uploads = [];
  globalThis.fetch = async (url, opts = {}) => {
    if (String(url) === '/api/oss-upload') {
      uploads.push(JSON.parse(opts.body));
      return new Response(JSON.stringify({ results: { img_0: 'https://oss.example/bobo-card.webp' } }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }
    return new Response(new Blob([new Uint8Array([13, 14, 15])], { type: 'image/webp' }));
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  const result = await uploadNonCdnImages(
    '',
    '<section><img src="/assets/fbif/bobo-card.webp"></section>'
  );

  assert.equal(result.failed, 0);
  assert.equal(uploads.length, 1);
  assert.doesNotMatch(result.footerCopy, /\/assets\/fbif/);
  assert.doesNotMatch(result.footerCopy, /data:image/);
  assert.match(result.footerCopy, /https:\/\/oss\.example\/bobo-card\.webp/);
});

test('uploadNonCdnImages: WeChat channel (transferMmbiz=false) leaves mmbiz URLs untouched', async (t) => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (url) => { calls.push(String(url)); return new Response('', { status: 200 }); };
  t.after(() => { globalThis.fetch = originalFetch; });

  const result = await uploadNonCdnImages(
    '<p><img src="https://mmbiz.qpic.cn/a.png"></p>',
    ''
  );

  assert.match(result.articleCopy, /mmbiz\.qpic\.cn\/a\.png/, 'mmbiz URL 应原样保留');
  assert.ok(!calls.some(u => u.includes('image-proxy')), '不应调用 image-proxy');
  assert.ok(!calls.some(u => u.includes('oss-upload')), '不应上传');
});

// 网站渠道把 mmbiz URL 列表整体发给 /api/oss-upload 的 urls 模式，后端并发取图+传 OSS，
// 返回 {mmbiz_url: oss_url} 映射；前端用映射替换 src。下列测试 mock 这个后端契约。
function mockOssUploadUrls(t, { mapper, capture } = {}) {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts = {}) => {
    if (String(url) === '/api/oss-upload') {
      const body = JSON.parse(opts.body || '{}');
      capture && capture(body);
      const results = {};
      for (const u of body.urls || []) {
        results[u] = mapper ? mapper(u) : ('https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/' + Math.abs(hashStr(u)) + '.png');
      }
      return new Response(JSON.stringify({ results }), { status: 200, headers: { 'content-type': 'application/json' } });
    }
    return new Response('');
  };
  t.after(() => { globalThis.fetch = originalFetch; });
}
function hashStr(s) { let h = 0; for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0; return h; }

test('uploadNonCdnImages: website channel (transferMmbiz=true) 发 urls 给后端并用映射替换', async (t) => {
  let captured = null;
  mockOssUploadUrls(t, { mapper: () => 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/abc.png', capture: b => (captured = b) });

  const result = await uploadNonCdnImages(
    '<p><img src="https://mmbiz.qpic.cn/a.png"></p>',
    '',
    { transferMmbiz: true }
  );

  assert.equal(result.failed, 0, '无失败');
  assert.deepEqual(captured.urls, ['https://mmbiz.qpic.cn/a.png'], '把 mmbiz URL 列表发给后端 urls 模式');
  assert.doesNotMatch(result.articleCopy, /mmbiz\.qpic\.cn/, 'mmbiz URL 应被替换');
  assert.match(result.articleCopy, /fbif-gary-img-hz\.oss-cn-hangzhou/, '替换为新桶 CDN URL');
});

test('uploadNonCdnImages: website channel 覆盖 mmecoa.qpic.cn 微信图床', async (t) => {
  let captured = null;
  mockOssUploadUrls(t, { mapper: () => 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/mmecoa.png', capture: b => (captured = b) });

  const result = await uploadNonCdnImages(
    '<p><img src="https://mmecoa.qpic.cn/mmecoa_jpg/abc/640"></p>',
    '',
    { transferMmbiz: true }
  );

  assert.deepEqual(captured.urls, ['https://mmecoa.qpic.cn/mmecoa_jpg/abc/640']);
  assert.equal(result.failed, 0);
  assert.doesNotMatch(result.articleCopy, /mmecoa\.qpic\.cn/, 'mmecoa URL 应被替换');
});

test('uploadNonCdnImages: website channel 同一 mmbiz URL 去重（只发一次）', async (t) => {
  let captured = null;
  mockOssUploadUrls(t, { capture: b => (captured = b) });

  await uploadNonCdnImages(
    '<p><img src="https://mmbiz.qpic.cn/same.png"></p><p><img src="https://mmbiz.qpic.cn/same.png"></p>',
    '',
    { transferMmbiz: true }
  );
  assert.equal(captured.urls.length, 1, '重复 mmbiz URL 去重后只发一次');
});

test('uploadNonCdnImages: website channel 多图全部正确替换（不串号）', async (t) => {
  // 每个 url 映射到含自身索引的唯一 CDN URL，验证替换没有张冠李戴
  mockOssUploadUrls(t, { mapper: u => 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/cdn-' + u.match(/img(\d+)/)[1] + '.png' });

  const N = 10;
  const html = Array.from({ length: N }, (_, i) => `<p><img src="https://mmbiz.qpic.cn/img${i}.png"></p>`).join('');
  const result = await uploadNonCdnImages(html, '', { transferMmbiz: true });

  assert.equal(result.failed, 0, '无失败');
  assert.doesNotMatch(result.articleCopy, /mmbiz\.qpic\.cn/, '所有 mmbiz URL 都被替换');
  for (let i = 0; i < N; i++) {
    assert.match(result.articleCopy, new RegExp('cdn-' + i + '\\.png'), `第 ${i} 张映射到正确的 CDN URL`);
  }
});

test('uploadNonCdnImages: website channel 后端转存失败的图保留原 mmbiz 链接并计入 failed', async (t) => {
  // 后端对某张返回 null（转存失败）→ 前端保留原 mmbiz、计 failed
  mockOssUploadUrls(t, { mapper: u => u.includes('bad') ? null : 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/ok.png' });

  const result = await uploadNonCdnImages(
    '<p><img src="https://mmbiz.qpic.cn/good.png"></p><p><img src="https://mmbiz.qpic.cn/bad.png"></p>',
    '',
    { transferMmbiz: true }
  );
  assert.equal(result.failed, 1, '1 张失败');
  assert.match(result.articleCopy, /mmbiz\.qpic\.cn\/bad\.png/, '失败的图保留原 mmbiz 链接');
  assert.doesNotMatch(result.articleCopy, /mmbiz\.qpic\.cn\/good\.png/, '成功的图被替换');
});
