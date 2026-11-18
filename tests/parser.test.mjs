import test from 'node:test';
import assert from 'node:assert/strict';
import { DOMParser } from '@xmldom/xmldom';
import {
  esc, escAttr, extractParagraph, parseDocxStyles, parseMdRuns, parseMdFrontmatter,
  normalizeClipboardTextForMarkdown, hasImageLikeLink, formatPlainTextForCopy,
  looksLikeArticleClipboardText, buildClipboardArticleBlocks, buildTableBlock,
  parseDocxNumbering, markdownFileBlocks, markdownFileTitle
} from '../public/js/parser.js';
import { createListMarkers } from '../public/js/docx-list-markers.js';

const WNS = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main';

function xml(s) {
  return new DOMParser().parseFromString(s, 'text/xml').documentElement;
}

function wordStyles(inner) {
  return new DOMParser().parseFromString(
    `<w:styles xmlns:w="${WNS}">${inner}</w:styles>`,
    'text/xml'
  );
}

// ---- esc / escAttr ----

test('esc escapes &, <, >', () => {
  assert.equal(esc('a & b < c > d'), 'a &amp; b &lt; c &gt; d');
});

test('esc handles empty string', () => {
  assert.equal(esc(''), '');
});

test('escAttr escapes double quotes in addition to &, <, >', () => {
  assert.equal(escAttr('a="b"&c'), 'a=&quot;b&quot;&amp;c');
});

// ---- parseMdRuns ----

test('parseMdRuns splits bold markers', () => {
  const runs = parseMdRuns('hello **world** end');
  assert.equal(runs.length, 3);
  assert.equal(runs[0].text, 'hello ');
  assert.equal(runs[0].bold, false);
  assert.equal(runs[1].text, 'world');
  assert.equal(runs[1].bold, true);
  assert.equal(runs[2].text, ' end');
  assert.equal(runs[2].bold, false);
});

test('parseMdRuns handles no bold', () => {
  const runs = parseMdRuns('plain text');
  assert.equal(runs.length, 1);
  assert.equal(runs[0].text, 'plain text');
  assert.equal(runs[0].bold, false);
});

test('parseMdRuns handles consecutive bold', () => {
  const runs = parseMdRuns('**a****b**');
  assert.equal(runs.filter(r => r.bold).length, 2);
});

test('parseMdRuns collapses newlines', () => {
  const runs = parseMdRuns('line1\n  line2');
  assert.equal(runs[0].text, 'line1line2');
});

test('parseMdRuns handles empty string', () => {
  const runs = parseMdRuns('');
  assert.equal(runs.length, 0);
});

test('parseMdRuns all bold', () => {
  const runs = parseMdRuns('**everything bold**');
  assert.equal(runs.length, 1);
  assert.equal(runs[0].bold, true);
  assert.equal(runs[0].text, 'everything bold');
});

test('normalizeClipboardTextForMarkdown treats single-line article breaks as paragraphs', () => {
  const input = '第一段，原文这里换行。\n第二段，应该是新的段落。\n第三段也一样。';

  assert.equal(
    normalizeClipboardTextForMarkdown(input),
    '第一段，原文这里换行。\n\n第二段，应该是新的段落。\n\n第三段也一样。'
  );
});

test('normalizeClipboardTextForMarkdown keeps existing blank-line paragraphs stable', () => {
  const input = '第一段。\n\n第二段。';

  assert.equal(normalizeClipboardTextForMarkdown(input), input);
});

test('normalizeClipboardTextForMarkdown handles mixed blank and single line breaks', () => {
  const input = '第一段。\n第二段。\n\n第三段。';

  assert.equal(normalizeClipboardTextForMarkdown(input), '第一段。\n\n第二段。\n\n第三段。');
});

test('formatPlainTextForCopy converts punctuation and preserves paragraph breaks', () => {
  const input = '第一段,测试.\n第二段?';

  assert.equal(formatPlainTextForCopy(input), '第一段，测试。\n\n第二段？');
});

