// 共享 DOCX 文章元素模型单测。
//
// WHY：DOCX 排版规则由公众号(fbif.js)与 FoodTalks 网站(foodtalks-site.js)共用一套，
// 收拢在 docx-article-model.js。本文件锁住「结构共用 + 口径分离」的核心契约：
//   - 图片一律按 parser 实际产出的 file 字段从 imgCache 取 src（防 file/id 再次错配）
//   - consumeAuthorLine 开关：公众号吃掉作者行、网站保留作者行交行为层
//   - classifyReferences 开关：公众号分类 refH/ref、网站让参考落为 txt 由行为层识别
//   - 前置材料剥离、标题/图注识别、空段 blankBefore 语义两侧一致
// 纯模块（仅依赖 image-utils.mjs 纯函数），无需浏览器 shim。
import test from 'node:test';
import assert from 'node:assert/strict';
import { classifyDocxArticle, parseAuthorLineWith } from '../public/js/docx-article-model.js';

// parser.buildParagraph 产出的段落对象形状最小子集。
function para(overrides = {}) {
  return {
    runs: [], text: '', isEmpty: false, hasImg: false, align: 'left',
    isList: false, styleId: '', styleName: '', contextualSpacing: false,
    spacing: {}, hasOutlineLevel: false, hasHeadingStyle: false, hasHL: false,
    ...overrides,
  };
}
const txt = (text, extra = {}) => para({ runs: [{ type: 'txt', text }], text, ...extra });
const img = (file, w = '100%') => para({ runs: [{ type: 'img', file, w }], hasImg: true });

test('图片按 file 字段从 imgCache 取 src（根治 file/id 错配）', () => {
  const { elems, imgN } = classifyDocxArticle(
    [txt('作者：张三'), img('image1.png')],
    { 'image1.png': 'blob:resolved' },
    {}
  );
  const imgs = elems.filter(e => e.k === 'img');
  assert.equal(imgN, 1);
  assert.equal(imgs[0].src, 'blob:resolved', '必须用 imgCache[run.file] 取图');
});

test('consumeAuthorLine 默认 true：吃掉作者行 + 捕获作者名，前置材料被剥离', () => {
  const { elems, author } = classifyDocxArticle(
    [txt('【选题会前置材料】'), txt('作者：李四'), txt('正文。')],
    {},
    {}
  );
  assert.equal(author, '李四', '捕获作者名');
  const texts = elems.map(e => (e.runs || []).map(r => r.text).join(''));
  assert.ok(!texts.some(t => t.includes('选题会前置材料')), '前置材料被剥离');
  assert.ok(!texts.some(t => t.includes('作者：李四')), 'consumeAuthorLine=true 吃掉作者行');
  assert.ok(texts.some(t => t.includes('正文')), '正文保留');
});

test('consumeAuthorLine=false（网站口径）：保留作者行在正文流，仍剥离其前置材料', () => {
  const { elems } = classifyDocxArticle(
    [txt('【选题会前置材料】'), txt('作者：李四'), txt('正文。')],
    {},
    { consumeAuthorLine: false }
  );
  const texts = elems.map(e => (e.runs || []).map(r => r.text).join(''));
  assert.ok(!texts.some(t => t.includes('选题会前置材料')), '前置材料仍被剥离');
  assert.ok(texts.some(t => t.includes('作者：李四')), 'consumeAuthorLine=false 保留作者行交给下游');
});

test('classifyReferences 默认 true：参考来源头/条目分类为 refH/ref', () => {
  const { elems } = classifyDocxArticle(
    [txt('作者：张三'), txt('正文。'), txt('参考来源：'), txt('[1] 某文献. 2020.')],
    {},
    {}
  );
  assert.ok(elems.some(e => e.k === 'refH'), '产出 refH');
  assert.ok(elems.some(e => e.k === 'ref'), '产出 ref');
});

test('classifyReferences=false（网站口径）：参考段落落为 txt，由下游行为层识别', () => {
  const { elems } = classifyDocxArticle(
    [txt('作者：张三'), txt('正文。'), txt('参考来源：'), txt('[1] 某文献. 2020.')],
    {},
    { classifyReferences: false }
  );
  assert.ok(!elems.some(e => e.k === 'refH'), '不在共享层分类 refH');
  assert.ok(!elems.some(e => e.k === 'ref'), '不在共享层分类 ref');
  const texts = elems.map(e => (e.runs || []).map(r => r.text).join(''));
  assert.ok(texts.some(t => t.includes('参考来源')), '参考段落以 txt 保留');
  assert.ok(texts.some(t => t.includes('[1]')), '参考条目以 txt 保留');
});

