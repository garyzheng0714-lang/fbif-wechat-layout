// 红线下放·公众号正向验证：证明 fbif 渲染器经 getActiveRuleBehavior().params 读取
// 【服务端覆盖的识别正则】并驱动输出（非只回退 const）。环境搭建对齐
// fbif-characterization.test.mjs（全局 stub + window._activeConfig 注入服务端 behavior）。
import test from 'node:test';
import assert from 'node:assert/strict';

const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };
globalThis.window = {
  __APP_BUILD_INFO: { build: 'test' },
  localStorage: storage,
  // 服务端 behavior 覆盖：把参考来源段头正则改成只认「我的引用」。
  _activeConfig: {
    behavior: {
      params: { ref_section_regex: { type: 'string', default: '^我的引用$' } },
    },
  },
};
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });

const fbif = (await import('../public/js/templates/fbif.js?fbifdownstream=' + Date.now())).default;

test('fbif processBlocks 用 behavior.params.ref_section_regex 覆盖识别参考段头', () => {
  const data = {
    title: '',
    blocks: [
      { k: 'txt', runs: [{ type: 'txt', text: '我的引用' }] },
      { k: 'txt', runs: [{ type: 'txt', text: '[1] 某文献 2026' }] },
    ],
  };
  const { lines } = fbif.processBlocks(data, {});
  // 覆盖后「我的引用」应被识别为参考段头：其所在行带 wx-tr 引用样式（默认 RE_REF_SECTION
  // 只认「参考来源/文献…」，不含「我的引用」，无覆盖时该行会是普通正文 wx-t）。
  const refLine = lines.find(l => l.includes('我的引用'));
  assert.ok(refLine, '「我的引用」应在输出中');
  assert.ok(refLine.includes('wx-tr'),
    `「我的引用」应按覆盖正则渲染为参考样式 wx-tr，实际：${refLine}`);
  // 反证：后续 [n] 条目也进入引用区（wx-tr），证明覆盖正则触发了整段 refMode。
  const itemLine = lines.find(l => l.includes('某文献'));
  assert.ok(itemLine && itemLine.includes('wx-tr'), '参考条目应进入引用区 wx-tr');
});
