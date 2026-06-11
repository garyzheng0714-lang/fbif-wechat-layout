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
//   - k:'footer-note' 文末图源/作者小字（网站「什么是系列」口径）
//
// 规则来源：docs/superpowers/specs/2026-05-27-foodtalks-site-layout-design.md §4

import {
  ASSET_LIBRARY_DEFAULT,
  FOOTER_SEGMENTS_DEFAULT,
  FOOTER_SEGMENT_ORDER_DEFAULT,
  FOOTER_PRESETS_DEFAULT,
} from './footer-segments-defaults.js';
import { resolveAssetRefs } from './asset-library.js';

function runsToText(runs) {
  return (runs || [])
    .filter(r => r.type === 'txt' || r.type === 'link')
    .map(r => r.text || '')
    .join('');
}

// ── 规则读取助手（红线下放：优先服务端 config.behavior，缺省/非法降级到代码内 const）──
// behavior = config.behavior（服务端 raw 下发：{templates, params, lists, *_regex}）。
function behaviorTemplate(behavior, key, fallback) {
  const v = behavior && behavior.templates && behavior.templates[key];
  return (typeof v === 'string' && v) ? v : fallback;
}
function behaviorParamStr(behavior, key, fallback) {
  const p = behavior && behavior.params && behavior.params[key];
  const v = p && p.default;
  return (typeof v === 'string' && v) ? v : fallback;
}
function behaviorListRaw(behavior, key) {
  const v = behavior && behavior.lists && behavior.lists[key];
  return (Array.isArray(v) && v.length) ? v : null;
}
// 正则字符串 → RegExp。识别开头 (?i) 等内联 flag（JS new RegExp 不支持内联 flag 组，
// 服务端用 RE2 (?i) 表达大小写不敏感，这里转成构造函数 flag）。
export function compileSiteRegex(src) {
  if (src instanceof RegExp) return src;
  let s = String(src), flags = '';
  const m = s.match(/^\(\?([a-z]+)\)/);
  if (m) { flags = m[1].replace(/[^gimsuy]/g, ''); s = s.slice(m[0].length); }
  return new RegExp(s, flags);
}
// 编译 behavior.lists 里的正则字符串列表；缺省/全部非法时降级到 fallback RegExp 数组。
function compileMarkerList(behavior, key, fallback) {
  const strs = behaviorListRaw(behavior, key);
  if (strs) {
    const out = [];
    for (const x of strs) { try { out.push(compileSiteRegex(x)); } catch (e) { /* 跳过非法项 */ } }
    if (out.length) return out;
  }
  return fallback;
}
// 单条正则：优先 behavior.params.<key>.default，非法/缺省降级 fallback 字符串。
function behaviorRegex(behavior, key, fallback) {
  const src = behaviorParamStr(behavior, key, fallback);
  try { return compileSiteRegex(src); } catch (e) { return compileSiteRegex(fallback); }
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

// 外部媒体公众号的尾巴（矩阵、版权、商务合作等）。这类词在正文中偶尔也可能出现，
// 所以只在靠近文章尾部且有尾部确认信号时触发。
const EXTERNAL_MEDIA_TRAILER_MARKERS = [
  /^\s*延伸阅读\s*$/,
  /^\s*END\s*$/i,
  /^\s*版权声明[：:]?\s*$/,
  /红餐网已同步入驻/,
  /内容交流\s*[\/／]\s*采访\s*[\/／]\s*转载/,
  /品牌几何资讯矩阵/,
  /商务合作\s*[|｜]\s*(?:电话|微信)/,
];

const EXTERNAL_MEDIA_TAIL_CONFIRM_RE =
  /(?:来源[：:]|作者[：:]|版权声明|红餐网已同步入驻|内容交流\s*[\/／]\s*采访\s*[\/／]\s*转载|品牌几何资讯矩阵|商务合作\s*[|｜]\s*(?:电话|微信))/;

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
  let s = elemText(elems[i]);
  for (let j = i + 1; j < elems.length && j < i + 5; j++) {
    if (elems[j] && (elems[j].k === 'p' || elems[j].k === 'h' || elems[j].k === 'cap')) {
      s += '\n' + elemText(elems[j]);
    }
  }
  return s;
}

