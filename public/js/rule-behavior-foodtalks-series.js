// rule-behavior-foodtalks-series.js — FoodTalks「什么是」系列专属行为 / 分类层。
//
// 与 rule-behavior.js（FBIF 公众号）、rule-behavior-foodtalks.js（FoodTalks 网站）平行，
// 三者互不 import（红线 #19：规则物理隔离）。纯函数模块，不依赖 engine.js / 浏览器全局
// （除尾部选择读 window._ftSeriesFooter，已做 typeof 守卫），可在 Node 直接单测。
//
// 输入：classifyDocxArticle 产出的中性 elem 序列（k:'h'|'txt'|'img'|'cap'|'table'，
//       带 docxMeta：blankBefore/styleName/...）+ config（含 behavior / modules）。
// 输出：系列 elem 序列，render 时各自渲成内联样式 HTML：
//   h1（序号图 + 居中标题）/ h2 / h3 / toc-title / toc-item / p / img / cap /
//   rec-title / rec-item / ref-item / table
//
// 规则来源：飞书《策划文公众号排版标准》。所有识别正则 / 文案 / 素材 URL 优先读
// 服务端 config，缺省降级到 foodtalks-series-defaults.js 的常量（红线：不硬编码）。

import {
  SERIES_REGEX,
  SERIES_CAPTION_KEYWORDS,
  SERIES_MATERIALS_DEFAULT,
  SERIES_FOOTER_DEFAULT,
  SERIES_TEMPLATES,
} from './foodtalks-series-defaults.js';

// ── 规则读取助手（优先 config.behavior，缺省/非法降级 const）──
function behOf(config) { return (config && config.behavior) || {}; }
function behParamStr(config, key, fallback) {
  const p = behOf(config).params;
  const s = p && p[key] && p[key].default;
  return (typeof s === 'string' && s) ? s : fallback;
}
function behList(config, key, fallback) {
  const v = behOf(config).lists && behOf(config).lists[key];
  return (Array.isArray(v) && v.length) ? v : fallback;
}
// 行为文案：优先 config.behavior.templates.<key>，缺省/非法降级 fallback（仿网站 behaviorTemplate）。
function behTemplate(config, key, fallback) {
  const t = behOf(config).templates;
  const v = t && t[key];
  return (typeof v === 'string' && v) ? v : fallback;
}

// 正则字符串 → RegExp。识别开头 (?i) 内联 flag（服务端 RE2 用 (?i) 表达大小写不敏感，
// JS new RegExp 不支持内联 flag 组，这里转为构造函数 flag）。
export function compileSeriesRegex(src) {
  if (src instanceof RegExp) return src;
  let s = String(src), flags = '';
  const m = s.match(/^\(\?([a-z]+)\)/);
  if (m) { flags = m[1].replace(/[^gimsuy]/g, ''); s = s.slice(m[0].length); }
  return new RegExp(s, flags);
}
// 从 config 取单条正则（behavior.params.<key>.default），非法/缺省降级 fallback 字符串。
function seriesRegex(config, key, fallbackStr) {
  const src = behParamStr(config, key, fallbackStr);
  try { return compileSeriesRegex(src); } catch (e) { return compileSeriesRegex(fallbackStr); }
}

function runsToText(runs) {
  return (runs || [])
    .filter(r => r && (r.type === 'txt' || r.type === 'link'))
    .map(r => r.text || '')
    .join('');
}
function elemText(e) {
  if (!e) return '';
  if (Array.isArray(e.runs)) return runsToText(e.runs);
  return typeof e.text === 'string' ? e.text : '';
}

// 半角括号转中文括号（规范《二·五·二级标题》：需注意调整为中文括号）。
function normalizeParens(text) {
  return String(text || '').replace(/\(/g, '（').replace(/\)/g, '）');
}

// 去掉 runs 里的超链接（推荐阅读「无超链接」），link 降级为纯文本 txt。
function stripLinks(runs) {
  return (runs || []).map(r => (r && r.type === 'link') ? { type: 'txt', text: r.text || '' } : r);
}

// 图注判定：含来源/图注关键词，或紧跟图片的短中性段。
function looksLikeCaption(text, keywords) {
  const plain = String(text || '').trim();
  if (!plain || plain.length > 80) return false;
  const kws = (Array.isArray(keywords) && keywords.length) ? keywords : SERIES_CAPTION_KEYWORDS;
  for (const kw of kws) { if (plain.indexOf(kw) >= 0) return true; }
  return false;
}

// 整段加粗短句 → 三级标题（正文加粗）。避免把整段加粗的强调正文误判：限长 + 不以句号结尾。
function isBoldLine(runs) {
  const txt = (runs || []).filter(r => r && r.type === 'txt' && r.text && r.text.trim());
  if (!txt.length) return false;
  if (!txt.every(r => r.bold)) return false;
  const plain = txt.map(r => r.text).join('').trim();
  if (!plain || plain.length > 30) return false;
  if (/[。.]$/.test(plain)) return false;
  return true;
}

