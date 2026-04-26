// More-articles card module for the "更多文章" section near the footer.
// Handles: state (localStorage), cover image loading (with CORS proxy fallback),
// Canvas compositing of cover + title overlay, upload to OSS, and merging the
// resulting card HTML into the existing footer.html in place of the default
// card group.
//
// Used by: public/js/engine.js (injects into footer before render + copy)
//          public/app.html (sidebar editor UI)

import { getActiveRuleConfig, getRuleNumber } from './rule-presets.js';

const LS_KEY = 'more_articles_v1';

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
    href: '',
    imgurl: '',
    title: '',
    cover_data_url: null,
    crop: { x: 0.5, y: 0.5, scale: 1 },
    composite_url: null,
    composite_hash: null,
  };
}

export function makeFreshUploadCards(n = getRuleNumber(getActiveRuleConfig(), 'more_articles_slots')) {
  return Array.from({ length: Math.max(0, Math.round(Number(n) || 0)) }, () => emptyCard());
}

function normalizeCard(c) {
  const base = emptyCard();
  if (!c || typeof c !== 'object') return base;
  return {
    ...base,
    ...c,
    crop: { ...base.crop, ...(c.crop || {}) },
  };
}

export function loadCards() {
  try {
    const raw = localStorage.getItem(LS_KEY);
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
    localStorage.setItem(LS_KEY, JSON.stringify(cards));
  } catch (err) {
    console.warn('[more-articles] saveCards failed', err);
  }
}

export function resetCards() {
  try { localStorage.removeItem(LS_KEY); } catch {}
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
  const resp = await fetch('/api/oss-upload', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ base64_images: { [key]: dataUrl } }),
  });
  if (!resp.ok) {
    const err = await resp.text().catch(() => '');
    throw new Error('oss-upload HTTP ' + resp.status + ' ' + err);
  }
  const json = await resp.json();
  const url = json && json.results && json.results[key];
  return url || dataUrl;
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
    if (c.composite_url && c.composite_hash === hash && !c.force_recompose) {
      c.final_url = c.composite_url;
      continue;
    }
    if (onProgress) onProgress(i, out.length);
    try {
      const dataUrl = await compositeCard(c);
      const uploaded = await uploadDataUrl(dataUrl, `more_article_${i}_${hash.slice(0, 10)}`);
      c.composite_url = uploaded;
      c.composite_hash = hash;
      c.final_url = uploaded;
      c.force_recompose = false;
    } catch (err) {
      console.warn('[more-articles] composite/upload failed for card', i, err);
      c.final_url = c.imgurl || '';
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

function cardHTML(card, index) {
  const rawImgurl = card.final_url || card.composite_url || card.imgurl || '';
  const href = escAttr(card.href || '');
  const marginTop = index === 0 ? 'margin-top: 0px;' : '';

  // Empty card (user added a slot but hasn't pasted a URL yet): render a
  // neutral-gray placeholder block matching the card proportions. Real cards
  // get their card-to-card gap from the inline <img>'s baseline leading
  // inside a line-height:1.6 span — the block-level placeholder would collapse
  // that gap, so we add an explicit margin-top on non-first slots to match.
  if (!rawImgurl) {
    const phGap = index === 0 ? '' : ' margin-top: 16px;';
    const PH_STYLE = 'display: block; width: 100%; aspect-ratio: 10 / 3; background: #e8e6e4; color: #a39e98; font-size: 15px; text-align: center; line-height: 1; border-radius: 4px;' + phGap;
    const INNER_STYLE = 'display: flex; align-items: center; justify-content: center; height: 100%; width: 100%;';
    return (
      `<section><p style="margin:0;padding:0;${marginTop}">` +
      `<span style="${PH_STYLE}"><span style="${INNER_STYLE}">待补充文章链接</span></span>` +
      `</p></section>`
    );
  }

  const imgurl = escAttr(rawImgurl);
  const SPAN_STYLE = 'color:rgba(0, 0, 0, 0.9);font-size:17px;font-family:mp-quote, &quot;PingFang SC&quot;, system-ui, -apple-system, BlinkMacSystemFont, &quot;Helvetica Neue&quot;, &quot;Hiragino Sans GB&quot;, &quot;Microsoft YaHei UI&quot;, &quot;Microsoft YaHei&quot;, Arial, sans-serif;line-height:1.6;letter-spacing:0.034em;font-style:normal;font-weight:normal;width:100%;';
  const IMG_STYLE = 'width: 661px !important; vertical-align: baseline; box-sizing: border-box; height: auto !important; max-width: 100% !important; visibility: visible !important;';
  return (
    `<section><p style="margin:0;padding:0;${marginTop}"><strong>` +
    `<a href="${href}" imgurl="${imgurl}" linktype="image" tab="innerlink" data-itemshowtype="0" target="_blank" data-linktype="1">` +
    `<span style="${SPAN_STYLE}" class="js_jump_icon h5_image_link">` +
    `<img alt="图片" class="rich_pages wxw-img" style="${IMG_STYLE}" src="${imgurl}">` +
    `</span></a></strong></p></section>`
  );
}

function buildCardsInnerHTML(cards) {
  return cards.map((c, i) => cardHTML(c, i)).join('');
}

// ---- Merge into footer.html (replaces the outer wrapper's inner content) ----
const OUTER_WRAPPER_OPEN = '<section style="text-align: center;margin-left: 8px;margin-right: 8px;">';

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

// Paragraph section that wraps BOTH the "/ 更多文章 /" title AND the outer
// cards wrapper. Found by scanning backwards from the cards wrapper.
const PARAGRAPH_WRAPPER_OPEN = '<section data-role="paragraph">';

function findMoreArticlesSectionBounds(footerHtml) {
  const outerStart = footerHtml.indexOf(OUTER_WRAPPER_OPEN);
  if (outerStart === -1) return null;
  const paraStart = footerHtml.lastIndexOf(PARAGRAPH_WRAPPER_OPEN, outerStart);
  if (paraStart === -1) return null;
  const paraEnd = findMatchingClose(footerHtml, paraStart);
  if (paraEnd === -1 || paraEnd < outerStart) return null;
  return { paraStart, paraEnd, outerStart };
}

export function mergeIntoFooter(footerHtml, cards) {
  if (!footerHtml) return footerHtml;
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
  const start = footerHtml.indexOf(OUTER_WRAPPER_OPEN);
  if (start === -1) return footerHtml;
  const end = findMatchingClose(footerHtml, start);
  if (end === -1) return footerHtml;
  const wrapperInnerStart = start + OUTER_WRAPPER_OPEN.length;
  const before = footerHtml.slice(0, wrapperInnerStart);
  const after = footerHtml.slice(end);
  return before + buildCardsInnerHTML(list) + after;
}
