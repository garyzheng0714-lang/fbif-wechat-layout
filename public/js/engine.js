// Shared WeChat formatting engine — UI coordination layer
// Delegates: parsing → parser.js, upload → uploader.js, copy → clipboard.js, CSS → css-inline.js
const assetQuery = new URL(import.meta.url).search;

// Load parser.js through assetQuery so the browser cache can't serve a stale
// copy across deploys. Static `import './parser.js'` would resolve to
// `/js/parser.js` (no version) and, under some proxy/cache conditions, keep
// returning an old revision even with `Cache-Control: no-cache`. Engine.js is
// the only consumer of parser.js; everyone else reaches it through engine's
// re-exports below, so one dynamic import here fixes the whole chain.
const parserModule = await import('./parser.js' + assetQuery);
const {
  esc, escAttr, parseMdRuns, parseMdFrontmatter, parseDocx, extractParagraph,
  W, R, WP, A, findAll, findOne, findDeep, wattr, rattr
} = parserModule;
export {
  esc, escAttr, parseMdRuns, parseMdFrontmatter, parseDocx, extractParagraph,
  W, R, WP, A, findAll, findOne, findDeep, wattr, rattr
};

// Re-export convertRuns so templates can apply punctuation rules to runs
// that arrive pre-formed (e.g. the new URL→blocks path, where runs come
// from the Go backend and bypass parseMdRuns).
const punctuationModule = await import('./punctuation.js' + assetQuery);
export const { convertRuns } = punctuationModule;

import { copyByClipboardApi, copyByClipboardEvent } from './clipboard.js';
import { loadThemeCSS, processForCopy, applyThemeVars } from './css-inline.js';
import { normalizeWechatRichHtml } from './wechat-normalize.js';
import {
  loadCards as loadMoreArticlesCards,
  saveCards as saveMoreArticlesCards,
  mergeIntoFooter as mergeMoreArticlesIntoFooter,
  compositePreviewDataUrl,
  ensureCompositeReady,
  makeFreshUploadCards,
} from './more-articles.js';

const imageUtilsModule = await import('./image-utils.mjs' + assetQuery);
const { inferWechatImageType, looksLikeGifSource } = imageUtilsModule;
export { inferWechatImageType, looksLikeGifSource };

const uploaderModule = await import('./uploader.js' + assetQuery);
const { uploadNonCdnImages, retryFailedImages, materializeLazyImages, isMmbizUrl } = uploaderModule;
const rulePresetModule = await import('./rule-presets.js' + assetQuery);
const { getActiveRuleBehavior } = rulePresetModule;
const behaviorModule = await import('./rule-behavior.js' + assetQuery);
const { renderBehaviorTemplate } = behaviorModule;

// Pre-load theme CSS
const _themeCSSReady = loadThemeCSS();

// ---- Legacy .doc (OLE2) → .docx bridge ----
// Sniff first 8 bytes for the OLE2 Compound Document magic. Detect by bytes,
// not filename: some files have `.doc` extension, others ship legacy binary
// content inside a `.docx`-named wrapper.
async function sniffIsOle2(file) {
  try {
    const buf = new Uint8Array(await file.slice(0, 8).arrayBuffer());
    return buf.length >= 8 &&
      buf[0] === 0xD0 && buf[1] === 0xCF && buf[2] === 0x11 && buf[3] === 0xE0 &&
      buf[4] === 0xA1 && buf[5] === 0xB1 && buf[6] === 0x1A && buf[7] === 0xE1;
  } catch {
    return false;
  }
}

// Use XHR (not fetch) so we get upload+download byte-level progress events.
// Budget: 0-45% upload, 45-50% server processing, 50-95% download.
function convertDocToDocxFile(file, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.responseType = 'blob';
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) {
        const pct = Math.round((e.loaded / e.total) * 45);
        onProgress(pct, `上传中 ${pct}%`);
      }
    };
    xhr.upload.onload = () => { if (onProgress) onProgress(47, '服务器转换中...'); };
    xhr.onprogress = (e) => {
      if (e.lengthComputable && onProgress) {
        const pct = 50 + Math.round((e.loaded / e.total) * 45);
        onProgress(pct, `下载中 ${pct}%`);
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        const newName = file.name.replace(/\.docx?$/i, '') + '.docx';
        resolve(new File([xhr.response], newName, {
          type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
        }));
      } else {
        let msg = '';
        try { msg = JSON.parse(xhr.responseText || '').error || ''; } catch {}
        reject(new Error('DOC 转换失败: ' + (msg || xhr.status)));
      }
    };
    xhr.onerror = () => reject(new Error('DOC 转换失败: 网络错误'));
    xhr.open('POST', '/api/doc-to-docx');
    const fd = new FormData();
    fd.append('file', file);
    xhr.send(fd);
  });
}

async function normalizeToDocx(file, onProgress) {
  if (await sniffIsOle2(file)) {
    if (onProgress) onProgress(0, '正在转换 DOC → DOCX...');
    return await convertDocToDocxFile(file, onProgress);
  }
  // A `.doc`-named file that isn't OLE2 is an unsupported legacy variant
  // (RTF / HTML / WordPerfect saved with the .doc extension). Reject it
  // here rather than letting it fall through to processMd, which would
  // feed binary bytes to the Markdown parser and produce garbage output.
  if (/\.doc$/i.test(file.name)) {
    throw new Error('不支持的 .doc 格式（非 Word OLE2 容器）。请在 Word 里"另存为 .docx"后重试。');
  }
  return file;
}

// Defer <img> display until the full bytes land. By default browsers paint
// PNG/JPEG top-down as bytes arrive, which looks like "half an image" for
// 1–2 s on any noticeable round-trip — especially for server-cached .doc
// images pulled via /api/doc-cache/. We add `wx-img-loading` before the
// image has finished loading, and drop the class on `load` so CSS can
// fade it in atomically.
function hookPreviewImages(container) {
  if (!container) return;
  const imgs = container.querySelectorAll('img');
  for (const img of imgs) {
    if (img.complete && img.naturalWidth > 0) continue;
    img.classList.add('wx-img-loading');
    const clear = () => img.classList.remove('wx-img-loading');
    img.addEventListener('load', clear, { once: true });
    img.addEventListener('error', clear, { once: true });
  }
}

