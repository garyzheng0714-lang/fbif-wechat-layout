// 载体切换在真引擎上跑一遍：app.html 的页面结构 + engine.js + 真模板（fbif / sticker），
// 粘贴、切载体、复制、草稿恢复都走网页同一批函数，只把剪贴板换成记录器。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parseHTML } from 'linkedom';

const PUBLIC_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'public');
const APP_HTML = await readFile(path.join(PUBLIC_DIR, 'app.html'), 'utf8');
const SAMPLE = '“这是啥？”\n\n“马冬莓。”\n\n……行了，别问了。\n\n大九酿造展位：3C馆 K30（3楼，「马冬莓」等你！）';
const STICKER_HTML = '<p>“这是啥？”<br><br>“马冬莓。”<br><br>……行了，别问了。<br><br>大九酿造展位：3C馆 K30（3楼，「马冬莓」等你！）</p>';
const PASTE_TAIL = '<p style="margin:0;padding:0;line-height:0;font-size:0;min-height:0;height:0;"><br/></p>';

const store = new Map();
const storage = {
  getItem: key => (store.has(key) ? store.get(key) : null),
  setItem: (key, value) => { store.set(key, String(value)); },
  removeItem: key => { store.delete(key); },
  clear: () => { store.clear(); },
  key: index => [...store.keys()][index] ?? null,
  get length() { return store.size; },
};
const clipboardWrites = [];
globalThis.localStorage = storage;
globalThis.sessionStorage = storage;
globalThis.location = new URL('https://layout.test/mp/fbif');
globalThis.fetch = async input => {
  const url = new URL(String(input), 'https://layout.test');
  try {
    return new Response(await readFile(path.join(PUBLIC_DIR, decodeURIComponent(url.pathname))));
  } catch {
    return new Response('', { status: 404 });
  }
};
globalThis.ClipboardItem = class { constructor(items) { this.items = items; } };
Object.defineProperty(globalThis, 'navigator', {
  configurable: true,
  value: {
    clipboard: {
      async write([item]) {
        clipboardWrites.push({
          html: await item.items['text/html'].text(),
          text: await item.items['text/plain'].text(),
        });
      },
    },
  },
});

// 每次「打开页面」都是一份新的 app.html 结构，引擎在上面 initApp，与网页一样。
function openPage() {
  const markup = APP_HTML.slice(0, APP_HTML.indexOf('<script type="module">')) + '</body></html>';
  const { window, document } = parseHTML(markup);
  Object.assign(window, {
    localStorage: storage,
    sessionStorage: storage,
    location: globalThis.location,
    __APP_BUILD_INFO: { build: 'carrier-engine-test' },
    _activeRuleState: 'resolved',
    _activeRuleReady: true,
  });
  globalThis.window = window;
  globalThis.document = document;
  globalThis.CustomEvent = window.CustomEvent;
  globalThis.Node = window.Node;
  return { window, document, contentArea: () => document.getElementById('contentArea') };
}

openPage();
const mod = p => import(pathToFileURL(path.join(PUBLIC_DIR, 'js', p)).href);
const engine = await mod('engine.js');
const fbif = (await mod('templates/fbif.js')).default;
const sticker = (await mod('templates/sticker.js')).default;
const quiet = console.log;

async function settle(ms = 300) {
  await new Promise(resolve => setTimeout(resolve, ms));
}

async function pasteArticle(page) {
  page.window._handleText(SAMPLE);
  await settle();
  assert.match(page.contentArea().innerHTML, /马冬莓/);
}

async function copy(page) {
  const before = clipboardWrites.length;
  assert.equal(await page.window.copyContent(), true);
  assert.equal(clipboardWrites.length, before + 1);
  return clipboardWrites.at(-1);
}

test.before(() => { console.log = () => {}; });
test.after(() => { console.log = quiet; });

test('切到贴图：按缓存原文重排成一个 <p>，复制原样写它；切回文章与原来逐字一样', async () => {
  const page = openPage();
  engine.initApp(fbif);
  await pasteArticle(page);
  const articlePreview = page.contentArea().innerHTML;
  const articleCopy = await copy(page);
  assert.match(articleCopy.html, /<section/);
  assert.ok(articleCopy.html.endsWith(PASTE_TAIL), '文章复制仍带零高度光标段');

  await page.window._engineSwitchTemplate(sticker);
  await settle(20);
  assert.equal(page.contentArea().getAttribute('data-template'), 'sticker');
  assert.equal(page.contentArea().innerHTML.replace(/<!--[\s\S]*?-->/g, ''), STICKER_HTML);
  assert.ok(page.document.body.classList.contains('article-text-preview'), '贴图只排正文：不出标题与底栏');
  assert.equal(page.document.getElementById('articleTitle').textContent, '');
  assert.deepEqual(await copy(page), { html: STICKER_HTML, text: SAMPLE });

  await page.window._engineSwitchTemplate(fbif);
  await settle(20);
  assert.equal(page.contentArea().getAttribute('data-template'), 'fbif');
  assert.equal(page.contentArea().innerHTML, articlePreview);
  assert.deepEqual(await copy(page), articleCopy, '文章复制与切换前逐字一样');
});

test('连着切两次只认最后一次：先切贴图又马上切回文章，预览是文章', async () => {
  const page = openPage();
  engine.initApp(fbif);
  await pasteArticle(page);
  const articlePreview = page.contentArea().innerHTML;

  const toSticker = page.window._engineSwitchTemplate(sticker);
  const backToArticle = page.window._engineSwitchTemplate(fbif);
  await Promise.all([toSticker, backToArticle]);
  await settle(20);
  assert.equal(page.contentArea().getAttribute('data-template'), 'fbif');
  assert.equal(page.contentArea().innerHTML, articlePreview);
});

