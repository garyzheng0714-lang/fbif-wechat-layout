// Shared WeChat formatting engine — UI coordination layer
// Delegates: parsing → parser.js, upload → uploader.js, copy → clipboard.js, CSS → css-inline.js
const assetQuery = new URL(import.meta.url).search;

// Re-export parser utilities for templates
export { esc, escAttr, parseMdRuns, parseMdFrontmatter, parseDocx, extractParagraph,
         W, R, WP, A, findAll, findOne, findDeep, wattr, rattr } from './parser.js';

import { parseDocx } from './parser.js';
import { uploadNonCdnImages, retryFailedImages } from './uploader.js';
import { copyByClipboardApi, copyByClipboardEvent } from './clipboard.js';
import { loadThemeCSS, processForCopy, applyThemeVars } from './css-inline.js';

const imageUtilsModule = await import('./image-utils.mjs' + assetQuery);
const { inferWechatImageType, looksLikeGifSource } = imageUtilsModule;
export { inferWechatImageType, looksLikeGifSource };

// Pre-load theme CSS
const _themeCSSReady = loadThemeCSS();

// ---- Footer ----
const footerReady = fetch('/footer.html').then(r => r.ok ? r.text() : '').catch(() => '');

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

  async function handleFiles(files) {
    const valid = Array.from(files).filter(isValidFile);
    if (valid.length === 0) {
      showError('请上传 ' + template.formats.join(' 或 ') + ' 文件');
      return;
    }
    if (valid.length === 1) {
      handleFile(valid[0]);
      return;
    }
    // Batch mode
    showBatchList(valid);
    errorEl.style.display = 'none';
    for (let i = 0; i < valid.length; i++) {
      updateBatchStatus(i, '处理中...', 'processing');
      try {
        const result = await processFile(valid[i]);
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

  async function processFile(file) {
    const t0 = performance.now();
    let result;
    if (file.name.toLowerCase().endsWith('.docx')) {
      const docxData = await parseDocx(file);
      result = template.processDocx(docxData);
    } else if (typeof template.processMd === 'function') {
      const text = await file.text();
      result = await template.processMd(text);
    } else {
      throw new Error('此模板不支持该文件格式');
    }
    const articleHtml = result.lines.join('\n');
    const footerHtml = localStorage.getItem('custom_footer') || await footerReady;
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
    handleFiles(e.dataTransfer.files);
  });
  fileInput.addEventListener('change', () => { if (fileInput.files.length) handleFiles(fileInput.files); });

  async function handleFile(file) {
    _convLog.length = 0;
    logConv('info', '开始转化', { file: file.name, size: (file.size / 1024).toFixed(1) + 'KB' });
    errorEl.style.display = 'none';
    dropZone.classList.add('processing');
    progress.style.display = 'block';
    progressFill.style.width = '50%';
    progressText.textContent = '正在解析...';
    try {
      const t0 = performance.now();

      let result;
      if (file.name.toLowerCase().endsWith('.docx')) {
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
      const footerHtml = localStorage.getItem('custom_footer') || await footerReady;
      const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
      const stats = '段落: ' + result.lines.length + ' | 图片: ' + result.imgN +
        ' | 标题: ' + result.headingN + ' | 耗时: ' + elapsed + 's';
      logConv('info', '排版完成', { elapsed: elapsed + 's', headings: result.headingN });

      progressFill.style.width = '100%';
      progressText.textContent = '排版完成!';
      const displayTitle = result.title || file.name.replace(/\.\w+$/i, '');
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

    document.getElementById('titleInput').value = title;
    document.getElementById('previewTitleDisplay').textContent = title;

    // Apply theme CSS variables to contentArea for preview
    const contentArea = document.getElementById('contentArea');
    if (window._activeConfig) applyThemeVars(contentArea, window._activeConfig.config);
    contentArea.innerHTML = _articleHtml + '\n' + _footerHtml;
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
      markFailedImages(_failedSrcs);
    };

    // Background: upload non-mmbiz images to WeChat CDN for the copy layer
    _uploadDone = false;
    const statsBar = document.getElementById('statsBar');
    const originalStats = stats;

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

    if (_uploadPromise && !_uploadDone) {
      btn.textContent = '等待图片上传...';
      await _uploadPromise;
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

    dropZone.classList.remove('processing');
    progress.style.display = 'none';
    progressFill.style.width = '0%';
    fileInput.value = '';
    const copyBtn = document.getElementById('copyBtn');
    if (copyBtn) { copyBtn.textContent = '复制到公众号'; copyBtn.classList.remove('copied'); }
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
