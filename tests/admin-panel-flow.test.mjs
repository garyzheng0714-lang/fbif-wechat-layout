import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

import { DEFAULT_RULE_CONFIG } from '../public/js/rule-presets.js';

const disabledArticleDefaults = { schema_version: 1, enabled: false, author_aliases: {}, profiles: [] };
const emptyBlocks = { schema_version: 2, enabled: false, presets: [] };
const assets = { schema_version: 1, assets: [] };

function seriesConfig(cardURL, extra = {}) {
  return {
    ...DEFAULT_RULE_CONFIG,
    heading_color: '#02396b',
    footer_enabled: false,
    more_articles_enabled: false,
    ...extra,
    modules: {
      asset_library: assets,
      bottom_blocks: emptyBlocks,
      article_footer_defaults: disabledArticleDefaults,
      ft_series_materials: { cards: { Eileen: cardURL } },
      ...(extra.modules || {}),
    },
  };
}

const siteBehavior = { params: { site_variant: { type: 'string', default: 'ft-series' } }, lists: { title_keywords: ['系列'] } };

async function waitFor(predicate, message) {
  for (let i = 0; i < 100; i++) {
    if (predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 0));
  }
  throw new Error(typeof message === 'function' ? message() : message);
}

test('规则后台完整状态机：按当前账号加载/保存系列私有模块，新建从系列 baseline 起步', async () => {
  const { document, window } = parseHTML('<!doctype html><html><body><button id="adminLockBtn">规则</button></body></html>');
  let focused = document.body;
  Object.defineProperty(document, 'activeElement', { configurable: true, get: () => focused });
  window.HTMLElement.prototype.focus = function focus() { focused = this; };
  globalThis.document = document;
  globalThis.window = window;
  window.confirm = () => true;
  window._activeRulePack = { id: 'foodtalks-series', channel: 'mp', templateId: 'foodtalks-series' };
  const baseline = {
    id: 'foodtalks-series-default', name: '系列基线', baseline_id: 'foodtalks-series-default', channel: 'mp', rule_family: 'foodtalks-series',
    config: seriesConfig('/baseline/eileen.jpg'), behavior: siteBehavior,
  };
  const custom = {
    id: 'series-custom', name: '系列自定义', baseline_id: 'foodtalks-series-default', channel: 'mp', rule_family: 'foodtalks-series',
    config: seriesConfig('/custom/eileen.jpg'),
    behavior: siteBehavior,
  };
  let doc = {
    active_id: 'default',
    presets: [{ id: 'default', name: 'FBIF', baseline_id: 'default', channel: 'mp', rule_family: 'fbif', config: DEFAULT_RULE_CONFIG }, baseline, custom],
    accounts: [{ id: 'foodtalks-series', name: 'FoodTalks什么是系列', active_id: 'series-custom' }],
  };
  const savedBodies = [];
  const requests = [];
  let holdNextPreview = false;
  let releasePreview = null;
  globalThis.fetch = async (url, options = {}) => {
    const path = String(url);
    const method = options.method || 'GET';
    requests.push(method + ' ' + path);
    if (path === '/api/rules/admin/presets' && method === 'GET') return { ok: true, status: 200, json: async () => doc };
    if (path === '/api/rules/admin/presets' && method === 'POST') {
      const body = JSON.parse(options.body);
      savedBodies.push(body);
      const next = { ...body, channel: 'mp', rule_family: 'foodtalks-series' };
      doc = { ...doc, presets: doc.presets.filter(p => p.id !== body.id).concat(next) };
      return { ok: true, status: 200, json: async () => doc };
    }
    if (path === '/api/rules/admin/active' && method === 'PUT') {
      const body = JSON.parse(options.body);
      doc = { ...doc, accounts: [{ id: body.account_id, name: 'FoodTalks什么是系列', active_id: body.id }] };
      return { ok: true, status: 200, json: async () => doc };
    }
    throw new Error('unexpected fetch ' + method + ' ' + path);
  };

  try {
    const { initAdminPanel } = await import('../public/js/admin-panel.js?flow=' + Date.now());
    initAdminPanel({
      rulePacks: [window._activeRulePack],
      getConfig: () => custom.config,
      getPreset: () => custom,
      getBehavior: () => custom.behavior,
      applyConfig(config, preset) {
        window._activeConfig = { config, preset, behavior: preset && preset.behavior };
        if (!holdNextPreview) return undefined;
        holdNextPreview = false;
        return new Promise(resolve => { releasePreview = resolve; });
      },
    });
    const opener = document.getElementById('adminLockBtn');
    opener.focus();
    opener.click();
    const overlay = document.body.lastElementChild;
    await waitFor(() => overlay.querySelector('[data-view="assignments"]'), '后台权限校验未完成');
    assert.ok(document.body.classList.contains('admin-panel-open'), '打开后台时锁住页面滚动');
    assert.ok(opener.hasAttribute('inert'), '后台打开时页面其余内容不可聚焦');
    overlay.querySelector('[data-view="rules"]').click();
    assert.equal(overlay.querySelector('[data-view="rules"]').getAttribute('aria-current'), 'page', '侧栏标记当前分区');
    const activeRuleTab = overlay.querySelector('.admin-rule-tabs [aria-selected="true"]');
    assert.equal(activeRuleTab?.getAttribute('role'), 'tab', '规则分区使用完整 tab 语义');
    holdNextPreview = true;
    overlay.querySelector('[data-action="save"]').click();
    await waitFor(() => savedBodies.length === 1, '保存现有系列规则未发请求');
    assert.equal(savedBodies[0].config.modules.ft_series_materials.cards.Eileen, '/custom/eileen.jpg');
    assert.equal(savedBodies[0].config.modules.fbif_footer, undefined);

    await waitFor(() => typeof releasePreview === 'function', '服务端已保存后未进入预览提交门');
    assert.match(overlay.querySelector('.admin-foot .admin-status')?.textContent || '', /处理中/,
      '预览尚未提交时不得提前宣称已应用');
    releasePreview({ previewSync: { status: 'failed', error: 'render failed' } });
    await waitFor(() => /规则已保存，但当前预览刷新失败/.test(overlay.querySelector('.admin-foot .admin-status')?.textContent || ''),
      '预览失败必须区分服务端已保存事实');
    overlay.querySelector('[data-action="new"]').click();
    overlay.querySelector('[data-action="save"]').click();
    await waitFor(() => savedBodies.length === 2, '保存新规则未发请求');
    const fresh = savedBodies[1].config.modules;
    assert.equal(savedBodies[1].config.heading_color, '#02396b');
    assert.deepEqual(fresh.bottom_blocks, emptyBlocks);
    assert.equal(savedBodies[1].config.modules.ft_series_materials.cards.Eileen, '/baseline/eileen.jpg');
    assert.deepEqual(savedBodies[1].behavior, siteBehavior);
    assert.equal(savedBodies[1].baseline_id, 'foodtalks-series-default');
    await waitFor(
      () => requests.filter(item => item === 'GET /api/rules/admin/presets').length >= 3 &&
        /\u5df2\u4fdd\u5b58\u5e76\u5e94\u7528/.test(overlay.querySelector('.admin-foot .admin-status')?.textContent || ''),
      '保存新规则后未完成回读与应用',
    );
    assert.equal(overlay.querySelector('[data-role="dirty-chip"]')?.textContent, '已保存', '保存回读后不应残留假未保存状态');
    overlay.querySelector('.admin-close').click();
    assert.ok(!document.body.classList.contains('admin-panel-open'), '关闭后恢复页面滚动');
    assert.ok(!opener.hasAttribute('inert'), '关闭后恢复页面交互');
    assert.equal(document.activeElement, opener, '关闭后焦点回到打开按钮');
  } finally {
    delete globalThis.fetch;
    delete globalThis.document;
    delete globalThis.window;
  }
});

