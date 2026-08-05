import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_RULE_CONFIG,
  applyActiveRuleConfig,
  buildRuleConfigDocument,
  getActiveRuleBehavior,
  listPublicRulePresets,
  loadActiveRulePreset,
  loadLocalRuleConfig,
  loadLocalRulePreset,
  normalizeRuleConfig,
  parseRuleConfigDocument,
  saveRulePreset,
  saveLocalRuleConfig,
  saveLocalRulePreset,
  setActiveRulePreset,
  validateRuleConfigDocument,
  validateRuleConfig,
} from '../public/js/rule-presets.js';
import { DEFAULT_BEHAVIOR } from '../public/js/rule-behavior.js';
import { SYSTEM_DEFAULT_RECOMMENDED_CARDS } from '../public/js/more-articles-config.js';

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

test('rule config document parse preserves structured modules', () => {
  const parsed = parseRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      article_footer_defaults: {
        schema_version: 1,
        enabled: false,
        author_aliases: {},
        profiles: [],
      },
      bottom_blocks: {
        schema_version: 2,
        enabled: false,
        presets: [],
      },
    },
  }));

  assert.equal(parsed.config.modules.article_footer_defaults.enabled, false);
  assert.equal(parsed.config.modules.bottom_blocks.enabled, false);
});

test('rule config accepts readable more_articles enabled and default_cards fields', () => {
  const parsed = parseRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      more_articles: {
        enabled: false,
        default_cards: [
          {
            href: 'https://mp.weixin.qq.com/s/custom-a',
            title: '自定义 A',
            imgurl: '//mmbiz.qpic.cn/custom-a.jpg',
          },
        ],
        more_articles_slots: '1',
      },
    },
  }));

  assert.equal(parsed.config.more_articles_enabled, false);
  assert.deepEqual(parsed.config.more_articles_default_cards, [{
    href: 'https://mp.weixin.qq.com/s/custom-a',
    title: '自定义 A',
    imgurl: 'https://mmbiz.qpic.cn/custom-a.jpg',
    image_urls: [],
    cover_data_url: null,
    crop: { x: 0.5, y: 0.5, scale: 1 },
  }]);
  assert.equal(parsed.config.more_articles_slots, '1');
});

test('rule config JSON document emits readable more_articles fields', () => {
  const doc = buildRuleConfigDocument({
    ...DEFAULT_RULE_CONFIG,
    more_articles_enabled: false,
    more_articles_default_cards: [{
      href: 'https://mp.weixin.qq.com/s/custom-b',
      title: '自定义 B',
      imgurl: 'https://mmbiz.qpic.cn/custom-b.jpg',
    }],
  }, {
    name: '更多文章测试',
  });

  assert.equal(doc.modules.more_articles.enabled, false);
  assert.deepEqual(doc.modules.more_articles.default_cards, [{
    href: 'https://mp.weixin.qq.com/s/custom-b',
    title: '自定义 B',
    imgurl: 'https://mmbiz.qpic.cn/custom-b.jpg',
    image_urls: [],
    cover_data_url: null,
    crop: { x: 0.5, y: 0.5, scale: 1 },
  }]);
  assert.equal(doc.modules.more_articles.more_articles_default_cards, undefined);
});

test('rule config keeps legacy defaults when more_articles default_cards are missing', () => {
  const config = normalizeRuleConfig({
    schema_version: 2,
    modules: {
      more_articles: {
        more_articles_slots: '3',
      },
    },
  });

  assert.equal(config.more_articles_enabled, true);
  assert.deepEqual(
    config.more_articles_default_cards.map(card => card.href),
    SYSTEM_DEFAULT_RECOMMENDED_CARDS.map(card => card.href)
  );
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
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      more_articles: {
        default_cards: [
          { href: 'javascript:alert(1)', title: '坏链接' },
        ],
      },
    },
  })), /公众号链接/);
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      more_articles: {
        default_cards: [
          {
            href: '',
            title: '坏图片',
            imgurl: 'javascript:alert(1)',
          },
        ],
      },
    },
  })), /默认更多文章.*imgurl/);
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      more_articles: {
        default_cards: [
          {
            href: '',
            title: '坏候选图',
            image_urls: ['https://mmbiz.qpic.cn/a.jpg', 'javascript:alert(1)'],
          },
        ],
      },
    },
  })), /默认更多文章.*image_urls/);
  assert.throws(() => validateRuleConfigDocument(JSON.stringify({
    schema_version: 2,
    modules: {
      more_articles: {
        default_cards: [
          {
            href: '',
            title: { text: '坏标题' },
          },
        ],
      },
    },
  })), /默认更多文章.*title/);
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

