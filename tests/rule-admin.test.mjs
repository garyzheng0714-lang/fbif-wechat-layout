import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { serverRuleConfigDocument } from '../public/js/admin-rule-payload.js';
import { syncRuleVisualEditor } from '../public/js/admin-rule-visual-sync.js';

async function readProjectFile(path) {
  return readFile(new URL(`../${path}`, import.meta.url), 'utf8');
}

test('规则后台：参数为可视化表单(无 JSON 代码框)，规则控件齐全', async () => {
  const adminPanel = await readProjectFile('public/js/admin-panel.js');

  // 用户裁定 2026-06-06：参数 tab = 可视化表单 + 实时预览，JSON 分栏代码框已删（后台要直观）
  assert.match(adminPanel, /admin-visual-zone/);
  assert.doesNotMatch(adminPanel, /admin-editor-split/);
  assert.doesNotMatch(adminPanel, /<textarea data-role="rule-json"/);
  // 规则控件保留
  assert.match(adminPanel, /data-action="new"/);
  assert.match(adminPanel, /应用到当前公众号/);
  assert.match(adminPanel, /复制为新规则/);
  assert.match(adminPanel, /保存修改/);
  assert.match(adminPanel, /closest\('#adminLockBtn'\)/, '规则后台入口应使用事件委托，账号菜单重绘后仍可打开');
  assert.doesNotMatch(adminPanel, />设为当前规则</);
  assert.match(adminPanel, /createMoreArticlesRuleEditor/);
  assert.match(adminPanel, /data-role="more-articles-rule-editor"/);
});

test('rule admin save-as generates a unique id instead of overwriting same-name rules', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const { uniqueClientRuleID } = await import('../public/js/admin-panel.js?unique=' + Date.now());

  assert.equal(uniqueClientRuleID('foodtalks series default', {
    presets: [{ id: 'foodtalks-series-default' }],
  }), 'foodtalks-series-default-2');
  assert.equal(uniqueClientRuleID('foodtalks series default', {
    rules: { 'foodtalks-series-default': { id: 'foodtalks-series-default' } },
  }), 'foodtalks-series-default-2');
  assert.match(adminPanel, /function ruleListFromDoc\(doc\)/);
  assert.match(adminPanel, /Array\.isArray\(doc\.presets\)/);
  assert.match(adminPanel, /while \(used\.has\(base \+ '-' \+ suffix\)\) suffix\+\+;/);
  assert.match(adminPanel, /const id = uniqueClientRuleID\(name, doc\);/);
  assert.doesNotMatch(adminPanel, /data-action="save-as"[\s\S]*const id = clientRuleID\(name\);/);
});

test('rule admin lets JSON explicitly clear a saved description', async () => {
  const adminPanel = await readProjectFile('public/js/admin-panel.js');
  const visualSync = await readProjectFile('public/js/admin-rule-visual-sync.js');

  assert.match(adminPanel, /workingDescription = parsed\.description;/);
  assert.match(visualSync, /descriptionInput\.value = description;/);
  assert.doesNotMatch(adminPanel, /if \(parsed\.description\) workingDescription/);
  assert.doesNotMatch(visualSync, /if \(description\) descriptionInput/);
});

test('rule visual sync updates metadata, fields, colors, and toggles', () => {
  const nodes = new Map();
  const addNode = (selector, node = {}) => {
    nodes.set(selector, node);
    return node;
  };
  addNode('[data-role="preset-name"]', { value: 'old' });
  addNode('[data-role="preset-description"]', { value: 'old' });
  addNode('input[data-rule-key="font_size"]', { value: '15' });
  addNode('input[data-rule-key="text_color"]', { value: '#000000' });
  addNode('[data-color-hex="text_color"]', { textContent: '#000000' });
  const toggled = new Set();
  const toggle = addNode('.admin-toggle[data-rule-key="footer_enabled"]', {
    classList: { toggle(name, on) { if (on) toggled.add(name); else toggled.delete(name); } },
    attrs: {},
    setAttribute(name, value) { this.attrs[name] = value; },
  });

  syncRuleVisualEditor({
    overlay: { querySelector: selector => nodes.get(selector) || null },
    schema: [
      { fields: [
        { key: 'font_size', type: 'number' },
        { key: 'text_color', type: 'color' },
        { key: 'footer_enabled', type: 'boolean' },
      ] },
    ],
    config: { font_size: '16', text_color: '#544545', footer_enabled: true },
    name: '新规则',
    description: '',
    fieldValue: (config, field) => config[field.key],
  });

  assert.equal(nodes.get('[data-role="preset-name"]').value, '新规则');
  assert.equal(nodes.get('[data-role="preset-description"]').value, '');
  assert.equal(nodes.get('input[data-rule-key="font_size"]').value, '16');
  assert.equal(nodes.get('input[data-rule-key="text_color"]').value, '#544545');
  assert.equal(nodes.get('[data-color-hex="text_color"]').textContent, '#544545');
  assert.equal(toggle.attrs['aria-pressed'], 'true');
  assert.equal(toggled.has('on'), true);
});

