import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

async function readProjectFile(path) {
  return readFile(new URL(`../${path}`, import.meta.url), 'utf8');
}

test('rule admin exposes JSON editing and new-rule controls', async () => {
  const adminPanel = await readProjectFile('public/js/admin-panel.js');

  assert.match(adminPanel, /parseRuleConfigDocument/);
  assert.match(adminPanel, /buildRuleConfigDocument/);
  assert.match(adminPanel, /data-role="rule-json"/);
  assert.match(adminPanel, /data-action="validate-json"/);
  assert.match(adminPanel, /data-action="apply-json"/);
  assert.match(adminPanel, /data-action="new"/);
  assert.match(adminPanel, /syncDirtyJSONBeforePersist/);
  assert.match(adminPanel, /syncDirtyJSONBeforePersist\(\);[\s\S]*saveRulePreset/);
  assert.match(adminPanel, /应用到当前公众号/);
  assert.match(adminPanel, /复制为新规则/);
  assert.match(adminPanel, /保存修改/);
  assert.doesNotMatch(adminPanel, />设为当前规则</);
  assert.doesNotMatch(adminPanel, />另存为规则</);
  assert.match(adminPanel, /createMoreArticlesRuleEditor/);
  assert.match(adminPanel, /data-role="more-articles-rule-editor"/);
  assert.match(adminPanel, /syncJSONEditorFromInputs/);
});

test('rule admin save-as generates a unique id instead of overwriting same-name rules', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const { uniqueClientRuleID } = await import('../public/js/admin-panel.js?unique=' + Date.now());

  assert.equal(uniqueClientRuleID('foodtalks series default', {
    presets: [{ id: 'foodtalks-series-default' }],
  }), 'foodtalks-series-default-2');
  assert.equal(uniqueClientRuleID('foodtalks series default', {
    rules: { 'foodtalks-series-default': { id: 'foodtalks-series-default' } },
  }), 'foodtalks-series-default-2');
  assert.match(adminPanel, /function ruleListFromDoc\(doc\)/);
  assert.match(adminPanel, /Array\.isArray\(doc\.presets\)/);
  assert.match(adminPanel, /while \(used\.has\(base \+ '-' \+ suffix\)\) suffix\+\+;/);
  assert.match(adminPanel, /const id = uniqueClientRuleID\(name, doc\);/);
  assert.doesNotMatch(adminPanel, /data-action="save-as"[\s\S]*const id = clientRuleID\(name\);/);
});

test('rule admin lets JSON explicitly clear a saved description', async () => {
  const adminPanel = await readProjectFile('public/js/admin-panel.js');

  assert.match(adminPanel, /workingDescription = parsed\.description;/);
  assert.match(adminPanel, /descriptionInput\.value = parsed\.description;/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description\) workingDescription/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description && descriptionInput\)/);
});

test('rule admin has configuration and rule-management views', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  assert.match(app, /rulePacks:\s*rulePackModule\.getRulePacksByChannel\(['"]mp['"]\)/);
  assert.match(adminPanel, /activeView = 'assignments'/);
  assert.match(adminPanel, /data-view="assignments"/);
  assert.match(adminPanel, /data-view="rules"/);
  assert.match(adminPanel, /规则配置/);
  assert.match(adminPanel, /已应用到：/);
  assert.match(adminPanel, /未应用/);
  assert.match(adminPanel, /renderApplicationBadges/);
  assert.match(adminPanel, /setActiveRulePreset\(password, ruleID, accountID\)/);
  assert.match(adminPanel, /仅在预览中查看，未改变公众号应用规则/);
  assert.match(css, /\.admin-layout/);
  assert.match(css, /\.admin-side-nav/);
  assert.match(css, /\.admin-applied-badges/);
});

test('rule admin deletes from the rule row with explicit confirmation', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  assert.match(adminPanel, /class="admin-icon-danger"/);
  assert.match(adminPanel, /aria-label="删除当前规则"/);
  assert.match(adminPanel, /window\.confirm/);
  assert.match(adminPanel, /删除后无法恢复，正在使用它的公众号会回到默认规则/);
  assert.doesNotMatch(adminPanel, /class="admin-secondary danger" data-action="delete"/);
  assert.match(css, /\.admin-preset-control/);
  assert.match(css, /\.admin-icon-danger/);
});

test('rule admin exposes behavior-code editing and validation preview', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  assert.match(adminPanel, /validateRuleBehavior/);
  assert.match(adminPanel, /validateBehaviorDraft/);
  assert.match(adminPanel, /behaviorDirty/);
  assert.match(adminPanel, /renderBehaviorTemplate/);
  assert.match(adminPanel, /\['behavior', '代码化规则'\]/);
  assert.match(adminPanel, /data-rule-tab="\$\{id\}"/);
  assert.match(adminPanel, /代码化规则/);
  assert.match(adminPanel, /data-role="behavior-json"/);
  assert.match(adminPanel, /data-action="validate-behavior"/);
  assert.match(adminPanel, /data-action="apply-behavior"/);
  assert.match(adminPanel, /\['validation', '校验预览'\]/);
  assert.match(adminPanel, /校验预览/);
  assert.match(adminPanel, /syncDirtyBehaviorBeforePersist\(\);[\s\S]*saveRulePreset/);
  assert.match(adminPanel, /behavior:\s*workingBehavior/);
  assert.match(css, /\.admin-rule-tabs/);
  assert.match(css, /\.admin-preview-grid/);
});

test('rule admin card editor has dedicated layout styles', async () => {
  const appHTML = await readProjectFile('public/app.html');
  const appCSS = await readProjectFile('public/app.css');
  const ruleAdminCSS = await readProjectFile('public/css/rule-admin.css');

  assert.match(appHTML, /<link rel="stylesheet" href="\/css\/rule-admin\.css">/);
  assert.match(appHTML, /href="\/app\.css"[\s\S]*href="\/css\/rule-admin\.css"[\s\S]*href="\/css\/wx-theme\.css"/);
  assert.match(ruleAdminCSS, /\.ma-rule-field\s+input\[type="url"\]/);
  assert.match(ruleAdminCSS, /\.ma-rule-field\s+textarea:focus/);
  assert.match(ruleAdminCSS, /@media[\s\S]*\.ma-rule-field\s*\{[^}]*grid-template-columns:\s*1fr;/);
  assert.match(ruleAdminCSS, /\.ma-rule-card\s+\.admin-json-actions\s*\{[^}]*flex-wrap:\s*wrap;/);
  assert.doesNotMatch(appCSS, /\.ma-rule-/);
  assert.ok(appCSS.split('\n').length <= 900);
});
