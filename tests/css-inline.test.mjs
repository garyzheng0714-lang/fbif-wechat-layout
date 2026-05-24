import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { processForCopy } from '../public/js/css-inline.js';
import { DEFAULT_RULE_CONFIG } from '../public/js/rule-presets.js';

test('copy CSS inlining does not let font family inject HTML', async () => {
  const themeCSS = await readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');
  const html = '<section class="wx-p"><span class="wx-leaf"><span class="wx-t">正文</span></span></section>';
  const out = processForCopy(html, themeCSS, {
    ...DEFAULT_RULE_CONFIG,
    font_family: "PingFang';</style><img src=x onerror=alert(1)>",
  });

  assert.doesNotMatch(out, /<img/i);
  assert.doesNotMatch(out, /onerror/i);
  assert.doesNotMatch(out, /<\/style/i);
  assert.match(out, /PingFang SC/);
});
