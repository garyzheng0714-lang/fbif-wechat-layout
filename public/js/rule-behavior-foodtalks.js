// rule-behavior-foodtalks.js — FoodTalks 网站专属行为模板
//
// 与 public/js/rule-behavior.js（公众号）平行，不互相 import。
// 纯函数模块，不依赖 engine.js / 浏览器 API，可在 Node 直接测试。
//
// 输入：elems 数组（{k: 'p'|'h'|'img'|..., runs: [...]}）+ config 对象
// 输出：新的 elems 数组（不修改输入），其中部分元素被提升为：
//   - k:'author'      作者署名行（仅第一个非空 p 命中时）
//   - k:'tip'         提示行（任意 p 行首命中）
//   - k:'ref-header'  参考来源段头
//   - k:'ref-item'    参考来源条目（[1] [2] 等，遇非 [n] 段则停止）
//
// 规则来源：docs/superpowers/specs/2026-05-27-foodtalks-site-layout-design.md §4

function runsToText(runs) {
  return (runs || [])
    .filter(r => r.type === 'txt' || r.type === 'link')
    .map(r => r.text || '')
    .join('');
}

// 公众号原文尾部专属标记 —— 网站发布版会把这一整段删除（更多文章导流、案例库
// 导流、加群/关注公众号引导、二维码、公众号口径的"提示："页脚）。只要正文里
// 出现下列任一标记，就把它及其后的所有元素全部切掉，正文主体到此为止。
//
// 设计为「找最早出现的标记作为切点」而非「从某固定段切到末尾」，是为了通配：
// 不同文章尾部模块的有无和顺序不同，公众号"提示："的措辞也可能不同（不一定是
// "欢迎转发朋友圈"），但下列任一标记一旦出现，其后必是公众号导流尾，网站不要。
const WECHAT_TRAILER_MARKERS = [
  /\/\s*更多文章\s*\//,
  /\/\s*1000\s*个食品创新案例\s*\//,
  /预览时标签不可点/,
  /加入微信群/,
  /关注公众号/,
  /点击获取更多/,
  /长按.{0,4}识别.{0,2}二维码/,
  /扫码.{0,4}(?:加群|关注|进群)/,
];

// 公众号口径的"提示："页脚特征句 —— "提示："段其后相邻几段命中其一，即认定它是
// 公众号原文自带的转载声明（而非正文里偶然出现的"提示："），应被网站口径页脚替换。
const WECHAT_TIP_SIGNATURES = [
  /欢迎转发朋友圈/,
  /后台回复.{0,4}转载/,
  /投稿与商务合作/,
  /fbifmedia/i,
  /微信[：:]\s*FBIF_/i,
];

// 从 index i 起收集随后若干段文本，用于判断 i 处"提示："是否公众号页脚（其转载
// 声明常分散在"提示："之后的相邻几段）。
function collectFollowingText(elems, i) {
  let s = runsToText(elems[i] && elems[i].runs);
  for (let j = i + 1; j < elems.length && j < i + 5; j++) {
    if (elems[j] && elems[j].k === 'p') s += '\n' + runsToText(elems[j].runs);
  }
  return s;
}

// 从尾部裁掉公众号导流段。返回 { elems, cutWechatTip } ——cutWechatTip 表示是否
// 切掉了公众号口径的"提示："页脚（决定后续是否要补一段网站口径页脚）。
function stripWechatTrailer(elems) {
  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    // 导流标记可能落在正文段(p)或图注(cap，如 "/ 更多文章 /")里，两者都要检查；
    // 图(img)无文本，跳过其文本判定但不阻断扫描。
    const text = (e.k === 'p' || e.k === 'cap') ? runsToText(e.runs) : '';
    // 公众号"提示："页脚：从这一段起切除，并标记需补网站页脚
    if (e.k === 'p' && /^\s*提示[：:]/.test(text) &&
        WECHAT_TIP_SIGNATURES.some(re => re.test(collectFollowingText(elems, i)))) {
      return { elems: elems.slice(0, i), cutWechatTip: true };
    }
    // 其余导流标记：命中即从此切除（不补页脚——这类切点不代表原文有转载声明）
    if (text && WECHAT_TRAILER_MARKERS.some(re => re.test(text))) {
      return { elems: elems.slice(0, i), cutWechatTip: false };
    }
  }
  return { elems, cutWechatTip: false };
}

