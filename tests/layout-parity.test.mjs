// 排版接口与网页编辑器逐字一样：拿网页编辑器真抓的复制结果（tests/fixtures/layout-parity，抓法见
// tools/layout-parity/README.md）当标准答案，7 个账号 × 5 种输入，接口的排版程序（render-service/layout.mjs）
// 排出来必须逐字相同。只允许两处不同，样稿里已经抹平：图片地址统一写成 IMG（网页转 data/OSS、接口由 Go 转存）；
// 公众号的「更多文章」在网页上用本篇显隐开关关掉后再抓（接口不出这一块）。
//
// 改了排版规则或渲染器、这里变红：先在网页上确认新样子是对的，再按 README 重抓样稿，不要改这个测试去迁就。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const FIXTURES = path.join(ROOT, 'tests/fixtures');
const PARITY = path.join(FIXTURES, 'layout-parity');
const ACCOUNTS = ['fbif', 'foodtalks-series', 'wowfood', 'ft-default', 'ft-series', 'foodtalks-mp', 'wechat-plain'];
const ARTICLE_URL = 'https://mp.weixin.qq.com/s/US1JYCoxRt-3WRgchINBDQ';
const PASTED_TEXT = '雀巢宣布收购某植物基品牌\n交易金额未披露，预计明年完成交割。\n双方表示将继续保留原品牌团队。';

const { loadLayoutEngine } = await import(path.join(ROOT, 'render-service/layout.mjs'));
const engine = await loadLayoutEngine({
  publicDir: path.join(ROOT, 'public'),
  fetchRules: account => readFile(path.join(PARITY, `rules--${account}.json`), 'utf8'),
  origin: 'http://127.0.0.1:19191',
  build: 'test',
});

const fetched = JSON.parse(await readFile(path.join(PARITY, 'url.fetch.json'), 'utf8'));

// 和 Go（api/layout_source.go）交给排版程序的载体同一个样子。
const INPUTS = {
  docx: async () => ({
    kind: 'docx',
    title: 'tea-pyramid',
    file_name: 'tea-pyramid.docx',
    docx_base64: (await readFile(path.join(FIXTURES, 'tea-pyramid.docx'))).toString('base64'),
  }),
  'docx-list': async () => ({
    kind: 'docx',
    title: 'feishu-ordered-list',
    file_name: 'feishu-ordered-list.docx',
    docx_base64: (await readFile(path.join(FIXTURES, 'feishu-ordered-list.docx'))).toString('base64'),
  }),
  markdown: async () => ({
    kind: 'markdown',
    file_name: 'layout-sample.md',
    text: await readFile(path.join(FIXTURES, 'layout-sample.md'), 'utf8'),
  }),
  text: async () => ({ kind: 'text', text: PASTED_TEXT }),
  url: async () => ({
    kind: 'blocks',
    title: fetched.title,
    source: ARTICLE_URL,
    source_name: fetched.source_name || '',
    byline: fetched.byline || '',
    blocks: fetched.blocks,
    fetch_meta: { title: fetched.title, source: ARTICLE_URL, source_name: fetched.source_name || '' },
  }),
};

function normalizeImageSources(html) {
  return String(html).replace(/(<img\b[^>]*?\ssrc=")[^"]*(")/gi, '$1IMG$2');
}

function firstDifference(got, want) {
  let i = 0;
  while (i < got.length && i < want.length && got[i] === want[i]) i++;
  return `第 ${i} 个字符起不同（接口 ${got.length} 字，网页 ${want.length} 字）\n接口：${got.slice(Math.max(0, i - 80), i + 120)}\n网页：${want.slice(Math.max(0, i - 80), i + 120)}`;
}

for (const account of ACCOUNTS) {
  for (const [id, carrier] of Object.entries(INPUTS)) {
    test(`排版接口与网页逐字一样：${account} × ${id}`, async () => {
      const out = await engine.layout({ account, title: '', repost: null, input: await carrier() });
      const got = normalizeImageSources(out.html);
      const want = await readFile(path.join(PARITY, `${account}--${id}.html`), 'utf8');
      assert.ok(got === want, firstDifference(got, want));
      assert.equal(out.repost, id === 'url', '链接默认转载，其余默认原创（和网页上同样操作一致）');
      if (id === 'text') assert.equal(out.footer, false, '粘贴文字只排正文不加页脚');
      assert.deepEqual(out.images, [...out.html.matchAll(/<img\b[^>]*?\ssrc="([^"]*)"/gi)].map(m => m[1]));
      assert.match(out.rule.config_hash, /^sha256:[0-9a-f]{64}$/);
    });
  }
}

test('排版接口：没有的账号、规则读不到、空文字都按错误码回', async () => {
  await assert.rejects(engine.layout({ account: 'nope', input: { kind: 'text', text: 'x' } }), { code: 'unknown_account' });
  await assert.rejects(engine.layout({ account: 'fbif', input: { kind: 'text', text: '   ' } }), { code: 'empty_article' });
  await assert.rejects(engine.layout({ account: 'fbif', input: { kind: 'pdf' } }), { code: 'unsupported_input' });
  await assert.rejects(
    engine.layout({ account: 'fbif', input: { kind: 'docx', docx_base64: Buffer.from('not a zip').toString('base64') } }),
    { code: 'word_unreadable' },
  );
});

test('排版接口：同时来的几篇排队一篇一篇排，互不串账号', async () => {
  const text = { kind: 'text', text: PASTED_TEXT };
  const [mp, site] = await Promise.all([
    engine.layout({ account: 'fbif', input: text }),
    engine.layout({ account: 'ft-default', input: text }),
  ]);
  assert.equal(normalizeImageSources(mp.html), await readFile(path.join(PARITY, 'fbif--text.html'), 'utf8'));
  assert.equal(normalizeImageSources(site.html), await readFile(path.join(PARITY, 'ft-default--text.html'), 'utf8'));
});
