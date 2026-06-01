// FBIF 公众号排版模板 — CSS class-based rendering
const assetQuery = new URL(import.meta.url).search;
const engineModule = await import('../engine.js' + assetQuery);
const { esc, escAttr, parseMdRuns, parseMdFrontmatter, looksLikeGifSource, convertRuns } = engineModule;
const ruleModule = await import('../rule-presets.js' + assetQuery);
const { getActiveRuleConfig, getActiveRuleBehavior, getRuleNumber } = ruleModule;
const behaviorModule = await import('../rule-behavior.js' + assetQuery);
const { applyBehaviorToElems, normalizeAuthorName, renderBehaviorTemplate } = behaviorModule;
// DOCX 内容结构分类已抽到共享模块（公众号与 FoodTalks 网站共用一套，红线#19：
// 两渲染器不互相 import，都只 import 本数据模块）。公众号走默认 opts（consumeAuthorLine
// / classifyReferences 均为 true），输出与抽取前逐字节一致（golden tea-pyramid 守卫）。
const docxModelModule = await import('../docx-article-model.js' + assetQuery);
const { classifyDocxArticle } = docxModelModule;

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

function activeBehaviorAccount() {
  const pack = typeof window !== 'undefined' ? window._activeRulePack : null;
  return {
    name: (pack && pack.behaviorAccountName) || 'FBIF',
    id: (pack && pack.id) || 'fbif',
  };
}