test('待迁移规则后台只允许显式确认基线，不允许编辑后直接烘入占位默认', async () => {
  const { document, window } = parseHTML('<!doctype html><html><body><button id="adminLockBtn">规则</button></body></html>');
  globalThis.document = document;
  globalThis.window = window;
  window.confirm = () => true;
  window._activeRulePack = { id: 'fbif', channel: 'mp', templateId: 'fbif' };
  const baseline = { id: 'default', name: 'FBIF 基线', baseline_id: 'default', channel: 'mp', rule_family: 'fbif', config: DEFAULT_RULE_CONFIG };
  const legacy = {
    id: 'legacy-custom', name: '历史规则', baseline_id: 'default', channel: 'mp', rule_family: 'fbif',
    migration_required: true, migration_note: '无法推断历史基线', config: { font_size: '16' }, behavior: {},
  };
  let doc = { active_id: 'legacy-custom', presets: [baseline, legacy], accounts: [{ id: 'fbif', name: 'FBIF', active_id: 'legacy-custom' }] };
  const posts = [];
  globalThis.fetch = async (url, options = {}) => {
    const method = options.method || 'GET';
    if (String(url) === '/api/rules/admin/presets' && method === 'GET') return { ok: true, status: 200, json: async () => doc };
    if (String(url) === '/api/rules/admin/presets' && method === 'POST') {
      const body = JSON.parse(options.body);
      posts.push(body);
      doc = { ...doc, presets: [baseline, { ...legacy, migration_required: false, baseline_id: body.baseline_id }] };
      return { ok: true, status: 200, json: async () => doc };
    }
    throw new Error('unexpected fetch ' + method + ' ' + url);
  };

  try {
    const { initAdminPanel } = await import('../public/js/admin-panel.js?migration=' + Date.now());
    initAdminPanel({
      rulePacks: [window._activeRulePack],
      getConfig: () => legacy.config,
      getPreset: () => legacy,
      getBehavior: () => legacy.behavior,
      applyConfig(config, preset) { window._activeConfig = { config, preset }; },
    });
    document.getElementById('adminLockBtn').click();
    const overlay = document.body.lastElementChild;
    await waitFor(() => overlay.querySelector('[data-view="rules"]'), '后台权限校验未完成');
    overlay.querySelector('[data-view="rules"]').click();

    assert.ok(overlay.querySelector('[data-role="migration-baseline"]'));
    assert.equal(overlay.querySelector('[data-rule-key]'), null, '确认前不渲染可编辑参数表单');
    assert.equal(overlay.querySelector('[data-action="set-active"]'), null, '迁移规则不应提供提前应用入口');
    overlay.querySelector('[data-action="save"]').click();
    await waitFor(() => /请先选择/.test(overlay.querySelector('.admin-status')?.textContent || ''));
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(posts.length, 0);

    const baselineSelect = overlay.querySelector('[data-role="migration-baseline"]');
    baselineSelect.querySelector('option[value="default"]').setAttribute('selected', '');
    assert.equal(baselineSelect.value, 'default');
    overlay.querySelector('[data-action="save"]').click();
    await waitFor(() => posts.length === 1, () => 'migration POST 未发出，status=' + (overlay.querySelector('.admin-status')?.textContent || '<none>'));
    assert.equal(posts[0].baseline_id, 'default');
    assert.equal(posts[0].migration_confirmed, true);
    await waitFor(() => /基线已确认/.test(overlay.innerHTML), '迁移状态未完成');
  } finally {
    delete globalThis.fetch;
    delete globalThis.document;
    delete globalThis.window;
  }
});

