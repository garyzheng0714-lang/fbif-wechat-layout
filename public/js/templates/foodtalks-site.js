// FoodTalks 网站排版模板 — 输出 inline-style HTML，给 FoodTalks 网站后台编辑器粘贴用
// 输出契约（M2，基于 8 篇真实 foodtalks.cn 文章采样对齐，参见
// docs/superpowers/specs/2026-05-27-foodtalks-site-layout-design.md）：
//   - <h2>/<h3>/<h4> 按原稿层级输出；config.modules.mctoc.enabled 开启时带锚点 ID
//   - <p style="text-align: justify;"> 所有段落两端对齐
//   - 空行：<p style="text-align: justify;"><br></p>（显式标签）
//   - 图片与图注：<figure class="image"><img ... class="mc-img"><figcaption>...</figcaption></figure>
//   - 参考来源：ref-header / ref-item 分支，14px #888888 段落，保留作者原始 [n] 编号（不再自动补）
//   - 链接：a 标签补 target="_blank" rel="noopener"
//   - 作者署名 / 提示行：由 applyFoodTalksBehavior 行为模板从文档中提升识别
//   - 可选 FBIF 公众号原创页脚：config.modules.fbif_footer.enabled 开启时在文末追加

const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter, endsWithEnglishAbbreviation } = engineModule;
// 等价于: import { applyFoodTalksBehavior, FBIF_FOOTER_HTML } from '../rule-behavior-foodtalks.js'
// 用动态 import 是为了配合浏览器端的 cache-busting assetQuery
const behaviorModule = await import('../rule-behavior-foodtalks.js' + assetQuery);
const { applyFoodTalksBehavior, buildFooterHtml, compileSiteRegex, pickSiteTipSourceUrl } = behaviorModule;

// ── 规则读取助手（红线下放：优先服务端 config / config.behavior，缺省/非法降级 const）──
// behOf/behTemplate/behParamStr/behList 共享实现收敛到 rule-behavior-utils.js（架构体检 S6）。
const behaviorUtilsModule = await import('../rule-behavior-utils.js' + assetQuery);
const { behOf, behTemplate, behParamStr, behList, isSectionNumberHeading } = behaviorUtilsModule;
function cfgNum(config, key, fallback) {
  const n = Number(config && config[key]);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}
