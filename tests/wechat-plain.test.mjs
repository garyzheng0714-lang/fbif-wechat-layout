// 「格式清理」（2026-10-27 用户裁定）：粘贴文字 → 点复制 → 粘回公众号编辑器是正常格式。
// 剪贴板 text/html 用排版接口同一条复制链（render-service/layout.mjs → engine.layoutArticleForCopy：
// 模板排正文、复制层内联、微信归一化、零高度光标段），与网页复制逐字一样（tests/layout-parity.test.mjs）。
// text/plain 是网页复制按钮读的 #contentArea.textContent：预览 = 正文行用换行连起来 + 页脚标记注释。

import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseHTML } from 'linkedom';
import { loadLayoutEngine, DEFAULT_PUBLIC_DIR } from '../render-service/layout.mjs';

const RULES = JSON.stringify({
  preset: {
    id: 'wechat-plain-default',
    version: 1,
    config: {
      footer_enabled: false,
      more_articles_enabled: false,
      modules: { bottom_blocks: { schema_version: 2, enabled: false, presets: [] } },
    },
    behavior: {},
  },
});
const engine = await loadLayoutEngine({
  publicDir: DEFAULT_PUBLIC_DIR,
  fetchRules: async () => RULES,
  origin: 'https://layout.test',
});
const mod = p => import(pathToFileURL(path.join(DEFAULT_PUBLIC_DIR, 'js', p)).href);
const template = (await mod('templates/wechat-plain.js')).default;
const { buildClipboardArticleBlocks, hasImageLikeLink, normalizeClipboardTextForMarkdown } = await mod('engine.js');
const { normalizeWechatRichHtml } = await mod('wechat-normalize.js');

const PASTE_TAIL = '<p style="margin:0;padding:0;line-height:0;font-size:0;min-height:0;height:0;"><br/></p>';

async function copiedHtml(input) {
  const out = await engine.layout({ account: 'wechat-plain', input });
  return out.html;
}

// engine._handleText：没有图片链接走粘贴块，有就当 Markdown；预览 DOM 是 normalizeRich 后的正文 + 页脚标记。
async function copiedPlainText(text) {
  const result = hasImageLikeLink(normalizeClipboardTextForMarkdown(text))
    ? await template.processMd(normalizeClipboardTextForMarkdown(text))
    : template.processBlocks(buildClipboardArticleBlocks(text));
  const articleHtml = normalizeWechatRichHtml(result.lines.join('\n'));
  const { document } = parseHTML(`<div id="contentArea">${articleHtml}<!--fbif-preview-footer--></div>`);
  return document.getElementById('contentArea').textContent;
}

const USER_SAMPLE = [
  '“这是啥？”',
  '',
  '“马冬莓。”',
  '',
  '“马冬什么？”',
  '',
  '……行了，别问了。草莓的莓，它是一罐酒。',
  '',
  '「马冬莓」是酸奶冰淇淋西打，也就是果酒。红颜草莓打头，黑莓垫底，果汁含量不低于50%。',
  '',
  '大九酿造展位：3C馆 K30（3楼，「马冬莓」等你！）',
].join('\n');

test('用户样稿：每段一个干净的 <p>，段间空行是一个 <p><br></p>，没有任何样式', async () => {
  assert.equal(await copiedHtml({ kind: 'text', text: USER_SAMPLE }), [
    '<p>“这是啥？”</p>',
    '<p><br></p>',
    '<p>“马冬莓。”</p>',
    '<p><br></p>',
    '<p>“马冬什么？”</p>',
    '<p><br></p>',
    '<p>……行了，别问了。草莓的莓，它是一罐酒。</p>',
    '<p><br></p>',
    '<p>「马冬莓」是酸奶冰淇淋西打，也就是果酒。红颜草莓打头，黑莓垫底，果汁含量不低于50%。</p>',
    '<p><br></p>',
    '<p>大九酿造展位：3C馆 K30（3楼，「马冬莓」等你！）</p>',
    PASTE_TAIL,
  ].join('\n'));
  assert.equal(await copiedPlainText(USER_SAMPLE), USER_SAMPLE);
});

test('空一行才出空行段，单个换行只换段', async () => {
  const html = '<p>甲。</p>\n<p><br></p>\n<p>乙。</p>\n<p>丙。</p>\n' + PASTE_TAIL;
  assert.equal(await copiedHtml({ kind: 'text', text: '甲。\n\n乙。\n丙。' }), html);
  assert.equal(await copiedPlainText('甲。\n\n乙。\n丙。'), '甲。\n\n乙。\n丙。');
});

