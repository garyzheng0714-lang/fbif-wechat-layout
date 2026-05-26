import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  DEFAULT_RULE_PACK_ID,
  getRulePack,
  getRulePackByPath,
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
  assert.ok(packs.some(p => p.id === 'fbif'), 'fbif should be in the list');
  assert.ok(packs.some(p => p.id === 'foodtalks-series'), 'foodtalks-series should be in the list');

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

test('getRulePackByPath resolves /mp/fbif to fbif pack', () => {
  const pack = getRulePackByPath('/mp/fbif');
  assert.equal(pack.id, 'fbif');
});

test('getRulePackByPath resolves /mp/foodtalks-series', () => {
  const pack = getRulePackByPath('/mp/foodtalks-series');
  assert.equal(pack.id, 'foodtalks-series');
});

test('getRulePackByPath falls back to channel default when packId missing', () => {
  const pack = getRulePackByPath('/mp');
  assert.equal(pack.id, 'fbif');
});

test('getRulePackByPath returns DEFAULT_RULE_PACK_ID for non-channel paths', () => {
  const pack = getRulePackByPath('/');
  assert.equal(pack.id, 'fbif');
});

test('getRulePackByPath returns DEFAULT when packId unknown in channel', () => {
  const pack = getRulePackByPath('/mp/does-not-exist');
  assert.equal(pack.id, 'fbif');
});

test('rule pack registry assigns ft-default to the website channel', () => {
  const pack = getRulePack('ft-default');
  assert.equal(pack.id, 'ft-default');
  assert.equal(pack.channel, 'ft');
  assert.equal(pack.templateId, 'foodtalks-site');
});

test('getRulePackByPath /ft/default resolves to ft-default', () => {
  const pack = getRulePackByPath('/ft/default');
  assert.equal(pack.id, 'ft-default');
});

test('getRulePackByPath /ft falls back to ft-default (channel default)', () => {
  const pack = getRulePackByPath('/ft');
  assert.equal(pack.id, 'ft-default');
});

test('getRulePackByPath /ft/unknown falls back to ft-default in same channel', () => {
  const pack = getRulePackByPath('/ft/unknown');
  assert.equal(pack.id, 'ft-default');
});
