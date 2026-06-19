import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { processForCopy } from '../public/js/css-inline.js';
import { DEFAULT_RULE_CONFIG } from '../public/js/rule-presets.js';

async function themeCSS() {
  return readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');
}

test('copy CSS inlining does not let font family inject HTML', async () => {
  const css = await themeCSS();
  const html = '<section class="wx-p"><span class="wx-leaf"><span class="wx-t">正文</span></span></section>';
  const out = processForCopy(html, css, {
    ...DEFAULT_RULE_CONFIG,
    font_family: "PingFang';</style><img src=x onerror=alert(1)>",
  });

  assert.doesNotMatch(out, /<img/i);
  assert.doesNotMatch(out, /onerror/i);
  assert.doesNotMatch(out, /<\/style/i);
  assert.match(out, /PingFang SC/);
});

test('copy CSS inlining lets wx-gap override wx-pi margin without duplicate declarations', async () => {
  const css = await themeCSS();
  const html = '<section class="wx-pi wx-mote-author wx-gap"><img src="x.png"></section>';
  const out = processForCopy(html, css, DEFAULT_RULE_CONFIG);
  const sectionTag = out.match(/<section[^>]*>/)?.[0] || '';

  assert.equal((sectionTag.match(/\bstyle=/g) || []).length, 1);
  assert.doesNotMatch(sectionTag, /\bclass=/);
  assert.match(sectionTag, /margin-bottom:\s*20px/);
  assert.equal((sectionTag.match(/margin-bottom:/g) || []).length, 1);
});

test('copy CSS inlining merges class styles into an existing style attribute', async () => {
  const css = await themeCSS();
  const html = '<section class="wx-pi wx-gap" style="line-height: 0; font-size: 0;"><img src="x.png"></section>';
  const out = processForCopy(html, css, DEFAULT_RULE_CONFIG);
  const sectionTag = out.match(/<section[^>]*>/)?.[0] || '';

  assert.equal((sectionTag.match(/\bstyle=/g) || []).length, 1);
  assert.match(sectionTag, /margin-bottom:\s*20px/);
  assert.match(sectionTag, /line-height:\s*0/);
  assert.match(sectionTag, /font-size:\s*0/);
});

test('copy CSS inlining keeps explicit inline style higher priority than class style', async () => {
  const css = await themeCSS();
  const html = '<section class="wx-p wx-gap" style="margin-bottom: 4px;">正文</section>';
  const out = processForCopy(html, css, DEFAULT_RULE_CONFIG);
  const sectionTag = out.match(/<section[^>]*>/)?.[0] || '';

  assert.match(sectionTag, /margin-bottom:\s*4px/);
  assert.equal((sectionTag.match(/margin-bottom:/g) || []).length, 1);
});
