// FoodTalks「什么是」系列分类层契约测试。
// 守护：标题分级（序号图序数）、目录边界、参考来源识别、推荐阅读不贪婪、尾部合成。
// 纯函数（rule-behavior-foodtalks-series.js 有 typeof window 守卫），Node 直接跑。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  classifySeries,
  buildSeriesFooterElems,
  getSeriesFooterSelection,
  sectionImageUrl,
} from '../public/js/rule-behavior-foodtalks-series.js';

const P = (text, extra = {}) => ({ k: 'txt', runs: [{ type: 'txt', text }], text, ...extra });

function build() {
  // 模拟 classifyDocxArticle 产出的中性 elem 序列（含 blankBefore 语义）。
  return [
    P('膨化食品包装生产效率翻倍？什么是安姆科柔印+挤复方案?'),
    P('包装交付周期缩短20%，能耗降低30%'),
    { k: 'img', src: '/head.png', w: '100%' },
    P('图片来源：乐事官网'),
    P('膨化食品包装生产效率实现进一步突破。', { blankBefore: true }),
    P('目录', { blankBefore: true }),
    P('一、千亿膨化食品迭代升级，包装降本增效压力凸显'),
    P('二、什么是安姆科"柔印+挤复"一体化方案？'),
    P('六、参考来源'),
    // 正文起点（blankBefore=true 切断目录）
    P('一、千亿膨化食品迭代升级，包装降本增效压力凸显', { blankBefore: true }),
    P('(一)膨化食品市场背景'),
    P('膨化食品正在经历一轮更细分的产品迭代。'),
    P('相关阅读：'),
    P('什么是安姆科纸基包装？碳足迹减半，重新定义食品纸包装'),
    P('打破不可回收"魔咒"，什么是安姆科可回收蒸煮包装？'),
    // 数字小标题：DOCX 标了标题样式 → 共享层产出 k:'h' 且只有 text、无 runs（真实形态）。
    // 它结束上面的推荐块，且本身不能被静默吞字（曾经的 bug）。
    { k: 'h', text: '2、个性与定制化' },
    P('在包装视觉和感官体验方面，安姆科可提供开窗设计、感官印刷、触感油墨等方案，这一段是正文不应进推荐列表。'),
    P('六、参考来源', { k: 'h', blankBefore: true }),
    P('[1] iimedia (2026), 《报告A》'),
    P('[2] FBIF (2025), 《报告B》'),
  ];
}

test('classifySeries: 一级标题按序数取序号图，"一、"前缀剥离', () => {
  const out = classifySeries(build(), {});
  const h1s = out.filter(e => e.k === 'h1');
  // 真正的一级标题：千亿…(1) + 参考来源(2)（目录里的不算）
  assert.equal(h1s.length, 2);
  assert.equal(h1s[0].index, 1);
  assert.equal(h1s[0].text, '千亿膨化食品迭代升级，包装降本增效压力凸显');
  assert.equal(h1s[1].index, 2);
  assert.equal(h1s[1].text, '参考来源');
});

test('classifySeries: 目录标题 + 条目，遇 blankBefore 正文起点截断', () => {
  const out = classifySeries(build(), {});
  assert.equal(out.filter(e => e.k === 'toc-title').length, 1);
  const tocItems = out.filter(e => e.k === 'toc-item');
  assert.equal(tocItems.length, 3); // 一、二、六（目录三条），不含正文起点的一、
  assert.ok(tocItems[0].text.startsWith('一、'));
});

test('classifySeries: 二级标题半角括号转中文', () => {
  const out = classifySeries(build(), {});
  const h2 = out.find(e => e.k === 'h2');
  assert.ok(h2);
  assert.equal(h2.text, '（一）膨化食品市场背景');
});

test('classifySeries: 参考来源条目识别为 ref-item', () => {
  const out = classifySeries(build(), {});
  const refs = out.filter(e => e.k === 'ref-item');
  assert.equal(refs.length, 2);
});