function cfgColor(config, key, fallback) {
  // 颜色进 style，做 hex 白名单防 XSS；非法/缺省降级 const。
  const v = config && config[key];
  return (typeof v === 'string' && /^#[0-9a-fA-F]{6}$/.test(String(v).trim())) ? String(v).trim() : fallback;
}
function behParamNum(config, key, fallback) {
  const p = behOf(config).params;
  const raw = p && p[key] && p[key].default;
  const n = Number(raw);
  return Number.isFinite(n) ? n : fallback;
}
function behParamBool(config, key, fallback) {
  const p = behOf(config).params;
  const raw = p && p[key] && p[key].default;
  if (typeof raw === 'boolean') return raw;
  if (typeof raw === 'string') {
    if (/^(true|1|yes)$/i.test(raw.trim())) return true;
    if (/^(false|0|no)$/i.test(raw.trim())) return false;
  }
  return fallback;
}
// 标题识别（detectHeadingLevel / isBoldHeading / promoteHeuristicHeadings）抽到无依赖
// 的 heading-detect.js，便于真实单元测试（见 tests/heading-detect.test.mjs）。
const headingModule = await import('../heading-detect.js' + assetQuery);
const { promoteHeuristicHeadings, hasExplicitDocxHeadings, isSecondaryNumberedText } = headingModule;
const interviewModule = await import('../site-interview.js' + assetQuery);
const { markInterviewSpeakerPrefixes } = interviewModule;
const { applySeriesContactLayout } = await import('../site-contact-layout.js' + assetQuery);
const { activeFooterSegments } = await import('../site-footer-active.js' + assetQuery);
const imageWidthModule = await import('../image-width.js' + assetQuery);
const { adaptiveImageWidth } = imageWidthModule;
// DOCX 内容结构分类与公众号共用一套（红线#19：两渲染器不互相 import，都只 import
// 本数据模块）。网站口径保留：consumeAuthorLine/classifyReferences 均 false（见 classifyDocx）。
const docxModelModule = await import('../docx-article-model.js' + assetQuery);
const { classifyDocxArticle } = docxModelModule;
const tableModule = await import('./shared/table-renderer.js' + assetQuery);
const { renderTable: renderSharedTable } = tableModule;
// 底部区块（统一模型）：中性编排器 + 网站渲染器（红线 #19：不 import 公众号渲染器）。
const blockRenderModule = await import('../blocks/block-render.js' + assetQuery);
const { renderBottomBlocksHtml } = blockRenderModule;
const blockMergeModule = await import('../blocks/block-merge.js' + assetQuery);
const { pickActivePreset } = blockMergeModule;
const siteBlocksModule = await import('./site-blocks.js' + assetQuery);
const { siteBlockRenderers } = siteBlocksModule;

// ---- 图片注释智能识别 ----

// 图片注释关键词（参考 /tmp/foodtalks-research/FoodtalksArticleLayout/layout-tool.html:1061-1074）
const IMAGE_CAPTION_KEYWORDS = [
  '图片来源', '资料来源', '数据来源', '来源',
  '制图', '截图', '图注', '注：',
  'Source', 'Photo', 'Image', 'Credit',
];

// ---- 标题启发式识别 ----
// detectHeadingLevel / isBoldHeading / promoteHeuristicHeadings 已抽到 heading-detect.js
// （顶部 import）。这里只保留图注识别等模板本地逻辑。

function hasImageCaptionKeyword(text, keywords) {
  if (!text) return false;
  const kws = (Array.isArray(keywords) && keywords.length) ? keywords : IMAGE_CAPTION_KEYWORDS;
  const lower = String(text).toLowerCase();
  for (const kw of kws) {
    if (text.indexOf(kw) >= 0) return true;
    if (kw.length <= 8 && lower.indexOf(kw.toLowerCase()) >= 0) return true;
  }
  return false;
}

// 图注前缀正则默认值（红线下放 behavior.params.image_caption_prefix_regex 的离线兜底）。
const IMAGE_CAPTION_PREFIX_RE = /^(图|图注|图片|上图|下图|表|资料来源|数据来源|图片来源|来源|注|Source|Photo|Image|Credit)\s*[：:\s\d]/i;

// 从 config 解析图注识别规则（关键词列表 + 前缀正则），缺省/非法降级到代码内 const。
function buildCaptionOpts(config) {
  let prefixRe = IMAGE_CAPTION_PREFIX_RE;
  const src = behParamStr(config, 'image_caption_prefix_regex', '');
  if (src) { try { prefixRe = compileSiteRegex(src); } catch (e) { /* 非法降级 */ } }
  return {
    keywords: behList(config, 'image_caption_keywords', IMAGE_CAPTION_KEYWORDS),
    prefixRe,
  };
}

// 从 config 解析标题识别规则（关键词列表 + 图注排除正则），传给 heading-detect.js。
// 缺省项为 null/undefined，heading-detect 内部各自降级到其代码内 const。
function buildHeadingOpts(config) {
  let captionLeadRe;
  const src = behParamStr(config, 'caption_lead_regex', '');
  if (src) { try { captionLeadRe = compileSiteRegex(src); } catch (e) { /* 非法降级 */ } }
  return {
    titleKeywords: behList(config, 'title_keywords', undefined),
    captionLeadRe,
    // 公众号→网站规范：一级=标题1（一、 / 章节句），二级=标题2（1、2、3），不进目录。
    arabicAsSubheading: true,
  };
}

function stripHtml(text) {
  return String(text || '').replace(/<[^>]+>/g, '');
}

function isCjk(ch) {
  if (!ch) return false;
  const c = ch.charCodeAt(0);
  return (c >= 0x4e00 && c <= 0x9fff) ||
    (c >= 0x3400 && c <= 0x4dbf) ||
    (c >= 0x3000 && c <= 0x303f) ||
    (c >= 0xff00 && c <= 0xffef);
}
function isAsciiLetter(ch) { return !!ch && /[a-zA-Z]/.test(ch); }
function isDigit(ch) { return !!ch && /[0-9]/.test(ch); }

// 行首阿拉伯序号（`1.`、`12.`）是列表/标题标记，不是句末，保留半角点
// （公众号原文 `1. 金骏眉` 不得变 `1。 金骏眉`；与 punctuation.js 规则 2c 同口径）。
// 只认行首 1–3 位数字，句中 `达到100.下一步`、四位年份 `2024.` 仍转换。
// leading 是同段此前各 run 的文本，序号可能单独成 run（如加粗）。
const LEADING_ORDINAL_TAIL_RE = /(?:^|\n)[ \t\u3000\u00a0]*\d{1,3}$/;

function normalizeChinesePunctuation(text, leading = '') {
  const chars = String(text || '').split('');
  const findTextChar = (i, step) => {
    let j = i + step;
    while (j >= 0 && j < chars.length && /\s/.test(chars[j])) j += step;
    return j >= 0 && j < chars.length ? chars[j] : '';
  };
  const touchesChinese = (i) => {
    const prev = findTextChar(i, -1);
    const next = findTextChar(i, +1);
    return isCjk(prev) || isCjk(next);
  };
  return chars.map((ch, i) => {
    const prev = findTextChar(i, -1);
    const next = findTextChar(i, +1);
    // 词中撇号（`tea’stone`、`Peet’s`）统一成英文 `'`：中文字体里 U+2019 是全角，
    // 读起来像中文引号（2026-10-15 用户反馈；与 punctuation.js 规则 3 同口径）。
    if ((ch === '’' || ch === '‘') && /[A-Za-z0-9_]/.test(prev) && /[A-Za-z0-9_]/.test(next)) {
      return "'";
    }
    if (!touchesChinese(i)) return ch;
    if (ch === ',') return '，';
    if (ch === ':') return '：';
    if (ch === ';') return '；';
    if (ch === '?') return '？';
    if (ch === '!') return '！';
    if (ch === '(') return '（';
    if (ch === ')') return '）';
    if (ch === '.') {
      if ((isDigit(prev) && isDigit(next)) || (isAsciiLetter(prev) && isAsciiLetter(next))) {
        return ch;
      }
      const before = leading + chars.slice(0, i).join('');
      if (!isDigit(chars[i + 1]) && LEADING_ORDINAL_TAIL_RE.test(before)) {
        return ch;
      }
      // 英文缩写的点（`Salt Co.和`、`U.S.市场`）不是句末（2026-10-23 #289；与 punctuation.js 规则 2d 同表）。
      if (endsWithEnglishAbbreviation(before)) return ch;
      return '。';
    }
    return ch;
  }).join('');
}

function isImageCaption(text, capOpts) {
  const opts = capOpts || {};
  const prefixRe = opts.prefixRe || IMAGE_CAPTION_PREFIX_RE;
  const plain = stripHtml(text || '').trim();
  if (!plain || plain.length > 120) return false;
  if (hasImageCaptionKeyword(plain, opts.keywords)) return true;
  if (prefixRe.test(plain)) {
    return true;
  }
  if (/^[▲△▵▽▼]/.test(plain)) return true;
  const sentenceEnd = (plain.match(/[。！？!?]/g) || []).length;
  const commaCount = (plain.match(/[，,；;]/g) || []).length;
  return plain.length <= 45 && sentenceEnd === 0 && commaCount <= 1;
}

// ---- 输出基本元 ----

const BLANK_LINE = '<p style="text-align: justify;"><br></p>';
// 图注后的空行与图注同为 center 对齐（基石规律：图注块后空行是 text-align:center）
const CENTER_BLANK_LINE = '<p style="text-align: center;"><br></p>';

function isSeriesSiteConfig(config = {}) {
  return behParamStr(config, 'site_variant', '') === 'ft-series';
}

function chineseSectionNumber(n) {
  return ['一', '二', '三', '四', '五', '六', '七', '八', '九', '十'][n - 1] || String(n);
}

function stripSectionNumber(text) {
  return String(text || '').replace(/^\s*(?:[一二三四五六七八九十]+|[0-9]+)\s*[、.．]\s*/, '').trim();
}

function sectionNumberPrefix(text) {
  const m = String(text || '').trim().match(/^((?:[一二三四五六七八九十]+|[0-9]+))\s*[、.．]\s*/);
  return m ? m[1] + '、' : '';
}

function hasSectionNumber(text) {
  return stripSectionNumber(text) !== String(text || '').trim();
}

function normalizeSectionTitleKey(text) {
  return stripSectionNumber(stripHtml(text || '')).replace(/\s+/g, '').trim();
}

function collectSeriesTocSections(elems) {
  const sections = new Map();
  let inToc = false;
  for (const e of elems || []) {
    if (!e) continue;
    const text = e.runs ? runsToPlain(e.runs) : (e.text || '');
    const key = normalizeSectionTitleKey(text);
    if (!inToc && e.k === 'h' && key === '目录') {
      inToc = true;
      continue;
    }
    if (!inToc) continue;
    if (e.blankBefore && sections.size) break;
    if (e.k !== 'h' && e.k !== 'p') {
      if (sections.size) break;
      continue;
    }
    const prefix = sectionNumberPrefix(text);
    if (!prefix) {
      if (sections.size) break;
      continue;
    }
    sections.set(key, prefix);
  }
  return sections;
}

// 目录章节查询（精确 key + 前缀宽松匹配）。公众号源稿目录条目与正文标题常有微小措辞
// 差异（上海艾录文实测：目录「二、什么是上海艾录微发泡片材？」vs 正文标题「什么是上海
// 艾录微发泡片材方案？」），精确匹配失败会导致分节图不删、标题编号缺失、甚至标题被
// img+h 分支误转图注（2026-06-11 飞书反馈批次定位）。宽松规则：去尾部标点后一方是
// 另一方前缀且 ≥6 字（startsWith 单向包含，避免短词误撞）。
function lookupTocPrefix(sections, key) {
  if (!key || !sections || !sections.size) return '';
  if (sections.has(key)) return sections.get(key);
  const norm = k => String(k || '').replace(/[？?！!。.：:]+$/, '');
  const nk = norm(key);
  if (nk.length < 6) return '';
  for (const [k, v] of sections) {
    const a = norm(k);
    if (a.length >= 6 && (a.startsWith(nk) || nk.startsWith(a))) return v;
  }
  return '';
}

function prefixHeadingRuns(runs, prefix) {
  const out = (runs || []).map(r => ({ ...r }));
  const idx = out.findIndex(r => r && r.type === 'txt' && String(r.text || '').trim());
  if (idx < 0) return [{ type: 'txt', text: prefix }];
  out[idx] = { ...out[idx], text: prefix + String(out[idx].text || '').replace(/^\s+/, '') };
  return out;
}

function isWideSectionDivider(e) {
  if (!e || e.k !== 'img') return false;
  const w = Number(e.width || 0);
  const h = Number(e.height || 0);
  if (!w || !h) return false;
  return w >= 900 && h / w > 0 && h / w <= 0.22;
}

function isImageTitleCaption(text) {
  const plain = stripHtml(text || '').trim();
  if (!plain || plain.length > 42) return false;
  if (hasSectionNumber(plain)) return false;
  if (/^[（(]?\s*(?:[一二三四五六七八九十]+|[0-9]+)\s*[)）、.．]/.test(plain)) return false;
  return true;
}

// FoodTalks 网站「什么是系列」的公众号源稿会用窄长图片表示 01/02/03... 分节号，
// 并把部分图片说明抓成 h。网站成品口径是文字编号 h2 + 图注，不保留这些装饰编号图。
function normalizeWhatIsSeriesElems(elems, config = {}) {
  if (!isSeriesSiteConfig(config)) return elems;
  const tocSections = collectSeriesTocSections(elems);
  const hasTocSections = tocSections.size > 0;
  const out = [];
  let sectionNo = 0;
  let inToc = false;
  let tocItemCount = 0;
  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    const next = elems[i + 1];
    const text = e && e.runs ? runsToPlain(e.runs) : (e && e.text) || '';
    const key = normalizeSectionTitleKey(text);
    if (!inToc && e && e.k === 'h' && key === '目录') {
      inToc = true;
      tocItemCount = 0;
      out.push(e);
      continue;
    }
    if (inToc) {
      if (e && e.blankBefore && tocItemCount > 0) {
        inToc = false;
      } else {
        const prefix = sectionNumberPrefix(text);
        if ((e.k === 'h' || e.k === 'p') && prefix) {
          tocItemCount += 1;
          // 目录条目保持普通段落（62303 成品：目录区仅「目录」二字是标题，条目是 16px
          // 正文文本）。标记交给 markTocEntries 统一打（飞书反馈问题十，2026-06-11 推翻
          // 旧「目录条目升级 h2」行为）。
          out.push(e);
          continue;
        }
        if (tocItemCount > 0) inToc = false;
      }
    }
    const nextText = next && next.k === 'h' ? runsToPlain(next.runs) : '';
    const tocPrefix = nextText ? lookupTocPrefix(tocSections, normalizeSectionTitleKey(nextText)) : '';
    const isSectionDivider = hasTocSections ? !!tocPrefix : isWideSectionDivider(e);
    if (isSectionDivider && next && next.k === 'h') {
      sectionNo += 1;
      const prefix = hasSectionNumber(nextText) ? '' : (tocPrefix || chineseSectionNumber(sectionNo) + '、');
      out.push({ ...next, runs: prefix ? prefixHeadingRuns(next.runs, prefix) : next.runs });
      i += 1;
      continue;
    }
    if (e && e.k === 'img' && next && next.k === 'h' && isImageTitleCaption(runsToPlain(next.runs))) {
      out.push(e);
      out.push({ k: 'cap', runs: next.runs });
      i += 1;
      continue;
    }
    out.push(e);
  }
  return out;
}

