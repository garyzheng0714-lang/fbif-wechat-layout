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