test('贴图下的草稿刷新后恢复成贴图预览，复制照样是贴图那份', async () => {
  const before = openPage();
  engine.initApp(sticker);
  await pasteArticle(before);
  assert.equal(before.contentArea().innerHTML.replace(/<!--[\s\S]*?-->/g, ''), STICKER_HTML);
  const snapshot = JSON.parse(JSON.stringify(before.window._captureSessionSnapshot()));

  const after = openPage();
  engine.initApp(sticker);
  assert.equal(after.window._restoreSessionSnapshot(snapshot), true);
  assert.equal(after.contentArea().innerHTML.replace(/<!--[\s\S]*?-->/g, ''), STICKER_HTML);
  assert.deepEqual(await copy(after), { html: STICKER_HTML, text: SAMPLE });
});

test('导入框里粘进来的链接随草稿保存：刷新后框里的字和链接都回来', async () => {
  const links = [{ text: '马冬莓', href: 'https://mp.weixin.qq.com/s/abc' }];
  const before = openPage();
  engine.initApp(fbif);
  await pasteArticle(before);
  const input = before.document.getElementById('urlInput');
  input.value = SAMPLE;
  input.dataset.links = JSON.stringify(links);
  const snapshot = JSON.parse(JSON.stringify(before.window._captureSessionSnapshot()));

  const after = openPage();
  engine.initApp(fbif);
  assert.equal(after.window._restoreSessionSnapshot(snapshot), true);
  const restored = after.document.getElementById('urlInput');
  assert.equal(restored.value, SAMPLE);
  assert.deepEqual(JSON.parse(restored.dataset.links), links);
});

test('文章草稿在贴图载体下恢复：按贴图从原文重排，不留文章预览', async () => {
  const before = openPage();
  engine.initApp(fbif);
  await pasteArticle(before);
  const snapshot = JSON.parse(JSON.stringify(before.window._captureSessionSnapshot()));

  const after = openPage();
  engine.initApp(sticker);
  assert.equal(after.window._restoreSessionSnapshot(snapshot), true);
  await settle(20);
  assert.equal(after.contentArea().innerHTML.replace(/<!--[\s\S]*?-->/g, ''), STICKER_HTML);
  assert.deepEqual(await copy(after), { html: STICKER_HTML, text: SAMPLE });
});

// 校对里点「改正」（红线 49）：同一台真引擎上跑，改的只是这一处，预览、复制、贴图那份都跟着改。
const TYPO_FIX = { wrong: '马冬莓', correct: '马冬梅', evidence: '“马冬莓。”' };

test('校对改正：只改原句里那一处，预览和复制都是改过的；撤销后与原来逐字一样', async () => {
  const page = openPage();
  engine.initApp(fbif);
  await pasteArticle(page);
  const original = page.contentArea().innerHTML;
  const originalCopy = await copy(page);

  assert.equal(await page.window._engineApi.setTextFixes([TYPO_FIX]), true);
  await settle(20);
  const fixedText = page.contentArea().textContent;
  assert.match(fixedText, /“马冬梅。”/);
  assert.match(fixedText, /「马冬莓」等你/, '别处同样的字不动');
  const session = page.window._engineApi.getArticleSession();
  assert.deepEqual(session.result.appliedTextFixes, [TYPO_FIX]);
  assert.match(session.result.sourceLines.join('\n'), /“马冬莓。”/, '没改过的原文留着，校对按它认是不是同一篇');
  const fixedCopy = await copy(page);
  assert.match(fixedCopy.html, /“马冬梅。”/);
  assert.doesNotMatch(fixedCopy.html, /“马冬莓。”/);

  await page.window._engineSwitchTemplate(sticker);
  await settle(20);
  assert.deepEqual(await copy(page), {
    html: STICKER_HTML.replace('“马冬莓。”', '“马冬梅。”'),
    text: SAMPLE.replace('“马冬莓。”', '“马冬梅。”'),
  }, '切到贴图改正还在，贴图自己的剪贴板内容也改了');
  await page.window._engineSwitchTemplate(fbif);
  await settle(20);

  assert.equal(await page.window._engineApi.setTextFixes([]), true);
  await settle(20);
  assert.equal(page.contentArea().innerHTML, original);
  assert.deepEqual(await copy(page), originalCopy);
});

test('校对改正跟着草稿走，新导入一篇自然清空', async () => {
  const before = openPage();
  engine.initApp(fbif);
  await pasteArticle(before);
  await before.window._engineApi.setTextFixes([TYPO_FIX]);
  await settle(20);
  const snapshot = JSON.parse(JSON.stringify(before.window._captureSessionSnapshot()));

  const after = openPage();
  engine.initApp(fbif);
  assert.equal(after.window._restoreSessionSnapshot(snapshot), true);
  assert.match(after.contentArea().textContent, /“马冬梅。”/);
  assert.deepEqual(after.window._engineApi.getArticleSession().result.appliedTextFixes, [TYPO_FIX]);
  assert.match((await copy(after)).html, /“马冬梅。”/);

  await pasteArticle(after);
  assert.match(after.contentArea().textContent, /“马冬莓。”/);
  assert.equal(after.window._engineApi.getArticleSession().result.appliedTextFixes, undefined);
});