// 目录区标记：「目录」标题后的连续编号条目（「一、…」「六、参考来源」）是导航文本，
// 不是标题也不是参考来源段头（62303 成品：目录条目为 16px 普通文本、条目间无空行）。
// 打标记后：promoteHeuristicHeadings 跳过（skipHeadingPromote）、behavior 阶段 3 跳过
// （tocEntry，避免「六、参考来源」被剥编号后误判 ref-header）、render 紧凑排列。
// 飞书反馈问题十/十六（2026-06-11）。
function markTocEntries(elems) {
  const out = (elems || []).slice();
  let tocIdx = -1;
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (!e || (e.k !== 'h' && e.k !== 'p')) continue;
    const text = e.runs ? runsToPlain(e.runs) : (e.text || '');
    if (text.trim() === '目录') { tocIdx = i; break; }
  }
  if (tocIdx < 0) return out;
  for (let i = tocIdx + 1; i < out.length; i++) {
    const e = out[i];
    // 仅普通段落可为目录条目；遇 h（如 series 归一化已升级的正文编号标题）/img 即正文开始
    if (!e || e.k !== 'p') break;
    if (e.blankBefore) break;
    const text = (e.runs ? runsToPlain(e.runs) : (e.text || '')).trim();
    if (!text) continue; // 目录条目之间的空段不结束目录区
    if (!sectionNumberPrefix(text)) break;
    out[i] = { ...e, tocEntry: true, skipHeadingPromote: true };
  }
  return out;
}