function elemText(e) {
  if (!e) return '';
  if (Array.isArray(e.runs)) return runsToText(e.runs);
  return typeof e.text === 'string' ? e.text : '';
}

function stripSectionPrefix(text) {
  return String(text || '').replace(/^\s*(?:[一二三四五六七八九十]+|[0-9]+)\s*[、.．]\s*/, '').trim();
}

function matchesRefHeader(text, refHeaderRe) {
  const raw = String(text || '').trim();
  if (!raw) return false;
  const stripped = stripSectionPrefix(raw);
  const candidates = [raw];
  if (stripped !== raw) candidates.push(stripped);
  // 兼容已持久化的旧规则：旧 regex 要求「参考来源：」必须带冒号；
  // 网站「什么是系列」成品标题是「七、参考来源」，无冒号。
  candidates.push(stripped + '：', stripped + ':');
  for (const candidate of candidates) {
    refHeaderRe.lastIndex = 0;
    if (refHeaderRe.test(candidate)) return true;
  }
  return false;
}

function splitFooterNoteLines(text) {
  const raw = String(text || '').replace(/\s+/g, ' ').trim();
  if (!raw) return [];
  const markers = ['本文作者：', '本文作者:', '本文图片来源：', '本文图片来源:'];
  for (const marker of markers) {
    const idx = raw.indexOf(marker);
    if (idx > 0) return [raw.slice(0, idx).trim(), raw.slice(idx).trim()].filter(Boolean);
  }
  return [raw];
}

function isNearTail(elems, i) {
  const len = Array.isArray(elems) ? elems.length : 0;
  if (len <= 0) return false;
  if (len <= 8) return i >= Math.max(1, len - 3);
  return i >= Math.floor(len * 0.65);
}

function isExternalMediaTrailer(elems, i, text, markers) {
  if (!text || !isNearTail(elems, i)) return false;
  if (!markers.some(re => re.test(text))) return false;
  const following = collectFollowingText(elems, i);
  if (/^\s*END\s*$/i.test(text)) return EXTERNAL_MEDIA_TAIL_CONFIRM_RE.test(following);
  return true;
}

// 切点守卫：候选切点之后若仍存在「参考来源」段头，说明正文主体未结束（正文联系
// 板块里的「扫码关注XX」「微信公众号」等字样会撞上导流标记——上海艾录文实测：
// 联系区图注「（扫码关注上海艾录）」后面还有完整的参考来源/文末署名，绝不能切）。
// 真正的公众号导流尾固定在参考来源之后，所以「后方还有参考来源 → 跳过该候选切点」。
function hasRefSectionAfter(elems, i, refHeaderRe) {
  if (!refHeaderRe) return false;
  for (let j = i + 1; j < elems.length; j++) {
    const e = elems[j];
    if (!e || (e.k !== 'p' && e.k !== 'h')) continue;
    if (matchesRefHeader(elemText(e).trim(), refHeaderRe)) return true;
  }
  return false;
}

