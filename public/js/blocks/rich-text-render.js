// rich-text-render.js — 中性富文本 html → 渠道 inline-style 的「编排层」（P2 渲染期转换）。
//
// 中性：不 import 任何渠道渲染器（红线#19）。渠道把「样式口径」用 ch 传进来（mp-blocks / site-blocks 各自提供
// spanStyle/pWrap/linkStyle），本模块只负责解析中性 html、遍历 inline 树、合并嵌套样式、调 ch 拼 HTML。
//
// parseDoc 注入：生产 = 浏览器 DOMParser（new DOMParser().parseFromString）；测试 = linkedom parseHTML。
// 两者都返回一个有 querySelectorAll / childNodes 的 document。

function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

const STYLE_KEYS = ['color', 'background-color', 'font-size', 'font-style', 'font-weight', 'text-decoration', 'text-align'];

// collectStyle 从一个 inline 元素累积样式键值：标签语义（strong/em/u）+ style 白名单属性。
function collectStyle(el) {
  const out = {};
  const tag = (el.tagName || '').toLowerCase();
  if (tag === 'strong' || tag === 'b') out['font-weight'] = 'bold';
  if (tag === 'em' || tag === 'i') out['font-style'] = 'italic';
  if (tag === 'u') out['text-decoration'] = 'underline';
  const style = el.getAttribute && el.getAttribute('style');
  if (style) {
    for (const decl of style.split(';')) {
      const i = decl.indexOf(':');
      if (i < 0) continue;
      const k = decl.slice(0, i).trim().toLowerCase();
      const v = decl.slice(i + 1).trim();
      if (STYLE_KEYS.includes(k) && v) out[k] = v;
    }
  }
  return out;
}

// renderInlineNode 递归把 inline 节点转渠道 HTML（合并嵌套样式：子继承父）。
function renderInlineNode(node, ch, inherited) {
  if (node.nodeType === 3) return esc(node.textContent); // 文本节点
  if (node.nodeType !== 1) return '';
  const tag = (node.tagName || '').toLowerCase();
  if (tag === 'br') return '<br>';
  const merged = Object.assign({}, inherited, collectStyle(node));
  let inner = '';
  node.childNodes.forEach((c) => { inner += renderInlineNode(c, ch, merged); });
  if (tag === 'a') {
    let href = (node.getAttribute && node.getAttribute('href')) || '';
    // 协议白名单：防 javascript:/data: 经 localStorage 快照路径绕过后端 sanitize（codex P2 review）。
    if (!/^(https?:|mailto:)/i.test(String(href).trim())) href = '#';
    return ch.link(href, merged, inner);
  }
  return ch.span(merged, inner);
}

// richTextToInline 把中性富文本 html 转成渠道 inline-style HTML。
//   parseDoc(html) → document（querySelectorAll/childNodes）
//   ch: { span(styleObj, inner), link(href, styleObj, inner), pWrap(align, inner) }
export function richTextToInline(html, parseDoc, ch) {
  if (!html || !String(html).trim()) return '';
  // parseDoc 返回「内容容器」：生产 = DOMParser 的 body（浏览器自动补 html/body）；测试 = linkedom
  // document（fragment 内容直接在 document 顶层、body 为空）。两者 childNodes 都是内容节点。
  // 遍历 childNodes 而非 querySelectorAll，避免顶层 inline/text 被静默跳过（codex P2 review #1）。
  const root = parseDoc(String(html));
  if (!root || !root.childNodes) return '';
  const out = [];
  let inlineBuf = [];
  const flushInline = () => {
    if (!inlineBuf.length) return;
    const inner = inlineBuf.join('');
    inlineBuf = [];
    if (inner.trim() || /<(span|a|strong|em|u|br)/i.test(inner)) out.push(ch.pWrap('left', inner));
  };
  Array.from(root.childNodes).forEach((node) => {
    if (node.nodeType === 3) { // 顶层文本（非空白才成段）
      const t = node.textContent;
      if (t && t.trim()) inlineBuf.push(esc(t));
      return;
    }
    if (node.nodeType !== 1) return;
    const tag = (node.tagName || '').toLowerCase();
    if (tag === 'p') {
      flushInline();
      const styleAttr = (node.getAttribute && node.getAttribute('style')) || '';
      const m = styleAttr.match(/text-align:\s*(left|center|right)/);
      const align = m ? m[1] : 'left';
      const blockStyle = collectStyle(node); // p 自身 color/font-size 等（codex P2 review #2）
      delete blockStyle['text-align'];       // 块级对齐由 pWrap 处理，不进 span
      let inner = '';
      node.childNodes.forEach((c) => { inner += renderInlineNode(c, ch, {}); });
      // p 自身样式包裹整段（让纯文本也带上 color/font-size；内层 span 的同名属性自然覆盖）
      if (Object.keys(blockStyle).length) inner = ch.span(blockStyle, inner);
      out.push(ch.pWrap(align, inner));
      return;
    }
    if (tag === 'ul' || tag === 'ol') {
      flushInline();
      const ordered = tag === 'ol';
      let idx = 0;
      Array.from(node.childNodes).forEach((li) => {
        // 只遍历直接子 li，避免嵌套列表的后代 li 被扁平化/重复（codex review Block 4）。
        if (li.nodeType !== 1 || (li.tagName || '').toLowerCase() !== 'li') return;
        idx += 1;
        let inner = '';
        li.childNodes.forEach((c) => { inner += renderInlineNode(c, ch, {}); });
        out.push(ch.pWrap('left', (ordered ? `${idx}. ` : '• ') + inner)); // ol 递增序号、ul 圆点
      });
      return;
    }
    // 顶层 inline 元素（span/strong/a/br…）累积，最后包成一段（不丢富文本）
    inlineBuf.push(renderInlineNode(node, ch, {}));
  });
  flushInline();
  return out.join('\n');
}

// styleObjToStr 把样式对象序列化成中性 style（白名单顺序，hex 颜色直出）。
function styleObjToStr(s) {
  const order = ['color', 'background-color', 'font-size', 'font-style', 'font-weight', 'text-decoration'];
  const parts = [];
  for (const k of order) if (s[k]) parts.push(`${k}: ${s[k]}`);
  return parts.join('; ');
}

// NEUTRAL_CH 输出中性白名单 html（供 wangEditor 产出适配，下面 normalizeRichHtml 用）。
const NEUTRAL_CH = {
  span: (s, inner) => { const st = styleObjToStr(s); return st ? `<span style="${st};">${inner}</span>` : inner; },
  link: (href, s, inner) => { const st = styleObjToStr(s); return `<a href="${esc(href)}"${st ? ` style="${st};"` : ''}>${inner}</a>`; },
  pWrap: (align, inner) => `<p style="text-align: ${align};">${inner}</p>`,
};

// normalizeRichHtml 把任意富文本 html（如 wangEditor getHtml）适配成中性白名单 html：
// 经解析 → 只留白名单标签/属性/style → 重组。保存前调用，确保过后端 validateRichTextHTML。
export function normalizeRichHtml(html, parseDoc) {
  return richTextToInline(html, parseDoc, NEUTRAL_CH);
}
