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
const { applyFoodTalksBehavior, FBIF_FOOTER_HTML } = behaviorModule;
// 标题识别（detectHeadingLevel / isBoldHeading / promoteHeuristicHeadings）抽到无依赖
// 的 heading-detect.js，便于真实单元测试（见 tests/heading-detect.test.mjs）。
const headingModule = await import('../heading-detect.js' + assetQuery);
const { promoteHeuristicHeadings } = headingModule;

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

function hasImageCaptionKeyword(text) {
  if (!text) return false;
  const lower = String(text).toLowerCase();
  for (const kw of IMAGE_CAPTION_KEYWORDS) {
    if (text.indexOf(kw) >= 0) return true;
    if (kw.length <= 8 && lower.indexOf(kw.toLowerCase()) >= 0) return true;
  }
  return false;
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

function isImageCaption(text) {
  const plain = stripHtml(text || '').trim();
  if (!plain || plain.length > 120) return false;
  if (hasImageCaptionKeyword(plain)) return true;
  if (/^(图|图注|图片|上图|下图|表|资料来源|数据来源|图片来源|来源|注|Source|Photo|Image|Credit)\s*[：:\s\d]/i.test(plain)) {
    return true;
  }
  if (/^[▲△▵▽▼]/.test(plain)) return true;
  const sentenceEnd = (plain.match(/[。！？!?]/g) || []).length;
  const commaCount = (plain.match(/[，,；;]/g) || []).length;
  return plain.length <= 45 && sentenceEnd === 0 && commaCount <= 1;
}

// ---- 输出基本元 ----

const BLANK_LINE = '<p style="text-align: justify;"><br></p>';

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

// 极端狭长图自适应宽度（参考 /tmp/foodtalks-research/FoodtalksArticleLayout/layout-tool.html:2808-2818）
function adaptiveImageWidth(naturalWidth, naturalHeight) {
  const w = Number(naturalWidth || 0);
  const h = Number(naturalHeight || 0);
  if (!w || !h) return 600;
  const ratio = h / w;
  if (ratio >= 3) return 280;
  if (ratio >= 2.2) return 360;
  if (ratio >= 1.6) return 480;
  return 600;
}

function renderImage(src, alt, naturalWidth, naturalHeight) {
  const safeSrc = escAttr(src || '');
  const safeAlt = escAttr(alt || '');
  const width = adaptiveImageWidth(naturalWidth, naturalHeight);
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
  return (
    `<p style="text-align: center;">` +
    `<span style="font-size: 12px; color: ${color};">${esc(text)}</span>` +
    `</p>`
  );
}

// 把图片后 3 段内最多 2 段"看起来像图注"的普通段提升为图注块
function promoteImageCaptions(elems) {
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
        promote = isImageCaption(plain);
      } else {
        promote = hasImageCaptionKeyword(plain);
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
      if (!prevWasHeading && lines.length > 0) lines.push(BLANK_LINE);
      lines.push(renderHeading(e.level === 3 ? 3 : 2, runsToPlain(e.runs), config));
      prevWasHeading = true;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'author') {
      const authorColor = (config && config.muted_color) || '#888888';
      lines.push(
        '<p style="text-align: justify;">' +
        `<span style="font-size: 14px;"><span style="color: ${authorColor};"><em>` +
        esc(e.text) +
        '</em></span></span></p>'
      );
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'tip') {
      const fullText = runsToPlain(e.runs);
      // 把"提示："这 3 个字符切出来加蓝色，剩余文本保持普通色
      const tipHead = fullText.match(/^(\s*提示[：:])(.*)$/);
      const head = tipHead ? tipHead[1] : '提示：';
      const rest = tipHead ? tipHead[2] : fullText;
      lines.push(
        '<p style="text-align: justify;">' +
        '<strong><span style="color: #01396a;"><em><span style="font-size: 14px;">' +
        esc(head) +
        '</span></em></span></strong>' +
        esc(rest) +
        '</p>'
      );
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'img') {
      lines.push(renderImage(e.src, e.alt, e.width, e.height));
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'cap') {
      lines.push(renderImageCaption(runsToPlain(e.runs), config));
      prevWasHeading = false;
      prevWasStandaloneLink = false;
      continue;
    }
    if (e.k === 'ref-header') {
      const refColor = (config && config.muted_color) || '#888888';
      lines.push(`<p style="text-align: justify;"><span style="color: ${refColor};">参考来源：</span></p>`);
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
  // FBIF 公众号原创页脚（可选输出）
  const fbifFooterEnabled = !!(
    config.modules &&
    config.modules.fbif_footer &&
    config.modules.fbif_footer.enabled
  );
  if (fbifFooterEnabled) {
    lines.push(BLANK_LINE);
    lines.push(FBIF_FOOTER_HTML);
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

// ---- DOCX 分类（M1 最小版） ----

// 找到首个"作者：" / "Author:" 段，剥离该段之前的所有前置材料。无匹配返回原数组。
function stripDocxPreambleBeforeAuthor(paragraphs) {
  if (!Array.isArray(paragraphs)) return paragraphs;
  const authorRegex = /^[\s　]*(作者|Author)\s*[:：]/i;
  for (let i = 0; i < paragraphs.length; i++) {
    const text = (paragraphs[i].runs || [])
      .filter(r => r.type === 'txt')
      .map(r => r.text)
      .join('');
    if (authorRegex.test(text)) {
      return paragraphs.slice(i);
    }
  }
  return paragraphs;
}

function classifyDocx(paragraphs, imgCache) {
  paragraphs = stripDocxPreambleBeforeAuthor(paragraphs || []);
  const elems = [];
  for (const pd of paragraphs) {
    const runs = pd.runs || [];
    const imgRun = runs.find(r => r.type === 'img');
    if (imgRun) {
      const src = imgCache && imgCache[imgRun.id] ? imgCache[imgRun.id] : (imgRun.src || '');
      elems.push({
        k: 'img',
        src,
        alt: imgRun.alt || '',
        width: imgRun.width || 0,
        height: imgRun.height || 0,
      });
      continue;
    }
    const text = runs.filter(r => r.type === 'txt').map(r => r.text).join('').trim();
    if (!text) continue;
    const styleName = (pd.styleName || '').toLowerCase();
    if (/heading\s*1|heading\s*2/.test(styleName)) {
      elems.push({ k: 'h', level: 2, runs });
      continue;
    }
    if (/heading\s*[3-6]/.test(styleName)) {
      elems.push({ k: 'h', level: 3, runs });
      continue;
    }
    elems.push({ k: 'p', runs });
  }
  return { elems, imgN: elems.filter(e => e.k === 'img').length };
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
      elems.push({
        k: 'img',
        src: b.src || '',
        alt: '',
        width: Number(b.dataW || 0) || 0,
        height: 0,
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

  processDocx({ paragraphs, imgCache, config = {}, isRepost = false }) {
    const { elems, imgN } = classifyDocx(paragraphs, imgCache);
    const behaved = applyFoodTalksBehavior(elems, config, { isRepost });
    return {
      lines: render(promoteImageCaptions(promoteHeuristicHeadings(behaved)), config),
      imgN,
      headingN: behaved.filter(e => e.k === 'h').length,
    };
  },

  async processMd(text, opts) {
    const config = (opts && opts.config) || {};
    const { elems, imgN, title } = await classifyMd(text);
    const behaved = applyFoodTalksBehavior(elems, config, { isRepost: !!(opts && opts.isRepost), title });
    return {
      lines: render(promoteImageCaptions(promoteHeuristicHeadings(behaved)), config),
      imgN,
      headingN: behaved.filter(e => e.k === 'h').length,
      title,
    };
  },

  processBlocks(data, opts) {
    const config = (opts && opts.config) || {};
    const { elems, imgN, title } = classifyBlocks(data);
    const behaved = applyFoodTalksBehavior(elems, config, { isRepost: !!(opts && opts.isRepost), title });
    return {
      lines: render(promoteImageCaptions(promoteHeuristicHeadings(behaved)), config),
      imgN,
      headingN: behaved.filter(e => e.k === 'h').length,
      title,
    };
  },
};
