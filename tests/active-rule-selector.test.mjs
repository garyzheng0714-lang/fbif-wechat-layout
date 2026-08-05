// 契约测试：active-rule-selector.js —— 顶部「当前规则」选择器（外壳共享模块，S1）。
// 意图：这一族此前在 app.html/site.html 各复制一份且已分叉、零行为覆盖（架构体检
// 认定的最大裸奔区之一）。本测试用真实 DOM（linkedom）锁定：两种渠道只读视图口径、
// 编辑-保存状态机、服务端拒绝反馈、server 预设落本地缓存的渠道变换——任何人改共享
// 实现导致两页行为漂移时，必须先让这里红。
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

const { createActiveRuleSelector, escText } = await import('../public/js/active-rule-selector.js');

function makeEnv({
  readonlyEditEntry = false,
  readonlyLabel = '当前排版规则',
  source = 'server',
  fallbackTransform,
  pack = { id: 'fbif', channel: 'mp', templateId: 'fbif' },
  currentPreset,
  refreshRuleConsumers: refreshRuleConsumersOverride,
  setActiveRulePreset: setActiveRulePresetOverride,
} = {}) {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="activeRuleCard"></div></body></html>');
  const initialPreset = currentPreset || { id: 'default', name: 'FBIF默认规则', channel: 'mp', config: { a: 1 } };
  window._activeConfig = { preset: initialPreset, config: initialPreset.config };
  const calls = { saveLocal: [], saveLocalPreset: [], setActive: [], applied: [] };
  const ruleModule = {
    loadActiveRulePreset: async () => ({ source: 'server', preset: { id: 'rule-x', name: '新规则', config: { b: 2 } } }),
    loadLocalRuleConfig: () => ({ local: true }),
    loadLocalRulePreset: () => null,
    saveLocalRuleConfig: (config, acct) => calls.saveLocal.push({ config, acct }),
    saveLocalRulePreset: (preset, acct) => calls.saveLocalPreset.push({ preset, acct }),
    applyActiveRuleConfig: (config, preset) => calls.applied.push({ config, preset }),
    listPublicRulePresets: async () => ({ presets: [
      { id: 'default', name: 'FBIF默认规则', channel: 'mp', rule_family: 'fbif' },
      { id: 'rule-x', name: '新规则', channel: 'mp', rule_family: 'fbif' },
      { id: 'legacy-ambiguous', name: '待迁移规则', channel: 'mp', rule_family: 'fbif', migration_required: true },
      { id: 'site-rule', name: '网站规则', channel: 'ft', rule_family: 'foodtalks-site' },
    ], accounts: [
      { id: 'fbif', active_revision: 7 },
      { id: 'ft-default', active_revision: 9 },
    ] }),
    setActiveRulePreset: setActiveRulePresetOverride || (async (id, acct, revision) => calls.setActive.push({ id, acct, revision })),
  };
  // 页面真实实现里 refreshRuleConsumers 会重渲选择器卡片（saveActiveRuleSelection
  // 成功路径依赖它），桩保持同一契约。
  let selectorRef = null;
  const selector = createActiveRuleSelector({
    documentRef: document,
    windowRef: window,
    getPack: () => pack,
    ruleModule,
    refreshRuleConsumers: options => {
      if (selectorRef) selectorRef.renderActiveRuleCard();
      return refreshRuleConsumersOverride ? refreshRuleConsumersOverride(options) : undefined;
    },
    fallbackTransform,
    readonlyLabel,
    readonlyEditEntry,
    initialSource: source,
    initialPresetID: 'default',
  });
  selectorRef = selector;
  return { document, window, selector, calls };
}

test('公众号口径只读视图：纯展示，无锁定徽章无编辑按钮', () => {
  const { document, selector } = makeEnv({ readonlyEditEntry: false, readonlyLabel: '当前排版规则' });
  selector.renderActiveRuleCard();
  const card = document.getElementById('activeRuleCard');
  assert.match(card.innerHTML, /当前排版规则/);
  assert.match(card.innerHTML, /FBIF默认规则/);
  assert.ok(!card.querySelector('[data-action="edit-active-rule"]'), '公众号只读态不应有编辑按钮');
  assert.ok(!/已锁定/.test(card.innerHTML), '公众号只读态不应有「已锁定」徽章');
});

