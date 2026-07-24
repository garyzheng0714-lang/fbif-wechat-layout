import test from 'node:test';
import assert from 'node:assert/strict';
import { markInterviewSpeakerPrefixes } from '../public/js/site-interview.js';

const p = (text, extraRuns = []) => ({
  k: 'p',
  runs: [{ type: 'txt', text }, ...extraRuns],
});

test('重复交替出现的采访者和受访者前缀会被标记，正文强调保持不变', () => {
  const elems = [
    p('《中国企业家》：第一个问题？'),
    p('朱富强：第一个回答。'),
    p('《中国企业家》：第二个问题？'),
    p('朱富强：第二个回答。'),
    p('《中国企业家》：第三个问题？'),
    p('朱富强：第三个回答。', [{ type: 'txt', text: '重点', bold: true }]),
  ];

  const out = markInterviewSpeakerPrefixes(elems);

  assert.deepEqual(out[0].runs.slice(0, 2), [
    { type: 'txt', text: '《中国企业家》', interviewSpeaker: 'publication' },
    { type: 'txt', text: '：第一个问题？' },
  ]);
  assert.deepEqual(out[1].runs.slice(0, 2), [
    { type: 'txt', text: '朱富强', interviewSpeaker: 'person' },
    { type: 'txt', text: '：第一个回答。' },
  ]);
  assert.equal(out[5].runs.at(-1).bold, true, '原有正文加粗不得丢失');
});

test('偶发冒号短语和单一重复标签不是访谈，不加样式', () => {
  const elems = [
    p('健康意识觉醒：'),
    p('消费者更加关注健康。'),
    p('提示：第一条'),
    p('提示：第二条'),
    p('提示：第三条'),
  ];

  assert.deepEqual(markInterviewSpeakerPrefixes(elems), elems);
});
