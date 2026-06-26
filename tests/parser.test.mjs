import test from 'node:test';
import assert from 'node:assert/strict';
import { DOMParser } from '@xmldom/xmldom';
import {
  esc, escAttr, extractParagraph, parseDocxStyles, parseMdRuns, parseMdFrontmatter,
  normalizeClipboardTextForMarkdown, hasImageLikeLink, formatPlainTextForCopy,
  looksLikeArticleClipboardText, buildClipboardArticleBlocks, buildTableBlock
} from '../public/js/parser.js';

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
