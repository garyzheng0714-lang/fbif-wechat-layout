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

test('rule admin lets JSON explicitly clear a saved description', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');

  assert.match(adminPanel, /workingDescription = parsed\.description;/);
  assert.match(adminPanel, /descriptionInput\.value = parsed\.description;/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description\) workingDescription/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description && descriptionInput\)/);
});