test('formatPlainTextForCopy removes stray spaces around Chinese punctuation', () => {
  const input = '第一段: 文字 (中文), 继续。';

  assert.equal(formatPlainTextForCopy(input), '第一段：文字（中文），继续。');
});

test('looksLikeArticleClipboardText detects title-author article drafts', () => {
  const input = [
    '年营收21.8亿！福南梅森凭什么成了英国茶叶天花板？',
    '作者：Mote莫特',
    '我以前一直不喜欢伯爵茶。',
    '味道冲，香气重，喝起来像一杯加了香水的红茶。',
  ].join('\n');

  assert.equal(looksLikeArticleClipboardText(input), true);
  assert.equal(looksLikeArticleClipboardText('第一段,测试.\n第二段?'), false);
});

test('buildClipboardArticleBlocks treats all pasted article lines as body text', () => {
  const input = [
    '年营收21.8亿！福南梅森凭什么成了英国茶叶天花板？',
    '作者：Mote莫特',
    '为什么它家的佛手柑不冲? 我查了一圈发现, 味道很柔和。',
    '',
    '这是我第一回迷上欧美的茶叶。',
  ].join('\n');

  const article = buildClipboardArticleBlocks(input);

  assert.equal(article.source, 'clipboard-text');
  assert.equal(article.bodyOnly, true);
  assert.equal(article.title, '');
  assert.equal(article.displayName, '剪贴板正文');
  assert.equal(article.blocks.some(block => block.k === 'h'), false);
  assert.equal(article.blocks[0].runs[0].text, '年营收21.8亿！福南梅森凭什么成了英国茶叶天花板？');
  assert.equal(article.blocks[1].runs[0].text, '作者：Mote莫特');
  assert.equal(article.blocks[2].runs[0].text, '为什么它家的佛手柑不冲？我查了一圈发现，味道很柔和。');
  assert.equal(article.blocks[2].blankBefore, false);
  assert.equal(article.blocks[3].blankBefore, true);
  assert.equal(article.blocks.every(block => block.compact === true), true);
});

test('buildClipboardArticleBlocks：飞书、Word 复制的纯文字段间只有一个换行，每段都不排紧', () => {
  const paras = [
    '在中国，如果一款豆腐卖不好，可能是因为名字起得不好或者包装设计不够诱人。',
    '展会里也差不多。豆腐、豆制品和广义上的植物基展商没有专区，分散在各个馆。',
    '麻烦在于，豆腐该回到哪个品类？买手要先相信趋势，再决定要不要尝一口。',
  ];
  assert.equal(buildClipboardArticleBlocks(paras.join('\n')).blocks.every(block => block.compact === false), true);
  assert.equal(buildClipboardArticleBlocks(paras.join('\n\n')).blocks.every(block => block.compact === true), true);
});

test('hasImageLikeLink detects image-bearing text', () => {
  assert.equal(hasImageLikeLink('![图](https://example.com/a.jpg)'), true);
  assert.equal(hasImageLikeLink('<img src="https://example.com/a.png">'), true);
  assert.equal(hasImageLikeLink('图片 https://mmbiz.qpic.cn/sz_mmbiz_jpg/abc/0'), true);
  assert.equal(hasImageLikeLink('图片 https://img.example.com/render?id=1&format=webp'), true);
});

test('hasImageLikeLink ignores ordinary article links', () => {
  assert.equal(hasImageLikeLink('https://mp.weixin.qq.com/s/abc'), false);
  assert.equal(hasImageLikeLink('https://www.foodtalks.cn/news/123'), false);
});

// ---- parseMdFrontmatter ----

test('parseMdFrontmatter extracts author', () => {
  const { author, content } = parseMdFrontmatter('---\nauthor: "张三"\n---\n正文内容');
  assert.equal(author, '张三');
  assert.equal(content, '正文内容');
});

