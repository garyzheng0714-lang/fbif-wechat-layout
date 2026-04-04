// Mote 莫特专用排版模板
import { esc, escAttr, looksLikeGifSource } from '../engine.js';

const BLANK = '<section><span style="font-size: 15px;"><br></span></section>';

// ---- Inline Rendering (simple spans, no leaf wrapping) ----
function renderInline(parts) {
  return parts.filter(p => p.type === 'txt' || p.type === 'link').map(p => {
    if (p.type === 'txt') {
      const t = esc(p.text);
      return p.bold ? '<strong>' + t + '</strong>' : t;
    }
    if (p.type === 'link') {
      const h = (p.href || '');
      if (!/^https?:\/\//i.test(h)) return esc(p.text);
      return '<a style="color: rgb(0, 112, 192); text-decoration: none;" href="' +
        escAttr(h) + '">' + esc(p.text) + '</a>';
    }
    return '';
  }).join('');
}

// ---- Paragraph Classification ----
function classify(pd) {
  if (pd.isEmpty) return 'blank';
  if (pd.hasImg) return 'image';
  if (pd.text.trim() === '参考来源') return 'ref_header';
  // Mote detects headings by bold + font size 32 or 36
  if (pd.allBold && pd.fontSizes.some(s => s === '32' || s === '36')) return 'heading';
  if (pd.isList) return 'list';
  if (pd.align === 'center') return 'caption';
  return 'text';
}

// ---- DOCX Processing ----
function classifyDocx(paragraphs, imgCache) {
  // Find "正文" marker
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

// ---- HTML Rendering (blank-section spacing) ----
function render(elements) {
  const lines = [];
  let inRef = false;
  let prevK = null;

  function ensureBlank() {
    if (lines.length && lines[lines.length - 1] !== BLANK) lines.push(BLANK);
  }

  // Author line (hardcoded for Mote)
  lines.push('<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: rgb(0, 112, 192);">作者：Mote莫特</span></section>');
  lines.push(BLANK);

  for (const elem of elements) {
    const k = elem.kind;

    if (k === 'ref_header') {
      inRef = true; ensureBlank();
      lines.push('<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: #888888;">参考来源：</span></section>');
      prevK = k; continue;
    }
    if (inRef) {
      lines.push('<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: #888888;">' + (elem.html || '') + '</span></section>');
      prevK = k; continue;
    }
    if (k === 'heading') {
      ensureBlank();
      lines.push('<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 18px; font-weight: bold; color: #544545;">' + esc(elem.text) + '</span></section>');
      lines.push(BLANK);
      prevK = k; continue;
    }
    if (k === 'image') {
      if (prevK !== 'image' && prevK !== 'caption' && prevK !== null) ensureBlank();
      const mSrc = elem.src || '';
      const mIsGif = elem.gif || looksLikeGifSource(mSrc);
      const mGifAttr = mIsGif ? ' data-type="gif"' : '';
      lines.push('<section style="text-align: center; margin-left: 8px; margin-right: 8px;"><img src="' + mSrc + '"' + mGifAttr + ' style="width: ' + elem.width + '; display: block; margin: 0 auto;" /></section>');
      prevK = k; continue;
    }
    if (k === 'caption') {
      lines.push('<section style="text-align: center; margin-left: 8px; margin-right: 8px;"><span style="font-size: 12px; color: #888888;">' + elem.html + '</span></section>');
      prevK = k; continue;
    }
    if (k === 'list') {
      if (prevK === 'text' && elem.num === 1) ensureBlank();
      lines.push('<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: #544545;">' + elem.num + '、' + elem.html + '</span></section>');
      prevK = k; continue;
    }
    if (k === 'text') {
      if (prevK === 'text' || prevK === 'caption' || prevK === 'image') ensureBlank();
      lines.push('<section style="margin-left: 8px; margin-right: 8px; line-height: 1.75em;"><span style="font-size: 15px; color: #544545;">' + elem.html + '</span></section>');
      prevK = k;
    }
  }

  // Dedup consecutive blanks
  const final = [];
  for (const l of lines) {
    if (l === BLANK && final.length && final[final.length - 1] === BLANK) continue;
    final.push(l);
  }
  return final;
}

export default {
  id: 'mote',
  name: 'Mote 排版',
  description: 'Mote 莫特专用排版格式，自动添加作者栏和底部模板',
  formats: ['.docx'],

  processDocx({ paragraphs, imgCache }) {
    const { elements, imgN } = classifyDocx(paragraphs, imgCache);
    const lines = render(elements);
    return { lines, imgN, headingN: elements.filter(e => e.kind === 'heading').length };
  },

  // Mote 暂不支持 Markdown
  processMd: null,
};
