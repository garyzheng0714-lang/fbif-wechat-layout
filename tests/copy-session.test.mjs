// 契约测试：copy-session.js —— 复制链路竞态守卫（S2）。
// 意图：守卫的语义是「慢速旧异步任务不得把结果写进新文章的复制层」（跨文章污染，
// 真实事故见 66535c8）。此前靠两个裸计数器 + 每个重置点手工成对递增维持；本测试
// 锁定统一抽象的三条硬契约：capture 后 invalidate 必 stale、独立 gate 互不影响、
// 文章级重置一次作废全部 gate。改弱任何一条都会让跨文章污染类 bug 复活。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { parseHTML } from 'linkedom';

const { createVersionGate, createCopySession } = await import('../public/js/copy-session.js');

function extractFunction(source, name) {
  const start = source.indexOf(`function ${name}`);
  assert.notEqual(start, -1, `${name} should exist`);
  // showPreview has a default object argument (`opts = {}`), so the first
  // opening brace after the name is not the function body.
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

function extractAssignedFunctionExpression(source, assignment) {
  const start = source.indexOf(assignment);
  assert.notEqual(start, -1, `${assignment} should exist`);
  const functionStart = source.indexOf('async function', start);
  assert.notEqual(functionStart, -1, `${assignment} should assign an async function`);
  const bodyStart = source.indexOf('{', functionStart);
  let depth = 0;
  for (let i = bodyStart; i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}') depth--;
    if (depth === 0) return source.slice(functionStart, i + 1);
  }
  assert.fail(`${assignment} function body should be complete`);
}

function extractExactAsyncFunction(source, signature) {
  const start = source.indexOf(signature);
  assert.notEqual(start, -1, `${signature} should exist`);
  const signatureEnd = source.indexOf(') {', start);
  const bodyStart = signatureEnd + 2;
  let depth = 0;
  for (let i = bodyStart; i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}') depth--;
    if (depth === 0) return source.slice(start, i + 1);
  }
  assert.fail(`${signature} should be complete`);
}

function extractAssignedAsyncArrow(source, assignment) {
  const assignmentStart = source.indexOf(assignment);
  assert.notEqual(assignmentStart, -1, `${assignment} should exist`);
  const start = source.indexOf('async (', assignmentStart);
  const bodyStart = source.indexOf('{', start);
  let depth = 0;
  for (let i = bodyStart; i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}') depth--;
    if (depth === 0) return source.slice(start, i + 1);
  }
  assert.fail(`${assignment} arrow should be complete`);
}

function deferred() {
  let resolve;
  const promise = new Promise(r => { resolve = r; });
  return { promise, resolve };
}