test('网站口径只读视图：「已锁定」徽章 + 铅笔编辑入口，点击进入编辑态', async () => {
  const { document, selector } = makeEnv({ readonlyEditEntry: true, readonlyLabel: '规则' });
  selector.renderActiveRuleCard();
  const card = document.getElementById('activeRuleCard');
  assert.match(card.innerHTML, /已锁定/);
  const editBtn = card.querySelector('[data-action="edit-active-rule"]');
  assert.ok(editBtn, '网站只读态必须有编辑入口');
  await selector.beginActiveRuleEdit();
  assert.match(card.innerHTML, /编辑中/);
  assert.ok(card.querySelector('[data-action="save-active-rule"]'));
  assert.ok(card.querySelector('[data-action="cancel-active-rule"]'));
});

test('规则菜单按当前渠道过滤，网站账号看不到公众号 preset', async () => {
  const { document, selector } = makeEnv({
    readonlyEditEntry: true,
    pack: { id: 'ft-default', channel: 'ft', templateId: 'foodtalks-site' },
    currentPreset: { id: 'site-rule', name: '网站规则', channel: 'ft', config: {} },
  });
  await selector.beginActiveRuleEdit();
  document.querySelector('#activeRuleSelect').click();
  assert.ok(document.querySelector('[data-rule-option="site-rule"]'));
  assert.equal(document.querySelector('[data-rule-option="default"]'), null);
  assert.equal(document.querySelector('[data-rule-option="rule-x"]'), null);
});

test('待确认基线的历史规则不进入前台绑定菜单', async () => {
  const { document, selector } = makeEnv({ readonlyEditEntry: true });
  await selector.beginActiveRuleEdit();
  document.querySelector('#activeRuleSelect').click();
  assert.equal(document.querySelector('[data-rule-option="legacy-ambiguous"]'), null);
  assert.ok(document.querySelector('[data-rule-option="rule-x"]'));
});

test('保存流程：选新规则 → setActiveRulePreset → 回只读并显示已保存', async () => {
  const { document, window, selector, calls } = makeEnv({ readonlyEditEntry: true });
  await selector.beginActiveRuleEdit();
  // 直接走保存路径（draft 换为 rule-x）
  const card = document.getElementById('activeRuleCard');
  // 菜单未展开时选项不渲染——通过触发器展开
  card.querySelector('#activeRuleSelect').dispatchEvent(new window.Event('click', { bubbles: true }));
  const ruleX = document.querySelector('[data-rule-option="rule-x"]');
  assert.ok(ruleX, '展开后应有 rule-x 选项');
  ruleX.dispatchEvent(new window.Event('click', { bubbles: true }));
  const save = document.querySelector('[data-action="save-active-rule"]');
  assert.ok(!save.hasAttribute('disabled'), '选了不同规则后保存应可用');
  save.dispatchEvent(new window.Event('click', { bubbles: true }));
  await new Promise(r => setTimeout(r, 20));
  assert.deepEqual(calls.setActive[0], { id: 'rule-x', acct: 'fbif', revision: 7 });
  assert.match(document.getElementById('activeRuleCard').innerHTML, /已保存/);
});

test('规则保存必须等预览提交后才离开保存中', async () => {
  let releasePreview;
  const preview = new Promise(resolve => { releasePreview = resolve; });
  const { document, window, selector, calls } = makeEnv({
    readonlyEditEntry: true,
    refreshRuleConsumers: () => preview,
  });
  await selector.beginActiveRuleEdit();
  document.querySelector('#activeRuleSelect').click();
  document.querySelector('[data-rule-option="rule-x"]').click();
  document.querySelector('[data-action="save-active-rule"]').click();
  for (let i = 0; i < 6; i++) await Promise.resolve();

  assert.equal(calls.setActive.length, 1, '服务端绑定已完成');
  assert.match(document.getElementById('activeRuleCard').textContent, /保存中/);
  assert.doesNotMatch(document.getElementById('activeRuleCard').textContent, /已保存/);

  releasePreview({ status: 'committed' });
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.match(document.getElementById('activeRuleCard').textContent, /已保存/);
  assert.doesNotMatch(document.getElementById('activeRuleCard').textContent, /保存中/);
});

