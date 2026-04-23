// DOCX/Markdown parsing — pure functions, no browser dependencies
// (except DOMParser which is available in Node 20+ via jsdom if needed)

import { convertRuns } from './punctuation.js';

const EXT_TO_MIME = {
  gif: 'image/gif',
  png: 'image/png',
  webp: 'image/webp',
  svg: 'image/svg+xml',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  bmp: 'image/bmp',
  tiff: 'image/tiff',
  tif: 'image/tiff',
};

// ---- XML Namespaces ----
export const W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main';
export const R = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships';
export const WP = 'http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing';
export const A = 'http://schemas.openxmlformats.org/drawingml/2006/main';
export const MC = 'http://schemas.openxmlformats.org/markup-compatibility/2006';

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

// ---- Run-level helpers ----
function extractRunProps(r) {
  const rPr = findOne(r, W, 'rPr');
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
  return { bold, hl, sz, color };
}

// Collect text/break content from a run in document order. Returns a list of
// segments — text fragments interleaved with {br:true} markers — so callers
// can later split a paragraph on <w:br/> (WPS exports use soft breaks instead
// of separate <w:p> elements).
function collectRunSegments(r) {
  const segments = [];
  let buf = '';
  const flush = () => { if (buf) { segments.push({ text: buf }); buf = ''; } };
  for (const c of r.children) {
    if (c.namespaceURI !== W) continue;
    switch (c.localName) {
      case 't': buf += c.textContent || ''; break;
      case 'br': flush(); segments.push({ br: true }); break;
      case 'tab': buf += ' '; break;
      case 'noBreakHyphen': buf += '-'; break;
      case 'softHyphen': break;
      case 'sym': {
        const ch = wattr(c, 'char');
        if (ch) { const n = parseInt(ch, 16); if (!isNaN(n)) buf += String.fromCodePoint(n); }
        break;
      }
    }
  }
  flush();
  return segments;
}

function visitRun(r, ctx, runs, ridToFile) {
  // Field-code state transitions (begin / separate / end)
  const fldChar = findOne(r, W, 'fldChar');
  if (fldChar) {
    const t = wattr(fldChar, 'fldCharType');
    if (t === 'begin') { ctx.fieldState = 'instruction'; ctx.fieldInstr = ''; ctx.fieldHref = ''; return; }
    if (t === 'separate') {
      const m = ctx.fieldInstr.match(/HYPERLINK\s+"([^"]+)"/i) || ctx.fieldInstr.match(/HYPERLINK\s+(\S+)/i);
      if (m) ctx.fieldHref = m[1].replace(/&amp;/g, '&');
      ctx.fieldState = ctx.fieldHref ? 'result' : 'none';
      return;
    }
    if (t === 'end') { ctx.fieldState = 'none'; ctx.fieldInstr = ''; ctx.fieldHref = ''; return; }
  }
  const instrText = findOne(r, W, 'instrText');
  if (instrText) {
    if (ctx.fieldState === 'instruction') ctx.fieldInstr += instrText.textContent || '';
    return;
  }

  // Drawing (inline image) takes precedence over text
  const drw = findOne(r, W, 'drawing');
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
    return;
  }

  const segments = collectRunSegments(r);
  if (!segments.length) return;
  if (ctx.fieldState === 'instruction') return;

  const href = (ctx.fieldState === 'result' && ctx.fieldHref) ? ctx.fieldHref : ctx.linkHref;
  const props = href ? null : extractRunProps(r);

  for (const seg of segments) {
    if (seg.br) {
      runs.push({ type: 'br' });
    } else if (href) {
      runs.push({ type: 'link', text: seg.text, href });
    } else {
      runs.push({ type: 'txt', text: seg.text, bold: props.bold, hl: props.hl, sz: props.sz, color: props.color });
    }
  }
}

// Recursive walker — transparently descends through <w:sdt>, <w:smartTag>,
// <w:ins>, <w:fldSimple>, <mc:AlternateContent>, etc. so we never lose
// runs hidden inside wrapper elements (common in WPS / 飞书 / 腾讯文档 exports).
function walkParagraph(node, ctx, runs, ridToFile, ridToUrl) {
  for (const child of node.children) {
    if (child.namespaceURI === W) {
      const tag = child.localName;
      if (tag === 'pPr' || tag === 'sdtPr' || tag === 'sdtEndPr' || tag === 'rPr') continue;
      if (tag === 'del') continue; // skip tracked deletions
      if (tag === 'r') { visitRun(child, ctx, runs, ridToFile); continue; }
      if (tag === 'hyperlink') {
        const rid = rattr(child, 'id');
        const href = ridToUrl[rid] || '';
        const prev = ctx.linkHref;
        if (href) ctx.linkHref = href;
        walkParagraph(child, ctx, runs, ridToFile, ridToUrl);
        ctx.linkHref = prev;
        continue;
      }
      if (tag === 'fldSimple') {
        const instr = wattr(child, 'instr') || '';
        const m = instr.match(/HYPERLINK\s+"([^"]+)"/i) || instr.match(/HYPERLINK\s+(\S+)/i);
        const prev = ctx.linkHref;
        if (m) ctx.linkHref = m[1].replace(/&amp;/g, '&');
        walkParagraph(child, ctx, runs, ridToFile, ridToUrl);
        ctx.linkHref = prev;
        continue;
      }
      // Default: recurse through any other w:* wrapper (sdt, sdtContent,
      // smartTag, ins, customXml*, bookmark*, permStart/End, etc.)
      walkParagraph(child, ctx, runs, ridToFile, ridToUrl);
    } else if (child.namespaceURI === MC && child.localName === 'AlternateContent') {
      let pick = null;
      for (const c of child.children) {
        if (c.localName === 'Choice') { pick = c; break; }
      }
      if (!pick) {
        for (const c of child.children) {
          if (c.localName === 'Fallback') { pick = c; break; }
        }
      }
      if (pick) walkParagraph(pick, ctx, runs, ridToFile, ridToUrl);
    } else {
      walkParagraph(child, ctx, runs, ridToFile, ridToUrl);
    }
  }
}

