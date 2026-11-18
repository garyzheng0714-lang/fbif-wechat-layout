// image-swipe.js — 公众号滑动图（几张图左右滑着看，红线 50）。两套公众号模板（fbif、foodtalks-series）共用，
// 不 import 任何渲染器；FoodTalks 网站只用 stripSwipeHint 去掉图注里的滑动提示。
//
// 2026-11-18 媒体提效群 Dudu：「排版工具有可能排版滑动图吗？（目前排版工具是将滑动图一张张摊开）」。
// 哪几张图是一组：
// - 公众号原文：抓原文时按原文的横滑外框记组号（块的 swipe，api/wechat_swipe.go）；
// - 飞书、Word、Markdown：连着两张以上的图，紧跟的图注里写了「左右滑动查看」这类话（byCaption）。
// 排出来的结构照 135 编辑器的滑动图写（外框横向可滑、长条 N 倍宽、每格 100/N），Dudu 把这个结构
// 粘进公众号后台预览过，滑得动。格子之间不能有空白字符，不然 inline-block 之间多出空隙、最后一格掉行。

// 提示语的认法与 Go 外发那份（api/distribution_rules.go swipeHintRe）一样，两边都用
// api/testdata/swipe-hints.json 考。单字的上、下要带「向」「往」才算（「销量下滑」不是提示）。
const ARROWS = '(?:[←→⇦⇨⬅➡👉👈◀▶«»＜＞<>]\\uFE0F?\\s*)*';
const HINT = '(?:请)?(?:' +
  '(?:向|往)?(?:左右|上下|[左右])滑动(?:屏幕|图片)?(?:查看|浏览|看)?' +
  '|(?:向|往)[上下]滑动(?:屏幕|图片)?(?:查看|浏览|看)?' +
  '|滑动(?:屏幕|图片)?(?:查看|浏览|看)' +
  '|(?:向|往)?(?:左右|[左右])滑(?:屏幕|图片)?(?:查看|浏览|看)' +
  ')(?:更多|全部|下一张|其他)?(?:图片|内容)?(?:查看)?';
const SWIPE_HINT_SOURCE = '[（(【\\[]\\s*' + ARROWS + HINT + ARROWS + '\\s*[）)】\\]]' +
  '|[，,；;]?\\s*' + ARROWS + HINT + '(?:\\s*' + ARROWS + ')?';
const VERTICAL_RE = /[上下]滑/u;

function hintMatches(text) {
  return String(text || '').match(new RegExp(SWIPE_HINT_SOURCE, 'gu')) || [];
}

// 去掉图注里的滑动提示，剩下的去掉两头的逗号、分号；整条都是提示的回空串。
export function stripSwipeHint(text) {
  return String(text || '')
    .replace(new RegExp(SWIPE_HINT_SOURCE, 'gu'), '')
    .trim()
    .replace(/^[；;，, ]+|[；;，, ]+$/gu, '')
    .trim();
}

// 这句话里有没有「左右滑」的提示（「上下滑动查看」的长图不算）。
export function isHorizontalSwipeHint(text) {
  return hintMatches(text).some(m => !VERTICAL_RE.test(m));
}

function elemPlainText(e) {
  if (!e) return '';
  if (typeof e.text === 'string' && e.text) return e.text;
  return (e.runs || []).map(r => (r && r.type !== 'img' && r.text) || '').join('');
}

// 一张能放进滑动图的普通正文图：不是作者名片、装饰小图、序号图这类。
function isPlainImage(e) {
  return !!e && e.k === 'img' && !e.maxPx && !e.className && !e.role;
}

// 图后面那几行说明里有没有「左右滑动查看」：图注（cap）都算，普通段只认整段就是这句提示的。
function captionSaysSwipe(elems, from) {
  for (let j = from; j < elems.length; j++) {
    const e = elems[j];
    const text = elemPlainText(e);
    if (e.k === 'cap') {
      if (isHorizontalSwipeHint(text)) return true;
      continue;
    }
    if (e.k === 'txt' || e.k === 'p') return isHorizontalSwipeHint(text) && !stripSwipeHint(text);
    return false;
  }
  return false;
}

