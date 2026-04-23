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

// ---- Pre-existing wrong-direction curly quotes (Word/WPS output) ----

test('curly: all-right double quotes get re-paired', () => {
  const input = '围美多用\u201D汤用豆腐\u201D\u201D煎用豆腐\u201D';
  const out = convertText(input);
  assert.ok(!out.includes('\u201D\u201D'), 'no adjacent right-right pairs');
  assert.ok(!out.includes('\u201C\u201C'), 'no adjacent left-left pairs');
  assert.equal(out, '围美多用\u201C汤用豆腐\u201D\u201C煎用豆腐\u201D');
});

test('curly: all-left double quotes get re-paired', () => {
  const input = '他叫\u201C豆碟豆腐\u201C';
  const out = convertText(input);
  assert.equal(out, '他叫\u201C豆碟豆腐\u201D');
});

test('curly: mixed wrong direction gets fixed', () => {
  const input = '他说\u201D你好\u201C';
  assert.equal(convertText(input), '他说\u201C你好\u201D');
});

test('curly: all-right single quotes inside double get re-paired', () => {
  const input = '他说\u201D再见\u2019朋友\u2019就走了\u201D';
  const out = convertText(input);
  assert.ok(out.includes('\u201C') && out.includes('\u201D'));
  assert.ok(out.includes('\u2018') && out.includes('\u2019'));
  assert.ok(!out.includes('\u201D\u201D'));
  assert.ok(!out.includes('\u2019\u2019'));
});

test("curly: mid-word U+2019 kept as apostrophe, not a closing quote", () => {
  const input = '他说 it\u2019s not true 但其实是真的';
  const out = convertText(input);
  assert.ok(out.includes('it\u2019s'));
});

test('curly: screenshot line — 汤用豆腐/煎用豆腐 no same-direction pairs remain', () => {
  const input = '圍美多用\u201D汤用豆腐\u201D\u201D煎用豆腐\u201D，把的\u201D\u201D炒的\u201D\u201D煎的\u201D、\u201D麻婆豆腐专用\u201D。';
  const out = convertText(input);
  assert.ok(!out.includes('\u201D\u201D'));
  assert.ok(!out.includes('\u201C\u201C'));
});

test('curly: closing quote after 。 is correctly closed, not reopened', () => {
  // Real screenshot: both quotes are U+201C (left-curly) in Word's output.
  // The closer sits right after `。`, which naive context rules misread as
  // an "opening context". Strict alternation gets it right.
  const input = '\u201C山姆的豆腐也能撕开，因为供货商也是圃美多。\u201C';
  const out = convertText(input);
  assert.equal(out, '\u201C山姆的豆腐也能撕开，因为供货商也是圃美多。\u201D');
});