test('parseMdFrontmatter handles no frontmatter', () => {
  const { author, content } = parseMdFrontmatter('没有 frontmatter 的文本');
  assert.equal(author, '');
  assert.equal(content, '没有 frontmatter 的文本');
});

test('parseMdFrontmatter handles author without quotes', () => {
  const { author, content } = parseMdFrontmatter('---\nauthor: 李四\n---\nbody');
  assert.equal(author, '李四');
  assert.equal(content, 'body');
});

test('parseMdFrontmatter handles frontmatter without author', () => {
  const { author, content } = parseMdFrontmatter('---\ntitle: test\n---\nbody');
  assert.equal(author, '');
  assert.equal(content, 'body');
});

test('parseMdFrontmatter handles empty input', () => {
  const { author, content } = parseMdFrontmatter('');
  assert.equal(author, '');
  assert.equal(content, '');
});

// ---- DOCX style semantics ----

test('extractParagraph preserves list style contextual spacing from styles.xml', () => {
  const styleSheet = parseDocxStyles(wordStyles(`
    <w:docDefaults>
      <w:pPrDefault><w:pPr><w:spacing w:after="160" w:line="278" w:lineRule="auto"/></w:pPr></w:pPrDefault>
    </w:docDefaults>
    <w:style w:type="paragraph" w:styleId="a0">
      <w:name w:val="Normal"/>
    </w:style>
    <w:style w:type="paragraph" w:styleId="a8">
      <w:name w:val="List"/>
      <w:basedOn w:val="a0"/>
      <w:pPr><w:contextualSpacing/></w:pPr>
    </w:style>
  `));
  const p = xml(`
    <w:p xmlns:w="${WNS}">
      <w:pPr><w:pStyle w:val="a8"/></w:pPr>
      <w:r><w:t>日本男前豆腐（男人味豆腐）</w:t></w:r>
    </w:p>
  `);

  const [para] = extractParagraph(p, {}, {}, styleSheet);

  assert.equal(para.text, '日本男前豆腐（男人味豆腐）');
  assert.equal(para.styleId, 'a8');
  assert.equal(para.styleName, 'List');
  assert.equal(para.isList, true);
  assert.equal(para.contextualSpacing, true);
  assert.equal(para.spacing.after, '160');
});

test('extractParagraph recognizes heading by style name when style id is opaque', () => {
  const styleSheet = parseDocxStyles(wordStyles(`
    <w:style w:type="paragraph" w:styleId="1">
      <w:name w:val="heading 1"/>
      <w:pPr><w:spacing w:before="340" w:after="330"/></w:pPr>
    </w:style>
  `));
  const p = xml(`
    <w:p xmlns:w="${WNS}">
      <w:pPr><w:pStyle w:val="1"/></w:pPr>
      <w:r><w:t>一、中国豆腐，卡在哪？</w:t></w:r>
    </w:p>
  `);

  const [para] = extractParagraph(p, {}, {}, styleSheet);

  assert.equal(para.styleId, '1');
  assert.equal(para.styleName, 'heading 1');
  assert.equal(para.hasHeadingStyle, true);
  assert.equal(para.spacing.after, '330');
});

// ---- buildTableBlock ----
// WHY：旧路径把 <w:tbl> 拍平成一维段落（行列归属全丢），表格在解析第一步就消失。
// 这些 case 锁住「表格保结构」契约：rows 二维网格、单元格 runs、首行表头标记。

function tableXml(inner, attrs = '') {
  return xml(`<w:tbl xmlns:w="${WNS}">${attrs}${inner}</w:tbl>`);
}
const tc = (text) => `<w:tc><w:p><w:r><w:t>${text}</w:t></w:r></w:p></w:tc>`;
const tr = (...cells) => `<w:tr>${cells.join('')}</w:tr>`;

