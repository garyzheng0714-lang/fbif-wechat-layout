// 微信「保存草稿」存活契约测试（2026-07-02 根治「保存草稿后更多文章消失」）。
//
// 为什么存在：粘进公众号编辑器的 HTML 要经历两道微信侧处理——
//   ① 粘贴阶段：编辑器把 <img src> 里的 base64 位图立即本地转存成 mmbiz，
//      外链 http 图异步拉取转存；但 SVG data: 不转存，且 <a linktype="image">
//      的 imgurl 属性从不被改写。
//   ② 保存草稿阶段：服务端清洗——src 残留 data:/blob:/相对路径的 <img> 被删；
//      imgurl 不是微信图床的图片链接卡片被整卡清除（这就是「更多文章消失」）。
// 本文件把这套微信侧行为固化成可执行模拟器：任何让复制层重新出现
// 「imgurl 非 mmbiz」「SVG 占位」「本地引用」形态的改动，在这里立即红灯，
// 不再等用户在真实后台发现。规则依据见 WORKLOG-draft-save-regression.md。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { mergeIntoFooter } from '../public/js/more-articles.js';
import { PLACEHOLDER_DATA_URL } from '../public/js/more-articles-placeholder.js';

// ---- 微信草稿保存模拟器（粘贴 + 保存两阶段） ----

const WECHAT_IMG_HOST_RE = /^https?:\/\/(?:mmbiz\.qpic\.cn|mmecoa\.qpic\.cn|mmbiz\.qlogo\.cn|wx\.qlogo\.cn)\//i;

function isWechatHostedImage(url) {
  return WECHAT_IMG_HOST_RE.test(String(url || '').trim());
}

