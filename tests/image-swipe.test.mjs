// 公众号滑动图（红线 50；2026-11-18 Dudu：「排版工具有可能排版滑动图吗？（目前排版工具是将滑动图一张张摊开）」）。
// 守：提示语认法与 Go 外发那份用同一份样例；原文记了组号的图、飞书/Word 稿图注写了「左右滑动查看」的图
// 排成滑动图（两套公众号模板）；格子之间没有空白；复制进公众号后台的样式里横滑还在。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };
globalThis.window = { __APP_BUILD_INFO: { build: 'test' }, localStorage: storage };
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });

const swipe = await import('../public/js/templates/shared/image-swipe.js?test=' + Date.now());
const fbif = (await import('../public/js/templates/fbif.js?test=' + Date.now())).default;
const series = (await import('../public/js/templates/foodtalks-series.js?test=' + Date.now())).default;
const { processForCopy } = await import('../public/js/css-inline.js?test=' + Date.now());
const { DEFAULT_RULE_CONFIG } = await import('../public/js/rule-presets.js?test-swipe=' + Date.now());
const samples = JSON.parse(await readFile(new URL('../api/testdata/swipe-hints.json', import.meta.url), 'utf8'));
const themeCSS = await readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');

const img = (name, extra = {}) => ({ k: 'img', src: `https://mmbiz.qpic.cn/${name}/640`, dataW: '1080', ...extra });
const cap = text => ({ k: 'cap', text });
const txt = text => ({ k: 'txt', runs: [{ type: 'txt', text }] });

