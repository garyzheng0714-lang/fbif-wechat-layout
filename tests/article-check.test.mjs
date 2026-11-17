// 左栏「AI 校对」卡（public/js/article-check.js）：文章排好自动查、同一篇不重查、改正和撤销走引擎、最新的一篇说了算。

import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { mountArticleCheck, sessionSource } from '../public/js/article-check.js';

const FIX = { wrong: '重回重回', correct: '重回', evidence: '仿佛重回重回高中生物课堂' };
const ISSUE = { label: '数字与口径', level: 'warning', evidence: '市占率约为45%', reason: '前后两个数，必须二选一。' };
const BLOCK = { label: '合规提示', level: 'block', evidence: '增强免疫力', reason: '普通食品写「增强免疫力」。' };
const NOTE = { label: '事实与常识', level: 'warning', evidence: '经认证', reason: '可能已经变化，请核实。' };
const LINES = ['<p>仿佛重回重回高中生物课堂。</p>', '<p>市占率约为45%，增强免疫力，经认证。</p>'];

const view = (status, extra = {}) => ({ status, fixes: [], issues: [], notes: [], ...extra });
const tick = (ms = 0) => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label) {
  for (let i = 0; i < 200; i++) {
    if (check()) return;
    await tick(5);
  }
  assert.fail('等不到：' + label);
}

function session(lines = LINES, extra = {}) {
  return { title: '标题', bodyOnly: false, articleHtml: lines.join('\n'), result: { lines, title: '标题' }, input: {}, ...extra };
}

// 一个假页面：引擎的当前稿、改正入口和服务端都是假的；replies 按顺序回，回完了就一直挂着。
async function page({ replies = [], enabled = true } = {}) {
  const { document, window } = parseHTML('<!doctype html><html><body><h2 id="title"></h2><div id="content"></div><div id="check" hidden></div></body></html>');
  // linkedom 的 Range 没有 setStart/setEnd，这里补一个只记位置的。
  document.createRange = () => {
    const range = {
      setStart(node, offset) { range.startContainer = node; range.startOffset = offset; },
      setEnd(node, offset) { range.endContainer = node; range.endOffset = offset; },
    };
    return range;
  };
  const state = { session: null, posts: [], fixes: [] };
  const queue = replies.slice();
  const fetchImpl = (url, init) => {
    // 加载时那句「能不能用」：照 enabled 答，不算进 posts。
    if (!init.method) return Promise.resolve({ ok: true, status: 200, json: async () => ({ enabled }) });
    state.posts.push({ url, ...JSON.parse(init.body) });
    const next = queue.shift();
    if (!next) {
      return new Promise((_, reject) => init.signal.addEventListener('abort', () => reject(new Error('aborted'))));
    }
    if (next instanceof Error) return Promise.reject(next);
    const { status, body } = typeof next.status === 'number' ? next : { status: 200, body: next };
    return Promise.resolve({ ok: status >= 200 && status < 300, status, json: async () => body });
  };
  const show = next => {
    state.session = next;
    document.getElementById('content').innerHTML = next ? next.articleHtml : '';
    window.dispatchEvent(new window.CustomEvent(next ? 'fbif:article-session-activated' : 'fbif:article-cleared'));
  };
  const mount = document.getElementById('check');
  const { ready } = mountArticleCheck({
    mount,
    titleEl: document.getElementById('title'),
    contentEl: document.getElementById('content'),
    getSession: () => state.session,
    // 引擎套用改正后会带着同一份原文重新激活这篇。
    setTextFixes: async fixes => {
      state.fixes.push(fixes);
      show({ ...state.session, result: { ...state.session.result, sourceLines: LINES, appliedTextFixes: fixes } });
      return true;
    },
    fetchImpl,
    windowRef: window,
    timing: { start: 0, retries: [0, 0], gap: 0 },
  });
  state.enabled = await ready;
  return {
    state, mount, show,
    text: () => mount.textContent,
    rows: () => [...mount.querySelectorAll('.proof-row')].map(row => row.textContent),
    click: selector => mount.querySelector(selector).dispatchEvent(new window.Event('click', { bubbles: true })),
  };
}