// 网站口径提示页脚（替换公众号页脚）。格式对齐 foodtalks.cn 发布版：
//   提示：
//   *本文为FBIF食品饮料创新（ID：FoodInnovation）原创《标题》，不代表FoodTalks
//   立场，转载请联系出处。
// 标题缺失时省略书名号段，保持声明仍可读。
// 返回值拆为 title（"提示："）+ rest_runs（正文 runs 数组，保留链接），
// 供 foodtalks-site.js 的 tip 分支渲染为两段式。
export function buildSiteFooterTipElem(title, opts = {}) {
  const t = String(title || '').trim();
  // 来源机构：转载文章用提取到的原始来源（如「食品内参」），FBIF 原创 / 无来源
  // 用默认全称。基石实测：转载文提示声明为「*本文为{来源}原创…」。
  const source = String(opts.source || '').trim();
  const isFbif = !source || /FBIF|食品饮料创新|FoodInnovation/i.test(source);
  const prefix = isFbif
    ? '*本文为FBIF食品饮料创新（ID：FoodInnovation）原创'
    : `*本文为${source}原创`;
  const suffix = '，不代表FoodTalks立场，转载请联系出处。';
  const runs = [{ type: 'txt', text: prefix }];
  if (t) {
    const label = `《${t}》`;
    // 原文链接优先用显式传入的 sourceUrl（processBlocks 从 data.source 传入），
    // 其次回退到全局变量（site.html fetchArticle 设置的 window._currentArticleUrl）。
    const sourceUrl = String(opts.sourceUrl || '').trim() ||
      ((typeof window !== 'undefined' && window._currentArticleUrl) || '');
    if (sourceUrl && /^https?:\/\//i.test(sourceUrl)) {
      runs.push({ type: 'link', text: label, href: sourceUrl });
    } else {
      runs.push({ type: 'txt', text: label });
    }
  }
  runs.push({ type: 'txt', text: suffix });
  return { k: 'tip', title: '提示：', rest_runs: runs };
}