function makePreviewRaceHarness(engine, { skipUpload = false } = {}) {
  const { document, window } = parseHTML(`<!doctype html><html><body>
    <div id="emptyState"></div><div id="previewContent"></div><div id="wxFooter"></div>
    <input id="titleInput"><div id="previewTitleDisplay"></div><div id="contentArea"></div>
    <div id="statsBar"></div><button id="retryBtn">retry</button>
  </body></html>`);
  const copySession = createCopySession();
  const uploads = [];
  const materializations = [];
  const marked = [];
  const logs = [];
  const deps = {
    window,
    document,
    template: { id: 'fbif', transferMmbiz: false },
    copySession,
    footerCopyGate: copySession.footerCopyGate,
    moreArticlesGate: copySession.moreArticlesGate,
    moreArticlesPreviewGate: copySession.moreArticlesPreviewGate,
    articleResultGate: copySession.articleResultGate,
    normalizeRich: value => String(value || ''),
    normalizeWechatRichHtml: value => String(value || ''),
    showImportSummary() {},
    applyThemeVars() {},
    hookPreviewImages() {},
    renderPreviewContent(container, articleHtml, footerHtml) {
      container.innerHTML = String(articleHtml || '') + String(footerHtml || '');
      return [];
    },
    renderPreviewFooter(container, footerHtml) {
      container.innerHTML = String(deps._articleHtml || '') + String(footerHtml || '');
      return false;
    },
    settledSidebarStatus: value => `settled:${value}`,
    uploadStatus: (stable, status) => `${stable}|${status}`,
    dispatchArticleEvent() {},
    isFooterEnabledForCurrentArticle: () => true,
    markFailedImages(value) { marked.push(Array.from(value || [])); },
    hasDocumentSummaryCard: () => false,
    copySafeFooterAfterImagePreparation: value => `safe:${value}`,
    prepareMoreArticlesForCopy: () => Promise.resolve(false),
    saveMoreArticlesCards() {},
    loadMoreArticlesCards: () => [],
    loadBaseFooterHtml: async () => ({ html: '', isV2: false }),
    buildFooterWithMoreArticles: async value => value,
    logConv(...args) { logs.push(args); },
    materializeLazyImages(articleCopy, footerCopy, options) {
      const task = deferred();
      materializations.push({ articleCopy, footerCopy, options, ...task });
      return task.promise;
    },
    uploadNonCdnImages(articleCopy, footerCopy, options) {
      const task = deferred();
      uploads.push({ articleCopy, footerCopy, options, ...task });
      return task.promise;
    },
    _articleHtml: '',
    _articleCopy: '',
    _footerHtml: '',
    _footerCopy: '',
    _failedSrcs: [],
    _uploadPromise: null,
    _uploadDone: false,
    _sourceIsDocx: skipUpload,
    _copyRequiresWechatImageTransfer: false,
    _plainTextCopy: null,
    _articleSession: null,
    _lastRenderInput: null,
    _isRepost: false,
    _moreArticlesCopyJob: null,
  };
  deps.reconcileFailedImageRefs = function(value = deps._failedSrcs) {
    deps._failedSrcs = Array.from(value || []);
    return deps._failedSrcs;
  };
  window._skipUpload = skipUpload;
  const resetRetryUploadUISource = extractFunction(engine, 'resetRetryUploadUI');
  const showPreviewSource = extractFunction(engine, 'showPreview');
  const showPreview = new Function('deps', `with (deps) { ${resetRetryUploadUISource}; ${showPreviewSource}; return showPreview; }`)(deps);
  return { deps, document, showPreview, uploads, materializations, marked, logs };
}

test('capture 后未失效 → 不 stale；invalidate 后 → 所有在途 capture 变 stale', () => {
  const gate = createVersionGate('t');
  const a = gate.capture();
  assert.equal(a.stale(), false);
  gate.invalidate();
  assert.equal(a.stale(), true, '失效后旧任务必须被拒绝回写');
  const b = gate.capture();
  assert.equal(b.stale(), false, '失效后的新 capture 是新版本，不受旧失效影响');
});

test('多个在途 capture 同时失效（一次 invalidate 拒绝全部旧任务）', () => {
  const gate = createVersionGate('t');
  const a = gate.capture();
  const b = gate.capture();
  gate.invalidate();
  assert.equal(a.stale(), true);
  assert.equal(b.stale(), true);
});

test('两个 gate 相互独立：作废更多文章任务不影响 footer 写权（反向亦然）', () => {
  const s = createCopySession();
  const footer = s.footerCopyGate.capture();
  const ma = s.moreArticlesGate.capture();
  s.moreArticlesGate.invalidate();
  assert.equal(ma.stale(), true);
  assert.equal(footer.stale(), false, 'prepare 新一轮不应作废进行中的 footer 上传回写权之外的语义');
});

