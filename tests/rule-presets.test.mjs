import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_RULE_CONFIG,
  applyActiveRuleConfig,
  buildRuleConfigDocument,
  getActiveRuleBehavior,
  loadActiveRulePreset,
  loadLocalRuleConfig,
  normalizeRuleConfig,
  parseRuleConfigDocument,
  saveLocalRuleConfig,
  setActiveRulePreset,
  validateRuleConfigDocument,
  validateRuleConfig,
} from '../public/js/rule-presets.js';
import { DEFAULT_BEHAVIOR } from '../public/js/rule-behavior.js';

test('rule config keeps legacy defaults when optional fields are missing', () => {
  const config = normalizeRuleConfig({ font_size: '16', footer_enabled: false });
  assert.equal(config.font_size, '16');
  assert.equal(config.heading_size, DEFAULT_RULE_CONFIG.heading_size);
  assert.equal(config.link_color, '#0070C0');
  assert.equal(config.footer_enabled, false);
  assert.equal(config.skip_upload, true);
});

test('rule config accepts modular JSON documents without changing flat consumers', () => {
  const config = normalizeRuleConfig({
    schema_version: 2,
    font_size: DEFAULT_RULE_CONFIG.font_size,
    modules: {
      theme: {
        font_size: '16',
        text_color: '#123abc',
      },
      more_articles: {
        more_articles_slots: '4',
      },
      workflow: {
        footer_enabled: false,
      },
    },
  });

  assert.equal(config.font_size, '16');
  assert.equal(config.text_color, '#123ABC');
  assert.equal(config.more_articles_slots, '4');
  assert.equal(config.footer_enabled, false);
  assert.equal(config.heading_size, DEFAULT_RULE_CONFIG.heading_size);
});

test('rule config JSON document round-trips through modules', () => {
  const doc = buildRuleConfigDocument({
    ...DEFAULT_RULE_CONFIG,
    font_size: '16',
    footer_enabled: false,
  }, {
    name: '测试规则',
    description: '用于 JSON 编辑框',
  });
  assert.equal(doc.schema_version, 2);
  assert.equal(doc.name, '测试规则');
  assert.equal(doc.description, '用于 JSON 编辑框');
  assert.equal(doc.modules.theme.font_size, '16');
  assert.equal(doc.modules.workflow.footer_enabled, false);

  const parsed = parseRuleConfigDocument(JSON.stringify(doc));
  assert.equal(parsed.name, '测试规则');
  assert.equal(parsed.description, '用于 JSON 编辑框');
  assert.equal(parsed.config.font_size, '16');
  assert.equal(parsed.config.footer_enabled, false);
});

test('rule config JSON validation rejects values that would be silently normalized', () => {
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      theme: {
        text_color: 'red',
      },
    },
  })), /颜色/);
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      theme: {
        font_size: '999',
      },
    },
  })), /不能大于/);
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      theme: {
        font_family: "PingFang';<img src=x>",
      },
    },
  })), /字体/);
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      surprise: {
        font_size: '16',
      },
    },
  })), /未知规则模块/);
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      workflow: {
        font_size: '16',
      },
    },
  })), /不能放在 workflow 模块/);
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      theme: {
        font_size: '16',
      },
      supporting: {
        font_size: '17',
      },
    },
  })), /重复规则字段|不能放在 supporting 模块/);
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

test('active rule fetch includes the selected account id', async () => {
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push({ url: String(url), options });
    return {
      ok: true,
      async json() {
        return {
          preset: {
            id: 'foodtalks-series-default',
            name: 'FoodTalks什么是系列默认规则',
            config: DEFAULT_RULE_CONFIG,
            behavior: {
              templates: {
                author_line: '撰文：{{ article.author }}',
              },
            },
          },
        };
      },
    };
  };

  try {
    const loaded = await loadActiveRulePreset('foodtalks-series');
    assert.equal(loaded.source, 'server');
    assert.equal(loaded.preset.id, 'foodtalks-series-default');
    assert.equal(loaded.preset.behavior.templates.author_line, '撰文：{{ article.author }}');
    assert.equal(calls[0].url, '/api/rules/active?account=foodtalks-series');
  } finally {
    delete globalThis.fetch;
  }
});

test('active rule fallback includes default behavior', async () => {
  const originalWarn = console.warn;
  console.warn = () => {};
  globalThis.fetch = async () => {
    throw new Error('offline');
  };
  globalThis.window = {
    localStorage: {
      getItem() { return null; },
      setItem() {},
    },
  };

  try {
    const loaded = await loadActiveRulePreset('foodtalks-series');
    assert.equal(loaded.source, 'local');
    assert.equal(loaded.preset.behavior.templates.author_line, DEFAULT_BEHAVIOR.templates.author_line);
  } finally {
    console.warn = originalWarn;
    delete globalThis.fetch;
    delete globalThis.window;
  }
});

test('applyActiveRuleConfig stores active behavior for renderers', () => {
  globalThis.window = {};
  try {
    applyActiveRuleConfig(DEFAULT_RULE_CONFIG, {
      id: 'custom',
      behavior: {
        templates: {
          source_line: '出品：{{ article.source }}',
        },
      },
    });
    assert.equal(getActiveRuleBehavior().templates.source_line, '出品：{{ article.source }}');
  } finally {
    delete globalThis.window;
  }
});

test('admin active rule writes include the selected account id', async () => {
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push({ url: String(url), options });
    return {
      ok: true,
      async json() {
        return { active_id: 'default', presets: [], accounts: [] };
      },
    };
  };

  try {
    await setActiveRulePreset('secret', 'foodtalks-rule', 'foodtalks-series');
    assert.equal(calls[0].url, '/api/rules/admin/active');
    assert.deepEqual(JSON.parse(calls[0].options.body), {
      id: 'foodtalks-rule',
      account_id: 'foodtalks-series',
    });
  } finally {
    delete globalThis.fetch;
  }
});

test('local rule fallback is isolated by account while preserving FBIF legacy keys', () => {
  const store = new Map();
  globalThis.window = {
    localStorage: {
      getItem(key) { return store.has(key) ? store.get(key) : null; },
      setItem(key, value) { store.set(key, String(value)); },
    },
  };

  try {
    saveLocalRuleConfig({ ...DEFAULT_RULE_CONFIG, font_size: '16' }, 'fbif');
    saveLocalRuleConfig({ ...DEFAULT_RULE_CONFIG, font_size: '18' }, 'foodtalks-series');

    assert.equal(JSON.parse(store.get('config_fbif')).font_size, '16');
    assert.equal(JSON.parse(store.get('config_foodtalks-series')).font_size, '18');
    assert.equal(loadLocalRuleConfig('fbif').font_size, '16');
    assert.equal(loadLocalRuleConfig('foodtalks-series').font_size, '18');
  } finally {
    delete globalThis.window;
  }
});