function renderRuns(runs, config = {}) {
  let leading = '';
  return (runs || []).filter(r => r.type !== 'img').map(r => {
    const before = leading;
    leading += r.text || '';
    if (r.type === 'txt') {
      let s = esc(normalizeChinesePunctuation(r.text, before));
      if (r.bold || r.interviewSpeaker) s = '<strong>' + s + '</strong>';
      // 排版规范 (6)：提问方 FoodTalks 蓝并加粗，回答方默认颜色并加粗
      //（2026-08-17 由「书名号媒体名上蓝」改为「提问方上蓝」；'publication' 兼容旧值）。
      if (r.interviewSpeaker === 'interviewer' || r.interviewSpeaker === 'publication') {
        const color = cfgColor(config, 'tip_title_color', '#01396a');
        s = `<span style="color: ${color};">${s}</span>`;
      }
      if (r.italic) s = '<em>' + s + '</em>';
      return s;
    }
    if (r.type === 'link') {
      const h = r.href || '';
      if (!/^https?:\/\//i.test(h)) return esc(r.text);
      return '<a href="' + escAttr(h) + '" target="_blank" rel="noopener">' + esc(r.text) + '</a>';
    }
    return '';
  }).join('');
}

function genMctocId() {
  return 'mctoc_' + Math.random().toString(36).slice(2, 14);
}

// 次级标题编号括号半角化：「（一）包装减量…」→「(一)包装减量…」（62303 成品 h3 实测
// 全部为英文括号且括号后无空格；飞书反馈问题十-2）。仅处理行首编号括号，正文括号不动。
function normalizeSubheadingParens(text) {
  return String(text || '').replace(/^[（(]\s*([一二三四五六七八九十]+|[0-9]+)\s*[)）]\s*/, '($1)');
}

// 公众号→网站规范（飞书《FoodTalks 网站排版规范》step2 修改标题格式）：
//   一级 = 标题1：中文序号「一、二、三」或无序号章节句 → h2，进目录
//   二级 = 标题2：「1、2、3」「a、b」或「（一）（二）」→ h3，不进目录、段前段后不空行
//   （前面没有上级章节标题时整组提成一级，见 heading-detect.js liftOrphanSubheadings）
function resolveSiteHeadingLevel(text, explicitLevel) {
  const n = Number(explicitLevel);
  if (n >= 4) return 4;
  if (n === 3 || isSecondaryNumberedText(text)) return 3;
  return 2;
}

function renderHeading(level, text, config = {}) {
  // 基石 news/62285 的标题是 <h2 style="text-align: justify;">…</h2>；id="mctoc_xxx" 是
  // FoodTalks 后台生成目录时自动注入的锚点，默认不输出（用户 2026-05-30 确认），仅在
  // mctoc 模块显式开启时补 id。
  const mctocEnabled = !!(config.modules && config.modules.mctoc && config.modules.mctoc.enabled);
  const idAttr = mctocEnabled ? ` id="${genMctocId()}"` : '';
  const tag = level === 4 ? 'h4' : (level === 3 ? 'h3' : 'h2');
  const t = level >= 3 ? normalizeSubheadingParens(text) : text;
  return `<${tag}${idAttr} style="text-align: justify;">${esc(t)}</${tag}>`;
}

function renderParagraph(runs, elem = {}, config = {}) {
  const inner = renderRuns(runs, config);
  if (!inner) return BLANK_LINE;
  if (elem.center) {
    if (elem.contactNote) {
      const noteColor = cfgColor(config, 'footer_note_color', (config && config.muted_color) || '#7f7f7f');
      const noteFs = cfgNum(config, 'footer_note_font_size', 14);
      return `<p style="text-align: center;"><span style="font-size: ${noteFs}px; color: ${noteColor};">${inner}</span></p>`;
    }
    return `<p style="text-align: center;">${inner}</p>`;
  }
  return `<p style="text-align: justify;">${inner}</p>`;
}

// adaptiveImageWidth（横图600/长图400/普通竖图高375且宽不低于300）已抽到
// image-width.js（顶部 import），可独立单测。

function imageWidthOptions(config = {}) {
  const imgCfg = (config && config.image) || {};
  return {
    portrait: Number(imgCfg.portrait_px) || 375,
    portraitMin: Number(imgCfg.portrait_min_px) || 300,
    landscape: Number(imgCfg.landscape_px) || 600,
    qr: Number(imgCfg.qr_px) || 150,
    qrMaxNatural: Number(imgCfg.qr_max_natural_px) || 300,
    long: Number(imgCfg.long_px) || 400,
    longMinRatio: Number(imgCfg.long_min_ratio) || 1.7,
  };
}

function parsePercent(value) {
  const m = String(value || '').trim().match(/^(\d+(?:\.\d+)?)%$/);
  return m ? Number(m[1]) : 0;
}

function renderedImageWidth(naturalWidth, naturalHeight, config = {}, elem = {}) {
  // 图宽规则（红线下放到 config.image；当前缺省为 portrait 375、portraitMin 300、
  // landscape 600、qr 150、qrMaxNatural 300、long 400、longMinRatio 1.7）。
  const opts = imageWidthOptions(config);
  if (isSeriesSiteConfig(config)) {
    const forced = Number(elem && (elem.renderWidth || elem.widthOverride));
    if (Number.isFinite(forced) && forced > 0) return Math.round(forced);
  }
  const w = Number(naturalWidth || 0);
  const h = Number(naturalHeight || 0);
  if (w && h) return adaptiveImageWidth(w, h, opts);

  // DOCX 本地上传没有原图尺寸，但 parser 已有显示宽高比 aspect。该路径只在系列行为
  // 显式开启时使用，避免改变 ft-default/公众号既有输出。
  if (isSeriesSiteConfig(config) && behParamBool(config, 'docx_use_image_aspect_width', false)) {
    const aspect = Number(elem && elem.aspect);
    if (aspect > 0) {
      const displayPct = parsePercent(elem.w);
      const qrMaxDisplayPct = behParamNum(config, 'docx_qr_max_display_percent', 35);
      if (displayPct > 0 && displayPct <= qrMaxDisplayPct && aspect >= 0.85 && aspect <= 1.18) {
        return opts.qr;
      }
      // aspect = 宽/高：长图（高/宽 ≥ longMinRatio）与 adaptiveImageWidth 同口径，固定可读宽度；
      // 竖图同样吃 portraitMin 下限
      if (aspect > 0 && 1 / aspect >= opts.longMinRatio) return opts.long;
      if (aspect <= 1) return Math.max(Math.round(opts.portrait * aspect), opts.portraitMin);
      return opts.landscape;
    }
  }
  return adaptiveImageWidth(w, h, opts);
}

function renderImage(src, alt, naturalWidth, naturalHeight, config = {}, elem = {}, captionHTML = '') {
  const safeSrc = escAttr(src || '');
  const safeAlt = escAttr(alt || '');
  const width = renderedImageWidth(naturalWidth, naturalHeight, config, elem);
  // mmbiz 等公众号图带防盗链：浏览器若发 Referer 头会被换成"未经允许不可引用"
  // 占位图。referrerpolicy="no-referrer" 让加载图片时不发 Referer，绕过防盗链。
  // 与公众号渲染（fbif.js）保持一致。
  const rpol = (src || '').startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
  // 内联 max-width:100% 是给外部 CMS / 邮件客户端用的兜底——本工具的 .mc-img 仅
  // 在预览页 CSS 中生效，复制粘贴到不带样式的容器时大图会溢出。
  return (
    `<figure class="image">` +
    `<img style="display: block; margin-left: auto; margin-right: auto; max-width: 100%; height: auto;" ` +
    `src="${safeSrc}" alt="${safeAlt}" width="${width}" class="mc-img"${rpol}>` +
    captionHTML +
    `</figure>`
  );
}

// 视频块（成品口径，安姆科那篇实测）：<video src=mp4 poster=jpg width:100% max-height:480px
// border-radius:8px controls>。视频后紧跟的来源说明走 cap 分支（12px muted 居中，同图注）。
function renderVideo(src, poster) {
  const safeSrc = escAttr(src || '');
  if (!safeSrc) return BLANK_LINE;
  const posterAttr = poster ? ` poster="${escAttr(poster)}"` : '';
  const rpol = String(src || '').startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
  return (
    `<p><video src="${safeSrc}"${posterAttr}${rpol} ` +
    `style="width: 100%; max-height: 480px; border-radius: 8px; outline: none;" ` +
    `controls="controls" x5-video-player-type="h5-page"></video></p>`
  );
}

function renderImageCaption(text, config = {}) {
  const color = (config && config.muted_color) || '#888888';
  const fs = cfgNum(config, 'caption_font_size', 12);
  return (
    `<p style="text-align: center;">` +
    `<span style="font-size: ${fs}px; color: ${color};">${esc(text)}</span>` +
    `</p>`
  );
}

function renderFigureCaption(captionElems, config = {}) {
  const captions = (captionElems || []).map(e => renderImageCaption(runsToPlain(e.runs), config)).join('');
  return captions ? `<figcaption>${captions}</figcaption>` : '';
}

// ---- 表格渲染（统一表格样式，与公众号物理隔离：各写各的 renderTable，共享中性表格结构）----
// 网站渲染器本就全 inline-style（粘进 FoodTalks 后台编辑器），表格同样把 border/底色/字号/
// 内距内联到每个 td。颜色/字号/边框/内距读扁平 config 键（cfgColor/cfgNum，红线下放规则后台
// 可配），缺省降级到与服务端 defaultRuleConfig 逐字一致的 const（与公众号同一套默认值=统一）。
function tableRunRenderers(config = {}) {
  return {
    text: r => renderRuns([r], config),
    link: r => renderRuns([r], config),
    image(r) {
    if (r.type === 'img') {
      if (!r.src) return '';
      const rpol = r.src.startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
      // 表格内图片按 Word 所见显示宽度(wpx)渲染:原图像素不一时 Word 里统一设置的大小
      // 才是编辑意图;width 属性 + style 双写(真值审计:CMS 剥 td 样式但保留 img 属性与 style)。
      const wpx = Math.round(Number(r.wpx));
      if (Number.isFinite(wpx) && wpx > 0) {
        return `<img width="${wpx}" style="display: block; margin: 0 auto; width: ${wpx}px; max-width: 100%; height: auto;" src="` +
          escAttr(r.src) + '"' + rpol + '>';
      }
      return '<img style="display: block; margin: 0 auto; max-width: 100%; height: auto;" src="' +
        escAttr(r.src) + '"' + rpol + '>';
    }
      return '';
    },
  };
}
function renderTable(elem, config = {}) {
  const fs = cfgNum(config, 'table_font_size', 15);
  const padRaw = Number(config && config.table_cell_padding);
  const pad = Number.isFinite(padRaw) && padRaw >= 0 ? padRaw : 8;
  return renderSharedTable(elem, {
    headerBg: cfgColor(config, 'table_header_bg', '#01396a'),
    headerColor: cfgColor(config, 'table_header_text_color', '#ffffff'),
    borderColor: cfgColor(config, 'table_border_color', '#e5e7eb'),
    fontSize: fs,
    padding: pad,
    headerBold: !(config && config.table_header_bold === false),
    outerBorder: true,
    cmsSurvival: true,
    renderers: tableRunRenderers(config),
  });
}

// 把图片后 3 段内最多 2 段"看起来像图注"的普通段提升为图注块
function promoteImageCaptions(elems, capOpts) {
  const opts = capOpts || {};
  const out = elems.slice();
  for (let i = 0; i < out.length; i++) {
    if (out[i].k !== 'img') continue;
    let promoted = 0;
    let scanned = 0;
    for (let j = i + 1; j < out.length && scanned < 3 && promoted < 2; j++) {
      const e = out[j];
      if (e.k === 'img' || e.k === 'h' || e.k === 'ref-header' || e.k === 'ref-item') break;
      if (e.k === 'cap') {
        promoted += 1;
        scanned += 1;
        continue;
      }
      if (e.k !== 'p') continue;
      scanned += 1;
      const plain = (e.runs || []).filter(r => r.type === 'txt').map(r => r.text).join('').trim();
      const isFirst = promoted === 0;
      let promote = false;
      if (isFirst) {
        promote = isImageCaption(plain, opts);
      } else {
        promote = hasImageCaptionKeyword(plain, opts.keywords);
      }
      if (promote) {
        out[j] = { k: 'cap', runs: e.runs };
        promoted += 1;
      } else {
        if (isFirst) break;
      }
    }
  }
  return out;
}

function cloneElem(e) {
  if (!e) return e;
  return {
    ...e,
    runs: Array.isArray(e.runs) ? e.runs.map(r => ({ ...r })) : e.runs,
  };
}

function markSeriesAboutLogoImages(elems, config = {}) {
  if (!isSeriesSiteConfig(config)) return elems;
  const width = behParamNum(config, 'about_logo_width_px', 0);
  if (!(width > 0)) return elems;

  let aboutRe = /^关于/;
  let logoRe = /logo|标志|商标/i;
  const aboutSrc = behParamStr(config, 'about_section_regex', '');
  const logoSrc = behParamStr(config, 'about_logo_caption_regex', '');
  if (aboutSrc) { try { aboutRe = compileSiteRegex(aboutSrc); } catch (e) { /* 非法降级 */ } }
  if (logoSrc) { try { logoRe = compileSiteRegex(logoSrc); } catch (e) { /* 非法降级 */ } }

  const out = (elems || []).map(cloneElem);
  let inAbout = false;
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (!e) continue;
    if (e.k === 'h') {
      const title = stripSectionNumber(runsToPlain(e.runs)).trim();
      aboutRe.lastIndex = 0;
      inAbout = aboutRe.test(title);
      continue;
    }
    if (!inAbout || e.k !== 'img') continue;
    const next = out[i + 1];
    const captionText = next && (next.k === 'cap' || next.k === 'p' || next.k === 'h')
      ? runsToPlain(next.runs || [])
      : '';
    logoRe.lastIndex = 0;
    if (logoRe.test(captionText)) out[i] = { ...e, renderWidth: width };
  }
  return out;
}

