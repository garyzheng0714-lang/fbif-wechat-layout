import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  DEFAULT_RULE_PACK_ID,
  getRulePack,
  getRulePackIDFromLocation,
  listRulePacks,
} from '../public/js/rule-packs/registry.js';

test('rule pack registry keeps FBIF as the default account pack', () => {
  const pack = getRulePack();
  assert.equal(DEFAULT_RULE_PACK_ID, 'fbif');
  assert.equal(pack.id, 'fbif');
  assert.equal(pack.templateId, 'fbif');
  assert.equal(pack.accountName, 'FBIF公众号');
  assert.equal(pack.previewAccountName, 'FBIF食品饮料创新');
  assert.equal(pack.behaviorAccountName, 'FBIF');
  assert.equal(pack.footerUrl, '/footer.html');
  assert.equal(pack.channel, 'mp');
  assert.equal(pack.location, '上海');
});

test('rule pack registry exposes FoodTalks what-is series as a selectable account', () => {
  const packs = listRulePacks();
  assert.deepEqual(packs.map(pack => pack.id), ['fbif', 'foodtalks-series']);

  const pack = getRulePack('foodtalks-series');
  assert.equal(pack.id, 'foodtalks-series');
  assert.equal(pack.templateId, 'fbif');
  assert.equal(pack.accountName, 'FoodTalks什么是系列');
  assert.equal(pack.previewAccountName, 'FoodTalks什么是系列');
  assert.equal(pack.behaviorAccountName, 'FoodTalks什么是系列');
  assert.equal(pack.footerUrl, '/footer.html');
});

test('rule pack can be selected by account query without adding visible template switching', () => {
  assert.equal(getRulePackIDFromLocation({ search: '?account=fbif' }), 'fbif');
  assert.equal(getRulePackIDFromLocation({ search: '?account=foodtalks-series' }), 'foodtalks-series');
  assert.equal(getRulePackIDFromLocation({ search: '?rule_pack=foodtalks-series' }), 'foodtalks-series');
  assert.equal(getRulePack('unknown').id, 'fbif');
});

test('rule pack registry assigns channel=mp to FoodTalks 什么是系列', () => {
  const pack = getRulePack('foodtalks-series');
  assert.equal(pack.id, 'foodtalks-series');
  assert.equal(pack.channel, 'mp');
  assert.equal(pack.templateId, 'fbif');
});

test('listRulePacks returns all packs with a channel field set', () => {
  const packs = listRulePacks();
  assert.ok(packs.length >= 2, 'expected at least 2 rule packs');
  for (const pack of packs) {
    assert.ok(pack.channel, `pack ${pack.id} missing channel`);
    assert.ok(['mp', 'ft'].includes(pack.channel), `pack ${pack.id} channel must be mp|ft`);
  }
});

test('app and engine read account chrome and footer from the rule pack registry', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  assert.match(app, /rule-packs\/registry\.js/);
  assert.match(app, /window\._activeRulePack = activeRulePack/);
  assert.match(app, /applyRulePackChrome\(activeRulePack\)/);
  assert.match(engine, /activeRulePack\.footerUrl/);
});
