// block-panel.test.mjs — 前台底部区块内容面板。
// WHY：系列公众号每篇文章需要在左侧替换作者名片、BD 名片和入群二维码。
// 只要这些 image 块被默认标记 frontend_editable，面板就必须显示对应换图入口。

import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

const storage = new Map();
const { document, window } = parseHTML('<!doctype html><html><head></head><body><div id="mount"></div></body></html>');
globalThis.window = window;
globalThis.document = document;
let activeElement = null;
Object.defineProperty(document, 'activeElement', { configurable: true, get: () => activeElement });
const buttonProto = Object.getPrototypeOf(document.createElement('button'));
const nativeFocus = buttonProto.focus;
buttonProto.focus = function focus(options) {
  activeElement = this;
  if (typeof nativeFocus === 'function') {
    try { nativeFocus.call(this, options); } catch {}
  }
};
globalThis.localStorage = {
  getItem(key) { return storage.has(key) ? storage.get(key) : null; },
  setItem(key, value) { storage.set(key, String(value)); },
  removeItem(key) { storage.delete(key); },
};

function keydown(key) {
  const event = new window.Event('keydown', { bubbles: true });
  Object.defineProperty(event, 'key', { value: key });
  return event;
}

const { mountBlockPanel } = await import('../public/js/blocks/block-panel.js?panel=' + Date.now());

function seriesBottomBlocks() {
  return {
    schema_version: 2,
    enabled: true,
    active_preset_id: 'default',
    presets: [{
      id: 'default',
      name: '系列页脚',
      order: ['author_card', 'bd_card', 'group_qr', 'more_articles'],
      blocks: {
        author_card: {
          type: 'image',
          display_name: '作者名片',
          enabled: true,
          frontend_editable: true,
          fields: { image: '' },
        },
        bd_card: {
          type: 'image',
          display_name: 'BD 名片',
          enabled: true,
          frontend_editable: true,
          fields: { image: '{{asset#ft_series_card_eileen}}' },
        },
        group_qr: {
          type: 'image',
          display_name: '包装群二维码',
          enabled: true,
          frontend_editable: true,
          fields: { image: '{{asset#ft_series_group_packaging}}' },
        },
        more_articles: {
          type: 'more_articles',
          display_name: '推荐文章',
          enabled: false,
          fields: { title: '/ 更多文章 /' },
        },
      },
    }],
  };
}

function fbifBottomBlocks() {
  return {
    schema_version: 2,
    enabled: true,
    active_preset_id: 'default',
    presets: [{
      id: 'default',
      name: 'FBIF 底部',
      order: ['tip', 'tip_gap', 'byline', 'more_articles'],
      blocks: {
        tip: {
          type: 'text',
          display_name: '提示声明',
          enabled: true,
          fields: { content: '提示：' },
        },
        tip_gap: {
          type: 'spacer',
          display_name: '间距',
          enabled: true,
          fields: { height: 16 },
        },
        byline: {
          type: 'text',
          display_name: '作者署名',
          enabled: true,
          fields: { content: 'Bobo' },
        },
        more_articles: {
          type: 'more_articles',
          display_name: '更多文章',
          enabled: false,
          fields: { title: '/ 更多文章 /' },
        },
      },
    }],
  };
}

const moteFooterDefaults = {
  enabled: true,
  author_aliases: {
    mote: ['Mote莫特', 'Mote 莫特'],
  },
  profiles: [{
    id: 'mote_author',
    name: 'Mote 作者默认',
    match: { author_alias_group: 'mote' },
    block_visibility: {
      byline: false,
      more_articles: true,
    },
    artifact_visibility: {
      mote_ref_author_card: false,
    },
  }],
};

const assetLibrary = {
  assets: [
    { id: 'ft_series_card_eileen', label: 'Eileen 名片', scope: 'foodtalks-series', url: '/assets/foodtalks-series/cards/eileen.jpg' },
    { id: 'ft_series_group_packaging', label: '包装群二维码', scope: 'foodtalks-series', url: '/assets/foodtalks-series/groups/packaging.png' },
  ],
};

test('系列公众号左侧面板显示作者名片、BD 名片和入群二维码换图入口', () => {
  storage.clear();
  const mount = document.getElementById('mount');
  const inst = mountBlockPanel({
    mount,
    accountId: 'foodtalks-series',
    getModule: seriesBottomBlocks,
    getAssetLibrary: () => assetLibrary,
    getBlockExtra: () => null,
    onChange: () => {},
    doc: document,
  });

  const titles = Array.from(mount.querySelectorAll('.bp-sec-title')).map(el => el.textContent.trim());
  assert.deepEqual(titles, ['作者名片', 'BD 名片', '包装群二维码']);
  assert.equal(mount.querySelectorAll('.bp-image-btn').length, 3, '三个图片区块都应有换图按钮');
  assert.equal(inst.hasEditableContent(), true);
});

