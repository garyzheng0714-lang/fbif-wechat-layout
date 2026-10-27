// 贴图载体（2026-10-27 用户裁定）：公众号贴图编辑器只认一个段落里的 <br>，空行 = 连着两个 <br>。
// 用户在贴图编辑器里试过的 5 种剪贴板形状里只有 <p>a<br><br>b<br><br>c</p> 对，这里逐字断言成品就是这个形状。

import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { loadLayoutEngine, DEFAULT_PUBLIC_DIR } from '../render-service/layout.mjs';

await loadLayoutEngine({
  publicDir: DEFAULT_PUBLIC_DIR,
  fetchRules: async () => '{}',
  origin: 'https://layout.test',
});
const mod = p => import(pathToFileURL(path.join(DEFAULT_PUBLIC_DIR, 'js', p)).href);
const sticker = (await mod('templates/sticker.js')).default;
const { buildClipboardArticleBlocks } = await mod('engine.js');

function pasted(text) {
  return sticker.processBlocks(buildClipboardArticleBlocks(text)).clipboard;
}

function shape(lines) {
  return '<p>' + lines.join('<br>') + '</p>';
}

const USER_SAMPLE = '“这是啥？”\n\n“马冬莓。”\n\n……行了，别问了。\n\n大九酿造展位：3C馆 K30（3楼，「马冬莓」等你！）';

test('用户样稿：一个 <p>，空行是连着两个 <br>，纯文本与粘进来的一样', () => {
  const out = pasted(USER_SAMPLE);
  assert.equal(out.html, '<p>“这是啥？”<br><br>“马冬莓。”<br><br>……行了，别问了。<br><br>大九酿造展位：3C馆 K30（3楼，「马冬莓」等你！）</p>');
  assert.equal(out.text, USER_SAMPLE);
});

test('预览与复制是同一块：结果只有一行 HTML，就是剪贴板那份', () => {
  const result = sticker.processBlocks(buildClipboardArticleBlocks(USER_SAMPLE));
  assert.deepEqual(result.lines, [result.clipboard.html]);
  assert.equal(result.title, '');
  assert.equal(sticker.bodyOnly, true);
});

test('连着的行只换行，不加空行', () => {
  const out = pasted('甲。\n乙。\n丙。');
  assert.equal(out.html, '<p>甲。<br>乙。<br>丙。</p>');
  assert.equal(out.text, '甲。\n乙。\n丙。');
});

test('几个空行、只有空格的行、Windows 换行、首尾空行：都收成一个空行，开头结尾不出空行', () => {
  const out = pasted('\r\n\r\n  甲。  \r\n\r\n\r\n   \r\n　　\r\n \r\n乙。\r\n丙。\r\n\r\n\r\n');
  assert.equal(out.html, '<p>甲。<br><br>乙。<br>丙。</p>');
  assert.equal(out.text, '甲。\n\n乙。\n丙。');
});

test('尖括号与 & 转义，不当标签', () => {
  const out = pasted('a < b & c > d');
  assert.equal(out.html, '<p>a &lt; b &amp; c &gt; d</p>');
  assert.equal(out.text, 'a < b & c > d');
});

test('Markdown：# 标题、加粗、链接只留字；空行与分隔线出一个空行；整行图片丢掉、夹在中间当空行', async () => {
  const markdown = [
    '---',
    'title: 不进正文',
    '---',
    '# 大标题',
    '',
    '第一段有**加粗**和[链接](https://example.com/a)。',
    '第二段。',
    '',
    '',
    '---',
    '',
    '> 引用一句',
    '- 列表一项',
    '![配图](https://mmbiz.qpic.cn/demo/0.jpg)',
    '图后一段。',
    '',
  ].join('\n');
  const { clipboard } = await sticker.processMd(markdown);
  const lines = ['大标题', '', '第一段有加粗和链接。', '第二段。', '', '引用一句', '列表一项', '', '图后一段。'];
  assert.equal(clipboard.html, shape(lines));
  assert.equal(clipboard.text, lines.join('\n'));
});

