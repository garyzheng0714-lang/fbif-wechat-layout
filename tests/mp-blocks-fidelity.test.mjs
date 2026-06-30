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
import { renderBottomBlocksHtml } from '../public/js/blocks/block-render.js';
import { mpBlockRenderers } from '../public/js/templates/mp-blocks.js';
import { parseHTML } from 'linkedom';

const mpParseDoc = (html) => parseHTML(html).document;

const assetLibrary = {
  schema_version: 1,
  assets: [
    { id: 'fbif_bobo_card', type: 'image', usage: 'avatar', url: '/assets/fbif/bobo-card.webp', scope: 'fbif' },
  ],
};

const OUTER_OPEN = '<section style="text-align: center;">';

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
  });
  // 顺序对齐当前 FBIF 底部契约：提示 → 名片 → 更多文章。
  const order = ['tip_title', 'sp1', 'tip_body', 'sp2', 'byline', 'more_articles'];
  return {
    schema_version: 2, enabled: true, active_preset_id: 'original',
    presets: [
      { id: 'original', name: '原创', order, blocks: shared(textB('* 本文为FBIF原创，欢迎转发朋友圈；\n* 转载请在后台回复“转载”了解规则；\n* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）', false, 15, '#888888', 'left')) },
      { id: 'repost', name: '转载', order, blocks: shared(textB('* 本文为转载文章。\n* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）', false, 15, '#888888', 'left')) },
    ],
  };
}

test('v2 FBIF 原创预设：顺序/三条文案/图宽661/粗体/间距复刻当前底部契约（防漂移断言）', () => {
  const out = renderBottomBlocksHtml(fbifV2Module(), undefined, mpBlockRenderers(assetLibrary, 'fbif'), { presetId: 'original' });
  // 粗体标题 17 深褐
  assert.match(out, /提示：/); assert.match(out, /font-size:17px/); assert.match(out, /font-weight:bold/); assert.match(out, /color:rgb\(84, 69, 69\)/);
  // 原创三条（含「转载请在后台回复」）
  assert.match(out, /本文为FBIF原创/); assert.match(out, /转载请在后台回复/); assert.match(out, /FBIF投稿与商务合作/);
  assert.match(out, /color:rgb\(136, 136, 136\)/); assert.match(out, /font-size:15px/);
  // 间距 + 图宽 661（非 100%）
  assert.match(out, /height:8px/); assert.match(out, /height:16px/);
  assert.match(out, /width:661px/);
  assert.ok(out.includes('src="/assets/fbif/bobo-card.webp"'));
  assert.ok(!out.includes('1000个食品创新案例'), 'FBIF 默认底部不应出现旧 1000 案例标题');
  assert.ok(!out.includes('promo-1000.webp'), 'FBIF 默认底部不应出现旧 1000 案例图片');
  // 顺序：名片 → 更多文章（防漂移）
  const iByline = out.indexOf('bobo-card.webp');
  const iMore = out.indexOf(OUTER_OPEN);
  assert.ok(iByline < iMore, '顺序应为 名片→更多文章');
  assert.match(out, /text-align:center/);
});

test('v2 FBIF 转载预设：提示正文切转载文案（两条），其余块一致', () => {
  const out = renderBottomBlocksHtml(fbifV2Module(), undefined, mpBlockRenderers(assetLibrary, 'fbif'), { presetId: 'repost' });
  assert.match(out, /本文为转载文章。/);
  assert.ok(!out.includes('本文为FBIF原创'), '转载预设不应出现原创文案');
  assert.ok(!out.includes('转载请在后台回复'), '转载预设不含原创第二条');
  assert.ok(out.includes('src="/assets/fbif/bobo-card.webp"'), '名片图两套预设一致');
});

test('防漂移：Go defaultMpBottomBlocksModuleV2 关键结构与 JS fbifV2Module 同步（切生产前哨兵）', async () => {
  const { readFile } = await import('node:fs/promises');
  const goSrc = await readFile(new URL('../api/rule_bottom_blocks.go', import.meta.url), 'utf8');
  // order 完整同步——任一侧改顺序而另一侧没跟 → 失败。
  assert.ok(goSrc.includes('"tip_title", "sp1", "tip_body", "sp2", "byline", "more_articles"'), 'order 同步');
  // 关键文案同步（含 codex review #2 要求的原创第二条）。
  assert.ok(goSrc.includes('本文为FBIF原创'), '原创文案同步');
  assert.ok(goSrc.includes('转载请在后台回复'), '原创第二条同步');
  // 图宽 661 精确（绑定 asset 引用，避免误匹配其它 661）。
  assert.ok(goSrc.includes('"{{asset#fbif_bobo_card}}", 661'), 'byline 图宽 661 同步');
  assert.ok(!goSrc.includes('promo_title'), 'Go 默认不应再声明 promo_title');
  assert.ok(!goSrc.includes('fbif_promo_1000'), 'Go 默认不应再引用 1000 案例图片');
});

test('P2 富文本：text.html 渲染期转 mp inline-style（hex→rgb、补字体栈、粗体/居中、回落散字段）', () => {
  const mod = {
    schema_version: 2, active_preset_id: 'default',
    presets: [{ id: 'default', order: ['rich', 'plain'], blocks: {
      rich: { type: 'text', enabled: true, fields: { html: '<p style="text-align:center"><span style="color:#544545;font-size:17px;font-weight:bold">提示：</span></p>' } },
      plain: { type: 'text', enabled: true, fields: { content: '纯文本回落', font_size: 15, text_color: '#888888' } },
    } }],
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

test('文字模块支持基础样式（v2 单预设）', () => {
  // WHY：底部重构后运营只面对「文字 / 图片 / 推荐文章」三类模块；文字模块必须能承担常见提示文案。
  // v2 无 blank_line 自动空段——块间距由 spacer 块承载（此处不测间距，只测文字样式）。
  const mod = {
    schema_version: 2, active_preset_id: 'default',
    presets: [{ id: 'default', order: ['notice', 'logo'], blocks: {
      notice: { type: 'text', enabled: true, fields: { content: '欢迎加入\n扫码联系', font_size: 18, text_color: '#396FBC', background_color: '#FFFFFF', italic: true, underline: true, align: 'center' } },
      logo: { type: 'image', enabled: true, fields: { image: '{{asset#fbif_bobo_card}}' } },
    } }],
  };
  const out = renderBottomBlocksHtml(mod, undefined, mpBlockRenderers(assetLibrary, 'fbif'));
  assert.match(out, /欢迎加入/);
  assert.match(out, /font-size:18px/);
  assert.match(out, /color:rgb\(57, 111, 188\)/);
  assert.match(out, /font-style:italic/);
  assert.match(out, /text-decoration:underline/);
});
