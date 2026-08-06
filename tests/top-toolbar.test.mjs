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
  assert.match(header, /id="copyBtn"[^>]*aria-label="复制全文"/, `${label} icon-only responsive copy button should keep an accessible name`);
  assert.match(html, /id="copySuccess"[^>]*role="status"[^>]*aria-live="polite"[^>]*aria-atomic="true"/, `${label} copy result should be announced to assistive technology`);
  assert.doesNotMatch(previewPanelTop, /id="previewToolbar"/, `${label} toolbar must not sit inside the article preview panel`);
}

function assertUnifiedImportComposer(html, label) {
  const cardStart = html.indexOf('<div class="import-card">');
  const composerStart = html.indexOf('class="import-composer dropzone upload-zone"', cardStart);
  const inputStart = html.indexOf('class="import-composer-input"', composerStart);
  const actionsStart = html.indexOf('<div class="import-composer-actions">', inputStart);
  const uploadStart = html.indexOf('data-upload-trigger', actionsStart);
  const feishuStart = html.indexOf('data-feishu-doc-trigger', uploadStart);
  // 粘贴即排版（2026-08-06 用户裁定）：动作行末尾是状态文案，不再有提交按钮。
  const capabilityStart = html.indexOf('id="feishuDocCapability"', feishuStart);
  const progressStart = html.indexOf('class="progress import-progress"', composerStart);

  assert.ok(cardStart >= 0, `${label} should keep one import card`);
  assert.ok(composerStart > cardStart && inputStart > composerStart, `${label} should use one unified import composer`);
  assert.ok(actionsStart > inputStart && uploadStart > actionsStart && feishuStart > uploadStart, `${label} upload and Feishu should be addon actions inside the same composer`);
  assert.ok(capabilityStart > feishuStart && progressStart > capabilityStart, `${label} capability status and progress should remain inside the same task`);
  assert.doesNotMatch(html, /id="importSubmitBtn"/, `${label} must not bring back the explicit import button`);
  assert.match(html.slice(actionsStart, capabilityStart), />上传文件<[\s\S]*?>飞书云文档</, `${label} should keep both import action labels expanded`);
}

test('app global header keeps only page actions while the rail owns target and identity', async () => {
  const html = await readShell('app.html');
  const authSession = await readPublic('js/auth-session.js');

  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const previewPanelStart = html.indexOf('<main class="preview-panel');
  const previewScrollStart = html.indexOf('<div class="preview-scroll"', previewPanelStart);
  const header = html.slice(headerStart, headerEnd);
  const previewPanelTop = html.slice(previewPanelStart, previewScrollStart);
  const visibleShell = html.slice(0, html.indexOf('<script type="module">'));

  assert.doesNotMatch(header, /id="userIdentity"/, 'app.html signed-in identity should live in the rail footer, not the top bar');
  assert.doesNotMatch(header, /id="acctMenu"/, 'publishing account should live in the import card, not the top bar');
  assert.doesNotMatch(html, /class="rail-head(?:-title)?"/, 'the rail should not keep a redundant import title container');
  assert.match(html, /role="group" aria-label="导入内容"/, 'the import composer should keep an accessible name without a visible title bar');
  assert.match(html, /<div class="rail-foot">[\s\S]*?id="userIdentity"/, 'app.html signed-in identity should sit in the rail footer');
  assertUnifiedImportComposer(html, 'app.html');
  assert.match(html, /class="import-destination-label">排版到</, 'publishing selector should explain the destination relationship');
  assert.match(header, /id="previewToolbar"/, 'app.html copy toolbar should live in the global top bar');
  assert.match(header, /id="copyBtn"/, 'app.html copy button should be a global top-right action');
  assert.match(header, /id="copyBtn"[^>]*aria-label="复制全文"/, 'app.html icon-only responsive copy button should keep an accessible name');
  assert.match(html, /id="copySuccess"[^>]*role="status"[^>]*aria-live="polite"[^>]*aria-atomic="true"/, 'app.html copy result should be announced to assistive technology');
  assert.match(header, /class="topbar-spacer"[\s\S]*id="previewToolbar"[\s\S]*id="copyBtn"/, 'app.html top bar should right-align the remaining page action');
  assert.doesNotMatch(header, /id="adminLockBtn"|topbar-settings-btn/, 'app.html settings should not be a persistent top-bar icon');
  assert.doesNotMatch(html, /settingsAction|acct-settings-item/, 'app.html should not put settings in the publishing account menu');
  assert.match(authSession, /user-identity-action/, 'rule admin should be rendered as an identity-menu action');
  assert.match(authSession, /id = 'adminLockBtn'/, 'rule admin should keep one delegated opener id');
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
  assert.match(html, /const \{[\s\S]*bindRailDivider[\s\S]*\} = editorShell;/, 'app.html should import the shared rail divider behavior');
  assert.match(html, /bindRailDivider\(\);/, 'app.html should bind finite drag and collapse behavior');
  assert.doesNotMatch(html, /function bindRailDivider\(\)/, 'app.html should not carry a private rail divider implementation');
});

