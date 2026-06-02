import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('app shell renders a unified top-bar account menu (single layer)', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/app.css', import.meta.url), 'utf8');
  const channelBarCss = await readFile(new URL('../public/css/channel-bar.css', import.meta.url), 'utf8');
  const menuJs = await readFile(new URL('../public/js/account-menu.js', import.meta.url), 'utf8');
  const allCss = css + '\n' + channelBarCss;

  // 单层账号菜单（2026-06-02 用户裁定「不要两层」）：顶栏头像菜单取代旧的
  // 侧栏方角分段 + 顶部圆形渠道胶囊。渲染逻辑在 account-menu.js，挂载点 #acctMenu。
  assert.match(app, /id="acctMenu"/);
  assert.match(app, /mountAccountMenu/);
  assert.match(app, /account-menu\.js/);
  // 旧的两层切换器残骸必须清掉
  assert.doesNotMatch(app, /id="accountSwitcher"/, '旧侧栏方角分段已移除');
  assert.doesNotMatch(app, /class="channel-switcher"/, '旧顶部圆形渠道胶囊已移除');
  // account-menu.js 契约：按渠道分组、列出全部账号、标记当前账号
  assert.match(menuJs, /listRulePacks/);
  assert.match(menuJs, /CHANNEL_LABEL/);
  assert.match(menuJs, /is-active/);
  assert.match(menuJs, /setActive/);
  assert.match(app, /switchAccount/);
  assert.match(app, /loadActiveRulePreset\(activeRulePack\.id\)/);
  assert.match(app, /hydrateActiveRulePreset\(activeRulePack\.id\)/);
  assert.match(app, /cacheServerRuleFallback\(loaded\.preset\.config\)/);
  assert.match(app, /id="activeRuleCard"/);
  assert.match(app, /renderActiveRuleCard/);
  assert.match(app, /id="activeRuleSelect"/);
  assert.match(app, /data-action="edit-active-rule"/);
  assert.match(app, /data-action="save-active-rule"/);
  assert.match(app, /data-action="cancel-active-rule"/);
  assert.match(app, /cancelActiveRuleEdit/);
  assert.match(app, /activeRuleSelectorOpen/);
  assert.match(app, /data-rule-option/);
  assert.doesNotMatch(app, /<select id="activeRuleSelect"/);
  assert.match(app, /listPublicRulePresets/);
  assert.match(app, /setActiveRulePreset\(password,\s*nextID,\s*activeRulePack\.id\)/);
  assert.match(app, /当前规则/);
  assert.ok(
    app.indexOf('id="activeRuleCard"') > app.indexOf('id="moreArticlesSection"') &&
      app.indexOf('id="activeRuleCard"') < app.indexOf('class="sidebar-footer"'),
    'current rule selector should sit below the more-articles section as secondary metadata',
  );
  assert.doesNotMatch(app, /id="settingsEditBtn"/);
  assert.doesNotMatch(app, /id="settingsForm"/);
  assert.match(allCss, /\.acct-trigger/);
  assert.match(allCss, /\.acct-panel/);
  assert.match(allCss, /\.acct-item\.is-active/);
  assert.match(css, /\.active-rule-card/);
  assert.match(css, /\.active-rule-card\.editing/);
  assert.match(css, /\.active-rule-readline/);
  assert.match(css, /\.active-rule-readvalue/);
  assert.match(css, /\.active-rule-select-wrap/);
  assert.match(css, /\.active-rule-trigger:disabled/);
  assert.match(css, /\.active-rule-menu/);
  assert.match(css, /\.active-rule-option/);
  assert.match(css, /\.active-rule-icon/);
  assert.match(css, /\.active-rule-save/);
  assert.match(css, /\.active-rule-cancel/);
});

test('app shell prefers pathname over query for initial rule pack selection', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const editorShell = await readFile(new URL('../public/js/editor-shell.js', import.meta.url), 'utf8');
  // 必须 import 新的 path 解析器（绝对路径）
  assert.match(app, /import\(['"]\/js\/rule-packs\/url\.js/);
  // 入口页只负责接线；路径优先和旧 ?account 兼容统一由 editor-shell 承担。
  assert.match(app, /resolveInitialRulePackID/);
  assert.match(editorShell, /parsePath\s*\(\s*loc\.pathname/);
  assert.match(editorShell, /getRulePackIDFromLocation/);
});

test('app.html writes path-based URL when switching rule packs', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  // 必须用 buildPath 构造新 URL
  assert.match(app, /buildPath\s*\(/);
  // rule-packs/url.js 已 import（Task 5 引入，Task 6 复用）
  assert.match(app, /rule-packs\/url\.js/);
  // 切换分支必须先判断 parsePath !== null
  assert.match(app, /rulePackUrlModule\.parsePath\(\s*location\.pathname\s*\)/);
});

test('app shell uses absolute /js/ paths for dynamic imports (path-safe under /mp/...)', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  // 不允许任何相对路径的 dynamic import（会在 /mp/fbif 下被解析为 /mp/js/...）
  const matches = app.match(/import\(['"]\.\//g);
  assert.equal(matches, null, 'app.html should not contain relative "./..." dynamic imports');
  // 至少要有几个绝对路径 /js/ import 在
  assert.match(app, /import\(['"]\/js\/engine\.js/);
  assert.match(app, /import\(['"]\/js\/rule-packs\/registry\.js/);
});
