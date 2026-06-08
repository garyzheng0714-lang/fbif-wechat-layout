import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

import {
  bindRailDivider,
  bindUrlImportShortcuts,
  createUrlImportHandler,
  foodTalksFetchErrorMessage,
  resolveInitialRulePackID,
} from '../public/js/editor-shell.js';
import * as rulePackModule from '../public/js/rule-packs/registry.js';
import * as rulePackUrlModule from '../public/js/rule-packs/url.js';

function makeClassList() {
  const names = new Set();
  return {
    add(name) { names.add(name); },
    remove(name) { names.delete(name); },
    contains(name) { return names.has(name); },
    toggle(name, force) {
      const shouldAdd = force === undefined ? !names.has(name) : !!force;
      if (shouldAdd) names.add(name);
      else names.delete(name);
      return shouldAdd;
    },
  };
}

function makeRailElement({ width = 300 } = {}) {
  const listeners = new Map();
  const attrs = new Map();
  const styleValues = new Map();
  const element = {
    attrs,
    listeners,
    classList: makeClassList(),
    capturedPointers: [],
    releasedPointers: [],
    style: {
      setProperty(name, value) { styleValues.set(name, value); },
      getPropertyValue(name) { return styleValues.get(name) || ''; },
    },
    title: '',
    addEventListener(type, handler) {
      const handlers = listeners.get(type) || [];
      handlers.push(handler);
      listeners.set(type, handlers);
    },
    removeEventListener(type, handler) {
      listeners.set(type, (listeners.get(type) || []).filter(item => item !== handler));
    },
    dispatch(type, event = {}) {
      for (const handler of [...(listeners.get(type) || [])]) handler(event);
    },
    setAttribute(name, value) { attrs.set(name, value); },
    getAttribute(name) { return attrs.get(name); },
    contains(target) { return target === element; },
    getBoundingClientRect() { return { width }; },
    setPointerCapture(pointerID) { element.capturedPointers.push(pointerID); },
    releasePointerCapture(pointerID) { element.releasedPointers.push(pointerID); },
  };
  return element;
}

function makeRailFixture({ mobile = false, includeNodes = true } = {}) {
  const layout = makeRailElement();
  const rail = makeRailElement({ width: 300 });
  const divider = makeRailElement();
  const button = makeRailElement();
  const body = { classList: makeClassList() };
  const elements = includeNodes
    ? new Map([['rail', rail], ['railDivider', divider], ['railToggleBtn', button]])
    : new Map();

  return {
    layout,
    rail,
    divider,
    button,
    body,
    documentRef: {
      body,
      querySelector(selector) {
        return selector === '.app-layout' && includeNodes ? layout : null;
      },
      getElementById(id) {
        return elements.get(id) || null;
      },
    },
    windowRef: {
      matchMedia() { return { matches: mobile }; },
      setTimeout(fn) { fn(); },
    },
  };
}

