// Shared WeChat formatting engine
// Handles: XML parsing, DOCX extraction, image upload, MD parsing, UI

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
  const mimeMap = { png: 'image/png', webp: 'image/webp', gif: 'image/gif', svg: 'image/svg+xml' };
  await Promise.all(Object.values(ridToFile).map(async (fn) => {
    const entry = zip.file('word/media/' + fn);
    if (entry) {
      const data = await entry.async('base64');
      const ext = fn.split('.').pop().toLowerCase();
      imgCache[fn] = 'data:' + (mimeMap[ext] || 'image/jpeg') + ';base64,' + data;
    }
  }));

  // Upload images to OSS and parse document.xml concurrently
  const [, docXml] = await Promise.all([
    uploadBase64Images(imgCache),
    docEntry.async('string'),
  ]);

  const docDom = new DOMParser().parseFromString(docXml, 'text/xml');
  const body = findOne(docDom.documentElement, W, 'body');
  const allParas = findAll(body, W, 'p');
  const paragraphs = allParas.map(p => extractParagraph(p, ridToFile, ridToUrl));

  return { paragraphs, imgCache };
}

// ---- Image Upload (to WeChat CDN directly) ----
async function uploadBase64Images(imgCache) {
  const entries = Object.entries(imgCache);
  for (let i = 0; i < entries.length; i += 20) {
    const batch = Object.fromEntries(entries.slice(i, i + 20));
    try {
      const resp = await fetch('/api/wechat-upload', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ base64_images: batch })
      });
      if (resp.ok) {
        const { results } = await resp.json();
        for (const [fn, url] of Object.entries(results)) {
          if (url) imgCache[fn] = url;
        }
      }
    } catch (err) { console.warn('Image upload failed', err); }
  }
}