test('标题识别：outlineLvl 或 heading 样式都算标题（h）', () => {
  const byOutline = classifyDocxArticle([txt('作者：张三'), txt('章节一', { hasOutlineLevel: true })], {}, {});
  assert.ok(byOutline.elems.some(e => e.k === 'h' && e.text === '章节一'), 'outlineLvl → h');
  const byStyle = classifyDocxArticle([txt('作者：张三'), txt('章节二', { hasHeadingStyle: true })], {}, {});
  assert.ok(byStyle.elems.some(e => e.k === 'h' && e.text === '章节二'), 'heading 样式 → h');
});

test('图注 look-ahead：图片后紧随的 center 段提升为 cap，且 cap 不带 docxMeta', () => {
  const { elems } = classifyDocxArticle(
    [txt('作者：张三'), img('p.png'), txt('图片来源：FBIF', { align: 'center' })],
    { 'p.png': 'blob:x' },
    {}
  );
  const cap = elems.find(e => e.k === 'cap');
  assert.ok(cap, '产出 cap');
  assert.equal(cap.text, '图片来源：FBIF');
  assert.ok(!('fromDocx' in cap), 'cap 不携带 docxMeta（图注紧贴图片，不参与间距协商）');
});

test('空段语义：空段只把 blankBefore 标到紧随其后的第一个 elem', () => {
  const { elems } = classifyDocxArticle(
    [txt('作者：张三'), txt('第一段。'), para({ isEmpty: true }), txt('空段后第一段。'), txt('再下一段。')],
    {},
    {}
  );
  const body = elems.filter(e => e.k === 'txt');
  // 第一段 blankBefore=false；空段后第一段 blankBefore=true；再下一段 blankBefore=false
  assert.equal(body[0].blankBefore, false);
  assert.equal(body[1].blankBefore, true, '空段后第一段标记 blankBefore');
  assert.equal(body[2].blankBefore, false, 'blankBefore 即取即清，不蔓延');
});

test('parseAuthorLineWith：捕获组 1 为作者名，无匹配返回空串', () => {
  assert.equal(parseAuthorLineWith('作者：王五', /^作者[：:]\s*(.+)$/), '王五');
  assert.equal(parseAuthorLineWith('正文不是作者行', /^作者[：:]\s*(.+)$/), '');
});

// ── 回归守卫：网站口径（classifyReferences=false）下的边界 ──
// WHY：共享层用 isHeading=hasOutlineLevel||hasHeadingStyle，比网站旧版仅认 styleName
// 字面含 heading 宽得多。中文 DOCX 给「参考来源：」打『标题3』样式 / 大纲级别极常见。
// 若这类参考段头被升为 h，会逃逸网站行为层的参考识别（只扫 k:'p'），丢失 #888 灰色 ref
// 口径、[n] 降级黑色正文——本组测试钉死「参考段头降级 txt 交还行为层」。

test('classifyReferences=false：带标题样式的参考段头不升 h，降级 txt（交网站行为层）', () => {
  const { elems } = classifyDocxArticle(
    [txt('作者：张三'), txt('正文。'),
     txt('参考来源：', { hasHeadingStyle: true }),   // 带标题样式
     txt('[1] 某文献. 2020.')],
    {},
    { classifyReferences: false }
  );
  assert.ok(!elems.some(e => e.k === 'h'), '参考段头不得升为 heading（否则逃逸网站参考识别）');
  const texts = elems.filter(e => e.k === 'txt').map(e => (e.runs || []).map(r => r.text).join(''));
  assert.ok(texts.some(t => t.includes('参考来源')), '参考段头以 txt 保留交行为层');
  assert.ok(texts.some(t => t.includes('[1]')), '参考条目以 txt 保留');
});

test('classifyReferences=false：带大纲级别的参考段头同样降级 txt', () => {
  const { elems } = classifyDocxArticle(
    [txt('作者：张三'), txt('参考资料', { hasOutlineLevel: true })],
    {},
    { classifyReferences: false }
  );
  assert.ok(!elems.some(e => e.k === 'h'), 'outlineLvl 参考段头不得升 heading');
  assert.ok(elems.some(e => e.k === 'txt' && (e.runs || []).map(r => r.text).join('').includes('参考资料')));
});

