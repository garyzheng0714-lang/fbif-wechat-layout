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
  assert.match(mount.textContent, /本文不输出底部区域/);
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

test('Mote 作者面板按递进层级组织底部区域和文中元素', () => {
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

  assert.match(mount.textContent, /文章专属设置/);
  // 2026-07-11 用户裁定：「已套用」灰字升级为作者规则选择器（自动/通用默认/各 profile）。
  const profileSelect = mount.querySelector('.bp-profile-select');
  assert.ok(profileSelect, '有 profiles 配置时必须渲染作者规则选择器');
  const optionTexts = Array.from(profileSelect.querySelectorAll('option')).map(o => o.textContent.trim());
  assert.deepEqual(optionTexts, ['自动 · Mote 作者默认', '通用默认', 'Mote 作者默认']);
  assert.ok(profileSelect.querySelector('option[selected]').textContent.includes('自动'), '默认跟随自动识别');
  assert.doesNotMatch(mount.textContent, /Mote 作者模块/);
  assert.match(mount.textContent, /文中元素/);
  assert.match(mount.textContent, /参考文献前署名图/);

  const bottomDetails = mount.querySelector('details.bp-footer-details');
  assert.ok(bottomDetails, '底部区域应使用可展开层级');
  assert.equal(bottomDetails.open, true, '底部区域默认展开');

  const blockNames = Array.from(mount.querySelectorAll('.bp-block-name')).map(el => el.textContent.trim());
  assert.deepEqual(blockNames, ['提示声明', '作者署名', '更多文章']);
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
  assert.match(mount.textContent, /本文不输出底部区域，预览和复制都不会包含这些模块。/);
  assert.equal(mount.querySelectorAll('.bp-block-item').length, 0, '关闭底部区域后不应继续显示可误点的子模块');
  assert.equal(document.activeElement && document.activeElement.dataset.footerSwitch, 'enabled', '关闭底部区域后焦点应留在父级胶囊开关');
  assert.equal(scrollHost.scrollTop, 96, '关闭底部区域后左侧滚动位置不应重置');

  inst.refresh();
  assert.equal(document.activeElement && document.activeElement.dataset.footerSwitch, 'enabled', '宿主重刷面板后焦点仍应留在父级胶囊开关');
  assert.equal(scrollHost.scrollTop, 96, '宿主重刷面板后左侧滚动位置不应重置');
});

test('Mote 文中元素开关识别旧 artifact 隐藏形态', () => {
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

// ---- 作者规则选择器（2026-07-11 用户裁定）----
// WHY：作者自动识别失败时整套作者规则曾静默消失且无 UI 可补救；选择器必须在
// 「无匹配」时依然可见可选（兜底），且无 profiles 配置的账号（网站渠道）UI 零变化。
test('作者未识别时选择器仍在，手选 profile 触发回调（静默失效的兜底）', () => {
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

  const select = mount.querySelector('.bp-profile-select');
  assert.ok(select, '无匹配时选择器仍必须渲染');
  const optionTexts = Array.from(select.querySelectorAll('option')).map(o => o.textContent.trim());
  assert.deepEqual(optionTexts, ['自动 · 通用默认', '通用默认', 'Mote 作者默认']);

  assert.equal(mount.querySelector('.bp-profile-note'), null, '自动态不显示「本篇例外」');

  // 手选 Mote → 回调收到 profile id，且面板重渲染出该 profile 的文中元素开关
  Object.defineProperty(select, 'value', { configurable: true, get: () => 'mote_author' });
  select.onchange();
  assert.equal(lastProfileChange, 'mote_author');
  const refreshed = mount.querySelector('.bp-profile-select');
  assert.ok(refreshed.querySelector('option[selected]').textContent.includes('Mote 作者默认'), '重渲染后选中手选项');
  assert.match(mount.textContent, /参考文献前署名图/, '手选后文中元素开关组出现');
  // 手动覆盖必须有「本篇例外」标记——与块开关偏离默认的既有语言一致
  const note = mount.querySelector('.bp-profile-note');
  assert.ok(note && note.textContent === '本篇例外', '手选态显示「本篇例外」');
  assert.match(refreshed.getAttribute('title') || '', /Mote 作者默认/, 'title 悬停显示完整当前值');
});

test('无 profiles 配置的账号不渲染选择器（网站渠道 UI 零变化）', () => {
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
  assert.equal(mount.querySelector('.bp-profile-select'), null);
  assert.doesNotMatch(mount.textContent, /文章专属设置/);
});
