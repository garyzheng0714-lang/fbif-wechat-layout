// render-service 冒烟：Block[] → 内联样式微信 HTML 的 API 输出契约。
// WHY：这是资讯订阅一键发布流水线消费的机器排版出口，契约破坏=线上草稿版式坏/外链被微信过滤。
// 断言的是「必须内联样式、外链必须降级、图片与文字不能丢」这些发布安全性质，不是具体像素。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const fbifRules = JSON.parse(await readFile(
  new URL('./fixtures/render-service-rules-fbif.json', import.meta.url),
  'utf8',
));
globalThis.fetch = async () => ({
  ok: true,
  json: async () => fbifRules,
});

// render.mjs 自带 globals 桩（node --test 每文件独立进程，不污染其他测试）。
const { renderArticle, stripExternalAnchors } = await import('../render-service/render.mjs');

const sampleBlocks = [
  { k: 'txt', runs: [{ type: 'txt', text: '雀巢宣布收购某植物基品牌，交易金额未披露。' }] },
  // Block 契约（api/wechat_blocks.go / fbif.js classifyBlocks）：h、cap 读 text；txt、bq、li 读 runs。
  { k: 'h', text: '交易细节' },
  {
    k: 'txt',
    runs: [
      { type: 'txt', text: '据' },
      { type: 'link', text: '外部报道', href: 'https://example.com/news/1' },
      { type: 'txt', text: '与' },
      { type: 'link', text: '公众号旧文', href: 'https://mp.weixin.qq.com/s/abc123' },
      { type: 'txt', text: '，双方已达成一致。' },
    ],
  },
  { k: 'img', src: 'https://mmbiz.qpic.cn/mmbiz_jpg/fake/640.jpg', dataRatio: '0.56' },
  { k: 'bq', runs: [{ type: 'txt', text: '「这是一次战略性收购。」' }] },
];

test('renderArticle：输出为内联样式 HTML，正文/标题/图片全保留', async () => {
  const { html, imgN, headingN } = await renderArticle({
    title: '雀巢收购植物基品牌',
    source: 'FoodDive',
    blocks: sampleBlocks,
    isRepost: true,
  });
  assert.ok(html.length > 200, '输出不能为空壳');
  assert.match(html, /style="/, '必须含内联样式（进公众号草稿的硬前提）');
  assert.match(html, /雀巢宣布收购某植物基品牌/, '正文文字不能丢');
  assert.match(html, /交易细节/, '标题文字不能丢');
  assert.match(html, /mmbiz\.qpic\.cn\/mmbiz_jpg\/fake\/640\.jpg/, '图片 src 不能丢（后续由发布方转存重写）');
  assert.equal(imgN, 1);
  assert.ok(headingN >= 1, '标题块必须被识别');
});

test('renderArticle：外链降为纯文本、公众号链接保留（微信草稿过滤契约）', async () => {
  const { html } = await renderArticle({ title: 't', blocks: sampleBlocks });
  assert.doesNotMatch(html, /href="https:\/\/example\.com/, '外链必须被降级，否则微信保存草稿时破坏版式');
  assert.match(html, /外部报道/, '外链文字必须保留');
  assert.match(html, /href="https:\/\/mp\.weixin\.qq\.com\/s\/abc123"/, 'mp.weixin.qq.com 链接是微信合法链接，保留');
});

test('renderArticle：空 blocks 显式报错（不静默出空文）', async () => {
  await assert.rejects(() => renderArticle({ title: 't', blocks: [] }), /blocks 不能为空/);
});

test('renderArticle：withFooter=false 时不带页脚（fetch 桩下页脚数据缺失也不报错）', async () => {
  const { html } = await renderArticle({ title: 't', blocks: sampleBlocks, withFooter: false });
  assert.doesNotMatch(html, /提示：/, '关闭页脚时不应出现提示声明');
});

test('absolutizeAssetURLs：站内相对素材补全为线上域名，绝对 URL 不动', async () => {
  const { absolutizeAssetURLs } = await import('../render-service/render.mjs');
  const out = absolutizeAssetURLs('<img src="/assets/fbif/bobo-card.webp"><img src="https://mmbiz.qpic.cn/a.jpg">', 'https://example.host');
  assert.match(out, /src="https:\/\/example\.host\/assets\/fbif\/bobo-card\.webp"/);
  assert.match(out, /src="https:\/\/mmbiz\.qpic\.cn\/a\.jpg"/);
});

test('stripExternalAnchors：只动 <a>，不碰其他标签', () => {
  const html = '<section class="wx-p"><a class="wx-a" href="https://x.com/a">A</a><img src="https://mmbiz.qpic.cn/i.jpg"></section>';
  const out = stripExternalAnchors(html);
  assert.doesNotMatch(out, /<a /);
  assert.match(out, /A/);
  assert.match(out, /<img src="https:\/\/mmbiz\.qpic\.cn\/i\.jpg">/);
});
