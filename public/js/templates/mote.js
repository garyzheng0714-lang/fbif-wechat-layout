// Mote 莫特专用排版模板 — CSS class-based rendering
const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, looksLikeGifSource, parseMdRuns, parseMdFrontmatter } = engineModule;

const BLANK = '<section><span class="wx-bls"><br></span></section>';

// ---- Inline Rendering (simple spans, class-based) ----
function renderInline(parts) {
  return parts.filter(p => p.type === 'txt' || p.type === 'link').map(p => {
    if (p.type === 'txt') {
      const t = esc(p.text);
      return p.bold ? '<strong>' + t + '</strong>' : t;
    }
    if (p.type === 'link') {
      const h = (p.href || '');
      if (!/^https?:\/\//i.test(h)) return esc(p.text);
      return '<a class="wx-a" href="' + escAttr(h) + '">' + esc(p.text) + '</a>';
    }
    return '';
  }).join('');
}

// ---- Paragraph Classification ----
function classify(pd) {
  if (pd.isEmpty) return 'blank';
  if (pd.hasImg) return 'image';
  if (pd.text.trim() === '参考来源') return 'ref_header';
  if (pd.allBold && pd.fontSizes.some(s => s === '32' || s === '36')) return 'heading';
  if (pd.isList) return 'list';
  if (pd.align === 'center') return 'caption';
  return 'text';
}

// ---- DOCX Processing ----
function classifyDocx(paragraphs, imgCache) {
  let startIdx = 0;
  for (let i = 0; i < paragraphs.length; i++) {
    if (paragraphs[i].text.trim() === '正文') { startIdx = i + 1; break; }
  }

  const elements = [];
  let imgN = 0, listCounter = 0;

  for (let i = startIdx; i < paragraphs.length; i++) {
    const pd = paragraphs[i];
    const kind = classify(pd);

    if (kind === 'blank') { listCounter = 0; continue; }
    if (kind === 'image') {
      for (const r of pd.runs) {
        if (r.type === 'img') {
          const src = imgCache[r.file] || '';
          imgN++;
          elements.push({ kind: 'image', src, width: r.w || '100%', gif: looksLikeGifSource(src) || /\.gif$/i.test(r.file) });
        }
      }
    } else if (kind === 'heading') {
      elements.push({ kind: 'heading', text: pd.text });
    } else if (kind === 'caption') {
      elements.push({ kind: 'caption', html: renderInline(pd.runs) });
    } else if (kind === 'ref_header') {
      elements.push({ kind: 'ref_header' });
    } else if (kind === 'list') {
      listCounter++;
      elements.push({ kind: 'list', html: renderInline(pd.runs), num: listCounter });
    } else if (kind === 'text') {
      listCounter = 0;
      elements.push({ kind: 'text', html: renderInline(pd.runs) });
    }
  }

  return { elements, imgN };
}

// ---- HTML Rendering (class-based, blank-section spacing) ----
function render(elements, author) {
  const lines = [];
  let inRef = false;
  let prevK = null;

  function ensureBlank() {
    if (lines.length && lines[lines.length - 1] !== BLANK) lines.push(BLANK);
  }

  // Author line — use detected author or default to Mote莫特
  const displayAuthor = author || 'Mote莫特';
  lines.push('<section class="wx-p"><span class="wx-auth">作者：' + esc(displayAuthor) + '</span></section>');
  lines.push(BLANK);

  for (const elem of elements) {
    const k = elem.kind;

    if (k === 'ref_header') {
      inRef = true; ensureBlank();
      lines.push('<section class="wx-p"><span class="wx-tr">参考来源：</span></section>');
      prevK = k; continue;
    }
    if (inRef) {
      lines.push('<section class="wx-p"><span class="wx-tr">' + (elem.html || '') + '</span></section>');
      prevK = k; continue;
    }
    if (k === 'heading') {
      ensureBlank();
      lines.push('<section class="wx-p"><span class="wx-th">' + esc(elem.text) + '</span></section>');
      lines.push(BLANK);
      prevK = k; continue;
    }
    if (k === 'image') {
      if (prevK !== 'image' && prevK !== 'caption' && prevK !== null) ensureBlank();
      const mSrc = elem.src || '';
      const mIsGif = elem.gif || looksLikeGifSource(mSrc);
      const mGifAttr = mIsGif ? ' data-type="gif"' : '';
      lines.push('<section class="wx-pi"><img src="' + mSrc + '"' + mGifAttr + ' style="width: ' + elem.width + '; display: block; margin: 0 auto;" /></section>');
      prevK = k; continue;
    }
    if (k === 'caption') {
      lines.push('<section class="wx-pc"><span class="wx-tc">' + elem.html + '</span></section>');
      prevK = k; continue;
    }
    if (k === 'attr') {
      lines.push('<section class="wx-p"><span class="wx-auth">' + esc(elem.role) + '：' + esc(elem.name) + '</span></section>');
      prevK = k; continue;
    }
    if (k === 'list') {
      if (prevK === 'text' && elem.num === 1) ensureBlank();
      lines.push('<section class="wx-p"><span class="wx-t">' + elem.num + '、' + elem.html + '</span></section>');
      prevK = k; continue;
    }
    if (k === 'text') {
      if (prevK === 'text' || prevK === 'caption' || prevK === 'image') ensureBlank();
      lines.push('<section class="wx-p"><span class="wx-t">' + elem.html + '</span></section>');
      prevK = k;
    }
  }

  const final = [];
  for (const l of lines) {
    if (l === BLANK && final.length && final[final.length - 1] === BLANK) continue;
    final.push(l);
  }
  return final;
}

// ---- Attribution Patterns ----
// 作者/撰文/撰稿 → "作者", 出品/来源/出处 → "来源"
const RE_AUTHOR = /^(?:\*\*)?(?:作者|撰文|撰稿|文)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)/;
const RE_SOURCE = /^(?:\*\*)?(?:出品|来源|出处)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)/;
const RE_EDITOR = /^(?:\*\*)?(?:编辑|责编)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)/;

