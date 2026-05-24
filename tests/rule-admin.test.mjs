import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('rule admin exposes JSON editing and new-rule controls', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');

  assert.match(adminPanel, /parseRuleConfigDocument/);
  assert.match(adminPanel, /buildRuleConfigDocument/);
  assert.match(adminPanel, /data-role="rule-json"/);
  assert.match(adminPanel, /data-action="validate-json"/);
  assert.match(adminPanel, /data-action="apply-json"/);
  assert.match(adminPanel, /data-action="new"/);
  assert.match(adminPanel, /syncDirtyJSONBeforePersist/);
  assert.match(adminPanel, /syncDirtyJSONBeforePersist\(\);[\s\S]*saveRulePreset/);
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
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');

  assert.match(adminPanel, /workingDescription = parsed\.description;/);
  assert.match(adminPanel, /descriptionInput\.value = parsed\.description;/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description\) workingDescription/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description && descriptionInput\)/);
});

test('rule admin has configuration and rule-management views', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  assert.match(app, /rulePacks:\s*rulePackModule\.listRulePacks\(\)/);
  assert.match(adminPanel, /activeView = 'assignments'/);
  assert.match(adminPanel, /data-view="assignments"/);
  assert.match(adminPanel, /data-view="rules"/);
  assert.match(adminPanel, /规则配置/);
  assert.match(adminPanel, /已应用到：/);
  assert.match(adminPanel, /未应用/);
  assert.match(adminPanel, /renderApplicationBadges/);
  assert.match(adminPanel, /setActiveRulePreset\(password, ruleID, accountID\)/);
  assert.match(css, /\.admin-layout/);
  assert.match(css, /\.admin-side-nav/);
  assert.match(css, /\.admin-applied-badges/);
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
