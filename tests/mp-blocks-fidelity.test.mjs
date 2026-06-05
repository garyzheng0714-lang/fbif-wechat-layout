// mp-blocks-fidelity.test.mjs — 公众号底部区块渲染【字节级复刻 footer.html】golden 守卫。
//
// 自洽思路：从已发布成品 public/footer.html 反向提取 FBIF 默认 bottom_blocks 字段，
// 经 mp 渲染器渲染后，应【字节还原】footer.html。两处按设计归一化后比对：
//   1) 图 src：渲染输出是素材库 URL，footer.html 是内联 base64 → 都归一为 [SRC]；
//   2) 更多文章卡片容器：渲染输出空容器（卡片由 more-articles 复制链路委托填充），
//      footer.html 内联 3 张默认卡 → 容器内部都归一为 [[CARDS]]。
// 除这两处外必须逐字节一致——任何排版漂移（间距/颜色/字体/锚点）都会让测试失败。

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { renderBottomBlocksHtml } from '../public/js/blocks/block-render.js';
import { mpBlockRenderers } from '../public/js/templates/mp-blocks.js';
import { parseHTML } from 'linkedom';

const mpParseDoc = (html) => parseHTML(html).document;

const dir = path.dirname(fileURLToPath(import.meta.url));
const footerHtml = fs.readFileSync(path.join(dir, '../public/footer.html'), 'utf8');

const assetLibrary = {
  schema_version: 1,
  assets: [
    { id: 'fbif_bobo_card', type: 'image', usage: 'avatar', url: '/assets/fbif/bobo-card.webp', scope: 'fbif' },
    { id: 'fbif_promo_1000', type: 'image', usage: 'decor', url: '/assets/fbif/promo-1000.webp', scope: 'fbif' },
  ],
};

// 从 footer.html 反向提取 FBIF 默认 bottom_blocks（base64 先占位，避免污染行切分）。
function fbifModule() {
  const segs = footerHtml.split(/(data:image\/[a-zA-Z.+-]+;base64,[A-Za-z0-9+/=]+)/);
  let sk = '';
  let n = 0;
  for (const p of segs) sk += p.startsWith('data:image') ? `__IMG${n++}__` : p;
  const L = sk.split('\n');
  return {
    enabled: true,
    order: ['tip', 'byline', 'more_articles', 'promo'],
    blocks: {
      tip: { type: 'declaration', enabled: true, fields: { title: '提示：', lines: [L[2], L[3], L[4], L[5], L[6], L[7]] } },
      byline: { type: 'name_card', enabled: true, fields: { intro_text: '', image: '{{asset#fbif_bobo_card}}' } },
      more_articles: { type: 'more_articles', enabled: true, fields: { title: '/ 更多文章 /' } },
      promo: { type: 'titled_media', enabled: true, fields: { title: '/ 1000个食品创新案例 /', image: '{{asset#fbif_promo_1000}}' } },
    },
  };
}

const OUTER_OPEN = '<section style="text-align: center;margin-left: 8px;margin-right: 8px;">';

// 把更多文章卡片外层容器（OUTER_OPEN 到其深度匹配的 </section>）整体替换为 [[CARDS]]。
function blankCards(html) {
  const i = html.indexOf(OUTER_OPEN);
  if (i < 0) return html;
  let depth = 0;
  let j = i;
  while (j < html.length) {
    const ni = html.indexOf('<section', j);
    const nc = html.indexOf('</section>', j);
    if (nc < 0) break;
    if (ni >= 0 && ni < nc) { depth++; j = ni + '<section'.length; }
    else {
      depth--;
      if (depth === 0) return html.slice(0, i) + '[[CARDS]]' + html.slice(nc + '</section>'.length);
      j = nc + '</section>'.length;
    }
  }
  return html;
}

