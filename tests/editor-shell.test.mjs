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
  assert.match(app, /onInputCleared:\s*clearRenderedImport/);
  assert.match(site, /onInputCleared:\s*clearRenderedImport/);
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
      /(?:trackActiveRuleHydration\(\s*)?hydrateActiveRulePreset\(activeRulePack\.id\)(?:,\s*activeRulePack\.id\))?\.catch/,
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
  assert.match(
    engine,
    /Object\.prototype\.hasOwnProperty\.call\(opts, 'title'\)/,
    '共享引擎应允许剪贴板正文覆盖文件名兜底标题'
  );
  assert.match(
    engine,
    /handleFile\(file, \{ \.\.\.opts, isRepost: false, title: '', displayName: '剪贴板正文' \}\);/,
    '包含图片链接的剪贴板正文仍不应把内部 clipboard.md 文件名渲染成文章标题'
  );
  assert.match(
    engine,
    /if \(!hasImageLikeLink\(body\)\) \{[\s\S]{0,120}showPlainTextResult\(body\);[\s\S]{0,40}return;/,
    '纯文字剪贴板内容应走轻量弹窗，不应渲染成公众号文章'
  );
  assert.match(
    engine,
    /navigator\.clipboard\.writeText\(text\)/,
    '纯文字弹窗应复制纯文本，而不是复用公众号 HTML 复制链路'
  );
  assert.match(
    engine,
    /showPreview\(session\.title \|\| '', session\.articleHtml, session\.footerHtml, session\.stats, session\.displayName \|\| session\.title \|\| ''\);/,
    '剪贴板正文的左侧状态名应独立于文章标题'
  );
  assert.match(
    engine,
    /fileInputEl\.multiple = false;/,
    '当前上传区只承诺单文件，不能在代码层继续暴露多文件选择'
  );
  assert.match(
    engine,
    /const selected = valid\[0\];[\s\S]{0,120}showUploadFile\(selected\);/,
    '拖拽、选择和粘贴文件应共用单文件上传状态'
  );
  assert.match(
    engine,
    /document\.addEventListener\('paste'[\s\S]{0,160}e\.clipboardData && e\.clipboardData\.files/,
    '上传区应支持从剪贴板粘贴文件'
  );
  assert.match(
    css,
    /\.dropzone\.dragover \.dropzone-cloud[\s\S]{0,100}animation: upload-float/,
    '文件拖入时应有明确的上传动效反馈'
  );
  assert.match(
    css,
    /\.dropzone\.has-file \.dropzone-empty/,
    '上传完成后应切换到居中的文件状态，而不是继续显示空态'
  );
  assert.match(
    app,
    /const importClipboardText = \(text\) => \{\s*window\._currentArticleUrl = '';\s*if \(window\._handleText\)/,
    '公众号页粘贴正文时应清空上一条 URL 导入状态'
  );
  assert.match(
    site,
    /const importClipboardText = \(text\) => \{\s*window\._currentArticleUrl = '';\s*if \(window\._handleText\)/,
    '网站页粘贴正文时应清空上一条 URL 导入状态，避免分享复制沿用旧链接'
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

test('createUrlImportHandler treats non-URL input as pasted text', async () => {
  const elements = new Map();
  const makeElement = value => ({ value, textContent: '', disabled: false, hidden: false, style: { display: '' } });
  elements.set('urlInput', makeElement('第一段正文,需要排版。'));
  const documentRef = {
    getElementById(id) {
      return elements.get(id);
    },
  };
  let imported = '';
  let fetched = false;
  const handler = createUrlImportHandler({
    documentRef,
    fetchImpl: async () => {
      fetched = true;
      throw new Error('should not fetch plain text');
    },
    importText(text) {
      imported = text;
    },
  });

  await handler();

  assert.equal(imported, '第一段正文,需要排版。');
  assert.equal(fetched, false);
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

test('bindUrlImportShortcuts imports non-URL clipboard text from the page', () => {
  const documentHandlers = {};
  let imported = '';
  let fetched = 0;
  let prevented = false;
  const urlInput = {
    value: '',
    tagName: 'TEXTAREA',
    classList: { contains: () => false },
    addEventListener() {},
  };
  const documentRef = {
    activeElement: null,
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
    importText: text => { imported = text; },
    focusTargetSelector: '[data-url-focus-target]',
  });

  documentHandlers.paste({
    clipboardData: { getData: () => '这是一段正文,需要直接排版。' },
    preventDefault() { prevented = true; },
  });

  assert.equal(prevented, true);
  assert.equal(imported, '这是一段正文,需要直接排版。');
  assert.equal(urlInput.value, '这是一段正文,需要直接排版。');
  assert.equal(fetched, 0);
});

test('bindUrlImportShortcuts clears the rendered article when the import box is emptied', () => {
  const inputHandlers = {};
  let cleared = 0;
  const urlInput = {
    value: '已经排版的正文',
    tagName: 'TEXTAREA',
    classList: { contains: () => false },
    addEventListener(type, handler) { inputHandlers[type] = handler; },
  };
  const documentRef = {
    activeElement: urlInput,
    getElementById(id) {
      if (id === 'urlInput') return urlInput;
      if (id === 'previewContent') return { style: { display: '' } };
      return null;
    },
    addEventListener() {},
    querySelectorAll() { return []; },
  };

  bindUrlImportShortcuts({
    documentRef,
    fetchArticle: () => {},
    importText: () => {},
    onInputCleared: () => { cleared += 1; },
  });

  urlInput.value = '还没清空';
  inputHandlers.input();
  assert.equal(cleared, 0);

  urlInput.value = '';
  inputHandlers.input();
  assert.equal(cleared, 1);
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

// ── 摘要自动提取（2026-06-11 飞书反馈问题七，成品 news/62139 实测）──
test('extractSiteSummary：开头短导语 → 摘要（62139 橙宝文实测）', async () => {
  const { extractSiteSummary } = await import('../public/js/editor-shell.js');
  const blocks = [
    { k: 'img', src: 'https://mmbiz.qpic.cn/head.png' },
    { k: 'cap', text: '橙宝果汁' },
    { k: 'txt', runs: [{ type: 'txt', text: '1500多家星级酒店的选择，高端酒店果汁品类市占率近60%' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '后续正文段。' }] },
  ];
  assert.equal(extractSiteSummary(blocks), '1500多家星级酒店的选择，高端酒店果汁品类市占率近60%');
});

test('extractSiteSummary：首段过长（62303 上海艾录文，摘要为编辑手写）→ 空串不自动填', async () => {
  const { extractSiteSummary } = await import('../public/js/editor-shell.js');
  const blocks = [
    { k: 'img', src: 'https://mmbiz.qpic.cn/head.png' },
    { k: 'txt', runs: [{ type: 'txt', text: '中东冲突波及全球石化产业链，自2026年2月以来，国内塑料原料价格普遍上涨20%-50%，并迅速传导至下游塑料包装。在这一背景下，塑料包装如何少用一点材料，不仅关乎可持续性，更影响供应链稳定性。' }] },
  ];
  assert.equal(extractSiteSummary(blocks), '');
});

test('extractSiteSummary：开头直接是标题 → 空串', async () => {
  const { extractSiteSummary } = await import('../public/js/editor-shell.js');
  assert.equal(extractSiteSummary([{ k: 'h', text: '一、第一章' }, { k: 'txt', runs: [{ type: 'txt', text: '短句' }] }]), '');
});

test('extractSiteSummary consume：命中段从 blocks 原地移除（62139：摘要句不进正文）', async () => {
  const { extractSiteSummary } = await import('../public/js/editor-shell.js');
  const blocks = [
    { k: 'img', src: 'https://mmbiz.qpic.cn/head.png' },
    { k: 'txt', runs: [{ type: 'txt', text: '短导语摘要句' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '后续正文段。' }] },
  ];
  assert.equal(extractSiteSummary(blocks, { consume: true }), '短导语摘要句');
  assert.equal(blocks.length, 2);
  assert.ok(!blocks.some(b => (b.runs || []).some(r => r.text === '短导语摘要句')));
});