test('resetForNewArticle 一次作废全部 gate（新文章导入后旧任务全体免疫）', () => {
  const s = createCopySession();
  const input = s.articleInputGate.capture();
  const render = s.renderCommitGate.capture();
  const article = s.articleResultGate.capture();
  const footer = s.footerCopyGate.capture();
  const ma = s.moreArticlesGate.capture();
  const preview = s.moreArticlesPreviewGate.capture();
  const copy = s.copyActionGate.capture();
  const retry = s.retryGate.capture();
  s.resetForNewArticle();
  assert.equal(input.stale(), true);
  assert.equal(render.stale(), true);
  assert.equal(article.stale(), true);
  assert.equal(footer.stale(), true);
  assert.equal(ma.stale(), true);
  assert.equal(preview.stale(), true);
  assert.equal(copy.stale(), true);
  assert.equal(retry.stale(), true);
});

test('file 输入 A→B 且 A 后完成时，只允许 B activateArticleSession', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const handleFileSource = extractExactAsyncFunction(engine, 'async function handleFile(');
  const session = createCopySession();
  const tasks = [];
  const activated = [];
  const deps = {
    copySession: session,
    articleInputGate: session.articleInputGate,
    renderCommitGate: session.renderCommitGate,
    _articleInputPending: false,
    _articlePreparationController: null,
    AbortController,
    _rerenderAfterInput: true,
    _rerenderAfterInputOptions: { articleArtifactOverride: { from: 'old-file' } },
    clearQueuedInputRerender() {
      deps._rerenderAfterInput = false;
      deps._rerenderAfterInputOptions = null;
    },
    _isRepost: false,
    _convLog: [],
    dispatchArticleEvent() {}, logConv() {}, showImportEntry() {}, showError() {},
    sanitizeErrMsg: value => value,
    errorEl: { style: {} },
    dropZone: { classList: { add() {}, remove() {} } },
    progress: { style: {} }, progressFill: { style: {} }, progressText: {},
    performance: { now: () => 0 },
    normalizeToDocx: async file => file,
    waitForActiveRuleConfigReady: async () => {},
    parseFileToArticleInput: async file => ({ kind: 'md', id: file.name, title: file.name }),
    renderArticleInput(input) {
      const task = deferred();
      tasks.push({ id: input.id, ...task });
      return task.promise;
    },
    buildArticleSession: async (input, result) => ({ input, title: result.label }),
    refreshLocalPreviewImages: async images => images,
    formatRenderStats: result => result.label,
    activateArticleSession: article => activated.push(article.title),
    setTimeout: fn => fn(),
  };
  const handleFile = new Function('deps', `with (deps) { ${handleFileSource}; return handleFile; }`)(deps);
  const a = handleFile({ name: 'A', size: 1 }, {});
  for (let i = 0; i < 5; i++) await Promise.resolve();
  const b = handleFile({ name: 'B', size: 1 }, {});
  assert.equal(deps._rerenderAfterInput, false, '新文件意图必须清掉旧文章的排队重排');
  assert.equal(deps._rerenderAfterInputOptions, null);
  for (let i = 0; i < 5; i++) await Promise.resolve();
  const result = label => ({ label, lines: [], imgN: 0, headingN: 0 });
  tasks.find(task => task.id === 'B').resolve(result('B'));
  await b;
  tasks.find(task => task.id === 'A').resolve(result('A'));
  await a;
  assert.deepEqual(activated, ['B']);
});

