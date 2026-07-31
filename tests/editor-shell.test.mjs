import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

import {
  bindRailDivider,
  bindUrlImportShortcuts,
  createUrlImportHandler,
  foodTalksFetchErrorMessage,
  hasExplicitRepostDisclosure,
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
  assert.match(app, /switchAccount[\s\S]{0,500}fetchArticle\.invalidate\(\)/, '公众号切账号必须作废旧 URL 请求');
  assert.match(app, /clearRenderedImport[\s\S]{0,220}fetchArticle\.invalidate\(\)/, '公众号清空必须作废旧 URL 请求');
  assert.match(site, /onSwitch:\s*\(id\)[\s\S]{0,220}fetchArticle\.invalidate\(\)/, '网站切账号必须作废旧 URL 请求');
  assert.match(site, /clearRenderedImport[\s\S]{0,220}fetchArticle\.invalidate\(\)/, '网站清空必须作废旧 URL 请求');
  assert.match(app, /onArticleIntent\(\)[\s\S]{0,220}invalidatePendingWork/, 'URL 意图必须作废旧文件解析');
  assert.match(site, /onArticleIntent\(\)[\s\S]{0,220}invalidatePendingWork/, '网站 URL 意图必须作废旧文件解析');
  assert.match(app, /fbif:article-input-started[\s\S]{0,260}url-import[\s\S]{0,160}fetchArticle\.invalidate\(\)/,
    '新本地输入必须作废在途 URL 请求');
  assert.match(app, /bindUrlImportShortcuts/);
  assert.match(site, /bindUrlImportShortcuts/);
  assert.match(app, /tryImportValue:\s*value\s*=>\s*feishuDocImport\?\.importValue/);
  assert.match(site, /tryImportValue:\s*value\s*=>\s*feishuDocImport\?\.importValue/);
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
      /(?:trackActiveRuleHydration\(\s*)?hydrateActiveRulePreset\((?:activeRulePack\.id|initialHydrationAccountID)\)/,
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

test('single-article product has no unreachable batch UI or documentation promise', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const site = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const css = await readFile(new URL('../public/app.css', import.meta.url), 'utf8');
  const readme = await readFile(new URL('../README.md', import.meta.url), 'utf8');

  assert.match(engine, /fileInputEl\.multiple = false/);
  assert.doesNotMatch(engine, /showBatchList|_batchResults|batch_state|batch-upload/);
  assert.doesNotMatch(app + site, /id="batchList"/);
  assert.doesNotMatch(css, /\.batch-(?:list|title|item|status)/);
  assert.doesNotMatch(readme, /支持多文件队列处理/);
  assert.match(readme, /编辑器一次只处理一篇文章/);
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
    /if \(!hasImageLikeLink\(body\)\) \{[\s\S]{0,140}_handleBlocks\(buildClipboardArticleBlocks\(raw\), \{ \.\.\.opts, isRepost: false, bodyOnly: true \}\);[\s\S]{0,40}return;/,
    '无图片的剪贴板正文应进入公众号正文排版，但不应生成完整公众号头尾'
  );
  assert.doesNotMatch(
    engine,
    /if \(!hasImageLikeLink\(body\)\) \{[\s\S]{0,120}showPlainTextResult\(body\);[\s\S]{0,40}return;/,
    '正文粘贴不能再掉进纯文字结果页，否则字体和公众号正文不一致'
  );
  assert.match(
    engine,
    /document\.body\.classList\.add\('has-preview', 'plain-text-preview'\)/,
    '纯文字结果应占用右侧预览区，而不是弹出小窗口'
  );
  assert.match(
    engine,
    /if \(_plainTextCopy !== null\) \{[\s\S]{0,260}const text = getPlainTextCopyValue\(\);[\s\S]{0,700}copyPlainTextToClipboard\(text\)/,
    '顶部复制全文在纯文字模式应复制结果文本区当前内容，而不是复用公众号 HTML 复制链路'
  );
  assert.match(
    engine,
    /const text = getPlainTextCopyValue\(\);[\s\S]{0,180}if \(!String\(text \|\| ''\)\.trim\(\)\)/,
    '纯文字结果为空时必须在写剪贴板前 fail closed'
  );
  assert.doesNotMatch(
    engine,
    /data-plain-text-dialog|ensurePlainTextDialog|plain-text-modal/,
    '纯文字结果不应退回弹窗实现'
  );
  assert.match(
    css,
    /\.empty-cta svg \{[^}]*opacity: 1;[^}]*color: #fff;/,
    '选择文件按钮里的上传图标不能继承空状态弱化透明度'
  );
  assert.match(
    css,
    /\.plain-text-page-output \{[\s\S]{0,260}-webkit-text-fill-color: #1F2329;/,
    '纯文字结果页应使用明确正文色，不能像禁用提示一样发灰'
  );
  assert.match(
    app,
    /window\.addEventListener\('fbif:plain-text-ready'[\s\S]{0,180}articleEditorActivated = false/,
    '纯文字结果页不应打开更多文章或底部模块侧栏'
  );
  assert.match(
    engine,
    /showPreview\(session\.title \|\| '', session\.articleHtml, session\.footerHtml, session\.stats, session\.displayName \|\| session\.title \|\| '', \{[\s\S]{0,80}bodyOnly: _articleBodyOnly/,
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
  let blockMeta = null;
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
    onBlocks(_blocks, meta) {
      blockMeta = meta;
    },
  });

  await handler();

  assert.equal(calls[1].currentUrl, 'https://mp.weixin.qq.com/s/abc');
  assert.deepEqual(requests[0], {
    url: '/api/fetch-article',
    body: { url: 'https://mp.weixin.qq.com/s/abc', account: 'ft-default' },
  });
  assert.deepEqual(calls[0].payload, {
    title: '标题',
    blocks: [{ type: 'p', text: '正文' }],
    source: 'https://mp.weixin.qq.com/s/abc',
  });
  assert.deepEqual(calls[0].options, { isRepost: true, source: 'url-import' });
  assert.deepEqual(blockMeta, { title: '标题', url: 'https://mp.weixin.qq.com/s/abc' }, '摘要链路仍必须拿到来源 URL');
  assert.equal(elements.get('importZone').hidden, false);
  assert.equal(elements.get('docZone').hidden, true);
  assert.equal(elements.get('progress').style.display, 'block');
  assert.equal(elements.get('progressFill').style.width, '30%');
  assert.equal(elements.get('progressText').textContent, '正在提取...');
});

test('createUrlImportHandler lets the website channel classify an owned WeChat source as original', async () => {
  const calls = [];
  const elements = new Map();
  const makeElement = value => ({ value, textContent: '', disabled: false, hidden: false, style: { display: '' } });
  elements.set('urlInput', makeElement('https://mp.weixin.qq.com/s/fbif-original'));
  elements.set('urlFetchBtn', makeElement(''));
  elements.set('error', makeElement(''));
  elements.set('importZone', makeElement(''));
  elements.set('docZone', makeElement(''));
  elements.set('progress', makeElement(''));
  elements.set('progressFill', { style: { width: '' } });
  elements.set('progressText', makeElement(''));

  const handler = createUrlImportHandler({
    documentRef: { getElementById: id => elements.get(id) },
    fetchImpl: async () => ({
      ok: true,
      json: async () => ({
        title: 'FBIF 原创',
        source_name: 'FBIF食品饮料创新',
        byline: 'Sage',
        blocks: [{ k: 'txt', runs: [{ type: 'txt', text: '作者：Sage' }] }],
      }),
    }),
    getAccountID: () => 'ft-default',
    getHandleBlocks: () => (payload, options) => calls.push({ payload, options }),
    resolveIsRepost: payload => payload.source_name !== 'FBIF食品饮料创新',
    onArticleUrl: (url, meta) => calls.push({ url, meta }),
  });

  await handler();

  assert.equal(calls[0].options.isRepost, false);
  assert.equal(calls[0].payload.source_name, 'FBIF食品饮料创新');
  assert.equal(calls[0].payload.sourceName, undefined, '结构化渲染载荷必须保留 API 的 source_name 字段契约');
  assert.equal(calls[0].payload.byline, 'Sage');
  assert.deepEqual(calls[1].meta, {
    isRepost: false,
    sourceName: 'FBIF食品饮料创新',
    byline: 'Sage',
  });
});

test('网站转载声明优先于公众号归属，避免把代发转载稿误判为原创', () => {
  assert.equal(hasExplicitRepostDisclosure([
    { k: 'txt', runs: [{ type: 'txt', text: '* 本文为转载文章。' }] },
  ]), true);
  assert.equal(hasExplicitRepostDisclosure([
    { k: 'txt', runs: [{ type: 'txt', text: '* 本文为转载文章，原文标题：《示例》。' }] },
  ]), true);
  assert.equal(hasExplicitRepostDisclosure([
    { k: 'txt', runs: [{ type: 'txt', text: '本文为原创文章。' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '正文提到转载文章的行业现象。' }] },
  ]), false);
});

test('URL 只在成功交给渲染器后提交：抓取或 handoff 失败保留旧稿来源', async () => {
  const elements = new Map();
  const makeElement = value => ({ value, textContent: '', disabled: false, hidden: false, style: { display: '' } });
  elements.set('urlInput', makeElement('https://example.test/fetch-fails'));
  elements.set('urlFetchBtn', makeElement('导入'));
  elements.set('error', makeElement(''));
  elements.set('importZone', makeElement(''));
  elements.set('docZone', makeElement(''));
  elements.set('progress', makeElement(''));
  elements.set('progressFill', { style: { width: '' } });
  elements.set('progressText', makeElement(''));
  const documentRef = { getElementById: id => elements.get(id) };
  const intents = [];
  let committedUrl = 'https://example.test/old-preview';
  let mode = 'fetch-fails';
  const handler = createUrlImportHandler({
    documentRef,
    getAccountID: () => 'fbif',
    fetchImpl: async () => mode === 'fetch-fails'
      ? { ok: false, status: 502, json: async () => ({ error: '抓取失败' }) }
      : { ok: true, json: async () => ({ title: '新稿', blocks: [{ type: 'p', text: '正文' }] }) },
    getHandleBlocks: () => () => { throw new Error('渲染器未就绪'); },
    onArticleIntent: url => intents.push(url),
    onArticleUrl: url => { committedUrl = url; },
  });

  assert.equal(await handler(), false);
  assert.equal(committedUrl, 'https://example.test/old-preview');

  mode = 'handoff-fails';
  elements.get('urlInput').value = 'https://example.test/handoff-fails';
  assert.equal(await handler(), false);
  assert.equal(committedUrl, 'https://example.test/old-preview');
  assert.deepEqual(intents, [
    'https://example.test/fetch-fails',
    'https://example.test/handoff-fails',
  ], '新 URL 意图仍应立即作废旧异步工作，只是不提前改来源 URL');
});

test('createUrlImportHandler latest-wins：A→B 且 A 后返时只交给渲染器 B', async () => {
  const elements = new Map();
  const makeElement = value => ({ value, textContent: '', disabled: false, hidden: false, style: { display: '' } });
  elements.set('urlInput', makeElement(''));
  elements.set('urlFetchBtn', makeElement('导入'));
  elements.set('error', makeElement(''));
  elements.set('importZone', makeElement(''));
  elements.set('docZone', makeElement(''));
  elements.set('progress', makeElement(''));
  elements.set('progressFill', { style: { width: '' } });
  elements.set('progressText', makeElement(''));
  const documentRef = { getElementById: id => elements.get(id) };
  const pending = [];
  const handedOff = [];
  const fetchImpl = (url, init) => {
    let resolve;
    const promise = new Promise(r => { resolve = r; });
    pending.push({ requested: JSON.parse(init.body).url, resolve });
    return promise;
  };
  const handler = createUrlImportHandler({
    documentRef,
    fetchImpl,
    getAccountID: () => 'fbif',
    getHandleBlocks: () => payload => handedOff.push(payload.title),
  });

  elements.get('urlInput').value = 'https://example.test/a';
  const requestA = handler();
  elements.get('urlInput').value = 'https://example.test/b';
  const requestB = handler();
  const response = title => ({ ok: true, json: async () => ({ title, blocks: [{ type: 'p', text: title }] }) });
  pending[1].resolve(response('B'));
  await requestB;
  pending[0].resolve(response('A'));
  await requestA;

  assert.deepEqual(handedOff, ['B'], '旧 A 响应不得在 B 之后重新导入');
  assert.equal(elements.get('error').style.display, 'none');
});

test('createUrlImportHandler invalidate / 账号变更会作废在途响应及旧错误 UI', async () => {
  const elements = new Map();
  const makeElement = value => ({ value, textContent: '', disabled: false, hidden: false, style: { display: '' } });
  elements.set('urlInput', makeElement('https://example.test/a'));
  elements.set('urlFetchBtn', makeElement('导入'));
  elements.set('error', makeElement(''));
  elements.set('importZone', makeElement(''));
  elements.set('docZone', makeElement(''));
  elements.set('progress', makeElement(''));
  elements.set('progressFill', { style: { width: '' } });
  elements.set('progressText', makeElement(''));
  const documentRef = { getElementById: id => elements.get(id) };
  let accountID = 'fbif';
  let rejectFetch;
  let handedOff = 0;
  const handler = createUrlImportHandler({
    documentRef,
    fetchImpl: () => new Promise((resolve, reject) => { rejectFetch = reject; }),
    getAccountID: () => accountID,
    getHandleBlocks: () => () => { handedOff++; },
  });

  const request = handler();
  accountID = 'wowfood';
  handler.invalidate();
  rejectFetch(new Error('old request failed'));
  await request;

  assert.equal(handedOff, 0);
  assert.equal(elements.get('error').textContent, '', '作废请求的错误不得污染新账号 UI');
  assert.equal(elements.get('urlFetchBtn').textContent, '导入', '作废动作必须立即恢复按钮');
  assert.equal(elements.get('urlFetchBtn').disabled, false);
  assert.equal(elements.get('progress').style.display, 'none', '作废动作必须收起旧请求进度');
  assert.equal(elements.get('progressFill').style.width, '0%');
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

test('bindUrlImportShortcuts leaves @ document mentions to the Feishu document controller', () => {
  const inputHandlers = {};
  let imported = 0;
  let fetched = 0;
  const urlInput = {
    value: '@新品趋势',
    tagName: 'TEXTAREA',
    classList: { contains: () => false },
    addEventListener(type, handler) { inputHandlers[type] = handler; },
  };
  const documentRef = {
    activeElement: urlInput,
    getElementById(id) {
      if (id === 'urlInput') return urlInput;
      if (id === 'previewContent') return { style: { display: 'none' } };
      return null;
    },
    addEventListener() {},
    querySelectorAll() { return []; },
  };
  bindUrlImportShortcuts({
    documentRef,
    fetchArticle: () => { fetched += 1; },
    importText: () => { imported += 1; },
    shouldSubmitValue: value => !value.startsWith('@'),
  });
  let prevented = false;
  inputHandlers.keydown({
    key: 'Enter',
    metaKey: true,
    ctrlKey: false,
    preventDefault() { prevented = true; },
  });
  assert.equal(prevented, true);
  assert.equal(imported, 0);
  assert.equal(fetched, 0);
});

test('bindUrlImportShortcuts gives a recognized special link to its importer before generic URL fetch', () => {
  const inputHandlers = {};
  let specialValue = '';
  let fetched = 0;
  const urlInput = {
    value: 'https://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc',
    tagName: 'TEXTAREA',
    classList: { contains: () => false },
    addEventListener(type, handler) { inputHandlers[type] = handler; },
  };
  const documentRef = {
    activeElement: urlInput,
    getElementById(id) {
      if (id === 'urlInput') return urlInput;
      if (id === 'previewContent') return { style: { display: 'none' } };
      return null;
    },
    addEventListener() {},
    querySelectorAll() { return []; },
  };
  bindUrlImportShortcuts({
    documentRef,
    fetchArticle: () => { fetched += 1; },
    tryImportValue(value) {
      specialValue = value;
      return true;
    },
  });

  let prevented = false;
  inputHandlers.keydown({
    key: 'Enter',
    metaKey: true,
    ctrlKey: false,
    preventDefault() { prevented = true; },
  });

  assert.equal(prevented, true);
  assert.equal(specialValue, urlInput.value);
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

test('extractSiteSummary：作者/编辑开头不是摘要，且不得从正文删除', async () => {
  const { extractSiteSummary } = await import('../public/js/editor-shell.js');
  const blocks = [
    { k: 'txt', runs: [{ type: 'txt', text: '作者：Sage' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '编辑：Bobo' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '在上海南京东路，你可能会看到一家坚果零食店。' }] },
  ];
  const before = structuredClone(blocks);

  assert.equal(extractSiteSummary(blocks, { consume: true }), '');
  assert.deepEqual(blocks, before, '作者行、编辑行和首段正文都必须保留');
});
