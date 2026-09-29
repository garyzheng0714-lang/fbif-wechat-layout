// Shared CJK punctuation. Policy describes behavior, never an account/channel.
// All edits use original UTF-16 positions, so contractions cannot move text into
// another bold/link run. No per-run fallback and no second conversion algorithm.
const {
  isCJK, hasHan, neighbor, isAsciiTight, protectedTextIndexes,
  leadingOrdinalPeriodIndexes, pairedFragments, englishFragmentProtectedIndexes,
} = await import('./punctuation-context.js' + new URL(import.meta.url).search);

const DEFAULTS = Object.freeze({
  locale: 'auto', context: 'paragraph', quotes: 'alternate',
  sequences: 'normalize', parentheses: 'fullwidth', trimParenSpacing: true,
});
const BASIC_MAP = { ',': '，', '.': '。', ';': '；', ':': '：', '!': '！', '?': '？', '(': '（', ')': '）' };

export function detectLanguage(text) {
  let cjk = 0, total = 0;
  for (const ch of text || '') {
    if (/\s/.test(ch)) continue;
    total++;
    if (isCJK(ch)) cjk++;
  }
  return total > 0 && cjk / total >= 0.20 ? 'zh' : 'en';
}

export function convertText(text, options = {}) {
  if (!text) return text;
  return convertMapped(text, options).text;
}

export function convertRuns(runs, options = {}) {
  if (!Array.isArray(runs) || !runs.length) return runs;
  let text = '';
  const owners = [], readonly = new Set();
  runs.forEach((run, owner) => {
    const textual = run && (run.type === 'txt' || run.type === 'link') && typeof run.text === 'string';
    // Images/unknown nodes block adjacency; br separates paragraphs. Link labels
    // participate in context but remain read-only, and href never enters text.
    const part = textual ? run.text : run?.type === 'br' ? '\n\n' : '\uFFFC';
    for (let i = 0; i < part.length; i++) {
      owners.push(textual ? owner : -1);
      if (run?.type !== 'txt') readonly.add(text.length + i);
    }
    text += part;
  });
  const converted = convertMapped(text, options, readonly);
  const parts = runs.map(() => '');
  for (let i = 0; i < converted.text.length; i++) {
    const owner = owners[converted.sources[i]];
    if (owner >= 0 && runs[owner].type === 'txt') parts[owner] += converted.text[i];
  }
  return runs.map((run, i) => run?.type === 'txt' && typeof run.text === 'string'
    ? { ...run, text: parts[i] } : run);
}

function convertMapped(text, options, readonly = new Set()) {
  const policy = { ...DEFAULTS, ...options };
  const identity = () => ({ text, sources: Array.from({ length: text.length }, (_, i) => i) });
  if (policy.locale === 'en' || (policy.context === 'paragraph' && policy.locale === 'auto' && detectLanguage(text) === 'en')) {
    return identity();
  }
  // Hard paragraph breaks bound every context rule, not just quote state. Keep
  // fenced code intact even when it contains blank lines, and retain source
  // offsets when assembling independently converted paragraphs.
  const protectedIndexes = protectedTextIndexes(text);
  let start = 0, out = '';
  const sources = [];
  const appendParagraph = end => {
    const localReadonly = new Set();
    for (let i = start; i < end; i++) if (readonly.has(i)) localReadonly.add(i - start);
    const part = convertParagraph(text.slice(start, end), policy, localReadonly);
    out += part.text;
    for (const source of part.sources) sources.push(start + source);
  };
  for (const match of text.matchAll(/\r?\n[ \t]*(?:\r?\n[ \t]*)+/g)) {
    if (protectedIndexes.has(match.index)) continue;
    appendParagraph(match.index);
    out += match[0];
    for (let i = 0; i < match[0].length; i++) sources.push(match.index + i);
    start = match.index + match[0].length;
  }
  appendParagraph(text.length);
  return { text: out, sources };
}

