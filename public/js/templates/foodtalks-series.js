// FoodTalks「什么是」系列公众号排版模板 — 输出内联样式 HTML，给微信公众号后台粘贴用。
//
// 与 FBIF（fbif.js / wx-theme.css）和 FoodTalks 网站（foodtalks-site.js）物理隔离（红线 #19）：
// 本模板不 import 那两侧，也不复用 wx-* class；全部内联样式，颜色/字号读 config（缺省降级
// foodtalks-series-defaults.js 常量）。共享层仅 import docx-article-model.js（中性结构分类）。
//
// 排版规范：飞书《策划文公众号排版标准》。核心差异——一级标题用「序号图 + 居中文字」呈现。

const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter, convertRuns, looksLikeGifSource } = engineModule;
const behaviorModule = await import('../rule-behavior-foodtalks-series.js' + assetQuery);
const {
  classifySeries, extractSeriesAuthorName, sectionImageUrl,
} = behaviorModule;
const docxModelModule = await import('../docx-article-model.js' + assetQuery);
const { classifyDocxArticle } = docxModelModule;
const defaultsModule = await import('../foodtalks-series-defaults.js' + assetQuery);
const { SERIES_COLORS, SERIES_SIZES, SERIES_LAYOUT, SERIES_FRONTMATTER } = defaultsModule;
const tableModule = await import('./shared/table-renderer.js' + assetQuery);
const { renderTable: renderSharedTable } = tableModule;
// 底部统一区块模型：中性编排器 + 系列专属渲染器（红线#19：系列(公众号)渲染器独立，
// 不 import mp-blocks/site-blocks；只 import 中性的 block-render 编排层与 asset-library 解析）。
const blockRenderModule = await import('../blocks/block-render.js' + assetQuery);
const { renderBottomBlocksHtml } = blockRenderModule;
const assetLibModule = await import('../asset-library.js' + assetQuery);
const { resolveAssetRefs } = assetLibModule;

// ── config 读取（优先扁平 config 键，缺省降级系列常量）──
function cfgNum(config, key, fallback) {
  const n = Number(config && config[key]);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}
