// block-editor-v2.test.mjs — 规则后台底部编辑器对「双预设(原创/转载)」结构的行为测试。
// 守护回归：FBIF 切 schema_version=2(presets[]) 后，旧编辑器只认顶层 blocks/order → 渲染「暂无区块」，
// 整个 FBIF 底部在规则后台消失。这类「源码看似实现、渲染却空白」的 bug 源码静态断言抓不到，必须真渲染。
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { createBlockEditor } from '../public/js/admin/block-editor.js';

function mkDoc() {
  return parseHTML('<!DOCTYPE html><html><head></head><body></body></html>').document;
}
function mount(doc) { const c = doc.createElement('div'); doc.body.appendChild(c); return c; }

const V2 = {
  schema_version: 2, enabled: true, active_preset_id: 'original',
  presets: [
    { id: 'original', name: '原创', order: ['tip', 'img'], blocks: {
      tip: { type: 'text', enabled: true, fields: { content: '原创声明' } },
      img: { type: 'image', enabled: true, fields: { image: '' } } } },
    { id: 'repost', name: '转载', order: ['tip', 'img'], blocks: {
      tip: { type: 'text', enabled: true, fields: { content: '转载声明' } },
      img: { type: 'image', enabled: true, fields: { image: '' } } } },
  ],
};
const V1 = { enabled: true, gap: 'blank_line', order: ['a', 'b'], blocks: {
  a: { type: 'image', enabled: true, fields: { image: '' } },
  b: { type: 'more_articles', enabled: true, fields: { title: '/ 更多文章 /' } } } };

test('V2 双预设不再「暂无区块」：渲染 active 预设的块 + 原创/转载切换 tab', () => {
  const doc = mkDoc();
  const c = mount(doc);
  createBlockEditor({ container: c, module: V2, channel: 'mp', onChange: () => {}, doc });
  assert.doesNotMatch(c.textContent, /暂无区块/, 'V2 不应显示「暂无区块」');
  const tabs = [...c.querySelectorAll('.be-preset')].map(b => b.textContent.trim());
  assert.deepEqual(tabs, ['原创', '转载'], '应有原创/转载两个预设 tab');
  const ids = [...c.querySelectorAll('.be-card')].map(card => card.dataset.id);
  assert.deepEqual(ids, ['tip', 'img'], '默认渲染 active(original) 预设的区块');
  assert.doesNotMatch(c.textContent, /模块间默认空行/, 'V2 不显示 gap 开关（间距走 spacer 块）');
});

test('切换到转载 tab → active 切换 + 渲染转载预设；编辑写回对应预设不串台', () => {
  const doc = mkDoc();
  const c = mount(doc);
  let captured = null;
  createBlockEditor({ container: c, module: V2, channel: 'mp', onChange: (m) => { captured = m; }, doc });
  const repostTab = [...c.querySelectorAll('.be-preset')].find(b => b.textContent.trim() === '转载');
  assert.ok(repostTab, '转载 tab 存在');
  repostTab.click();
  assert.equal(c.querySelector('.be-preset.active').textContent.trim(), '转载', '点击后转载 tab 激活');
  // 在转载预设下删一个块，应只改 repost 预设，original 不动
  const delBtn = [...c.querySelectorAll('.be-card')].find(card => card.dataset.id === 'img').querySelector('.be-del');
  delBtn.click();
  const orig = captured.presets.find(p => p.id === 'original');
  const rep = captured.presets.find(p => p.id === 'repost');
  assert.deepEqual(rep.order, ['tip'], '转载预设删掉了 img');
  assert.deepEqual(orig.order, ['tip', 'img'], '原创预设不受影响（不串台）');
});

test('v1 单套（系列）路径不变：直接渲染顶层块、无预设 tab、有空行开关', () => {
  const doc = mkDoc();
  const c = mount(doc);
  createBlockEditor({ container: c, module: V1, channel: 'mp', onChange: () => {}, doc });
  assert.equal(c.querySelectorAll('.be-preset').length, 0, 'v1 无预设 tab');
  const ids = [...c.querySelectorAll('.be-card')].map(card => card.dataset.id);
  assert.deepEqual(ids, ['a', 'b'], 'v1 渲染顶层块');
  assert.match(c.textContent, /模块间默认空行/, 'v1 显示空行开关');
});
