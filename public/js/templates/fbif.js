// FBIF 公众号排版模板 — CSS class-based rendering
const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter, looksLikeGifSource, convertRuns, convertText } = engineModule;
const ruleModule = await import('../rule-presets.js' + assetQuery);
const { getActiveRuleConfig, getActiveRuleBehavior, getRuleNumber, getRuleBoolean } = ruleModule;
const behaviorModule = await import('../rule-behavior.js' + assetQuery);
const { applyBehaviorToElems, normalizeAuthorName, renderBehaviorTemplate } = behaviorModule;
const behaviorUtilsModule = await import('../rule-behavior-utils.js' + assetQuery);
const { tryCompileBehaviorRegex } = behaviorUtilsModule;
// DOCX 内容结构分类已抽到共享模块（公众号与 FoodTalks 网站共用一套，红线#19：
// 两渲染器不互相 import，都只 import 本数据模块）。公众号走默认 opts（consumeAuthorLine
// / classifyReferences 均为 true），输出与抽取前逐字节一致（golden tea-pyramid 守卫）。
const docxModelModule = await import('../docx-article-model.js' + assetQuery);
const { classifyDocxArticle } = docxModelModule;
// 标题启发式纯函数（网站渲染器也用，红线#19 中性共享模块，不跨渲染器 import）。
const headingDetectModule = await import('../heading-detect.js' + assetQuery);
const { detectHeadingLevel, isBoldHeading, hasExplicitDocxHeadings } = headingDetectModule;
const tableModule = await import('./shared/table-renderer.js' + assetQuery);
const { renderTable: renderSharedTable } = tableModule;
// 底部追加式区块（统一模型）：中性编排器 + 公众号渲染器（红线 #19：不 import 网站渲染器）。
const blockRenderModule = await import('../blocks/block-render.js' + assetQuery);
const { renderBottomBlocksHtml } = blockRenderModule;
const mpBlocksModule = await import('./mp-blocks.js' + assetQuery);
const { mpBlockRenderers } = mpBlocksModule;
const articleFooterModule = await import('../article-footer-defaults.js' + assetQuery);
const {
  mergeArticleArtifactOverrides,
  prepareBottomBlocksForArticle,
  resolveArticleFooterSelection,
} = articleFooterModule;

// ---- Gold-standard WeChat ProseMirror wrapping (now class-based) ----
function leafWrap(inner) {
  return '<span leaf="" class="wx-leaf">' + inner + '</span>';
}
function leafWrapAuthor(inner) {
  return '<span leaf="" class="wx-la">' + inner + '</span>';
}

function ruleNumber(key) {
  return getRuleNumber(getActiveRuleConfig(), key);
}

// rawActiveModules 读【原始】服务端规则模块（绕过 normalizeRuleConfig 丢 .modules 的行为）。
// 与网站 site.html serverRuleModules 同源做法——底部区块/素材库等结构化模块必须从原始 config 取。
function rawActiveModules() {
  const ac = (typeof window !== 'undefined' && window._activeConfig) || {};
  let cfg = (ac.preset && (ac.preset.raw_config || ac.preset.config)) || ac.config || {};
  if (cfg && cfg.config && cfg.config.modules) cfg = cfg.config;
  return (cfg && cfg.modules && typeof cfg.modules === 'object') ? cfg.modules : {};
}

function activeBehaviorAccount() {
  const pack = typeof window !== 'undefined' ? window._activeRulePack : null;
  return {
    name: (pack && pack.behaviorAccountName) || 'FBIF',
    id: (pack && pack.id) || 'fbif',
  };
}

// 读 behavior.params.<key>.default 编译为正则（红线下放：公众号识别正则规则后台可配；
// 读不到/非法时降级到传入的 fallback RegExp const）。共享编译器负责 RE2 内联 flag、
// 浏览器安全子集与输入长度上限，避免这里形成第二套正则执行口径。
function behaviorRegexParam(key, fallbackRe) {
  try {
    const params = getActiveRuleBehavior().params || {};
    const src = params[key] && params[key].default;
    if (typeof src === 'string' && src) {
      return tryCompileBehaviorRegex(src, `behavior.params.${key}.default`) || fallbackRe;
    }
  } catch (e) { /* 降级到 const */ }
  return fallbackRe;
}

// 读 behavior.params.<key>.default 为正数（红线下放：扫描上限等数值规则后台可配；
// 读不到/非法时降级到传入的 fallback）。
function behaviorNumberParam(key, fallback) {
  try {
    const params = getActiveRuleBehavior().params || {};
    const v = Number(params[key] && params[key].default);
    if (Number.isFinite(v) && v > 0) return v;
  } catch (e) { /* 降级到 const */ }
  return fallback;
}

// 读 behavior.params.<key>.default 为布尔值；缺失或非法时降级到 fallback。
function behaviorBooleanParam(key, fallback) {
  try {
    const params = getActiveRuleBehavior().params || {};
    const v = params[key] && params[key].default;
    if (typeof v === 'boolean') return v;
  } catch (e) { /* 降级到 fallback */ }
  return fallback;
}