// Build a Paragraph data object from a list of runs and shared block-level
// properties. Heading/outline flags are passed in so split paragraphs can
// downgrade trailing slices to plain body text.
function buildParagraph(runs, base) {
  runs = convertRuns(runs);
  const text = runs.filter(r => r.type !== 'img' && r.type !== 'br').map(r => r.text || '').join('');
  const textRuns = runs.filter(r => r.type === 'txt');
  return {
    align: base.align,
    runs,
    text,
    hasImg: runs.some(r => r.type === 'img'),
    isEmpty: !runs.some(r => r.type === 'img') && !text.trim(),
    isList: base.isList,
    hasHL: runs.some(r => r.hl),
    hasOutlineLevel: base.hasOutlineLevel,
    hasHeadingStyle: base.hasHeadingStyle,
    allBold: textRuns.length > 0 && textRuns.every(r => r.bold),
    fontSizes: [...new Set(textRuns.filter(r => r.sz).map(r => r.sz))],
  };
}

// ---- Generic Paragraph Data Extraction ----
// Returns one or more Paragraph objects. WPS-exported docx files often pack
// multiple visual paragraphs into a single <w:p> with <w:br/> separators —
// we split on those breaks so downstream rendering treats each line as its
// own paragraph. Heading/outline style only applies to the first slice.
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
  const ctx = { fieldState: 'none', fieldInstr: '', fieldHref: '', linkHref: '' };
  walkParagraph(p, ctx, runs, ridToFile, ridToUrl);

  const base = { align, isList, hasOutlineLevel, hasHeadingStyle };
  if (!runs.some(r => r.type === 'br')) {
    return [buildParagraph(runs, base)];
  }

  const groups = [];
  let cur = [];
  for (const r of runs) {
    if (r.type === 'br') { groups.push(cur); cur = []; } else { cur.push(r); }
  }
  groups.push(cur);

  // Heading style applies to the first slice that actually has content. Leading
  // empty <w:br/>-only slices (common in WPS exports — e.g. blank line before
  // "信息来源" heading) would otherwise consume and lose the heading flags.
  const firstNonEmptyIdx = groups.findIndex(g => g.length > 0);
  return groups.map((g, i) => buildParagraph(g, i === firstNonEmptyIdx
    ? base
    : { ...base, hasOutlineLevel: false, hasHeadingStyle: false }));
}

// ---- DOCX Infrastructure ----
// Note: parseDocx depends on JSZip (global) and DOMParser (browser).
// It stays here for co-location with extractParagraph, but is only
// callable in a browser environment.
export async function parseDocx(file) {
  const zip = await JSZip.loadAsync(file);

  // Some writers (Windows tooling, older Pages/Keynote exports, WPS variants)
  // store zip entries with backslashes or unusual casing instead of the
  // spec-required forward-slash lowercased form. JSZip does exact-match
  // lookups, so we build a normalized index once and resolve every lookup
  // through it — forward slashes, backslashes, and case all collapse to a
  // single canonical key.
  const canonicalize = (k) => k.replace(/\\/g, '/').toLowerCase();
  const zipIndex = {};
  for (const key of Object.keys(zip.files)) {
    zipIndex[canonicalize(key)] = zip.files[key];
  }
  const zipFile = (p) => {
    const entry = zipIndex[canonicalize(p)];
    return entry && !entry.dir ? entry : null;
  };

  const relsEntry = zipFile('word/_rels/document.xml.rels');
  const docEntry = zipFile('word/document.xml');
  if (!relsEntry) {
    const keys = Object.keys(zip.files).slice(0, 20).join(', ');
    throw new Error('无效的 DOCX 文件：缺少 document.xml.rels（zip keys: ' + keys + '）');
  }
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

  // imgCache values are strings usable as <img src>. Two paths:
  //  (1) SERVER-STRIPPED DOCX: the backend removes word/media/* and injects
  //      word/_fbif_imageUrls.json pointing to cached URLs. We use those
  //      URLs verbatim — browser fetches images in parallel via HTTP/2,
  //      so text renders before all image bytes arrive (big UX win for
  //      14 MB .doc uploads where server-to-client bandwidth dominates).
  //  (2) LOCAL DOCX: images embedded in word/media/*. We read each as a
  //      Blob and expose via URL.createObjectURL — zero base64 encoding,
  //      parseDocx stays at ~0.1 s regardless of image count.
  // Downstream uploader (uploader.js) materializes blob: / http(s): URLs
  // to base64 only at copy time (OSS upload) — quality is never touched.
  const imgCache = {};
  const urlsEntry = zipFile('word/_fbif_imageUrls.json');
  let externalUrls = null;
  if (urlsEntry) {
    try {
      externalUrls = JSON.parse(await urlsEntry.async('string'));
    } catch (e) {
      externalUrls = null;
    }
  }
  if (externalUrls) {
    for (const fn of Object.values(ridToFile)) {
      if (externalUrls[fn]) imgCache[fn] = externalUrls[fn];
    }
  } else {
    await Promise.all(Object.values(ridToFile).map(async (fn) => {
      const entry = zipFile('word/media/' + fn);
      if (!entry) return;
      const blob = await entry.async('blob');
      const mime = EXT_TO_MIME[(fn.split('.').pop() || '').toLowerCase()] || blob.type || 'image/png';
      const typed = blob.type === mime ? blob : blob.slice(0, blob.size, mime);
      imgCache[fn] = URL.createObjectURL(typed);
    }));
  }

  const docXml = await docEntry.async('string');
  const docDom = new DOMParser().parseFromString(docXml, 'text/xml');
  const body = findOne(docDom.documentElement, W, 'body');
  const paragraphs = [];
  collectBlockParagraphs(body, ridToFile, ridToUrl, paragraphs);

  return { paragraphs, imgCache };
}