function swipeBoxes(html) {
  return html.match(/<section style="display: inline-block; width: 100%; vertical-align: top; overflow-x: auto;[\s\S]*?<\/section><\/section><\/section>(?:<\/section>)?/g) || [];
}

function imagesIn(html) {
  return [...html.matchAll(/<img src="([^"]+)"/g)].map(m => m[1].replace(/^https:\/\/mmbiz\.qpic\.cn\//, '').replace(/\/640$/, ''));
}

test('滑动提示：与 Go 外发同一份样例（api/testdata/swipe-hints.json）', () => {
  assert.ok(samples.strip.length > 10);
  for (const [input, want] of samples.strip) {
    assert.equal(swipe.stripSwipeHint(input), want, input);
  }
  for (const [input, want] of samples.horizontal) {
    assert.equal(swipe.isHorizontalSwipeHint(input), want, input);
  }
});

test('认组：原文记了组号的连成一组；飞书、Word 稿只认紧跟着「左右滑动」图注的两张以上', () => {
  const marked = swipe.groupImageSwipes([img('a1', { swipe: 1 }), img('a2', { swipe: 1 }), img('b1', { swipe: 2 }), img('b2', { swipe: 2 }), cap('说明')]);
  assert.deepEqual(marked.map(e => e.k), ['swipe', 'swipe', 'cap']);
  assert.deepEqual(marked[0].items.map(e => e.src.split('/')[3]), ['a1', 'a2']);

  const plain = [img('x1'), img('x2'), cap('产品包装（左右滑动查看）')];
  assert.equal(swipe.groupImageSwipes(plain), plain, '没开 byCaption 不按图注认');
  assert.deepEqual(swipe.groupImageSwipes(plain, { byCaption: true }).map(e => e.k), ['swipe', 'cap']);
  assert.deepEqual(swipe.groupImageSwipes([img('x1'), img('x2'), cap('品牌视觉'), cap('←向左滑动查看更多')], { byCaption: true }).map(e => e.k), ['swipe', 'cap', 'cap']);
  assert.deepEqual(swipe.groupImageSwipes([img('x1'), img('x2'), txt('左右滑动查看')], { byCaption: true }).map(e => e.k), ['swipe', 'txt']);

  for (const [name, elems] of Object.entries({
    '只有一张': [img('x1'), cap('左右滑动查看')],
    '图注没写滑动': [img('x1'), img('x2'), cap('图片来源：So Acai')],
    '上下滑的长图': [img('x1'), img('x2'), cap('长图（上下滑动查看）')],
    '提示在正文里': [img('x1'), img('x2'), txt('销量一路下滑，左右滑动查看更多数据见下文')],
    '作者名片不并进去': [img('x1'), { ...img('card'), className: 'wx-mote-author', role: 'mote-author-card' }, cap('左右滑动查看')],
  })) {
    const out = swipe.groupImageSwipes(elems, { byCaption: true });
    if (name === '作者名片不并进去') {
      assert.deepEqual(out.map(e => e.k), ['img', 'img', 'cap'], name);
    } else {
      assert.equal(out.some(e => e.k === 'swipe'), false, name);
    }
  }
});

test('排法：照 135 滑动图结构，N 格各 100/N（不超 100%），格子之间没有空白', () => {
  const html = swipe.renderImageSwipe([1, 2, 3, 4, 5, 6].map(n => ({ n })), item => `<img src="${item.n}" />`);
  assert.match(html, /^<section style="display: inline-block; width: 100%; vertical-align: top; overflow-x: auto; box-sizing: border-box;"><section style="overflow: hidden; width: 600%; max-width: 600% !important; box-sizing: border-box;">/);
  const widths = [...html.matchAll(/display: inline-block; width: ([0-9.]+)%; vertical-align: top; box-sizing/g)].map(m => Number(m[1]));
  assert.equal(widths.length, 6);
  assert.ok(widths.reduce((a, b) => a + b, 0) <= 100, '六格加起来超过 100% 最后一格会掉到下一行');
  assert.doesNotMatch(html, />\s+</, '格子之间不能有空白字符');
});

test('FBIF 公众号：原文的滑动图排成滑动图，图注跟在后面，单张图照旧；复制进后台横滑还在', () => {
  const html = fbif.processBlocks({
    title: '设计一个持续生长的超级食物品牌',
    blocks: [
      txt('正文第一段。'),
      img('a1', { swipe: 1 }), img('a2', { swipe: 1 }), img('a3', { swipe: 1 }),
      cap('So Acai品牌视觉：以呈现超级食物本身为主'),
      cap('←向左滑动查看更多'),
      txt('正文第二段。'),
      img('single'),
      cap('图片来源：So Acai'),
    ],
  }, { isRepost: true }).lines.join('\n');
  const boxes = swipeBoxes(html);
  assert.equal(boxes.length, 1);
  assert.deepEqual(imagesIn(boxes[0]), ['a1', 'a2', 'a3']);
  assert.match(html, /<section class="wx-pi"><section style="display: inline-block; width: 100%;[^"]*overflow-x: auto;/, '图与图注之间不留段距');
  assert.ok(html.indexOf('a3/640') < html.indexOf('So Acai品牌视觉') && html.indexOf('So Acai品牌视觉') < html.indexOf('←向左滑动查看更多'), '图注照原文顺序跟在后面');
  assert.match(html, /<section class="wx-pi[^"]*"><img src="https:\/\/mmbiz\.qpic\.cn\/single\/640"/, '单张图照旧');

  const copy = processForCopy(html, themeCSS, DEFAULT_RULE_CONFIG);
  const box = copy.slice(copy.lastIndexOf('<section', copy.indexOf('overflow-x: auto')));
  assert.match(box, /overflow-x: auto/);
  assert.match(box, /width: 300%; max-width: 300% !important/);
  assert.equal((box.match(/width: 33\.3333%/g) || []).length, 3);
  const outer = copy.slice(copy.lastIndexOf('<section', copy.lastIndexOf('<section', copy.indexOf('overflow-x: auto')) - 1), copy.indexOf('overflow-x: auto'));
  assert.match(outer, /margin-left: 8px/, '滑动图左右缩进与单张图一样');
});

