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

test('classifySeries: 推荐阅读块末尾补 14px 灰斜体说明（规范§二·七，可配文案）', () => {
  // 规范要求每个推荐阅读块下方补一行说明；此前被误删，本测试守护其回归。
  const out = classifySeries(build(), {});
  const notes = out.filter(e => e.k === 'rec-note');
  assert.equal(notes.length, 1, '本块有条目 → 补一条说明');
  assert.match(notes[0].text, /查看更多推荐阅读/);
  // 说明必须紧跟在推荐条目之后（块尾），不在条目之前
  const lastRecItem = out.map(e => e.k).lastIndexOf('rec-item');
  const noteIdx = out.map(e => e.k).indexOf('rec-note');
  assert.ok(noteIdx > lastRecItem, '说明行在最后一条 rec-item 之后');
});

test('classifySeries: 推荐阅读说明文案走 behavior.templates 可覆盖（红线：规则后台可配）', () => {
  const cfg = { behavior: { templates: { recommended_reading_note: '自定义说明X' } } };
  const out = classifySeries(build(), cfg);
  const note = out.find(e => e.k === 'rec-note');
  assert.equal(note.text, '自定义说明X');
});

test('classifySeries: 无条目的推荐阅读块不补孤立说明行', () => {
  const elems = [
    { k: 'txt', runs: [{ type: 'txt', text: '相关阅读：' }], text: '相关阅读：' },
    { k: 'h', text: '正文标题' }, // 紧跟标题 → 推荐块无条目
  ];
  const out = classifySeries(elems, {});
  assert.equal(out.filter(e => e.k === 'rec-note').length, 0, '无条目不补说明');
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

test('classifySeries: 参考来源条目清除超链接（规范§六 非本号链接全部清除）', () => {
  const elems = [
    P('六、参考来源'),
    { k: 'txt', runs: [{ type: 'txt', text: '[1] iiMedia (2026), ' }, { type: 'link', text: '报告链接', href: 'https://e.com/r' }], text: '[1] iiMedia (2026), 报告链接' },
  ];
  const out = classifySeries(elems, {});
  const ref = out.find(e => e.k === 'ref-item');
  assert.ok(ref, '应识别为参考条目');
  assert.ok(ref.runs.every(r => r.type !== 'link'), '参考条目外链必须降级为纯文本');
  assert.match(ref.runs.map(r => r.text).join(''), /报告链接/, '链接文字保留为纯文本');
});

test('buildSeriesFooterElems: 默认 BD 名片=Eileen（取自 cards 库）+ 包装群，无作者名片', () => {
  const sel = getSeriesFooterSelection({});
  assert.equal(sel.group, '包装');
  assert.equal(sel.bd_card, 'Eileen');
  const footer = buildSeriesFooterElems({});
  const srcs = footer.map(e => e.src);
  assert.ok(srcs.some(s => s.includes('cards/eileen')), '应含 Eileen BD 名片（从 cards 库取）');
  assert.ok(srcs.some(s => s.includes('packaging')), '应含包装群二维码');
  assert.ok(!srcs.some(s => s.includes('cards/aimee')), '默认不预选作者名片');
});

test('buildSeriesFooterElems: 默认 BD 名片走规则后台 footer_default.bd_card 可改', () => {
  // 红线：规则不硬编码——默认 BD 名片对象由 config.modules.ft_series_materials.footer_default 决定。
  const cfg = { modules: { ft_series_materials: { footer_default: { group: '包装', bd_card: 'Isaac' } } } };
  const sel = getSeriesFooterSelection(cfg);
  assert.equal(sel.bd_card, 'Isaac');
  const srcs = buildSeriesFooterElems(cfg).map(e => e.src);
  assert.ok(srcs.some(s => s.includes('cards/isaac')), 'BD 名片改 Isaac → 取 cards/isaac.jpg');
  assert.ok(!srcs.some(s => s.includes('cards/eileen')), '不再带默认 Eileen');
});

test('buildSeriesFooterElems: BD 名片可置空（不带）', () => {
  const cfg = { modules: { ft_series_materials: { footer_default: { group: '', bd_card: '' } } } };
  assert.equal(buildSeriesFooterElems(cfg).length, 0, 'BD/群/作者全空 → 尾部无图');
});

test('buildSeriesFooterElems: 新增群品类（渠道/代工/酒类/食品资讯/方便速食）可选', () => {
  for (const [cat, slug] of [['渠道','channel'],['代工','oem'],['酒类','alcohol'],['食品资讯','news'],['方便速食','convenience']]) {
    const cfg = { modules: { ft_series_materials: { footer_default: { group: cat, bd_card: '' } } } };
    const srcs = buildSeriesFooterElems(cfg).map(e => e.src);
    assert.ok(srcs.some(s => s.includes('groups/' + slug)), `${cat} → groups/${slug}.png`);
  }
});

test('sectionImageUrl: 按序数返回序号图，缺号返回空串', () => {
  assert.ok(sectionImageUrl({}, 1).includes('sections/01.png'));
  assert.ok(sectionImageUrl({}, 6).includes('sections/06.png'));
  assert.equal(sectionImageUrl({}, 99), '');
});

test('classifySeries: 文末署名/图源说明 → footer-note（成品 12px #7f7f7f 斜体），不算正文', () => {
  // WHY：成品文末「本文作者：X」「本文为标注来源的图片均来自X」是 12px #7f7f7f 斜体小字，
  // 此前误渲成 15px #544545 普通正文（用户报「引用来源字体格式不对」）。
  const out = classifySeries([
    P('正文引子。'),
    P('本文为标注来源的图片均来自安姆科'),
    P('本文作者：Isaac, FoodTalks'),
  ], {});
  assert.equal(out.filter(e => e.k === 'footer-note').length, 2);
  assert.equal(out.filter(e => e.k === 'p').length, 1, '只有引子是正文');
});

test('classifySeries: 文末「提示：」→ tip-title，「* …」声明 → tip-item', () => {
  const out = classifySeries([P('提示：'), P('* 本文为FoodTalks原创首发，欢迎无条件转发朋友圈。')], {});
  assert.ok(out.find(e => e.k === 'tip-title'), '「提示：」标签');
  assert.ok(out.find(e => e.k === 'tip-item'), '「* …」声明条目');
});

test('classifySeries: 无序号「参考来源：」段兜底进入参考模式 → [n] 归 ref-item（灰色）', () => {
  // WHY：WPS 导出常把「参考来源：」丢成无序号普通段，一级标题正则不命中。若不兜底，
  // [n] 条目会渲成 #544545 正文而非 #888888 灰，参考来源「字体格式不对」。
  const out = classifySeries([P('参考来源：'), P('[1] 报告A'), P('[2] 报告B')], {});
  assert.equal(out.filter(e => e.k === 'ref-item').length, 2);
});

test('classifySeries: 联系小节（倒数第二个一级标题含「联系」）公司信息居中、描述左对齐', () => {
  // WHY：成品「联系XXX」下方公司信息（办公室名/地址/官网/电话）居中，描述性正文左对齐。
  const out = classifySeries([
    P('五、联系安姆科'),
    P('安姆科总部位于上海，热诚欢迎各位前来参观交流。'),                              // 描述 → 左
    { k: 'txt', runs: [{ type: 'txt', text: '上海总部办公室', bold: true }], text: '上海总部办公室' }, // 加粗 → 居中
    P('地址：上海市闵行区申长路988弄'),                                            // 字段 → 居中
    P('官网：https://www.amcor.com/'),
    P('六、参考来源'),
    P('[1] 报告A'),
  ], {});
  const txt = e => e.text || (e.runs || []).map(r => r.text).join('');
  const desc = out.find(e => txt(e).startsWith('安姆科总部'));
  const office = out.find(e => txt(e) === '上海总部办公室');
  const addr = out.find(e => txt(e).startsWith('地址'));
  const web = out.find(e => txt(e).startsWith('官网'));
  assert.ok(!desc.center, '描述性正文不居中');
  assert.equal(office.center, true, '办公室名（加粗 h3）居中');
  assert.equal(addr.center, true, '地址行居中');
  assert.equal(web.center, true, '官网行居中');
});

test('classifySeries: 联系为最后一个一级标题（无参考来源在后）时不触发居中', () => {
  // WHY：用户约定「且此标题为倒数第二个标题时」。联系是最后一个一级标题 → 不居中。
  const txt = e => e.text || (e.runs || []).map(r => r.text).join('');
  const out = classifySeries([
    P('五、产品方案'), P('正文'),
    P('六、联系安姆科'), P('地址：上海市'),
  ], {});
  const addr = out.find(e => txt(e).startsWith('地址'));
  assert.ok(!addr.center, '联系为最后一个一级标题时不触发');
});

test('classifySeries: 联系字段正则走规则后台可配（红线：不硬编码）', () => {
  const txt = e => e.text || (e.runs || []).map(r => r.text).join('');
  const cfg = { behavior: { params: { contact_field_regex: { default: '^邮编' } } } };
  const out = classifySeries([
    P('五、联系我们'), P('邮编：200000'), P('地址：上海'),
    P('六、参考来源'), P('[1] A'),
  ], cfg);
  assert.equal(out.find(e => txt(e).startsWith('邮编')).center, true, '自定义字段命中→居中');
  assert.ok(!out.find(e => txt(e).startsWith('地址')).center, '默认地址正则被覆盖→不再命中');
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