test('服务端规则已保存但预览失败时，不得伪装整体回滚或笼统显示已保存', async () => {
  const { document, window, selector, calls } = makeEnv({
    readonlyEditEntry: true,
    refreshRuleConsumers: async () => ({ status: 'failed', error: 'render failed' }),
  });
  await selector.beginActiveRuleEdit();
  document.querySelector('#activeRuleSelect').click();
  document.querySelector('[data-rule-option="rule-x"]').click();
  document.querySelector('[data-action="save-active-rule"]').click();
  await new Promise(resolve => setTimeout(resolve, 0));

  assert.equal(calls.setActive.length, 1, '预览失败不得否认服务端已保存的事实');
  assert.match(document.getElementById('activeRuleCard').textContent, /规则已保存，但当前预览刷新失败/);
});

test('服务端拒绝规则绑定时显示权限错误且不伪装成已保存', async () => {
  const { document, window, selector } = makeEnv({
    readonlyEditEntry: true,
    setActiveRulePreset: async () => { throw new Error('当前账号没有规则后台权限'); },
  });
  await selector.beginActiveRuleEdit();
  window._activeConfig.preset.id = 'default';
  const card = document.getElementById('activeRuleCard');
  card.querySelector('#activeRuleSelect').dispatchEvent(new window.Event('click', { bubbles: true }));
  document.querySelector('[data-rule-option="rule-x"]').dispatchEvent(new window.Event('click', { bubbles: true }));
  document.querySelector('[data-action="save-active-rule"]').dispatchEvent(new window.Event('click', { bubbles: true }));
  await new Promise(r => setTimeout(r, 20));
  assert.match(document.getElementById('activeRuleCard').innerHTML, /当前账号没有规则后台权限/);
  assert.doesNotMatch(document.getElementById('activeRuleCard').innerHTML, /已保存/);
});

test('server 预设落本地缓存经过渠道 fallbackTransform（公众号 structuredFallbackConfig 口径）', async () => {
  const transform = (config, preset) => (preset && preset.config) || config;
  const { selector, calls } = makeEnv({ fallbackTransform: transform });
  await selector.hydrateActiveRulePreset('fbif');
  assert.equal(calls.saveLocalPreset.length, 1);
  assert.deepEqual(calls.saveLocalPreset[0], {
    preset: { id: 'rule-x', name: '新规则', config: { b: 2 } },
    acct: 'fbif',
  });
});

test('网站首屏本地规则携带账号身份，离线时也不走公众号 behavior', () => {
  const { selector } = makeEnv();
  const local = selector.localActiveRulePreset('ft-default');
  assert.equal(local.rule_account_id, 'ft-default');
  assert.equal(local.id, 'local');
});

test('adoptAccount：并行水合+目录并复位选择器状态（账号切换契约）', async () => {
  const { document, selector } = makeEnv({ readonlyEditEntry: true });
  await selector.beginActiveRuleEdit();
  await selector.adoptAccount({ rerender: false });
  assert.equal(selector.getSource(), 'server');
  selector.renderActiveRuleCard();
  assert.ok(!/编辑中/.test(document.getElementById('activeRuleCard').innerHTML), '切账号后选择器必须退出编辑态');
});