function convertParagraph(text, policy, readonly) {
  const protectedIndexes = protectedTextIndexes(text);
  const quoted = policy.quotes === 'preserve' ? text : convertQuotes(text, protectedIndexes, readonly);
  const pairs = pairedFragments(quoted, protectedIndexes);
  const english = englishFragmentProtectedIndexes(pairs, quoted);
  const ordinals = leadingOrdinalPeriodIndexes(text);
  const parentheses = new Map();
  if (policy.parentheses === 'paired') {
    for (const pair of pairs) {
      if (!pair.parens) continue;
      // Matched English parentheses retain their shape; Han content and an
      // existing fullwidth delimiter select a fullwidth pair, never half a pair.
      const locked = readonly.has(pair.start) ? quoted[pair.start] : readonly.has(pair.end) ? quoted[pair.end] : '';
      const wide = locked ? /[（）]/.test(locked)
        : hasHan(pair.content) || quoted[pair.start] === '（' || quoted[pair.end] === '）';
      parentheses.set(pair.start, wide ? '（' : '(');
      parentheses.set(pair.end, wide ? '）' : ')');
    }
  }
  // Pair repair is 1:1. Neighbor rules must see its final glyphs already, or
  // English,(Tea） would require a second pass to localize the comma.
  const contextChars = quoted.split('');
  for (const [i, glyph] of parentheses) if (!readonly.has(i)) contextChars[i] = glyph;
  const context = contextChars.join('');

  // A consecutive punctuation group shares its Chinese context. This reaches
  // the final form in one pass (?! -> ？！), including when parser + renderer
  // both run, without mutating the source while probing its neighbors.
  const adjacent = new Set();
  if (policy.context === 'adjacent') {
    for (const match of context.matchAll(/[.,;:!?() \t\n\r\u3000\u00a0]+/g)) {
      if (!isCJK(neighbor(context, match.index, -1)) &&
          !isCJK(neighbor(context, match.index + match[0].length - 1, 1))) continue;
      for (let i = match.index; i < match.index + match[0].length; i++) {
        if (BASIC_MAP[context[i]]) adjacent.add(i);
      }
    }
  }

  const sequences = new Map();
  for (const match of text.matchAll(/\.{3,}|(?<![\w-])-{2,}(?![\w-])/g)) {
    const end = match.index + match[0].length;
    let blocked = false;
    for (let i = match.index; i < end; i++) {
      if (protectedIndexes.has(i) || readonly.has(i)) blocked = true;
    }
    if (!blocked) sequences.set(match.index, { end, value: policy.sequences === 'preserve'
      ? match[0] : match[0][0] === '.' ? '……' : '——' });
  }

  let out = '';
  const sources = [];
  const append = (value, start, length = 1) => {
    out += value;
    // 1:1 edits keep every source offset. A contracted token belongs to its
    // first character; following words always retain their own style/owner.
    for (let i = 0; i < value.length; i++) sources.push(value.length === length ? start + i : start);
  };
  for (let i = 0; i < text.length; i++) {
    if (protectedIndexes.has(i) || readonly.has(i)) { append(text[i], i); continue; }
    const sequence = sequences.get(i);
    if (sequence) { append(sequence.value, i, sequence.end - i); i = sequence.end - 1; continue; }
    const ch = quoted[i];
    if (parentheses.has(i)) { append(parentheses.get(i), i); continue; }
    if (!BASIC_MAP[ch]) { append(ch, i); continue; }
    const prev = neighbor(context, i, -1), next = neighbor(context, i, 1);
    let value = ch;
    if (policy.context !== 'adjacent' || adjacent.has(i)) {
      const paren = ch === '(' || ch === ')';
      const ascii = isAsciiTight(prev) && isAsciiTight(next);
      const abbreviation = ch === '.' && /[A-Za-z]/.test(quoted[i - 1] || '') &&
        (i + 1 === quoted.length || /\s/.test(quoted[i + 1]));
      if (paren) {
        if (policy.parentheses === 'fullwidth') value = BASIC_MAP[ch];
      } else if (!english.has(i) && !ordinals.has(i) &&
          (policy.context === 'adjacent' || !ascii) && !abbreviation) value = BASIC_MAP[ch];
    }
    append(value, i);
  }
  return policy.trimParenSpacing ? tidyParenSpacing({ text: out, sources }, protectedIndexes, readonly) : { text: out, sources };
}

// Quote state is per paragraph, independent for single/double quotes. A read-only
// link quote still participates in state, but its original glyph is not changed.
function convertQuotes(text, protectedIndexes, readonly) {
  const chars = text.split('');
  let singleClose = false, doubleClose = false;
  const breaks = new Set(Array.from(text.matchAll(/\r?\n[ \t]*(?:\r?\n[ \t]*)+/g), m => m.index));
  for (let i = 0; i < chars.length; i++) {
    if (protectedIndexes.has(i)) continue;
    if (breaks.has(i)) { singleClose = false; doubleClose = false; }
    const ch = text[i];
    if (!/["'“”‘’]/.test(ch)) continue;
    const single = /['‘’]/.test(ch);
    const apostrophe = single && /^[A-Za-z0-9_]$/.test(neighbor(text, i, -1)) &&
      /^[A-Za-z0-9_]$/.test(neighbor(text, i, 1));
    let value;
    if (apostrophe) value = '’';
    else if (single) { value = singleClose ? '’' : '‘'; singleClose = !singleClose; }
    else { value = doubleClose ? '”' : '“'; doubleClose = !doubleClose; }
    if (!readonly.has(i)) chars[i] = value;
  }
  return chars.join('');
}

function tidyParenSpacing(mapped, protectedIndexes, readonly) {
  const remove = new Set();
  for (const match of mapped.text.matchAll(/[ \t]+/g)) {
    const mutableParen = i => /[（）]/.test(mapped.text[i] || '') &&
      !protectedIndexes.has(mapped.sources[i]) && !readonly.has(mapped.sources[i]);
    if (!mutableParen(match.index - 1) && !mutableParen(match.index + match[0].length)) continue;
    for (let i = match.index; i < match.index + match[0].length; i++) {
      const source = mapped.sources[i];
      if (!protectedIndexes.has(source) && !readonly.has(source)) remove.add(i);
    }
  }
  let text = '';
  const sources = [];
  for (let i = 0; i < mapped.text.length; i++) {
    if (remove.has(i)) continue;
    text += mapped.text[i];
    sources.push(mapped.sources[i]);
  }
  return { text, sources };
}