test('buildTableBlock 保住二维行列结构（不再拍平）', () => {
  const tbl = tableXml(
    tr(tc('产品名称'), tc('价格')) + tr(tc('话梅'), tc('¥11.9'))
  );
  const block = buildTableBlock(tbl, {}, {}, null);
  assert.equal(block.type, 'table');
  assert.equal(block.rows.length, 2, '两行');
  assert.equal(block.rows[0].length, 2, '每行两列');
  assert.equal(block.rows[0][0].runs.map(r => r.text).join(''), '产品名称');
  assert.equal(block.rows[1][1].runs.map(r => r.text).join(''), '¥11.9');
});

test('buildTableBlock 在 tblLook w:firstRow=1 时把首行标为表头', () => {
  const tbl = tableXml(
    tr(tc('产品名称'), tc('价格')) + tr(tc('话梅'), tc('¥11.9')),
    '<w:tblPr><w:tblLook w:firstRow="1"/></w:tblPr>'
  );
  const block = buildTableBlock(tbl, {}, {}, null);
  assert.equal(block.rows[0][0].header, true, '首行是表头');
  assert.equal(block.rows[0][1].header, true);
  assert.equal(block.rows[1][0].header, false, '正文行不是表头');
});

test('buildTableBlock 无 firstRow 标记时不强行设表头', () => {
  const tbl = tableXml(tr(tc('a'), tc('b')));
  const block = buildTableBlock(tbl, {}, {}, null);
  assert.equal(block.rows[0][0].header, false);
});

test('buildTableBlock 携带段落兜底字段（下游段落扫描不崩）', () => {
  const block = buildTableBlock(tableXml(tr(tc('x'))), {}, {}, null);
  assert.equal(block.isEmpty, false);
  assert.equal(block.isHeading ?? false, false);
  assert.equal(block.text, '');
  assert.equal(block.hasOutlineLevel, false);
  assert.equal(block.hasHeadingStyle, false);
});

// ---- 飞书云文档格式保真：run 显式着色 + 无序列表项目符号 ----
// 用户报告的根因样本（文档 YM3td8b6lo6aMdxhly2c4OZknre 的「表格注释」段）：飞书导出的
// DOCX 里灰色注释带 <w:color w:val="8f959e"/> 且挂 numPr，导入后颜色与项目符号双双丢失。

function wordNumbering(inner) {
  return new DOMParser().parseFromString(
    `<w:numbering xmlns:w="${WNS}">${inner}</w:numbering>`,
    'text/xml'
  );
}

// 逐字取自飞书真实导出的 word/numbering.xml。
const FEISHU_NUMBERING = wordNumbering(`
  <w:abstractNum w:abstractNumId="872000">
    <w:lvl><w:numFmt w:val="bullet"/><w:suff w:val="tab"/><w:lvlText w:val="•"/>
      <w:rPr><w:color w:val="3370ff"/></w:rPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="872000"/></w:num>
`);

test('parseDocxNumbering maps numId to the bullet glyph and its color', () => {
  const map = parseDocxNumbering(FEISHU_NUMBERING);
  assert.deepEqual(map['1'], {
    absId: '872000', fmt: 'bullet', text: '•', color: '3370ff', start: 1, startOverride: null,
  });
});

test('parseDocxNumbering on a missing numbering.xml yields an empty map, not a crash', () => {
  assert.deepEqual(parseDocxNumbering(null), {});
});

test('createListMarkers returns the bullet for unordered lists', () => {
  const markerFor = createListMarkers(parseDocxNumbering(FEISHU_NUMBERING));
  assert.deepEqual(markerFor('1'), { marker: '•', markerColor: '3370ff' });
});

