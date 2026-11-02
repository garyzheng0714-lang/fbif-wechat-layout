// 「贴图」载体模板（2026-10-27 用户裁定：「文章是自己的排版规则，贴图是贴图的规则」）。
// 公众号贴图编辑器丢掉段距和空块，只认一个段落里的换行：粘进去要的是 <p>甲<br><br>乙</p>（空行 = 连着两个 <br>）。
// 所以这里从解析出的原文块直接排成几行字，不经文章预览、不读文章规则，文章规则怎么改都不会串进来。
//
// 行（lines）：string[]，'' 是空行。段与段之间一律空一行，原文有没有空行都一样（2026-10-29 老板：「贴图默认
// 要确保它要有一行空白」；飞书、Word 复制的纯文字段间只有一个换行）；段内换行（Word 软换行、<br>）与表格各行
// 只换行；活动的时间、地点几行连着、不空行（EVENT_INFO_LINE）；开头结尾不出空行，也不连出两个空行。加粗、
// 颜色只留字；链接只留链到公众号文章的（贴图编辑器只认这种，粘进去变成文章链接；别的网址它只留字），其余只留字；
// 图片、视频丢掉。没有标题、署名、目录、页脚、更多文章、底部区块。标点照公众号口径转换。
// 预览与复制是同一个 <p>…</p>；预览外观在 app.css 的 #contentArea[data-template="sticker"]，不进剪贴板。
// 与其他渲染器物理隔离（红线 #19）：只 import engine 导出与中性的 docx-article-model。

const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter, markdownLineBody, convertRuns, convertText } = engineModule;
const docxModelModule = await import('../docx-article-model.js' + assetQuery);
const { classifyDocxArticle } = docxModelModule;

// 一行 = { text, html }；null 是空行。text 进 text/plain，html 进预览与 text/html。
const BLANK = null;

function cleanLine(text) {
  return String(text || '').replace(/\u00A0/g, ' ').trim();
}

function textLine(text) {
  const clean = cleanLine(text);
  return clean ? { text: clean, html: esc(clean) } : BLANK;
}

function textLines(text) {
  return String(text || '').split(/\r?\n/).map(textLine);
}

const WECHAT_ARTICLE_HOST = 'mp.weixin.qq.com';

function articleHref(href) {
  try {
    const url = new URL(String(href || ''));
    return /^https?:$/.test(url.protocol) && url.hostname === WECHAT_ARTICLE_HOST ? url.href : '';
  } catch {
    return '';
  }
}

// 段内的字按换行切成行，每行是几段字（链到公众号文章的带 href）；行首尾的空白去掉。
function runsLines(runs) {
  const rows = [[]];
  for (const run of runs || []) {
    if (!run) continue;
    if (run.type === 'br') { rows.push([]); continue; }
    if (run.type !== 'txt' && run.type !== 'link') continue;
    const href = run.type === 'link' ? articleHref(run.href) : '';
    String(run.text || '').replace(/\u00A0/g, ' ').split(/\r?\n/).forEach((part, i) => {
      if (i) rows.push([]);
      if (part) rows[rows.length - 1].push({ text: part, href });
    });
  }
  return rows.map(pieces => {
    const kept = pieces.slice();
    while (kept.length && !(kept[0].text = kept[0].text.trimStart())) kept.shift();
    while (kept.length && !(kept[kept.length - 1].text = kept[kept.length - 1].text.trimEnd())) kept.pop();
    if (!kept.length) return BLANK;
    return {
      text: kept.map(piece => piece.text).join(''),
      html: kept.map(piece => piece.href
        ? '<a href="' + escAttr(piece.href) + '">' + esc(piece.text) + '</a>'
        : esc(piece.text)).join(''),
    };
  });
}

function blockLines(elem) {
  const lines = runsLines(Array.isArray(elem.runs) ? elem.runs : [{ type: 'txt', text: elem.text || '' }]);
  const marker = cleanLine(elem.listMarker);
  const first = lines.findIndex(Boolean);
  if (marker && first >= 0) {
    lines[first] = { text: marker + ' ' + lines[first].text, html: esc(marker) + ' ' + lines[first].html };
  }
  return lines;
}

function plainLines(elem) {
  return textLines(convertText(String(elem.text || '')));
}

function tableLines(elem) {
  return (elem.rows || []).map(row => textLine((Array.isArray(row) ? row : [])
    .map(cell => runsLines(cell && cell.runs).filter(Boolean).map(line => line.text).join(' '))
    .filter(Boolean)
    .join(' ')));
}

const separator = () => [BLANK];

// 活动的时间、地点行（2026-11-02 用户，Mote 展商贴图：「时间和地点这2处不分行」）：以日历、时钟、图钉开头
// （📅📆🗓⏰⌚🕐–🕧📍📌），或写「时间：」「日期：」「地点：」「地址：」；连着的这几行之间不空行，前后照常空一行。
const EVENT_INFO_LINE = /^(?:•\s*)?(?:[\u{1F4C5}\u{1F4C6}\u{1F5D3}\u{23F0}\u{231A}\u{1F550}-\u{1F567}\u{1F4CD}\u{1F4CC}]|(?:时间|日期|地点|地址)\s*[:：])/u;
const isEventInfo = line => EVENT_INFO_LINE.test(line.text);

// 块种类 → 几行字。旧写法 p/image/blockquote 同新写法；不认识的块当空行。
const LINES_BY_KIND = {
  txt: blockLines,
  p: blockLines,
  li: blockLines,
  bq: blockLines,
  blockquote: blockLines,
  h: plainLines,
  cap: plainLines,
  table: tableLines,
  img: separator,
  image: separator,
  video: separator,
};

function stickerLines(elems) {
  const lines = [];
  for (const elem of elems || []) {
    if (!elem) continue;
    let blank = true;
    for (const line of (LINES_BY_KIND[elem.k] || separator)(elem)) {
      if (!line) { blank = true; continue; }
      if (blank && lines.length && !(isEventInfo(lines[lines.length - 1]) && isEventInfo(line))) lines.push(BLANK);
      blank = false;
      lines.push(line);
    }
  }
  return lines;
}

function result(elems) {
  const lines = stickerLines(elems);
  const html = '<p>' + lines.map(line => line ? line.html : '').join('<br>') + '</p>';
  return {
    lines: [html],
    imgN: 0,
    headingN: 0,
    title: '',
    articleMeta: {},
    clipboard: { html, text: lines.map(line => line ? line.text : '').join('\n') },
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

// Markdown 一行一段：空行与分隔线跳过，整行图片丢掉，# 标题、> 引用去掉记号，- 列表换成「•」。
function elemsFromMarkdown(text) {
  const { content } = parseMdFrontmatter(String(text || '').replace(/\r\n?/g, '\n'));
  const elems = [];
  for (const raw of content.split('\n')) {
    const line = raw.trim();
    if (!line || /^([-*_]\s*){3,}$/.test(line) || MD_IMAGE_LINE.test(line)) continue;
    const body = markdownLineBody(line).replace(MD_INLINE_IMAGE, '');
    elems.push({ k: 'txt', runs: parseMdRuns(body) });
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
  description: '排成公众号贴图文字：一段字，段间空一行',
  formats: ['.docx', '.doc', '.md', '.txt'],
  // 载体能力：只排正文（engine 不出标题、页脚、更多文章、底部区块），复制交 result.clipboard。
  bodyOnly: true,
  // 导入框只把这里留下的链接画蓝（import-links.js）。
  keepsLink: href => !!articleHref(href),

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
