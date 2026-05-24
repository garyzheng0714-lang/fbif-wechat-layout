import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('app shell renders a capsule account switcher above the upload area', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/app.css', import.meta.url), 'utf8');

  assert.match(app, /id="accountSwitcher"/);
  assert.match(app, /class="account-switcher"/);
  assert.match(app, /listRulePacks/);
  assert.match(app, /switchAccount/);
  assert.match(app, /aria-pressed/);
  assert.match(app, /loadActiveRulePreset\(activeRulePack\.id\)/);
  assert.match(app, /saveLocalRuleConfig\(initialRule\.preset\.config,\s*activeRulePack\.id\)/);
  assert.match(css, /\.account-switcher/);
  assert.match(css, /\.account-switcher-btn\.active/);
});
