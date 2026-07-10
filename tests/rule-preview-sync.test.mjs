import test from 'node:test';
import assert from 'node:assert/strict';

import {
  requestRulePreviewSync,
  rulePreviewCommitted,
  rulePreviewSavedMessage,
} from '../public/js/rule-preview-sync.js';

test('requestRulePreviewSync 优先读取引擎的结构化提交结果', async () => {
  const calls = [];
  const result = await requestRulePreviewSync({
    rerenderWithStatus() {
      calls.push('detailed');
      return Promise.resolve({ status: 'committed' });
    },
    rerender() { calls.push('legacy'); },
  }, true);
  assert.deepEqual(result, { status: 'committed' });
  assert.deepEqual(calls, ['detailed']);
});

test('requestRulePreviewSync 把引擎异常收敛为 failed，不把服务端保存伪装成回滚', async () => {
  const result = await requestRulePreviewSync({
    rerenderWithStatus() { throw new Error('preview exploded'); },
  }, true);
  assert.deepEqual(result, { status: 'failed', error: 'preview exploded' });
  assert.match(rulePreviewSavedMessage(result), /规则已保存，但当前预览刷新失败/);
  assert.equal(rulePreviewCommitted(result), false);
});

test('输入处理期的 deferred 明确表示规则已保存、预览稍后自动应用', () => {
  const result = { status: 'deferred' };
  assert.match(rulePreviewSavedMessage(result), /当前文章排版完成后会自动应用/);
  assert.equal(rulePreviewCommitted(result), false);
  assert.equal(rulePreviewCommitted({ status: 'noop' }), true);
  assert.equal(rulePreviewCommitted({ status: 'not-requested' }), true);
});
