// More-articles card module for the "更多文章" section near the footer.
// Handles: state (localStorage), cover image loading (with CORS proxy fallback),
// Canvas compositing of cover + title overlay, upload to OSS, and merging the
// resulting card HTML into the existing footer.html in place of the default
// card group.
//
// Used by: public/js/engine.js (injects into footer before render + copy)
//          public/app.html (sidebar editor UI)

import { getActiveRuleConfig, getRuleNumber } from './rule-presets.js';
import {
  emptyMoreArticleCard,
  moreArticleCardsForRule,
  normalizeMoreArticleDefaultCard,
} from './more-articles-config.js';
import { getRulePackByPath, DEFAULT_RULE_PACK_ID } from './rule-packs/registry.js';
import { fetchWithTimeout } from './fetch-timeout.js';
import { isMmbizUrl } from './uploader.js';
import { PLACEHOLDER_DATA_URL } from './more-articles-placeholder.js';

const LS_KEY = 'more_articles_v1';

// 卡片状态按账号隔离（修复跨账号覆盖 Bug：旧版用单一全局 key，FBIF↔WowFood
// 切换会互相覆盖对方的「更多文章」自定义卡片）。账号由当前 URL 路径派生。
function activeAccountId() {
  try {
    const path = (typeof globalThis !== 'undefined' && globalThis.location && globalThis.location.pathname) || '';
    const pack = getRulePackByPath(path);
    return (pack && pack.id) || DEFAULT_RULE_PACK_ID;
  } catch {
    return DEFAULT_RULE_PACK_ID;
  }
}
function lsKey() {
  return LS_KEY + '::' + activeAccountId();
}

// Composite canvas style values taken from the approved bottom-banner reference.
const BANNER_STYLE_SPEC = Object.freeze({
  width: 1000,
  height: 300,
  overlayAlpha: 120 / 255,
  title: Object.freeze({
    x: 61,
    y: 92,
    // Symmetric to title.x so the text block isn't visually pulled left when
    // it wraps to fill the row: right padding = width - x - title.width = 61.
    width: 878,
    wrapTolerance: 2,
    height: 116,
    fontSize: 48,
    lineHeight: 70,
    fontWeight: 650,
    fill: '#FFFFFF',
    maxLines: 2,
  }),
});

const COMPOSITE_OUTPUT_TYPE = 'image/png';
// Skipping the locally-loaded NotoSansHans / Noto Sans CJK SC: the @font-face
// for NotoSansHans is bound to the Bold (700) cut, so any weight request
// against that family snaps back to the Bold file. Falling through to the
// system Noto / PingFang faces lets weight 650 actually render lighter.
const COMPOSITE_FONT_FAMILIES = [
  'Noto Sans SC',
  'PingFang SC',
  'Microsoft YaHei',
  'Hiragino Sans GB',
  'sans-serif',
];
const COMPOSITE_FONT_STACK = COMPOSITE_FONT_FAMILIES.map(f => /\s/.test(f) ? `"${f}"` : f).join(', ');
const COMPOSITE_STYLE_VERSION = '2026-04-26-weight-650-center-fix';
const LINE_START_FORBIDDEN_PUNCTUATION = new Set(Array.from(
  '，。！？；：、,.!?;:)]}）】》〉」』”’"\''
));
const LINE_END_FORBIDDEN_PUNCTUATION = new Set(Array.from(
  '“‘「『'
));

export function getBannerStyleSpec(config = getActiveRuleConfig()) {
  const overlayAlpha = Math.max(0, Math.min(1, getRuleNumber(config, 'banner_overlay_alpha')));
  const maxLines = Math.max(1, Math.round(getRuleNumber(config, 'banner_title_max_lines')));
  return {
    width: BANNER_STYLE_SPEC.width,
    height: BANNER_STYLE_SPEC.height,
    overlayAlpha,
    title: {
      ...BANNER_STYLE_SPEC.title,
      x: getRuleNumber(config, 'banner_title_x'),
      y: getRuleNumber(config, 'banner_title_y'),
      width: getRuleNumber(config, 'banner_title_width'),
      height: getRuleNumber(config, 'banner_title_box_height'),
      fontSize: getRuleNumber(config, 'banner_title_font_size'),
      lineHeight: getRuleNumber(config, 'banner_title_line_height'),
      maxLines,
    },
  };
}

