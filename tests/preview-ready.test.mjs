import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

import {
  pendingPreviewRefs,
  decodePreviewImages,
  preparePreviewArticle,
} from '../public/js/preview-ready.js';

function readyImage() {
  return {
    complete: true,
    naturalWidth: 800,
    decode: () => Promise.resolve(),
    set src(value) { this._src = value; },
    get src() { return this._src; },
  };
}

function brokenImage() {
  return {
    complete: false,
    naturalWidth: 0,
    decode: () => Promise.reject(new Error('broken bytes')),
    set src(value) { this._src = value; },
    get src() { return this._src; },
  };
}

test('pendingPreviewRefs 只认站内占位，不误伤本地与远程图', () => {
  const html = [
    '<img src="/api/doc-cache/hash/a.png">',
    '<img src="https://example.com/api/doc-cache/hash/b.png">',
    '<img src="/api/image-proxy?url=x">',
    '<img src="blob:https://example.com/123">',
    '<img src="/assets/logo.png">',
    '<img src="data:image/png;base64,AQID">',
    '<img src="https://mmbiz.qpic.cn/pic.jpg">',
    '<img src="/api/doc-cache/hash/a.png">',
  ].join('');
  assert.deepEqual(pendingPreviewRefs(html), [
    '/api/doc-cache/hash/a.png',
    'https://example.com/api/doc-cache/hash/b.png',
    '/api/image-proxy?url=x',
  ]);
});

test('无占位正文原样放行，不触发 materialize', async () => {
  let materializeCalls = 0;
  const html = '<p>正文</p><img src="blob:https://example.com/1">';
  const result = await preparePreviewArticle(html, {
    async materialize() { materializeCalls++; return {}; },
  });
  assert.equal(result.changed, false);
  assert.equal(result.stale, false);
  assert.equal(result.articleHtml, html);
  assert.equal(materializeCalls, 0);
});

test('占位正文必须回填+解码成功后才返回就绪预览', async () => {
  const calls = [];
  const progress = [];
  const result = await preparePreviewArticle('<p><img src="/api/doc-cache/hash/img.png"></p>', {
    async materialize(articleHtml, footerHtml, options) {
      calls.push('materialize');
      assert.equal(footerHtml, '');
      options.onProgress(1, 1);
      return {
        articleCopy: '<p><img src="data:image/png;base64,AQID"></p>',
        footerCopy: '',
        failed: 0,
        failedSrcs: [],
        replacements: new Map([['/api/doc-cache/hash/img.png', 'data:image/png;base64,AQID']]),
      };
    },
    createImage: () => { calls.push('decode'); return readyImage(); },
    onProgress(update) { progress.push(update); },
  });

  assert.equal(result.stale, false);
  assert.equal(result.changed, true);
  assert.match(result.articleHtml, /data:image\/png;base64,AQID/);
  assert.equal(result.replacements.get('/api/doc-cache/hash/img.png'), 'data:image/png;base64,AQID');
  assert.deepEqual(calls, ['materialize', 'decode']);
  assert.deepEqual(progress[0], { phase: 'download', done: 1, total: 1 });
  assert.deepEqual(progress.at(-1), { phase: 'decode', done: 1, total: 1 });
});

test('回填失败 fail closed：报错且不进入解码', async () => {
  let decodeCalls = 0;
  await assert.rejects(
    () => preparePreviewArticle('<img src="/api/doc-cache/hash/broken.png">', {
      async materialize() {
        return {
          articleCopy: '<img src="/api/doc-cache/hash/broken.png">',
          footerCopy: '',
          failed: 1,
          failedSrcs: ['/api/doc-cache/hash/broken.png'],
        };
      },
      createImage: () => { decodeCalls++; return readyImage(); },
    }),
    /1 张图片下载失败/,
  );
  assert.equal(decodeCalls, 0);
});

test('解码失败 fail closed：绝不交付显示不出来的预览', async () => {
  await assert.rejects(
    () => preparePreviewArticle('<img src="/api/doc-cache/hash/bad.png">', {
      async materialize() {
        return {
          articleCopy: '<img src="data:image/png;base64,BAD0">',
          footerCopy: '',
          failed: 0,
          failedSrcs: [],
          replacements: new Map(),
        };
      },
      createImage: brokenImage,
    }),
    /1 张图片无法显示/,
  );
});

test('新导入意图令旧准备作废：stale 返回且不报错', async () => {
  let stale = false;
  let decodeCalls = 0;
  const result = await preparePreviewArticle('<img src="/api/doc-cache/hash/old.png">', {
    async materialize() {
      stale = true;
      return { articleCopy: '<img src="data:image/png;base64,AQID">', footerCopy: '', failed: 0, failedSrcs: [] };
    },
    createImage: () => { decodeCalls++; return readyImage(); },
    isStale: () => stale,
  });
  assert.equal(result.stale, true);
  assert.equal(result.changed, false);
  assert.equal(decodeCalls, 0);
});

test('decodePreviewImages 只解码 data: 位图并汇报失败清单', async () => {
  const html = [
    '<img src="data:image/png;base64,OK01">',
    '<img src="data:image/png;base64,BAD1">',
    '<img src="https://example.com/remote.png">',
  ].join('');
  const result = await decodePreviewImages(html, {
    createImage: () => ({
      complete: false,
      naturalWidth: 0,
      decode() {
        return this._src.includes('OK01') ? Promise.resolve() : Promise.reject(new Error('bad'));
      },
      set src(value) {
        this._src = value;
        if (value.includes('OK01')) { this.complete = true; this.naturalWidth = 10; }
      },
      get src() { return this._src; },
    }),
  });
  assert.equal(result.total, 2);
  assert.deepEqual(result.failedSrcs, ['data:image/png;base64,BAD1']);
});

test('文件预览不等待图片就绪门，同时保留飞书导入进度反馈', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const handleStart = engine.indexOf('async function handleFile(');
  const handleEnd = engine.indexOf('\n  // Dual-layer rendering:', handleStart);
  assert.ok(handleStart >= 0 && handleEnd > handleStart, '必须能定位真实 handleFile');
  const handleFile = engine.slice(handleStart, handleEnd);

  assert.doesNotMatch(handleFile, /await\s+(?:preparePreviewArticle|materializeLazyImages|uploadNonCdnImages)/);
  assert.doesNotMatch(engine, /import\('\.\/preview-ready\.js/);

  const feishuImport = await readFile(new URL('../public/js/feishu-doc-import.js', import.meta.url), 'utf8');
  assert.match(feishuImport, /飞书正在生成 Word/);
  assert.match(feishuImport, /正在接收文档/);
  assert.match(feishuImport, /正在提取正文并排版/);
});
