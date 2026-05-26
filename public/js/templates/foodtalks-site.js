// FoodTalks 网站排版模板 — 输出 inline-style HTML，给 FoodTalks 网站后台编辑器粘贴用
// 输出契约（参考 /tmp/foodtalks-research/FoodtalksArticleLayout/layout-tool.html）：
//   - <h2>/<h3> 纯标签，无 style
//   - <p> 纯标签，无 style
//   - 空行：<p><br /></p>（显式标签，不是 CSS margin）
//   - 图片：<p style="text-align: center;"><img width="600" style="width: 600px; max-width: 100%; height: auto; display: inline-block;"></p>
//   - 图注：<p style="text-align: center;"><span style="font-size: 12px; color: #7f7f7f;">...</span></p>
//   - 参考文献：<p><span style="font-size: 16px; color: #7f7f7f;">[1] ...</span></p>，自动补编号

const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter } = engineModule;

// ---- 输出基本元 ----

const BLANK_LINE = '<p><br /></p>';

function renderRuns(runs) {
  return (runs || []).filter(r => r.type !== 'img').map(r => {
    if (r.type === 'txt') {
      let s = esc(r.text);
      if (r.bold) s = '<strong>' + s + '</strong>';
      if (r.italic) s = '<em>' + s + '</em>';
      return s;
    }
    if (r.type === 'link') {
      const h = r.href || '';
      if (!/^https?:\/\//i.test(h)) return esc(r.text);
      return '<a href="' + escAttr(h) + '">' + esc(r.text) + '</a>';
    }
    return '';
  }).join('');
}

function renderHeading(level, text) {
  if (level === 3) return `<h3>${esc(text)}</h3>`;
  return `<h2>${esc(text)}</h2>`;
}

function renderParagraph(runs) {
  const inner = renderRuns(runs);
  if (!inner) return BLANK_LINE;
  return `<p>${inner}</p>`;
}

// 极端狭长图自适应宽度（参考 /tmp/foodtalks-research/FoodtalksArticleLayout/layout-tool.html:2808-2818）
function adaptiveImageWidth(naturalWidth, naturalHeight) {
  const w = Number(naturalWidth || 0);
  const h = Number(naturalHeight || 0);
  if (!w || !h) return 600;
  const ratio = h / w;
  if (ratio >= 3) return 280;
  if (ratio >= 2.2) return 360;
  if (ratio >= 1.6) return 480;
  return 600;
}

function renderImage(src, alt, naturalWidth, naturalHeight) {
  const safeSrc = escAttr(src || '');
  const safeAlt = escAttr(alt || '');
  const width = adaptiveImageWidth(naturalWidth, naturalHeight);
  return (
    `<p style="text-align: center;">` +
    `<img src="${safeSrc}" alt="${safeAlt}" width="${width}" ` +
    `style="width: ${width}px; max-width: 100%; height: auto; display: inline-block;">` +
    `</p>`
  );
}

function renderImageCaption(text) {
  return (
    `<p style="text-align: center;">` +
    `<span style="font-size: 12px; color: #7f7f7f;">${esc(text)}</span>` +
    `</p>`
  );
}

function renderReference(text, indexOneBased) {
  const numbered = ensureReferenceNumber(text, indexOneBased);
  return (
    `<p>` +
    `<span style="font-size: 16px; color: #7f7f7f;">${esc(numbered)}</span>` +
    `</p>`
  );
}

// ---- 参考文献编号 ----

function ensureReferenceNumber(content, indexOneBased) {
  const text = String(content || '').trim();
  const m = text.match(/^\[(\d+)\]\s*(.*)$/);
  if (m) {
    return `[${m[1]}] ${m[2]}`;
  }
  return `[${indexOneBased}] ${text}`;
}

// ---- 渲染主入口：把分类后的 elems 拼装成 HTML 行数组 ----

function render(elems) {
  const lines = [];
  let refIndex = 0;
  let prevWasHeading = false;
  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    if (e.k === 'h') {
      if (!prevWasHeading && lines.length > 0) lines.push(BLANK_LINE);
      lines.push(renderHeading(e.level === 3 ? 3 : 2, runsToPlain(e.runs)));
      prevWasHeading = true;
      continue;
    }
    if (e.k === 'img') {
      lines.push(renderImage(e.src, e.alt, e.width, e.height));
      prevWasHeading = false;
      continue;
    }
    if (e.k === 'cap') {
      lines.push(renderImageCaption(runsToPlain(e.runs)));
      prevWasHeading = false;
      continue;
    }
    if (e.k === 'ref') {
      refIndex += 1;
      lines.push(renderReference(runsToPlain(e.runs), refIndex));
      prevWasHeading = false;
      continue;
    }
    lines.push(renderParagraph(e.runs || []));
    lines.push(BLANK_LINE);
    prevWasHeading = false;
  }
  return lines;
}

