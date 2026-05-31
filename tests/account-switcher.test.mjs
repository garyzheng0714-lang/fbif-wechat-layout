import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('app shell renders a capsule account switcher above the upload area', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/app.css', import.meta.url), 'utf8');
  // .account-switcher / .account-switcher-btn styles moved to channel-bar.css
  // alongside the top channel pill (both are "switcher controls"). Tests below
  // that reference `css` now check the merged stylesheet.
  const channelBarCss = await readFile(new URL('../public/css/channel-bar.css', import.meta.url), 'utf8');
  const allCss = css + '\n' + channelBarCss;

  assert.match(app, /id="accountSwitcher"/);
  assert.match(app, /class="account-switcher"/);
  assert.match(app, /getRulePacksByChannel\(['"]mp['"]\)/);
  assert.match(app, /switchAccount/);
  assert.match(app, /aria-pressed/);
  assert.match(app, /loadActiveRulePreset\(activeRulePack\.id\)/);
  assert.match(app, /saveLocalRuleConfig\(initialRule\.preset\.config,\s*activeRulePack\.id\)/);
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
  assert.match(allCss, /\.account-switcher/);
  assert.match(allCss, /\.account-switcher-btn\.active/);
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