test('这篇没改过的标题和正文：改正后看 sourceLines，贴图和粘贴的纯文字没有标题，没有正文不查', () => {
  assert.deepEqual(
    { ...sessionSource(session(['<p>改过的</p>'], { result: { lines: ['<p>改过的</p>'], sourceLines: LINES, sourceTitle: '原标题', title: '改过的标题' } })), lines: undefined },
    { title: '原标题', text: '仿佛重回重回高中生物课堂。\n市占率约为45%，增强免疫力，经认证。', lines: undefined },
  );
  assert.equal(sessionSource(session(LINES, { bodyOnly: true })).title, '');
  assert.equal(sessionSource({ title: '只恢复了预览', articleHtml: '<p>正文</p>' }).text, '正文');
  assert.equal(sessionSource(session(['<p><img src="a.png"></p>'])), null);
  assert.equal(sessionSource(null), null);
});

test('文章排好自动查：先出要核对的，带着已拿到的那一步接着等，查完列出改法', async () => {
  const p = await page({ replies: [
    view('verifying', { issues: [{ ...ISSUE }, { ...ISSUE, evidence: '仿佛重回重回高中生物课堂', reason: '叠字。' }] }),
    null, // 第二次请求先挂着：这时卡上是初检的条目，说正在复核
  ] });
  p.show(session());
  await until(() => p.state.posts.length === 2, '接着等复核');
  assert.match(p.text(), /正在复核改法/);
  assert.equal(p.rows().length, 2);
});

test('查完列出改法、要核对的、待核的；原句里标出出错的字', async () => {
  const p = await page({ replies: [
    view('verifying', { issues: [ISSUE] }),
    view('done', { fixes: [FIX], issues: [BLOCK, ISSUE], notes: [NOTE] }),
  ] });
  assert.equal(p.mount.hidden, true, '没有文章不出卡');
  p.show(session());
  assert.equal(p.mount.hidden, false);
  assert.match(p.text(), /AI 校对正在通读全文，约 3 分钟/);
  await until(() => p.state.posts.length === 2 && !/正在/.test(p.text()), '查完');
  assert.deepEqual(p.state.posts.map(post => [post.url, post.title, post.text, post.have, post.retry]), [
    ['/api/article-check', '标题', '仿佛重回重回高中生物课堂。\n市占率约为45%，增强免疫力，经认证。', '', false],
    ['/api/article-check', '标题', '仿佛重回重回高中生物课堂。\n市占率约为45%，增强免疫力，经认证。', 'verifying', false],
  ]);
  assert.deepEqual(p.rows(), [
    '重回重回→应为重回仿佛重回重回高中生物课堂改正',
    '阻断普通食品写「增强免疫力」。增强免疫力',
    '前后两个数，必须二选一。市占率约为45%',
    '可能已经变化，请核实。经认证',
  ]);
  assert.equal(p.mount.querySelector('.proof-notes summary').textContent, '待核 1 条');
  assert.equal(p.mount.querySelector('.proof-evidence mark').textContent, '重回重回', '原句里标出出错的字');
});

test('没发现错误说一句；同一篇再激活（换规则重排）不重查', async () => {
  const p = await page({ replies: [view('done')] });
  p.show(session());
  await until(() => /没发现错误/.test(p.text()), '查完');
  p.show(session());
  p.show(session(LINES, { title: '标题' }));
  await tick(20);
  assert.equal(p.state.posts.length, 1);
  assert.equal(p.rows().length, 0);
});