// 透传共享层 docxMeta（连续紧凑段距判定 isCompactDocxPair 的输入，红线#18）。
function carryDocxMeta(e) {
  return {
    blankBefore: e.blankBefore, fromDocx: e.fromDocx,
    styleId: e.styleId, styleName: e.styleName,
    contextualSpacing: e.contextualSpacing, spacing: e.spacing, isList: e.isList,
  };
}

// ── 主分类：中性 elem → 系列 elem ──
export function classifySeries(neutralElems, config = {}) {
  const elems = Array.isArray(neutralElems) ? neutralElems : [];
  const level1Re = seriesRegex(config, 'level1_regex', SERIES_REGEX.level1_regex);
  const level2Re = seriesRegex(config, 'level2_regex', SERIES_REGEX.level2_regex);
  const tocRe    = seriesRegex(config, 'toc_marker_regex', SERIES_REGEX.toc_marker_regex);
  const recRe    = seriesRegex(config, 'recommended_reading_regex', SERIES_REGEX.recommended_reading_regex);
  const refSecRe = seriesRegex(config, 'ref_section_regex', SERIES_REGEX.ref_section_regex);
  const refItemRe = seriesRegex(config, 'ref_item_regex', SERIES_REGEX.ref_item_regex);
  const capKeywords = behList(config, 'image_caption_keywords', SERIES_CAPTION_KEYWORDS);

  const isHeadingLike = (e) => e && (e.k === 'h' || e.k === 'txt');

  const out = [];
  let sectionIndex = 0; // 第 N 个一级标题 → 序号图 N
  let refMode = false;

  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    if (!e) continue;
    const plain = elemText(e).trim();

    // 图片：透传（保留 blankBefore 供间距判断）
    if (e.k === 'img') { refMode = false; out.push({ k: 'img', src: e.src || '', w: e.w, aspect: e.aspect, blankBefore: e.blankBefore }); continue; }
    // 共享层已配对的图注
    if (e.k === 'cap') { out.push({ k: 'cap', text: e.text || '', runs: e.runs }); continue; }
    // 表格透传
    if (e.k === 'table') { refMode = false; out.push({ k: 'table', rows: e.rows || [], colWidths: e.colWidths || [] }); continue; }

    // 目录：标题 + 其后连续的一级序号行（遇 blankBefore 视为正文起点，停止收集）
    if (isHeadingLike(e) && tocRe.test(plain)) {
      out.push({ k: 'toc-title' });
      refMode = false;
      let collected = 0;
      let j = i + 1;
      while (j < elems.length) {
        const ej = elems[j];
        if (!isHeadingLike(ej)) break;
        const pj = elemText(ej).trim();
        if (!level1Re.test(pj)) break;
        if (collected > 0 && ej.blankBefore) break; // 正文起点
        out.push({ k: 'toc-item', text: pj });
        collected++;
        j++;
      }
      i = j - 1;
      continue;
    }

    // 推荐阅读 / 相关阅读：段头 + 其后连续标题行（无超链接、空心圆）
    if (isHeadingLike(e) && recRe.test(plain)) {
      out.push({ k: 'rec-title', text: plain.replace(/[：:]\s*$/, '：') });
      refMode = false;
      let j = i + 1;
      let recItems = 0;
      while (j < elems.length) {
        const ej = elems[j];
        if (!ej) break;
        if (ej.k === 'img' || ej.k === 'cap' || ej.k === 'table') break;
        if (ej.k === 'h') break;                          // DOCX 标题样式段（含数字小标题）= 块结束
        const pj = elemText(ej).trim();
        if (!pj) break;                                   // 空段 = 推荐块结束
        if (ej.blankBefore) break;                        // 原文有空行 = 块结束
        if (level1Re.test(pj) || level2Re.test(pj) || tocRe.test(pj) || recRe.test(pj)) break;
        if (/^\s*\d+\s*[、.．]/.test(pj)) break;            // 阿拉伯数字小标题（1、2、…）= 下一节
        if (isBoldLine(ej.runs)) break;                   // 加粗小标题 = 下一节，不并入推荐
        out.push({ k: 'rec-item', runs: stripLinks(ej.runs) });
        recItems++;
        j++;
      }
      // 规范《二·七》：每个推荐阅读块（有条目时）下方补一行 14px 灰斜体说明文字。
      if (recItems > 0) {
        out.push({ k: 'rec-note', text: behTemplate(config, 'recommended_reading_note', SERIES_TEMPLATES.recommended_reading_note) });
      }
      i = j - 1;
      continue;
    }

    // 标题分级
    if (isHeadingLike(e)) {
      if (level1Re.test(plain)) {
        const stripped = plain.replace(level1Re, '').trim();
        sectionIndex++;
        out.push({ k: 'h1', index: sectionIndex, text: stripped, full: plain });
        // 参考来源是一级标题（如「六、参考来源」），其后 [n] 行进入参考条目模式
        refMode = refSecRe.test(stripped);
        continue;
      }
      if (level2Re.test(plain)) {
        refMode = false;
        out.push({ k: 'h2', text: normalizeParens(plain) });
        continue;
      }
      // 其余：DOCX 标题样式段（k:'h'，如阿拉伯数字小标题「2、个性与定制化」，标了标题样式
      // 但不匹配 一、/（一）），或整段加粗短句 → 三级标题（正文加粗）。共享层 k:'h' 只带 text
      // 不带 runs，缺失时从纯文本合成 runs，避免文字丢失（红线：失败要响，不能静默吞字）。
      if (e.k === 'h' || isBoldLine(e.runs)) {
        refMode = false;
        const runs = (Array.isArray(e.runs) && e.runs.length) ? e.runs : [{ type: 'txt', text: plain }];
        out.push({ k: 'h3', runs });
        continue;
      }
    }

    // 参考来源条目（规范《六·超链接》：非本公众号链接全部清除 → 参考外链降级纯文本）
    if (refMode) {
      if (refItemRe.test(plain) || /^[—–─-]/.test(plain)) {
        out.push({ k: 'ref-item', runs: stripLinks(e.runs), ...carryDocxMeta(e) });
        continue;
      }
      refMode = false; // 遇非 [n] / 非延续行，退出参考模式
    }

    // 紧跟图片但共享层没配上的来源说明行 → 图注
    if (isHeadingLike(e) && out.length && out[out.length - 1].k === 'img' && looksLikeCaption(plain, capKeywords)) {
      out.push({ k: 'cap', runs: e.runs });
      continue;
    }

    // 普通正文（runs 缺失时从 text 兜底合成，防止 text-only 元素被静默吞字）；
    // 透传 docxMeta 供连续紧凑段距判定（红线#18）。
    out.push({ k: 'p', runs: (Array.isArray(e.runs) && e.runs.length) ? e.runs : [{ type: 'txt', text: plain }], ...carryDocxMeta(e) });
  }

  return out;
}