function normalize(html) {
  return blankCards(html.replace(/src="[^"]*"/g, 'src="[SRC]"')).trim();
}

test('mp 底部区块渲染字节级复刻 footer.html（图 src 与更多文章卡片区归一）', () => {
  const out = renderBottomBlocksHtml(fbifModule(), undefined, mpBlockRenderers(assetLibrary, 'fbif'));
  assert.equal(normalize(out), normalize(footerHtml));
});

test('波波名片图正确解析为素材库 URL（不是 base64、不补 width/height）', () => {
  const out = renderBottomBlocksHtml(fbifModule(), undefined, mpBlockRenderers(assetLibrary, 'fbif'));
  assert.ok(out.includes('src="/assets/fbif/bobo-card.webp"'), '波波名片应引用素材库 URL');
  assert.ok(out.includes('src="/assets/fbif/promo-1000.webp"'), '1000案例图应引用素材库 URL');
  assert.ok(!/bobo-card\.webp"[^>]*\swidth=/.test(out), '名片图不应被补 width 属性（保真）');
});

test('更多文章块输出 mergeIntoFooter 的两个锚点（委托复制链路可定位）', () => {
  const out = renderBottomBlocksHtml(fbifModule(), undefined, mpBlockRenderers(assetLibrary, 'fbif'));
  assert.ok(out.includes('<section data-role="paragraph">\n'), '需含无 style 的 paragraph 锚点');
  assert.ok(out.includes(OUTER_OPEN), '需含卡片容器锚点');
});

// v2 双预设 FBIF 默认（对应 api/rule_bottom_blocks.go: defaultMpBottomBlocksModuleV2）渲染断言。
// 视觉关键属性 golden；像素级 1:1 由公众号后台复制最终确认（CLAUDE.md UI 协议）。
function fbifV2Module() {
  const textB = (content, bold, size, color, align) => ({ type: 'text', enabled: true, fields: { content, bold, font_size: size, text_color: color, align } });
  const sp = (h) => ({ type: 'spacer', enabled: true, fields: { height: h } });
  const img = (asset, width) => ({ type: 'image', enabled: true, fields: { image: asset, width } });
  const shared = (tipBody) => ({
    tip_title: textB('提示：', true, 17, '#544545', 'left'),
    sp1: sp(8), tip_body: tipBody, sp2: sp(16),
    byline: img('{{asset#fbif_bobo_card}}', 661),
    more_articles: { type: 'more_articles', enabled: true, fields: { title: '/ 更多文章 /' } },
    sp_ma: sp(20),
    promo_title: textB('/ 1000个食品创新案例 /', true, 17, '#544545', 'center'),
    sp4: sp(20), promo_img: img('{{asset#fbif_promo_1000}}', 661),
  });
  // 顺序对齐 footer.html：提示 → 名片 → 更多文章 → 1000标题 → 1000图。
  const order = ['tip_title', 'sp1', 'tip_body', 'sp2', 'byline', 'more_articles', 'sp_ma', 'promo_title', 'sp4', 'promo_img'];
  return {
    schema_version: 2, enabled: true, active_preset_id: 'original',
    presets: [
      { id: 'original', name: '原创', order, blocks: shared(textB('* 本文为FBIF原创，欢迎转发朋友圈；\n* 转载请在后台回复“转载”了解规则；\n* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）', false, 15, '#888888', 'left')) },
      { id: 'repost', name: '转载', order, blocks: shared(textB('* 本文为转载，不代表FBIF立场。\n* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）', false, 15, '#888888', 'left')) },
    ],
  };
}

test('v2 FBIF 原创预设：顺序/三条文案/图宽661/粗体/间距复刻 footer.html（防漂移断言，codex review）', () => {
  const out = renderBottomBlocksHtml(fbifV2Module(), undefined, mpBlockRenderers(assetLibrary, 'fbif'), { presetId: 'original' });
  // 粗体标题 17 深褐
  assert.match(out, /提示：/); assert.match(out, /font-size:17px/); assert.match(out, /font-weight:bold/); assert.match(out, /color:rgb\(84, 69, 69\)/);
  // 原创三条（含「转载请在后台回复」）
  assert.match(out, /本文为FBIF原创/); assert.match(out, /转载请在后台回复/); assert.match(out, /FBIF投稿与商务合作/);
  assert.match(out, /color:rgb\(136, 136, 136\)/); assert.match(out, /font-size:15px/);
  // 间距 + 图宽 661（非 100%）
  assert.match(out, /height:8px/); assert.match(out, /height:16px/); assert.match(out, /height:20px/);
  assert.match(out, /width:661px/);
  assert.ok(out.includes('src="/assets/fbif/bobo-card.webp"')); assert.ok(out.includes('src="/assets/fbif/promo-1000.webp"'));
  // 顺序：名片 → 更多文章 → 1000标题 → 1000图（防漂移）
  const iByline = out.indexOf('bobo-card.webp');
  const iMore = out.indexOf(OUTER_OPEN);
  const iPromoTitle = out.indexOf('1000个食品创新案例');
  const iPromoImg = out.indexOf('promo-1000.webp');
  assert.ok(iByline < iMore && iMore < iPromoTitle && iPromoTitle < iPromoImg, '顺序应为 名片→更多文章→1000标题→1000图');
  assert.match(out, /text-align:center/);
});

test('v2 FBIF 转载预设：提示正文切转载文案（两条），其余块一致', () => {
  const out = renderBottomBlocksHtml(fbifV2Module(), undefined, mpBlockRenderers(assetLibrary, 'fbif'), { presetId: 'repost' });
  assert.match(out, /本文为转载，不代表FBIF立场/);
  assert.ok(!out.includes('本文为FBIF原创'), '转载预设不应出现原创文案');
  assert.ok(!out.includes('转载请在后台回复'), '转载预设不含原创第二条');
  assert.ok(out.includes('src="/assets/fbif/bobo-card.webp"'), '名片图两套预设一致');
});

test('防漂移：Go defaultMpBottomBlocksModuleV2 关键结构与 JS fbifV2Module 同步（切生产前哨兵）', async () => {
  const { readFile } = await import('node:fs/promises');
  const goSrc = await readFile(new URL('../api/rule_bottom_blocks.go', import.meta.url), 'utf8');
  const orig = fbifV2Module().presets[0].blocks;
  // order 完整同步——任一侧改顺序而另一侧没跟 → 失败。
  assert.ok(goSrc.includes('"tip_title", "sp1", "tip_body", "sp2", "byline", "more_articles", "sp_ma", "promo_title", "sp4", "promo_img"'), 'order 同步');
  // 关键文案同步（含 codex review #2 要求的原创第二条）。
  assert.ok(goSrc.includes('本文为FBIF原创'), '原创文案同步');
  assert.ok(goSrc.includes('转载请在后台回复'), '原创第二条同步');
  assert.ok(goSrc.includes(orig.promo_title.fields.content), 'promo_title 文案同步');
  // 图宽 661 精确（绑定 asset 引用，避免误匹配其它 661）。
  assert.ok(goSrc.includes('"{{asset#fbif_bobo_card}}", 661'), 'byline 图宽 661 同步');
  assert.ok(goSrc.includes('"{{asset#fbif_promo_1000}}", 661'), 'promo_img 图宽 661 同步');
});

test('P2 富文本：text.html 渲染期转 mp inline-style（hex→rgb、补字体栈、粗体/居中、回落散字段）', () => {
  const mod = {
    enabled: true,
    order: ['rich', 'plain'],
    blocks: {
      rich: { type: 'text', enabled: true, fields: { html: '<p style="text-align:center"><span style="color:#544545;font-size:17px;font-weight:bold">提示：</span></p>' } },
      plain: { type: 'text', enabled: true, fields: { content: '纯文本回落', font_size: 15, text_color: '#888888' } },
    },
  };
  const out = renderBottomBlocksHtml(mod, undefined, mpBlockRenderers(assetLibrary, 'fbif', { parseDoc: mpParseDoc }));
  // 富文本块：hex→rgb、补 mp-quote 字体栈、粗体、居中
  assert.match(out, /提示：/);
  assert.match(out, /color:rgb\(84, 69, 69\)/);
  assert.match(out, /font-size:17px/);
  assert.match(out, /font-weight:bold/);
  assert.match(out, /mp-quote/);
  assert.match(out, /text-align:center/);
  // 纯文本块（无 html）回落散字段渲染
  assert.match(out, /纯文本回落/);
  assert.match(out, /color:rgb\(136, 136, 136\)/);
});

test('文字模块支持基础样式，并在模块之间插入默认空行', () => {
  // WHY：底部重构后运营只面对「文字 / 图片 / 推荐文章」三类模块；文字模块必须能承担常见提示文案。
  const mod = {
    enabled: true,
    gap: 'blank_line',
    order: ['notice', 'logo'],
    blocks: {
      notice: { type: 'text', enabled: true, fields: { content: '欢迎加入\n扫码联系', font_size: 18, text_color: '#396FBC', background_color: '#FFFFFF', italic: true, underline: true, align: 'center' } },
      logo: { type: 'image', enabled: true, fields: { image: '{{asset#fbif_bobo_card}}' } },
    },
  };
  const out = renderBottomBlocksHtml(mod, undefined, mpBlockRenderers(assetLibrary, 'fbif'));
  assert.match(out, /欢迎加入/);
  assert.match(out, /font-size:18px/);
  assert.match(out, /color:rgb\(57, 111, 188\)/);
  assert.match(out, /font-style:italic/);
  assert.match(out, /text-decoration:underline/);
  assert.match(out, /<br>/, '两个非空模块之间应有默认空行');
});