test('local fallback preserves custom more articles default cards', () => {
  const store = new Map();
  globalThis.window = {
    localStorage: {
      getItem(key) { return store.has(key) ? store.get(key) : null; },
      setItem(key, value) { store.set(key, String(value)); },
    },
  };
  try {
    saveLocalRuleConfig({
      ...DEFAULT_RULE_CONFIG,
      more_articles_default_cards: [{
        href: 'https://mp.weixin.qq.com/s/custom-local',
        title: '本地自定义',
        imgurl: '//mmbiz.qpic.cn/custom-local.jpg',
      }],
    });

    const loaded = loadLocalRuleConfig();
    assert.deepEqual(loaded.more_articles_default_cards, [{
      href: 'https://mp.weixin.qq.com/s/custom-local',
      title: '本地自定义',
      imgurl: 'https://mmbiz.qpic.cn/custom-local.jpg',
      image_urls: [],
      cover_data_url: null,
      crop: { x: 0.5, y: 0.5, scale: 1 },
    }]);
  } finally {
    delete globalThis.window;
  }
});

test('local fallback preserves structured footer modules', () => {
  const store = new Map();
  globalThis.window = {
    localStorage: {
      getItem(key) { return store.has(key) ? store.get(key) : null; },
      setItem(key, value) { store.set(key, String(value)); },
    },
  };
  const structured = {
    ...DEFAULT_RULE_CONFIG,
    modules: {
      bottom_blocks: {
        schema_version: 2,
        enabled: true,
        presets: [{ id: 'default', order: [], blocks: {} }],
      },
      article_footer_defaults: {
        schema_version: 1,
        enabled: true,
        author_aliases: { mote: ['Mote莫特'] },
        profiles: [{
          id: 'mote_author',
          match: { author_alias_group: 'mote' },
          block_visibility: { byline: false },
          artifact_visibility: { mote_ref_author_card: false },
        }],
      },
    },
  };
  try {
    const saved = saveLocalRuleConfig(structured, 'fbif');
    assert.equal(saved.modules.article_footer_defaults.enabled, true);

    const stored = JSON.parse(store.get('config_fbif'));
    assert.equal(stored.modules.bottom_blocks.enabled, true);
    assert.equal(stored.modules.article_footer_defaults.profiles[0].block_visibility.byline, false);

    const loaded = loadLocalRuleConfig('fbif');
    assert.equal(loaded.modules.bottom_blocks.schema_version, 2);
    assert.equal(loaded.modules.article_footer_defaults.profiles[0].artifact_visibility.mote_ref_author_card, false);
  } finally {
    delete globalThis.window;
  }
});

test('网站 server fallback 在离线时保留 preset 身份与自定义 behavior', () => {
  const store = new Map();
  globalThis.window = {
    localStorage: {
      getItem(key) { return store.has(key) ? store.get(key) : null; },
      setItem(key, value) { store.set(key, String(value)); },
    },
  };
  const behavior = {
    author_regex: '^署名[：:]',
    lists: { wechat_trailer_markers: ['网站自定义尾标'] },
  };
  try {
    saveLocalRulePreset({
      id: 'website-custom',
      name: '网站自定义',
      baseline_id: 'ft-default-v1',
      config: { tip_title_color: '#123456' },
      behavior,
    }, 'ft-default');
    const local = loadLocalRulePreset('ft-default');
    assert.equal(local.id, 'website-custom');
    assert.equal(local.baseline_id, 'ft-default-v1');
    assert.equal(local.config.tip_title_color, '#123456');
    assert.deepEqual(local.behavior, behavior);
    assert.equal(local.behavior.actions, undefined);
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
            config: { ...DEFAULT_RULE_CONFIG, modules: { asset_library: { assets: [] } } },
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
    assert.deepEqual(loaded.preset.raw_config.modules.asset_library.assets, []);
    assert.equal(loaded.preset.behavior.templates.author_line, '撰文：{{ article.author }}');
    assert.equal(calls[0].url, '/api/rules/active?account=foodtalks-series');
  } finally {
    delete globalThis.fetch;
  }
});

