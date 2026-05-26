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

// ---- 图片注释智能识别 ----

// 图片注释关键词（参考 /tmp/foodtalks-research/FoodtalksArticleLayout/layout-tool.html:1061-1074）
const IMAGE_CAPTION_KEYWORDS = [
  '图片来源', '资料来源', '数据来源', '来源',
  '制图', '截图', '图注', '注：',
  'Source', 'Photo', 'Image', 'Credit',
];

// ---- 标题启发式识别 ----

const TITLE_KEYWORDS = [
  '背景', '简介', '总结', '分析', '案例', '介绍', '概述', '结论', '展望',
];

// detectHeadingLevel: 返回 2 / 3 / 0（不是标题）
function detectHeadingLevel(text) {
  const t = String(text || '').trim();
  if (!t) return 0;

  // （一）（二）→ h3
  if (/^[（(]\s*[一二三四五六七八九十]+\s*[)）]/.test(t)) return 3;

  // 一、二、三...（中文标点：、：．: 后跟内容）→ h2
  if (/^[一二三四五六七八九十]+\s*[、：．:\s]/.test(t)) return 2;

  // 关键词 + 短句 + 无尾句号 → h2
  if (t.length <= 30 && !/[。.]$/.test(t)) {
    for (const kw of TITLE_KEYWORDS) {
      if (t.startsWith(kw)) return 2;
    }
  }

  return 0;
}

// 把被启发式识别为标题的普通段转换成 'h' 元素
function promoteHeuristicHeadings(elems) {
  return elems.map(e => {
    if (e.k !== 'p') return e;
    const plain = (e.runs || []).filter(r => r.type === 'txt').map(r => r.text).join('');
    const level = detectHeadingLevel(plain);
    if (level === 0) return e;
    return { k: 'h', level, runs: e.runs };
  });
}

function hasImageCaptionKeyword(text) {
  if (!text) return false;
  const lower = String(text).toLowerCase();
  for (const kw of IMAGE_CAPTION_KEYWORDS) {
    if (text.indexOf(kw) >= 0) return true;
    if (kw.length <= 8 && lower.indexOf(kw.toLowerCase()) >= 0) return true;
  }
  return false;
}

function stripHtml(text) {
  return String(text || '').replace(/<[^>]+>/g, '');
}

function isImageCaption(text) {
  const plain = stripHtml(text || '').trim();
  if (!plain || plain.length > 120) return false;
  if (hasImageCaptionKeyword(plain)) return true;
  if (/^(图|图注|图片|上图|下图|表|资料来源|数据来源|图片来源|来源|注|Source|Photo|Image|Credit)\s*[：:\s\d]/i.test(plain)) {
    return true;
  }
  if (/^[▲△▵▽▼]/.test(plain)) return true;
  const sentenceEnd = (plain.match(/[。！？!?]/g) || []).length;
  const commaCount = (plain.match(/[，,；;]/g) || []).length;
  return plain.length <= 45 && sentenceEnd === 0 && commaCount <= 1;
}

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

// 把图片后 3 段内最多 2 段"看起来像图注"的普通段提升为图注块
function promoteImageCaptions(elems) {
  const out = elems.slice();
  for (let i = 0; i < out.length; i++) {
    if (out[i].k !== 'img') continue;
    let promoted = 0;
    let scanned = 0;
    for (let j = i + 1; j < out.length && scanned < 3 && promoted < 2; j++) {
      const e = out[j];
      if (e.k === 'img' || e.k === 'h' || e.k === 'ref') break;
      if (e.k !== 'p') continue;
      scanned += 1;
      const plain = (e.runs || []).filter(r => r.type === 'txt').map(r => r.text).join('').trim();
      const isFirst = promoted === 0;
      let promote = false;
      if (isFirst) {
        promote = isImageCaption(plain);
      } else {
        promote = hasImageCaptionKeyword(plain);
      }
      if (promote) {
        out[j] = { k: 'cap', runs: e.runs };
        promoted += 1;
      } else {
        if (isFirst) break;
      }
    }
  }
  return out;
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
    return { lines: render(promoteImageCaptions(promoteHeuristicHeadings(elems))), imgN, headingN: elems.filter(e => e.k === 'h').length };
  },

  async processMd(text) {
    const { elems, imgN, title } = await classifyMd(text);
    return { lines: render(promoteImageCaptions(promoteHeuristicHeadings(elems))), imgN, headingN: elems.filter(e => e.k === 'h').length, title };
  },

  processBlocks(data) {
    const { elems, imgN, title } = classifyBlocks(data);
    return { lines: render(promoteImageCaptions(promoteHeuristicHeadings(elems))), imgN, headingN: elems.filter(e => e.k === 'h').length, title };
  },
};
