import test from 'node:test';
import assert from 'node:assert/strict';
import {
  detectHeadingLevel,
  isBoldHeading,
  promoteHeuristicHeadings,
  hasExplicitDocxHeadings,
  liftOrphanSubheadings,
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
  '毕业20年的老粉凭什么“回坑”？', // 疑问句标题：以问号结尾也要识别
  '结语',
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

test('isBoldHeading 放行短疑问句标题，但仍按长度拦截超长疑问句', () => {
  // 短疑问句标题（基石 news/62244 真实标题）→ 升级
  assert.equal(isBoldHeading([{ type: 'txt', text: '毕业20年的老粉凭什么“回坑”？', bold: true }]), true);
  // 超长疑问句（>36 字）→ 仍被长度拦截，不误升级
  const longQ = '这是一段非常长的加粗疑问句用来测试长度边界是否能正确拦截它吗真的能拦住吗？';
  assert.equal(isBoldHeading([{ type: 'txt', text: longQ, bold: true }]), false);
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

test('isBoldHeading 不忽略段内链接，避免加粗开头把整段链接正文误升标题', () => {
  const runs = [
    { type: 'txt', text: '据', bold: true },
    { type: 'link', text: '外部报道', href: 'https://example.com/news' },
  ];
  assert.equal(isBoldHeading(runs), false);
});

test('isBoldHeading 拒绝加粗的图注/来源行', () => {
  assert.equal(isBoldHeading([{ type: 'txt', text: '图片来源：大塚食品', bold: true }]), false);
  assert.equal(isBoldHeading([{ type: 'txt', text: '拍摄：Mote莫特', bold: true }]), false);
});

test('isBoldHeading 拒绝编号步骤、装饰线和未完句（黄仁勋样本误判防护）', () => {
  assert.equal(isBoldHeading([{ type: 'txt', text: '1. 当天下午官微发博，英伟达创始人黄仁勋来到门店；', bold: true }]), false);
  assert.equal(isBoldHeading([{ type: 'txt', text: '价格不傲慢，人人可消费；', bold: true }]), false);
  assert.equal(isBoldHeading([{ type: 'txt', text: '━ ━ ━ ━ ━', bold: true }]), false);
  assert.equal(isBoldHeading([{ type: 'txt', text: '最后，想问问你：', bold: true }]), false);
});

test('网站渠道 arabicAsSubheading：加粗「2、难点」升二级标题，公众号默认仍不升', () => {
  const t = '2、第二大难点：改变消费者固有认知';
  assert.equal(isBoldHeading([{ type: 'txt', text: t, bold: true }]), false);
  assert.equal(isBoldHeading([{ type: 'txt', text: t, bold: true }], { arabicAsSubheading: true }), true);
  // 62873：挂在「三大难题」下面（前面没有上级章节标题的序号句另见 liftOrphanSubheadings）。
  const out = promoteHeuristicHeadings(
    [boldP('创业三大“地狱级”难题，如何层层破解？'), plainP('品牌在起步阶段就直面三大难题。'),
      { k: 'p', runs: [{ type: 'txt', text: t, bold: true }] }],
    { arabicAsSubheading: true },
  );
  assert.equal(out[2].k, 'h');
  assert.equal(out[2].level, 3);
});

for (const label of ['健康意识觉醒：', '女性经济崛起：', '代际变迁：', '经济下行：']) {
  test(`isBoldHeading 拒绝真露评测集中的正文标签: ${label}`, () => {
    assert.equal(isBoldHeading([{ type: 'txt', text: label, bold: true }]), false);
  });
}

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

// ---- hasExplicitDocxHeadings: 信号优先级门控（显式 Word 标题样式在场 ⇒ 启发式停用） ----
// WHY：启发式存在的唯一理由是填补"作者没用标题样式"的空缺；一旦文档里有真·样式标题，
// 整段加粗只是强调，再跑启发式就会把强调句误升 h2（玛黑兄弟样本，见
// WORKLOG-docx-heading-signal-priority.md）。此函数是两侧模板共用的门控依据。

test('hasExplicitDocxHeadings：存在样式标题元素时为 true', () => {
  assert.equal(hasExplicitDocxHeadings([
    { k: 'txt', runs: [] },
    { k: 'h', text: '太阳王的密使' },
  ]), true);
});

test('hasExplicitDocxHeadings：全文无样式标题时为 false（启发式才补位）', () => {
  assert.equal(hasExplicitDocxHeadings([
    { k: 'txt', runs: [] },
    { k: 'refH', text: '参考来源' },
    { k: 'img', src: 'x' },
  ]), false);
});

test('hasExplicitDocxHeadings：空/无效输入为 false', () => {
  assert.equal(hasExplicitDocxHeadings([]), false);
  assert.equal(hasExplicitDocxHeadings(null), false);
  assert.equal(hasExplicitDocxHeadings([null, undefined]), false);
});

test('promoteHeuristicHeadings 不升级已标记的联系信息块', () => {
  const elems = [
    { k: 'p', skipHeadingPromote: true, contactInfo: true, runs: [{ type: 'txt', text: '商务合作对接', bold: true }] },
  ];
  const out = promoteHeuristicHeadings(elems);
  assert.equal(out[0].k, 'p', '联系信息块里的加粗短句不是正文标题');
  assert.equal(out[0].contactInfo, true);
});

test('isBoldHeading 拒绝署名行（作者/编辑/出品等前缀，2026-08-17 运营反馈：转载稿尾部署名曾被升 h2）', () => {
  for (const t of ['作者：小食代，编辑：Bobo', '编辑：张洁', '出品：天下网商', '策划：某某团队', '撰文：叶晨']) {
    assert.equal(isBoldHeading([{ type: 'txt', text: t, bold: true }]), false, t);
  }
});

// ---- 引语、口语感叹、设问不是标题（2026-10-15，编辑在 #115/#128/#131/#146/#157/#121 都改回了正文） ----

const SITE = { arabicAsSubheading: true };
const bold = text => [{ type: 'txt', text, bold: true }];

test('isBoldHeading 拒绝整句引语与转述（#115 网友评论、#161、#202）', () => {
  for (const t of [
    '“研发灵感来自一个想感受一下东北人吃绿化带的上海人。。。”',
    '“适合写不出稿子的时候来一块”',
    '“从未想过对夹子音免疫的我，会栽在一块臭夹子蛋糕手里！”',
    '“一路走来挺不容易，”她补充道，“这是一段漫长的旅程。”',
    '他后来说：“失败让我从营销主义者，变成了彻头彻尾的产品主义者！”',
    '因此，他明确表示：「PB对我们来讲是个好事，不是坏事。」',
  ]) {
    assert.equal(isBoldHeading(bold(t), SITE), false, t);
  }
});

test('isBoldHeading 拒绝口语感叹（#131）', () => {
  for (const t of [
    '是猫咪救了瑞幸！这应该写入《盘点那些猫咪救场的神级场面》！',
    '不装垃圾，也可以套头上啊，一样拉风！',
    '瑞幸赶紧澄清，垃圾袋一款是一包，是真实可用的塑料袋哦！',
  ]) {
    assert.equal(isBoldHeading(bold(t), SITE), false, t);
  }
});

test('isBoldHeading 不误伤带引号、感叹号的真标题', () => {
  for (const t of [
    '“罪恶饮料”，让全球年轻人上头',
    '文案出圈密码是 “活人感”',
    '8G冲浪实锤！玩梗玩到出神入化',
    '“茶叶版星巴克”梦碎：379家店，全部关门',
    '1、资产沉淀：“他们做世界杯，蒙牛做足球”',
    '逃不出的“周末限定款”？',
    '为什么是人？为什么是现在？',
  ]) {
    assert.equal(isBoldHeading(bold(t), SITE), true, t);
  }
});

test('promoteHeuristicHeadings：冒号引出的加粗问句是正文（#146、#249），引出的短语仍是标题（#187）', () => {
  const out = promoteHeuristicHeadings([
    plainP('只有一句口号，还不足以让消费者相信，还需要继续回答一个问题：'),
    boldP('为什么你能比榴莲鲜果还好吃？'),
    plainP('以下是听证会的记录稿（经联商翻译中心编译）：'),
    boldP('从区域折扣店到全国主流超市'),
    plainP('但理想之路并不轻松，品牌在起步阶段就直面三大难题：'),
    boldP('1、第一大难点：生产成本远高于行业竞品'),
  ], SITE);
  assert.deepEqual(out.map(e => e.k), ['p', 'p', 'p', 'h', 'p', 'h']);
});

test('promoteHeuristicHeadings：两个加粗问句紧挨着是设问（#121），单独一个问句仍是标题（#109）', () => {
  const pair = promoteHeuristicHeadings([
    plainP('同样在7月，百事也在中国推出益生元可乐。'),
    boldP('益生元气泡水市场究竟有什么样的吸引力？巨头在抢什么？'),
    boldP('这些产品竞争力如何？以及中国益生元气泡水，究竟能做成多大的生意？'),
    plainP('我们会在文章中逐步探讨。'),
  ], SITE);
  assert.deepEqual(pair.map(e => e.k), ['p', 'p', 'p', 'p']);
  const single = promoteHeuristicHeadings([
    plainP('山姆恰恰是把最重要的笔墨，留给了这些看似不会被记住的小事里。'),
    boldP('为什么是人？为什么是现在？'),
    plainP('我决定带着这两个问题重新看这两支片子。'),
  ], SITE);
  assert.deepEqual(single.map(e => e.k), ['p', 'h', 'p']);
});

// ---- 二级序号前面没有上级章节标题时整组提成一级（#151、#121），有上级仍按第 39 条排二级 ----

const h = (level, text) => ({ k: 'h', level, runs: [{ type: 'txt', text }] });
const levels = elems => elems.map(e => `${e.level}${e.topLevel ? '*' : ''}`).join(' ');

test('liftOrphanSubheadings：「引子」后的 1、…5、整组提成一级（#151）', () => {
  const out = liftOrphanSubheadings([
    h(2, '引子'), h(3, '1、不要低估大单品的生命力'), h(3, '2、为什么旺仔牛奶可以考虑做低温'), h(2, '结语'),
  ]);
  assert.equal(levels(out), '2 2* 2* 2');
});

test('liftOrphanSubheadings：开篇就是 1、2、3，下面的 a、b 排二级（#121）', () => {
  const out = liftOrphanSubheadings([
    h(3, '1、poppi年销50亿，短短6年，美国益生元气泡水经历了什么？'),
    h(3, '3、各大益生元气泡水产品，竞争力究竟如何？'),
    h(2, 'a、站在同一起跑线上，但是竞争优势并不相同'),
    h(2, 'b、各大益生元气泡水产品的竞争优势分析与判断'),
    h(3, '4、益生元气泡水在中国，究竟能做成多大的生意？'),
    h(2, '总结'),
  ]);
  assert.equal(levels(out), '2* 2* 3 3 2* 2');
});

test('liftOrphanSubheadings：有上级章节标题的序号句仍是二级（每周新品合集、62873）', () => {
  assert.equal(levels(liftOrphanSubheadings([
    h(2, '本周创新看点'), h(3, '1、山姆上新满记甜品「芋泥乳酪双皮奶」'), h(3, '2、元气森林×上海力波推出联名限定0糖啤酒'),
  ])), '2 3 3');
  assert.equal(levels(liftOrphanSubheadings([
    h(2, '创业三大“地狱级”难题，如何层层破解？'), h(3, '1、第一大难点：生产成本远高于行业竞品'),
    h(3, '2、第二大难点：改变消费者固有认知'), h(2, '重资产打造硬核供应链，建立品牌护城河'),
  ])), '2 3 3 2');
});

test('liftOrphanSubheadings：挂在序号句下面的 ①② 排二级（#229），不在序号句下面的 a. 不动（#259）', () => {
  assert.equal(levels(liftOrphanSubheadings([
    h(3, '1、为什么东方树叶应该做强做大无糖绿茶？'), h(3, '2、为什么抹茶是很好的选择？'),
    h(2, '①、东方树叶围绕绿茶有什么选择？'), h(3, '3、东方树叶可能会以什么方式做抹茶？'),
  ])), '2* 2* 3 2*');
  assert.equal(levels(liftOrphanSubheadings([
    h(2, '品牌如何得以长盛不衰'), h(2, 'a. 一以贯之的产品策略'), h(2, '物美价廉'),
  ])), '2 2 2');
});

test('小数不是序号：「1.8万家店」「3.9元」不排二级（#199、#218）', () => {
  const out = promoteHeuristicHeadings([
    boldP('1.8万家店，CU靠什么让消费者反复进店？'), plainP('正文。'),
    boldP('3.9元的成本藏在哪里？'), plainP('正文。'),
    boldP('1）不追求拓展品类'),
  ], SITE);
  assert.deepEqual(out.filter(e => e.k === 'h').map(e => `${e.level}${e.topLevel ? '*' : ''}`), ['2', '2', '3']);
});