test('ft-default active rule preserves raw website behavior through load and apply', async () => {
  const websiteBehavior = {
    author_regex: '^\\s*署名[：:]\\s*',
    tip_regex: '^\\s*网站提示[：:]',
    lists: { wechat_trailer_markers: ['网站自定义尾标'] },
    params: { site_variant: { type: 'string', default: 'ft-default' } },
  };
  globalThis.fetch = async () => ({
    ok: true,
    async json() {
      return {
        preset: {
          id: 'website-custom',
          baseline_id: 'ft-default-v1',
          config: { ...DEFAULT_RULE_CONFIG, tip_title_color: '#112233', author_font_size: '16' },
          behavior: websiteBehavior,
        },
      };
    },
  });
  globalThis.window = { localStorage: { getItem() { return null; }, setItem() {} } };

  try {
    const loaded = await loadActiveRulePreset('ft-default');
    assert.deepEqual(loaded.preset.behavior, websiteBehavior, '网站 behavior 加载时必须原样保留');
    assert.equal(loaded.preset.config.tip_title_color, '#112233', '网站专属顶层 config 不得被公众号 schema 裁掉');
    assert.equal(loaded.preset.rule_account_id, 'ft-default');
    applyActiveRuleConfig(loaded.preset.config, loaded.preset);
    assert.deepEqual(window._activeConfig.behavior, websiteBehavior, '网站 behavior 应原样灌入 site.html');
    assert.equal(window._activeConfig.config.author_font_size, '16');
    assert.equal(window._activeConfig.behavior.actions, undefined, '不得注入公众号 Mote actions');
  } finally {
    delete globalThis.fetch;
    delete globalThis.window;
  }
});

test('FoodTalks 系列 load/apply 保留独立 behavior schema，不注入 FBIF Mote 动作', async () => {
  const behavior = {
    params: { level1_regex: { type: 'string', default: '^第[一二三]章' } },
    templates: { recommended_reading_note: '系列自定义说明' },
    lists: { image_caption_keywords: ['系列图源'] },
  };
  globalThis.fetch = async () => ({
    ok: true,
    async json() {
      return { preset: { id: 'series-custom', baseline_id: 'foodtalks-series-default', rule_family: 'foodtalks-series', config: { heading_color: '#02396b' }, behavior } };
    },
  });
  globalThis.window = { localStorage: { getItem() { return null; }, setItem() {} } };
  try {
    const loaded = await loadActiveRulePreset('foodtalks-series');
    assert.deepEqual(loaded.preset.behavior, behavior);
    assert.equal(loaded.preset.config.heading_color, '#02396b');
    applyActiveRuleConfig(loaded.preset.config, loaded.preset);
    assert.deepEqual(window._activeConfig.behavior, behavior);
    assert.equal(window._activeConfig.behavior.actions, undefined);
  } finally {
    delete globalThis.fetch;
    delete globalThis.window;
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
    const loaded = await loadActiveRulePreset('fbif');
    assert.equal(loaded.source, 'local');
    assert.equal(loaded.preset.behavior.templates.author_line, DEFAULT_BEHAVIOR.templates.author_line);
  } finally {
    console.warn = originalWarn;
    delete globalThis.fetch;
    delete globalThis.window;
  }
});

test('ft-default offline fallback uses website behavior defaults, never FBIF actions', async () => {
  const originalWarn = console.warn;
  console.warn = () => {};
  globalThis.fetch = async () => { throw new Error('offline'); };
  globalThis.window = { localStorage: { getItem() { return null; }, setItem() {} } };
  try {
    const loaded = await loadActiveRulePreset('ft-default');
    assert.equal(loaded.source, 'local');
    assert.deepEqual(loaded.preset.behavior, {}, '网站模板的自有常量才是离线 behavior 基线');
    assert.equal(loaded.preset.behavior.actions, undefined, '离线网站不得注入公众号 Mote actions');
  } finally {
    console.warn = originalWarn;
    delete globalThis.fetch;
    delete globalThis.window;
  }
});

test('FoodTalks 系列首次离线 fallback 不注入 FBIF behavior', async () => {
  const originalWarn = console.warn;
  console.warn = () => {};
  globalThis.fetch = async () => { throw new Error('offline'); };
  globalThis.window = { localStorage: { getItem() { return null; }, setItem() {} } };
  try {
    const loaded = await loadActiveRulePreset('foodtalks-series');
    assert.deepEqual(loaded.preset.behavior, {});
    assert.equal(loaded.preset.behavior.actions, undefined);
  } finally {
    console.warn = originalWarn;
    delete globalThis.fetch;
    delete globalThis.window;
  }
});

