// DOCX/Markdown parsing — pure functions, no browser dependencies
// (except DOMParser which is available in Node 20+ via jsdom if needed)

import { inferImageMimeFromBase64, looksLikeGifSource } from './image-utils.mjs';

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
// Note: parseDocx depends on JSZip (global) and DOMParser (browser).
// It stays here for co-location with extractParagraph, but is only
// callable in a browser environment.
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
  let author = '', title = '', content = text;
  const m = text.match(/^---\s*\n([\s\S]*?)\n---\s*\n([\s\S]*)$/);
  if (m) {
    content = m[2];
    const am = m[1].match(/author:\s*"?([^"\n]+)"?/);
    if (am) author = am[1].trim();
    const tm = m[1].match(/^title:\s*"?([^"\n]+)"?/m);
    if (tm) title = tm[1].trim().replace(/"$/, '');
  }
  return { author, title, content };
}
