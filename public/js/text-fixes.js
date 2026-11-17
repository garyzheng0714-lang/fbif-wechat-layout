// 校对用的文字工具：排好的 HTML → 纯文字、在文字里找到一处错、把它改掉。纯字符串，不碰 DOM。
//
// 一处错 = { wrong, correct, evidence }：把 evidence 这句里的 wrong 换成 correct（见 api/publish_proofread.go）。
// 找的时候不看空白、引号、全半角（与服务端核对证据同一个口径 riskEvidenceNormalize），所以模型照抄时
// 丢了个空格、换了引号方向也找得到；改的时候只动出错的那几个字，标签原样留着。

const BLOCK_END = /^<\/(p|div|section|h[1-6]|li|tr|blockquote|figcaption)\b/i;
const LINE_BREAK = /^<br\b/i;
const NAMED_ENTITIES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ', ensp: ' ', emsp: ' ' };
const ENTITY = /^&(#x[0-9a-f]+|#[0-9]+|[a-z][a-z0-9]*);/i;

function decodeEntity(body) {
  if (body[0] !== '#') return Object.prototype.hasOwnProperty.call(NAMED_ENTITIES, body.toLowerCase()) ? NAMED_ENTITIES[body.toLowerCase()] : null;
  const code = /^#x/i.test(body) ? parseInt(body.slice(2), 16) : parseInt(body.slice(1), 10);
  if (!Number.isFinite(code) || code <= 0 || code > 0x10FFFF) return null;
  try { return String.fromCodePoint(code); } catch { return null; }
}

// 标签在哪结束：引号里的 > 不算。
function tagEnd(html, from) {
  let quote = '';
  for (let i = from + 1; i < html.length; i++) {
    const ch = html[i];
    if (quote) { if (ch === quote) quote = ''; continue; }
    if (ch === '"' || ch === "'") quote = ch;
    else if (ch === '>') return i + 1;
  }
  return html.length;
}

// 一段 HTML 的文字，和每个字在 HTML 里占的位置（from[i]、to[i]）。<br> 和段落结束算一个换行。
function readHtml(html) {
  const source = String(html || '');
  let text = '';
  const from = [];
  const to = [];
  const push = (chars, start, end) => {
    for (const unit of chars.split('')) { text += unit; from.push(start); to.push(end); }
  };
  let i = 0;
  while (i < source.length) {
    const ch = source[i];
    if (ch === '<' && /[a-zA-Z/!]/.test(source[i + 1] || '')) {
      const end = source.startsWith('<!--', i)
        ? (source.indexOf('-->', i + 4) < 0 ? source.length : source.indexOf('-->', i + 4) + 3)
        : tagEnd(source, i);
      const tag = source.slice(i, end);
      if (LINE_BREAK.test(tag) || BLOCK_END.test(tag)) push('\n', end, end);
      i = end;
      continue;
    }
    if (ch === '&') {
      const match = ENTITY.exec(source.slice(i, i + 12));
      const decoded = match && decodeEntity(match[1]);
      if (decoded) { push(decoded, i, i + match[0].length); i += match[0].length; continue; }
    }
    push(ch, i, i + 1);
    i++;
  }
  return { text, from, to };
}

function escapeHtml(text) {
  return String(text).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

const NOISE = new Set(['“', '”', '‘', '’', '"', "'", '*', '_', '`', '…']);

// 归一：去空白和引号这类噪声、全角字母数字折半角、小写。at[i] 是归一后第 i 个字在原文里的位置。
function normalize(text) {
  let norm = '';
  const at = [];
  const source = String(text || '');
  for (let i = 0; i < source.length; i++) {
    let ch = source[i];
    if (/\s/.test(ch) || NOISE.has(ch)) continue;
    const code = ch.charCodeAt(0);
    if (code >= 0xFF10 && code <= 0xFF19) ch = String.fromCharCode(code - 0xFF10 + 48);
    else if (code >= 0xFF21 && code <= 0xFF3A) ch = String.fromCharCode(code - 0xFF21 + 97);
    else if (code >= 0xFF41 && code <= 0xFF5A) ch = String.fromCharCode(code - 0xFF41 + 97);
    else if (ch === '．') ch = '.';
    else { const lower = ch.toLowerCase(); if (lower.length === 1) ch = lower; }
    norm += ch;
    at.push(i);
  }
  return { norm, at };
}

function allIndexes(haystack, needle) {
  const found = [];
  if (!needle) return found;
  for (let i = haystack.indexOf(needle); i >= 0; i = haystack.indexOf(needle, i + 1)) found.push(i);
  return found;
}

// 在 text 里找 needle，返回原文位置 { start, end }，找不到或说不准是哪一处回 null。
// 给了 evidence：只认 evidence 这句里的那一处；evidence 找不到（模型照抄走了样）时，全文只有一处才认。
// 没给 evidence：认第一处。跨了行的不认。
export function locate(text, needle, evidence = '') {
  const doc = normalize(text);
  const want = normalize(needle).norm;
  if (!want) return null;
  let hit = -1;
  const sentence = normalize(evidence).norm;
  if (sentence) {
    for (const start of allIndexes(doc.norm, sentence)) {
      const inside = doc.norm.slice(start, start + sentence.length).indexOf(want);
      if (inside >= 0) { hit = start + inside; break; }
    }
    if (hit < 0) {
      const everywhere = allIndexes(doc.norm, want);
      if (everywhere.length === 1) hit = everywhere[0];
    }
  } else {
    hit = doc.norm.indexOf(want);
  }
  if (hit < 0) return null;
  const start = doc.at[hit];
  const end = doc.at[hit + want.length - 1] + 1;
  return String(text).slice(start, end).includes('\n') ? null : { start, end };
}

// 校对送给模型的正文：每行 HTML 的文字，一段一行，空行去掉（同 api/publish_enrich.go 的 htmlToPlainText）。
export function articleText(lines) {
  return (lines || [])
    .flatMap(line => readHtml(line).text.split('\n'))
    .map(line => line.replace(/ /g, ' ').trim())
    .filter(Boolean)
    .join('\n');
}

const isHighSurrogate = ch => ch >= '\uD800' && ch <= '\uDBFF';
const isLowSurrogate = ch => ch >= '\uDC00' && ch <= '\uDFFF';

// 把几处错在一份文字里各自的位置找出来；位置按原文算，互相压着的后一处不要。
// 每处只留真正不一样的那一截（「转为健身」→「专为健身」只换「转」），没变的字原样留着，加粗、链接不跟着动。
function plan(text, fixes) {
  const taken = [];
  const spots = [];
  (fixes || []).forEach((fix, index) => {
    const spot = fix && locate(text, fix.wrong, fix.evidence);
    if (!spot || taken.some(other => spot.start < other.end && other.start < spot.end)) return;
    taken.push(spot);
    const raw = text.slice(spot.start, spot.end);
    const correct = String(fix.correct || '');
    let head = 0;
    while (head < raw.length && head < correct.length && raw[head] === correct[head]) head++;
    if (head && isHighSurrogate(raw[head - 1])) head--;
    let tail = 0;
    while (tail < raw.length - head && tail < correct.length - head && raw[raw.length - 1 - tail] === correct[correct.length - 1 - tail]) tail++;
    if (tail && isLowSurrogate(raw[raw.length - tail])) tail--;
    spots.push({ index, start: spot.start + head, end: spot.end - tail, text: correct.slice(head, correct.length - tail), afterKept: head > 0 });
  });
  return spots.sort((a, b) => b.start - a.start);
}

// 纯文字里改掉这几处。applied[i] 说第 i 处改上了没有。
export function fixPlainText(text, fixes) {
  let out = String(text || '');
  const applied = (fixes || []).map(() => false);
  for (const spot of plan(out, fixes)) {
    out = out.slice(0, spot.start) + spot.text + out.slice(spot.end);
    applied[spot.index] = true;
  }
  return { text: out, applied };
}

// 几行 HTML 里改掉这几处：改后的字放在出错的第一个字那里，其余出错的字删掉，夹在中间的标签留着；
// 只是补字（「圣在」→「圣迪乐在」）就接在前一个没变的字后面。
export function fixHtmlLines(lines, fixes) {
  const source = (lines || []).map(line => String(line == null ? '' : line));
  const applied = (fixes || []).map(() => false);
  if (!applied.length) return { lines: source, applied };
  const parts = source.map(readHtml);
  // 各行文字用换行接成一整篇，记下每行从哪开始。
  const starts = [];
  let whole = '';
  parts.forEach((part, i) => { starts.push(whole.length); whole += part.text + (i < parts.length - 1 ? '\n' : ''); });
  const out = source.slice();
  for (const spot of plan(whole, fixes)) {
    let row = starts.length - 1;
    while (row > 0 && starts[row] > spot.start) row--;
    const { from, to } = parts[row];
    const first = spot.start - starts[row];
    const last = spot.end - starts[row] - 1;
    const added = escapeHtml(spot.text);
    let html = out[row];
    if (last < first) {
      const at = spot.afterKept ? to[first - 1] : from[first];
      html = html.slice(0, at) + added + html.slice(at);
    } else {
      // 从后往前删，前面的位置不动；同一个实体占好几个字时只删一次。
      for (let i = last; i > first; i--) {
        if (from[i] === from[i - 1]) continue;
        html = html.slice(0, from[i]) + html.slice(to[i]);
      }
      html = html.slice(0, from[first]) + added + html.slice(to[first]);
    }
    out[row] = html;
    applied[spot.index] = true;
  }
  return { lines: out, applied };
}

export function sameFix(a, b) {
  return !!a && !!b && a.wrong === b.wrong && a.correct === b.correct && a.evidence === b.evidence;
}
