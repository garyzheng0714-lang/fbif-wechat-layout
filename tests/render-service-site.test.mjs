// render-service 网站渠道（packId=ft-default）契约 + fbif 逐字节防回归。
//
// WHY：/api/render 是资讯订阅发布流水线的机器排版出口，本文件守三条合同：
//   1. 网站渠道输出必须是「粘进 FoodTalks CMS（TinyMCE）能存活」的网站口径 HTML——
//      署名 #888888、外链保留、不做微信归一化、表格带 CMS 存活属性（红线 #24）；
//   2. 页脚走 config.modules.bottom_blocks 主路径（红线 #20/#22），素材引用解析 + 绝对化；
//   3. 接入网站渠道不许动公众号一个字节（红线 #19）——fbif 输出与重构前 golden 逐字节一致。
//
// 规则拉取用 fixture 桩（不调真实 API）：fixture 由本仓库 Go API 的默认种子 dump 而来
// （RULES_STORE_PATH 指向临时文件启动 `go run . -port <n>` 后 curl /api/rules/active）。
// golden/render-service-fbif.html 由【重构前】render.mjs + 同一 fixture + 同一样例生成，
// 是「fbif 逐字节不变」的真值。样例 blocks 单一真相在 fixtures/render-service-sample-blocks.mjs。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const fbifRules = JSON.parse(await readFile(new URL('./fixtures/render-service-rules-fbif.json', import.meta.url), 'utf8'));
const ftRules = JSON.parse(await readFile(new URL('./fixtures/render-service-rules-ft-default.json', import.meta.url), 'utf8'));
// 服务端默认种子的页脚是关的（enabled=false，由运营在规则后台开启）；生产 ft-default 的
// 运营态是开启的（页脚完整输出是用户 2026-05-31 裁定的产品合同）。测试要守的是
// 「页脚开启时 bottom_blocks 主路径正确渲染」，故桩里显式开启，模拟运营态。
ftRules.preset.config.modules.bottom_blocks.enabled = true;
ftRules.preset.config.modules.fbif_footer.enabled = true; // tip 片段开关挂在页脚库上

// 规则拉取桩：必须在 import render.mjs 之前设置（render.mjs 顶层捕获 realFetch）。
// 按 account 分流并计数——顺带守「缓存按账号分 key」（两账号各拉一次，互不污染）。
const fetchedAccounts = [];
globalThis.fetch = async (url) => {
  const u = String(url);
  const account = (u.match(/[?&]account=([^&]+)/) || [])[1] || '';
  fetchedAccounts.push(account);
  if (account === 'fbif') return { ok: true, json: async () => fbifRules };
  if (account === 'ft-default') return { ok: true, json: async () => ftRules };
  return { ok: false, json: async () => ({}) };
};

const { renderArticle, SUPPORTED_PACK_IDS } = await import('../render-service/render.mjs');
const { SAMPLE_ARTICLE } = await import('./fixtures/render-service-sample-blocks.mjs');

const golden = await readFile(new URL('./golden/render-service-fbif.html', import.meta.url), 'utf8');

const siteResult = await renderArticle({ ...SAMPLE_ARTICLE, packId: 'ft-default' });
const siteHtml = siteResult.html;

