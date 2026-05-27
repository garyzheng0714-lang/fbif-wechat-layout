import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyFoodTalksBehavior,
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

test('elems 数组不被原地修改（pure function）', () => {
  const elems = [p('作者：xxx'), p('正文')];
  const snapshot = JSON.stringify(elems);
  applyFoodTalksBehavior(elems, DEFAULT_CONFIG);
  assert.equal(JSON.stringify(elems), snapshot, 'applyFoodTalksBehavior 不能原地修改输入');
});
