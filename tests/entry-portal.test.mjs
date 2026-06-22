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
  assert.match(html, /class=['"][^'"]*\bimport-card-head\b[^'"]*['"]/, 'website import area should have a quiet source panel header');
  assert.match(html, /class=['"][^'"]*\bimport-title\b[^'"]*['"][^>]*>导入内容</);
  assert.doesNotMatch(html, /import-mode-badge/, 'import panel should not add decorative badges that compete with the editor input');
  assert.match(html, /class=['"][^'"]*\bimport-actions\b[^'"]*['"]/, 'website import area should group file fallback and primary action together');
  assert.match(html, /id=['"]dropZone['"]/);
  assert.match(html, /class=['"][^'"]*\bimport-card\b[^'"]*['"]/);
  assert.match(html, /class=['"][^'"]*\bdropzone\b[^'"]*['"][^>]*id=['"]dropZone['"]/);
  assert.match(html, /class=['"][^'"]*\burl-row\b[^'"]*['"][^>]*data-url-focus-target/);
  assert.match(html, /id=['"]urlFetchBtn['"][^>]*class=['"][^'"]*\bbtn-sm\b/, 'website URL import action should use the shared shell button size');
  assert.match(html, /id=['"]urlFetchBtn['"][\s\S]*?>排版<\/button>/, 'website primary import action should be phrased as formatting');
  assert.match(html, /id=['"]urlInput['"]/);
  assert.match(html, /<textarea[^>]*id=['"]urlInput['"]/, 'shared import box should accept multi-line pasted text');
  assert.match(html, /id=['"]previewContent['"]/);
  assert.match(html, /id=['"]copyBtn['"]/);
});

test('app.html uses the unified source import panel', async () => {
  const html = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  assert.match(html, /class=['"][^'"]*\bimport-card-head\b[^'"]*['"]/);
  assert.match(html, /class=['"][^'"]*\bimport-title\b[^'"]*['"][^>]*>导入内容</);
  assert.doesNotMatch(html, /import-mode-badge/);
  assert.match(html, /class=['"][^'"]*\bimport-actions\b[^'"]*['"]/);
  assert.match(html, /class=['"][^'"]*\bdropzone\b[^'"]*['"][^>]*id=['"]dropZone['"]/);
  assert.match(html, /class=['"][^'"]*\burl-row\b[^'"]*['"][^>]*data-url-focus-target/);
  assert.match(html, /<textarea[^>]*id=['"]urlInput['"]/);
  assert.match(html, /id=['"]urlFetchBtn['"][^>]*class=['"][^'"]*\bbtn-sm\b/);
  assert.match(html, /id=['"]urlFetchBtn['"][\s\S]*?>排版<\/button>/);
  assert.match(html, /id=['"]docZone['"][^>]*hidden/, 'public-account page should keep the imported-source summary card');
});

test('site.html uses the shared shell empty state without WeChat-only actions', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const emptyStart = html.indexOf('id="emptyState"');
  const emptyEnd = html.indexOf('<!-- Preview content -->', emptyStart);
  const empty = html.slice(emptyStart, emptyEnd);

  assert.ok(emptyStart > 0, 'website shell should render an empty state');
  assert.match(empty, /class=['"]empty-ico['"]/);
  assert.match(empty, /class=['"]empty-title['"]>还没有内容<\/div>/);
  assert.match(empty, /class=['"]empty-sub['"][\s\S]*FoodTalks 文章链接 \/ 正文/);
  assert.doesNotMatch(empty, /<p>上传文件开始预览<\/p>/, 'website shell should not keep the old one-line empty state');
  assert.doesNotMatch(empty, /class=['"]empty-cta['"]/, 'website shell should not inherit the public-account file CTA by accident');
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

test('site.html updates DOCX summary without wiping URL summaries, and resets MD summaries', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  assert.match(html, /fbif:article-session-activated/, '网站页应监听渲染 session 激活事件');
  assert.match(html, /session\.input\.kind !== 'docx' && session\.input\.kind !== 'md'/, 'blocks 路径摘要由 URL onBlocks 管，不应被事件重置');
  assert.match(html, /session\.input\.kind === 'docx'[\s\S]{0,120}session\.result\.summary/, 'DOCX 摘要应来自渲染结果');
  assert.match(html, /summaryEl\.textContent = summary \|\| FOODTALKS_PREVIEW_META\.summary/, 'MD 等本地文件应重置为默认摘要，避免残留上一篇 DOCX 摘要');
});

test('site.html uses absolute /js/ paths for dynamic imports', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const relMatches = html.match(/import\(['"]\.\//g);
  assert.equal(relMatches, null, 'site.html should not contain relative "./..." dynamic imports');
  assert.match(html, /import\(['"]\/js\/engine\.js/);
  assert.match(html, /import\(['"]\/js\/rule-packs\/url\.js/);
});

test('local dev build IDs do not reuse stale versioned JS cache', async () => {
  for (const name of ['app.html', 'site.html']) {
    const html = await readFile(new URL('../public/' + name, import.meta.url), 'utf8');
    assert.match(html, /rawBuildId === 'dev'/, name + ' should detect local dev builds');
    assert.match(html, /'dev-' \+ Date\.now\(\)/, name + ' should create a fresh local asset version');
    assert.match(html, /!isDevBuild && latestBuildId !== rawBuildId/, name + ' should not reload-loop on dev builds');
  }
});
