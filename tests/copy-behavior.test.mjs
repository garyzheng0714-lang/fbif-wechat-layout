import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { copyCompositeCacheKey, copySafeMoreArticleCard, mergeIntoFooter } from '../public/js/more-articles.js';
import { normalizeWechatRichHtml } from '../public/js/wechat-normalize.js';

function extractFunction(source, name) {
  const start = source.indexOf(`function ${name}`);
  assert.notEqual(start, -1, `${name} should exist`);
  const bodyStart = source.indexOf('{', start);
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

function extractAssignedSyncFunctionExpression(source, assignment) {
  const start = source.indexOf(assignment);
  assert.notEqual(start, -1, `${assignment} should exist`);
  const functionStart = source.indexOf('function', start);
  assert.notEqual(functionStart, -1, `${assignment} should assign a function`);
  const bodyStart = source.indexOf('{', functionStart);
  let depth = 0;
  for (let i = bodyStart; i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}') depth--;
    if (depth === 0) return source.slice(functionStart, i + 1);
  }
  assert.fail(`${assignment} function body should be complete`);
}

test('copy button does not auto-open WeChat backend', async () => {
  const html = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const rulePresets = await readFile(new URL('../public/js/rule-presets.js', import.meta.url), 'utf8');
  const claudeGuide = await readFile(new URL('../CLAUDE.md', import.meta.url), 'utf8');
  assert.doesNotMatch(html, /window\.open\s*\(/);
  assert.doesNotMatch(html, /正在打开微信后台/);
  assert.doesNotMatch(claudeGuide, /复制后自动打开微信后台/);
  assert.doesNotMatch(claudeGuide, /window\.open\s*\(/);
  assert.doesNotMatch(html, /直接把图片引用写入 HTML/);
  assert.doesNotMatch(html, /仅当 DOCX 图片/);
  assert.doesNotMatch(html, /服务端缓存地址/);
  assert.match(rulePresets, /loadLocalRuleConfig/);
  assert.match(rulePresets, /skip_upload_restored_v3/);
  assert.match(rulePresets, /window\.localStorage\.setItem\(LEGACY_SKIP_UPLOAD_KEY, 'true'\)/);
});

test('copy keeps legacy DOCX skip-upload behavior', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  assert.doesNotMatch(engine, /hasDeferredCopyImages/);
  assert.match(engine, /materializeLazyImages/);
  assert.match(engine, /const skipUploadForThisFile = !!window\._skipUpload && _sourceIsDocx;/);
  assert.match(engine, /return ok;/);
});

test('网站渠道跳过公众号导向的图片归一化（内联 logo 不被强制居中）', async () => {
  // 红线#19/#20：normalizeWechatRichHtml 给所有 <img> 强制 display:block + margin:0 auto，
  // 会把网站页脚的内联 FoodTalks logo 误居中（预览与复制都中招）。网站渲染器输出已是干净
  // inline-style（正文图自带居中、logo 故意左对齐），须经渠道感知的 normalizeRich 跳过。
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  assert.match(engine, /function normalizeRich\(html\)/, '必须有渠道感知的 normalizeRich 包装器');
  assert.match(engine, /function isWebsiteTemplate\(\)/, '必须有网站模板能力判断');
  assert.match(
    engine,
    /if \(isWebsiteTemplate\(\)\) return html/,
    'normalizeRich 必须对 foodtalks-site 渠道跳过归一化'
  );
  // 预览/页脚更新/复制三条内容路径都须走 normalizeRich，而非裸 normalizeWechatRichHtml。
  assert.match(engine, /_articleHtml = normalizeRich\(articleContent\)/);
  assert.match(engine, /_footerHtml = bodyOnly \? '' : normalizeRich\(footerContent\)/);
  assert.match(engine, /html = normalizeRich\(html\)/, '复制路径必须走 normalizeRich');
});

test('copy click does not wait on background image or more-articles preparation', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyFn = engine.slice(engine.indexOf('window.copyContent = async function()'));
  assert.doesNotMatch(copyFn, /await _uploadPromise/);
  assert.doesNotMatch(copyFn, /await ensureCompositeReady/);
  assert.doesNotMatch(copyFn, /上传更多文章/);
  assert.match(copyFn, /prepareMoreArticlesForCopy\('copy-click'\)/);
});

