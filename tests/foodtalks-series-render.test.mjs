// FoodTalks「什么是」系列【渲染器】级契约测试（与纯分类层 foodtalks-series.test.mjs 互补）。
// 守护：连续紧凑正文段距（红线#18）、信息表表头横幅跨列+列宽、头图满宽/二维码 140。
// 渲染器 top-level await import engine.js，需浏览器全局 shim（仿 fbif-template.test.mjs）。

import test from 'node:test';
import assert from 'node:assert/strict';

const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };
globalThis.window = { __APP_BUILD_INFO: { build: 'test' }, localStorage: storage };
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });

const template = (await import('../public/js/templates/foodtalks-series.js?test=' + Date.now())).default;

// parser.buildParagraph 形状最小子集（model 通过 docxMeta 补 fromDocx）。
function p(text, extra = {}) {
  return {
    text, runs: text ? [{ type: 'txt', text }] : [], isEmpty: !String(text || '').trim(),
    hasImg: false, align: 'left', isList: false, styleId: '', styleName: '',
    contextualSpacing: false, spacing: {}, hasOutlineLevel: false, hasHeadingStyle: false, hasHL: false,
    ...extra,
  };
}
const imgP = (file, w, aspect) => p('', { hasImg: true, isEmpty: false, runs: [{ type: 'img', file, w, aspect }] });
const tableP = (rows, colWidths) => p('', { type: 'table', isEmpty: false, rows, colWidths });

const render = (paras, imgCache = {}) => template.processDocx({ paragraphs: paras, imgCache, config: {} }).lines.join('\n');
// 取含指定文字的 <section> 的 margin-bottom（"margin: 0 8px <mb>"）。
function marginBottomOf(html, text) {
  const re = new RegExp(`<section style="margin: 0 8px ([0-9]+)px;[^"]*">(?:(?!</section>)[\\s\\S])*?${text}`);
  const m = html.match(re);
  return m ? Number(m[1]) : null;
}

test('render: 连续正文无空段→紧凑(0)，原文有空段→恢复 gap(20)（红线#18）', () => {
  const html = render([
    imgP('h.png', '96%', 1.6),                  // 头图（正文锚点）
    p('甲段落文本', { spacing: { after: 120 } }), // 与乙之间无空段 → 紧凑
    p('乙段落文本', { spacing: { after: 120 } }),
    p(''),                                       // 空段
    p('丙段落文本', { spacing: { after: 120 } }), // 空段后 → 恢复 gap
  ], { 'h.png': 'blob:h' });
  assert.equal(marginBottomOf(html, '甲段落文本'), 0, '甲→乙 紧凑无 gap');
  assert.equal(marginBottomOf(html, '乙段落文本'), 20, '乙→丙 间有空段 → 恢复 20px gap');
});