test('editor shells both import the shared editor-shell helpers', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const site = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');

  assert.match(app, /import\(['"]\/js\/editor-shell\.js/);
  assert.match(site, /import\(['"]\/js\/editor-shell\.js/);
  assert.match(app, /createUrlImportHandler/);
  assert.match(site, /createUrlImportHandler/);
  assert.match(app, /bindUrlImportShortcuts/);
  assert.match(site, /bindUrlImportShortcuts/);
  assert.match(app, /bindRailDivider/);
  assert.match(site, /bindRailDivider/);
  assert.equal(typeof bindRailDivider, 'function');
});

test('bindRailDivider no-ops safely when shell nodes are missing', () => {
  const fixture = makeRailFixture({ includeNodes: false });

  assert.doesNotThrow(() => bindRailDivider(fixture));
});

test('bindRailDivider toggles collapsed state from the handle button', () => {
  const fixture = makeRailFixture();
  bindRailDivider(fixture);

  fixture.button.dispatch('click');
  assert.equal(fixture.layout.classList.contains('rail-collapsed'), true);
  assert.equal(fixture.button.getAttribute('aria-expanded'), 'false');
  assert.equal(fixture.button.getAttribute('aria-label'), '展开左栏');

  fixture.button.dispatch('click');
  assert.equal(fixture.layout.classList.contains('rail-collapsed'), false);
  assert.equal(fixture.button.getAttribute('aria-expanded'), 'true');
  assert.equal(fixture.button.getAttribute('aria-label'), '收起左栏');
});

test('bindRailDivider ignores drag gestures on mobile viewports', () => {
  const fixture = makeRailFixture({ mobile: true });
  bindRailDivider(fixture);
  let prevented = false;

  fixture.divider.dispatch('pointerdown', {
    pointerId: 1,
    clientX: 100,
    target: fixture.divider,
    preventDefault() { prevented = true; },
  });

  assert.equal(prevented, false);
  assert.deepEqual(fixture.divider.capturedPointers, []);
  assert.equal(fixture.body.classList.contains('is-resizing-rail'), false);
  assert.equal(fixture.divider.listeners.get('pointermove'), undefined);
});

test('bindRailDivider resizes on desktop drag and cleans listeners on release', () => {
  const fixture = makeRailFixture();
  bindRailDivider(fixture);

  fixture.divider.dispatch('pointerdown', {
    pointerId: 7,
    clientX: 100,
    target: fixture.divider,
    preventDefault() {},
  });
  assert.deepEqual(fixture.divider.capturedPointers, [7]);
  assert.equal(fixture.body.classList.contains('is-resizing-rail'), true);

  fixture.divider.dispatch('pointermove', { clientX: 160 });
  assert.equal(fixture.layout.style.getPropertyValue('--rail-current-w'), '360px');
  assert.equal(fixture.layout.classList.contains('rail-collapsed'), false);

  fixture.divider.dispatch('pointerup');
  assert.deepEqual(fixture.divider.releasedPointers, [7]);
  assert.equal(fixture.body.classList.contains('is-resizing-rail'), false);
  assert.equal(fixture.divider.listeners.get('pointermove').length, 0);
  assert.equal(fixture.divider.listeners.get('pointerup').length, 0);
  assert.equal(fixture.divider.listeners.get('pointercancel').length, 0);
});

test('editor shells bind upload before remote rule hydration settles', async () => {
  for (const name of ['app.html', 'site.html']) {
    const source = await readFile(new URL('../public/' + name, import.meta.url), 'utf8');
    const initIndex = source.indexOf('await loadTemplate(currentTemplateId)');
    assert.ok(initIndex > 0, `${name} should initialize its template`);

    const beforeInit = source.slice(0, initIndex);
    assert.doesNotMatch(
      beforeInit,
      /const\s+initialRule\s*=\s*await\s+loadActiveRulePreset/,
      `${name} must not block upload binding on the active rule request`
    );
    assert.doesNotMatch(
      beforeInit,
      /let\s+activeRuleCatalog\s*=\s*await\s+loadRuleCatalog/,
      `${name} must not block upload binding on the rule catalog request`
    );
    assert.match(
      beforeInit,
      /hydrateActiveRulePreset\(activeRulePack\.id\)\.catch/,
      `${name} should hydrate the active rule in the background`
    );
    assert.match(
      beforeInit,
      /refreshActiveRuleCatalog\(activeRulePack\.id\)\.catch/,
      `${name} should hydrate the rule catalog in the background`
    );
  }
});

test('editor shells load JSZip from the same-origin vendor asset', async () => {
  for (const name of ['app.html', 'site.html']) {
    const source = await readFile(new URL('../public/' + name, import.meta.url), 'utf8');
    assert.match(source, /<script src="\/vendor\/jszip\.min\.js"><\/script>/);
    assert.doesNotMatch(source, /cdn\.jsdelivr\.net\/npm\/jszip/);
  }
});

test('公众号上传摘要收进文件卡片，网站页保留旧统计行', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const site = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/css/app-shell.css', import.meta.url), 'utf8');
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  const docZoneStart = app.indexOf('id="docZone"');
  const statsBarStart = app.indexOf('id="statsBar"');
  const docZoneEnd = app.indexOf('<div class="error" id="error">');
  assert.ok(docZoneStart > 0, '公众号页应有上传后文件卡片');
  assert.ok(
    statsBarStart > docZoneStart && statsBarStart < docZoneEnd,
    '旧统计/上传状态行应归属文件卡片，避免在左栏形成割裂的第二块信息'
  );
  assert.match(app, /id="docSwap"[^>]*aria-label="重新上传"[^>]*>重新上传<\/button>/, '文件卡片右侧动作应是明确的重新上传按钮，不应使用刷新图标');
  assert.doesNotMatch(app, /id="docSwap"[\s\S]{0,200}<svg/, '重新上传入口不能再退回成语义不清的循环箭头图标');
  assert.doesNotMatch(css, /\.doc-stat::before/, '上传摘要统计前不应出现突兀的成功绿点');
  assert.doesNotMatch(site, /id="docZone"/, '网站页仍走旧上传区，不能被公众号文件卡片样式隐式接管');
  assert.match(
    engine,
    /return hasDocumentSummaryCard\(\) \? formatSidebarStatus\(stats\) : \(stats \|\| ''\);/,
    '共享引擎应只在新文件卡片页隐藏成功态统计行，网站页继续显示旧统计'
  );
});

test('editor shells do not block startup on external font stylesheets', async () => {
  for (const name of ['app.html', 'site.html']) {
    const source = await readFile(new URL('../public/' + name, import.meta.url), 'utf8');
    assert.doesNotMatch(source, /fontsource/);
    assert.doesNotMatch(source, /cdn\.jsdelivr\.net/);
  }
});

test('resolveInitialRulePackID preserves app path behavior', () => {
  const id = resolveInitialRulePackID({
    locationLike: { pathname: '/mp/foodtalks-series', search: '?account=fbif' },
    storage: null,
    rulePackModule,
    rulePackUrlModule,
  });

  assert.equal(id, 'foodtalks-series');
});

test('resolveInitialRulePackID falls back by channel for website shell', () => {
  const id = resolveInitialRulePackID({
    locationLike: { pathname: '/ft/default', search: '?account=fbif' },
    storage: null,
    rulePackModule,
    rulePackUrlModule,
    useChannelDefaultPath: true,
  });

  assert.equal(id, 'ft-default');
});

test('createUrlImportHandler passes source URL with structured blocks', async () => {
  const calls = [];
  const requests = [];
  const elements = new Map();
  const makeElement = value => ({ value, textContent: '', disabled: false, hidden: false, style: { display: '' } });
  elements.set('urlInput', makeElement('https://mp.weixin.qq.com/s/abc'));
  elements.set('urlFetchBtn', makeElement(''));
  elements.set('error', makeElement(''));
  elements.set('importZone', makeElement(''));
  elements.set('docZone', makeElement(''));
  elements.set('progress', makeElement(''));
  elements.set('progressFill', { style: { width: '' } });
  elements.set('progressText', makeElement(''));

  const documentRef = {
    getElementById(id) {
      return elements.get(id);
    },
  };
  const fetchImpl = async (url, init) => {
    requests.push({ url, body: JSON.parse(init.body) });
    return {
      ok: true,
      json: async () => ({ title: '标题', blocks: [{ type: 'p', text: '正文' }] }),
    };
  };
  const handler = createUrlImportHandler({
    documentRef,
    fetchImpl,
    getAccountID: () => 'ft-default',
    getHandleBlocks: () => (payload, options) => calls.push({ payload, options }),
    onArticleUrl(url) {
      calls.push({ currentUrl: url });
    },
  });

  await handler();

  assert.equal(calls[0].currentUrl, 'https://mp.weixin.qq.com/s/abc');
  assert.deepEqual(requests[0], {
    url: '/api/fetch-article',
    body: { url: 'https://mp.weixin.qq.com/s/abc', account: 'ft-default' },
  });
  assert.deepEqual(calls[1].payload, {
    title: '标题',
    blocks: [{ type: 'p', text: '正文' }],
    source: 'https://mp.weixin.qq.com/s/abc',
  });
  assert.deepEqual(calls[1].options, { isRepost: true });
  assert.equal(elements.get('importZone').hidden, false);
  assert.equal(elements.get('docZone').hidden, true);
  assert.equal(elements.get('progress').style.display, 'block');
  assert.equal(elements.get('progressFill').style.width, '30%');
  assert.equal(elements.get('progressText').textContent, '正在提取...');
});

test('bindUrlImportShortcuts lets the import row focus the URL input', () => {
  const targetHandlers = {};
  let focused = false;
  let selected = false;

  const urlInput = {
    value: '',
    tagName: 'INPUT',
    classList: { contains: () => false },
    addEventListener() {},
    focus() { focused = true; },
    select() { selected = true; },
  };
  const focusTarget = {
    addEventListener(type, handler) { targetHandlers[type] = handler; },
  };
  const documentRef = {
    activeElement: null,
    getElementById(id) {
      if (id === 'urlInput') return urlInput;
      if (id === 'previewContent') return { style: { display: 'none' } };
      return null;
    },
    addEventListener() {},
    querySelectorAll() { return [focusTarget]; },
  };

  bindUrlImportShortcuts({
    documentRef,
    fetchArticle: () => {},
    focusTargetSelector: '[data-url-focus-target]',
  });

  targetHandlers.click({ target: focusTarget });
  assert.equal(focused, true);
  assert.equal(selected, false);

  urlInput.value = 'https://mp.weixin.qq.com/s/abc';
  targetHandlers.click({ target: focusTarget });
  assert.equal(selected, true);
});

test('bindUrlImportShortcuts leaves other text inputs alone on paste', () => {
  const documentHandlers = {};
  let fetched = 0;
  let prevented = false;
  const urlInput = {
    value: '',
    tagName: 'INPUT',
    classList: { contains: () => false },
    addEventListener() {},
  };
  const otherInput = {
    tagName: 'INPUT',
    classList: { contains: () => false },
  };
  const documentRef = {
    activeElement: otherInput,
    getElementById(id) {
      if (id === 'urlInput') return urlInput;
      if (id === 'previewContent') return { style: { display: 'none' } };
      return null;
    },
    addEventListener(type, handler) { documentHandlers[type] = handler; },
    querySelectorAll() { return []; },
  };

  bindUrlImportShortcuts({
    documentRef,
    fetchArticle: () => { fetched += 1; },
    focusTargetSelector: '[data-url-focus-target]',
  });

  documentHandlers.paste({
    clipboardData: { getData: () => 'https://mp.weixin.qq.com/s/abc' },
    preventDefault() { prevented = true; },
  });

  assert.equal(prevented, false);
  assert.equal(urlInput.value, '');
  assert.equal(fetched, 0);
});

test('foodTalksFetchErrorMessage keeps internal details out of the UI', () => {
  assert.equal(
    foodTalksFetchErrorMessage(new Error('exec /usr/bin/tool: PATH not found')),
    '文章抓取失败，请稍后重试',
  );
  assert.equal(
    foodTalksFetchErrorMessage(new Error('文章内容为空')),
    '文章内容为空',
  );
});
