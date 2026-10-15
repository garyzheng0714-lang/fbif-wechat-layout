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
// 2026-08-17 运营反馈：转载稿尾部加粗的「作者：小食代，编辑：Bobo」曾被升成 h2，
// 补署名类前缀（作者/编辑/编审/责编/主编/美编/出品/策划/撰文/撰稿/排版/设计/校对/审校/选题）。
const CAPTION_LEAD_RE = /^(图|图注|图片|上图|下图|表|资料来源|数据来源|图片来源|来源|制图|截图|拍摄|摄影|注|作者|编辑|编审|责编|主编|美编|出品|策划|撰文|撰稿|排版|设计|校对|审校|选题|Source|Photo|Image|Credit)\s*[：:]/i;
const DECORATIVE_LINE_RE = /^[\s\-_=—–━·•*]+$/;
// 二级序号：「1、」「2.」「1）」后跟正文。「1.8万家店」「3.9元」是小数不是序号
// （#199、#218 曾被排成二级标题）。与 api/wechat_blocks.go 的 siteArabicHeadingRe 同一口径。
export const SECONDARY_NUMBER_RE = /^\d+\s*(?:[、)）]\s*\S|[.．]\s*[^\d\s])/;
const PAREN_NUMBER_RE = /^[（(]\s*(?:[一二三四五六七八九十]+|[0-9]+)\s*[)）]/;
// 再下一层的子序号：「a、」「b.」「①」。只有挂在「1、2、3」下面时才是二级（#121、#229）。
const SUB_NUMBER_RE = /^(?:[A-Za-z]\s*(?:[、)）]\s*\S|[.．]\s*[^\d\s])|[\u2460-\u2473])/;

// 引语与口语感叹不是章节标题（2026-10-15，编辑在 #115/#128/#131/#157 都改回了正文）：
//   - 整句被引号括起来（网友评论、宣传语、受访者原话）；
//   - 「他后来说：“…”」式转述：以引号收尾、引号前是「说/表示/坦言」这类说话动词；
//     「1、资产沉淀：“他们做世界杯，蒙牛做足球”」这种「标签：“口号”」仍是标题（#142）；
//   - 一行连着几句、以感叹号收尾（「是猫咪救了瑞幸！这应该写入…！」）；
//   - 带语气词的口语（「也可以套头上啊，一样拉风！」「是真实可用的塑料袋哦！」）。
// 「“罪恶饮料”，让全球年轻人上头」「文案出圈密码是 “活人感”」「8G冲浪实锤！玩梗玩到出神入化」
// 这类真标题不受影响：前两个不是整句引语，后一个不以感叹号收尾。
const EXCLAIM_SENTENCES_RE = /[！!？?。][^！!？?。]+[！!]+$/;
const MODAL_PARTICLE_RE = /[啊呀啦哦嘛呗咯喔哟][，,！!～~…]/;

