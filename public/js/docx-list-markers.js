// DOCX 列表项的行首符号：无序列表的「•」与有序列表的「1.」。
// 输入是 parser.parseDocxNumbering 解析好的 { numId: { absId, fmt, text, color, start, startOverride } }，
// 本模块不碰 XML，只负责「按文档顺序数序号」这份状态。

export const NO_LIST_MARKER = Object.freeze({ marker: '', markerColor: '' });

// 有序列表只还原 lvlText 恰好一个 %1 的阿拉伯数字（decimal）：这是飞书真实导出（2026-09-28
// 小罐茶稿）核验过的形态。字母/罗马/中文计数、「%1.%2」多级序号没有样本，维持不显示。
const ORDERED_LEVEL_TEXT_RE = /^[^%]*%1[^%]*$/;

// 一次 parseDocx 建一份：有序列表的序号是按文档顺序数出来的状态，不能跨文档共享。
// 返回 listMarkerFor(numId, ilvl) → { marker, markerColor }，每个 <w:p> 调一次。
// 计数口径与 Word 一致：计数挂在 abstractNum 上，多个 w:num 指向同一 abstractNum 时接续编号；
// w:num 的 startOverride 只在该 num 首次出现时把计数重置为起始值。飞书导出每项独占一个
// abstractNum 且 w:start 就是显示序号，同一套口径下自然得到原序号。
// 嵌套有序项（ilvl>0）不显示也不推进一级计数：只解析了 lvl 0，没有下级的格式可用。
export function createListMarkers(numbering) {
  const lastNumber = new Map();
  const seenNums = new Set();
  return function listMarkerFor(numId, ilvl = '') {
    const def = numbering && numId ? numbering[numId] : null;
    if (!def) return NO_LIST_MARKER;
    if (def.fmt === 'bullet') {
      const marker = String(def.text || '').trim();
      return marker ? { marker, markerColor: def.color || '' } : NO_LIST_MARKER;
    }
    if (def.fmt !== 'decimal' || !ORDERED_LEVEL_TEXT_RE.test(def.text || '')) return NO_LIST_MARKER;
    if (Number(ilvl || 0) > 0) return NO_LIST_MARKER;

    let n;
    if (!seenNums.has(numId) && def.startOverride != null) n = def.startOverride;
    else if (lastNumber.has(def.absId)) n = lastNumber.get(def.absId) + 1;
    else n = def.start;
    seenNums.add(numId);
    lastNumber.set(def.absId, n);
    return { marker: def.text.replace('%1', String(n)).trim(), markerColor: def.color || '' };
  };
}