test('copyContent writes clipboard before pending more-articles preparation resolves', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyExpr = extractAssignedFunctionExpression(engine, 'window.copyContent =');

  let prepareResolve;
  let prepareResolved = false;
  const pendingPreparation = new Promise(resolve => {
    prepareResolve = () => {
      prepareResolved = true;
      resolve(true);
    };
  });
  const events = [];
  const deps = {
    window: {},
    document: {
      getElementById(id) {
        if (id === 'contentArea') return { textContent: 'plain fallback' };
        if (id === 'copyBtn') {
          return {
            textContent: '复制全文',
            classList: { add() {}, toggle() {} },
            style: {},
          };
        }
        return null;
      },
    },
    _plainTextCopy: null,
    getPlainTextCopyValue: () => '',
    copyPlainTextToClipboard: async () => false,
    PLAIN_TEXT_RULE_LABEL: 'plain',
    _lastCopiedHtml: '',
    logConv() {},
    _articleBodyOnly: false,
    prepareMoreArticlesForCopy(reason) {
      events.push(`prepare:${reason}`);
      return pendingPreparation;
    },
    isFooterEnabledForCurrentArticle: () => true,
    _articleCopy: '<section class="wx-p">正文</section>',
    _footerCopy: '<section>更多文章安全副本</section>',
    _copyRequiresWechatImageTransfer: false,
    findWechatImageSrcs: () => [],
    _uploadPromise: null,
    _uploadDone: false,
    _themeCSSReady: Promise.resolve(''),
    processForCopy(html) { return html; },
    normalizeRich(html) { return html; },
    findUnsafeCopyImageRefs: () => [],
    async copyByClipboardApi(html) {
      events.push(`clipboard:${prepareResolved ? 'after' : 'before'}`);
      deps.copiedHtml = html;
      return true;
    },
    async copyByClipboardEvent() {
      events.push('fallback');
      return false;
    },
    _failedSrcs: [],
    Array,
    Set,
  };
  deps.window._copyImageWarning = 0;

  const copyContent = new Function('deps', `with (deps) { return (${copyExpr}); }`)(deps);
  const result = await copyContent();

  assert.equal(result, true);
  assert.equal(prepareResolved, false, 'copyContent must finish before more-articles preparation resolves');
  assert.deepEqual(events, ['prepare:copy-click', 'clipboard:before']);
  assert.match(deps.copiedHtml, /更多文章安全副本/);

  prepareResolve();
  await pendingPreparation;
});

test('more articles prepared footer copy is projected through copy-safe cards before replacing the copy layer', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const prepareFn = engine.slice(
    engine.indexOf('function prepareMoreArticlesForCopy'),
    engine.indexOf('function showPreview')
  );

  assert.match(prepareFn, /const copySafeCards = uploaded\.map\(copySafeMoreArticleCard\)/);
  assert.match(prepareFn, /const mergedFooter = normalizeWechatRichHtml\(mergeMoreArticlesIntoFooter\(baseFooter, copySafeCards\)\)/);
  assert.doesNotMatch(prepareFn, /await uploadNonCdnImages\('', mergedFooter,/);
  assert.match(prepareFn, /_footerCopy = mergedFooter/);
});

test('background image preparation re-projects MP footer copy through copy-safe more-article cards before writeback', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  assert.match(engine, /function isWebsiteTemplate\(\)/);
  assert.match(engine, /function copySafeFooterAfterImagePreparation\(footerCopy\)/);
  const websiteHelper = extractFunction(engine, 'isWebsiteTemplate');
  assert.match(websiteHelper, /_activeTemplate\.transferMmbiz/);
  assert.match(websiteHelper, /_activeTemplate\.id === 'foodtalks-site'/);
  assert.doesNotMatch(websiteHelper, /_activeTemplate\.channel === 'site'/);
  const helper = extractFunction(engine, 'copySafeFooterAfterImagePreparation');
  assert.match(helper, /if \(isWebsiteTemplate\(\)\) return footerCopy/);
  assert.match(helper, /const cards = loadMoreArticlesCards\(\)/);
  assert.match(helper, /cards\.map\(copySafeMoreArticleCard\)/);
  assert.match(helper, /mergeMoreArticlesIntoFooter\(footerCopy, copySafeCards\)/);
  assert.match(engine, /_footerCopy = copySafeFooterAfterImagePreparation\(result\.footerCopy\)/);
});