// ── 尾部素材读取 / 合成 ──

// 从 config.modules.ft_series_materials 读素材表，缺省降级到 defaults。
export function getSeriesMaterials(config = {}) {
  const m = config && config.modules && config.modules.ft_series_materials;
  // 顶层键级合并：服务端 sections/groups/cards/... 覆盖默认，缺省项回退 defaults。
  return (m && typeof m === 'object') ? { ...SERIES_MATERIALS_DEFAULT, ...m } : SERIES_MATERIALS_DEFAULT;
}

// 当前文章尾部选择：侧栏 window._ftSeriesFooter 覆盖 > 规则 footer_default。
export function getSeriesFooterSelection(config = {}) {
  const def = getSeriesMaterials(config).footer_default || SERIES_FOOTER_DEFAULT;
  const live = (typeof window !== 'undefined' && window._ftSeriesFooter) || null;
  return {
    author_card: (live && live.author_card != null) ? live.author_card : def.author_card,
    group: (live && live.group != null) ? live.group : def.group,
    bd_card: (live && live.bd_card != null) ? live.bd_card : def.bd_card,
  };
}

// 取一级标题第 N 个的序号图 URL（缺该序号则返回空串，渲染器降级为纯文字标题）。
export function sectionImageUrl(config, n) {
  const sections = getSeriesMaterials(config).sections || {};
  const url = sections[n] || sections[String(n)];
  return (typeof url === 'string' && url) ? url : '';
}

// 合成尾部图片 elem 序列：作者名片 →（空行）→ BD 名片 → 群二维码。
// BD 名片复用 cards 名片库（默认 Eileen，规则后台 footer_default.bd_card 可改）。
// 缺图项自动跳过；返回 [{k:'footer-img', src}] 序列（render 各自渲成自适应图）。
export function buildSeriesFooterElems(config = {}) {
  const mats = getSeriesMaterials(config);
  const sel = getSeriesFooterSelection(config);
  const out = [];
  // getSeriesMaterials 始终返回含 cards/groups 的完整对象，故无需再 `|| {}` 兜底。
  const authorUrl = sel.author_card && mats.cards[sel.author_card];
  if (authorUrl) out.push({ k: 'footer-img', src: authorUrl, gap: true });
  const bdUrl = sel.bd_card && mats.cards[sel.bd_card];
  if (bdUrl) out.push({ k: 'footer-img', src: bdUrl, gap: true });
  const groupUrl = sel.group && mats.groups[sel.group];
  if (groupUrl) out.push({ k: 'footer-img', src: groupUrl, gap: true });
  return out;
}
