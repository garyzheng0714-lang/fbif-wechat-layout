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
  SERIES_TEMPLATES,
} from './foodtalks-series-defaults.js';

// ── 规则读取助手：共享实现收敛到 rule-behavior-utils.js（架构体检 S6，勿再本地复制）──
import {
  behList, behTemplate, compileBehaviorRegex, behaviorRegex, elemText,
} from './rule-behavior-utils.js';

// 兼容导出：foodtalks-series-defaults.js 等外部消费方仍以 compileSeriesRegex 命名引用。
export const compileSeriesRegex = compileBehaviorRegex;
// 从 config 取单条正则（behavior.params.<key>.default），非法/缺省降级 fallback 字符串。
const seriesRegex = behaviorRegex;

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
  const tipLabelRe = seriesRegex(config, 'tip_label_regex', SERIES_REGEX.tip_label_regex);
  const tipItemRe = seriesRegex(config, 'tip_item_regex', SERIES_REGEX.tip_item_regex);
  const footerNoteRe = seriesRegex(config, 'footer_note_regex', SERIES_REGEX.footer_note_regex);
  const capKeywords = behList(config, 'image_caption_keywords', SERIES_CAPTION_KEYWORDS);

  const isHeadingLike = (e) => e && (e.k === 'h' || e.k === 'txt');

  // 商务合作导联行替换（2026-06-11 飞书反馈）：检测「商务合作对接请点击：…」整行 →
  // 「如有合作意向，请移至“联系XX”获取联系方式」；{contact_section} 取文中「联系XX」
  // 标题文本（目录条目与正文标题同文）。找不到联系小节则不替换（不造假引用）。
  const coopReplaceRe = seriesRegex(config, 'biz_coop_replace_regex', SERIES_REGEX.biz_coop_replace_regex);
  const coopTpl = behTemplate(config, 'biz_coop_replacement', SERIES_TEMPLATES.biz_coop_replacement);
  let coopContactSection = '';
  for (const e2 of elems) {
    if (!e2 || (e2.k !== 'h' && e2.k !== 'txt')) continue;
    const m = elemText(e2).trim().match(/^(?:[一二三四五六七八九十百零]+\s*[、.．]\s*)?(联系\S{1,24})\s*$/);
    if (m) { coopContactSection = m[1]; break; }
  }

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
      // 无序号的「参考来源：」段兜底（WPS 导出常丢标题样式 → 一级标题正则不命中）：
      // 仍进入参考模式，保证其后 [n] 条目渲成 15px 灰色（红线：参考来源识别要稳）。
      if (refSecRe.test(plain)) {
        refMode = true;
        out.push({ k: 'h3', runs: [{ type: 'txt', text: plain.replace(/\s*[：:]\s*$/, ''), bold: true }] });
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

    // 商务合作导联行（2026-06-11 飞书反馈）：整行替换为指引文案，落回普通正文段
    if (coopContactSection && coopReplaceRe.test(plain)) {
      const replaced = coopTpl.replace('{contact_section}', coopContactSection);
      out.push({ k: 'p', runs: [{ type: 'txt', text: replaced }], ...carryDocxMeta(e) });
      continue;
    }

    // 文末署名 / 图源说明（规范《十》，成品实测：12px #7f7f7f 斜体小字）
    if (footerNoteRe.test(plain)) {
      out.push({ k: 'footer-note', runs: stripLinks(e.runs), text: plain });
      continue;
    }
    // 文末「提示：」标签（成品：15px 深蓝粗体）
    if (tipLabelRe.test(plain)) {
      out.push({ k: 'tip-title', text: plain });
      continue;
    }
    // 文末「* …」原创/转载声明条目（成品：15px #888888 斜体；外链清除）
    if (tipItemRe.test(plain)) {
      out.push({ k: 'tip-item', runs: stripLinks(e.runs), text: plain });
      continue;
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

  applyContactCentering(out, config);
  return out;
}

function cleanSeriesAuthorName(raw) {
  let s = String(raw || '').replace(/^本文作者\s*[：:]\s*/i, '').trim();
  const parts = s.split(/[,，、/／|｜]/).map(p => p.trim()).filter(Boolean);
  const author = parts.find(p => !/^foodtalks$/i.test(p.replace(/\s+/g, '')));
  if (author) return author;
  s = s.replace(/\bFoodTalks\b/ig, '').replace(/^[,，、/／|｜\s]+|[,，、/／|｜\s]+$/g, '').trim();
  return s;
}

// 从系列分类结果中提取“本文作者”。用于上传后自动补作者名片；不参与正文排版。
export function extractSeriesAuthorName(elems) {
  for (const e of Array.isArray(elems) ? elems : []) {
    if (!e || e.k !== 'footer-note') continue;
    const text = elemText(e).trim();
    const m = text.match(/本文作者\s*[：:]\s*(.+)$/i);
    if (m) return cleanSeriesAuthorName(m[1]);
  }
  return '';
}

// 「联系XXX」公司信息居中：当倒数第二个一级标题含「联系」二字时，其与最后一个一级标题
// （参考来源）之间的【公司信息块】居中。公司信息块从办公室名（加粗 → h3）或地址/官网/电话
// 等字段行（contact_field_regex）开始；一旦进入信息块，后续姓名、职位、备注等短行也同属
// 联系信息，必须跟随居中。联系小节开头偶尔出现的描述性正文仍保持左对齐。
function applyContactCentering(out, config) {
  const h1Pos = [];
  for (let i = 0; i < out.length; i++) if (out[i].k === 'h1') h1Pos.push(i);
  if (h1Pos.length < 2) return;
  const secIdx = h1Pos[h1Pos.length - 2]; // 倒数第二个一级标题
  const sectionRe = seriesRegex(config, 'contact_section_regex', SERIES_REGEX.contact_section_regex);
  const title = out[secIdx].full || out[secIdx].text || '';
  if (!sectionRe.test(title)) return;
  const endIdx = h1Pos[h1Pos.length - 1]; // 最后一个一级标题（参考来源）= 区间右界
  const fieldRe = seriesRegex(config, 'contact_field_regex', SERIES_REGEX.contact_field_regex);
  let inInfoBlock = false;
  for (let i = secIdx + 1; i < endIdx; i++) {
    const e = out[i];
    if (e.k === 'h3') {
      e.center = true; // 加粗公司信息行（办公室名/微信公众号/商务合作）
      inInfoBlock = true;
      continue;
    }
    if (e.k !== 'p') continue;
    const text = elemText(e).trim();
    if (fieldRe.test(text)) {
      e.center = true; // 地址/官网/电话 …
      inInfoBlock = true;
      continue;
    }
    if (inInfoBlock && text && text.length <= 80) {
      e.center = true; // 姓名/职位/备注等联系信息延续行
    }
  }
}

// ── 尾部素材读取 / 合成 ──

// 从 config.modules.ft_series_materials 读素材表，缺省降级到 defaults。
export function getSeriesMaterials(config = {}) {
  const m = config && config.modules && config.modules.ft_series_materials;
  // 顶层键级合并：服务端 sections/groups/cards/... 覆盖默认，缺省项回退 defaults。
  return (m && typeof m === 'object') ? { ...SERIES_MATERIALS_DEFAULT, ...m } : SERIES_MATERIALS_DEFAULT;
}

// 取一级标题第 N 个的序号图 URL（缺该序号则返回空串，渲染器降级为纯文字标题）。
export function sectionImageUrl(config, n) {
  const sections = getSeriesMaterials(config).sections || {};
  const url = sections[n] || sections[String(n)];
  return (typeof url === 'string' && url) ? url : '';
}