test('FBIF 公众号：飞书、Word 稿连着几张图、图注写了「左右滑动查看」才排成滑动图', () => {
  const imgP = file => ({ text: '', runs: [{ type: 'img', file, w: '100%' }], isEmpty: false, hasImg: true });
  const p = (text, extra = {}) => ({ text, runs: [{ type: 'txt', text }], isEmpty: false, ...extra });
  const render = paragraphs => fbif.processDocx({
    paragraphs: [p('作者：张三'), p('正文第一段。'), ...paragraphs, p('正文最后一段。')],
    imgCache: { 'a.png': 'https://cdn.example/a.png', 'b.png': 'https://cdn.example/b.png', 'c.png': 'https://cdn.example/c.png' },
  }).lines.join('\n');

  const swiped = render([imgP('a.png'), imgP('b.png'), imgP('c.png'), p('喜茶的周边（左右滑动查看）', { align: 'center' })]);
  assert.equal(swipeBoxes(swiped).length, 1);
  assert.match(swiped, /喜茶的周边（左右滑动查看）/, '图注照原样留着');

  const stacked = render([imgP('a.png'), imgP('b.png'), p('喜茶的周边', { align: 'center' })]);
  assert.equal(swipeBoxes(stacked).length, 0);
  assert.equal((stacked.match(/<img /g) || []).length, 2);
});

test('「什么是」系列 / FoodTalks食品资讯：原文的滑动图同样排成滑动图，缩进照本账号图片缩进', () => {
  const out = series.processBlocks({
    title: '什么是巴西莓',
    blocks: [
      img('head'),
      txt('正文第一段。'),
      img('b1', { swipe: 1 }), img('b2', { swipe: 1 }),
      cap('巴西莓系列产品包装'),
      txt('正文第二段。'),
    ],
  }, { config: {} }).lines.join('\n');
  const boxes = swipeBoxes(out);
  assert.equal(boxes.length, 1);
  assert.deepEqual(imagesIn(boxes[0]), ['b1', 'b2']);
  assert.match(out, /<section style="margin: 0 [0-9a-z]+ 0px; text-align: center; line-height: 0; font-size: 0;"><section style="display: inline-block; width: 100%;[^"]*overflow-x: auto;/, '滑动图紧贴图注');

  const imgP = (file) => ({ text: '', runs: [{ type: 'img', file, w: '100%' }], isEmpty: false, hasImg: true, align: 'center', spacing: {} });
  const p = (text, extra = {}) => ({ text, runs: [{ type: 'txt', text }], isEmpty: false, align: 'left', spacing: {}, ...extra });
  const docx = series.processDocx({
    paragraphs: [p('什么是巴西莓'), imgP('a.png'), p('正文。'), imgP('b.png'), imgP('c.png'), p('左右滑动查看更多', { align: 'center' }), p('正文二。')],
    imgCache: { 'a.png': 'https://cdn.example/a.png', 'b.png': 'https://cdn.example/b.png', 'c.png': 'https://cdn.example/c.png' },
    config: {},
  }).lines.join('\n');
  assert.equal(swipeBoxes(docx).length, 1);
});

test('FoodTalks 网站（去提示）：图注里的「左右滑动查看」去掉，整条只有提示的不要', () => {
  const elems = [
    { k: 'img', src: 'a' },
    { k: 'img', src: 'b' },
    { k: 'cap', runs: [{ type: 'txt', text: 'So Acai品牌视觉' }] },
    { k: 'cap', runs: [{ type: 'txt', text: '←向左滑动查看更多' }] },
    { k: 'p', runs: [{ type: 'txt', text: '正文。' }] },
    { k: 'img', src: 'c' },
    { k: 'cap', runs: [{ type: 'txt', text: '喜茶的周边' }, { type: 'txt', text: '（左右滑动查看）', italic: true }] },
    { k: 'p', runs: [{ type: 'txt', text: '左右滑动查看' }] },
    { k: 'p', runs: [{ type: 'txt', text: '销量下滑，左右滑动查看也没用。' }] },
  ];
  const out = swipe.withoutSwipeHints(elems);
  assert.deepEqual(out.map(e => [e.k, (e.runs || []).map(r => r.text).join('')]), [
    ['img', ''], ['img', ''],
    ['cap', 'So Acai品牌视觉'],
    ['p', '正文。'],
    ['img', ''],
    ['cap', '喜茶的周边'],
    ['p', '销量下滑，左右滑动查看也没用。'],
  ]);
  const untouched = [{ k: 'img', src: 'a' }, { k: 'cap', runs: [{ type: 'txt', text: '图片来源：So Acai' }] }];
  assert.equal(swipe.withoutSwipeHints(untouched), untouched);
});