test('retry upload also re-projects footer copy through copy-safe cards', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const retryFn = extractAssignedFunctionExpression(engine, 'window.retryUpload =');

  assert.match(retryFn, /_footerCopy = copySafeFooterAfterImagePreparation\(result\.footerCopy\)/);
  assert.doesNotMatch(retryFn, /_footerCopy = result\.footerCopy/);
});

test('restored session footer copy is re-projected before entering the copy layer', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const restoreStart = engine.indexOf('window._restoreSessionSnapshot = function');
  assert.notEqual(restoreStart, -1, 'restore snapshot hook should exist');
  const restoreFn = engine.slice(restoreStart, engine.indexOf('function restoredImportInputValue', restoreStart + 1) > -1
    ? engine.indexOf('function restoredImportInputValue', restoreStart + 1)
    : engine.indexOf('window._debugState', restoreStart));

  assert.match(restoreFn, /_footerCopy = copySafeFooterAfterImagePreparation\(cs\.footer_copy \|\| ''\)/);
  assert.doesNotMatch(restoreFn, /_footerCopy = cs\.footer_copy \|\| ''/);
});

test('restoreSessionSnapshot behavior rewrites legacy unsafe footer_copy to a copy-safe footer', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const restoreExpr = extractAssignedSyncFunctionExpression(engine, 'window._restoreSessionSnapshot =');
  const copySafeFooterHelper = extractFunction(engine, 'copySafeFooterAfterImagePreparation');
  const mmbizCover = 'https://mmbiz.qpic.cn/sz_mmbiz_jpg/restore-safe-cover/0?wx_fmt=jpeg';
  const ossComposite = 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/restore-unsafe.png';
  const anchorFooter = [
    '<section data-role="paragraph">',
    '<section style="margin:20px 8px 20px;padding:0;text-align:center;"><strong>/ 更多文章 /</strong></section>',
    '<section style="text-align: center;"></section>',
    '</section>',
  ].join('\n');
  const legacyUnsafeFooterCopy = mergeIntoFooter(anchorFooter, [
    { href: 'https://mp.weixin.qq.com/s/restore', imgurl: mmbizCover, final_url: ossComposite, title: '旧快照卡片' },
  ]);
  assert.match(legacyUnsafeFooterCopy, /oss-cn-hangzhou/, 'legacy snapshot fixture must contain an unsafe external card image');

  const actualProjector = new Function('deps', `
    with (deps) {
      ${copySafeFooterHelper}
      return copySafeFooterAfterImagePreparation;
    }
  `)({
    isWebsiteTemplate: () => false,
    loadMoreArticlesCards: () => [{
      href: 'https://mp.weixin.qq.com/s/restore',
      imgurl: mmbizCover,
      final_url: ossComposite,
      title: '旧快照卡片',
    }],
    copySafeMoreArticleCard,
    mergeMoreArticlesIntoFooter: mergeIntoFooter,
    normalizeWechatRichHtml,
  });

  const element = (extra = {}) => ({
    textContent: '',
    value: '',
    innerHTML: '',
    style: {},
    classList: { add() {}, toggle() {} },
    setAttribute() {},
    removeAttribute() {},
    querySelector() { return null; },
    ...extra,
  });
  const projectedInputs = [];
  const deps = {
    window: {},
    document: {
      body: { classList: { toggle() {} } },
      getElementById(id) {
        if (id === 'previewScroll') return element({ scrollTop: 0 });
        return element();
      },
    },
    copySafeFooterAfterImagePreparation(html) {
      projectedInputs.push(html);
      return actualProjector(html);
    },
    restoredImportInputValue: () => '',
    normalizeFooterVisibilityOverride: value => value || null,
    normalizeArticleArtifactOverride: value => value || null,
    showImportSummary() {},
    formatSidebarStatus: text => text,
    resetImportEntry() {},
    dispatchArticleEvent(type) { deps.dispatchedType = type; },
    PLAIN_TEXT_RULE_LABEL: 'plain',
    _articleHtml: '',
    _articleCopy: '',
    _footerHtml: '',
    _footerCopy: '',
    // S2：裸计数器已收敛为 copy-session 版本门；恢复路径必须调用文章级重置作废在途回写。
    copySessionResetCalls: 0,
    copySession: { resetForNewArticle() { deps.copySessionResetCalls++; } },
    _plainTextCopy: null,
    _isRepost: false,
    _sourceIsDocx: false,
    _articleBodyOnly: false,
    _copyRequiresWechatImageTransfer: false,
    _failedSrcs: [],
    _uploadDone: false,
    _uploadPromise: null,
    _moreArticlesCopyJob: null,
    _lastRenderInput: null,
    _articleSession: null,
    _batchFiles: [],
    _batchResults: [],
  };

  const restoreSessionSnapshot = new Function('deps', `with (deps) { return (${restoreExpr}); }`)(deps);
  const ok = restoreSessionSnapshot({
    schema_version: 1,
    closure_state: { footer_copy: legacyUnsafeFooterCopy },
    dom: {},
  });

  assert.equal(ok, true);
  assert.deepEqual(projectedInputs, [legacyUnsafeFooterCopy]);
  assert.doesNotMatch(deps._footerCopy, /oss-cn-hangzhou/, 'restored copy layer must not keep unsafe external card images');
  assert.doesNotMatch(deps._footerCopy, /linktype="image"/, 'restored unsafe legacy card must fail closed instead of reverting to the original cover');
  assert.doesNotMatch(deps._footerCopy, new RegExp(mmbizCover.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), 'restored copy layer must not silently use the no-title original cover');
  assert.equal(deps.copySessionResetCalls, 1, '恢复快照必须做一次文章级重置（作废在途异步回写）');
  assert.equal(deps.dispatchedType, 'fbif:article-restored');
});