export async function uploadUrlImages(elems) {
  const imgElems = elems.filter(e => e.k === 'img' && e.src && e.src.startsWith('http'));
  for (let i = 0; i < imgElems.length; i += 20) {
    const batch = imgElems.slice(i, i + 20);
    try {
      const resp = await fetch('/api/wechat-upload', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ urls: batch.map(e => e.src) })
      });
      if (resp.ok) {
        const { results } = await resp.json();
        for (const e of batch) { if (results[e.src]) e.src = results[e.src]; }
      }
    } catch (err) { console.warn('Image upload failed', err); }
  }
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
    if (f && template.formats.some(fmt => f.name.endsWith(fmt))) handleFile(f);
    else showError('请上传 ' + template.formats.join(' 或 ') + ' 文件');
  });
  fileInput.addEventListener('change', () => { if (fileInput.files[0]) handleFile(fileInput.files[0]); });

  async function handleFile(file) {
    errorEl.style.display = 'none';
    dropZone.classList.add('processing');
    progress.style.display = 'block';
    progressFill.style.width = '30%';
    progressText.textContent = '正在解压 ' + file.name + '...';
    try {
      progressFill.style.width = '50%';
      progressText.textContent = '正在排版并上传图片...';
      const t0 = performance.now();

      let result;
      if (file.name.endsWith('.docx')) {
        const docxData = await parseDocx(file);
        result = template.processDocx(docxData);
      } else if (typeof template.processMd === 'function') {
        const text = await file.text();
        result = await template.processMd(text);
      } else {
        throw new Error('此模板不支持该文件格式');
      }

      // Use custom footer from localStorage if available
      let footerHtml = localStorage.getItem('custom_footer') || await footerReady;

      // Upload footer base64 images to WeChat CDN
      const dataUriRe = /src="(data:image\/[^"]+)"/g;
      const footerDataUris = {};
      let m2;
      while ((m2 = dataUriRe.exec(footerHtml)) !== null) {
        const uri = m2[1];
        footerDataUris['footer_' + Object.keys(footerDataUris).length] = uri;
      }
      if (Object.keys(footerDataUris).length > 0) {
        progressText.textContent = '正在上传底部图片...';
        try {
          const fResp = await fetch('/api/wechat-upload', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ base64_images: footerDataUris })
          });
          if (fResp.ok) {
            const { results } = await fResp.json();
            for (const [key, wechatUrl] of Object.entries(results)) {
              if (wechatUrl) footerHtml = footerHtml.replace(footerDataUris[key], wechatUrl);
            }
          }
        } catch (e) { console.warn('Footer image upload failed', e); }
      }

      const articleHtml = result.lines.join('\n');
      const elapsed = ((performance.now() - t0) / 1000).toFixed(1);
      const stats = '段落: ' + result.lines.length + ' | 图片: ' + result.imgN +
        ' | 标题: ' + result.headingN + ' | 耗时: ' + elapsed + 's';

      progressFill.style.width = '100%';
      progressText.textContent = '排版完成!';
      setTimeout(() => showPreview(file.name.replace(/\.\w+$/i, ''), articleHtml, footerHtml, stats), 300);
    } catch (err) {
      dropZone.classList.remove('processing');
      progress.style.display = 'none';
      showError('排版失败: ' + err.message);
    }
  }

  // Track content and footer separately for footer toggle/replace
  let _articleHtml = '';
  let _footerHtml = '';

  function showPreview(title, articleContent, footerContent, stats) {
    _articleHtml = articleContent;
    _footerHtml = footerContent;

    document.getElementById('uploadView').style.display = 'none';
    document.getElementById('previewView').style.display = 'block';

    // Title
    const titleInput = document.getElementById('titleInput');
    titleInput.value = title;
    document.getElementById('previewTitleDisplay').textContent = title;
    titleInput.addEventListener('input', () => {
      document.getElementById('previewTitleDisplay').textContent = titleInput.value;
    });

    // Content
    document.getElementById('contentArea').innerHTML = _articleHtml + '\n' + _footerHtml;
    document.getElementById('statsBar').textContent = stats;

    // Cover: extract first image
    const firstImg = document.getElementById('contentArea').querySelector('img');
    const coverImg = document.getElementById('coverImg');
    const coverPlaceholder = document.getElementById('coverPlaceholder');
    if (firstImg && firstImg.src && (firstImg.src.startsWith('http') || firstImg.src.startsWith('data:'))) {
      coverImg.src = firstImg.src;
      coverImg.style.display = 'block';
      coverPlaceholder.style.display = 'none';
    }

    // Footer preview
    document.getElementById('footerPreview').innerHTML = _footerHtml;

    // Footer update function (called by toggle and file replace)
    window._updateFooter = function(newHtml) {
      if (newHtml !== null && newHtml !== undefined) _footerHtml = newHtml;
      const enabled = document.getElementById('footerEnabled').checked;
      document.getElementById('contentArea').innerHTML = _articleHtml + '\n' + (enabled ? _footerHtml : '');
    };
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
  };

  window.copyContent = async function() {
    const content = document.getElementById('contentArea');
    const html = content.innerHTML;
    const btn = document.getElementById('copyBtn');
    let ok = false;
    if (navigator.clipboard && typeof ClipboardItem !== 'undefined') {
      try {
        await navigator.clipboard.write([new ClipboardItem({
          'text/html': new Blob([html], { type: 'text/html' }),
          'text/plain': new Blob([content.textContent || ''], { type: 'text/plain' }),
        })]);
        ok = true;
      } catch (e) { /* fallback */ }
    }
    if (!ok) {
      const handler = function(e) {
        e.clipboardData.setData('text/html', html);
        e.clipboardData.setData('text/plain', content.textContent || '');
        e.preventDefault();
      };
      document.addEventListener('copy', handler);
      const range = document.createRange();
      range.selectNodeContents(content);
      const sel = window.getSelection();
      sel.removeAllRanges(); sel.addRange(range);
      ok = document.execCommand('copy');
      sel.removeAllRanges();
      document.removeEventListener('copy', handler);
    }
    btn.textContent = ok ? '已复制!' : '复制失败';
    btn.classList.add('copied');
    setTimeout(() => { btn.textContent = '复制正文'; btn.classList.remove('copied'); }, 2000);
  };

  // Push to WeChat drafts (images already on WeChat CDN, push is instant)
  window.pushToDraft = async function() {
    const btn = document.getElementById('draftBtn');
    const title = document.getElementById('titleInput').value.trim() || '未命名文章';
    const content = document.getElementById('contentArea').innerHTML;

    // Get cover from sidebar
    const coverEl = document.getElementById('coverImg');
    const coverSrc = coverEl ? coverEl.src : '';
    const cover = (coverSrc.startsWith('http') || coverSrc.startsWith('data:')) ? coverSrc : null;

    btn.textContent = '推送中...';
    btn.disabled = true;

    try {
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
        btn.textContent = '推送到草稿箱';
        btn.classList.remove('pushed');
        btn.disabled = false;
      }, 1500);
    } catch (err) {
      btn.textContent = '失败: ' + err.message;
      setTimeout(() => { btn.textContent = '推送到草稿箱'; btn.disabled = false; }, 3000);
    }
  };
}
