// FoodTalks 网站渠道 DOCX 端到端测试。
//
// WHY：网站 DOCX 图片此前【零测试覆盖】——foodtalks-site-template.test.mjs 只用
// 正则扫源码字符串，从未以含图片 run 的 paragraphs 真正调用 processDocx，所以
// 「图片取图字段读错（imgRun.id 而非 parser 实际产出的 file）」一路漏到线上，
// 表现为网站 DOCX 上传后图片统计正确但 src 为空、整篇图片空白。
//
// 本文件用 fbif-characterization 同款 Node shim 真实加载网站模板，端到端跑
// processDocx，锁住：DOCX 排版规则与公众号共用结构分类（图片按 file 取图、标题/
// 图注/前置剥离），网站口径（参考来源/作者/提示）仍走网站行为层。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { DOMParser as XmldomParser } from '@xmldom/xmldom';
import JSZip from 'jszip';

const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };

class DOMParserShim {
  parseFromString(xml, type) {
    const doc = new XmldomParser().parseFromString(xml, type);
    doc.querySelectorAll = (selector) => Array.from(doc.getElementsByTagName(selector));
    return doc;
  }
}

globalThis.DOMParser = DOMParserShim;
globalThis.JSZip = JSZip;
globalThis.window = { __APP_BUILD_INFO: { build: 'test' }, localStorage: storage };
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });
globalThis.URL.createObjectURL = (blob) => 'blob:test/' + (blob && blob.size || 0);

const { parseDocx } = await import('../public/js/parser.js?ftdocx=' + Date.now());
const template = (await import('../public/js/templates/foodtalks-site.js?ftdocx=' + Date.now())).default;

// parser.buildParagraph 产出的段落对象形状（含共享分类层读取的全部 docx meta）。
function para(overrides = {}) {
  return {
    runs: [], text: '', isEmpty: false, hasImg: false, align: 'left',
    isList: false, styleId: '', styleName: '', styleChain: [],
    contextualSpacing: false, spacing: {}, hasOutlineLevel: false,
    hasHeadingStyle: false, hasHL: false, allBold: false, fontSizes: [],
    ...overrides,
  };
}

function imgPara(file, w = '100%') {
  return para({ runs: [{ type: 'img', file, w }], hasImg: true });
}

function txtPara(text, extra = {}) {
  return para({ runs: [{ type: 'txt', text }], text, ...extra });
}