test('rule admin strips JSON metadata before sending config to the server', () => {
  const config = serverRuleConfigDocument({
    schema_version: 2,
    name: '只属于规则名称输入框',
    description: '只属于说明输入框',
    modules: { theme: { font_size: '16' } },
  });

  assert.deepEqual(config, {
    schema_version: 2,
    modules: { theme: { font_size: '16' } },
  });
});

test('rule admin has configuration and rule-management views', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  assert.match(app, /rulePacks:\s*rulePackModule\.listRulePacks\(\)/);
  assert.match(adminPanel, /activeView = 'assignments'/);
  assert.match(adminPanel, /data-view="assignments"/);
  assert.match(adminPanel, /data-view="rules"/);
  assert.match(adminPanel, /规则配置/);
  assert.match(adminPanel, /已应用到：/);
  assert.match(adminPanel, /未应用/);
  assert.match(adminPanel, /renderApplicationBadges/);
  assert.match(adminPanel, /setActiveRulePreset\(password, ruleID, accountID\)/);
  assert.match(adminPanel, /仅在预览中查看，未改变公众号应用规则/);
  assert.match(css, /\.admin-layout/);
  assert.match(css, /\.admin-side-nav/);
  assert.match(css, /\.admin-applied-badges/);
});

test('rule admin seeds article footer defaults from the current account', async () => {
  const adminPanel = await readProjectFile('public/js/admin-panel.js');

  assert.match(adminPanel, /articleFooterDefaultsSeedForAccount/);
  assert.match(adminPanel, /workingArticleFooterDefaults = defaultArticleFooterDefaultsForCurrentAccount\(\);/);
  assert.doesNotMatch(adminPanel, /workingArticleFooterDefaults = defaultArticleFooterDefaultsData\(\);/);
});

test('app caches structured server presets for local fallback', async () => {
  const app = await readProjectFile('public/app.html');

  // S1(2026-07-10)：cacheServerRuleFallback 本体收编进 active-rule-selector.js，
  // 「server 预设经渠道变换落本地缓存」的行为契约由
  // tests/active-rule-selector.test.mjs 的 fallbackTransform 测试守护；
  // app.html 只需证明它把 structuredFallbackConfig 作为公众号口径的变换注入工厂。
  assert.match(app, /function\s+structuredFallbackConfig\(config,\s*preset\)/);
  assert.match(app, /preset && \(preset\.raw_config \|\| preset\.config\)/);
  assert.match(app, /fallbackTransform:\s*\(config, preset\) => structuredFallbackConfig\(config, preset\)/);
});

test('rule admin deletes from the rule row with explicit confirmation', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  assert.match(adminPanel, /class="admin-icon-danger"/);
  assert.match(adminPanel, /aria-label="删除当前规则"/);
  assert.match(adminPanel, /window\.confirm/);
  assert.match(adminPanel, /删除后无法恢复，正在使用它的公众号会回到默认规则/);
  assert.doesNotMatch(adminPanel, /class="admin-secondary danger" data-action="delete"/);
  assert.match(css, /\.admin-preset-control/);
  assert.match(css, /\.admin-icon-danger/);
});

test('规则后台：删掉「代码化规则」「校验预览」tab，但 behavior 数据链路保留', async () => {
  const adminPanel = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  // 用户裁定 2026-06-06：删掉复杂的代码化规则/校验预览 tab，胶囊只剩参数 + 底部
  assert.doesNotMatch(adminPanel, /\['behavior', '代码化规则'\]/);
  assert.doesNotMatch(adminPanel, /\['validation', '校验预览'\]/);
  assert.match(adminPanel, /const tabs = \[\['params', '参数'\], \['bottom', '底部'\]\]/);
  // behavior 数据链路保留：原作者行 / Mote 署名 / 原创转载声明等服务端规则仍生效（只删编辑 UI，数据照常加载/保存）
  assert.match(adminPanel, /normalizeRuleBehavior/);
  assert.match(adminPanel, /behavior:\s*workingBehavior/);
  assert.match(css, /\.admin-rule-tabs/);
});

test('rule admin card editor has dedicated layout styles', async () => {
  const appHTML = await readProjectFile('public/app.html');
  const siteHTML = await readProjectFile('public/site.html');
  const appCSS = await readProjectFile('public/app.css');
  const ruleAdminCSS = await readProjectFile('public/css/rule-admin.css');

  assert.match(appHTML, /<link rel="stylesheet" href="\/css\/rule-admin\.css">/);
  assert.match(siteHTML, /<link rel="stylesheet" href="\/css\/rule-admin\.css">/);
  assert.match(appHTML, /href="\/app\.css"[\s\S]*href="\/css\/rule-admin\.css"[\s\S]*href="\/css\/wx-theme\.css"/);
  assert.match(ruleAdminCSS, /\.ma-rule-field\s+input\[type="url"\]/);
  assert.match(ruleAdminCSS, /\.ma-rule-field\s+textarea:focus/);
  assert.match(ruleAdminCSS, /@media[\s\S]*\.ma-rule-field\s*\{[^}]*grid-template-columns:\s*1fr;/);
  assert.match(ruleAdminCSS, /\.ma-rule-card-head/);
  assert.match(ruleAdminCSS, /\.ma-rule-card-foot\s*\{[^}]*flex-wrap:\s*wrap;/);
  assert.doesNotMatch(appCSS, /\.ma-rule-/);
  assert.ok(appCSS.split('\n').length <= 900);
});

