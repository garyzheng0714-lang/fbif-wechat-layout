// 从网页 HTML 读出小标题清单，一行一个「h2 标题」。考卷的答案（编辑改定稿）和我们排出来的网站稿用同一个读法。
const NAMED = {
  amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ',
  ldquo: '“', rdquo: '”', lsquo: '‘', rsquo: '’', mdash: '—', ndash: '–', hellip: '…', middot: '·',
};

export function decode(text) {
  return text.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (all, e) => {
    if (e[0] === '#') return String.fromCodePoint(e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : Number(e.slice(1)));
    return NAMED[e.toLowerCase()] ?? all;
  });
}

export function outlineFromHTML(html) {
  const out = [];
  for (const m of String(html || '').matchAll(/<(h[2-4])\b[^>]*>([\s\S]*?)<\/\1>/gi)) {
    const text = decode(m[2].replace(/<[^>]+>/g, '')).replace(/\s+/g, ' ').trim();
    if (text) out.push(`${m[1].toLowerCase()} ${text}`);
  }
  return out;
}
