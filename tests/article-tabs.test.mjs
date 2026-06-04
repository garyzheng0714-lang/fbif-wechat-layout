import test from 'node:test';
import assert from 'node:assert/strict';
import { access, readFile } from 'node:fs/promises';

test('app shell stays single-article and does not render article tabs', async () => {
  const html = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/css/app-shell.css', import.meta.url), 'utf8');
  const appLayoutStart = html.indexOf('<div class="app-layout">');
  const railStart = html.indexOf('<aside class="rail sidebar"', appLayoutStart);
  const previewPanelStart = html.indexOf('<main class="preview-panel"', appLayoutStart);
  const toolbarStart = html.indexOf('<div class="preview-toolbar-strip"', previewPanelStart);
  const previewScrollStart = html.indexOf('<div class="preview-scroll', previewPanelStart);
  const previewHead = html.slice(previewPanelStart, previewScrollStart);

  assert.ok(toolbarStart > previewPanelStart, '文章操作条应位于正文预览区内部');
  assert.ok(toolbarStart < previewScrollStart, '文章操作条应贴在正文滚动区上方');
  assert.ok(toolbarStart > railStart, '文章操作条不能出现在左侧栏上方');
  assert.match(previewHead, /id="previewToolbar"/, '正文区顶部仍保留复制和刷新操作');
  assert.match(previewHead, /id="copyBtn"/, '复制按钮仍位于正文渲染区右上角');
  assert.match(previewHead, /id="articleRefreshBtn"/, '刷新按钮仍位于正文渲染区右上角');
  assert.doesNotMatch(html, /articleTabsHost|article-tabs-strip|article-tab-|mountArticleTabs|\/js\/article-tabs\.js/, 'app.html 不应再挂载文章标签页');
  assert.doesNotMatch(css, /article-tabs|article-tab/, '外壳样式不应保留文章标签页选择器');
  assert.match(css, /\.preview-toolbar-strip\s*\{/, '外壳样式应使用普通预览操作条');
});

test('article-tab state module is removed instead of hidden', async () => {
  await assert.rejects(
    access(new URL('../public/js/article-tabs.js', import.meta.url)),
    /ENOENT/,
    '标签页模块应被删除，避免后续误恢复多文章状态',
  );
});

test('engine remains single-article while keeping rerender snapshots', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  assert.match(engine, /last_render_input:\s*clonePlain\(_lastRenderInput\)/, '单篇模式仍需保存重排输入，供刷新和切规则重排使用');
  assert.doesNotMatch(engine, /prepareArticleTabForNewUpload|_articleTabs|article_state|readArticleLocalState|applyArticleLocalState/, '引擎不应再保存或恢复多标签文章状态');
  assert.doesNotMatch(app, /captureArticleTab|_articleTabs/, '账号切换不应再反向捕获标签页状态');
  assert.match(app, /articleRefreshButton\.addEventListener\('click'/, '刷新按钮应直接触发当前单篇重排');
  assert.match(app, /fbif:article-previewed/, '上传完成后应启用当前单篇刷新按钮');
  assert.match(app, /fbif:article-cleared/, '清空内容后应禁用当前单篇刷新按钮');
});