// 逐字取自飞书真实导出（小罐茶稿）：每个有序列表项独占一个 abstractNum，w:start 就是显示序号。
const FEISHU_ORDERED_NUMBERING = wordNumbering(`
  <w:abstractNum w:abstractNumId="201232"><w:lvl><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:suff w:val="tab"/><w:lvlText w:val="%1."/><w:rPr><w:color w:val="3370ff"/></w:rPr></w:lvl></w:abstractNum>
  <w:abstractNum w:abstractNumId="201233"><w:lvl><w:start w:val="2"/><w:numFmt w:val="decimal"/><w:suff w:val="tab"/><w:lvlText w:val="%1."/><w:rPr><w:color w:val="3370ff"/></w:rPr></w:lvl></w:abstractNum>
  <w:abstractNum w:abstractNumId="201237"><w:lvl><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:suff w:val="tab"/><w:lvlText w:val="%1."/><w:rPr><w:color w:val="3370ff"/></w:rPr></w:lvl></w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="201232"/></w:num>
  <w:num w:numId="2"><w:abstractNumId w:val="201233"/></w:num>
  <w:num w:numId="6"><w:abstractNumId w:val="201237"/></w:num>
`);

test('createListMarkers: 飞书 per-item numbering shows each item\'s own number', () => {
  const markerFor = createListMarkers(parseDocxNumbering(FEISHU_ORDERED_NUMBERING));
  assert.deepEqual(['1', '2', '6'].map(id => markerFor(id)), [
    { marker: '1.', markerColor: '3370ff' },
    { marker: '2.', markerColor: '3370ff' },
    { marker: '1.', markerColor: '3370ff' },
  ]);
});

const WORD_ORDERED_NUMBERING = wordNumbering(`
  <w:abstractNum w:abstractNumId="9">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/></w:lvl>
    <w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="%2)"/></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="9"/></w:num>
  <w:num w:numId="2"><w:abstractNumId w:val="9"/>
    <w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>
  <w:num w:numId="3"><w:abstractNumId w:val="9"/></w:num>
`);

test('createListMarkers: Word items sharing one numId count up in document order', () => {
  const markerFor = createListMarkers(parseDocxNumbering(WORD_ORDERED_NUMBERING));
  assert.deepEqual(['1', '1', '1'].map(id => markerFor(id).marker), ['1.', '2.', '3.']);
});

test('createListMarkers: startOverride restarts a list, a plain num continues it', () => {
  const markerFor = createListMarkers(parseDocxNumbering(WORD_ORDERED_NUMBERING));
  const seq = ['1', '1', '2', '2', '3'].map(id => markerFor(id).marker);
  // numId 2 带 startOverride=1 → 重新从 1 起；numId 3 同一 abstractNum、无 override → 接着数。
  assert.deepEqual(seq, ['1.', '2.', '1.', '2.', '3.']);
});

test('createListMarkers: missing w:start counts from 1', () => {
  const markerFor = createListMarkers(parseDocxNumbering(wordNumbering(`
    <w:abstractNum w:abstractNumId="9"><w:lvl><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/></w:lvl></w:abstractNum>
    <w:num w:numId="1"><w:abstractNumId w:val="9"/></w:num>
  `)));
  assert.equal(markerFor('1').marker, '1.');
});

test('createListMarkers: nested ordered items get no marker and do not advance level 0', () => {
  const markerFor = createListMarkers(parseDocxNumbering(WORD_ORDERED_NUMBERING));
  const seq = [['1', '0'], ['1', '1'], ['1', '1'], ['1', '0']].map(([id, lvl]) => markerFor(id, lvl).marker);
  assert.deepEqual(seq, ['1.', '', '', '2.']);
});

test('createListMarkers: unverified formats and multi-level text keep today\'s no-marker behavior', () => {
  const markerFor = createListMarkers(parseDocxNumbering(wordNumbering(`
    <w:abstractNum w:abstractNumId="1"><w:lvl><w:numFmt w:val="chineseCounting"/><w:lvlText w:val="%1、"/></w:lvl></w:abstractNum>
    <w:abstractNum w:abstractNumId="2"><w:lvl><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="%1."/></w:lvl></w:abstractNum>
    <w:abstractNum w:abstractNumId="3"><w:lvl><w:numFmt w:val="decimal"/><w:lvlText w:val="%1.%2."/></w:lvl></w:abstractNum>
    <w:num w:numId="1"><w:abstractNumId w:val="1"/></w:num>
    <w:num w:numId="2"><w:abstractNumId w:val="2"/></w:num>
    <w:num w:numId="3"><w:abstractNumId w:val="3"/></w:num>
  `)));
  for (const id of ['1', '2', '3']) assert.deepEqual(markerFor(id), { marker: '', markerColor: '' });
});

