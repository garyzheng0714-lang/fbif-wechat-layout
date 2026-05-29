import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyFoodTalksBehavior,
  buildSiteFooterTipElem,
  FBIF_FOOTER_HTML,
} from '../public/js/rule-behavior-foodtalks.js';

const DEFAULT_CONFIG = {
  behavior: {
    author_regex: '^\\s*作者[：:]\\s*',
    tip_regex: '^\\s*提示[：:]',
    ref_header_regex: '^\\s*参考(?:来源|资料)[：:]\\s*$',
    ref_item_regex: '^\\s*\\[\\d+\\]',
  },
  modules: {
    fbif_footer: { enabled: false },
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

test('FBIF_FOOTER_HTML 是常量字符串', () => {
  assert.equal(typeof FBIF_FOOTER_HTML, 'string');
  assert.ok(FBIF_FOOTER_HTML.length > 100, 'FBIF_FOOTER_HTML 应该有合理长度');
  assert.match(FBIF_FOOTER_HTML, /商务合作/, '应包含商务合作引导');
  assert.match(FBIF_FOOTER_HTML, /Lemon/, '应包含 Lemon 微信号');
  assert.match(FBIF_FOOTER_HTML, /加入微信群/, '应包含加入微信群占位');
  assert.doesNotMatch(FBIF_FOOTER_HTML, /关注公众号/, 'FBIF footer 不应含"关注公众号"标题');
  assert.doesNotMatch(FBIF_FOOTER_HTML, /点击获取更多/, 'FBIF footer 不应含"点击获取更多"');
  assert.doesNotMatch(FBIF_FOOTER_HTML, /FoodTalks logo/i, 'FBIF footer 不应含 FoodTalks logo');
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
  const lastText = last.runs.map(r => r.text).join('');
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
  const text = e.runs.map(r => r.text).join('');
  assert.equal(e.k, 'tip');
  assert.match(text, /原创，不代表FoodTalks立场/, '无标题时不应出现空书名号');
  assert.doesNotMatch(text, /《》/, '不应出现空书名号');
});

test('elems 数组不被原地修改（pure function）', () => {
  const elems = [p('作者：xxx'), p('正文')];
  const snapshot = JSON.stringify(elems);
  applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(JSON.stringify(elems), snapshot, 'applyFoodTalksBehavior 不能原地修改输入');
});
