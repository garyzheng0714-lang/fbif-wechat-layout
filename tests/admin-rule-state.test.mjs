import test from 'node:test';
import assert from 'node:assert/strict';

import {
  accountBindingRevision,
  baselinePresetForNewRule,
  compatibleRulePresets,
  migrationBaselinePresets,
  presetForAccount,
  uniqueClientRuleName,
} from '../public/js/admin-rule-state.js';
import { preserveUneditableRuleConfig } from '../public/js/admin-rule-payload.js';

const presets = [
  { id: 'default', baseline_id: 'default', channel: 'mp', rule_family: 'fbif', config: { text_color: '#544545' } },
  { id: 'ft-default-v1', baseline_id: 'ft-default-v1', channel: 'ft', rule_family: 'foodtalks-site', config: { text_color: '#111111' } },
  { id: 'ft-series-v1', baseline_id: 'ft-series-v1', channel: 'ft', rule_family: 'foodtalks-site', config: { text_color: '#333333', justify_paragraphs: false } },
  { id: 'site-custom', baseline_id: 'ft-series-v1', channel: 'ft', rule_family: 'foodtalks-site', config: { text_color: '#abcdef' } },
];

// 复制规则时 ID 一直会去重，显示名却曾原样沿用——下拉里两条同名规则用户无从分辨。
test('规则名去重：复制与新建都不得产生同名规则', () => {
  const doc = { presets: [{ name: 'FBIF默认规则' }, { name: '新规则' }] };

  assert.equal(uniqueClientRuleName('WowFood默认规则', doc), 'WowFood默认规则', '不重名时保持原名');
  assert.equal(uniqueClientRuleName('FBIF默认规则', doc), 'FBIF默认规则 副本');
  assert.equal(uniqueClientRuleName('新规则', doc), '新规则 副本');

  const withCopy = { presets: [...doc.presets, { name: 'FBIF默认规则 副本' }] };
  assert.equal(uniqueClientRuleName('FBIF默认规则', withCopy), 'FBIF默认规则 副本 2', '第二次复制继续递增');

  const withCopy2 = { presets: [...withCopy.presets, { name: 'FBIF默认规则 副本 2' }] };
  assert.equal(uniqueClientRuleName('FBIF默认规则', withCopy2), 'FBIF默认规则 副本 3');

  // 复制一份副本时要还原出原名再递增，否则会叠成「副本 副本」。
  assert.equal(uniqueClientRuleName('FBIF默认规则 副本', withCopy), 'FBIF默认规则 副本 2');
  assert.equal(uniqueClientRuleName('FBIF默认规则 副本 2', withCopy2), 'FBIF默认规则 副本 3');

  assert.equal(uniqueClientRuleName('  ', doc), '新规则 副本', '空名回落到默认名后仍须去重');
  assert.equal(uniqueClientRuleName('FBIF默认规则', { presets: [] }), 'FBIF默认规则', '空规则库不加后缀');
});

test('账号状态回退只读该账号 binding，不把网站回退到 legacy FBIF active_id', () => {
  const doc = {
    active_id: 'default',
    presets,
    accounts: [{ id: 'ft-series', active_id: 'site-custom' }],
  };
  assert.equal(presetForAccount(doc, 'ft-series')?.id, 'site-custom');
  assert.equal(presetForAccount(doc, 'missing')?.id, undefined, '未知账号不得静默读 FBIF active_id');
});

test('账号绑定修订号只从当前账号读取', () => {
  const doc = {
    accounts: [
      { id: 'fbif', active_revision: 4 },
      { id: 'ft-series', active_revision: 9 },
    ],
  };
  assert.equal(accountBindingRevision(doc, 'ft-series'), 9);
  assert.equal(accountBindingRevision(doc, 'missing'), 0);
});

test('新建规则从显式 baseline preset 起步，不从 FBIF DEFAULT_RULE_CONFIG 起步', () => {
  const selected = presets.find(item => item.id === 'site-custom');
  const baseline = baselinePresetForNewRule({ presets }, selected);
  assert.equal(baseline.id, 'ft-series-v1');
  assert.equal(baseline.config.text_color, '#333333');
  assert.equal(baseline.config.justify_paragraphs, false);
});

test('规则选项按渲染规则族过滤，同为 mp 的独立模板也不可互绑', () => {
  const withSeries = presets.concat({ id: 'series', channel: 'mp', rule_family: 'foodtalks-series' });
  assert.deepEqual(compatibleRulePresets(withSeries, 'foodtalks-site').map(item => item.id), ['ft-default-v1', 'ft-series-v1', 'site-custom']);
  assert.deepEqual(compatibleRulePresets(withSeries, 'fbif').map(item => item.id), ['default']);
  assert.deepEqual(compatibleRulePresets(withSeries, 'foodtalks-series').map(item => item.id), ['series']);
});

test('待确认基线的历史规则在后台可见，便于人工修复', () => {
  const migrationRule = {
    id: 'legacy-ambiguous',
    baseline_id: 'default',
    rule_family: 'fbif',
    migration_required: true,
  };
  const visible = compatibleRulePresets(presets.concat(migrationRule), 'foodtalks-site');
  assert.ok(visible.some(item => item.id === 'legacy-ambiguous'));
});

test('历史基线确认只列当前渲染规则族，不提供必然被后端拒绝的跨族选项', () => {
  assert.deepEqual(migrationBaselinePresets(presets, 'fbif').map(item => item.id), ['default']);
  assert.deepEqual(migrationBaselinePresets(presets, 'foodtalks-site').map(item => item.id), ['ft-default-v1', 'ft-series-v1']);
});

test('保存公众号系列规则保留不可视编辑字段和 ft_series_materials，不注入网站-only 模块', () => {
  const editable = {
    schema_version: 2,
    modules: {
      theme: { font_size: '16' },
      bottom_blocks: { schema_version: 2, enabled: false, presets: [] },
    },
  };
  const original = {
    heading_color: '#02396b',
    modules: {
      ft_series_materials: { cards: { Eileen: '/custom/eileen.jpg' } },
      asset_library: { schema_version: 1, assets: [] },
    },
  };
  const merged = preserveUneditableRuleConfig(editable, original);
  assert.equal(merged.heading_color, '#02396b');
  assert.equal(merged.modules.ft_series_materials.cards.Eileen, '/custom/eileen.jpg');
  assert.equal(merged.modules.fbif_footer, undefined);
});
