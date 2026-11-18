// 智能体交接（public/js/agent-handoff.js，红线 51）：打开 ?agent=<编号> 按智能体交来的同一份输入导入，
// 链接、文字走粘贴那条路，Markdown、Word 走上传那条路；编号从地址栏去掉，刷新不再导一遍。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parseHTML } from 'linkedom';
import { runAgentHandoff, takeAgentHandoffID } from '../public/js/agent-handoff.js';
import { bindUrlImportShortcuts } from '../public/js/editor-shell.js';

const ID = 'ag_AbCdEfGhIjKlMnOpQrStUv';

function fakeWindow(href) {
  const win = {
    location: { href },
    history: {
      state: { keep: true },
      replaceState(state, _title, url) {
        win.replaced = { state, url };
        win.location.href = new URL(url, href).href;
      },
    },
  };
  return win;
}

function json(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body, blob: async () => new Blob([body]) };
}

function page(href, replies) {
  const { document } = parseHTML('<!doctype html><html><body><div id="error" style="display:none"></div></body></html>');
  const calls = { fetched: [], submitted: [], texts: [], files: [], article: 0 };
  const windowRef = fakeWindow(href);
  const run = (extra = {}) => runAgentHandoff({
    windowRef,
    documentRef: document,
    fetchImpl: async (url, opts) => {
      calls.fetched.push({ url, opts });
      const reply = replies[url];
      if (reply instanceof Error) throw reply;
      return reply || json(404, { error: '没有' });
    },
    submit: value => { calls.submitted.push(value); return !value.startsWith('@'); },
    importText: text => calls.texts.push(text),
    uploadFile: file => calls.files.push(file),
    ensureArticle: async () => { calls.article++; },
    ...extra,
  });
  return { document, windowRef, calls, run, error: () => document.getElementById('error').textContent };
}

test('地址里的编号取出来就从地址栏去掉，别的参数和锚点照留', () => {
  const win = fakeWindow(`https://x.test/mp/fbif?a=1&agent=${ID}#top`);
  assert.equal(takeAgentHandoffID(win), ID);
  assert.deepEqual(win.replaced, { state: { keep: true }, url: '/mp/fbif?a=1#top' });
  assert.equal(takeAgentHandoffID(fakeWindow('https://x.test/mp/fbif')), '');
  const bad = fakeWindow('https://x.test/mp/fbif?agent=../../etc');
  assert.equal(takeAgentHandoffID(bad), '', '不像编号的不认');
  assert.equal(bad.replaced.url, '/mp/fbif', '不认的也从地址栏去掉');
});

test('链接、文字走粘贴那条路；先切回「文章」', async () => {
  const link = page(`https://x.test/mp/fbif?agent=${ID}`, {
    [`/api/agent/results/${ID}`]: json(200, { kind: 'url', url: 'https://mp.weixin.qq.com/s/abc' }),
  });
  assert.equal(await link.run(), 'imported');
  assert.deepEqual(link.calls.submitted, ['https://mp.weixin.qq.com/s/abc']);
  assert.equal(link.calls.article, 1);
  assert.equal(link.calls.fetched[0].opts.credentials, 'same-origin');

  const text = page(`https://x.test/mp/fbif?agent=${ID}`, {
    [`/api/agent/results/${ID}`]: json(200, { kind: 'text', text: '第一段\n\n第二段' }),
  });
  assert.equal(await text.run(), 'imported');
  assert.deepEqual(text.calls.submitted, ['第一段\n\n第二段']);
  assert.deepEqual(text.calls.texts, []);

  // 框里 @ 开头会被当成飞书搜索：粘贴那条路不收时直接按文字导入。
  const at = page(`https://x.test/mp/fbif?agent=${ID}`, {
    [`/api/agent/results/${ID}`]: json(200, { kind: 'text', text: '@品牌 新品上市' }),
  });
  await at.run();
  assert.deepEqual(at.calls.texts, ['@品牌 新品上市']);
});

