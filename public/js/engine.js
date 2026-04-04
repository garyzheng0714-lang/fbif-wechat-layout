// Shared WeChat formatting engine — UI coordination layer
// Delegates: parsing → parser.js, upload → uploader.js, copy → clipboard.js
const assetQuery = new URL(import.meta.url).search;

// Re-export parser utilities for templates
export { esc, escAttr, parseMdRuns, parseMdFrontmatter, parseDocx, extractParagraph,
         W, R, WP, A, findAll, findOne, findDeep, wattr, rattr } from './parser.js';

import { parseDocx } from './parser.js';
import { uploadNonCdnImages, retryFailedImages } from './uploader.js';
import { copyByClipboardApi, copyByClipboardEvent } from './clipboard.js';

const imageUtilsModule = await import('./image-utils.mjs' + assetQuery);
const { inferWechatImageType, looksLikeGifSource } = imageUtilsModule;
export { inferWechatImageType, looksLikeGifSource };

// ---- Footer ----
const footerReady = fetch('/footer.html').then(r => r.ok ? r.text() : '').catch(() => '');

// ---- App Initialization ----
export function initApp(template) {
  document.getElementById('pageTitle').textContent = template.name;
  document.getElementById('pageSubtitle').textContent = template.description;
  document.getElementById('fileHint').textContent =
    '支持 ' + template.formats.join('、') + ' 格式';
  document.getElementById('fileInput').accept = template.formats.join(',');

  const dropZone = document.getElementById('dropZone');
  const fileInput = document.getElementById('fileInput');
  const progress = document.getElementById('progress');
  const progressFill = document.getElementById('progressFill');
  const progressText = document.getElementById('progressText');
  const errorEl = document.getElementById('error');

  function showError(msg) { errorEl.textContent = msg; errorEl.style.display = 'block'; }

  dropZone.addEventListener('click', () => fileInput.click());
  dropZone.addEventListener('dragover', e => { e.preventDefault(); dropZone.classList.add('dragover'); });
  dropZone.addEventListener('dragleave', () => dropZone.classList.remove('dragover'));
  dropZone.addEventListener('drop', e => {
    e.preventDefault(); dropZone.classList.remove('dragover');
    const f = e.dataTransfer.files[0];
    if (f && template.formats.some(fmt => f.name.toLowerCase().endsWith(fmt))) handleFile(f);
    else showError('请上传 ' + template.formats.join(' 或 ') + ' 文件');
  });
  fileInput.addEventListener('change', () => { if (fileInput.files[0]) handleFile(fileInput.files[0]); });

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
      setTimeout(() => showPreview(file.name.replace(/\.\w+$/i, ''), articleHtml, footerHtml, stats), 200);
    } catch (err) {
      logConv('error', '排版失败', { error: err.message });
      dropZone.classList.remove('processing');
      progress.style.display = 'none';
      showError('排版失败: ' + err.message);
    }
  }

  // Dual-layer rendering:
  //   Display layer (_articleHtml) — original URLs for browser preview
  //   Copy layer (_articleCopy)   — ALL images re-uploaded to WeChat CDN for paste
  let _articleHtml = '';
  let _articleCopy = '';
  let _footerHtml = '';
  let _footerCopy = '';
  let _uploadPromise = null;
  let _uploadDone = false;
  let _failedSrcs = [];

  // Mark failed images with red border in preview
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

    document.getElementById('uploadView').style.display = 'none';
    document.getElementById('previewView').style.display = 'block';

    document.getElementById('titleInput').value = title;
    document.getElementById('previewTitleDisplay').textContent = title;

    document.getElementById('contentArea').innerHTML = _articleHtml + '\n' + _footerHtml;
    document.getElementById('statsBar').textContent = stats;

    // Hide retry button initially
    const retryBtn = document.getElementById('retryBtn');
    if (retryBtn) retryBtn.style.display = 'none';

    window._updateFooter = function(newHtml) {
      if (typeof newHtml === 'string' && newHtml !== '') {
        _footerHtml = newHtml;
        _footerCopy = newHtml;
      }
      const enabled = document.getElementById('footerEnabled').checked;
      document.getElementById('contentArea').innerHTML = _articleHtml + '\n' + (enabled ? _footerHtml : '');
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

  // Retry failed image uploads
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

  // ---- Copy ----
  let _lastCopiedHtml = '';

  window.copyContent = async function() {
    const content = document.getElementById('contentArea');
    const btn = document.getElementById('copyBtn');

    if (_uploadPromise && !_uploadDone) {
      btn.textContent = '等待图片上传...';
      await _uploadPromise;
    }

    const enabled = document.getElementById('footerEnabled').checked;
    const html = _articleCopy + '\n' + (enabled ? _footerCopy : '');
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

    btn.textContent = ok ? '已复制!' : '复制失败';
    btn.classList.add('copied');
    setTimeout(() => { btn.textContent = '复制正文'; btn.classList.remove('copied'); }, 2000);
  };

  window.getClipboardHtml = function() { return _lastCopiedHtml; };

  // ---- UI Controls ----
  const ZOOM_STEPS = [50, 75, 100, 125, 150, 200];
  let zoomIdx = ZOOM_STEPS.length - 1;

  window.goBack = function() {
    document.getElementById('previewView').style.display = 'none';
    document.getElementById('uploadView').style.display = 'block';
    dropZone.classList.remove('processing');
    progress.style.display = 'none';
    progressFill.style.width = '0%';
    fileInput.value = '';
    // Reset zoom to default
    zoomIdx = ZOOM_STEPS.length - 1;
    document.getElementById('phoneFrame').style.maxWidth = (420 * ZOOM_STEPS[zoomIdx] / 100) + 'px';
    document.getElementById('zoomLabel').textContent = ZOOM_STEPS[zoomIdx] + '%';
  };

  window.zoom = function(dir) {
    zoomIdx = Math.max(0, Math.min(ZOOM_STEPS.length - 1, zoomIdx + dir));
    const pct = ZOOM_STEPS[zoomIdx];
    document.getElementById('phoneFrame').style.maxWidth = (420 * pct / 100) + 'px';
    document.getElementById('zoomLabel').textContent = pct + '%';
    if (window._positionTOC) window._positionTOC();
  };
}
