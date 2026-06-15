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

function imgPara(file, w = '100%', aspect = 0, afterRuns = []) {
  const runs = [{ type: 'img', file, w, aspect }, ...afterRuns];
  return para({
    runs,
    text: afterRuns.filter(r => r.type !== 'img').map(r => r.text || '').join(''),
    hasImg: true,
  });
}

function txtPara(text, extra = {}) {
  return para({ runs: [{ type: 'txt', text }], text, ...extra });
}

function seriesDocxConfig(extra = {}) {
  return {
    justify_paragraphs: false,
    muted_color: '#7f7f7f',
    caption_font_size: '12',
    footer_note_color: '#7f7f7f',
    footer_note_font_size: '14',
    behavior: {
      params: {
        site_variant: { type: 'string', default: 'ft-series' },
        docx_summary_max_chars: { type: 'number', default: 80 },
        docx_summary_consume: { type: 'boolean', default: true },
        docx_same_paragraph_image_caption: { type: 'boolean', default: true },
        docx_use_image_aspect_width: { type: 'boolean', default: true },
        docx_qr_max_display_percent: { type: 'number', default: 35 },
      },
    },
    modules: { fbif_footer: { enabled: false }, bottom_blocks: { enabled: false, presets: [] } },
    ...extra,
  };
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

test('网站什么是系列 DOCX：短摘要进入摘要字段并从正文移除', () => {
  // WHY：URL 导入已有摘要消费逻辑，DOCX 上传也应把开头短导语放进网站摘要框，
  // 否则 62303 这类文章会把摘要留在正文第一段。
  const summary = '少用即环保，以年消耗1000吨塑料测算，可减少约692.0吨碳排放';
  const result = template.processDocx({
    paragraphs: [
      txtPara(summary),
      imgPara('head.jpeg', '96%', 1.333, [{ type: 'txt', text: '盛放食品的塑料托盒' }]),
      txtPara('正文第一段。'),
    ],
    imgCache: { 'head.jpeg': 'blob:head' },
    config: seriesDocxConfig(),
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.equal(result.summary, summary, '短导语应返回给网站摘要框');
  assert.doesNotMatch(html, new RegExp(summary), '摘要被消费后不应在正文重复出现');
  assert.match(html, /盛放食品的塑料托盒/, '头图同段说明不能因摘要消费被吞掉');
});

test('网站默认 DOCX：未配置系列摘要参数时不自动消费首段', () => {
  // WHY：红线是不同网站账号规则分开；ft-default 不应因为 ft-series 的 DOCX 摘要规则改变输出。
  const summary = '少用即环保，以年消耗1000吨塑料测算，可减少约692.0吨碳排放';
  const result = template.processDocx({
    paragraphs: [txtPara(summary), imgPara('head.jpeg', '96%', 1.333)],
    imgCache: { 'head.jpeg': 'blob:head' },
    config: { modules: { fbif_footer: { enabled: false } } },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.equal(result.summary || '', '', 'ft-default 不返回 DOCX 摘要');
  assert.match(html, new RegExp(summary), 'ft-default 仍保留首段正文');
});

test('网站什么是系列 DOCX：目录空段后的一、标题不再被当成目录条目', () => {
  // WHY：真实 DOCX 的目录和正文之间有空段；目录后的正文一级标题也是“一、...”，
  // 若空段边界不生效，正文标题会被 skipHeadingPromote 错过。
  const heading = '一、食品包装为什么需要“轻量化”方案？';
  const result = template.processDocx({
    paragraphs: [
      txtPara('目录', { runs: [{ type: 'txt', text: '目录', bold: true }], allBold: true }),
      txtPara(heading),
      txtPara('二、什么是上海艾录微发泡片材？'),
      para({ isEmpty: true }),
      txtPara(heading, { runs: [{ type: 'txt', text: heading, bold: true }], allBold: true }),
      txtPara('正文第一段。'),
    ],
    imgCache: {},
    config: seriesDocxConfig(),
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /<p>一、食品包装为什么需要“轻量化”方案？<\/p>\n<p>二、什么是上海艾录微发泡片材？<\/p>/, '目录条目仍是紧凑普通段落');
  const h2s = html.match(/<h2>一、食品包装为什么需要“轻量化”方案？<\/h2>/g) || [];
  assert.equal(h2s.length, 1, '空段后的正文一级标题应升级为 h2，且只出现一次');
});

test('网站什么是系列 DOCX：摘要提取不得把目录当摘要吃掉', () => {
  // WHY：摘要提取发生在目录标记前；“目录”是普通加粗段且后文常有图片，不能被短文本规则误吞。
  const result = template.processDocx({
    paragraphs: [
      txtPara('目录', { runs: [{ type: 'txt', text: '目录', bold: true }], allBold: true }),
      txtPara('一、食品包装为什么需要“轻量化”方案？'),
      para({ isEmpty: true }),
      txtPara('一、食品包装为什么需要“轻量化”方案？', { runs: [{ type: 'txt', text: '一、食品包装为什么需要“轻量化”方案？', bold: true }], allBold: true }),
      imgPara('p.jpeg', '96%', 1.333, [{ type: 'txt', text: '配图说明' }]),
    ],
    imgCache: { 'p.jpeg': 'blob:p' },
    config: seriesDocxConfig(),
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.equal(result.summary || '', '', '目录不应成为摘要');
  assert.match(html, /<h2>目录<\/h2>/, '目录标题必须保留');
  assert.match(html, /<h2>一、食品包装为什么需要“轻量化”方案？<\/h2>/, '正文标题仍应升级');
});

test('网站什么是系列 DOCX：同段图片说明保留为图注，下一段来源继续并入图注组', () => {
  // WHY：真实 DOCX 会把“ESG标识”和图片放同段，图片来源放下一段；二者都属于图片说明组。
  const result = template.processDocx({
    paragraphs: [
      imgPara('esg.jpeg', '52%', 1, [{ type: 'txt', text: 'ESG标识' }]),
      txtPara('图片来源：小红书@ESG绿色主义'),
      txtPara('正文第一段。'),
    ],
    imgCache: { 'esg.jpeg': 'blob:esg' },
    config: seriesDocxConfig(),
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /font-size: 12px; color: #7f7f7f;">ESG标识/, '同段图片文字应渲染为图注');
  assert.match(html, /font-size: 12px; color: #7f7f7f;">图片来源：小红书@ESG绿色主义/, '下一段来源应继续并入图注组');
});

test('网站什么是系列 DOCX：按图片显示比例输出方图/竖图/二维码宽度', () => {
  // WHY：parser 已解析 DOCX 显示宽高比；网站适配层不能丢掉 aspect 后全部回退 600。
  const result = template.processDocx({
    paragraphs: [
      imgPara('square.jpeg', '52%', 1),
      imgPara('portrait.png', '64%', 0.981),
      imgPara('qr.png', '26%', 1),
    ],
    imgCache: { 'square.jpeg': 'blob:square', 'portrait.png': 'blob:portrait', 'qr.png': 'blob:qr' },
    config: seriesDocxConfig(),
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /src="blob:square"[^>]*width="375"/, '1:1 正文图按高 375 输出宽 375');
  assert.match(html, /src="blob:portrait"[^>]*width="368"/, '近竖图按 375 * 宽高比 输出宽度');
  assert.match(html, /src="blob:qr"[^>]*width="150"/, '显示宽度很小的近方图按二维码宽度 150');
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

test('网站页脚库：BD 名片片段使用传入的素材引用渲染，支持当前文章选择覆盖', () => {
  // WHY：用户需要自己选择本篇 BD 名片。选择器只应替换 ft-series 网站页脚库里的 bd_card
  // asset 引用，渲染器仍走素材库解析，不硬编码图片 URL。
  const result = template.processBlocks({
    title: 'BD 名片选择样例',
    blocks: [{ k: 'txt', runs: [{ type: 'txt', text: '正文。' }] }],
  }, {
    config: {
      modules: {
        asset_library: {
          assets: [
            { id: 'ftss_bd_soso', label: 'BD名片 Soso', url: '/assets/ft-series/bd/soso.png', scope: 'global' },
            { id: 'ftss_bd_tiki', label: 'BD名片 Tiki', url: '/assets/ft-series/bd/tiki.png', scope: 'global' },
          ],
        },
        fbif_footer: {
          enabled: true,
          active_preset_id: 'p1',
          segment_order: ['bd_card'],
          segments: {
            bd_card: {
              html_lines: ['<p><img class="mc-img" src="{{asset#ftss_bd_tiki}}" alt="BD名片" width="600" /></p>'],
            },
          },
          presets: [{ id: 'p1', name: '默认', segments: { tip: false, bd_card: true } }],
        },
      },
    },
    isRepost: false,
  });
  const html = result.lines.join('\n');
  assert.match(html, /\/assets\/ft-series\/bd\/tiki\.png/, '应渲染当前文章选择的 Tiki 名片');
  assert.doesNotMatch(html, /\/assets\/ft-series\/bd\/soso\.png/, '不应回退到默认 Soso 名片');
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
