import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyFoodTalksBehavior,
  buildSiteFooterTipElem,
  extractRepostSource,
  buildAuthorLineFromSource,
  buildFooterHtml,
  FOOTER_PRESETS,
} from '../public/js/rule-behavior-foodtalks.js';
import { ASSET_LIBRARY_DEFAULT } from '../public/js/footer-segments-defaults.js';

const DEFAULT_CONFIG = {
  behavior: {
    author_regex: '^\\s*作者[：:]\\s*',
    tip_regex: '^\\s*提示[：:]',
    ref_header_regex: '^\\s*参考(?:来源|资料)[：:]?\\s*$',
    ref_item_regex: '^\\s*\\[\\d+\\]',
  },
  modules: {
    fbif_footer: { enabled: false },
  },
};

const SERIES_CONFIG = {
  ...DEFAULT_CONFIG,
  behavior: {
    ...DEFAULT_CONFIG.behavior,
    params: {
      site_variant: { type: 'string', default: 'ft-series' },
      recommended_reading_regex: { type: 'string', default: '^\\s*(?:相关阅读|推荐阅读)\\s*[：:]?\\s*$' },
      footer_note_regex: { type: 'string', default: '^本文(?:(?:未|为)标注来源(?:的)?图片(?:/数据)?均来自|作者\\s*[：:]|图片来源)|^图片均来自|^[*＊]\\s*注[：:]' },
    },
  },
};

function p(text) {
  return { k: 'p', runs: [{ type: 'txt', text }] };
}
function h(level, text) {
  return { k: 'h', level, runs: [{ type: 'txt', text }] };
}
function img(src) {
  return { k: 'img', src, alt: '', width: 600, height: 400 };
}
function cap(text) {
  return { k: 'cap', runs: [{ type: 'txt', text }] };
}

const REPOST = { isRepost: true };

// 复刻 news/62285 真实抓取尾部：参考来源 → 公众号"提示："页脚 → 二维码 →
// 更多文章 → 1000个案例 → 预览时标签不可点。网站渠道应从公众号"提示："起全切，
// 并补一段网站口径页脚。
function wechatTrailerElems() {
  return [
    p('参考来源：'),
    p('[1] 来源 A'),
    p('提示：'),
    p('* 本文为FBIF原创，欢迎转发朋友圈；'),
    p('* 转载请在后台回复“转载”了解规则；'),
    p('* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）'),
    img('https://mmbiz.qpic.cn/qrcode.png'),
    cap('/ 更多文章 /'),
    img('https://mmbiz.qpic.cn/a.png'),
    cap('/ 1000个食品创新案例 /'),
    img('https://mmbiz.qpic.cn/b.png'),
    p('预览时标签不可点'),
  ];
}

test('作者识别：首段命中正则 → 提升为 author', () => {
  const elems = [p('作者：Mote莫特'), p('正文第一段。'), p('正文第二段。')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[0].k, 'author', '第 0 项应被提升为 author');
  assert.equal(out[0].text, '作者：Mote莫特');
  assert.equal(out[1].k, 'p', '第 1 项保持 p');
});

test('作者识别：非首段命中不提升', () => {
  const elems = [p('引言段。'), p('作者：xxx'), p('正文。')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[0].k, 'p');
  assert.equal(out[1].k, 'p', '非首段的"作者："不提升');
});

