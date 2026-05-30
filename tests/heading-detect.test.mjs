import test from 'node:test';
import assert from 'node:assert/strict';
import {
  detectHeadingLevel,
  isBoldHeading,
  promoteHeuristicHeadings,
} from '../public/js/heading-detect.js';

// 测试数据取自金标准 docs/agents/baselines/62285-foodtalks-backend.html（Teavana）
// 和第 2 篇 news/62244（MATCH）的真实标题/正文，确保"以终为始"——判据对真实样本成立。

function boldP(text) {
  return { k: 'p', runs: [{ type: 'txt', text, bold: true }] };
}
function plainP(text) {
  return { k: 'p', runs: [{ type: 'txt', text }] };
}

// ---- isBoldHeading: 正样本（基石里真实的 h2 标题，原文都是整段加粗） ----

const REAL_HEADINGS = [
  '2个外行，教美国人喝茶',
  '320家店、2亿+美元营收',
  '星巴克史上最大的一笔收购，6.2亿美元',
  '疯狂的梦想：5年要开到1000家店',
  'Oprah说她从来没听过，转身创下史上最佳销售纪录',
  '“茶叶版星巴克”梦碎：379家店，全部关门',
  '2017年，门店内Teavana茶饮销售16亿美元',
  '无数美国人怀念它',
  'Teavana门店在日本、韩国的重生',
  '总结',
  // MATCH/62244 的真实标题
  '一瓶能“大口畅饮”的碳酸饮料',
  '聚焦高中生群体，All in校园场景',
];

for (const t of REAL_HEADINGS) {
  test(`isBoldHeading 识别真实加粗标题: ${t}`, () => {
    assert.equal(isBoldHeading([{ type: 'txt', text: t, bold: true }]), true);
  });
}

// ---- isBoldHeading: 负样本 ----

test('isBoldHeading 拒绝非加粗段（普通正文）', () => {
  assert.equal(isBoldHeading([{ type: 'txt', text: '这是我茶叶系列的第12篇。' }]), false);
});

test('isBoldHeading 拒绝加粗但以句号结尾的强调正文（MATCH 真实样本）', () => {
  // "如今的增长，多亏了17年前，MATCH主动将品牌的目标人群…" 整段加粗、45字、以句号结尾 → 不是标题
  const t = '如今的增长，多亏了17年前，MATCH主动将品牌的目标人群，从“年轻人”收窄至“高中生”。';
  assert.equal(isBoldHeading([{ type: 'txt', text: t, bold: true }]), false);
});

test('isBoldHeading 拒绝超长加粗段（>36 字）', () => {
  const t = '这是一段非常长的加粗文字'.repeat(4); // 约 44 字
  assert.equal(isBoldHeading([{ type: 'txt', text: t, bold: true }]), false);
});

test('isBoldHeading 拒绝部分加粗的正文（混排非粗体）', () => {
  // "收购后的计划更疯狂…要做成 <strong>茶叶版的星巴克</strong>，谁也无法抗拒…"
  // 段内只有一部分 bold → 不是标题
  const runs = [
    { type: 'txt', text: '收购后的计划更疯狂：5年内从300家开到1000家，要做成' },
    { type: 'txt', text: '茶叶版的星巴克', bold: true },
    { type: 'txt', text: '，谁也无法抗拒这个诱人的梦想。' },
  ];
  assert.equal(isBoldHeading(runs), false);
});

test('isBoldHeading 拒绝加粗的图注/来源行', () => {
  assert.equal(isBoldHeading([{ type: 'txt', text: '图片来源：大塚食品', bold: true }]), false);
  assert.equal(isBoldHeading([{ type: 'txt', text: '拍摄：Mote莫特', bold: true }]), false);
});

test('isBoldHeading 拒绝空 runs', () => {
  assert.equal(isBoldHeading([]), false);
  assert.equal(isBoldHeading([{ type: 'txt', text: '   ', bold: true }]), false);
});

// ---- detectHeadingLevel: 显式编号/关键词标题仍生效 ----

test('detectHeadingLevel 识别中文数字编号标题', () => {
  assert.equal(detectHeadingLevel('一、品牌起源'), 2);
  assert.equal(detectHeadingLevel('（一）背景'), 3);
});

test('detectHeadingLevel 识别关键词标题', () => {
  assert.equal(detectHeadingLevel('总结'), 2);
  assert.equal(detectHeadingLevel('背景介绍'), 2);
});

test('detectHeadingLevel 拒绝普通正文', () => {
  assert.equal(detectHeadingLevel('这是一段普通的正文内容。'), 0);
});

// ---- promoteHeuristicHeadings: 端到端把加粗段升级成 h2，正文不动 ----

test('promoteHeuristicHeadings 把加粗标题段升级为 h2、普通段保持 p', () => {
  const elems = [
    plainP('作者引子，一段普通正文，长度足够，以句号结尾。'),
    boldP('2个外行，教美国人喝茶'),
    plainP('Teavana并不是懂茶的人做出来的。'),
    boldP('总结'),
  ];
  const out = promoteHeuristicHeadings(elems);
  assert.equal(out[0].k, 'p', '普通正文保持 p');
  assert.equal(out[1].k, 'h', '加粗短句升级为 h');
  assert.equal(out[1].level, 2);
  assert.equal(out[2].k, 'p');
  assert.equal(out[3].k, 'h', '关键词标题升级为 h');
});

test('promoteHeuristicHeadings 不动已分类的 author/tip/cap/img 块', () => {
  const elems = [
    { k: 'author', text: '作者：Mote莫特' },
    { k: 'tip', runs: [{ type: 'txt', text: '提示：', bold: true }] },
    { k: 'cap', runs: [{ type: 'txt', text: '图注', bold: true }] },
    { k: 'img', src: 'x' },
  ];
  const out = promoteHeuristicHeadings(elems);
  assert.equal(out[0].k, 'author', 'author 不被误升级');
  assert.equal(out[1].k, 'tip', 'tip（整段加粗、短、不收尾）不被误升级');
  assert.equal(out[2].k, 'cap', 'cap 不被误升级');
  assert.equal(out[3].k, 'img');
});
