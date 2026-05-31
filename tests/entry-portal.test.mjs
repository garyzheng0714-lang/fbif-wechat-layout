import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('index.html redirects to /mp/fbif (portal removed)', async () => {
  // Portal selection page was retired in favor of the top channel pill inside
  // the editors. `/` now redirects to /mp/fbif at the server level, and
  // index.html provides a meta-refresh + JS fallback for direct hits.
  const html = await readFile(new URL('../public/index.html', import.meta.url), 'utf8');
  // Must redirect to the public-account editor on both meta-refresh and JS paths.
  assert.match(html, /<meta\s+http-equiv=['"]refresh['"][^>]*\/mp\/fbif/);
  assert.match(html, /location\.replace\(['"]\/mp\/fbif['"]\)/);
  // Must not still render the old portal cards/labels.
  assert.doesNotMatch(html, /channel-card-disabled/);
  assert.doesNotMatch(html, /class=['"]channel-card['"]/);
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

test('site.html renders the FoodTalks article preview shell', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/site.css', import.meta.url), 'utf8');
  assert.match(html, /FOODTALKS_PREVIEW_META/);
  assert.match(html, /id=['"]ftReadCount['"]/);
  assert.match(html, /id=['"]ftPreviewTags['"]/);
  assert.match(html, /id=['"]ftShareButton['"]/);
  assert.match(html, /id=['"]ftSharePopover['"]/);
  assert.match(html, /class=['"]ft-time-svg['"]/);
  assert.match(html, /#C9C9C9/);
  assert.match(html, /#CACCCD/);
  assert.match(html, /dataset\.previewTagKey/);
  assert.match(css, /\.preview-site \.ft-preview-tag:hover/);
  assert.match(css, /\.preview-site \.ft-share-popover/);
});

test('site.html uses absolute /js/ paths for dynamic imports', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const relMatches = html.match(/import\(['"]\.\//g);
  assert.equal(relMatches, null, 'site.html should not contain relative "./..." dynamic imports');
  assert.match(html, /import\(['"]\/js\/engine\.js/);
  assert.match(html, /import\(['"]\/js\/rule-packs\/url\.js/);
});