export function emptyCard() {
  return {
    ...emptyMoreArticleCard(),
    composite_url: null,
    composite_hash: null,
  };
}

function seedDefaultCard(card) {
  return {
    ...emptyCard(),
    ...normalizeMoreArticleDefaultCard(card),
    composite_url: null,
    composite_hash: null,
  };
}

export function makeFreshUploadCards(n = null) {
  const config = getActiveRuleConfig();
  const defaultCards = moreArticleCardsForRule(config);
  if (config.more_articles_enabled === false) return [];
  const count = n == null
    ? Math.max(0, Math.round(getRuleNumber(config, 'more_articles_slots')))
    : Math.max(0, Math.round(Number(n) || 0));
  return Array.from({ length: count }, (_, i) => {
    const seed = defaultCards[i];
    if (!seed) return emptyCard();
    return seedDefaultCard(seed);
  });
}

function normalizeCard(c) {
  const base = emptyCard();
  if (!c || typeof c !== 'object') return base;
  const imageUrls = Array.isArray(c.image_urls)
    ? Array.from(new Set(c.image_urls.map(v => String(v || '').trim()).filter(Boolean)))
    : [];
  return {
    ...base,
    ...c,
    image_urls: imageUrls,
    crop: { ...base.crop, ...(c.crop || {}) },
  };
}

export function loadCards() {
  try {
    const key = lsKey();
    let raw = localStorage.getItem(key);
    // 旧版单 key 一次性迁移：仅默认账号继承遗留数据（旧全局 key 历史上被各账号互相
    // 覆盖，只能归给默认账号，避免把脏状态扩散到其它账号）。迁移后【删除】遗留 key，
    // 否则它永久残留、且 resetCards 清空后会被再次复活（清空形同无效）。
    if (activeAccountId() === DEFAULT_RULE_PACK_ID) {
      const legacy = localStorage.getItem(LS_KEY);
      if (legacy != null) {
        if (!raw) { localStorage.setItem(key, legacy); raw = legacy; }
        localStorage.removeItem(LS_KEY);
      }
    }
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.map(normalizeCard);
  } catch {
    return [];
  }
}

export function saveCards(cards) {
  try {
    localStorage.setItem(lsKey(), JSON.stringify(cards));
  } catch (err) {
    console.warn('[more-articles] saveCards failed', err);
  }
}

export function resetCards() {
  try {
    localStorage.removeItem(lsKey());
    // 默认账号一并清掉旧版全局 key，否则下次 loadCards 会从它复活已被清空的数据。
    if (activeAccountId() === DEFAULT_RULE_PACK_ID) localStorage.removeItem(LS_KEY);
  } catch {}
}

// ---- Image loading with CORS-proxy fallback ----
// mmbiz.qpic.cn usually does NOT send CORS headers, so a direct <img
// crossOrigin=anonymous> would taint the canvas. Prefer the server-side
// image-proxy endpoint for http(s) URLs; data URLs load directly.
export function loadImage(src) {
  return new Promise((resolve, reject) => {
    if (!src) return reject(new Error('empty src'));
    const attempt = (url, useCrossOrigin) => new Promise((res, rej) => {
      const img = new Image();
      if (useCrossOrigin) img.crossOrigin = 'anonymous';
      img.onload = () => res(img);
      img.onerror = () => rej(new Error('image load failed: ' + url));
      img.src = url;
    });
    if (src.startsWith('data:')) {
      attempt(src, false).then(resolve, reject);
      return;
    }
    const proxied = '/api/image-proxy?url=' + encodeURIComponent(src);
    attempt(proxied, true).then(resolve, () => attempt(src, true).then(resolve, reject));
  });
}

// ---- Text wrapping (CJK-friendly, Canvas measureText based) ----
function wrapTextForCanvas(ctx, text, maxWidth) {
  const chars = Array.from(String(text || '').replace(/\r\n?/g, '\n'));
  const lines = [];
  let line = '';
  for (const ch of chars) {
    if (ch === '\n') { lines.push(line); line = ''; continue; }
    const next = line + ch;
    if (ctx.measureText(next).width > maxWidth && line) {
      lines.push(line);
      line = ch;
    } else {
      line = next;
    }
  }
  if (line) lines.push(line);
  return lines;
}

