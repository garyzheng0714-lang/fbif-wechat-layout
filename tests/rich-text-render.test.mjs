// rich-text-render.test.mjs — 中性富文本 html → 渠道 inline 的编排层测试（P2 渲染期转换）。
// 用 linkedom 注入 parseDoc（生产是浏览器 DOMParser）；mock ch 验证结构/样式合并。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { richTextToInline, normalizeRichHtml } from '../public/js/blocks/rich-text-render.js';

const parseDoc = (html) => parseHTML(html).document;

function styleStr(s) {
  return 'style="' + Object.keys(s).sort().map((k) => `${k}:${s[k]}`).join(';') + '"';
}
const ch = {
  span: (s, inner) => `<span ${styleStr(s)}>${inner}</span>`,
  link: (href, s, inner) => `<a href="${href}" ${styleStr(s)}>${inner}</a>`,
  pWrap: (align, inner) => `<p align="${align}">${inner}</p>`,
};

test('p > span：提取 style 白名单 + 段对齐', () => {
  const out = richTextToInline('<p style="text-align:center"><span style="color:#544545;font-weight:bold;font-size:17px">提示</span></p>', parseDoc, ch);
  assert.equal(out, '<p align="center"><span style="color:#544545;font-size:17px;font-weight:bold">提示</span></p>');
});

test('strong/em/u 标签映射成样式，嵌套子继承父', () => {
  const out = richTextToInline('<p><strong>粗<em>粗斜</em></strong></p>', parseDoc, ch);
  assert.match(out, /<span style="font-weight:bold">粗/);
  assert.match(out, /<span style="font-style:italic;font-weight:bold">粗斜<\/span>/);
});

test('a 保留 href + 样式', () => {
  const out = richTextToInline('<p><a href="https://x.com" style="color:#105b89">链接</a></p>', parseDoc, ch);
  assert.match(out, /<a href="https:\/\/x\.com" style="color:#105b89">链接<\/a>/);
});

test('多段 + br + 列表降为带项目符号的段', () => {
  const out = richTextToInline('<p>A<br>B</p><ul><li>项</li></ul>', parseDoc, ch);
  assert.match(out, /<p align="left">A<br>B<\/p>/);
  assert.match(out, /<p align="left">• 项<\/p>/);
});

test('p 块样式包裹整段（含纯文本，codex #2）', () => {
  const out = richTextToInline('<p style="color:#333;font-size:14px">纯文本</p>', parseDoc, ch);
  assert.match(out, /<span style="color:#333;font-size:14px">纯文本<\/span>/);
});

test('href 协议白名单：javascript:→#，https 保留（codex #2）', () => {
  assert.match(richTextToInline('<p><a href="javascript:alert(1)">x</a></p>', parseDoc, ch), /<a href="#"/);
  assert.match(richTextToInline('<p><a href="https://x.com">y</a></p>', parseDoc, ch), /href="https:\/\/x\.com"/);
});

test('顶层 inline/文本无 p 包裹也成段（不丢富文本，codex #1/#5）', () => {
  assert.match(richTextToInline('<span style="color:#544545">提示</span>', parseDoc, ch), /<p align="left"><span style="color:#544545">提示<\/span><\/p>/);
  assert.match(richTextToInline('纯顶层文本', parseDoc, ch), /<p align="left">纯顶层文本<\/p>/);
});

test('ol 用递增序号、ul 用圆点；只遍历直接子 li（codex Block 4）', () => {
  assert.match(richTextToInline('<ol><li>甲</li><li>乙</li></ol>', parseDoc, ch), /<p align="left">1\. 甲<\/p>\n<p align="left">2\. 乙<\/p>/);
  assert.match(richTextToInline('<ul><li>项</li></ul>', parseDoc, ch), /<p align="left">• 项<\/p>/);
});

test('空 html → 空串', () => {
  assert.equal(richTextToInline('', parseDoc, ch), '');
  assert.equal(richTextToInline('   ', parseDoc, ch), '');
});

test('normalizeRichHtml：wangEditor 产出 → 中性白名单（去 div/data-*，保留 p/span/style）', () => {
  const dirty = '<p style="text-align:center"><span style="color:#544545;font-weight:bold" data-w-e="1">提示</span></p><div>x</div>';
  const out = normalizeRichHtml(dirty, parseDoc);
  assert.match(out, /<p style="text-align: center;"><span style="color: #544545; font-weight: bold;">提示<\/span><\/p>/);
  assert.ok(!out.includes('data-w-e'), 'data-* 去除');
  assert.match(out, /<p style="text-align: left;">x<\/p>/, 'div 标签丢、内容保留为段');
});
