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
export function buildSiteFooterTipElem(title) {
  const t = String(title || '').trim();
  const titlePart = t ? `原创《${t}》，` : '原创，';
  const text = `提示：*本文为FBIF食品饮料创新（ID：FoodInnovation）${titlePart}不代表FoodTalks立场，转载请联系出处。`;
  return { k: 'tip', runs: [{ type: 'txt', text }] };
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

  // 阶段 1：作者识别（仅第一个非空 p）
  const firstIdx = firstNonEmptyPIndex(out);
  if (firstIdx >= 0) {
    const text = runsToText(out[firstIdx].runs);
    if (authorRe.test(text)) {
      out[firstIdx] = { k: 'author', text };
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
    out.push(buildSiteFooterTipElem(opts.title));
  }

  return out;
}

// FBIF 公众号原创页脚片段（可选，由 config.modules.fbif_footer.enabled 决定）
//
// 注意：刻意不包含「关注公众号 + FoodTalks logo + 点击获取更多」部分——
// 那一段由 FoodTalks 系统在发布时自动追加，若此处重复输出会导致页面重复。
export const FBIF_FOOTER_HTML = [
  '<p style="text-align: justify;"><em><span style="font-size: 14px; color: #7f7f7f;">*资讯投稿与商务合作，请查看 FoodTalks 商务合作方案，并联系 Bobo。</span></em></p>',
  '<p><br></p>',
  '<p style="color: #01396a; font-size: 20px;"><strong>加入微信群</strong></p>',
  '<p><br></p>',
  '<p>[加入微信群二维码请在 FoodTalks 后台手动插入]</p>',
  '<p style="text-align: justify;"><em><span style="color: #7f7f7f; font-size: 14px;">*如遇二维码失效无法加群，请联系工作人员 Lemon（微信号：FBIF_Lemon）。</span></em></p>',
].join('\n');