test('Markdown、Word 走上传那条路，文件名和智能体那边一样', async () => {
  const md = page(`https://x.test/mp/fbif?agent=${ID}`, {
    [`/api/agent/results/${ID}`]: json(200, { kind: 'markdown', text: '# 茶\n\n正文', file_name: '茶的故事.md' }),
  });
  assert.equal(await md.run(), 'imported');
  assert.equal(md.calls.files.length, 1);
  assert.equal(md.calls.files[0].name, '茶的故事.md');
  assert.equal(await md.calls.files[0].text(), '# 茶\n\n正文');

  const word = page(`https://x.test/ft/ft-default?agent=${ID}`, {
    [`/api/agent/results/${ID}`]: json(200, { kind: 'word', file_name: '稿子.docx' }),
    [`/api/agent/results/${ID}/file`]: json(200, 'DOCX-BYTES'),
  });
  assert.equal(await word.run({ ensureArticle: null }), 'imported');
  assert.equal(word.calls.files[0].name, '稿子.docx');
  assert.equal(await word.calls.files[0].text(), 'DOCX-BYTES');
});

test('取不到写在导入卡的错误条，不导入', async () => {
  const gone = page(`https://x.test/mp/fbif?agent=${ID}`, {
    [`/api/agent/results/${ID}`]: json(404, { error: '这一篇已超过 7 天，请在智能体里重新排' }),
  });
  assert.equal(await gone.run(), 'failed');
  assert.equal(gone.error(), '这一篇已超过 7 天，请在智能体里重新排');
  assert.deepEqual(gone.calls.submitted, []);

  const offline = page(`https://x.test/mp/fbif?agent=${ID}`, { [`/api/agent/results/${ID}`]: new Error('断网') });
  assert.equal(await offline.run(), 'failed');
  assert.match(offline.error(), /重新打开智能体给的链接/);

  const none = page('https://x.test/mp/fbif', {});
  assert.equal(await none.run(), 'none');
  assert.equal(none.calls.fetched.length, 0);
});

test('粘贴那条路的 submit：框里换成这段字、清掉上次粘贴记下的链接', () => {
  const { document } = parseHTML('<!doctype html><html><body><textarea id="urlInput"></textarea></body></html>');
  const input = document.getElementById('urlInput');
  input.dataset.links = JSON.stringify([{ text: '旧', href: 'https://a.test' }]);
  const seen = [];
  const { submit } = bindUrlImportShortcuts({
    documentRef: document,
    fetchArticle: () => seen.push(['fetch', input.value]),
    importText: (text, { links }) => seen.push(['text', text, links]),
  });
  assert.equal(submit('第一段\n第二段'), true);
  assert.deepEqual(seen, [['text', '第一段\n第二段', []]]);
  assert.equal(submit('https://mp.weixin.qq.com/s/abc'), true);
  assert.deepEqual(seen[1], ['fetch', 'https://mp.weixin.qq.com/s/abc']);
});

test('两个编辑器都挂上交接，公众号编辑器先切回文章；上传走引擎同一个入口', () => {
  const app = readFileSync(new URL('../public/app.html', import.meta.url), 'utf8');
  const site = readFileSync(new URL('../public/site.html', import.meta.url), 'utf8');
  const engine = readFileSync(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  for (const html of [app, site]) {
    assert.match(html, /const urlImport = bindUrlImportShortcuts\(/);
    assert.match(html, /runAgentHandoff\(\{[\s\S]*?submit: value => urlImport\.submit\(value\)[\s\S]*?uploadFile: file => window\._uploadFiles\(\[file\]\)/);
  }
  assert.match(app, /ensureArticle: \(\) => carrier\.select\('article'\)/);
  assert.match(engine, /window\._uploadFiles = files => handleUploadFiles\(files, \{ isRepost: false \}\)/);
});
