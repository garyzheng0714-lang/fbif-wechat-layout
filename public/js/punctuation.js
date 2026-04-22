// Smart CJK punctuation converter.
//
// Rules (see design notes in plan):
//   1. Paragraphs with < 30% CJK characters are treated as English and left alone.
//   2. Chinese paragraphs convert ASCII punctuation to full-width, EXCEPT where
//      both neighboring non-space chars are ASCII alphanumeric (protects `Co., Ltd.`,
//      numbers like `35,000`, decimals, etc.).
//   3. Double and single quotes use stack-based directional pairing — never emits
//      same-direction pairs like `"你好"` or `"你好"`.
//   4. Fenced/inline code, URLs, emails, and HTML tags are masked before conversion
//      and restored after, so their contents are never modified.

const CJK_THRESHOLD = 0.20;

// ---- Public API ----

export function convertText(text, { locale = 'auto' } = {}) {
  if (!text) return text;
  const lang = locale === 'auto' ? detectLanguage(text) : locale;
  if (lang === 'en') return text;

  const { masked, slots } = mask(text);
  let out = masked;
  out = convertEllipsisAndDash(out);
  out = convertQuotes(out);
  out = convertBasicPunct(out);
  return unmask(out, slots);
}

export function convertRuns(runs) {
  if (!Array.isArray(runs) || runs.length === 0) return runs;
  const textRuns = runs.filter(r => r && r.type === 'txt' && typeof r.text === 'string');
  if (textRuns.length === 0) return runs;

  const joined = textRuns.map(r => r.text).join('');
  const lang = detectLanguage(joined);
  if (lang === 'en') return runs;

  const converted = convertText(joined, { locale: 'zh' });
  // convertText preserves total code-point count for all passes (1:1 mapping),
  // so we can split back to runs by their original text lengths.
  if (Array.from(converted).length !== Array.from(joined).length) {
    // Length mismatch (e.g. `...` → `……` collapses 3→2). Fall back to skipping
    // run-aware conversion to stay safe on format runs.
    return runs;
  }

  const pieces = [];
  const codePoints = Array.from(converted);
  let cursor = 0;
  for (const r of textRuns) {
    const len = Array.from(r.text).length;
    pieces.push(codePoints.slice(cursor, cursor + len).join(''));
    cursor += len;
  }

  let tIdx = 0;
  return runs.map(r => {
    if (r && r.type === 'txt' && typeof r.text === 'string') {
      const next = { ...r, text: pieces[tIdx] };
      tIdx++;
      return next;
    }
    return r;
  });
}

export function detectLanguage(text) {
  const ratio = cjkRatio(text);
  return ratio >= CJK_THRESHOLD ? 'zh' : 'en';
}

// ---- Language detection ----

function cjkRatio(text) {
  if (!text) return 0;
  let cjk = 0, total = 0;
  for (const ch of text) {
    if (/\s/.test(ch)) continue;
    total++;
    if (isCJK(ch)) cjk++;
  }
  return total === 0 ? 0 : cjk / total;
}

function isCJK(ch) {
  const code = ch.codePointAt(0);
  return (
    (code >= 0x4E00 && code <= 0x9FFF) ||
    (code >= 0x3400 && code <= 0x4DBF) ||
    (code >= 0x20000 && code <= 0x2A6DF) ||
    (code >= 0x3000 && code <= 0x303F) ||
    (code >= 0xFF00 && code <= 0xFFEF)
  );
}

// ---- Mask / Unmask ----
// Replace protected regions with placeholders N where N is an index.
// The sentinel byte U+E000 is in the Private Use Area and never occurs in
// legitimate text — it survives our punctuation passes unchanged.

const MASK_OPEN = '';
const MASK_CLOSE = '';

