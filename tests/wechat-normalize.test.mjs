import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { normalizeWechatRichHtml } from '../public/js/wechat-normalize.js';

test('wechat normalizer removes pasted empty br sections', () => {
  const html = '<section><span style="font-size:15px;"><br></span></section><section><span>正文</span></section>';
  const normalized = normalizeWechatRichHtml(html);
  assert.doesNotMatch(normalized, /<br/i);
  assert.match(normalized, /正文/);
});

test('wechat normalizer turns inline images into block image boxes', () => {
  const html = '<section style="text-align:center"><img style="width: 661px !important; vertical-align: baseline;" src="x.png"></section>';
  const normalized = normalizeWechatRichHtml(html);
  assert.match(normalized, /line-height:\s*0/);
  assert.match(normalized, /font-size:\s*0/);
  assert.match(normalized, /display:\s*block/);
  assert.match(normalized, /vertical-align:\s*top/);
  assert.doesNotMatch(normalized, /vertical-align:\s*baseline/i);
});

test('footer source is already a normalized component, not pasted editor html', async () => {
  const footer = await readFile(new URL('../public/footer.html', import.meta.url), 'utf8');
  assert.doesNotMatch(footer, /<section[^>]*>\s*(?:<span[^>]*>\s*)?<br\s*\/?>(?:\s*<\/span>)?\s*<\/section>/i);
  assert.doesNotMatch(footer, /vertical-align:\s*baseline/i);
  assert.doesNotMatch(footer, /line-height:\s*1\.6/i);
  assert.match(footer, /<!--REPOST_BULLET1_START-->/);
  assert.match(footer, /<section style="text-align: center;margin-left: 8px;margin-right: 8px;">/);
});