function behaviorContext(author, source) {
  return {
    article: {
      author: author || '',
      source: source || '',
      author_normalized: normalizeAuthorName(author),
    },
    account: activeBehaviorAccount(),
  };
}

function articleMeta(author, source) {
  return {
    author: author || '',
    source: source || '',
    author_normalized: normalizeAuthorName(author),
  };
}

function attributionText(role, name, baseContext, behavior) {
  if (role === '作者') {
    const context = {
      ...baseContext,
      article: { ...baseContext.article, author: name || '', author_normalized: normalizeAuthorName(name) },
    };
    return renderBehaviorTemplate(behavior.templates.author_line, context);
  }
  if (role === '来源') {
    const context = {
      ...baseContext,
      article: { ...baseContext.article, source: name || '' },
    };
    return renderBehaviorTemplate(behavior.templates.source_line, context);
  }
  return role + '：' + name;
}

// DOCX run 的显式着色（<w:rPr><w:color w:val="..">）→ inline style。
// 不着色的 run 一个字节都不变，继续吃 .wx-t 的规则正文色；css-inline 把 class 样式排在
// inline style 之前合并，所以这里写的颜色在预览和微信复制层都赢过正文色。
// 两个值【不算】着色意图，必须忽略，否则整篇正文会盖掉规则包的正文色：
//   - 'auto'：OOXML 明确表示「跟随默认前景色」；
//   - '000000'：Word/WPS 在作者没动过颜色时也会给每个 run 补一份的默认黑。
// 飞书导出只在作者真正改过颜色处才写 w:color（本文 613 段里仅 5 处灰 #8f959e），
// 这正是导入后灰色注释被排成正文黑的根因。
const RUN_HEX_COLOR_RE = /^[0-9a-fA-F]{6}$/;
function runColorStyle(color) {
  const v = String(color || '').trim().replace(/^#/, '');
  if (!RUN_HEX_COLOR_RE.test(v)) return '';
  if (/^0{6}$/.test(v)) return '';
  return ' style="color: #' + v.toLowerCase() + ';"';
}

// 无序列表的项目符号。公众号不产 <ul>/<li>：微信编辑器会重排列表结构，css-inline 也只
// 认 .wx-* class，裸 <li> 到了微信就丢样式。所以符号渲染成正文行首的一个 span——结构上
// 仍是普通段落（间距/署名/参考来源等既有判定全部不受影响），视觉上还原「• 内容」。
// 符号字形与颜色都来自 numbering.xml 原值（飞书是蓝点 #3370ff），拿不到颜色就跟随正文色。
// 符号与正文之间用不间断空格 U+00A0：普通空格会让微信在窄屏把符号单独甩到上一行末尾。
function renderListMarker(e) {
  const marker = (e && e.listMarker) || '';
  if (!marker) return '';
  return leafWrap('<span textstyle="" class="wx-t"' + runColorStyle(e.listMarkerColor) +
    '>' + esc(marker) + '\u00A0</span>');
}

function renderRuns(runs, opts) {
  const textCls = (opts && opts.textCls) || 'wx-t';
  return runs.filter(r => r.type !== 'img').map(r => {
    if (r.type === 'txt') {
      const cls = r.bold ? textCls + ' wx-bold' : textCls;
      return leafWrap('<span textstyle="" class="' + cls + '"' + runColorStyle(r.color) + '>' + esc(r.text) + '</span>');
    }
    if (r.type === 'link') {
      const h = (r.href || '');
      if (!/^https?:\/\//i.test(h)) return leafWrap(esc(r.text));
      return leafWrap('<a class="wx-a" href="' + escAttr(h) + '">' + esc(r.text) + '</a>');
    }
    return '';
  }).join('');
}

// ---- Table rendering (统一表格样式) ----
// 公众号表格【必须全 inline-style】：css-inline.js 只内联 .wx-* class，碰不到裸 <table>/<td>，
// 复制到微信会丢样式。单元格文本也不走 wx-t（那会被 css-inline 套上正文色 #544545，与
// 表格自有色冲突），而是输出极简 inline HTML（<strong>/<a>/纯文本），样式全由 <td> 内联控制。
// 颜色/字号/边框/内距全部读 getActiveRuleConfig() 的扁平规则键（红线下放，规则后台可配），
// 读不到/非法时降级到与服务端 defaultRuleConfig 逐字一致的 const。
function ruleColor(key, fallback) {
  const v = getActiveRuleConfig()[key];
  return (typeof v === 'string' && /^#[0-9a-fA-F]{6}$/.test(v.trim())) ? v.trim() : fallback;
}
function tableRunRenderers() {
  return {
    text(r) {
      if (r.type === 'txt') { const t = esc(r.text); return r.bold ? '<strong>' + t + '</strong>' : t; }
      return '';
    },
    link(r) {
      if (r.type === 'link') {
        const h = r.href || '';
        return /^https?:\/\//i.test(h) ? '<a href="' + escAttr(h) + '">' + esc(r.text) + '</a>' : esc(r.text);
      }
      return '';
    },
    image(r) {
      if (r.type === 'img' && r.src) {
        const rpol = r.src.startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
        const gifAttr = r.gif ? ' data-type="gif"' : '';
        return '<img src="' + escAttr(r.src) + '"' + gifAttr + rpol +
          ' style="max-width: 100%; height: auto; display: block; margin: 0 auto;" />';
      }
      return '';
    },
  };
}

function renderTable(elem) {
  const cfg = getActiveRuleConfig();
  const fs = getRuleNumber(cfg, 'table_font_size') || 15;
  const padRaw = getRuleNumber(cfg, 'table_cell_padding');
  const pad = Number.isFinite(padRaw) && padRaw >= 0 ? padRaw : 8;
  return renderSharedTable(elem, {
    headerBg: ruleColor('table_header_bg', '#01396a'),
    headerColor: ruleColor('table_header_text_color', '#ffffff'),
    borderColor: ruleColor('table_border_color', '#e5e7eb'),
    fontSize: fs,
    padding: pad,
    headerBold: getRuleBoolean(cfg, 'table_header_bold'),
    marginAuto: true,
    renderers: tableRunRenderers(),
  });
}

// ---- Spacing & Section Helpers ----
function sectionClass(name, gap) {
  return name + (gap ? ' wx-gap' : '');
}
function sec(inner, gap) {
  return '<section class="' + sectionClass('wx-p', gap) + '">' + inner + '</section>';
}
function secCenter(inner, gap) {
  return '<section class="' + sectionClass('wx-pc', gap) + '">' + inner + '</section>';
}

function sameDocxStyle(a, b) {
  if (!a || !b) return false;
  if (a.styleId && b.styleId) return a.styleId === b.styleId;
  if (a.styleName && b.styleName) return a.styleName === b.styleName;
  return false;
}

function isCompactDocxPair(a, b) {
  if (!a || !b) return false;
  if ((a.k !== 'txt' && a.k !== 'ref') || (b.k !== 'txt' && b.k !== 'ref')) return false;
  if (!a.fromDocx || !b.fromDocx || b.blankBefore) return false;
  if (a.isList && b.isList) return true;
  if (sameDocxStyle(a, b) && (a.contextualSpacing || b.contextualSpacing)) return true;

  const after = Number(a.spacing && a.spacing.after);
  const before = Number(b.spacing && b.spacing.before);
  return Number.isFinite(after) && after <= 160 &&
    (!Number.isFinite(before) || before <= 160);
}

function isMoteAuthorCard(e) {
  return !!e && (e.role === 'mote-author-card' || e.className === 'wx-mote-author');
}

function elemExtraClass(e) {
  return isMoteAuthorCard(e) ? ' wx-mote-author' : '';
}

function isNumericHeadingMarker(e) {
  return !!e && e.k === 'h' && /^0?\d{1,2}$/.test((e.text || '').trim());
}

function shouldSpaceAfter(e, next) {
  // No `next` means this is the article body's tail. The FBIF footer is
  // concatenated afterwards with `margin:0;padding:0;` on its outer section,
  // so without `wx-gap` here the last paragraph (often a [n] reference) sits
  // flush against the 「提示：」 line. Emit the standard 20px gap.
  if (!next) return true;
  if (next.blankBefore && !(e.k === 'ref' && next.k === 'ref')) return true;
  if (isNumericHeadingMarker(e) && next.k === 'h') return false;
  if (e.compact && next.compact && !next.blankBefore) return false;
  if (isMoteAuthorCard(next)) return true;
  if (next.k === 'cap') return false;
  if (e.k === 'ref' && next.k === 'ref') return false;
  if (e.k === 'attr' && next.k === 'attr') return false;
  if (e.k === 'img' && (next.k === 'cap' || next.k === 'img')) return false;
  if (isCompactDocxPair(e, next)) return false;
  return true;
}

// DOCX 前置「作者：」行识别正则默认值（红线下放 behavior.params.docx_author_line_regex）。
const RE_DOCX_AUTHOR_LINE = /^作者[：:]\s*(.+)$/;
// 编辑部结构标记默认值（红线下放 behavior.params.docx_front_matter_marker_regex）：
// 命中即重置前置扫描窗口，防止前置材料过长时作者行掉出上限（2026-07-11 生产事故根因）。
const RE_DOCX_FRONT_MATTER_MARKER = /^(正文|标题|封面|推荐语|校对)[：:]?$/;

// ---- DOCX Classification ----
// 内容结构分类下沉到共享模块 docx-article-model.js（与 FoodTalks 网站共用一套）。
// 公众号走默认 opts：consumeAuthorLine=true（吃掉作者行、顶部单独渲染）、
// classifyReferences=true（分类 refH/ref）。正则仍由本模板从全局 behavior 解析后注入
// （规则源无关边界）；ref_heading_prefix_regex 是把旧 line 224 写死正则下放为可配项，
// 缺省 fallback 与旧值逐字一致——故公众号输出逐字节不变（golden tea-pyramid 守卫）。
// DOCX 整段加粗标题启发式升级：classifyDocxArticle 只认 Word 大纲级别/标题样式，但大量作者
// 用「整段加粗」或「一、xx」中文编号写章节标题。复用 heading-detect 纯函数（isBoldHeading 看
// runs 的 bold 标志、detectHeadingLevel 看纯文本）把这类正文段（k:'txt'）升级为标题（k:'h'），
// 与 MD 路径整段加粗标题识别对齐。只动顶层正文段：参考段内行已是 k:'ref'、图注 k:'cap'，不受影响。
// 保留原 elem 的段距 meta（blankBefore 等），与既有 docx heading 同款（docx-article-model.js:203）。
function promoteDocxBoldHeadings(elems) {
  return (elems || []).map(e => {
    if (!e || e.k !== 'txt') return e;
    const plain = (e.runs || []).filter(r => r && r.type === 'txt').map(r => r.text).join('');
    if (detectHeadingLevel(plain) === 0 && !isBoldHeading(e.runs)) return e;
    const text = plain.trim().replace(/[：:]$/, '').replace(/^0?\d+\s+/, '');
    if (!text) return e;
    const { k, runs, ...meta } = e;
    return { k: 'h', text, ...meta };
  });
}

function classifyDocx(paragraphs, imgCache) {
  const result = classifyDocxArticle(paragraphs, imgCache, {
    authorLineRegex: behaviorRegexParam('docx_author_line_regex', RE_DOCX_AUTHOR_LINE),
    refSectionRegex: behaviorRegexParam('ref_section_regex', RE_REF_SECTION),
    refHeadingPrefixRegex: behaviorRegexParam('ref_heading_prefix_regex', /^(参考(来源|文献|资料|链接)|信息来源)/),
    frontMatterMarkerRegex: behaviorRegexParam('docx_front_matter_marker_regex', RE_DOCX_FRONT_MATTER_MARKER),
    frontMatterScanLimit: behaviorNumberParam('docx_front_matter_scan_limit', 25),
  });
  // 信号优先级：正文里已有真·Word 标题样式的标题（k:'h'）时，作者显然会用标题样式，
  // 整段加粗只是强调 ⇒ 启发式停用（否则加粗强调句会被误升 h2，判据加多少条都堵不完）。
  // 零样式标题的文档（微信作者常态）才走启发式补位。见 WORKLOG-docx-heading-signal-priority.md。
  if (hasExplicitDocxHeadings(result.elems)) return result;
  return { ...result, elems: promoteDocxBoldHeadings(result.elems) };
}

// ---- Attribution Patterns ----
// 作者/撰文/撰稿 → "作者", 出品/来源/出处 → "来源"
const RE_AUTHOR = /^(?:\*\*)?(?:作者|撰文|撰稿|文)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)/;
const RE_SOURCE = /^(?:\*\*)?(?:出品|来源|出处)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)/;
const RE_EDITOR = /^(?:\*\*)?(?:编辑|责编)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)/;
// 参考来源段头（锚定整行）默认正则 + x-reader 元数据过滤默认正则（红线下放）。
const RE_REF_SECTION = /^(参考(来源|文献|资料|链接)|信息来源)[：:]?$/;
const RE_MD_METADATA_SKIP = /^Author:\s|^Published:\s|^Source:\s/i;