test('连着几个空行、只有空格或全角空格的行、Windows 换行、首尾空行：都收成同一个形状', async () => {
  const messy = '\r\n\r\n  甲。  \r\n\r\n\r\n   \r\n　　\r\n\u00A0\r\n乙。\r\n丙。\r\n\r\n\r\n';
  assert.equal(
    await copiedHtml({ kind: 'text', text: messy }),
    '<p>甲。</p>\n<p><br></p>\n<p>乙。</p>\n<p>丙。</p>\n' + PASTE_TAIL,
  );
  assert.equal(await copiedPlainText(messy), '甲。\n\n乙。\n丙。');
});

test('带图片链接的文字按 Markdown 排：只留加粗，图片单独一段，链接只留字；engine 先把每行隔成空行，所以行间都有空行', async () => {
  const text = '## 小标题\n这是**加粗**的开头，见[原文](https://example.com/a)。\n![配图](https://mmbiz.qpic.cn/demo/0.jpg)\n- 列表一项';
  assert.equal(await copiedHtml({ kind: 'text', text }), [
    '<p>小标题</p>',
    '<p><br></p>',
    '<p>这是<strong>加粗</strong>的开头，见原文。</p>',
    '<p><br></p>',
    '<p><img src="https://mmbiz.qpic.cn/demo/0.jpg" referrerpolicy="no-referrer" style="display: block; margin: 0 auto; max-width: 100% !important; height: auto !important; vertical-align: top; box-sizing: border-box;"></p>',
    '<p><br></p>',
    '<p>列表一项</p>',
    PASTE_TAIL,
  ].join('\n'));
  assert.equal(await copiedPlainText(text), '小标题\n\n这是加粗的开头，见原文。\n\n\n\n列表一项');
});

test('Markdown 文件：空行（连着几个也一样）与分隔线出一个空行，相邻两行各成一段不加空行', async () => {
  const markdown = '# 标题\n\n第一段。\n第二段。\n\n\n第三段。\n\n---\n\n第四段。\n';
  assert.equal(await copiedHtml({ kind: 'markdown', text: markdown }), [
    '<p>标题</p>',
    '<p><br></p>',
    '<p>第一段。</p>',
    '<p>第二段。</p>',
    '<p><br></p>',
    '<p>第三段。</p>',
    '<p><br></p>',
    '<p>第四段。</p>',
    PASTE_TAIL,
  ].join('\n'));
});

test('链接抓回的正文块：小标题、列表、图注都是普通段，表格是裸表格，视频丢掉，空段照原文（开头不出空行）', async () => {
  const blocks = [
    { k: 'h', level: 2, text: '一、市场', blankBefore: true },
    { k: 'txt', runs: [{ type: 'txt', text: '开头，' }, { type: 'txt', text: '重点', bold: true }, { type: 'link', text: '链接字', href: 'https://example.com' }] },
    { k: 'li', runs: [{ type: 'txt', text: '第一项' }], blankBefore: true },
    { k: 'li', runs: [{ type: 'txt', text: '第二项' }] },
    { k: 'img', src: 'https://mmbiz.qpic.cn/demo/1.jpg', dataW: '1080', blankBefore: true },
    { k: 'cap', text: '图片来源：网络' },
    { k: 'video', src: 'https://v.qq.com/x.mp4', poster: 'https://mmbiz.qpic.cn/demo/p.jpg', blankBefore: true },
    { k: 'table', rows: [
      [{ runs: [{ type: 'txt', text: '品类' }], header: true }, { runs: [{ type: 'txt', text: '份额' }], header: true }],
      [{ runs: [{ type: 'txt', text: '燕麦奶' }] }, { runs: [{ type: 'txt', text: '40%', bold: true }] }],
    ] },
  ];
  assert.equal(await copiedHtml({ kind: 'blocks', title: '标题不进正文', blocks }), [
    '<p>一、市场</p>',
    '<p>开头，<strong>重点</strong>链接字</p>',
    '<p><br></p>',
    '<p>第一项</p>',
    '<p>第二项</p>',
    '<p><br></p>',
    '<p><img src="https://mmbiz.qpic.cn/demo/1.jpg" referrerpolicy="no-referrer" style="display: block; margin: 0 auto; max-width: 100% !important; height: auto !important; vertical-align: top; box-sizing: border-box;"></p>',
    '<p>图片来源：网络</p>',
    '<p><br></p>',
    '<table><tbody><tr><td>品类</td><td>份额</td></tr><tr><td>燕麦奶</td><td><strong>40%</strong></td></tr></tbody></table>',
    PASTE_TAIL,
  ].join('\n'));
});

test('没有页脚：模板明确交空底部，不回退公众号页脚', () => {
  assert.deepEqual(template.renderBottomFooter({ isRepost: true }), { html: '' });
});
