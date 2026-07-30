// fetch-timeout 验证「网络卡死不让 UI 永久转圈」这一意图：
// 超时必须主动 abort 并抛出可读中文提示，成功/普通错误必须原样透传，
// 否则上层无法据此恢复按钮、给提示或回退本地缓存。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fetchWithTimeout } from '../public/js/fetch-timeout.js';

test('成功响应原样透传（不被超时包装干扰）', async () => {
  const resp = await fetchWithTimeout('/x', { method: 'POST' }, {
    fetchImpl: async (url, opts) => ({ ok: true, url, hasSignal: !!opts.signal }),
  });
  assert.equal(resp.ok, true);
  assert.equal(resp.url, '/x');
  assert.equal(resp.hasSignal, true, '应注入 AbortSignal 供超时打断');
});

test('超时必须 abort 底层请求并抛出中文提示', async () => {
  let aborted = false;
  const fetchImpl = (_url, opts) => new Promise((_resolve, reject) => {
    opts.signal.addEventListener('abort', () => {
      aborted = true;
      reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
    });
  });
  await assert.rejects(
    fetchWithTimeout('/x', {}, { fetchImpl, timeoutMs: 10, timeoutMessage: '抓取超时，请检查网络后重试' }),
    /抓取超时/,
  );
  assert.equal(aborted, true, '超时应触发 AbortController.abort()');
});

test('非超时错误原样冒泡（不被误判成超时）', async () => {
  const fetchImpl = async () => { throw new Error('boom-network'); };
  await assert.rejects(fetchWithTimeout('/x', {}, { fetchImpl }), /boom-network/);
});

test('调用方作废旧任务时立即 abort，不能伪装成网络超时', async () => {
  const controller = new AbortController();
  const fetchImpl = (_url, opts) => new Promise((_resolve, reject) => {
    opts.signal.addEventListener('abort', () => {
      reject(Object.assign(new Error('superseded'), { name: 'AbortError' }));
    });
  });
  const pending = fetchWithTimeout('/x', { signal: controller.signal }, {
    fetchImpl,
    timeoutMs: 1000,
    timeoutMessage: '不应出现这个超时提示',
  });
  controller.abort();

  await assert.rejects(pending, /superseded/);
});