test('ft-default first-run local config is website default, not FBIF footer baseline', () => {
  delete globalThis.window;
  const config = loadLocalRuleConfig('ft-default');
  assert.equal(config.tip_title_color, '#01396a');
  assert.equal(config.image.width, 600);
  assert.equal(config.modules.mctoc.enabled, false);
  assert.equal(config.modules.bottom_blocks.enabled, false);
  assert.equal(config.modules.article_footer_defaults.enabled, false);
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

test('applyActiveRuleConfig preserves structured modules for runtime UIs', () => {
  globalThis.window = {};
  try {
    applyActiveRuleConfig(DEFAULT_RULE_CONFIG, {
      id: 'structured',
      raw_config: { modules: { asset_library: { assets: [{ id: 'x' }] }, ft_series_materials: { groups: { 包装: '/a.png' } } } },
      behavior: DEFAULT_BEHAVIOR,
    });
    assert.deepEqual(window._activeConfig.config.modules.asset_library.assets, [{ id: 'x' }]);
    assert.equal(window._activeConfig.config.modules.ft_series_materials.groups['包装'], '/a.png');
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
    await setActiveRulePreset('foodtalks-rule', 'foodtalks-series', 12);
    assert.equal(calls[0].url, '/api/rules/admin/active');
    assert.deepEqual(JSON.parse(calls[0].options.body), {
      id: 'foodtalks-rule',
      account_id: 'foodtalks-series',
      base_revision: 12,
    });
  } finally {
    delete globalThis.fetch;
  }
});

test('admin write conflict preserves revisions and changed paths for the UI', async () => {
  globalThis.fetch = async () => ({
    ok: false,
    status: 409,
    async json() {
      return {
        error: '规则已由其他管理员更新；请重新加载后合并。服务端变化：config.font_size',
        code: 'revision_conflict',
        expected_revision: 3,
        current_revision: 4,
        changed_paths: ['config.font_size'],
      };
    },
  });
  try {
    await assert.rejects(
      () => saveRulePreset({ id: 'shared', version: 3, baseline_id: 'default', config: {} }),
      error => {
        assert.equal(error.code, 'revision_conflict');
        assert.equal(error.expectedRevision, 3);
        assert.equal(error.currentRevision, 4);
        assert.deepEqual(error.changedPaths, ['config.font_size']);
        assert.match(error.message, /重新加载/);
        return true;
      },
    );
  } finally {
    delete globalThis.fetch;
  }
});

test('admin preset save preserves structured modules', async () => {
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push({ url: String(url), options });
    return {
      ok: true,
      async json() {
        return { ok: true };
      },
    };
  };

  try {
    await saveRulePreset({
      id: 'structured-save',
      name: '结构化保存',
      baseline_id: 'ft-default-v1',
      config: {
        ...DEFAULT_RULE_CONFIG,
        modules: {
          article_footer_defaults: {
            schema_version: 1,
            enabled: false,
            author_aliases: {},
            profiles: [],
          },
        },
      },
      behavior: {
        author_regex: '^\\s*署名[：:]\\s*',
        lists: { wechat_trailer_markers: ['网站自定义尾标'] },
      },
    });
    const body = JSON.parse(calls[0].options.body);
    assert.equal(body.base_revision, 0, '新规则必须以不存在修订 0 做条件写入');
    assert.equal(body.baseline_id, 'ft-default-v1', '显式规则基线必须透传到保存 API');
    assert.equal(body.behavior.author_regex, '^\\s*署名[：:]\\s*');
    assert.deepEqual(body.behavior.lists.wechat_trailer_markers, ['网站自定义尾标']);
    assert.equal(body.behavior.actions, undefined, '保存网站规则不得注入公众号 actions');
    assert.equal(body.config.modules.article_footer_defaults.enabled, false);
  } finally {
    delete globalThis.fetch;
  }
});

test('保存 FoodTalks 系列 preset 原样发送独立 behavior，不注入 Mote', async () => {
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return { ok: true, json: async () => ({ ok: true }) };
  };
  const behavior = { params: { level1_regex: { type: 'string', default: '^章节' } }, lists: { image_caption_keywords: ['图源'] } };
  try {
    await saveRulePreset({
      id: 'series-custom', name: '系列自定义', baseline_id: 'foodtalks-series-default',
      config: { heading_color: '#02396b' }, behavior,
    });
    const body = JSON.parse(calls[0].options.body);
    assert.equal(body.base_revision, 0);
    assert.deepEqual(body.behavior, behavior);
    assert.equal(body.behavior.actions, undefined);
    assert.equal(body.config.heading_color, '#02396b');
  } finally {
    delete globalThis.fetch;
  }
});

test('public rule catalog fetches the rule list without admin headers', async () => {
  const calls = [];
  globalThis.fetch = async (url, options = {}) => {
    calls.push({ url: String(url), options });
    return {
      ok: true,
      async json() {
        return { presets: [{ id: 'default', name: 'FBIF默认规则' }], accounts: [] };
      },
    };
  };

  try {
    const catalog = await listPublicRulePresets();
    assert.equal(catalog.presets[0].id, 'default');
    assert.equal(calls[0].url, '/api/rules/presets');
    assert.equal(calls[0].options.cache, 'no-store');
    assert.equal(calls[0].options.headers, undefined);
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
