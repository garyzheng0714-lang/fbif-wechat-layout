// 校对用的文字工具：排好的 HTML → 纯文字、找到一处错、改掉它（public/js/text-fixes.js）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { articleText, fixHtmlLines, fixPlainText, locate } from '../public/js/text-fixes.js';

test('送去校对的正文：一段一行，标签去掉、实体还原、空行不要', () => {
  const lines = [
    '<p class="wx-p"><span class="wx-t">走进超市，A&amp;B 两个牌子&nbsp;都在。</span></p>',
    '<p><img src="a.png" alt="不算正文"></p>',
    '<section><p>第一句<br>第二句</p><p>第三句</p></section>',
    '',
  ];
  assert.equal(articleText(lines), '走进超市，A&B 两个牌子 都在。\n第一句\n第二句\n第三句');
});

test('找一处错：只认原句里的那一处，不看空白和引号', () => {
  const text = '神圣在国内很常见。\n经认证，圣在国内高端品牌鸡蛋细分市场，圣迪乐销量领先。';
  const spot = locate(text, '圣在', '圣在国内高端品牌鸡蛋');
  assert.equal(text.slice(spot.start, spot.end), '圣在');
  assert.equal(text.slice(0, spot.start).endsWith('经认证，'), true, '不能认成前面「神圣在」里的那一处');

  const quoted = '他说“农夫 三泉”很好喝。';
  const hit = locate(quoted, '农夫三泉', '他说"农夫三泉"很好喝');
  assert.equal(quoted.slice(hit.start, hit.end), '农夫 三泉');
});

test('找一处错：原句对不上时全文只有一处才认，跨了行的不认', () => {
  assert.deepEqual(locate('只有一处腌采。', '腌采', '模型抄走了样的原句'), { start: 4, end: 6 });
  assert.equal(locate('这里腌采，那里也腌采。', '腌采', '模型抄走了样的原句'), null);
  assert.equal(locate('上一行重回\n重回下一行', '重回重回', ''), null);
  assert.equal(locate('正文', '', '正文'), null);
});

test('纯文字里改掉：只换原句里的那一处', () => {
  const fixed = fixPlainText('神圣在国内。圣在国内高端品牌鸡蛋。', [{ wrong: '圣在', correct: '圣迪乐在', evidence: '圣在国内高端品牌鸡蛋' }]);
  assert.equal(fixed.text, '神圣在国内。圣迪乐在国内高端品牌鸡蛋。');
  assert.deepEqual(fixed.applied, [true]);
});

test('HTML 里改掉：只动不一样的字，加粗和链接原样留着', () => {
  const line = '<p><span class="t">有些菌种</span><strong>转为健身</strong><a href="https://x.cn/?a=1&amp;b=2">牛马打造</a>、很细。</p>';
  const { lines, applied } = fixHtmlLines([line], [{ wrong: '转为健身牛马打造', correct: '专为健身牛马打造', evidence: '有些菌种转为健身牛马打造' }]);
  assert.deepEqual(applied, [true]);
  assert.equal(lines[0], '<p><span class="t">有些菌种</span><strong>专为健身</strong><a href="https://x.cn/?a=1&amp;b=2">牛马打造</a>、很细。</p>');
});

test('HTML 里改掉：叠字删后面多的，漏字补在前一个字后面，改后的字照样转义', () => {
  const lines = [
    '<p>仿佛<em>重回重</em>回高中生物课堂</p>',
    '<p>经认证，<b>圣</b>在国内高端品牌鸡蛋</p>',
    '<p>写成 A&lt;B 了</p>',
  ];
  const fixed = fixHtmlLines(lines, [
    { wrong: '重回重回', correct: '重回', evidence: '仿佛重回重回高中生物课堂' },
    { wrong: '圣在', correct: '圣迪乐在', evidence: '圣在国内高端品牌鸡蛋' },
    { wrong: 'A<B', correct: 'A>B', evidence: '写成 A<B 了' },
  ]);
  assert.deepEqual(fixed.applied, [true, true, true]);
  assert.deepEqual(fixed.lines, [
    '<p>仿佛<em>重回</em>高中生物课堂</p>',
    '<p>经认证，<b>圣迪乐</b>在国内高端品牌鸡蛋</p>',
    '<p>写成 A&gt;B 了</p>',
  ]);
});

test('HTML 里改掉：同一行两处都改上，找不到的那处说没改上、别的照改', () => {
  const fixed = fixHtmlLines(['<p>层不出穷的腌采</p>'], [
    { wrong: '层不出穷', correct: '层出不穷', evidence: '层不出穷的腌采' },
    { wrong: '不存在的字', correct: '别的', evidence: '' },
    { wrong: '腌采', correct: '腌菜', evidence: '层不出穷的腌采' },
  ]);
  assert.deepEqual(fixed.applied, [true, false, true]);
  assert.equal(fixed.lines[0], '<p>层出不穷的腌菜</p>');
});

test('没有要改的就原样返回', () => {
  const lines = ['<p>甲</p>', '<p>乙</p>'];
  assert.deepEqual(fixHtmlLines(lines, []).lines, lines);
  assert.equal(fixPlainText('标题', undefined).text, '标题');
});