test('点改正交给引擎重排，改过的那行变成可撤销，不重查；全部改正一次交齐', async () => {
  const second = { wrong: '45%', correct: '54%', evidence: '市占率约为45%' };
  const p = await page({ replies: [view('done', { fixes: [FIX, second] })] });
  p.show(session());
  await until(() => p.rows().length === 2, '查完');
  assert.match(p.text(), /全部改正/);

  p.click('[data-fix="0"]');
  await until(() => /撤销/.test(p.rows()[0]), '改上');
  assert.deepEqual(p.state.fixes.at(-1), [FIX]);
  assert.doesNotMatch(p.text(), /全部改正/, '只剩一处没改就不出全部改正');

  p.click('[data-undo="0"]');
  await until(() => /改正$/.test(p.rows()[0]), '撤销');
  assert.deepEqual(p.state.fixes.at(-1), []);

  p.click('[data-fix-all]');
  await until(() => p.rows().every(row => /撤销$/.test(row)), '全部改上');
  assert.deepEqual(p.state.fixes.at(-1), [FIX, second]);
  assert.equal(p.state.posts.length, 1, '改正、撤销都不重查');
});

test('在这篇里找不到的改法只列出来，不给改正按钮', async () => {
  const lost = { wrong: '腌采', correct: '腌菜', evidence: '别的稿子里的腌采' };
  const p = await page({ replies: [view('done', { fixes: [lost] })] });
  p.show(session());
  await until(() => p.rows().length === 1, '查完');
  assert.equal(p.mount.querySelector('[data-fix]'), null);
});

test('这个环境没接模型：加载时问到了就整个不出卡，也不开查', async () => {
  const p = await page({ enabled: false, replies: [view('done')] });
  assert.equal(p.state.enabled, false);
  p.show(session());
  await tick(20);
  assert.equal(p.mount.hidden, true);
  assert.equal(p.state.posts.length, 0);
});

test('换了一篇：旧的那次作废，卡片只认最新这篇', async () => {
  const p = await page({ replies: [] });
  p.show(session());
  await until(() => p.state.posts.length === 1, '第一篇开查');
  p.show(session(['<p>另一篇。</p>']));
  await until(() => p.state.posts.length === 2, '第二篇开查');
  assert.equal(p.state.posts[1].text, '另一篇。');
  assert.match(p.text(), /正在通读全文/);
  p.show(null);
  assert.equal(p.mount.hidden, true, '清空文章收起卡片');
  assert.equal(p.mount.textContent, '');
});

test('没查成给重试：点了带 retry 重新交；内部原因不上页面', async () => {
  const p = await page({ replies: [view('failed', { error: '校对没完成' }), view('done')] });
  p.show(session());
  await until(() => /重试/.test(p.text()), '没查成');
  assert.equal(p.text(), 'AI 校对校对没完成重试');
  p.click('[data-retry]');
  await until(() => /没发现错误/.test(p.text()), '重试查完');
  assert.deepEqual(p.state.posts.map(post => post.retry), [false, true]);
});

test('断网、发版重启自己再试两次，还不行才说没完成；太频繁照服务端的话说', async () => {
  const flaky = await page({ replies: [new Error('offline'), { status: 502, body: {} }, view('done')] });
  flaky.show(session());
  await until(() => /没发现错误/.test(flaky.text()), '重试后查完');
  assert.equal(flaky.state.posts.length, 3);

  const down = await page({ replies: [new Error('offline'), new Error('offline'), new Error('offline'), view('done')] });
  down.show(session());
  await until(() => /校对没完成/.test(down.text()), '放弃');
  assert.equal(down.state.posts.length, 3, '试满就停，不一直打');

  const off = await page({ replies: [view('off')] });
  off.show(session());
  await until(() => off.state.posts.length === 1 && off.mount.hidden, '开查时才发现没接模型：收起卡');
  await tick(20);
  assert.equal(off.state.posts.length, 1, '没接模型不重试');

  const busy = await page({ replies: [{ status: 429, body: { error: '校对太频繁，请稍后再试' } }] });
  busy.show(session());
  await until(() => /校对太频繁/.test(busy.text()), '限流');
  assert.equal(busy.state.posts.length, 1, '被限流不自己重试');
});