test('rule admin has footer-library tab and workingFooter save injection', async () => {
  const adminPanel = await readProjectFile('public/js/admin-panel.js');
  const blockData = await readProjectFile('public/js/admin-block-data.js');

  // import
  assert.match(adminPanel, /import\s*\{[^}]*createFooterLibraryEditor[^}]*\}\s*from\s*['"]\.\/footer-library-rule-editor\.js['"]/);

  // ft-default 系判断
  assert.match(adminPanel, /isFtDefaultPack\(\)/);
  assert.match(adminPanel, /templateId.*foodtalks-site|foodtalks-site.*templateId/);

  // 网站页脚库收敛到统一「底部」分区里，仅对 ft-default 系展示
  assert.match(adminPanel, /\['bottom', '底部'\]/);
  assert.match(blockData, /isFt \? '<h3>网站页脚库<\/h3><div data-role="footer-library-editor"><\/div>'/); // 渲染移到 admin-block-data helper
  assert.match(blockData, /<h3>文章默认方案<\/h3><div data-role="article-footer-defaults-editor"><\/div>/, '公众号后台不应把作者默认写成某个作者专属模块');
  assert.doesNotMatch(blockData, /<h3>作者默认<\/h3>/);

  // workingFooter 变量声明
  assert.match(adminPanel, /let workingFooter\s*=/);

  // refreshDoc 同步 workingFooter
  assert.match(adminPanel, /workingFooter\s*=\s*footerFromPreset\(selected\)/);

  // preset-select change 同步 workingFooter（全文出现 ≥2 次 footerFromPreset：refreshDoc + preset-select）
  const footerFromPresetCount = (adminPanel.match(/footerFromPreset/g) || []).length;
  assert.ok(footerFromPresetCount >= 2, `footerFromPreset 应至少出现 2 次，实际 ${footerFromPresetCount}`);

  // Footer Tab 挂载点（在 admin-block-data helper）
  assert.match(blockData, /data-role="footer-library-editor"/);

  // bindFooterLibraryEditor 调用 createFooterLibraryEditor
  assert.match(adminPanel, /createFooterLibraryEditor\(\{[\s\S]*?container[\s\S]*?footerData[\s\S]*?onChange[\s\S]*?\}\)/);

  // 保存注入：savePresetWithFooter 函数中含 fbif_footer
  assert.match(adminPanel, /savePresetWithFooter/);
  assert.match(adminPanel, /configDoc\.modules\.fbif_footer/);
  assert.match(adminPanel, /serverRuleConfigDocument\(configDoc\)/);

  // 两处保存都调用 savePresetWithFooter 而非裸 saveRulePreset
  const saveCount = (adminPanel.match(/await savePresetWithFooter/g) || []).length;
  assert.ok(saveCount >= 2, `应有至少 2 处 savePresetWithFooter 调用，实际 ${saveCount}`);

  // 行数守护
  assert.ok(adminPanel.split('\n').length <= 900, `admin-panel.js 超过 900 行`);
});

test('rule admin：素材库提升为左侧独立 view（用户裁定 2026-06-06，推翻旧「嵌胶囊 tab」）', async () => {
  const adminPanel = await readProjectFile('public/js/admin-panel.js');
  const blockData = await readProjectFile('public/js/admin-block-data.js');
  const css = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  // 左侧三视图：账号 / 规则 / 素材库（素材库从胶囊 tab 提升为独立 view）
  assert.match(adminPanel, /data-view="assignments"[^>]*>账号/);
  assert.match(adminPanel, /data-view="rules"/);
  assert.match(adminPanel, /data-view="assets"/);
  assert.match(adminPanel, /renderAssetsLibraryView/);
  assert.match(adminPanel, /data-role="asset-library-editor"/);  // 素材库 view 挂载点
  assert.doesNotMatch(adminPanel, /\['assets', '素材库'\]/);        // 不再是胶囊 tab
  // 底部 tab 仍是区块编辑器
  assert.match(adminPanel, /\['bottom', '底部'\]/);
  assert.match(blockData, /data-role="block-editor"/);
  assert.match(adminPanel, /data-role="dirty-chip"/);
  assert.match(adminPanel, /adminDirtyCount/);
  assert.match(css, /\.admin-dirty-chip/);
});
