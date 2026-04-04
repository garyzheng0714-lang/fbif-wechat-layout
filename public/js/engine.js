// Shared WeChat formatting engine
// Handles: XML parsing, DOCX extraction, image upload, MD parsing, UI
const assetQuery = new URL(import.meta.url).search;
const imageUtilsModule = await import('./image-utils.mjs' + assetQuery);
const { inferImageMimeFromBase64, inferWechatImageType, looksLikeGifSource } = imageUtilsModule;
export { inferWechatImageType, looksLikeGifSource };

// ---- XML Namespaces ----
export const W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main';
export const R = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships';
export const WP = 'http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing';
export const A = 'http://schemas.openxmlformats.org/drawingml/2006/main';

// ---- XML Helpers ----
export function findAll(el, ns, tag) {
  const r = [];
  if (!el) return r;
  for (const c of el.children) if (c.localName === tag && c.namespaceURI === ns) r.push(c);
  return r;
}
export function findOne(el, ns, tag) {
  if (!el) return null;
  for (const c of el.children) if (c.localName === tag && c.namespaceURI === ns) return c;
  return null;
}
export function findDeep(el, ns, tag) {
  if (!el) return null;
  if (el.localName === tag && el.namespaceURI === ns) return el;
  for (const c of el.children) { const f = findDeep(c, ns, tag); if (f) return f; }
  return null;
}
export function wattr(el, name) {
  if (!el) return '';
  return el.getAttributeNS(W, name) || el.getAttribute('w:' + name) || el.getAttribute(name) || '';
}
export function rattr(el, name) {
  if (!el) return '';
  return el.getAttributeNS(R, name) || el.getAttribute('r:' + name) || '';
}
export function esc(t) {
  return t.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
export function escAttr(t) {
  return t.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// ---- Generic Paragraph Data Extraction ----
// Returns a superset of signals that any template can use for classification
export function extractParagraph(p, ridToFile, ridToUrl) {
  const pPr = findOne(p, W, 'pPr');
  let align = 'left', isList = false, hasOutlineLevel = false, hasHeadingStyle = false;

  if (pPr) {
    if (findOne(pPr, W, 'outlineLvl')) hasOutlineLevel = true;
    const ps = findOne(pPr, W, 'pStyle');
    if (ps && /heading/i.test(wattr(ps, 'val'))) hasHeadingStyle = true;
    const jc = findOne(pPr, W, 'jc');
    if (jc) align = wattr(jc, 'val') || 'left';
    if (findOne(pPr, W, 'numPr')) isList = true;
  }

  const runs = [];
  for (const child of p.children) {
    if (child.localName === 'r' && child.namespaceURI === W) {
      const rPr = findOne(child, W, 'rPr');
      let bold = false, hl = false, sz = '', color = '';
      if (rPr) {
        const bEl = findOne(rPr, W, 'b');
        if (bEl) { const v = wattr(bEl, 'val'); if (v !== '0' && v !== 'false') bold = true; }
        const rStyle = findOne(rPr, W, 'rStyle');
        if (rStyle && /strong/i.test(wattr(rStyle, 'val'))) bold = true;
        const shd = findOne(rPr, W, 'shd');
        if (shd) { const fill = wattr(shd, 'fill'); if (fill && !/^(auto|ffffff|FFFFFF)$/i.test(fill)) hl = true; }
        if (findOne(rPr, W, 'highlight')) hl = true;
        const s = findOne(rPr, W, 'sz');
        if (s) sz = wattr(s, 'val');
        const c = findOne(rPr, W, 'color');
        if (c) color = wattr(c, 'val');
      }
      const drw = findOne(child, W, 'drawing');
      if (drw) {
        const blip = findDeep(drw, A, 'blip');
        const embed = blip ? rattr(blip, 'embed') : '';
        const ext = findDeep(drw, WP, 'extent');
        let w = '100%';
        if (ext) {
          const cx = parseInt(ext.getAttribute('cx') || '0');
          if (cx > 0) w = Math.min(100, Math.round(cx / 5486400 * 100)) + '%';
        }
        runs.push({ type: 'img', file: ridToFile[embed] || '', w });
      } else {
        let txt = '';
        for (const t of child.getElementsByTagNameNS(W, 't')) txt += t.textContent || '';
        if (txt) runs.push({ type: 'txt', text: txt, bold, hl, sz, color });
      }
    } else if (child.localName === 'hyperlink' && child.namespaceURI === W) {
      const rid = rattr(child, 'id');
      const href = ridToUrl[rid] || '';
      for (const r of findAll(child, W, 'r')) {
        let txt = '';
        for (const t of r.getElementsByTagNameNS(W, 't')) txt += t.textContent || '';
        if (txt) runs.push({ type: 'link', text: txt, href });
      }
    }
  }

  const text = runs.filter(r => r.type !== 'img').map(r => r.text || '').join('');
  const textRuns = runs.filter(r => r.type === 'txt');

  return {
    align, runs, text,
    hasImg: runs.some(r => r.type === 'img'),
    isEmpty: !runs.some(r => r.type === 'img') && !text.trim(),
    isList, hasHL: runs.some(r => r.hl),
    hasOutlineLevel, hasHeadingStyle,
    allBold: textRuns.length > 0 && textRuns.every(r => r.bold),
    fontSizes: [...new Set(textRuns.filter(r => r.sz).map(r => r.sz))],
  };
}

// ---- DOCX Infrastructure ----
export async function parseDocx(file) {
  const zip = await JSZip.loadAsync(file);

  // Validate DOCX structure
  const relsEntry = zip.file('word/_rels/document.xml.rels');
  const docEntry = zip.file('word/document.xml');
  if (!relsEntry) throw new Error('无效的 DOCX 文件：缺少 document.xml.rels');
  if (!docEntry) throw new Error('无效的 DOCX 文件：缺少 document.xml');

  // Parse relationships
  const relsXml = await relsEntry.async('string');
  const relsDom = new DOMParser().parseFromString(relsXml, 'text/xml');
  const ridToFile = {}, ridToUrl = {};
  for (const rel of relsDom.querySelectorAll('Relationship')) {
    const rid = rel.getAttribute('Id'), target = rel.getAttribute('Target');
    const rtype = rel.getAttribute('Type') || '';
    if (rtype.includes('image')) ridToFile[rid] = target.replace('media/', '');
    else if (rtype.includes('hyperlink')) ridToUrl[rid] = (target || '').replace(/&amp;/g, '&');
  }

  // Load images as base64 (concurrently)
  const imgCache = {};
  await Promise.all(Object.values(ridToFile).map(async (fn) => {
    const entry = zip.file('word/media/' + fn);
    if (entry) {
      const data = await entry.async('base64');
      const ext = fn.split('.').pop().toLowerCase();
      const mime = inferImageMimeFromBase64(data, ext);
      imgCache[fn] = 'data:' + mime + ';base64,' + data;
    }
  }));

  // Parse document.xml (NO image upload here — done after preview)
  const docXml = await docEntry.async('string');

  const docDom = new DOMParser().parseFromString(docXml, 'text/xml');
  const body = findOne(docDom.documentElement, W, 'body');
  const allParas = findAll(body, W, 'p');
  const paragraphs = allParas.map(p => extractParagraph(p, ridToFile, ridToUrl));

  return { paragraphs, imgCache };
}

// Image upload functions removed — backgroundUploadImages handles all uploads

// ---- Markdown Utilities ----
export function parseMdRuns(text) {
  text = text.replace(/\s*\n\s*/g, '');
  const parts = text.split(/\*\*/);
  const runs = [];
  for (let i = 0; i < parts.length; i++) {
    if (!parts[i]) continue;
    runs.push({ type: 'txt', text: parts[i], bold: i % 2 === 1 });
  }
  return runs;
}

export function parseMdFrontmatter(text) {
  let author = '', content = text;
  const m = text.match(/^---\s*\n([\s\S]*?)\n---\s*\n([\s\S]*)$/);
  if (m) {
    content = m[2];
    const am = m[1].match(/author:\s*"?([^"\n]+)"?/);
    if (am) author = am[1].trim();
  }
  return { author, content };
}

// ---- Footer ----
const footerReady = fetch('/footer.html').then(r => r.ok ? r.text() : '').catch(() => '');

// ---- App Initialization ----
export function initApp(template) {
  document.getElementById('pageTitle').textContent = template.name;
  document.getElementById('pageSubtitle').textContent = template.description;
  document.getElementById('fileHint').textContent =
    '支持 ' + template.formats.join('、') + ' 格式，图片会上传至 CDN 以兼容微信';
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
    _convLog.length = 0; // reset log for each new file
    logConv('info', '开始转化', { file: file.name, size: (file.size / 1024).toFixed(1) + 'KB' });
    errorEl.style.display = 'none';
    dropZone.classList.add('processing');
    progress.style.display = 'block';
    progressFill.style.width = '50%';
    progressText.textContent = '正在解析...';
    try {
      const t0 = performance.now();

      // Step 1: Parse and format (NO network calls — instant)
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

      // Step 2: Show preview IMMEDIATELY (with base64 images for display)
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

  // Two-layer rendering: display (original images) + push (WeChat CDN images)
  let _articleDisplay = '';  // original URLs — for preview (no watermarks)
  let _articlePush = '';     // mmbiz URLs — for push to WeChat
  let _footerDisplay = '';
  let _footerPush = '';
  let _uploadPromise = null;
  let _uploadDone = false;
  let _uploadTotal = 0;
  let _uploadCurrent = 0;
  let _uploadFailed = 0;

  function showPreview(title, articleContent, footerContent, stats) {
    _articleDisplay = articleContent;
    _articlePush = articleContent;
    _footerDisplay = footerContent;
    _footerPush = footerContent;

    document.getElementById('uploadView').style.display = 'none';
    document.getElementById('previewView').style.display = 'block';

    // Title — set in toolbar and hidden input (for push)
    document.getElementById('titleInput').value = title;
    document.getElementById('previewTitleDisplay').textContent = title;

    // Render with original images (no WeChat watermarks)
    document.getElementById('contentArea').innerHTML = _articleDisplay + '\n' + _footerDisplay;
    document.getElementById('statsBar').textContent = stats;

    // Thumbnails and TOC are populated by MutationObserver in app.html

    // Footer update
    window._updateFooter = function(newHtml) {
      if (typeof newHtml === 'string' && newHtml !== '') {
        _footerDisplay = newHtml;
        _footerPush = newHtml;
      }
      const enabled = document.getElementById('footerEnabled').checked;
      document.getElementById('contentArea').innerHTML = _articleDisplay + '\n' + (enabled ? _footerDisplay : '');
    };

    // Step 3: Background upload
    const statsBar = document.getElementById('statsBar');
    _uploadDone = false;
    _uploadFailed = 0;
    updateButtonStates();
    _uploadPromise = backgroundUploadImages(statsBar, stats);
  }

  function updateButtonStates() {
    const draftBtn = document.getElementById('draftBtn');
    const copyBtn = document.getElementById('copyBtn');
    if (!draftBtn) return;

    if (!_uploadDone && _uploadTotal > 0) {
      draftBtn.textContent = '上传中 (' + _uploadCurrent + '/' + _uploadTotal + ')...';
      draftBtn.disabled = true;
      copyBtn.title = '图片上传中，粘贴后部分图片可能缺失';
    } else {
      draftBtn.disabled = false;
      if (_uploadFailed > 0) {
        draftBtn.textContent = '推送到公众号草稿箱 (' + _uploadFailed + '张失败)';
      } else {
        draftBtn.textContent = '推送到公众号草稿箱';
      }
      copyBtn.title = '';
    }
  }

  // ---- Conversion Log ----
  const _convLog = [];
  function logConv(level, msg, detail) {
    const entry = { time: new Date().toISOString(), level, msg };
    if (detail) entry.detail = detail;
    _convLog.push(entry);
    const prefix = level === 'error' ? '❌' : level === 'warn' ? '⚠️' : '✅';
    console.log('[排版日志] ' + prefix + ' ' + msg, detail || '');
    // Push to UI log panel
    if (window._pushLog) window._pushLog(level, msg, detail);
  }
  // Expose log for debugging — type getConvLog() in console
  window.getConvLog = function() { return JSON.parse(JSON.stringify(_convLog)); };

  function formatRatio(v) {
    if (!v || !isFinite(v) || v <= 0) return '1';
    return v.toFixed(6).replace(/0+$/, '').replace(/\.$/, '');
  }

  function collectPreviewImageMeta(rootEl) {
    if (!rootEl) return [];
    const out = [];
    for (const img of rootEl.querySelectorAll('img')) {
      const w = img.naturalWidth || img.width || 0;
      const h = img.naturalHeight || img.height || 0;
      const ratio = (w > 0 && h > 0) ? (h / w) : 1;
      out.push({ width: w, height: h, ratio });
    }
    return out;
  }

  function normalizePushHtmlForWechat(html, options = {}) {
    const metaList = Array.isArray(options.metaList) ? options.metaList : [];
    const metaOffset = Number.isFinite(options.metaOffset) ? options.metaOffset : 0;
    const doc = new DOMParser().parseFromString('<div id="root">' + html + '</div>', 'text/html');
    const root = doc.getElementById('root');
    if (!root) {
      return {
        html,
        stats: { total: 0, taggedGif: 0, likelyGif: 0, patchedGif: 0, dataUri: 0, missingSrc: 0 },
      };
    }

    const stats = { total: 0, taggedGif: 0, likelyGif: 0, patchedGif: 0, dataUri: 0, missingSrc: 0 };
    let localIndex = 0;
    for (const img of root.querySelectorAll('img')) {
      const globalIndex = metaOffset + localIndex;
      localIndex++;
      stats.total++;
      const src = (img.getAttribute('src') || '').trim();
      if (!src) {
        stats.missingSrc++;
        continue;
      }
      let normalizedSrc = src;
      if (normalizedSrc.startsWith('http://mmbiz.qpic.cn/')) {
        normalizedSrc = 'https://' + normalizedSrc.slice('http://'.length);
        img.setAttribute('src', normalizedSrc);
      }
      if (normalizedSrc.startsWith('data:image/')) stats.dataUri++;

      // WeChat editor compatibility: keep key metadata fields and class names.
      img.removeAttribute('referrerpolicy');
      img.setAttribute('data-src', normalizedSrc);
      const cls = (img.getAttribute('class') || '').split(/\s+/).filter(Boolean);
      if (!cls.includes('rich_pages')) cls.push('rich_pages');
      if (!cls.includes('wxw-img')) cls.push('wxw-img');
      img.setAttribute('class', cls.join(' '));
      img.setAttribute('alt', '图片');

      const meta = metaList[globalIndex] || {};
      const width = Math.max(1, Number(meta.width) || Number(img.getAttribute('data-w')) || 640);
      const ratio = Number(meta.ratio) > 0 ? Number(meta.ratio) :
        (Number(meta.height) > 0 ? Number(meta.height) / width : Number(img.getAttribute('data-ratio')) || 1);
      const ratioText = formatRatio(ratio);
      const height = Math.max(1, Math.round(width * (ratio > 0 ? ratio : 1)));
      img.setAttribute('data-ratio', ratioText);
      img.setAttribute('data-w', String(width));
      img.setAttribute('data-s', `${height},${width}`);
      img.setAttribute('data-index', String(globalIndex));
      img.setAttribute('data-report-img-idx', String(globalIndex));
      img.setAttribute('_width', String(width));
      img.setAttribute('data-fail', '0');
      if (!img.getAttribute('data-imgfileid')) {
        img.setAttribute('data-imgfileid', String(100000000 + Math.floor(Math.random() * 900000000)));
      }
      if (!img.getAttribute('data-original-style')) {
        img.setAttribute('data-original-style', img.getAttribute('style') || '');
      }

      const tagged = (img.getAttribute('data-type') || '').toLowerCase() === 'gif';
      const inferredType = inferWechatImageType(normalizedSrc);
      if (inferredType) img.setAttribute('data-type', inferredType);
      const likelyGif = inferredType === 'gif' || looksLikeGifSource(normalizedSrc);
      if (tagged) stats.taggedGif++;
      if (likelyGif) {
        stats.likelyGif++;
        if (!tagged) {
          img.setAttribute('data-type', 'gif');
          stats.patchedGif++;
          stats.taggedGif++;
        }
      }
    }
    return { html: root.innerHTML, stats };
  }

  function mergeNormalizeStats(a, b) {
    return {
      total: a.total + b.total,
      taggedGif: a.taggedGif + b.taggedGif,
      likelyGif: a.likelyGif + b.likelyGif,
      patchedGif: a.patchedGif + b.patchedGif,
      dataUri: a.dataUri + b.dataUri,
      missingSrc: a.missingSrc + b.missingSrc,
    };
  }

  function getReadyDraftPayload(enabled, previewMeta = []) {
    const articleNorm = normalizePushHtmlForWechat(_articlePush, { metaList: previewMeta, metaOffset: 0 });
    _articlePush = articleNorm.html;

    const empty = { total: 0, taggedGif: 0, likelyGif: 0, patchedGif: 0, dataUri: 0, missingSrc: 0 };
    let footerNorm = { html: '', stats: empty };
    if (enabled) {
      footerNorm = normalizePushHtmlForWechat(_footerPush, {
        metaList: previewMeta,
        metaOffset: articleNorm.stats.total,
      });
      _footerPush = footerNorm.html;
    }

    const stats = mergeNormalizeStats(articleNorm.stats, footerNorm.stats);
    if (stats.patchedGif > 0) {
      logConv('warn', '自动补齐GIF标记', {
        patched: stats.patchedGif,
        likelyGif: stats.likelyGif,
        taggedGif: stats.taggedGif,
      });
    }

    return {
      html: _articlePush + '\n' + (enabled ? _footerPush : ''),
      stats,
    };
  }

  function normalizeCopyHtmlForWechat(html) {
    const doc = new DOMParser().parseFromString('<div id="root">' + html + '</div>', 'text/html');
    const root = doc.getElementById('root');
    if (!root) return { html, stats: { total: 0, gif: 0, missingSrc: 0 } };

    const stats = { total: 0, gif: 0, missingSrc: 0 };
    for (const img of root.querySelectorAll('img')) {
      stats.total++;
      let src = (img.getAttribute('src') || '').trim();
      if (!src) {
        stats.missingSrc++;
        continue;
      }
      if (src.startsWith('http://mmbiz.qpic.cn/')) {
        src = 'https://' + src.slice('http://'.length);
        img.setAttribute('src', src);
      }

      // Strip only non-essential WeChat metadata; keep class, data-w, data-ratio
      // which the WeChat editor needs to recognise images as its own content
      for (const attr of [
        'data-s',
        'data-index',
        'data-report-img-idx',
        '_width',
        'data-fail',
        'data-imgfileid',
        'data-original-style',
        'data-gif-singleurl',
        'data-cover',
      ]) {
        img.removeAttribute(attr);
      }

      // Ensure WeChat editor image classes are present
      const cls = (img.getAttribute('class') || '').split(/\s+/).filter(Boolean);
      if (!cls.includes('rich_pages')) cls.push('rich_pages');
      if (!cls.includes('wxw-img')) cls.push('wxw-img');
      img.setAttribute('class', cls.join(' '));

      const inferred = inferWechatImageType(src);
      const isGif = inferred === 'gif' || looksLikeGifSource(src);
      img.setAttribute('data-src', src);
      img.removeAttribute('loading');
      img.removeAttribute('decoding');
      img.removeAttribute('referrerpolicy');

      // Set data-type for all detected formats (not just GIF)
      if (isGif) {
        stats.gif++;
        img.setAttribute('data-type', 'gif');
      } else if (inferred) {
        img.setAttribute('data-type', inferred);
      }
    }
    return { html: root.innerHTML, stats };
  }

  async function copyByClipboardEvent(html, plainText) {
    const handler = function(e) {
      e.clipboardData.setData('text/html', html);
      e.clipboardData.setData('text/plain', plainText);
      e.preventDefault();
    };
    document.addEventListener('copy', handler, true);
    const temp = document.createElement('div');
    temp.style.position = 'fixed';
    temp.style.left = '-99999px';
    temp.style.top = '0';
    temp.innerHTML = html;
    document.body.appendChild(temp);
    try {
      const range = document.createRange();
      range.selectNodeContents(temp);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
      const ok = document.execCommand('copy');
      sel.removeAllRanges();
      return !!ok;
    } finally {
      document.body.removeChild(temp);
      document.removeEventListener('copy', handler, true);
    }
  }

  async function copyByNativeTempSelection(html) {
    const temp = document.createElement('div');
    temp.style.position = 'fixed';
    temp.style.left = '-99999px';
    temp.style.top = '0';
    temp.innerHTML = html;
    document.body.appendChild(temp);
    try {
      const range = document.createRange();
      range.selectNodeContents(temp);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
      const ok = document.execCommand('copy');
      sel.removeAllRanges();
      return !!ok;
    } finally {
      document.body.removeChild(temp);
    }
  }

  async function copyByClipboardApi(html, plainText) {
    if (!(navigator.clipboard && typeof ClipboardItem !== 'undefined')) return false;
    await navigator.clipboard.write([new ClipboardItem({
      'text/html': new Blob([html], { type: 'text/html' }),
      'text/plain': new Blob([plainText], { type: 'text/plain' }),
    })]);
    return true;
  }

  function getReadyCopyPayload(enabled, previewMeta = []) {
    const base = getReadyDraftPayload(enabled, previewMeta);
    const copyNorm = normalizeCopyHtmlForWechat(base.html);
    return {
      html: copyNorm.html,
      stats: base.stats,
    };
  }

  async function backgroundUploadImages(statsBar, originalStats) {
    const allHtml = _articlePush + '\n' + _footerPush;

    // Collect data: URIs (DOCX images + footer)
    const base64Map = {};
    const b64Re = /src="(data:image\/[^"]+)"/g;
    let m3;
    while ((m3 = b64Re.exec(allHtml)) !== null) {
      base64Map['img_' + Object.keys(base64Map).length] = m3[1];
    }

    // Collect http URLs (MD external images) — re-upload ALL including mmbiz
    // Even mmbiz URLs must be re-uploaded to the user's own account CDN,
    // otherwise cross-account images lose GIF animation when pasted into editor
    const httpUrls = [];
    const httpRe = /src="(https?:\/\/[^"]+)"/g;
    while ((m3 = httpRe.exec(allHtml)) !== null) {
      httpUrls.push(m3[1]);
    }

    logConv('info', '图片扫描完成', {
      base64: Object.keys(base64Map).length,
      httpUrls: httpUrls.length,
    });

    _uploadTotal = Object.keys(base64Map).length + httpUrls.length;
    _uploadCurrent = 0;
    _uploadFailed = 0;

    if (_uploadTotal === 0) { _uploadDone = true; updateButtonStates(); return; }

    statsBar.textContent = '上传图片 (0/' + _uploadTotal + ')...';
    updateButtonStates();
    let done = 0;
    const failedDetails = [];

    function updateProgress() {
      _uploadCurrent = done;
      statsBar.textContent = '上传图片 (' + done + '/' + _uploadTotal + ')...';
      updateButtonStates();
    }

    // Upload base64 images in batches of 10 — only update PUSH layer, not display
    const b64Entries = Object.entries(base64Map);
    for (let i = 0; i < b64Entries.length; i += 10) {
      const batch = Object.fromEntries(b64Entries.slice(i, i + 10));
      try {
        const resp = await fetch('/api/wechat-upload', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ base64_images: batch })
        });
        if (resp.ok) {
          const { results } = await resp.json();
          for (const [key, wechatUrl] of Object.entries(results)) {
            if (!wechatUrl) {
              _uploadFailed++;
              const mime = (base64Map[key] || '').split(';')[0].split(':')[1] || 'unknown';
              failedDetails.push({ type: 'base64', key, mime });
              logConv('error', 'base64图片上传失败', { key, mime });
            } else {
              _articlePush = _articlePush.split(base64Map[key]).join(wechatUrl);
              _footerPush = _footerPush.split(base64Map[key]).join(wechatUrl);
              logConv('info', 'base64图片上传成功', { key, cdn: wechatUrl.substring(0, 60) + '...' });
            }
            done++;
          }
        } else {
          const errText = await resp.text().catch(() => 'unknown');
          logConv('error', 'base64批次上传HTTP错误', { status: resp.status, body: errText.substring(0, 200) });
          done += Object.keys(batch).length; _uploadFailed += Object.keys(batch).length;
          Object.keys(batch).forEach(k => failedDetails.push({ type: 'base64', key: k, error: 'HTTP ' + resp.status }));
        }
        updateProgress();
      } catch (e) {
        logConv('error', 'base64批次上传网络错误', { error: e.message });
        done += Object.keys(batch).length; _uploadFailed += Object.keys(batch).length;
        Object.keys(batch).forEach(k => failedDetails.push({ type: 'base64', key: k, error: e.message }));
        updateProgress();
      }
    }

    // Upload http URLs in batches of 3 — smaller batches give more responsive
    // progress updates and avoid long stalls at 0/N when the server re-uploads
    for (let i = 0; i < httpUrls.length; i += 3) {
      const batch = httpUrls.slice(i, i + 3);
      try {
        const resp = await fetch('/api/wechat-upload', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ urls: batch })
        });
        if (resp.ok) {
          const { results } = await resp.json();
          for (const [origUrl, wechatUrl] of Object.entries(results)) {
            if (!wechatUrl) {
              _uploadFailed++;
              failedDetails.push({ type: 'url', url: origUrl });
              logConv('error', 'URL图片上传失败', { url: origUrl.substring(0, 80) });
            } else {
              _articlePush = _articlePush.split(origUrl).join(wechatUrl);
              _footerPush = _footerPush.split(origUrl).join(wechatUrl);
              logConv('info', 'URL图片上传成功', { url: origUrl.substring(0, 60), cdn: wechatUrl.substring(0, 60) + '...' });
            }
            done++;
          }
        } else {
          const errText = await resp.text().catch(() => 'unknown');
          logConv('error', 'URL批次上传HTTP错误', { status: resp.status, body: errText.substring(0, 200) });
          done += batch.length; _uploadFailed += batch.length;
          batch.forEach(u => failedDetails.push({ type: 'url', url: u, error: 'HTTP ' + resp.status }));
        }
        updateProgress();
      } catch (e) {
        logConv('error', 'URL批次上传网络错误', { error: e.message });
        done += batch.length; _uploadFailed += batch.length;
        batch.forEach(u => failedDetails.push({ type: 'url', url: u, error: e.message }));
        updateProgress();
      }
    }

    // Final pass: normalize GIF attributes after URL replacements
    const previewMeta = collectPreviewImageMeta(document.getElementById('contentArea'));
    const articleNorm = normalizePushHtmlForWechat(_articlePush, { metaList: previewMeta, metaOffset: 0 });
    const footerNorm = normalizePushHtmlForWechat(_footerPush, {
      metaList: previewMeta,
      metaOffset: articleNorm.stats.total,
    });
    _articlePush = articleNorm.html;
    _footerPush = footerNorm.html;
    const finalNorm = mergeNormalizeStats(articleNorm.stats, footerNorm.stats);
    if (finalNorm.patchedGif > 0) {
      logConv('warn', '上传后自动补齐GIF标记', {
        patched: finalNorm.patchedGif,
        likelyGif: finalNorm.likelyGif,
        taggedGif: finalNorm.taggedGif,
      });
    }

    _uploadDone = true;
    _uploadCurrent = done;
    updateButtonStates();
    if (_uploadFailed > 0) {
      statsBar.textContent = originalStats + ' | ' + _uploadFailed + '张图片上传失败';
      logConv('warn', '上传完成，' + _uploadFailed + '张失败', failedDetails);
    } else {
      statsBar.textContent = originalStats + ' | 图片已同步';
      logConv('info', '全部图片上传成功', { total: done });
    }
  }

  // Expose global handlers for onclick attributes
  const ZOOM_STEPS = [50, 75, 100, 125, 150, 200];
  let zoomIdx = ZOOM_STEPS.length - 1;

  window.goBack = function() {
    document.getElementById('previewView').style.display = 'none';
    document.getElementById('uploadView').style.display = 'block';
    dropZone.classList.remove('processing');
    progress.style.display = 'none';
    progressFill.style.width = '0%';
    fileInput.value = '';
  };

  window.zoom = function(dir) {
    zoomIdx = Math.max(0, Math.min(ZOOM_STEPS.length - 1, zoomIdx + dir));
    const pct = ZOOM_STEPS[zoomIdx];
    document.getElementById('phoneFrame').style.maxWidth = (420 * pct / 100) + 'px';
    document.getElementById('zoomLabel').textContent = pct + '%';
    if (window._positionTOC) window._positionTOC();
  };

  // ---- Clipboard Diagnostics ----
  // Stores the last copied HTML for inspection via inspectClipboard() or the UI panel
  let _lastCopiedHtml = '';
  let _lastCopyMethod = '';

  function analyzeHtml(html) {
    const doc = new DOMParser().parseFromString('<div id="r">' + html + '</div>', 'text/html');
    const root = doc.getElementById('r');
    const imgs = root ? root.querySelectorAll('img') : [];
    const details = [];
    for (const img of imgs) {
      const src = img.getAttribute('src') || '';
      const dataSrc = img.getAttribute('data-src') || '';
      const dataType = img.getAttribute('data-type') || '';
      const cls = img.getAttribute('class') || '';
      const isMmbiz = src.includes('mmbiz.qpic.cn');
      const isDataUri = src.startsWith('data:');
      details.push({
        src: src.substring(0, 100) + (src.length > 100 ? '...' : ''),
        dataSrc: dataSrc ? 'yes' : 'NO',
        dataType: dataType || 'NONE',
        class: cls || 'NONE',
        isMmbiz, isDataUri,
        hasSrc: !!src,
      });
    }
    return {
      htmlLength: html.length,
      imgCount: imgs.length,
      gifCount: details.filter(d => d.dataType === 'gif').length,
      mmbizCount: details.filter(d => d.isMmbiz).length,
      dataUriCount: details.filter(d => d.isDataUri).length,
      missingSrcCount: details.filter(d => !d.hasSrc).length,
      images: details,
    };
  }

  // Console API: type inspectClipboard() to get full diagnostics
  window.inspectClipboard = function() {
    if (!_lastCopiedHtml) { console.log('还没有复制过内容'); return null; }
    const analysis = analyzeHtml(_lastCopiedHtml);
    console.log('%c[剪贴板诊断]', 'color: #0070C0; font-weight: bold;');
    console.log('复制方法:', _lastCopyMethod);
    console.log('HTML 大小:', (analysis.htmlLength / 1024).toFixed(1) + 'KB');
    console.log('图片总数:', analysis.imgCount);
    console.log('GIF 数:', analysis.gifCount);
    console.log('mmbiz CDN:', analysis.mmbizCount);
    console.log('data:URI (未上传):', analysis.dataUriCount);
    console.log('缺失 src:', analysis.missingSrcCount);
    console.table(analysis.images);
    console.log('完整 HTML (前2000字符):', _lastCopiedHtml.substring(0, 2000));
    return analysis;
  };

  // Console API: type getClipboardHtml() to get the raw HTML string
  window.getClipboardHtml = function() { return _lastCopiedHtml; };

  window.copyContent = async function() {
    const content = document.getElementById('contentArea');
    const btn = document.getElementById('copyBtn');

    // Wait for background image upload to finish — must use CDN URLs
    if (_uploadPromise && !_uploadDone) {
      btn.textContent = '等待图片上传...';
      await _uploadPromise;
    }
    if (_uploadFailed > 0) {
      btn.textContent = '有图片上传失败，无法复制';
      setTimeout(() => { btn.textContent = '复制正文'; }, 2400);
      return;
    }

    // Use PUSH layer (WeChat CDN URLs), not display layer (base64/original)
    const enabled = document.getElementById('footerEnabled').checked;
    const payload = getReadyCopyPayload(enabled, collectPreviewImageMeta(content));
    if (payload.stats.dataUri > 0 || payload.stats.missingSrc > 0) {
      logConv('error', '复制前校验失败', payload.stats);
      btn.textContent = '图片未就绪，无法复制';
      setTimeout(() => { btn.textContent = '复制正文'; }, 2400);
      return;
    }
    const html = payload.html;

    const plainText = content.textContent || '';
    let ok = false;
    _lastCopyMethod = '';

    // Clipboard API first — writes exact HTML blob without DOM rendering,
    // most reliable for preserving all image tags and attributes
    try {
      ok = await copyByClipboardApi(html, plainText);
      if (ok) _lastCopyMethod = 'ClipboardAPI';
    } catch {}

    if (!ok) {
      try {
        ok = await copyByClipboardEvent(html, plainText);
        if (ok) _lastCopyMethod = 'ClipboardEvent';
      } catch {}
    }

    if (!ok) {
      try {
        ok = await copyByNativeTempSelection(html);
        if (ok) _lastCopyMethod = 'NativeTempSelection';
      } catch {}
    }

    // Save for diagnostics
    _lastCopiedHtml = html;
    const analysis = analyzeHtml(html);
    logConv(ok ? 'info' : 'error', ok ? '复制完成' : '复制失败', {
      method: _lastCopyMethod,
      htmlSize: (analysis.htmlLength / 1024).toFixed(1) + 'KB',
      imgCount: analysis.imgCount,
      gifCount: analysis.gifCount,
      mmbizCount: analysis.mmbizCount,
      dataUriCount: analysis.dataUriCount,
      missingSrcCount: analysis.missingSrcCount,
    });

    // Update diagnostic panel if visible
    if (window._updateDiagPanel) window._updateDiagPanel(analysis);

    btn.textContent = ok ? '已复制!' : '复制失败';
    btn.classList.add('copied');
    setTimeout(() => { btn.textContent = '复制正文'; btn.classList.remove('copied'); }, 2000);
  };

  // Push to WeChat drafts (images already on WeChat CDN, push is instant)
  window.pushToDraft = async function() {
    const btn = document.getElementById('draftBtn');
    const title = document.getElementById('titleInput').value.trim() || '未命名文章';

    // Get cover from sidebar
    const coverEl = document.getElementById('coverImg');
    const coverSrc = coverEl ? coverEl.src : '';
    const cover = (coverSrc.startsWith('http') || coverSrc.startsWith('data:')) ? coverSrc : null;

    btn.textContent = '推送中...';
    btn.disabled = true;

    try {
      // Wait for background image upload to finish
      if (_uploadPromise) {
        btn.textContent = '等待图片上传完成...';
        await _uploadPromise;
      }
      if (_uploadFailed > 0) {
        throw new Error('仍有' + _uploadFailed + '张图片上传失败');
      }

      // Use push layer (WeChat CDN URLs), not display layer
      const enabled = document.getElementById('footerEnabled').checked;
      const payload = getReadyDraftPayload(enabled, collectPreviewImageMeta(document.getElementById('contentArea')));
      if (payload.stats.dataUri > 0 || payload.stats.missingSrc > 0) {
        throw new Error('图片未就绪，请重新上传');
      }
      const content = payload.html;

      const resp = await fetch('/api/wechat-draft', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, content, cover }),
      });
      const data = await resp.json();

      if (!resp.ok) throw new Error(data.error || '推送失败');

      btn.textContent = '推送成功!';
      btn.classList.add('pushed');
      setTimeout(() => {
        window.open('https://mp.weixin.qq.com/', '_blank');
        btn.textContent = '推送到公众号草稿箱';
        btn.classList.remove('pushed');
        btn.disabled = false;
      }, 1000);
    } catch (err) {
      btn.textContent = '失败: ' + err.message;
      setTimeout(() => { btn.textContent = '推送到公众号草稿箱'; btn.disabled = false; }, 3000);
    }
  };
}
