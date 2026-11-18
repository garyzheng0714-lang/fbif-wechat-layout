// 排版接口与网页编辑器逐字一样：拿网页编辑器真抓的复制结果（tests/fixtures/layout-parity，抓法见
// tools/layout-parity/README.md）当标准答案，6 个账号 × 5 种输入，接口的排版程序（render-service/layout.mjs）
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
const ACCOUNTS = ['fbif', 'foodtalks-series', 'wowfood', 'ft-default', 'ft-series', 'foodtalks-mp'];
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

// .md 文件按 Markdown 认结构（parser.js markdownFileBlocks，三个模板共用；智能体交的 Markdown 也走这里，红线 51）：
// 不管哪个账号，成品里都不许漏出 #、-、> 记号（页脚自带的「* 本文为…」是规定样式，不算）；开头一行 # 是文章标题，## 是小标题。
test('Markdown 文件：每个账号都不漏记号，开头 # 是标题、## 是小标题', async () => {
  const input = await INPUTS.markdown();
  for (const account of ACCOUNTS) {
    const out = await engine.layout({ account, title: '', repost: null, input });
    const lines = out.html.replace(/<[^>]+>/g, '\n').split('\n').map(line => line.trim()).filter(Boolean);
    for (const line of lines) assert.doesNotMatch(line, /^(#{1,6}|-|>|&gt;)\s/, `${account} 漏了 Markdown 记号：${line}`);
    assert.ok(lines.includes('市场有多大') && lines.includes('品牌怎么做'), `${account} 的 ## 小标题没单独成行`);
    assert.ok(lines.some(line => line.endsWith('燕麦奶：份额第一')) && lines.some(line => line.endsWith('豆奶：老牌品类')), `${account} 的列表项没各自成行`);
    assert.equal(out.title, '植物基饮料的下一个十年', `${account} 没把开头 # 那行当标题`);
    assert.ok(!lines.includes('植物基饮料的下一个十年'), `${account} 把标题又排进了正文`);
  }
});

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

// Go 交来的 fetch_meta.provenance（和发布流程同一个原创/转载判定）照样写进网站开头署名和文末声明。
test('排版接口：抓取带来的原创/转载判定写进网站开头署名和文末声明', async () => {
  const blocks = [
    { k: 'txt', runs: [{ type: 'txt', text: '作者：董慧' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '正文第一段。' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '提示：' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '* 本文为转载文章。' }] },
  ];
  const input = provenance => ({
    kind: 'blocks', title: '标题', source: ARTICLE_URL, source_name: 'FBIF食品饮料创新', blocks,
    fetch_meta: { title: '标题', source: ARTICLE_URL, source_name: 'FBIF食品饮料创新', provenance },
  });
  const named = await engine.layout({ account: 'ft-default', title: '', repost: null, input: input({ original: false, source: '晚点LatePost' }) });
  assert.match(named.html, /本文为晚点LatePost原创，作者：董慧，转载请联系出处。/);
  assert.match(named.html, /\*本文为晚点LatePost原创/);
  const own = await engine.layout({ account: 'ft-default', title: '', repost: null, input: input({ original: true }) });
  assert.doesNotMatch(own.html, /晚点LatePost|本文为转载文章/);
  assert.match(own.html, /\*本文为FBIF食品饮料创新（ID：FoodInnovation）原创/);
});
