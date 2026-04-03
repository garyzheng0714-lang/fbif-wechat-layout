// FBIF 公众号排版模板
import { esc, escAttr, parseMdRuns, parseMdFrontmatter } from '../engine.js';

const FONT_STACK = 'mp-quote, "PingFang SC", system-ui, -apple-system, BlinkMacSystemFont, "Helvetica Neue", "Hiragino Sans GB", "Microsoft YaHei UI", "Microsoft YaHei", Arial, sans-serif';

// ---- Gold-standard WeChat ProseMirror wrapping ----
function leafWrap(inner) {
  return '<span leaf="" style="color: rgba(0,0,0,0.9); font-size: 17px; font-family: ' + FONT_STACK + '; letter-spacing: 0.034em; font-style: normal; font-weight: normal;">' + inner + '</span>';
}
function textSpan(text, opts) {
  let s = 'font-size: ' + (opts.fontSize || '15px') + '; color: ' + (opts.color || 'rgb(84, 69, 69)');
  if (opts.bold) s += '; font-weight: bold';
  return '<span textstyle="" style="' + s + ';">' + text + '</span>';
}
function renderRuns(runs, opts) {
  const c = (opts && opts.color) || 'rgb(84, 69, 69)';
  const fs = (opts && opts.fontSize) || '15px';
  return runs.filter(r => r.type !== 'img').map(r => {
    if (r.type === 'txt') return leafWrap(textSpan(esc(r.text), { fontSize: fs, color: c, bold: r.bold }));
    if (r.type === 'link') return leafWrap('<a style="color: rgb(0, 112, 192); text-decoration: none; font-size: ' + fs + ';" href="' + escAttr(r.href || '') + '">' + esc(r.text) + '</a>');
    return '';
  }).join('');
}

// ---- Spacing & Section Helpers ----
const GAP = 'padding-bottom: 20px; ';
function sec(inner, gap) {
  return '<section style="text-align: left; ' + (gap ? GAP : '') + 'margin: 0px 8px; line-height: 1.75em;">' + inner + '</section>';
}
function secCenter(inner, gap) {
  return '<section style="text-align: center; ' + (gap ? GAP : '') + 'margin: 0px 8px; line-height: 1.75em;">' + inner + '</section>';
}

// ---- DOCX Classification ----
function classifyDocx(paragraphs, imgCache) {
  // Detect headings using outline level / pStyle
  const pds = paragraphs.map(p => ({
    ...p,
    isHeading: p.hasOutlineLevel || p.hasHeadingStyle,
  }));

  // Extract meta info (标题/摘要/作者)
  let author = '', contentStart = 0;
  for (let i = 0; i < pds.length; i++) {
    if (!pds[i].isHeading) continue;
    if (pds[i].text.trim().startsWith('标题')) {
      let foundNext = false;
      for (let j = i + 1; j < pds.length; j++) {
        if (pds[j].isHeading) { contentStart = j; foundNext = true; break; }
        const m = pds[j].text.trim().match(/^作者[：:]\s*(.+)/);
        if (m) author = m[1].trim();
      }
      if (!foundNext) {
        contentStart = i + 1;
        while (contentStart < pds.length && !pds[contentStart].isHeading) contentStart++;
        if (contentStart >= pds.length) contentStart = pds.length;
      }
    }
    break;
  }
  // Skip "引言" marker
  if (contentStart < pds.length && pds[contentStart].isHeading &&
      pds[contentStart].text.trim().replace(/[：:]$/, '') === '引言') contentStart++;

  // Build element list
  const elems = [];
  let inRef = false, imgN = 0;

  for (let i = contentStart; i < pds.length; i++) {
    const pd = pds[i];
    if (pd.isEmpty) continue;
    if (pd.hasHL && pd.text.includes('插入视频')) continue;

    if (pd.isHeading) {
      const ht = pd.text.trim().replace(/[：:]$/, '');
      if (ht === '引言' || ht === '标题') continue;
      if (ht.startsWith('参考来源')) { inRef = true; elems.push({ k: 'refH' }); continue; }
      elems.push({ k: 'h', text: ht.replace(/^0?\d+\s+/, '') });
      continue;
    }
    if (inRef) { elems.push({ k: 'ref', runs: pd.runs }); continue; }
    if (pd.hasImg) {
      for (const r of pd.runs) {
        if (r.type === 'img') { imgN++; elems.push({ k: 'img', src: imgCache[r.file] || '', w: r.w }); }
      }
      while (i + 1 < pds.length) {
        const nx = pds[i + 1];
        if (nx.isEmpty) { i++; continue; }
        if (!nx.hasImg && !nx.isHeading && nx.align === 'center' && nx.text.trim()) {
          let ct = nx.text.trim().replace(/^[（(]插入视频[）)]\s*/, '');
          if (ct) elems.push({ k: 'cap', text: ct });
          i++;
        } else break;
      }
      continue;
    }
    elems.push({ k: 'txt', runs: pd.runs });
  }

  return { elems, author, imgN };
}

