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
  assert.match(adminPanel, /createMoreArticlesRuleEditor/);
  assert.match(adminPanel, /data-role="more-articles-rule-editor"/);
  assert.match(adminPanel, /syncJSONEditorFromInputs/);
});

test('rule admin lets JSON explicitly clear a saved description', async () => {
  const adminPanel = await readProjectFile('public/js/admin-panel.js');

  assert.match(adminPanel, /workingDescription = parsed\.description;/);
  assert.match(adminPanel, /descriptionInput\.value = parsed\.description;/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description\) workingDescription/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description && descriptionInput\)/);
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