function parseAttribution(para) {
  const clean = para.replace(/\*\*/g, '').trim();
  // 作者/来源/编辑识别正则（红线下放 behavior.params.*_line_regex，缺省降级同值 const）。
  const reAuthor = behaviorRegexParam('author_line_regex', RE_AUTHOR);
  const reSource = behaviorRegexParam('source_line_regex', RE_SOURCE);
  const reEditor = behaviorRegexParam('editor_line_regex', RE_EDITOR);
  let m = para.match(reAuthor);
  if (m) return { role: '作者', name: m[1].replace(/\*\*/g, '').trim() };
  m = para.match(reSource);
  if (m) return { role: '来源', name: m[1].replace(/\*\*/g, '').trim() };
  m = para.match(reEditor);
  if (m) return { role: '编辑', name: m[1].replace(/\*\*/g, '').trim() };
  // Bottom-of-article pattern: "作者 | xxx" without bold markers
  m = clean.match(/^(?:作者|撰文|撰稿)[：:/|｜]\s*(.+)/);
  if (m) return { role: '作者', name: m[1].trim() };
  m = clean.match(/^(?:出品|来源|出处)[：:/|｜]\s*(.+)/);
  if (m) return { role: '来源', name: m[1].trim() };
  m = clean.match(/^(?:编辑|责编)[：:/|｜]\s*(.+)/);
  if (m) return { role: '编辑', name: m[1].trim() };
  return null;
}