function cfgColor(config, key, fallback) {
  const v = config && config[key];
  return (typeof v === 'string' && /^#[0-9a-fA-F]{6}$/.test(String(v).trim())) ? String(v).trim() : fallback;
}
// 绝对 http(s) 链接判定（热路径：表格/正文/图片每次渲染都用，提到模块级避免逐元素重编译）。
const HTTP_RE = /^https?:\/\//i;

// 站内相对路径 → 绝对地址（复制时 uploadNonCdnImages 需可达 URL；预览相对亦可，统一绝对更稳）。
function absUrl(src) {
  const s = String(src || '');
  if (s.startsWith('/') && typeof window !== 'undefined' && window.location) {
    return window.location.origin + s;
  }
  return s;
}

// ── 基本渲染元 ──
function styleProps(config) {
  return {
    heading: cfgColor(config, 'heading_color', SERIES_COLORS.heading_color),
    text: cfgColor(config, 'text_color', SERIES_COLORS.text_color),
    muted: cfgColor(config, 'muted_color', SERIES_COLORS.muted_color),
    link: cfgColor(config, 'link_color', SERIES_COLORS.link_color),
    bodySize: cfgNum(config, 'font_size', SERIES_SIZES.font_size),
    h1Size: cfgNum(config, 'h1_font_size', SERIES_SIZES.h1_font_size),
    h2Size: cfgNum(config, 'h2_font_size', SERIES_SIZES.h2_font_size),
    tocTitleSize: cfgNum(config, 'toc_title_size', SERIES_SIZES.toc_title_size),
    capSize: cfgNum(config, 'caption_font_size', SERIES_SIZES.caption_font_size),
    attrSize: cfgNum(config, 'attribution_font_size', SERIES_SIZES.attribution_font_size),
    noteSize: cfgNum(config, 'footer_note_font_size', SERIES_SIZES.footer_note_font_size),
    noteColor: cfgColor(config, 'footer_note_color', SERIES_COLORS.footer_note_color),
    lh: SERIES_LAYOUT.line_height,
    marginX: SERIES_LAYOUT.margin_x,
    gap: SERIES_LAYOUT.gap,
    font: SERIES_LAYOUT.font_stack,
  };
}

function section(inner, opts) {
  const { align = 'left', gap = true, sp } = opts;
  const style =
    `margin: 0 ${sp.marginX}px ${gap ? sp.gap : 0}px; text-align: ${align}; line-height: ${sp.lh};`;
  return `<section style="${style}">${inner}</section>`;
}

// 图片块。width 取自 DOCX 原始百分比（如二维码 27%），缺省 100%（序号图/尾部名片等满列资源）；
// 始终配 max-width:100% 防溢出，居中。沿用 fbif.js 口径（小图不拉满，与微信成品一致）。
// line-height:0 防图下基线空隙。
function imageSection(src, gap, sp, { alt = '', width = '100%' } = {}) {
  const url = absUrl(src);
  const isGif = looksLikeGifSource(url);
  const rpol = HTTP_RE.test(url) ? ' referrerpolicy="no-referrer"' : '';
  const gifAttr = isGif ? ' data-type="gif"' : '';
  const style = `margin: 0 0 ${gap ? sp.gap : 0}px; text-align: center; line-height: 0; font-size: 0;`;
  return (
    `<section style="${style}">` +
    `<img src="${escAttr(url)}"${gifAttr}${rpol} alt="${escAttr(alt)}" ` +
    `style="width: ${width}; max-width: 100%; height: auto; display: block; margin: 0 auto; vertical-align: top;" />` +
    `</section>`
  );
}

const MORE_CARDS_WRAPPER_OPEN = '<section style="text-align: center;" data-card-inset="0">';

function centerCss(center) {
  return center === false ? '' : 'text-align:center;';
}

function alignOr(v, fallback = 'left') {
  return ['left', 'center', 'right'].includes(v) ? v : fallback;
}

function moreArticlesTitle(f, sp) {
  const title = String(f.title == null ? '' : f.title).trim();
  if (!title) return '';
  const size = cfgNum(f, 'title_size', 17);
  const color = /^#[0-9a-fA-F]{6}$/.test(String(f.title_color || '')) ? f.title_color : sp.text;
  return `<section style="margin:20px 8px 20px;padding:0;${centerCss(f.center)}"><span style="color:${color};font-size:${size}px;font-family:${sp.font};line-height:${sp.lh};font-style:normal;font-weight:normal;"><strong>${esc(title)}</strong></span></section>`;
}

// 系列底部区块渲染器（type → HTML）。复用本模板的 imageSection，保持与正文同一字节口径
// （满宽 100%，区别于 FBIF mp-blocks 的 661px）。图片字段走素材库 {{asset#id}}；解析失败/为空
// → 不渲染（对齐旧 buildSeriesFooterElems 跳过空选的语义）。more_articles 输出现有复制链路
// 识别的两个锚点，卡片仍由 more-articles.js 非阻塞合成（红线 #9）。
function seriesBlockRenderers(sp, assetLibrary, scope) {
  const toUrl = (raw) => {
    const v = String(raw == null ? '' : raw).trim();
    if (!v) return '';
    const url = resolveAssetRefs([v], assetLibrary, scope)[0] || '';
    if (!url || url.startsWith('data:')) return ''; // 解析回退透明图 → 视为无图
    return url;
  };
  return {
    // 间距模块（v2 原子）：定高留白段，精确控制块间距（与 mp-blocks / site-blocks 同口径，渠道无关）。
    // height<=0 不输出；上限 200 与 catalog schema 一致。此前系列缺此渲染器，间距块会被 block-render 静默跳过。
    spacer: (f) => {
      let h = Math.round(Number(f.height));
      if (!Number.isFinite(h) || h <= 0) return '';
      if (h > 200) h = 200;
      return `<section style="height:${h}px;line-height:0;font-size:0;">&nbsp;</section>`;
    },
    text: (f) => {
      const lines = String(f.content == null ? '' : f.content).split(/\r?\n/).map(s => s.trim()).filter(Boolean);
      if (!lines.length) return '';
      const bg = String(f.background_color == null ? '' : f.background_color).trim();
      const size = cfgNum(f, 'font_size', sp.bodySize);
      const color = /^#[0-9a-fA-F]{6}$/.test(String(f.text_color || '')) ? f.text_color : sp.text;
      const style =
        `color:${color};font-size:${size}px;font-family:${sp.font};line-height:${sp.lh};` +
        `font-style:${f.italic ? 'italic' : 'normal'};font-weight:normal;text-decoration:${f.underline ? 'underline' : 'none'};` +
        (bg ? `background-color:${bg};` : '');
      const align = alignOr(f.align);
      return lines.map(line => `<p style="margin:0 8px;padding:0;text-align:${align};"><span style="${style}">${esc(line)}</span></p>`).join('\n');
    },
    image: (f) => {
      const url = toUrl(f.image);
      if (!url) return '';
      const w = Number(f.width) || 0;
      return imageSection(url, true, sp, { width: w > 0 ? w + 'px' : '100%' });
    },
    more_articles: (f) => {
      const parts = ['<section data-role="paragraph">'];
      const title = moreArticlesTitle(f, sp);
      if (title) parts.push(title);
      parts.push(MORE_CARDS_WRAPPER_OPEN + '</section>');
      parts.push('</section>');
      return parts.join('\n');
    },
  };
}

// 图片显示宽度（规范《五·图片》）：头图满宽 1411 → 100%；方形小图=二维码 → 固定 qr_size
// （默认 140×140）；其余横图/纵图保留 DOCX 显示宽度百分比。qr_size 走 config 可配。
function seriesImageWidth(e, isHead, config) {
  if (isHead) return '100%';
  const a = Number(e.aspect) || 0;
  const pct = parseInt(e.w, 10);
  if (a >= 0.85 && a <= 1.18 && Number.isFinite(pct) && pct <= 40) {
    return cfgNum(config, 'qr_size', 140) + 'px';
  }
  return e.w || '100%';
}

// 文本 span（内联字号/颜色/字体）。
function span(text, { size, color, bold, italic, sp }) {
  const s =
    `font-size: ${size}px; line-height: ${sp.lh}; color: ${color}; font-family: ${sp.font};` +
    (bold ? ' font-weight: bold;' : '') + (italic ? ' font-style: italic;' : '');
  return `<span style="${s}">${esc(text)}</span>`;
}

// runs → 内联 HTML（正文/参考/推荐条目）。link 渲成主色下划线。
function renderRuns(runs, { size, color, sp }) {
  return (runs || []).filter(r => r.type !== 'img').map(r => {
    if (r.type === 'txt') {
      return span(r.text, { size, color, bold: r.bold, sp });
    }
    if (r.type === 'link') {
      const h = r.href || '';
      const inner = span(r.text, { size, color: sp.link, sp });
      if (!HTTP_RE.test(h)) return inner;
      return `<a href="${escAttr(h)}" style="color: ${sp.link}; text-decoration: underline;">${inner}</a>`;
    }
    return '';
  }).join('');
}

function runsToPlain(runs) {
  return (runs || []).filter(r => r.type === 'txt' || r.type === 'link').map(r => r.text || '').join('');
}

// ── 表格（统一表格样式，内联；表头底色读 table_header_bg，系列默认 #02396b）──
function tableRunRenderers() {
  return {
    text(r) {
      if (r.type === 'txt') { const t = esc(r.text); return r.bold ? '<strong>' + t + '</strong>' : t; }
      return '';
    },
    link(r) {
      if (r.type === 'link') {
        const h = r.href || '';
        return HTTP_RE.test(h) ? `<a href="${escAttr(h)}">${esc(r.text)}</a>` : esc(r.text);
      }
      return '';
    },
  };
}
// 信息表（成品标准）：首行=标题文字+其余空 → 渲成跨整行的深蓝实心横幅（白字加粗居中、无内框）；
// 列宽按 DOCX gridCol 比例（左窄右宽）走 colgroup + table-layout:fixed；正文格细灰边框居中、多行。
function renderTable(elem, config) {
  const fs = cfgNum(config, 'table_font_size', 15);
  const padRaw = Number(config && config.table_cell_padding);
  const pad = Number.isFinite(padRaw) && padRaw >= 0 ? padRaw : 8;
  return renderSharedTable(elem, {
    headerBg: cfgColor(config, 'table_header_bg', '#02396b'),
    headerColor: cfgColor(config, 'table_header_text_color', '#ffffff'),
    borderColor: cfgColor(config, 'table_border_color', '#888888'),
    fontSize: fs,
    padding: pad,
    headerBold: !(config && config.table_header_bold === false),
    marginAuto: true,
    verticalAlignMiddle: true,
    useColgroup: true,
    bannerHeader: true,
    renderers: tableRunRenderers(),
  });
}

// 连续正文紧凑判定（红线#18）：移植 fbif.js 的同名口径（不跨渲染器 import——红线#19），
// 阈值 160 与 fbif 逐字一致。只作用于正文类块（p / ref-item）：Word 段距小、无空段、
// contextualSpacing 列表、同样式时不留 gap；原文有空段（blankBefore）则恢复标准间距。
function sameDocxStyle(a, b) {
  if (!a || !b) return false;
  if (a.styleId && b.styleId) return a.styleId === b.styleId;
  if (a.styleName && b.styleName) return a.styleName === b.styleName;
  return false;
}
function isCompactDocxPair(a, b) {
  if (!a || !b) return false;
  if ((a.k !== 'p' && a.k !== 'ref-item') || (b.k !== 'p' && b.k !== 'ref-item')) return false;
  if (!a.fromDocx || !b.fromDocx || b.blankBefore) return false;
  if (a.isList && b.isList) return true;
  if (sameDocxStyle(a, b) && (a.contextualSpacing || b.contextualSpacing)) return true;
  const after = Number(a.spacing && a.spacing.after);
  const before = Number(b.spacing && b.spacing.before);
  return Number.isFinite(after) && after <= 160 &&
    (!Number.isFinite(before) || before <= 160);
}

// ── 间距：默认每块后留 1 行空（gap）；紧凑成对例外不留 ──
function gapAfter(e, next) {
  if (!next) return true;
  if (e.k === 'img' && next.k === 'cap') return false;       // 图 → 图注紧贴
  if (e.k === 'cap' && next.k === 'cap') return false;       // 图注 → 图片来源紧贴
  if (e.k === 'ref-item' && next.k === 'ref-item') return false;
  if (e.k === 'rec-title') return false;                      // 段头紧贴首条
  if (e.k === 'rec-item' && next.k === 'rec-item') return false;
  if (e.k === 'rec-item' && next.k === 'rec-note') return false; // 说明行紧贴最后一条 ○
  if (e.k === 'toc-title') return false;                      // 「目录」紧贴首条
  if (e.k === 'toc-item' && next.k === 'toc-item') return false;
  if (e.k === 'footer-note' && next.k === 'footer-note') return false; // 文末署名 / 图源说明紧贴
  if (e.k === 'tip-title') return false;                      // 「提示：」紧贴首条声明
  if (e.k === 'tip-item' && next.k === 'tip-item') return false;
  if (isCompactDocxPair(e, next)) return false;               // 连续紧凑正文（红线#18）
  return true;
}

// ── 渲染主入口 ──
function render(elems, config = {}) {
  const sp = styleProps(config);
  const lines = [];
  let headImageDone = false; // 首个正文图 = 头图（满宽）；其后按 aspect/DOCX 宽度

  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    const next = i + 1 < elems.length ? elems[i + 1] : null;
    const gap = gapAfter(e, next);

    switch (e.k) {
      case 'h1': {
        // 序号图（前空行由上一块 gap 提供；序号图与文字间不空行）+ 居中标题文字（后空行）
        const imgUrl = sectionImageUrl(config, e.index);
        if (imgUrl) lines.push(imageSection(imgUrl, false, sp, { alt: e.text }));
        lines.push(section(
          span(e.text, { size: sp.h1Size, color: sp.heading, bold: true, sp }),
          { align: 'center', gap, sp }));
        break;
      }
      case 'h2':
        lines.push(section(
          span(e.text, { size: sp.h2Size, color: sp.heading, bold: true, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'h3':
        lines.push(section(
          renderRuns((e.runs || []).map(r => ({ ...r, bold: true })), { size: sp.bodySize, color: sp.text, sp }),
          { align: e.center ? 'center' : 'left', gap, sp }));
        break;
      case 'toc-title':
        lines.push(section(
          span('目录', { size: sp.tocTitleSize, color: sp.heading, bold: true, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'toc-item':
        lines.push(section(
          span(e.text, { size: sp.bodySize, color: sp.text, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'img': {
        // 头图满宽；方形小图=二维码 → 140；其余横图/纵图按 DOCX 显示宽度（见 seriesImageWidth）。
        const isHead = !headImageDone;
        headImageDone = true;
        lines.push(imageSection(e.src, gap, sp, { width: seriesImageWidth(e, isHead, config) }));
        break;
      }
      case 'cap':
        lines.push(section(
          span(e.text || runsToPlain(e.runs), { size: sp.capSize, color: sp.muted, sp }),
          { align: 'center', gap, sp }));
        break;
      case 'rec-title':
        lines.push(section(
          span(e.text || '相关阅读：', { size: sp.bodySize, color: sp.muted, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'rec-item': {
        // 空心圆项目符号 + 灰色、无超链接（stripLinks 已在分类层处理）
        const inner = '○ ' + runsToPlain(e.runs);
        lines.push(section(
          span(inner, { size: sp.bodySize, color: sp.muted, sp }),
          { align: 'left', gap, sp }));
        break;
      }
      case 'rec-note':
        // 规范《二·七》：推荐阅读块下方补充说明，14px 灰斜体左对齐（同作者/图源说明口径）。
        lines.push(section(
          span(e.text, { size: sp.attrSize, color: sp.muted, italic: true, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'ref-item':
        lines.push(section(
          renderRuns(e.runs, { size: sp.bodySize, color: sp.muted, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'footer-note':
        // 文末署名 / 图源说明：12px #7f7f7f 斜体小字（成品实测口径）。
        lines.push(section(
          span(e.text || runsToPlain(e.runs), { size: sp.noteSize, color: sp.noteColor, italic: true, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'tip-title':
        // 文末「提示：」标签：15px 深蓝粗体左对齐。
        lines.push(section(
          span(e.text || '提示：', { size: sp.bodySize, color: sp.heading, bold: true, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'tip-item':
        // 文末「* …」原创/转载声明条目：15px #888888 斜体左对齐。
        lines.push(section(
          span(e.text || runsToPlain(e.runs), { size: sp.bodySize, color: sp.muted, italic: true, sp }),
          { align: 'left', gap, sp }));
        break;
      case 'footer-img':
        lines.push(imageSection(e.src, e.gap !== false, sp));
        break;
      case 'table':
        lines.push(`<section style="margin: 0 0 ${gap ? sp.gap : 0}px; overflow-x: auto;">${renderTable(e, config)}</section>`);
        break;
      case 'p':
      default:
        lines.push(section(
          renderRuns(e.runs, { size: sp.bodySize, color: sp.text, sp }) || '<br>',
          { align: e.center ? 'center' : 'left', gap, sp }));
        break;
    }
  }
  return lines;
}

// ── DOCX 分类（共享中性层 + 系列口径）──
// consumeAuthorLine=false：系列文章无「作者：」前置行，正文从标题/副标题起。
// classifyReferences=false：参考来源是一级标题（「六、参考来源」），由 classifySeries 自己识别。
// 正文起点信号 + 扫描上限走 config.behavior.params（红线：规则后台可配），缺省降级系列常量。
function buildDocxOpts(config) {
  const params = (config && config.behavior && config.behavior.params) || {};
  const sig = (params.content_start_signal && params.content_start_signal.default) || SERIES_FRONTMATTER.content_start_signal;
  const limRaw = Number(params.front_matter_scan_limit && params.front_matter_scan_limit.default);
  const lim = Number.isFinite(limRaw) && limRaw > 0 ? limRaw : SERIES_FRONTMATTER.front_matter_scan_limit;
  return { consumeAuthorLine: false, classifyReferences: false, contentStartSignal: sig, frontMatterScanLimit: lim };
}

function processDocxInternal(paragraphs, imgCache, config) {
  const neutral = classifyDocxArticle(paragraphs || [], imgCache || {}, buildDocxOpts(config));
  const elems = classifySeries(neutral.elems, config);
  // 底部页脚不再 concat 进正文——改由 export default 的 renderBottomFooter() 经 engine footer 链路渲染。
  return {
    elems,
    imgN: elems.filter(e => e.k === 'img').length,
    headingN: elems.filter(e => e.k === 'h1' || e.k === 'h2' || e.k === 'h3').length,
    authorName: extractSeriesAuthorName(elems),
  };
}

export default {
  id: 'foodtalks-series',
  name: 'FoodTalks「什么是」系列排版',
  description: '按 FoodTalks「什么是」系列规范：序号图标题、深蓝主色、目录、推荐阅读、参考来源、尾部名片',
  formats: ['.docx', '.doc', '.md', '.txt'],

  processDocx({ paragraphs, imgCache, config = {} }) {
    const { elems, imgN, headingN, authorName } = processDocxInternal(paragraphs, imgCache, config);
    return { lines: render(elems, config), imgN, headingN, authorName };
  },

  async processMd(text, opts = {}) {
    const config = (opts && opts.config) || {};
    const fm = parseMdFrontmatter(text);
    const body = (fm && fm.content) || text;
    // 最小 MD 支持：按段落转 runs，交给同一分类/渲染（标题靠正文编号正则识别）。
    const paras = String(body).split(/\n\s*\n/).map(p => p.trim()).filter(Boolean);
    const neutral = paras.map(p => ({ k: 'txt', runs: parseMdRuns(p), text: p }));
    const elems = classifySeries(neutral, config);
    return {
      lines: render(elems, config),
      imgN: elems.filter(e => e.k === 'img').length,
      headingN: elems.filter(e => e.k === 'h1' || e.k === 'h2' || e.k === 'h3').length,
      authorName: extractSeriesAuthorName(elems),
      title: (fm && fm.title) || '',
    };
  },

  processBlocks(data, opts = {}) {
    const config = (opts && opts.config) || {};
    // URL 导入：把结构化块转成中性 elem（与 fbif 同口径），再走系列分类。
    const neutral = ((data && data.blocks) || []).map(b => {
      if (!b || !b.k) return null;
      if (b.k === 'img') return { k: 'img', src: b.src || '', w: '100%' };
      if (b.k === 'cap') return { k: 'cap', text: b.text || '' };
      if (b.k === 'table') return {
        k: 'table',
        rows: (b.rows || []).map(row => (row || []).map(c => ({ runs: convertRuns(c.runs || []), header: !!c.header }))),
      };
      if (b.k === 'h') return { k: 'h', text: b.text || '', runs: [{ type: 'txt', text: b.text || '' }] };
      return { k: 'txt', runs: convertRuns(b.runs || []) };
    }).filter(Boolean);
    const elems = classifySeries(neutral, config);
    return {
      lines: render(elems, config),
      imgN: elems.filter(e => e.k === 'img').length,
      headingN: elems.filter(e => e.k === 'h1' || e.k === 'h2' || e.k === 'h3').length,
      authorName: extractSeriesAuthorName(elems),
      title: (data && data.title) || '',
    };
  },

  // 整段底部由统一区块模型渲染（替代旧 buildSeriesFooterElems 的 body-concat）。
  // engine.loadBaseFooterHtml 接管时调用；bottom_blocks 缺失/禁用返回 null → fallback footerUrl
  // （系列 footerUrl='' → 空）。红线#19：seriesBlockRenderers 只在系列侧定义，engine 经此回调拿结果。
  renderBottomFooter(opts) {
    const ac = (typeof window !== 'undefined' && window._activeConfig) || {};
    let cfg = (ac.preset && (ac.preset.raw_config || ac.preset.config)) || ac.config || {};
    if (cfg && cfg.config && cfg.config.modules) cfg = cfg.config;
    const mods = (cfg && cfg.modules && typeof cfg.modules === 'object') ? cfg.modules : {};
    const bb = mods.bottom_blocks;
    if (!bb || typeof bb !== 'object' || bb.enabled === false) return null;
    const scope = (typeof window !== 'undefined' && window._activeRulePack && window._activeRulePack.id) || 'foodtalks-series';
    const ov = opts && Object.prototype.hasOwnProperty.call(opts, 'bottomBlocksOverride')
      ? opts.bottomBlocksOverride
      : ((typeof window !== 'undefined' && window._bottomBlocksOverride) || undefined);
    const sp = styleProps((ac.config && typeof ac.config === 'object') ? ac.config : {});
    // v2 双预设：来源决定原创/转载（v1 模块忽略 presetId，行为不变）。
    const presetId = (opts && opts.isRepost) ? 'repost' : 'original';
    const html = renderBottomBlocksHtml(bb, ov, seriesBlockRenderers(sp, mods.asset_library, scope), { presetId });
    return { html, isV2: Number(bb.schema_version) >= 2 }; // isV2：engine 据此对 v2 跳过 applyRepostTransform（codex #4）
  },
};