test('site toolbar lives in the global top-right bar, not the article preview panel', async () => {
  const html = await readShell('site.html');
  assertToolbarInGlobalHeader(html, 'site.html');
  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const header = html.slice(headerStart, headerEnd);
  assert.doesNotMatch(header, /id="userIdentity"/, 'site.html signed-in identity should live in the rail footer');
  assert.doesNotMatch(header, /id="acctMenu"/, 'site publishing account should live in the import card');
  assert.doesNotMatch(html, /class="rail-head(?:-title)?"/, 'site.html should not keep a redundant import title container');
  assert.match(html, /role="group" aria-label="导入内容"/, 'site import composer should keep an accessible name without a visible title bar');
  assert.match(html, /<div class="rail-foot">[\s\S]*?id="userIdentity"/, 'site.html signed-in identity should sit in the rail footer');
  assert.match(html, /class="import-destination-label">排版到</, 'site publishing selector should explain the destination relationship');
  assertUnifiedImportComposer(html, 'site.html');
  assert.doesNotMatch(header, /id="adminLockBtn"/, 'site.html rule admin should move into the identity menu');
  assert.match(html, /class="rail-divider"/, 'site.html should render the shared rail divider');
  assert.match(html, /id="railToggleBtn"/, 'site.html rail divider should expose a real collapse control');
  assert.match(html, /const \{[\s\S]*bindRailDivider[\s\S]*\} = editorShell;/, 'site.html should import the shared rail divider behavior');
  assert.match(html, /bindRailDivider\(\);/, 'site.html should bind finite drag and collapse behavior');
});