test('保存或另存 POST 在途中关闭并切账号，已落库规则不得串绑新账号', async () => {
  async function exercise(action) {
    const { document, window } = parseHTML('<!doctype html><html><body><button id="adminLockBtn">规则</button></body></html>');
    globalThis.document = document;
    globalThis.window = window;
    window.confirm = () => true;
    const packA = { id: 'account-a', channel: 'mp', templateId: 'fbif', accountName: 'A' };
    const packB = { id: 'account-b', channel: 'mp', templateId: 'fbif', accountName: 'B' };
    window._activeRulePack = packA;
    const ruleA = {
      id: 'rule-a', name: 'A 规则', baseline_id: 'default', channel: 'mp', rule_family: 'fbif',
      config: DEFAULT_RULE_CONFIG, behavior: {},
    };
    const ruleB = {
      id: 'rule-b', name: 'B 规则', baseline_id: 'default', channel: 'mp', rule_family: 'fbif',
      config: DEFAULT_RULE_CONFIG, behavior: {},
    };
    let doc = {
      presets: [
        { id: 'default', name: '基线', baseline_id: 'default', channel: 'mp', rule_family: 'fbif', config: DEFAULT_RULE_CONFIG },
        ruleA,
        ruleB,
      ],
      accounts: [
        { id: 'account-a', name: 'A', active_id: 'rule-a' },
        { id: 'account-b', name: 'B', active_id: 'rule-b' },
      ],
    };
    let resolvePost;
    let postJSONRead = false;
    const activeWrites = [];
    globalThis.fetch = async (url, options = {}) => {
      const method = options.method || 'GET';
      if (String(url) === '/api/rules/admin/presets' && method === 'GET') return { ok: true, status: 200, json: async () => doc };
      if (String(url) === '/api/rules/admin/presets' && method === 'POST') {
        const body = JSON.parse(options.body);
        return new Promise(resolve => {
          resolvePost = () => {
            doc = { ...doc, presets: doc.presets.filter(p => p.id !== body.id).concat({ ...body, channel: 'mp', rule_family: 'fbif' }) };
            resolve({ ok: true, status: 200, json: async () => { postJSONRead = true; return doc; } });
          };
        });
      }
      if (String(url) === '/api/rules/admin/active' && method === 'PUT') {
        activeWrites.push(JSON.parse(options.body));
        return { ok: true, status: 200, json: async () => doc };
      }
      throw new Error('unexpected fetch ' + method + ' ' + url);
    };

    try {
      const { initAdminPanel } = await import('../public/js/admin-panel.js?account-race=' + action + '-' + Date.now());
      initAdminPanel({
        rulePacks: [packA, packB],
        getConfig: () => ruleA.config,
        getPreset: () => ruleA,
        getBehavior: () => ruleA.behavior,
        applyConfig(config, preset) { window._activeConfig = { config, preset }; },
      });
      document.getElementById('adminLockBtn').click();
      let overlay = document.body.lastElementChild;
      await waitFor(() => overlay.querySelector('[data-view="rules"]'), '后台权限校验未完成');
      overlay.querySelector('[data-view="rules"]').click();
      overlay.querySelector(`[data-action="${action}"]`).click();
      await waitFor(() => typeof resolvePost === 'function', action + ' 未发出 POST');

      overlay.querySelector('.admin-close').click();
      assert.equal(document.querySelector('.admin-overlay'), null, '面板应已关闭');
      window._activeRulePack = packB;
      resolvePost();
      await waitFor(() => postJSONRead, 'POST 成功响应未被读取');
      for (let i = 0; i < 8; i++) await Promise.resolve();

      assert.deepEqual(activeWrites, [], '旧 A 操作不得在后返时将规则 PUT 绑定到 B');
      document.getElementById('adminLockBtn').click();
      overlay = document.body.lastElementChild;
      await waitFor(
        () => /规则已保存，但未应用.*当前账号已变化/.test(overlay.querySelector('.admin-foot .admin-status')?.textContent || ''),
        () => 'status=' + (overlay.querySelector('.admin-foot .admin-status')?.textContent || '<empty>') + '; html=' + overlay.outerHTML.slice(0, 500),
      );
      assert.match(
        overlay.querySelector('.admin-foot .admin-status')?.textContent || '',
        /规则已保存，但未应用.*当前账号已变化/,
        'POST 已成功时必须说明服务端事实，不得假装整体未保存',
      );
    } finally {
      delete globalThis.fetch;
      delete globalThis.document;
      delete globalThis.window;
    }
  }

  await exercise('save');
  await exercise('save-as');
});
