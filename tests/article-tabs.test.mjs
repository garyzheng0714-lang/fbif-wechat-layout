import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('article tabs sit above the article preview, not above the left rail', async () => {
  const html = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const appLayoutStart = html.indexOf('<div class="app-layout">');
  const railStart = html.indexOf('<aside class="rail sidebar"', appLayoutStart);
  const previewPanelStart = html.indexOf('<main class="preview-panel"', appLayoutStart);
  const stripStart = html.indexOf('<div class="article-tabs-strip"', previewPanelStart);
  const previewScrollStart = html.indexOf('<div class="preview-scroll', previewPanelStart);
  const header = html.slice(headerStart, headerEnd);
  const previewHead = html.slice(previewPanelStart, previewScrollStart);

  assert.doesNotMatch(html, /class="has-article-tabs"/, '标签栏在正文区内部，不应再扣减整个工作区高度');
  assert.ok(stripStart > previewPanelStart, '文章标签栏必须位于正文预览区内部');
  assert.ok(stripStart < previewScrollStart, '文章标签栏必须贴在正文滚动区上方');
  assert.ok(stripStart > railStart, '文章标签栏不能出现在左侧栏上方');
  assert.doesNotMatch(header, /id="articleTabsHost"/, '全局顶栏不应再承载文章标签');
  assert.match(previewHead, /id="articleTabsHost"/, '正文预览区顶部必须提供文章标签挂载点');
  assert.match(html, /id="articleRefreshBtn"/, '顶栏必须提供当前文章重新排版按钮');
  assert.match(html, /\/js\/article-tabs\.js/, '标签逻辑必须拆到独立模块，避免继续撑大 app.html');
  assert.match(html, /mountArticleTabs\(\{/, '启动时必须挂载文章标签控制器');
});

test('engine snapshots include rerender input and per-article sidebar state', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  assert.match(engine, /last_render_input:\s*clonePlain\(_lastRenderInput\)/, '快照必须保存重新排版所需的原始解析输入');
  assert.match(engine, /article_state:\s*readArticleLocalState\(\)/, '快照必须包含本篇文章的侧栏局部状态');
  assert.match(engine, /more_articles_cards/, '更多文章卡片必须随文章切换隔离');
  assert.match(engine, /bottom_blocks_override/, '底部区块本篇覆盖必须随文章切换隔离');
  assert.match(engine, /applyArticleLocalState\(snap\.article_state\)/, '恢复标签时必须同步本篇侧栏状态');
});

test('new imports create article tabs and completed previews update tab snapshots', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const tabs = await readFile(new URL('../public/js/article-tabs.js', import.meta.url), 'utf8');

  assert.match(engine, /prepareArticleTabForNewUpload/, '新上传或 URL 导入前必须通知标签控制器');
  assert.match(engine, /fbif:article-previewed/, '预览完成后必须发事件让标签保存真实快照');
  assert.match(tabs, /prepareForNewArticle/, '标签模块必须提供新文章入口');
  assert.match(tabs, /role', 'tablist'/, '标签列表必须使用 ARIA tablist 语义');
  assert.match(tabs, /aria-selected/, '活动标签必须暴露 aria-selected');
  assert.match(tabs, /event\.key === 'Delete'/, '标签必须支持键盘删除当前文章');
});