test('网站 DOCX：图片经 imgCache 按 file 字段取到 src（杜绝 src 为空）', () => {
  // WHY：parser.js 产出的图片 run 是 {type:'img', file, w}，没有 id 字段。
  // 网站必须用 imgCache[run.file] 取图，否则整篇 DOCX 图片全空白。
  const result = template.processDocx({
    paragraphs: [imgPara('image1.png')],
    imgCache: { 'image1.png': 'blob:test-image' },
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /blob:test-image/, '图片 src 必须解析到 imgCache 里的值');
  assert.doesNotMatch(html, /src="\s*"/, '绝不允许输出空 src');
  assert.equal(result.imgN, 1);
});

test('网站 DOCX：imgCache 未命中也绝不输出 src=""', () => {
  // WHY：即便缓存缺该图，也不能产出 <img src=""> 这种坏 HTML 污染网站后台。
  const result = template.processDocx({
    paragraphs: [imgPara('missing.png')],
    imgCache: {},
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.doesNotMatch(html, /src="\s*"/, '缓存未命中时不得输出空 src');
});

test('网站 DOCX：剥离首个「作者：」行之前的前置材料（与公众号共用前置剥离）', () => {
  // WHY：红线#12——首个「作者：」行是正文起点，其前的编辑前置材料默认不显示。
  const result = template.processDocx({
    paragraphs: [
      txtPara('【编辑前置材料：选题会记录，请勿外传】'),
      txtPara('作者：张三'),
      txtPara('这是正文第一段。'),
    ],
    imgCache: {},
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.doesNotMatch(html, /选题会记录/, '前置材料不得出现在输出里');
  assert.match(html, /这是正文第一段/, '正文必须保留');
});

test('网站 DOCX：参考来源仍由网站行为层按 [n] 口径识别（口径保留）', () => {
  // WHY：用户裁定「结构共用 + 网站口径保留」——参考来源识别留在 applyFoodTalksBehavior
  // 那套对 8 篇基石调过的更保守规则（只认 [n]/— 行），不切换成公众号更宽的规则。
  const result = template.processDocx({
    paragraphs: [
      txtPara('正文段落。'),
      txtPara('参考来源：'),
      txtPara('[1] 某篇论文标题. 2020.'),
      txtPara('[2] 另一篇. 2021.'),
    ],
    imgCache: {},
    config: { muted_color: '#888888' },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /参考来源/, '参考来源段头必须渲染');
  assert.match(html, /\[1\]/, '参考条目必须保留');
  assert.match(html, /#888888/, '参考区块走 muted 灰色口径');
});

test('网站什么是系列 URL 导入：分节图转文字标题，图片标题转图注，参考/署名保持网站口径', () => {
  // WHY：橙宝样例的微信源稿结构是「窄长分节图 + h」和「普通图片 + h 图注」。
  // 网站成品不展示分节装饰图；参考来源标题没有冒号但后续 [n] 仍必须连续灰色；文末图源/作者是 14px 灰色斜体。
  const imgBlock = (src, dataW, dataRatio) => ({ k: 'img', src, dataW, dataRatio });
  const hBlock = (text) => ({ k: 'h', text });
  const txtBlock = (text) => ({ k: 'txt', runs: [{ type: 'txt', text }] });
  const result = template.processBlocks({
    title: '橙宝样例',
    blocks: [
      hBlock('目录'),
      hBlock('一、穿越周期：橙宝的40年进化史'),
      txtBlock('七、参考来源'),
      imgBlock('https://mmbiz.qpic.cn/section-01.png'),
      hBlock('穿越周期：橙宝的40年进化史'),
      imgBlock('https://mmbiz.qpic.cn/content-image.png', 900, 0.62),
      hBlock('橙宝的40年进化史'),
      imgBlock('https://mmbiz.qpic.cn/wide-logo.png', 1080, 0.15),
      hBlock('橙宝logo'),
      imgBlock('https://mmbiz.qpic.cn/section-02.png'),
      hBlock('参考来源'),
      txtBlock('[1] 中国消费者网（2025），《HPP果汁成果汁界新晋顶流》'),
      txtBlock('本文未标注来源图片/数据均来自橙宝 本文作者：Ian, FoodTalks'),
    ],
  }, {
    config: {
      justify_paragraphs: false,
      muted_color: '#7f7f7f',
      caption_font_size: '12',
      footer_note_color: '#7f7f7f',
      footer_note_font_size: '14',
      modules: { fbif_footer: { enabled: false } },
    },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.doesNotMatch(html, /section-01\.png|section-02\.png/, '分节装饰图不得输出到网站正文');
  assert.match(html, /<h2[^>]*>一、穿越周期：橙宝的40年进化史<\/h2>/, '分节标题应转成文字编号 h2');
  assert.doesNotMatch(html, /<h2[^>]*>穿越周期：橙宝的40年进化史<\/h2>/, '正文分节标题不得留下未编号副本');
  assert.doesNotMatch(html, /<h2[^>]*>橙宝的40年进化史<\/h2>/, '图片后的短 h 不得继续作为大标题');
  assert.match(html, /font-size: 12px; color: #7f7f7f;">橙宝的40年进化史/, '图片后的短 h 应转为灰色图注');
  assert.match(html, /wide-logo\.png/, '目录存在时，非目录章节标题后的宽图不得被误删');
  assert.doesNotMatch(html, /<h2[^>]*>橙宝logo<\/h2>/, '宽图后的短 h 应转图注，不得误当分节标题');
  assert.match(html, /<h2[^>]*>七、参考来源<\/h2>/, '无冒号参考来源标题应按目录编号保留为 h2');
  assert.doesNotMatch(html, /<h2[^>]*>参考来源<\/h2>/, '参考来源正文标题不得留下未编号副本');
  assert.match(html, /<span style="color: #7f7f7f;">\[1\]/, '参考条目必须进入灰色 ref-item 分支');
  assert.match(html, /font-size: 14px; color: #7f7f7f;">本文未标注来源图片\/数据均来自橙宝/, '文末图源说明应是 14px 灰色小字');
  assert.match(html, /<br><em><span style="font-size: 14px; color: #7f7f7f;">本文作者：Ian, FoodTalks/, '文末作者应拆到下一行');
});

test('网站什么是系列 URL 导入：联系区信息块居中，组内紧凑，仅显式空段拉开', () => {
  // WHY：橙宝样例的「联系橙宝」不是普通正文段列表；公司信息与 BD 信息应成组居中。
  // 此前网站渲染器给每个 p 后都塞 <p><br></p>，导致姓名/职位/电话之间出现异常空行。
  const imgBlock = (src, dataW, dataRatio) => ({ k: 'img', src, dataW, dataRatio });
  const hBlock = (text) => ({ k: 'h', text });
  const txtBlock = (text, extra = {}) => ({ k: 'txt', runs: [{ type: 'txt', text }], ...extra });
  const boldBlock = (text, extra = {}) => ({ k: 'txt', runs: [{ type: 'txt', text, bold: true }], ...extra });
  const result = template.processBlocks({
    title: '橙宝样例',
    blocks: [
      hBlock('六、联系橙宝'),
      imgBlock('https://mmbiz.qpic.cn/logo.jpg', 607, 0.66),
      hBlock('橙宝logo'),
      boldBlock('天津橙宝鲜橙汁有限公司', { blankBefore: true }),
      txtBlock('总部地址：天津市静海经济开发区聚海道16号'),
      txtBlock('联系电话：+86 022-28011188'),
      boldBlock('商务合作对接', { blankBefore: true }),
      txtBlock('郝佳丽'),
      txtBlock('总经理助理'),
      txtBlock('联系电话：17310962656'),
      txtBlock('（添加请备注：公司 姓名 职位）'),
      hBlock('七、参考来源'),
      txtBlock('[1] 中国消费者网（2025），《HPP果汁成果汁界新晋顶流》'),
    ],
  }, {
    config: {
      justify_paragraphs: false,
      muted_color: '#7f7f7f',
      footer_note_color: '#7f7f7f',
      footer_note_font_size: '14',
      modules: { fbif_footer: { enabled: false } },
    },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(
    html,
    /<p style="text-align: center;"><strong>天津橙宝鲜橙汁有限公司<\/strong><\/p>\n<p style="text-align: center;">总部地址：天津市静海经济开发区聚海道16号<\/p>\n<p style="text-align: center;">联系电话：\+86 022-28011188<\/p>/,
    '公司信息三行应居中且组内不插空行，不能被 logo 图注误吞'
  );
  assert.doesNotMatch(html, /font-size: 12px; color: #7f7f7f;">天津橙宝鲜橙汁有限公司/, '公司名不能被误判为图注小灰字');
  assert.match(
    html,
    /联系电话：\+86 022-28011188<\/p>\n<p><br><\/p>\n<p style="text-align: center;"><strong>商务合作对接<\/strong><\/p>/,
    '原文显式空段应只在公司信息与 BD 信息之间保留'
  );
  assert.match(
    html,
    /<strong>商务合作对接<\/strong><\/p>\n<p style="text-align: center;">郝佳丽<\/p>\n<p style="text-align: center;">总经理助理<\/p>\n<p style="text-align: center;">联系电话：17310962656<\/p>/,
    'BD 姓名/职位/电话应紧凑居中'
  );
  assert.match(
    html,
    /<p style="text-align: center;"><span style="font-size: 14px; color: #7f7f7f;">（添加请备注：公司 姓名 职位）<\/span><\/p>/,
    '备注行应按成品小灰字居中'
  );
});

test('网站什么是系列 URL 导入：联系区即使是最后一节也按信息块处理', () => {
  // WHY：不是所有文章都保证「联系」后面还有「参考来源」标题；联系区不能只靠倒数第二节识别。
  const hBlock = (text) => ({ k: 'h', text });
  const txtBlock = (text, extra = {}) => ({ k: 'txt', runs: [{ type: 'txt', text }], ...extra });
  const boldBlock = (text, extra = {}) => ({ k: 'txt', runs: [{ type: 'txt', text, bold: true }], ...extra });
  const result = template.processBlocks({
    title: '联系最后一节样例',
    blocks: [
      hBlock('六、联系品牌方'),
      boldBlock('某某食品有限公司'),
      txtBlock('总部地址：上海市浦东新区一条很长很长但仍属于联系字段的地址信息'),
      boldBlock('商务合作对接', { blankBefore: true }),
      txtBlock('张三'),
      txtBlock('品牌经理'),
      txtBlock('联系电话：13800000000'),
      txtBlock('本文作者：Ian, FoodTalks'),
    ],
  }, {
    config: {
      justify_paragraphs: false,
      muted_color: '#7f7f7f',
      modules: { fbif_footer: { enabled: false } },
    },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /<p style="text-align: center;"><strong>某某食品有限公司<\/strong><\/p>/, '最后一节里的公司名也应居中');
  assert.match(html, /<p style="text-align: center;">总部地址：上海市浦东新区一条很长很长但仍属于联系字段的地址信息<\/p>/, '长地址字段也应作为联系信息居中');
  assert.match(html, /<p style="text-align: center;">张三<\/p>\n<p style="text-align: center;">品牌经理<\/p>\n<p style="text-align: center;">联系电话：13800000000<\/p>/, '最后一节里的 BD 信息仍应紧凑居中');
  assert.doesNotMatch(html, /text-align: center;">本文作者/, '文末作者边界不能被误并入联系信息块');
});

test('网站页脚库：无 active_segments 时使用 active preset 的 segments 渲染 BD 名片', () => {
  // WHY：ft-series 默认页脚库已经是 presets 结构；渲染器若只读旧 active_segments，
  // 网站底部 BD 名片/商务合作会整体不出现。
  const result = template.processBlocks({
    title: '页脚样例',
    blocks: [{ k: 'txt', runs: [{ type: 'txt', text: '正文。' }] }],
  }, {
    config: {
      modules: {
        fbif_footer: {
          enabled: true,
          active_preset_id: 'p1',
          segment_order: ['bd_card'],
          segments: { bd_card: { html_lines: ['<p>BD名片出现</p>'] } },
          presets: [{ id: 'p1', name: '默认', segments: { tip: false, bd_card: true } }],
        },
      },
    },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /BD名片出现/, 'active preset 勾选的 bd_card 必须渲染');
});

test('网站页脚库：active_segments 残留时仍以 active preset 为准', () => {
  // WHY：规则从旧结构迁移到 presets 后，浏览器本地或旧规则可能残留 active_segments；
  // 新旧字段共存时若优先旧字段，BD 名片会在别的文章上再次漏渲。
  const result = template.processBlocks({
    title: '页脚迁移样例',
    blocks: [{ k: 'txt', runs: [{ type: 'txt', text: '正文。' }] }],
  }, {
    config: {
      modules: {
        fbif_footer: {
          enabled: true,
          active_segments: { tip: false, bd_card: false },
          active_preset_id: 'p1',
          segment_order: ['bd_card'],
          segments: { bd_card: { html_lines: ['<p>BD名片按 preset 出现</p>'] } },
          presets: [{ id: 'p1', name: '默认', segments: { tip: false, bd_card: true } }],
        },
      },
    },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /BD名片按 preset 出现/, '新 preset 必须压过旧 active_segments 残留值');
});

// ---- 统一表格样式（网站渲染：本就 inline-style，与公众号物理隔离各写各的 renderTable） ----
// WHY：网站渠道也要支持表格，默认样式与公众号同一套（蓝底白字表头 + 浅灰网格线 = 统一），
// 但渲染函数物理隔离；样式读扁平 config 键，缺省降级与服务端 defaultRuleConfig 逐字一致。
function tablePara(rows) {
  return para({ type: 'table', rows, isEmpty: false });
}
const stcell = (text, header = false) => ({ runs: [{ type: 'txt', text }], header });

test('网站 DOCX 表格渲成 inline-style <table>（默认与公众号同一套蓝底白字）', () => {
  const result = template.processDocx({
    paragraphs: [
      tablePara([
        [stcell('产品名称', true), stcell('价格', true)],
        [stcell('话梅'), stcell('¥11.9')],
      ]),
    ],
    imgCache: {},
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /<table[^>]*border-collapse: collapse/);
  assert.match(html, /background-color: #01396a/, '表头默认 FoodTalks 蓝');
  assert.match(html, /color: #ffffff/, '表头白字');
  assert.match(html, /border: 1px solid #e5e7eb/, '浅灰网格线');
  assert.match(html, /产品名称/);
  assert.match(html, /¥11\.9/);
});

test('网站表格样式可被规则后台 config 覆盖（红线：不可写死）', () => {
  const result = template.processDocx({
    paragraphs: [tablePara([[stcell('h', true)], [stcell('x')]])],
    imgCache: {},
    config: { table_header_bg: '#123456', table_border_color: '#abcdef', table_font_size: '18' },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /background-color: #123456/, '表头底色走 config 覆盖');
  assert.match(html, /border: 1px solid #abcdef/, '边框色走 config 覆盖');
  assert.match(html, /font-size: 18px/, '字号走 config 覆盖');
});

test('网站 DOCX 表格单元格内图片渲成 inline <img>（src 注入；空 src 跳过）', () => {
  const imgCell = (file) => ({ runs: [{ type: 'img', file, w: '80%' }], header: false });
  const result = template.processDocx({
    paragraphs: [tablePara([
      [imgCell('p1.png'), stcell('话梅')],
      [imgCell('missing.png'), stcell('芒果干')],
    ])],
    imgCache: { 'p1.png': 'blob:p1' }, // missing.png 不在缓存 → 空 src
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /<img[^>]*src="blob:p1"/, '有缓存的单元格图渲成 inline <img>');
  assert.match(html, /<img[^>]*max-width: 100%/, '单元格图尺寸受约束');
  assert.doesNotMatch(html, /<img[^>]*src="">/, '网站绝不输出空 src 图');
  // 负向断言：单元格图不吃 run 的 w 百分比（撑爆 table-layout:fixed），只走 max-width:100%。
  assert.doesNotMatch(html, /<img[^>]*\bwidth:\s*80%/, '单元格图不复用 run 宽度');
  assert.match(html, /话梅/);
});

test('网站表格单元格 http 图加 referrerpolicy（绕防盗链）；blob 图不加', () => {
  const imgCell = (file) => ({ runs: [{ type: 'img', file, w: '80%' }], header: false });
  const result = template.processDocx({
    paragraphs: [tablePara([[imgCell('remote.png'), imgCell('local.png')]])],
    imgCache: { 'remote.png': 'https://mmbiz.qpic.cn/x.png', 'local.png': 'blob:local' },
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /<img[^>]*src="https:\/\/mmbiz\.qpic\.cn\/x\.png"[^>]*referrerpolicy="no-referrer"/, 'http 图加防盗链');
  assert.doesNotMatch(html, /src="blob:local"[^>]*referrerpolicy/, '本地 blob 图不加 referrerpolicy');
});

test('网站 DOCX：带标题样式的「参考来源：」仍渲染成 #888 灰色 ref（不退化为大标题）', () => {
  // WHY 回归守卫：中文 DOCX 常给「参考来源：」打『标题3』样式/大纲级别。共享层 isHeading
  // 比旧网站宽，若把它升成 <h2>，会逃逸 applyFoodTalksBehavior 的参考识别（只扫 k:'p'），
  // 导致参考区块丢失 #888 灰色口径、[n] 降级黑色正文。此前无测试覆盖，回归零捕获。
  const result = template.processDocx({
    paragraphs: [
      txtPara('正文段落。'),
      txtPara('参考来源：', { hasHeadingStyle: true }),   // 带标题样式
      txtPara('[1] 某篇论文. 2020.'),
      txtPara('[2] 另一篇. 2021.'),
    ],
    imgCache: {},
    config: { muted_color: '#888888' },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.doesNotMatch(html, /<h2[^>]*>\s*参考来源/, '参考段头不得渲染成 <h2> 大标题');
  assert.match(html, /#888888/, '参考区块必须保留 #888 灰色口径');
  assert.match(html, /\[1\]/, '参考条目保留');
});

test('网站 DOCX：标题口径——outlineLvl 与中文「标题N」样式段都识别为标题', () => {
  // WHY：用户裁定「结构共用」，标题检测与公众号共用（outlineLvl||heading 样式）。锁住这条
  // 扩大的口径，防未来误改（规则九）。注意：纯正文不得误升标题。
  const result = template.processDocx({
    paragraphs: [
      txtPara('作者：张三'),
      txtPara('用大纲级别的小节标题', { hasOutlineLevel: true }),
      txtPara('这是普通正文，不该升标题。'),
    ],
    imgCache: {},
    config: {},
    isRepost: false,
  });
  assert.equal(result.headingN, 1, '恰好识别 1 个标题');
  const html = result.lines.join('\n');
  assert.match(html, /<h2[^>]*>用大纲级别的小节标题<\/h2>/, 'outlineLvl 段升为 <h2>');
  assert.doesNotMatch(html, /<h2[^>]*>这是普通正文/, '正文不得误升标题');
});

test('网站 DOCX：空作者名「作者：」也剥离其前置材料（红线#12）', () => {
  const result = template.processDocx({
    paragraphs: [
      txtPara('【编辑前置材料勿外传】'),
      txtPara('作者：'),
      txtPara('正文第一段。'),
    ],
    imgCache: {},
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.doesNotMatch(html, /勿外传/, '空作者名也要剥离前置材料');
  assert.match(html, /正文第一段/, '正文保留');
});

test('网站 DOCX：缓存未命中时 imgN 反映实际渲染图数（不高估）', () => {
  // WHY（nit→守卫）：一张命中 + 一张未命中，只应渲染 1 个 <img>，imgN 也应为 1。
  const result = template.processDocx({
    paragraphs: [imgPara('hit.png'), imgPara('miss.png')],
    imgCache: { 'hit.png': 'blob:hit' },
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  const imgCount = (html.match(/<img\b/g) || []).length;
  assert.equal(imgCount, 1, '只渲染命中那张图');
  assert.equal(result.imgN, 1, 'imgN 必须等于实际渲染图数，不得高估为 2');
  assert.match(html, /blob:hit/);
  assert.doesNotMatch(html, /src="\s*"/);
});

test('网站 DOCX：真实文章 fixture 不出现空 src，图片全部带 src', async () => {
  // WHY：用真实 35 段·多图 DOCX 端到端验证，比合成段落更接近线上场景。
  const buffer = await fs.readFile(new URL('./fixtures/tea-pyramid.docx', import.meta.url));
  const docx = await parseDocx(buffer);
  const result = template.processDocx({
    paragraphs: docx.paragraphs,
    imgCache: docx.imgCache,
    config: {},
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.doesNotMatch(html, /src="\s*"/, '真实 DOCX 渲染不得出现空 src');
  // 每个 <img> 都必须带非空 src
  const imgs = html.match(/<img\b[^>]*>/g) || [];
  assert.ok(imgs.length > 0, 'fixture 应含图片');
  for (const tag of imgs) {
    assert.match(tag, /src="[^"]+"/, `图片标签必须带非空 src：${tag}`);
  }
});

// ── 2026-06-11 飞书反馈批次（成品 news/62303 逐元素核对）──

test('网站 URL 导入：目录条目渲染为普通段落且条目间无空行（反馈问题十）', () => {
  // WHY：62303 成品目录区仅「目录」二字是 h2，条目是 16px 普通文本、紧密相连；
  // 旧行为把「一、…」条目升级成 h2 蓝粗大字，且「六、参考来源」被误判 ref-header。
  const hBlock = (text) => ({ k: 'h', text });
  const txtBlock = (text) => ({ k: 'txt', runs: [{ type: 'txt', text }] });
  const result = template.processBlocks({
    title: '上海艾录样例',
    blocks: [
      hBlock('目录'),
      txtBlock('一、食品包装为什么需要“轻量化”方案？'),
      txtBlock('五、联系上海艾录'),
      txtBlock('六、参考来源'),
      hBlock('食品包装为什么需要“轻量化”方案？'),
      txtBlock('正文内容段。'),
    ],
  }, { config: { modules: { fbif_footer: { enabled: false } } }, isRepost: false });
  const html = result.lines.join('\n');
  assert.match(html, /<h2[^>]*>目录<\/h2>/, '「目录」二字保持 h2');
  assert.doesNotMatch(html, /<h2[^>]*>一、食品包装为什么需要“轻量化”方案？<\/h2>/, '目录条目不得升级 h2');
  assert.match(html, /<p[^>]*>一、食品包装为什么需要“轻量化”方案？<\/p>/, '目录条目应为普通段落');
  assert.doesNotMatch(html, /参考来源：/, '「六、参考来源」目录条目不得被误判为参考来源段头');
  // 条目间紧凑：两个相邻条目之间不得有空行 <p ...><br></p>
  assert.match(html, /一、食品包装为什么需要“轻量化”方案？<\/p>\n<p[^>]*>五、联系上海艾录<\/p>/, '目录条目间不插空行');
});

test('网站 URL 导入：（一）编号标题升 h3 且括号半角化（反馈问题十-2）', () => {
  const hBlock = (text) => ({ k: 'h', text });
  const txtBlock = (text) => ({ k: 'txt', runs: [{ type: 'txt', text }] });
  const result = template.processBlocks({
    title: '样例',
    blocks: [
      hBlock('一、食品包装为什么需要“轻量化”方案？'),
      hBlock('（一）包装减量正在从“理念”变成“经营问题”'),
      txtBlock('正文段。'),
      hBlock('（二）在“少用料”和“够好用”之间找到平衡点'),
      txtBlock('又一段。'),
    ],
  }, { config: { modules: { fbif_footer: { enabled: false } } }, isRepost: false });
  const html = result.lines.join('\n');
  assert.match(html, /<h3[^>]*>\(一\)包装减量正在从“理念”变成“经营问题”<\/h3>/, '（一）应为 h3 且括号半角');
  assert.match(html, /<h3[^>]*>\(二\)在“少用料”和“够好用”之间找到平衡点<\/h3>/);
  // 62303 后台源码：h3 紧贴上文（无空行 div），间距由后台 CSS 提供
  assert.match(html, /正文段。<\/p>\n<h3/, 'h3 前不插空行');
});

test('网站 URL 导入：二维码 150、纵图长边 375（反馈问题十一/十五）', () => {
  const result = template.processBlocks({
    title: '样例',
    blocks: [
      { k: 'txt', runs: [{ type: 'txt', text: '正文段。' }] },
      { k: 'img', src: 'https://mmbiz.qpic.cn/qr.png', dataW: '161', dataRatio: '1.0124' },
      { k: 'img', src: 'https://mmbiz.qpic.cn/portrait.png', dataW: '840', dataRatio: '1.019047' },
      { k: 'img', src: 'https://mmbiz.qpic.cn/square.png', dataW: '1080', dataRatio: '1' },
      { k: 'img', src: 'https://mmbiz.qpic.cn/landscape.png', dataW: '1080', dataRatio: '0.75' },
    ],
  }, { config: { modules: { fbif_footer: { enabled: false } } }, isRepost: false });
  const html = result.lines.join('\n');
  assert.match(html, /qr\.png[^>]*width="150"/, '近正方形小图按二维码 150');
  assert.match(html, /portrait\.png[^>]*width="368"/, '840×856 纵图宽 368（高 375）');
  assert.match(html, /square\.png[^>]*width="375"/, '1:1 大图 375');
  assert.match(html, /landscape\.png[^>]*width="600"/, '横图维持 600');
});

test('网站 URL 导入：商务合作导联行渲染 14px 斜体灰、链接保留（反馈问题九）', () => {
  const result = template.processBlocks({
    title: '样例',
    blocks: [
      { k: 'txt', runs: [{ type: 'txt', text: '一文了解！' }] },
      { k: 'txt', runs: [
        { type: 'txt', text: '商务合作对接请点击：' },
        { type: 'link', text: '上海艾录商务合作联系方式', href: 'https://www.foodtalks.cn/company/ailu/contact' },
      ] },
    ],
  }, { config: { modules: { fbif_footer: { enabled: false } } }, isRepost: false });
  const html = result.lines.join('\n');
  assert.match(
    html,
    /<p><em><span style="font-size: 14px;"><span style="color: #7f7f7f;">商务合作对接请点击：<\/span><a href="https:\/\/www\.foodtalks\.cn\/company\/ailu\/contact"[^>]*>上海艾录商务合作联系方式<\/a><\/span><\/em><\/p>/,
    '62139/62303 成品口径：整行 14px 斜体、文本灰、链接保留'
  );
});