// ---- Footer ----
// footerUrl 语义：字段未声明（undefined）→ 用默认公众号页脚 /footer.html；
// 显式空串 '' → 该渠道无公众号页脚（网站渠道 ft-default 即如此，footer 内容
// 由 FoodTalks 系统在发布时自动追加，工具侧不得再拼公众号那套）。此前用 `||`
// 会把显式空串也 fallback 到 /footer.html，导致网站渠道误带公众号页脚。
const _stockFooterCache = new Map();
function currentStockFooterUrl() {
  const activeRulePack = typeof window !== 'undefined' ? window._activeRulePack : null;
  return activeRulePack && activeRulePack.footerUrl != null ? activeRulePack.footerUrl : '/footer.html';
}
function stockFooterReady() {
  const url = currentStockFooterUrl();
  if (!url) return Promise.resolve('');
  if (!_stockFooterCache.has(url)) {
    _stockFooterCache.set(url, fetch(url).then(r => r.ok ? r.text() : '').catch(() => ''));
  }
  return _stockFooterCache.get(url);
}
// 当前渠道模板（initApp/switchTemplate 设置）。footer 链路据此调用模板的 renderBottomFooter()——
// engine 渠道共用、不可 import 公众号渲染器，故通过模板回调接管底部渲染（红线 #19）。
let _activeTemplate = null;

// 网站渲染器（foodtalks-site）输出已是完整 inline-style 的干净 HTML：正文图自带
// display:block + margin:0 auto 居中、内联 logo 故意左对齐。公众号导向的
// normalizeWechatRichHtml 会给所有 <img> 强制套 margin:0 auto，把网站页脚的内联 logo
// 误居中（预览与复制路径都中招）。网站渠道整体跳过归一化（红线#19/#20：与公众号物理
// 隔离、页脚字节保真），其余渠道照常归一化。
function normalizeRich(html) {
  if (_activeTemplate && _activeTemplate.id === 'foodtalks-site') return html;
  return normalizeWechatRichHtml(html);
}

// loadBaseFooterHtml 返回 { html, isV2 }：isV2=true 表示 v2 双预设底部（已按 presetId 分原创/转载）。
// 调用方把 isV2 作为局部值传给 buildFooterWithMoreArticles，避免模块级单例在 await 间被并发重入覆盖
// （codex review #4 竞态修复——多条 load→build 链交错时单例会互相覆盖 isV2）。
async function loadBaseFooterHtml(opts = {}) {
  const custom = localStorage.getItem('custom_footer');
  if (custom) return { html: custom, isV2: false };
  // 公众号 bottom_blocks 启用时，整段底部由区块渲染（替代 footer.html）。
  // html 为 null/'' → 模块未接管，fallback 到 footerUrl(footer.html)。
  // isRepost 显式传入（重渲染用 cached.isRepost，避免读全局 _isRepost 漂移；codex review #5）。
  const isRepost = opts.isRepost != null ? opts.isRepost : _isRepost;
  if (_activeTemplate && typeof _activeTemplate.renderBottomFooter === 'function') {
    const res = _activeTemplate.renderBottomFooter({ isRepost });
    const html = res && typeof res === 'object' ? res.html : res; // 模板返回 {html,isV2} 或（自定义）字符串
    if (html != null && html !== '') {
      return { html, isV2: !!(res && typeof res === 'object' && res.isV2) };
    }
  }
  return { html: await stockFooterReady(), isV2: false };
}

// Module-level flag: true when the current article was fetched from a URL
// (i.e. it is a repost). Set explicitly by handleFile/processFile at each
// entry so flags never leak across articles. Read by applyRepostTransform
// at every footer-rebuild site.
let _isRepost = false;

// 渲染调度层活跃配置 —— site.html / app.html 启动时通过 setActiveConfig 注入
let _activeConfig = {};
export function setActiveConfig(cfg) {
  _activeConfig = cfg || {};
}
export function getActiveConfig() {
  return _activeConfig;
}

function footerBehaviorContext() {
  const pack = typeof window !== 'undefined' ? window._activeRulePack : null;
  return {
    article: {},
    account: {
      name: (pack && pack.behaviorAccountName) || 'FBIF',
      id: (pack && pack.id) || 'fbif',
    },
  };
}

function footerBehaviorText(slot, fallback) {
  const behavior = getActiveRuleBehavior();
  const text = renderBehaviorTemplate(behavior.templates && behavior.templates[slot], footerBehaviorContext());
  return text || fallback;
}

// 读 behavior.params.<key>.default（红线下放：规则后台可配；读不到降级 fallback）。
function footerBehaviorParam(key, fallback) {
  try {
    const params = getActiveRuleBehavior().params || {};
    const v = params[key] && params[key].default;
    if (typeof v === 'string' && v) return v;
  } catch (e) { /* 降级 */ }
  return fallback;
}

// Rewrites the stock footer notice from the active behavior rule. URL imports
// still remove the second stock bullet because it only applies to originals.
// Anchors: four <!--REPOST_BULLET{1,2}_{START,END}--> HTML comments embedded
// in footer.html. If the user customized the footer and removed the sentinels
// or the bullet text, transform no-ops (logged) — custom footers are the
// user's responsibility.
function applyRepostTransform(html, isRepost = _isRepost) {
  if (typeof html !== 'string' || !html) return html;
  let out = html;
  let changed = false;
  // 机构名走 account.name（红线下放：fallback 不再写死 FBIF；模板缺省时按账号名动态构造）。
  const acctName = footerBehaviorContext().account.name || 'FBIF';
  const before = footerBehaviorText('footer_original_notice', '* 本文为' + acctName + '原创，欢迎转发朋友圈；');
  const after = footerBehaviorText('footer_repost_notice', '* 本文为转载，不代表' + acctName + '立场。');
  // footer.html 内嵌存量原创声明（红线下放 behavior.params.footer_stock_original_text，
  // 默认即 footer.html 中的 FBIF 文案；按此字面量在静态页脚里定位替换）。
  const stockOriginal = footerBehaviorParam('footer_stock_original_text', '* 本文为FBIF原创，欢迎转发朋友圈；');
  const nextNotice = isRepost ? after : before;
  if (out.includes(stockOriginal)) {
    out = out.replace(stockOriginal, esc(nextNotice));
    changed = true;
  }
  if (!isRepost) return out;
  if (out.includes(before)) {
    out = out.replace(before, esc(after));
    changed = true;
  }
  const s = '<!--REPOST_BULLET2_START-->';
  const e = '<!--REPOST_BULLET2_END-->';
  const i = out.indexOf(s);
  const j = out.indexOf(e);
  if (i >= 0 && j > i) {
    out = out.slice(0, i) + out.slice(j + e.length);
    changed = true;
  }
  if (!changed) {
    console.warn('[engine] repost transform no-op: footer sentinels/anchors not found (custom footer?)');
  }
  return out;
}

// Build a footer HTML that has the "更多文章" section replaced with the user's
// current card config. Each customized card is composited to a data URL for
// instant preview (no upload). Copy-time will re-run with uploaded OSS URLs.
async function buildFooterWithMoreArticles(baseFooterHtml, opts = {}) {
  const isRepost = opts.isRepost != null ? opts.isRepost : _isRepost;
  // v2 双预设已按 presetId 分原创/转载，跳过旧锚点替换（codex review #4，互斥）。isV2 由调用方局部传入。
  const base = opts.isV2 ? baseFooterHtml : applyRepostTransform(baseFooterHtml, isRepost);
  const transformed = normalizeWechatRichHtml(base);
  const cards = loadMoreArticlesCards();
  const cardsForPreview = await Promise.all(
    cards.map(async c => ({ ...c, final_url: await compositePreviewDataUrl(c) }))
  );
  return normalizeWechatRichHtml(mergeMoreArticlesIntoFooter(transformed, cardsForPreview));
}