test('链接抓回的块：小标题、列表、引用、图注、表格行都是字；图片视频丢掉；原文空段照出', () => {
  const blocks = [
    { k: 'img', src: 'https://mmbiz.qpic.cn/demo/head.jpg' },
    { k: 'h', level: 2, text: '一、市场', blankBefore: true },
    { k: 'txt', runs: [{ type: 'txt', text: '开头，' }, { type: 'txt', text: '重点', bold: true }, { type: 'link', text: '链接字', href: 'https://example.com' }] },
    { k: 'li', runs: [{ type: 'txt', text: '第一项' }], blankBefore: true },
    { k: 'li', runs: [{ type: 'txt', text: '第二项' }] },
    { k: 'bq', runs: [{ type: 'txt', text: '引用的话' }] },
    { k: 'img', src: 'https://mmbiz.qpic.cn/demo/1.jpg', dataW: '1080' },
    { k: 'cap', text: '图片来源：网络' },
    { k: 'video', src: 'https://v.qq.com/x.mp4', poster: 'https://mmbiz.qpic.cn/demo/p.jpg' },
    { k: 'table', rows: [
      [{ runs: [{ type: 'txt', text: '品类' }], header: true }, { runs: [{ type: 'txt', text: '份额' }], header: true }],
      [{ runs: [{ type: 'txt', text: '燕麦奶' }] }, { runs: [{ type: 'txt', text: '40%', bold: true }] }],
    ] },
    { k: 'img', src: 'https://mmbiz.qpic.cn/demo/tail.jpg' },
  ];
  const { clipboard } = sticker.processBlocks({ title: '标题不进正文', blocks });
  const lines = ['一、市场', '开头，重点链接字', '', '第一项', '第二项', '引用的话', '', '图片来源：网络', '', '品类 份额', '燕麦奶 40%'];
  assert.equal(clipboard.html, shape(lines));
  assert.equal(clipboard.text, lines.join('\n'));
});

test('Word：空段即空行，列表带编号，前面的作者行、参考来源都当正文', () => {
  const para = (text, extra = {}) => ({ text, runs: [{ type: 'txt', text }], ...extra });
  const empty = { text: '', runs: [], isEmpty: true };
  const paragraphs = [
    empty,
    para('作者：小编'),
    para('第一段。'),
    empty,
    empty,
    para('第二段。'),
    { text: '软换行前软换行后', runs: [{ type: 'txt', text: '软换行前' }, { type: 'br' }, { type: 'txt', text: '软换行后' }] },
    para('小标题', { hasHeadingStyle: true }),
    para('第一项', { isList: true, listMarker: '1.' }),
    para('参考来源：'),
    para('[1] 某报告'),
    empty,
  ];
  const { clipboard } = sticker.processDocx({ paragraphs, imgCache: {} });
  const lines = ['作者：小编', '第一段。', '', '第二段。', '软换行前', '软换行后', '小标题', '1. 第一项', '参考来源：', '[1] 某报告'];
  assert.equal(clipboard.html, shape(lines));
  assert.equal(clipboard.text, lines.join('\n'));
});

test('没有字时剪贴板纯文本为空（引擎据此拒绝复制）', () => {
  const { clipboard } = sticker.processBlocks({ blocks: [{ k: 'img', src: 'https://mmbiz.qpic.cn/demo/1.jpg' }] });
  assert.equal(clipboard.html, '<p></p>');
  assert.equal(clipboard.text, '');
});

test('贴图模板与其他渲染器物理隔离：只 import engine 与中性模块', async () => {
  const { readFile } = await import('node:fs/promises');
  const source = await readFile(path.join(DEFAULT_PUBLIC_DIR, 'js', 'templates', 'sticker.js'), 'utf8');
  const imports = [...source.matchAll(/import\(\s*'([^']+)'/g)].map(m => m[1]);
  assert.deepEqual(imports, ['../engine.js', '../docx-article-model.js']);
});
