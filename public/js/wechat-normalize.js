// WeChat-rich HTML normalizer.
// The preview DOM must already be safe for direct select-all copy, because not
// every user clicks our copy button. Keep image blocks as block layout instead
// of inline text boxes so WeChat mobile preview does not materialize baseline
// leading as an empty paragraph under images.

function parseStyle(style) {
  const items = [];
  const seen = new Set();
  for (const part of String(style || '').split(';')) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const idx = trimmed.indexOf(':');
    if (idx === -1) continue;
    const name = trimmed.slice(0, idx).trim().toLowerCase();
    const value = trimmed.slice(idx + 1).trim();
    if (!name || seen.has(name)) continue;
    seen.add(name);
    items.push([name, value]);
  }
  return items;
}

function serializeStyle(items) {
  return items.map(([name, value]) => `${name}: ${value};`).join(' ');
}

function mergeStyle(style, declarations) {
  const next = parseStyle(style).filter(([name]) => !(name in declarations));
  for (const [name, value] of Object.entries(declarations)) {
    next.push([name, value]);
  }
  return serializeStyle(next);
}

function setTagStyle(tag, declarations) {
  const styleRe = /\sstyle=(["'])([\s\S]*?)\1/i;
  const m = tag.match(styleRe);
  if (m) {
    const style = mergeStyle(m[2], declarations);
    return tag.replace(styleRe, ` style="${style}"`);
  }
  return tag.replace(/\s*\/?>$/, (end) => ` style="${mergeStyle('', declarations)}"${end}`);
}

const EMPTY_BR_SECTION_RE = /<section\b[^>]*>\s*(?:<span\b[^>]*>\s*)?<br\s*\/?>\s*(?:<\/span>\s*)?<\/section>\s*/gi;

const IMAGE_STYLE = {
  display: 'block',
  margin: '0 auto',
  'max-width': '100% !important',
  height: 'auto !important',
  'vertical-align': 'top',
  'box-sizing': 'border-box',
};

const IMAGE_INLINE_WRAPPER_STYLE = {
  display: 'block',
  'line-height': '0',
  'font-size': '0',
  width: '100%',
};

const IMAGE_BLOCK_WRAPPER_STYLE = {
  'line-height': '0',
  'font-size': '0',
  'text-align': 'center',
};

export function normalizeWechatRichHtml(html) {
  if (typeof html !== 'string' || !html) return html;
  let out = html.replace(EMPTY_BR_SECTION_RE, '');

  out = out.replace(/<img\b[^>]*>/gi, (tag) => setTagStyle(tag, IMAGE_STYLE));

  out = out.replace(
    /<span\b(?=[^>]*\bclass=(["'])[^"']*(?:js_jump_icon|h5_image_link)[^"']*\1)[^>]*>/gi,
    (tag) => setTagStyle(tag, IMAGE_INLINE_WRAPPER_STYLE)
  );

  out = out.replace(
    /<section\b([^>]*)>\s*(<img\b[^>]*>)\s*<\/section>/gi,
    (_match, attrs, img) => `${setTagStyle(`<section${attrs}>`, IMAGE_BLOCK_WRAPPER_STYLE)}${img}</section>`
  );

  return out;
}

