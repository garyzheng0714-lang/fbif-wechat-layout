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

test('app copy toolbar stays in the global top-right bar while rule admin moves to the sidebar footer', async () => {
  const html = await readShell('app.html');
  assertToolbarInGlobalHeader(html, 'app.html');

  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const footerStart = html.indexOf('<div class="sidebar-footer">');
  const footerEnd = html.indexOf('</aside>', footerStart);
  const header = html.slice(headerStart, headerEnd);
  const footer = html.slice(footerStart, footerEnd);

  assert.doesNotMatch(header, /id="adminLockBtn"/, 'app.html rule admin button should not stay in the top bar');
  assert.match(footer, /id="adminLockBtn"/, 'app.html rule admin icon should live in the sidebar footer');
  assert.match(footer, /id="activeRuleCard"/, 'app.html active rule status should live in the fixed sidebar footer');
  assert.match(footer, /当前排版规则/, 'app.html active rule status should name the applied typography rule clearly');
  assert.match(footer, /class="rail-icon-btn admin-lock-btn"/, 'app.html sidebar rule admin entry should be icon-only');
  assert.match(footer, /class="rail-icon-tooltip"[^>]*>规则后台</, 'app.html sidebar rule admin entry should reveal a text tooltip');
  assert.doesNotMatch(footer, /微信公众号后台/, 'app.html should remove the WeChat backend footer link');
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
  assert.match(css, /\.rail-divider\s*\{[\s\S]*?width:\s*4px;/, 'rail divider should stay slim');
  assert.match(css, /\.rail-divider-toggle\s*\{[\s\S]*?width:\s*24px;[\s\S]*?height:\s*50px;[\s\S]*?cursor:\s*pointer;/, 'rail handle should be smaller and use pointer feedback');
  assert.match(css, /\.rail-divider-toggle:hover\s*\{[\s\S]*?--rail-toggle-scale:\s*1\.06;/, 'rail handle should animate on hover');
  assert.match(css, /\.sidebar-footer\s+\.active-rule-card/, 'active rule status should be styled as part of the fixed sidebar footer');
  assert.match(css, /\.sidebar-footer\s+\.active-rule-readmeta\s*\{[\s\S]*?background:\s*var\(--fs-bg-nested\);[\s\S]*?box-shadow:\s*inset 0 0 0 1px var\(--fs-divider\);/, 'active rule status should read as a designed compact capsule');
});