test('底部区域关闭后不继续暴露底部图片区块编辑入口', () => {
  storage.clear();
  const mount = document.getElementById('mount');
  mount.innerHTML = '';
  const inst = mountBlockPanel({
    mount,
    accountId: 'foodtalks-series',
    getModule: seriesBottomBlocks,
    getAssetLibrary: () => assetLibrary,
    getFooterVisibilityOverride: () => ({ enabled: 'hide' }),
    getBlockExtra: () => null,
    onChange: () => {},
    doc: document,
  });

  const parentSwitch = mount.querySelector('details.bp-footer-details .bp-parent-switch [role="switch"]');
  assert.ok(parentSwitch, '底部区域关闭态仍应保留父级开关');
  assert.equal(parentSwitch.getAttribute('aria-checked'), 'false');
  assert.match(mount.textContent, /本文不输出文末模块/);
  assert.equal(mount.querySelectorAll('.bp-block-item').length, 0, '关闭底部区域后不应显示子模块开关');
  assert.equal(mount.querySelectorAll('.bp-sec').length, 0, '关闭底部区域后不应显示底部图片编辑 section');
  assert.equal(mount.querySelectorAll('.bp-image-btn').length, 0, '关闭底部区域后不应继续显示换图入口');
  assert.equal(inst.hasEditableContent(), true, '父级开关本身仍是可编辑内容');
});

test('底部模块禁用时不显示会误导的底部区域开关', () => {
  storage.clear();
  const mount = document.getElementById('mount');
  mount.innerHTML = '';
  const inst = mountBlockPanel({
    mount,
    accountId: 'wowfood',
    getModule: () => ({ schema_version: 2, enabled: false, presets: [] }),
    getAssetLibrary: () => assetLibrary,
    getBlockExtra: () => null,
    onChange: () => {},
    doc: document,
  });

  assert.equal(mount.querySelector('[data-footer-switch="enabled"]'), null);
  assert.doesNotMatch(mount.textContent, /底部区域/);
  assert.equal(inst.hasEditableContent(), false);
});

