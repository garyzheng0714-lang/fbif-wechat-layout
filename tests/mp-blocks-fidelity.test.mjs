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
