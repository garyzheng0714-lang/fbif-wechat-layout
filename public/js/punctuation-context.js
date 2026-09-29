// Text-only context for punctuation. Indices are UTF-16 offsets into the source,
// never offsets into a shortened result; renderers and account names stay out.
export function isCJK(ch) {
  const n = ch?.codePointAt(0) || 0;
  return (n >= 0x4e00 && n <= 0x9fff) || (n >= 0x3400 && n <= 0x4dbf) ||
    (n >= 0x20000 && n <= 0x2a6df) || (n >= 0x3000 && n <= 0x303f) ||
    (n >= 0xff00 && n <= 0xffef);
}

export function hasHan(text) {
  return /[\u3400-\u4dbf\u4e00-\u9fff\u{20000}-\u{2a6df}]/u.test(text);
}

export function neighbor(text, i, direction) {
  let j = i + direction;
  while (j >= 0 && j < text.length && /[ \t\n\r\u3000\u00a0]/.test(text[j])) j += direction;
  if (j < 0 || j >= text.length) return '';
  if (/[\uDC00-\uDFFF]/.test(text[j]) && j > 0) j--;
  return String.fromCodePoint(text.codePointAt(j));
}

export function isAsciiTight(ch) {
  const n = ch?.charCodeAt(0) || 0;
  return n >= 0x21 && n <= 0x7e;
}

// Protect actual source ranges instead of mask sentinels. No collision with
// user text, no expansion, and a URL/code token can span several rich-text runs.
export function protectedTextIndexes(text) {
  const indexes = new Set();
  for (const pattern of [
    /```[\s\S]*?```/g,
    /`[^`\n]+`/g,
    /<[^<>]+>/g,
    /https?:\/\/[^\s<>"'，。；：？！、）】」』》〉\uFFFC]+/g,
    /\b[\w.+-]+@[\w-]+\.[\w.-]+\b/g,
  ]) {
    for (const match of text.matchAll(pattern)) {
      for (let i = match.index; i < match.index + match[0].length; i++) indexes.add(i);
    }
  }
  return indexes;
}

export function leadingOrdinalPeriodIndexes(text) {
  const indexes = new Set();
  for (const m of text.matchAll(/(^|\n)[ \t\u3000\u00a0]*\d{1,3}(?=\.(?!\d))/g)) {
    indexes.add(m.index + m[0].length);
  }
  return indexes;
}

export function pairedFragments(text, protectedIndexes) {
  const pairs = [], stack = [];
  const openToClose = new Map([['(', ')'], ['（', '）'], ['“', '”'], ['‘', '’']]);
  for (let i = 0; i < text.length; i++) {
    if (protectedIndexes.has(i)) continue;
    const ch = text[i];
    if (openToClose.has(ch)) { stack.push({ ch, i }); continue; }
    if (!/[)）”’]/.test(ch)) continue;
    for (let j = stack.length - 1; j >= 0; j--) {
      const open = stack[j];
      const parens = /[（(]/.test(open.ch) && /[）)]/.test(ch);
      if (!parens && openToClose.get(open.ch) !== ch) continue;
      let content = '';
      for (let k = open.i + 1; k < i; k++) {
        if (!protectedIndexes.has(k)) content += text[k];
      }
      pairs.push({ start: open.i, end: i, content, parens });
      stack.splice(j, 1);
      break;
    }
  }
  return pairs;
}

export function englishFragmentProtectedIndexes(pairs, text) {
  const indexes = new Set();
  for (const pair of pairs) {
    if (hasHan(pair.content) || !/[A-Za-z]/.test(pair.content)) continue;
    for (let i = pair.start + 1; i < pair.end; i++) {
      if (/[.,;:!?]/.test(text[i])) indexes.add(i);
    }
  }
  return indexes;
}