test('app rail divider stays visually slim and the handle behaves like a clickable control', async () => {
  const css = await readPublic('css/app-shell.css');
  const channelCss = await readPublic('css/channel-bar.css');
  const authCss = await readPublic('css/auth-editor.css');
  const shellJs = await readPublic('js/editor-shell.js');
  assert.match(css, /\.rail-divider\s*\{[\s\S]*?width:\s*4px;/, 'rail divider should stay slim');
  assert.match(css, /\.rail-divider-toggle\s*\{[\s\S]*?width:\s*22px;[\s\S]*?height:\s*38px;[\s\S]*?backdrop-filter:\s*blur\(8px\);[\s\S]*?cursor:\s*pointer;/, 'rail handle should be compact, softly blurred, and clickable');
  assert.match(css, /\.rail-divider-toggle:hover\s*\{[\s\S]*?--rail-toggle-scale:\s*1\.03;/, 'rail handle should use restrained hover motion');
  assert.match(shellJs, /export function bindRailDivider\(/, 'rail divider behavior should live in the shared shell helper');
  assert.match(authCss, /\.rail-foot > \.user-identity\s*\{[\s\S]*?width:\s*100%;/, 'signed-in identity should fill the rail footer');
  assert.match(css, /\.rail-foot\s*\{[\s\S]*?padding:\s*0;[\s\S]*?align-items:\s*stretch;/, 'the login footer should attach directly to its divider without an inset gap');
  assert.match(authCss, /\.rail-foot \.user-identity-trigger\s*\{[\s\S]*?min-height:\s*56px;[\s\S]*?padding:\s*0 12px;[\s\S]*?border-radius:\s*0;/, 'the login row should fill the footer surface edge to edge');
  assert.match(authCss, /\.rail-foot \.user-identity-menu\s*\{[\s\S]*?bottom:\s*calc\(100% \+ 8px\);/, 'the rail-footer identity menu must open upward, not off-screen');
  assert.match(css, /\.import-account-target \.acct-trigger\s*\{[\s\S]*?height:\s*26px;/, 'publishing account should be a compact chip inside the import card');
  assert.doesNotMatch(css, /\.rail-head(?:-title)?\s*\{/, 'the removed title container should not leave dead shell styles');
  assert.match(css, /\.rail-body\s*\{[\s\S]*?background:\s*var\(--fs-bg-nested\);/, 'the rail work area should be the nested surface the import card floats on');
  assert.match(css, /\.dropzone\s*\{[\s\S]*?min-height:\s*198px;[\s\S]*?background:\s*var\(--fs-bg-card\);[\s\S]*?border:\s*1px solid var\(--fs-border-card\);[\s\S]*?border-radius:\s*var\(--fs-r-l\);/, 'text, upload and Feishu should share one established import frame');
  assert.match(css, /\.import-composer-shell\s*\{[\s\S]*?container-type:\s*inline-size;/, 'the import footer should respond to the actual rail width instead of the full viewport');
  assert.match(css, /\.import-composer-addon\s*\{[\s\S]*?display:\s*flex;[\s\S]*?flex-wrap:\s*nowrap;[\s\S]*?align-items:\s*center;/, 'upload and Feishu should keep one stable toolbar baseline');
  assert.doesNotMatch(css, /@container \(max-width:\s*440px\)\s*\{[\s\S]*?\.import-action-label\s*\{[\s\S]*?display:\s*none;/, 'narrow rails must not collapse upload or Feishu labels');
  assert.match(css, /\.import-destination\s*\{[\s\S]*?display:\s*flex;[\s\S]*?align-items:\s*center;/, 'publishing selector should be the first row of the import card');
  assert.match(css, /\.import-account-target \.acct-trigger-name\s*\{[\s\S]*?text-overflow:\s*ellipsis;/, 'a long account name must ellipsize inside the card instead of pushing the caret out');
  assert.doesNotMatch(css, /topbar-publishing-destination|topbar-account-target|topbar-publishing-label/, 'top-bar publishing styles should not survive the move into the import card');
  assert.doesNotMatch(css, /\.import-composer:has\([^}]+max-width:\s*(?:102|44)px;/, 'the submit action must not force the publishing account into an ellipsis');
  assert.match(css, /\.import-feishu-trigger\.is-restricted:hover\s*\{[\s\S]*?background:\s*var\(--fs-bg-hover\);[\s\S]*?color:\s*var\(--fs-text-1\);/, 'password users should still see Feishu as an actionable entry that opens the login explanation');
  assert.match(css, /\.dropzone\.dragover \.import-composer-input[\s\S]*?\.dropzone\.dragover \.dropzone-drag-prompt\s*\{[\s\S]*?display:\s*flex;/, 'file drag should replace the whole composer with one drop state');
  assert.doesNotMatch(css, /preview-toolbar-strip|app-brand|tb-icon-action|article-refresh-spin/, 'old preview action strip and refresh styles should not be restored');
  assert.doesNotMatch(css, /article-tabs|article-tab/, 'article tab styles should not be restored');
  assert.doesNotMatch(css, /topbar-settings-btn|topbar-settings-tooltip/, 'app shell should not keep dead top-bar settings styles');
  assert.doesNotMatch(channelCss, /acct-settings|acct-menu-divider/, 'publishing account menu should only switch publishing accounts');
  assert.match(authCss, /\.user-identity-menu\s*\{[\s\S]*?font-family:\s*var\(--fs-font\)/, 'identity menu should use the project typography token');
  assert.match(authCss, /\.user-identity-status\s*\{[\s\S]*?background:\s*var\(--fs-success-soft\)[\s\S]*?color:\s*var\(--fs-success\)/, 'identity status should use semantic tokens');
  assert.match(authCss, /\.rail-foot \.user-identity-menu\s*\{[\s\S]*?bottom:\s*calc\(100% \+ 8px\);[\s\S]*?width:\s*min\(340px, calc\(100% - 24px\), calc\(100vw - 24px\)\)/, 'identity menu should stay inside the rail and viewport');
  assert.doesNotMatch(css, /\.sidebar-footer\s+\.active-rule-card/, 'left-bottom rule card styles should not be restored');
});
