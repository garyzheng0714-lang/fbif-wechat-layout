import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyFoodTalksBehavior,
  buildSiteFooterTipElem,
  extractRepostSource,
  buildAuthorLineFromSource,
  buildFooterHtml,
  pickSiteTipSourceUrl,
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

const TIP_ENABLED_CONFIG = {
  ...DEFAULT_CONFIG,
  modules: {
    fbif_footer: {
      enabled: true,
      active_segments: { tip: true },
    },
  },
};

const TIP_DISABLED_CONFIG = {
  ...DEFAULT_CONFIG,
  modules: {
    fbif_footer: {
      enabled: true,
      active_segments: { tip: false },
    },
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
function textOf(e) {
  return e ? (e.runs || []).map(r => r.text).join('') + (e.text || '') : '';
}

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

test('作者识别：非首段命中不提升为开头署名', () => {
  const elems = [p('引言段。'), p('作者：xxx'), p('正文。')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(out[0].k, 'p');
  // 非首段的"作者："绝不提升为 author（开头署名）；短文里它落在尾部区域时
  // 会被阶段 4.5 降级为 footer-note 灰色小字（2026-08-17 运营反馈：尾部裸署名行不再黑粗）。
  assert.notEqual(out[1].k, 'author', '非首段的"作者："不得提升为开头署名');
  assert.equal(out[1].k, 'footer-note', '尾部区域的裸署名行应降级为灰色小字');
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

test('提示声明模块：左侧开启时即使原文没有提示段也生成网站声明', () => {
  const elems = [p('正文第一段。')];
  const out = applyFoodTalksBehavior(elems, TIP_ENABLED_CONFIG, { title: '测试标题' });
  const tip = out.find(e => e.k === 'tip');
  assert.ok(tip, '提示声明模块开启时应补网站声明');
  assert.equal(tip.title, '提示：');
  assert.match(tip.rest_runs.map(r => r.text).join(''), /测试标题/);
});

test('提示声明模块：左侧关闭时不主动生成网站声明', () => {
  const elems = [p('正文第一段。')];
  const out = applyFoodTalksBehavior(elems, TIP_DISABLED_CONFIG, { title: '测试标题' });
  assert.ok(!out.some(e => e.k === 'tip'), '提示声明模块关闭时不应补网站声明');
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

test('pickSiteTipSourceUrl：自家号转发且无阅读原文时不链回提交的公众号页', () => {
  const elems = [p('正文'), p('来源：惊蛰研究所（ID：jingzheyanjiusuo）')];
  assert.equal(
    pickSiteTipSourceUrl({
      source: 'https://mp.weixin.qq.com/s/fbif-repost',
      source_name: 'FBIF食品饮料创新',
    }, elems),
    '',
    '不得把自家转载页当原文',
  );
  assert.equal(
    pickSiteTipSourceUrl({
      source: 'https://mp.weixin.qq.com/s/fbif-repost',
      source_name: 'FBIF食品饮料创新',
      original_source_url: 'https://www.tmtpost.com/8103277.html',
    }, elems),
    'https://www.tmtpost.com/8103277.html',
    '有阅读原文/核验原文时用它',
  );
  assert.equal(
    pickSiteTipSourceUrl({
      source: 'https://mp.weixin.qq.com/s/jingzhe-original',
      source_name: '惊蛰研究所',
    }, [p('正文')]),
    'https://mp.weixin.qq.com/s/jingzhe-original',
    '提交的就是原账号文章时应保留',
  );
});

test('buildSiteFooterTipElem：转载文用提取的来源机构名替换 FBIF，标题带原文链接', () => {
  const e = buildSiteFooterTipElem('某标题', { source: '食品内参', sourceUrl: 'https://mp.weixin.qq.com/s/abc' });
  const text = e.rest_runs.map(r => r.text).join('');
  assert.match(text, /本文为食品内参原创/, '用原来源机构');
  assert.doesNotMatch(text, /FBIF食品饮料创新/, '转载文不写 FBIF');
  const link = e.rest_runs.find(r => r.type === 'link');
  assert.ok(link && link.href === 'https://mp.weixin.qq.com/s/abc', '标题带原文链接');
  const noLink = buildSiteFooterTipElem('某标题', { source: '惊蛰研究所', sourceUrl: '' });
  assert.equal(noLink.rest_runs.some(r => r.type === 'link'), false, '没有可核验原文时标题不带链接');
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
  const rest = out.map(e => (e.runs || []).map(r => r.text).join('') + (e.text || '')).join('\n');
  assert.doesNotMatch(rest, /^来源：食品内参/m, '开头已有署名则删掉文末来源行');
  assert.doesNotMatch(rest, /^作者：内参君/m, '开头已有署名则删掉文末作者行');
});

test('作者合成：仅有来源、无独立作者段时合成「本文为X原创，转载请联系出处。」', () => {
  const elems = [p('正文。'), p('来源：零售圈（ID: retaileco）'), p('* 本文为转载文章。')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true });
  assert.equal(out[0].k, 'author');
  assert.equal(out[0].text, '本文为零售圈原创，转载请联系出处。');
  assert.ok(!out.some(e => ((e.runs || []).map(r => r.text).join('') + (e.lines || []).join('')).includes('来源：零售圈')),
    '开头已有署名则文末来源行应删除');
});

test('作者合成：开头已有作者行时只补来源，不重复合成', () => {
  const elems = [p('作者：张三'), p('来源：某媒体'), p('作者：李四')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true });
  assert.equal(out[0].k, 'author');
  assert.equal(out[0].text, '本文为某媒体原创，作者：张三，转载请联系出处。');
  assert.ok(!out.slice(1).some(e => e.k === 'author'), '不应额外合成作者行');
});

// 2026-10-14 核对近一月 FoodTalks 自动发布：转载稿开头只有「作者：…」的 32 篇里，
// 24 篇被编辑在后台手改成「本文为{来源}原创，作者：…，转载请联系出处。」。以下逐字取自编辑终稿。
test('转载署名：开头只有作者行时补上来源（编辑终稿口径）', () => {
  const cases = [
    [[p('作者：国君，编辑：子琪。'), p('正文。'), p('来源：咖门（ID：kamen）')],
      '本文为咖门原创，作者：国君，编辑：子琪，转载请联系出处。'],
    [[p('作者：Chloe Sorvino'), p('翻译：Lemin'), p('正文。'), p('来源：福布斯')],
      '本文为福布斯原创，作者：Chloe Sorvino，翻译：Lemin，转载请联系出处。'],
    [[p('作者：常韵'), p('采访：常韵、Sophia'), p('正文。'), p('来源：TopDigital')],
      '本文为TopDigital原创，作者：常韵，采访：常韵、Sophia，转载请联系出处。'],
    [[p('编译：李言'), p('正文。'), p('来源：联商网')],
      '本文为联商网原创，编译：李言，转载请联系出处。'],
  ];
  for (const [elems, want] of cases) {
    const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true });
    assert.equal(out[0].k, 'author');
    assert.equal(out[0].text, want);
    assert.equal(out.filter(e => e.k === 'author').length, 1, '只一行署名');
    assert.equal(textOf(out[1]), '正文。', '翻译/编译/采访行并进署名，不再单独成段');
  }
});

// 2026-10-28 用户用零售商业财经 https://mp.weixin.qq.com/s/y9m1xFDGK2Dycv5w-GJOPQ 排网站默认：
// 正文没有「来源：X」行，开头只有「作者：柴元锦」「@零售商业财经 原创出品」，文末声明却写成了 FBIF。
test('转载署名：正文没写来源时用公众号名，开头与文末声明都是它', () => {
  const elems = [p('作者：柴元锦 '), p('@零售商业财经  原创出品'), p('正文。')];
  const out = applyFoodTalksBehavior(elems, TIP_ENABLED_CONFIG, { isRepost: true, title: '麦德龙启动B2B2C战略', sourceName: '零售商业财经' });
  assert.equal(out[0].k, 'author');
  assert.equal(out[0].text, '本文为零售商业财经原创，作者：柴元锦，转载请联系出处。');
  assert.equal(textOf(out[1]), '正文。', '「@零售商业财经 原创出品」已写进署名，删掉');
  const tip = out.find(e => e.k === 'tip');
  const tipText = tip.rest_runs.map(r => r.text).join('');
  assert.match(tipText, /^\*本文为零售商业财经原创《麦德龙启动B2B2C战略》/);
  assert.doesNotMatch(tipText, /FBIF/);

  const noAuthor = applyFoodTalksBehavior([p('正文。')], DEFAULT_CONFIG, { isRepost: true, sourceName: '灵兽' });
  assert.equal(noAuthor[0].text, '本文为灵兽原创，转载请联系出处。', '开头没有作者行也照模板补一行');

  const declared = applyFoodTalksBehavior([p('作者：张三'), p('正文。'), p('来源：咖门')], DEFAULT_CONFIG, { isRepost: true, sourceName: '某转发号' });
  assert.equal(declared[0].text, '本文为咖门原创，作者：张三，转载请联系出处。', '正文写了来源以正文为准');

  const own = applyFoodTalksBehavior([p('作者：Pride'), p('正文。')], TIP_ENABLED_CONFIG, { isRepost: true, title: 'T', sourceName: 'FBIF食品饮料创新' });
  assert.equal(own[0].text, '作者：Pride', '自家号不补来源');
  assert.match(own.find(e => e.k === 'tip').rest_runs.map(r => r.text).join(''), /FBIF食品饮料创新（ID：FoodInnovation）原创/);

  const body = applyFoodTalksBehavior([p('作者：柴元锦'), p('正文。'), p('零售商业财经原创出品的报告显示……')], DEFAULT_CONFIG, { isRepost: true, sourceName: '零售商业财经' });
  assert.equal(textOf(body[2]), '零售商业财经原创出品的报告显示……', '整行不只是出品声明的不删');
});

// 2026-10-30 #294：FBIF 号发的合作稿，公众号文末「* 本文为转载文章。」，没写原作者；
// 网站文末却写成了「本文为FBIF食品饮料创新…原创」（用户：「以微信公众号文章为准」）。
test('文末声明：自家号明写「本文为转载文章」、说不出来源时写转载，不写 FBIF 原创', () => {
  const tail = [p('提示：'), p('* 本文为转载文章。'), p('* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）')];
  const out = applyFoodTalksBehavior([p('正文。'), ...tail], DEFAULT_CONFIG, { isRepost: true, title: 'T', sourceName: 'FBIF食品饮料创新' });
  const tipText = out.find(e => e.k === 'tip').rest_runs.map(r => r.text).join('');
  assert.equal(tipText, '*本文为转载文章《T》，不代表FoodTalks立场，转载请联系出处。');

  const own = applyFoodTalksBehavior([p('正文。'), p('提示：'), p('* 本文为FBIF原创，欢迎转发朋友圈；')], DEFAULT_CONFIG, { isRepost: true, title: 'T', sourceName: 'FBIF食品饮料创新' });
  assert.match(own.find(e => e.k === 'tip').rest_runs.map(r => r.text).join(''), /^\*本文为FBIF食品饮料创新（ID：FoodInnovation）原创《T》/, '自家原创照旧');
});

// 同日 #294 重新同步：重抓回来的尾巴少了联系 Bobo 那行，「提示：」认不出是公众号页脚，原样留在网站上。
test('切尾：「提示：」后紧跟「本文为转载文章」也认作公众号页脚', () => {
  const elems = [p('正文。'), p('参考来源：'), p('[1] 某规划纲要'), p('提示：'), p('* 本文为转载文章。'), p('/ 更多文章 /'), img('https://mmbiz.qpic.cn/a.png')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true, title: 'T', provenance: { original: false } });
  const texts = out.map(textOf);
  assert.ok(!texts.includes('提示：') && !texts.includes('* 本文为转载文章。'), '公众号「提示：」页脚切掉');
  assert.equal(out.filter(e => e.k === 'tip').length, 1);
  assert.equal(out.find(e => e.k === 'tip').rest_runs.map(r => r.text).join(''), '*本文为转载文章《T》，不代表FoodTalks立场，转载请联系出处。');
});

// 发布流程交来后台的判定（和 CMS 原创/转载同一个函数，认 AI 抽的出处和「本文转载自X」等写法），
// 文末声明照它写：#19/#102/#119/#309/#311 正文只有「本文经授权转自《晚点LatePost》」这类写法，原先都写成了 FBIF 原创。
test('文末声明：照发布流程交来的原创/转载判定写', () => {
  const tail = [p('提示：'), p('* 本文为转载文章。'), p('* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）')];
  const tipOf = (elems, provenance) => applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true, title: 'T', provenance })
    .find(e => e.k === 'tip').rest_runs.map(r => r.text).join('');
  const head = p('本文经授权转自《晚点LatePost》（ID：postlate）；作者：董慧，编辑：黄俊杰。');
  assert.match(tipOf([head, p('正文。'), ...tail], { original: false, source: '晚点LatePost' }), /^\*本文为晚点LatePost原创《T》/);
  assert.match(tipOf([p('正文。'), ...tail], { original: false, source: '' }), /^\*本文为转载文章《T》/);
  assert.match(tipOf([p('正文。'), p('来源：咖门（ID：kamen）'), ...tail], { original: false, source: '咖门饮品' }), /^\*本文为咖门原创《T》/,
    '正文写了「来源：X」以正文为准，和开头署名同一来源');
  assert.match(tipOf([p('正文。'), p('提示：'), p('* 本文为FBIF原创，欢迎转发朋友圈；')], { original: true }), /^\*本文为FBIF食品饮料创新（ID：FoodInnovation）原创《T》/);
});

