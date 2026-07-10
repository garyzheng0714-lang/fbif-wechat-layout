import test from 'node:test';
import assert from 'node:assert/strict';
import { adminDirtyCount } from '../public/js/admin-dirty-state.js';
import { normalizeRuleConfig } from '../public/js/rule-presets.js';
import { DEFAULT_BEHAVIOR } from '../public/js/rule-behavior.js';

test('ft-default 原始 behavior 加载后不被误报为未保存改动', () => {
  const behavior = {
    author_regex: '^\\s*署名[：:]',
    lists: { wechat_trailer_markers: ['网站尾标'] },
  };
  const selected = {
    id: 'website-custom',
    baseline_id: 'ft-default-v1',
    name: '网站规则',
    description: '',
    config: {},
    behavior,
  };
  assert.equal(adminDirtyCount({
    selected,
    selectedID: selected.id,
    workingName: selected.name,
    workingDescription: '',
    workingConfig: normalizeRuleConfig(selected.config),
    workingBehavior: behavior,
    isFtDefault: true,
    accountID: 'ft-default',
  }), 0);
});

test('FoodTalks 系列缺省 behavior 以空对象为基线，不注入 FBIF 默认造成假 dirty', () => {
  const selected = {
    id: 'foodtalks-series-default',
    baseline_id: 'foodtalks-series-default',
    name: '系列默认',
    description: '',
    config: {},
  };
  assert.equal(adminDirtyCount({
    selected,
    selectedID: selected.id,
    workingName: selected.name,
    workingDescription: '',
    workingConfig: normalizeRuleConfig(selected.config),
    workingBehavior: {},
    isFtDefault: false,
    accountID: 'foodtalks-series',
  }), 0);
});

test('文章默认方案发生变化必须计入 dirty，不能关闭后台静默丢稿', () => {
  const original = {
    schema_version: 1,
    enabled: true,
    author_aliases: { mote: ['Mote莫特'] },
    profiles: [{ id: 'mote', match: { author_alias_group: 'mote' }, block_visibility: { byline: true } }],
  };
  const selected = {
    id: 'default',
    name: 'FBIF默认规则',
    config: { modules: { article_footer_defaults: original } },
    behavior: DEFAULT_BEHAVIOR,
  };
  const changed = structuredClone(original);
  changed.profiles[0].block_visibility.byline = false;
  assert.ok(adminDirtyCount({
    selected,
    selectedID: selected.id,
    workingName: selected.name,
    workingDescription: '',
    workingConfig: normalizeRuleConfig(selected.config),
    workingBehavior: DEFAULT_BEHAVIOR,
    workingArticleFooterDefaults: changed,
    isFtDefault: false,
    accountID: 'fbif',
  }) > 0);
});
