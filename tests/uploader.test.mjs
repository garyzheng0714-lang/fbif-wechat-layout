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

test('materializeLazyImages uses bounded parallel reads for image-heavy DOCX files', async (t) => {
  const originalFetch = globalThis.fetch;
  let active = 0;
  let maxActive = 0;
  const progress = [];
  globalThis.fetch = async () => {
    active++;
    maxActive = Math.max(maxActive, active);
    await new Promise(resolve => setTimeout(resolve, 10));
    active--;
    return new Response(new Blob([new Uint8Array([1, 2, 3])], { type: 'image/png' }));
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  const html = Array.from({ length: 10 }, (_, i) => (
    `<img src="blob:docx-image-${i}">`
  )).join('');
  const result = await materializeLazyImages(html, '', {
    onProgress(done, total) { progress.push([done, total]); },
  });

  assert.equal(result.failed, 0);
  assert.ok(maxActive > 1, `expected parallel image reads, observed ${maxActive}`);
  assert.ok(maxActive <= 4, `expected at most 4 parallel image reads, observed ${maxActive}`);
  assert.deepEqual(progress.at(-1), [10, 10]);
  assert.equal((result.articleCopy.match(/data:image\/png;base64,AQID/g) || []).length, 10);
});

test('materializeLazyImages accepts a higher bounded concurrency for atomic Feishu import', async (t) => {
  const originalFetch = globalThis.fetch;
  let active = 0;
  let maxActive = 0;
  globalThis.fetch = async () => {
    active++;
    maxActive = Math.max(maxActive, active);
    await new Promise(resolve => setTimeout(resolve, 8));
    active--;
    return new Response(new Blob([new Uint8Array([1])], { type: 'image/png' }));
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  const html = Array.from({ length: 12 }, (_, i) => `<img src="/api/doc-cache/hash/${i}.png">`).join('');
  const result = await materializeLazyImages(html, '', { concurrency: 6, timeoutMs: 90000 });

  assert.equal(result.failed, 0);
  assert.ok(maxActive > 4, `expected Feishu override above default concurrency, observed ${maxActive}`);
  assert.ok(maxActive <= 6, `expected at most six parallel reads, observed ${maxActive}`);
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

test('uploadNonCdnImages keeps safe bitmap data when optional OSS upload fails', async (t) => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () => new Response('busy', { status: 503 });
  t.after(() => { globalThis.fetch = originalFetch; });

  const safeData = 'data:image/png;base64,c2FmZS1iaXRtYXA=';
  const result = await uploadNonCdnImages(`<p><img src="${safeData}"></p>`, '');

  assert.equal(result.failed, 1);
  assert.match(result.articleCopy, new RegExp(safeData.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
});

test('uploadNonCdnImages leaves headroom under the server six-request concurrency guard', async (t) => {
  const originalFetch = globalThis.fetch;
  let active = 0;
  let maxActive = 0;
  globalThis.fetch = async (url, options = {}) => {
    assert.equal(String(url), '/api/oss-upload');
    const body = JSON.parse(options.body || '{}');
    const key = Object.keys(body.base64_images || {})[0];
    active++;
    maxActive = Math.max(maxActive, active);
    await new Promise(resolve => setTimeout(resolve, 5));
    active--;
    return new Response(JSON.stringify({ results: { [key]: `https://oss.example/${key}.png` } }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  const html = Array.from({ length: 12 }, (_, i) => (
    `<img src="data:image/png;base64,aW1hZ2Ut${i}">`
  )).join('');
  const result = await uploadNonCdnImages(html, '');

  assert.equal(result.failed, 0);
  assert.ok(maxActive <= 4, `expected at most 4 parallel uploads, observed ${maxActive}`);
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

test('copy-transfer progress has one combined denominator across mmbiz and base64 work', async (t) => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, options = {}) => {
    assert.equal(String(url), '/api/oss-upload');
    const body = JSON.parse(options.body || '{}');
    if (Array.isArray(body.urls)) {
      return new Response(JSON.stringify({
        results: Object.fromEntries(body.urls.map(src => [src, 'https://oss.example/mmbiz.png'])),
      }), { status: 200, headers: { 'content-type': 'application/json' } });
    }
    const key = Object.keys(body.base64_images || {})[0];
    return new Response(JSON.stringify({
      results: { [key]: `https://oss.example/${key}.png` },
    }), { status: 200, headers: { 'content-type': 'application/json' } });
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  const progress = [];
  const result = await uploadNonCdnImages(
    [
      '<img src="https://mmbiz.qpic.cn/one.png">',
      '<img src="https://mmbiz.qpic.cn/two.png">',
      '<img src="data:image/png;base64,AQID">',
    ].join(''),
    '',
    {
      transferMmbiz: true,
      onProgress(done, total) { progress.push([done, total]); },
    },
  );

  assert.equal(result.failed, 0);
  assert.deepEqual(progress, [[0, 3], [1, 3], [2, 3], [3, 3]]);
});

test('superseding an upload aborts remaining transfer work', async (t) => {
  const originalFetch = globalThis.fetch;
  const controller = new AbortController();
  let calls = 0;
  globalThis.fetch = async (_url, options = {}) => {
    calls++;
    return await new Promise((resolve, reject) => {
      options.signal?.addEventListener('abort', () => reject(
        Object.assign(new Error('aborted'), { name: 'AbortError' }),
      ), { once: true });
      setTimeout(() => resolve(new Response(JSON.stringify({
        results: { img_0: 'https://oss.example/late.png' },
      }), { status: 200, headers: { 'content-type': 'application/json' } })), 100);
    });
  };
  t.after(() => { globalThis.fetch = originalFetch; });

  const pending = uploadNonCdnImages(
    '<img src="data:image/png;base64,AQID"><img src="data:image/png;base64,BAUG">',
    '',
    { signal: controller.signal },
  );
  await new Promise(resolve => setTimeout(resolve, 5));
  controller.abort();

  await assert.rejects(pending, error => error?.name === 'AbortError');
  assert.ok(calls <= 2, `aborted upload started unexpected extra work: ${calls}`);
});