// 用户 2026-10-30：「网站排版只认得「来源：某某」这一种写法」——开头署名和文末声明照同一个判定写。
test('开头署名：照后台的原创/转载判定补来源，开头已写明出处的不补', () => {
  const tail = [p('提示：'), p('* 本文为转载文章。'), p('* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）')];
  const run = (elems, opts) => applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true, title: 'T', ...opts });
  const tipText = out => out.find(e => e.k === 'tip').rest_runs.map(r => r.text).join('');

  const named = run([p('作者：董慧'), p('正文。'), ...tail], { provenance: { original: false, source: '晚点LatePost' } });
  assert.equal(named[0].text, '本文为晚点LatePost原创，作者：董慧，转载请联系出处。');
  assert.match(tipText(named), /^\*本文为晚点LatePost原创《T》/, '开头和文末同一来源');

  const synthesized = run([p('正文。'), p('作者：董慧'), ...tail], { provenance: { original: false, source: '晚点LatePost' } });
  assert.equal(synthesized[0].text, '本文为晚点LatePost原创，作者：董慧，转载请联系出处。', '开头没署名时照判定合成');

  // #309：编辑把「本文转载自公众号新周刊」原样留在开头。
  const declared = run([p('本文转载自公众号新周刊（ID：new-weekly）'), p('作者：hh'), p('编辑：陆一鸣'), p('正文。'), ...tail],
    { provenance: { original: false, source: '新周刊' } });
  assert.deepEqual(declared.slice(0, 2).map(textOf), ['作者：hh。', '本文转载自公众号新周刊（ID：new-weekly）'], '开头已写明出处，不再补「本文为新周刊原创」');
  assert.match(tipText(declared), /^\*本文为新周刊原创《T》/);

  const toggled = run([p('作者：董慧'), p('正文。'), ...tail], { isRepost: false, provenance: { original: false, source: '晚点LatePost' } });
  assert.equal(toggled[0].text, '作者：董慧', '编辑在网页上点了「原创」');
  assert.match(tipText(toggled), /^\*本文为FBIF食品饮料创新（ID：FoodInnovation）原创《T》/);
});

