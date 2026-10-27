// 「格式清理」模板（2026-10-27 用户裁定）：粘贴进来的文字排成微信公众号编辑器自己的段落形状，
// 复制回编辑器就是正常格式，放进任何一篇文章或贴图文字框都不带别的样式。
//
// 输出只有这几种，没有 class、style、标题、署名、目录、页脚：
//   段落 <p>文字</p>（只留加粗 <strong>）；空行 <p><br></p>（原文连着几个空行也只出一个，开头结尾不出）；
//   图片 <p><img src></p>；表格 <table><tbody><tr><td>。视频丢掉。
// 预览外观（微信默认 17px / 1.6 / rgba(0,0,0,0.9)）由 app.css 按 #contentArea[data-template] 写，不进复制。
// 与其他渲染器物理隔离（红线 #19）：只 import engine 导出与中性的 docx-article-model。

const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter, convertRuns, convertText } = engineModule;
const docxModelModule = await import('../docx-article-model.js' + assetQuery);
const { classifyDocxArticle } = docxModelModule;

const BLANK_LINE = '<p><br></p>';

// 一段 runs 按换行切成几行，每行是 [{ text, bold }]；首尾空白去掉，nbsp 当普通空格。
function runLines(runs) {
  const lines = [[]];
  for (const run of runs || []) {
    if (!run) continue;
    if (run.type === 'br') { lines.push([]); continue; }
    if (run.type !== 'txt' && run.type !== 'link') continue;
    const parts = String(run.text || '').replace(/\u00A0/g, ' ').split(/\r?\n/);
    parts.forEach((text, i) => {
      if (i > 0) lines.push([]);
      if (text) lines[lines.length - 1].push({ text, bold: !!run.bold });
    });
  }
  return lines.map(trimLine);
}

function trimLine(segments) {
  const out = segments.map(s => ({ ...s }));
  while (out.length) {
    out[0].text = out[0].text.trimStart();
    if (out[0].text) break;
    out.shift();
  }
  while (out.length) {
    const last = out[out.length - 1];
    last.text = last.text.trimEnd();
    if (last.text) break;
    out.pop();
  }
  return out;
}

function segmentsHtml(segments) {
  let html = '';
  let bold = false;
  for (const segment of segments) {
    if (segment.bold !== bold) {
      html += bold ? '</strong>' : '<strong>';
      bold = segment.bold;
    }
    html += esc(segment.text);
  }
  return bold ? html + '</strong>' : html;
}

// 每种块渲染成几行：'' 是空行，其余是一个完整的 <p>/<table>。
function paragraphsFromRuns(elem) {
  const runs = Array.isArray(elem.runs) ? elem.runs : [{ type: 'txt', text: elem.text || '' }];
  return runLines(runs).map(segments => (segments.length ? '<p>' + segmentsHtml(segments) + '</p>' : ''));
}

function paragraphsFromText(elem) {
  return paragraphsFromRuns({ runs: [{ type: 'txt', text: convertText(String(elem.text || '')) }] });
}

function imageTag(src) {
  const referrer = /^https?:/i.test(src) ? ' referrerpolicy="no-referrer"' : '';
  return '<img src="' + escAttr(src) + '"' + referrer + '>';
}

function imageParagraph(elem) {
  const src = typeof elem.src === 'string' ? elem.src.trim() : '';
  return src ? ['<p>' + imageTag(src) + '</p>'] : [];
}

function cellHtml(cell) {
  const runs = (cell && cell.runs) || [];
  const images = runs.filter(r => r && r.type === 'img' && r.src).map(r => imageTag(r.src));
  return runLines(runs).filter(s => s.length).map(segmentsHtml).concat(images).join('<br>');
}

function tableHtml(elem) {
  const rows = (elem.rows || []).filter(row => Array.isArray(row) && row.length);
  if (!rows.length) return [];
  const body = rows.map(row => '<tr>' + row.map(cell => '<td>' + cellHtml(cell) + '</td>').join('') + '</tr>').join('');
  return ['<table><tbody>' + body + '</tbody></table>'];
}

const dropped = () => [];

// 块种类 → 渲染。小标题、引用、列表、图注在这里都是普通段落；旧写法 p/image/blockquote 同新写法。
const RENDERERS = {
  txt: paragraphsFromRuns,
  p: paragraphsFromRuns,
  li: paragraphsFromRuns,
  bq: paragraphsFromRuns,
  blockquote: paragraphsFromRuns,
  h: paragraphsFromText,
  cap: paragraphsFromText,
  img: imageParagraph,
  image: imageParagraph,
  table: tableHtml,
  video: dropped,
};

function render(elems) {
  const lines = [];
  let blank = false;
  for (const elem of elems) {
    if (!elem) continue;
    blank = blank || !!elem.blankBefore;
    for (const line of (RENDERERS[elem.k] || dropped)(elem)) {
      if (!line) { blank = true; continue; }
      if (blank && lines.length) lines.push(BLANK_LINE);
      blank = false;
      lines.push(line);
    }
  }
  return lines;
}

function result(elems) {
  const lines = render(elems);
  return {
    lines,
    imgN: lines.reduce((n, line) => n + (line.match(/<img\b/g) || []).length, 0),
    headingN: 0,
    title: '',
    articleMeta: {},
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

// Markdown 一行一段，原文空行（分隔线同）照出空行：整行图片出图片段，# 标题、> 引用、- 列表去掉标记。
// 粘贴的文字带图片链接时 engine 先把每行隔成空行，那条路上每行之间都会有空行。
function elemsFromMarkdown(text) {
  const { content } = parseMdFrontmatter(String(text || '').replace(/\r\n?/g, '\n'));
  const elems = [];
  let blankBefore = false;
  for (const raw of content.split('\n')) {
    const line = raw.trim();
    if (!line || /^([-*_]\s*){3,}$/.test(line)) { blankBefore = true; continue; }
    const image = line.match(/^!\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)$/);
    const body = line.replace(/^#{1,6}\s+/, '').replace(/^>\s?/, '').replace(/^[-*+]\s+/, '');
    elems.push(image ? { k: 'img', src: image[1], blankBefore } : { k: 'txt', runs: parseMdRuns(body), blankBefore });
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
  id: 'wechat-plain',
  name: '格式清理',
  description: '排成微信编辑器原生段落：只保留分段、空行与加粗',
  formats: ['.docx', '.doc', '.md', '.txt'],

  processDocx({ paragraphs, imgCache }) {
    return result(elemsFromDocx(paragraphs, imgCache));
  },
  async processMd(text) {
    return result(elemsFromMarkdown(text));
  },
  processBlocks(data) {
    return result(elemsFromBlocks(data && data.blocks));
  },
  // 明确出空底部：engine 不再回退到 footerUrl 的公众号页脚。
  renderBottomFooter() {
    return { html: '' };
  },
};