test('adoptAccount：A→B 快速切换且 A 后返时，旧 A 不 apply 也不缓存到 B', async () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="activeRuleCard"></div></body></html>');
  let accountID = 'account-a';
  const pendingLoads = new Map();
  const calls = { applied: [], saved: [], hydrated: [], refreshed: [] };
  const defer = () => {
    let resolve;
    const promise = new Promise(r => { resolve = r; });
    return { promise, resolve };
  };
  const ruleModule = {
    loadActiveRulePreset(id) {
      const task = defer();
      pendingLoads.set(id, task);
      return task.promise;
    },
    loadLocalRuleConfig: () => ({}),
    saveLocalRuleConfig: (config, id) => calls.saved.push({ config, id }),
    applyActiveRuleConfig: (config, preset) => {
      calls.applied.push(preset.id);
      window._activeConfig = { config, preset };
    },
    listPublicRulePresets: async () => ({ presets: [] }),
    setActiveRulePreset: async () => {},
  };
  const selector = createActiveRuleSelector({
    documentRef: document,
    windowRef: window,
    getPack: () => ({ id: accountID }),
    ruleModule,
    refreshRuleConsumers: options => calls.refreshed.push(options),
    afterHydrate: pack => calls.hydrated.push(pack.id),
  });

  const adoptA = selector.adoptAccount({ rerender: false });
  accountID = 'account-b';
  const adoptB = selector.adoptAccount({ rerender: true });

  pendingLoads.get('account-b').resolve({
    source: 'server',
    preset: { id: 'rule-b', name: 'Rule B', config: { owner: 'B' } },
  });
  const resultB = await adoptB;
  pendingLoads.get('account-a').resolve({
    source: 'server',
    preset: { id: 'rule-a', name: 'Rule A', config: { owner: 'A' } },
  });
  const resultA = await adoptA;

  assert.equal(resultB.stale, false);
  assert.equal(resultB.accountID, 'account-b');
  assert.equal(resultA.stale, true, '后返的 A 必须显式标记为 stale');
  assert.equal(resultA.accountID, 'account-a', '返回值保留任务启动时的账号');
  assert.deepEqual(calls.applied, ['rule-b']);
  assert.deepEqual(calls.saved, [{ config: { owner: 'B' }, id: 'account-b' }]);
  assert.deepEqual(calls.hydrated, ['account-b']);
  assert.deepEqual(calls.refreshed, [{ rerender: true }]);
});

test('hydrate guard：A→B→A 后，最早的 A 响应不得覆盖新 A', async () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="activeRuleCard"></div></body></html>');
  let accountID = 'account-a';
  const loads = [];
  const applied = [];
  const defer = () => {
    let resolve;
    const promise = new Promise(r => { resolve = r; });
    return { promise, resolve };
  };
  const selector = createActiveRuleSelector({
    documentRef: document,
    windowRef: window,
    getPack: () => ({ id: accountID }),
    ruleModule: {
      loadActiveRulePreset(id) {
        const task = defer();
        loads.push({ id, ...task });
        return task.promise;
      },
      loadLocalRuleConfig: () => ({}),
      saveLocalRuleConfig: () => {},
      applyActiveRuleConfig(config, preset) {
        applied.push(preset.id);
        window._activeConfig = { config, preset };
      },
      listPublicRulePresets: async () => ({ presets: [] }),
      setActiveRulePreset: async () => {},
    },
    refreshRuleConsumers() {},
  });

  const initialA = selector.hydrateActiveRulePreset('account-a');
  accountID = 'account-b';
  selector.adoptAccount({ rerender: false });
  accountID = 'account-a';
  const latestA = selector.adoptAccount({ rerender: false });

  loads[2].resolve({ source: 'server', preset: { id: 'new-a', config: { version: 2 } } });
  await latestA;
  loads[0].resolve({ source: 'server', preset: { id: 'old-a', config: { version: 1 } } });
  await initialA;

  assert.deepEqual(applied, ['new-a']);
  assert.equal(window._activeConfig.preset.id, 'new-a');
});

