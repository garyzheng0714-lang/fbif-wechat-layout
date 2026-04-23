// FBIF 公众号排版模板 — CSS class-based rendering
const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter, looksLikeGifSource, convertRuns } = engineModule;

// ---- Gold-standard WeChat ProseMirror wrapping (now class-based) ----
function leafWrap(inner) {
  return '<span leaf="" class="wx-leaf">' + inner + '</span>';
}
function leafWrapAuthor(inner) {
  return '<span leaf="" class="wx-la">' + inner + '</span>';
}

function renderRuns(runs, opts) {
  const textCls = (opts && opts.textCls) || 'wx-t';
  return runs.filter(r => r.type !== 'img').map(r => {
    if (r.type === 'txt') {
      const cls = r.bold ? textCls + ' wx-bold' : textCls;
      return leafWrap('<span textstyle="" class="' + cls + '">' + esc(r.text) + '</span>');
    }
    if (r.type === 'link') {
      const h = (r.href || '');
      if (!/^https?:\/\//i.test(h)) return leafWrap(esc(r.text));
      return leafWrap('<a class="wx-a" href="' + escAttr(h) + '">' + esc(r.text) + '</a>');
    }
    return '';
  }).join('');
}

// ---- Spacing & Section Helpers ----
function sec(inner, gap) {
  return '<section class="wx-p' + (gap ? ' wx-gap' : '') + '">' + inner + '</section>';
}
function secCenter(inner, gap) {
  return '<section class="wx-pc' + (gap ? ' wx-gap' : '') + '">' + inner + '</section>';
}

// ---- DOCX Classification ----
function classifyDocx(paragraphs, imgCache) {
  const pds = paragraphs.map(p => ({
    ...p,
    isHeading: p.hasOutlineLevel || p.hasHeadingStyle,
  }));

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
  if (contentStart < pds.length && pds[contentStart].isHeading &&
      pds[contentStart].text.trim().replace(/[：:]$/, '') === '引言') contentStart++;

  const elems = [];
  let inRef = false, imgN = 0;

  for (let i = contentStart; i < pds.length; i++) {
    const pd = pds[i];
    if (pd.isEmpty) continue;
    if (pd.hasHL && pd.text.includes('插入视频')) continue;

    // WPS exports often leave "参考来源：" as a plain paragraph (no heading
    // style / outlineLvl), so detect it by text content too — otherwise the
    // marker and every citation below it fall through to the txt branch and
    // render with 正文 styling plus wx-gap between each entry.
    const trimmed = pd.text.trim();
    if (/^(参考(来源|文献|资料|链接)|信息来源)[：:]?$/.test(trimmed)) {
      inRef = true;
      elems.push({ k: 'refH', text: trimmed.replace(/[：:]$/, '') });
      continue;
    }

    if (pd.isHeading) {
      const ht = trimmed.replace(/[：:]$/, '');
      if (ht === '引言' || ht === '标题') continue;
      if (/^(参考(来源|文献|资料|链接)|信息来源)/.test(ht)) { inRef = true; elems.push({ k: 'refH', text: ht }); continue; }
      // A real non-ref heading closes any prior ref section.
      inRef = false;
      elems.push({ k: 'h', text: ht.replace(/^0?\d+\s+/, '') });
      continue;
    }
    if (inRef) {
      if (!trimmed) continue;
      elems.push({ k: 'ref', runs: pd.runs });
      continue;
    }
    if (pd.hasImg) {
      for (const r of pd.runs) {
        if (r.type === 'img') {
          const src = imgCache[r.file] || '';
          imgN++;
          elems.push({ k: 'img', src, w: r.w, gif: looksLikeGifSource(src) || /\.gif$/i.test(r.file) });
        }
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
  // Bottom-of-article pattern: "作者 | xxx" without bold markers
  m = clean.match(/^(?:作者|撰文|撰稿)[：:/|｜]\s*(.+)/);
  if (m) return { role: '作者', name: m[1].trim() };
  m = clean.match(/^(?:出品|来源|出处)[：:/|｜]\s*(.+)/);
  if (m) return { role: '来源', name: m[1].trim() };
  m = clean.match(/^(?:编辑|责编)[：:/|｜]\s*(.+)/);
  if (m) return { role: '编辑', name: m[1].trim() };
  return null;
}

// ---- Markdown Classification ----
async function classifyMd(text) {
  const { author: fmAuthor, title, content } = parseMdFrontmatter(text);
  const rawParas = content.split(/\n\s*\n/).map(p => p.trim()).filter(p => p);

  const elems = [];
  let imgN = 0, expectHeading = false, stopped = false, inRef = false;
  let author = fmAuthor, source = '';

  for (let i = 0; i < rawParas.length && !stopped; i++) {
    const para = rawParas[i].replace(/\s+$/gm, '');
    if (/^#\s/.test(para)) continue;
    // Skip x-reader metadata lines
    if (/^Author:\s|^Published:\s|^Source:\s/i.test(para)) continue;
    if (/Author:.*\|.*Published:/i.test(para)) continue;

    // Detect author/source/editor attribution
    const attr = parseAttribution(para);
    if (attr) {
      if (attr.role === '作者' && !author) author = attr.name;
      if (attr.role === '来源') source = attr.name;
      // Render attribution as styled text
      inRef = false;
      elems.push({ k: 'attr', role: attr.role, name: attr.name });
      continue;
    }

    if (/^>\s/.test(para)) {
      const bqText = para.split('\n').map(l => l.replace(/^>\s*/, '')).join(' ').trim();
      if (bqText) { inRef = false; elems.push({ k: 'bq', runs: parseMdRuns(bqText) }); }
      continue;
    }
    if (/^[-*_\s]{3,}$/.test(para.replace(/\s/g, ''))) {
      if (/^\*\s+\*\s+\*/.test(para.trim())) stopped = true;
      continue;
    }

    // Section number (e.g. **01**) followed by bold heading — match BEFORE
    // the ref regex so "**01**" / "**参考文献**" don't get mistaken for body text.
    if (/^\*\*0?\d+\*\*$/.test(para)) { expectHeading = true; continue; }
    if (expectHeading) {
      const hm = para.match(/^\*\*(.+)\*\*$/);
      if (hm) { inRef = false; elems.push({ k: 'h', text: hm[1] }); expectHeading = false; continue; }
      expectHeading = false;
    }
    // Standalone bold line (centered heading) → treat as heading. Runs before
    // the plain-text "参考X" regex so that "**参考文献**" becomes a heading
    // (bold emphasis marker) rather than opening a reference section.
    const boldOnly = para.match(/^\*\*(.+)\*\*$/);
    if (boldOnly && para.length < 60) {
      inRef = false;
      elems.push({ k: 'h', text: boldOnly[1] });
      continue;
    }

    if (/^(参考(来源|文献|资料|链接)|信息来源)/.test(para)) { inRef = true; elems.push({ k: 'refH', text: para.replace(/[：:].*$/, '').trim() }); continue; }
    if (inRef) { elems.push({ k: 'ref', runs: parseMdRuns(para) }); continue; }

    const imgMatch = para.match(/^!\[([^\]]*)\]\(([^)]+)\)\s*$/);
    if (imgMatch) {
      const imgSrc = imgMatch[2].replace(/"/g, '').replace(/</g, '').replace(/>/g, '');
      // Respect WeChat's data-w when it signals a decorative narrow image
      // (e.g. section-number banners at 279px). Without this, small images
      // get stretched to 100% of the content column.
      const dwMatch = imgSrc.match(/[#&]dataW=(\d+)/);
      const maxPx = dwMatch && Number(dwMatch[1]) > 0 && Number(dwMatch[1]) < 640
        ? Number(dwMatch[1]) : 0;
      imgN++;
      inRef = false;
      elems.push({ k: 'img', src: imgSrc, w: '100%', maxPx, gif: looksLikeGifSource(imgSrc) });
      continue;
    }

    elems.push({ k: 'txt', runs: parseMdRuns(para) });
  }

  return { elems, author, source, title, imgN };
}

// ---- HTML Rendering (class-based) ----
function render(elems, author, source) {
  const lines = [];

  // Author and source lines at the top
  if (author) {
    lines.push(sec(leafWrapAuthor('<span textstyle="" class="wx-ta">作者：' + esc(author) + '</span>'), !source));
  }
  if (source) {
    lines.push(sec(leafWrapAuthor('<span textstyle="" class="wx-ta">来源：' + esc(source) + '</span>'), true));
  }

  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    const nextK = i + 1 < elems.length ? elems[i + 1].k : null;
    const spaceAfter = nextK && nextK !== 'cap' &&
      !(e.k === 'ref' && nextK === 'ref') &&
      !(e.k === 'attr' && nextK === 'attr') &&
      !(e.k === 'img' && (nextK === 'cap' || nextK === 'img'));

    switch (e.k) {
      case 'h':
        lines.push(sec(leafWrap('<span textstyle="" class="wx-th">' + esc(e.text) + '</span>'), spaceAfter));
        break;
      case 'img': {
        const src = e.src || '';
        const isGif = e.gif || looksLikeGifSource(src);
        const rpol = src.startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
        const gifAttr = isGif ? ' data-type="gif"' : '';
        const sizeStyle = e.maxPx
          ? 'max-width: ' + e.maxPx + 'px; width: 100%; height: auto; display: block; margin: 0 auto;'
          : 'width: ' + e.w + '; display: block; margin: 0 auto;';
        lines.push('<section class="wx-pi' + (spaceAfter ? ' wx-gap' : '') + '">' +
          '<img src="' + src + '"' + gifAttr + rpol + ' style="' + sizeStyle + '" /></section>');
        break;
      }
      case 'cap':
        lines.push(secCenter(leafWrap('<span textstyle="" class="wx-tc">' + esc(e.text) + '</span>'), spaceAfter));
        break;
      case 'txt':
        lines.push(sec(renderRuns(e.runs), spaceAfter));
        break;
      case 'attr':
        lines.push(sec(leafWrapAuthor('<span textstyle="" class="wx-ta">' + esc(e.role) + '：' + esc(e.name) + '</span>'), spaceAfter));
        break;
      case 'refH': {
        const refHeadText = (e.text || '参考来源').replace(/[：:]?$/, '：');
        lines.push(sec(leafWrap('<span textstyle="" class="wx-tr">' + esc(refHeadText) + '</span>'), false));
        break;
      }
      case 'ref':
        lines.push(sec(renderRuns(e.runs, { textCls: 'wx-tr' }), spaceAfter));
        break;
      case 'bq':
        lines.push('<section class="wx-bq' + (spaceAfter ? ' wx-gap' : '') + '">' +
          renderRuns(e.runs, { textCls: 'wx-tr' }) + '</section>');
        break;
    }
  }
  return lines;
}

// ---- Blocks Classification (URL-import path) ----
// Input is already structurally classified by the Go DOM walker, so this
// step is light: apply punctuation conversion to runs, promote attribution
// paragraphs to 'attr' blocks, and track 参考文献/信息来源 sections. No
// visual-heuristic role-guessing — that was the whole point of moving to
// structured blocks. See api/wechat_blocks.go for the upstream contract.
function classifyBlocks({ title, blocks }) {
  const elems = [];
  let author = '', source = '', imgN = 0, inRef = false;

  for (const b of (blocks || [])) {
    if (!b || !b.k) continue;

    if (b.k === 'img') {
      const w = Number(b.dataW || 0);
      const maxPx = w > 0 && w < 640 ? w : 0;
      imgN++;
      inRef = false;
      elems.push({ k: 'img', src: b.src || '', w: '100%', maxPx, gif: looksLikeGifSource(b.src || '') });
      continue;
    }

    if (b.k === 'cap') {
      elems.push({ k: 'cap', text: b.text || '' });
      continue;
    }

    if (b.k === 'h') {
      const ht = (b.text || '').trim();
      if (/^(参考(来源|文献|资料|链接)|信息来源)/.test(ht)) {
        inRef = true;
        elems.push({ k: 'refH', text: ht.replace(/[：:].*$/, '').trim() });
      } else {
        inRef = false;
        elems.push({ k: 'h', text: ht });
      }
      continue;
    }

    // txt / bq / li — all carry runs. Apply punctuation pass.
    const runs = convertRuns(b.runs || []);
    // Text used for attribution / 参考文献 regex matching. Include both
    // txt and link run types so "作者：<a>张三</a>" still yields
    // "作者：张三" (link runs carry display text, not the URL).
    const text = runs
      .filter(r => r && (r.type === 'txt' || r.type === 'link'))
      .map(r => r.text || '')
      .join('')
      .trim();

    if (b.k === 'bq') {
      inRef = false;
      elems.push({ k: 'bq', runs });
      continue;
    }

    // Attribution — 作者 / 来源 / 编辑. Matches the classifyMd path exactly
    // so URL-import and MD-import produce the same attr blocks.
    const attr = parseAttribution(text);
    if (attr) {
      if (attr.role === '作者' && !author) author = attr.name;
      if (attr.role === '来源') source = attr.name;
      inRef = false;
      elems.push({ k: 'attr', role: attr.role, name: attr.name });
      continue;
    }

    // Reference section marker. Text-level, not style-level — same rule as MD path.
    if (/^(参考(来源|文献|资料|链接)|信息来源)[：:]?$/.test(text)) {
      inRef = true;
      elems.push({ k: 'refH', text: text.replace(/[：:]$/, '') });
      continue;
    }

    if (inRef) {
      elems.push({ k: 'ref', runs });
      continue;
    }

    // li and txt render the same (FBIF template has no dedicated list style);
    // collapsing them here keeps the renderer's switch simple.
    elems.push({ k: 'txt', runs });
  }

  return { elems, author, source, title: title || '', imgN };
}

export default {
  id: 'fbif',
  name: 'FBIF 公众号排版',
  description: '按 FBIF 排版规范格式化，支持标题/引言/图片说明/参考来源',
  formats: ['.docx', '.doc', '.md', '.txt'],

  processDocx({ paragraphs, imgCache }) {
    const { elems, author, imgN } = classifyDocx(paragraphs, imgCache);
    return { lines: render(elems, author, ''), imgN, headingN: elems.filter(e => e.k === 'h').length };
  },
  async processMd(text) {
    const { elems, author, source, title, imgN } = await classifyMd(text);
    return { lines: render(elems, author, source), imgN, headingN: elems.filter(e => e.k === 'h').length, title };
  },
  processBlocks(data) {
    const { elems, author, source, title, imgN } = classifyBlocks(data);
    return { lines: render(elems, author, source), imgN, headingN: elems.filter(e => e.k === 'h').length, title };
  },
};