test('DOCX A 图片预解码晚于 B 完成时，不得激活旧文章', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const handleFileSource = extractExactAsyncFunction(engine, 'async function handleFile(');
  const session = createCopySession();
  const decodes = [];
  const activated = [];
  const deps = {
    copySession: session,
    articleInputGate: session.articleInputGate,
    _articleInputPending: false,
    _articlePreparationController: null,
    AbortController,
    clearQueuedInputRerender() {},
    _isRepost: false,
    _convLog: [],
    dispatchArticleEvent() {}, logConv() {}, showImportEntry() {}, showError() {},
    sanitizeErrMsg: value => value,
    errorEl: { style: {} },
    dropZone: { classList: { add() {}, remove() {} } },
    progress: { style: {}, classList: { remove() {} } },
    progressFill: { style: {} },
    progressText: {},
    performance: { now: () => 0 },
    normalizeToDocx: async file => file,
    waitForActiveRuleConfigReady: async () => {},
    parseFileToArticleInput: async file => ({
      kind: 'docx',
      id: file.name,
      title: file.name,
      payload: { imgCache: { cover: `blob:${file.name}` } },
    }),
    preloadLocalPreviewImages(imageCache) {
      const task = deferred();
      decodes.push({ src: imageCache.cover, ...task });
      return task.promise;
    },
    renderArticleInput: async input => ({
      label: input.id,
      lines: [],
      imgN: 1,
      headingN: 0,
    }),
    buildArticleSession: async (input, result) => ({ input, title: result.label }),
    refreshLocalPreviewImages: async images => images,
    formatRenderStats: result => result.label,
    activateArticleSession: article => activated.push(article.title),
    setTimeout: fn => fn(),
  };
  const handleFile = new Function('deps', `with (deps) { ${handleFileSource}; return handleFile; }`)(deps);
  const a = handleFile({ name: 'A.docx', size: 1 }, {});
  for (let i = 0; i < 8; i++) await Promise.resolve();
  const b = handleFile({ name: 'B.docx', size: 1 }, {});
  for (let i = 0; i < 8; i++) await Promise.resolve();

  decodes.find(task => task.src === 'blob:B.docx').resolve(new Map());
  await b;
  decodes.find(task => task.src === 'blob:A.docx').resolve(new Map());
  await a;

  assert.deepEqual(activated, ['B.docx']);
});

test('公开的 _handleFile 必须返回排版 Promise，供飞书导入等待真实结果', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  assert.match(
    engine,
    /window\._handleFile\s*=\s*\(file,\s*opts\)\s*=>\s*\{[\s\S]*?return handleFile\(file,\s*opts\);[\s\S]*?\};/,
  );
});

test('blocks 输入 A→B 且 A 后完成时，只允许 B activateArticleSession', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const handleBlocksSource = extractAssignedAsyncArrow(engine, 'window._handleBlocks =');
  const session = createCopySession();
  const tasks = [];
  const activated = [];
  const deps = {
    copySession: session,
    articleInputGate: session.articleInputGate,
    renderCommitGate: session.renderCommitGate,
    _articleInputPending: false,
    _rerenderAfterInput: true,
    _rerenderAfterInputOptions: { articleArtifactOverride: { from: 'old-blocks' } },
    clearQueuedInputRerender() {
      deps._rerenderAfterInput = false;
      deps._rerenderAfterInputOptions = null;
    },
    _isRepost: false, _convLog: [],
    dispatchArticleEvent() {}, logConv() {}, clearUploadVisual() {}, showImportEntry() {}, showError() {},
    sanitizeErrMsg: value => value,
    errorEl: { style: {} },
    dropZone: { classList: { add() {}, remove() {} } },
    progress: { style: {} }, progressFill: { style: {} }, progressText: {},
    performance: { now: () => 0 }, template: { processBlocks() {} },
    waitForActiveRuleConfigReady: async () => {},
    normalizeArticleArtifactOverride: value => value,
    renderArticleInput(input) {
      const task = deferred();
      tasks.push({ id: input.title, ...task });
      return task.promise;
    },
    buildArticleSession: async (input, result) => ({ input, title: result.label }),
    formatRenderStats: result => result.label,
    activateArticleSession: article => activated.push(article.title),
    setTimeout: fn => fn(),
  };
  const handleBlocks = new Function('deps', `with (deps) { return (${handleBlocksSource}); }`)(deps);
  const a = handleBlocks({ title: 'A', blocks: [] }, {});
  for (let i = 0; i < 5; i++) await Promise.resolve();
  const b = handleBlocks({ title: 'B', blocks: [] }, {});
  assert.equal(deps._rerenderAfterInput, false, '新区块意图必须清掉旧文章的排队重排');
  assert.equal(deps._rerenderAfterInputOptions, null);
  for (let i = 0; i < 5; i++) await Promise.resolve();
  const result = label => ({ label, lines: [], imgN: 0, headingN: 0 });
  tasks.find(task => task.id === 'B').resolve(result('B'));
  await b;
  tasks.find(task => task.id === 'A').resolve(result('A'));
  await a;
  assert.deepEqual(activated, ['B']);
});

