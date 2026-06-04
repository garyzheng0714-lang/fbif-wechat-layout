import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('app shell renders a unified rail account menu (single layer)', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/app.css', import.meta.url), 'utf8');
  const channelBarCss = await readFile(new URL('../public/css/channel-bar.css', import.meta.url), 'utf8');
  const menuJs = await readFile(new URL('../public/js/account-menu.js', import.meta.url), 'utf8');
  const allCss = css + '\n' + channelBarCss;

  // 单层账号菜单：左侧工具区顶部的头像菜单取代旧的
  // 侧栏方角分段 + 顶部圆形渠道胶囊。渲染逻辑在 account-menu.js，挂载点 #acctMenu。
  assert.match(app, /id="acctMenu"/);
  assert.match(app, /mountAccountMenu/);
  assert.match(app, /account-menu\.js/);
  const headerStart = app.indexOf('<header class="global-channel-bar">');
  const headerEnd = app.indexOf('</header>', headerStart);
  const railStart = app.indexOf('<aside class="rail sidebar"');
  const railBodyStart = app.indexOf('<div class="rail-body sidebar-body scroll-y">', railStart);
  const header = app.slice(headerStart, headerEnd);
  const railHead = app.slice(railStart, railBodyStart);
  const visibleShell = app.slice(0, app.indexOf('<script type="module">'));
  assert.doesNotMatch(header, /id="acctMenu"/, '公众号菜单不应再占据全局顶栏');
  assert.doesNotMatch(app, /captureArticleTab|_articleTabs|mountArticleTabs/, '账号切换不应再绑定文章标签页状态');
  assert.match(header, /FBIF文章排版工具/, '全局顶栏必须展示产品身份');
  assert.match(header, /class="app-brand-logo"/, '全局顶栏必须展示 FBIF logo');
  assert.match(header, /id="adminLockBtn"/, '设置入口应在全局顶栏右侧');
  assert.match(railHead, /class="rail-account-bar"/, '公众号菜单应固定在左侧工具区顶部');
  assert.match(railHead, /id="acctMenu"/, '左侧工具区顶部必须提供公众号菜单挂载点');
  // 旧的两层切换器残骸必须清掉
  assert.doesNotMatch(app, /id="accountSwitcher"/, '旧侧栏方角分段已移除');
  assert.doesNotMatch(app, /class="channel-switcher"/, '旧顶部圆形渠道胶囊已移除');
  // account-menu.js 契约：按渠道分组、列出全部账号、标记当前账号
  assert.match(menuJs, /listRulePacks/);
  assert.match(menuJs, /CHANNEL_LABEL/);
  assert.match(menuJs, /is-active/);
  assert.match(menuJs, /setActive/);
  assert.match(app, /switchAccount/);
  assert.match(app, /loadActiveRulePreset\(activeRulePack\.id\)/);
  assert.match(app, /hydrateActiveRulePreset\(activeRulePack\.id\)/);
  assert.match(app, /cacheServerRuleFallback\(loaded\.preset\.config\)/);
  assert.doesNotMatch(visibleShell, /class="sidebar-footer"/, '左侧底部不再显示当前规则区');
  assert.doesNotMatch(visibleShell, /id="activeRuleCard"/, '当前规则卡片不应再出现在左侧底部');
  assert.doesNotMatch(visibleShell, /当前排版规则/, '左侧底部不再展示当前规则文案');
  assert.match(app, /renderActiveRuleCard/);
  assert.match(app, /id="activeRuleSelect"/);
  assert.doesNotMatch(app, /data-action="edit-active-rule"/, '规则切换编辑不应回到左侧底部入口');
  assert.match(app, /data-action="save-active-rule"/);
  assert.match(app, /data-action="cancel-active-rule"/);
  assert.match(app, /cancelActiveRuleEdit/);
  assert.match(app, /activeRuleSelectorOpen/);
  assert.match(app, /data-rule-option/);
  assert.doesNotMatch(app, /<select id="activeRuleSelect"/);
  assert.match(app, /listPublicRulePresets/);
  assert.match(app, /setActiveRulePreset\(password,\s*nextID,\s*activeRulePack\.id\)/);
  assert.doesNotMatch(app, /id="settingsEditBtn"/);
  assert.doesNotMatch(app, /id="settingsForm"/);
  assert.match(allCss, /\.acct-trigger/);
  assert.match(allCss, /\.acct-panel/);
  assert.match(allCss, /\.acct-item\.is-active/);
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
    /nextPack\.channel !== activeRulePack\.channel[\s\S]{0,500}location\.href = next\.toString\(\)/,
    '只有跨渠道账号切换才允许整页导航',
  );
  assert.match(
    app,
    /activeRulePack\.templateId !== currentTemplateId[\s\S]{0,160}await loadTemplate\(activeRulePack\.templateId,\s*\{\s*rerender:\s*false\s*\}\)/,
    '同渠道跨模板账号切换应热加载模板，保留当前页面里的 DOCX 图片 blob',
  );
  assert.doesNotMatch(
    app,
    /templateId !== currentTemplateId[\s\S]{0,240}location\.href/,
    '不能再用模板差异触发整页 reload',
  );
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