test('转载署名：自家稿与数据出处不补来源', () => {
  const own = applyFoodTalksBehavior([p('作者：Pride'), p('编辑：Bobo'), p('正文。')], DEFAULT_CONFIG, { isRepost: true });
  assert.equal(own[0].text, '作者：Pride，编辑：Bobo。', 'FBIF 原创保持作者行');
  const fbif = applyFoodTalksBehavior([p('作者：Pride'), p('正文。'), p('来源：FBIF食品饮料创新')], DEFAULT_CONFIG, { isRepost: true });
  assert.equal(fbif[0].text, '作者：Pride', '来源是自家号不补');
  const data = [p('作者：Pride'), p('正文。'), p('来源：英敏特报告《跨界饮料 – 中国 – 2025年》')];
  assert.equal(extractRepostSource(data), '', '数据出处不是转载来源');
  assert.equal(applyFoodTalksBehavior(data, DEFAULT_CONFIG, { isRepost: true })[0].text, '作者：Pride');
});

test('开头已有网站署名时删除文末公众号口径来源/作者（数英 DIGITALING 实证）', () => {
  const elems = [
    p('正文最后一段，一起走向下一个30年。'),
    p('来源：数英DIGITALING（ID：digitaling）'),
    p('作者：Weavy'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true, title: '从山姆30年里，看见长期主义' });
  assert.equal(out[0].k, 'author');
  assert.match(out[0].text, /本文为数英DIGITALING原创/);
  assert.match(out[0].text, /作者：Weavy/);
  const rest = out.slice(1).map(e => {
    if (e.k === 'footer-note') return (e.lines || []).join('\n');
    return (e.runs || []).map(r => r.text).join('') + (e.text || '');
  }).join('\n');
  assert.doesNotMatch(rest, /来源：数英/, '文末来源行应删除');
  assert.doesNotMatch(rest, /作者：Weavy/, '文末作者行应删除');
});