test('保存 guard：A 保存后切 B，旧 A 后返不得 apply 或缓存到 B', async () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="activeRuleCard"></div></body></html>');
  let accountID = 'account-a';
  window._activeConfig = { preset: { id: 'default', name: 'Default', config: {} }, config: {} };
  const defer = () => {
    let resolve;
    const promise = new Promise(r => { resolve = r; });
    return { promise, resolve };
  };
  const setTask = defer();
  const loads = [];
  const calls = { applied: [], saved: [], setActive: [] };
  let selector;
  selector = createActiveRuleSelector({
    documentRef: document,
    windowRef: window,
    getPack: () => ({ id: accountID }),
    ruleModule: {
      loadActiveRulePreset(id) {
        const task = defer();
        loads.push({ id, ...task });
        return task.promise;
      },
      loadLocalRuleConfig: () => ({}),
      saveLocalRuleConfig: (config, id) => calls.saved.push({ owner: config.owner, id }),
      applyActiveRuleConfig(config, preset) {
        calls.applied.push({ rule: preset.id, account: accountID });
        window._activeConfig = { config, preset };
      },
      listPublicRulePresets: async () => ({ presets: [{ id: 'default' }, { id: 'rule-x' }], accounts: [{ id: 'account-a', active_revision: 11 }] }),
      setActiveRulePreset: async (...args) => { calls.setActive.push(args); return setTask.promise; },
    },
    refreshRuleConsumers: () => selector.renderActiveRuleCard(),
  });
  selector.renderActiveRuleCard();
  await selector.beginActiveRuleEdit();
  document.querySelector('#activeRuleSelect').click();
  document.querySelector('[data-rule-option="rule-x"]').click();
  document.querySelector('[data-action="save-active-rule"]').click();
  for (let i = 0; i < 4; i++) await Promise.resolve();
  setTask.resolve({});
  for (let i = 0; i < 4; i++) await Promise.resolve();
  assert.equal(loads[0].id, 'account-a');

  accountID = 'account-b';
  const adoptB = selector.adoptAccount({ rerender: false });
  loads.find(load => load.id === 'account-b').resolve({
    source: 'server', preset: { id: 'rule-b', config: { owner: 'B' } },
  });
  await adoptB;
  loads.find(load => load.id === 'account-a').resolve({
    source: 'server', preset: { id: 'rule-a', config: { owner: 'A' } },
  });
  await new Promise(resolve => setTimeout(resolve, 0));

  assert.deepEqual(calls.applied, [{ rule: 'rule-b', account: 'account-b' }]);
  assert.deepEqual(calls.saved, [{ owner: 'B', id: 'account-b' }]);
  assert.equal(window._activeConfig.preset.id, 'rule-b');
  assert.deepEqual(calls.setActive[0], ['rule-x', 'account-a', 11], '保存请求必须固定启动时账号及绑定修订');
});

test('保存 single-flight：保存中再点击不得发第二个 PUT', async () => {
  const { document, window, selector, calls } = makeEnv({ readonlyEditEntry: true });
  await selector.beginActiveRuleEdit();
  document.querySelector('#activeRuleSelect').click();
  document.querySelector('[data-rule-option="rule-x"]').click();
  const first = document.querySelector('[data-action="save-active-rule"]');
  first.click();
  const second = document.querySelector('[data-action="save-active-rule"]');
  if (second) second.click();
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(calls.setActive.length, 1);
});

test('保存中取消必须禁用，PUT 成功后仍水合并应用新规则', async () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="activeRuleCard"></div></body></html>');
  window._activeConfig = { preset: { id: 'default', name: '默认', channel: 'mp', config: {} }, config: {} };
  let release;
  const pendingPut = new Promise(resolve => { release = resolve; });
  const applied = [];
  let selector;
  selector = createActiveRuleSelector({
    documentRef: document,
    windowRef: window,
    getPack: () => ({ id: 'fbif', channel: 'mp', templateId: 'fbif' }),
    ruleModule: {
      loadActiveRulePreset: async () => ({ source: 'server', preset: { id: 'rule-x', name: 'X', channel: 'mp', config: {} } }),
      loadLocalRuleConfig: () => ({}), saveLocalRuleConfig: () => {},
      applyActiveRuleConfig(config, preset) { applied.push(preset.id); window._activeConfig = { config, preset }; },
      listPublicRulePresets: async () => ({ presets: [{ id: 'default', rule_family: 'fbif' }, { id: 'rule-x', rule_family: 'fbif' }] }),
      setActiveRulePreset: () => pendingPut,
    },
    refreshRuleConsumers: () => selector.renderActiveRuleCard(),
    readonlyEditEntry: true,
  });
  selector.renderActiveRuleCard();
  await selector.beginActiveRuleEdit();
  document.querySelector('#activeRuleSelect').click();
  document.querySelector('[data-rule-option="rule-x"]').click();
  document.querySelector('[data-action="save-active-rule"]').click();
  for (let i = 0; i < 4; i++) await Promise.resolve();
  assert.ok(document.querySelector('[data-action="cancel-active-rule"]').hasAttribute('disabled'));
  assert.equal(selector.cancelActiveRuleEdit(), false);
  release({});
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(applied, ['rule-x']);
  assert.equal(window._activeConfig.preset.id, 'rule-x');
});