test('classifySeries: 推荐阅读不贪婪——遇加粗小标题/正文即停', () => {
  const out = classifySeries(build(), {});
  const recItems = out.filter(e => e.k === 'rec-item');
  assert.equal(recItems.length, 2); // 仅两条交叉引用标题
  // 正文「在包装视觉…」不应被并入推荐
  const recTexts = recItems.map(e => (e.runs || []).map(r => r.text).join(''));
  assert.ok(recTexts.every(t => !t.includes('正文不应进推荐列表')));
});

test('classifySeries: DOCX 标题样式段(k:h 仅 text 无 runs)不丢字，归三级标题', () => {
  // 回归：共享 classifyDocxArticle 给标题段产出 {k:"h", text} 而非 runs。数字小标题
  // 「2、个性与定制化」不匹配 一、/（一），曾因渲染只读 runs 而被静默吞字。
  const elems = [
    { k: 'txt', runs: [{ type: 'txt', text: '相关阅读：' }], text: '相关阅读：' },
    { k: 'txt', runs: [{ type: 'txt', text: '什么是X？' }], text: '什么是X？' },
    { k: 'h', text: '2、个性与定制化' },
    { k: 'txt', runs: [{ type: 'txt', text: '正文段落。' }], text: '正文段落。', blankBefore: true },
  ];
  const out = classifySeries(elems, {});
  const h3 = out.find(e => e.k === 'h3');
  assert.ok(h3, '数字小标题应归为三级标题(h3)');
  assert.equal((h3.runs || []).map(r => r.text).join(''), '2、个性与定制化');
  const allText = out.map(e => e.text || (e.runs || []).map(r => r.text).join('')).join('|');
  assert.ok(allText.includes('个性与定制化'), '小标题文字不能丢失');
});

test('classifySeries: 推荐阅读条目清除超链接（无 link run）', () => {
  const elems = [
    { k: 'txt', runs: [{ type: 'txt', text: '相关阅读：' }], text: '相关阅读：' },
    { k: 'txt', runs: [{ type: 'link', text: '什么是X？', href: 'https://e.com' }], text: '什么是X？' },
    { k: 'txt', runs: [{ type: 'txt', text: '正文段。' }], text: '正文段。', blankBefore: true },
  ];
  const out = classifySeries(elems, {});
  const rec = out.find(e => e.k === 'rec-item');
  assert.ok(rec);
  assert.ok(rec.runs.every(r => r.type !== 'link'), '推荐条目不应保留 link run');
});

test('buildSeriesFooterElems: 默认带 Eileen + 包装群，无作者名片', () => {
  const sel = getSeriesFooterSelection({});
  assert.equal(sel.group, '包装');
  assert.equal(sel.include_eileen, true);
  const footer = buildSeriesFooterElems({});
  const srcs = footer.map(e => e.src);
  assert.ok(srcs.some(s => s.includes('eileen')), '应含 Eileen 名片');
  assert.ok(srcs.some(s => s.includes('packaging')), '应含包装群二维码');
  assert.ok(!srcs.some(s => s.includes('cards/aimee')), '默认不预选作者名片');
});

test('sectionImageUrl: 按序数返回序号图，缺号返回空串', () => {
  assert.ok(sectionImageUrl({}, 1).includes('sections/01.png'));
  assert.ok(sectionImageUrl({}, 6).includes('sections/06.png'));
  assert.equal(sectionImageUrl({}, 99), '');
});

test('classifySeries: config.behavior 正则可覆盖默认（红线：规则后台可配）', () => {
  // 用自定义 level1 正则（仅认「第N章」），默认的「一、」就不再当一级标题
  const cfg = { behavior: { params: { level1_regex: { default: '^第[一二三]章\\s*' } } } };
  const elems = [
    P('第一章 开篇'),
    P('一、不再是一级标题'),
  ];
  const out = classifySeries(elems, cfg);
  assert.equal(out.filter(e => e.k === 'h1').length, 1);
  assert.equal(out.find(e => e.k === 'h1').text, '开篇');
});