// 2026-10-15 对照 FoodTalks 线上终稿：编辑把文末译者挪进开头署名（#126/#127），其余人员行
// 直接删（#97 新媒体编辑/视觉、#99 新视觉/创意、#139 总编辑、#142 采访、#143 审核）。
const bodyFill = n => Array.from({ length: n }, (_, i) => p('正文段落 ' + i + '。'));
const restText = out => out.map(e => {
  if (e.k === 'footer-note') return (e.lines || []).join('\n');
  return (e.runs || []).map(r => r.text).join('') + (e.text || '');
}).join('\n');

test('文末人员行：译者并进开头署名，其余删掉（编辑终稿口径）', () => {
  const cases = [
    [[p('作者：Chloe Sorvino'), ...bodyFill(10), p('翻译：Lemin'), p('来源：福布斯')],
      '本文为福布斯原创，作者：Chloe Sorvino，翻译：Lemin，转载请联系出处。'],
    [[...bodyFill(10), p('编译：李言'), p('来源：联商网')],
      '本文为联商网原创，编译：李言，转载请联系出处。'],
    [[p('作者：黄茗婷'), ...bodyFill(10), p('新媒体编辑：乔悦'), p('视觉：顾芗'), p('来源：盐财经')],
      '本文为盐财经原创，作者：黄茗婷，转载请联系出处。'],
    [[p('作者：斯小乐'), ...bodyFill(10), p('新视觉/创意：BOEN'), p('来源：风味星球')],
      '本文为风味星球原创，作者：斯小乐，转载请联系出处。'],
    [[p('作者：Hanna Zhou'), ...bodyFill(10), p('总编辑：范怿'), p('审核：kiki'), p('采访：Ruonan，阿拜'), p('来源：胖鲸头条')],
      '本文为胖鲸头条原创，作者：Hanna Zhou，转载请联系出处。'],
  ];
  for (const [elems, want] of cases) {
    const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true, title: 'T' });
    assert.equal(out[0].k, 'author');
    assert.equal(out[0].text, want);
    const rest = restText(out.slice(1));
    assert.doesNotMatch(rest, /翻译：|编译：|新媒体编辑|视觉：|创意：|总编辑|审核：|采访：/, '文末人员行不再单独出现');
    assert.match(rest, /正文段落 9。/, '正文保留');
  }
});

