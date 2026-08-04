import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

function extractFunction(source, name) {
  const functionStart = source.indexOf(`function ${name}`);
  assert.notEqual(functionStart, -1, `${name} should exist`);
  const start = source.slice(functionStart - 6, functionStart) === 'async ' ? functionStart - 6 : functionStart;
  const signatureEnd = source.indexOf(') {', start);
  assert.notEqual(signatureEnd, -1, `${name} signature should be complete`);
  const bodyStart = signatureEnd + 2;
  let depth = 0;
  for (let i = bodyStart; i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}') depth--;
    if (depth === 0) return source.slice(start, i + 1);
  }
  assert.fail(`${name} function body should be complete`);
}

function deferred() {
  let resolve;
  const promise = new Promise(r => { resolve = r; });
  return { promise, resolve };
}

test('app shell renders one publishing-account menu inside the import card', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/app.css', import.meta.url), 'utf8');
  const channelBarCss = await readFile(new URL('../public/css/channel-bar.css', import.meta.url), 'utf8');
  const menuJs = await readFile(new URL('../public/js/account-menu.js', import.meta.url), 'utf8');
  const allCss = css + '\n' + channelBarCss;

  // 单层发布账号菜单：住在左栏导入卡第一行——工具贴着它作用的对象（这次导入去哪个账号）。
  // 顶栏只留当前页面动作；登录身份在左栏底部。渲染逻辑仍在 account-menu.js，挂载点仍是唯一的 #acctMenu。
  assert.match(app, /id="acctMenu"/);
  assert.match(app, /mountAccountMenu/);
  assert.match(app, /account-menu\.js/);
  const headerStart = app.indexOf('<header class="global-channel-bar">');
  const headerEnd = app.indexOf('</header>', headerStart);
  const header = app.slice(headerStart, headerEnd);
  const visibleShell = app.slice(0, app.indexOf('<script type="module">'));
  const dropZoneAt = app.indexOf('id="dropZone"');
  const composerInputAt = app.indexOf('data-url-focus-target');
  const accountAt = app.indexOf('id="acctMenu"');
  assert.doesNotMatch(header, /id="userIdentity"/, '顶栏不再承载登录身份');
  assert.doesNotMatch(header, /id="acctMenu"/, '顶栏不再承载发布账号菜单');
  assert.match(app, /class="import-destination-label">排版到</, '导入卡应说明「排版到」这层关系');
  assert.ok(accountAt > dropZoneAt && accountAt < composerInputAt, '公众号菜单应是导入卡的第一行');
  assert.doesNotMatch(app, /captureArticleTab|_articleTabs|mountArticleTabs/, '账号切换不应再绑定文章标签页状态');
  assert.doesNotMatch(header, /FBIF文章排版工具|class="app-brand-logo"|class="app-brand"/, '全局顶栏不应展示产品身份文案');
  assert.match(header, /class="topbar-spacer"[\s\S]*id="previewToolbar"[\s\S]*id="copyBtn"/, '顶栏只剩当前页面动作，复制全文靠右');
  assert.doesNotMatch(header, /id="adminLockBtn"/, '设置入口不应常驻顶栏');
  assert.match(app, /settingsAction:\s*\{[\s\S]*id:\s*'adminLockBtn'[\s\S]*label:\s*'规则设置'/, '设置入口应收进账号菜单底部');
  assert.doesNotMatch(header, /id="articleRefreshBtn"/, '刷新按钮不应出现在顶栏');
  // 旧的两层切换器残骸必须清掉
  assert.doesNotMatch(app, /id="accountSwitcher"/, '旧侧栏方角分段已移除');
  assert.doesNotMatch(app, /class="channel-switcher"/, '旧顶部圆形渠道胶囊已移除');
  // account-menu.js 契约：按渠道分组、列出全部账号、标记当前账号
  assert.match(menuJs, /listRulePacks/);
  assert.match(menuJs, /CHANNEL_LABEL/);
  assert.match(menuJs, /is-active/);
  assert.match(menuJs, /setActive/);
  assert.match(menuJs, /acct-menu-divider/);
  assert.match(menuJs, /data-acct-settings-action/);
  assert.match(app, /switchAccount/);
  // S1(2026-07-10)：账号切换的「水合+拉目录+复位选择器」收编进共享模块
  // active-rule-selector.js（行为契约见 tests/active-rule-selector.test.mjs 的
  // adoptAccount 测试）；app.html 只需证明它把切换委托给了共享模块。
  assert.match(app, /ruleSelector\.adoptAccount\(/);
  assert.match(app, /hydrateActiveRulePreset\(initialHydrationAccountID\)/);
  assert.doesNotMatch(visibleShell, /class="sidebar-footer"/, '左侧底部不再显示当前规则区');
  assert.doesNotMatch(visibleShell, /id="activeRuleCard"/, '当前规则卡片不应再出现在左侧底部');
  assert.doesNotMatch(visibleShell, /当前排版规则/, '左侧底部不再展示当前规则文案');
  // S1(2026-07-10)：规则选择器 UI 本体收编进共享模块 active-rule-selector.js
  // （交互契约由 tests/active-rule-selector.test.mjs 行为测试守护）。这里守两件事：
  // ① app.html 确实委托共享模块且用公众号口径（只读态无编辑入口）；
  // ② 共享模块保持自定义触发器（非原生 select）与保存/取消动作结构。
  assert.match(app, /active-rule-selector\.js/);
  assert.match(app, /renderActiveRuleCard/);
  assert.match(app, /readonlyEditEntry:\s*false/, '公众号只读态不应有编辑入口（规则编辑走后台）');
  const selectorJs = await readFile(new URL('../public/js/active-rule-selector.js', import.meta.url), 'utf8');
  assert.match(selectorJs, /id="activeRuleSelect"/);
  assert.doesNotMatch(selectorJs, /<select id="activeRuleSelect"/);
  assert.match(selectorJs, /data-action="save-active-rule"/);
  assert.match(selectorJs, /data-action="cancel-active-rule"/);
  assert.match(selectorJs, /data-rule-option/);
  assert.match(selectorJs, /listPublicRulePresets/);
  assert.match(selectorJs, /const accountID = getPack\(\)\.id[\s\S]{0,1200}setActiveRulePreset\(nextID,\s*accountID,\s*activeBindingRevision\(accountID\)\)/,
    '保存必须固定启动时账号与绑定修订，不得在 await 后重读 getPack');
  assert.doesNotMatch(app, /id="settingsEditBtn"/);
  assert.doesNotMatch(app, /id="settingsForm"/);
  assert.match(allCss, /\.acct-trigger/);
  assert.match(allCss, /\.acct-panel/);
  assert.match(allCss, /\.acct-item\.is-active/);
  assert.match(allCss, /\.acct-settings-item/);
  assert.doesNotMatch(css, /\.sidebar-footer\s+\.active-rule-card/);
  assert.doesNotMatch(css, /\.rail-icon-btn/);
  assert.match(css, /\.active-rule-card\.editing/);
  assert.match(css, /\.active-rule-readline/);
  assert.match(css, /\.active-rule-readvalue/);
  assert.match(css, /\.active-rule-select-wrap/);
  assert.match(css, /\.active-rule-trigger:disabled/);
  assert.match(css, /\.active-rule-menu/);
  assert.match(css, /\.active-rule-option/);
  assert.match(css, /\.active-rule-icon/);
  assert.match(css, /\.active-rule-save/);
  assert.match(css, /\.active-rule-cancel/);
});

test('app shell prefers pathname over query for initial rule pack selection', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const editorShell = await readFile(new URL('../public/js/editor-shell.js', import.meta.url), 'utf8');
  // 必须 import 新的 path 解析器（绝对路径）
  assert.match(app, /import\(['"]\/js\/rule-packs\/url\.js/);
  // 入口页只负责接线；路径优先和旧 ?account 兼容统一由 editor-shell 承担。
  assert.match(app, /resolveInitialRulePackID/);
  assert.match(editorShell, /parsePath\s*\(\s*loc\.pathname/);
  assert.match(editorShell, /getRulePackIDFromLocation/);
});

test('app.html writes path-based URL when switching rule packs', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  // 必须用 buildPath 构造新 URL
  assert.match(app, /buildPath\s*\(/);
  // rule-packs/url.js 已 import（Task 5 引入，Task 6 复用）
  assert.match(app, /rule-packs\/url\.js/);
  // 切换分支必须先判断 parsePath !== null
  assert.match(app, /rulePackUrlModule\.parsePath\(\s*location\.pathname\s*\)/);
});

test('app shell uses absolute /js/ paths for dynamic imports (path-safe under /mp/...)', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  // 不允许任何相对路径的 dynamic import（会在 /mp/fbif 下被解析为 /mp/js/...）
  const matches = app.match(/import\(['"]\.\//g);
  assert.equal(matches, null, 'app.html should not contain relative "./..." dynamic imports');
  // 至少要有几个绝对路径 /js/ import 在
  assert.match(app, /import\(['"]\/js\/engine\.js/);
  assert.match(app, /import\(['"]\/js\/rule-packs\/registry\.js/);
});

test('mp accounts hot-swap templates instead of reloading the page', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  // WHY：本地 DOCX 图片是当前页面创建的 blob: URL；同渠道换账号若整页 reload，
  // 新页面只能恢复失效的 blob 字符串，预览里的图片会全部消失。
  assert.match(
    app,
    /nextPack\.channel !== rollbackState\.pack\.channel[\s\S]{0,500}location\.href = next\.toString\(\)/,
    '只有跨渠道账号切换才允许整页导航',
  );
  assert.match(
    app,
    /nextPack\.templateId !== currentTemplateId[\s\S]{0,220}await loadTemplate\(nextPack\.templateId,\s*\{\s*rerender:\s*false,\s*stale\s*\}\)/,
    '同渠道跨模板账号切换应热加载模板，保留当前页面里的 DOCX 图片 blob',
  );
  assert.doesNotMatch(
    app,
    /templateId !== currentTemplateId[\s\S]{0,240}location\.href/,
    '不能再用模板差异触发整页 reload',
  );
});

test('跨模板 A→B 快速切换时，后返的 A 模板不得覆盖 B', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const loadTemplateSource = extractFunction(app, 'loadTemplate');
  const switchAccountSource = extractFunction(app, 'switchAccount');
  const templateLoads = new Map();
  const adopted = [];
  const tracked = [];
  const switchedTemplates = [];
  const packs = {
    fbif: { id: 'fbif', channel: 'mp', templateId: 'fbif' },
    series: { id: 'series', channel: 'mp', templateId: 'foodtalks-series' },
    wowfood: { id: 'wowfood', channel: 'mp', templateId: 'fbif' },
  };
  const deps = {
    activeRulePack: packs.fbif,
    currentTemplateId: 'fbif',
    accountSwitchGeneration: 0,
    rulePackModule: { getRulePack: id => packs[id] },
    rulePackUrlModule: { parsePath: () => null, buildPath: () => '/' },
    location: { pathname: '/mp/fbif', href: 'https://example.test/mp/fbif' },
    window: {
      _activeRulePack: packs.fbif,
      _engineApi: {
        async switchTemplate(template) { switchedTemplates.push(template.id); },
      },
    },
    URL,
    persistAccountSelection() {},
    applyRulePackChrome() {},
    accountMenu: { setActive() {} },
    syncEngineConfig() {},
    initApp() {},
    wrapEngineFunctions() {},
    importTemplateModule(templateID) {
      const task = deferred();
      templateLoads.set(templateID, task);
      return task.promise;
    },
    ruleSelector: {
      async adoptAccount() {
        adopted.push(deps.activeRulePack.id);
        return { accountID: deps.activeRulePack.id, stale: false };
      },
    },
    trackActiveRuleHydration(promise, accountID) {
      tracked.push(accountID);
      return promise;
    },
  };
  deps.committedAccountState = {
    pack: packs.fbif, templateId: 'fbif', activeConfig: deps.window._activeConfig,
    skipUpload: deps.window._skipUpload, bottomBlocksOverride: deps.window._bottomBlocksOverride, ruleSource: 'server',
  };
  const actualLoadTemplate = new Function('deps', `with (deps) { ${loadTemplateSource}; return loadTemplate; }`)(deps);
  deps.loadTemplate = actualLoadTemplate;
  const switchAccount = new Function('deps', `with (deps) { ${switchAccountSource}; return switchAccount; }`)(deps);

  const switchToA = switchAccount('series');
  assert.deepEqual(tracked, ['series'], '跨模板 import 一开始就必须进入 rules-ready 门');
  const switchToB = switchAccount('wowfood');
  const resultB = await switchToB;
  templateLoads.get('foodtalks-series').resolve({ default: { id: 'foodtalks-series' } });
  const resultA = await switchToA;

  assert.equal(resultB.stale, false);
  assert.equal(resultB.accountID, 'wowfood');
  assert.equal(resultA.stale, true);
  assert.equal(resultA.accountID, 'series');
  assert.equal(deps.activeRulePack.id, 'wowfood');
  assert.equal(deps.currentTemplateId, 'fbif');
  assert.deepEqual(switchedTemplates, [], '过期 series 模板不得进入引擎');
  assert.deepEqual(adopted, ['wowfood'], '过期 series 不得继续规则水合');
  assert.deepEqual(tracked, ['series', 'wowfood']);
});

test('账号切换是事务：模板加载失败后回滚原账号并可重试', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const loadTemplateSource = extractFunction(app, 'loadTemplate');
  const switchAccountSource = extractFunction(app, 'switchAccount');
  const packs = {
    fbif: { id: 'fbif', channel: 'mp', templateId: 'fbif' },
    series: { id: 'series', channel: 'mp', templateId: 'foodtalks-series' },
  };
  let attempts = 0;
  const menu = [];
  const deps = {
    activeRulePack: packs.fbif,
    currentTemplateId: 'fbif',
    accountSwitchGeneration: 0,
    rulePackModule: { getRulePack: id => packs[id] },
    rulePackUrlModule: { parsePath: () => null, buildPath: () => '/' },
    location: { pathname: '/mp/fbif', href: 'https://example.test/mp/fbif' },
    window: {
      _activeRulePack: packs.fbif,
      _activeConfig: { preset: { id: 'fbif-rule' }, config: { owner: 'fbif' } },
      _skipUpload: false,
      _bottomBlocksOverride: undefined,
      fetchArticle: { invalidate() {} },
      _engineApi: { async switchTemplate() {} },
    },
    URL,
    persistAccountSelection() {},
    applyRulePackChrome() {},
    applyAccountCapabilityUI: async () => {},
    accountMenu: { setActive(id) { menu.push(id); } },
    syncEngineConfig() {},
    initApp() {}, wrapEngineFunctions() {},
    async importTemplateModule(templateID) {
      attempts++;
      if (attempts === 1) throw new Error('template unavailable');
      return { default: { id: templateID } };
    },
    ruleSelector: {
      getSource: () => 'server', setSource() {},
      async adoptAccount() { return { accountID: deps.activeRulePack.id, stale: false }; },
    },
    trackActiveRuleHydration(promise) { return promise; },
  };
  deps.committedAccountState = {
    pack: packs.fbif, templateId: 'fbif', activeConfig: deps.window._activeConfig,
    skipUpload: deps.window._skipUpload, bottomBlocksOverride: deps.window._bottomBlocksOverride, ruleSource: 'server',
  };
  deps.loadTemplate = new Function('deps', `with (deps) { ${loadTemplateSource}; return loadTemplate; }`)(deps);
  const switchAccount = new Function('deps', `with (deps) { ${switchAccountSource}; return switchAccount; }`)(deps);

  await assert.rejects(switchAccount('series'), /template unavailable/);
  assert.equal(deps.activeRulePack.id, 'fbif');
  assert.equal(deps.window._activeRulePack.id, 'fbif');
  assert.equal(deps.currentTemplateId, 'fbif');
  assert.notEqual(menu.at(-1), 'series', '失败目标不得留在菜单高亮');

  const retried = await switchAccount('series');
  assert.equal(retried.stale, false);
  assert.equal(deps.activeRulePack.id, 'series');
  assert.equal(deps.currentTemplateId, 'foodtalks-series');
  assert.equal(menu.at(-1), 'series');
});

test('账号规则已水合但预览未提交时，不得提前提交 URL/菜单并必须回滚', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const switchAccountSource = extractFunction(app, 'switchAccount');
  const packs = {
    a: { id: 'a', channel: 'mp', templateId: 'fbif' },
    b: { id: 'b', channel: 'mp', templateId: 'fbif' },
  };
  const initialConfig = { preset: { id: 'rule-a' }, config: { owner: 'A' } };
  const persisted = [];
  const menu = [];
  const deps = {
    activeRulePack: packs.a,
    currentTemplateId: 'fbif',
    accountSwitchGeneration: 0,
    committedAccountState: {
      pack: packs.a,
      templateId: 'fbif',
      activeConfig: initialConfig,
      skipUpload: false,
      bottomBlocksOverride: { owner: 'A' },
      ruleSource: 'server',
    },
    rulePackModule: { getRulePack: id => packs[id] },
    rulePackUrlModule: { parsePath: () => null, buildPath: () => '/' },
    location: { pathname: '/mp/a', href: 'https://example.test/mp/a' },
    URL,
    window: {
      _activeRulePack: packs.a,
      _activeConfig: initialConfig,
      _skipUpload: false,
      _bottomBlocksOverride: { owner: 'A' },
      fetchArticle: { invalidate() {} },
      _engineApi: { invalidatePendingWork() {} },
    },
    persistAccountSelection(id) { persisted.push(id); },
    applyRulePackChrome() {},
    applyAccountCapabilityUI: async () => {},
    accountMenu: { setActive(id) { menu.push(id); } },
    syncEngineConfig() {},
    async loadTemplate() { return true; },
    rulePreviewCommitted: result => ['committed', 'noop', 'not-requested'].includes(result && result.status),
    ruleSelector: {
      getSource: () => 'server',
      setSource() {},
      async adoptAccount() {
        deps.window._activeConfig = { preset: { id: 'rule-b' }, config: { owner: 'B' } };
        deps.window._skipUpload = true;
        deps.window._bottomBlocksOverride = { owner: 'B' };
        return { accountID: 'b', stale: false, previewSync: { status: 'failed' } };
      },
    },
    trackActiveRuleHydration(promise) { return promise; },
  };
  const switchAccount = new Function('deps', `with (deps) { ${switchAccountSource}; return switchAccount; }`)(deps);

  await assert.rejects(switchAccount('b'), /预览未能提交/);
  assert.equal(deps.activeRulePack.id, 'a');
  assert.equal(deps.window._activeRulePack.id, 'a');
  assert.equal(deps.window._activeConfig.config.owner, 'A');
  assert.deepEqual(deps.window._bottomBlocksOverride, { owner: 'A' });
  assert.deepEqual(persisted, [], '预览失败前不得持久化目标账号');
  assert.equal(menu.at(-1), 'a', '菜单最终必须回到已提交账号');
});

test('目标预览已提交后 afterHydrate 失败，回滚必须重投原规则并刷新全部消费者', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const switchAccountSource = extractFunction(app, 'switchAccount');
  const packs = {
    a: { id: 'a', channel: 'mp', templateId: 'fbif' },
    b: { id: 'b', channel: 'mp', templateId: 'fbif' },
  };
  const initialConfig = { preset: { id: 'rule-a' }, config: { owner: 'A' } };
  const previewCommits = [];
  const consumerRefreshes = [];
  const menu = [];
  let rollbackStatus = 'committed';
  const deps = {
    activeRulePack: packs.a,
    currentTemplateId: 'fbif',
    accountSwitchGeneration: 0,
    committedAccountState: { pack: packs.a, templateId: 'fbif', activeConfig: initialConfig, ruleSource: 'server' },
    rulePackModule: { getRulePack: id => packs[id] },
    rulePackUrlModule: { parsePath: () => null, buildPath: () => '/' },
    location: { pathname: '/mp/a', href: 'https://example.test/mp/a' },
    URL,
    window: {
      _activeRulePack: packs.a,
      _activeConfig: initialConfig,
      fetchArticle: { invalidate() {} },
      _engineApi: { invalidatePendingWork() {} },
    },
    persistAccountSelection() {},
    applyRulePackChrome() {},
    applyAccountCapabilityUI: async () => {},
    accountMenu: { setActive(id) { menu.push(id); } },
    async loadTemplate() { return true; },
    syncEngineConfig() {},
    refreshRuleConsumers(options) {
      consumerRefreshes.push({ owner: deps.window._activeConfig.config.owner, rerender: options.rerender });
      return { status: rollbackStatus };
    },
    rulePreviewCommitted: result => ['committed', 'noop', 'not-requested'].includes(result && result.status),
    ruleSelector: {
      getSource: () => 'server',
      setSource() {},
      async adoptAccount() {
        deps.window._activeConfig = { preset: { id: 'rule-b' }, config: { owner: 'B' } };
        previewCommits.push('B');
        throw new Error('afterHydrate failed');
      },
    },
    trackActiveRuleHydration(promise) { return promise; },
  };
  const switchAccount = new Function('deps', `with (deps) { ${switchAccountSource}; return switchAccount; }`)(deps);

  await assert.rejects(switchAccount('b'), /afterHydrate failed/);
  assert.deepEqual(previewCommits, ['B'], '目标正文曾经已提交');
  assert.deepEqual(consumerRefreshes, [{ owner: 'A', rerender: true }], '回滚必须用 A 快照重投正文并刷新 sidecars');
  assert.equal(deps.window._activeConfig.config.owner, 'A');
  assert.equal(menu.at(-1), 'a');

  rollbackStatus = 'failed';
  await assert.rejects(switchAccount('b'), /原账号已回滚，但当前预览恢复失败/);
});

test('模板已进入引擎后才报错，账号事务仍必须回滚原模板', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const loadTemplateSource = extractFunction(app, 'loadTemplate');
  const switchAccountSource = extractFunction(app, 'switchAccount');
  const packs = {
    fbif: { id: 'fbif', channel: 'mp', templateId: 'fbif' },
    series: { id: 'series', channel: 'mp', templateId: 'foodtalks-series' },
  };
  const switched = [];
  let failTarget = true;
  const deps = {
    activeRulePack: packs.fbif,
    currentTemplateId: 'fbif',
    accountSwitchGeneration: 0,
    rulePackModule: { getRulePack: id => packs[id] },
    rulePackUrlModule: { parsePath: () => null, buildPath: () => '/' },
    location: { pathname: '/mp/fbif', href: 'https://example.test/mp/fbif' },
    window: {
      _activeRulePack: packs.fbif,
      _activeConfig: { preset: { id: 'fbif-rule' }, config: { owner: 'fbif' } },
      _engineTemplate: 'fbif',
      _engineApi: {
        async switchTemplate(template) {
          switched.push(template.id);
          deps.window._engineTemplate = template.id;
          if (template.id === 'foodtalks-series' && failTarget) {
            failTarget = false;
            throw new Error('template shell failed');
          }
        },
      },
    },
    URL,
    persistAccountSelection() {}, applyRulePackChrome() {},
    applyAccountCapabilityUI: async () => {}, accountMenu: { setActive() {} },
    syncEngineConfig() {}, initApp() {}, wrapEngineFunctions() {},
    async importTemplateModule(templateID) { return { default: { id: templateID } }; },
    ruleSelector: {
      getSource: () => 'server', setSource() {},
      async adoptAccount() { return { accountID: deps.activeRulePack.id, stale: false }; },
    },
    trackActiveRuleHydration(promise) { return promise; },
  };
  deps.committedAccountState = {
    pack: packs.fbif, templateId: 'fbif', activeConfig: deps.window._activeConfig,
    skipUpload: deps.window._skipUpload, bottomBlocksOverride: deps.window._bottomBlocksOverride, ruleSource: 'server',
  };
  deps.loadTemplate = new Function('deps', `with (deps) { ${loadTemplateSource}; return loadTemplate; }`)(deps);
  const switchAccount = new Function('deps', `with (deps) { ${switchAccountSource}; return switchAccount; }`)(deps);

  await assert.rejects(switchAccount('series'), /template shell failed/);
  assert.equal(deps.activeRulePack.id, 'fbif');
  assert.equal(deps.currentTemplateId, 'fbif');
  assert.equal(deps.window._engineTemplate, 'fbif', '引擎不得留在失败目标模板');
  assert.deepEqual(switched, ['foodtalks-series', 'fbif']);
});

test('原模板回滚失败时必须 fail-closed，不得伪装已恢复菜单和预览', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const switchAccountSource = extractFunction(app, 'switchAccount');
  const packs = {
    a: { id: 'a', channel: 'mp', templateId: 'template-a' },
    b: { id: 'b', channel: 'mp', templateId: 'template-b' },
  };
  const chrome = [];
  const menu = [];
  const navigations = [];
  let refreshes = 0;
  const locationState = {
    pathname: '/mp/a',
    _href: 'https://example.test/mp/a',
    get href() { return this._href; },
    set href(value) { this._href = value; navigations.push(value); },
  };
  const initialConfig = { config: { owner: 'A' } };
  const deps = {
    activeRulePack: packs.a,
    currentTemplateId: 'template-a',
    accountSwitchGeneration: 0,
    committedAccountState: {
      pack: packs.a,
      templateId: 'template-a',
      activeConfig: initialConfig,
      ruleSource: 'server',
    },
    rulePackModule: { getRulePack: id => packs[id] },
    rulePackUrlModule: {
      parsePath: () => ({ channel: 'mp', id: 'a' }),
      buildPath: (channel, id) => `/${channel}/${id}`,
    },
    location: locationState,
    URL,
    window: {
      _activeRulePack: packs.a,
      _activeConfig: initialConfig,
      _engineTemplate: 'template-a',
      fetchArticle: { invalidate() {} },
      _engineApi: { invalidatePendingWork() {} },
    },
    persistAccountSelection() {},
    applyRulePackChrome(pack) { chrome.push(pack.id); },
    accountMenu: { setActive(id) { menu.push(id); } },
    applyAccountCapabilityUI: async () => {},
    syncEngineConfig() {},
    refreshRuleConsumers() { refreshes++; return { status: 'committed' }; },
    rulePreviewCommitted: result => result?.status === 'committed',
    async loadTemplate(templateID) {
      if (templateID === 'template-b') {
        deps.currentTemplateId = templateID;
        deps.window._engineTemplate = templateID;
        return true;
      }
      throw new Error('rollback template unavailable');
    },
    ruleSelector: {
      getSource: () => 'server',
      setSource() {},
      async adoptAccount() { throw new Error('target hydrate failed'); },
    },
    trackActiveRuleHydration(promise) { return promise; },
  };
  const switchAccount = new Function('deps', `with (deps) { ${switchAccountSource}; return switchAccount; }`)(deps);

  await assert.rejects(
    switchAccount('b'),
    /原账号模板回滚失败.*当前页面不可继续编辑/,
  );
  assert.equal(refreshes, 0, '模板未恢复时不得重投 A 规则到 B 模板');
  assert.deepEqual(chrome, [], '不得将 chrome 伪装成已恢复 A');
  assert.deepEqual(menu, [], '不得将菜单伪装成已恢复 A');
  assert.equal(deps.currentTemplateId, 'template-b');
  assert.equal(deps.window._engineTemplate, 'template-b');
  assert.equal(navigations.at(-1), 'https://example.test/mp/a', '必须强制回到已提交 A 的 canonical URL');
});

test('A→B pending→C 失败：C 必须回滚已提交 A，B 后返不得制造混合态', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const loadTemplateSource = extractFunction(app, 'loadTemplate');
  const switchAccountSource = extractFunction(app, 'switchAccount');
  const packs = {
    fbif: { id: 'fbif', channel: 'mp', templateId: 'fbif' },
    series: { id: 'series', channel: 'mp', templateId: 'foodtalks-series' },
    wowfood: { id: 'wowfood', channel: 'mp', templateId: 'fbif' },
  };
  const delayedB = deferred();
  const persisted = [];
  const menu = [];
  const adopted = [];
  const initialConfig = { preset: { id: 'rule-a' }, config: { owner: 'A' } };
  const deps = {
    activeRulePack: packs.fbif,
    currentTemplateId: 'fbif',
    accountSwitchGeneration: 0,
    rulePackModule: { getRulePack: id => packs[id] },
    rulePackUrlModule: { parsePath: () => null, buildPath: () => '/' },
    location: { pathname: '/mp/fbif', href: 'https://example.test/mp/fbif' },
    window: {
      _activeRulePack: packs.fbif,
      _activeConfig: initialConfig,
      _skipUpload: false,
      _bottomBlocksOverride: { owner: 'A' },
      _engineTemplate: 'fbif',
      fetchArticle: { invalidate() {} },
      _engineApi: {
        invalidatePendingWork() {},
        async switchTemplate(template) { deps.window._engineTemplate = template.id; },
      },
    },
    URL,
    persistAccountSelection(id) { persisted.push(id); },
    applyRulePackChrome() {}, applyAccountCapabilityUI: async () => {},
    accountMenu: { setActive(id) { menu.push(id); } },
    syncEngineConfig() {}, initApp() {}, wrapEngineFunctions() {},
    importTemplateModule(templateID) {
      if (templateID === 'foodtalks-series') return delayedB.promise;
      return Promise.resolve({ default: { id: templateID } });
    },
    ruleSelector: {
      getSource: () => 'server', setSource() {},
      async adoptAccount() {
        const id = deps.activeRulePack.id;
        adopted.push(id);
        if (id === 'wowfood') {
          deps.window._activeConfig = { preset: { id: 'rule-c' }, config: { owner: 'C' } };
          deps.window._skipUpload = true;
          deps.window._bottomBlocksOverride = { owner: 'C' };
          throw new Error('C hydrate failed');
        }
        return { accountID: id, stale: false };
      },
    },
    trackActiveRuleHydration(promise) { return promise; },
  };
  deps.committedAccountState = {
    pack: packs.fbif, templateId: 'fbif', activeConfig: initialConfig,
    skipUpload: false, bottomBlocksOverride: { owner: 'A' }, ruleSource: 'server',
  };
  deps.loadTemplate = new Function('deps', `with (deps) { ${loadTemplateSource}; return loadTemplate; }`)(deps);
  const switchAccount = new Function('deps', `with (deps) { ${switchAccountSource}; return switchAccount; }`)(deps);

  const switchB = switchAccount('series');
  await assert.rejects(switchAccount('wowfood'), /C hydrate failed/);
  assert.equal(deps.activeRulePack.id, 'fbif');
  assert.equal(deps.window._activeRulePack.id, 'fbif');
  assert.equal(deps.currentTemplateId, 'fbif');
  assert.equal(deps.window._engineTemplate, 'fbif');
  assert.equal(deps.window._activeConfig.config.owner, 'A');
  assert.equal(deps.window._skipUpload, false);
  assert.deepEqual(deps.window._bottomBlocksOverride, { owner: 'A' });

  delayedB.resolve({ default: { id: 'foodtalks-series' } });
  const staleB = await switchB;
  assert.equal(staleB.stale, true);
  assert.equal(deps.activeRulePack.id, 'fbif');
  assert.equal(deps.window._engineTemplate, 'fbif');
  assert.deepEqual(adopted, ['wowfood'], 'B 过期后不得继续水合');
  assert.deepEqual(persisted, [], '失败/过期目标都不得持久化');
  assert.equal(menu.at(-1), 'fbif');
});

test('已提交账号保存新规则后，回滚快照同步更新；暂存账号不得更新', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const refreshSource = extractFunction(app, 'refreshRuleConsumers');
  const packs = { a: { id: 'a' }, b: { id: 'b' } };
  const deps = {
    activeRulePack: packs.a,
    currentTemplateId: 'template-a',
    committedAccountState: { pack: packs.a, activeConfig: { config: { version: 1 } } },
    window: { _activeConfig: { config: { version: 2 } }, _skipUpload: false, _bottomBlocksOverride: { a: 1 } },
    ruleSelector: { getSource: () => 'server' },
    syncWorkflowControls() {}, syncEngineConfig() {}, renderActiveRuleCard() {}, loadConfig: () => ({}),
  };
  const refresh = new Function('deps', `with (deps) { ${refreshSource}; return refreshRuleConsumers; }`)(deps);
  refresh();
  assert.equal(deps.committedAccountState.activeConfig.config.version, 2);

  const committedA = deps.committedAccountState;
  deps.activeRulePack = packs.b;
  deps.window._activeConfig = { config: { version: 99 } };
  refresh();
  assert.equal(deps.committedAccountState, committedA);
  assert.equal(deps.committedAccountState.pack.id, 'a');
});

test('失败回滚模板后返时已有更新切换，旧回滚不得覆盖新账号', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const loadTemplateSource = extractFunction(app, 'loadTemplate');
  const switchAccountSource = extractFunction(app, 'switchAccount');
  const packs = {
    fbif: { id: 'fbif', channel: 'mp', templateId: 'fbif' },
    series: { id: 'series', channel: 'mp', templateId: 'foodtalks-series' },
    wowfood: { id: 'wowfood', channel: 'mp', templateId: 'fbif' },
  };
  const fbifLoads = [];
  const switched = [];
  const deps = {
    activeRulePack: packs.fbif, currentTemplateId: 'fbif', accountSwitchGeneration: 0,
    rulePackModule: { getRulePack: id => packs[id] },
    rulePackUrlModule: { parsePath: () => null, buildPath: () => '/' },
    location: { pathname: '/mp/fbif', href: 'https://example.test/mp/fbif' }, URL,
    window: {
      _activeRulePack: packs.fbif, _activeConfig: { config: { owner: 'A' } },
      _engineApi: { async switchTemplate(template) { switched.push(template.id); } },
    },
    persistAccountSelection() {}, applyRulePackChrome() {}, applyAccountCapabilityUI: async () => {},
    accountMenu: { setActive() {} }, syncEngineConfig() {}, initApp() {}, wrapEngineFunctions() {},
    importTemplateModule(templateID) {
      if (templateID !== 'fbif') return Promise.resolve({ default: { id: templateID } });
      const task = deferred();
      fbifLoads.push(task);
      return task.promise;
    },
    ruleSelector: {
      getSource: () => 'server', setSource() {},
      async adoptAccount() {
        if (deps.activeRulePack.id === 'series') throw new Error('series hydrate failed');
        return { accountID: deps.activeRulePack.id, stale: false };
      },
    },
    trackActiveRuleHydration(promise) { return promise; },
  };
  deps.committedAccountState = {
    pack: packs.fbif, templateId: 'fbif', activeConfig: deps.window._activeConfig,
    skipUpload: false, bottomBlocksOverride: undefined, ruleSource: 'server',
  };
  deps.loadTemplate = new Function('deps', `with (deps) { ${loadTemplateSource}; return loadTemplate; }`)(deps);
  const switchAccount = new Function('deps', `with (deps) { ${switchAccountSource}; return switchAccount; }`)(deps);

  const failing = switchAccount('series');
  for (let i = 0; i < 6 && fbifLoads.length < 1; i++) await Promise.resolve();
  assert.equal(fbifLoads.length, 1, '失败切换已进入 A 模板回滚');
  const newest = switchAccount('wowfood');
  for (let i = 0; i < 6 && fbifLoads.length < 2; i++) await Promise.resolve();
  assert.equal(fbifLoads.length, 2);
  fbifLoads[1].resolve({ default: { id: 'fbif' } });
  await newest;
  fbifLoads[0].resolve({ default: { id: 'fbif' } });
  await assert.rejects(failing, /series hydrate failed/);

  assert.equal(deps.activeRulePack.id, 'wowfood');
  assert.equal(deps.currentTemplateId, 'fbif');
  assert.equal(deps.committedAccountState.pack.id, 'wowfood');
  assert.deepEqual(switched, ['foodtalks-series', 'fbif'], '旧回滚过期后不得再进引擎');
});

test('engine supports template hot-swap and reads footer from the current account', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  // WHY：账号切换后当前文章必须用新模板/新规则重排，但不能重建编辑会话。
  assert.match(engine, /export function initApp\(initialTemplate\)/);
  assert.match(engine, /let template = initialTemplate/);
  assert.match(engine, /window\._engineSwitchTemplate/);
  assert.match(engine, /switchTemplate: window\._engineSwitchTemplate/);
  assert.match(engine, /await _engineRerender\(\)/);
  assert.match(engine, /function currentStockFooterUrl\(\)[\s\S]{0,220}window\._activeRulePack/);
  assert.doesNotMatch(engine, /const footerReady\s*=/, '页脚不能在模块加载时按初始账号锁死');
});
