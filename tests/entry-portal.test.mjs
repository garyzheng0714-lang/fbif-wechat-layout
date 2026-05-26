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

test('site.html exists and shares fonts/css with app.html', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  assert.match(html, /href=['"]\/site\.css['"]/);
  // 标题告诉用户这是网站工具，不是微信工具
  assert.match(html, /<title>[^<]*网站[^<]*<\/title>/);
});

test('site.html removes public-account specific DOM', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  assert.doesNotMatch(html, /id=['"]accountSwitcher['"]/);
  assert.doesNotMatch(html, /id=['"]moreArticlesEditor['"]/);
  assert.doesNotMatch(html, /id=['"]moreArticlesSection['"]/);
  assert.doesNotMatch(html, /mp\.weixin\.qq\.com/);
  assert.doesNotMatch(html, /微信公众号后台/);
});

test('site.html keeps upload + URL + preview + copy button', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  assert.match(html, /id=['"]dropZone['"]/);
  assert.match(html, /id=['"]urlInput['"]/);
  assert.match(html, /id=['"]previewContent['"]/);
  assert.match(html, /id=['"]copyBtn['"]/);
});

test('site.html uses absolute /js/ paths for dynamic imports', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const relMatches = html.match(/import\(['"]\.\//g);
  assert.equal(relMatches, null, 'site.html should not contain relative "./..." dynamic imports');
  assert.match(html, /import\(['"]\/js\/engine\.js/);
  assert.match(html, /import\(['"]\/js\/rule-packs\/url\.js/);
});