test('文末人员行：开头没有署名时降级为灰色小字', () => {
  const elems = [...bodyFill(10), p('英文来源：Rabobank'), p('中文编辑：Riri')];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: false, title: 'T' });
  const notes = out.filter(e => e.k === 'footer-note').map(e => e.lines.join(''));
  assert.deepEqual(notes, ['英文来源：Rabobank', '中文编辑：Riri']);
});

test('开头署名：「来源」行夹在署名与「编译」之间时编译仍并入（#162 联商网）', () => {
  const elems = [p('来源：联商网'), p('编译：李言'), img('a.png'), cap('图片来源：dpa/Marcel Kusch'), ...bodyFill(6)];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true, title: 'T' });
  assert.equal(out[0].k, 'author');
  assert.equal(out[0].text, '本文为联商网原创，编译：李言，转载请联系出处。');
  const rest = restText(out.slice(1));
  assert.doesNotMatch(rest, /来源：联商网|编译：李言/);
});

test('来源号文末关注引导与口号切掉（#128 Vista看天下）', () => {
  const elems = [
    p('点击上方蓝字关注我们'),
    ...bodyFill(12),
    p('谁能用产品接住这份情绪，谁就能切出一块新大陆。'),
    p('本文经授权转载自Vista看天下，点击下方按钮立刻关注。'),
    p('不苛求正确，只好好说话。'),
    p('来源：Vista看天下'),
    p('提示：'),
    p('* 本文为转载文章，欢迎转发朋友圈；'),
    p('* 转载请在后台回复“转载”了解规则；'),
  ];
  const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true, title: 'T' });
  const joined = restText(out);
  assert.match(joined, /切出一块新大陆/, '正文结尾保留');
  assert.doesNotMatch(joined, /点击下方按钮立刻关注/);
  assert.doesNotMatch(joined, /不苛求正确/, '关注引导后的账号口号一并切掉');
  assert.match(joined, /正文段落 11。/, '开头的关注引导不触发切尾，正文完整');
  const tip = out.find(e => e.k === 'tip');
  assert.ok(tip, '切掉的部分里有公众号「提示：」页脚，仍补 FoodTalks 转载声明');
  assert.match(tip.rest_runs.map(r => r.text).join(''), /本文为Vista看天下原创《T》/);
});

