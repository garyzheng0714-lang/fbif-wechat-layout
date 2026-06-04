import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

async function readShell(fileName) {
  return readFile(new URL(`../public/${fileName}`, import.meta.url), 'utf8');
}

async function readPublic(fileName) {
  return readFile(new URL(`../public/${fileName}`, import.meta.url), 'utf8');
}

function assertToolbarInGlobalHeader(html, label) {
  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const appLayoutStart = html.indexOf('<div class="app-layout">');
  const previewPanelStart = html.indexOf('<main class="preview-panel');
  const previewPanelEnd = html.indexOf('<div class="preview-scroll"', previewPanelStart);
  const header = html.slice(headerStart, headerEnd);
  const previewPanelTop = html.slice(previewPanelStart, previewPanelEnd);

  assert.ok(headerStart >= 0, `${label} should have the global channel bar`);
  assert.ok(headerEnd > headerStart, `${label} should close the global channel bar before the app layout`);
  assert.ok(headerEnd < appLayoutStart, `${label} header should sit above the app layout`);
  assert.match(header, /id="previewToolbar"/, `${label} toolbar should live in the global top bar`);
  assert.match(header, /id="copyBtn"/, `${label} copy button should live in the top bar`);
  assert.doesNotMatch(previewPanelTop, /id="previewToolbar"/, `${label} toolbar must not sit inside the article preview panel`);
}

test('app global header keeps settings inside the account menu and copy on the right', async () => {
  const html = await readShell('app.html');

  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const previewPanelStart = html.indexOf('<main class="preview-panel');
  const previewScrollStart = html.indexOf('<div class="preview-scroll"', previewPanelStart);
  const header = html.slice(headerStart, headerEnd);
  const previewPanelTop = html.slice(previewPanelStart, previewScrollStart);
  const visibleShell = html.slice(0, html.indexOf('<script type="module">'));

  assert.match(header, /id="acctMenu"/, 'app.html account menu should live in the top-left bar');
  assert.match(header, /id="previewToolbar"/, 'app.html copy toolbar should live in the global top bar');
  assert.match(header, /id="copyBtn"/, 'app.html copy button should be a global top-right action');
  assert.match(header, /id="acctMenu"[\s\S]*class="topbar-spacer"[\s\S]*id="previewToolbar"[\s\S]*id="copyBtn"/, 'app.html copy should stay on the right');
  assert.doesNotMatch(header, /id="adminLockBtn"|topbar-settings-btn/, 'app.html settings should not be a persistent top-bar icon');
  assert.match(html, /settingsAction:\s*\{[\s\S]*id:\s*'adminLockBtn'[\s\S]*label:\s*'规则设置'/, 'app.html settings should be registered as an account-menu footer action');
  assert.doesNotMatch(header, /app-brand|app-brand-logo|FBIF文章排版工具/, 'app.html should not show product branding in the top bar');
  assert.doesNotMatch(header, /id="articleRefreshBtn"/, 'app.html should not show a refresh button in the top bar');
  assert.doesNotMatch(previewPanelTop, /id="previewToolbar"|id="copyBtn"|id="articleRefreshBtn"|preview-toolbar-strip/, 'app.html preview panel should not own the top actions');
  assert.doesNotMatch(previewPanelTop, /articleTabsHost|article-tabs-strip|article-tab-/, 'app.html preview panel should not render article tabs');
  assert.doesNotMatch(visibleShell, /class="sidebar-footer"/, 'app.html should not show a fixed left-bottom footer');
  assert.doesNotMatch(visibleShell, /id="activeRuleCard"/, 'app.html should not show the current-rule card at the left bottom');
  assert.doesNotMatch(visibleShell, /当前排版规则/, 'app.html should not show bottom current-rule text');
  assert.doesNotMatch(visibleShell, /微信公众号后台/, 'app.html should remove the WeChat backend footer link');
  assert.doesNotMatch(html, /已锁定/, 'app.html should not show a locked badge in the fixed rule status');
  assert.doesNotMatch(html, /data-action="edit-active-rule"/, 'app.html should not expose front-end rule editing in the fixed footer');
  assert.match(html, /class="rail-divider"/, 'app.html should render the WeChat-style rail divider');
  assert.match(html, /id="railToggleBtn"/, 'app.html rail divider should expose a real collapse control');
  assert.match(html, /function bindRailDivider\(\)/, 'app.html should bind finite drag and collapse behavior');
  assert.match(html, /startedOnButton = button\.contains\(event\.target\)/, 'app.html rail handle should support click and drag without conflicting');
});

test('site toolbar lives in the global top-right bar, not the article preview panel', async () => {
  const html = await readShell('site.html');
  assertToolbarInGlobalHeader(html, 'site.html');
  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const header = html.slice(headerStart, headerEnd);
  assert.match(header, /id="adminLockBtn"/, 'site.html rule admin button should remain in the top bar');
});

test('app rail divider stays visually slim and the handle behaves like a clickable control', async () => {
  const css = await readPublic('css/app-shell.css');
  const channelCss = await readPublic('css/channel-bar.css');
  assert.match(css, /\.rail-divider\s*\{[\s\S]*?width:\s*4px;/, 'rail divider should stay slim');
  assert.match(css, /\.rail-divider-toggle\s*\{[\s\S]*?width:\s*24px;[\s\S]*?height:\s*50px;[\s\S]*?cursor:\s*pointer;/, 'rail handle should be smaller and use pointer feedback');
  assert.match(css, /\.rail-divider-toggle:hover\s*\{[\s\S]*?--rail-toggle-scale:\s*1\.06;/, 'rail handle should animate on hover');
  assert.match(css, /\.global-channel-bar > \.acct-menu\s*\{[\s\S]*?flex:\s*0 1 auto;/, 'account switcher should be anchored in the top-left bar');
  assert.doesNotMatch(css, /preview-toolbar-strip|app-brand|tb-icon-action|article-refresh-spin/, 'old preview action strip and refresh styles should not be restored');
  assert.doesNotMatch(css, /article-tabs|article-tab/, 'article tab styles should not be restored');
  assert.doesNotMatch(css, /topbar-settings-btn|topbar-settings-tooltip/, 'app shell should not keep dead top-bar settings styles');
  assert.match(channelCss, /\.acct-menu-divider\s*\{[\s\S]*?background:\s*var\(--fs-divider/, 'account menu settings should be separated from account choices');
  assert.match(channelCss, /\.acct-settings-item\s*\{[\s\S]*?color:\s*var\(--fs-text-2/, 'account menu settings should be a muted menu action');
  assert.doesNotMatch(css, /\.sidebar-footer\s+\.active-rule-card/, 'left-bottom rule card styles should not be restored');
});