test('preview footer and copy footer are separate so preview data images do not leak into copy', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  assert.match(engine, /ensureCopyCompositeDataUrl/, 'copy-safe 层必须走严格合成，不得复用会吞错的 preview helper');
  assert.match(engine, /async function copySafeMoreArticleCardForFooter/, '初始复制层必须有复制专用卡片生成器');
  assert.match(
    engine,
    /await Promise\.all\(cards\.map\(copySafeMoreArticleCardForFooter\)\)/,
    '初始 copy-safe footer 必须先生成本地合成位图，不能直接退回原始封面'
  );
  const helper = extractFunction(engine, 'copySafeMoreArticleCardForFooter');
  assert.doesNotMatch(helper, /compositePreviewDataUrl/, 'copy-safe 生成器不得调用会失败后退回 imgurl 的 preview helper');
  assert.match(engine, /footerCopy = await buildFooterWithMoreArticles\(baseFooterHtml, \{ isRepost, isV2, copySafe: true \}\)/);
  assert.match(engine, /footerCopy: session\.footerCopy/);
  assert.match(engine, /_footerCopy = bodyOnly \? '' : normalizeRich\(\(opts && opts\.footerCopy\) \|\| footerContent\)/);
  assert.match(engine, /const copySafeMerged = await buildFooterWithMoreArticles\(base, \{ isRepost: rp, isV2, copySafe: true \}\)/);
  assert.match(engine, /_articleSession\.footerCopy = copySafeMerged/);
});