test('保存 PUT 成功后并发刷新目录，不得把保存事务误判 stale 卡在保存中', async () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="activeRuleCard"></div></body></html>');
  window._activeConfig = { preset: { id: 'default', name: '默认', channel: 'mp', config: {} }, config: {} };
  const defer = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
  const activeLoad = defer();
  const saveCatalog = defer();
  const refreshCatalog = defer();
  let catalogCalls = 0;
  const applied = [];
  let selector;
  selector = createActiveRuleSelector({
    documentRef: document,
    windowRef: window,
    getPack: () => ({ id: 'fbif', channel: 'mp', templateId: 'fbif' }),
    ruleModule: {
      loadActiveRulePreset: () => activeLoad.promise,
      loadLocalRuleConfig: () => ({}),
      saveLocalRuleConfig: () => {},
      applyActiveRuleConfig(config, preset) { applied.push(preset.id); window._activeConfig = { config, preset }; },
      setActiveRulePreset: async () => {},
      listPublicRulePresets() {
        catalogCalls++;
        if (catalogCalls === 1) return Promise.resolve({ presets: [{ id: 'default', rule_family: 'fbif' }, { id: 'rule-x', rule_family: 'fbif' }] });
        return catalogCalls === 2 ? saveCatalog.promise : refreshCatalog.promise;
      },
    },
    refreshRuleConsumers: () => selector.renderActiveRuleCard(),
    readonlyEditEntry: true,
  });
  selector.renderActiveRuleCard();
  await selector.beginActiveRuleEdit();
  document.querySelector('#activeRuleSelect').click();
  document.querySelector('[data-rule-option="rule-x"]').click();
  document.querySelector('[data-action="save-active-rule"]').click();
  for (let i = 0; i < 4; i++) await Promise.resolve();
  const refresh = selector.refreshActiveRuleCatalog('fbif');
  refreshCatalog.resolve({ presets: [{ id: 'rule-newer', rule_family: 'fbif' }] });
  await refresh;
  saveCatalog.resolve({ presets: [{ id: 'rule-x', rule_family: 'fbif' }] });
  activeLoad.resolve({ source: 'server', preset: { id: 'rule-x', name: 'X', channel: 'mp', config: {} } });
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(applied, ['rule-x']);
  assert.match(document.getElementById('activeRuleCard').textContent, /已保存/);
  assert.doesNotMatch(document.getElementById('activeRuleCard').textContent, /保存中/);
});

test('begin edit 后切账号：旧目录后返不得让新账号重新进入编辑态', async () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="activeRuleCard"></div></body></html>');
  let accountID = 'account-a';
  window._activeConfig = { preset: { id: 'default', name: 'Default', config: {} }, config: {} };
  const catalogLoads = [];
  const defer = () => {
    let resolve;
    const promise = new Promise(r => { resolve = r; });
    return { promise, resolve };
  };
  const selector = createActiveRuleSelector({
    documentRef: document,
    windowRef: window,
    getPack: () => ({ id: accountID }),
    ruleModule: {
      loadActiveRulePreset: async id => ({ source: 'server', preset: { id: 'rule-' + id, config: {} } }),
      loadLocalRuleConfig: () => ({}), saveLocalRuleConfig: () => {},
      applyActiveRuleConfig(config, preset) { window._activeConfig = { config, preset }; },
      listPublicRulePresets() {
        const task = defer();
        catalogLoads.push(task);
        return task.promise;
      },
      setActiveRulePreset: async () => {},
    },
    refreshRuleConsumers() {},
    readonlyEditEntry: true,
  });
  selector.renderActiveRuleCard();
  const oldBegin = selector.beginActiveRuleEdit();
  accountID = 'account-b';
  const adoptB = selector.adoptAccount({ rerender: false });
  catalogLoads[1].resolve({ presets: [{ id: 'rule-account-b' }] });
  await adoptB;
  catalogLoads[0].resolve({ presets: [{ id: 'old-a' }] });
  await oldBegin;
  selector.renderActiveRuleCard();
  assert.doesNotMatch(document.getElementById('activeRuleCard').innerHTML, /编辑中/);
});

test('escText 转义 HTML 敏感字符', () => {
  assert.equal(escText('<a b="c">&'), '&lt;a b=&quot;c&quot;&gt;&amp;');
  assert.equal(escText(null), '');
});