test('Mote 作者：规则分段切换 + 署名图并入底部区域统一列表（架构修正）', () => {
  storage.clear();
  const mount = document.getElementById('mount');
  const originalParent = mount.parentNode;
  const scrollHost = document.createElement('div');
  scrollHost.className = 'scroll-y';
  originalParent.insertBefore(scrollHost, mount);
  scrollHost.appendChild(mount);
  scrollHost.scrollTop = 96;
  mount.innerHTML = '';
  const moreEditor = document.createElement('div');
  moreEditor.id = 'moreEditorProbe';
  moreEditor.textContent = '更多文章卡片编辑器';
  let artifactOverride = {};
  let lastArtifactChange = null;
  let footerVisibilityOverride = {};
  let lastFooterVisibilityChange = null;

  const inst = mountBlockPanel({
    mount,
    accountId: 'fbif',
    getModule: fbifBottomBlocks,
    getAssetLibrary: () => assetLibrary,
    getArticleMeta: () => ({ author: 'Mote莫特' }),
    getFooterDefaults: () => moteFooterDefaults,
    getFooterVisibilityOverride: () => footerVisibilityOverride,
    onFooterVisibilityChange: (next) => {
      footerVisibilityOverride = next || {};
      lastFooterVisibilityChange = next;
    },
    getArticleArtifactOverride: () => artifactOverride,
    onArticleArtifactChange: (next) => {
      artifactOverride = next || {};
      lastArtifactChange = next;
    },
    getBlockExtra: (block) => block && block.type === 'more_articles' ? moreEditor : null,
    onChange: () => {},
    doc: document,
  });

  // 2026-07-11 架构修正：删掉独立「文章专属设置」框和「文中元素」分组，全部收进「底部区域」。
  assert.doesNotMatch(mount.textContent, /文章专属设置/);
  assert.doesNotMatch(mount.textContent, /文中元素/);
  const bottomDetails = mount.querySelector('details.bp-footer-details');
  assert.ok(bottomDetails, '底部区域应使用可展开层级');
  assert.equal(bottomDetails.open, true, '底部区域默认展开');
  // 规则是分段切换（好切换的动作），归入底部区域头部
  const ruleTabs = bottomDetails.querySelector('.bp-rule-tabs');
  assert.ok(ruleTabs, '规则分段切换在底部区域内');
  const tabLabels = Array.from(ruleTabs.querySelectorAll('.bp-rule-tab')).map(b => b.textContent);
  assert.deepEqual(tabLabels, ['自动', '默认', 'Mote 作者默认']);
  assert.equal(ruleTabs.querySelector('.bp-rule-tab[aria-selected="true"]').textContent, '自动', '默认选中自动档');
  // 参考文献前署名图并入底部模块统一列表（署名图在前，渲染在正文更靠上位置）
  assert.match(mount.textContent, /参考文献前署名图/);
  const blockNames = Array.from(mount.querySelectorAll('.bp-block-name')).map(el => el.textContent.trim());
  assert.deepEqual(blockNames, ['参考文献前署名图', '提示声明', '作者署名', '更多文章']);
  assert.equal(blockNames.includes('间距'), false, '间距不应作为勾选模块出现');

  const moreNode = mount.querySelector('[data-block-id="more_articles"]');
  assert.ok(moreNode, '更多文章应有自己的父节点');
  assert.equal(moreNode.contains(moreEditor), true, '更多文章卡片编辑器应嵌在更多文章节点下');
  assert.equal(mount.querySelector('.bp-sec[data-id="more_articles"]'), null, '更多文章不应再作为割裂的同级 section');

  const artifactSwitch = mount.querySelector('[data-artifact-id="mote_ref_author_card"][role="switch"]');
  assert.ok(artifactSwitch, 'Mote 文中署名图需要独立开关');
  assert.equal(artifactSwitch.getAttribute('aria-checked'), 'false');
  artifactSwitch.click();
  assert.deepEqual(lastArtifactChange, { mote_ref_author_card: 'show' });

  const artifactSwitchAfterClick = mount.querySelector('[data-artifact-id="mote_ref_author_card"][role="switch"]');
  assert.equal(artifactSwitchAfterClick.getAttribute('aria-checked'), 'true');

  const visibleCheckboxes = mount.querySelectorAll('input[type="checkbox"]');
  assert.equal(visibleCheckboxes.length, 0, 'block-panel 可见开关不应再渲染 checkbox input');

  const parentSwitch = mount.querySelector('details.bp-footer-details .bp-parent-switch [role="switch"]');
  assert.ok(parentSwitch, '底部区域应有总开关');
  assert.equal(parentSwitch.getAttribute('aria-checked'), 'true');

  const tipSwitch = mount.querySelector('[data-block-id="tip"] [role="switch"]');
  const bylineSwitch = mount.querySelector('[data-block-id="byline"] [role="switch"]');
  const moreArticlesSwitch = mount.querySelector('[data-block-id="more_articles"] [role="switch"]');
  assert.ok(tipSwitch, '提示声明应渲染为 switch');
  assert.ok(bylineSwitch, '作者署名应渲染为 switch');
  assert.ok(moreArticlesSwitch, '更多文章应渲染为 switch');
  assert.equal(tipSwitch.getAttribute('aria-checked'), 'true');
  assert.equal(bylineSwitch.getAttribute('aria-checked'), 'false');
  assert.equal(moreArticlesSwitch.getAttribute('aria-checked'), 'true');
  assert.doesNotMatch(mount.textContent, /跟随默认/, '默认状态不应用重复文案制造视觉噪音');

  moreArticlesSwitch.focus();
  moreArticlesSwitch.dispatchEvent(keydown(' '));
  assert.deepEqual(lastFooterVisibilityChange, { blocks: { more_articles: 'hide' } });
  assert.match(mount.textContent, /本篇例外/, '只有本篇偏离默认时才显示例外状态');

  footerVisibilityOverride = {};
  lastFooterVisibilityChange = null;
  inst.refresh();

  const parentSwitchAfterRerender = mount.querySelector('details.bp-footer-details .bp-parent-switch [role="switch"]');
  parentSwitchAfterRerender.focus();
  parentSwitchAfterRerender.dispatchEvent(keydown('Enter'));

  assert.deepEqual(lastFooterVisibilityChange, { enabled: 'hide' });
  assert.match(mount.textContent, /本文不输出文末模块（提示、名片、更多文章），预览和复制都不会包含它们。/);
  assert.equal(mount.querySelectorAll('[data-block-id]').length, 0, '关闭后文末模块开关消失');
  assert.ok(mount.querySelector('[data-artifact-id="mote_ref_author_card"]'), '署名图渲染在正文、独立于本文显示，仍保留');
  assert.equal(document.activeElement && document.activeElement.dataset.footerSwitch, 'enabled', '关闭底部区域后焦点应留在父级胶囊开关');
  assert.equal(scrollHost.scrollTop, 96, '关闭底部区域后左侧滚动位置不应重置');

  inst.refresh();
  assert.equal(document.activeElement && document.activeElement.dataset.footerSwitch, 'enabled', '宿主重刷面板后焦点仍应留在父级胶囊开关');
  assert.equal(scrollHost.scrollTop, 96, '宿主重刷面板后左侧滚动位置不应重置');
});

