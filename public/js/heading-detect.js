// 标题识别（网站渲染器用）— 纯函数、零外部依赖，可独立单元测试。
// 公众号原文里的章节小标题有两种写法，都要升级成 <h2>：
//   1. 显式编号/关键词：「一、…」「（一）…」「总结」
//   2. 整段加粗：作者把整段文字加粗、独占一段（微信编辑器没有真 h2）。这是最普遍的写法。
// FoodTalks 发布版（金标准 docs/agents/baselines/62285-foodtalks-backend.html）正是把这两类
// 都渲染成 <h2 style="text-align: justify;">…</h2>。

const TITLE_KEYWORDS = [
  '背景', '简介', '总结', '分析', '案例', '介绍', '概述', '结论', '展望',
];

// 图注/来源类开头词——标题判据要排除它们，避免把"图片来源：xx"之类误升级。
const CAPTION_LEAD_RE = /^(图|图注|图片|上图|下图|表|资料来源|数据来源|图片来源|来源|制图|截图|拍摄|摄影|注|Source|Photo|Image|Credit)\s*[：:]/i;
const DECORATIVE_LINE_RE = /^[\s\-_=—–━·•*]+$/;
const ARABIC_LIST_PREFIX_RE = /^\d+\s*[.、．]\s*\S/;

// detectHeadingLevel: 基于纯文本的显式标题识别。返回 2 / 3 / 0（不是标题）。
// opts（可选，红线下放：调用方从 config 解析后注入，缺省降级到代码内 const）：
//   { titleKeywords }。
export function detectHeadingLevel(text, opts) {
  const t = String(text || '').trim();
  if (!t) return 0;

  // （一）（二）→ h3
  if (/^[（(]\s*[一二三四五六七八九十]+\s*[)）]/.test(t)) return 3;

  // 一、二、三...（中文标点：、：．: 后跟内容）→ h2
  if (/^[一二三四五六七八九十]+\s*[、：．:\s]/.test(t)) return 2;

  // 关键词 + 短句 + 无尾句号 → h2
  const kws = (opts && Array.isArray(opts.titleKeywords) && opts.titleKeywords.length)
    ? opts.titleKeywords : TITLE_KEYWORDS;
  if (t.length <= 30 && !/[。.]$/.test(t)) {
    for (const kw of kws) {
      if (t.startsWith(kw)) return 2;
    }
  }

  return 0;
}

// isBoldHeading: 整段加粗 → 标题。判据收紧到只认真标题，避开"整段加粗的强调正文"：
//   1. 段内每个可见文本 run 都 bold（不能混排非粗体文本）
//   2. 纯文本短（<= 36 字；实测两篇基准标题最长 25 字，正文段动辄上百字）
//   3. 不以句号（。.）结尾——陈述句正文常以句号收尾；但问号/感叹号结尾要放行，
//      因为疑问/感叹句标题真实存在（如基石 news/62244「毕业20年的老粉凭什么"回坑"？」）。
//      实测两篇里被排除的"加粗强调正文"都是 43~45 字 + 句号结尾，靠句号 + 长度双重拦截。
//   4. 不是图注/来源行
// 段内允许逗号/顿号/冒号（"疯狂的梦想：5年要开到1000家店"是标题）。
export function isBoldHeading(runs, opts) {
  const captionRe = (opts && opts.captionLeadRe) || CAPTION_LEAD_RE;
  const txtRuns = (runs || []).filter(r => r && r.type === 'txt' && r.text && r.text.trim());
  if (!txtRuns.length) return false;
  if (!txtRuns.every(r => r.bold)) return false;
  const plain = txtRuns.map(r => r.text).join('').trim();
  if (plain.length === 0 || plain.length > 36) return false;
  if (DECORATIVE_LINE_RE.test(plain)) return false;
  if (ARABIC_LIST_PREFIX_RE.test(plain)) return false;
  if (/[。.]$/.test(plain)) return false;
  if (/[，,；;]$/.test(plain)) return false;
  if (/^(?:最后|那么|所以|因此|总之|说到底)[，,].*[：:]$/.test(plain)) return false;
  if (captionRe.test(plain)) return false;
  return true;
}

// promoteHeuristicHeadings: 把被启发式识别为标题的普通段（k:'p'）转换成 'h' 元素。
// 先认显式编号/关键词标题，再认整段加粗标题。已是 author/tip/cap/ref 等非 'p' 块不动。
export function promoteHeuristicHeadings(elems, opts) {
  return (elems || []).map(e => {
    if (!e || e.k !== 'p') return e;
    const plain = (e.runs || []).filter(r => r.type === 'txt').map(r => r.text).join('');
    const level = detectHeadingLevel(plain, opts);
    if (level !== 0) return { k: 'h', level, runs: e.runs };
    if (isBoldHeading(e.runs, opts)) return { k: 'h', level: 2, runs: e.runs };
    return e;
  });
}
