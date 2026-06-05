// text-migrate.test.mjs — 散字段 → 中性 html 迁移 + 往返渲染（P2）。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { textFieldsToRichHtml } from '../public/js/blocks/text-migrate.js';
import { richTextToInline } from '../public/js/blocks/rich-text-render.js';

test('散字段 → 中性 html：粗体标题', () => {
  const html = textFieldsToRichHtml({ content: '提示：', bold: true, font_size: 17, text_color: '#544545', align: 'left' });
  assert.equal(html, '<p style="text-align: left;"><span style="color: #544545; font-size: 17px; font-weight: bold;">提示：</span></p>');
});

test('多行 → 多 p，空行跳过', () => {
  const html = textFieldsToRichHtml({ content: 'A\n\nB', font_size: 15, text_color: '#888888' });
  assert.match(html, /A<\/span><\/p><p[^>]*><span[^>]*>B<\/span><\/p>/);
});

test('空 content → 空串；HTML 特殊字符被转义', () => {
  assert.equal(textFieldsToRichHtml({ content: '' }), '');
  assert.match(textFieldsToRichHtml({ content: '<b>x</b>' }), /&lt;b&gt;x&lt;\/b&gt;/);
});

test('往返：迁移产出能被 rich-text-render 正确渲染', () => {
  const html = textFieldsToRichHtml({ content: '欢迎', bold: true, font_size: 16, text_color: '#396FBC', align: 'center' });
  const ch = {
    span: (s, i) => `<span data-keys="${Object.keys(s).sort().join(',')}">${i}</span>`,
    link: () => '',
    pWrap: (a, i) => `<p data-align="${a}">${i}</p>`,
  };
  const out = richTextToInline(html, (h) => parseHTML(h).document, ch);
  assert.match(out, /data-align="center"/);
  assert.match(out, /data-keys="color,font-size,font-weight"/);
  assert.match(out, /欢迎/);
});
