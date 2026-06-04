import test from 'node:test';
import assert from 'node:assert/strict';
import { access, readFile } from 'node:fs/promises';

test('app shell stays single-article and does not render article tabs', async () => {
  const html = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/css/app-shell.css', import.meta.url), 'utf8');
  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const header = html.slice(headerStart, headerEnd);
  const previewPanelStart = html.indexOf('<main class="preview-panel"');
  const previewScrollStart = html.indexOf('<div class="preview-scroll', previewPanelStart);
  const previewHead = html.slice(previewPanelStart, previewScrollStart);

  assert.match(header, /id="previewToolbar"[\s\S]*id="copyBtn"/, '复制按钮应位于全局顶栏右上角');
  assert.match(header, /id="adminLockBtn"[\s\S]*id="copyBtn"/, '设置按钮应位于复制全文左侧');
  assert.doesNotMatch(header, /id="articleRefreshBtn"/, '顶栏不应显示刷新按钮');
  assert.doesNotMatch(previewHead, /id="previewToolbar"|id="copyBtn"|id="articleRefreshBtn"|preview-toolbar-strip/, '正文预览区顶部不应再放操作条');
  assert.doesNotMatch(html, /articleTabsHost|article-tabs-strip|article-tab-|mountArticleTabs|\/js\/article-tabs\.js/, 'app.html 不应再挂载文章标签页');
  assert.doesNotMatch(css, /article-tabs|article-tab/, '外壳样式不应保留文章标签页选择器');
  assert.doesNotMatch(css, /preview-toolbar-strip|tb-icon-action|article-refresh-spin/, '外壳样式不应保留刷新按钮或预览操作条');
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
  assert.doesNotMatch(app, /articleRefreshButton|id="articleRefreshBtn"/, '刷新按钮不应再出现在单篇模式外壳');
});