test('_engineRerender latest-wins：旧规则重排后返不得覆盖新规则', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const rerenderSource = extractExactAsyncFunction(engine, 'async function _engineRerender(');
  const session = createCopySession();
  const tasks = [];
  const activated = [];
  const deps = {
    renderCommitGate: session.renderCommitGate,
    _articleInputPending: false,
    _rerenderAfterInput: false,
    _rerenderAfterInputOptions: null,
    clearQueuedInputRerender() {
      deps._rerenderAfterInput = false;
      deps._rerenderAfterInputOptions = null;
    },
    _articleSession: { input: { id: 'article' } },
    _lastRenderInput: null,
    normalizeArticleArtifactOverride: value => value,
    renderArticleInput() {
      const task = deferred();
      tasks.push(task);
      return task.promise;
    },
    buildArticleSession: async (input, result) => ({ input, title: result }),
    refreshLocalPreviewImages: async images => images,
    activateArticleSession: article => activated.push(article.title),
    console,
  };
  const rerender = new Function('deps', `with (deps) { ${rerenderSource}; return _engineRerender; }`)(deps);
  const a = rerender();
  const b = rerender();
  tasks[1].resolve('B');
  await b;
  tasks[0].resolve('A');
  await a;
  assert.deepEqual(activated, ['B']);
});

test('规则在输入解析期变更：重排不丢失，输入结束后自动消费最新选项', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const queueSource = extractFunction(engine, 'queueRerenderAfterInput');
  const clearSource = extractFunction(engine, 'clearQueuedInputRerender');
  const flushSource = extractFunction(engine, 'flushRerenderAfterInput');
  const rerenderSource = extractExactAsyncFunction(engine, 'async function _engineRerender(');
  const calls = [];
  const deps = {
    _articleInputPending: true,
    _rerenderAfterInput: false,
    _rerenderAfterInputOptions: null,
    console,
  };
  deps.queueRerenderAfterInput = new Function('deps', `with (deps) { ${queueSource}; return queueRerenderAfterInput; }`)(deps);
  deps.clearQueuedInputRerender = new Function('deps', `with (deps) { ${clearSource}; return clearQueuedInputRerender; }`)(deps);
  const rerender = new Function('deps', `with (deps) { ${rerenderSource}; return _engineRerender; }`)(deps);

  assert.equal(await rerender({ autofillAuthorCard: true }), false);
  assert.equal(deps._rerenderAfterInput, true);
  assert.deepEqual(deps._rerenderAfterInputOptions, { autofillAuthorCard: true });

  deps._articleInputPending = false;
  deps._engineRerender = async options => { calls.push(options); return true; };
  const flush = new Function('deps', `with (deps) { ${flushSource}; return flushRerenderAfterInput; }`)(deps);
  assert.equal(flush(), true);
  await Promise.resolve();
  assert.deepEqual(calls, [{ autofillAuthorCard: true }]);
  assert.equal(deps._rerenderAfterInput, false);

  const activate = extractFunction(engine, 'activateArticleSession');
  assert.match(activate, /flushRerenderAfterInput\(\)/, '新输入激活后必须自动消费待处理重排');
  const rulesReady = engine.slice(engine.indexOf("window.addEventListener('fbif:rules-ready'"), engine.indexOf('function invalidatePendingArticleWork'));
  assert.match(rulesReady, /_articleInputPending\s*\|\|/, '首篇文章尚无 session 时，规则变更也必须进入待处理重排');
});

