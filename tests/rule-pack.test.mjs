import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  DEFAULT_RULE_PACK_ID,
  getRulePack,
  getRulePackIDFromLocation,
} from '../public/js/rule-packs/registry.js';

test('rule pack registry keeps FBIF as the default account pack', () => {
  const pack = getRulePack();
  assert.equal(DEFAULT_RULE_PACK_ID, 'fbif');
  assert.equal(pack.id, 'fbif');
  assert.equal(pack.templateId, 'fbif');
  assert.equal(pack.accountName, 'FBIF食品饮料创新');
  assert.equal(pack.footerUrl, '/footer.html');
});

test('rule pack can be selected by account query without adding visible template switching', () => {
  assert.equal(getRulePackIDFromLocation({ search: '?account=fbif' }), 'fbif');
  assert.equal(getRulePackIDFromLocation({ search: '?rule_pack=fbif' }), 'fbif');
  assert.equal(getRulePack('unknown').id, 'fbif');
});

test('app and engine read account chrome and footer from the rule pack registry', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  assert.match(app, /rule-packs\/registry\.js/);
  assert.match(app, /window\._activeRulePack = activeRulePack/);
  assert.match(app, /applyRulePackChrome\(activeRulePack\)/);
  assert.match(engine, /activeRulePack\.footerUrl/);
});
