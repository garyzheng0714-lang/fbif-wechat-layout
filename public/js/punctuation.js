// Smart CJK punctuation converter.
//
// Rules:
//   1. Paragraphs with < 20% CJK characters are treated as English and left alone.
//   2. Chinese paragraphs convert ASCII punctuation to full-width, EXCEPT where
//      both neighboring non-space chars are ASCII alphanumeric (protects `Co., Ltd.`,
//      numbers like `35,000`, decimals, etc.).
//   3. Double and single quotes use stack-based directional pairing — never emits
//      same-direction pairs like `"你好"` or `"你好"`. Pre-existing curly quotes in
//      the input are registered on the same stack so ASCII and curly mix correctly.
//   4. Quote stack resets on paragraph breaks (two or more consecutive newlines)
//      so an unclosed quote in one paragraph does not corrupt the next.
//   5. Fenced/inline code, URLs, emails, and HTML tags are masked before conversion
//      and restored after, so their contents are never modified. URL masks stop at
//      trailing CJK punctuation like `。，；：？！、）】」』》` so a Chinese period
//      after a URL is never eaten.
//   6. Whitespace probes (neighbor lookup) skip ` \t\n\r\u3000\u00A0` so pairing
//      does not break on fullwidth / no-break spaces or soft line breaks.

const CJK_THRESHOLD = 0.20;

// ---- Public API ----

export function convertText(text, { locale = 'auto' } = {}) {
  if (!text) return text;
  const lang = locale === 'auto' ? detectLanguage(text) : locale;
  if (lang === 'en') return text;

  // Split on hard paragraph breaks (two+ newlines, spaces allowed between) and
  // convert each paragraph in isolation so the quote stack resets.
  // The split pattern is captured, so separators are preserved in the rejoin.
  const parts = text.split(/(\r?\n[ \t]*(?:\r?\n[ \t]*)+)/);
  if (parts.length === 1) return convertSingleParagraph(text);
  return parts.map((part, i) => (i % 2 === 1 ? part : convertSingleParagraph(part))).join('');
}