// Walk block-level content collecting paragraphs. Descends into tables
// (flattening cells row-by-row), sdt wrappers, and mc:AlternateContent
// so table text and SDT-wrapped paragraphs aren't silently dropped.
export function collectBlockParagraphs(container, ridToFile, ridToUrl, out) {
  for (const child of container.children) {
    if (child.namespaceURI === W) {
      const tag = child.localName;
      if (tag === 'p') {
        out.push(...extractParagraph(child, ridToFile, ridToUrl));
        continue;
      }
      if (tag === 'tbl') {
        for (const tr of findAll(child, W, 'tr')) {
          for (const tc of findAll(tr, W, 'tc')) {
            collectBlockParagraphs(tc, ridToFile, ridToUrl, out);
          }
        }
        continue;
      }
      // Skip metadata / bookkeeping elements that never hold content.
      if (tag === 'sectPr' || tag === 'tblPr' || tag === 'tblGrid' ||
          tag === 'trPr' || tag === 'tcPr' || tag === 'sdtPr' ||
          tag === 'sdtEndPr' || tag === 'bookmarkStart' ||
          tag === 'bookmarkEnd' || tag === 'proofErr') continue;
      // Other w:* wrappers (sdt, sdtContent, ins, etc.) — recurse.
      collectBlockParagraphs(child, ridToFile, ridToUrl, out);
    } else if (child.namespaceURI === MC && child.localName === 'AlternateContent') {
      let pick = null;
      for (const c of child.children) { if (c.localName === 'Choice') { pick = c; break; } }
      if (!pick) for (const c of child.children) { if (c.localName === 'Fallback') { pick = c; break; } }
      if (pick) collectBlockParagraphs(pick, ridToFile, ridToUrl, out);
    } else {
      collectBlockParagraphs(child, ridToFile, ridToUrl, out);
    }
  }
}

// ---- Markdown Utilities ----
// Splits `text` on ** and emits txt runs with the correct bold flag. The
// `startBold` param lets callers carry bold state across a boundary that
// doesn't go through this function (e.g. a [link](url) token sits between
// two pushBoldSplit calls). Returns the bold state AFTER this chunk so the
// next chunk can resume. Without this, `**A [link](url) B**` loses bold on
// B because each chunk otherwise starts from bold=false.
function pushBoldSplit(runs, text, startBold = false) {
  const parts = text.split(/\*\*/);
  let bold = startBold;
  for (let i = 0; i < parts.length; i++) {
    if (parts[i]) runs.push({ type: 'txt', text: parts[i], bold });
    if (i < parts.length - 1) bold = !bold;
  }
  return bold;
}

export function parseMdRuns(text) {
  text = text.replace(/\s*\n\s*/g, '');
  const runs = [];
  // Match [display text](url) — url is any non-space, non-`)` sequence.
  const re = /\[([^\]]+)\]\(([^)\s]+)\)/g;
  let lastIdx = 0;
  let m;
  let curBold = false;
  while ((m = re.exec(text)) !== null) {
    const before = text.slice(lastIdx, m.index);
    if (before) curBold = pushBoldSplit(runs, before, curBold);
    runs.push({ type: 'link', text: m[1], href: m[2], bold: curBold });
    lastIdx = m.index + m[0].length;
  }
  const rest = text.slice(lastIdx);
  if (rest) pushBoldSplit(runs, rest, curBold);
  return convertRuns(runs);
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