test('ft-default：输出网站口径特征（署名 #888888、h2 无 mctoc 锚点）', () => {
  // 网站署名 #888888 14px 斜体是 FoodTalks 编辑部原生口径（红线 #19：不是公众号 #396FBC）。
  assert.match(siteHtml, /<em><span style="font-size: 14px; color: #888888;">作者：张三<\/span><\/em>/,
    '作者行必须走网站署名口径');
  assert.doesNotMatch(siteHtml, /#396FBC/i, '公众号 Mote 蓝不得泄漏进网站输出');
  assert.match(siteHtml, /<h2 style="text-align: justify;">交易细节<\/h2>/, '标题走网站 h2 口径');
  // mctoc 锚点 id 强制关（与 site.html 同口径；genMctocId 随机，开了输出不可复现）。
  assert.doesNotMatch(siteHtml, /id="mctoc_/, 'FoodTalks 后台自己生成目录锚点，本工具不输出');
  assert.equal(siteResult.imgN, 1);
  assert.ok(siteResult.headingN >= 1);
});

test('ft-default：外链保留、不做微信归一化（粘贴目标是 CMS 不是微信编辑器）', () => {
  // 公众号渠道会把外链降纯文本（微信草稿过滤外链）；网站文章保留外链是正常形态，
  // 降级了反而破坏正文（这正是渠道差异必须参数化、不能共享后处理的原因）。
  assert.match(siteHtml, /<a href="https:\/\/example\.com\/news\/1" target="_blank" rel="noopener">外部报道<\/a>/,
    '网站渠道外链必须原样保留');
  // normalizeWechatRichHtml 会给所有 <img> 强制 max-width:100% !important 并误居中网站
  // 内联 logo（engine.js isWebsiteTemplate 因此整体跳过）；API 路径必须同语义。
  assert.doesNotMatch(siteHtml, /!important/, '出现 !important = 微信归一化泄漏进网站渠道');
  assert.match(siteHtml, /<img style="display: block; margin-left: auto; margin-right: auto; max-width: 100%; height: auto;" src="https:\/\/mmbiz\.qpic\.cn/,
    '正文图保持网站渲染器原样输出');
});

test('ft-default：页脚走 bottom_blocks 主路径，素材解析 + 绝对化，完整输出（红线 #20/#22）', () => {
  // 转载 tip：走正文流的两段式提示声明，原文链接来自 data.source。
  assert.match(siteHtml, /提示：/, 'tip 提示声明必须存在');
  assert.match(siteHtml, /不代表FoodTalks立场，转载请联系出处/, '转载声明文案来自服务端 behavior 模板');
  assert.match(siteHtml, /<a href="https:\/\/mp\.weixin\.qq\.com\/s\/sample-source"[^>]*>《雀巢收购植物基品牌》<\/a>/,
    'tip 内标题链接指向原文 URL');
  // 6 片段完整输出（用户 2026-05-31 裁定）：biz_coop / join_group / follow_account / logo / more_info。
  assert.match(siteHtml, /FoodTalks商务合作方案/, 'biz_coop 片段');
  assert.match(siteHtml, /加入微信群/, 'join_group 片段');
  assert.match(siteHtml, /关注公众号/, 'follow_account 片段');
  assert.match(siteHtml, /FoodTalks logo/, 'logo 片段');
  assert.match(siteHtml, /点击获取更多/, 'more_info 片段');
  // {{asset#ft_footer_bobo_card}} → /assets/fbif/bobo-card.webp → 绝对化到线上域名：
  // 站内相对路径粘进 CMS 必是死图，绝对化是发布方转存的前提。
  assert.match(siteHtml, /src="https:\/\/fbifmp-layout\.foodtalks\.cn\/assets\/fbif\/bobo-card\.webp"/,
    '素材占位符解析 + 相对路径绝对化必须在 API 路径生效');
  assert.doesNotMatch(siteHtml, /\{\{asset[#:]/, '不得有未解析的素材占位符');
});

test('ft-default：withFooter=false 摘除整组页脚（bottom_blocks + tip 片段）', async () => {
  const { html } = await renderArticle({ ...SAMPLE_ARTICLE, packId: 'ft-default', withFooter: false });
  assert.doesNotMatch(html, /FoodTalks商务合作方案|加入微信群|点击获取更多/, '底部区块不得输出');
  assert.doesNotMatch(html, /提示：/, 'tip 片段开关挂在页脚库上，withFooter=false 一并摘除');
  assert.match(html, /雀巢宣布收购某植物基品牌/, '正文不受影响');
});

// ── 表格 CMS 存活（红线 #24，真值审计 foodtalks.cn/news/62475 + TinyMCE 沙盒实测）──
// FoodTalks CMS 粘贴会剥光 table/td 的内联样式与 colgroup，只保留 HTML 属性
// （border/cellpadding/align/bgcolor/width）与内容层（strong、img 的属性和 style）。
// 这里用模拟清洗器复演该行为（同 tests/table-renderer-shared.test.mjs 的审计口径），
// 断言清洗后关键视觉仍有属性层兜底——守的是「发布后表头不褪色、格子不歪」这一结果。
function simulateFoodTalksCmsWash(html) {
  return html
    .replace(/<colgroup>[\s\S]*?<\/colgroup>/gi, '')
    .replace(/<(table|td|th|tr)\b([^>]*)>/gi, (m, tag, attrs) =>
      '<' + tag + attrs.replace(/\s+style="[^"]*"/gi, '') + '>');
}

test('ft-default：表格 CMS 存活开关沿 headless 路径传到 shared/table-renderer（红线 #24）', () => {
  assert.match(siteHtml, /<table border="1" cellpadding="8" /, 'outerBorder+cmsSurvival：table 属性层');
  assert.match(siteHtml, /<td align="center" bgcolor="#01396a" style="[^"]*"><strong>品类<\/strong><\/td>/,
    '表头格：align/bgcolor 属性 + strong 内容层（与 style 双写）');
  const washed = simulateFoodTalksCmsWash(siteHtml);
  assert.match(washed, /<table border="1" cellpadding="8"/, '清洗后 border/cellpadding 属性存活');
  assert.match(washed, /<td align="center" bgcolor="#01396a"><strong>品类<\/strong><\/td>/,
    '清洗后表头仍居中、有底色标记、文字加粗');
  assert.match(washed, /<td align="center">植物基饮料<\/td>/, '清洗后正文格仍居中');
});

test('fbif：接入网站渠道后输出与重构前 golden 逐字节一致（红线 #19）', async () => {
  // golden 由重构前 render.mjs + 同一 fixture + 同一样例生成（生成方式见文件头）。
  // 任何 fbif 字节漂移（哪怕空格）都意味着渠道参数化渗漏进了公众号路径——直接打回。
  const { html, imgN, headingN } = await renderArticle(SAMPLE_ARTICLE);
  assert.equal(html, golden, 'fbif 输出必须与重构前逐字节一致');
  assert.equal(imgN, 1);
  assert.equal(headingN, 1);
  // 公众号表格不得沾染 CMS 存活属性（cmsSurvival 只属网站渠道）。
  assert.doesNotMatch(html, /cellpadding|bgcolor|align="center"/, 'CMS 存活防御层零泄漏到公众号');
});

test('fbif ↔ ft-default 交错渲染互不污染（规则缓存按账号分 key + 全局 config 按请求重灌）', async () => {
  // WHY：window._activeConfig 是进程级全局，渲染服务是常驻进程——若两账号共用一个缓存槽
  // 或全局残留上一账号规则，公众号草稿会错渲网站页脚（渠道串台，历史头号事故源）。
  const ft1 = await renderArticle({ ...SAMPLE_ARTICLE, packId: 'ft-default' });
  const fbif1 = await renderArticle(SAMPLE_ARTICLE);
  const ft2 = await renderArticle({ ...SAMPLE_ARTICLE, packId: 'ft-default' });
  assert.equal(fbif1.html, golden, '夹在两次网站渲染之间的 fbif 输出仍与 golden 逐字节一致');
  assert.equal(ft2.html, ft1.html, '夹了 fbif 渲染后网站输出不漂移');
  // 60s 缓存内每账号只应拉一次规则；计数>2 说明缓存 key 被互相冲掉。
  assert.deepEqual([...new Set(fetchedAccounts)].sort(), ['fbif', 'ft-default'], '只拉这两个账号');
  assert.equal(fetchedAccounts.length, 2, '每账号 60s 内只拉一次（缓存未被另一账号污染冲掉）');
});

test('未知 packId 显式 400 失败（不静默降级到公众号模板）', async () => {
  assert.deepEqual([...SUPPORTED_PACK_IDS].sort(), ['fbif', 'ft-default']);
  await assert.rejects(
    () => renderArticle({ ...SAMPLE_ARTICLE, packId: 'ft-series' }),
    (err) => err.status === 400 && /暂只支持 packId=/.test(err.message),
    '未接入的渠道必须显式报错——静默 fallback 到 fbif 会把网站文章排成公众号版式',
  );
});