// ---- 渲染主入口：把分类后的 elems 拼装成 HTML 行数组 ----

function render(elems, config = {}) {
  const lines = [];
  let prevWasHeading = false;
  let prevWasStandaloneLink = false;
  // 正文末元素是否为 tip 提示声明——决定底部页脚前是否插分隔空行（红线#20：提示声明
  // 与商务合作行同属一组，应连续无空行）。tip 总被 behavior 置于 elems 末尾。
  let endedWithTip = false;
  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    endedWithTip = false;
    if (e.k === 'h') {
      const headingText = runsToPlain(e.runs);
      // 一级标题前 1 个空行；二级标题（标题2：1、2、3 / (一)…）紧贴上文
      // （规范：段前、段后均不空行；62303 后台源码 h3 前无空行）。
      const lv = e.topLevel ? 2 : resolveSiteHeadingLevel(headingText, e.level);
      const prev = elems[i - 1];
      const last = lines[lines.length - 1];
      if (isSeriesSiteConfig(config) && prev && (prev.k === 'img' || prev.k === 'video' || prev.k === 'cap') &&
          (last === BLANK_LINE || last === CENTER_BLANK_LINE)) {
        lines.pop();
      } else if (lv >= 3) {
        // h3/h4 紧贴：吃掉上一块刚补的尾空行（含图注块尾的 center 空行——排版规范：
        // 二级标题段前不空行，上一行为图片来源仍不空行），对齐 FoodTalks 次级标题结构
        if (lines.length > 0 && (last === BLANK_LINE || last === CENTER_BLANK_LINE)) lines.pop();
      } else if (!prevWasHeading && lines.length > 0 && last !== BLANK_LINE && last !== CENTER_BLANK_LINE) {
        // h2 段前恰好 1 个空行：图注块尾已压 center 空行时视同已有空行，不再叠加
        // （2026-08-17 运营反馈：一级标题前有图+图注时会多出一个空行）。
        lines.push(BLANK_LINE);
      }
      lines.push(renderHeading(lv, headingText, config));
      prevWasHeading = true;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'coop-line') {
      // 商务合作导联行（62139/62303 成品逐元素核对）：
      // <em><span 14px><span #7f7f7f>文本</span><a …>链接</a></span></em>
      const coopFs = cfgNum(config, 'footer_note_font_size', 14);
      const coopColor = cfgColor(config, 'footer_note_color', (config && config.muted_color) || '#7f7f7f');
      const inner = (e.runs || []).filter(r => r.type !== 'img').map(r => {
        if (r.type === 'link' && /^https?:\/\//i.test(r.href || '')) {
          return '<a href="' + escAttr(r.href) + '" target="_blank" rel="noopener">' + esc(r.text) + '</a>';
        }
        return `<span style="color: ${coopColor};">${esc(normalizeChinesePunctuation(r.text || ''))}</span>`;
      }).join('');
      lines.push(`<p><em><span style="font-size: ${coopFs}px;">${inner}</span></em></p>`);
      lines.push(BLANK_LINE);
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'author') {
      // 嵌套顺序对齐基石：<em><span style="font-size:14px; color:..."> 文本 </span></em>
      const authorColor = (config && config.muted_color) || '#888888';
      const authorFs = cfgNum(config, 'author_font_size', 14);
      lines.push(
        '<p style="text-align: justify;"><em>' +
        `<span style="font-size: ${authorFs}px; color: ${authorColor};">` +
        esc(e.text) +
        '</span></em></p>'
      );
      lines.push(BLANK_LINE);
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'footer-note') {
      const noteColor = cfgColor(config, 'footer_note_color', (config && config.muted_color) || '#7f7f7f');
      const noteFs = cfgNum(config, 'footer_note_font_size', 14);
      const parts = (Array.isArray(e.lines) ? e.lines : [e.text]).filter(Boolean).map(t =>
        `<em><span style="font-size: ${noteFs}px; color: ${noteColor};">${esc(t)}</span></em>`
      );
      if (parts.length) {
        // 62303 成品口径：组前 1 空行（[4] 末条 → 空行 → 本文未标注…），组内连续行
        // 之间无空行（本文未标注/本文作者两行紧贴），组尾 1 空行。
        const prevIsNote = i > 0 && elems[i - 1] && elems[i - 1].k === 'footer-note';
        const last = lines[lines.length - 1];
        if (prevIsNote && last === BLANK_LINE) lines.pop();
        else if (!prevIsNote && lines.length > 0 && last !== BLANK_LINE && last !== CENTER_BLANK_LINE) {
          lines.push(BLANK_LINE);
        }
        lines.push(`<p>${parts.join('<br>')}</p>`);
        lines.push(BLANK_LINE);
      }
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'tip') {
      // 提示声明受页脚库勾选 active_segments.tip 控制：仅当用户显式取消勾选（=0）时不输出。
      // 默认（未设 / 未配页脚）仍输出，保证转载版权声明默认在（基石所有文章都有）。
      const ftSeg = activeFooterSegments(config.modules && config.modules.fbif_footer);
      if (ftSeg && Object.prototype.hasOwnProperty.call(ftSeg, 'tip') && !ftSeg.tip) {
        prevWasHeading = false;
        prevWasStandaloneLink = false;
        continue;
      }
      // 提示段前补 1 个空行（基石：参考来源末项 / 正文 与「提示：」之间有空行）
      if (lines.length > 0 && lines[lines.length - 1] !== BLANK_LINE) {
        lines.push(BLANK_LINE);
      }
      // 两段式：第一段 "提示：" 独占一行（蓝色加粗斜体），第二段正文（灰色斜体）。
      // 颜色/字号/标题文案均红线下放（config.tip_title_color/tip_font_size/tip_body_color
      // + behavior.templates.tip_title_default），缺省降级 #01396a / 14 / #7f7f7f / 提示：。
      const head = e.title || behTemplate(config, 'tip_title_default', '提示：');
      const tipTitleColor = cfgColor(config, 'tip_title_color', '#01396a');
      const tipBodyColor = cfgColor(config, 'tip_body_color', '#7f7f7f');
      const tipFs = cfgNum(config, 'tip_font_size', 14);
      lines.push(
        '<p style="text-align: justify;">' +
        `<strong><span style="color: ${tipTitleColor};"><em><span style="font-size: ${tipFs}px;">` +
        esc(head) +
        '</span></em></span></strong></p>'
      );
      // 第二段：正文（灰色斜体，保留链接）
      const restRuns = e.rest_runs || e.runs || [];
      const restInner = renderRuns(restRuns, config);
      if (restInner) {
        lines.push(
          `<p style="text-align: justify;"><em><span style="font-size: ${tipFs}px; color: ${tipBodyColor};">` +
          restInner +
          '</span></em></p>'
        );
      }
      endedWithTip = true;
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'img') {
      const captions = [];
      while (elems[i + 1] && elems[i + 1].k === 'cap') {
        captions.push(elems[i + 1]);
        i += 1;
      }
      lines.push(renderImage(
        e.src,
        e.alt,
        e.width,
        e.height,
        config,
        e,
        renderFigureCaption(captions, config),
      ));
      // 图与图注现在是一个原生 figure；块后保留原有空行语义。
      lines.push(captions.length ? CENTER_BLANK_LINE : BLANK_LINE);
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'video') {
      lines.push(renderVideo(e.src, e.poster));
      // 视频后若不是来源说明（cap），补空行（同图片口径）
      if (!elems[i + 1] || elems[i + 1].k !== 'cap') {
        lines.push(BLANK_LINE);
      }
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'cap') {
      lines.push(renderImageCaption(runsToPlain(e.runs), config));
      // 连续图注（图注 + 图片来源）之间不插空行；最后一个图注后用 center 对齐空行
      if (!elems[i + 1] || elems[i + 1].k !== 'cap') {
        lines.push(CENTER_BLANK_LINE);
      }
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'ref-header') {
      const refColor = (config && config.muted_color) || '#888888';
      const refLabel = behTemplate(config, 'ref_header_label', '参考来源：');
      lines.push(`<p style="text-align: justify;"><span style="color: ${refColor};">${esc(refLabel)}</span></p>`);
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'ref-item') {
      // 用 renderRuns 而不是 runsToPlain：参考条目可能含超链接（如 [1] 论文标题 https://...），
      // runsToPlain 仅保留 txt 类型 run，会把 link 文本吃掉。
      const inner = renderRuns(e.runs, config);
      const refColor = (config && config.muted_color) || '#888888';
      lines.push(
        `<p style="text-align: justify;"><span style="color: ${refColor};">` +
        inner +
        '</span></p>'
      );
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'table') {
      lines.push(renderTable(e, config));
      lines.push(BLANK_LINE);
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    // p
    const standalone = isStandaloneLinkParagraph(e.runs);
    lines.push(renderParagraph(e.runs || [], e, config));
    const next = elems[i + 1];
    const compactContactPair = e.contactInfo && next && next.contactInfo && !next.blankBefore;
    // 目录条目之间不插空行（62303 成品：目录区条目紧密相连，仅区尾 1 空行）。
    const compactTocPair = e.tocEntry && next && next.tocEntry;
    const compactRelatedPair = e.relatedReading && next && next.relatedReading && !next.blankBefore;
    // 如果当前是独立链接 + 上一段也是独立链接，不插空行；联系信息块内部同理保持紧凑。
    if (!(standalone && prevWasStandaloneLink) && !compactContactPair && !compactTocPair && !compactRelatedPair) {
      lines.push(BLANK_LINE);
    }
    prevWasStandaloneLink = standalone;
    prevWasHeading = false;
  }
  // 底部：统一区块模型（config.modules.bottom_blocks）优先；缺省回退历史页脚库（fbif_footer）。
  // tip 提示声明不在此处——它走正文流（参考来源之后），由 behavior 提升为 tip elem 渲染。
  const mods = config.modules || {};
  const bottomBlocks = mods.bottom_blocks;
  // 防泄漏：mergeRuleDefaults 以公众号基线(含 mp 的 name_card bottom_blocks)为合并底，
  // 无 bottom_blocks 的网站旧规则会被塞进公众号版。网站页脚段是 rich_html，故仅当存在
  // rich_html 区块时才认其为「网站底部」走区块渲染；否则回退 fbif_footer（保住页脚）。
  // 网站 v2 底部识别（统一打包）：取 active preset 的 blocks（复用 block-merge 的 pickActivePreset）。
  // 仅含 rich_html 区块才认作「网站底部」——rich_html 是网站特有的页脚段类型，公众号 v2
  // presets 是 text/image/spacer 原子、不含 rich_html，故据此区分「网站自己的底部」与
  // 「mergeRuleDefaults 泄漏进来的公众号 bottom_blocks」（rich_html 特征解决了旧 schema_version
  // 无法区分公众号 v2 泄漏的问题——前任推迟的 templateId 标记不再必需）。
  const _ap = (bottomBlocks && typeof bottomBlocks === 'object') ? pickActivePreset(bottomBlocks) : {};
  const bbActiveBlocks = (_ap && typeof _ap.blocks === 'object') ? _ap.blocks : null;
  const bbIsSiteFooter = bbActiveBlocks && Object.keys(bbActiveBlocks).some(k => bbActiveBlocks[k] && bbActiveBlocks[k].type === 'rich_html');
  if (bottomBlocks && typeof bottomBlocks === 'object' && bbIsSiteFooter) {
    // 单篇覆盖由 site.html syncEngineConfig 从 localStorage 映射进来（开关/排序/字段）。
    const override = config._bottomBlocksOverride || {};
    const footerHtml = renderBottomBlocksHtml(bottomBlocks, override, siteBlockRenderers(mods.asset_library, 'ft-default'));
    if (footerHtml) {
      // 正文以 tip 提示声明收尾时，它与底部首块（商务合作）同属一组声明，应连续无空行
      // （红线#20）。仅当正文不是以 tip 收尾时才插正文→页脚分隔空行。
      if (!endedWithTip && lines[lines.length - 1] !== BLANK_LINE && lines[lines.length - 1] !== CENTER_BLANK_LINE) {
        lines.push(BLANK_LINE);
      }
      lines.push(footerHtml);
    }
  } else {
    // 回退：历史页脚库渲染（与旧行为字节一致；bottom_blocks 缺省时仍可用）。
    const footerCfg = mods.fbif_footer || {};
    if (footerCfg.enabled) {
      const footerHtml = buildFooterHtml(activeFooterSegments(footerCfg), footerCfg, mods.asset_library, 'ft-default');
      if (footerHtml) {
        if (!endedWithTip && lines[lines.length - 1] !== BLANK_LINE && lines[lines.length - 1] !== CENTER_BLANK_LINE) {
          lines.push(BLANK_LINE);
        }
        lines.push(footerHtml);
      }
    }
  }
  // 系列口径（config.justify_paragraphs===false）：FoodTalks「什么是」系列成品正文/标题/署名/参考
  // 是裸标签（样式靠后台 CSS，8 篇成品 justify=0），去掉 ft-default 的 text-align: justify。
  // ft-default 默认 justify=true → 不走此清理，输出字节不变（红线#19：网站内部共用渲染器，config 区分）。
  if (config && config.justify_paragraphs === false) {
    return lines.map(l => l
      .replace(/<p style="text-align: justify;">/g, '<p>')
      .replace(/(<h[1-6][^>]*?) style="text-align: justify;"/g, '$1')
      .replace(/ ?text-align: justify;/g, ''));
  }
  return lines;
}