function balanceLinePunctuation(lines) {
  const out = lines.slice();
  for (let i = 1; i < out.length; i++) {
    let chars = Array.from(out[i] || '');
    while (chars.length && LINE_START_FORBIDDEN_PUNCTUATION.has(chars[0])) {
      out[i - 1] = (out[i - 1] || '') + chars.shift();
    }
    out[i] = chars.join('');
  }
  for (let i = 0; i < out.length - 1; i++) {
    let prev = Array.from(out[i] || '');
    let next = Array.from(out[i + 1] || '');
    while (prev.length && LINE_END_FORBIDDEN_PUNCTUATION.has(prev[prev.length - 1])) {
      next.unshift(prev.pop());
    }
    out[i] = prev.join('');
    out[i + 1] = next.join('');
  }
  return out.filter(line => line !== '');
}

export function wrapBannerTitleLines(ctx, text, maxWidth) {
  return balanceLinePunctuation(wrapTextForCanvas(ctx, text, maxWidth));
}

export function computeBannerTitleLayout(ctx, text, { scale = 1, fontFamily = COMPOSITE_FONT_STACK, styleSpec = getBannerStyleSpec() } = {}) {
  const title = styleSpec.title;
  const maxWidth = (title.width + title.wrapTolerance) * scale;
  const leadingRatio = title.lineHeight / title.fontSize;
  let fontSize = Math.max(12, title.fontSize * scale);
  const minFontSize = Math.max(10, 30 * scale);
  const step = Math.max(1, 2 * scale);
  let lines = [];

  while (fontSize >= minFontSize) {
    ctx.font = `${title.fontWeight} ${fontSize}px ${fontFamily}`;
    lines = wrapBannerTitleLines(ctx, text, maxWidth);
    if (lines.length <= title.maxLines) break;
    fontSize -= step;
  }

  if (lines.length > title.maxLines) {
    lines = lines.slice(0, title.maxLines);
    lines = balanceLinePunctuation(lines);
    const lastIndex = Math.max(0, lines.length - 1);
    let last = lines[lastIndex] || '';
    while (last.length > 1 && ctx.measureText(last + '…').width > maxWidth) {
      last = last.slice(0, -1);
    }
    lines[lastIndex] = last + '…';
  }

  ctx.font = `${title.fontWeight} ${fontSize}px ${fontFamily}`;
  const lineHeight = fontSize * leadingRatio;
  let y = title.y * scale;
  if (lines.length === 1) {
    // Center the glyph (em-box, fontSize tall) inside the title box —
    // NOT the line box (lineHeight tall). With textBaseline='top' the glyph
    // sits at the top of the line box, so centering the line box leaves the
    // bottom (lineHeight - fontSize) of leading as empty space below the
    // text and shifts the visible glyph upward.
    y += Math.max(0, (title.height * scale - fontSize) / 2);
  }

  return {
    lines,
    fontSize,
    lineHeight,
    x: title.x * scale,
    y,
    maxWidth,
    fontFamily,
    fontWeight: title.fontWeight,
    fill: title.fill,
  };
}