// 从 elems 中提取转载来源机构名（用于提示声明 prefix）。扫描「来源：XX」整行段，
// 去掉「（ID：xxx）」尾注保留纯机构名。在 stripWechatTrailer 切尾【之前】调用，
// 因为来源行常紧邻被切掉的导流段。无匹配返回空串。
export function extractRepostSource(elems) {
  if (!Array.isArray(elems)) return '';
  for (const e of elems) {
    if (!e || e.k !== 'p') continue;
    const text = runsToText(e.runs).trim();
    const m = text.match(/^来源[：:]\s*(.+)$/);
    if (m) return m[1].replace(/[（(]\s*ID[：:].*$/i, '').trim();
  }
  return '';
}

// 合成网站口径开头作者行：FoodTalks 编辑会把公众号正文【尾部】的「来源：X」「作者：Y，
// 编审：Z」重写成统一格式放到文章【开头】（基石 62245/62246/62238 即如此）。当公众号
// 开头没有作者行、但正文里能提取到来源/作者时，据此合成一行，渲染成 14px 灰斜体署名。
// 格式：「本文为{来源}原创，{作者：Y，编审：Z}，转载请联系出处。」缺项自动省略。
export function buildAuthorLineFromSource(elems) {
  if (!Array.isArray(elems)) return '';
  let source = '', authorSeg = '';
  for (const e of elems) {
    if (!e || e.k !== 'p') continue;
    const text = runsToText(e.runs).replace(/[​-‏⁠﻿]/g, '').trim();
    let m;
    if (!source && (m = text.match(/^来源[：:]\s*(.+)$/))) {
      source = m[1].replace(/[（(]\s*ID[：:].*$/i, '').trim();
    }
    // 作者整行（含其后「编审/编辑/主编/美编：…」），原样保留
    if (!authorSeg && /^作者[：:]/.test(text)) authorSeg = text.replace(/[。\s]*$/, '');
  }
  if (!source && !authorSeg) return '';
  const parts = [];
  if (source) parts.push(`本文为${source}原创`);
  if (authorSeg) parts.push(authorSeg);
  let line = parts.join('，');
  if (source) line += '，转载请联系出处。';
  else if (!/[。.]$/.test(line)) line += '。';
  return line;
}

function isEmptyP(e) {
  if (!e || e.k !== 'p') return false;
  return runsToText(e.runs).trim() === '';
}

function firstNonEmptyPIndex(elems) {
  for (let i = 0; i < elems.length; i++) {
    if (elems[i].k === 'p' && !isEmptyP(elems[i])) return i;
  }
  return -1;
}

export function applyFoodTalksBehavior(elems, config = {}, opts = {}) {
  if (!Array.isArray(elems)) return elems;

  const behavior = (config && config.behavior) || {};
  const authorRe    = new RegExp(behavior.author_regex     || '^\\s*作者[：:]\\s*');
  const tipRe       = new RegExp(behavior.tip_regex        || '^\\s*提示[：:]');
  const refHeaderRe = new RegExp(behavior.ref_header_regex || '^\\s*参考(?:来源|资料)[：:]\\s*$');
  const refItemRe   = new RegExp(behavior.ref_item_regex   || '^\\s*\\[\\d+\\]');

  // 阶段 0：切掉公众号原文尾部导流段（更多文章 / 案例库 / 加群 / 公众号"提示："
  // 页脚 / 二维码）。网站发布版不保留这些；若切掉的是公众号转载声明页脚，则在
  // 正文末尾补一段网站口径页脚。仅对从公众号 URL 导入的文章生效（isRepost）。
  const trimmed = opts.isRepost ? stripWechatTrailer(elems) : { elems, cutWechatTip: false };

  // 浅拷贝每个元素（runs 数组本身也新建），保证不原地修改输入
  const out = trimmed.elems.map(e => ({
    ...e,
    runs: Array.isArray(e.runs) ? e.runs.slice() : e.runs,
  }));

  // 阶段 1：作者识别（开头第一个非空 p）。两种格式：
  //   - 「作者：X」(FBIF 原创)；其后若紧跟「编辑/编审：Y」段则合并为一行（基石 62244
  //     抓取出来是「作者：Pride」「编辑：Bobo」两段，网站口径合并为「作者：Pride，编辑：Bobo。」）
  //   - 「本文为X原创…作者…转载请联系出处」转载整行（基石 62245/62246 网站口径首行）
  const firstIdx = firstNonEmptyPIndex(out);
  if (firstIdx >= 0) {
    const text = runsToText(out[firstIdx].runs);
    const isAuthorLine = authorRe.test(text) ||
      /^\s*本文(系|为).{0,40}(原创|首发|授权)/.test(text);
    if (isAuthorLine) {
      let merged = text;
      const nextIdx = firstIdx + 1;
      if (out[nextIdx] && out[nextIdx].k === 'p') {
        const nextText = runsToText(out[nextIdx].runs).trim();
        if (/^(编辑|编审|责编|主编|美编)[：:]/.test(nextText)) {
          merged = merged.replace(/[，,。\s]*$/, '') + '，' + nextText;
          if (!/[。.]$/.test(merged)) merged += '。';
          out.splice(nextIdx, 1);  // 移除被并入的编辑段（out 是新数组，不改输入）
        }
      }
      out[firstIdx] = { k: 'author', text: merged };
    }
  }

  // 阶段 1.5：开头无作者行但正文（含尾部）有「来源：X」「作者：Y」→ 合成开头作者行。
  // 仅 isRepost 触发；扫【原始 elems】（来源/作者常在被切的尾部）。
  if (opts.isRepost) {
    const headIsAuthor = firstIdx >= 0 && out[firstIdx] && out[firstIdx].k === 'author';
    if (!headIsAuthor) {
      const synthesized = buildAuthorLineFromSource(elems);
      if (synthesized) out.unshift({ k: 'author', text: synthesized });
    }
  }

  // 阶段 2：提示行识别（任意 k:'p'）
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (e.k !== 'p') continue;
    const text = runsToText(e.runs);
    if (tipRe.test(text)) {
      out[i] = { k: 'tip', runs: e.runs };
    }
  }

  // 阶段 3：参考来源段头 + 后续 ref-item（遇非 [n] 开头段即中断）
  let refMode = false;
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (e.k !== 'p') {
      // 任何不是 p 的元素（包括前面阶段提升出来的 author/tip，以及 h/img）
      // 都中断 refMode —— 否则参考段中间夹一个提示行会让其后的 [n] 仍然被
      // 误识别为 ref-item。
      if (refMode) refMode = false;
      continue;
    }
    const text = runsToText(e.runs).trim();
    if (!refMode) {
      if (refHeaderRe.test(text)) {
        out[i] = { k: 'ref-header' };
        refMode = true;
      }
      continue;
    }
    // refMode 已开启：[n] 开头是新条目；破折号/连字符开头（— – ─ -）是上一条的
    // 说明延续行（基石 news/62285 参考来源里每条 [n] 下紧跟一行「—说明」，整块连续
    // 无空行）。两者都归 ref-item，避免说明行 fallback 成正文段而被插入空行。
    if (refItemRe.test(text) || /^[—–─-]/.test(text)) {
      out[i] = { k: 'ref-item', runs: e.runs };
    } else {
      refMode = false;  // 遇到既非 [n] 也非说明行的段，停止识别
    }
  }

  // 阶段 4：补网站口径提示页脚。仅当上一步切掉了公众号转载声明页脚时才补，
  // 避免给非原创 / 已是 FoodTalks 原生口径的文章凭空加声明。
  if (trimmed.cutWechatTip) {
    // 来源从【原始 elems】提取（切尾前），因为「来源：XX」行常紧邻被切的导流段。
    out.push(buildSiteFooterTipElem(opts.title, {
      source: opts.isRepost ? extractRepostSource(elems) : '',
      sourceUrl: opts.sourceUrl || '',
    }));
  }

  return out;
}