// 从尾部裁掉公众号导流段。返回 { elems, cutWechatTip } ——cutWechatTip 表示是否
// 切掉了公众号口径的"提示："页脚（决定后续是否要补一段网站口径页脚）。
function stripWechatTrailer(elems, markers, signatures, externalMarkers, refHeaderRe) {
  const trailerMarkers = markers || WECHAT_TRAILER_MARKERS;
  const tipSignatures = signatures || WECHAT_TIP_SIGNATURES;
  const mediaMarkers = externalMarkers || EXTERNAL_MEDIA_TRAILER_MARKERS;
  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    // 导流标记可能落在正文段(p)或图注(cap，如 "/ 更多文章 /")里，两者都要检查；
    // 图(img)无文本，跳过其文本判定但不阻断扫描。
    const text = (e.k === 'p' || e.k === 'h' || e.k === 'cap') ? elemText(e) : '';
    // 公众号"提示："页脚：从这一段起切除，并标记需补网站页脚
    if (e.k === 'p' && /^\s*提示[：:]/.test(text) &&
        tipSignatures.some(re => re.test(collectFollowingText(elems, i)))) {
      if (hasRefSectionAfter(elems, i, refHeaderRe)) continue;
      return { elems: elems.slice(0, i), cutWechatTip: true };
    }
    // 其余导流标记：命中即从此切除（不补页脚——这类切点不代表原文有转载声明）
    if (text && trailerMarkers.some(re => re.test(text))) {
      if (hasRefSectionAfter(elems, i, refHeaderRe)) continue;
      return { elems: elems.slice(0, i), cutWechatTip: false };
    }
    if (isExternalMediaTrailer(elems, i, text, mediaMarkers)) {
      if (hasRefSectionAfter(elems, i, refHeaderRe)) continue;
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
  const behavior = opts.behavior || {};
  // 来源机构：转载文章用提取到的原始来源（如「食品内参」），FBIF 原创 / 无来源
  // 用默认全称。基石实测：转载文提示声明为「*本文为{来源}原创…」。
  const source = String(opts.source || '').trim();
  const fbifSourceRe = behaviorRegex(behavior, 'fbif_source_pattern', '(?i)FBIF|食品饮料创新|FoodInnovation');
  const isFbif = !source || fbifSourceRe.test(source);
  const prefix = isFbif
    ? behaviorTemplate(behavior, 'site_tip_prefix_fbif', '*本文为FBIF食品饮料创新（ID：FoodInnovation）原创')
    : `*本文为${source}原创`;
  const suffix = behaviorTemplate(behavior, 'site_tip_suffix', '，不代表FoodTalks立场，转载请联系出处。');
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
  return { k: 'tip', title: behaviorTemplate(behavior, 'tip_title_default', '提示：'), rest_runs: runs };
}

// 从 elems 中提取转载来源机构名（用于提示声明 prefix）。扫描「来源：XX」整行段，
// 去掉「（ID：xxx）」尾注保留纯机构名。在 stripWechatTrailer 切尾【之前】调用，
// 因为来源行常紧邻被切掉的导流段。无匹配返回空串。
export function extractRepostSource(elems, behavior = {}) {
  if (!Array.isArray(elems)) return '';
  const sourceRe = behaviorRegex(behavior, 'repost_source_regex', '^来源[：:]\\s*(.+)$');
  for (const e of elems) {
    if (!e || e.k !== 'p') continue;
    const text = runsToText(e.runs).trim();
    const m = text.match(sourceRe);
    if (m) return m[1].replace(/[（(]\s*ID[：:].*$/i, '').trim();
  }
  return '';
}

// 合成网站口径开头作者行：FoodTalks 编辑会把公众号正文【尾部】的「来源：X」「作者：Y，
// 编审：Z」重写成统一格式放到文章【开头】（基石 62245/62246/62238 即如此）。当公众号
// 开头没有作者行、但正文里能提取到来源/作者时，据此合成一行，渲染成 14px 灰斜体署名。
// 格式：「本文为{来源}原创，{作者：Y，编审：Z}，转载请联系出处。」缺项自动省略。
export function buildAuthorLineFromSource(elems, behavior = {}) {
  if (!Array.isArray(elems)) return '';
  const sourceRe = behaviorRegex(behavior, 'repost_source_regex', '^来源[：:]\\s*(.+)$');
  let source = '', authorSeg = '';
  for (const e of elems) {
    if (!e || e.k !== 'p') continue;
    const text = runsToText(e.runs).replace(/[​-‏⁠﻿]/g, '').trim();
    let m;
    if (!source && (m = text.match(sourceRe))) {
      source = m[1].replace(/[（(]\s*ID[：:].*$/i, '').trim();
    }
    // 作者整行（含其后「编审/编辑/主编/美编：…」），原样保留
    if (!authorSeg && /^作者[：:]/.test(text)) authorSeg = text.replace(/[。\s]*$/, '');
  }
  if (!source && !authorSeg) return '';
  // 转载来源作者行句式（红线下放：{source} 占位，缺省降级同值 const）。
  const prefixTpl = behaviorTemplate(behavior, 'author_line_repost_prefix', '本文为{source}原创');
  const suffixTpl = behaviorTemplate(behavior, 'author_line_repost_suffix', '，转载请联系出处。');
  const parts = [];
  if (source) parts.push(prefixTpl.replace('{source}', source));
  if (authorSeg) parts.push(authorSeg);
  let line = parts.join('，');
  if (source) line += suffixTpl;
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
  const refHeaderRe = new RegExp(behavior.ref_header_regex || '^\\s*参考(?:来源|资料)[：:]?\\s*$');
  const refItemRe   = new RegExp(behavior.ref_item_regex   || '^\\s*\\[\\d+\\]');
  // 文末小字（14px 灰斜体）：图源/作者说明 + 正文「*注：…」补充注释（62303 实测同口径）。
  const footerNoteRe = behaviorRegex(behavior, 'footer_note_regex', '^(?:本文(?:未标注来源|作者\\s*[：:]|图片来源)|图片均来自|[*＊]\\s*注[：:])');
  // 商务合作导联行（62139/62303 实测：14px 斜体、文本 #7f7f7f、链接保留）。
  const coopLineRe = behaviorRegex(behavior, 'biz_coop_line_regex', '^\\s*(?:商务合作对接请点击|如有合作意向)');
  // 公众号导流尾段标记 / 提示页脚特征句（红线下放：behavior.lists，缺省降级 const）。
  const trailerMarkers = compileMarkerList(behavior, 'wechat_trailer_markers', WECHAT_TRAILER_MARKERS);
  const tipSignatures  = compileMarkerList(behavior, 'wechat_tip_signatures', WECHAT_TIP_SIGNATURES);
  const externalMarkers = compileMarkerList(behavior, 'external_media_trailer_markers', EXTERNAL_MEDIA_TRAILER_MARKERS);

  // 阶段 0：切掉公众号原文尾部导流段（更多文章 / 案例库 / 加群 / 公众号"提示："
  // 页脚 / 二维码）。网站发布版不保留这些；若切掉的是公众号转载声明页脚，则在
  // 正文末尾补一段网站口径页脚。仅对从公众号 URL 导入的文章生效（isRepost）。
  const trimmed = opts.isRepost ? stripWechatTrailer(elems, trailerMarkers, tipSignatures, externalMarkers, refHeaderRe) : { elems, cutWechatTip: false };

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
      const synthesized = buildAuthorLineFromSource(elems, behavior);
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

  // 阶段 2.5：商务合作导联行（「商务合作对接请点击：…」/「如有合作意向，请移至…」）。
  // 62139/62303 成品口径：整行 14px 斜体，纯文本灰色，链接保留默认色。
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (e.k !== 'p' || e.tocEntry) continue;
    const text = runsToText(e.runs).trim();
    if (text && coopLineRe.test(text)) {
      out[i] = { k: 'coop-line', runs: e.runs };
    }
  }

  // 阶段 3：参考来源段头 + 后续 ref-item（遇非 [n] 开头段即中断）。
  // 网站「什么是系列」URL 源稿的参考标题常是 h2「七、参考来源」，标题本身应保留，
  // 但其后的 [n] 条目仍要进入 ref-item 分支，才能连续无空行并套灰色。
  let refMode = false;
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (e.k === 'h') {
      const text = runsToText(e.runs).trim();
      refMode = matchesRefHeader(text, refHeaderRe);
      continue;
    }
    if (e.k !== 'p') {
      // 任何不是 p 的元素（包括前面阶段提升出来的 author/tip，以及 h/img）
      // 都中断 refMode —— 否则参考段中间夹一个提示行会让其后的 [n] 仍然被
      // 误识别为 ref-item。
      if (refMode) refMode = false;
      continue;
    }
    // 目录条目（如「六、参考来源」）是导航文本，不是参考来源段头——
    // matchesRefHeader 会剥掉「六、」前缀导致误命中，这里显式跳过（飞书反馈问题十/十六）。
    if (e.tocEntry) continue;
    const text = runsToText(e.runs).trim();
    if (!refMode) {
      if (matchesRefHeader(text, refHeaderRe)) {
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

  // 阶段 4：文末图源/作者小字。真实网站口径是 14px #7f7f7f 斜体，且可能被抓成一段。
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (!e || e.k !== 'p') continue;
    const text = runsToText(e.runs).trim();
    if (!text || !footerNoteRe.test(text)) continue;
    const lines = splitFooterNoteLines(text);
    if (lines.length) out[i] = { k: 'footer-note', lines };
  }

  // 阶段 5：补网站口径提示页脚。仅当上一步切掉了公众号转载声明页脚时才补，
  // 避免给非原创 / 已是 FoodTalks 原生口径的文章凭空加声明。
  if (trimmed.cutWechatTip) {
    // 来源从【原始 elems】提取（切尾前），因为「来源：XX」行常紧邻被切的导流段。
    out.push(buildSiteFooterTipElem(opts.title, {
      source: opts.isRepost ? extractRepostSource(elems, behavior) : '',
      sourceUrl: opts.sourceUrl || '',
      behavior,
    }));
  }

  return out;
}

// ─────────────────────────────────────────────────────────────────────────
// 页脚库（footer library）
//
// 用户 2026-05-31 裁定：网站文章底部页脚由本工具【完整输出】（含「关注公众号 +
// FoodTalks logo + 点击获取更多」）。页脚【内容与预设】以服务端 /api/rules 的
// config.modules.fbif_footer 为准（规则后台可编辑——红线：规则不硬编码）；
// FOOTER_*_DEFAULT（footer-segments-defaults.js）仅作离线/接口异常时的降级兜底。

// 按 segments 勾选 + 页脚库内容拼接页脚 HTML（不含 tip）。
//   enabledSegments: { biz_coop:1, join_group:1, ... } 当前文章勾选了哪些片段
//   library: 服务端 config.modules.fbif_footer（提供 segments 内容 + segment_order），
//            缺省时降级到 FOOTER_SEGMENTS_DEFAULT。
//   assetLibrary: 服务端 config.modules.asset_library，缺省时降级到 ASSET_LIBRARY_DEFAULT。
// 勾选全 false / 缺省时返回空串。
export function buildFooterHtml(enabledSegments, library, assetLibrary, scope = 'global') {
  const seg = enabledSegments || {};
  const lib = library || {};
  const segments = (lib.segments && typeof lib.segments === 'object') ? lib.segments : FOOTER_SEGMENTS_DEFAULT;
  const order = (Array.isArray(lib.segment_order) && lib.segment_order.length)
    ? lib.segment_order : FOOTER_SEGMENT_ORDER_DEFAULT;
  const assets = (assetLibrary && (Array.isArray(assetLibrary) || Array.isArray(assetLibrary.assets)))
    ? assetLibrary
    : ASSET_LIBRARY_DEFAULT;
  const lines = [];
  for (const key of order) {
    if (!seg[key]) continue;
    const entry = segments[key];
    if (!entry) continue;
    // 兼容服务端 {html_lines:[...]} 与纯数组两种结构
    const htmlLines = Array.isArray(entry) ? entry : (Array.isArray(entry.html_lines) ? entry.html_lines : []);
    lines.push(...resolveAssetRefs(htmlLines, assets, scope));
  }
  return lines.join('\n');
}

// 兼容旧导出名（降级默认值，真相以服务端为准）。
export const FOOTER_PRESETS = FOOTER_PRESETS_DEFAULT;
export const FOOTER_SEGMENT_ORDER = FOOTER_SEGMENT_ORDER_DEFAULT;