test('Mote 署名图开关识别旧 artifact 隐藏形态', () => {
  storage.clear();
  const mount = document.getElementById('mount');
  const cases = [
    { mote_ref_author_card: 'hidden' },
    { mote_ref_author_card: { hidden: true } },
    { mote_ref_author_card: { enabled: false } },
  ];

  for (const artifactOverride of cases) {
    mount.innerHTML = '';
    mountBlockPanel({
      mount,
      accountId: 'fbif',
      getModule: fbifBottomBlocks,
      getAssetLibrary: () => assetLibrary,
      getArticleMeta: () => ({ author: 'Mote莫特' }),
      getFooterDefaults: () => moteFooterDefaults,
      getArticleArtifactOverride: () => artifactOverride,
      getBlockExtra: () => null,
      onChange: () => {},
      doc: document,
    });

    const artifactSwitch = mount.querySelector('[data-artifact-id="mote_ref_author_card"][role="switch"]');
    assert.ok(artifactSwitch, 'Mote 文中署名图需要独立开关');
    assert.equal(artifactSwitch.getAttribute('aria-checked'), 'false');
  }
});

// ---- 作者规则分段切换（2026-07-11 架构修正：规则归入底部区域头部）----
// WHY：作者自动识别失败时整套作者规则曾静默消失且无 UI 可补救；规则切换必须在
// 「无匹配」时依然可见可选（兜底），且无 profiles 配置的账号（网站渠道）UI 零变化。
test('作者未识别时规则分段切换仍在，手选 profile 救回署名图（静默失效兜底）', () => {
  storage.clear();
  const mount = document.getElementById('mount');
  mount.innerHTML = '';
  let lastProfileChange = 'unset';
  let profileOverride;
  mountBlockPanel({
    mount,
    accountId: 'fbif',
    getModule: fbifBottomBlocks,
    getAssetLibrary: () => assetLibrary,
    getArticleMeta: () => ({ author: '' }), // 识别失败场景
    getFooterDefaults: () => moteFooterDefaults,
    getArticleFooterProfileOverride: () => profileOverride,
    onArticleFooterProfileChange: (v) => { lastProfileChange = v; profileOverride = v; },
    getBlockExtra: () => null,
    onChange: () => {},
    doc: document,
  });

  const tabs = mount.querySelector('.bp-rule-tabs');
  assert.ok(tabs, '无匹配时规则切换仍必须渲染');
  const tabLabels = Array.from(tabs.querySelectorAll('.bp-rule-tab')).map(b => b.textContent);
  assert.deepEqual(tabLabels, ['自动', '默认', 'Mote 作者默认']);
  assert.equal(tabs.querySelector('.bp-rule-tab[aria-selected="true"]').textContent, '自动', '默认选中自动档');
  // 作者未识别 → 自动档匹配不到 profile → 署名图行不出现
  assert.equal(mount.querySelector('[data-artifact-id="mote_ref_author_card"]'), null, '自动+无作者：署名图行不出现');

  // 手选 Mote → 回调 + 重渲染出署名图行（识别失败也能手动救回）
  const moteTab = Array.from(tabs.querySelectorAll('.bp-rule-tab')).find(b => b.textContent === 'Mote 作者默认');
  moteTab.onclick();
  assert.equal(lastProfileChange, 'mote_author');
  const refreshed = mount.querySelector('.bp-rule-tabs');
  assert.equal(refreshed.querySelector('.bp-rule-tab[aria-selected="true"]').textContent, 'Mote 作者默认', '手选后高亮 Mote');
  assert.ok(mount.querySelector('[data-artifact-id="mote_ref_author_card"]'), '手选 Mote 后署名图行出现');
});

test('无 profiles 配置的账号不渲染规则切换（网站渠道 UI 零变化）', () => {
  storage.clear();
  const mount = document.getElementById('mount');
  mount.innerHTML = '';
  mountBlockPanel({
    mount,
    accountId: 'fbif',
    getModule: fbifBottomBlocks,
    getAssetLibrary: () => assetLibrary,
    getArticleMeta: () => ({ author: 'Mote莫特' }),
    getFooterDefaults: () => null,
    getBlockExtra: () => null,
    onChange: () => {},
    doc: document,
  });
  assert.equal(mount.querySelector('.bp-rule-tabs'), null, '无 profiles 时不渲染规则切换');
  assert.doesNotMatch(mount.textContent, /文章专属设置/);
});