test('classifyReferences=false：带标题样式的「引言/标题」段不被静默删（无内容丢失）', () => {
  const { elems } = classifyDocxArticle(
    [txt('作者：张三'), txt('引言', { hasHeadingStyle: true }), txt('正文。')],
    {},
    { classifyReferences: false }
  );
  // 网站口径不应吃掉「引言」（那是公众号专属语义）——它作为普通标题保留。
  assert.ok(elems.some(e => (e.text || '') === '引言' || (e.runs || []).some(r => r.text === '引言')),
    '「引言」不得被静默删除');
});

test('公众号语义（默认）：带标题样式的参考段头仍归 refH，引言/标题仍跳过', () => {
  // 对照组：classifyReferences=true（公众号）保持旧语义不变。
  const { elems } = classifyDocxArticle(
    [txt('作者：张三'), txt('引言', { hasHeadingStyle: true }),
     txt('参考来源', { hasHeadingStyle: true }), txt('[1] x.')],
    {},
    {}
  );
  assert.ok(!elems.some(e => (e.text || '') === '引言'), '公众号跳过「引言」标记');
  assert.ok(elems.some(e => e.k === 'refH'), '公众号参考段头归 refH');
});

test('consumeAuthorLine=false + 空作者名「作者：」：仍剥离其前置材料', () => {
  // WHY（红线#12 在网站边界）：网站作者正则 (.*) 允许空名，前置剥离须由「命中作者行」
  // 触发，而非「捕获到非空名」。否则「作者：」后无内容时前置材料泄漏到网站输出。
  const { elems } = classifyDocxArticle(
    [txt('【前置材料勿外传】'), txt('作者：'), txt('正文第一段。')],
    {},
    { consumeAuthorLine: false, authorLineRegex: /^[\s　]*(?:作者|Author)\s*[:：]\s*(.*)$/i }
  );
  const texts = elems.map(e => (e.runs || []).map(r => r.text).join(''));
  assert.ok(!texts.some(t => t.includes('勿外传')), '空作者名也要剥离前置材料');
  assert.ok(texts.some(t => t.includes('正文第一段')), '正文保留');
});

// ---- 表格块透传（k:'table'） ----
// WHY：parser.buildTableBlock 产出的 {type:'table', rows} 是公众号与网站共用的表格结构来源，
// 分类层必须原样透传成中性 k:'table' 元素，且不能让表格块污染作者行/正文起点扫描。
const tableCell = (text, header = false) => ({ runs: [{ type: 'txt', text }], header });
const tablePara = (rows, extra = {}) => para({ type: 'table', rows, ...extra });

test("表格块透传成 k:'table'，二维 rows 原样保留", () => {
  const { elems } = classifyDocxArticle(
    [
      txt('作者：张三'),
      tablePara([
        [tableCell('产品名称', true), tableCell('价格', true)],
        [tableCell('话梅'), tableCell('¥11.9')],
      ]),
    ],
    {},
    {}
  );
  const tbl = elems.find(e => e.k === 'table');
  assert.ok(tbl, '产出 k:table 元素');
  assert.equal(tbl.rows.length, 2);
  assert.equal(tbl.rows[0][0].header, true, '表头标记保留');
  assert.equal(tbl.rows[1][1].runs[0].text, '¥11.9');
});

test('正文中的表格块被保留，且不破坏作者行扫描', () => {
  // 作者行后的表格属正文，必须保留。表格自身不应被误判成作者行（text:'' 不命中正则）。
  const { elems, author } = classifyDocxArticle(
    [
      txt('作者：李四'),
      txt('正文。'),
      tablePara([[tableCell('A'), tableCell('B')]]),
    ],
    {},
    {}
  );
  assert.equal(author, '李四', '作者行正常识别');
  assert.ok(elems.some(e => e.k === 'table'), '正文表格保留');
  assert.ok(
    elems.some(e => e.k === 'txt' && (e.runs || []).map(r => r.text).join('') === '正文。'),
    '正文保留'
  );
});

test('作者行之前的表格按前置材料剥离（红线#12：作者行是正文起点）', () => {
  const { elems } = classifyDocxArticle(
    [
      tablePara([[tableCell('前置占位')]]),
      txt('作者：王五'),
      txt('正文。'),
    ],
    {},
    {}
  );
  assert.ok(!elems.some(e => e.k === 'table'), '作者行之前的表格作为前置材料被剥离');
});