test('render: 信息表首行=标题+空 → 跨列深蓝横幅 + colgroup 左窄右宽（width 属性存活微信）', () => {
  const rows = [
    [{ runs: [{ type: 'txt', text: '安姆科挤出复合工艺' }] }, { runs: [] }],
    [{ runs: [{ type: 'txt', text: '效率提升' }] }, { runs: [{ type: 'txt', text: '生产速度翻倍' }] }],
  ];
  const html = render([imgP('h.png', '96%', 1.6), tableP(rows, [1935, 6345])], { 'h.png': 'blob:h' });
  assert.match(html, /colspan="2"/, '表头跨整行');
  assert.match(html, /background-color: #02396b/, '表头深蓝实心 bar');
  assert.match(html, /<colgroup>/, '按 gridCol 下发列宽');
  // 微信公众号粘贴剥离 <col> 的 style，但保留 width 属性 → 列宽必须用 width 属性承载，否则后台均分。
  assert.match(html, /<col width="23%"/, '左列窄：整数百分比走 width 属性（微信存活）');
  assert.match(html, /<col width="77%"/, '右列宽：末列吸收舍入误差合计 100');
  assert.match(html, /width: 23\.37%/, 'style 保留精确小数供浏览器预览');
  assert.match(html, /width: 76\.63%/, '右列预览精确值');
  assert.match(html, /border: 1px solid #888888/, '正文格框线 = 灰 #888888（成品口径）');
});

test('render: 头图满宽 100%、方形小图=二维码 → 140px', () => {
  const html = render([
    imgP('head.png', '96%', 1.62),  // 头图（横图）→ 100%
    p('正文'),
    imgP('qr.png', '24%', 1.0),     // 方形 + 小 → 二维码 140px
    imgP('photo.png', '82%', 1.45), // 横图（非头图）→ 保留 DOCX 82%
  ], { 'head.png': 'blob:head', 'qr.png': 'blob:qr', 'photo.png': 'blob:photo' });
  assert.match(html, /src="blob:head"[^>]*style="width: 100%/, '头图满宽');
  assert.match(html, /src="blob:qr"[^>]*style="width: 140px/, '二维码固定 140px');
  assert.match(html, /src="blob:photo"[^>]*style="width: 82%/, '普通横图保留 DOCX 宽度');
});

// 取含指定文字的 <section> 的 text-align（"text-align: <align>;"）。
function sectionAlignOf(html, text) {
  const re = new RegExp(`<section style="[^"]*text-align: (left|center)[^"]*">(?:(?!</section>)[\\s\\S])*?${text}`);
  const m = html.match(re);
  return m ? m[1] : null;
}
// 取含指定文字的 <span> 的内联 style。
function spanStyleOf(html, text) {
  const re = new RegExp(`<span style="([^"]*)">${text}`);
  const m = html.match(re);
  return m ? m[1] : null;
}

test('render: 文末署名/图源说明 = 12px #7f7f7f 斜体小字（成品实测口径）', () => {
  // WHY：成品文末「本文作者：X」「本文为标注来源的图片均来自X」是 12px #7f7f7f 斜体；
  // 此前误渲成 15px #544545 普通正文（用户报「底部引用来源字体格式不对」）。
  const html = render([
    imgP('h.png', '96%', 1.6),
    p('正文引子。'),
    p('本文为标注来源的图片均来自安姆科'),
    p('本文作者：Isaac, FoodTalks'),
  ], { 'h.png': 'blob:h' });
  for (const t of ['本文为标注来源的图片均来自安姆科', '本文作者：Isaac, FoodTalks']) {
    const s = spanStyleOf(html, t);
    assert.ok(s, `应找到「${t}」的 span`);
    assert.match(s, /font-size: 12px/, '12px 小字');
    assert.match(s, /color: #7f7f7f/, '#7f7f7f 灰');
    assert.match(s, /font-style: italic/, '斜体');
  }
});

test('processDocx: 暴露 authorName 给上传流程自动补作者名片', () => {
  // WHY：作者名片自动补充发生在 engine 生成底部区块前；模板必须把分类层识别到的作者显式返回。
  const result = template.processDocx({
    paragraphs: [
      imgP('h.png', '96%', 1.6),
      p('正文引子。'),
      p('本文作者：Ian, FoodTalks'),
    ],
    imgCache: { 'h.png': 'blob:h' },
    config: {},
  });
  assert.equal(result.authorName, 'Ian');
});

test('render: 文末「提示：」深蓝粗体、「* 声明」#888888 斜体（成品口径）', () => {
  const html = render([
    imgP('h.png', '96%', 1.6),
    p('正文引子。'),
    p('提示：'),
    p('* 本文为FoodTalks原创首发，欢迎无条件转发朋友圈。'),
  ], { 'h.png': 'blob:h' });
  const tip = spanStyleOf(html, '提示：');
  assert.match(tip, /color: #02396b/, '提示标签深蓝');
  assert.match(tip, /font-weight: bold/, '提示标签粗体');
  const item = spanStyleOf(html, '\\* 本文为FoodTalks原创首发，欢迎无条件转发朋友圈。');
  assert.match(item, /color: #888888/, '声明条目灰');
  assert.match(item, /font-style: italic/, '声明条目斜体');
});

test('render: 联系小节公司信息块整体居中、描述性正文左对齐（成品口径）', () => {
  // WHY：成品「联系XXX」下方公司信息是一个整体；姓名、职位、备注不能漏成左对齐。
  const html = render([
    imgP('h.png', '96%', 1.6),
    p('正文引子。'),
    p('五、联系安姆科'),
    p('安姆科总部位于上海，热诚欢迎各位前来参观交流。'),
    p('上海总部办公室', { runs: [{ type: 'txt', text: '上海总部办公室', bold: true }] }),
    p('地址：上海市闵行区申长路988弄'),
    p('官网：https://www.amcor.com/'),
    p('商务合作对接'),
    p('郝佳丽'),
    p('总经理助理'),
    p('联系电话：17310962656'),
    p('（添加请备注：公司 姓名 职位）'),
    p('六、参考来源'),
    p('[1] 报告A'),
  ], { 'h.png': 'blob:h' });
  assert.equal(sectionAlignOf(html, '安姆科总部位于上海'), 'left', '描述性正文左对齐');
  assert.equal(sectionAlignOf(html, '上海总部办公室'), 'center', '办公室名居中');
  assert.equal(sectionAlignOf(html, '地址：上海市闵行区申长路988弄'), 'center', '地址行居中');
  assert.equal(sectionAlignOf(html, '官网：https://www\\.amcor\\.com/'), 'center', '官网行居中');
  assert.equal(sectionAlignOf(html, '商务合作对接'), 'center', '商务合作标题居中');
  assert.equal(sectionAlignOf(html, '郝佳丽'), 'center', '姓名居中');
  assert.equal(sectionAlignOf(html, '总经理助理'), 'center', '职位居中');
  assert.equal(sectionAlignOf(html, '联系电话：17310962656'), 'center', '联系电话居中');
  assert.equal(sectionAlignOf(html, '（添加请备注：公司 姓名 职位）'), 'center', '添加备注居中');
});