function runsToPlain(runs) {
  return (runs || []).filter(r => r.type === 'txt' || r.type === 'link').map(r => r.text).join('');
}

// 判断段是不是"独立链接段"：runs 里只有 1 个 link、其他没有可见内容（image 也算 hidden）
function isStandaloneLinkParagraph(runs) {
  if (!Array.isArray(runs)) return false;
  const visible = runs.filter(r => r.type !== 'img');
  if (visible.length !== 1) return false;
  return visible[0].type === 'link';
}

// ---- DOCX 分类（共用结构层 + 网站口径适配） ----
// DOCX 内容结构分类下沉到共享模块 docx-article-model.js（与公众号共用一套）：前置材料
// 剥离、标题识别（outlineLvl/heading 样式，比旧版仅认 styleName 更全）、图片按 file 字段
// 取图（根治旧版 imgCache[imgRun.id] 导致 src 恒空的 bug）、图注 look-ahead。
//
// 网站口径保留（用户 2026-06-01 裁定「结构共用 + 网站口径保留」）：
//   - consumeAuthorLine=false —— 作者行留在正文流，交给 applyFoodTalksBehavior 渲成
//     #888 14px 斜体署名（网站口径，非公众号 #396FBC）。
//   - classifyReferences=false —— 参考来源不在共享层分类，仍由 applyFoodTalksBehavior
//     按网站更保守的 [n]/— 口径识别（对 8 篇基石调过）。