function mask(text) {
  const slots = [];
  const patterns = [
    /```[\s\S]*?```/g,
    /`[^`\n]+`/g,
    /<[^<>]+>/g,
    /https?:\/\/[^\s<>"']+/g,
    /\b[\w.+-]+@[\w-]+\.[\w.-]+\b/g,
  ];
  let out = text;
  for (const re of patterns) {
    out = out.replace(re, (m) => {
      const i = slots.length;
      slots.push(m);
      return `${MASK_OPEN}${i}${MASK_CLOSE}`;
    });
  }
  return { masked: out, slots };
}

function unmask(text, slots) {
  const re = new RegExp(`${MASK_OPEN}(\\d+)${MASK_CLOSE}`, 'g');
  return text.replace(re, (_, n) => slots[Number(n)] ?? '');
}

// ---- Basic punctuation conversion ----

const BASIC_MAP = {
  ',': '，',
  '.': '。',
  ';': '；',
  ':': '：',
  '!': '！',
  '?': '？',
  '(': '（',
  ')': '）',
};

function convertBasicPunct(text) {
  let out = '';
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    const full = BASIC_MAP[ch];
    if (full) {
      if (bothSidesAscii(text, i)) { out += ch; continue; }
      // Abbreviation / sentence-end guard: `.` after ASCII letter and followed
      // immediately by whitespace (or end-of-text) is an English period — keep.
      // Covers `Ltd. 的`, `Inc. 成立`, `etc. 还有`, `The quick brown fox.` etc.
      if (ch === '.' && i > 0 && /[A-Za-z]/.test(text[i - 1]) &&
          (i + 1 >= text.length || /\s/.test(text[i + 1]))) {
        out += ch; continue;
      }
      out += full;
    } else {
      out += ch;
    }
  }
  return out;
}

function bothSidesAscii(text, i) {
  return isAsciiTight(neighbor(text, i, -1)) && isAsciiTight(neighbor(text, i, +1));
}

// "Tight" ASCII: any printable ASCII non-space char. Keeps `Co., Ltd.`, `3.14`,
// `35,000`, `f(x,y)` untouched inside Chinese paragraphs.
function isAsciiTight(ch) {
  if (!ch) return false;
  const c = ch.charCodeAt(0);
  return c >= 0x21 && c <= 0x7E;
}

function neighbor(text, i, dir) {
  let j = i + dir;
  while (j >= 0 && j < text.length) {
    const c = text[j];
    if (!/[ \t]/.test(c)) return c;
    j += dir;
  }
  return '';
}


// ---- Ellipsis and em-dash ----

function convertEllipsisAndDash(text) {
  return text
    .replace(/\.{3,}/g, '……')
    .replace(/(?<![\w-])-{2,}(?![\w-])/g, '——');
}

// ---- Smart quote pairing ----
// Stack-based directional conversion for `"` and `'`.
// Apostrophes inside a word (`it's`, `don't`) are detected first.

function convertQuotes(text) {
  const chars = Array.from(text);
  const out = [];
  const stack = [];

  for (let i = 0; i < chars.length; i++) {
    const ch = chars[i];

    if (ch === '"' || ch === "'") {
      const prev = neighborArr(chars, i, -1);
      const next = neighborArr(chars, i, +1);

      if (ch === "'" && isWordChar(prev) && isWordChar(next)) {
        out.push('’');
        continue;
      }

      const isOpening = shouldOpen(prev, stack, ch);
      if (isOpening) {
        out.push(ch === '"' ? '“' : '‘');
        stack.push(ch);
      } else {
        out.push(ch === '"' ? '”' : '’');
        for (let s = stack.length - 1; s >= 0; s--) {
          if (stack[s] === ch) { stack.splice(s, 1); break; }
        }
      }
      continue;
    }

    out.push(ch);
  }

  return out.join('');
}

function neighborArr(chars, i, dir) {
  let j = i + dir;
  while (j >= 0 && j < chars.length) {
    const c = chars[j];
    if (!/[ \t]/.test(c)) return c;
    j += dir;
  }
  return '';
}

function shouldOpen(prev, stack, kind) {
  if (prev === '') return true;
  if (/[\s\n\r]/.test(prev)) return true;
  if (isOpeningPunct(prev)) return true;
  const hasMatchingOpen = stack.includes(kind);
  if (hasMatchingOpen) return false;
  return true;
}

function isOpeningPunct(ch) {
  return /[（(\[\{「『《【〈—,，。：:；;？?！!、“‘]/.test(ch);
}

function isWordChar(ch) {
  return /[A-Za-z0-9_]/.test(ch);
}