function runsToPlain(runs) {
  return (runs || []).filter(r => r.type === 'txt').map(r => r.text).join('');
}

// ---- DOCX 分类（M1 最小版） ----

function classifyDocx(paragraphs, imgCache) {
  const elems = [];
  for (const pd of paragraphs || []) {
    const runs = pd.runs || [];
    const imgRun = runs.find(r => r.type === 'img');
    if (imgRun) {
      const src = imgCache && imgCache[imgRun.id] ? imgCache[imgRun.id] : (imgRun.src || '');
      elems.push({
        k: 'img',
        src,
        alt: imgRun.alt || '',
        width: imgRun.width || 0,
        height: imgRun.height || 0,
      });
      continue;
    }
    const text = runs.filter(r => r.type === 'txt').map(r => r.text).join('').trim();
    if (!text) continue;
    const styleName = (pd.styleName || '').toLowerCase();
    if (/heading\s*1|heading\s*2/.test(styleName)) {
      elems.push({ k: 'h', level: 2, runs });
      continue;
    }
    if (/heading\s*[3-6]/.test(styleName)) {
      elems.push({ k: 'h', level: 3, runs });
      continue;
    }
    elems.push({ k: 'p', runs });
  }
  return { elems, imgN: elems.filter(e => e.k === 'img').length };
}

// ---- Markdown 分类（最小版） ----

async function classifyMd(text) {
  const fm = parseMdFrontmatter(text);
  const body = fm.body || text;
  const lines = body.split(/\r?\n/);
  const elems = [];
  let imgN = 0;
  for (const line of lines) {
    if (!line.trim()) continue;
    let m;
    if ((m = line.match(/^#{1,2}\s+(.*)$/))) {
      elems.push({ k: 'h', level: 2, runs: parseMdRuns(m[1]) });
      continue;
    }
    if ((m = line.match(/^#{3,6}\s+(.*)$/))) {
      elems.push({ k: 'h', level: 3, runs: parseMdRuns(m[1]) });
      continue;
    }
    if ((m = line.match(/^!\[([^\]]*)\]\(([^)]+)\)\s*$/))) {
      elems.push({ k: 'img', src: m[2], alt: m[1] });
      imgN += 1;
      continue;
    }
    elems.push({ k: 'p', runs: parseMdRuns(line) });
  }
  return { elems, imgN, title: (fm.frontmatter && fm.frontmatter.title) || '' };
}

// ---- Blocks 分类（从后端 /api/fetch-article 来的结构化块） ----

function classifyBlocks(data) {
  const elems = [];
  let imgN = 0;
  for (const b of (data && data.blocks) || []) {
    if (b.type === 'h1' || b.type === 'h2') {
      elems.push({ k: 'h', level: 2, runs: [{ type: 'txt', text: b.text || '' }] });
      continue;
    }
    if (b.type === 'h3' || b.type === 'h4' || b.type === 'h5' || b.type === 'h6') {
      elems.push({ k: 'h', level: 3, runs: [{ type: 'txt', text: b.text || '' }] });
      continue;
    }
    if (b.type === 'img') {
      elems.push({
        k: 'img',
        src: b.src || '',
        alt: b.alt || '',
        width: b.width || 0,
        height: b.height || 0,
      });
      imgN += 1;
      continue;
    }
    if (b.type === 'cap' || b.type === 'caption') {
      elems.push({ k: 'cap', runs: [{ type: 'txt', text: b.text || '' }] });
      continue;
    }
    if (b.type === 'ref' || b.type === 'reference') {
      elems.push({ k: 'ref', runs: [{ type: 'txt', text: b.text || '' }] });
      continue;
    }
    const runs = Array.isArray(b.runs) && b.runs.length
      ? b.runs
      : [{ type: 'txt', text: b.text || '' }];
    elems.push({ k: 'p', runs });
  }
  return { elems, imgN, title: (data && data.title) || '' };
}

// ---- Template export ----

export default {
  id: 'foodtalks-site',
  name: 'FoodTalks 网站排版',
  description: '按 FoodTalks 网站后台规范输出 inline-style HTML',
  formats: ['.docx', '.doc', '.md', '.txt'],

  processDocx({ paragraphs, imgCache }) {
    const { elems, imgN } = classifyDocx(paragraphs, imgCache);
    return { lines: render(elems), imgN, headingN: elems.filter(e => e.k === 'h').length };
  },

  async processMd(text) {
    const { elems, imgN, title } = await classifyMd(text);
    return { lines: render(elems), imgN, headingN: elems.filter(e => e.k === 'h').length, title };
  },

  processBlocks(data) {
    const { elems, imgN, title } = classifyBlocks(data);
    return { lines: render(elems), imgN, headingN: elems.filter(e => e.k === 'h').length, title };
  },
};