// 从 config 解析共享分类所需的注入项（规则源无关边界：共享模块只接已编译 RegExp）。
function buildDocxOpts(config) {
  let authorLineRegex = /^[\s　]*(?:作者|Author)\s*[:：]\s*(.*)$/i;
  const src = behParamStr(config, 'docx_preamble_author_regex', '');
  if (src) { try { authorLineRegex = compileSiteRegex(src); } catch (e) { /* 非法降级 */ } }
  return {
    authorLineRegex,
    consumeAuthorLine: false,
    classifyReferences: false,
    extractVideo: true,
    sameParagraphImageTextAsCaption: isSeriesSiteConfig(config) &&
      behParamBool(config, 'docx_same_paragraph_image_caption', false),
  };
}

// 中性元素 → 网站本地 elem。网站渲染结构与公众号物理隔离（红线#19）。
function adaptNeutralToSite(e) {
  if (e.k === 'img') return e.src ? { k: 'img', src: e.src, alt: '', width: 0, height: 0, w: e.w, aspect: e.aspect, blankBefore: e.blankBefore } : null;
  if (e.k === 'video') return e.src ? { k: 'video', src: e.src, poster: e.poster || '', blankBefore: e.blankBefore } : null;
  if (e.k === 'h') {
    const styleLevel = String(e.styleName || '').match(/(?:heading|标题)\s*([1-9])/i);
    const sourceLevel = styleLevel ? Number(styleLevel[1]) : 2;
    const level = resolveSiteHeadingLevel(e.text, sourceLevel >= 4 ? 4 : (sourceLevel === 3 ? 3 : 2));
    return { k: 'h', level, runs: [{ type: 'txt', text: e.text }], blankBefore: e.blankBefore };
  }
  if (e.k === 'cap') return { k: 'cap', runs: [{ type: 'txt', text: e.text }], blankBefore: e.blankBefore };
  if (e.k === 'table') return { k: 'table', rows: e.rows || [], colWidths: e.colWidths || [], blankBefore: e.blankBefore };
  return { k: 'p', runs: e.runs || [], blankBefore: e.blankBefore };
}

function classifyDocx(paragraphs, imgCache, config = {}) {
  const { elems } = classifyDocxArticle(paragraphs || [], imgCache || {}, buildDocxOpts(config));
  // imgN 按【实际渲染】的图计数：共享层无条件计每个 img run，但空 src 图在 adapter 被
  // filter 丢弃不渲染，故缓存未命中时重算，避免状态栏「图片：N」高估（与旧版口径一致）。
  let siteElems = elems.map(adaptNeutralToSite).filter(Boolean);
  // 信号优先级（与公众号 fbif.js classifyDocx 同一原则，见 WORKLOG-docx-heading-signal-priority.md）：
  // DOCX 里已有真·Word 标题样式的标题 ⇒ 整段加粗只是强调，给所有正文段打 skipHeadingPromote，
  // 渲染期 promoteHeuristicHeadings 对其免疫。只在 DOCX 分类口打标——URL 导入/MD 路径不受影响。
  if (hasExplicitDocxHeadings(siteElems)) {
    siteElems = siteElems.map(e => (e.k === 'p' ? { ...e, skipHeadingPromote: true } : e));
  }
  // 表格内图片藏在 k:'table' 的 rows.runs 里（不在顶层 k:'img'），单独累加有 src 的，
  // 与公众号口径一致（否则『顶层无图、表格有图』的文章状态栏会显示图片:0）。
  const tableImgN = siteElems
    .filter(e => e.k === 'table')
    .reduce((n, t) => n + (t.rows || []).reduce((rn, row) =>
      rn + (row || []).reduce((cn, cell) =>
        cn + (cell.runs || []).filter(r => r.type === 'img' && r.src).length, 0), 0), 0);
  return { elems: siteElems, imgN: siteElems.filter(e => e.k === 'img').length + tableImgN };
}

function extractDocxSummary(elems, config = {}) {
  const maxLen = behParamNum(config, 'docx_summary_max_chars', 0);
  if (!isSeriesSiteConfig(config) || maxLen <= 0) return { elems, summary: '' };
  for (let i = 0; i < (elems || []).length; i++) {
    const e = elems[i];
    if (!e) continue;
    if (e.k === 'img' || e.k === 'video' || e.k === 'h' || e.k === 'table') break;
    if (e.k !== 'p') continue;
    const text = runsToPlain(e.runs).trim();
    if (!text) continue;
    if (text === '目录' || sectionNumberPrefix(text)) return { elems, summary: '' };
    if (/^(?:作者|来源|参考(?:来源|资料))[：:]/.test(text)) return { elems, summary: '' };
    if (text.length > maxLen) return { elems, summary: '' };
    const hasMediaAfter = elems.slice(i + 1).some(x => x && (x.k === 'img' || x.k === 'video'));
    if (!hasMediaAfter) return { elems, summary: '' };
    const consume = behParamBool(config, 'docx_summary_consume', true);
    return {
      elems: consume ? elems.filter((_, idx) => idx !== i) : elems,
      summary: text,
    };
  }
  return { elems, summary: '' };
}

// ---- Markdown 分类（最小版） ----

