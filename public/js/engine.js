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
  esc, escAttr, parseMdRuns, parseMdFrontmatter, normalizeClipboardTextForMarkdown,
  hasImageLikeLink, formatPlainTextForCopy, looksLikeArticleClipboardText,
  buildClipboardArticleBlocks, parseDocx, extractParagraph,
  W, R, WP, A, findAll, findOne, findDeep, wattr, rattr
} = parserModule;
export {
  esc, escAttr, parseMdRuns, parseMdFrontmatter, normalizeClipboardTextForMarkdown,
  hasImageLikeLink, formatPlainTextForCopy, looksLikeArticleClipboardText,
  buildClipboardArticleBlocks, parseDocx, extractParagraph,
  W, R, WP, A, findAll, findOne, findDeep, wattr, rattr
};

// Re-export convertRuns so templates can apply punctuation rules to runs
// that arrive pre-formed (e.g. the new URL→blocks path, where runs come
// from the Go backend and bypass parseMdRuns). convertText covers the
// plain-string elems (headings, captions) that carry no runs at all.
const punctuationModule = await import('./punctuation.js' + assetQuery);
export const { convertRuns, convertText, endsWithEnglishAbbreviation } = punctuationModule;

import { copyByClipboardApi, copyByClipboardEvent } from './clipboard.js';
import { createCopySession } from './copy-session.js';
import { loadThemeCSS, processForCopy, applyThemeVars } from './css-inline.js';
import { normalizeWechatRichHtml } from './wechat-normalize.js';
import {
  loadCards as loadMoreArticlesCards,
  saveCards as saveMoreArticlesCards,
  mergeIntoFooter as mergeMoreArticlesIntoFooter,
  compositePreviewDataUrl,
  ensureCompositeReady,
  ensureCopyCompositeDataUrl,
  makeFreshUploadCards,
  copySafeMoreArticleCard,
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
const authorCardAutofillModule = await import('./author-card-autofill.js' + assetQuery);
const { applySeriesAuthorCardAutofill } = authorCardAutofillModule;
const blockMergeModule = await import('./blocks/block-merge.js' + assetQuery);
const { stripReadonlyOverride } = blockMergeModule;
const articleFooterDefaultsModule = await import('./article-footer-defaults.js' + assetQuery);
const { normalizeArticleFooterProfileOverride } = articleFooterDefaultsModule;
const previewDomModule = await import('./preview-dom.js' + assetQuery);
const {
  commitPreparedPreviewContent,
  discardPreparedPreviewContent,
  renderPreviewContent,
  prepareConnectedPreviewContent,
  preparePreviewContent,
  validatePreparedPreview,
  updatePreviewFooter,
  adoptPreviewContent,
  collectReusableArticleImages,
  indexPreparedPreviewImages,
} = previewDomModule;
const previewImageLoaderModule = await import('./preview-image-loader.js' + assetQuery);
const {
  preloadLocalPreviewImages,
  refreshLocalPreviewImages,
} = previewImageLoaderModule;
const feishuDocReadyModule = await import('./feishu-doc-ready.js' + assetQuery);
const {
  finalizeFeishuArticlePreview,
  prepareFeishuArticlePreview,
} = feishuDocReadyModule;

// Pre-load theme CSS
const _themeCSSReady = loadThemeCSS();

// 把疑似内部技术细节的错误消息兜底成中文白话（守「绝不向用户暴露内部错误」契约）。
// 后端 doc-to-docx 等已尽量中文化，此处是最后一道防线：兜住后端漏网、解析器抛出的
// zip/Go/栈技术串。命中技术特征 → 通用中文兜底；否则原样透传。
const INTERNAL_ERR_RE = /LIBREOFFICE|CLOUDCONVERT|converter|multipart|EOF|zip keys|document\.xml|\bexec\b|\bPATH\b|undefined|\.go:|panic|\bnull\b|\bstack\b|\$\{|0x[0-9a-f]{2,}/i;
function sanitizeErrMsg(raw) {
  const s = String(raw == null ? '' : raw).trim();
  if (!s) return '处理失败，请重试';
  return INTERNAL_ERR_RE.test(s) ? '处理失败，请重试或更换文件' : s;
}

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
        reject(new Error('DOC 转换失败: ' + (msg ? sanitizeErrMsg(msg) : ('错误码 ' + xhr.status))));
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

// Remote/server-cached images can paint top-down while bytes arrive, so keep
// their atomic fade. Local DOCX blob images are pre-decoded and moved into the
// preview as ready nodes; never hide those behind the remote loading class.
function hookPreviewImages(container) {
  if (!container) return;
  const imgs = typeof container.querySelectorAll === 'function'
    ? container.querySelectorAll('img')
    : container;
  for (const img of imgs) {
    const src = img.getAttribute('src') || '';
    if (src.startsWith('blob:')) continue;
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
function isWebsiteTemplate() {
  return !!(_activeTemplate && (_activeTemplate.transferMmbiz || _activeTemplate.id === 'foodtalks-site'));
}

function normalizeRich(html) {
  if (isWebsiteTemplate()) return html;
  return normalizeWechatRichHtml(html);
}

// loadBaseFooterHtml 返回 { html, isV2 }：isV2=true 表示 v2 双预设底部（已按 presetId 分原创/转载）。
// 调用方把 isV2 作为局部值传给 buildFooterWithMoreArticles，避免模块级单例在 await 间被并发重入覆盖
// （codex review #4 竞态修复——多条 load→build 链交错时单例会互相覆盖 isV2）。
async function loadBaseFooterHtml(opts = {}) {
  // 公众号 bottom_blocks 启用时，整段底部由区块渲染（替代 footer.html）。
  // 模板返回 null/undefined → 模块未接管，fallback 到 footerUrl(footer.html)。
  // 模板返回对象时即表示已接管；对象里的空 html 是“明确输出空底部”，不能再 fallback。
  // isRepost 显式传入（重渲染用 cached.isRepost，避免读全局 _isRepost 漂移；codex review #5）。
  const isRepost = opts.isRepost != null ? opts.isRepost : _isRepost;
  if (_activeTemplate && typeof _activeTemplate.renderBottomFooter === 'function') {
    const renderOpts = { isRepost };
    if (Object.prototype.hasOwnProperty.call(opts, 'bottomBlocksOverride')) {
      renderOpts.bottomBlocksOverride = opts.bottomBlocksOverride;
    }
    if (Object.prototype.hasOwnProperty.call(opts, 'articleMeta')) {
      renderOpts.articleMeta = opts.articleMeta;
    }
    if (Object.prototype.hasOwnProperty.call(opts, 'footerVisibilityOverride')) {
      renderOpts.footerVisibilityOverride = opts.footerVisibilityOverride;
    }
    const res = _activeTemplate.renderBottomFooter(renderOpts);
    if (res && typeof res === 'object') {
      return {
        html: res.html == null ? '' : String(res.html),
        isV2: !!res.isV2,
        articleFooterProfile: res.articleFooterProfile || null,
      };
    }
    const html = res; // 自定义模板可返回字符串；空字符串仍按“未接管”兼容旧行为。
    if (html != null && html !== '') {
      return { html, isV2: false, articleFooterProfile: null };
    }
  }
  return { html: await stockFooterReady(), isV2: false, articleFooterProfile: null };
}

// Module-level flag: true when the current article was fetched from a URL
// (i.e. it is a repost). Set explicitly by each article-input entry
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

function bottomOverrideKey(accountId) {
  return 'bb.override::' + accountId;
}

function readBottomBlocksOverride(accountId) {
  let raw = {};
  try { raw = JSON.parse(localStorage.getItem(bottomOverrideKey(accountId)) || 'null') || {}; } catch { raw = {}; }
  return migrateStoredBottomBlocksOverride(accountId, raw);
}

function hasBottomBlocksOverride(override) {
  return !!(override && typeof override === 'object' && !Array.isArray(override) && Object.keys(override).length);
}

function normalizeFooterVisibilityOverride(override) {
  if (!override || typeof override !== 'object' || Array.isArray(override)) return undefined;
  const out = {};
  if (override.enabled === true || override.enabled === false || override.enabled === 'show' || override.enabled === 'hide') {
    out.enabled = override.enabled;
  }
  if (override.blocks && typeof override.blocks === 'object' && !Array.isArray(override.blocks)) {
    const blocks = {};
    for (const id of Object.keys(override.blocks)) {
      const value = override.blocks[id];
      if (value === true || value === false || value === 'show' || value === 'hide') blocks[id] = value;
    }
    if (Object.keys(blocks).length) out.blocks = blocks;
  }
  return Object.keys(out).length ? out : undefined;
}

function visibilityOverrideHides(value) {
  return value === false || value === 'hide';
}

function visibilityOverrideShows(value) {
  return value === true || value === 'show';
}

function normalizeArticleArtifactOverride(override) {
  if (!override || typeof override !== 'object' || Array.isArray(override)) return undefined;
  const out = {};
  for (const id of Object.keys(override)) {
    const value = override[id];
    if (value === true || value === false || value === 'show' || value === 'hide' || value === 'hidden') {
      out[id] = value;
    } else if (value && typeof value === 'object' && !Array.isArray(value)) {
      const next = {};
      if (value.hidden === true || value.hidden === false) next.hidden = value.hidden;
      if (value.enabled === true || value.enabled === false) next.enabled = value.enabled;
      if (Object.keys(next).length) out[id] = next;
    }
  }
  return Object.keys(out).length ? out : undefined;
}

function writeBottomBlocksOverride(accountId, override) {
  const next = hasBottomBlocksOverride(override) ? override : {};
  try {
    if (hasBottomBlocksOverride(next)) localStorage.setItem(bottomOverrideKey(accountId), JSON.stringify(next));
    else localStorage.removeItem(bottomOverrideKey(accountId));
  } catch {}
  window._bottomBlocksOverride = hasBottomBlocksOverride(next) ? next : undefined;
  const inst = window._blockPanelInst;
  if (inst && window._blockPanelAcct === accountId && typeof inst.setOverride === 'function') {
    inst.setOverride(next);
  }
}

function bottomBlocksModuleForCurrentRule() {
  const cfg = (window._activeConfig && window._activeConfig.config) || _activeConfig || {};
  return cfg && cfg.modules && cfg.modules.bottom_blocks;
}

function frontendEditableBlockIDs(module) {
  const ids = new Set();
  function collect(blocks) {
    if (!blocks || typeof blocks !== 'object' || Array.isArray(blocks)) return;
    for (const id of Object.keys(blocks)) {
      const block = blocks[id];
      if (block && typeof block === 'object' && block.frontend_editable === true) ids.add(id);
    }
  }
  if (module && Array.isArray(module.presets)) {
    for (const preset of module.presets) collect(preset && preset.blocks);
  } else {
    collect(module && module.blocks);
  }
  return ids;
}

function migrateStoredBottomBlocksOverride(accountId, override) {
  const module = bottomBlocksModuleForCurrentRule();
  if (!module || typeof module !== 'object') return override;
  const next = stripReadonlyOverride(override, frontendEditableBlockIDs(module));
  try {
    if (JSON.stringify(next) !== JSON.stringify(override)) {
      if (hasBottomBlocksOverride(next)) localStorage.setItem(bottomOverrideKey(accountId), JSON.stringify(next));
      else localStorage.removeItem(bottomOverrideKey(accountId));
    }
  } catch {}
  return next;
}

async function waitForActiveRuleConfigReady(reason) {
  const p = typeof window !== 'undefined' && window._activeRuleReadyPromise;
  if (p && typeof p.then === 'function' && window._activeRuleReady !== true) {
    await p;
  }
  const state = typeof window !== 'undefined' && window._activeRuleState;
  if (state && state !== 'resolved') {
    logConv('error', '规则尚未由服务端确认，已停止生成最终排版', { reason, state });
    throw new Error('排版规则尚未加载成功，请点击顶部“重试加载”后再导入。');
  }
}

function resolveAuthorCardOverrideForArticle(result) {
  const pack = typeof window !== 'undefined' ? window._activeRulePack : null;
  const accountId = pack && pack.id;
  if (!accountId) return { accountId: '', override: undefined, changed: false };
  const config = (window._activeConfig && window._activeConfig.config) || _activeConfig || {};
  const current = readBottomBlocksOverride(accountId);
  const applied = applySeriesAuthorCardAutofill({ result, config, accountId, override: current });
  return { accountId, override: applied.override, changed: applied.changed };
}

function readCurrentBottomBlocksOverrideForArticle() {
  const pack = typeof window !== 'undefined' ? window._activeRulePack : null;
  const accountId = pack && pack.id;
  return { accountId: accountId || '', override: accountId ? readBottomBlocksOverride(accountId) : undefined };
}

function syncAuthorCardForNewArticle(result) {
  const applied = resolveAuthorCardOverrideForArticle(result);
  const accountId = applied.accountId;
  if (!accountId) return undefined;
  if (applied.changed) writeBottomBlocksOverride(accountId, applied.override);
  return applied.override;
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
  const after = footerBehaviorText('footer_repost_notice', '* 本文为转载文章。');
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

function moreArticleCardNeedsCopyComposite(card) {
  return !!(card && ((card.title && String(card.title).trim()) || card.cover_data_url));
}

async function copySafeMoreArticleCardForFooter(card) {
  if (moreArticleCardNeedsCopyComposite(card)) await ensureCopyCompositeDataUrl(card);
  return copySafeMoreArticleCard(card);
}

// Build a footer HTML that has the "更多文章" section replaced with the user's
// current card config. Preview can use OSS/proxy URLs; the WeChat copy layer
// must carry a local bitmap composite so title overlays survive T0 draft save.
async function buildFooterWithMoreArticles(baseFooterHtml, opts = {}) {
  const isRepost = opts.isRepost != null ? opts.isRepost : _isRepost;
  // v2 双预设已按 presetId 分原创/转载，跳过旧锚点替换（codex review #4，互斥）。isV2 由调用方局部传入。
  const base = opts.isV2 ? baseFooterHtml : applyRepostTransform(baseFooterHtml, isRepost);
  const transformed = normalizeWechatRichHtml(base);
  // opts.cards：接口排版传空列表（卡片要编辑挑、要画布合成）；网页一律读本篇存的卡片。
  const ownCards = !Array.isArray(opts.cards);
  const cards = ownCards ? loadMoreArticlesCards() : opts.cards;
  const cardsForFooter = opts.copySafe
    ? await Promise.all(cards.map(copySafeMoreArticleCardForFooter))
    : await Promise.all(cards.map(async c => ({ ...c, final_url: await compositePreviewDataUrl(c) })));
  if (ownCards && opts.copySafe && (!opts.canCommitCards || opts.canCommitCards())) saveMoreArticlesCards(cards);
  return normalizeWechatRichHtml(mergeMoreArticlesIntoFooter(transformed, cardsForFooter));
}

function currentRuntimeConfig() {
  const cfg = (window._activeConfig && window._activeConfig.config) || _activeConfig || {};
  return cfg && cfg.config && cfg.config.modules ? cfg.config : cfg;
}

function hasManagedBottomBlocksFooter() {
  const cfg = currentRuntimeConfig();
  const bb = cfg && cfg.modules && cfg.modules.bottom_blocks;
  return !!(bb && typeof bb === 'object' && bb.enabled !== false);
}

// footerChecked：页面隐藏的 #footerEnabled 勾选框（syncWorkflowControls 按规则 footer_enabled 设）。
function footerEnabledFor(footerVisibilityOverride, footerChecked) {
  const override = normalizeFooterVisibilityOverride(footerVisibilityOverride);
  if (override && visibilityOverrideHides(override.enabled)) return false;
  if (override && visibilityOverrideShows(override.enabled)) return true;
  return hasManagedBottomBlocksFooter() || !!footerChecked;
}

function renderArticleInputWith(template, input) {
  if (!input) throw new Error('没有可渲染的文章输入');
  const isRepost = !!input.isRepost;
  if (input.kind === 'docx') {
    return template.processDocx({
      ...input.payload,
      isRepost,
      config: _activeConfig,
      articleArtifactOverride: input.articleArtifactOverride,
      articleFooterProfileOverride: input.articleFooterProfileOverride,
    });
  }
  if (input.kind === 'md') {
    if (typeof template.processMd !== 'function') throw new Error('此模板不支持该文件格式');
    return template.processMd(input.payload, {
      isRepost,
      config: _activeConfig,
      articleArtifactOverride: input.articleArtifactOverride,
      articleFooterProfileOverride: input.articleFooterProfileOverride,
    });
  }
  if (input.kind === 'blocks') {
    if (typeof template.processBlocks !== 'function') throw new Error('当前模板不支持 blocks 路径');
    return template.processBlocks(input.payload, {
      isRepost,
      config: _activeConfig,
      articleArtifactOverride: input.articleArtifactOverride,
      articleFooterProfileOverride: input.articleFooterProfileOverride,
    });
  }
  throw new Error('未知文章输入类型');
}

function copyBodyHasMeaningfulContent(html) {
  const source = String(html || '').trim();
  if (!source) return false;
  if (/<img\b[^>]*\bsrc\s*=\s*(["'])[^"']+\1/i.test(source) || /<table\b/i.test(source)) return true;
  const visibleText = source
    .replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, ' ')
    .replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, ' ')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&(nbsp|ensp|emsp|#160|#xa0);/gi, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  return visibleText.length > 0;
}

// 复制层内联：主题 CSS 写进 style 属性（公众号、FoodTalks 后台都不认 class），再按渠道归一化。
function inlineCopyHtml(sourceArtifact, themeCSS, config) {
  return normalizeRich(processForCopy(
    sourceArtifact,
    themeCSS,
    config,
    { preserveClasses: isWebsiteTemplate() },
  ));
}

// 公众号 ProseMirror 在粘贴 HTML 末尾若是 <img>/<section> 而无段落节点，
// 会自动追加一个标准高度的 <p><br></p> 作为光标占位段——这就是用户每次
// 都要手动删的"二维码图下方空行"。在这里附一个零高度 <p>，让编辑器认
// 为尾部已有可定位段，不再追加可见空段。
function withPasteTail(html) {
  if (isWebsiteTemplate()) return html;
  return html + '<p style="margin:0;padding:0;line-height:0;font-size:0;min-height:0;height:0;"><br/></p>';
}

// 接口排版（render-service/layout.mjs）：与网页「导入 → 预览 → 点复制」同一条链，函数都是网页在用的那几个——
// 模板排正文、loadBaseFooterHtml 出底部、站内图转成图片数据、复制层内联。网页才有的不做：预览、本篇手改、
// 转存图床（Go 做）、更多文章（cards 恒为空；要藏掉整块由调用方传 footerVisibilityOverride）。
// 会改 _activeTemplate/_isRepost 这两个模块级状态，只给 Node 里逐篇串行调用。
export async function layoutArticleForCopy(template, input, {
  autofillAuthorCard = false,
  footerVisibilityOverride,
  footerChecked = false,
} = {}) {
  _activeTemplate = template;
  const isRepost = !!input.isRepost;
  _isRepost = isRepost;
  const bodyOnly = !!input.bodyOnly;
  const result = await renderArticleInputWith(template, input);
  const articleMeta = result.articleMeta || {};
  const visibility = bodyOnly ? undefined : normalizeFooterVisibilityOverride(footerVisibilityOverride);
  let footerCopy = '';
  if (!bodyOnly) {
    const bottomBlocksOverride = autofillAuthorCard
      ? resolveAuthorCardOverrideForArticle(result).override
      : readCurrentBottomBlocksOverrideForArticle().override;
    const { html: baseFooterHtml, isV2 } = await loadBaseFooterHtml({
      isRepost,
      bottomBlocksOverride,
      articleMeta,
      footerVisibilityOverride: visibility,
    });
    footerCopy = await buildFooterWithMoreArticles(baseFooterHtml, { isRepost, isV2, copySafe: true, cards: [] });
  }
  // showPreview 先归一成预览层、再归一一次成复制层，这里照样两次。
  const articleCopy = normalizeRich(normalizeRich(result.lines.join('\n')));
  footerCopy = bodyOnly ? '' : normalizeRich(footerCopy);
  const materialized = await materializeLazyImages(articleCopy, footerCopy);
  const footer = !bodyOnly && footerEnabledFor(visibility, footerChecked);
  const sourceArtifact = materialized.articleCopy + '\n' + (footer ? materialized.footerCopy : '');
  const config = (window._activeConfig && window._activeConfig.config) || {};
  const html = withPasteTail(inlineCopyHtml(sourceArtifact, await _themeCSSReady, config));
  return {
    html,
    title: result.title || input.title || '',
    footer,
    isRepost,
    articleMeta,
    imageCount: result.imgN || 0,
    headingCount: result.headingN || 0,
    hasBody: copyBodyHasMeaningfulContent(materialized.articleCopy),
    unreadableImages: materialized.failedSrcs || [],
  };
}

// ---- App Initialization ----
export function initApp(initialTemplate) {
  let template = initialTemplate;
  _activeTemplate = initialTemplate;
  function applyTemplateShell(nextTemplate) {
    document.getElementById('pageTitle').textContent = nextTemplate.name;
    document.getElementById('pageSubtitle').textContent = nextTemplate.description;
    const fileHint = document.getElementById('fileHint');
    if (fileHint) fileHint.textContent = 'Word / MD / TXT';
    fileInputEl.accept = nextTemplate.formats.join(',');
  }
  const fileInputEl = document.getElementById('fileInput');
  applyTemplateShell(template);
  fileInputEl.multiple = false;

  const dropZone = document.getElementById('dropZone');
  const fileInput = fileInputEl;
  const uploadLabel = dropZone ? dropZone.querySelector('[data-upload-label]') : null;
  const uploadTrigger = dropZone ? dropZone.querySelector('[data-upload-trigger]') : null;
  const importInput = document.getElementById('urlInput');
  const addFileText = dropZone ? dropZone.querySelector('[data-add-file-text]') : null;
  const uploadedFile = dropZone ? dropZone.querySelector('[data-uploaded-file]') : null;
  const uploadedName = dropZone ? dropZone.querySelector('[data-uploaded-name]') : null;
  const uploadedSize = dropZone ? dropZone.querySelector('[data-uploaded-size]') : null;
  const uploadExt = dropZone ? dropZone.querySelector('[data-file-ext]') : null;
  const removeUpload = dropZone ? dropZone.querySelector('[data-remove-upload]') : null;
  const defaultUploadLabel = uploadLabel ? uploadLabel.textContent : '粘贴或拖拽至这里上传';
  const progress = document.getElementById('progress');
  const progressFill = document.getElementById('progressFill');
  const progressText = document.getElementById('progressText');
  const progressPercentEl = document.getElementById('progressPercent');
  const errorEl = document.getElementById('error');
  // 外壳侧写进度只走这里：条宽、aria 值和卡内可见百分比必须一起更新，
  // 否则那个百分比会停在旧数字上——可见控件不许说假话。
  // （飞书导入路径由 import-progress.js 的 bindImportProgressDom 统一渲染。）
  function setShellProgress(percent) {
    const value = Math.max(0, Math.min(100, Number(percent) || 0));
    progressFill.style.width = value + '%';
    const rounded = Math.round(value);
    progress.setAttribute?.('aria-valuenow', String(rounded));
    if (progressPercentEl) progressPercentEl.textContent = rounded + '%';
  }
  const importZone = document.getElementById('importZone');
  const docZone = document.getElementById('docZone');
  const docName = document.getElementById('docName');
  const docStat = document.getElementById('docStat');
  const docSwap = document.getElementById('docSwap');

  function showError(msg) { errorEl.textContent = msg; errorEl.style.display = 'block'; }

  function displayFileExt(file) {
    const ext = String((file && file.name || '').split('.').pop() || '').toUpperCase();
    if (ext === 'DOCX') return 'DOC';
    if (ext === 'MARKDOWN') return 'MD';
    if (ext === 'TXT') return 'T';
    return ext ? ext.slice(0, 3) : 'T';
  }

  function displayFileSize(file) {
    const bytes = Number(file && file.size);
    if (!Number.isFinite(bytes) || bytes <= 0) return '';
    if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function setDropActive(active) {
    if (!dropZone) return;
    dropZone.classList.toggle('dragover', !!active);
    if (uploadLabel && !dropZone.classList.contains('has-file')) {
      uploadLabel.textContent = active ? '粘贴或拖拽上传' : defaultUploadLabel;
    }
  }

  function clearUploadVisual() {
    if (!dropZone) return;
    setDropActive(false);
    dropZone.classList.remove('has-file');
    if (uploadLabel) uploadLabel.textContent = defaultUploadLabel;
    if (addFileText) addFileText.textContent = '上传文件';
    if (uploadedName) uploadedName.textContent = '';
    if (uploadedSize) uploadedSize.textContent = '';
    if (uploadExt) uploadExt.textContent = 'T';
    if (uploadedFile) uploadedFile.hidden = true;
    if (fileInput) fileInput.value = '';
  }

  function showUploadFile(file) {
    if (!dropZone || !file) return;
    setDropActive(false);
    dropZone.classList.add('has-file');
    if (addFileText) addFileText.textContent = '重新选择文件';
    if (uploadedName) uploadedName.textContent = file.name || '未命名文件';
    if (uploadedSize) uploadedSize.textContent = displayFileSize(file);
    if (uploadExt) uploadExt.textContent = displayFileExt(file);
    if (uploadedFile) uploadedFile.hidden = false;
  }

  function showImportEntry() {
    if (importZone) importZone.hidden = false;
    if (docZone) docZone.hidden = true;
  }

  function resetImportEntry() {
    showImportEntry();
    dropZone.classList.remove('processing');
    clearUploadVisual();
    progress.style.display = 'none';
    setShellProgress(0);
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

  function showImportSummary(title, stats, options = {}) {
    if (docZone) {
      if (docName) docName.textContent = title || '未命名文档';
      if (docStat) docStat.textContent = formatImportStat(stats);
      docZone.hidden = true;
    }
    if (importZone) importZone.hidden = false;
    dropZone.classList.remove('processing');
    if (!options.keepProgressVisible) progress.style.display = 'none';
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

  function footerCheckboxChecked() {
    const footerEl = document.getElementById('footerEnabled');
    return !!(footerEl && footerEl.checked);
  }

  function isFooterEnabledForCurrentArticle() {
    if (_articleBodyOnly) return false;
    return footerEnabledFor(
      _articleSession && Object.prototype.hasOwnProperty.call(_articleSession, 'footerVisibilityOverride')
        ? _articleSession.footerVisibilityOverride
        : window._footerVisibilityOverride,
      footerCheckboxChecked(),
    );
  }

  function isFooterEnabledForSession(session) {
    if (!session || session.bodyOnly) return false;
    return footerEnabledFor(session.footerVisibilityOverride, footerCheckboxChecked());
  }

  function renderPreviewFooter(contentArea, footerHtml) {
    const result = updatePreviewFooter(contentArea, footerHtml);
    const images = result.preservedArticle
      ? result.images
      : renderPreviewContent(contentArea, _articleHtml, footerHtml);
    hookPreviewImages(images);
    return result.preservedArticle;
  }

  function refreshFooterPreviewFromVisibility() {
    if (typeof window._updateFooter === 'function') {
      window._updateFooter('');
      return true;
    }
    const contentArea = document.getElementById('contentArea');
    if (!contentArea || !_articleHtml) return false;
    const enabled = isFooterEnabledForCurrentArticle();
    renderPreviewFooter(contentArea, enabled ? _footerHtml : '');
    markFailedImages(_failedSrcs);
    return true;
  }

  // Cache the latest parsed inputs so rerender() can re-run template.process*
  // with the new config without re-parsing the source file.
  let _lastRenderInput = null;
  // shape: { kind: 'docx'|'md'|'blocks', payload: <data>, isRepost, title? }
  let _articleSession = null;
  // shape: { input, title, articleHtml, footerHtml, stats, articleMeta, articleFooterProfile, bottomBlocksOverride, bottomBlocksAccountId, sourceIsDocx, bodyOnly }

  async function parseFileToArticleInput(file, isRepost, opts = {}) {
    const sourceIsDocx = file.name.toLowerCase().endsWith('.docx');
    const fallbackTitle = Object.prototype.hasOwnProperty.call(opts, 'title')
      ? String(opts.title || '')
      : file.name.replace(/\.\w+$/i, '');
    const displayName = opts.displayName ? String(opts.displayName) : '';
    const articleArtifactOverride = normalizeArticleArtifactOverride(opts.articleArtifactOverride);
    if (sourceIsDocx) {
      const importProgress = opts && opts.importProgress;
      const docxData = await parseDocx(file, {
        onProgress(done, total, status) {
          importProgress?.updatePhase('parse', done, total, {
            status: status || '正在解析 Word 正文…',
          });
        },
      });
      return { kind: 'docx', payload: docxData, isRepost, title: fallbackTitle, displayName, sourceIsDocx, articleArtifactOverride };
    }
    if (typeof template.processMd === 'function') {
      const text = await file.text();
      return { kind: 'md', payload: text, isRepost, title: fallbackTitle, displayName, sourceIsDocx: false, articleArtifactOverride };
    }
    throw new Error('此模板不支持该文件格式');
  }

  async function renderArticleInput(input) {
    return renderArticleInputWith(template, input);
  }

  function formatRenderStats(result, elapsed) {
    const parts = [
      '段落: ' + result.lines.length,
      '图片: ' + result.imgN,
      '标题: ' + result.headingN,
    ];
    if (elapsed) parts.push('耗时: ' + elapsed + 's');
    return parts.join(' | ');
  }

  async function buildArticleSession(input, result, options = {}) {
    const commitGate = options.commitGate || null;
    if (commitGate && commitGate.stale()) return null;
    if (options.resetArticleArtifacts) resetMoreArticlesForNewArticle();
    const bodyOnly = !!input.bodyOnly;
    const articleHtml = result.lines.join('\n');
    const articleMeta = result.articleMeta || {};
    const footerVisibilityOverride = bodyOnly
      ? undefined
      : normalizeFooterVisibilityOverride(
        Object.prototype.hasOwnProperty.call(options, 'footerVisibilityOverride')
          ? options.footerVisibilityOverride
          : input.footerVisibilityOverride
      );
    const bottomBlocksState = bodyOnly
      ? { override: undefined, accountId: null }
      : options.autofillAuthorCard
      ? resolveAuthorCardOverrideForArticle(result)
      : readCurrentBottomBlocksOverrideForArticle();
    const isRepost = !!input.isRepost;
    let footerHtml = '';
    let footerCopy = '';
    let articleFooterProfile = null;
    if (!bodyOnly) {
      const { html: baseFooterHtml, isV2, articleFooterProfile: profile } = await loadBaseFooterHtml({
        isRepost,
        bottomBlocksOverride: bottomBlocksState.override,
        articleMeta,
        footerVisibilityOverride,
      });
      if (commitGate && commitGate.stale()) return null;
      articleFooterProfile = profile || null;
      footerHtml = await buildFooterWithMoreArticles(baseFooterHtml, { isRepost, isV2 });
      if (commitGate && commitGate.stale()) return null;
      footerCopy = await buildFooterWithMoreArticles(baseFooterHtml, {
        isRepost,
        isV2,
        copySafe: true,
        canCommitCards: () => !commitGate || !commitGate.stale(),
      });
      if (commitGate && commitGate.stale()) return null;
    }
    const elapsed = options.startedAt ? ((performance.now() - options.startedAt) / 1000).toFixed(1) : options.elapsed;
    const title = result.title || input.title || '';
    const articleArtifactOverride = normalizeArticleArtifactOverride(
      Object.prototype.hasOwnProperty.call(options, 'articleArtifactOverride')
        ? options.articleArtifactOverride
        : input.articleArtifactOverride
    );
    const articleFooterProfileOverride = normalizeArticleFooterProfileOverride(
      Object.prototype.hasOwnProperty.call(options, 'articleFooterProfileOverride')
        ? options.articleFooterProfileOverride
        : input.articleFooterProfileOverride
    );
    return {
      input: { ...input, title, footerVisibilityOverride, articleArtifactOverride, articleFooterProfileOverride },
      result,
      title,
      displayName: input.displayName || title,
      articleHtml,
      footerHtml,
      footerCopy,
      stats: options.stats || formatRenderStats(result, elapsed),
      articleMeta,
      articleFooterProfile,
      footerVisibilityOverride,
      articleArtifactOverride,
      articleFooterProfileOverride,
      bottomBlocksOverride: bottomBlocksState.override,
      bottomBlocksAccountId: bottomBlocksState.accountId,
      sourceIsDocx: !!input.sourceIsDocx,
      bodyOnly,
    };
  }

  function activateArticleSession(session, options = {}) {
    if (!session) return;
    _articleInputPending = false;
    _articleSession = session;
    _lastRenderInput = session.input || null;
    _isRepost = !!(_lastRenderInput && _lastRenderInput.isRepost);
    _sourceIsDocx = !!session.sourceIsDocx;
    _articleBodyOnly = !!session.bodyOnly;
    window._articleMeta = session.articleMeta || {};
    window._articleFooterProfile = session.articleFooterProfile || null;
    window._footerVisibilityOverride = normalizeFooterVisibilityOverride(session.footerVisibilityOverride);
    window._articleArtifactOverride = normalizeArticleArtifactOverride(session.articleArtifactOverride);
    window._articleFooterProfileOverride = normalizeArticleFooterProfileOverride(session.articleFooterProfileOverride);
    const accountId = session.bottomBlocksAccountId || (window._activeRulePack && window._activeRulePack.id);
    if (accountId && session.bottomBlocksOverride !== undefined) {
      writeBottomBlocksOverride(accountId, session.bottomBlocksOverride || {});
    }
    showPreview(session.title || '', session.articleHtml, session.footerHtml, session.stats, session.displayName || session.title || '', {
      bodyOnly: _articleBodyOnly,
      footerCopy: session.footerCopy,
      preparedImages: options.preparedImages,
      preparedPreview: options.preparedPreview,
      preparedClipboardArtifact: options.preparedClipboardArtifact,
      keepProgressVisible: options.keepProgressVisible,
      copyPrepared: options.copyPrepared,
      articleCopy: options.articleCopy,
      requiresWechatTransfer: options.requiresWechatTransfer,
    });
    if (typeof window !== 'undefined' && typeof window.dispatchEvent === 'function' && typeof CustomEvent === 'function') {
      window.dispatchEvent(new CustomEvent('fbif:article-session-activated', { detail: { session } }));
    }
    flushRerenderAfterInput();
  }

  function handleUploadFiles(files, opts) {
    const list = Array.from(files || []);
    if (list.length === 0) return;
    const valid = list.filter(isValidFile);
    if (valid.length === 0) {
      showImportEntry();
      showError('请上传 ' + template.formats.join(' 或 ') + ' 文件');
      return;
    }
    const selected = valid[0];
    showUploadFile(selected);
    handleFile(selected, opts);
  }

  // 2026-08-03 左栏两段式：导入卡里有输入框，整卡点击不再弹文件选择器，
  // 上传只由 [data-upload-trigger] 按钮触发。
  uploadTrigger?.addEventListener('click', () => {
    fileInput.value = '';
    fileInput.click();
  });
  dropZone.addEventListener('dragenter', e => { e.preventDefault(); setDropActive(true); });
  dropZone.addEventListener('dragover', e => { e.preventDefault(); setDropActive(true); });
  dropZone.addEventListener('dragleave', e => {
    if (!e.relatedTarget || !dropZone.contains(e.relatedTarget)) setDropActive(false);
  });
  dropZone.addEventListener('drop', e => {
    e.preventDefault();
    setDropActive(false);
    handleUploadFiles(e.dataTransfer.files, { isRepost: false });
  });
  fileInput.addEventListener('change', () => {
    if (!fileInput.files.length) return;
    handleUploadFiles(fileInput.files, { isRepost: false });
    fileInput.value = '';
  });
  if (removeUpload) {
    removeUpload.addEventListener('click', e => {
      e.preventDefault();
      e.stopPropagation();
      clearUploadVisual();
      fileInput.value = '';
      importInput?.focus();
    });
  }
  document.addEventListener('paste', e => {
    const files = e.clipboardData && e.clipboardData.files;
    if (!files || !files.length) return;
    e.preventDefault();
    e.stopPropagation();
    handleUploadFiles(files, { isRepost: false });
  }, true);
  if (docSwap) {
    docSwap.addEventListener('click', () => {
      resetImportEntry();
      fileInput.click();
    });
  }

  // Exposed so app.html fetchArticle() can feed the synthetic File produced
  // from a URL fetch through the same pipeline as a local upload, but with
  // isRepost=true so the footer swaps to the 转载 variant.
  window._handleFile = (file, opts) => {
    if (opts && opts.isRepost) clearUploadVisual();
    if (opts && opts.source === 'feishu-doc-import') showUploadFile(file);
    return handleFile(file, opts);
  };

  function copyPlainTextToClipboard(text) {
    return (async () => {
      // HTML 源码可能很长。部分 WebView 的 navigator.clipboard.writeText()
      // 会一直 pending，导致菜单不收起且没有反馈。先使用用户点击手势内可同步
      // 完成的 selection copy；只有浏览器拒绝该路径时才调用现代 API，并给它
      // 明确超时，避免复制入口永久卡住。
      const previousFocus = document.activeElement;
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.left = '-9999px';
      document.body.appendChild(ta);
      ta.focus();
      ta.select();
      let ok = false;
      try { ok = document.execCommand('copy'); } catch {}
      ta.remove();
      if (previousFocus && typeof previousFocus.focus === 'function') previousFocus.focus();
      if (ok) return true;

      try {
        if (navigator.clipboard) {
          return await Promise.race([
            navigator.clipboard.writeText(text).then(() => true, () => false),
            new Promise(resolve => setTimeout(() => resolve(false), 1200)),
          ]);
        }
      } catch {}
      return false;
    })();
  }

  function getPlainTextCopyValue() {
    const output = document.querySelector('.plain-text-page-output');
    if (output && typeof output.value === 'string') return output.value;
    if (output) return normalizePlainTextOutputValue(output.innerText || output.textContent || '');
    return _plainTextCopy || '';
  }

  function normalizePlainTextOutputValue(text) {
    return String(text || '')
      .replace(/\r\n?/g, '\n')
      .replace(/\u00A0/g, ' ')
      .replace(/\n{3,}/g, '\n\n');
  }

  function showPlainTextResult(text) {
    const converted = formatPlainTextForCopy(text);
    if (!converted) return false;
    if (window._resetCopyFeedback) window._resetCopyFeedback();
    _plainTextCopy = converted;
    _copyArtifactUnavailableReason = '';
    _lastCopiedHtml = '';
    _articleSession = null;
    _lastRenderInput = null;
    _sourceIsDocx = false;
    _articleBodyOnly = false;
    _isRepost = false;
    _articleHtml = '';
    _articleCopy = '';
    _footerHtml = '';
    _footerCopy = '';
    _failedSrcs = [];
    _copyRequiresWechatImageTransfer = false;
    _uploadDone = true;
    _uploadPromise = null;
    copySession.resetForNewArticle();
    _articleInputPending = false;
    clearQueuedInputRerender();
    _moreArticlesCopyJob = null;
    resetRetryUploadUI();
    clearUploadVisual();
    showImportEntry();
    progress.style.display = 'none';
    setShellProgress(0);
    dropZone.classList.remove('processing');

    const emptyState = document.getElementById('emptyState');
    const previewContent = document.getElementById('previewContent');
    const wxFooter = document.getElementById('wxFooter');
    if (emptyState) emptyState.style.display = 'none';
    if (previewContent) previewContent.style.display = '';
    if (wxFooter) wxFooter.style.display = 'none';
    document.body.classList.remove('article-text-preview');
    document.body.classList.add('has-preview', 'plain-text-preview');

    const titleInput = document.getElementById('titleInput');
    const titleDisplay = document.getElementById('previewTitleDisplay');
    const articleTitle = document.getElementById('articleTitle');
    if (titleInput) titleInput.value = '';
    if (titleDisplay) titleDisplay.textContent = '';
    if (articleTitle) articleTitle.textContent = '';

    const contentArea = document.getElementById('contentArea');
    if (contentArea) {
      contentArea.removeAttribute('style');
      contentArea.innerHTML =
        '<section class="plain-text-page" data-rule-label="' + escAttr(PLAIN_TEXT_RULE_LABEL) + '">' +
        '<div class="plain-text-page-output" contenteditable="plaintext-only" role="textbox" aria-multiline="true" spellcheck="false" tabindex="0" aria-label="处理后的文字">' +
        esc(converted) +
        '</div>' +
        '</section>';
    }
    const statsBar = document.getElementById('statsBar');
    if (statsBar) statsBar.textContent = '';
    const retryBtn = document.getElementById('retryBtn');
    if (retryBtn) retryBtn.style.display = 'none';
    const previewScroll = document.getElementById('previewScroll');
    if (previewScroll) previewScroll.scrollTop = 0;
    window._getArticleImageSrcs = function() { return []; };
    window._updateMoreArticles = async function() { return false; };
    dispatchArticleEvent('fbif:plain-text-ready', { text: converted, rule: PLAIN_TEXT_RULE_LABEL });
    return true;
  }

  window._handleText = (text, opts = {}) => {
    const raw = String(text || '');
    const body = normalizeClipboardTextForMarkdown(raw);
    if (!body) return;
    clearUploadVisual();
    if (!hasImageLikeLink(body)) {
      window._handleBlocks(buildClipboardArticleBlocks(raw), { ...opts, isRepost: false, bodyOnly: true });
      return;
    }
    const file = new File([body], 'clipboard.md', { type: 'text/markdown' });
    handleFile(file, { ...opts, isRepost: false, title: '', displayName: '剪贴板正文' });
  };

  // URL-import structured-blocks entry point. Used when the server responds
  // to /api/fetch-article with {title, blocks} (preferred path — preserves
  // visual signals). Falls back to _handleFile with a synthetic .md File
  // when the server returns {title, content} (Jina Reader fallback for
  // non-WeChat pages whose direct fetch has no body).
  window._handleBlocks = async (data, opts) => {
    // 新输入是文章级意图边界：解析、旧文章上传、更多文章、复制、重试等
    // 所有在途回写都必须同时失效，不能只挡解析结果。
    copySession.resetForNewArticle();
    clearQueuedInputRerender();
    const inputCommit = articleInputGate.capture();
    _articleInputPending = true;
    // Repost flag 局部捕获（同 handleFile）：footer 链路用局部，不回读全局。
    const isRepost = !!(opts && opts.isRepost);
    _isRepost = isRepost;
    _convLog.length = 0;
    clearUploadVisual();
    dispatchArticleEvent('fbif:article-input-started', {
      source: 'blocks',
      origin: opts && opts.source || '',
      title: data && data.title || '',
      isRepost,
    });
    logConv('info', '开始转化', { source: 'blocks', blocks: (data.blocks || []).length });
    errorEl.style.display = 'none';
    showImportEntry();
    dropZone.classList.add('processing');
    progress.style.display = 'block';
    setShellProgress(70);
    progressText.textContent = '正在排版...';
    try {
      const t0 = performance.now();
      if (typeof template.processBlocks !== 'function') {
        throw new Error('当前模板不支持 blocks 路径');
      }
      // await: processBlocks is synchronous today but we're inside an
      // async context anyway, and future work (e.g. image pre-fetch)
      // might make it async. The await has zero cost on plain returns.
      const bodyOnly = !!((opts && opts.bodyOnly) || (data && data.bodyOnly));
      // 粘贴纯文本只排正文、不生成页脚。规则服务还没确认时也必须立刻出预览，
      // 不能卡在 waitForActiveRuleConfigReady 上变成「框里有字、右侧空白」。
      if (!bodyOnly) await waitForActiveRuleConfigReady('blocks-upload');
      if (inputCommit.stale()) return false;
      const input = {
        kind: 'blocks',
        payload: data,
        isRepost,
        title: data.title,
        displayName: data.displayName || '',
        sourceIsDocx: false,
        bodyOnly,
        articleArtifactOverride: normalizeArticleArtifactOverride(opts && opts.articleArtifactOverride),
      };
      const result = await renderArticleInput(input);
      if (inputCommit.stale()) return false;
      logConv('info', '结构化解析完成', { paragraphs: result.lines.length, images: result.imgN });

      const session = await buildArticleSession(input, result, {
        resetArticleArtifacts: true,
        startedAt: t0,
        autofillAuthorCard: true,
        commitGate: inputCommit,
      });
      if (!session || inputCommit.stale()) return false;
      const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
      session.stats = formatRenderStats(result, elapsed);
      logConv('info', '排版完成', { elapsed: elapsed + 's', headings: result.headingN });

      setShellProgress(100);
      progressText.textContent = '排版完成!';
      dropZone.classList.remove('processing');
      setTimeout(() => {
        if (inputCommit.stale()) return;
        _articleInputPending = false;
        activateArticleSession(session);
      }, 200);
      return true;
    } catch (err) {
      if (inputCommit.stale()) return false;
      _articleInputPending = false;
      logConv('error', '排版失败', { error: err.message });
      dropZone.classList.remove('processing');
      progress.style.display = 'none';
      showError('排版失败: ' + sanitizeErrMsg(err.message));
      flushRerenderAfterInput();
      return false;
    }
  };

  // 规则在文件 / blocks 解析期间变更时，不能直接丢掉重排。
  // 先合并为一个待处理任务；当前输入激活（或失败）后立即消费。
  function queueRerenderAfterInput(options = {}) {
    _rerenderAfterInput = true;
    _rerenderAfterInputOptions = { ...(_rerenderAfterInputOptions || {}), ...options };
  }

  function clearQueuedInputRerender() {
    _rerenderAfterInput = false;
    _rerenderAfterInputOptions = null;
  }

  function flushRerenderAfterInput() {
    if (!_rerenderAfterInput || _articleInputPending) return false;
    const options = _rerenderAfterInputOptions || {};
    clearQueuedInputRerender();
    Promise.resolve(_engineRerender(options)).catch(err => console.error('[engine] queued rerender failed:', err));
    return true;
  }

  // Re-render the current article session with the latest _activeConfig without
  // re-parsing the source file. Safe to call before any file is loaded.
  async function _engineRerender(options = {}) {
    const detailedOutcome = options && options.__detailedOutcome === true;
    const outcome = (status, legacyValue, err) => detailedOutcome
      ? { status, ...(err ? { error: String(err && err.message || err) } : {}) }
      : legacyValue;
    if (_articleInputPending) {
      queueRerenderAfterInput(options);
      return outcome('deferred', false);
    }
    clearQueuedInputRerender();
    renderCommitGate.invalidate();
    const renderCommit = renderCommitGate.capture();
    const cached = (_articleSession && _articleSession.input) || _lastRenderInput;
    if (!cached) return outcome('noop', undefined);
    const articleArtifactOverride = Object.prototype.hasOwnProperty.call(options, 'articleArtifactOverride')
      ? normalizeArticleArtifactOverride(options.articleArtifactOverride)
      : cached.articleArtifactOverride;
    const articleFooterProfileOverride = Object.prototype.hasOwnProperty.call(options, 'articleFooterProfileOverride')
      ? normalizeArticleFooterProfileOverride(options.articleFooterProfileOverride)
      : cached.articleFooterProfileOverride;
    const hasInputOverrides = Object.prototype.hasOwnProperty.call(options, 'articleArtifactOverride')
      || Object.prototype.hasOwnProperty.call(options, 'articleFooterProfileOverride');
    const renderInput = hasInputOverrides
      ? { ...cached, articleArtifactOverride, articleFooterProfileOverride }
      : cached;
    // Use a local repost flag so we don't pollute the module-level _isRepost,
    // which handleFile / _handleBlocks set per upload and other code paths read.
    try {
      const t0 = performance.now();
      const result = await renderArticleInput(renderInput);
      if (renderCommit.stale()) return outcome('superseded', false);
      const session = await buildArticleSession(renderInput, result, {
        startedAt: t0,
        autofillAuthorCard: !!options.autofillAuthorCard,
        footerVisibilityOverride: Object.prototype.hasOwnProperty.call(options, 'footerVisibilityOverride')
          ? options.footerVisibilityOverride
          : renderInput.footerVisibilityOverride,
        articleArtifactOverride,
        articleFooterProfileOverride,
        commitGate: renderCommit,
      });
      if (!session || renderCommit.stale()) return outcome('superseded', false);
      const reusableImages = renderInput.kind === 'docx'
        ? collectReusableArticleImages(document.getElementById('contentArea'))
        : new Map();
      await refreshLocalPreviewImages(reusableImages);
      if (renderCommit.stale()) return outcome('superseded', false);
      activateArticleSession(session, { preparedImages: reusableImages });
      return outcome('committed', true);
    } catch (err) {
      if (renderCommit.stale()) return outcome('superseded', false);
      console.error('[engine] rerender failed:', err);
      return outcome('failed', false, err);
    }
  }

  // Make rerender callable from inline scripts (site.html / app.html sidebar)
  window._engineRerender = _engineRerender;
  window.addEventListener('fbif:rules-ready', event => {
    if (event && event.detail && event.detail.failed) return;
    if (_articleInputPending || (_articleSession && _articleSession.input) || _lastRenderInput) {
      _engineRerender({ autofillAuthorCard: true });
    }
  });
  function invalidatePendingArticleWork() {
    copySession.resetForNewArticle();
    if (_articlePreparationController) _articlePreparationController.abort();
    _articlePreparationController = null;
    _articleInputPending = false;
    clearQueuedInputRerender();
    _moreArticlesCopyJob = null;
    dropZone.classList.remove('processing');
    progress.style.display = 'none';
    setShellProgress(0);
    resetRetryUploadUI();
  }
  window._engineSwitchTemplate = async function(nextTemplate, options = {}) {
    if (!nextTemplate) return false;
    invalidatePendingArticleWork();
    template = nextTemplate;
    _activeTemplate = nextTemplate;
    applyTemplateShell(template);
    if (options.rerender !== false) await _engineRerender();
    return true;
  };
  async function setFooterVisibilityOverride(next) {
    const normalized = normalizeFooterVisibilityOverride(next);
    window._footerVisibilityOverride = normalized;
    if (!_articleSession) return refreshFooterPreviewFromVisibility();
    _articleSession.footerVisibilityOverride = normalized;
    _articleSession.input = { ...(_articleSession.input || {}), footerVisibilityOverride: normalized };
    _lastRenderInput = _articleSession.input;
    await _engineRerender({ footerVisibilityOverride: normalized });
    return true;
  }
  async function setArticleArtifactOverride(next) {
    const normalized = normalizeArticleArtifactOverride(next);
    window._articleArtifactOverride = normalized;
    if (!_articleSession) return false;
    _articleSession.articleArtifactOverride = normalized;
    _articleSession.input = { ...(_articleSession.input || {}), articleArtifactOverride: normalized };
    _lastRenderInput = _articleSession.input;
    await _engineRerender({ articleArtifactOverride: normalized });
    return true;
  }
  // 作者规则手选覆盖（undefined=自动 / 'none'=通用默认 / '<profile_id>'）。与
  // setArticleArtifactOverride 同款：写会话与全局后走版本门重渲染（红线 #25）。
  async function setArticleFooterProfileOverride(next) {
    const normalized = normalizeArticleFooterProfileOverride(next);
    window._articleFooterProfileOverride = normalized;
    if (!_articleSession) return false;
    _articleSession.articleFooterProfileOverride = normalized;
    _articleSession.input = { ...(_articleSession.input || {}), articleFooterProfileOverride: normalized };
    _lastRenderInput = _articleSession.input;
    await _engineRerender({ articleFooterProfileOverride: normalized });
    return true;
  }
  async function setArticleStrategy(isRepost) {
    const nextIsRepost = !!isRepost;
    _isRepost = nextIsRepost;
    if (!_articleSession && !_lastRenderInput) return false;
    if (_articleSession) {
      _articleSession.input = { ...(_articleSession.input || {}), isRepost: nextIsRepost };
      _lastRenderInput = _articleSession.input;
    } else if (_lastRenderInput) {
      _lastRenderInput = { ..._lastRenderInput, isRepost: nextIsRepost };
    }
    await _engineRerender();
    return true;
  }
  const setArticleRepost = setArticleStrategy;
  window._engineApi = {
    ...window._engineApi,
    setActiveConfig,
    getActiveConfig,
    rerender: (options) => _engineRerender(options),
    rerenderWithStatus: (options = {}) => _engineRerender({ ...options, __detailedOutcome: true }),
    switchTemplate: window._engineSwitchTemplate,
    setFooterVisibilityOverride,
    setArticleArtifactOverride,
    setArticleFooterProfileOverride,
    setArticleStrategy,
    setArticleRepost,
    invalidatePendingWork: invalidatePendingArticleWork,
  };

  async function handleFile(file, opts) {
    const importProgress = opts && opts.importProgress;
    const hasImportProgress = !!(
      importProgress
      && typeof importProgress.updatePhase === 'function'
      && typeof importProgress.completePhase === 'function'
    );
    copySession.resetForNewArticle();
    if (_articlePreparationController) _articlePreparationController.abort();
    const preparationController = typeof AbortController === 'function' ? new AbortController() : null;
    _articlePreparationController = preparationController;
    const externalSignal = opts && opts.signal;
    let externallyAborted = !!externalSignal?.aborted;
    const abortFromExternalIntent = () => {
      externallyAborted = true;
      preparationController?.abort();
    };
    externalSignal?.addEventListener?.('abort', abortFromExternalIntent, { once: true });
    if (externallyAborted) preparationController?.abort();
    clearQueuedInputRerender();
    const capturedInputCommit = articleInputGate.capture();
    // The Feishu chooser owns an outer latest-wins generation. Its signal and
    // stale callback must participate in every engine commit check, otherwise a
    // canceled @ import could still replace the preview after handoff.
    const inputCommit = {
      stale: () => (
        externallyAborted
        || !!opts?.isStale?.()
        || capturedInputCommit.stale()
      ),
    };
    _articleInputPending = true;
    // Repost flag 局部捕获：footer 选预设全程用此局部，绝不在 await 之后回读模块级 _isRepost
    // （并发导入交错会污染全局；codex re-review）。全局仍同步设置，供快照/renderBottomFooter 兜底。
    const isRepost = !!(opts && opts.isRepost);
    _isRepost = isRepost;
    _convLog.length = 0;
    dispatchArticleEvent('fbif:article-input-started', {
      source: 'file',
      origin: opts && opts.source || '',
      file: file && file.name || '',
      isRepost,
    });
    logConv('info', '开始转化', { file: file.name, size: (file.size / 1024).toFixed(1) + 'KB' });
    errorEl.style.display = 'none';
    showImportEntry();
    dropZone.classList.add('processing');
    if (!hasImportProgress) {
      progress.style.display = 'block';
      progress.classList?.remove('is-indeterminate');
      // 本地上传的进度权重按真实耗时分配：解析很快，图片准备才是大头。
      // 先前解析阶段直接冲到 97%，把主要等待压进最后 1.5%，导致出现
      // 「1/13 图 → 98%」这种自相矛盾的读数。
      const legacyPercent = opts && opts.source === 'feishu-doc-import' ? 20 : 8;
      setShellProgress(legacyPercent);
      progressText.textContent = '正在解析...';
    }
    const superseded = () => {
      importProgress?.cancel?.('superseded');
      return false;
    };
    // 本地上传的图片准备进度：把真实 done/total 写进进度条，占准备阶段
    // 98%→99.5% 这一小段，进度只前进、不倒退。
    // 图片准备（回填 40→75%）与像素确认（解码 75→95%）是导入的主要耗时，
    // 因此占进度条的主段；文案里的 done/total 与这里的百分比必须互相自洽。
    const LOCAL_PHASE_RANGE = { materialize: [40, 75], decode: [75, 95] };
    const reportLocalPreparationProgress = (text, done, total, phase = 'materialize') => {
      progressText.textContent = text;
      if (!(total > 0)) return;
      const [from, to] = LOCAL_PHASE_RANGE[phase] || LOCAL_PHASE_RANGE.materialize;
      setShellProgress(from + (Math.min(done, total) / total) * (to - from));
    };
    let preparedPreview = null;
    try {
      const t0 = performance.now();

      file = await normalizeToDocx(file, (pct, msg) => {
        if (inputCommit.stale()) return;
        if (!hasImportProgress) {
          progressText.textContent = msg;
          // .doc→.docx 转换自带 0–100 的进度，映射进总进度的 8→30 段，
          // 不能直传，否则转换一结束进度条就冲到满格再退回去。
          setShellProgress(8 + Math.max(0, Math.min(100, Number(pct) || 0)) * 0.22);
        }
      });
      if (inputCommit.stale()) return superseded();
      importProgress?.updatePhase?.('rules', 0, 1, { status: '正在确认排版规则…' });
      await waitForActiveRuleConfigReady('single-upload');
      if (inputCommit.stale()) return superseded();
      importProgress?.completePhase?.('rules', { status: '排版规则已确认' });
      if (!hasImportProgress) {
        setShellProgress(35);
        progressText.textContent = '正在解析...';
      } else {
        importProgress.updatePhase('parse', 0, 6, { status: '正在打开 Word 文档…' });
      }

      const input = await parseFileToArticleInput(file, isRepost, opts);
      if (inputCommit.stale()) return superseded();
      importProgress?.completePhase?.('parse', { status: 'Word 正文解析完成' });
      importProgress?.updatePhase?.('render', 0, 1, { status: '正在生成排版结构…' });
      const result = await renderArticleInput(input);
      if (inputCommit.stale()) return superseded();
      importProgress?.completePhase?.('render', { status: '排版结构已生成' });
      const articleHtml = result.lines.join('\n');
      const isFeishuDocument = opts && opts.source === 'feishu-doc-import';
      // 服务端剥图的 DOCX（飞书导出未走 OSS 镜像时、以及 .doc→docx 转换）正文里是
      // /api/doc-cache/* 占位。这类正文若直接进 DOM，浏览器会边下边画：先空洞、再
      // 半张图。必须先把占位回填成位图、在离屏 staging 里解码完，才整帧上屏。
      const hasDeferredPreviewImages = /\/api\/doc-cache\//.test(articleHtml);
      const needsAtomicPreview = isFeishuDocument || hasDeferredPreviewImages;
      let preparedClipboardArtifact = null;
      let requiresWechatTransfer = false;
      const preparedImagesPromise = input.kind === 'docx' && !needsAtomicPreview
        ? preloadLocalPreviewImages(input.payload && input.payload.imgCache, { articleHtml })
        : Promise.resolve(new Map());
      if (input.kind === 'docx') {
        logConv('info', 'DOCX解析完成', { paragraphs: result.lines.length, images: result.imgN });
        if (!hasImportProgress) {
          setShellProgress(40);
          progressText.textContent = '正在准备图片...';
        }
      } else if (input.kind === 'md') {
        logConv('info', 'Markdown解析完成', { paragraphs: result.lines.length, images: result.imgN });
      }

      // New input discards the prior article's per-article edits and restores
      // this rule's recommendation list.
      importProgress?.updatePhase?.('session', 0, 1, { status: '正在组装文章与底部模块…' });
      const session = await buildArticleSession(input, result, {
        resetArticleArtifacts: true,
        startedAt: t0,
        autofillAuthorCard: true,
        commitGate: inputCommit,
      });
      if (!session || inputCommit.stale()) return superseded();
      importProgress?.completePhase?.('session', { status: '文章结构已组装' });
      let preparedImages = await preparedImagesPromise;
      if (inputCommit.stale()) return superseded();
      if (needsAtomicPreview) {
        importProgress?.updatePhase?.('theme', 0, 1, { status: '正在准备立即复制所需样式…' });
        await _themeCSSReady;
        if (inputCommit.stale()) return superseded();
        importProgress?.completePhase?.('theme', { status: '复制样式已准备' });
        const ready = await prepareFeishuArticlePreview(session.articleHtml, {
          footerHtml: session.footerCopy,
          previewFooterHtml: isFooterEnabledForSession(session) ? session.footerHtml : '',
          materialize: materializeLazyImages,
          isStale: () => inputCommit.stale(),
          signal: preparationController && preparationController.signal,
          onProgress({ phase, done, total }) {
            if (inputCommit.stale()) return;
            const status = phase === 'materialize'
              ? '正在准备正文图片'
              : '正在准备右侧排版';
            const text = total > 0 ? `${status} ${done}/${total}…` : `${status}…`;
            importProgress?.updatePhase?.(phase, done, total, { status: text });
            // 本地上传没有飞书那套加权 tracker，但真实完成数同样要报给用户，
            // 不能在这里干等着只显示一句「正在准备图片...」。
            if (!hasImportProgress) reportLocalPreparationProgress(text, done, total);
          },
        });
        if (ready.stale || inputCommit.stale()) return superseded();
        session.articleHtml = ready.articleHtml;
        session.footerHtml = ready.previewFooterHtml;
        session.articleCopy = ready.articleHtml;
        session.footerCopy = ready.footerCopy;
        // Create the final image nodes while they are already connected to an
        // invisible staging root. This gives Chromium one real request per
        // occurrence and keeps the previous article visible throughout.
        session.articleHtml = normalizeRich(session.articleHtml);
        session.footerHtml = normalizeRich(session.footerHtml);
        preparedPreview = prepareConnectedPreviewContent(
          session.articleHtml,
          isFooterEnabledForSession(session) ? session.footerHtml : '',
        );
        preparedImages = indexPreparedPreviewImages(preparedPreview);
        importProgress?.completePhase?.('materialize', { status: '正文图片已准备' });
        const stagedImageRefs = preparedPreview.images
          .map(image => image.getAttribute('src') || '')
          .filter(Boolean);
        const stagedDecode = await refreshLocalPreviewImages(preparedImages, {
          concurrency: 6,
          refs: stagedImageRefs,
          report: true,
          signal: preparationController && preparationController.signal,
          onProgress(done, total) {
            if (inputCommit.stale()) return;
            const text = total > 0
              ? `正在确认图片像素 ${done}/${total}…`
              : '正在确认图片像素…';
            importProgress?.updatePhase?.('decode', done, total, { status: text });
            if (!hasImportProgress) reportLocalPreparationProgress(text, done, total, 'decode');
          },
        });
        if (inputCommit.stale()) return superseded();
        if (stagedDecode.failedSrcs.length > 0) {
          throw new Error(`飞书文档有 ${stagedDecode.failedSrcs.length} 张图片无法显示，已保留当前预览，请重试`);
        }
        preparedImages = stagedDecode.images;
        const stagedValidation = validatePreparedPreview(preparedPreview);
        if (stagedValidation.ready !== stagedValidation.total) {
          throw new Error(`飞书文档有 ${stagedValidation.total - stagedValidation.ready} 张图片尚未准备完成，已保留当前预览，请重试`);
        }
        importProgress?.completePhase?.('decode', { status: '显示图片像素已确认' });
        if (ready.replacements.size > 0 && session.input?.payload?.imgCache) {
          const imgCache = Object.fromEntries(
            Object.entries(session.input.payload.imgCache).map(([key, value]) => [
              key,
              ready.replacements.get(value) || value,
            ]),
          );
          session.input = {
            ...session.input,
            payload: { ...session.input.payload, imgCache },
          };
        }
        importProgress?.updatePhase?.('copy-transfer', 0, 0, {
          status: '正在准备可立即复制的图片版本…',
        });
        // 复制层同步准备只给飞书导入：它导入后往往立刻复制进公众号后台。
        // .doc 转换走后台 _uploadPromise 回填 + 复制按钮挡回（规则 9），
        // 不在导入时同步等上传，避免把 .doc 导入拖慢。
        const needsCopyTransfer = isFeishuDocument && !(window._skipUpload && input.sourceIsDocx);
        if (needsCopyTransfer) {
          const transferMmbiz = template.transferMmbiz != null
            ? !!template.transferMmbiz
            : template.id === 'foodtalks-site';
          requiresWechatTransfer = transferMmbiz;
          const copyReady = await uploadNonCdnImages(session.articleCopy, session.footerCopy, {
            transferMmbiz,
            signal: preparationController && preparationController.signal,
            onProgress(done, total) {
              if (inputCommit.stale()) return;
              importProgress?.updatePhase?.('copy-transfer', done, total, {
                status: total > 0
                  ? `正在准备可立即复制的图片版本 ${done}/${total}…`
                  : '正在准备可立即复制的图片版本…',
              });
            },
            onLog(...args) {
              if (!inputCommit.stale()) logConv(...args);
            },
          });
          if (inputCommit.stale()) return superseded();
          if (copyReady.failed > 0) {
            // 公众号允许 bitmap data: 作为立即可复制的安全降级；OSS 增强失败
            // 不能反过来阻断导入。网站残留 mmbiz、blob/本地引用等不安全形态
            // 会由 prepareClipboardArtifact 的最终产物检查统一 fail closed。
            logConv('warn', '部分图片未完成可选 CDN 增强，继续做最终复制安全校验', {
              failed: copyReady.failed,
              transferMmbiz,
            });
          }
          session.articleCopy = copyReady.articleCopy;
          session.footerCopy = copyReady.footerCopy;
        }
        session.copyPrepared = true;
        importProgress?.completePhase?.('copy-transfer', { status: '可立即复制的图片版本已准备' });
        session.articleCopy = normalizeRich(session.articleCopy);
        session.footerCopy = normalizeRich(session.footerCopy);

        const finalizedPreview = await finalizeFeishuArticlePreview({
          ...ready,
          articleHtml: session.articleHtml,
          previewFooterHtml: session.footerHtml,
          footerCopy: session.footerCopy,
          preparedImages,
        }, {
          refresh: refreshLocalPreviewImages,
          isStale: () => inputCommit.stale(),
          signal: preparationController && preparationController.signal,
          onProgress({ done, total }) {
            if (inputCommit.stale()) return;
            importProgress?.updatePhase?.('refresh', done, total, {
              status: total > 0
                ? `正在做显示前的最后图片校验 ${done}/${total}…`
                : '正在做显示前的最后图片校验…',
            });
          }
        });
        if (finalizedPreview.stale || inputCommit.stale()) return superseded();
        preparedImages = finalizedPreview.preparedImages;
        importProgress?.completePhase?.('refresh', { status: '全部显示图片最终校验完成' });

        const previewValidation = validatePreparedPreview(preparedPreview);
        if (previewValidation.ready !== previewValidation.total) {
          throw new Error(`仍有 ${previewValidation.total - previewValidation.ready} 张图片尚未显示，已保留当前预览，请重试`);
        }
        preparedClipboardArtifact = await prepareClipboardArtifact(
          session.articleCopy,
          session.footerCopy,
          {
            includeFooter: isFooterEnabledForSession(session),
            requiresWechatTransfer,
            signal: preparationController && preparationController.signal,
          },
        );
        if (inputCommit.stale()) return superseded();
      } else {
        await refreshLocalPreviewImages(preparedImages);
      }
      if (inputCommit.stale()) return superseded();
      const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
      session.stats = formatRenderStats(result, elapsed);
      logConv('info', '排版完成', { elapsed: elapsed + 's', headings: result.headingN });

      // Drop the processing state so the upload zone is clickable again for
      // the next file. (Previously only the error branch cleared it, so a
      // successful upload left pointer-events:none on the drop zone.)
      dropZone.classList.remove('processing');
      const activationDelayMs = Number.isFinite(Number(opts && opts.activationDelayMs))
        ? Math.max(0, Number(opts.activationDelayMs))
        : 0;
      const activate = () => {
        if (inputCommit.stale()) return;
        _articleInputPending = false;
        activateArticleSession(session, {
          preparedImages,
          preparedPreview,
          preparedClipboardArtifact,
          keepProgressVisible: hasImportProgress,
          copyPrepared: !!session.copyPrepared,
          articleCopy: session.articleCopy,
          requiresWechatTransfer,
        });
      };
      if (hasImportProgress) {
        activate();
        if (inputCommit.stale()) return superseded();
        const sourceArtifact = _articleCopy + '\n' + (isFooterEnabledForCurrentArticle() ? _footerCopy : '');
        if (!matchingPreparedClipboardArtifact(sourceArtifact)) {
          throw new Error('复制内容尚未完成最终确认，已停止完成进度，请重试');
        }
        importProgress.completePhase('preview-commit', { status: '右侧排版已显示' });
        importProgress.completePhase('copy-commit', { status: '复制内容已准备' });
        if (!importProgress.commit({ status: '排版完成，可以立即复制' })) {
          throw new Error('总进度校验未通过，请重试');
        }
        const committedTracker = importProgress;
        setTimeout(() => {
          if (
            committedTracker.snapshot?.().committed
            && progress.getAttribute('aria-valuenow') === '100'
          ) {
            progress.style.display = 'none';
            progress.dataset.totalProgress = 'false';
          }
        }, 700);
      } else {
        setShellProgress(100);
        progressText.textContent = '排版完成!';
        if (activationDelayMs > 0) setTimeout(activate, activationDelayMs);
        else activate();
      }
      return true;
    } catch (err) {
      if (inputCommit.stale()) return false;
      importProgress?.cancel?.('failed');
      _articleInputPending = false;
      logConv('error', '排版失败', { error: err.message });
      dropZone.classList.remove('processing');
      progress.style.display = 'none';
      showError('排版失败: ' + sanitizeErrMsg(err.message));
      flushRerenderAfterInput();
      return false;
    } finally {
      discardPreparedPreviewContent(preparedPreview);
      externalSignal?.removeEventListener?.('abort', abortFromExternalIntent);
      const stillOwnsPreparation = _articlePreparationController === preparationController;
      if (stillOwnsPreparation && inputCommit.stale()) {
        _articleInputPending = false;
        dropZone.classList.remove('processing');
        flushRerenderAfterInput();
      }
      if (stillOwnsPreparation) {
        _articlePreparationController = null;
      }
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
  let _articleBodyOnly = false;
  let _copyRequiresWechatImageTransfer = false;
  let _moreArticlesCopyJob = null;
  // 复制层竞态守卫（架构体检 S2：旧 _footerCopyGen/_moreArticlesCopySeq 两个裸计数器
  // 收敛为版本门，语义不变——footerCopyGate = _footerCopy 写权版本（慢速旧上传只有在
  // 版本未变时才允许回写）；moreArticlesGate = 更多文章合成任务版本。契约与「文章级
  // 重置必须全部作废」由 copy-session.js + tests/copy-session.test.mjs 守护）。
  const copySession = createCopySession();
  const {
    articleInputGate,
    renderCommitGate,
    articleResultGate,
    footerCopyGate,
    moreArticlesGate,
    moreArticlesPreviewGate,
    copyActionGate,
    retryGate,
  } = copySession;
  let _articleInputPending = false;
  let _articlePreparationController = null;
  let _rerenderAfterInput = false;
  let _rerenderAfterInputOptions = null;
  const PLAIN_TEXT_RULE_LABEL = '中文标点规则';
  let _plainTextCopy = null;
  let _lastCopiedHtml = '';
  let _copyArtifactUnavailableReason = '';
  let _preparedClipboardArtifact = null;
  window._copyInProgress = false;

  function findWechatImageSrcs(html) {
    const urls = [];
    const re = /<img\b[^>]*\bsrc=(["'])(https?:\/\/[^"']+)\1/gi;
    let m;
    while ((m = re.exec(String(html || ''))) !== null) {
      if (isMmbizUrl(m[2])) urls.push(m[2]);
    }
    return urls;
  }

  function isUnsafeCopyImageRef(value, attr, requiresWechatTransfer) {
    const src = String(value || '').trim();
    if (!src) return false;
    if (/^data:/i.test(src)) {
      // 公众号渠道 <img src> 里的 base64 位图是安全形态：微信编辑器粘贴时会
      // 立即本地转存（DOCX 正文图长期依赖的契约）。svg 微信不转存；imgurl
      // 是图片链接卡片的服务端校验属性、粘贴时不会被改写——这两类留在剪贴
      // 板都会在保存草稿时丢内容，必须继续判为不安全。
      if (attr === 'src' && !requiresWechatTransfer
        && /^data:image\/(?:png|jpe?g|gif|webp)[;,]/i.test(src)) return false;
      return true;
    }
    if (/^blob:/i.test(src)) return true;
    if (/^\/(?:assets\/|api\/doc-cache\/|api\/image-proxy(?:\?|$))/i.test(src)) return true;
    if (/^https?:\/\/(?:localhost|127\.0\.0\.1|\[::1\])(?::|\/|$)/i.test(src)) return true;
    return false;
  }

  function findUnsafeCopyImageRefs(html, requiresWechatTransfer) {
    const refs = [];
    const seen = new Set();
    const re = /\b(src|imgurl)=(["'])([^"']+)\2/gi;
    let m;
    while ((m = re.exec(String(html || ''))) !== null) {
      const attr = m[1].toLowerCase();
      const ref = m[3];
      if (!isUnsafeCopyImageRef(ref, attr, requiresWechatTransfer) || seen.has(ref)) continue;
      seen.add(ref);
      refs.push(ref);
    }
    return refs;
  }

  function filterFailedImageRefsInHtml(failedSrcs, html) {
    const present = new Set();
    const attrRe = /\b(?:src|imgurl)=(["'])([^"']+)\1/gi;
    let match;
    while ((match = attrRe.exec(String(html || ''))) !== null) present.add(match[2]);
    const refs = [];
    const seen = new Set();
    for (const value of Array.isArray(failedSrcs) ? failedSrcs : []) {
      const ref = String(value || '').trim();
      if (!ref || seen.has(ref) || !present.has(ref)) continue;
      seen.add(ref);
      refs.push(ref);
    }
    return refs;
  }

  function reconcileFailedImageRefs(failedSrcs = _failedSrcs) {
    const includeFooter = !_articleBodyOnly && isFooterEnabledForCurrentArticle();
    const currentCopyHtml = _articleCopy + '\n' + (includeFooter ? _footerCopy : '');
    _failedSrcs = filterFailedImageRefsInHtml(failedSrcs, currentCopyHtml);
    return _failedSrcs;
  }

  function markFailedImages(failedSrcs) {
    const contentArea = document.getElementById('contentArea');
    if (!contentArea) return;
    const refs = Array.isArray(failedSrcs) ? failedSrcs : [];
    const imgs = contentArea.querySelectorAll('img');
    imgs.forEach(img => {
      const src = img.getAttribute('src') || '';
      if (refs.some(fs => src.includes(fs.substring(0, 60)))) {
        img.style.outline = '3px solid #ff4d4f';
        img.style.outlineOffset = '-3px';
        img.title = '上传失败 — 点击"重试上传"';
      } else {
        img.style.outline = '';
        img.title = '';
      }
    });
  }

  function resetRetryUploadUI() {
    const retryBtn = document.getElementById('retryBtn');
    if (!retryBtn) return;
    retryBtn.textContent = '重试上传';
    retryBtn.disabled = false;
    retryBtn.style.display = 'none';
  }

  function cardsNeedComposite(cards) {
    return cards.some(c => (c.title && c.title.trim()) || c.cover_data_url);
  }

  function copySafeFooterAfterImagePreparation(footerCopy) {
    if (isWebsiteTemplate()) return footerCopy;
    const cards = loadMoreArticlesCards();
    if (!cards || cards.length === 0) return footerCopy;
    const copySafeCards = cards.map(copySafeMoreArticleCard);
    return normalizeWechatRichHtml(mergeMoreArticlesIntoFooter(footerCopy, copySafeCards));
  }

  // 新文章导入：把「更多文章」卡片重置为规则默认列表 + 作废上一篇在途的预热 job——两步必须原子。
  // 只清卡片不作废 job，旧 job 会在卡片重置之后、showPreview 之前的窗口（两个 await + setTimeout）
  // 里 resolve，此时 seq 未变 → 通过 742 校验 → saveMoreArticlesCards 把上一篇卡片写回存储，
  // 下一篇 loadMoreArticlesCards 读到旧卡片（跨文章污染；codex re-review #3）。
  function resetMoreArticlesForNewArticle() {
    saveMoreArticlesCards(makeFreshUploadCards());
    moreArticlesGate.invalidate();
    _moreArticlesCopyJob = null;
    _preparedClipboardArtifact = null;
    if (window._refreshMoreArticlesSidebar) window._refreshMoreArticlesSidebar();
  }

  function prepareMoreArticlesForCopy(reason) {
    if (isWebsiteTemplate()) return Promise.resolve(false);
    if (_moreArticlesCopyJob) return _moreArticlesCopyJob;
    const cards = loadMoreArticlesCards();
    if (!cardsNeedComposite(cards)) return Promise.resolve(false);
    moreArticlesGate.invalidate();
    const gate = moreArticlesGate.capture();
    _moreArticlesCopyJob = ensureCompositeReady(cards).then(async uploaded => {
      if (gate.stale()) return false;
      saveMoreArticlesCards(uploaded);
      const rp = _lastRenderInput && _lastRenderInput.isRepost != null ? _lastRenderInput.isRepost : _isRepost;
      const { html: base0, isV2 } = await loadBaseFooterHtml({
        isRepost: rp,
        articleMeta: _articleSession && _articleSession.articleMeta,
        bottomBlocksOverride: _articleSession && _articleSession.bottomBlocksOverride,
        footerVisibilityOverride: _articleSession && _articleSession.footerVisibilityOverride,
      });
      const baseFooter = normalizeWechatRichHtml(isV2 ? base0 : applyRepostTransform(base0, rp));
      if (gate.stale()) return false;
      const copySafeCards = uploaded.map(copySafeMoreArticleCard);
      const mergedFooter = normalizeWechatRichHtml(mergeMoreArticlesIntoFooter(baseFooter, copySafeCards));
      if (gate.stale()) return false;
      // Rebuilding from the base footer reintroduces same-origin assets such as
      // the Bobo card. Materialize them again before this path becomes the
      // authoritative copy footer; bitmap data remains immediately safe even
      // when the optional OSS enhancement is unavailable.
      const materializedFooter = await materializeLazyImages('', mergedFooter, {
        onLog(...args) {
          if (!gate.stale()) logConv(...args);
        },
      });
      if (gate.stale()) return false;
      const pendingFailedRefs = (_failedSrcs || []).concat(materializedFooter.failedSrcs || []);
      _footerCopy = materializedFooter.footerCopy;
      _preparedClipboardArtifact = null;
      footerCopyGate.invalidate();
      reconcileFailedImageRefs(pendingFailedRefs);
      if (!_failedSrcs.length) {
        resetRetryUploadUI();
        markFailedImages([]);
      }
      logConv('info', '更多文章图片已后台准备完成', { reason });
      return true;
    }).catch(err => {
      if (!gate.stale()) logConv('warn', '更多文章后台准备失败，继续使用当前预览版', { error: err.message, reason });
      return false;
    }).finally(() => {
      if (!gate.stale()) _moreArticlesCopyJob = null;
    });
    return _moreArticlesCopyJob;
  }

  function showPreview(title, articleContent, footerContent, stats, importTitle = title, opts = {}) {
    const bodyOnly = !!(opts && opts.bodyOnly);
    // 渲染新文章 → 复位上一篇的复制状态（按钮变身 + 后台入口），避免显示陈旧的「✓已复制」。
    if (window._resetCopyFeedback) window._resetCopyFeedback();
    _plainTextCopy = null;
    _copyArtifactUnavailableReason = '';
    document.body.classList.remove('plain-text-preview');
    document.body.classList.toggle('article-text-preview', bodyOnly);
    _articleHtml = normalizeRich(articleContent);
    _articleCopy = normalizeRich((opts && opts.articleCopy) || _articleHtml);
    _footerHtml = bodyOnly ? '' : normalizeRich(footerContent);
    _footerCopy = bodyOnly ? '' : normalizeRich((opts && opts.footerCopy) || footerContent);
    _preparedClipboardArtifact = opts?.preparedClipboardArtifact || null;
    _failedSrcs = [];
    _copyRequiresWechatImageTransfer = !!opts?.requiresWechatTransfer;
    // 文章级重置：作废全部在途异步回写（footer 写权 + 更多文章预热 job）。
    // 新文章导入路径已在更早的 resetMoreArticlesForNewArticle() 原子作废（codex re-review #3）；
    // 此处再作废对新稿无害、对 rerender 必要（同篇换配置时丢弃旧预热并重建）。
    copySession.resetForPreviewCommit();
    _moreArticlesCopyJob = null;

    // Split layout: show preview content, hide empty state
    const emptyState = document.getElementById('emptyState');
    const previewContent = document.getElementById('previewContent');
    const wxFooter = document.getElementById('wxFooter');
    if (emptyState) emptyState.style.display = 'none';
    if (previewContent) previewContent.style.display = '';
    if (wxFooter) wxFooter.style.display = bodyOnly ? 'none' : '';
    document.body.classList.add('has-preview');

    document.getElementById('titleInput').value = bodyOnly ? '' : title;
    document.getElementById('previewTitleDisplay').textContent = bodyOnly ? '' : title;

    // Apply theme CSS variables to contentArea for preview
    const contentArea = document.getElementById('contentArea');
    if (window._activeConfig) applyThemeVars(contentArea, window._activeConfig.config);
    const renderedImages = opts?.preparedPreview
      ? commitPreparedPreviewContent(contentArea, opts.preparedPreview)
      : renderPreviewContent(
        contentArea,
        _articleHtml,
        bodyOnly ? '' : _footerHtml,
        { preparedImages: opts && opts.preparedImages },
      );
    hookPreviewImages(renderedImages);
    showImportSummary(importTitle, stats, {
      keepProgressVisible: !!(opts && opts.keepProgressVisible),
    });
    document.getElementById('statsBar').textContent = settledSidebarStatus(stats);

    const retryBtn = document.getElementById('retryBtn');
    resetRetryUploadUI();

    window._updateFooter = function(newHtml) {
      if (bodyOnly) {
        renderPreviewFooter(contentArea, '');
        return;
      }
      if (typeof newHtml === 'string' && newHtml !== '') {
        _footerHtml = normalizeRich(newHtml);
        _footerCopy = _footerHtml;
        _preparedClipboardArtifact = null;
        footerCopyGate.invalidate();
        if (_articleSession) _articleSession.footerHtml = _footerHtml;
        // 预览层可能含 data: 合成图 / 本地素材引用；立即重建复制安全层，
        // 不让预览形态直接留在剪贴板层（保存草稿消失回归的旁路之一）。
        moreArticlesGate.invalidate();
        _moreArticlesCopyJob = null;
        prepareMoreArticlesForCopy('footer-edit');
      }
      const enabled = isFooterEnabledForCurrentArticle();
      renderPreviewFooter(contentArea, enabled ? _footerHtml : '');
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
      if (bodyOnly) return false;
      if (cardsOverride) saveMoreArticlesCards(cardsOverride);
      moreArticlesPreviewGate.invalidate();
      const previewGate = moreArticlesPreviewGate.capture();
      moreArticlesGate.invalidate();
      _moreArticlesCopyJob = null;
      const rp = _lastRenderInput && _lastRenderInput.isRepost != null ? _lastRenderInput.isRepost : _isRepost;
      const { html: base, isV2 } = await loadBaseFooterHtml({
        isRepost: rp,
        articleMeta: _articleSession && _articleSession.articleMeta,
        bottomBlocksOverride: _articleSession && _articleSession.bottomBlocksOverride,
        footerVisibilityOverride: _articleSession && _articleSession.footerVisibilityOverride,
      });
      const merged = await buildFooterWithMoreArticles(base, { isRepost: rp, isV2 });
      if (previewGate.stale()) return false;
      const copySafeMerged = await buildFooterWithMoreArticles(base, {
        isRepost: rp,
        isV2,
        copySafe: true,
        canCommitCards: () => !previewGate.stale(),
      });
      if (previewGate.stale()) return false;
      _footerHtml = merged;
      _footerCopy = copySafeMerged;
      _preparedClipboardArtifact = null;
      footerCopyGate.invalidate();
      if (_articleSession) {
        _articleSession.footerHtml = merged;
        _articleSession.footerCopy = copySafeMerged;
      }
      const enabled = isFooterEnabledForCurrentArticle();
      renderPreviewFooter(contentArea, enabled ? _footerHtml : '');
      markFailedImages(_failedSrcs);
      prepareMoreArticlesForCopy('edit');
      return true;
    };

    dispatchArticleEvent(bodyOnly ? 'fbif:article-text-ready' : 'fbif:article-previewed', { title, stats });

    // Background: upload non-mmbiz images to WeChat CDN for the copy layer
    _uploadDone = false;
    _uploadPromise = null;
    const statsBar = document.getElementById('statsBar');
    const stableStats = settledSidebarStatus(stats);
    const articleResult = articleResultGate.capture();

    const skipUploadForThisFile = !!window._skipUpload && _sourceIsDocx;
    if (bodyOnly) {
      _uploadDone = true;
      statsBar.textContent = settledSidebarStatus(stats);
      return;
    }
    if (opts && opts.copyPrepared) {
      _uploadDone = true;
      statsBar.textContent = stableStats;
      return;
    }
    if (skipUploadForThisFile) {
      const footerWriteGate = footerCopyGate.capture();
      _uploadPromise = materializeLazyImages(_articleCopy, _footerCopy, {
        onProgress(done, total) {
          if (articleResult.stale()) return;
          if (total > 0) statsBar.textContent = '准备图片 (' + done + '/' + total + ')...';
        },
        onLog(...args) {
          if (!articleResult.stale()) logConv(...args);
        },
      }).then(result => {
        if (articleResult.stale()) return false;
        _articleCopy = result.articleCopy;
        if (!footerWriteGate.stale()) _footerCopy = copySafeFooterAfterImagePreparation(result.footerCopy);
        reconcileFailedImageRefs(result.failedSrcs || []);
        _uploadDone = true;
        const failedCount = _failedSrcs.length;
        if (failedCount > 0) {
          statsBar.textContent = uploadStatus(stableStats, failedCount + '张图片准备失败');
          markFailedImages(_failedSrcs);
        } else {
          statsBar.textContent = stableStats;
        }
        return true;
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
    const footerWriteGate = footerCopyGate.capture();
    _uploadPromise = uploadNonCdnImages(_articleCopy, _footerCopy, {
      transferMmbiz,
      onProgress(done, total) {
        if (articleResult.stale()) return;
        statsBar.textContent = (transferMmbiz ? '转存图片 (' : '上传图片 (') + done + '/' + total + ')...';
      },
      onLog(...args) {
        if (!articleResult.stale()) logConv(...args);
      },
      onMaterialized(result) {
        if (articleResult.stale()) return;
        _articleCopy = result.articleCopy;
        if (!footerWriteGate.stale()) _footerCopy = copySafeFooterAfterImagePreparation(result.footerCopy);
        reconcileFailedImageRefs(result.failedSrcs || []);
      },
    }).then(result => {
      if (articleResult.stale()) return false;
      _articleCopy = result.articleCopy;
      if (!footerWriteGate.stale()) _footerCopy = copySafeFooterAfterImagePreparation(result.footerCopy);
      reconcileFailedImageRefs(result.failedSrcs || []);
      _uploadDone = true;
      const failedCount = _failedSrcs.length;
      if (failedCount > 0) {
        statsBar.textContent = uploadStatus(stableStats, failedCount + '张图片上传失败');
        markFailedImages(_failedSrcs);
        if (retryBtn) retryBtn.style.display = 'inline-block';
      } else {
        statsBar.textContent = hasDocumentSummaryCard() ? '' : uploadStatus(stableStats, '全部图片已就绪');
      }
      return true;
    });
    prepareMoreArticlesForCopy('preview');
  }

  window.retryUpload = async function() {
    reconcileFailedImageRefs();
    if (!_failedSrcs.length) {
      resetRetryUploadUI();
      return true;
    }
    retryGate.invalidate();
    const retryCommit = retryGate.capture();
    const retryBtn = document.getElementById('retryBtn');
    const statsBar = document.getElementById('statsBar');
    if (retryBtn) { retryBtn.textContent = '重试中...'; retryBtn.disabled = true; }

    const articleResult = articleResultGate.capture();
    const footerWriteGate = footerCopyGate.capture();
    try {
      const result = await retryFailedImages(_articleCopy, _footerCopy, _failedSrcs, {
        onProgress(done, total) {
          if (articleResult.stale() || retryCommit.stale()) return;
          statsBar.textContent = '重试上传 (' + done + '/' + total + ')...';
        },
        onLog(...args) {
          if (!articleResult.stale() && !retryCommit.stale()) logConv(...args);
        },
      });
      if (articleResult.stale() || retryCommit.stale()) return false;
      _articleCopy = result.articleCopy;
      if (!footerWriteGate.stale()) _footerCopy = copySafeFooterAfterImagePreparation(result.footerCopy);
      reconcileFailedImageRefs(result.failedSrcs);

      const failedCount = _failedSrcs.length;
      if (failedCount > 0) {
        statsBar.textContent = failedCount + '张图片仍然失败';
        markFailedImages(_failedSrcs);
      } else {
        statsBar.textContent = hasDocumentSummaryCard() ? '' : '全部图片已就绪';
        markFailedImages([]);
        if (retryBtn) retryBtn.style.display = 'none';
      }
      return true;
    } catch (err) {
      if (articleResult.stale() || retryCommit.stale()) return false;
      statsBar.textContent = '重试上传失败';
      logConv('error', '重试上传失败', { error: err.message });
      return false;
    } finally {
      if (!articleResult.stale() && !retryCommit.stale() && retryBtn) {
        retryBtn.textContent = '重试上传';
        retryBtn.disabled = false;
      }
    }
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
  function copyButtonLabel(btn) {
    if (!btn) return '复制全文';
    const label = typeof btn.querySelector === 'function' ? btn.querySelector('[data-copy-label]') : null;
    return (label ? label.textContent : btn.textContent) || '复制全文';
  }

  function setCopyButtonLabel(btn, text) {
    if (!btn) return;
    const label = typeof btn.querySelector === 'function' ? btn.querySelector('[data-copy-label]') : null;
    if (label) label.textContent = text;
    else btn.textContent = text;
  }

  function setCopyButtonCopied(btn, copied) {
    if (!btn) return;
    btn.classList.toggle('copied', !!copied);
    const group = typeof btn.closest === 'function' ? btn.closest('.copy-split') : null;
    if (group) group.classList.toggle('is-copied', !!copied);
    if (!copied) setCopyButtonWarning(btn, false);
  }

  function setCopyButtonWarning(btn, warning) {
    if (!btn) return;
    btn.classList.toggle('is-warning', !!warning);
    const group = typeof btn.closest === 'function' ? btn.closest('.copy-split') : null;
    if (group) group.classList.toggle('is-warning', !!warning);
  }

  function copyArtifactFingerprint(html) {
    const source = String(html || '');
    let hash = 0x811c9dc5;
    for (let i = 0; i < source.length; i++) {
      hash ^= source.charCodeAt(i);
      hash = Math.imul(hash, 0x01000193);
    }
    return (hash >>> 0).toString(16).padStart(8, '0');
  }

  function copyPreparationFingerprint(sourceArtifact, config) {
    return copyArtifactFingerprint([
      sourceArtifact,
      JSON.stringify(config || {}),
      isWebsiteTemplate() ? 'website' : 'wechat',
    ].join('\n'));
  }

  async function prepareClipboardArtifact(articleCopy, footerCopy, {
    includeFooter,
    requiresWechatTransfer,
    signal,
  } = {}) {
    if (!copyBodyHasMeaningfulContent(articleCopy)) {
      throw new Error('当前正文复制产物为空，已保留当前预览，请检查飞书文档后重试');
    }
    if (signal?.aborted) throw signal.reason || new DOMException('The operation was aborted', 'AbortError');
    const sourceArtifact = String(articleCopy || '') + '\n' + (includeFooter ? String(footerCopy || '') : '');
    const pendingWechatSrcs = requiresWechatTransfer ? findWechatImageSrcs(sourceArtifact) : [];
    if (pendingWechatSrcs.length > 0) {
      throw new Error(`仍有 ${pendingWechatSrcs.length} 张微信图片未完成转存，已保留当前预览，请重试`);
    }
    const config = (window._activeConfig && window._activeConfig.config) || {};
    const themeCSS = await _themeCSSReady;
    if (signal?.aborted) throw signal.reason || new DOMException('The operation was aborted', 'AbortError');
    let html = inlineCopyHtml(sourceArtifact, themeCSS, config);
    const unsafeCopyRefs = findUnsafeCopyImageRefs(html, requiresWechatTransfer);
    if (unsafeCopyRefs.length > 0) {
      throw new Error(`仍有 ${unsafeCopyRefs.length} 张图片未准备为可复制版本，已保留当前预览，请重试`);
    }
    html = withPasteTail(html);
    return {
      html,
      sourceHash: copyArtifactFingerprint(sourceArtifact),
      preparationHash: copyPreparationFingerprint(sourceArtifact, config),
      unsafeCopyRefs,
    };
  }

  function matchingPreparedClipboardArtifact(sourceArtifact) {
    const prepared = _preparedClipboardArtifact;
    if (!prepared?.html) return null;
    const config = (window._activeConfig && window._activeConfig.config) || {};
    if (prepared.sourceHash !== copyArtifactFingerprint(sourceArtifact)) return null;
    if (prepared.preparationHash !== copyPreparationFingerprint(sourceArtifact, config)) return null;
    return prepared;
  }

  window.copyContent = async function() {
    copyActionGate.invalidate();
    const copyAction = copyActionGate.capture();
    const content = document.getElementById('contentArea');
    const btn = document.getElementById('copyBtn');

    if (window._activeRuleState && window._activeRuleState !== 'resolved') {
      _lastCopiedHtml = '';
      logConv('error', '复制已阻止：排版规则尚未由服务端确认', { state: window._activeRuleState });
      showError('排版规则尚未加载成功，请点击顶部“重试加载”后再复制。');
      setCopyButtonLabel(btn, '规则未就绪');
      setCopyButtonCopied(btn, false);
      btn.style.background = '';
      return false;
    }

    if (_copyArtifactUnavailableReason) {
      _lastCopiedHtml = '';
      window._copyImageWarning = 0;
      logConv('error', '复制已阻止：恢复会话缺少安全复制产物', { reason: 'degraded-session' });
      showError(_copyArtifactUnavailableReason);
      setCopyButtonLabel(btn, '请重新导入');
      setCopyButtonCopied(btn, false);
      btn.style.background = '';
      return false;
    }

    if (_plainTextCopy !== null) {
      const text = getPlainTextCopyValue();
      if (!String(text || '').trim()) {
        _lastCopiedHtml = '';
        logConv('error', '纯文字复制已阻止：正文为空');
        showError('当前正文为空，请重新导入后再复制。');
        setCopyButtonLabel(btn, '请重新导入');
        setCopyButtonCopied(btn, false);
        btn.style.background = '';
        return false;
      }
      const ok = await copyPlainTextToClipboard(text);
      if (copyAction.stale()) return false;
      _plainTextCopy = text;
      _lastCopiedHtml = '';
      logConv(ok ? 'info' : 'error', ok ? '纯文字复制完成' : '纯文字复制失败', {
        rule: PLAIN_TEXT_RULE_LABEL,
        chars: text.length,
      });
      setCopyButtonLabel(btn, ok ? '\u2713 已复制' : '复制失败');
      setCopyButtonCopied(btn, ok);
      btn.style.background = '';
      return ok;
    }

    const enabled = isFooterEnabledForCurrentArticle();
    if (!copyBodyHasMeaningfulContent(_articleCopy)) {
      _lastCopiedHtml = '';
      logConv('error', '复制已阻止：正文复制产物为空');
      showError('当前正文复制产物为空，请重新导入后再复制。');
      setCopyButtonLabel(btn, '请重新导入');
      setCopyButtonCopied(btn, false);
      btn.style.background = '';
      return false;
    }
    const sourceArtifact = _articleCopy + '\n' + (enabled ? _footerCopy : '');
    const artifactHash = copyArtifactFingerprint(sourceArtifact);
    const preparedArtifact = (
      typeof _preparedClipboardArtifact !== 'undefined'
      && _preparedClipboardArtifact?.html
      && _preparedClipboardArtifact.sourceHash === artifactHash
    ) ? _preparedClipboardArtifact : null;
    if (!preparedArtifact && !_articleBodyOnly) prepareMoreArticlesForCopy('copy-click');
    let html = preparedArtifact?.html || sourceArtifact;
    const pendingWechatSrcs = _copyRequiresWechatImageTransfer ? findWechatImageSrcs(html) : [];
    if (pendingWechatSrcs.length > 0) {
      const waiting = _uploadPromise && !_uploadDone;
      logConv(waiting ? 'warn' : 'error', waiting
        ? '网站图片仍在转存，请稍后再复制'
        : '仍有微信图床图片未转存，已阻止复制', {
        count: pendingWechatSrcs.length,
        first: pendingWechatSrcs[0].slice(0, 80),
      });
      const originalText = copyButtonLabel(btn);
      setCopyButtonLabel(btn, waiting ? '图片准备中' : '图片需重试');
      setTimeout(() => { if (!copyAction.stale()) setCopyButtonLabel(btn, originalText); }, 1600);
      return false;
    }

    // 服务端剥离图片的 DOCX（飞书云文档导入、.doc 转换）先用 /api/doc-cache/* 占位，
    // 真正能粘进公众号的位图由后台 materialize 回填。回填期间点复制，写进剪贴板的是
    // 站内相对地址——微信后台取不到，草稿里就是空图。后台还在跑时立刻挡回：按钮照旧
    // 瞬时反馈（复制速度契约不变），只是不再交付一份注定缺图的剪贴板；准备完成后按钮
    // 自动恢复。
    if (_uploadPromise && !_uploadDone) {
      const preparingRefs = findUnsafeCopyImageRefs(html, _copyRequiresWechatImageTransfer);
      if (preparingRefs.length > 0) {
        logConv('warn', '图片仍在准备，已阻止复制缺图产物', {
          count: preparingRefs.length,
          first: preparingRefs[0].slice(0, 80),
        });
        const originalText = copyButtonLabel(btn);
        setCopyButtonLabel(btn, '图片准备中');
        setCopyButtonCopied(btn, false);
        const restore = () => { if (!copyAction.stale()) setCopyButtonLabel(btn, originalText); };
        _uploadPromise.then(restore, restore);
        return false;
      }
    }

    if (!preparedArtifact) {
      // Inline CSS classes → style attributes for WeChat compatibility
      const config = (window._activeConfig && window._activeConfig.config) || {};
      const themeCSS = await _themeCSSReady;
      if (copyAction.stale()) return false;
      const currentArtifact = _articleCopy + '\n' + (isFooterEnabledForCurrentArticle() ? _footerCopy : '');
      if (copyArtifactFingerprint(currentArtifact) !== artifactHash) {
        _lastCopiedHtml = '';
        logConv('warn', '复制已取消：排版产物在复制期间更新', { artifactHash });
        showError('排版内容刚刚更新，请再次点击复制。');
        setCopyButtonLabel(btn, '复制全文');
        setCopyButtonCopied(btn, false);
        btn.style.background = '';
        return false;
      }
      html = processForCopy(html, themeCSS, config, { preserveClasses: isWebsiteTemplate() });
      html = normalizeRich(html);

      // 零高度光标段：见 withPasteTail。
      if (!isWebsiteTemplate()) {
        html += '<p style="margin:0;padding:0;line-height:0;font-size:0;min-height:0;height:0;"><br/></p>';
      }
    }
    const unsafeCopyRefs = findUnsafeCopyImageRefs(html, _copyRequiresWechatImageTransfer);

    const plainText = content.textContent || '';

    let ok = false;
    let method = '';

    try {
      ok = await copyByClipboardApi(html, plainText);
      if (copyAction.stale()) return false;
      if (ok) method = 'ClipboardAPI';
    } catch {}
    if (copyAction.stale()) return false;

    if (!ok) {
      try {
        ok = await copyByClipboardEvent(html, plainText);
        if (copyAction.stale()) return false;
        if (ok) method = 'ClipboardEvent';
      } catch {}
    }

    if (copyAction.stale()) return false;
    _lastCopiedHtml = ok ? html : '';
    const imgCount = (html.match(/<img[\s>]/gi) || []).length;
    // 告警只以已写入剪贴板的最终 HTML 为真值。OSS 上传是后台增强；
    // 公众号 <img src> 中的位图 data: 仍会被微信粘贴转存，不得误报为缺图。
    const failedImgCount = ok ? unsafeCopyRefs.length : 0;
    window._copyImageWarning = failedImgCount;
    logConv(ok ? (failedImgCount ? 'warn' : 'info') : 'error',
      ok ? (failedImgCount ? '复制完成，但有图片无法安全粘贴' : '复制完成') : '复制失败', {
        method,
        htmlSize: (html.length / 1024).toFixed(1) + 'KB',
        textChars: plainText.length,
        imgCount,
        failedImgs: failedImgCount || undefined,
        channel: (_activeTemplate && _activeTemplate.id) || 'unknown',
        ruleRevision: (window._activeConfig && (window._activeConfig.revision || window._activeConfig.version)) || undefined,
        artifactHash,
      });

    setCopyButtonLabel(btn, ok ? '\u2713 已复制' : '复制失败');
    if (ok && failedImgCount) setCopyButtonLabel(btn, '已复制·' + failedImgCount + '张图缺失');
    setCopyButtonCopied(btn, ok);
    // 按钮警告态走样式类（2026-08-05 de7a8ea），不再写 inline 底色。
    setCopyButtonWarning(btn, ok && failedImgCount > 0);
    btn.style.background = '';
    return ok;
  };

  window.copyHTMLSource = async function() {
    copyActionGate.invalidate();
    const copyAction = copyActionGate.capture();

    if (!isWebsiteTemplate()) {
      showError('复制 HTML 仅用于网站排版。');
      return false;
    }
    if (window._activeRuleState && window._activeRuleState !== 'resolved') {
      _lastCopiedHtml = '';
      showError('排版规则尚未加载成功，请点击顶部“重试加载”后再复制。');
      return false;
    }
    if (_copyArtifactUnavailableReason) {
      _lastCopiedHtml = '';
      showError(_copyArtifactUnavailableReason);
      return false;
    }
    if (_plainTextCopy !== null) {
      _lastCopiedHtml = '';
      showError('当前是纯文字结果，没有可复制的 HTML。');
      return false;
    }

    if (!_articleBodyOnly) prepareMoreArticlesForCopy('copy-html-click');
    if (!copyBodyHasMeaningfulContent(_articleCopy)) {
      _lastCopiedHtml = '';
      showError('当前正文复制产物为空，请重新导入后再复制。');
      return false;
    }

    const enabled = isFooterEnabledForCurrentArticle();
    const sourceArtifact = _articleCopy + '\n' + (enabled ? _footerCopy : '');
    const artifactHash = copyArtifactFingerprint(sourceArtifact);
    const pendingWechatSrcs = _copyRequiresWechatImageTransfer ? findWechatImageSrcs(sourceArtifact) : [];
    if (pendingWechatSrcs.length > 0) {
      _lastCopiedHtml = '';
      showError(_uploadPromise && !_uploadDone
        ? '网站图片仍在转存，请稍后再复制 HTML。'
        : '仍有微信图床图片未转存，请先重试上传。');
      return false;
    }

    // 与 copyContent 同一条红线：站内占位地址出了这个页面就取不到，
    // HTML 源码同样不能在图片回填完成前交付。
    if (_uploadPromise && !_uploadDone
      && findUnsafeCopyImageRefs(sourceArtifact, _copyRequiresWechatImageTransfer).length > 0) {
      _lastCopiedHtml = '';
      showError('图片仍在准备，请等状态条显示准备完成后再复制 HTML。');
      return false;
    }

    const config = (window._activeConfig && window._activeConfig.config) || {};
    const themeCSS = await _themeCSSReady;
    if (copyAction.stale()) return false;
    const currentArtifact = _articleCopy + '\n' + (isFooterEnabledForCurrentArticle() ? _footerCopy : '');
    if (copyArtifactFingerprint(currentArtifact) !== artifactHash) {
      _lastCopiedHtml = '';
      showError('排版内容刚刚更新，请再次点击复制 HTML。');
      return false;
    }

    const html = inlineCopyHtml(sourceArtifact, themeCSS, config);
    const ok = await copyPlainTextToClipboard(html);
    if (copyAction.stale()) return false;
    _lastCopiedHtml = ok ? html : '';
    logConv(ok ? 'info' : 'error', ok ? 'HTML 源码复制完成' : 'HTML 源码复制失败', {
      htmlSize: (html.length / 1024).toFixed(1) + 'KB',
      channel: (_activeTemplate && _activeTemplate.id) || 'unknown',
      ruleRevision: (window._activeConfig && (window._activeConfig.revision || window._activeConfig.version)) || undefined,
      artifactHash,
    });
    return ok;
  };

  const immediateCopyContent = window.copyContent;
  window.copyContent = function(...args) {
    window._copyInProgress = true;
    try {
      return Promise.resolve(immediateCopyContent(...args)).finally(() => {
        window._copyInProgress = false;
      });
    } catch (error) {
      window._copyInProgress = false;
      throw error;
    }
  };

  const immediateCopyHTMLSource = window.copyHTMLSource;
  window.copyHTMLSource = function(...args) {
    window._copyInProgress = true;
    try {
      return Promise.resolve(immediateCopyHTMLSource(...args)).finally(() => {
        window._copyInProgress = false;
      });
    } catch (error) {
      window._copyInProgress = false;
      throw error;
    }
  };

  window._hasUnsavedEditorWork = function() {
    const previewContent = document.getElementById('previewContent');
    const importInput = document.getElementById('urlInput');
    return !!(
      window._copyInProgress
      || _articleInputPending
      || (_uploadPromise && !_uploadDone)
      || dropZone.classList.contains('processing')
      || (previewContent && previewContent.style.display !== 'none')
      || (importInput && String(importInput.value || '').trim())
      || document.body.classList.contains('has-preview')
    );
  };

  window.getClipboardHtml = function() { return _lastCopiedHtml; };

  // ---- UI Controls ----
  const ZOOM_STEPS = [50, 75, 100, 125, 150, 200];
  let zoomIdx = ZOOM_STEPS.length - 1;

  window.goBack = function() {
    copySession.resetForNewArticle();
    _articleInputPending = false;
    clearQueuedInputRerender();
    _moreArticlesCopyJob = null;
    resetRetryUploadUI();
    // Split layout: hide preview, show empty state
    const emptyState = document.getElementById('emptyState');
    const previewContent = document.getElementById('previewContent');
    const wxFooter = document.getElementById('wxFooter');
    if (emptyState) emptyState.style.display = '';
    if (previewContent) previewContent.style.display = 'none';
    if (wxFooter) wxFooter.style.display = 'none';
    document.body.classList.remove('has-preview', 'plain-text-preview', 'article-text-preview');

    _plainTextCopy = null;
    _copyArtifactUnavailableReason = '';
    _articleBodyOnly = false;
    _articleSession = null;
    window._articleMeta = {};
    window._articleFooterProfile = null;
    window._footerVisibilityOverride = undefined;
    window._articleArtifactOverride = undefined;
    _lastRenderInput = null;
    dropZone.classList.remove('processing');
    resetImportEntry();
    fileInput.value = '';
    const copyBtn = document.getElementById('copyBtn');
    if (copyBtn) {
      setCopyButtonLabel(copyBtn, '复制全文');
      setCopyButtonCopied(copyBtn, false);
    }
    const contentArea = document.getElementById('contentArea');
    if (contentArea) contentArea.innerHTML = '';
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
    const importInput = document.getElementById('urlInput');
    const titleDisplay = document.getElementById('previewTitleDisplay');
    const titleInput = document.getElementById('titleInput');
    const date = document.getElementById('wxDate');
    const previewContent = document.getElementById('previewContent');
    const emptyState = document.getElementById('emptyState');
    const wxFooter = document.getElementById('wxFooter');
    return {
      schema_version: 1,
      timestamp: Date.now(),
      closure_state: {
        article_html: _articleHtml,
        article_copy: _articleCopy,
        footer_html: _footerHtml,
        footer_copy: _footerCopy,
        copy_unavailable_reason: _copyArtifactUnavailableReason,
        plain_text_copy: _plainTextCopy === null ? null : getPlainTextCopyValue(),
        is_repost: _isRepost,
        source_is_docx: _sourceIsDocx,
        article_body_only: _articleBodyOnly,
        copy_requires_wechat_image_transfer: _copyRequiresWechatImageTransfer,
        failed_srcs: _failedSrcs ? _failedSrcs.slice() : [],
        upload_done: _uploadDone,
        last_render_input: clonePlain(_lastRenderInput),
        article_session: clonePlain(_articleSession),
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
        has_preview_class: document.body.classList.contains('has-preview'),
        plain_text_preview: document.body.classList.contains('plain-text-preview'),
        article_text_preview: document.body.classList.contains('article-text-preview'),
        import_visible: importZone ? !importZone.hidden : true,
        doc_visible: docZone ? !docZone.hidden : false,
        doc_name: docName ? docName.textContent : '',
        doc_stat: docStat ? docStat.textContent : '',
        stats_text: (document.getElementById('statsBar') || {}).textContent || '',
        preview_scroll_top: (document.getElementById('previewScroll') || {}).scrollTop || 0,
        import_input_value: importInput ? importInput.value : '',
      },
    };
  };

  function restoredImportInputValue(dom, closureState) {
    if (Object.prototype.hasOwnProperty.call(dom, 'import_input_value')) {
      return dom.import_input_value || '';
    }
    const input = (closureState && (closureState.last_render_input || (closureState.article_session || {}).input)) || null;
    if (
      input &&
      input.kind === 'md' &&
      !input.isRepost &&
      !input.sourceIsDocx &&
      !input.title &&
      typeof input.payload === 'string'
    ) {
      return input.payload;
    }
    return '';
  }

  window._restoreSessionSnapshot = function(snap) {
    if (!snap || snap.schema_version !== 1) return false;
    const cs = snap.closure_state || {};
    const dom = snap.dom || {};
    _articleHtml = cs.article_html || '';
    _articleCopy = cs.article_copy || '';
    _footerHtml = cs.footer_html || '';
    _footerCopy = copySafeFooterAfterImagePreparation(cs.footer_copy || '');
    copySession.resetForNewArticle();
    _articleInputPending = false;
    clearQueuedInputRerender();
    _plainTextCopy = cs.plain_text_copy != null ? cs.plain_text_copy : null;
    const restoredCopyMissing = !!dom.preview_visible && _plainTextCopy === null && !String(_articleCopy || '').trim();
    _copyArtifactUnavailableReason = String(
      cs.copy_unavailable_reason
        || snap.degraded_reason
        || ((snap.degraded || restoredCopyMissing) ? '文章内容过大，仅恢复了预览。请重新导入后再复制。' : '')
    );
    _isRepost = !!cs.is_repost;
    _sourceIsDocx = !!cs.source_is_docx;
    _articleBodyOnly = !!cs.article_body_only || !!dom.article_text_preview;
    _copyRequiresWechatImageTransfer = !!cs.copy_requires_wechat_image_transfer;
    _failedSrcs = cs.failed_srcs || [];
    _uploadDone = !!cs.upload_done;
    _uploadPromise = null;
    _moreArticlesCopyJob = null;
    _lastRenderInput = cs.last_render_input || null;
    _articleSession = cs.article_session || null;
    window._articleMeta = (_articleSession && _articleSession.articleMeta) || {};
    window._articleFooterProfile = (_articleSession && _articleSession.articleFooterProfile) || null;
    window._footerVisibilityOverride = normalizeFooterVisibilityOverride(_articleSession && _articleSession.footerVisibilityOverride);
    window._articleArtifactOverride = normalizeArticleArtifactOverride(_articleSession && _articleSession.articleArtifactOverride);
    if (!_articleSession && _lastRenderInput) {
      _articleSession = {
        input: _lastRenderInput,
        title: dom.title_input_value || dom.article_title_text || '',
        articleHtml: _articleHtml,
        footerHtml: _footerHtml,
        stats: dom.stats_text || '',
        sourceIsDocx: _sourceIsDocx,
        bodyOnly: _articleBodyOnly,
      };
    }
    reconcileFailedImageRefs();
    const ca = document.getElementById('contentArea');
    if (ca) {
      ca.innerHTML = dom.content_area_html || '';
      adoptPreviewContent(ca, _articleBodyOnly || !isFooterEnabledForCurrentArticle() ? '' : _footerHtml);
      if (dom.content_area_style) ca.setAttribute('style', dom.content_area_style);
      else ca.removeAttribute('style');
      if (dom.plain_text_preview) {
        const output = ca.querySelector('.plain-text-page-output');
        if (output && typeof output.value === 'string') output.value = _plainTextCopy || '';
        else if (output) output.textContent = _plainTextCopy || '';
      }
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
    document.body.classList.toggle('has-preview', !!dom.has_preview_class);
    document.body.classList.toggle('plain-text-preview', !!dom.plain_text_preview);
    document.body.classList.toggle('article-text-preview', !!dom.article_text_preview);
    const previewScroll = document.getElementById('previewScroll');
    if (previewScroll) previewScroll.scrollTop = dom.preview_scroll_top || 0;
    const importInput = document.getElementById('urlInput');
    if (importInput) importInput.value = restoredImportInputValue(dom, cs);
    if (dom.plain_text_preview) {
      dispatchArticleEvent('fbif:plain-text-ready', { text: _plainTextCopy || '', rule: PLAIN_TEXT_RULE_LABEL });
    } else if (dom.article_text_preview) {
      dispatchArticleEvent('fbif:article-text-ready', { snapshot: snap });
    } else {
      dispatchArticleEvent('fbif:article-restored', { snapshot: snap });
    }
    if (_copyArtifactUnavailableReason) showError(_copyArtifactUnavailableReason);
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
    rerender: (options) => {
      if (typeof window._engineRerender === 'function') {
        return window._engineRerender(options);
      }
    },
  };
}