test('URL / 账号切换作废文件解析时，必须同步解锁上传区', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const invalidateSource = extractFunction(engine, 'invalidatePendingArticleWork');
  const session = createCopySession();
  const input = session.articleInputGate.capture();
  const render = session.renderCommitGate.capture();
  const article = session.articleResultGate.capture();
  const footer = session.footerCopyGate.capture();
  const more = session.moreArticlesGate.capture();
  const preview = session.moreArticlesPreviewGate.capture();
  const copy = session.copyActionGate.capture();
  const retry = session.retryGate.capture();
  const removed = [];
  let retryReset = 0;
  let preparationAborts = 0;
  const deps = {
    copySession: session,
    articleInputGate: session.articleInputGate,
    renderCommitGate: session.renderCommitGate,
    _articleInputPending: true,
    _articlePreparationController: {
      abort() { preparationAborts++; },
    },
    _rerenderAfterInput: true,
    _rerenderAfterInputOptions: { articleArtifactOverride: { from: 'old' } },
    _moreArticlesCopyJob: { old: true },
    clearQueuedInputRerender() {
      deps._rerenderAfterInput = false;
      deps._rerenderAfterInputOptions = null;
    },
    dropZone: { classList: { remove: name => removed.push(name) } },
    progress: { style: { display: 'block' } },
    progressFill: { style: { width: '70%' } },
    resetRetryUploadUI() { retryReset++; },
  };
  const invalidate = new Function('deps', `with (deps) { ${invalidateSource}; return invalidatePendingArticleWork; }`)(deps);
  invalidate();
  assert.equal(input.stale(), true);
  assert.equal(render.stale(), true);
  assert.equal(article.stale(), true);
  assert.equal(footer.stale(), true);
  assert.equal(more.stale(), true);
  assert.equal(preview.stale(), true);
  assert.equal(copy.stale(), true);
  assert.equal(retry.stale(), true);
  assert.equal(deps._articleInputPending, false);
  assert.equal(deps._articlePreparationController, null);
  assert.equal(preparationAborts, 1);
  assert.equal(deps._rerenderAfterInput, false);
  assert.equal(deps._rerenderAfterInputOptions, null);
  assert.equal(deps._moreArticlesCopyJob, null);
  assert.equal(deps.progress.style.display, 'none');
  assert.equal(deps.progressFill.style.width, '0%');
  assert.equal(retryReset, 1);
  assert.deepEqual(removed, ['processing']);
});

test('goBack / 快照恢复 / 纯文字都会终止 pending input，复制与重试有独立回写门', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const plain = extractFunction(engine, 'showPlainTextResult');
  const back = engine.slice(engine.indexOf('window.goBack = function'), engine.indexOf('window.zoom = function'));
  const restore = engine.slice(engine.indexOf('window._restoreSessionSnapshot = function'), engine.indexOf('window._restoreSessionSnapshot = function') + 900);
  const copy = extractAssignedFunctionExpression(engine, 'window.copyContent =');
  const retry = extractAssignedFunctionExpression(engine, 'window.retryUpload =');
  assert.match(plain, /copySession\.resetForNewArticle\(\)[\s\S]{0,100}_articleInputPending\s*=\s*false/);
  assert.match(back, /copySession\.resetForNewArticle\(\)[\s\S]{0,100}_articleInputPending\s*=\s*false/);
  assert.match(restore, /copySession\.resetForNewArticle\(\)[\s\S]{0,100}_articleInputPending\s*=\s*false/);
  assert.match(copy, /copyActionGate\.invalidate\(\)[\s\S]{0,100}copyActionGate\.capture\(\)/);
  assert.match(copy, /await _themeCSSReady;[\s\S]{0,100}copyAction\.stale\(\)/);
  assert.match(retry, /retryGate\.invalidate\(\)[\s\S]{0,100}retryGate\.capture\(\)/);
  assert.match(retry, /finally\s*\{/);
});