async function classifyMd(text) {
  const fm = parseMdFrontmatter(text);
  const body = fm.body || text;
  const lines = body.split(/\r?\n/);
  const elems = [];
  let imgN = 0;
  for (const line of lines) {
    if (!line.trim()) continue;
    let m;
    if ((m = line.match(/^(#{1,6})\s+(.*)$/))) {
      const sourceLevel = m[1].length;
      const mapped = sourceLevel >= 4 ? 4 : (sourceLevel === 3 ? 3 : 2);
      elems.push({ k: 'h', level: resolveSiteHeadingLevel(m[2], mapped), runs: parseMdRuns(m[2]) });
      continue;
    }
    if ((m = line.match(/^!\[([^\]]*)\]\(([^)]+)\)\s*$/))) {
      elems.push({ k: 'img', src: m[2], alt: m[1] });
      imgN += 1;
      continue;
    }
    elems.push({ k: 'p', runs: parseMdRuns(line) });
  }
  return { elems, imgN, title: (fm.frontmatter && fm.frontmatter.title) || '' };
}

// dropRepeatedDecorHeadings：同一文本的短标题在全文出现 ≥2 次 → 全部删除。
// 公众号原文常在每个章节前放居中/加粗的账号名装饰行（如「天下网商」×3），视觉信号
// 与标题无异，会被误升 h2（2026-08-17 运营反馈「标题识别不准」的实锤案例；线上成品
// news/62813 的编辑处理是删除）。真实文章标题不会整篇重复，重复即装饰水印。
// 带中文/数字序号的标题（一、/（一）/1.）即使重复也保留，绝不误删真标题。
function dropRepeatedDecorHeadings(elems) {
  const list = Array.isArray(elems) ? elems : [];
  const counts = new Map();
  const headingText = (e) => runsToPlain(e.runs || []).trim();
  for (const e of list) {
    if (!e || e.k !== 'h') continue;
    const t = headingText(e);
    if (t) counts.set(t, (counts.get(t) || 0) + 1);
  }
  return list.filter(e => {
    if (!e || e.k !== 'h') return true;
    const t = headingText(e);
    if (!t || t.length > 16) return true;
    if (/^[一二三四五六七八九十\d]+\s*[、．.：:]/.test(t)) return true;
    if (/^[（(]\s*(?:[一二三四五六七八九十]+|\d+)\s*[)）]/.test(t)) return true;
    return (counts.get(t) || 0) < 2;
  });
}

// ---- Blocks 分类（从后端 /api/fetch-article 来的结构化块） ----

// 服务端 /api/fetch-article 返回的 block 字段契约见 api/wechat_blocks.go：
// 类型在 `b.k`（img/h/cap/txt/bq/li），图片地址 `b.src`、宽度 `b.dataW`（字符串），
// 文本在 `b.runs` 或 `b.text`。产出的 elem 结构与 classifyDocx 一致，下游 render
// 直接消费（图片读 src/alt/width，标题/图注/正文读 runs）。
function classifyBlocks(data) {
  const elems = [];
  let imgN = 0;
  const meta = b => (b && b.blankBefore ? { blankBefore: true } : {});
  const blocks = (data && data.blocks) || [];
  for (let i = 0; i < blocks.length; i++) {
    const b = blocks[i];
    if (!b || !b.k) continue;
    if (isSectionNumberHeading(b, blocks[i + 1])) continue;
    if (b.k === 'img') {
      // 自适应宽度（adaptiveImageWidth）按 height/width 比例缩窄竖长图。URL 导入此前
      // 只传 dataW、height=0，导致自适应完全不生效（恒返回 600）。这里补出 height：
      // 优先 data-h，否则用 data-ratio（微信 = 高/宽）× 宽度反推。
      const w = Number(b.dataW || 0) || 0;
      const ratio = Number(b.dataRatio || 0) || 0;
      const h = Number(b.dataH || 0) || (w && ratio ? Math.round(w * ratio) : 0);
      elems.push({
        k: 'img',
        src: b.src || '',
        alt: '',
        width: w,
        height: h,
        ...meta(b),
      });
      imgN += 1;
      continue;
    }
    if (b.k === 'video') {
      elems.push({ k: 'video', src: b.src || '', poster: b.poster || '', ...meta(b) });
      continue;
    }
    if (b.k === 'h') {
      // 标题1 → h2；标题2（（一）/ 1、2、3）→ h3。公众号→网站规范。
      const hText = b.text || '';
      const hLevel = resolveSiteHeadingLevel(hText, b.level);
      elems.push({ k: 'h', level: hLevel, runs: [{ type: 'txt', text: hText }], ...meta(b) });
      continue;
    }
    if (b.k === 'cap') {
      elems.push({ k: 'cap', runs: [{ type: 'txt', text: b.text || '' }], ...meta(b) });
      continue;
    }
    if (b.k === 'table') {
      elems.push({
        k: 'table',
        rows: (b.rows || []).map(row => (row || []).map(c => ({
          runs: Array.isArray(c.runs) ? c.runs : [],
          header: !!c.header,
        }))),
        ...meta(b),
      });
      continue;
    }
    // txt / bq / li 都带 runs；缺 runs 时回退到 text。
    const runs = Array.isArray(b.runs) && b.runs.length
      ? b.runs
      : [{ type: 'txt', text: b.text || '' }];
    elems.push({ k: 'p', runs, ...meta(b) });
  }
  return { elems, imgN, title: (data && data.title) || '' };
}

// ---- Template export ----

export default {
  id: 'foodtalks-site',
  name: 'FoodTalks 网站排版',
  description: '按 FoodTalks 网站后台规范输出 inline-style HTML',
  formats: ['.docx', '.doc', '.md', '.txt'],
  // 渠道能力声明：网站渠道 mmbiz 图需转存自有 CDN（engine.js 读此标志，替代硬编码 id 判断）。
  transferMmbiz: true,

  processDocx({ paragraphs, imgCache, config = {}, isRepost = false }) {
    const { elems, imgN } = classifyDocx(paragraphs, imgCache, config);
    const docxSummary = extractDocxSummary(elems, config);
    const normalized = applySeriesContactLayout(markTocEntries(normalizeWhatIsSeriesElems(docxSummary.elems, config)), config);
    const behaved = markInterviewSpeakerPrefixes(applyFoodTalksBehavior(normalized, config, { isRepost }));
    // 先做标题升级，render 与 headingN 都基于升级后的 elems —— 否则加粗段升级的
    // h2 不计入侧栏「标题」统计，会显示 0 误导用户（实际已正确输出）。
    const promoted = promoteImageCaptions(promoteHeuristicHeadings(behaved, buildHeadingOpts(config)), buildCaptionOpts(config));
    const finalElems = dropRepeatedDecorHeadings(markSeriesAboutLogoImages(promoted, config));
    return {
      lines: render(finalElems, config),
      imgN,
      headingN: finalElems.filter(e => e.k === 'h').length,
      summary: docxSummary.summary,
    };
  },

  async processMd(text, opts) {
    const config = (opts && opts.config) || {};
    const { elems, imgN, title } = await classifyMd(text);
    const normalized = applySeriesContactLayout(markTocEntries(normalizeWhatIsSeriesElems(elems, config)), config);
    const behaved = markInterviewSpeakerPrefixes(applyFoodTalksBehavior(normalized, config, { isRepost: !!(opts && opts.isRepost), title }));
    const promoted = promoteImageCaptions(promoteHeuristicHeadings(behaved, buildHeadingOpts(config)), buildCaptionOpts(config));
    const finalElems = dropRepeatedDecorHeadings(markSeriesAboutLogoImages(promoted, config));
    return {
      lines: render(finalElems, config),
      imgN,
      headingN: finalElems.filter(e => e.k === 'h').length,
      title,
    };
  },

  processBlocks(data, opts) {
    const config = (opts && opts.config) || {};
    const { elems, imgN, title } = classifyBlocks(data);
    // 转载声明《标题》链接：有阅读原文用阅读原文；自家号转发且没有原文时不链回
    // 提交的公众号转载页。自动发布路径会把已解析的原文 URL 放进 data.source。
    const normalized = applySeriesContactLayout(markTocEntries(normalizeWhatIsSeriesElems(elems, config)), config);
    const behaved = markInterviewSpeakerPrefixes(applyFoodTalksBehavior(normalized, config, {
      isRepost: !!(opts && opts.isRepost),
      title,
      // 转载声明《》里的标题：服务端找到改过标题的原文时带回原文真标题，否则用本稿标题。
      tipTitle: String(data.original_title || '').trim() || title,
      sourceUrl: pickSiteTipSourceUrl(data, elems),
      sourceName: data.source_name,
    }));
    // URL Blocks 已由服务端完成媒体绑定、正文屏障和字数限制，cap/txt 是最终角色。
    const promoted = promoteHeuristicHeadings(behaved, buildHeadingOpts(config));
    const finalElems = dropRepeatedDecorHeadings(markSeriesAboutLogoImages(promoted, config));
    return {
      lines: render(finalElems, config),
      imgN,
      headingN: finalElems.filter(e => e.k === 'h').length,
      title,
    };
  },
};