// ─────────────────────────────────────────────────────────────────────────
// 页脚库（footer library）
//
// 用户 2026-05-31 裁定：网站文章底部页脚必须由本工具【完整输出】（含「关注公众号
// + FoodTalks logo + 点击获取更多」），用固定 static.foodtalks.cn 资源复刻——
// 不再依赖 FoodTalks 系统自动追加（旧 CLAUDE.md 规则 20 据此作废）。页脚拆成可
// 独立勾选的片段，由 config.modules.fbif_footer.segments.<key> 控制；不同文章可
// 用不同预设组合（见 docs/superpowers/specs/2026-05-31-ft-footer-library-and-fidelity.md）。
//
// 下列资源 URL 已核对 6 篇真实发布版基石（62285/62244/62245/62246/62238/62243）
// 100% 一致，可安全固化为常量。
const FT_FOOTER_DECOR_IMG  = 'https://static.foodtalks.cn/image/post/07079a66940e1ff57e71b140e7e36357.webp';
const FT_FOOTER_GROUP_QR   = 'https://static.foodtalks.cn/image/news/6e309987660da9af009b89be08c4aa74ed78.png';
const FT_FOOTER_ACCOUNT_QR = 'https://static.foodtalks.cn/image/post/b5b448f324a538209eb53379bef8dea9a7bf.jpg';
const FT_FOOTER_LOGO       = 'https://static.foodtalks.cn/uploads/images/202007/e155b7852c10a72f7b73157fdf9c4bf0.png';

// 每个 segment 是一组连续输出的 HTML 行。tip（提示声明）不在此处——它内容动态、
// 位置在正文参考来源之后，由 applyFoodTalksBehavior 提升为 tip elem 走正文流。
// 这里只放 tip 之后的固定营销段。
export const FOOTER_SEGMENTS = {
  biz_coop: [
    '<p style="text-align: left;"><em><span style="color: #7f7f7f; font-size: 14px;">*资讯投稿与商务合作，请查看&ldquo;<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/30752" target="_blank" rel="noopener">FoodTalks商务合作方案</a>&rdquo;，并联系Bobo。</span></em></p>',
  ],
  join_group: [
    `<p style="text-align: center;"><img style="display: block; margin-left: auto; margin-right: auto;" src="${FT_FOOTER_DECOR_IMG}" alt="" width="600" /></p>`,
    '<p style="color: #01396a; font-size: 20px;"><strong>加入微信群</strong></p>',
    '<p><br /></p>',
    `<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="${FT_FOOTER_GROUP_QR}" alt="00" width="600" height="267" /></p>`,
    '<p style="text-align: left;"><em><span style="color: #7f7f7f; font-size: 14px;">*如遇二维码失效无法加群，请联系工作人员Lemon（微信号：FBIF_Lemon）。</span></em></p>',
  ],
  follow_account: [
    '<p style="color: #01396a; font-size: 20px;"><strong>关注公众号</strong></p>',
    '<p><br /></p>',
    `<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="${FT_FOOTER_ACCOUNT_QR}" alt="" width="600" height="221" /></p>`,
  ],
  logo: [
    `<p><img style="max-width: 100%; height: 27px; width: 126px;" src="${FT_FOOTER_LOGO}" alt="FoodTalks logo" width="130" height="27" border="0" /></p>`,
  ],
  more_info: [
    '<p>点击获取更多<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/" target="_blank" rel="noopener">全球食品资讯</a></p>',
  ],
};

// 片段输出顺序（对齐基石：商务合作 → 加群 → 关注公众号 → logo → 点击获取更多）
export const FOOTER_SEGMENT_ORDER = ['biz_coop', 'join_group', 'follow_account', 'logo', 'more_info'];

// 按 segments 开关拼接页脚 HTML（不含 tip）。segments 缺省或全 false 时返回空串。
export function buildFooterHtml(segments) {
  const seg = segments || {};
  const lines = [];
  for (const key of FOOTER_SEGMENT_ORDER) {
    if (seg[key] && FOOTER_SEGMENTS[key]) lines.push(...FOOTER_SEGMENTS[key]);
  }
  return lines.join('\n');
}

// 页脚预设库：积累常用组合，左侧栏一键切换。后端 /api/rules 可覆盖。
export const FOOTER_PRESETS = [
  { id: 'fbif-original', name: 'FBIF原创完整页脚', segments: { tip: true, biz_coop: true, join_group: true, follow_account: true, logo: true, more_info: true } },
  { id: 'repost-simple', name: '转载简版（提示+商务）', segments: { tip: true, biz_coop: true, join_group: false, follow_account: false, logo: false, more_info: false } },
  { id: 'tip-only',      name: '仅提示声明',          segments: { tip: true, biz_coop: false, join_group: false, follow_account: false, logo: false, more_info: false } },
  { id: 'none',          name: '无页脚',              segments: { tip: false, biz_coop: false, join_group: false, follow_account: false, logo: false, more_info: false } },
];