test('initial copy-safe more-article helper emits data bitmap and fails closed on composite errors', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copySafeHelper = new Function('deps', `
    with (deps) {
      ${extractFunction(engine, 'moreArticleCardNeedsCopyComposite')}
      async ${extractFunction(engine, 'copySafeMoreArticleCardForFooter')}
      return copySafeMoreArticleCardForFooter;
    }
  `)({
    ensureCopyCompositeDataUrl: async (card) => {
      card.copy_data_url = 'data:image/png;base64,initial-copy-composite';
      card.copy_data_key = copyCompositeCacheKey(card);
      return card.copy_data_url;
    },
    copySafeMoreArticleCard,
  });

  const card = {
    href: 'https://mp.weixin.qq.com/s/a',
    imgurl: 'https://mmbiz.qpic.cn/sz_mmbiz_jpg/cover-a/0?wx_fmt=jpeg',
    title: '初始复制层必须有标题',
  };
  const copyCard = await copySafeHelper(card);
  const merged = mergeIntoFooter([
    '<section data-role="paragraph">',
    '<section style="margin:20px 8px 20px;padding:0;text-align:center;"><strong>/ 更多文章 /</strong></section>',
    '<section style="text-align: center;" data-card-inset="8"></section>',
    '</section>',
  ].join('\n'), [copyCard]);

  assert.match(merged, /linktype="image"/);
  assert.match(merged, /imgurl="https:\/\/mmbiz\.qpic\.cn\/sz_mmbiz_jpg\/cover-a\/0\?wx_fmt=jpeg"/);
  assert.match(merged, /src="data:image\/png;base64,initial-copy-composite"/);

  const failingHelper = new Function('deps', `
    with (deps) {
      ${extractFunction(engine, 'moreArticleCardNeedsCopyComposite')}
      async ${extractFunction(engine, 'copySafeMoreArticleCardForFooter')}
      return copySafeMoreArticleCardForFooter;
    }
  `)({
    ensureCopyCompositeDataUrl: async () => {
      throw new Error('copy composite failed');
    },
    copySafeMoreArticleCard,
  });
  await assert.rejects(
    () => failingHelper({ ...card, title: '失败不能复制原封面' }),
    /copy composite failed/,
    '初始 copy-safe 合成失败必须阻断，不能继续生成无标题原封面'
  );
});

test('copy warning is derived from final HTML unsafe image src and imgurl refs', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyFn = engine.slice(engine.indexOf('window.copyContent = async function()'));

  assert.match(engine, /function findUnsafeCopyImageRefs\(html, requiresWechatTransfer\)/);
  assert.match(engine, /src\|imgurl/);
  assert.match(copyFn, /const unsafeCopyRefs = findUnsafeCopyImageRefs\(html, _copyRequiresWechatImageTransfer\)/);
  assert.match(copyFn, /new Set\(\[ \.\.\.\(_failedSrcs \|\| \[\]\), \.\.\.unsafeCopyRefs \]\)/);
});

test('materialized DOCX image srcs are copy-safe but metadata image refs still warn', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const findUnsafeCopyImageRefs = new Function(`
    ${extractFunction(engine, 'isUnsafeCopyImageRef')}
    ${extractFunction(engine, 'findUnsafeCopyImageRefs')}
    return findUnsafeCopyImageRefs;
  `)();

  const refs = findUnsafeCopyImageRefs(`
    <p><img src="data:image/png;base64,docx-image"></p>
    <p><img src="blob:docx-local-image"></p>
    <section imgurl="data:image/jpeg;base64,preview-leak"></section>
    <p><img src="/api/doc-cache/article/image1.png"></p>
    <p><img src="https://cdn.example.com/article/image2.png"></p>
  `);

  assert.deepEqual(refs, [
    'blob:docx-local-image',
    'data:image/jpeg;base64,preview-leak',
    '/api/doc-cache/article/image1.png',
  ]);
});

test('pure text copy bypasses article HTML and footer pipeline', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyFn = engine.slice(engine.indexOf('window.copyContent = async function()'));
  const plainIndex = copyFn.indexOf('copyPlainTextToClipboard(text)');
  const articleIndex = copyFn.indexOf("prepareMoreArticlesForCopy('copy-click')");

  assert.match(copyFn, /if \(_plainTextCopy !== null\)/);
  assert.match(copyFn, /const text = getPlainTextCopyValue\(\)/);
  assert.ok(plainIndex > -1, '纯文字复制应写入纯文本');
  assert.ok(articleIndex > -1, '公众号文章复制仍应保留更多文章预热入口');
  assert.ok(
    plainIndex < articleIndex,
    '纯文字复制必须先返回，避免触发公众号 HTML、页脚和更多文章复制链路'
  );
});