test('文末「点击在看、点赞」引导切掉，不被升成小标题（灵兽）', () => {
  const elems = [
    ...bodyFill(12),
    p('这些都在提醒后面想入局的后来者，得谨慎一点。'),
    { k: 'p', runs: [{ type: 'txt', text: '喜欢文章点击"在看、点赞、分享"', bold: true }] },
  ];
  const joined = restText(applyFoodTalksBehavior(elems, DEFAULT_CONFIG, { isRepost: true, title: 'T' }));
  assert.match(joined, /得谨慎一点/, '正文结尾保留');
  assert.doesNotMatch(joined, /在看/);
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

test('尾部清理：自家稿 isRepost=false 同样切除微信导流尾', () => {
  const out = applyFoodTalksBehavior(wechatTrailerElems(), DEFAULT_CONFIG, {
    isRepost: false,
    title: '印尼博士创业',
  });
  const joined = out.map(e => (e.runs || []).map(r => r.text).join('') + (e.text || '')).join('\n');
  assert.doesNotMatch(joined, /更多文章/, '进网站一律切尾，与是否原创无关');
  assert.doesNotMatch(joined, /欢迎转发朋友圈/, '公众号提示页脚应被切除');
  assert.doesNotMatch(joined, /预览时标签不可点/, '微信更多文章残留应被切除');
  const last = out[out.length - 1];
  assert.equal(last.k, 'tip', '切掉公众号提示后应补网站口径提示');
  assert.match(last.rest_runs.map(r => r.text).join(''), /本文为FBIF食品饮料创新（ID：FoodInnovation）原创/);
});

test('尾部清理：「提示：」被识别成标题或图注时仍切除公众号尾注', () => {
  // 2026-09-08 头条任务 #175：「提示：」抓成 h，尾注残留导致发布被拦截。
  for (const tip of [h(2, '提示：'), cap('提示：')]) {
    const elems = wechatTrailerElems();
    elems[2] = tip;
    const out = applyFoodTalksBehavior(elems, DEFAULT_CONFIG, REPOST);
    assert.doesNotMatch(JSON.stringify(out), /欢迎转发朋友圈|fbifmedia|更多文章/);
    assert.ok(out.some(e => e.k === 'ref-item'), '正文参考来源必须保留');
    assert.equal(out.at(-1).k, 'tip', '切尾后仍补网站口径提示');
  }
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