export function simulateWechatDraftSave(html) {
  let out = String(html || '');
  let transferSeq = 0;

  // 粘贴阶段：base64 位图与外链 http 图的 <img src> 被编辑器转存为 mmbiz。
  // SVG data: 不转存；imgurl 属性保持原样（编辑器从不改写它）。
  out = out.replace(/(<img\b[^>]*\bsrc=)(["'])([^"']+)\2/gi, (m, prefix, q, src) => {
    const isBitmapDataUri = /^data:image\/(?:png|jpe?g|gif|webp)[;,]/i.test(src);
    const isExternalHttp = /^https?:\/\//i.test(src) && !isWechatHostedImage(src);
    if (isBitmapDataUri || isExternalHttp) {
      return prefix + q + 'https://mmbiz.qpic.cn/simulated/' + (transferSeq++) + q;
    }
    return m;
  });

  // 保存阶段①：src 仍是 data:/blob:/相对路径/localhost 的 <img> 被删。
  out = out.replace(/<img\b[^>]*\bsrc=(["'])(?:data:|blob:|\/|https?:\/\/(?:localhost|127\.0\.0\.1))[^"']*\1[^>]*>/gi, '');

  // 保存阶段②：<a linktype="image"> 的 imgurl 非微信图床 → 整卡（含子内容）清除。
  out = out.replace(/<a\b[^>]*\blinktype=(["'])image\1[^>]*>[\s\S]*?<\/a>/gi, (m) => {
    const im = m.match(/\bimgurl=(["'])([^"']*)\1/i);
    const imgurl = im ? im[2] : '';
    return isWechatHostedImage(imgurl) ? m : '';
  });

  return out;
}

// ---- 测试数据 ----

const MMBIZ_COVER_A = 'https://mmbiz.qpic.cn/sz_mmbiz_jpg/cover-a/0?wx_fmt=jpeg';
const MMBIZ_COVER_B = 'https://mmbiz.qpic.cn/sz_mmbiz_jpg/cover-b/0?wx_fmt=jpeg';
const OSS_COMPOSITE_A = 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/composite-a.png';
const OSS_COMPOSITE_B = 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/composite-b.png';
const DATA_PNG_COMPOSITE = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==';

function cardCountIn(html) {
  return (html.match(/linktype="image"/g) || []).length;
}

async function fbifFooter() {
  return readFile(new URL('../public/footer.html', import.meta.url), 'utf8');
}

// v2 blocks 渠道的锚点形态（mp-blocks / foodtalks-series 输出的空卡片容器）
const V2_ANCHOR_FOOTER = [
  '<section data-role="paragraph">',
  '<section style="margin:20px 8px 20px;padding:0;text-align:center;"><strong>/ 更多文章 /</strong></section>',
  '<section style="text-align: center;"></section>',
  '</section>',
].join('\n');

// ---- 核心存活契约 ----

test('更多文章卡片（OSS 合成图 + mmbiz imgurl）经微信粘贴+保存草稿后存活', async () => {
  const merged = mergeIntoFooter(await fbifFooter(), [
    { href: 'https://mp.weixin.qq.com/s/a', imgurl: MMBIZ_COVER_A, final_url: OSS_COMPOSITE_A, title: '标题A' },
    { href: 'https://mp.weixin.qq.com/s/b', imgurl: MMBIZ_COVER_B, final_url: OSS_COMPOSITE_B, title: '标题B' },
  ]);
  assert.equal(cardCountIn(merged), 2);

  const saved = simulateWechatDraftSave(merged);
  assert.equal(cardCountIn(saved), 2, '保存草稿后卡片必须全部存活');
  assert.match(saved, /href="https:\/\/mp\.weixin\.qq\.com\/s\/a"/);
  assert.match(saved, /href="https:\/\/mp\.weixin\.qq\.com\/s\/b"/);
});

test('合成图上传失败降级为 data: PNG 时，卡片保存草稿后依然存活', async () => {
  const merged = mergeIntoFooter(await fbifFooter(), [
    { href: 'https://mp.weixin.qq.com/s/a', imgurl: MMBIZ_COVER_A, final_url: DATA_PNG_COMPOSITE, title: '标题A' },
  ]);
  const saved = simulateWechatDraftSave(merged);
  assert.equal(cardCountIn(saved), 1, 'data: 位图由粘贴阶段转存，卡片不能丢');
});

test('v2 blocks 渠道（mp-blocks/series 锚点）的卡片同样存活', () => {
  const merged = mergeIntoFooter(V2_ANCHOR_FOOTER, [
    { href: 'https://mp.weixin.qq.com/s/a', imgurl: MMBIZ_COVER_A, final_url: OSS_COMPOSITE_A, title: '标题A' },
    { href: 'https://mp.weixin.qq.com/s/b', imgurl: MMBIZ_COVER_B, final_url: DATA_PNG_COMPOSITE, title: '标题B' },
  ]);
  assert.equal(cardCountIn(merged), 2);
  const saved = simulateWechatDraftSave(merged);
  assert.equal(cardCountIn(saved), 2);
});

test('卡片 imgurl 恒为微信图床封面，不跟随合成图外链', async () => {
  const merged = mergeIntoFooter(await fbifFooter(), [
    { href: 'https://mp.weixin.qq.com/s/a', imgurl: MMBIZ_COVER_A, final_url: OSS_COMPOSITE_A, title: '标题A' },
  ]);
  const im = merged.match(/\bimgurl="([^"]+)"/);
  assert.ok(im, '卡片必须有 imgurl');
  assert.equal(im[1], MMBIZ_COVER_A, 'imgurl 必须是原始 mmbiz 封面（微信保存草稿的服务端校验属性）');
  const src = merged.match(/linktype="image"[\s\S]*?<img[^>]*\bsrc="([^"]+)"/);
  assert.equal(src[1], OSS_COMPOSITE_A, '<img src> 仍用合成图');
});

// ---- 模拟器必须有牙齿：旧回归形态必须被杀 ----

test('【反例守卫】imgurl 为 OSS 外链的旧形态会被保存草稿整卡清除', () => {
  const legacyCard =
    '<section><p><strong>' +
    `<a href="https://mp.weixin.qq.com/s/a" imgurl="${OSS_COMPOSITE_A}" linktype="image" target="_blank">` +
    `<span><img alt="图片" src="${OSS_COMPOSITE_A}"></span></a></strong></p></section>`;
  const saved = simulateWechatDraftSave(legacyCard);
  assert.equal(cardCountIn(saved), 0, '模拟器必须能杀掉旧回归形态，否则守卫失效');
});

test('【反例守卫】SVG data: 图在保存草稿后被清除', () => {
  const svgImg = '<img alt="图片" src="data:image/svg+xml;charset=utf-8,%3Csvg%3E%3C/svg%3E">';
  const saved = simulateWechatDraftSave(svgImg);
  assert.doesNotMatch(saved, /<img/, 'SVG data: 不被微信转存，保存后必丢');
});

// ---- 结构性防回归：锚点字节一致 + 占位图形态 ----

test('各模板源码中的更多文章锚点必须能被 mergeIntoFooter 命中（防 46b2c2b 型失配回归）', async () => {
  const read = (p) => readFile(new URL(p, import.meta.url), 'utf8');
  const [mpBlocks, series] = await Promise.all([
    read('../public/js/templates/mp-blocks.js'),
    read('../public/js/templates/foodtalks-series.js'),
  ]);
  const extract = (src, name, file) => {
    const m = src.match(new RegExp(name + "\\s*=\\s*'([^']+)'"));
    assert.ok(m, `${file} 中找不到 ${name}`);
    return m[1];
  };
  const anchors = [
    ['mp-blocks.js', extract(mpBlocks, 'CARDS_WRAPPER_OPEN', 'mp-blocks.js')],
    ['foodtalks-series.js', extract(series, 'MORE_CARDS_WRAPPER_OPEN', 'foodtalks-series.js')],
  ];
  const testCards = [
    { href: 'https://mp.weixin.qq.com/s/a', imgurl: MMBIZ_COVER_A, final_url: OSS_COMPOSITE_A, title: '标题A' },
  ];
  for (const [file, anchor] of anchors) {
    const footer = [
      '<section data-role="paragraph">',
      '<section style="margin:20px 8px 20px;padding:0;text-align:center;"><strong>/ 更多文章 /</strong></section>',
      anchor + '</section>',
      '</section>',
    ].join('\n');
    const merged = mergeIntoFooter(footer, testCards);
    assert.equal(cardCountIn(merged), 1, `${file} 的锚点「${anchor}」必须能被 mergeIntoFooter 填卡（锚点失配 = 复制层丢更多文章）`);
  }
  const mergedFbif = mergeIntoFooter(await fbifFooter(), testCards);
  assert.ok(cardCountIn(mergedFbif) >= 1, 'footer.html（v1 渠道）的锚点必须能被 mergeIntoFooter 填卡');
});

test('空卡占位图必须是 PNG 位图 data:（微信粘贴可转存），不得回退 SVG', async () => {
  assert.match(PLACEHOLDER_DATA_URL, /^data:image\/png[;,]/, '占位图必须是 PNG data URL');
  const merged = mergeIntoFooter(await fbifFooter(), [{ href: '', imgurl: '', title: '' }]);
  const saved = simulateWechatDraftSave(merged);
  assert.match(saved, /https:\/\/mmbiz\.qpic\.cn\/simulated\//, '占位图应在粘贴阶段被转存存活，供微信内替换图片');
});
