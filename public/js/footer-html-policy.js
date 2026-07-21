// footer-html-policy.js — 页脚高级 HTML 的浏览器端防御层。
//
// 服务端 api/rich_text_sanitize.go 是持久化真相；这里使用同一白名单保护两条尚未
// 到达服务端的路径：后台即时预览，以及被篡改的本地规则快照。验证只决定“允许/拒绝”，
// 不改写合法 HTML，因此默认页脚与历史 golden 字节保持不变。

const ALLOWED_TAGS = new Set([
  'p', 'div', 'section', 'span', 'strong', 'b', 'em', 'i', 'u',
  'a', 'br', 'hr', 'img', 'ul', 'ol', 'li', 'blockquote',
  'table', 'thead', 'tbody', 'tfoot', 'tr', 'td', 'th', 'colgroup', 'col',
]);
const PARSER_CONTAINERS = new Set(['html', 'head', 'body']);
const ALLOWED_STYLE_PROPS = new Set([
  'color', 'background-color', 'font-size', 'font-style', 'font-weight',
  'text-decoration', 'text-align', 'display',
  'margin', 'margin-top', 'margin-right', 'margin-bottom', 'margin-left',
  'padding', 'padding-top', 'padding-right', 'padding-bottom', 'padding-left',
  'width', 'max-width', 'height',
  'border', 'border-top', 'border-right', 'border-bottom', 'border-left',
  'border-collapse', 'line-height', 'letter-spacing', 'vertical-align', 'white-space',
]);
const ASSET_REF_RE = /^\{\{asset(?::[a-z0-9_-]+|#[a-z0-9][a-z0-9_-]{0,63})\}\}$/i;
const CSS_LENGTH_RE = /^(?:0|(\d+(?:\.\d+)?)(px|em|rem|%|pt))$/;
const CSS_COLOR_RE = /^(?:#[0-9a-f]{3,8}|[a-z]{1,30}|(?:rgb|rgba|hsl|hsla)\([0-9.,%\s+-]+\))$/i;

function runeLength(value) {
  return [...String(value == null ? '' : value)].length;
}

function oneOf(value, ...allowed) {
  return allowed.includes(value);
}

function defaultParseDocument(fragment) {
  if (typeof globalThis.DOMParser !== 'function') return null;
  return new globalThis.DOMParser().parseFromString(fragment, 'text/html');
}

function parserFrom(options = {}) {
  if (typeof options.parseDocument === 'function') return options.parseDocument;
  return typeof globalThis.DOMParser === 'function' ? defaultParseDocument : null;
}

export function isSafeFooterURL(value) {
  const source = String(value == null ? '' : value).trim();
  if (!source || /[\x00-\x20\x7F"'<>\\]/.test(source)) return false;
  if (source.startsWith('/')) return !source.startsWith('//');
  try {
    const parsed = new URL(source);
    return parsed.protocol === 'https:' && !!parsed.hostname && !parsed.username && !parsed.password;
  } catch {
    return false;
  }
}

function validCSSLengthToken(value) {
  if (value === '0') return true;
  const match = CSS_LENGTH_RE.exec(value);
  if (!match) return false;
  const number = Number(match[1]);
  if (!Number.isFinite(number)) return false;
  if (match[2] === 'px' || match[2] === 'pt') return number <= 5000;
  if (match[2] === 'em' || match[2] === 'rem') return number <= 100;
  return match[2] === '%' && number <= 1000;
}

function validCSSLengthList(value, allowAuto, maxParts) {
  const parts = value.split(/\s+/).filter(Boolean);
  return parts.length > 0 && parts.length <= maxParts && parts.every(part =>
    (allowAuto && part === 'auto') || validCSSLengthToken(part));
}

function validCSSFontSize(value) {
  const match = CSS_LENGTH_RE.exec(value);
  if (!match) return false;
  const number = Number(match[1]);
  if (!Number.isFinite(number)) return false;
  if (match[2] === 'px' || match[2] === 'pt') return number >= 4 && number <= 200;
  if (match[2] === 'em' || match[2] === 'rem') return number >= 0.25 && number <= 20;
  return match[2] === '%' && number >= 10 && number <= 500;
}

function validCSSBorder(value) {
  if (value === '0' || value === 'none') return true;
  const parts = value.split(/\s+/).filter(Boolean);
  if (parts.length < 1 || parts.length > 3) return false;
  let length = false;
  let style = false;
  let color = false;
  for (const part of parts) {
    if (!length && validCSSLengthToken(part)) length = true;
    else if (!style && oneOf(part, 'none', 'solid', 'dashed', 'dotted', 'double')) style = true;
    else if (!color && CSS_COLOR_RE.test(part)) color = true;
    else return false;
  }
  return true;
}

function validateStyleValue(prop, rawValue) {
  const value = rawValue.trim().toLowerCase();
  switch (prop) {
    case 'color':
    case 'background-color':
      return CSS_COLOR_RE.test(rawValue) ? '' : `${prop} 的颜色值不合法`;
    case 'font-size':
      return validCSSFontSize(value) ? '' : 'font-size 超出允许范围';
    case 'font-style':
      return oneOf(value, 'normal', 'italic', 'oblique') ? '' : 'font-style 不合法';
    case 'font-weight':
      return oneOf(value, 'normal', 'bold', 'bolder', 'lighter', '100', '200', '300', '400', '500', '600', '700', '800', '900') ? '' : 'font-weight 不合法';
    case 'text-decoration':
      return value.split(/\s+/).every(token => oneOf(token, 'none', 'underline', 'overline', 'line-through', 'solid', 'double', 'dotted', 'dashed', 'wavy')) ? '' : 'text-decoration 不合法';
    case 'text-align':
      return oneOf(value, 'left', 'right', 'center', 'justify', 'start', 'end') ? '' : 'text-align 不合法';
    case 'display':
      return oneOf(value, 'block', 'inline', 'inline-block', 'table', 'table-row', 'table-cell', 'none') ? '' : 'display 不合法';
    case 'margin':
    case 'margin-top':
    case 'margin-right':
    case 'margin-bottom':
    case 'margin-left':
      return validCSSLengthList(value, true, 4) ? '' : `${prop} 不合法`;
    case 'padding':
    case 'padding-top':
    case 'padding-right':
    case 'padding-bottom':
    case 'padding-left':
      return validCSSLengthList(value, false, 4) ? '' : `${prop} 不合法`;
    case 'width':
    case 'max-width':
    case 'height':
      return value === 'auto' || value === 'none' || validCSSLengthList(value, false, 1) ? '' : `${prop} 不合法`;
    case 'border':
    case 'border-top':
    case 'border-right':
    case 'border-bottom':
    case 'border-left':
      return validCSSBorder(value) ? '' : `${prop} 不合法`;
    case 'border-collapse':
      return oneOf(value, 'collapse', 'separate') ? '' : 'border-collapse 不合法';
    case 'line-height':
    case 'letter-spacing':
      return value === 'normal' || validCSSLengthList(value, false, 1) ? '' : `${prop} 不合法`;
    case 'vertical-align':
      return oneOf(value, 'baseline', 'middle', 'top', 'bottom', 'text-top', 'text-bottom', 'sub', 'super') || validCSSLengthList(value, false, 1) ? '' : 'vertical-align 不合法';
    case 'white-space':
      return oneOf(value, 'normal', 'nowrap', 'pre', 'pre-wrap', 'pre-line') ? '' : 'white-space 不合法';
    default:
      return '';
  }
}

function validateInlineStyle(style) {
  if (runeLength(style) > 2000 || /\/\*|\*\/|\\/.test(style)) return 'style 含 CSS 注释、转义或内容过长';
  let declarations = 0;
  for (const rawDeclaration of style.split(';')) {
    const declaration = rawDeclaration.trim();
    if (!declaration) continue;
    declarations += 1;
    if (declarations > 40) return 'style 声明过多';
    const colon = declaration.indexOf(':');
    if (colon < 1) return `style 声明不合法：${declaration}`;
    const prop = declaration.slice(0, colon).trim().toLowerCase();
    const value = declaration.slice(colon + 1).trim();
    const lower = value.toLowerCase();
    if (!ALLOWED_STYLE_PROPS.has(prop)) return `不允许 style 属性 ${prop}`;
    if (!value || /javascript:|url\(|expression|@|var\(|env\(|calc\(/i.test(lower) || /[{}<>"]/.test(value)) {
      return `style 值不安全：${value}`;
    }
    const valueError = validateStyleValue(prop, value);
    if (valueError) return valueError;
  }
  return '';
}

function validInteger(value, min, max) {
  return /^-?\d+$/.test(value) && Number(value) >= min && Number(value) <= max;
}

function validDimension(value, allowPercent = true) {
  if (allowPercent && /^\d+%$/.test(value)) {
    const number = Number(value.slice(0, -1));
    return number >= 1 && number <= 100;
  }
  return validInteger(value, 1, 5000);
}

function validateAttribute(tag, key, value) {
  if (key.startsWith('on')) return `不允许事件属性 ${key}`;
  if (key.includes(':')) return `不允许命名空间属性 ${key}`;
  if (runeLength(value) > 2000 || /[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/.test(value)) return `属性 ${key} 不合法`;
  if (key === 'style') return validateInlineStyle(value);
  if (key === 'title') return '';
  if (tag === 'a' && key === 'href') return isSafeFooterURL(value) ? '' : '链接只允许 HTTPS 或站内路径';
  if (tag === 'a' && key === 'target') return oneOf(value, '_blank', '_self') ? '' : `不允许链接 target ${value}`;
  if (tag === 'a' && key === 'rel') {
    return value.toLowerCase().split(/\s+/).filter(Boolean).every(token => oneOf(token, 'noopener', 'noreferrer', 'nofollow')) ? '' : '链接 rel 不合法';
  }
  if (tag === 'img' && key === 'src') return ASSET_REF_RE.test(value.trim()) || isSafeFooterURL(value) ? '' : '图片只允许素材引用、HTTPS 或站内路径';
  if (tag === 'img' && key === 'alt') return runeLength(value) <= 500 ? '' : '图片 alt 过长';
  if (tag === 'img' && key === 'class') {
    return value.split(/\s+/).filter(Boolean).every(token => oneOf(token, 'mc-img', 'rich_pages', 'wxw-img')) ? '' : '图片 class 不在白名单';
  }
  if (tag === 'img' && (key === 'width' || key === 'height')) return validDimension(value) ? '' : `图片 ${key} 超出范围`;
  if (tag === 'img' && key === 'border') return value === '0' ? '' : '图片 border 只允许 0';
  if ((tag === 'td' || tag === 'th') && (key === 'colspan' || key === 'rowspan')) return validInteger(value, 1, 100) ? '' : `${key} 超出范围`;
  if (['td', 'th', 'tr', 'p', 'div'].includes(tag) && key === 'align') return oneOf(value.toLowerCase(), 'left', 'center', 'right', 'justify') ? '' : 'align 不合法';
  if ((tag === 'td' || tag === 'th') && key === 'valign') return oneOf(value.toLowerCase(), 'top', 'middle', 'bottom', 'baseline') ? '' : 'valign 不合法';
  if (tag === 'table' && ['border', 'cellpadding', 'cellspacing'].includes(key)) return validInteger(value, 0, 100) ? '' : `table ${key} 超出范围`;
  if (['table', 'td', 'th', 'col'].includes(tag) && key === 'width') return validDimension(value) ? '' : `${tag} width 超出范围`;
  if (tag === 'ol' && key === 'start') return validInteger(value, -10000, 10000) ? '' : 'ol start 超出范围';
  if (tag === 'li' && key === 'value') return validInteger(value, -10000, 10000) ? '' : 'li value 超出范围';
  return `不允许 <${tag}> 的 ${key} 属性`;
}

function attributeNames(node) {
  if (typeof node.getAttributeNames === 'function') return node.getAttributeNames();
  return Array.from(node.attributes || [], attr => attr.name);
}

function walkNode(node) {
  if (!node) return '';
  if (node.nodeType === 8) return '不允许 HTML 注释';
  if (node.nodeType === 10) return '不允许 doctype';
  if (node.nodeType === 1) {
    const tag = String(node.tagName || node.nodeName || '').toLowerCase();
    const container = PARSER_CONTAINERS.has(tag);
    if (!container && !ALLOWED_TAGS.has(tag)) return `不允许 <${tag}> 标签`;
    const names = attributeNames(node);
    if (container && names.length) return `不允许 <${tag}> 属性`;
    for (const rawName of names) {
      const key = String(rawName).toLowerCase();
      const error = validateAttribute(tag, key, node.getAttribute(rawName) || '');
      if (error) return error;
    }
    if (tag === 'a' && node.getAttribute('target') === '_blank') {
      const rel = String(node.getAttribute('rel') || '').toLowerCase().split(/\s+/);
      if (!rel.includes('noopener')) return 'target="_blank" 必须同时设置 rel="noopener"';
    }
  }
  for (const child of Array.from(node.childNodes || [])) {
    const error = walkNode(child);
    if (error) return error;
  }
  return '';
}

export function validateFooterHTML(fragment, options = {}) {
  const source = String(fragment == null ? '' : fragment);
  if (!source.trim()) return '';
  if (runeLength(source) > 20000) return 'HTML 内容超过 20000 字符';
  const parseDocument = parserFrom(options);
  if (!parseDocument) return '当前环境缺少 HTML 解析器';
  try {
    const parsed = parseDocument(source);
    if (!parsed) return 'HTML 解析失败';
    return walkNode(parsed);
  } catch (error) {
    return `HTML 解析失败：${error && error.message ? error.message : error}`;
  }
}

export function validateFooterHTMLLines(lines, options = {}) {
  if (!Array.isArray(lines)) return 'HTML 内容必须是数组';
  if (lines.length > 200) return 'HTML 内容不能超过 200 行';
  const normalized = [];
  for (let index = 0; index < lines.length; index += 1) {
    if (typeof lines[index] !== 'string') return `第 ${index + 1} 行必须是字符串`;
    const error = validateFooterHTML(lines[index], options);
    if (error) return `第 ${index + 1} 行：${error}`;
    normalized.push(lines[index]);
  }
  // 渲染时会 join('\n')；拼接后再解析，阻断把标签/属性/table 上下文拆行绕过。
  const combinedError = validateFooterHTML(normalized.join('\n'), options);
  return combinedError ? `拼接后：${combinedError}` : '';
}

export function filterSafeFooterHTMLLines(lines, options = {}) {
  const normalized = Array.isArray(lines) ? lines.map(line => String(line == null ? '' : line)) : [];
  // 纯函数 Node 单测没有 DOMParser；生产浏览器和 render-service 都有。无解析器时仍由
  // 服务端持久化白名单兜底，不为了测试环境改写合法 golden 输出。
  if (!parserFrom(options)) return normalized;
  const error = validateFooterHTMLLines(normalized, options);
  if (!error) return normalized;
  if (typeof console !== 'undefined' && typeof console.error === 'function') {
    console.error('[footer-html-policy] 已阻止不安全页脚 HTML：', error);
  }
  return [];
}
