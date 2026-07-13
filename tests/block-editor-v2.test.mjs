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

test('V2 保存输出不携带已退役的顶层 blocks/order/gap', () => {
  const doc = mkDoc();
  const c = mount(doc);
  const polluted = JSON.parse(JSON.stringify(V2));
  polluted.blocks = { ghost: { type: 'text', fields: { content: '旧字段' } } };
  polluted.order = ['ghost'];
  polluted.gap = 'blank_line';
  let captured = null;
  const editor = createBlockEditor({ container: c, module: polluted, channel: 'mp', onChange: (m) => { captured = m; }, doc });
  const initial = editor.getModule();
  assert.equal('blocks' in initial, false, 'getModule 不应输出顶层 blocks');
  assert.equal('order' in initial, false, 'getModule 不应输出顶层 order');
  assert.equal('gap' in initial, false, 'getModule 不应输出顶层 gap');

  c.querySelector('.be-del').click();
  assert.ok(captured, '编辑后应触发 onChange');
  assert.equal('blocks' in captured, false, 'onChange 不应输出顶层 blocks');
  assert.equal('order' in captured, false, 'onChange 不应输出顶层 order');
  assert.equal('gap' in captured, false, 'onChange 不应输出顶层 gap');
  assert.equal(captured.schema_version, 2);
  assert.ok(Array.isArray(captured.presets), 'V2 数据必须留在 presets[] 内');
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

test('公众号底部新增下拉只剩 4 原子（旧语义块 2026-06-06 归网站渠道）', async () => {
  const { blockTypesForChannel } = await import('../public/js/blocks/block-registry.gen.js');
  const mp = blockTypesForChannel('mp').map(t => t.type).sort();
  assert.deepEqual(mp, ['image', 'more_articles', 'spacer', 'text'], '公众号下拉只 4 原子');
  assert.ok(!mp.some(t => ['rich_html', 'declaration', 'name_card', 'qr_card', 'titled_media'].includes(t)), '公众号下拉不含旧语义块');
});

test('底部区块编辑器提供上下移动、展开和上下文明确的控件名称', () => {
  const doc = mkDoc();
  const c = mount(doc);
  createBlockEditor({ container: c, module: V2, channel: 'mp', onChange: () => {}, doc });

  assert.equal(c.querySelector('.be-add select').getAttribute('aria-label'), '新增区块类型');
  assert.equal(c.querySelector('.be-presets').getAttribute('role'), 'tablist');
  assert.equal(c.querySelector('.be-preset.active').getAttribute('aria-selected'), 'true');

  const cards = [...c.querySelectorAll('.be-card')];
  assert.equal(cards.length, 2);
  cards.forEach(card => {
    const label = card.getAttribute('aria-label');
    assert.ok(label && /区块$/.test(label), '卡片有可理解的分组名称');
    assert.equal(card.querySelector('.be-toggle').tagName, 'BUTTON', '展开入口必须是按钮');
    assert.match(card.querySelector('.be-enabled input').getAttribute('aria-label'), /默认启用/);
    assert.match(card.querySelector('.be-del').getAttribute('aria-label'), /^删除/);
    assert.ok(card.querySelector('[data-block-action="up"]'), '提供键盘可用的上移按钮');
    assert.ok(card.querySelector('[data-block-action="down"]'), '提供键盘可用的下移按钮');
  });
  assert.ok(cards[0].querySelector('[data-block-action="up"]').disabled, '首项不可继续上移');
  assert.ok(cards[1].querySelector('[data-block-action="down"]').disabled, '末项不可继续下移');
});

test('键盘移动区块后保持操作焦点，字段连续输入不会被重渲染打断', () => {
  const doc = mkDoc();
  let focused = doc.body;
  Object.defineProperty(doc, 'activeElement', { configurable: true, get: () => focused });
  doc.defaultView.HTMLElement.prototype.focus = function focus() { focused = this; };
  const c = mount(doc);
  let captured = null;
  createBlockEditor({ container: c, module: V2, channel: 'mp', onChange: (m) => { captured = m; }, doc });

  c.querySelector('.be-card[data-id="tip"] [data-block-action="down"]').click();
  assert.deepEqual(captured.presets[0].order, ['img', 'tip']);
  assert.equal(doc.activeElement?.dataset.blockAction, 'down', '移动后焦点留在同一区块操作上');

  c.querySelector('.be-card[data-id="tip"] .be-toggle').click();
  const field = c.querySelector('.be-card[data-id="tip"] .be-fields textarea, .be-card[data-id="tip"] .be-fields input[type="text"]');
  assert.ok(field, '文字区块应有可编辑字段');
  field.value = '第一段';
  field.dispatchEvent(new doc.defaultView.Event('input', { bubbles: true }));
  assert.ok(c.contains(field), '第一次输入后不得替换当前控件');
  field.value = '第一段继续输入';
  field.dispatchEvent(new doc.defaultView.Event('input', { bubbles: true }));
  assert.ok(c.contains(field), '连续输入期间控件保持连接');
});

test('富文本工具栏异步挂载后仍为图标按钮提供可访问名称', async () => {
  const doc = mkDoc();
  const previousWindow = globalThis.window;
  globalThis.window = doc.defaultView;
  doc.defaultView.wangEditor = {
    createEditor: () => ({ destroy() {} }),
    createToolbar: ({ selector, config }) => {
      queueMicrotask(() => {
        config.toolbarKeys.filter(key => key !== '|').forEach(key => {
          const button = doc.createElement('button');
          if (key === 'fontSize') button.textContent = '默认字号';
          selector.appendChild(button);
        });
      });
    },
  };

  try {
    const c = mount(doc);
    createBlockEditor({ container: c, module: V2, channel: 'mp', onChange: () => {}, doc });
    c.querySelector('.be-card[data-id="tip"] .be-toggle').click();
    await Promise.resolve();

    const toolbar = c.querySelector('[role="toolbar"]');
    assert.equal(toolbar?.getAttribute('aria-label'), '富文本格式');
    const labels = [...toolbar.querySelectorAll('button')].map(button => button.getAttribute('aria-label'));
    assert.deepEqual(labels, ['加粗', '斜体', '下划线', '文字颜色', '背景颜色', '字号', '左对齐', '居中', '右对齐', '无序列表', '有序列表', '插入链接']);
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
  }
});