test('createListMarkers: each document parse starts its own count', () => {
  const numbering = parseDocxNumbering(WORD_ORDERED_NUMBERING);
  const first = createListMarkers(numbering);
  first('1'); first('1');
  assert.equal(createListMarkers(numbering)('1').marker, '1.');
});

test('extractParagraph numbers an ordered item once, even across soft breaks', () => {
  const markerFor = createListMarkers(parseDocxNumbering(WORD_ORDERED_NUMBERING));
  const item = () => xml(`
    <w:p xmlns:w="${WNS}">
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>第一行</w:t><w:br/><w:t>同一项的第二行</w:t></w:r>
    </w:p>
  `);

  const first = extractParagraph(item(), {}, {}, null, markerFor);
  const second = extractParagraph(item(), {}, {}, null, markerFor);

  assert.deepEqual(first.map(p => p.listMarker), ['1.', '']);
  assert.deepEqual(second.map(p => p.listMarker), ['2.', '']);
});

test('extractParagraph keeps the run color and the bullet of a 飞书 list item', () => {
  const p = xml(`
    <w:p xmlns:w="${WNS}">
      <w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:rPr><w:color w:val="8f959e"/></w:rPr><w:t>表内为伊藤园单体数据。</w:t></w:r>
    </w:p>
  `);

  const [para] = extractParagraph(p, {}, {}, null, createListMarkers(parseDocxNumbering(FEISHU_NUMBERING)));

  assert.equal(para.isList, true);
  assert.equal(para.listMarker, '•');
  assert.equal(para.listMarkerColor, '3370ff');
  assert.equal(para.runs[0].color, '8f959e');
});

test('extractParagraph draws the bullet once per list item, not once per soft break', () => {
  const p = xml(`
    <w:p xmlns:w="${WNS}">
      <w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>第一行</w:t><w:br/><w:t>同一项的第二行</w:t></w:r>
    </w:p>
  `);

  const paras = extractParagraph(p, {}, {}, null, createListMarkers(parseDocxNumbering(FEISHU_NUMBERING)));

  assert.equal(paras.length, 2);
  assert.equal(paras[0].listMarker, '•');
  assert.equal(paras[1].listMarker, '');
});

test('a paragraph outside any list carries no marker', () => {
  const p = xml(`
    <w:p xmlns:w="${WNS}">
      <w:r><w:t>我有4个问题：</w:t></w:r>
    </w:p>
  `);

  const [para] = extractParagraph(p, {}, {}, null, createListMarkers(parseDocxNumbering(FEISHU_NUMBERING)));

  assert.equal(para.isList, false);
  assert.equal(para.listMarker, '');
});

test('buildClipboardArticleBlocks 认 Markdown：链接、加粗成 run，标题与引用记号去掉，无序列表换成「•」，网址不转全角', () => {
  const article = buildClipboardArticleBlocks(
    '## 小标题\n> 引用一句\n- 看[官网](https://www.foodtalks.cn/news/1)。\n这是**重点**，(括号)照常转。\n**加粗**后面看报名。',
    { links: [{ text: '报名', href: 'https://mp.weixin.qq.com/s/one' }] },
  );
  const view = run => [run.type, run.text, run.href || '', !!run.bold];
  assert.deepEqual(article.blocks.map(block => block.runs.map(view)), [
    [['txt', '小标题', '', false]],
    [['txt', '引用一句', '', false]],
    [['txt', '• 看', '', false], ['link', '官网', 'https://www.foodtalks.cn/news/1', false], ['txt', '。', '', false]],
    [['txt', '这是', '', false], ['txt', '重点', '', true], ['txt', '，（括号）照常转。', '', false]],
    [['txt', '加粗', '', true], ['txt', '后面看', '', false], ['link', '报名', 'https://mp.weixin.qq.com/s/one', false], ['txt', '。', '', false]],
  ]);
});

