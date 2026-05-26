import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('index.html is a channel-picker portal (no auto-redirect)', async () => {
  const html = await readFile(new URL('../public/index.html', import.meta.url), 'utf8');
  // 不再有 location.replace
  assert.doesNotMatch(html, /location\.replace\(/);
  // 必须有 mp 入口和 ft 入口的链接
  assert.match(html, /href=['"]\/mp\/fbif['"]/);
  assert.match(html, /href=['"]\/ft\/default['"]/);
  // 必须显示"公众号"和"网站"两个文案
  assert.match(html, /公众号/);
  assert.match(html, /网站/);
});

test('index.html links to shared site.css for portal styles', async () => {
  const html = await readFile(new URL('../public/index.html', import.meta.url), 'utf8');
  assert.match(html, /href=['"]\/site\.css['"]/);
});

test('site.css exists and defines .channel-portal layout', async () => {
  const css = await readFile(new URL('../public/site.css', import.meta.url), 'utf8');
  assert.match(css, /\.channel-portal\b/);
  assert.match(css, /\.channel-card\b/);
});