test('新文章导入原子作废上一篇的更多文章预热 job（防跨文章卡片污染）', async () => {
  // 红线#9：更多文章卡片是单篇 curation。清卡片(makeFreshUploadCards)与作废预热 job 必须原子同处，
  // 否则两步之间的窗口（loadBaseFooterHtml/buildFooterWithMoreArticles 两个 await + 200ms setTimeout）
  // 里，上一篇在途 job 会 resolve 并把旧卡片写回存储，下一篇 loadMoreArticlesCards 读到旧卡片
  // （codex re-review #2/#3）。
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  // 原子复位 helper：三要素同处。
  const helper = engine.slice(engine.indexOf('function resetMoreArticlesForNewArticle'));
  const helperBody = helper.slice(0, helper.indexOf('\n  function '));
  assert.match(helperBody, /saveMoreArticlesCards\(makeFreshUploadCards\(\)\)/, 'helper 须重置卡片');
  // S2：seq++ 裸计数器收敛为版本门，作废语义等价（moreArticlesGate.invalidate）。
  assert.match(helperBody, /moreArticlesGate\.invalidate\(\)/, 'helper 须作废在途 job（版本门）');
  assert.match(helperBody, /_moreArticlesCopyJob = null/, 'helper 须 job=null 让本篇重建');
  // 三处新稿路径都通过 session 构建传 reset 标记，不得再裸调 saveMoreArticlesCards+refreshSidebar
  // （漏作废 → 暴露窗口）。
  assert.doesNotMatch(
    engine,
    /saveMoreArticlesCards\(makeFreshUploadCards\(\)\);\n\s*if \(window\._refreshMoreArticlesSidebar\)/,
    '新稿点不得裸调 saveMoreArticlesCards+refreshSidebar，须走原子 helper'
  );
  const buildSession = engine.slice(engine.indexOf('async function buildArticleSession'), engine.indexOf('function activateArticleSession'));
  assert.match(
    buildSession,
    /if \(options\.resetArticleArtifacts\) resetMoreArticlesForNewArticle\(\)/,
    'session 构建应负责原子 reset'
  );
  assert.ok((engine.match(/resetArticleArtifacts: true/g) || []).length >= 3, '单篇/批量/blocks 三处新稿入口都要传 reset 标记');
  // 双保险：在途 job resolve 的两个 await 边界都校验版本门，过期则自我丢弃，不写回 _footerCopy。
  assert.ok((engine.match(/if \(gate\.stale\(\)\) return false/g) || []).length >= 2, '≥2 处版本门校验（原 742/747 seq 校验）');
  // showPreview 仍保留作废，供 rerender（同篇换配置）丢弃旧预热并重建。
  const showPreview = engine.slice(engine.indexOf('function showPreview('));
  const resetZone = showPreview.slice(0, showPreview.indexOf('// Split layout'));
  assert.match(resetZone, /_moreArticlesCopyJob = null/, 'showPreview 保留作废供 rerender');
});

test('website copy blocks when WeChat image CDN URLs still remain', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyFn = engine.slice(engine.indexOf('window.copyContent = async function()'));
  assert.match(engine, /findWechatImageSrcs/);
  assert.match(engine, /_copyRequiresWechatImageTransfer/);
  assert.match(copyFn, /仍有微信图床图片未转存，已阻止复制/);
  assert.doesNotMatch(copyFn, /本次复制使用当前可粘贴内容/);
});

test('copy keeps the final WeChat cursor paragraph zero-height', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyFn = engine.slice(engine.indexOf('window.copyContent = async function()'));
  assert.ok(copyFn.includes('html += \'<p style="margin:0;padding:0;line-height:0;font-size:0;min-height:0;height:0;"><br/></p>\';'));
});

test('footer original and repost notices come from behavior rules', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  assert.match(engine, /getActiveRuleBehavior/);
  assert.match(engine, /footer_original_notice/);
  assert.match(engine, /footer_repost_notice/);
  assert.match(engine, /renderBehaviorTemplate/);
  assert.doesNotMatch(engine, /const after = '\* 本文为转载，不代表FBIF立场。'/);
  assert.match(engine, /\* 本文为转载文章。/);
});

test('deploy writes rule admin password to production env', async () => {
  const workflow = await readFile(new URL('../.github/workflows/deploy.yml', import.meta.url), 'utf8');
  assert.match(workflow, /ADMIN_PASSWORD:\s*\$\{\{ secrets\.ADMIN_PASSWORD \}\}/);
  assert.match(workflow, /ADMIN_PASSWORD GitHub secret is required/);
  assert.match(workflow, /ADMIN_PASSWORD=%s\\n/);
});