// ---- App Initialization ----
export function initApp(initialTemplate) {
  let template = initialTemplate;
  _activeTemplate = initialTemplate;
  function applyTemplateShell(nextTemplate) {
    document.getElementById('pageTitle').textContent = nextTemplate.name;
    document.getElementById('pageSubtitle').textContent = nextTemplate.description;
    document.getElementById('fileHint').textContent = '支持 Word、Markdown、TXT 格式';
    fileInputEl.accept = nextTemplate.formats.join(',');
  }
  const fileInputEl = document.getElementById('fileInput');
  applyTemplateShell(template);
  fileInputEl.multiple = true;

  const dropZone = document.getElementById('dropZone');
  const fileInput = fileInputEl;
  const progress = document.getElementById('progress');
  const progressFill = document.getElementById('progressFill');
  const progressText = document.getElementById('progressText');
  const errorEl = document.getElementById('error');
  const batchList = document.getElementById('batchList');
  const importZone = document.getElementById('importZone');
  const docZone = document.getElementById('docZone');
  const docName = document.getElementById('docName');
  const docStat = document.getElementById('docStat');
  const docSwap = document.getElementById('docSwap');

  function showError(msg) { errorEl.textContent = msg; errorEl.style.display = 'block'; }

  function showImportEntry() {
    if (importZone) importZone.hidden = false;
    if (docZone) docZone.hidden = true;
  }

  function resetImportEntry() {
    showImportEntry();
    dropZone.classList.remove('processing');
    progress.style.display = 'none';
    progressFill.style.width = '0%';
    progressText.textContent = '正在排版...';
    fileInput.value = '';
  }

  function formatImportStat(stats) {
    const text = String(stats || '');
    const para = text.match(/段落:\s*(\d+)/);
    const image = text.match(/图片:\s*(\d+)/);
    if (para || image) {
      return [
        para ? para[1] + ' 段' : '',
        image ? image[1] + ' 张图片' : '',
      ].filter(Boolean).join(' · ');
    }
    return text || '已完成提取';
  }

  function formatSidebarStatus(status) {
    const text = String(status || '').trim();
    return /^段落\s*(?:[:：]|\d)/.test(text) ? '' : text;
  }

  function hasDocumentSummaryCard() {
    return !!document.getElementById('docZone');
  }

  function settledSidebarStatus(stats) {
    return hasDocumentSummaryCard() ? formatSidebarStatus(stats) : (stats || '');
  }

  function uploadStatus(stats, suffix) {
    if (hasDocumentSummaryCard()) return suffix;
    return [stats || '', suffix || ''].filter(Boolean).join(' | ');
  }

  function showImportSummary(title, stats) {
    if (!docZone) return;
    if (docName) docName.textContent = title || '未命名文档';
    if (docStat) docStat.textContent = formatImportStat(stats);
    if (importZone) importZone.hidden = true;
    docZone.hidden = false;
    dropZone.classList.remove('processing');
    progress.style.display = 'none';
  }

  function clonePlain(value) {
    if (value == null) return value;
    try { return JSON.parse(JSON.stringify(value)); } catch { return null; }
  }

  function dispatchArticleEvent(name, detail = {}) {
    window.dispatchEvent(new CustomEvent(name, { detail }));
  }

  function isValidFile(f) {
    return template.formats.some(fmt => f.name.toLowerCase().endsWith(fmt));
  }

  // Cache the latest parsed inputs so rerender() can re-run template.process*
  // with the new config without re-parsing the source file.
  let _lastRenderInput = null;
  // shape: { kind: 'docx'|'md'|'blocks', payload: <data>, isRepost, title? }

  // Batch file queue
  let _batchFiles = [];
  let _batchResults = [];

  function showBatchList(files) {
    _batchFiles = files;
    _batchResults = files.map(() => null);
    if (!batchList) return;
    if (files.length <= 1) { batchList.style.display = 'none'; return; }

    batchList.style.display = 'block';
    batchList.innerHTML = '<div class="batch-title">文件队列 (' + files.length + ')</div>';
    files.forEach((f, i) => {
      const item = document.createElement('div');
      item.className = 'batch-item';
      item.id = 'batch-' + i;
      item.innerHTML = '<span class="batch-name">' + f.name + '</span>' +
        '<span class="batch-status" id="batch-status-' + i + '">等待中</span>';
      item.addEventListener('click', () => {
        if (_batchResults[i]) {
          const r = _batchResults[i];
          showPreview(r.title || f.name.replace(/\.\w+$/i, ''), r.articleHtml, r.footerHtml, r.stats);
        }
      });
      batchList.appendChild(item);
    });
  }

  function updateBatchStatus(idx, status, cls) {
    const el = document.getElementById('batch-status-' + idx);
    if (el) { el.textContent = status; el.className = 'batch-status ' + (cls || ''); }
  }

  async function handleFiles(files, opts) {
    const valid = Array.from(files).filter(isValidFile);
    if (valid.length === 0) {
      showImportEntry();
      showError('请上传 ' + template.formats.join(' 或 ') + ' 文件');
      return;
    }
    if (valid.length === 1) {
      handleFile(valid[0], opts);
      return;
    }
    // Batch mode
    showBatchList(valid);
    errorEl.style.display = 'none';
    for (let i = 0; i < valid.length; i++) {
      updateBatchStatus(i, '处理中...', 'processing');
      try {
        const result = await processFile(valid[i], opts);
        _batchResults[i] = result;
        updateBatchStatus(i, '✓ 完成', 'done');
      } catch (err) {
        updateBatchStatus(i, '✗ 失败', 'failed');
      }
    }
    if (_batchResults[0]) {
      const r = _batchResults[0];
      showPreview(r.title || valid[0].name.replace(/\.\w+$/i, ''), r.articleHtml, r.footerHtml, r.stats);
    }
  }

  async function processFile(file, opts) {
    // Repost flag 局部捕获：footer 选预设全程用此局部，绝不在 await 之后回读模块级 _isRepost
    // （并发导入交错会污染全局；codex re-review）。全局仍同步设置，供快照/renderBottomFooter 兜底。
    const isRepost = !!(opts && opts.isRepost);
    _isRepost = isRepost;
    const t0 = performance.now();
    file = await normalizeToDocx(file);
    let result;
    const isDocx = file.name.toLowerCase().endsWith('.docx');
    _sourceIsDocx = isDocx;
    const fallbackTitle = file.name.replace(/\.\w+$/i, '');
    if (isDocx) {
      const docxData = await parseDocx(file);
      result = template.processDocx({ ...docxData, isRepost, config: _activeConfig });
      _lastRenderInput = { kind: 'docx', payload: docxData, isRepost, title: result.title || fallbackTitle };
    } else if (typeof template.processMd === 'function') {
      const text = await file.text();
      result = await template.processMd(text, { isRepost, config: _activeConfig });
      _lastRenderInput = { kind: 'md', payload: text, isRepost, title: result.title || fallbackTitle };
    } else {
      throw new Error('此模板不支持该文件格式');
    }
    const articleHtml = result.lines.join('\n');
    // New article upload resets the "更多文章" section to blank placeholder
    // slots — cards are a per-article curation, filled in manually each time.
    resetMoreArticlesForNewArticle();
    const { html: baseFooterHtml, isV2 } = await loadBaseFooterHtml({ isRepost });
    const footerHtml = await buildFooterWithMoreArticles(baseFooterHtml, { isRepost, isV2 });
    const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
    const stats = '段落: ' + result.lines.length + ' | 图片: ' + result.imgN +
      ' | 标题: ' + result.headingN + ' | 耗时: ' + elapsed + 's';
    return { articleHtml, footerHtml, stats, title: result.title };
  }

  dropZone.addEventListener('click', () => fileInput.click());
  dropZone.addEventListener('keydown', e => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    fileInput.click();
  });
  dropZone.addEventListener('dragover', e => { e.preventDefault(); dropZone.classList.add('dragover'); });
  dropZone.addEventListener('dragleave', () => dropZone.classList.remove('dragover'));
  dropZone.addEventListener('drop', e => {
    e.preventDefault(); dropZone.classList.remove('dragover');
    handleFiles(e.dataTransfer.files, { isRepost: false });
  });
  fileInput.addEventListener('change', () => { if (fileInput.files.length) handleFiles(fileInput.files, { isRepost: false }); });
  if (docSwap) {
    docSwap.addEventListener('click', () => {
      resetImportEntry();
      fileInput.click();
    });
  }

  // Exposed so app.html fetchArticle() can feed the synthetic File produced
  // from a URL fetch through the same pipeline as a local upload, but with
  // isRepost=true so the footer swaps to the 转载 variant.
  window._handleFile = (file, opts) => handleFile(file, opts);

  // URL-import structured-blocks entry point. Used when the server responds
  // to /api/fetch-article with {title, blocks} (preferred path — preserves
  // visual signals). Falls back to _handleFile with a synthetic .md File
  // when the server returns {title, content} (x-reader fallback).
  window._handleBlocks = async (data, opts) => {
    // Repost flag 局部捕获（同 processFile/handleFile）：footer 链路用局部，不回读全局。
    const isRepost = !!(opts && opts.isRepost);
    _isRepost = isRepost;
    _convLog.length = 0;
    logConv('info', '开始转化', { source: 'blocks', blocks: (data.blocks || []).length });
    errorEl.style.display = 'none';
    showImportEntry();
    dropZone.classList.add('processing');
    progress.style.display = 'block';
    progressFill.style.width = '70%';
    progressText.textContent = '正在排版...';
    try {
      const t0 = performance.now();
      if (typeof template.processBlocks !== 'function') {
        throw new Error('当前模板不支持 blocks 路径');
      }
      _sourceIsDocx = false;
      // await: processBlocks is synchronous today but we're inside an
      // async context anyway, and future work (e.g. image pre-fetch)
      // might make it async. The await has zero cost on plain returns.
      const result = await template.processBlocks(data, { isRepost, config: _activeConfig });
      _lastRenderInput = { kind: 'blocks', payload: data, isRepost, title: data.title };
      logConv('info', '结构化解析完成', { paragraphs: result.lines.length, images: result.imgN });

      const articleHtml = result.lines.join('\n');
      resetMoreArticlesForNewArticle();
      const { html: baseFooterHtml, isV2 } = await loadBaseFooterHtml({ isRepost });
      const footerHtml = await buildFooterWithMoreArticles(baseFooterHtml, { isRepost, isV2 });
      const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
      const stats = '段落: ' + result.lines.length + ' | 图片: ' + result.imgN +
        ' | 标题: ' + result.headingN + ' | 耗时: ' + elapsed + 's';
      logConv('info', '排版完成', { elapsed: elapsed + 's', headings: result.headingN });

      progressFill.style.width = '100%';
      progressText.textContent = '排版完成!';
      const displayTitle = result.title || data.title || '转载文章';
      dropZone.classList.remove('processing');
      setTimeout(() => showPreview(displayTitle, articleHtml, footerHtml, stats), 200);
    } catch (err) {
      logConv('error', '排版失败', { error: err.message });
      dropZone.classList.remove('processing');
      progress.style.display = 'none';
      showError('排版失败: ' + err.message);
    }
  };

  // Re-render the current article with the latest _activeConfig without
  // re-parsing the source file. Safe to call before any file is loaded
  // (returns silently when _lastRenderInput is null).
  async function _engineRerender() {
    if (!_lastRenderInput) return;
    const cached = _lastRenderInput;
    // Use a local repost flag so we don't pollute the module-level _isRepost,
    // which handleFile / _handleBlocks set per upload and other code paths read.
    const isRepost = cached.isRepost;
    try {
      const t0 = performance.now();
      let result;
      if (cached.kind === 'docx') {
        result = template.processDocx({ ...cached.payload, isRepost, config: _activeConfig });
      } else if (cached.kind === 'md') {
        if (typeof template.processMd !== 'function') return;
        result = await template.processMd(cached.payload, { isRepost, config: _activeConfig });
      } else if (cached.kind === 'blocks') {
        if (typeof template.processBlocks !== 'function') return;
        result = await template.processBlocks(cached.payload, { isRepost, config: _activeConfig });
      } else {
        return;
      }
      const articleHtml = result.lines.join('\n');
      const { html: baseFooterHtml, isV2 } = await loadBaseFooterHtml({ isRepost });
      const footerHtml = await buildFooterWithMoreArticles(baseFooterHtml, { isRepost, isV2 });
      const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
      const stats = '段落: ' + result.lines.length + ' | 图片: ' + result.imgN +
        ' | 标题: ' + result.headingN + ' | 耗时: ' + elapsed + 's';
      const displayTitle = result.title || cached.title || '';
      showPreview(displayTitle, articleHtml, footerHtml, stats);
    } catch (err) {
      console.error('[engine] rerender failed:', err);
    }
  }

  // Make rerender callable from inline scripts (site.html / app.html sidebar)
  window._engineRerender = _engineRerender;
  window._engineSwitchTemplate = async function(nextTemplate, options = {}) {
    if (!nextTemplate) return false;
    template = nextTemplate;
    _activeTemplate = nextTemplate;
    applyTemplateShell(template);
    if (options.rerender !== false) await _engineRerender();
    return true;
  };
  window._engineApi = {
    ...window._engineApi,
    setActiveConfig,
    getActiveConfig,
    rerender: () => _engineRerender(),
    switchTemplate: window._engineSwitchTemplate,
  };

  async function handleFile(file, opts) {
    // Repost flag 局部捕获：footer 选预设全程用此局部，绝不在 await 之后回读模块级 _isRepost
    // （并发导入交错会污染全局；codex re-review）。全局仍同步设置，供快照/renderBottomFooter 兜底。
    const isRepost = !!(opts && opts.isRepost);
    _isRepost = isRepost;
    _convLog.length = 0;
    logConv('info', '开始转化', { file: file.name, size: (file.size / 1024).toFixed(1) + 'KB' });
    errorEl.style.display = 'none';
    showImportEntry();
    dropZone.classList.add('processing');
    progress.style.display = 'block';
    progressFill.style.width = '50%';
    progressText.textContent = '正在解析...';
    try {
      const t0 = performance.now();

      file = await normalizeToDocx(file, (pct, msg) => {
        progressText.textContent = msg;
        progressFill.style.width = pct + '%';
      });
      progressFill.style.width = '97%';
      progressText.textContent = '正在解析...';

      let result;
      const isDocx = file.name.toLowerCase().endsWith('.docx');
      _sourceIsDocx = isDocx;
      const fallbackTitle = file.name.replace(/\.\w+$/i, '');
      if (isDocx) {
        const docxData = await parseDocx(file);
        result = template.processDocx({ ...docxData, isRepost, config: _activeConfig });
        _lastRenderInput = { kind: 'docx', payload: docxData, isRepost, title: result.title || fallbackTitle };
        logConv('info', 'DOCX解析完成', { paragraphs: result.lines.length, images: result.imgN });
      } else if (typeof template.processMd === 'function') {
        const text = await file.text();
        result = await template.processMd(text, { isRepost, config: _activeConfig });
        _lastRenderInput = { kind: 'md', payload: text, isRepost, title: result.title || fallbackTitle };
        logConv('info', 'Markdown解析完成', { paragraphs: result.lines.length, images: result.imgN });
      } else {
        throw new Error('此模板不支持该文件格式');
      }

      const articleHtml = result.lines.join('\n');
      // Single-file upload path — same reset behavior as batch path: wipe
      // any prior article's "更多文章" cards to blank placeholder slots, so
      // sidebar shows empty inputs and footer renders gray placeholders.
      resetMoreArticlesForNewArticle();
      const { html: baseFooterHtml, isV2 } = await loadBaseFooterHtml({ isRepost });
      const footerHtml = await buildFooterWithMoreArticles(baseFooterHtml, { isRepost, isV2 });
      const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
      const stats = '段落: ' + result.lines.length + ' | 图片: ' + result.imgN +
        ' | 标题: ' + result.headingN + ' | 耗时: ' + elapsed + 's';
      logConv('info', '排版完成', { elapsed: elapsed + 's', headings: result.headingN });

      progressFill.style.width = '100%';
      progressText.textContent = '排版完成!';
      const displayTitle = result.title || file.name.replace(/\.\w+$/i, '');
      // Drop the processing state so the upload zone is clickable again for
      // the next file. (Previously only the error branch cleared it, so a
      // successful upload left pointer-events:none on the drop zone.)
      dropZone.classList.remove('processing');
      setTimeout(() => showPreview(displayTitle, articleHtml, footerHtml, stats), 200);
    } catch (err) {
      logConv('error', '排版失败', { error: err.message });
      dropZone.classList.remove('processing');
      progress.style.display = 'none';
      showError('排版失败: ' + err.message);
    }
  }

  // Dual-layer rendering:
  //   Display layer (_articleHtml) — CSS classes, rendered by browser via <link> to wx-theme.css
  //   Copy layer (_articleCopy)   — classes inlined to style="" via css-inline.js, images on CDN
  let _articleHtml = '';
  let _articleCopy = '';
  let _footerHtml = '';
  let _footerCopy = '';
  let _uploadPromise = null;
  let _uploadDone = false;
  let _failedSrcs = [];
  let _sourceIsDocx = false;
  let _copyRequiresWechatImageTransfer = false;
  let _moreArticlesCopySeq = 0;
  let _moreArticlesCopyJob = null;

  function findWechatImageSrcs(html) {
    const urls = [];
    const re = /<img\b[^>]*\bsrc=(["'])(https?:\/\/[^"']+)\1/gi;
    let m;
    while ((m = re.exec(String(html || ''))) !== null) {
      if (isMmbizUrl(m[2])) urls.push(m[2]);
    }
    return urls;
  }

  function markFailedImages(failedSrcs) {
    if (!failedSrcs.length) return;
    const imgs = document.getElementById('contentArea').querySelectorAll('img');
    imgs.forEach(img => {
      const src = img.getAttribute('src') || '';
      if (failedSrcs.some(fs => src.includes(fs.substring(0, 60)))) {
        img.style.outline = '3px solid #ff4d4f';
        img.style.outlineOffset = '-3px';
        img.title = '上传失败 — 点击"重试上传"';
      } else {
        img.style.outline = '';
        img.title = '';
      }
    });
  }

  function cardsNeedComposite(cards) {
    return cards.some(c => (c.title && c.title.trim()) || c.cover_data_url);
  }

  // 新文章导入：把「更多文章」卡片重置为空白占位 + 作废上一篇在途的预热 job——两步必须原子。
  // 只清卡片不作废 job，旧 job 会在卡片重置之后、showPreview 之前的窗口（两个 await + setTimeout）
  // 里 resolve，此时 seq 未变 → 通过 742 校验 → saveMoreArticlesCards 把上一篇卡片写回存储，
  // 下一篇 loadMoreArticlesCards 读到旧卡片（跨文章污染；codex re-review #3）。
  function resetMoreArticlesForNewArticle() {
    saveMoreArticlesCards(makeFreshUploadCards());
    _moreArticlesCopySeq++;
    _moreArticlesCopyJob = null;
    if (window._refreshMoreArticlesSidebar) window._refreshMoreArticlesSidebar();
  }

  function prepareMoreArticlesForCopy(reason) {
    if (_moreArticlesCopyJob) return _moreArticlesCopyJob;
    const cards = loadMoreArticlesCards();
    if (!cardsNeedComposite(cards)) return Promise.resolve(false);
    const seq = ++_moreArticlesCopySeq;
    _moreArticlesCopyJob = ensureCompositeReady(cards).then(async uploaded => {
      if (seq !== _moreArticlesCopySeq) return false;
      saveMoreArticlesCards(uploaded);
      const rp = _lastRenderInput && _lastRenderInput.isRepost != null ? _lastRenderInput.isRepost : _isRepost;
      const { html: base0, isV2 } = await loadBaseFooterHtml({ isRepost: rp });
      const baseFooter = normalizeWechatRichHtml(isV2 ? base0 : applyRepostTransform(base0, rp));
      if (seq !== _moreArticlesCopySeq) return false;
      _footerCopy = normalizeWechatRichHtml(mergeMoreArticlesIntoFooter(baseFooter, uploaded));
      logConv('info', '更多文章图片已后台准备完成', { reason });
      return true;
    }).catch(err => {
      logConv('warn', '更多文章后台准备失败，继续使用当前预览版', { error: err.message, reason });
      return false;
    }).finally(() => {
      if (seq === _moreArticlesCopySeq) _moreArticlesCopyJob = null;
    });
    return _moreArticlesCopyJob;
  }

  function showPreview(title, articleContent, footerContent, stats) {
    // 渲染新文章 → 复位上一篇的复制状态（按钮变身 + 后台入口），避免显示陈旧的「✓已复制」。
    if (window._resetCopyFeedback) window._resetCopyFeedback();
    _articleHtml = normalizeRich(articleContent);
    _articleCopy = _articleHtml;
    _footerHtml = normalizeRich(footerContent);
    _footerCopy = _footerHtml;
    _failedSrcs = [];
    _copyRequiresWechatImageTransfer = false;
    // 作废旧配置遗留的「更多文章」预热 job（主要供 rerender：同篇换配置时丢弃旧预热并重建）。
    // 新文章导入路径已在更早的 resetMoreArticlesForNewArticle() 原子作废（codex re-review #3）；
    // 此处再作废对新稿无害、对 rerender 必要。seq++ 让在途 job 在 742/747 校验失败自我丢弃。
    _moreArticlesCopySeq++;
    _moreArticlesCopyJob = null;

    // Split layout: show preview content, hide empty state
    const emptyState = document.getElementById('emptyState');
    const previewContent = document.getElementById('previewContent');
    const wxFooter = document.getElementById('wxFooter');
    if (emptyState) emptyState.style.display = 'none';
    if (previewContent) previewContent.style.display = '';
    if (wxFooter) wxFooter.style.display = '';
    document.body.classList.add('has-preview');

    document.getElementById('titleInput').value = title;
    document.getElementById('previewTitleDisplay').textContent = title;
    showImportSummary(title, stats);

    // Apply theme CSS variables to contentArea for preview
    const contentArea = document.getElementById('contentArea');
    if (window._activeConfig) applyThemeVars(contentArea, window._activeConfig.config);
    contentArea.innerHTML = _articleHtml + '\n' + _footerHtml;
    hookPreviewImages(contentArea);
    document.getElementById('statsBar').textContent = settledSidebarStatus(stats);

    const retryBtn = document.getElementById('retryBtn');
    if (retryBtn) retryBtn.style.display = 'none';

    window._updateFooter = function(newHtml) {
      if (typeof newHtml === 'string' && newHtml !== '') {
        _footerHtml = normalizeRich(newHtml);
        _footerCopy = _footerHtml;
      }
      const enabled = document.getElementById('footerEnabled').checked;
      contentArea.innerHTML = _articleHtml + '\n' + (enabled ? _footerHtml : '');
      hookPreviewImages(contentArea);
      markFailedImages(_failedSrcs);
    };

    // Expose the article's image URLs so the sidebar's cover-picker modal
    // can offer them as thumbnails. Footer images are intentionally excluded
    // — we only want images from the uploaded article, not the FBIF footer
    // assets (QR code, avatar, etc).
    window._getArticleImageSrcs = function() {
      const srcs = [];
      const seen = new Set();
      const re = /<img\b[^>]*?\bsrc=["']([^"']+)["']/gi;
      let m;
      while ((m = re.exec(_articleHtml)) !== null) {
        const src = m[1];
        if (!src || seen.has(src)) continue;
        seen.add(src);
        srcs.push(src);
      }
      return srcs;
    };

    // Re-render the "更多文章" section after the user edits card config.
    // Uses fast preview data URLs; real OSS URLs are swapped in at copy time.
    window._updateMoreArticles = async function(cardsOverride) {
      if (cardsOverride) saveMoreArticlesCards(cardsOverride);
      _moreArticlesCopySeq++;
      _moreArticlesCopyJob = null;
      const rp = _lastRenderInput && _lastRenderInput.isRepost != null ? _lastRenderInput.isRepost : _isRepost;
      const { html: base, isV2 } = await loadBaseFooterHtml({ isRepost: rp });
      const merged = await buildFooterWithMoreArticles(base, { isRepost: rp, isV2 });
      _footerHtml = merged;
      _footerCopy = merged;
      const enabled = document.getElementById('footerEnabled').checked;
      contentArea.innerHTML = _articleHtml + '\n' + (enabled ? _footerHtml : '');
      hookPreviewImages(contentArea);
      markFailedImages(_failedSrcs);
      prepareMoreArticlesForCopy('edit');
    };

    dispatchArticleEvent('fbif:article-previewed', { title, stats });

    // Background: upload non-mmbiz images to WeChat CDN for the copy layer
    _uploadDone = false;
    _uploadPromise = null;
    const statsBar = document.getElementById('statsBar');
    const stableStats = settledSidebarStatus(stats);

    const skipUploadForThisFile = !!window._skipUpload && _sourceIsDocx;
    if (skipUploadForThisFile) {
      _uploadPromise = materializeLazyImages(_articleCopy, _footerCopy, {
        onProgress(done, total) {
          if (total > 0) statsBar.textContent = '准备图片 (' + done + '/' + total + ')...';
        },
        onLog: logConv,
      }).then(result => {
        _articleCopy = result.articleCopy;
        _footerCopy = result.footerCopy;
        _failedSrcs = result.failedSrcs || [];
        _uploadDone = true;
        if (result.failed > 0) {
          statsBar.textContent = uploadStatus(stableStats, result.failed + '张图片准备失败');
          markFailedImages(_failedSrcs);
        } else {
          statsBar.textContent = stableStats;
        }
      });
      logConv('info', 'DOCX 跳过上传：仅准备本地图片，不上传');
      prepareMoreArticlesForCopy('preview');
      return;
    }

    // 网站渠道（foodtalks-site）：mmbiz 图必须转存到自有 CDN，否则粘进
    // foodtalks.cn 会触发微信防盗链占位图。公众号渠道保持 mmbiz 直用。
    // 渠道能力由模板声明 transferMmbiz 标志（红线下放：不再比对硬编码 id 字符串；
    // 旧模板未声明时降级到原 id 判断兜底）。
    const transferMmbiz = template.transferMmbiz != null
      ? !!template.transferMmbiz
      : template.id === 'foodtalks-site';
    _copyRequiresWechatImageTransfer = transferMmbiz;
    _uploadPromise = uploadNonCdnImages(_articleCopy, _footerCopy, {
      transferMmbiz,
      onProgress(done, total) {
        statsBar.textContent = (transferMmbiz ? '转存图片 (' : '上传图片 (') + done + '/' + total + ')...';
      },
      onLog: logConv,
      onMaterialized(result) {
        _articleCopy = result.articleCopy;
        _footerCopy = result.footerCopy;
        _failedSrcs = result.failedSrcs || [];
      },
    }).then(result => {
      _articleCopy = result.articleCopy;
      _footerCopy = result.footerCopy;
      _failedSrcs = result.failedSrcs || [];
      _uploadDone = true;
      if (result.failed > 0) {
        statsBar.textContent = uploadStatus(stableStats, result.failed + '张图片上传失败');
        markFailedImages(_failedSrcs);
        if (retryBtn) retryBtn.style.display = 'inline-block';
      } else {
        statsBar.textContent = hasDocumentSummaryCard() ? '' : uploadStatus(stableStats, '全部图片已就绪');
      }
    });
    prepareMoreArticlesForCopy('preview');
  }

  window.retryUpload = async function() {
    if (!_failedSrcs.length) return;
    const retryBtn = document.getElementById('retryBtn');
    const statsBar = document.getElementById('statsBar');
    if (retryBtn) { retryBtn.textContent = '重试中...'; retryBtn.disabled = true; }

    const result = await retryFailedImages(_articleCopy, _footerCopy, _failedSrcs, {
      onProgress(done, total) {
        statsBar.textContent = '重试上传 (' + done + '/' + total + ')...';
      },
      onLog: logConv,
    });
    _articleCopy = result.articleCopy;
    _footerCopy = result.footerCopy;
    _failedSrcs = result.failedSrcs;

    if (result.failed > 0) {
      statsBar.textContent = result.failed + '张图片仍然失败';
      markFailedImages(_failedSrcs);
    } else {
      statsBar.textContent = hasDocumentSummaryCard() ? '' : '全部图片已就绪';
      markFailedImages([]);
      if (retryBtn) retryBtn.style.display = 'none';
    }
    if (retryBtn) { retryBtn.textContent = '重试上传'; retryBtn.disabled = false; }
  };

  // ---- Conversion Log ----
  const _convLog = [];
  function logConv(level, msg, detail) {
    const entry = { time: new Date().toISOString(), level, msg };
    if (detail) entry.detail = detail;
    _convLog.push(entry);
    const prefix = level === 'error' ? '❌' : level === 'warn' ? '⚠️' : '✅';
    console.log('[排版日志] ' + prefix + ' ' + msg, detail || '');
    if (window._pushLog) window._pushLog(level, msg, detail);
  }
  window.getConvLog = function() { return JSON.parse(JSON.stringify(_convLog)); };

  // ---- Copy (with CSS inlining) ----
  let _lastCopiedHtml = '';

  window.copyContent = async function() {
    const content = document.getElementById('contentArea');
    const btn = document.getElementById('copyBtn');

    prepareMoreArticlesForCopy('copy-click');

    const enabled = document.getElementById('footerEnabled').checked;
    let html = _articleCopy + '\n' + (enabled ? _footerCopy : '');
    const pendingWechatSrcs = _copyRequiresWechatImageTransfer ? findWechatImageSrcs(html) : [];
    if (pendingWechatSrcs.length > 0) {
      const waiting = _uploadPromise && !_uploadDone;
      logConv(waiting ? 'warn' : 'error', waiting
        ? '网站图片仍在转存，请稍后再复制'
        : '仍有微信图床图片未转存，已阻止复制', {
        count: pendingWechatSrcs.length,
        first: pendingWechatSrcs[0].slice(0, 80),
      });
      const originalText = btn.textContent || '复制全文';
      btn.textContent = waiting ? '图片准备中' : '图片需重试';
      setTimeout(() => { btn.textContent = originalText; }, 1600);
      return false;
    }

    // Inline CSS classes → style attributes for WeChat compatibility
    const config = (window._activeConfig && window._activeConfig.config) || {};
    const themeCSS = await _themeCSSReady;
    html = processForCopy(html, themeCSS, config);
    html = normalizeRich(html);

    // 公众号 ProseMirror 在粘贴 HTML 末尾若是 <img>/<section> 而无段落节点，
    // 会自动追加一个标准高度的 <p><br></p> 作为光标占位段——这就是用户每次
    // 都要手动删的"二维码图下方空行"。在这里附一个零高度 <p>，让编辑器认
    // 为尾部已有可定位段，不再追加可见空段。
    html += '<p style="margin:0;padding:0;line-height:0;font-size:0;min-height:0;height:0;"><br/></p>';

    const plainText = content.textContent || '';

    let ok = false;
    let method = '';

    try {
      ok = await copyByClipboardApi(html, plainText);
      if (ok) method = 'ClipboardAPI';
    } catch {}

    if (!ok) {
      try {
        ok = await copyByClipboardEvent(html, plainText);
        if (ok) method = 'ClipboardEvent';
      } catch {}
    }

    _lastCopiedHtml = html;
    const imgCount = (html.match(/<img[\s>]/gi) || []).length;
    logConv(ok ? 'info' : 'error', ok ? '复制完成' : '复制失败', {
      method,
      htmlSize: (html.length / 1024).toFixed(1) + 'KB',
      imgCount,
    });

    btn.textContent = ok ? '\u2713 已复制' : '复制失败';
    btn.classList.add('copied');
    return ok;
  };

  window.getClipboardHtml = function() { return _lastCopiedHtml; };

  // ---- UI Controls ----
  const ZOOM_STEPS = [50, 75, 100, 125, 150, 200];
  let zoomIdx = ZOOM_STEPS.length - 1;

  window.goBack = function() {
    // Split layout: hide preview, show empty state
    const emptyState = document.getElementById('emptyState');
    const previewContent = document.getElementById('previewContent');
    const wxFooter = document.getElementById('wxFooter');
    if (emptyState) emptyState.style.display = '';
    if (previewContent) previewContent.style.display = 'none';
    if (wxFooter) wxFooter.style.display = 'none';
    document.body.classList.remove('has-preview');

    dropZone.classList.remove('processing');
    resetImportEntry();
    fileInput.value = '';
    const copyBtn = document.getElementById('copyBtn');
    if (copyBtn) { copyBtn.textContent = '复制全文'; copyBtn.classList.remove('copied'); }
    document.getElementById('statsBar').textContent = '';
    dispatchArticleEvent('fbif:article-cleared');
  };

  window.zoom = function(dir) {
    zoomIdx = Math.max(0, Math.min(ZOOM_STEPS.length - 1, zoomIdx + dir));
    const pct = ZOOM_STEPS[zoomIdx];
    document.getElementById('phoneFrame').style.maxWidth = (420 * pct / 100) + 'px';
    document.getElementById('zoomLabel').textContent = pct + '%';
    if (window._positionTOC) window._positionTOC();
  };

  // ---- Real-time theme updates ----
  window.updateThemePreview = function(config) {
    const contentArea = document.getElementById('contentArea');
    if (contentArea) applyThemeVars(contentArea, config);
  };

  // ---- Session snapshot (cross-channel navigation) ----
  // Captures all closure + DOM state needed to restore the editor after a top
  // pill switches channel. Closure copies of _articleHtml/_articleCopy/etc.
  // are the source of truth for `copyContent`; DOM is the source of truth for
  // preview. Both must be persisted, otherwise restore yields a broken
  // editor (preview shows but copy is empty, or vice versa).
  window._captureSessionSnapshot = function() {
    const ca = document.getElementById('contentArea');
    const titleDisplay = document.getElementById('previewTitleDisplay');
    const titleInput = document.getElementById('titleInput');
    const date = document.getElementById('wxDate');
    const previewContent = document.getElementById('previewContent');
    const emptyState = document.getElementById('emptyState');
    const wxFooter = document.getElementById('wxFooter');
    const batchListEl = document.getElementById('batchList');
    return {
      schema_version: 1,
      timestamp: Date.now(),
      closure_state: {
        article_html: _articleHtml,
        article_copy: _articleCopy,
        footer_html: _footerHtml,
        footer_copy: _footerCopy,
        is_repost: _isRepost,
        source_is_docx: _sourceIsDocx,
        copy_requires_wechat_image_transfer: _copyRequiresWechatImageTransfer,
        failed_srcs: _failedSrcs ? _failedSrcs.slice() : [],
        upload_done: _uploadDone,
        last_render_input: clonePlain(_lastRenderInput),
      },
      batch_state: {
        // File objects can't be serialized; persist only result metadata so a
        // restored editor can still display the batch list. Re-running a batch
        // requires re-uploading the files.
        file_meta: _batchFiles ? _batchFiles.map(f => ({ name: f.name, size: f.size })) : [],
        results: _batchResults ? JSON.parse(JSON.stringify(_batchResults)) : [],
      },
      dom: {
        content_area_html: ca ? ca.innerHTML : '',
        content_area_style: ca ? (ca.getAttribute('style') || '') : '',
        title_display: titleDisplay ? titleDisplay.textContent : '',
        title_input_value: titleInput ? titleInput.value : '',
        article_title_text: (document.getElementById('articleTitle') || {}).textContent || '',
        date_text: date ? date.textContent : '',
        preview_visible: previewContent ? previewContent.style.display !== 'none' : false,
        empty_visible: emptyState ? emptyState.style.display !== 'none' : true,
        footer_visible: wxFooter ? wxFooter.style.display !== 'none' : false,
        batch_visible: batchListEl ? batchListEl.style.display !== 'none' : false,
        batch_html: batchListEl ? batchListEl.innerHTML : '',
        has_preview_class: document.body.classList.contains('has-preview'),
        import_visible: importZone ? !importZone.hidden : true,
        doc_visible: docZone ? !docZone.hidden : false,
        doc_name: docName ? docName.textContent : '',
        doc_stat: docStat ? docStat.textContent : '',
        stats_text: (document.getElementById('statsBar') || {}).textContent || '',
        preview_scroll_top: (document.getElementById('previewScroll') || {}).scrollTop || 0,
      },
    };
  };

  window._restoreSessionSnapshot = function(snap) {
    if (!snap || snap.schema_version !== 1) return false;
    const cs = snap.closure_state || {};
    _articleHtml = cs.article_html || '';
    _articleCopy = cs.article_copy || '';
    _footerHtml = cs.footer_html || '';
    _footerCopy = cs.footer_copy || '';
    _isRepost = !!cs.is_repost;
    _sourceIsDocx = !!cs.source_is_docx;
    _copyRequiresWechatImageTransfer = !!cs.copy_requires_wechat_image_transfer;
    _failedSrcs = cs.failed_srcs || [];
    _uploadDone = !!cs.upload_done;
    _uploadPromise = null;
    _moreArticlesCopySeq++;
    _moreArticlesCopyJob = null;
    _lastRenderInput = cs.last_render_input || null;
    if (snap.batch_state) {
      _batchFiles = [];
      _batchResults = snap.batch_state.results || [];
    }
    const dom = snap.dom || {};
    const ca = document.getElementById('contentArea');
    if (ca) {
      ca.innerHTML = dom.content_area_html || '';
      if (dom.content_area_style) ca.setAttribute('style', dom.content_area_style);
      else ca.removeAttribute('style');
    }
    const titleDisplay = document.getElementById('previewTitleDisplay');
    if (titleDisplay) titleDisplay.textContent = dom.title_display || '';
    const titleInput = document.getElementById('titleInput');
    if (titleInput) titleInput.value = dom.title_input_value || '';
    // titleInput → articleTitle setter is defined later in app.html, so we set
    // h2#articleTitle directly here to avoid order-of-init breakage.
    const articleTitleEl = document.getElementById('articleTitle');
    if (articleTitleEl) articleTitleEl.textContent = dom.article_title_text || dom.title_input_value || '';
    const date = document.getElementById('wxDate');
    if (date) date.textContent = dom.date_text || '';
    const previewContent = document.getElementById('previewContent');
    if (previewContent) previewContent.style.display = dom.preview_visible ? '' : 'none';
    const emptyState = document.getElementById('emptyState');
    if (emptyState) emptyState.style.display = dom.empty_visible ? '' : 'none';
    const wxFooter = document.getElementById('wxFooter');
    if (wxFooter) wxFooter.style.display = dom.footer_visible ? '' : 'none';
    if (dom.doc_visible || (dom.preview_visible && !dom.import_visible)) {
      showImportSummary(
        dom.doc_name || dom.title_input_value || dom.article_title_text || '',
        dom.doc_stat || dom.stats_text || ''
      );
      const statsBarEl = document.getElementById('statsBar');
      if (statsBarEl) statsBarEl.textContent = formatSidebarStatus(dom.stats_text || '');
    } else {
      resetImportEntry();
    }
    const batchListEl = document.getElementById('batchList');
    if (batchListEl) {
      batchListEl.innerHTML = dom.batch_html || '';
      batchListEl.style.display = dom.batch_visible ? '' : 'none';
    }
    document.body.classList.toggle('has-preview', !!dom.has_preview_class);
    const previewScroll = document.getElementById('previewScroll');
    if (previewScroll) previewScroll.scrollTop = dom.preview_scroll_top || 0;
    dispatchArticleEvent('fbif:article-restored', { snapshot: snap });
    return true;
  };
}

// Inline scripts (site.html / app.html) push config / trigger rerender
// through this small surface. Defined at module scope so it's set even if
// initApp hasn't been called yet (e.g., before file is uploaded).
if (typeof window !== 'undefined') {
  window._engineApi = {
    ...window._engineApi,
    setActiveConfig,
    getActiveConfig,
    rerender: () => {
      if (typeof window._engineRerender === 'function') {
        return window._engineRerender();
      }
    },
  };
}
