// mp-blocks.js — 微信公众号渠道的底部区块渲染器（type → HTML）。
//
// 红线 #19：公众号渲染器与网站渲染器物理隔离——本文件不 import foodtalks-site.js / site-blocks.js，
// 只 import 中性的 asset-library（{{asset#id}} 占位符解析，兼容旧 {{asset:usage}}）。输出自包含 inline 样式 section。
//
// 各 type 的 inline style 字节级复刻 public/footer.html（FBIF 公众号成品页脚）：
//   - 可配字段（文字/字号/颜色/居中/图）填入；固定排版（font-family / 间距 / 661px）写在模板常量；
//   - 颜色字段（hex）渲染为 rgb(r, g, b)，与 footer.html 口径一致；
//   - more_articles 块只输出「标题 + 空卡片容器（带 mergeIntoFooter 锚点）」，卡片由现有
//     more-articles.js 复制链路委托填充（红线 #9/#14，不在此重复造卡）。

import { resolveAssetRefs } from '../asset-library.js';

// footer.html 标题/正文统一字体栈（mp-quote 优先 + 微信端字体回退）。&quot; 是 footer 原始转义形态，保留。
const FONT_FAMILY = 'mp-quote, &quot;PingFang SC&quot;, system-ui, -apple-system, BlinkMacSystemFont, &quot;Helvetica Neue&quot;, &quot;Hiragino Sans GB&quot;, &quot;Microsoft YaHei UI&quot;, &quot;Microsoft YaHei&quot;, Arial, sans-serif';
// 满宽图样式（661px = 微信正文宽），复刻 footer.html 的名片/引流图 <img>。
const IMG_FULL = 'display:block;width:661px !important;max-width:100% !important;height:auto !important;margin:0 auto;vertical-align:top;box-sizing:border-box;visibility:visible !important;';
// 更多文章卡片外层容器开标签——必须与 more-articles.js 的 OUTER_WRAPPER_OPEN 字节一致（委托锚点）。
const CARDS_WRAPPER_OPEN = '<section style="text-align: center;margin-left: 8px;margin-right: 8px;">';

function asLines(v) {
  if (Array.isArray(v)) return v;
  if (v == null || v === '') return [];
  return [String(v)];
}

