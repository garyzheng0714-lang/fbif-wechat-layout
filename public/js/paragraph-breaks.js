// 这篇稿子靠什么分段（红线#18）：
//   · 用空行分段（莫特、「什么是」系列的稿子）：段与段之间空一行，没空行连着的几行是一组（网址、目录、联系方式），排紧；
//   · 靠段距分段（2026-11-02 EZ 豆腐稿）：飞书、Word 里每段自带段距，段与段之间不空行，每段都要留空。
// 只看长段（≥30 字）之间：短行连着多是有意排紧的一组，不代表作者的分段习惯。
// 2026-11-02 拿 11 篇飞书稿核对：用空行分段的长段之间 91%–100% 空了一行，EZ 稿只有 15%。

const PROSE_MIN_CHARS = 30;

// paragraphs：按原文顺序 [{ text, blankBefore, body }]；body=false（标题、图、列表）打断相邻。
// 没有相邻长段时按用空行分段（原来的排法）。
export function separatesWithBlankLines(paragraphs) {
  let pairs = 0;
  let blank = 0;
  let prevProse = false;
  for (const p of paragraphs || []) {
    const prose = !!(p && p.body) && String(p.text || '').replace(/\s+/g, '').length >= PROSE_MIN_CHARS;
    if (prose && prevProse) {
      pairs++;
      if (p.blankBefore) blank++;
    }
    prevProse = prose;
  }
  return pairs === 0 || blank * 2 >= pairs;
}
