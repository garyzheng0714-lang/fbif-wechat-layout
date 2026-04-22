import { test } from 'node:test';
import assert from 'node:assert/strict';
import { convertText, convertRuns, detectLanguage } from '../public/js/punctuation.js';

// ---- Language detection ----

test('detectLanguage: pure Chinese paragraph', () => {
  assert.equal(detectLanguage('第一段位是把豆腐切小，不浪费了'), 'zh');
});

test('detectLanguage: pure English paragraph', () => {
  assert.equal(detectLanguage('See the README.md for more info.'), 'en');
});

test('detectLanguage: mixed with majority Chinese (Asahico case)', () => {
  assert.equal(detectLanguage('第二段位是帮你加点味, Asahico 的"四季膳"系列把芥末'), 'zh');
});

test('detectLanguage: mostly English with stray CJK', () => {
  assert.equal(detectLanguage('The Asahico 四季膳 product line'), 'en');
});

// ---- Basic punctuation in Chinese context ----

test('convert: comma between CJK', () => {
  assert.equal(convertText('35 克一小盒,一盒八个'), '35 克一小盒，一盒八个');
});

test('convert: period between CJK', () => {
  assert.equal(convertText('不浪费了.但切、调味、开火还是你的事'), '不浪费了。但切、调味、开火还是你的事');
});

test('convert: parentheses in Chinese context', () => {
  assert.equal(convertText('他(小王)来了'), '他（小王）来了');
});

test('convert: colon and semicolon', () => {
  assert.equal(convertText('做小:加味;做完'), '做小：加味；做完');
});

test('convert: exclamation and question', () => {
  assert.equal(convertText('真的吗?太好了!'), '真的吗？太好了！');
});

// ---- Protected regions ----

test('convert: keep comma inside 35,000', () => {
  assert.equal(convertText('一人锅系列累计卖了 35,000 份。'), '一人锅系列累计卖了 35,000 份。');
});

test('convert: keep period inside 3.14', () => {
  assert.equal(convertText('圆周率是 3.14 左右。'), '圆周率是 3.14 左右。');
});

test('convert: keep Co., Ltd. commas and periods inside clearly-Chinese paragraph', () => {
  const input = '来自 Asahico Co., Ltd. 的产品，质量非常棒，很值得推荐。';
  const out = convertText(input);
  assert.ok(out.includes('Co., Ltd.'), 'Co., Ltd. preserved');
  assert.ok(out.includes('质量非常棒，'), 'Chinese comma converted');
});

test('convert: preserve URLs untouched', () => {
  const out = convertText('参考 https://example.com/a.b?c=1 的说明。');
  assert.ok(out.includes('https://example.com/a.b?c=1'));
  assert.ok(out.endsWith('说明。'));
});

test('convert: preserve email untouched', () => {
  const out = convertText('联系 foo.bar@example.com 获取更多信息。');
  assert.ok(out.includes('foo.bar@example.com'));
});

test('convert: preserve inline code', () => {
  const out = convertText('使用 `arr.push(1,2,3)` 即可。');
  assert.ok(out.includes('`arr.push(1,2,3)`'));
});

test('convert: preserve fenced code block', () => {
  const out = convertText('示例：\n```js\nconst x = {a:1, b:2};\n```\n结束。');
  assert.ok(out.includes('const x = {a:1, b:2};'));
});

// ---- English paragraphs stay English ----

test('convert: English paragraph untouched', () => {
  const input = 'See the README.md for more info, please.';
  assert.equal(convertText(input), input);
});

// ---- Smart quote pairing ----

test('quotes: simple Chinese-context double quote pair', () => {
  assert.equal(convertText('他叫"豆碟豆腐",35 克一小盒'), '他叫“豆碟豆腐”，35 克一小盒');
});

test('quotes: multiple pairs in one paragraph', () => {
  assert.equal(
    convertText('在"妈妈"和"独居青年"两个完全不同的人群里同时火了'),
    '在“妈妈”和“独居青年”两个完全不同的人群里同时火了',
  );
});

test('quotes: never emits same-direction pair (primary bug fix)', () => {
  const out = convertText('"你好"和"世界"');
  assert.ok(!out.includes('““'), 'no double left quotes adjacent');
  assert.ok(!out.includes('””'), 'no double right quotes adjacent');
  assert.equal(out, '“你好”和“世界”');
});

