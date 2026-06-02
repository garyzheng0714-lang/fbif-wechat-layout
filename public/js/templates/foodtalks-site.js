// FoodTalks 网站排版模板 — 输出 inline-style HTML，给 FoodTalks 网站后台编辑器粘贴用
// 输出契约（M2，基于 8 篇真实 foodtalks.cn 文章采样对齐，参见
// docs/superpowers/specs/2026-05-27-foodtalks-site-layout-design.md）：
//   - <h2 id="mctoc_xxxxxxxxxxxx">/<h3 id=...> 当 config.modules.mctoc.enabled 开启时带锚点 ID，否则纯标签
//   - <p style="text-align: justify;"> 所有段落两端对齐
//   - 空行：<p style="text-align: justify;"><br></p>（显式标签）
//   - 图片：<p><img style="display: block; margin-left: auto; margin-right: auto;" class="mc-img" width=...></p>
//   - 图注：<p style="text-align: center;"><span style="font-size: 12px; color: #888888;">...</span></p>
//   - 参考来源：ref-header / ref-item 分支，14px #888888 段落，保留作者原始 [n] 编号（不再自动补）
//   - 链接：a 标签补 target="_blank" rel="noopener"
//   - 作者署名 / 提示行：由 applyFoodTalksBehavior 行为模板从文档中提升识别
//   - 可选 FBIF 公众号原创页脚：config.modules.fbif_footer.enabled 开启时在文末追加

const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter } = engineModule;
// 等价于: import { applyFoodTalksBehavior, FBIF_FOOTER_HTML } from '../rule-behavior-foodtalks.js'
// 用动态 import 是为了配合浏览器端的 cache-busting assetQuery
const behaviorModule = await import('../rule-behavior-foodtalks.js' + assetQuery);
const { applyFoodTalksBehavior, buildFooterHtml, compileSiteRegex } = behaviorModule;

// ── 规则读取助手（红线下放：优先服务端 config / config.behavior，缺省/非法降级 const）──
function behOf(config) { return (config && config.behavior) || {}; }
function cfgNum(config, key, fallback) {
  const n = Number(config && config[key]);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}