// 把一组图合成一块 { k: 'swipe', items }，blankBefore 跟第一张。只有一张的照旧是图。
// byCaption：没记组号的稿（飞书、Word、Markdown）按图注里的滑动提示认。
export function groupImageSwipes(elems, { byCaption = false } = {}) {
  const list = Array.isArray(elems) ? elems : [];
  const out = [];
  let changed = false;
  for (let i = 0; i < list.length; i++) {
    const e = list[i];
    let end = i + 1;
    if (e && e.k === 'img' && e.swipe) {
      while (end < list.length && list[end] && list[end].k === 'img' && list[end].swipe === e.swipe) end++;
    } else if (byCaption && isPlainImage(e)) {
      while (end < list.length && isPlainImage(list[end]) && !list[end].swipe) end++;
      if (end - i < 2 || !captionSaysSwipe(list, end)) end = i + 1;
    }
    if (end - i < 2) { out.push(e); continue; }
    const items = list.slice(i, end);
    out.push({ k: 'swipe', items, blankBefore: !!items[0].blankBefore });
    changed = true;
    i = end - 1;
  }
  return changed ? out : list;
}

// 一组图排成滑动图。imageTag(item) 给出这一格的 <img …/>（各模板自己的图片写法）。
export function renderImageSwipe(items, imageTag) {
  const n = items.length;
  const slide = Math.floor(10000 * 100 / n) / 10000;
  const cells = items.map(item =>
    '<section style="display: inline-block; width: ' + slide + '%; vertical-align: top; box-sizing: border-box;">' +
    '<section style="text-align: center; margin: 0px; line-height: 0; box-sizing: border-box;">' +
    imageTag(item) +
    '</section></section>').join('');
  return '<section style="display: inline-block; width: 100%; vertical-align: top; overflow-x: auto; box-sizing: border-box;">' +
    '<section style="overflow: hidden; width: ' + (n * 100) + '%; max-width: ' + (n * 100) + '% !important; box-sizing: border-box;">' +
    cells +
    '</section></section>';
}

const EDGE_SEPARATORS = /^[\s；;，,]+|[\s；;，,]+$/gu;

function stripRuns(runs) {
  const re = new RegExp(SWIPE_HINT_SOURCE, 'gu');
  const out = (runs || []).map(r => (r && typeof r.text === 'string' && r.type !== 'img')
    ? { ...r, text: r.text.replace(re, '') }
    : r);
  const texts = out.map((r, idx) => ({ r, idx })).filter(({ r }) => r && typeof r.text === 'string' && r.text.trim());
  if (!texts.length) return [];
  const first = texts[0].idx;
  const last = texts[texts.length - 1].idx;
  out[first] = { ...out[first], text: out[first].text.replace(/^[\s；;，,]+/u, '') };
  out[last] = { ...out[last], text: out[last].text.replace(/[\s；;，,]+$/u, '') };
  return out.filter(r => !r || typeof r.text !== 'string' || r.text !== '' || r.type === 'br');
}

// FoodTalks 网站、外发平台上图一张张竖着排，「左右滑动查看」不对，去掉（红线 50）：图注里的提示去掉，
// 整条只有提示的图注、紧跟在图后面只有提示的一段整条不要。其余原样，没改动时回原数组。
export function withoutSwipeHints(elems) {
  const list = Array.isArray(elems) ? elems : [];
  const out = [];
  let changed = false;
  let carryBlank = false;
  for (const e of list) {
    const isCap = e && e.k === 'cap';
    const afterImage = out.length && ['img', 'video', 'cap'].includes(out[out.length - 1].k);
    const isHintParagraph = e && (e.k === 'p' || e.k === 'txt') && afterImage;
    const text = elemPlainText(e);
    if (!(isCap || isHintParagraph) || !hintMatches(text).length) {
      out.push(carryBlank && e ? { ...e, blankBefore: true } : e);
      carryBlank = false;
      continue;
    }
    if (isHintParagraph && stripSwipeHint(text)) { out.push(e); continue; }
    changed = true;
    const kept = typeof e.text === 'string' && e.text
      ? { ...e, text: stripSwipeHint(e.text) }
      : { ...e, runs: stripRuns(e.runs) };
    if (!elemPlainText(kept).replace(EDGE_SEPARATORS, '')) {
      carryBlank = carryBlank || !!e.blankBefore;
      continue;
    }
    out.push(carryBlank ? { ...kept, blankBefore: true } : kept);
    carryBlank = false;
  }
  return changed ? out : list;
}
