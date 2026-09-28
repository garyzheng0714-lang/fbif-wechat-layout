// 飞书有序列表与行首序号的端到端守护（2026-09-28 小罐茶稿实证）。
// fixture 截自该稿飞书导出 docx 的真实 document.xml / numbering.xml（去图）：
//   · 三级标题「1. “洗茶”」—— 飞书把标题自动编号导出成字面文本「1. 」；
//   · 三组有序列表 5+4+5 项 —— 每项独占一个 abstractNum，w:start 就是显示序号。
// 曾经的两处错误：标题序号被标点规则改成「1。」；列表序号整段丢失。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { DOMParser as XmldomParser } from '@xmldom/xmldom';
import JSZip from 'jszip';

const storage = {
  getItem() { return null; },
  setItem() {},
  removeItem() {},
};

class DOMParserShim {
  parseFromString(xml, type) {
    const doc = new XmldomParser().parseFromString(xml, type);
    doc.querySelectorAll = (selector) => Array.from(doc.getElementsByTagName(selector));
    return doc;
  }
}

globalThis.DOMParser = DOMParserShim;
globalThis.JSZip = JSZip;
globalThis.window = {
  __APP_BUILD_INFO: { build: 'test' },
  localStorage: storage,
};
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });
globalThis.URL.createObjectURL = (blob) => 'blob:test/' + (blob && blob.size || 0);

const { parseDocx } = await import('../public/js/parser.js?feishuOrdered=' + Date.now());
const template = (await import('../public/js/templates/fbif.js?feishuOrdered=' + Date.now())).default;
const { processForCopy } = await import('../public/js/css-inline.js?feishuOrdered=' + Date.now());
const { DEFAULT_RULE_CONFIG } = await import('../public/js/rule-presets.js?feishuOrdered=' + Date.now());

const EXPECTED_MARKERS = ['1.', '2.', '3.', '4.', '5.', '1.', '2.', '3.', '4.', '1.', '2.', '3.', '4.', '5.'];

async function parseFixture() {
  const buffer = await fs.readFile(new URL('./fixtures/feishu-ordered-list.docx', import.meta.url));
  return parseDocx(buffer);
}

async function renderFixture() {
  const docx = await parseFixture();
  const result = template.processDocx({ paragraphs: docx.paragraphs, imgCache: {}, isRepost: false });
  return result.lines.join('\n');
}

test('飞书 docx: every ordered list item carries its own number', async () => {
  const { paragraphs } = await parseFixture();
  const items = paragraphs.filter(p => p.isList);
  assert.deepEqual(items.map(p => p.listMarker), EXPECTED_MARKERS);
  assert.ok(items.every(p => p.listMarkerColor === '3370ff'));
  assert.ok(items[0].text.startsWith('大红袍茶：'));
  // 序号是渲染层的独立符号，不能混进正文文本（否则会被标点规则改写、被复制两次）。
  assert.ok(items.every(p => !/^\d/.test(p.text)));
});

test('飞书 docx: heading "1. “洗茶”" keeps its halfwidth ordinal period', async () => {
  const { paragraphs } = await parseFixture();
  const heading = paragraphs.find(p => p.text.includes('洗茶') && p.hasOutlineLevel);
  assert.equal(heading.text, '1. “洗茶”');
});

test('公众号 preview: numbers render in front of each item, none becomes 「1。」', async () => {
  const html = await renderFixture();
  const markers = [...html.matchAll(/style="color: #3370ff;">(\d+\.) <\/span>/g)].map(m => m[1]);
  assert.deepEqual(markers, EXPECTED_MARKERS);
  assert.match(html, /1\. “洗茶”/);
  assert.doesNotMatch(html, /\d。\s*“洗茶”/);
});

test('公众号 copy HTML keeps each number and its color', async () => {
  const html = await renderFixture();
  const themeCSS = await fs.readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');
  const copyHtml = processForCopy(html, themeCSS, DEFAULT_RULE_CONFIG);
  const at = copyHtml.indexOf('大红袍茶：');
  const before = copyHtml.slice(copyHtml.lastIndexOf('<section', at), at);
  assert.match(before, /1\. /);
  assert.match(before, /color:\s*#3370ff/i);
});