function cfgColor(config, key, fallback) {
  // 颜色进 style，做 hex 白名单防 XSS；非法/缺省降级 const。
  const v = config && config[key];
  return (typeof v === 'string' && /^#[0-9a-fA-F]{6}$/.test(String(v).trim())) ? String(v).trim() : fallback;
}
function behTemplate(config, key, fallback) {
  const t = behOf(config).templates;
  const v = t && t[key];
  return (typeof v === 'string' && v) ? v : fallback;
}
function behParamStr(config, key, fallback) {
  const p = behOf(config).params;
  const s = p && p[key] && p[key].default;
  return (typeof s === 'string' && s) ? s : fallback;
}
function behList(config, key, fallback) {
  const v = behOf(config).lists && behOf(config).lists[key];
  return (Array.isArray(v) && v.length) ? v : fallback;
}
// 标题识别（detectHeadingLevel / isBoldHeading / promoteHeuristicHeadings）抽到无依赖
// 的 heading-detect.js，便于真实单元测试（见 tests/heading-detect.test.mjs）。
const headingModule = await import('../heading-detect.js' + assetQuery);
const { promoteHeuristicHeadings } = headingModule;
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

function normalizeChinesePunctuation(text) {
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
    if (!touchesChinese(i)) return ch;
    const prev = findTextChar(i, -1);
    const next = findTextChar(i, +1);
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

function renderRuns(runs) {
  return (runs || []).filter(r => r.type !== 'img').map(r => {
    if (r.type === 'txt') {
      let s = esc(normalizeChinesePunctuation(r.text));
      if (r.bold) s = '<strong>' + s + '</strong>';
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

function renderHeading(level, text, config = {}) {
  // 基石 news/62285 的标题是 <h2 style="text-align: justify;">…</h2>；id="mctoc_xxx" 是
  // FoodTalks 后台生成目录时自动注入的锚点，默认不输出（用户 2026-05-30 确认），仅在
  // mctoc 模块显式开启时补 id。
  const mctocEnabled = !!(config.modules && config.modules.mctoc && config.modules.mctoc.enabled);
  const idAttr = mctocEnabled ? ` id="${genMctocId()}"` : '';
  const tag = level === 3 ? 'h3' : 'h2';
  return `<${tag}${idAttr} style="text-align: justify;">${esc(text)}</${tag}>`;
}

function renderParagraph(runs) {
  const inner = renderRuns(runs);
  if (!inner) return BLANK_LINE;
  return `<p style="text-align: justify;">${inner}</p>`;
}

// adaptiveImageWidth（竖图400/横图600）已抽到 image-width.js（顶部 import），可独立单测。

function renderImage(src, alt, naturalWidth, naturalHeight, config = {}) {
  const safeSrc = escAttr(src || '');
  const safeAlt = escAttr(alt || '');
  // 图宽规则（红线下放：config.image.portrait_px/landscape_px，缺省降级 400/600）。
  const imgCfg = (config && config.image) || {};
  const width = adaptiveImageWidth(naturalWidth, naturalHeight, {
    portrait: Number(imgCfg.portrait_px) || 400,
    landscape: Number(imgCfg.landscape_px) || 600,
  });
  // mmbiz 等公众号图带防盗链：浏览器若发 Referer 头会被换成"未经允许不可引用"
  // 占位图。referrerpolicy="no-referrer" 让加载图片时不发 Referer，绕过防盗链。
  // 与公众号渲染（fbif.js）保持一致。
  const rpol = (src || '').startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
  // 内联 max-width:100% 是给外部 CMS / 邮件客户端用的兜底——本工具的 .mc-img 仅
  // 在预览页 CSS 中生效，复制粘贴到不带样式的容器时大图会溢出。
  return (
    `<p>` +
    `<img style="display: block; margin-left: auto; margin-right: auto; max-width: 100%; height: auto;" ` +
    `src="${safeSrc}" alt="${safeAlt}" width="${width}" class="mc-img"${rpol}>` +
    `</p>`
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

// ---- 表格渲染（统一表格样式，与公众号物理隔离：各写各的 renderTable，仅共享中性 rows 结构）----
// 网站渲染器本就全 inline-style（粘进 FoodTalks 后台编辑器），表格同样把 border/底色/字号/
// 内距内联到每个 td。颜色/字号/边框/内距读扁平 config 键（cfgColor/cfgNum，红线下放规则后台
// 可配），缺省降级到与服务端 defaultRuleConfig 逐字一致的 const（与公众号同一套默认值=统一）。
function tableRunRenderers() {
  return {
    text: r => renderRuns([r]),
    link: r => renderRuns([r]),
    image(r) {
    if (r.type === 'img') {
      if (!r.src) return '';
      const rpol = r.src.startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
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
    renderers: tableRunRenderers(),
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

// ---- 渲染主入口：把分类后的 elems 拼装成 HTML 行数组 ----

function render(elems, config = {}) {
  const lines = [];
  let prevWasHeading = false;
  let prevWasStandaloneLink = false;
  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    if (e.k === 'h') {
      // 标题前 1 个空行。p 分支已在每段后留空行，这里避免再叠加成双空行。
      if (!prevWasHeading && lines.length > 0 && lines[lines.length - 1] !== BLANK_LINE) {
        lines.push(BLANK_LINE);
      }
      lines.push(renderHeading(e.level === 3 ? 3 : 2, runsToPlain(e.runs), config));
      prevWasHeading = true;
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
    if (e.k === 'tip') {
      // 提示声明受页脚库勾选 active_segments.tip 控制：仅当用户显式取消勾选（=0）时不输出。
      // 默认（未设 / 未配页脚）仍输出，保证转载版权声明默认在（基石所有文章都有）。
      const ftSeg = config.modules && config.modules.fbif_footer && config.modules.fbif_footer.active_segments;
      if (ftSeg && ftSeg.tip === 0) {
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
      const restInner = renderRuns(restRuns);
      if (restInner) {
        lines.push(
          `<p style="text-align: justify;"><em><span style="font-size: ${tipFs}px; color: ${tipBodyColor};">` +
          restInner +
          '</span></em></p>'
        );
      }
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'img') {
      lines.push(renderImage(e.src, e.alt, e.width, e.height, config));
      // 图片后若不是图注，补空行（基石：图→正文间有空行；图→图注紧贴无空行）
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
      const inner = renderRuns(e.runs);
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
    lines.push(renderParagraph(e.runs || []));
    // 如果当前是独立链接 + 上一段也是独立链接，不插空行
    if (!(standalone && prevWasStandaloneLink)) {
      lines.push(BLANK_LINE);
    }
    prevWasStandaloneLink = standalone;
    prevWasHeading = false;
  }
  // 底部：统一区块模型（config.modules.bottom_blocks）优先；缺省回退历史页脚库（fbif_footer）。
  // tip 提示声明不在此处——它走正文流（参考来源之后），由 behavior 提升为 tip elem 渲染。
  const mods = config.modules || {};
  const bottomBlocks = mods.bottom_blocks;
  if (bottomBlocks && typeof bottomBlocks === 'object') {
    // 单篇覆盖由 site.html syncEngineConfig 从 localStorage 映射进来（开关/排序/字段）。
    const override = config._bottomBlocksOverride || {};
    const footerHtml = renderBottomBlocksHtml(bottomBlocks, override, siteBlockRenderers(mods.asset_library, 'ft-default'));
    if (footerHtml) {
      lines.push(BLANK_LINE);
      lines.push(footerHtml);
    }
  } else {
    // 回退：历史页脚库渲染（与旧行为字节一致；bottom_blocks 缺省时仍可用）。
    const footerCfg = mods.fbif_footer || {};
    if (footerCfg.enabled) {
      const footerHtml = buildFooterHtml(footerCfg.active_segments, footerCfg, mods.asset_library, 'ft-default');
      if (footerHtml) {
        lines.push(BLANK_LINE);
        lines.push(footerHtml);
      }
    }
  }
  return lines;
}

function runsToPlain(runs) {
  return (runs || []).filter(r => r.type === 'txt').map(r => r.text).join('');
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
  return { authorLineRegex, consumeAuthorLine: false, classifyReferences: false };
}

// 中性元素 → 网站本地 elem。网站渲染结构与公众号物理隔离（红线#19）：图片走像素 width
// 属性（renderImage 的 adaptiveImageWidth；DOCX 无像素尺寸 → width/height=0 → 默认 600，
// 与改动前一致），标题层级沿用旧版按 styleName 区分 h2/h3，图注/正文包成 runs。参考来源
// 因 classifyReferences=false 不出现在中性序列里，落为 p 交行为层识别。
function adaptNeutralToSite(e) {
  // 缓存未命中（src 空）→ 丢弃该图，绝不输出 <img src="">（坏 HTML 会污染网站后台）。
  if (e.k === 'img') return e.src ? { k: 'img', src: e.src, alt: '', width: 0, height: 0 } : null;
  if (e.k === 'h') {
    const level = /heading\s*[3-6]/.test(String(e.styleName || '').toLowerCase()) ? 3 : 2;
    return { k: 'h', level, runs: [{ type: 'txt', text: e.text }] };
  }
  if (e.k === 'cap') return { k: 'cap', runs: [{ type: 'txt', text: e.text }] };
  if (e.k === 'table') return { k: 'table', rows: e.rows || [] };
  return { k: 'p', runs: e.runs || [] };
}

function classifyDocx(paragraphs, imgCache, config = {}) {
  const { elems } = classifyDocxArticle(paragraphs || [], imgCache || {}, buildDocxOpts(config));
  // imgN 按【实际渲染】的图计数：共享层无条件计每个 img run，但空 src 图在 adapter 被
  // filter 丢弃不渲染，故缓存未命中时重算，避免状态栏「图片：N」高估（与旧版口径一致）。
  const siteElems = elems.map(adaptNeutralToSite).filter(Boolean);
  // 表格内图片藏在 k:'table' 的 rows.runs 里（不在顶层 k:'img'），单独累加有 src 的，
  // 与公众号口径一致（否则『顶层无图、表格有图』的文章状态栏会显示图片:0）。
  const tableImgN = siteElems
    .filter(e => e.k === 'table')
    .reduce((n, t) => n + (t.rows || []).reduce((rn, row) =>
      rn + (row || []).reduce((cn, cell) =>
        cn + (cell.runs || []).filter(r => r.type === 'img' && r.src).length, 0), 0), 0);
  return { elems: siteElems, imgN: siteElems.filter(e => e.k === 'img').length + tableImgN };
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
    if ((m = line.match(/^#{1,2}\s+(.*)$/))) {
      elems.push({ k: 'h', level: 2, runs: parseMdRuns(m[1]) });
      continue;
    }
    if ((m = line.match(/^#{3,6}\s+(.*)$/))) {
      elems.push({ k: 'h', level: 3, runs: parseMdRuns(m[1]) });
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

// ---- Blocks 分类（从后端 /api/fetch-article 来的结构化块） ----

// 服务端 /api/fetch-article 返回的 block 字段契约见 api/wechat_blocks.go：
// 类型在 `b.k`（img/h/cap/txt/bq/li），图片地址 `b.src`、宽度 `b.dataW`（字符串），
// 文本在 `b.runs` 或 `b.text`。产出的 elem 结构与 classifyDocx 一致，下游 render
// 直接消费（图片读 src/alt/width，标题/图注/正文读 runs）。
function classifyBlocks(data) {
  const elems = [];
  let imgN = 0;
  for (const b of (data && data.blocks) || []) {
    if (!b || !b.k) continue;
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
      });
      imgN += 1;
      continue;
    }
    if (b.k === 'h') {
      elems.push({ k: 'h', level: 2, runs: [{ type: 'txt', text: b.text || '' }] });
      continue;
    }
    if (b.k === 'cap') {
      elems.push({ k: 'cap', runs: [{ type: 'txt', text: b.text || '' }] });
      continue;
    }
    if (b.k === 'table') {
      elems.push({
        k: 'table',
        rows: (b.rows || []).map(row => (row || []).map(c => ({
          runs: Array.isArray(c.runs) ? c.runs : [],
          header: !!c.header,
        }))),
      });
      continue;
    }
    // txt / bq / li 都带 runs；缺 runs 时回退到 text。
    const runs = Array.isArray(b.runs) && b.runs.length
      ? b.runs
      : [{ type: 'txt', text: b.text || '' }];
    elems.push({ k: 'p', runs });
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
    const behaved = applyFoodTalksBehavior(elems, config, { isRepost });
    // 先做标题升级，render 与 headingN 都基于升级后的 elems —— 否则加粗段升级的
    // h2 不计入侧栏「标题」统计，会显示 0 误导用户（实际已正确输出）。
    const promoted = promoteImageCaptions(promoteHeuristicHeadings(behaved, buildHeadingOpts(config)), buildCaptionOpts(config));
    return {
      lines: render(promoted, config),
      imgN,
      headingN: promoted.filter(e => e.k === 'h').length,
    };
  },

  async processMd(text, opts) {
    const config = (opts && opts.config) || {};
    const { elems, imgN, title } = await classifyMd(text);
    const behaved = applyFoodTalksBehavior(elems, config, { isRepost: !!(opts && opts.isRepost), title });
    const promoted = promoteImageCaptions(promoteHeuristicHeadings(behaved, buildHeadingOpts(config)), buildCaptionOpts(config));
    return {
      lines: render(promoted, config),
      imgN,
      headingN: promoted.filter(e => e.k === 'h').length,
      title,
    };
  },

  processBlocks(data, opts) {
    const config = (opts && opts.config) || {};
    const { elems, imgN, title } = classifyBlocks(data);
    // data.source 是后端抓取的原文 URL（公众号链接），用于提示声明的标题超链接。
    const behaved = applyFoodTalksBehavior(elems, config, {
      isRepost: !!(opts && opts.isRepost),
      title,
      sourceUrl: (data && data.source) || '',
    });
    const promoted = promoteImageCaptions(promoteHeuristicHeadings(behaved, buildHeadingOpts(config)), buildCaptionOpts(config));
    return {
      lines: render(promoted, config),
      imgN,
      headingN: promoted.filter(e => e.k === 'h').length,
      title,
    };
  },
};