test('alternation: closing quote after 。 with ASCII input', () => {
  const input = '"山姆的豆腐也能撕开，因为供货商也是圃美多。"';
  assert.equal(convertText(input), '\u201C山姆的豆腐也能撕开，因为供货商也是圃美多。\u201D');
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

// ---- Paragraph break resets quote stack (#4) ----

test('paragraphs: unclosed quote in para 1 does not swallow para 2 opening', () => {
  const input = '他说"你好。\n\n他又说"这不对"。';
  const out = convertText(input);
  assert.ok(out.includes('他又说“这不对”。'));
});

test('paragraphs: multiple paragraphs each get independent pairing', () => {
  const input = '"第一段"。\n\n"第二段"。\n\n"第三段"。';
  const out = convertText(input);
  assert.equal(out, '“第一段”。\n\n“第二段”。\n\n“第三段”。');
});

test('paragraphs: paragraph break with spaces between newlines', () => {
  const input = '上段"甲"\n  \n下段"乙"';
  const out = convertText(input);
  assert.equal(out, '上段“甲”\n  \n下段“乙”');
});

test('paragraphs: CRLF (Windows) paragraph break resets the quote stack', () => {
  const input = '上段"甲"\r\n\r\n下段"乙"';
  const out = convertText(input);
  assert.equal(out, '上段“甲”\r\n\r\n下段“乙”');
});

// ---- Pre-existing curly quotes mixed with ASCII (#2) ----

test('curly+ASCII: already-curly stays intact and threads the stack', () => {
  const input = '他说“你好”，然后"再见"';
  const out = convertText(input);
  assert.equal(out, '他说“你好”，然后“再见”');
});

test('curly+ASCII: ASCII pair right after a curly pair', () => {
  const input = '“前文”，然后"后文"的内容';
  const out = convertText(input);
  assert.equal(out, '“前文”，然后“后文”的内容');
});

test('curly+ASCII: curly right double does not push a phantom opener', () => {
  const input = '”A”';
  const out = convertText(input);
  assert.equal(out, '”A”');
});

// ---- URL trailing CJK punctuation (#6) ----

test('mask: URL is not eaten by trailing Chinese period', () => {
  const out = convertText('参考 https://example.com。很好');
  assert.ok(out.includes('https://example.com'));
  assert.ok(out.endsWith('。很好'));
});

test('mask: URL stops before CJK comma and bracket', () => {
  const out = convertText('链接 https://example.com，或 https://b.org）结束');
  assert.ok(out.includes('https://example.com'));
  assert.ok(out.includes('https://b.org'));
  assert.ok(out.includes('，'));
  assert.ok(out.includes('）'));
});

// ---- PUA sentinel collision (#5) ----

test('mask: stray U+E000 in input survives unmask unchanged', () => {
  const input = '你好\uE000世界。';
  const out = convertText(input);
  assert.ok(out.includes('\uE000'), 'user PUA preserved');
  assert.ok(out.includes('世界。'));
});

test('mask: sentinel-shaped sequence with out-of-range index is preserved', () => {
  const origWarn = console.warn;
  console.warn = () => {};
  try {
    const input = '前\uE000\uE0019999\uE002\uE003后。';
    const out = convertText(input);
    assert.ok(out.includes('\uE000\uE0019999\uE002\uE003'), 'out-of-range sentinel preserved, not dropped');
  } finally {
    console.warn = origWarn;
  }
});

// ---- Neighbor skipping: newline / fullwidth / nbsp (#3) ----

test('neighbor: quote pair closes across a soft newline', () => {
  const input = '"你好\n世界"';
  const out = convertText(input);
  assert.equal(out, '“你好\n世界”');
});

test('neighbor: quote pair closes across a fullwidth space', () => {
  const input = '"你好\u3000世界"';
  const out = convertText(input);
  assert.equal(out, '“你好\u3000世界”');
});

test('neighbor: quote pair closes across a no-break space', () => {
  const input = '"你好\u00A0世界"';
  const out = convertText(input);
  assert.equal(out, '“你好\u00A0世界”');
});

// ---- convertRuns fallback now converts (#1) ----

test('runs: ellipsis in joined text no longer disables quote conversion', () => {
  const runs = [
    { type: 'txt', text: '他说...然后' },
    { type: 'txt', text: '"这不对"，' },
    { type: 'txt', text: '就走了。' },
  ];
  const out = convertRuns(runs);
  const joined = out.map(r => r.text).join('');
  assert.ok(joined.includes('……'), 'ellipsis converted');
  assert.ok(joined.includes('“这不对”'), 'quotes converted in slow path');
});

test('runs: HTML in joined text still yields quote conversion on slow path', () => {
  const runs = [
    { type: 'txt', text: '看<b>这</b>段"重点"，' },
    { type: 'txt', text: '明白吗?' },
  ];
  const out = convertRuns(runs);
  const joined = out.map(r => r.text).join('');
  assert.ok(joined.includes('“重点”'), 'quotes converted even with HTML in joined text');
  assert.ok(joined.includes('？'), 'question mark converted');
});

// ---- Unbalanced / orphan quotes ----

test('orphan: lone opening quote stays curly-open, no crash', () => {
  const out = convertText('他说"你好');
  assert.equal(out, '他说“你好');
});

test('orphan: never emits identical-direction adjacent pair', () => {
  const out = convertText('你好"，完了');
  assert.ok(!out.includes('““'));
  assert.ok(!out.includes('””'));
});