test('作者识别：首段是空 p / 图片 / 标题时，跳到第一个非空 p', () => {
  const elems = [
    { k: 'p', runs: [] },  // 空 p
    h(2, '某个标题'),
    p('作者：Test'),
    p('正文。'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[2].k, 'author', '第一个非空 p 命中作者正则才被提升');
});

test('提示行识别：行首"提示："被提升为 tip', () => {
  const elems = [p('正文。'), p('提示：本文为原创内容。'), p('其他段。')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[1].k, 'tip', '提示行应被提升');
});

test('提示行：非行首的"提示："不提升', () => {
  const elems = [p('在此提示：xxx')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[0].k, 'p', '非行首"提示："保持 p');
});

test('参考来源识别：header 段 + 后续 [n] 条目', () => {
  const elems = [
    p('正文。'),
    p('参考来源：'),
    p('[1] 来源 A'),
    p('[2] 来源 B'),
    p('其他段。'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[1].k, 'ref-header');
  assert.equal(out[2].k, 'ref-item');
  assert.equal(out[3].k, 'ref-item');
  assert.equal(out[4].k, 'p', '非 [n] 开头的段不再被识别为 ref-item');
});

test('参考来源识别：网站系列标题「七、参考来源」保留为 h，后续 [n] 条目仍进 ref-item', () => {
  // WHY：微信源稿用窄长分节图 + h「参考来源」，网站成品是文字标题「七、参考来源」。
  // 标题本身要保留为网站 h2，但后续条目不能退化成黑色正文或被空行拆散。
  const elems = [
    p('正文。'),
    h(2, '七、参考来源'),
    p('[1] 来源 A'),
    p('[2] 来源 B'),
    p('本文未标注来源图片/数据均来自橙宝 本文作者：Ian, FoodTalks'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[1].k, 'h', '参考来源标题应保持 h2，而不是 ref-header');
  assert.equal(out[2].k, 'ref-item');
  assert.equal(out[3].k, 'ref-item');
  assert.equal(out[4].k, 'footer-note');
});

test('参考来源：[n] 条目下的「—说明」延续行也归 ref-item（基石 62285 结构）', () => {
  // 基石 news/62285 参考来源每条 [n] 下紧跟一行破折号说明，整块连续无空行。
  const elems = [
    p('参考来源：'),
    p('[1] Starbucks’ tea party, 2013.12, Fortune'),
    p('—记者Beth Kowitt实地探访NYC Tea Bar'),
    p('[2] Starbucks: The Art of Endless Transformation'),
    p('—Schultz与Oprah婚礼偶遇故事的原始出处'),
    p('提示：之后的正文。'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[0].k, 'ref-header');
  assert.equal(out[1].k, 'ref-item', '[1] 条目');
  assert.equal(out[2].k, 'ref-item', '—说明行也是 ref-item（不 fallback 成正文段）');
  assert.equal(out[3].k, 'ref-item', '[2] 条目');
  assert.equal(out[4].k, 'ref-item', '第二条说明行');
  assert.notEqual(out[5].k, 'ref-item', '说明行之外的段中断 refMode');
});

test('参考来源：保留原编号，不重新编号', () => {
  const elems = [
    p('参考来源：'),
    p('[5] 第五条'),
    p('[7] 第七条'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  const text1 = out[1].runs.map(r => r.text).join('');
  const text2 = out[2].runs.map(r => r.text).join('');
  assert.ok(text1.includes('[5]'));
  assert.ok(text2.includes('[7]'));
});

test('参考来源识别：遇到非 [n] 开头的段就停止', () => {
  const elems = [
    p('参考来源：'),
    p('[1] 第一条'),
    p('随便一段正文'),
    p('[2] 第二条'),  // 不应识别（已停）
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[1].k, 'ref-item');
  assert.equal(out[2].k, 'p');
  assert.equal(out[3].k, 'p', '中断后即使再出现 [2] 也不识别');
});

// 用户 2026-05-31 裁定：页脚必须由本工具【完整输出】（含关注公众号/logo/点击获取更多），
// 旧「不输出这段」契约（及 CLAUDE.md 规则 20）作废。页脚拆为可勾选片段。
test('页脚库：buildFooterHtml 全开 = 完整页脚（含关注公众号/logo/点击获取更多，真实二维码）', () => {
  const full = buildFooterHtml({ biz_coop: true, join_group: true, follow_account: true, logo: true, more_info: true });
  assert.match(full, /商务合作/, '含商务合作');
  assert.match(full, /news\/30752/, '商务合作带真实链接');
  assert.match(full, /\/assets\/fbif\/bobo-card\.webp/, '商务合作含 Bobo 二维码');
  assert.match(full, /加入微信群/, '含加入微信群');
  assert.match(full, /Lemon/, '含 Lemon 微信号');
  assert.match(full, /image\/news\/6e309987/, '含真实加群二维码（非占位符）');
  assert.match(full, /关注公众号/, '含关注公众号（裁定：完整输出）');
  assert.match(full, /b5b448f324a53820/, '含关注公众号真实二维码');
  assert.match(full, /FoodTalks logo/, '含 FoodTalks logo');
  assert.match(full, /点击获取更多/, '含点击获取更多');
  assert.match(full, /www\.foodtalks\.cn\//, '点击获取更多带链接');
  assert.doesNotMatch(full, /\[加入微信群二维码请在/, '不再有占位符');
});

test('页脚库：segments 关闭则不输出对应片段，全关返回空串', () => {
  const simple = buildFooterHtml({ biz_coop: true });
  assert.match(simple, /商务合作/);
  assert.match(simple, /bobo-card\.webp/, '只开商务合作时应输出 Bobo 二维码');
  assert.doesNotMatch(simple, /加入微信群/, 'join_group 关则无加群');
  assert.doesNotMatch(simple, /6e309987660da9af/, 'join_group 关则无加群二维码');
  assert.doesNotMatch(simple, /关注公众号/, 'follow_account 关则无关注公众号');
  assert.equal(buildFooterHtml({}), '', '全关返回空串');
  assert.equal(buildFooterHtml(), '', 'segments 缺省返回空串');
});

test('页脚库：商务合作和加群二维码归属不串片段', () => {
  const biz = buildFooterHtml({ biz_coop: true });
  const group = buildFooterHtml({ join_group: true });
  assert.match(biz, /bobo-card\.webp/, '商务合作片段放 Bobo 二维码');
  assert.doesNotMatch(biz, /6e309987660da9af/, '商务合作片段不放加群二维码');
  assert.match(group, /6e309987660da9af/, '加群片段放群二维码');
  assert.doesNotMatch(group, /bobo-card\.webp/, '加群片段不放 Bobo 二维码');
  assert.doesNotMatch(group, /07079a66940e1ff57e71b140e7e36357/, '加群片段不放 Bobo banner 图');
});

test('页脚库：默认占位符迁移后输出 HTML 与旧固定 URL 字节一致', () => {
  const html = buildFooterHtml({ join_group: true, follow_account: true, logo: true });
  const expected = [
    '<p style="color: #01396a; font-size: 20px;"><strong>加入微信群</strong></p>',
    '<p><br /></p>',
    '<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="https://static.foodtalks.cn/image/news/6e309987660da9af009b89be08c4aa74ed78.png" alt="00" width="600" height="267" /></p>',
    '<p style="text-align: left;"><em><span style="color: #7f7f7f; font-size: 14px;">*如遇二维码失效无法加群，请联系工作人员Lemon（微信号：FBIF_Lemon）。</span></em></p>',
    '<p style="color: #01396a; font-size: 20px;"><strong>关注公众号</strong></p>',
    '<p><br /></p>',
    '<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="https://static.foodtalks.cn/image/post/b5b448f324a538209eb53379bef8dea9a7bf.jpg" alt="" width="600" height="221" /></p>',
    '<p><img style="max-width: 100%; height: 27px; width: 126px;" src="https://static.foodtalks.cn/uploads/images/202007/e155b7852c10a72f7b73157fdf9c4bf0.png" alt="FoodTalks logo" width="130" height="27" border="0" /></p>',
  ].join('\n');
  assert.equal(html, expected);
  assert.doesNotMatch(html, /\{\{asset/, '渲染结果不得残留 asset 占位符');
});

test('页脚库：改素材库里 qr_group 一处，所有 usage 引用自动换图', () => {
  const assetLibrary = JSON.parse(JSON.stringify(ASSET_LIBRARY_DEFAULT));
  const qr = assetLibrary.assets.find(asset => asset.usage === 'qr_group');
  qr.url = 'https://static.foodtalks.cn/image/news/replaced-qr.png';

  const html = buildFooterHtml({ join_group: true }, undefined, assetLibrary, 'ft-default');
  assert.match(html, /replaced-qr\.png/, '页脚引用 qr_group 的位置应使用素材库新 URL');
  assert.doesNotMatch(html, /6e309987660da9af/, '旧二维码 URL 不应继续输出');
});

test('页脚库：FOOTER_PRESETS 含预设，fbif-original 全开含关注公众号', () => {
  const ids = FOOTER_PRESETS.map(x => x.id);
  assert.ok(ids.includes('fbif-original') && ids.includes('none'));
  const orig = FOOTER_PRESETS.find(x => x.id === 'fbif-original');
  assert.equal(orig.segments.follow_account, true, 'fbif-original 含关注公众号');
});

test('作者识别：转载整行「本文为X原创…作者…」也提升为 author', () => {
  const elems = [p('本文为食品内参原创，作者：内参君，编审：橘子，转载请联系出处。'), p('正文。')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[0].k, 'author', '转载整行应提升为 author');
});

test('作者识别：开头「作者：X」+「编辑：Y」分两段 → 合并为单 author 行', () => {
  const elems = [p('作者：Pride'), p('编辑：Bobo'), p('正文。')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[0].k, 'author');
  assert.match(out[0].text, /作者：Pride，编辑：Bobo。/, '作者+编辑合并为一行');
  assert.equal(out.length, 2, '编辑段被并入，数组长度 -1');
  assert.equal(out[1].k, 'p');
});

test('buildSiteFooterTipElem：转载文用提取的来源机构名替换 FBIF，标题带原文链接', () => {
  const e = buildSiteFooterTipElem('某标题', { source: '食品内参', sourceUrl: 'https://mp.weixin.qq.com/s/abc' });
  const text = e.rest_runs.map(r => r.text).join('');
  assert.match(text, /本文为食品内参原创/, '用原来源机构');
  assert.doesNotMatch(text, /FBIF食品饮料创新/, '转载文不写 FBIF');
  const link = e.rest_runs.find(r => r.type === 'link');
  assert.ok(link && link.href === 'https://mp.weixin.qq.com/s/abc', '标题带原文链接');
});

test('extractRepostSource：从「来源：X（ID：y）」提取纯机构名（去 ID）', () => {
  const elems = [p('正文'), p('来源：品牌几何（ID：brand-vista）'), p('* 本文为转载文章。')];
  assert.equal(extractRepostSource(elems), '品牌几何');
});

test('作者合成：开头无作者行但尾部有「来源：X」「作者：Y」→ 合成开头作者行（基石 62245）', () => {
  const elems = [
    p('正文开头。'), p('更多正文。'),
    p('来源：食品内参'),
    p('作者：内参君，编审：橘子'),
    p('* 本文为转载文章。'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true });
  assert.equal(out[0].k, 'author', '开头应插入合成作者行');
  assert.match(out[0].text, /本文为食品内参原创/);
  assert.match(out[0].text, /作者：内参君，编审：橘子/);
  assert.match(out[0].text, /转载请联系出处。$/);
});

test('作者合成：仅有来源、无独立作者段时合成「本文为X原创，转载请联系出处。」', () => {
  const elems = [p('正文。'), p('来源：零售圈（ID: retaileco）'), p('* 本文为转载文章。')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true });
  assert.equal(out[0].k, 'author');
  assert.equal(out[0].text, '本文为零售圈原创，转载请联系出处。');
});

test('作者合成：开头已有作者行时不重复合成', () => {
  const elems = [p('作者：张三'), p('来源：某媒体'), p('作者：李四')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true });
  assert.equal(out[0].k, 'author');
  assert.equal(out[0].text, '作者：张三');
  assert.ok(!out.slice(1).some(e => e.k === 'author'), '不应额外合成作者行');
});

test('作者合成：非 isRepost 不合成', () => {
  const elems = [p('正文。'), p('来源：X'), p('作者：Y')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: false });
  assert.notEqual(out[0].k, 'author');
});

test('buildAuthorLineFromSource：纯函数直测', () => {
  assert.equal(
    buildAuthorLineFromSource([p('来源：食品内参'), p('作者：内参君')]),
    '本文为食品内参原创，作者：内参君，转载请联系出处。'
  );
  assert.equal(buildAuthorLineFromSource([p('正文无来源作者')]), '');
});

test('文末图源/作者：合并成一段时拆为 footer-note 两行', () => {
  // WHY：橙宝微信源稿把「本文未标注来源图片/数据均来自橙宝」和
  // 「本文作者：Ian, FoodTalks」抓成同一个 txt，网站成品要求它们是 14px 灰色斜体小字。
  const out = applyFoodTalksBehavior([
    p('正文结尾。'),
    p('本文未标注来源图片/数据均来自橙宝 本文作者：Ian, FoodTalks'),
  ], DEFAULT_CONFIG);
  assert.equal(out[1].k, 'footer-note');
  assert.deepEqual(out[1].lines, [
    '本文未标注来源图片/数据均来自橙宝',
    '本文作者：Ian, FoodTalks',
  ]);
});

test('尾部清理：公众号"提示："页脚起全切，并补网站口径页脚（isRepost）', () => {
  const out = applyFoodTalksBehavior(wechatTrailerElems(), DEFAULT_CONFIG, {
    ...REPOST,
    title: '星巴克曾花45亿买了茶品牌',
  });
  const texts = out.map(e => (e.runs || []).map(r => r.text).join('') + (e.text || ''));
  const joined = texts.join('\n');
  // 公众号导流全部切除
  assert.doesNotMatch(joined, /更多文章/, '"更多文章"应被切除');
  assert.doesNotMatch(joined, /1000个食品创新案例/, '"1000个案例"应被切除');
  assert.doesNotMatch(joined, /预览时标签不可点/, '"预览时标签不可点"应被切除');
  assert.doesNotMatch(joined, /欢迎转发朋友圈/, '公众号页脚应被切除');
  assert.doesNotMatch(joined, /fbifmedia/, '公众号商务合作微信号应被切除');
  // 二维码图（mmbiz）也随尾部切除
  assert.equal(out.filter(e => e.k === 'img').length, 0, '尾部二维码图应被切除');
  // 参考来源保留
  assert.ok(out.some(e => e.k === 'ref-header'), '参考来源段头保留');
  assert.ok(out.some(e => e.k === 'ref-item'), '参考来源条目保留');
  // 末尾补了网站口径提示页脚
  const last = out[out.length - 1];
  assert.equal(last.k, 'tip', '末尾应是网站口径提示页脚');
  assert.equal(last.title, '提示：', 'tip 应有 title 字段');
  const lastText = last.rest_runs.map(r => r.text).join('');
  assert.match(lastText, /本文为FBIF食品饮料创新（ID：FoodInnovation）原创/, '网站口径声明');
  assert.match(lastText, /《星巴克曾花45亿买了茶品牌》/, '页脚带文章标题');
  assert.match(lastText, /不代表FoodTalks立场，转载请联系出处/, '网站口径立场声明');
});

test('尾部清理：非 isRepost（如 DOCX 直传）不切尾部、不补页脚', () => {
  const out = applyFoodTalksBehavior(wechatTrailerElems(), DEFAULT_CONFIG, { isRepost: false });
  const joined = out.map(e => (e.runs || []).map(r => r.text).join('')).join('\n');
  assert.match(joined, /更多文章/, '非 repost 不应切除尾部');
});

test('尾部清理：仅有导流模块、无公众号"提示："页脚时，切除但不补页脚', () => {
  const elems = [
    p('正文结尾。'),
    cap('/ 更多文章 /'),
    img('https://mmbiz.qpic.cn/a.png'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { ...REPOST, title: 'X' });
  const joined = out.map(e => (e.runs || []).map(r => r.text).join('')).join('\n');
  assert.doesNotMatch(joined, /更多文章/, '"更多文章"应被切除');
  assert.ok(!out.some(e => e.k === 'tip'), '无公众号页脚来源时不应补网站页脚');
});

test('尾部清理：红餐网尾部媒体矩阵从"延伸阅读"起切除', () => {
  const elems = [
    p('正文第一段。'),
    p('Gelato在中国市场继续增长。'),
    p('这部分仍然是正文。'),
    ...Array.from({ length: 6 }, (_, i) => p('正文继续 ' + i)),
    h(2, '延伸阅读'),
    p('一年狂开900店，茶咖品牌都在跟。'),
    p('红餐网已同步入驻多平台。'),
    p('来源：红餐网（ID：hongcan18）'),
    p('作者：小食代，编辑：Bobo'),
    p('内容交流/采访/转载 | 微信：hongcango'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { ...REPOST, title: 'Gelato在中国野蛮生长' });
  const joined = out.map(e => (e.runs || []).map(r => r.text).join('') + (e.text || '')).join('\n');
  assert.match(joined, /Gelato在中国市场继续增长/, '正文保留');
  assert.match(joined, /本文为红餐网原创/, '仍可从原始尾部合成转载作者行');
  assert.doesNotMatch(joined, /延伸阅读/, '延伸阅读起的原媒体尾巴应删除');
  assert.doesNotMatch(joined, /红餐网已同步入驻/, '媒体矩阵应删除');
  assert.doesNotMatch(joined, /内容交流\/采访\/转载/, '联系方式应删除');
});

test('尾部清理：品牌几何 END/版权/矩阵尾巴切除', () => {
  const elems = [
    p('正文第一段。'),
    p('黄仁勋走进蜜雪冰城。'),
    p('这部分仍然是正文。'),
    ...Array.from({ length: 6 }, (_, i) => p('正文继续 ' + i)),
    p('END'),
    p('来源：品牌几何（ID：brand-vista）'),
    p('作者：几何研究员'),
    p('品牌几何资讯矩阵'),
    p('版权声明'),
    p('商务合作 | 电话/微信：123456'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { ...REPOST, title: '黄仁勋喝爆蜜雪冰城' });
  const joined = out.map(e => (e.runs || []).map(r => r.text).join('') + (e.text || '')).join('\n');
  assert.match(joined, /黄仁勋走进蜜雪冰城/, '正文保留');
  assert.match(joined, /本文为品牌几何原创/, '仍可从原始尾部合成转载作者行');
  assert.doesNotMatch(joined, /\nEND\n/, 'END 起的原媒体尾巴应删除');
  assert.doesNotMatch(joined, /品牌几何资讯矩阵/, '媒体矩阵应删除');
  assert.doesNotMatch(joined, /版权声明/, '版权声明应删除');
  assert.doesNotMatch(joined, /商务合作 \| 电话\/微信/, '商务合作尾巴应删除');
});

test('尾部清理：正文前段偶现 END 不误切', () => {
  const elems = [
    p('正文第一段。'),
    p('END'),
    ...Array.from({ length: 12 }, (_, i) => p('正文继续 ' + i)),
    p('真实结尾。'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { ...REPOST, title: 'X' });
  const joined = out.map(e => (e.runs || []).map(r => r.text).join('')).join('\n');
  assert.match(joined, /\nEND\n/, '正文里的 END 应保留');
  assert.match(joined, /正文继续 11/, 'END 后正文应保留');
});

test('尾部清理：正文里偶现"提示："（非公众号页脚）不被误切', () => {
  const elems = [
    p('提示：记得喝茶。'),  // 正文里的提示，后续无公众号特征句
    p('正文继续。'),
    p('参考来源：'),
    p('[1] 来源 A'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { ...REPOST, title: 'X' });
  const joined = out.map(e => (e.runs || []).map(r => r.text).join('')).join('\n');
  assert.match(joined, /记得喝茶/, '正文里的"提示："不应触发切除');
  assert.ok(out.some(e => e.k === 'ref-item'), '其后参考来源仍保留');
});

test('buildSiteFooterTipElem：无标题时省略书名号段', () => {
  const e = buildSiteFooterTipElem('');
  const text = e.rest_runs.map(r => r.text).join('');
  assert.equal(e.k, 'tip');
  assert.equal(e.title, '提示：');
  assert.match(text, /原创，不代表FoodTalks立场/, '无标题时不应出现空书名号');
  assert.doesNotMatch(text, /《》/, '不应出现空书名号');
});

test('elems 数组不被原地修改（pure function）', () => {
  const elems = [p('作者：xxx'), p('正文')];
  const snapshot = JSON.stringify(elems);
  applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(JSON.stringify(elems), snapshot, 'applyFoodTalksBehavior 不能原地修改输入');
});

// ── 2026-06-11 飞书反馈批次（依据成品 news/62303 / 62139 逐元素核对）──

// 反馈问题十六根因：正文「联系XX」板块里的「（扫码关注上海艾录）」图注撞上导流标记
// `扫码.{0,4}关注`，导致其后的参考来源/文末署名整块被切。守卫：切点后仍有参考来源
// 段头 → 跳过该切点，真正的导流尾（参考来源之后）照常切。
test('切尾守卫：正文联系区「扫码关注XX」后仍有参考来源 → 不在此切（62303 上海艾录文）', () => {
  const elems = [
    p('正文段'),
    h(2, '联系上海艾录'),
    p('微信公众号'),
    img('https://mmbiz.qpic.cn/wechat-qr.png'),
    cap('（扫码关注上海艾录）'),
    p('商务合作对接人联系方式'),
    p('彭辉'),
    h(2, '参考来源'),
    p('[1] 来源 A'),
    p('本文未标注来源的图片均来自上海艾录'),
    p('本文作者：Isaac, FoodTalks'),
    p('提示：'),
    p('* 本文为FoodTalks原创首发，欢迎转发朋友圈。'),
    p('关注公众号'),
    p('加入微信群'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, REPOST);
  const texts = out.map(e => (e.runs || []).map(r => r.text).join('') || e.text || e.k);
  assert.ok(out.some(e => e.k === 'cap' && (e.runs || []).map(r => r.text).join('').includes('扫码关注上海艾录')),
    '联系区扫码图注应保留');
  assert.ok(texts.some(t => String(t).includes('彭辉')), '商务对接人名片应保留');
  assert.ok(out.some(e => e.k === 'ref-item'), '参考来源条目应保留');
  assert.ok(out.some(e => e.k === 'footer-note'), '文末署名应保留');
  // 真正的导流尾（参考来源之后的提示页脚/关注公众号/加入微信群）仍要切掉
  assert.ok(!texts.some(t => String(t).includes('加入微信群')), '参考来源后的导流尾仍应切除');
  assert.ok(!texts.some(t => String(t) === '关注公众号'), '关注公众号导流仍应切除');
});

// 反馈问题十/十六：目录条目（tocEntry 标记）不得被剥编号后误判为参考来源段头。
test('目录条目「六、参考来源」带 tocEntry 标记时不被识别为 ref-header', () => {
  const elems = [
    h(2, '目录'),
    { k: 'p', runs: [{ type: 'txt', text: '一、第一章' }], tocEntry: true, skipHeadingPromote: true },
    { k: 'p', runs: [{ type: 'txt', text: '六、参考来源' }], tocEntry: true, skipHeadingPromote: true },
    p('正文'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, {});
  assert.ok(!out.some(e => e.k === 'ref-header'), '目录条目不应变成 ref-header');
  assert.equal(out[2].k, 'p');
});

// 反馈问题九：商务合作导联行 → coop-line（62139/62303 成品 14px 斜体灰）。
test('商务合作导联行识别为 coop-line（两种句式）', () => {
  for (const text of [
    '商务合作对接请点击：上海艾录商务合作联系方式',
    '如有合作意向，请移至“联系上海艾录”获取联系方式',
  ]) {
    const out = applyFoodTalksBehavior([p('正文'), p(text)], DEFAULT_CONFIG, {});
    assert.equal(out[1].k, 'coop-line', text);
  }
});

// 反馈问题十四/十六：「*注：…」「本文未标注来源的图片均来自XX」按文末小字（footer-note）渲染。
test('footer_note 默认正则覆盖 *注 与 本文未标注来源的图片均来自 变体', () => {
  const out = applyFoodTalksBehavior([
    p('正文'),
    p('*注：CO₂排放减量数据来源于Promix网站提供的计算工具'),
    p('本文未标注来源的图片均来自上海艾录'),
  ], DEFAULT_CONFIG, {});
  assert.equal(out[1].k, 'footer-note');
  assert.equal(out[2].k, 'footer-note');
});

test('ft-series：编号参考来源普通段保留为 h2，后续 [n] 仍进 ref-item', () => {
  // WHY：6/15 上海艾录 DOCX 的「六、参考来源」是全粗普通段。系列网站应把它当标题，
  // 但后续参考条目仍要走灰色 ref-item；不能提前降级成 ref-header。
  const out = applyFoodTalksBehavior([
    p('正文'),
    p('六、参考来源'),
    p('[1] 来源 A'),
    p('[2] 来源 B'),
  ], SERIES_CONFIG, {});
  assert.equal(out[1].k, 'h');
  assert.equal(out[1].level, 2);
  assert.equal(out[2].k, 'ref-item');
  assert.equal(out[3].k, 'ref-item');
});

test('ft-series：兼容 DOCX 错字「本文为标注来源的图片均来自」为文末小字', () => {
  // WHY：用户上传 DOCX 原文少了「未」，标准稿是「未标注」。工具不改正文内容，
  // 但在 ft-series 中窄容错，避免样式降级成普通正文。
  const out = applyFoodTalksBehavior([
    p('正文'),
    p('本文为标注来源的图片均来自上海艾录'),
    p('本文作者：Isaac, FoodTalks'),
  ], SERIES_CONFIG, {});
  assert.equal(out[1].k, 'footer-note');
  assert.deepEqual(out[1].lines, ['本文为标注来源的图片均来自上海艾录']);
  assert.equal(out[2].k, 'footer-note');
});

test('ft-series：相关阅读组打标为紧凑段，非系列默认不触发', () => {
  const series = applyFoodTalksBehavior([
    p('相关阅读：'),
    p('第一篇相关文章'),
    p('第二篇相关文章'),
    { ...p('正文继续。'), blankBefore: true },
  ], SERIES_CONFIG, {});
  assert.equal(series[0].relatedReading, true);
  assert.equal(series[1].relatedReading, true);
  assert.equal(series[2].relatedReading, true);
  assert.equal(series[3].relatedReading, undefined);

  const normal = applyFoodTalksBehavior([
    p('相关阅读：'),
    p('第一篇相关文章'),
  ], DEFAULT_CONFIG, {});
  assert.equal(normal[0].relatedReading, undefined);
  assert.equal(normal[1].relatedReading, undefined);
});