test('过期的更多文章任务失败不得记入当前文章日志', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const prepare = extractFunction(engine, 'prepareMoreArticlesForCopy');
  const catchIndex = prepare.indexOf('.catch(err =>');
  assert.notEqual(catchIndex, -1);
  assert.match(prepare.slice(catchIndex), /if \(!gate\.stale\(\)\)\s*logConv/);
});

test('文章 A 上传任务晚于 B 导入完成时，不得回写 B 的复制状态或 UI', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const { deps, document, showPreview, uploads, marked, logs } = makePreviewRaceHarness(engine);

  showPreview('A', '<p>article-A</p>', '<p>footer-A</p>', 'stats-A');
  showPreview('B', '<p>article-B</p>', '<p>footer-B</p>', 'stats-B');
  assert.equal(uploads.length, 2);

  const staleA = uploads[0];
  staleA.options.onProgress(1, 2);
  staleA.options.onLog('info', 'A should be stale');
  staleA.options.onMaterialized({
    articleCopy: '<p>materialized-A</p>',
    footerCopy: '<p>materialized-footer-A</p>',
    failedSrcs: ['a-materialize-failed'],
  });
  staleA.resolve({
    articleCopy: '<p>uploaded-A</p>',
    footerCopy: '<p>uploaded-footer-A</p>',
    failedSrcs: ['a-upload-failed'],
    failed: 1,
  });
  await staleA.promise;
  await Promise.resolve();

  assert.equal(deps._articleCopy, '<p>article-B</p>');
  assert.equal(deps._footerCopy, '<p>footer-B</p>');
  assert.deepEqual(deps._failedSrcs, []);
  assert.equal(deps._uploadDone, false, 'B 仍在上传，A 不得把它标记为完成');
  assert.equal(document.getElementById('statsBar').textContent, 'settled:stats-B');
  assert.equal(document.getElementById('retryBtn').style.display, 'none');
  assert.deepEqual(marked, []);
  assert.deepEqual(logs, [], '过期任务的异步日志也不应污染当前 UI');

  const currentB = uploads[1];
  currentB.resolve({
    articleCopy: '<p>uploaded-B</p>',
    footerCopy: '<p>uploaded-footer-B</p>',
    failedSrcs: [],
    failed: 0,
  });
  await currentB.promise;
  await Promise.resolve();
  assert.equal(deps._articleCopy, '<p>uploaded-B</p>', '当前任务仍必须能正常提交');
  assert.equal(deps._uploadDone, true);
});

test('DOCX 跳过上传时，旧 A 的延迟图片准备结果不得回写 B', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const { deps, document, showPreview, materializations, marked, logs } = makePreviewRaceHarness(engine, { skipUpload: true });

  showPreview('A', '<p>article-A</p>', '<p>footer-A</p>', 'stats-A');
  showPreview('B', '<p>article-B</p>', '<p>footer-B</p>', 'stats-B');
  assert.equal(materializations.length, 2);
  const logsBeforeStaleCallbacks = logs.slice();

  const staleA = materializations[0];
  staleA.options.onProgress(1, 1);
  staleA.options.onLog('info', 'stale materialize');
  staleA.resolve({
    articleCopy: '<p>materialized-A</p>',
    footerCopy: '<p>materialized-footer-A</p>',
    failedSrcs: ['a-failed'],
    failed: 1,
  });
  await staleA.promise;
  await Promise.resolve();

  assert.equal(deps._articleCopy, '<p>article-B</p>');
  assert.equal(deps._footerCopy, '<p>footer-B</p>');
  assert.deepEqual(deps._failedSrcs, []);
  assert.equal(deps._uploadDone, false);
  assert.equal(document.getElementById('statsBar').textContent, 'settled:stats-B');
  assert.deepEqual(marked, []);
  assert.deepEqual(logs, logsBeforeStaleCallbacks, '过期图片准备回调不得追加日志');
});

