// 粘贴进导入框的超链接。导入框只收纯文字，粘贴时从剪贴板 text/html 记下链接（锚文字 + 地址，按出现顺序），
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
