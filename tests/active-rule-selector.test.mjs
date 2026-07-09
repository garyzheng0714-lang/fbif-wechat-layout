// 契约测试：active-rule-selector.js —— 顶部「当前规则」选择器（外壳共享模块，S1）。
// 意图：这一族此前在 app.html/site.html 各复制一份且已分叉、零行为覆盖（架构体检
// 认定的最大裸奔区之一）。本测试用真实 DOM（linkedom）锁定：两种渠道只读视图口径、
// 编辑-保存状态机、密码失败回收、server 预设落本地缓存的渠道变换——任何人改共享
// 实现导致两页行为漂移时，必须先让这里红。
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

const { createActiveRuleSelector, escText } = await import('../public/js/active-rule-selector.js');

function makeEnv({ readonlyEditEntry = false, readonlyLabel = '当前排版规则', source = 'server', fallbackTransform } = {}) {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="activeRuleCard"></div></body></html>');
  window._activeConfig = { preset: { id: 'default', name: 'FBIF默认规则', config: { a: 1 } }, config: { a: 1 } };
  const calls = { saveLocal: [], setActive: [], adminLogin: [], applied: [] };
  const ruleModule = {
    loadActiveRulePreset: async () => ({ source: 'server', preset: { id: 'rule-x', name: '新规则', config: { b: 2 } } }),
    loadLocalRuleConfig: () => ({ local: true }),
    saveLocalRuleConfig: (config, acct) => calls.saveLocal.push({ config, acct }),
    applyActiveRuleConfig: (config, preset) => calls.applied.push({ config, preset }),
    adminLogin: async (pw) => { calls.adminLogin.push(pw); if (pw === 'bad') { const e = new Error('401 unauthorized'); throw e; } },
    listPublicRulePresets: async () => ({ presets: [{ id: 'default', name: 'FBIF默认规则' }, { id: 'rule-x', name: '新规则' }] }),
    setActiveRulePreset: async (pw, id, acct) => calls.setActive.push({ pw, id, acct }),
  };
  // 页面真实实现里 refreshRuleConsumers 会重渲选择器卡片（saveActiveRuleSelection
  // 成功路径依赖它），桩保持同一契约。
  let selectorRef = null;
  const selector = createActiveRuleSelector({
    documentRef: document,
    windowRef: window,
    getPack: () => ({ id: 'fbif' }),
    ruleModule,
    refreshRuleConsumers: () => { if (selectorRef) selectorRef.renderActiveRuleCard(); },
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

test('保存流程：选新规则 → 输密码 → setActiveRulePreset → 回只读并显示已保存', async () => {
  const { document, window, selector, calls } = makeEnv({ readonlyEditEntry: true });
  window.prompt = () => 'good-pass';
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
  assert.deepEqual(calls.setActive[0], { pw: 'good-pass', id: 'rule-x', acct: 'fbif' });
  assert.match(document.getElementById('activeRuleCard').innerHTML, /已保存/);
});

test('密码错误：401 类失败清空缓存密码并显示错误状态', async () => {
  const { document, window, selector } = makeEnv({ readonlyEditEntry: true });
  let prompts = 0;
  window.prompt = () => { prompts++; return 'bad'; };
  await selector.beginActiveRuleEdit();
  window._activeConfig.preset.id = 'default';
  const card = document.getElementById('activeRuleCard');
  card.querySelector('#activeRuleSelect').dispatchEvent(new window.Event('click', { bubbles: true }));
  document.querySelector('[data-rule-option="rule-x"]').dispatchEvent(new window.Event('click', { bubbles: true }));
  document.querySelector('[data-action="save-active-rule"]').dispatchEvent(new window.Event('click', { bubbles: true }));
  await new Promise(r => setTimeout(r, 20));
  assert.match(document.getElementById('activeRuleCard').innerHTML, /401/);
  // 再保存一次会重新 prompt（密码已被清空）——这是「密码错误可重试」的契约
  document.querySelector('[data-action="save-active-rule"]') ||
    await selector.beginActiveRuleEdit();
  const before = prompts;
  document.querySelector('[data-action="save-active-rule"]').dispatchEvent(new window.Event('click', { bubbles: true }));
  await new Promise(r => setTimeout(r, 20));
  assert.ok(prompts > before, '缓存密码清空后重试必须重新询问密码');
});

test('server 预设落本地缓存经过渠道 fallbackTransform（公众号 structuredFallbackConfig 口径）', async () => {
  const transform = (config, preset) => (preset && preset.config) || config;
  const { selector, calls } = makeEnv({ fallbackTransform: transform });
  await selector.hydrateActiveRulePreset('fbif');
  assert.equal(calls.saveLocal.length, 1);
  assert.deepEqual(calls.saveLocal[0], { config: { b: 2 }, acct: 'fbif' });
});

test('adoptAccount：并行水合+目录并复位选择器状态（账号切换契约）', async () => {
  const { document, selector } = makeEnv({ readonlyEditEntry: true });
  await selector.beginActiveRuleEdit();
  await selector.adoptAccount({ rerender: false });
  assert.equal(selector.getSource(), 'server');
  selector.renderActiveRuleCard();
  assert.ok(!/编辑中/.test(document.getElementById('activeRuleCard').innerHTML), '切账号后选择器必须退出编辑态');
});

test('escText 转义 HTML 敏感字符', () => {
  assert.equal(escText('<a b="c">&'), '&lt;a b=&quot;c&quot;&gt;&amp;');
  assert.equal(escText(null), '');
});