// ---- Composite: cover + dark overlay + title text ----
export async function compositeCard(card) {
  const src = card.cover_data_url || card.imgurl;
  if (!src) throw new Error('card has no cover');
  const img = await loadImage(src);

  const imgW = img.naturalWidth || img.width;
  const imgH = img.naturalHeight || img.height;

  // Fixed landscape output for visual consistency across cards.
  const styleSpec = getBannerStyleSpec();
  const W = styleSpec.width;
  const H = styleSpec.height;
  const canvas = document.createElement('canvas');
  canvas.width = W;
  canvas.height = H;
  const ctx = canvas.getContext('2d');

  const c = card.crop || { x: 0.5, y: 0.5, scale: 1 };
  // Cover-crop baseline: image is scaled to completely fill the 10:3 canvas.
  // For portrait sources the top/bottom get cropped; for very-wide sources the
  // sides get cropped. User's crop.x/crop.y picks the focal point.
  const baseScale = Math.max(W / imgW, H / imgH);
  const finalScale = baseScale * Math.max(0.5, Math.min(3, c.scale || 1));
  const drawW = imgW * finalScale;
  const drawH = imgH * finalScale;
  const fx = Math.max(0, Math.min(1, c.x == null ? 0.5 : c.x));
  const fy = Math.max(0, Math.min(1, c.y == null ? 0.5 : c.y));
  const dx = W / 2 - drawW * fx;
  const dy = H / 2 - drawH * fy;
  // Clamp so the image always covers the full canvas (no black edges)
  const clampedDx = Math.min(0, Math.max(W - drawW, dx));
  const clampedDy = Math.min(0, Math.max(H - drawH, dy));

  ctx.fillStyle = '#333';
  ctx.fillRect(0, 0, W, H);
  ctx.drawImage(img, clampedDx, clampedDy, drawW, drawH);

  // Flat dark overlay — PSD 矩形 1 is pure black filled at 50% opacity across
  // the full 1000×300 artboard.
  ctx.fillStyle = `rgba(0,0,0,${styleSpec.overlayAlpha})`;
  ctx.fillRect(0, 0, W, H);

  // Title: white, bold Chinese sans-serif, 48px with 70px leading.
  // Auto-shrink proportionally if the title exceeds 2 lines so overflow still
  // fits without breaking the PSD composition.
  const title = String(card.title || '').trim();
  if (title) {
    ctx.textBaseline = 'top';
    const layout = computeBannerTitleLayout(ctx, title, { styleSpec });
    ctx.fillStyle = layout.fill;
    ctx.font = `${layout.fontWeight} ${layout.fontSize}px ${layout.fontFamily}`;
    let y = layout.y;
    for (const line of layout.lines) {
      ctx.fillText(line, layout.x, y);
      y += layout.lineHeight;
    }
  }

  return canvas.toDataURL(COMPOSITE_OUTPUT_TYPE);
}

// ---- Fingerprint for skip-upload ----
async function sha1Hex(str) {
  const buf = new TextEncoder().encode(str);
  const digest = await crypto.subtle.digest('SHA-1', buf);
  return Array.from(new Uint8Array(digest))
    .map(b => b.toString(16).padStart(2, '0'))
    .join('');
}

export async function computeCardHash(card) {
  const styleSpec = getBannerStyleSpec();
  const input = JSON.stringify({
    title: card.title || '',
    imgurl: card.imgurl || '',
    cover_data_url_len: card.cover_data_url ? card.cover_data_url.length : 0,
    cover_data_url_head: card.cover_data_url ? card.cover_data_url.slice(0, 120) : '',
    crop: card.crop || null,
    style: {
      version: COMPOSITE_STYLE_VERSION,
      overlayAlpha: styleSpec.overlayAlpha,
      title: styleSpec.title,
    },
  });
  return sha1Hex(input);
}

// ---- Upload to OSS (reuses existing /api/oss-upload endpoint) ----
export async function uploadDataUrl(dataUrl, key) {
  const resp = await fetchWithTimeout('/api/oss-upload', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ base64_images: { [key]: dataUrl } }),
  }, { timeoutMs: 20000 });
  if (!resp.ok) {
    const err = await resp.text().catch(() => '');
    throw new Error('oss-upload HTTP ' + resp.status + ' ' + err);
  }
  const json = await resp.json();
  const url = json && json.results && json.results[key];
  if (!/^https?:\/\//i.test(String(url || '').trim())) {
    throw new Error('oss-upload did not return a remote image URL');
  }
  return url;
}

function isRemoteImageUrl(url) {
  return /^https?:\/\//i.test(String(url || '').trim());
}

export function copySafeMoreArticleCard(card) {
  const c = { ...(card || {}), crop: { ...((card && card.crop) || {}) } };
  const remoteComposite = isRemoteImageUrl(c.composite_url) ? c.composite_url : '';
  const remoteOriginal = isRemoteImageUrl(c.imgurl) ? c.imgurl : '';
  return {
    ...c,
    imgurl: remoteOriginal,
    composite_url: remoteComposite || null,
    final_url: remoteComposite || remoteOriginal || '',
  };
}

