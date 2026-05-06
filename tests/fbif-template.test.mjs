import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';

const storage = {
  getItem() { return null; },
  setItem() {},
  removeItem() {},
};

globalThis.window = {
  __APP_BUILD_INFO: { build: 'test' },
  localStorage: storage,
};
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });

const template = (await import('../public/js/templates/fbif.js?test=' + Date.now())).default;
const moteImageUrl = 'https://fbif-feishu-base.oss-cn-shanghai.aliyuncs.com/fbif-attachment-to-url/2026/05/tblMQeXvSGd7Hebf_-CkOuPdpNzb-_3u9Rxc5KA_1778028720411/img_v3_0211e_8b79127d-72a5-4011-9f01-fe3b30c7548g_1778028720565.png';

function p(text, extra = {}) {
  return {
    text,
    runs: text ? [{ type: 'txt', text }] : [],
    isEmpty: !String(text || '').trim(),
    ...extra,
  };
}

function h(text) {
  return p(text, { hasHeadingStyle: true });
}

function renderDocx(paragraphs, opts = {}) {
  return template.processDocx({
    paragraphs,
    imgCache: {},
    isRepost: !!opts.isRepost,
  }).lines.join('\n');
}

test('docx lead author starts the article and hides front matter', () => {
  const html = renderDocx([
    p('不应显示的标题'),
    h('头图'),
    h('推荐语'),
    p('不应显示的推荐语'),
    h('正文'),
    p('作者：Mote莫特'),
    p('第一段内容'),
    h('参考来源'),
    p('[1] ALDI source'),
  ]);

  assert.match(html, /class="wx-ta wx-ta-author">作者：Mote莫特/);
  assert.equal((html.match(/作者：Mote莫特/g) || []).length, 1);
  assert.doesNotMatch(html, /不应显示的标题|不应显示的推荐语|>头图<|>推荐语<|>正文</);
  assert.match(html, /第一段内容/);

  const imageIdx = html.indexOf(moteImageUrl);
  const refIdx = html.indexOf('参考来源：');
  assert.ok(imageIdx >= 0, 'Mote image should be inserted');
  assert.ok(refIdx > imageIdx, 'Mote image should appear above reference heading');
});

test('docx Mote author accepts the spaced name form', () => {
  const html = renderDocx([
    p('作者：Mote 莫特'),
    p('正文内容'),
    h('参考来源'),
    p('[1] source'),
  ]);

  assert.match(html, /作者：Mote 莫特/);
  assert.match(html, new RegExp(moteImageUrl.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
});

test('original author color matches RGB 57,111,188', async () => {
  const css = await fs.readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');
  assert.match(css, /\.wx-ta-author\s*\{\s*color:\s*#396FBC;\s*font-style:\s*normal;\s*\}/);
});
