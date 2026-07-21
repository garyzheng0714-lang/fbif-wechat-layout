import test from 'node:test';
import assert from 'node:assert/strict';
import { DOMParser } from 'linkedom';

import {
  filterSafeFooterHTMLLines,
  validateFooterHTML,
  validateFooterHTMLLines,
} from '../public/js/footer-html-policy.js';
import { FOOTER_SEGMENTS_DEFAULT } from '../public/js/footer-segments-defaults.js';
import { buildFooterHtml } from '../public/js/rule-behavior-foodtalks.js';
import { renderBottomBlocksHtml } from '../public/js/blocks/block-render.js';
import { siteBlockRenderers } from '../public/js/templates/site-blocks.js';

const parseDocument = html => new DOMParser().parseFromString(html, 'text/html');
const options = { parseDocument };

test('页脚解析器接受默认图片、链接、素材引用与受控表格', () => {
  for (const [key, segment] of Object.entries(FOOTER_SEGMENTS_DEFAULT)) {
    assert.equal(validateFooterHTMLLines(segment.html_lines, options), '', key);
  }
  const table = `<table style="border-collapse: collapse; width: 100%;"><tbody><tr><td colspan="3" style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; background-color: #01396a; color: #ffffff; font-size: 16px;"><strong>合作方案</strong></td></tr></tbody></table>`;
  assert.equal(validateFooterHTML(table, options), '');
});

test('页脚解析器拒绝可执行标签、危险属性、URL 与 CSS', () => {
  const unsafe = {
    event: `<img src="https://example.com/x.png" onerror="alert(1)">`,
    script: `<script>alert(1)</script>`,
    iframe: `<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
    object: `<object data="https://example.com/x"></object>`,
    form: `<form action="/x"><input name="x"></form>`,
    meta: `<meta http-equiv="refresh" content="0;url=https://example.com">`,
    svg: `<svg><a href="javascript:alert(1)">x</a></svg>`,
    encodedProtocol: `<a href="java&#x73;cript:alert(1)">x</a>`,
    imageData: `<img src="data:image/svg+xml,&lt;svg onload=alert(1)&gt;">`,
    srcset: `<img src="https://example.com/x.png" srcset="javascript:alert(1) 2x">`,
    styleURL: `<p style="background-color:url(https://example.com/x)">x</p>`,
    position: `<p style="position:fixed">x</p>`,
    cssEscape: `<p style="color:\\6a avascript">x</p>`,
    hugeWidth: `<p style="width:999999px">x</p>`,
    opener: `<a href="https://example.com" target="_blank">x</a>`,
    comment: `<p><!-- hidden --></p>`,
  };
  for (const [name, html] of Object.entries(unsafe)) {
    assert.notEqual(validateFooterHTML(html, options), '', name);
  }
});

test('页脚解析器会在 html_lines 拼接后再次校验', () => {
  const split = [
    '<img src="https://example.com/x.png"',
    ' onerror="alert(1)">',
  ];
  assert.match(validateFooterHTMLLines(split, options), /拼接后：.*事件属性/);
  assert.deepEqual(filterSafeFooterHTMLLines(split, options), []);
});

test('旧页脚与 bottom_blocks 渲染路径在浏览器解析器下均 fail closed', () => {
  const previous = globalThis.DOMParser;
  globalThis.DOMParser = DOMParser;
  try {
    const library = {
      segment_order: ['bad'],
      segments: { bad: { html_lines: ['<img src="https://example.com/x.png" onerror="alert(1)">'] } },
    };
    assert.equal(buildFooterHtml({ bad: true }, library, { assets: [] }), '');

    const module = {
      schema_version: 2,
      enabled: true,
      active_preset_id: 'default',
      presets: [{
        id: 'default',
        order: ['bad'],
        blocks: {
          bad: {
            type: 'rich_html',
            enabled: true,
            fields: { html_lines: ['<iframe src="https://example.com"></iframe>'] },
          },
        },
      }],
    };
    const output = renderBottomBlocksHtml(module, { enabled: true }, siteBlockRenderers({ assets: [] }));
    assert.equal(output, '');
  } finally {
    if (previous === undefined) delete globalThis.DOMParser;
    else globalThis.DOMParser = previous;
  }
});
