// 「贴图」载体模板（2026-10-27 用户裁定：「文章是自己的排版规则，贴图是贴图的规则」）。
// 公众号贴图编辑器丢掉段距和空块，只认一个段落里的换行：粘进去要的是 <p>甲<br><br>乙</p>（空行 = 连着两个 <br>）。
// 所以这里从解析出的原文块直接排成几行字，不经文章预览、不读文章规则，文章规则怎么改都不会串进来。
//
// 行（lines）：string[]，'' 是空行。原文空行（blankBefore、Markdown 空行或分隔线、Word 空段）出一个空行；
// 连着的行只换行；开头结尾不出空行，也不连出两个空行。加粗、链接、颜色只留字；图片、视频丢掉，
// 夹在两段字中间时当空行。没有标题、署名、目录、页脚、更多文章、底部区块。标点照公众号口径转换。
// 预览与复制是同一个 <p>…</p>；预览外观在 app.css 的 #contentArea[data-template="sticker"]，不进剪贴板。
// 与其他渲染器物理隔离（红线 #19）：只 import engine 导出与中性的 docx-article-model。

const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, parseMdRuns, parseMdFrontmatter, convertRuns, convertText } = engineModule;
const docxModelModule = await import('../docx-article-model.js' + assetQuery);
const { classifyDocxArticle } = docxModelModule;

const BLANK = '';

function cleanLine(text) {
  return String(text || '').replace(/\u00A0/g, ' ').trim();
}

function textLines(text) {
  return String(text || '').split(/\r?\n/).map(cleanLine);
}

function runsText(runs) {
  return (runs || []).map(run => {
    if (!run) return '';
    if (run.type === 'br') return '\n';
    return run.type === 'txt' || run.type === 'link' ? String(run.text || '') : '';
  }).join('');
}

function runsLines(elem) {
  const runs = Array.isArray(elem.runs) ? elem.runs : [{ type: 'txt', text: elem.text || '' }];
  const lines = textLines(runsText(runs));
  const marker = cleanLine(elem.listMarker);
  if (marker) {
    const first = lines.findIndex(Boolean);
    if (first >= 0) lines[first] = marker + ' ' + lines[first];
  }
  return lines;
}

function plainLines(elem) {
  return textLines(convertText(String(elem.text || '')));
}

function tableLines(elem) {
  return (elem.rows || []).map(row => (Array.isArray(row) ? row : [])
    .map(cell => textLines(runsText(cell && cell.runs)).filter(Boolean).join(' '))
    .filter(Boolean)
    .join(' '));
}

const separator = () => [BLANK];

// 块种类 → 几行字。旧写法 p/image/blockquote 同新写法；不认识的块当空行。
const LINES_BY_KIND = {
  txt: runsLines,
  p: runsLines,
  li: runsLines,
  bq: runsLines,
  blockquote: runsLines,
  h: plainLines,
  cap: plainLines,
  table: tableLines,
  img: separator,
  image: separator,
  video: separator,
};

function stickerLines(elems) {
  const lines = [];
  let blank = false;
  for (const elem of elems || []) {
    if (!elem) continue;
    blank = blank || !!elem.blankBefore;
    for (const line of (LINES_BY_KIND[elem.k] || separator)(elem)) {
      if (!line) { blank = true; continue; }
      if (blank && lines.length) lines.push(BLANK);
      blank = false;
      lines.push(line);
    }
  }
  return lines;
}

function result(elems) {
  const lines = stickerLines(elems);
  const html = '<p>' + lines.map(esc).join('<br>') + '</p>';
  return {
    lines: [html],
    imgN: 0,
    headingN: 0,
    title: '',
    articleMeta: {},
    clipboard: { html, text: lines.join('\n') },
  };
}

// URL 导入、粘贴纯文本的块：标点照公众号模板的口径补一遍（粘贴文本已转过，再转不变）。
function elemsFromBlocks(blocks) {
  return (blocks || []).filter(b => b && b.k).map(block => {
    if (Array.isArray(block.runs)) return { ...block, runs: convertRuns(block.runs) };
    if (block.k === 'table') {
      return { ...block, rows: (block.rows || []).map(row => (row || []).map(cell => ({ ...cell, runs: convertRuns((cell && cell.runs) || []) }))) };
    }
    return block;
  });
}

const MD_IMAGE_LINE = /^!\[[^\]]*\]\([^)]*\)$/;
const MD_INLINE_IMAGE = /!\[[^\]]*\]\([^)]*\)/g;

// Markdown 一行一行排：空行与分隔线是原文空行，整行图片丢掉，# 标题、> 引用、- 列表去掉标记只留字。
function elemsFromMarkdown(text) {
  const { content } = parseMdFrontmatter(String(text || '').replace(/\r\n?/g, '\n'));
  const elems = [];
  let blankBefore = false;
  for (const raw of content.split('\n')) {
    const line = raw.trim();
    if (!line || /^([-*_]\s*){3,}$/.test(line)) { blankBefore = true; continue; }
    if (MD_IMAGE_LINE.test(line)) {
      elems.push({ k: 'img', blankBefore });
    } else {
      const body = line.replace(/^#{1,6}\s+/, '').replace(/^>\s?/, '').replace(/^[-*+]\s+/, '').replace(MD_INLINE_IMAGE, '');
      elems.push({ k: 'txt', runs: parseMdRuns(body), blankBefore });
    }
    blankBefore = false;
  }
  return elems;
}

// Word：中性分类，不认作者行、参考来源、前置材料（全文都是正文）；Word 空段即空行。
const NEVER = /(?!)/;
function elemsFromDocx(paragraphs, imgCache) {
  return classifyDocxArticle(paragraphs, imgCache, {
    authorLineRegex: NEVER,
    consumeAuthorLine: false,
    classifyReferences: false,
  }).elems;
}

export default {
  id: 'sticker',
  name: '贴图',
  description: '排成公众号贴图文字：一段字，空行照原文',
  formats: ['.docx', '.doc', '.md', '.txt'],
  // 载体能力：只排正文（engine 不出标题、页脚、更多文章、底部区块），复制交 result.clipboard。
  bodyOnly: true,

  processDocx({ paragraphs, imgCache }) {
    return result(elemsFromDocx(paragraphs, imgCache));
  },
  async processMd(text) {
    return result(elemsFromMarkdown(text));
  },
  processBlocks(data) {
    return result(elemsFromBlocks(data && data.blocks));
  },
};
