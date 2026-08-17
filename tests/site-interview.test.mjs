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
    { type: 'txt', text: '《中国企业家》', interviewSpeaker: 'interviewer' },
    { type: 'txt', text: '：第一个问题？' },
  ]);
  assert.deepEqual(out[1].runs.slice(0, 2), [
    { type: 'txt', text: '朱富强', interviewSpeaker: 'interviewee' },
    { type: 'txt', text: '：第一个回答。' },
  ]);
  assert.equal(out[5].runs.at(-1).bold, true, '原有正文加粗不得丢失');
});

test('无书名号的机构提问方按问号率/首现判定为提问方（2026-08-17 62807 实证形态）', () => {
  // TopDigital 提问（问句带问号）、吴昌盛回答——规范：提问方 FoodTalks 蓝并加粗。
  const elems = [
    p('TopDigital：当初为什么选择从常温即食鸡胸肉切入？'),
    p('吴昌盛：我们当时看到了健身人群的真实需求。'),
    p('TopDigital：如何保持战略定力？'),
    p('吴昌盛：核心是回到用户价值。'),
    p('TopDigital：接下来会怎么走？'),
    p('吴昌盛：继续把品类做深。'),
  ];
  const out = markInterviewSpeakerPrefixes(elems);
  assert.equal(out[0].runs[0].interviewSpeaker, 'interviewer', 'TopDigital 应判提问方');
  assert.equal(out[1].runs[0].interviewSpeaker, 'interviewee', '吴昌盛 应判回答方');
});

test('双方都无问号时，首个出现的角色是提问方（先问后答惯例）', () => {
  const elems = [
    p('主持人：请谈谈今年的计划。'),
    p('张三：我们打算扩产。'),
    p('主持人：还有别的吗。'),
    p('张三：还有出海。'),
    p('主持人：谢谢分享。'),
    p('张三：谢谢。'),
  ];
  const out = markInterviewSpeakerPrefixes(elems);
  assert.equal(out[0].runs[0].interviewSpeaker, 'interviewer');
  assert.equal(out[1].runs[0].interviewSpeaker, 'interviewee');
});

test('提问方前缀独立成段时与下一段提问内容合并（规范：提问方+提问内容间不空行）', () => {
  const elems = [
    p('TopDigital：'),
    p('当初为什么选择这个赛道？'),
    p('吴昌盛：看到了真实需求。'),
    p('TopDigital：如何保持定力？'),
    p('吴昌盛：回到用户价值。'),
    p('TopDigital：未来的计划？'),
    p('吴昌盛：把品类做深。'),
  ];
  const out = markInterviewSpeakerPrefixes(elems);
  assert.equal(out.length, elems.length - 1, '独立前缀段应与内容段合并');
  assert.equal(out[0].runs[0].interviewSpeaker, 'interviewer');
  assert.equal(out[0].runs[0].text, 'TopDigital');
  const merged = out[0].runs.map(r => r.text).join('');
  assert.equal(merged, 'TopDigital：当初为什么选择这个赛道？');
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
