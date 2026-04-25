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

function cssPx(value, fallback) {
  const n = Number(value);
  return (Number.isFinite(n) ? n : fallback) + 'px';
}

export function resolveVars(css, config) {
  const family = config.font_family || 'PingFang SC';
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

export function inlineCSS(html, resolvedCSS) {
  const rules = parseRules(resolvedCSS);
  return html.replace(/\bclass="([^"]+)"/g, (_, classes) => {
    const styles = classes.split(/\s+/).map(c => rules[c]).filter(Boolean).join(' ');
    return styles ? 'style="' + styles + '"' : '';
  });
}

export function processForCopy(html, rawCSS, config) {
  const resolved = resolveVars(rawCSS, config);
  return inlineCSS(html, resolved);
}

export function applyThemeVars(el, config) {
  const family = config.font_family || 'PingFang SC';
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