// ---- Ensure each card has a usable final image URL ----
// If the card has a title set OR a custom cover, we composite + upload.
// Otherwise we keep the original imgurl untouched.
export async function ensureCompositeReady(cards, { onProgress } = {}) {
  const out = cards.map(c => ({ ...c, crop: { ...(c.crop || {}) } }));
  for (let i = 0; i < out.length; i++) {
    const c = out[i];
    const hasTitle = !!(c.title && c.title.trim());
    const hasCustomCover = !!c.cover_data_url;
    const needsComposite = hasTitle || hasCustomCover;
    if (!needsComposite) {
      c.final_url = c.imgurl;
      continue;
    }
    const hash = await computeCardHash(c);
    if (/^https?:\/\//i.test(String(c.composite_url || '').trim()) && c.composite_hash === hash && !c.force_recompose) {
      c.final_url = c.composite_url;
      continue;
    }
    if (onProgress) onProgress(i, out.length);
    let compositeDataUrl = '';
    try {
      compositeDataUrl = await compositeCard(c);
      const uploaded = await uploadDataUrl(compositeDataUrl, `more_article_${i}_${hash.slice(0, 10)}`);
      c.composite_url = uploaded;
      c.composite_hash = hash;
      c.final_url = uploaded;
      c.force_recompose = false;
    } catch (err) {
      console.warn('[more-articles] composite/upload failed for card', i, err);
      c.composite_url = null;
      c.composite_hash = null;
      // 上传失败但合成成功：用 data: 合成图兜底。微信编辑器对粘贴的 base64
      // 位图会立即本地转存（与 DOCX 正文图同契约），比丢掉标题叠字更安全。
      c.final_url = compositeDataUrl || c.imgurl || '';
    }
  }
  return out;
}

// Same-origin preview data URL (no upload). Used by sidebar thumbnail and by
// the live preview so the user sees title overlays instantly.
//
// If the card has no title and no custom cover, we don't need a composite —
// just return a URL the browser can display. Remote WeChat CDN URLs are
// routed through /api/image-proxy so WeChat's anti-hotlinking placeholder
// doesn't show up; the proxy sets the right Referer header.
export async function compositePreviewDataUrl(card) {
  const hasTitle = !!(card.title && card.title.trim());
  const hasCustomCover = !!card.cover_data_url;
  if (!hasTitle && !hasCustomCover) {
    const raw = card.imgurl || '';
    if (!raw) return '';
    if (raw.startsWith('data:') || raw.startsWith('/')) return raw;
    return '/api/image-proxy?url=' + encodeURIComponent(raw);
  }
  try { return await compositeCard(card); }
  catch (err) { console.warn('[more-articles] preview composite failed', err); return card.imgurl || ''; }
}