function pushUniqueAttribution(attrs, attr) {
  if (!attr || !attr.role || !attr.name) return;
  if (attrs.some(a => a.role === attr.role && a.name === attr.name)) return;
  attrs.push({ k: 'attr', role: attr.role, name: attr.name });
}

// URL 导入的文章级署名固定归一为三行：
//   1. 来源
//   2. 作者
//   3. 编辑
// 同一角色只取开头首次出现的值，避免原文重复署名重复渲染。
function orderArticleAttributions(attrs) {
  const first = role => attrs.find(attr => attr?.role === role);
  return [first('来源'), first('作者'), first('编辑')].filter(Boolean);
}

// 页脚分界词「提示：」默认正则（红线下放 behavior.params.footer_prompt_marker）。
const RE_FOOTER_PROMPT_MARKER = /^提示[：:]$/;
function isFooterPromptText(text) {
  const re = behaviorRegexParam('footer_prompt_marker', RE_FOOTER_PROMPT_MARKER);
  return re.test(String(text || '').trim());
}

function rawBlockText(block) {
  if (!block) return '';
  if (block.text) return String(block.text).trim();
  return (block.runs || [])
    .filter(r => r && (r.type === 'txt' || r.type === 'link'))
    .map(r => r.text || '')
    .join('')
    .trim();
}