test('文章 A 重试上传期间导入 B，A 重试结果不得回写 B', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const retryExpression = extractAssignedFunctionExpression(engine, 'window.retryUpload =');
  const { document, window } = parseHTML('<!doctype html><html><body><div id="statsBar">B status</div><button id="retryBtn">retry</button></body></html>');
  const copySession = createCopySession();
  const task = deferred();
  let retryOptions;
  const marked = [];
  const logs = [];
  const deps = {
    window,
    document,
    articleResultGate: copySession.articleResultGate,
    footerCopyGate: copySession.footerCopyGate,
    retryGate: copySession.retryGate,
    retryFailedImages(articleCopy, footerCopy, failedSrcs, options) {
      retryOptions = options;
      return task.promise;
    },
    copySafeFooterAfterImagePreparation: value => `safe:${value}`,
    markFailedImages(value) { marked.push(Array.from(value || [])); },
    hasDocumentSummaryCard: () => false,
    logConv(...args) { logs.push(args); },
    reconcileFailedImageRefs(value = deps._failedSrcs) {
      deps._failedSrcs = Array.from(value || []);
      return deps._failedSrcs;
    },
    resetRetryUploadUI() {},
    _articleCopy: '<p>article-A</p>',
    _footerCopy: '<p>footer-A</p>',
    _failedSrcs: ['a-failed'],
  };
  const retryUpload = new Function('deps', `with (deps) { return (${retryExpression}); }`)(deps);

  const pending = retryUpload();
  copySession.resetForNewArticle();
  deps._articleCopy = '<p>article-B</p>';
  deps._footerCopy = '<p>footer-B</p>';
  deps._failedSrcs = [];
  document.getElementById('statsBar').textContent = 'B status';
  const retryBtn = document.getElementById('retryBtn');
  retryBtn.textContent = '重试上传';
  retryBtn.disabled = false;
  retryBtn.style.display = 'none';

  retryOptions.onProgress(1, 1);
  retryOptions.onLog('info', 'A retry stale');
  task.resolve({
    articleCopy: '<p>retried-A</p>',
    footerCopy: '<p>retried-footer-A</p>',
    failedSrcs: ['still-A'],
    failed: 1,
  });
  const result = await pending;

  assert.equal(result, false, '过期重试应显式返回未提交');
  assert.equal(deps._articleCopy, '<p>article-B</p>');
  assert.equal(deps._footerCopy, '<p>footer-B</p>');
  assert.deepEqual(deps._failedSrcs, []);
  assert.equal(document.getElementById('statsBar').textContent, 'B status');
  assert.equal(retryBtn.textContent, '重试上传');
  assert.equal(retryBtn.disabled, false);
  assert.equal(retryBtn.style.display, 'none');
  assert.deepEqual(marked, []);
  assert.deepEqual(logs, []);
});

// 机制防回退：engine.js 不得再出现裸计数器守卫（有人绕过 gate 加回 _footerCopyGen
// 之类的数字比较，就会重新引入「重置点忘记成对递增」的漏洞类）。
test('engine.js 已无裸计数器守卫', async () => {
  const { readFile } = await import('node:fs/promises');
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  // 匹配使用形态（声明/递增/数字比较），注释中提及历史名不算回退。
  assert.ok(!/let _footerCopyGen|_footerCopyGen\+\+|===? footerGenAtStart/.test(engine), 'engine.js 不应再有 _footerCopyGen 裸计数器用法');
  assert.ok(!/let _moreArticlesCopySeq|_moreArticlesCopySeq\+\+|!== _moreArticlesCopySeq/.test(engine), 'engine.js 不应再有 _moreArticlesCopySeq 裸计数器用法');
  assert.ok(engine.includes('copy-session.js'), 'engine.js 应使用 copy-session.js 的版本门');
});