// ---- HTML generation ----
function escAttr(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

// PNG placeholder for empty card slots (imported constant; see the module
// comment for why it must be a bitmap, not SVG). Rendered as the <img src> so
// the empty card has the same DOM shape as a real card — copy the HTML to
// WeChat, then swap each placeholder image via WeChat's "替换图片" UI.

const DEFAULT_CARD_INSET_PX = 8;
const CARD_SECTION_BASE_STYLE = 'margin:0;';
const CARD_P_STYLE = 'margin:0;padding:0;line-height:0;font-size:0;';
const SPAN_STYLE = 'display:block;line-height:0;font-size:0;width:100%;';
const IMG_STYLE = 'display:block;width: 661px !important; max-width: 100% !important; height: auto !important; margin: 0 auto; vertical-align: top; box-sizing: border-box; visibility: visible !important;';

function nonNegativePx(value, fallback) {
  const n = Number(value);
  if (!Number.isFinite(n) || n < 0) return fallback;
  return Math.round(n);
}

function cardSectionStyle(insetPx) {
  const inset = nonNegativePx(insetPx, DEFAULT_CARD_INSET_PX);
  const marginX = inset > 0 ? `margin-left:${inset}px;margin-right:${inset}px;` : '';
  return `${CARD_SECTION_BASE_STYLE}${marginX}padding:0;text-align:center;line-height:0;font-size:0;`;
}

// <a linktype="image"> 的 imgurl 是微信服务端校验属性：粘贴时编辑器只转存
// <img src>、从不改写 imgurl；保存草稿时 imgurl 非微信图床的图片链接卡片会被
// 整卡清除（2026-07-02「保存草稿后更多文章消失」根因）。因此 imgurl 恒用
// 原始微信图床封面，合成图只放 <img src>。
function wechatSafeCardImgurl(card, fallback) {
  const original = String((card && card.imgurl) || '').trim();
  if (isMmbizUrl(original)) return original;
  // 非微信封面（外站文章卡）：远程原图仍优于合成图外链/data:。
  if (/^https?:\/\//i.test(original)) return original;
  return fallback;
}

function cardHTML(card, index, insetPx) {
  const rawImgurl = card.final_url || card.composite_url || card.imgurl || '';
  const href = escAttr(card.href || '');
  const marginTop = index === 0 ? 'margin-top:0px;' : 'margin-top:12px;';
  const sectionStyle = cardSectionStyle(insetPx);

  // Empty card (no URL pasted yet): render the same block image shape as a real
  // card, with a neutral-gray SVG as the src. The <img> stays in place for
  // direct replacement in WeChat's editor.
  if (!rawImgurl) {
    return (
      `<section style="${sectionStyle}${marginTop}"><p style="${CARD_P_STYLE}">` +
      `<span style="${SPAN_STYLE}">` +
      `<img alt="图片" class="rich_pages wxw-img" style="${IMG_STYLE}" src="${PLACEHOLDER_DATA_URL}">` +
      `</span></p></section>`
    );
  }

  const imgurl = escAttr(rawImgurl);
  const cardImgurl = escAttr(wechatSafeCardImgurl(card, rawImgurl));
  return (
    `<section style="${sectionStyle}${marginTop}"><p style="${CARD_P_STYLE}"><strong>` +
    `<a href="${href}" imgurl="${cardImgurl}" linktype="image" tab="innerlink" data-itemshowtype="0" target="_blank" data-linktype="1">` +
    `<span style="${SPAN_STYLE}" class="js_jump_icon h5_image_link">` +
    `<img alt="图片" class="rich_pages wxw-img" style="${IMG_STYLE}" src="${imgurl}">` +
    `</span></a></strong></p></section>`
  );
}

function buildCardsInnerHTML(cards, insetPx) {
  return cards.map((c, i) => cardHTML(c, i, insetPx)).join('');
}

// ---- Merge into footer.html (replaces the outer wrapper's inner content) ----
const OUTER_WRAPPER_STYLE = 'text-align: center;';
const LEGACY_OUTER_WRAPPER_OPEN = '<section style="text-align: center;margin-left: 8px;margin-right: 8px;">';

function outerWrapperOpen(insetPx) {
  return `<section style="${OUTER_WRAPPER_STYLE}" data-card-inset="${nonNegativePx(insetPx, DEFAULT_CARD_INSET_PX)}">`;
}

function findMatchingClose(html, openIdx) {
  const OPEN = '<section';
  const CLOSE = '</section>';
  let depth = 0;
  let i = openIdx;
  const n = html.length;
  while (i < n) {
    const ni = html.indexOf(OPEN, i);
    const nc = html.indexOf(CLOSE, i);
    if (nc === -1) return -1;
    if (ni !== -1 && ni < nc) {
      depth++;
      i = ni + OPEN.length;
    } else {
      depth--;
      if (depth === 0) return nc;
      i = nc + CLOSE.length;
    }
  }
  return -1;
}

function stripDeprecatedPromo1000(footerHtml) {
  const marker = '1000个食品创新案例';
  let out = String(footerHtml || '');
  for (;;) {
    const markerIdx = out.indexOf(marker);
    if (markerIdx === -1) return out;
    const titleStart = out.lastIndexOf('<section', markerIdx);
    if (titleStart === -1) return out.replace(marker, '');
    const titleEnd = findMatchingClose(out, titleStart);
    if (titleEnd === -1) return out.replace(marker, '');

    const titleCloseEnd = titleEnd + '</section>'.length;
    let removeEnd = titleCloseEnd;
    const afterTitle = out.slice(titleCloseEnd);
    const ws = afterTitle.match(/^\s*/);
    const nextStart = titleCloseEnd + (ws ? ws[0].length : 0);
    if (out.startsWith('<section', nextStart)) {
      const nextEnd = findMatchingClose(out, nextStart);
      const nextHtml = nextEnd === -1 ? '' : out.slice(nextStart, nextEnd + '</section>'.length);
      if (/<img\b/i.test(nextHtml)) {
        removeEnd = nextEnd + '</section>'.length;
      }
    }
    out = out.slice(0, titleStart) + out.slice(removeEnd);
  }
}

// Paragraph section that wraps BOTH the "/ 更多文章 /" title AND the outer
// cards wrapper. Found by scanning backwards from the cards wrapper.
const PARAGRAPH_WRAPPER_OPEN = '<section data-role="paragraph">';

function findOuterWrapper(html) {
  const wrappers = [];
  const re = /<section\b[^>]*>/gi;
  let match;
  while ((match = re.exec(html)) !== null) {
    const open = match[0];
    const style = attrValue(open, 'style') || '';
    if (!isCardsWrapperStyle(style) && open !== LEGACY_OUTER_WRAPPER_OPEN) continue;
    if (!isInsideMoreArticlesParagraph(html, match.index)) continue;
    wrappers.push({
      open,
      start: match.index,
      insetPx: cardsWrapperInset(open, style),
    });
  }
  wrappers.sort((a, b) => a.start - b.start);
  return wrappers[0] || null;
}

function isInsideMoreArticlesParagraph(html, start) {
  const paraStart = html.lastIndexOf(PARAGRAPH_WRAPPER_OPEN, start);
  if (paraStart === -1) return false;
  const paraEnd = findMatchingClose(html, paraStart);
  return paraEnd !== -1 && paraEnd > start;
}

function attrValue(tag, name) {
  const re = new RegExp(`\\s${name}=(["'])([^"']*)\\1`, 'i');
  const match = tag.match(re);
  return match ? match[2] : '';
}

function isCardsWrapperStyle(style) {
  const normalized = String(style || '').trim().replace(/\s+/g, ' ');
  return normalized === OUTER_WRAPPER_STYLE
    || normalized === 'text-align:center;'
    || hasHorizontalInset(style);
}

function hasHorizontalInset(style) {
  return /margin-left\s*:\s*8px/i.test(style) && /margin-right\s*:\s*8px/i.test(style);
}

function cardsWrapperInset(open, style) {
  const explicit = attrValue(open, 'data-card-inset');
  if (explicit !== '') return nonNegativePx(explicit, DEFAULT_CARD_INSET_PX);
  if (hasHorizontalInset(style)) return 8;
  return DEFAULT_CARD_INSET_PX;
}

function findMoreArticlesSectionBounds(footerHtml) {
  const wrapper = findOuterWrapper(footerHtml);
  if (!wrapper) return null;
  const outerStart = wrapper.start;
  const paraStart = footerHtml.lastIndexOf(PARAGRAPH_WRAPPER_OPEN, outerStart);
  if (paraStart === -1) return null;
  const paraEnd = findMatchingClose(footerHtml, paraStart);
  if (paraEnd === -1 || paraEnd < outerStart) return null;
  return { paraStart, paraEnd, outerStart };
}

export function mergeIntoFooter(footerHtml, cards) {
  if (!footerHtml) return footerHtml;
  footerHtml = stripDeprecatedPromo1000(footerHtml);
  const list = Array.isArray(cards) ? cards : [];

  // Empty list → remove the entire "更多文章" section (title + cards wrapper).
  if (list.length === 0) {
    const bounds = findMoreArticlesSectionBounds(footerHtml);
    if (!bounds) return footerHtml;
    return footerHtml.slice(0, bounds.paraStart)
      + footerHtml.slice(bounds.paraEnd + '</section>'.length);
  }

  // Non-empty → replace the inner content of the outer cards wrapper with
  // the user's N cards.
  const wrapper = findOuterWrapper(footerHtml);
  if (!wrapper) return footerHtml;
  const start = wrapper.start;
  const end = findMatchingClose(footerHtml, start);
  if (end === -1) return footerHtml;
  const before = footerHtml.slice(0, start) + outerWrapperOpen(wrapper.insetPx);
  const after = footerHtml.slice(end);
  return before + buildCardsInnerHTML(list, wrapper.insetPx) + after;
}
