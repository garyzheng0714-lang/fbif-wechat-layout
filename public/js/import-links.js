// 导入框里的链接画成蓝色下划线（2026-10-28 用户：「这个输入框里，也要显示超链接的样式」）。
// textarea 画不了局部样式：框后面垫一层排版一模一样的字，链接那几个字蓝色加下划线；框里有链接时
// 框自己的字设成透明（光标、选区照旧），看到的就是垫着的那层。哪些字算链接用 linkRanges，与排版挂链接同一个认法。

import { linkRanges, readImportLinks } from './clipboard-links.js';

// 垫层与框逐像素对齐：影响折行和字位置的样式都照框的计算值抄。
const MIRRORED_STYLES = [
  'boxSizing', 'fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'fontVariant', 'fontKerning',
  'letterSpacing', 'wordSpacing', 'lineHeight', 'textAlign', 'textIndent', 'textTransform', 'tabSize',
  'whiteSpace', 'overflowWrap', 'wordBreak', 'lineBreak', 'direction',
  'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
  'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth',
];

// 框里的字 → 一段段字，链接那段带 href。
export function linkSegments(text, links) {
  const lines = String(text || '').split('\n');
  const ranges = linkRanges(lines, links);
  const segments = [];
  lines.forEach((line, i) => {
    let pos = 0;
    for (const { start, end, href } of ranges[i]) {
      if (start > pos) segments.push({ text: line.slice(pos, start) });
      segments.push({ text: line.slice(start, end), href });
      pos = end;
    }
    const rest = line.slice(pos) + (i < lines.length - 1 ? '\n' : '');
    if (rest) segments.push({ text: rest });
  });
  return segments;
}

export function mountImportLinks(input) {
  const doc = input && input.ownerDocument;
  const box = input && input.parentElement;
  if (!doc || !box) return;
  const mirror = doc.createElement('div');
  mirror.className = 'import-textarea-links';
  mirror.setAttribute('aria-hidden', 'true');
  input.before(mirror);

  function render() {
    const links = readImportLinks(input);
    const segments = links.length ? linkSegments(input.value, links) : [];
    const hasLinks = segments.some(segment => segment.href);
    box.classList.toggle('has-links', hasLinks);
    if (!hasLinks) {
      mirror.replaceChildren();
      return;
    }
    const style = doc.defaultView.getComputedStyle(input);
    for (const name of MIRRORED_STYLES) mirror.style[name] = style[name];
    // 框出了竖滚动条，字能用的宽度就窄一条；垫层没有滚动条，右边多留这么宽。
    const scrollbar = input.offsetWidth - input.clientWidth
      - parseFloat(style.borderLeftWidth) - parseFloat(style.borderRightWidth);
    mirror.style.paddingRight = `calc(${style.paddingRight} + ${Math.max(0, scrollbar)}px)`;
    mirror.style.top = `${input.offsetTop}px`;
    mirror.style.left = `${input.offsetLeft}px`;
    mirror.style.width = `${input.offsetWidth}px`;
    mirror.style.height = `${input.offsetHeight}px`;
    mirror.replaceChildren(...segments.map(segment => {
      if (!segment.href) return doc.createTextNode(segment.text);
      const link = doc.createElement('span');
      link.className = 'import-textarea-link';
      link.textContent = segment.text;
      return link;
    }), doc.createTextNode('​'));
    mirror.scrollTop = input.scrollTop;
  }

  // 别的模块也直接改框里的字（恢复草稿、飞书文档搜索），程序改值不发事件；接住每次改值，垫层跟着框走。
  const native = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input), 'value');
  if (native && native.get && native.set) {
    Object.defineProperty(input, 'value', {
      configurable: true,
      get() { return native.get.call(this); },
      set(value) { native.set.call(this, value); render(); },
    });
  }
  input.addEventListener('input', render);
  input.addEventListener('scroll', () => { mirror.scrollTop = input.scrollTop; });
  new doc.defaultView.MutationObserver(render).observe(input, { attributes: true, attributeFilter: ['data-links'] });
  new doc.defaultView.ResizeObserver(render).observe(input);
  render();
}