// FBIF 标准页脚特征词正则默认值（红线下放 behavior.params.footer_prompt_detection_regex）。
const RE_FOOTER_PROMPT_DETECTION = /本文为.*转载|转载请|FBIF投稿|商务合作|fbifmedia1/i;
function isStandardFooterPrompt(blocks, index) {
  const tail = (blocks || [])
    .slice(index + 1, index + 6)
    .map(rawBlockText)
    .filter(Boolean)
    .join('\n');
  const re = behaviorRegexParam('footer_prompt_detection_regex', RE_FOOTER_PROMPT_DETECTION);
  return re.test(tail);
}

// ---- Markdown Classification ----
async function classifyMd(text) {
  const { author: fmAuthor, title, content } = parseMdFrontmatter(text);
  const rawParas = content.split(/\n\s*\n/).map(p => p.trim()).filter(p => p);

  const elems = [];
  let imgN = 0, expectHeading = false, stopped = false, inRef = false;
  let author = fmAuthor, source = '';

  for (let i = 0; i < rawParas.length && !stopped; i++) {
    const para = rawParas[i].replace(/\s+$/gm, '');
    if (/^#\s/.test(para)) continue;
    // Skip x-reader metadata lines（红线下放 behavior.params.md_metadata_skip_regex）
    if (behaviorRegexParam('md_metadata_skip_regex', RE_MD_METADATA_SKIP).test(para)) continue;
    if (/Author:.*\|.*Published:/i.test(para)) continue;

    // Detect author/source/editor attribution
    const attr = parseAttribution(para);
    if (attr) {
      if (attr.role === '作者' && !author) author = attr.name;
      if (attr.role === '来源') source = attr.name;
      // Render attribution as styled text
      inRef = false;
      elems.push({ k: 'attr', role: attr.role, name: attr.name });
      continue;
    }

    if (/^>\s/.test(para)) {
      const bqText = para.split('\n').map(l => l.replace(/^>\s*/, '')).join(' ').trim();
      if (bqText) { inRef = false; elems.push({ k: 'bq', runs: parseMdRuns(bqText) }); }
      continue;
    }
    if (/^[-*_\s]{3,}$/.test(para.replace(/\s/g, ''))) {
      if (/^\*\s+\*\s+\*/.test(para.trim())) stopped = true;
      continue;
    }

    // Section number (e.g. **01**) followed by bold heading — match BEFORE
    // the ref regex so "**01**" / "**参考文献**" don't get mistaken for body text.
    if (/^\*\*0?\d+\*\*$/.test(para)) { expectHeading = true; continue; }
    if (expectHeading) {
      const hm = para.match(/^\*\*(.+)\*\*$/);
      if (hm) { inRef = false; elems.push({ k: 'h', text: hm[1] }); expectHeading = false; continue; }
      expectHeading = false;
    }
    // Standalone bold line (centered heading) → treat as heading. Runs before
    // the plain-text "参考X" regex so that "**参考文献**" becomes a heading
    // (bold emphasis marker) rather than opening a reference section.
    const boldOnly = para.match(/^\*\*(.+)\*\*$/);
    if (boldOnly && para.length < ruleNumber('md_heading_max_chars')) {
      inRef = false;
      elems.push({ k: 'h', text: boldOnly[1] });
      continue;
    }

    if (/^(参考(来源|文献|资料|链接)|信息来源)/.test(para)) { inRef = true; elems.push({ k: 'refH', text: para.replace(/[：:].*$/, '').trim() }); continue; }
    if (inRef) { elems.push({ k: 'ref', runs: parseMdRuns(para) }); continue; }

    const imgMatch = para.match(/^!\[([^\]]*)\]\(([^)]+)\)\s*$/);
    if (imgMatch) {
      const imgSrc = imgMatch[2].replace(/"/g, '').replace(/</g, '').replace(/>/g, '');
      // Respect WeChat's data-w when it signals a decorative narrow image
      // (e.g. section-number banners at 279px). Without this, small images
      // get stretched to 100% of the content column.
      const dwMatch = imgSrc.match(/[#&]dataW=(\d+)/);
      const decorativeLimit = ruleNumber('decorative_image_max_px');
      const maxPx = dwMatch && Number(dwMatch[1]) > 0 && Number(dwMatch[1]) < decorativeLimit
        ? Number(dwMatch[1]) : 0;
      imgN++;
      inRef = false;
      elems.push({ k: 'img', src: imgSrc, w: '100%', maxPx, gif: looksLikeGifSource(imgSrc) });
      continue;
    }

    elems.push({ k: 'txt', runs: parseMdRuns(para) });
  }

  return { elems, author, source, title, imgN };
}

// 小标题（h）和图注（cap）是纯字符串 elem，不带 runs，因此两条解析路径
// （Markdown 和 URL→blocks）都绕过了 convertRuns 的标点转换，括号一直是半角。
// 在渲染处统一补上，口径与正文完全一致：同一套 auto 语言判定，整段判为英文时
// 不动，中文语境里 (x) 一律转成（x）。
function localizePunct(text) {
  return convertText(String(text || ''));
}

// ---- HTML Rendering (class-based) ----
// opts.isRepost — 转载路径保持微信原色（var(--wx-link-color)）；原创路径
// 给作者行加 wx-ta-author，覆盖为 #396FBC（FBIF 视觉规范）。
function prepareRenderState(elems, author, source, opts) {
  const behavior = getActiveRuleBehavior();
  const context = behaviorContext(author, source);
  const mods = rawActiveModules();
  // 作者规则选择（手选覆盖自动识别）：手选 profile 注入其行为身份（署名图等按作者触发的
  // 规则一并生效）；手选「通用默认」中和身份（识别到 Mote 也不触发）；不改 author 显示名。
  const selection = resolveArticleFooterSelection({
    articleMeta: context.article,
    footerDefaults: mods.article_footer_defaults,
    profileOverride: opts?.articleFooterProfileOverride,
  });
  if (selection.authorNormalizedOverride !== undefined) {
    context.article = { ...context.article, author_normalized: selection.authorNormalizedOverride };
  }
  const artifactDefaults = selection.profile && Object.keys(selection.profile.artifactVisibility).length
    ? selection.profile.artifactVisibility
    : undefined;
  const articleArtifactOverride = mergeArticleArtifactOverrides(
    artifactDefaults,
    opts?.articleArtifactOverride,
  );
  return {
    behavior,
    context,
    elems: applyBehaviorToElems(elems, context, behavior, { articleArtifactOverride }),
  };
}

function renderTopAttributionLines(author, source, opts, context, behavior) {
  if (opts?.suppressTopAttribution) return [];
  const lines = [];
  if (author) {
    const authorCls = opts?.isRepost ? 'wx-ta' : 'wx-ta wx-ta-author';
    lines.push(sec(leafWrapAuthor('<span textstyle="" class="' + authorCls + '">' + esc(attributionText('作者', author, context, behavior)) + '</span>'), !source));
  }
  if (source) {
    lines.push(sec(leafWrapAuthor('<span textstyle="" class="wx-ta">' + esc(attributionText('来源', source, context, behavior)) + '</span>'), true));
  }
  return lines;
}

function renderImageElement(elem, spaceAfter) {
  const src = elem.src || '';
  const isGif = elem.gif || looksLikeGifSource(src);
  const referrerPolicy = src.startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
  const gifAttr = isGif ? ' data-type="gif"' : '';
  const sizeStyle = elem.maxPx
    ? 'max-width: ' + elem.maxPx + 'px; width: 100%; height: auto; display: block; margin: 0 auto; vertical-align: top;'
    : 'width: ' + elem.w + '; max-width: 100%; height: auto; display: block; margin: 0 auto; vertical-align: top;';
  return '<section class="' + sectionClass('wx-pi' + elemExtraClass(elem), spaceAfter) + '">' +
    '<img src="' + escAttr(src) + '"' + gifAttr + referrerPolicy + ' style="' + sizeStyle + '" /></section>';
}

function renderTableElement(elem, spaceAfter) {
  // 表格整体走 inline-style（含外层间距）：css-inline 不碰裸 table，间距也得内联。
  const gapPx = spaceAfter ? (getRuleNumber(getActiveRuleConfig(), 'paragraph_gap') || 20) : 0;
  return '<section style="margin: 0 0 ' + gapPx + 'px; overflow-x: auto;">' +
    renderTable(elem) + '</section>';
}

function renderElement(elem, spaceAfter, context, behavior) {
  switch (elem.k) {
    case 'h':
      return sec(leafWrap('<span textstyle="" class="wx-th">' + esc(localizePunct(elem.text)) + '</span>'), spaceAfter);
    case 'img':
      return renderImageElement(elem, spaceAfter);
    case 'cap':
      return secCenter(leafWrap('<span textstyle="" class="wx-tc">' + esc(localizePunct(elem.text)) + '</span>'), spaceAfter);
    case 'txt':
      return sec(renderListMarker(elem) + renderRuns(elem.runs), spaceAfter);
    case 'attr':
      return sec(leafWrapAuthor('<span textstyle="" class="wx-ta">' +
        esc(attributionText(elem.role, elem.name, context, behavior)) + '</span>'), spaceAfter);
    case 'refH': {
      const heading = elem.text.replace(/[：:]?$/, '：');
      return sec(leafWrap('<span textstyle="" class="wx-tr">' + esc(heading) + '</span>'), false);
    }
    case 'ref':
      return sec(renderRuns(elem.runs, { textCls: 'wx-tr' }), spaceAfter);
    case 'bq':
      return '<section class="' + sectionClass('wx-bq', spaceAfter) + '">' +
        renderRuns(elem.runs, { textCls: 'wx-tr' }) + '</section>';
    case 'table':
      return renderTableElement(elem, spaceAfter);
    default:
      return '';
  }
}

function render(elems, author, source, opts) {
  const prepared = prepareRenderState(elems, author, source, opts);
  const lines = renderTopAttributionLines(author, source, opts, prepared.context, prepared.behavior);

  for (let index = 0; index < prepared.elems.length; index++) {
    const elem = prepared.elems[index];
    const next = index + 1 < prepared.elems.length ? prepared.elems[index + 1] : null;
    const line = renderElement(elem, shouldSpaceAfter(elem, next), prepared.context, prepared.behavior);
    if (line) lines.push(line);
  }

  // 底部区块不再追加到正文末——改由 export default 的 renderBottomFooter() 作为整段页脚渲染，
  // 经 engine footer 链路接管（替代 footer.html；更多文章卡片复制链路在其输出上委托工作）。
  return lines;
}

// ---- Blocks Classification (URL-import path) ----
// Input is already structurally classified by the Go DOM walker, so this
// step is light: apply punctuation conversion to runs, promote attribution
// paragraphs to 'attr' blocks, and track 参考文献/信息来源 sections. No
// visual-heuristic role-guessing — that was the whole point of moving to
// structured blocks. See api/wechat_blocks.go for the upstream contract.
const ATTRIBUTION_BLOCK_KINDS = new Set(['h', 'txt', 'bq', 'li']);

function captureBlockAttribution(state, attr, replaceSource) {
  if (attr.role === '作者' && !state.author) state.author = attr.name;
  if (attr.role === '来源' && (replaceSource || !state.source)) state.source = attr.name;
  pushUniqueAttribution(state.attrs, attr);
  state.inRef = false;
}

function takeLeadingAttribution(state, block, blockIndex) {
  if (state.isClipboardText ||
      blockIndex >= state.leadingAttributionScanLimit ||
      !ATTRIBUTION_BLOCK_KINDS.has(block.k)) return false;

  const attr = parseAttribution(rawBlockText(block));
  if (!attr) return false;
  captureBlockAttribution(state, attr, false);
  return true;
}

function takeStructuralBlock(state, block) {
  switch (block.k) {
    case 'img': {
      const width = Number(block.dataW);
      const maxPx = width > 0 && width < ruleNumber('decorative_image_max_px') ? width : 0;
      const src = typeof block.src === 'string' ? block.src : '';
      state.imgN++;
      state.inRef = false;
      state.elems.push({ k: 'img', src, w: '100%', maxPx, gif: looksLikeGifSource(src) });
      return true;
    }
    case 'cap': {
      state.elems.push({ k: 'cap', text: block.text });
      return true;
    }
    case 'table': {
      state.inRef = false;
      state.elems.push({
        k: 'table',
        rows: (block.rows || []).map(row => (row || []).map(cell => {
          const { runs = [], header = false } = cell;
          return { runs: convertRuns(runs), header: !!header };
        })),
      });
      return true;
    }
    case 'h': {
      const text = block.text.trim();
      const isReference = /^(参考(来源|文献|资料|链接)|信息来源)/.test(text);
      state.inRef = isReference;
      state.elems.push(isReference
        ? { k: 'refH', text: text.replace(/[：:].*$/, '').trim() }
        : { k: 'h', text });
      return true;
    }
    default:
      return false;
  }
}

function blockRunsText(runs) {
  return runs
    .filter(run => run?.type === 'txt' || run?.type === 'link')
    .map(run => run.text || '')
    .join('')
    .trim();
}

function appendRunsBlock(state, block, blockIndex) {
  // txt / bq / li — all carry runs. Apply punctuation pass.
  const runs = convertRuns(block.runs);
  // Include txt and link display text so "作者：<a>张三</a>" still yields "作者：张三".
  const text = blockRunsText(runs);
  if (!state.isClipboardText && state.attrs.length > 0 &&
      isFooterPromptText(text) && isStandardFooterPrompt(state.inputBlocks, blockIndex)) return false;

  if (block.k === 'bq') {
    state.inRef = false;
    state.elems.push({ k: 'bq', runs });
    return true;
  }

  const attr = state.isClipboardText ? null : parseAttribution(text);
  if (attr) {
    captureBlockAttribution(state, attr, true);
    return true;
  }

  if (behaviorRegexParam('ref_section_regex', RE_REF_SECTION).test(text)) {
    state.inRef = true;
    state.elems.push({ k: 'refH', text: text.replace(/[：:]$/, '') });
    return true;
  }

  if (state.inRef) {
    state.elems.push({ k: 'ref', runs, blankBefore: !!block.blankBefore, compact: !!block.compact });
    return true;
  }

  // li and txt render the same; collapsing them keeps the renderer switch simple.
  state.elems.push({ k: 'txt', runs, blankBefore: !!block.blankBefore, compact: !!block.compact });
  return true;
}

function applySourceNameFallback(state, data) {
  if (state.isClipboardText || state.source ||
      !behaviorBooleanParam('url_source_name_fallback_enabled', true)) return;

  const sourceName = String(data.source_name || '').trim();
  if (!sourceName) return;
  state.source = sourceName;
  pushUniqueAttribution(state.attrs, { role: '来源', name: sourceName });
}

function classifyBlocks(data) {
  const input = data || {};
  const state = {
    elems: [],
    attrs: [],
    author: '',
    source: '',
    imgN: 0,
    inRef: false,
    isClipboardText: input.source === 'clipboard-text',
    inputBlocks: input.blocks || [],
    leadingAttributionScanLimit: behaviorNumberParam('url_attribution_scan_limit', 12),
  };

  for (let blockIndex = 0; blockIndex < state.inputBlocks.length; blockIndex++) {
    const block = state.inputBlocks[blockIndex];
    if (!block?.k) continue;

    // 微信原文可能把开头署名判成 h；仅在有界窗口内先识别文章级署名。
    if (takeLeadingAttribution(state, block, blockIndex)) continue;
    if (takeStructuralBlock(state, block)) continue;
    if (!appendRunsBlock(state, block, blockIndex)) break;
  }

  // 正文无显式来源时才用公众号名称 source_name 兜底。
  applySourceNameFallback(state, input);
  return {
    elems: state.elems,
    attributions: orderArticleAttributions(state.attrs),
    author: state.author,
    source: state.source,
    title: input.title || '',
    imgN: state.imgN,
  };
}

export default {
  id: 'fbif',
  name: 'FBIF 公众号排版',
  description: '按 FBIF 排版规范格式化，支持标题/引言/图片说明/参考来源',
  formats: ['.docx', '.doc', '.md', '.txt'],

  processDocx({ paragraphs, imgCache, isRepost, config: _config, articleArtifactOverride, articleFooterProfileOverride }) {
    // config 当前不消费 —— 仅为接口兼容，不破坏公众号现有行为
    const { elems, author, imgN } = classifyDocx(paragraphs, imgCache);
    return {
      lines: render(elems, author, '', { isRepost, articleArtifactOverride, articleFooterProfileOverride }),
      imgN,
      headingN: elems.filter(e => e.k === 'h').length,
      articleMeta: articleMeta(author, ''),
    };
  },
  async processMd(text, { config: _config, ...opts } = {}) {
    // config 当前不消费 —— 仅为接口兼容，不破坏公众号现有行为
    // opts 已含 articleArtifactOverride / articleFooterProfileOverride，透传 render。
    const { elems, author, source, title, imgN } = await classifyMd(text);
    return {
      lines: render(elems, author, source, opts),
      imgN,
      headingN: elems.filter(e => e.k === 'h').length,
      title,
      articleMeta: articleMeta(author, source),
    };
  },
  processBlocks(data, { config: _config, ...opts } = {}) {
    // config 当前不消费 —— 仅为接口兼容，不破坏公众号现有行为
    const { elems, attributions, author, source, title, imgN } = classifyBlocks(data);
    // 2026-09-01 用户裁定：转载署名放正文开头；原创维持标准页脚前的位置。
    const positionedElems = opts.isRepost ? attributions.concat(elems) : elems.concat(attributions);
    return {
      lines: render(positionedElems, author, source, { ...opts, suppressTopAttribution: true }),
      imgN,
      headingN: elems.filter(e => e.k === 'h').length,
      title,
      articleMeta: articleMeta(author, source),
    };
  },

  // 整段底部由统一区块模型渲染（替代 footer.html）。engine.loadBaseFooterHtml 接管时调用：
  // 模块缺失/禁用返回 null → engine fallback 到 footerUrl(footer.html)。
  // 红线 #19：mpBlockRenderers 只在公众号侧 import；engine 通过此回调拿结果、不直接依赖渲染器。
  renderBottomFooter(opts) {
    const mods = rawActiveModules();
    const bb = mods.bottom_blocks;
    if (!bb || typeof bb !== 'object' || bb.enabled === false) return null;
    const scope = (typeof window !== 'undefined' && window._activeRulePack && window._activeRulePack.id) || 'fbif';
    const ov = opts && Object.prototype.hasOwnProperty.call(opts, 'bottomBlocksOverride')
      ? opts.bottomBlocksOverride
      : ((typeof window !== 'undefined' && window._bottomBlocksOverride) || undefined);
    const profileOverride = opts && Object.prototype.hasOwnProperty.call(opts, 'articleFooterProfileOverride')
      ? opts.articleFooterProfileOverride
      : ((typeof window !== 'undefined' && window._articleFooterProfileOverride) || undefined);
    // v2 双预设：来源决定原创/转载（v1 模块忽略 presetId，行为不变）。
    const presetId = (opts && opts.isRepost) ? 'repost' : 'original';
    const prepared = prepareBottomBlocksForArticle({
      module: bb,
      articleMeta: opts && opts.articleMeta,
      footerDefaults: mods.article_footer_defaults,
      contentOverride: ov,
      visibilityOverride: opts && opts.footerVisibilityOverride,
      profileOverride,
    });
    const html = renderBottomBlocksHtml(
      prepared.module,
      prepared.override,
      mpBlockRenderers(mods.asset_library, scope),
      { presetId }
    );
    return {
      html,
      isV2: Number(bb.schema_version) >= 2,
      articleFooterProfile: prepared.articleFooterProfile,
    }; // isV2：engine 据此对 v2 跳过 applyRepostTransform（codex #4）
  },
};