// ---- Markdown Classification ----
async function classifyMd(text) {
  const { author, content } = parseMdFrontmatter(text);
  const rawParas = content.split(/\n\s*\n/).map(p => p.trim()).filter(p => p);

  const elems = [];
  let imgN = 0, expectHeading = false, stopped = false, inRef = false;

  for (let i = 0; i < rawParas.length && !stopped; i++) {
    const para = rawParas[i].replace(/\s+$/gm, '');
    if (/^#\s/.test(para)) continue;
    if (/^>\s/.test(para)) continue;
    if (/^[-*_\s]{3,}$/.test(para.replace(/\s/g, ''))) {
      // Only stop on "* * *" with spaces (FBIF footer separator), not "***" or "---"
      if (/^\*\s+\*\s+\*/.test(para.trim())) stopped = true;
      continue;
    }
    if (/^参考来源/.test(para)) { inRef = true; elems.push({ k: 'refH' }); continue; }
    if (inRef) { elems.push({ k: 'ref', runs: parseMdRuns(para) }); continue; }

    const imgMatch = para.match(/^!\[([^\]]*)\]\(([^)]+)\)\s*$/);
    if (imgMatch) {
      const imgSrc = imgMatch[2].replace(/"/g, '').replace(/</g, '').replace(/>/g, '');
      imgN++; elems.push({ k: 'img', src: imgSrc, w: '100%' }); continue;
    }

    if (/^\*\*0?\d+\*\*$/.test(para)) { expectHeading = true; continue; }
    if (expectHeading) {
      const hm = para.match(/^\*\*(.+)\*\*$/);
      if (hm) { elems.push({ k: 'h', text: hm[1] }); expectHeading = false; continue; }
      expectHeading = false;
    }
    elems.push({ k: 'txt', runs: parseMdRuns(para) });
  }

  // No upload here — background upload in engine handles it after preview
  return { elems, author, imgN };
}

// ---- HTML Rendering ----
function render(elems, author) {
  const lines = [];

  if (author) {
    lines.push(sec('<span leaf="" style="color: rgb(0, 112, 192); font-size: 15px; font-style: italic; font-family: ' + FONT_STACK + '; letter-spacing: 0.034em;">' +
      '<span textstyle="" style="font-size: 15px; color: rgb(0, 112, 192); font-style: italic;">作者：' +
      esc(author) + '</span></span>', true));
  }

  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    const nextK = i + 1 < elems.length ? elems[i + 1].k : null;
    const spaceAfter = nextK && nextK !== 'cap' &&
      !(e.k === 'ref' && nextK === 'ref') &&
      !(e.k === 'img' && (nextK === 'cap' || nextK === 'img'));

    switch (e.k) {
      case 'h':
        lines.push(sec(leafWrap(textSpan(esc(e.text), { fontSize: '18px', bold: true })), spaceAfter));
        break;
      case 'img': {
        const src = e.src || '';
        const rpol = src.startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
        lines.push('<section style="text-align: center; ' + (spaceAfter ? GAP : '') + 'margin: 0px 8px;">' +
          '<img src="' + src + '"' + rpol + ' style="width: ' + e.w + '; display: block; margin: 0 auto;" /></section>');
        break;
      }
      case 'cap':
        lines.push(secCenter(leafWrap(textSpan(esc(e.text), { fontSize: '12px', color: 'rgb(136, 136, 136)' })), spaceAfter));
        break;
      case 'txt':
        lines.push(sec(renderRuns(e.runs), spaceAfter));
        break;
      case 'refH':
        lines.push(sec(leafWrap(textSpan('参考来源：', { color: 'rgb(136, 136, 136)' })), false));
        break;
      case 'ref':
        lines.push(sec(renderRuns(e.runs, { color: 'rgb(136, 136, 136)' }), spaceAfter));
        break;
    }
  }
  return lines;
}

export default {
  id: 'fbif',
  name: 'FBIF 公众号排版',
  description: '按 FBIF 排版规范格式化，支持标题/引言/图片说明/参考来源',
  icon: '📰',
  badge: { text: 'FBIF 规范', class: 'badge-blue' },
  formats: ['.docx', '.md', '.txt'],

  processDocx({ paragraphs, imgCache }) {
    const { elems, author, imgN } = classifyDocx(paragraphs, imgCache);
    return { lines: render(elems, author), imgN, headingN: elems.filter(e => e.k === 'h').length };
  },
  async processMd(text) {
    const { elems, author, imgN } = await classifyMd(text);
    return { lines: render(elems, author), imgN, headingN: elems.filter(e => e.k === 'h').length };
  },
};
