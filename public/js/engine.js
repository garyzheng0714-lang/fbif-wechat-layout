// Shared WeChat formatting engine
// Handles: XML parsing, DOCX extraction, MD parsing, copy, UI
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

  const relsEntry = zip.file('word/_rels/document.xml.rels');
  const docEntry = zip.file('word/document.xml');
  if (!relsEntry) throw new Error('无效的 DOCX 文件：缺少 document.xml.rels');
  if (!docEntry) throw new Error('无效的 DOCX 文件：缺少 document.xml');

  const relsXml = await relsEntry.async('string');
  const relsDom = new DOMParser().parseFromString(relsXml, 'text/xml');
  const ridToFile = {}, ridToUrl = {};
  for (const rel of relsDom.querySelectorAll('Relationship')) {
    const rid = rel.getAttribute('Id'), target = rel.getAttribute('Target');
    const rtype = rel.getAttribute('Type') || '';
    if (rtype.includes('image')) ridToFile[rid] = target.replace('media/', '');
    else if (rtype.includes('hyperlink')) ridToUrl[rid] = (target || '').replace(/&amp;/g, '&');
  }

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

  const docXml = await docEntry.async('string');
  const docDom = new DOMParser().parseFromString(docXml, 'text/xml');
  const body = findOne(docDom.documentElement, W, 'body');
  const allParas = findAll(body, W, 'p');
  const paragraphs = allParas.map(p => extractParagraph(p, ridToFile, ridToUrl));

  return { paragraphs, imgCache };
}

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

  // Article HTML — display layer always uses original images (base64 or mmbiz URLs).
  // Copy layer: mmbiz URLs work directly in WeChat editor; base64 must be uploaded first.
  let _articleHtml = '';   // display (original)
  let _articleCopy = '';   // copy (base64 replaced with CDN URLs after upload)
  let _footerHtml = '';
  let _uploadPromise = null;
  let _uploadDone = false;

  function showPreview(title, articleContent, footerContent, stats) {
    _articleHtml = articleContent;
    _articleCopy = articleContent;
    _footerHtml = footerContent;

    document.getElementById('uploadView').style.display = 'none';
    document.getElementById('previewView').style.display = 'block';

    document.getElementById('titleInput').value = title;
    document.getElementById('previewTitleDisplay').textContent = title;

    document.getElementById('contentArea').innerHTML = _articleHtml + '\n' + _footerHtml;
    document.getElementById('statsBar').textContent = stats;

    window._updateFooter = function(newHtml) {
      if (typeof newHtml === 'string' && newHtml !== '') _footerHtml = newHtml;
      const enabled = document.getElementById('footerEnabled').checked;
      document.getElementById('contentArea').innerHTML = _articleHtml + '\n' + (enabled ? _footerHtml : '');
    };

    // Upload base64 images in background (DOCX only — Markdown uses URLs that work directly)
    _uploadDone = false;
    _uploadPromise = uploadBase64Images(document.getElementById('statsBar'), stats);
  }

  async function uploadBase64Images(statsBar, originalStats) {
    // Collect data: URIs from article + footer
    const allHtml = _articleCopy + '\n' + _footerHtml;
    const base64Map = {};
    const re = /src="(data:image\/[^"]+)"/g;
    let m;
    while ((m = re.exec(allHtml)) !== null) {
      base64Map['img_' + Object.keys(base64Map).length] = m[1];
    }

    const total = Object.keys(base64Map).length;
    if (total === 0) {
      // No base64 images (Markdown file) — copy is ready immediately
      _uploadDone = true;
      logConv('info', '无需上传图片（全部为 URL）');
      return;
    }

    logConv('info', '开始上传 DOCX 图片', { total });
    statsBar.textContent = '上传图片 (0/' + total + ')...';
    let done = 0, failed = 0;

    // Upload in parallel, 5 at a time
    const entries = Object.entries(base64Map);
    const CONCURRENCY = 5;
    let next = 0;

    async function worker() {
      while (next < entries.length) {
        const [key, dataUri] = entries[next++];
        try {
          const resp = await fetch('/api/wechat-upload', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ base64_images: { [key]: dataUri } })
          });
          if (resp.ok) {
            const { results } = await resp.json();
            const cdnUrl = results[key] || Object.values(results)[0];
            if (cdnUrl) {
              _articleCopy = _articleCopy.split(dataUri).join(cdnUrl);
              logConv('info', '图片上传成功', { key, cdn: cdnUrl.substring(0, 60) + '...' });
            } else {
              failed++;
              logConv('error', '图片上传失败（空URL）', { key });
            }
          } else {
            failed++;
            logConv('error', '图片上传HTTP错误', { key, status: resp.status });
          }
        } catch (e) {
          failed++;
          logConv('error', '图片上传网络错误', { key, error: e.message });
        }
        done++;
        statsBar.textContent = '上传图片 (' + done + '/' + total + ')...';
      }
    }

    await Promise.all(Array.from({ length: Math.min(CONCURRENCY, entries.length) }, () => worker()));

    _uploadDone = true;
    if (failed > 0) {
      statsBar.textContent = originalStats + ' | ' + failed + '张图片上传失败';
      logConv('warn', '上传完成，' + failed + '张失败');
    } else {
      statsBar.textContent = originalStats + ' | 图片已就绪';
      logConv('info', '全部图片上传成功', { total });
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
    if (window._pushLog) window._pushLog(level, msg, detail);
  }
  window.getConvLog = function() { return JSON.parse(JSON.stringify(_convLog)); };

  // ---- Copy ----
  async function copyByClipboardApi(html, plainText) {
    if (!(navigator.clipboard && typeof ClipboardItem !== 'undefined')) return false;
    await navigator.clipboard.write([new ClipboardItem({
      'text/html': new Blob([html], { type: 'text/html' }),
      'text/plain': new Blob([plainText], { type: 'text/plain' }),
    })]);
    return true;
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

  let _lastCopiedHtml = '';

  window.copyContent = async function() {
    const content = document.getElementById('contentArea');
    const btn = document.getElementById('copyBtn');

    // Wait for base64 upload if DOCX images are still uploading
    if (_uploadPromise && !_uploadDone) {
      btn.textContent = '等待图片上传...';
      await _uploadPromise;
    }

    // Use copy layer — base64 replaced with CDN URLs, mmbiz URLs kept as-is
    const enabled = document.getElementById('footerEnabled').checked;
    const html = _articleCopy + '\n' + (enabled ? _footerHtml : '');
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
  };

  window.zoom = function(dir) {
    zoomIdx = Math.max(0, Math.min(ZOOM_STEPS.length - 1, zoomIdx + dir));
    const pct = ZOOM_STEPS[zoomIdx];
    document.getElementById('phoneFrame').style.maxWidth = (420 * pct / 100) + 'px';
    document.getElementById('zoomLabel').textContent = pct + '%';
    if (window._positionTOC) window._positionTOC();
  };
}
