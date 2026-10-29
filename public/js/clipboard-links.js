// 粘贴进导入框的超链接与列表记号。导入框只收纯文字，粘贴时从剪贴板 text/html 记下链接（锚文字 + 地址，按出现顺序），
// 记在框的 data-links 上，随框里的字一起进草稿。排版（parser.js 挂回正文）和框里画蓝字（import-links.js）
// 都用 linkRanges 认锚文字，所以框里是蓝字的，排出来就带链接。只认 http(s)。

const HTML_ANCHOR_RE = /<a\b[^>]*?\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))[^>]*>([\s\S]*?)<\/a>/gi;
const HTML_ENTITIES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' };

function decodeHtmlText(text) {
  return String(text || '').replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (all, name) => {
    if (name[0] === '#') {
      const code = name[1] === 'x' || name[1] === 'X' ? parseInt(name.slice(2), 16) : parseInt(name.slice(1), 10);
      return Number.isFinite(code) ? String.fromCodePoint(code) : all;
    }
    return HTML_ENTITIES[name.toLowerCase()] ?? all;
  });
}

function isWebLink(href) {
  return /^https?:\/\//i.test(String(href || ''));
}

export function extractClipboardLinks(html) {
  const links = [];
  for (const m of String(html || '').matchAll(HTML_ANCHOR_RE)) {
    const href = decodeHtmlText(m[1] ?? m[2] ?? m[3] ?? '').trim();
    const text = decodeHtmlText(m[4].replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim();
    if (text && isWebLink(href)) links.push({ text, href });
  }
  return links;
}

// 链接按顺序认：每条从上一条之后找锚文字，找不到（被删了、改了字）就跳过这条，不挡后面的；锚文字不跨行。
// lines: string[] → 每行一组 { start, end, href }。
export function linkRanges(lines, links) {
  const anchors = (links || []).filter(link => link && link.text && isWebLink(link.href));
  let next = 0;
  return lines.map((line, i) => {
    const ranges = [];
    let pos = 0;
    while (next < anchors.length) {
      const { text, href } = anchors[next];
      const at = line.indexOf(text, pos);
      if (at < 0) {
        if (lines.slice(i + 1).some(later => later.includes(text))) break;
        next += 1;
        continue;
      }
      ranges.push({ start: at, end: at + text.length, href });
      pos = at + text.length;
      next += 1;
    }
    return ranges;
  });
}

// 列表项的圆点、序号：选中复制网页（AI 答复、文章）时浏览器给的纯文字里没有，只在 text/html 的 <li> 里。
// 每项取自己那行字（嵌套的下级列表另算一项），无序记「•」，有序按 start 数「3.」。
const LIST_TAG_RE = /<(\/?)(ul|ol|li)\b([^>]*)>/gi;

function listItemText(html) {
  const text = decodeHtmlText(html.replace(/<br\b[^>]*>|<\/(?:p|div|h[1-6])>/gi, '\n').replace(/<[^>]*>/g, ''));
  return text.split('\n').map(line => line.replace(/\s+/g, ' ').trim()).find(Boolean) || '';
}

export function extractClipboardListItems(html) {
  const source = String(html || '');
  const items = [];
  const lists = [];
  let item = null;
  let last = 0;
  const finish = () => {
    const text = item && listItemText(item.html);
    if (text) items.push({ text, marker: item.marker });
    item = null;
  };
  for (const m of source.matchAll(LIST_TAG_RE)) {
    if (item) item.html += source.slice(last, m.index);
    last = m.index + m[0].length;
    const [, closing, tag, attrs] = m;
    finish();
    const name = tag.toLowerCase();
    if (name === 'li') {
      const list = lists[lists.length - 1];
      if (!closing && list) item = { html: '', marker: list.ordered ? `${list.next++}.` : '•' };
    } else if (closing) {
      lists.pop();
    } else {
      const start = /\bstart\s*=\s*["']?(\d+)/i.exec(attrs);
      lists.push({ ordered: name === 'ol', next: start ? Number(start[1]) : 1 });
    }
  }
  return items;
}

const LEADING_LIST_MARKER_RE = /^(?:[•◦▪·*+-]|\d+[.)、])\s*/;

// 按出现顺序认：每项从上一项之后找字一样的那一行补上记号；那行已经带着记号就不补，找不到就跳过这项。
export function withListMarkers(text, items) {
  const lines = String(text || '').split('\n');
  let next = 0;
  for (const { text: itemText, marker } of items || []) {
    for (let i = next; i < lines.length; i++) {
      const plain = lines[i].replace(/\s+/g, ' ').trim();
      if (plain !== itemText && plain.replace(LEADING_LIST_MARKER_RE, '') !== itemText) continue;
      if (plain === itemText) lines[i] = lines[i].replace(/^(\s*)/, `$1${marker} `);
      next = i + 1;
      break;
    }
  }
  return lines.join('\n');
}

export function readImportLinks(input) {
  try {
    const links = JSON.parse((input && input.dataset && input.dataset.links) || '[]');
    return Array.isArray(links) ? links : [];
  } catch {
    return [];
  }
}

export function writeImportLinks(input, links) {
  if (input && input.dataset) input.dataset.links = JSON.stringify(links || []);
}