function isQuotedSpeech(t) {
  if (!/[”」』"]$/.test(t)) return false;
  if (/^[“「『"]/.test(t)) return t.length >= 8;
  const open = Math.max(t.lastIndexOf('“'), t.lastIndexOf('「'), t.lastIndexOf('『'));
  if (open <= 0) return false;
  return /(?:说|道|表示|称|问|答|坦言|直言|透露|指出|强调|感叹|吐槽|总结)\s*[：:，,]?\s*$/.test(t.slice(0, open));
}

export function isQuoteOrExclamationLine(text) {
  const t = String(text || '').trim();
  return isQuotedSpeech(t) || EXCLAIM_SENTENCES_RE.test(t) || MODAL_PARTICLE_RE.test(t);
}

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
  const visibleRuns = (runs || []).filter(r =>
    r && (r.type === 'txt' || r.type === 'link') && r.text && r.text.trim());
  if (!visibleRuns.length) return false;
  if (!visibleRuns.every(r => r.bold)) return false;
  const plain = visibleRuns.map(r => r.text).join('').trim();
  if (plain.length === 0 || plain.length > 36) return false;
  if (DECORATIVE_LINE_RE.test(plain)) return false;
  // 网站渠道把「1、2、3」当二级标题（标题2）；公众号仍把阿拉伯数字编号当步骤列表，不升标题。
  if (SECONDARY_NUMBER_RE.test(plain) && !(opts && opts.arabicAsSubheading)) return false;
  if (/[。.]$/.test(plain)) return false;
  if (/[，,；;]$/.test(plain)) return false;
  // 「健康意识觉醒：」「女性经济崛起：」这类冒号结尾短句是下文标签，
  // 不是独立章节标题；真标题中的冒号后应还有标题正文。
  if (/[：:]$/.test(plain)) return false;
  if (/^(?:最后|那么|所以|因此|总之|说到底)[，,].*[：:]$/.test(plain)) return false;
  if (captionRe.test(plain)) return false;
  if (isQuoteOrExclamationLine(plain)) return false;
  return true;
}

// hasExplicitDocxHeadings: 正文元素里是否存在来自 Word 标题样式/大纲级别的真标题。
// DOCX 分类产物里 k:'h' 只可能来自 hasHeadingStyle || hasOutlineLevel（docx-article-model.js）。
// 信号优先级原则（根治「正文加粗被误判标题」，见 WORKLOG-docx-heading-signal-priority.md）：
// 作者用了真标题样式 ⇒ 结构信号显式存在 ⇒ 整段加粗只是强调，加粗/编号启发式必须整体停用，
// 保真基准是 Word 里实际显示的样子；只有全文零样式标题（微信作者常态）才轮到启发式补位。
export function hasExplicitDocxHeadings(elems) {
  return (elems || []).some(e => e && e.k === 'h');
}

// promoteHeuristicHeadings: 把被启发式识别为标题的普通段（k:'p'）转换成 'h' 元素。
// 先认显式编号/关键词标题，再认整段加粗标题。已是 author/tip/cap/ref 等非 'p' 块不动。
// 加粗问句还要看上下文（2026-10-15，编辑在 #146/#121 都改回了正文）：
//   - 上一段以冒号收尾（「于是我们继续追问：」「真正的问题变成了：」）：这是被引出的问题；
//   - 两个加粗问句紧挨着（#121 导语「…巨头在抢什么？」「…能做成多大的生意？」）：是设问。
// 不是问句的不受影响（#187「以下是听证会的记录稿：」后面的「从区域折扣店到全国主流超市」
// 是标题），带序号的也不受影响。
export function promoteHeuristicHeadings(elems, opts) {
  const list = elems || [];
  const promoted = list.map((e, i) => {
    if (!e || e.k !== 'p') return e;
    if (e.skipHeadingPromote) return e;
    const plain = elemPlain(e);
    const level = detectHeadingLevel(plain, opts);
    if (level !== 0) return { k: 'h', level, runs: e.runs };
    if (isBoldHeading(e.runs, opts) && !isBodyContextLine(list, i, plain, opts)) {
      const lv = (opts && opts.arabicAsSubheading && SECONDARY_NUMBER_RE.test(plain.trim())) ? 3 : 2;
      return { k: 'h', level: lv, runs: e.runs };
    }
    return e;
  });
  return opts && opts.arabicAsSubheading ? liftOrphanSubheadings(promoted) : promoted;
}

function elemPlain(e) {
  return ((e && e.runs) || []).filter(r => r && (r.type === 'txt' || r.type === 'link')).map(r => r.text || '').join('');
}

function isNumberedLine(t) {
  return SECONDARY_NUMBER_RE.test(t) || SUB_NUMBER_RE.test(t) || PAREN_NUMBER_RE.test(t) ||
    /^[一二三四五六七八九十]+\s*[、：．:\s]/.test(t);
}

function neighborText(list, i, step) {
  for (let j = i + step; j >= 0 && j < list.length; j += step) {
    const e = list[j];
    if (!e || e.k !== 'p') return null;
    const t = elemPlain(e).trim();
    if (t) return { e, t };
  }
  return null;
}

function isBodyContextLine(list, i, plain, opts) {
  const t = plain.trim();
  if (isNumberedLine(t) || !/[？?]$/.test(t)) return false;
  const prev = neighborText(list, i, -1);
  if (prev && /[：:]$/.test(prev.t)) return true;
  const isBoldQuestion = n => n && /[？?]$/.test(n.t) && !isNumberedLine(n.t) && isBoldHeading(n.e.runs, opts);
  return isBoldQuestion(prev) || isBoldQuestion(neighborText(list, i, 1));
}

// 二级序号（1、2、3 /（一））前面没有上级章节标题时，它们自己就是一级（2026-10-15）：
// #151「引子 / 1、… 5、/ 结语」、#121「1、… 4、」下面还套着「a、b、」，编辑都排成一级。
// 前面只有「引子/前言」这类开场标题不算上级。同一组序号整组一起提；有上级（每周新品合集
// 「本周创新看点」下的 1、2、3，62873「三大难题」下的 1、2、3）仍按第 39 条排二级。
// 挂在序号句下面的「a、b、」「①②」排二级（#121、#229）；不在序号句下面的不动（#259
// 「a. 一以贯之的产品策略」下面还有一层不带序号的加粗标题，排二级会上下颠倒）。
// 提上来的标题带 topLevel，渲染时不再按「1、」开头改回二级。
const INTRO_HEADING_RE = /^(?:引子|引言|前言|导语|导读|序言|楔子|开篇|写在前面(?:的话)?)$/;

export function isIntroHeadingText(text) {
  return INTRO_HEADING_RE.test(String(text || '').replace(/[\s:：。．.、]/g, ''));
}

function numberKind(t) {
  if (PAREN_NUMBER_RE.test(t)) return 'paren';
  if (SECONDARY_NUMBER_RE.test(t)) return 'arabic';
  if (SUB_NUMBER_RE.test(t)) return 'sub';
  return '';
}

export function liftOrphanSubheadings(elems) {
  let hasParent = false;
  let liftedKind = '';
  let underNumbered = false;
  return (elems || []).map(e => {
    if (!e || e.k !== 'h' || (e.level && e.level > 3)) return e;
    const t = elemPlain(e).trim() || String(e.text || '').trim();
    const kind = numberKind(t);
    if (kind === 'sub') {
      return underNumbered && e.level !== 3 ? { ...e, level: 3 } : e;
    }
    if (!kind) {
      if (e.level === 3) return e;
      hasParent = !isIntroHeadingText(t);
      liftedKind = '';
      underNumbered = false;
      return e;
    }
    underNumbered = true;
    if (e.level !== 3 || hasParent || (liftedKind && liftedKind !== kind)) return e;
    liftedKind = kind;
    return { ...e, level: 2, topLevel: true };
  });
}