function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// hex 颜色 → rgb(r, g, b)（footer.html 口径）。非 hex（已是 rgb()/具名色）原样返回。
function colorToRgb(v) {
  const m = /^#?([0-9a-fA-F]{6})$/.exec(String(v == null ? '' : v).trim());
  if (!m) return String(v == null ? '' : v);
  const n = parseInt(m[1], 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
}

function intOr(v, fallback) {
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? Math.round(n) : fallback;
}

function alignOr(v, fallback = 'left') {
  return ['left', 'center', 'right'].includes(v) ? v : fallback;
}

// 栏目标题 span 样式（声明标题 / 更多文章 / 1000案例 共用，color/size 可配）。
function titleSpanStyle(color, size) {
  return `color:${colorToRgb(color)};font-size:${size}px;font-family:${FONT_FAMILY};line-height:1.75;letter-spacing:0.034em;font-style:normal;font-weight:normal;`;
}

function textBlockSpanStyle(f) {
  const bg = String(f.background_color == null ? '' : f.background_color).trim();
  return `color:${colorToRgb(f.text_color || '#544545')};font-size:${intOr(f.font_size, 15)}px;font-family:${FONT_FAMILY};line-height:1.75;letter-spacing:0.034em;font-style:${f.italic ? 'italic' : 'normal'};font-weight:normal;text-decoration:${f.underline ? 'underline' : 'none'};${bg ? `background-color:${colorToRgb(bg)};` : ''}`;
}

// 名片/二维码上方说明文字 span 样式（可空，color/size 可配）。
function captionSpanStyle(color, size) {
  return `color:${colorToRgb(color)};font-size:${size}px;font-family:${FONT_FAMILY};line-height:1.75;letter-spacing:0.034em;font-style:normal;font-weight:normal;`;
}

// 满宽图 section（margin 由各块给定，复刻 footer.html 精确块间距）。
function fullImageSection(margin, url) {
  return `<section style="margin:${margin};padding:0;text-align:center;line-height:0;font-size:0;"><img alt="图片" class="rich_pages wxw-img" style="${IMG_FULL}" src="${url}"></section>`;
}

// 居中开关 → text-align 片段（footer.html 标题均居中；center=false 时去掉）。
function centerCss(center) {
  return center === false ? '' : 'text-align:center;';
}

// mpBlockRenderers 返回公众号渠道的「type → 渲染函数」表。
//   assetLibrary: config.modules.asset_library（解析 {{asset#id}}）
//   scope: 资产作用域，公众号用账号 id（如 'fbif'）；缺省 'global'
export function mpBlockRenderers(assetLibrary, scope = 'global') {
  const resolve = (lines) => resolveAssetRefs(lines, assetLibrary, scope).join('\n');
  return {
    __separator: () => '<p style="margin:0 8px;padding:0;line-height:1.75;font-size:15px;"><br></p>',

    // 文字模块：基础样式字段 → 确定性 inline HTML，避免引入富文本库污染微信复制。
    text: (f) => {
      const lines = String(f.content == null ? '' : f.content).split(/\r?\n/).map(s => s.trim()).filter(Boolean);
      if (!lines.length) return '';
      const align = alignOr(f.align);
      const style = textBlockSpanStyle(f);
      return lines.map(line => `<p style="margin:0 8px;padding:0;text-align:${align};"><span style="${style}">${esc(line)}</span></p>`).join('\n');
    },

    // 自定义内容：html_lines 原样输出（历史段无损迁移 / 高级兜底）。
    rich_html: (f) => resolve(asLines(f.html_lines)),

    // 声明/提示：paragraph 外层 + 标题（固定 17px 深褐、左对齐）+ 条目行（原样，含转载锚点）。
    declaration: (f) => {
      const parts = ['<section data-role="paragraph" style="margin:0;padding:0;">'];
      const title = String(f.title == null ? '' : f.title).trim();
      if (title) {
        parts.push(`<section style="margin:0 8px 8px;padding:0;"><span style="${titleSpanStyle('#544545', 17)}"><strong>${esc(title)}</strong></span></section>`);
      }
      for (const ln of asLines(f.lines)) parts.push(String(ln == null ? '' : ln));
      parts.push('</section>');
      return resolve(parts);
    },

    // 名片：上方文字（可空）+ 满宽名片图。复刻 FBIF 波波名片（无文字，图 margin:16px 8px 0）。
    name_card: (f) => {
      const lines = [];
      const text = String(f.intro_text == null ? '' : f.intro_text).trim();
      if (text) {
        lines.push(`<section style="margin:0 8px 12px;padding:0;text-align:center;"><span style="${captionSpanStyle(f.intro_color || '#888888', intOr(f.intro_size, 14))}">${esc(text)}</span></section>`);
      }
      const img = String(f.image == null ? '' : f.image).trim();
      if (img) lines.push(fullImageSection('16px 8px 0', img));
      return resolve(lines);
    },

    // 二维码卡：说明（可空）+ 满宽二维码图。
    qr_card: (f) => {
      const lines = [];
      const cap = String(f.caption == null ? '' : f.caption).trim();
      if (cap) {
        lines.push(`<section style="margin:0 8px 12px;padding:0;text-align:center;"><span style="${captionSpanStyle(f.caption_color || '#888888', intOr(f.caption_size, 14))}">${esc(cap)}</span></section>`);
      }
      const img = String(f.image == null ? '' : f.image).trim();
      if (img) lines.push(fullImageSection('16px 8px 0', img));
      return resolve(lines);
    },

    // 标题+图片：栏目标题（文字/字号/颜色/居中可配）+ 满宽图。复刻 1000案例（标题 margin:20px、图 margin:0 8px 0）。
    titled_media: (f) => {
      const parts = [];
      const title = String(f.title == null ? '' : f.title).trim();
      if (title) {
        parts.push('<section data-role="paragraph" style="margin:0;padding:0;">');
        parts.push(`<section style="margin:20px 8px 20px;padding:0;${centerCss(f.center)}"><span style="${titleSpanStyle(f.title_color || '#544545', intOr(f.title_size, 17))}"><strong>${esc(title)}</strong></span></section>`);
        parts.push('</section>');
      }
      const img = String(f.image == null ? '' : f.image).trim();
      if (img) parts.push(fullImageSection('0 8px 0', img));
      return resolve(parts);
    },

    // 更多文章：标题 + 空卡片容器（带 mergeIntoFooter 锚点）。卡片由现有 more-articles 复制链路委托填充。
    // paragraph 外层【无 style】——必须与 more-articles.js 的 PARAGRAPH_WRAPPER_OPEN 字节一致。
    more_articles: (f) => {
      const title = String(f.title == null ? '' : f.title).trim();
      const parts = ['<section data-role="paragraph">'];
      if (title) {
        parts.push(`<section style="margin:20px 8px 20px;padding:0;${centerCss(f.center)}"><span style="${titleSpanStyle(f.title_color || '#544545', intOr(f.title_size, 17))}"><strong>${esc(title)}</strong></span></section>`);
      }
      parts.push(CARDS_WRAPPER_OPEN + '</section>');
      parts.push('</section>');
      return parts.join('\n'); // 纯结构、无 {{asset}}：保持锚点字节精确
    },

    // 单图：满宽图（宽度/居中可配）。
    image: (f) => {
      const img = String(f.image == null ? '' : f.image).trim();
      if (!img) return '';
      const w = intOr(f.width, 0);
      const widthCss = w > 0 ? `width:${w}px !important;max-width:100% !important;` : 'width:100% !important;max-width:100% !important;';
      const centerCssImg = f.center === false ? '' : 'margin:0 auto;';
      const style = `display:block;${widthCss}height:auto !important;${centerCssImg}vertical-align:top;box-sizing:border-box;`;
      return resolve([`<section style="margin:0 8px 0;padding:0;text-align:center;line-height:0;font-size:0;"><img alt="图片" class="rich_pages wxw-img" style="${style}" src="${img}"></section>`]);
    },
  };
}
