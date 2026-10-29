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
const { extractClipboardLinks, extractClipboardListItems, withListMarkers } = await mod('clipboard-links.js');
const { linkSegments } = await mod('import-links.js');

function pasted(text, links) {
  return sticker.processBlocks(buildClipboardArticleBlocks(text, { links })).clipboard;
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

// 2026-10-29 老板：「相当于贴图默认要确保它要有一行空白」「这些分段都要默认加一个空白行」——
// 飞书、多维表格、Word 里复制的字每段只隔一个换行（段距不进纯文字），原先排出来挤成一团。
const NO_BLANK_SAMPLE = [
  '什么是真正的 Wasabi？先别说芥末！🌿',
  '上课了！📚 先做道选择题。',
  '吃日料刺身的时候，碟子边上那一小撮绿色的酱，是什么？',
  'B. 山葵 🟢',
  '本山葵展位：1D 馆 138（1 楼）',
];

test('原文段间没有空行：贴图照样每段空一行', () => {
  const out = pasted(NO_BLANK_SAMPLE.join('\n'));
  assert.equal(out.html, shape(NO_BLANK_SAMPLE.flatMap((line, i) => (i ? ['', line] : [line]))));
  assert.equal(out.text, NO_BLANK_SAMPLE.join('\n\n'));
});

test('几个空行、只有空格的行、Windows 换行、首尾空行：段间都是一个空行，开头结尾不出空行', () => {
  const out = pasted('\r\n\r\n  甲。  \r\n\r\n\r\n   \r\n　　\r\n \r\n乙。\r\n丙。\r\n\r\n\r\n');
  assert.equal(out.html, '<p>甲。<br><br>乙。<br><br>丙。</p>');
  assert.equal(out.text, '甲。\n\n乙。\n\n丙。');
});

test('尖括号与 & 转义，不当标签', () => {
  const out = pasted('a < b & c > d');
  assert.equal(out.html, '<p>a &lt; b &amp; c &gt; d</p>');
  assert.equal(out.text, 'a < b & c > d');
});

test('Markdown：# 标题、加粗、链接只留字；每行之间一个空行，分隔线不多出；整行图片丢掉', async () => {
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
  const lines = ['大标题', '', '第一段有加粗和链接。', '', '第二段。', '', '引用一句', '', '• 列表一项', '', '图后一段。'];
  assert.equal(clipboard.html, shape(lines));
  assert.equal(clipboard.text, lines.join('\n'));
});

// 2026-10-29 用户：AI 答复点复制按钮粘进来是 Markdown 符号，选中复制又丢了圆点，两种都「很奇怪」。
const AI_REPLY_HTML = '<meta charset="utf-8"><p>好的，这里有几个带超链接的示例：</p><ul>' +
  '<li><a href="https://www.anthropic.com">Anthropic 官网</a></li>' +
  '<li><a href="https://docs.claude.com">Claude 文档</a></li>' +
  '<li><a href="https://zh.wikipedia.org">维基百科</a></li>' +
  '</ul><p>如果你想要的是某个具体主题的链接，告诉我，我可以帮你搜索。</p>';

test('AI 答复点复制（Markdown）与选中复制（纯文字里没有圆点）排出来一样：列表带「•」，不出 Markdown 符号', () => {
  const links = extractClipboardLinks(AI_REPLY_HTML);
  const copyButton = [
    '好的，这里有几个带超链接的示例：',
    '',
    '- [Anthropic 官网](https://www.anthropic.com)',
    '- [Claude 文档](https://docs.claude.com)',
    '- [维基百科](https://zh.wikipedia.org)',
    '',
    '如果你想要的是某个具体主题的链接，告诉我，我可以帮你搜索。',
  ].join('\n');
  const selected = withListMarkers(
    '好的，这里有几个带超链接的示例：\n\nAnthropic 官网\nClaude 文档\n维基百科\n\n如果你想要的是某个具体主题的链接，告诉我，我可以帮你搜索。',
    extractClipboardListItems(AI_REPLY_HTML),
  );
  const lines = ['好的，这里有几个带超链接的示例：', '', '• Anthropic 官网', '', '• Claude 文档', '', '• 维基百科', '', '如果你想要的是某个具体主题的链接，告诉我，我可以帮你搜索。'];
  for (const text of [copyButton, selected]) {
    const out = pasted(text, links);
    assert.equal(out.html, shape(lines));
    assert.equal(out.text, lines.join('\n'));
  }
});

test('贴图下框里画蓝的字，就是贴图排出来带链接的字：普通网址不画蓝', () => {
  const text = '欢迎免费报名观展！\n看官网了解更多。';
  const links = [
    { text: '免费报名观展', href: 'https://mp.weixin.qq.com/s/1DgVTUgu0K-_h1gS0BnKWg' },
    { text: '官网', href: 'https://www.foodtalks.cn/' },
  ];
  const shown = linkSegments(text, links, sticker.keepsLink).filter(segment => segment.href).map(segment => segment.text);
  const linked = [...pasted(text, links).html.matchAll(/<a [^>]*>([^<]*)<\/a>/g)].map(m => m[1]);
  assert.deepEqual(shown, ['免费报名观展']);
  assert.deepEqual(shown, linked);
});

test('链接抓回的块：小标题、列表、引用、图注、表格行都是字；图片视频丢掉；块间一个空行，表格各行只换行', () => {
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
  const lines = ['一、市场', '', '开头，重点链接字', '', '第一项', '', '第二项', '', '引用的话', '', '图片来源：网络', '', '品类 份额', '燕麦奶 40%'];
  assert.equal(clipboard.html, shape(lines));
  assert.equal(clipboard.text, lines.join('\n'));
});

test('Word：段间一个空行（连着的空段不多出），软换行只换行，列表带编号，前面的作者行、参考来源都当正文', () => {
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
  const lines = ['作者：小编', '', '第一段。', '', '第二段。', '', '软换行前', '软换行后', '', '小标题', '', '1. 第一项', '', '参考来源：', '', '[1] 某报告'];
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

// 2026-10-27 用户在贴图编辑器里试：链到公众号文章的 <a> 粘进去变成文章链接，链到普通网页的只剩字。
test('粘贴带链接：链到公众号文章的留成 <a>，普通网页只留字；纯文本不带链接', () => {
  const text = '来f展，2027年4月21-23日在杭州。\n\n欢迎免费报名观展！更多看官网。';
  const links = [
    { text: '免费报名观展', href: 'https://mp.weixin.qq.com/s/1DgVTUgu0K-_h1gS0BnKWg' },
    { text: '官网', href: 'https://www.foodtalks.cn/' },
  ];
  const out = sticker.processBlocks(buildClipboardArticleBlocks(text, { links })).clipboard;
  assert.equal(out.html, '<p>来f展，2027年4月21-23日在杭州。<br><br>欢迎<a href="https://mp.weixin.qq.com/s/1DgVTUgu0K-_h1gS0BnKWg">免费报名观展</a>！更多看官网。</p>');
  assert.equal(out.text, text);
});

test('Markdown 里链到公众号文章的也留成 <a>', async () => {
  const out = (await sticker.processMd('看[这篇](https://mp.weixin.qq.com/s/abc)和[那个](https://example.com)。')).clipboard;
  assert.equal(out.html, '<p>看<a href="https://mp.weixin.qq.com/s/abc">这篇</a>和那个。</p>');
});