// 读 behavior.params.<key>.default 编译为正则（红线下放：公众号识别正则规则后台可配；
// 读不到/非法时降级到传入的 fallback RegExp const）。识别开头 (?i) 内联 flag —— 服务端
// 用 RE2 (?i) 表达大小写不敏感，JS new RegExp 不支持内联 flag 组，这里转为构造函数 flag。
function behaviorRegexParam(key, fallbackRe) {
  try {
    const params = getActiveRuleBehavior().params || {};
    const src = params[key] && params[key].default;
    if (typeof src === 'string' && src) {
      let s = src, flags = '';
      const m = s.match(/^\(\?([a-z]+)\)/);
      if (m) { flags = m[1].replace(/[^gimsuy]/g, ''); s = s.slice(m[0].length); }
      return new RegExp(s, flags);
    }
  } catch (e) { /* 降级到 const */ }
  return fallbackRe;
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

function renderRuns(runs, opts) {
  const textCls = (opts && opts.textCls) || 'wx-t';
  return runs.filter(r => r.type !== 'img').map(r => {
    if (r.type === 'txt') {
      const cls = r.bold ? textCls + ' wx-bold' : textCls;
      return leafWrap('<span textstyle="" class="' + cls + '">' + esc(r.text) + '</span>');
    }
    if (r.type === 'link') {
      const h = (r.href || '');
      if (!/^https?:\/\//i.test(h)) return leafWrap(esc(r.text));
      return leafWrap('<a class="wx-a" href="' + escAttr(h) + '">' + esc(r.text) + '</a>');
    }
    return '';
  }).join('');
}

// ---- Spacing & Section Helpers ----
function sec(inner, gap) {
  return '<section class="wx-p' + (gap ? ' wx-gap' : '') + '">' + inner + '</section>';
}
function secCenter(inner, gap) {
  return '<section class="wx-pc' + (gap ? ' wx-gap' : '') + '">' + inner + '</section>';
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

function shouldSpaceAfter(e, next) {
  // No `next` means this is the article body's tail. The FBIF footer is
  // concatenated afterwards with `margin:0;padding:0;` on its outer section,
  // so without `wx-gap` here the last paragraph (often a [n] reference) sits
  // flush against the 「提示：」 line. Emit the standard 20px gap.
  if (!next) return true;
  if (next.k === 'cap') return false;
  if (e.k === 'ref' && next.k === 'ref') return false;
  if (e.k === 'attr' && next.k === 'attr') return false;
  if (e.k === 'img' && (next.k === 'cap' || next.k === 'img')) return false;
  if (isCompactDocxPair(e, next)) return false;
  return true;
}

// DOCX 前置「作者：」行识别正则默认值（红线下放 behavior.params.docx_author_line_regex）。
const RE_DOCX_AUTHOR_LINE = /^作者[：:]\s*(.+)$/;

// ---- DOCX Classification ----
// 内容结构分类下沉到共享模块 docx-article-model.js（与 FoodTalks 网站共用一套）。
// 公众号走默认 opts：consumeAuthorLine=true（吃掉作者行、顶部单独渲染）、
// classifyReferences=true（分类 refH/ref）。正则仍由本模板从全局 behavior 解析后注入
// （规则源无关边界）；ref_heading_prefix_regex 是把旧 line 224 写死正则下放为可配项，
// 缺省 fallback 与旧值逐字一致——故公众号输出逐字节不变（golden tea-pyramid 守卫）。
function classifyDocx(paragraphs, imgCache) {
  return classifyDocxArticle(paragraphs, imgCache, {
    authorLineRegex: behaviorRegexParam('docx_author_line_regex', RE_DOCX_AUTHOR_LINE),
    refSectionRegex: behaviorRegexParam('ref_section_regex', RE_REF_SECTION),
    refHeadingPrefixRegex: behaviorRegexParam('ref_heading_prefix_regex', /^(参考(来源|文献|资料|链接)|信息来源)/),
  });
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

// ---- HTML Rendering (class-based) ----
// opts.isRepost — 转载路径保持微信原色（var(--wx-link-color)）；原创路径
// 给作者行加 wx-ta-author，覆盖为 #396FBC（FBIF 视觉规范）。
function render(elems, author, source, opts) {
  const isRepost = !!(opts && opts.isRepost);
  const behavior = getActiveRuleBehavior();
  const context = behaviorContext(author, source);
  elems = applyBehaviorToElems(elems, context, behavior);
  const lines = [];

  // Author and source lines at the top
  if (author) {
    const authorCls = isRepost ? 'wx-ta' : 'wx-ta wx-ta-author';
    lines.push(sec(leafWrapAuthor('<span textstyle="" class="' + authorCls + '">' + esc(attributionText('作者', author, context, behavior)) + '</span>'), !source));
  }
  if (source) {
    lines.push(sec(leafWrapAuthor('<span textstyle="" class="wx-ta">' + esc(attributionText('来源', source, context, behavior)) + '</span>'), true));
  }

  for (let i = 0; i < elems.length; i++) {
    const e = elems[i];
    const next = i + 1 < elems.length ? elems[i + 1] : null;
    const spaceAfter = shouldSpaceAfter(e, next);

    switch (e.k) {
      case 'h':
        lines.push(sec(leafWrap('<span textstyle="" class="wx-th">' + esc(e.text) + '</span>'), spaceAfter));
        break;
      case 'img': {
        const src = e.src || '';
        const isGif = e.gif || looksLikeGifSource(src);
        const rpol = src.startsWith('http') ? ' referrerpolicy="no-referrer"' : '';
        const gifAttr = isGif ? ' data-type="gif"' : '';
        const sizeStyle = e.maxPx
          ? 'max-width: ' + e.maxPx + 'px; width: 100%; height: auto; display: block; margin: 0 auto; vertical-align: top;'
          : 'width: ' + e.w + '; max-width: 100%; height: auto; display: block; margin: 0 auto; vertical-align: top;';
        lines.push('<section class="wx-pi' + (spaceAfter ? ' wx-gap' : '') + '">' +
          '<img src="' + escAttr(src) + '"' + gifAttr + rpol + ' style="' + sizeStyle + '" /></section>');
        break;
      }
      case 'cap':
        lines.push(secCenter(leafWrap('<span textstyle="" class="wx-tc">' + esc(e.text) + '</span>'), spaceAfter));
        break;
      case 'txt':
        lines.push(sec(renderRuns(e.runs), spaceAfter));
        break;
      case 'attr':
        lines.push(sec(leafWrapAuthor('<span textstyle="" class="wx-ta">' + esc(attributionText(e.role, e.name, context, behavior)) + '</span>'), spaceAfter));
        break;
      case 'refH': {
        const refHeadText = (e.text || '参考来源').replace(/[：:]?$/, '：');
        lines.push(sec(leafWrap('<span textstyle="" class="wx-tr">' + esc(refHeadText) + '</span>'), false));
        break;
      }
      case 'ref':
        lines.push(sec(renderRuns(e.runs, { textCls: 'wx-tr' }), spaceAfter));
        break;
      case 'bq':
        lines.push('<section class="wx-bq' + (spaceAfter ? ' wx-gap' : '') + '">' +
          renderRuns(e.runs, { textCls: 'wx-tr' }) + '</section>');
        break;
    }
  }
  return lines;
}

// ---- Blocks Classification (URL-import path) ----
// Input is already structurally classified by the Go DOM walker, so this
// step is light: apply punctuation conversion to runs, promote attribution
// paragraphs to 'attr' blocks, and track 参考文献/信息来源 sections. No
// visual-heuristic role-guessing — that was the whole point of moving to
// structured blocks. See api/wechat_blocks.go for the upstream contract.
function classifyBlocks({ title, blocks }) {
  const elems = [];
  const attrs = [];
  let imgN = 0, inRef = false;
  const inputBlocks = blocks || [];

  for (let blockIndex = 0; blockIndex < inputBlocks.length; blockIndex++) {
    const b = inputBlocks[blockIndex];
    if (!b || !b.k) continue;

    if (b.k === 'img') {
      const w = Number(b.dataW || 0);
      const maxPx = w > 0 && w < ruleNumber('decorative_image_max_px') ? w : 0;
      imgN++;
      inRef = false;
      elems.push({ k: 'img', src: b.src || '', w: '100%', maxPx, gif: looksLikeGifSource(b.src || '') });
      continue;
    }

    if (b.k === 'cap') {
      elems.push({ k: 'cap', text: b.text || '' });
      continue;
    }

    if (b.k === 'h') {
      const ht = (b.text || '').trim();
      if (/^(参考(来源|文献|资料|链接)|信息来源)/.test(ht)) {
        inRef = true;
        elems.push({ k: 'refH', text: ht.replace(/[：:].*$/, '').trim() });
      } else {
        inRef = false;
        elems.push({ k: 'h', text: ht });
      }
      continue;
    }

    // txt / bq / li — all carry runs. Apply punctuation pass.
    const runs = convertRuns(b.runs || []);
    // Text used for attribution / 参考文献 regex matching. Include both
    // txt and link run types so "作者：<a>张三</a>" still yields
    // "作者：张三" (link runs carry display text, not the URL).
    const text = runs
      .filter(r => r && (r.type === 'txt' || r.type === 'link'))
      .map(r => r.text || '')
      .join('')
      .trim();

    if (attrs.length && isFooterPromptText(text) && isStandardFooterPrompt(inputBlocks, blockIndex)) {
      break;
    }

    if (b.k === 'bq') {
      inRef = false;
      elems.push({ k: 'bq', runs });
      continue;
    }

    // URL imports use the standard FBIF footer, so article-level attribution
    // is collected and emitted once at the article tail before that footer.
    const attr = parseAttribution(text);
    if (attr) {
      pushUniqueAttribution(attrs, attr);
      inRef = false;
      continue;
    }

    // Reference section marker. Text-level, not style-level — same rule as MD path.
    if (behaviorRegexParam('ref_section_regex', RE_REF_SECTION).test(text)) {
      inRef = true;
      elems.push({ k: 'refH', text: text.replace(/[：:]$/, '') });
      continue;
    }

    if (inRef) {
      elems.push({ k: 'ref', runs });
      continue;
    }

    // li and txt render the same (FBIF template has no dedicated list style);
    // collapsing them here keeps the renderer's switch simple.
    elems.push({ k: 'txt', runs });
  }

  return { elems: elems.concat(attrs), author: '', source: '', title: title || '', imgN };
}

export default {
  id: 'fbif',
  name: 'FBIF 公众号排版',
  description: '按 FBIF 排版规范格式化，支持标题/引言/图片说明/参考来源',
  formats: ['.docx', '.doc', '.md', '.txt'],

  processDocx({ paragraphs, imgCache, isRepost, config: _config }) {
    // config 当前不消费 —— 仅为接口兼容，不破坏公众号现有行为
    const { elems, author, imgN } = classifyDocx(paragraphs, imgCache);
    return { lines: render(elems, author, '', { isRepost }), imgN, headingN: elems.filter(e => e.k === 'h').length };
  },
  async processMd(text, { config: _config, ...opts } = {}) {
    // config 当前不消费 —— 仅为接口兼容，不破坏公众号现有行为
    const { elems, author, source, title, imgN } = await classifyMd(text);
    return { lines: render(elems, author, source, opts), imgN, headingN: elems.filter(e => e.k === 'h').length, title };
  },
  processBlocks(data, { config: _config, ...opts } = {}) {
    // config 当前不消费 —— 仅为接口兼容，不破坏公众号现有行为
    const { elems, author, source, title, imgN } = classifyBlocks(data);
    return { lines: render(elems, author, source, opts), imgN, headingN: elems.filter(e => e.k === 'h').length, title };
  },
};
