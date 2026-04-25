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

import { uploadNonCdnImages, retryFailedImages } from './uploader.js';
import { copyByClipboardApi, copyByClipboardEvent } from './clipboard.js';
import { loadThemeCSS, processForCopy, applyThemeVars } from './css-inline.js';
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
const footerReady = fetch('/footer.html').then(r => r.ok ? r.text() : '').catch(() => '');

// Module-level flag: true when the current article was fetched from a URL
// (i.e. it is a repost). Set explicitly by handleFile/processFile at each
// entry so flags never leak across articles. Read by applyRepostTransform
// at every footer-rebuild site.
let _isRepost = false;

// Rewrites the stock FBIF footer from 原创 → 转载 when _isRepost is true.
// Anchors: four <!--REPOST_BULLET{1,2}_{START,END}--> HTML comments embedded
// in footer.html. If the user customized the footer and removed the sentinels
// or the bullet text, transform no-ops (logged) — custom footers are the
// user's responsibility.
function applyRepostTransform(html) {
  if (!_isRepost) return html;
  if (typeof html !== 'string' || !html) return html;
  let out = html;
  let changed = false;
  const before = '* 本文为FBIF原创，欢迎转发朋友圈；';
  const after = '* 本文为转载，不代表FBIF立场。';
  if (out.includes(before)) {
    out = out.replace(before, after);
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
async function buildFooterWithMoreArticles(baseFooterHtml) {
  const transformed = applyRepostTransform(baseFooterHtml);
  const cards = loadMoreArticlesCards();
  const cardsForPreview = await Promise.all(
    cards.map(async c => ({ ...c, final_url: await compositePreviewDataUrl(c) }))
  );
  return mergeMoreArticlesIntoFooter(transformed, cardsForPreview);
}

function hasDeferredCopyImages(articleCopy, footerCopy) {
  return /<img\b[^>]*\bsrc=["'](?:blob:|\/api\/doc-cache\/)/i
    .test((articleCopy || '') + '\n' + (footerCopy || ''));
}

// ---- App Initialization ----
export function initApp(template) {
  document.getElementById('pageTitle').textContent = template.name;
  document.getElementById('pageSubtitle').textContent = template.description;
  document.getElementById('fileHint').textContent =
    '支持 ' + template.formats.join('、') + ' 格式';
  const fileInputEl = document.getElementById('fileInput');
  fileInputEl.accept = template.formats.join(',');
  fileInputEl.multiple = true;

  const dropZone = document.getElementById('dropZone');
  const fileInput = fileInputEl;
  const progress = document.getElementById('progress');
  const progressFill = document.getElementById('progressFill');
  const progressText = document.getElementById('progressText');
  const errorEl = document.getElementById('error');
  const batchList = document.getElementById('batchList');

  function showError(msg) { errorEl.textContent = msg; errorEl.style.display = 'block'; }

  function isValidFile(f) {
    return template.formats.some(fmt => f.name.toLowerCase().endsWith(fmt));
  }

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
    // Set repost flag per-call so it never leaks across articles.
    _isRepost = !!(opts && opts.isRepost);
    const t0 = performance.now();
    file = await normalizeToDocx(file);
    let result;
    const isDocx = file.name.toLowerCase().endsWith('.docx');
    _sourceIsDocx = isDocx;
    if (isDocx) {
      const docxData = await parseDocx(file);
      result = template.processDocx(docxData);
    } else if (typeof template.processMd === 'function') {
      const text = await file.text();
      result = await template.processMd(text);
    } else {
      throw new Error('此模板不支持该文件格式');
    }
    const articleHtml = result.lines.join('\n');
    // New article upload resets the "更多文章" section to blank placeholder
    // slots — cards are a per-article curation, filled in manually each time.
    saveMoreArticlesCards(makeFreshUploadCards());
    if (window._refreshMoreArticlesSidebar) window._refreshMoreArticlesSidebar();
    const baseFooterHtml = localStorage.getItem('custom_footer') || await footerReady;
    const footerHtml = await buildFooterWithMoreArticles(baseFooterHtml);
    const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
    const stats = '段落: ' + result.lines.length + ' | 图片: ' + result.imgN +
      ' | 标题: ' + result.headingN + ' | 耗时: ' + elapsed + 's';
    return { articleHtml, footerHtml, stats, title: result.title };
  }

  dropZone.addEventListener('click', () => fileInput.click());
  dropZone.addEventListener('dragover', e => { e.preventDefault(); dropZone.classList.add('dragover'); });
  dropZone.addEventListener('dragleave', () => dropZone.classList.remove('dragover'));
  dropZone.addEventListener('drop', e => {
    e.preventDefault(); dropZone.classList.remove('dragover');
    handleFiles(e.dataTransfer.files, { isRepost: false });
  });
  fileInput.addEventListener('change', () => { if (fileInput.files.length) handleFiles(fileInput.files, { isRepost: false }); });

  // Exposed so app.html fetchArticle() can feed the synthetic File produced
  // from a URL fetch through the same pipeline as a local upload, but with
  // isRepost=true so the footer swaps to the 转载 variant.
  window._handleFile = (file, opts) => handleFile(file, opts);

  // URL-import structured-blocks entry point. Used when the server responds
  // to /api/fetch-article with {title, blocks} (preferred path — preserves
  // visual signals). Falls back to _handleFile with a synthetic .md File
  // when the server returns {title, content} (x-reader fallback).
  window._handleBlocks = async (data, opts) => {
    _isRepost = !!(opts && opts.isRepost);
    _convLog.length = 0;
    logConv('info', '开始转化', { source: 'blocks', blocks: (data.blocks || []).length });
    errorEl.style.display = 'none';
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
      const result = await template.processBlocks(data);
      logConv('info', '结构化解析完成', { paragraphs: result.lines.length, images: result.imgN });

      const articleHtml = result.lines.join('\n');
      saveMoreArticlesCards(makeFreshUploadCards());
      if (window._refreshMoreArticlesSidebar) window._refreshMoreArticlesSidebar();
      const baseFooterHtml = localStorage.getItem('custom_footer') || await footerReady;
      const footerHtml = await buildFooterWithMoreArticles(baseFooterHtml);
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

  async function handleFile(file, opts) {
    // Set repost flag per-call so it never leaks across articles.
    _isRepost = !!(opts && opts.isRepost);
    _convLog.length = 0;
    logConv('info', '开始转化', { file: file.name, size: (file.size / 1024).toFixed(1) + 'KB' });
    errorEl.style.display = 'none';
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
      if (isDocx) {
        const docxData = await parseDocx(file);
        result = template.processDocx(docxData);
        logConv('info', 'DOCX解析完成', { paragraphs: result.lines.length, images: result.imgN });
      } else if (typeof template.processMd === 'function') {
        const text = await file.text();
        result = await template.processMd(text);
        logConv('info', 'Markdown解析完成', { paragraphs: result.lines.length, images: result.imgN });
      } else {
        throw new Error('此模板不支持该文件格式');
      }

      const articleHtml = result.lines.join('\n');
      // Single-file upload path — same reset behavior as batch path: wipe
      // any prior article's "更多文章" cards to blank placeholder slots, so
      // sidebar shows empty inputs and footer renders gray placeholders.
      saveMoreArticlesCards(makeFreshUploadCards());
      if (window._refreshMoreArticlesSidebar) window._refreshMoreArticlesSidebar();
      const baseFooterHtml = localStorage.getItem('custom_footer') || await footerReady;
      const footerHtml = await buildFooterWithMoreArticles(baseFooterHtml);
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

  function showPreview(title, articleContent, footerContent, stats) {
    _articleHtml = articleContent;
    _articleCopy = articleContent;
    _footerHtml = footerContent;
    _footerCopy = footerContent;
    _failedSrcs = [];

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

    // Apply theme CSS variables to contentArea for preview
    const contentArea = document.getElementById('contentArea');
    if (window._activeConfig) applyThemeVars(contentArea, window._activeConfig.config);
    contentArea.innerHTML = _articleHtml + '\n' + _footerHtml;
    hookPreviewImages(contentArea);
    document.getElementById('statsBar').textContent = stats;

    const retryBtn = document.getElementById('retryBtn');
    if (retryBtn) retryBtn.style.display = 'none';

    window._updateFooter = function(newHtml) {
      if (typeof newHtml === 'string' && newHtml !== '') {
        _footerHtml = newHtml;
        _footerCopy = newHtml;
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
      const base = localStorage.getItem('custom_footer') || await footerReady;
      const merged = await buildFooterWithMoreArticles(base);
      _footerHtml = merged;
      _footerCopy = merged;
      const enabled = document.getElementById('footerEnabled').checked;
      contentArea.innerHTML = _articleHtml + '\n' + (enabled ? _footerHtml : '');
      hookPreviewImages(contentArea);
      markFailedImages(_failedSrcs);
    };

    // Background: upload non-mmbiz images to WeChat CDN for the copy layer
    _uploadDone = false;
    _uploadPromise = null;
    const statsBar = document.getElementById('statsBar');
    const originalStats = stats;

    const skipUploadForThisFile =
      !!window._skipUpload && _sourceIsDocx && !hasDeferredCopyImages(_articleCopy, _footerCopy);
    if (skipUploadForThisFile) {
      _uploadDone = true;
      logConv('info', 'DOCX 跳过上传：不启动后台上传');
      return;
    }
    if (!!window._skipUpload && _sourceIsDocx) {
      logConv('info', 'DOCX 图片需要先上传：检测到临时图片地址，跳过上传会导致微信不可用');
    }

    _uploadPromise = uploadNonCdnImages(_articleCopy, _footerCopy, {
      onProgress(done, total) {
        statsBar.textContent = '上传图片 (' + done + '/' + total + ')...';
      },
      onLog: logConv,
    }).then(result => {
      _articleCopy = result.articleCopy;
      _footerCopy = result.footerCopy;
      _failedSrcs = result.failedSrcs || [];
      _uploadDone = true;
      if (result.failed > 0) {
        statsBar.textContent = originalStats + ' | ' + result.failed + '张图片上传失败';
        markFailedImages(_failedSrcs);
        if (retryBtn) retryBtn.style.display = 'inline-block';
      } else {
        statsBar.textContent = originalStats + ' | 全部图片已就绪';
      }
    });
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
      statsBar.textContent = '全部图片已就绪';
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

    const skipUpload =
      !!window._skipUpload && _sourceIsDocx && !hasDeferredCopyImages(_articleCopy, _footerCopy);
    if (_uploadPromise && !_uploadDone && !skipUpload) {
      btn.textContent = '等待图片上传...';
      await _uploadPromise;
    }
    if (skipUpload) {
      logConv('info', 'DOCX 跳过上传：base64 直接写入剪贴板');
    }

    // Upload any customized "更多文章" composites to OSS and rebuild _footerCopy
    // with public URLs before writing to clipboard.
    try {
      const cards = loadMoreArticlesCards();
      const needsWork = cards.some(c => (c.title && c.title.trim()) || c.cover_data_url);
      if (needsWork) {
        btn.textContent = '上传更多文章...';
        const uploaded = await ensureCompositeReady(cards);
        saveMoreArticlesCards(uploaded);
        const baseFooter = applyRepostTransform(localStorage.getItem('custom_footer') || await footerReady);
        _footerCopy = mergeMoreArticlesIntoFooter(baseFooter, uploaded);
      }
    } catch (err) {
      logConv('warn', '更多文章上传失败，使用本地预览版', { error: err.message });
    }

    const enabled = document.getElementById('footerEnabled').checked;
    let html = _articleCopy + '\n' + (enabled ? _footerCopy : '');

    // Inline CSS classes → style attributes for WeChat compatibility
    const config = (window._activeConfig && window._activeConfig.config) || {};
    const themeCSS = await _themeCSSReady;
    html = processForCopy(html, themeCSS, config);

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
    progress.style.display = 'none';
    progressFill.style.width = '0%';
    fileInput.value = '';
    const copyBtn = document.getElementById('copyBtn');
    if (copyBtn) { copyBtn.textContent = '复制全文'; copyBtn.classList.remove('copied'); }
    document.getElementById('statsBar').textContent = '';
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
}