function parseAttribution(para) {
  const clean = para.replace(/\*\*/g, '').trim();
  let m = para.match(RE_AUTHOR);
  if (m) return { role: '作者', name: m[1].replace(/\*\*/g, '').trim() };
  m = para.match(RE_SOURCE);
  if (m) return { role: '来源', name: m[1].replace(/\*\*/g, '').trim() };
  m = para.match(RE_EDITOR);
  if (m) return { role: '编辑', name: m[1].replace(/\*\*/g, '').trim() };
  m = clean.match(/^(?:作者|撰文|撰稿)[：:/|｜]\s*(.+)/);
  if (m) return { role: '作者', name: m[1].trim() };
  m = clean.match(/^(?:出品|来源|出处)[：:/|｜]\s*(.+)/);
  if (m) return { role: '来源', name: m[1].trim() };
  m = clean.match(/^(?:编辑|责编)[：:/|｜]\s*(.+)/);
  if (m) return { role: '编辑', name: m[1].trim() };
  return null;
}

// ---- Markdown Processing ----
function classifyMd(text) {
  const { author: fmAuthor, content } = parseMdFrontmatter(text);
  const rawParas = content.split(/\n\s*\n/).map(p => p.trim()).filter(p => p);

  const elements = [];
  let imgN = 0, inRef = false, listCounter = 0, expectHeading = false;
  let author = fmAuthor;

  for (const para of rawParas) {
    if (/^#\s/.test(para)) continue;
    if (/^>\s/.test(para)) continue;
    // Skip x-reader metadata lines
    if (/^Author:\s|^Published:\s|^Source:\s/i.test(para)) continue;
    if (/Author:.*\|.*Published:/i.test(para)) continue;
    if (/^\*\s+\*\s+\*/.test(para.trim())) break;
    if (/^[-*_\s]{3,}$/.test(para.replace(/\s/g, ''))) continue;

    // Detect author/source/editor attribution
    const attr = parseAttribution(para);
    if (attr) {
      if (attr.role === '作者' && !author) author = attr.name;
      elements.push({ kind: 'attr', role: attr.role, name: attr.name });
      continue;
    }

    if (/^参考来源/.test(para)) {
      inRef = true;
      elements.push({ kind: 'ref_header' });
      continue;
    }
    if (inRef) {
      elements.push({ kind: 'text', html: renderInline(parseMdRuns(para)) });
      continue;
    }

    const headingMatch = para.match(/^##\s+(.+)$/);
    if (headingMatch) {
      listCounter = 0;
      elements.push({ kind: 'heading', text: headingMatch[1].replace(/\*\*/g, '') });
      continue;
    }

    const imgMatch = para.match(/^!\[([^\]]*)\]\(([^)]+)\)\s*$/);
    if (imgMatch) {
      const imgSrc = imgMatch[2].replace(/"/g, '').replace(/</g, '').replace(/>/g, '');
      imgN++;
      elements.push({ kind: 'image', src: imgSrc, width: '100%', gif: looksLikeGifSource(imgSrc) });
      continue;
    }

    // Section number (e.g. **01**) followed by bold heading
    if (/^\*\*0?\d+\*\*$/.test(para)) { expectHeading = true; continue; }
    if (expectHeading) {
      const hm = para.match(/^\*\*(.+)\*\*$/);
      if (hm) { elements.push({ kind: 'heading', text: hm[1] }); expectHeading = false; continue; }
      expectHeading = false;
    }
    // Standalone bold line → treat as heading
    const boldOnly = para.match(/^\*\*(.+)\*\*$/);
    if (boldOnly && para.length < 60) {
      elements.push({ kind: 'heading', text: boldOnly[1] });
      continue;
    }

    const listMatch = para.match(/^\d+[.、]\s*(.+)$/);
    if (listMatch) {
      listCounter++;
      elements.push({ kind: 'list', html: renderInline(parseMdRuns(listMatch[1])), num: listCounter });
      continue;
    }

    listCounter = 0;
    elements.push({ kind: 'text', html: renderInline(parseMdRuns(para)) });
  }

  return { elements, imgN, author };
}

export default {
  id: 'mote',
  name: 'Mote 排版',
  description: 'Mote 莫特专用排版格式，自动添加作者栏和底部模板',
  formats: ['.docx', '.md', '.txt'],

  processDocx({ paragraphs, imgCache }) {
    const { elements, imgN } = classifyDocx(paragraphs, imgCache);
    const lines = render(elements, '');
    return { lines, imgN, headingN: elements.filter(e => e.kind === 'heading').length };
  },

  async processMd(text) {
    const { elements, imgN, author } = classifyMd(text);
    const lines = render(elements, author);
    return { lines, imgN, headingN: elements.filter(e => e.kind === 'heading').length };
  },
};
