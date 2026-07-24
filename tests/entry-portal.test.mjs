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
  assert.match(html, /class=['"][^'"]*\bimport-actions\b[^'"]*['"]/, 'website import area should keep the file entry as a first-class zone');
  assert.match(html, /id=['"]dropZone['"]/);
  assert.match(html, /data-upload-label[^>]*>粘贴或拖拽至这里上传</, 'upload zone should explain paste and drag inside the box');
  assert.match(html, /data-add-file-text[^>]*>添加本地文件</, 'file picker should be expressed inside the upload zone');
  assert.match(html, /data-uploaded-file hidden/, 'upload zone should have a centered uploaded-file state');
  assert.match(html, /class=['"][^'"]*\bimport-card\b[^'"]*['"]/);
  assert.match(html, /class=['"][^'"]*\bdropzone\b[^'"]*['"][^>]*id=['"]dropZone['"]/);
  assert.match(html, /class=['"][^'"]*\burl-row\b[^'"]*['"][^>]*data-url-focus-target/);
  assert.doesNotMatch(html, /id=['"]urlFetchBtn['"]/, 'import panel should not keep a hidden third action; paste and keyboard shortcuts route URL/text formatting');
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
  assert.match(html, /data-upload-label[^>]*>粘贴或拖拽至这里上传</);
  assert.match(html, /data-add-file-text[^>]*>添加本地文件</);
  assert.match(html, /data-uploaded-file hidden/);
  assert.match(html, /class=['"][^'"]*\burl-row\b[^'"]*['"][^>]*data-url-focus-target/);
  assert.match(html, /<textarea[^>]*id=['"]urlInput['"]/);
  assert.doesNotMatch(html, /id=['"]urlFetchBtn['"]/);
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

test('site.html 左栏使用网站 beta 固定分区和原创/转载策略', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  assert.doesNotMatch(html, /class=['"][^'"]*\bsite-strategy-panel\b[^'"]*['"]/);
  assert.doesNotMatch(html, /id=['"]siteStrategyTitle['"]/);
  assert.doesNotMatch(html, /data-site-section=['"]body-readonly['"]/);
  assert.doesNotMatch(html, /正文内容（暂不需要）/);
  assert.doesNotMatch(html, /标准网站稿/);
  assert.doesNotMatch(html, /转载清洗/);
  assert.doesNotMatch(html, /轻量发布/);
  assert.match(html, /data-site-section=['"]top-info['"][\s\S]*文章顶部信息/);
  assert.match(html, /data-site-section=['"]footer-trailer['"][\s\S]*文章底部拖尾/);
  assert.match(html, /data-site-section=['"]footer-trailer['"][\s\S]*data-site-strategy=['"]original['"][\s\S]{0,220}>[\s\S]*原创/);
  assert.match(html, /data-site-section=['"]footer-trailer['"][\s\S]*data-site-strategy=['"]repost['"][\s\S]{0,220}>[\s\S]*转载/);
});

test('site.html 恢复转载会话时同步左栏策略按钮', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const listenerIndex = html.indexOf("window.addEventListener('fbif:article-restored'");
  const restoreIndex = html.indexOf('restoreCurrentChannel();');
  assert.notEqual(listenerIndex, -1, '网站页应监听文章恢复事件');
  assert.notEqual(restoreIndex, -1, '网站页应恢复当前渠道快照');
  assert.ok(listenerIndex < restoreIndex, '恢复快照必须发生在站点策略 listener 注册之后');
  assert.match(html, /const\s+restoredInput\s*=\s*\(cs\.article_session\s*&&\s*cs\.article_session\.input\)\s*\|\|\s*cs\.last_render_input\s*\|\|\s*null/);
  assert.match(html, /setSiteStrategyFromInput\(restoredInput,\s*cs\.is_repost\)/);
});

test('site.html 顶部信息字段可编辑并同步右侧只读预览', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  assert.match(html, /id=['"]siteArticleTitleInput['"]/);
  assert.match(html, /id=['"]siteArticleAuthorInput['"]/);
  assert.match(html, /id=['"]siteArticleSummaryInput['"]/);
  assert.match(html, /id=['"]siteArticleTagsEditor['"]/);
  assert.doesNotMatch(html, /id=['"]siteArticleTagsInput['"]/);
  assert.match(html, /function\s+renderSiteTagEditor\(/);
  assert.match(html, /function\s+editSiteTagAt\(/);
  assert.match(html, /dataset\.siteTagIndex/);
  assert.match(html, /className\s*=\s*'site-tag-chip'/);
  assert.match(html, /function\s+syncSiteArticleMetaPreview\(/);
  assert.match(html, /siteArticleTitleInput[\s\S]{0,220}titleInput\.value/);
  assert.match(html, /siteArticleAuthorInput[\s\S]{0,260}ft-meta-author[\s\S]{0,180}wx-meta-account/);
  assert.match(html, /siteArticleSummaryInput[\s\S]{0,260}ftSummary/);
  assert.match(html, /siteArticleTagsEditor[\s\S]{0,300}ftPreviewTags/);
  assert.match(html, /function\s+siteMetaFromSession\(/, '导入会话应抽取标题和作者填充顶部信息');
  assert.match(html, /articleMeta[\s\S]{0,120}\.author/, '作者应优先读取结构化 articleMeta.author');
  assert.match(html, /result[\s\S]{0,120}\.authorName/, '作者应兜底读取渲染结果 authorName');
  assert.match(html, /extractSiteAuthorFromInput\(session\.input\)/, '作者应从导入正文 payload 兜底识别');
  assert.match(html, /if \(!siteArticleMetaDirty\.title[\s\S]{0,160}meta\.title/, '标题仅在未手动编辑时自动填充');
  assert.match(html, /if \(!siteArticleMetaDirty\.author[\s\S]{0,160}meta\.author/, '作者仅在未手动编辑时自动填充');
});

test('site.html 导入后左栏保持可滚动且导入区半折叠', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/site.css', import.meta.url), 'utf8');
  assert.match(css, /\.sidebar-site \.rail-body > \*[\s\S]{0,120}flex:\s*0 0 auto/, '左栏子区块不能被 flex 压缩裁切');
  assert.match(html, /id=['"]siteImportSummary['"]/, '导入区应有已导入内容摘要条');
  assert.match(html, /function\s+setSiteImportCompact\(/, '导入后应进入半折叠状态');
  assert.match(html, /function\s+clearSiteImportCompact\(/, '清空导入时应恢复完整导入区');
  assert.match(css, /\.import-zone\.is-compact[\s\S]{0,260}\.import-actions/, '半折叠状态应隐藏大上传区');
  assert.match(css, /\.import-zone\.is-compact[\s\S]{0,320}\.url-row/, '半折叠状态应隐藏大文本框');
});

test('site.html updates DOCX summary without wiping URL summaries, and resets MD summaries', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  assert.match(html, /fbif:article-session-activated/, '网站页应监听渲染 session 激活事件');
  assert.match(html, /session\.input\.kind !== 'docx' && session\.input\.kind !== 'md'/, 'blocks 路径摘要由 URL onBlocks 管，不应被事件重置');
  assert.match(html, /session\.input\.kind === 'docx'[\s\S]{0,120}session\.result\.summary/, 'DOCX 摘要应来自渲染结果');
  assert.match(html, /summaryEl\.textContent = summary;/, 'MD 等本地文件应重置为空摘要，避免残留上一篇 DOCX 摘要或展示假数据');
  assert.doesNotMatch(html, /summary:\s*['"]这是一段摘要['"]/, '网站摘要不得使用演示占位文本冒充真实内容');
});

test('site.html uses absolute /js/ paths for dynamic imports', async () => {
  const html = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const relMatches = html.match(/import\(['"]\.\//g);
  assert.equal(relMatches, null, 'site.html should not contain relative "./..." dynamic imports');
  assert.match(html, /import\(['"]\/js\/engine\.js/);
  assert.match(html, /import\(['"]\/js\/rule-packs\/url\.js/);
});

test('local dev build IDs do not reuse stale versioned JS cache', async () => {
  const refresh = await readFile(new URL('../public/js/version-refresh.js', import.meta.url), 'utf8');
  for (const name of ['app.html', 'site.html']) {
    const html = await readFile(new URL('../public/' + name, import.meta.url), 'utf8');
    assert.match(html, /rawBuildId === 'dev'/, name + ' should detect local dev builds');
    assert.match(html, /'dev-' \+ Date\.now\(\)/, name + ' should create a fresh local asset version');
    assert.match(html, /installVersionRefresh\(\{ currentBuildId: rawBuildId, isDevBuild, loadVersionInfo \}\)/,
      name + ' should pass dev-build state to the shared refresh controller');
  }
  assert.match(refresh, /if \(isDevBuild \|\| !latestBuildId \|\| latestBuildId === currentBuildId\) return 'none'/,
    'shared refresh controller should not reload-loop on dev builds');
});
