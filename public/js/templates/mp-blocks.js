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
import { richTextToInline } from '../blocks/rich-text-render.js';

// footer.html 标题/正文统一字体栈（mp-quote 优先 + 微信端字体回退）。&quot; 是 footer 原始转义形态，保留。
const FONT_FAMILY = 'mp-quote, &quot;PingFang SC&quot;, system-ui, -apple-system, BlinkMacSystemFont, &quot;Helvetica Neue&quot;, &quot;Hiragino Sans GB&quot;, &quot;Microsoft YaHei UI&quot;, &quot;Microsoft YaHei&quot;, Arial, sans-serif';
// 更多文章卡片外层容器开标签。data-card-inset 声明唯一横向缩进所有者，
// more-articles.js 合成卡片时把它落到卡片 section，避免外层/内层双缩进。
const CARDS_WRAPPER_OPEN = '<section style="text-align: center;" data-card-inset="8">';

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

// 栏目标题 span 样式（声明标题 / 更多文章共用，color/size 可配）。
function titleSpanStyle(color, size) {
  return `color:${colorToRgb(color)};font-size:${size}px;font-family:${FONT_FAMILY};line-height:1.75;letter-spacing:0.034em;font-style:normal;font-weight:normal;`;
}

function textBlockSpanStyle(f) {
  const bg = String(f.background_color == null ? '' : f.background_color).trim();
  return `color:${colorToRgb(f.text_color || '#544545')};font-size:${intOr(f.font_size, 15)}px;font-family:${FONT_FAMILY};line-height:1.75;letter-spacing:0.034em;font-style:${f.italic ? 'italic' : 'normal'};font-weight:${f.bold ? 'bold' : 'normal'};text-decoration:${f.underline ? 'underline' : 'none'};${bg ? `background-color:${colorToRgb(bg)};` : ''}`;
}

// 居中开关 → text-align 片段（footer.html 标题均居中；center=false 时去掉）。
function centerCss(center) {
  return center === false ? '' : 'text-align:center;';
}

// mpBlockRenderers 返回公众号渠道的「type → 渲染函数」表。
//   assetLibrary: config.modules.asset_library（解析 {{asset#id}}）
//   scope: 资产作用域，公众号用账号 id（如 'fbif'）；缺省 'global'
export function mpBlockRenderers(assetLibrary, scope = 'global', opts = {}) {
  const resolve = (lines) => resolveAssetRefs(lines, assetLibrary, scope).join('\n');
  // P2 富文本：parseDoc 注入（生产=浏览器 DOMParser；测试=linkedom）；mpCh 提供 mp 口径样式。
  const parseDoc = opts.parseDoc || ((html) => {
    if (typeof DOMParser === 'undefined') return null;
    const d = new DOMParser().parseFromString(html, 'text/html');
    return d.body || d; // 内容容器（childNodes 语义对齐测试侧 linkedom document）
  });
  const fsOk = (v) => {
    const m = /^(\d+(?:\.\d+)?)(px|em|rem|%|pt)$/.exec(String(v || ''));
    if (!m) return false;
    if (m[2] === 'px') { const n = Number(m[1]); return n >= 6 && n <= 100; } // px 范围防滥用（codex P2 review #3）
    return true;
  };
  const mpSpan = (s) => {
    const parts = [
      `color:${colorToRgb(s.color || '#544545')}`,
      `font-size:${fsOk(s['font-size']) ? s['font-size'] : '15px'}`,
      `font-family:${FONT_FAMILY}`,
      'line-height:1.75', 'letter-spacing:0.034em',
      `font-style:${s['font-style'] === 'italic' ? 'italic' : 'normal'}`,
      `font-weight:${s['font-weight'] === 'bold' ? 'bold' : 'normal'}`,
      `text-decoration:${s['text-decoration'] === 'underline' ? 'underline' : 'none'}`,
    ];
    if (s['background-color']) parts.push(`background-color:${colorToRgb(s['background-color'])}`);
    return parts.join(';') + ';';
  };
  const mpCh = {
    span: (s, inner) => `<span style="${mpSpan(s)}">${inner}</span>`,
    link: (href, s, inner) => `<a href="${esc(href)}" style="${mpSpan(s)}">${inner}</a>`,
    pWrap: (align, inner) => `<p style="margin:0 8px;text-align:${align};padding:0;">${inner}</p>`,
  };
  return {

    // 间距模块（v2 原子）：定高留白段，精确控制块间距（取代 v1 的自动空行）。
    // height<=0 不输出（=运营删掉该间距）。line-height:0;font-size:0 让段高精确等于 height。
    spacer: (f) => {
      let h = Math.round(Number(f.height));
      if (!Number.isFinite(h) || h <= 0) return '';
      if (h > 200) h = 200; // 与 catalog schema(0..200) 一致，防脏本地数据渲出超大留白（codex review）
      return `<section style="height:${h}px;line-height:0;font-size:0;">&nbsp;</section>`;
    },

    // 文字模块：富文本 html 非空 → 渲染期转 mp inline-style（P2）；否则散字段回落（迁移期并存）。
    text: (f) => {
      const richHtml = String(f.html == null ? '' : f.html).trim();
      if (richHtml && parseDoc) {
        const out = richTextToInline(richHtml, parseDoc, mpCh);
        if (out) return out;
      }
      const lines = String(f.content == null ? '' : f.content).split(/\r?\n/).map(s => s.trim()).filter(Boolean);
      if (!lines.length) return '';
      const align = alignOr(f.align);
      const style = textBlockSpanStyle(f);
      return lines.map(line => `<p style="margin:0 8px;padding:0;text-align:${align};"><span style="${style}">${esc(line)}</span></p>`).join('\n');
    },

    // 自定义内容：html_lines 原样输出（历史段无损迁移 / 高级兜底）。
    rich_html: (f) => resolve(asLines(f.html_lines)),

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