function convertSingleParagraph(text) {
  if (!text) return text;
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

  // Fast path: joined conversion preserved codepoint count (1:1 per char).
  // We can slice `converted` back into the original run boundaries while
  // keeping cross-run quote pairing (e.g. quote opens in one bold run and
  // closes in the next).
  const origCount = Array.from(joined).length;
  const convCount = Array.from(converted).length;
  if (convCount === origCount) {
    const codePoints = Array.from(converted);
    let cursor = 0;
    return runs.map(r => {
      if (r && r.type === 'txt' && typeof r.text === 'string') {
        const len = Array.from(r.text).length;
        const next = { ...r, text: codePoints.slice(cursor, cursor + len).join('') };
        cursor += len;
        return next;
      }
      return r;
    });
  }

  // Slow path: codepoint count changed (e.g. `...` → `……` collapses 3→2, or
  // mask sentinels inflated an HTML fragment). Previously we bailed with an
  // unconverted copy — that silently dropped every quote in the paragraph.
  // Instead, convert each text run independently. Cross-run quote pairing is
  // lost in this mode, but per-run quotes, ellipses and basic punctuation
  // still get converted, which is strictly better than no conversion.
  return runs.map(r => {
    if (r && r.type === 'txt' && typeof r.text === 'string') {
      return { ...r, text: convertText(r.text, { locale: 'zh' }) };
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
// Replace protected regions with placeholders so their contents are never
// touched by punctuation passes.
//
// Sentinels use a 2-codepoint prefix (U+E000 U+E001) + 4-digit zero-padded
// index + 2-codepoint suffix (U+E002 U+E003), all in the Private Use Area.
// A stray single PUA char in the input (rare but real — some legacy Chinese
// fonts use U+E000) cannot collide with the multi-codepoint sentinel. If an
// out-of-range sentinel does appear, unmask preserves the raw chars instead
// of silently dropping them.

const MASK_OPEN = '\uE000\uE001';
const MASK_CLOSE = '\uE002\uE003';
const MASK_INDEX_WIDTH = 4;

function maskSlot(index) {
  return `${MASK_OPEN}${String(index).padStart(MASK_INDEX_WIDTH, '0')}${MASK_CLOSE}`;
}

function mask(text) {
  const slots = [];
  const patterns = [
    /```[\s\S]*?```/g,
    /`[^`\n]+`/g,
    /<[^<>]+>/g,
    // Stop URL at whitespace, angle brackets, ASCII quotes, AND trailing CJK
    // punctuation so a sentence-ending `。` after a URL is never eaten.
    /https?:\/\/[^\s<>"'，。；：？！、）】」』》〉]+/g,
    /\b[\w.+-]+@[\w-]+\.[\w.-]+\b/g,
  ];
  let out = text;
  for (const re of patterns) {
    out = out.replace(re, (m) => {
      const i = slots.length;
      slots.push(m);
      return maskSlot(i);
    });
  }
  return { masked: out, slots };
}

function unmask(text, slots) {
  const re = new RegExp(`${MASK_OPEN}(\\d{${MASK_INDEX_WIDTH}})${MASK_CLOSE}`, 'g');
  return text.replace(re, (match, n) => {
    const idx = Number(n);
    if (idx < 0 || idx >= slots.length) return match;
    return slots[idx];
  });
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

// Skip horizontal space, tab, line breaks, fullwidth space, and no-break space.
const NEIGHBOR_SKIP = /[ \t\n\r\u3000\u00A0]/;

function neighbor(text, i, dir) {
  let j = i + dir;
  while (j >= 0 && j < text.length) {
    const c = text[j];
    if (!NEIGHBOR_SKIP.test(c)) return c;
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
// - Pre-existing curly quotes in the input are pushed/popped on the same stack
//   so ASCII quotes mixed with already-curly quotes still pair correctly.
// - Apostrophes inside a word (`it's`, `don't`) are detected first and never
//   affect the stack.

function convertQuotes(text) {
  const chars = Array.from(text);
  const out = [];
  const stack = [];

  for (let i = 0; i < chars.length; i++) {
    const ch = chars[i];

    // Pre-existing curly opening quotes: pass through, register on stack so
    // subsequent ASCII quotes pair correctly.
    if (ch === '\u201C' || ch === '\u2018') {
      out.push(ch);
      stack.push(ch === '\u201C' ? '"' : "'");
      continue;
    }

    // Pre-existing curly closing double quote: pass through, pop matching.
    if (ch === '\u201D') {
      out.push(ch);
      popMatching(stack, '"');
      continue;
    }

    // Pre-existing curly right single quote: ambiguous (real closing quote OR
    // in-word apostrophe). Mid-word → treat as apostrophe (do not touch stack).
    if (ch === '\u2019') {
      const prev = neighborArr(chars, i, -1);
      const next = neighborArr(chars, i, +1);
      out.push(ch);
      if (!(isWordChar(prev) && isWordChar(next))) {
        popMatching(stack, "'");
      }
      continue;
    }

    // ASCII quotes
    if (ch === '"' || ch === "'") {
      const prev = neighborArr(chars, i, -1);
      const next = neighborArr(chars, i, +1);

      // Mid-word apostrophe: `it's`, `don't`, `O'Brien`.
      if (ch === "'" && isWordChar(prev) && isWordChar(next)) {
        out.push('\u2019');
        continue;
      }

      const isOpening = shouldOpen(prev, stack, ch);
      if (isOpening) {
        out.push(ch === '"' ? '\u201C' : '\u2018');
        stack.push(ch);
      } else {
        out.push(ch === '"' ? '\u201D' : '\u2019');
        popMatching(stack, ch);
      }
      continue;
    }

    out.push(ch);
  }

  return out.join('');
}

function popMatching(stack, kind) {
  for (let s = stack.length - 1; s >= 0; s--) {
    if (stack[s] === kind) { stack.splice(s, 1); return; }
  }
}

function neighborArr(chars, i, dir) {
  let j = i + dir;
  while (j >= 0 && j < chars.length) {
    const c = chars[j];
    if (!NEIGHBOR_SKIP.test(c)) return c;
    j += dir;
  }
  return '';
}

function shouldOpen(prev, stack, kind) {
  if (prev === '') return true;
  const hasMatchingOpen = stack.includes(kind);
  // Whitespace before a quote is normally an opening context. BUT if there is
  // an unclosed matching quote on the stack, prefer closing it — this fixes
  // pairs where the closer sits right after a soft newline or fullwidth space,
  // e.g. `"你好\n世界"` where the second quote's prev is `\n`.
  if (/[\s\n\r\u3000\u00A0]/.test(prev)) {
    return !hasMatchingOpen;
  }
  if (isOpeningPunct(prev)) return true;
  if (hasMatchingOpen) return false;
  return true;
}

// Punctuation that typically precedes an opening quote.
function isOpeningPunct(ch) {
  return /[（(\[\{「『《【〈—,，。：:；;？?！!、\u201C\u2018]/.test(ch);
}

// ASCII word chars only. CJK is intentionally excluded — `男's` is not a real
// construct; pairing for CJK + ASCII apostrophe goes through the stack path.
function isWordChar(ch) {
  return /[A-Za-z0-9_]/.test(ch);
}