test('quotes: nested single inside double', () => {
  const out = convertText('他说"再见\'朋友\'就走了"');
  assert.ok(out.includes('“'));
  assert.ok(out.includes('”'));
  assert.ok(out.includes('‘'));
  assert.ok(out.includes('’'));
});

test("quotes: apostrophe in English-only paragraph left alone", () => {
  assert.equal(convertText("it's a test"), "it's a test");
});

test("quotes: Chinese paragraph with English it's keeps apostrophe as right single", () => {
  const input = "他说 it's not true 但其实是真的";
  const out = convertText(input);
  assert.ok(out.includes("it’s"));
});

test('quotes: opening quote after Chinese comma', () => {
  const out = convertText('他说，"这不可能"');
  assert.equal(out, '他说，“这不可能”');
});

test('quotes: opening quote at paragraph start', () => {
  assert.equal(convertText('"用勺子挖着吃的沙拉"'), '“用勺子挖着吃的沙拉”');
});

// ---- User screenshot cases ----

test('screenshot: 豆碟豆腐 paragraph', () => {
  const input = '男前豆腐还有一款更有意思的产品叫"豆碟豆腐",35 克一小盒,一盒八个';
  const expected = '男前豆腐还有一款更有意思的产品叫“豆碟豆腐”，35 克一小盒，一盒八个';
  assert.equal(convertText(input), expected);
});

test('screenshot: Asahico 四季膳 mixed paragraph', () => {
  const input = '第二段位是帮你加点味, Asahico 的"四季膳"系列把芥末、毛豆的味道直接揉进豆腐本体,80g×4,撕开就能当下酒菜。';
  const out = convertText(input);
  assert.ok(out.includes('Asahico 的“四季膳”系列'));
  assert.ok(out.includes('加点味，'));
  assert.ok(out.includes('豆腐本体，'));
  assert.ok(out.includes('80g×4，'));
});

test('screenshot: 用勺子挖着吃的沙拉 case', () => {
  const input = '2021 年直接定位成"用勺子挖着吃的沙拉".';
  assert.equal(convertText(input), '2021 年直接定位成“用勺子挖着吃的沙拉”。');
});

// ---- Ellipsis and dash ----

test('ellipsis: ... becomes …… in Chinese context', () => {
  assert.equal(convertText('等等…等等...好吧'), '等等…等等……好吧');
});

test('em-dash: -- becomes —— in Chinese context', () => {
  assert.equal(convertText('这是一个--例子'), '这是一个——例子');
});

test('em-dash: does not touch hyphen in word', () => {
  const input = 'co-founder 是 start-up 的合伙人。';
  const out = convertText(input);
  assert.ok(out.includes('co-founder'));
  assert.ok(out.includes('start-up'));
});

// ---- convertRuns ----

test('runs: single text run gets converted', () => {
  const runs = [{ type: 'txt', text: '他叫"小明",是吗?' }];
  const out = convertRuns(runs);
  assert.equal(out[0].text, '他叫“小明”，是吗？');
});

test('runs: preserves non-text runs untouched', () => {
  const runs = [
    { type: 'txt', text: '看图：' },
    { type: 'img', file: 'foo.png' },
    { type: 'txt', text: ',很清楚。' },
  ];
  const out = convertRuns(runs);
  assert.equal(out[0].text, '看图：');
  assert.equal(out[1].type, 'img');
  assert.equal(out[2].text, '，很清楚。');
});

test('runs: cross-run quote pairing', () => {
  const runs = [
    { type: 'txt', text: '他叫"豆碟', bold: false },
    { type: 'txt', text: '豆腐",真好吃。', bold: true },
  ];
  const out = convertRuns(runs);
  assert.equal(out[0].text, '他叫“豆碟');
  assert.equal(out[1].text, '豆腐”，真好吃。');
  assert.equal(out[1].bold, true);
});

test('runs: English-only runs left alone', () => {
  const runs = [{ type: 'txt', text: 'See the README.md, please.' }];
  const out = convertRuns(runs);
  assert.equal(out[0].text, 'See the README.md, please.');
});

test('runs: empty array returns empty array', () => {
  assert.deepEqual(convertRuns([]), []);
});

test('runs: idempotent on already-converted text', () => {
  const runs = [{ type: 'txt', text: '他叫“豆碟豆腐”，35 克一小盒。' }];
  const out = convertRuns(runs);
  assert.equal(out[0].text, '他叫“豆碟豆腐”，35 克一小盒。');
});
