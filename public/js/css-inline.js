// css-inline.js — Lightweight CSS inliner for WeChat article copy
// Resolves CSS variables and converts class attributes to inline styles.

let _themeCSS = null;

export async function loadThemeCSS() {
  if (_themeCSS) return _themeCSS;
  const resp = await fetch('/css/wx-theme.css?v=' + (window.__APP_BUILD_INFO?.build || Date.now()));
  _themeCSS = await resp.text();
  return _themeCSS;
}

const FALLBACK_FONTS = "system-ui, -apple-system, BlinkMacSystemFont, 'Helvetica Neue', 'Hiragino Sans GB', 'Microsoft YaHei UI', 'Microsoft YaHei', Arial, sans-serif";
const SAFE_FONT_FAMILY_RE = /^[A-Za-z0-9\u4e00-\u9fff -]{1,40}$/;

function safeFontFamily(value) {
  const family = String(value || '').trim();
  return SAFE_FONT_FAMILY_RE.test(family) ? family : 'PingFang SC';
}

function cssPx(value, fallback) {
  const n = Number(value);
  return (Number.isFinite(n) ? n : fallback) + 'px';
}

export function resolveVars(css, config) {
  const family = safeFontFamily(config.font_family);
  const fontStack = "mp-quote, '" + family + "', " + FALLBACK_FONTS;

  return css
    .replace(/var\(--wx-font-size\)/g, cssPx(config.font_size, 15))
    .replace(/var\(--wx-heading-size\)/g, cssPx(config.heading_size, 18))
    .replace(/var\(--wx-text-color\)/g, config.text_color || '#544545')
    .replace(/var\(--wx-link-color\)/g, config.link_color || '#0070C0')
    .replace(/var\(--wx-muted-color\)/g, config.muted_color || '#888888')
    .replace(/var\(--wx-line-height\)/g, config.line_height || '1.75')
    .replace(/var\(--wx-letter-spacing\)/g, (config.letter_spacing || '0.034') + 'em')
    .replace(/var\(--wx-paragraph-margin-x\)/g, cssPx(config.paragraph_margin_x, 8))
    .replace(/var\(--wx-paragraph-gap\)/g, cssPx(config.paragraph_gap, 20))
    .replace(/var\(--wx-caption-font-size\)/g, cssPx(config.caption_font_size, 12))
    .replace(/var\(--wx-attribution-font-size\)/g, cssPx(config.attribution_font_size, 15))
    .replace(/var\(--wx-blockquote-padding-left\)/g, cssPx(config.blockquote_padding_left, 12))
    .replace(/var\(--wx-blockquote-border-width\)/g, cssPx(config.blockquote_border_width, 3))
    .replace(/var\(--wx-font-stack\)/g, fontStack);
}

function parseRules(css) {
  const rules = {};
  const clean = css
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/:root\s*\{[^}]*\}/g, '');
  const re = /\.([\w-]+)\s*\{([^}]+)\}/g;
  let m;
  while ((m = re.exec(clean)) !== null) {
    let body = m[2].trim().replace(/\s*\n\s*/g, ' ').replace(/\s+/g, ' ');
    if (body && !body.endsWith(';')) body += ';';
    rules[m[1]] = body;
  }
  return rules;
}

function parseDeclarations(style) {
  const entries = [];
  for (const part of String(style || '').split(';')) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const idx = trimmed.indexOf(':');
    if (idx === -1) continue;
    const name = trimmed.slice(0, idx).trim().toLowerCase();
    const value = trimmed.slice(idx + 1).trim();
    if (!name) continue;
    entries.push([name, value]);
  }
  return entries;
}

function mergeStyles(styles) {
  const order = [];
  const values = new Map();
  for (const style of styles) {
    for (const [name, value] of parseDeclarations(style)) {
      if (!values.has(name)) order.push(name);
      values.set(name, value);
    }
  }
  return order.map(name => name + ': ' + values.get(name) + ';').join(' ');
}

export function inlineCSS(html, resolvedCSS) {
  const rules = parseRules(resolvedCSS);
  return html.replace(/<([A-Za-z][\w:-]*)([^<>]*?)>/g, (tag) => {
    const classRe = /\sclass=(["'])([^"']*)\1/i;
    const classMatch = tag.match(classRe);
    if (!classMatch) return tag;

    const classSet = new Set(classMatch[2].split(/\s+/).filter(Boolean));
    const classStyles = Object.entries(rules)
      .filter(([className]) => classSet.has(className))
      .map(([, style]) => style);
    let next = tag.replace(classRe, '');
    if (!classStyles.length) return next;

    const styleRe = /\sstyle=(["'])([\s\S]*?)\1/i;
    const styleMatch = next.match(styleRe);
    const merged = mergeStyles(styleMatch ? classStyles.concat(styleMatch[2]) : classStyles);
    if (styleMatch) return next.replace(styleRe, ' style="' + merged + '"');
    return next.replace(/\s*\/?>$/, (end) => ' style="' + merged + '"' + end);
  });
}

export function processForCopy(html, rawCSS, config) {
  const resolved = resolveVars(rawCSS, config);
  return inlineCSS(html, resolved);
}

export function applyThemeVars(el, config) {
  const family = safeFontFamily(config.font_family);
  const fontStack = "mp-quote, '" + family + "', " + FALLBACK_FONTS;
  el.style.setProperty('--wx-font-size', cssPx(config.font_size, 15));
  el.style.setProperty('--wx-heading-size', cssPx(config.heading_size, 18));
  el.style.setProperty('--wx-text-color', config.text_color || '#544545');
  el.style.setProperty('--wx-link-color', config.link_color || '#0070C0');
  el.style.setProperty('--wx-muted-color', config.muted_color || '#888888');
  el.style.setProperty('--wx-line-height', config.line_height || '1.75');
  el.style.setProperty('--wx-letter-spacing', (config.letter_spacing || '0.034') + 'em');
  el.style.setProperty('--wx-paragraph-margin-x', cssPx(config.paragraph_margin_x, 8));
  el.style.setProperty('--wx-paragraph-gap', cssPx(config.paragraph_gap, 20));
  el.style.setProperty('--wx-caption-font-size', cssPx(config.caption_font_size, 12));
  el.style.setProperty('--wx-attribution-font-size', cssPx(config.attribution_font_size, 15));
  el.style.setProperty('--wx-blockquote-padding-left', cssPx(config.blockquote_padding_left, 12));
  el.style.setProperty('--wx-blockquote-border-width', cssPx(config.blockquote_border_width, 3));
  el.style.setProperty('--wx-font-stack', fontStack);
}