test('buildClipboardArticleBlocks 按锚文字把粘贴时记下的链接挂回去；找不到的跳过，不挡后面的', () => {
  const article = buildClipboardArticleBlocks('欢迎报名，报名截止前报名。\n\n第二段看官网。', {
    links: [
      { text: '已删掉的字', href: 'https://a.example/x' },
      { text: '报名', href: 'https://mp.weixin.qq.com/s/one' },
      { text: '报名', href: 'https://mp.weixin.qq.com/s/two' },
      { text: '官网', href: 'https://www.foodtalks.cn/' },
      { text: '不是网址', href: 'javascript:alert(1)' },
    ],
  });
  assert.deepEqual(article.blocks.map(block => block.runs), [
    [
      { type: 'txt', text: '欢迎' },
      { type: 'link', text: '报名', href: 'https://mp.weixin.qq.com/s/one' },
      { type: 'txt', text: '，' },
      { type: 'link', text: '报名', href: 'https://mp.weixin.qq.com/s/two' },
      { type: 'txt', text: '截止前报名。' },
    ],
    [
      { type: 'txt', text: '第二段看' },
      { type: 'link', text: '官网', href: 'https://www.foodtalks.cn/' },
      { type: 'txt', text: '。' },
    ],
  ]);
  assert.deepEqual(buildClipboardArticleBlocks('欢迎报名。').blocks[0].runs, [{ type: 'txt', text: '欢迎报名。' }]);
});

// .md 文件三个模板共用一套切分（红线 51：智能体交的 Markdown 和人上传的 .md 同一条路）。
test('.md 文件按 Markdown 认结构：开头一级标题是文章标题，小标题、列表每一项、引用各自成段', () => {
  const md = [
    '# 植物基饮料的下一个十年',
    '',
    '作者：李四',
    '这一行和上一行是同一段。',
    '## 市场有多大 ##',
    '- 燕麦奶：份额第一',
    '* 豆奶：老牌品类',
    '1. 先做口味',
    '2) 再做渠道',
    '> 「植物基不是替代，',
    '> 是新的选择。」',
    '紧跟引用的一行另起一段。',
    '![图](https://img.test/a.png)',
    '* * *',
    '# 后面的一级标题是小标题',
    '**加粗开头**不是列表。',
  ].join('\n');
  const blocks = markdownFileBlocks(md);
  assert.deepEqual(blocks, [
    { k: 'title', text: '植物基饮料的下一个十年' },
    { k: 'p', text: '作者：李四\n这一行和上一行是同一段。' },
    { k: 'h', level: 2, text: '市场有多大' },
    { k: 'li', text: '• 燕麦奶：份额第一' },
    { k: 'li', text: '• 豆奶：老牌品类' },
    { k: 'li', text: '1. 先做口味' },
    { k: 'li', text: '2) 再做渠道' },
    { k: 'bq', text: '「植物基不是替代，\n是新的选择。」' },
    { k: 'p', text: '紧跟引用的一行另起一段。' },
    { k: 'p', text: '![图](https://img.test/a.png)' },
    { k: 'p', text: '* * *' },
    { k: 'h', level: 1, text: '后面的一级标题是小标题' },
    { k: 'p', text: '**加粗开头**不是列表。' },
  ]);
  assert.equal(markdownFileTitle(blocks), '植物基饮料的下一个十年');
  assert.equal(markdownFileTitle(markdownFileBlocks('正文先来\n\n# 不是标题')), '', '不在开头的一级标题不当文章标题');
  assert.equal(markdownFileTitle(markdownFileBlocks('# **加粗的**[标题](https://a.test)')), '加粗的标题', '标题只留字');
});
