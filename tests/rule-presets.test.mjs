import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_RULE_CONFIG,
  loadLocalRuleConfig,
  normalizeRuleConfig,
  saveLocalRuleConfig,
  validateRuleConfig,
} from '../public/js/rule-presets.js';

test('rule config keeps legacy defaults when optional fields are missing', () => {
  const config = normalizeRuleConfig({ font_size: '16', footer_enabled: false });
  assert.equal(config.font_size, '16');
  assert.equal(config.heading_size, DEFAULT_RULE_CONFIG.heading_size);
  assert.equal(config.link_color, '#0070C0');
  assert.equal(config.footer_enabled, false);
  assert.equal(config.skip_upload, true);
});

test('local fallback keeps legacy skip-upload preference', () => {
  const store = new Map([
    ['skip_upload', 'true'],
  ]);
  globalThis.window = {
    localStorage: {
      getItem(key) { return store.has(key) ? store.get(key) : null; },
      setItem(key, value) { store.set(key, String(value)); },
    },
  };
  try {
    const loaded = loadLocalRuleConfig();
    assert.equal(loaded.skip_upload, true);

    saveLocalRuleConfig({ ...DEFAULT_RULE_CONFIG, skip_upload: false });
    assert.equal(JSON.parse(store.get('config_fbif')).skip_upload, false);
    assert.equal(store.get('skip_upload'), 'false');
  } finally {
    delete globalThis.window;
  }
});

test('local fallback restores skip-upload after bad migration', () => {
  const store = new Map([
    ['config_fbif', JSON.stringify({ skip_upload: false })],
    ['skip_upload', 'false'],
    ['skip_upload_migrated_v2', 'true'],
  ]);
  globalThis.window = {
    localStorage: {
      getItem(key) { return store.has(key) ? store.get(key) : null; },
      setItem(key, value) { store.set(key, String(value)); },
    },
  };
  try {
    const loaded = loadLocalRuleConfig();
    assert.equal(loaded.skip_upload, true);
    assert.equal(store.get('skip_upload'), 'true');
    assert.equal(store.get('skip_upload_restored_v3'), 'true');
  } finally {
    delete globalThis.window;
  }
});

test('rule config clamps numeric fields and normalizes colors', () => {
  const config = normalizeRuleConfig({
    font_size: '99',
    banner_overlay_alpha: '-1',
    text_color: 'red',
  });
  assert.equal(config.font_size, '24');
  assert.equal(config.banner_overlay_alpha, '0');
  assert.equal(config.text_color, '#544545');
});

test('rule config validation reports cross-rule conflicts', () => {
  const warnings = validateRuleConfig({
    ...DEFAULT_RULE_CONFIG,
    font_size: '18',
    heading_size: '16',
    banner_title_width: '980',
    banner_title_x: '80',
  });
  assert.ok(warnings.some(w => w.includes('标题字号小于正文字号')));
  assert.ok(warnings.some(w => w.includes('标题宽度超出')));
});
