// block-merge.test.mjs — 底部区块「两层合并」纯函数的意图测试。
// WHY：保证「后台永久默认 vs 前台单篇逐字段覆盖」语义——未覆盖项跟随后台默认、
// 已覆盖项保留并被标记、可恢复默认。这是整套底部模块化交互的契约底座。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resolveBlocks, flattenFields, hasAnyOverride } from '../public/js/blocks/block-merge.js';

function baseModule() {
  return {
    enabled: true,
    order: ['intro', 'card', 'more'],
    blocks: {
      intro: { type: 'rich_html', enabled: true, fields: { html_lines: ['<p>hi</p>'] } },
      card: { type: 'name_card', enabled: false, fields: { intro_text: '找小明' } },
      more: { type: 'more_articles', enabled: true, fields: {} },
    },
  };
}

test('无覆盖时直通后台默认：顺序、开关、字段默认值均跟随', () => {
  const r = resolveBlocks(baseModule(), null);
  assert.equal(r.enabled, true);
  assert.equal(r.enabledOverridden, false);
  assert.equal(r.orderOverridden, false);
  assert.deepEqual(r.blocks.map(b => b.id), ['intro', 'card', 'more']);

  const card = r.blocks.find(b => b.id === 'card');
  assert.equal(card.enabled, false);          // 后台默认关
  // base.fields 覆盖 schema 默认；未在 base 设置的字段回落 schema 默认。
  assert.equal(card.fields.intro_text.value, '找小明');
  assert.equal(card.fields.intro_text.overridden, false);
  assert.equal(card.fields.intro_size.value, 14);       // schema 默认
  assert.equal(card.fields.intro_color.value, '#888888');
  assert.equal(card.fields.image.value, '');
});

test('字段覆盖：被改字段标 overridden 并取覆盖值，其余字段仍跟随默认', () => {
  const override = { overrides: { card: { fields: { intro_text: '找小红' } } } };
  const r = resolveBlocks(baseModule(), override);
  const card = r.blocks.find(b => b.id === 'card');
  assert.equal(card.fields.intro_text.value, '找小红');
  assert.equal(card.fields.intro_text.overridden, true);
  assert.equal(card.fields.intro_text.default, '找小明');   // 仍记得后台默认，供恢复
  assert.equal(card.fields.intro_size.value, 14);
  assert.equal(card.fields.intro_size.overridden, false);
});

test('开关覆盖：单篇可临时开启后台默认关闭的区块', () => {
  const r = resolveBlocks(baseModule(), { overrides: { card: { enabled: true } } });
  const card = r.blocks.find(b => b.id === 'card');
  assert.equal(card.enabled, true);
  assert.equal(card.enabledDefault, false);
  assert.equal(card.enabledOverridden, true);
});

test('顺序覆盖：重排生效并标记 orderOverridden', () => {
  const r = resolveBlocks(baseModule(), { order: ['more', 'intro', 'card'] });
  assert.deepEqual(r.blocks.map(b => b.id), ['more', 'intro', 'card']);
  assert.equal(r.orderOverridden, true);
});

test('顺序覆盖缺新块：后台后加的块自动补到末尾，不丢失', () => {
  // 用户存覆盖时只有 intro/card；后台后来新增 more —— more 应自动出现在末尾。
  const r = resolveBlocks(baseModule(), { order: ['card', 'intro'] });
  assert.deepEqual(r.blocks.map(b => b.id), ['card', 'intro', 'more']);
});

test('flattenFields 取最终值映射，供渠道渲染器消费', () => {
  const r = resolveBlocks(baseModule(), { overrides: { card: { fields: { intro_size: 20 } } } });
  const card = r.blocks.find(b => b.id === 'card');
  assert.deepEqual(flattenFields(card), {
    intro_text: '找小明',
    intro_size: 20,
    intro_color: '#888888',
    image: '',
  });
});

test('恢复默认：清空覆盖即回到后台默认（card 文字回 找小明）', () => {
  const card = resolveBlocks(baseModule(), {}).blocks.find(b => b.id === 'card');
  assert.equal(card.fields.intro_text.value, '找小明');
  assert.equal(card.fields.intro_text.overridden, false);
});

test('审计修复：order 重复 id 去重，区块不被渲染两次', () => {
  const r = resolveBlocks(baseModule(), { order: ['card', 'card', 'intro'] });
  assert.deepEqual(r.blocks.map(b => b.id), ['card', 'intro', 'more']);
  // 解析顺序里每个 id 唯一
  const ids = r.blocks.map(b => b.id);
  assert.equal(new Set(ids).size, ids.length);
});

test('审计修复：override 独有字段不被丢弃（出现在结果与 flattenFields）', () => {
  const r = resolveBlocks(baseModule(), { overrides: { card: { fields: { extra_note: '临时备注' } } } });
  const card = r.blocks.find(b => b.id === 'card');
  assert.equal(card.fields.extra_note.value, '临时备注');
  assert.equal(card.fields.extra_note.overridden, true);
  assert.equal(flattenFields(card).extra_note, '临时备注');
});

test('hasAnyOverride 正确判定是否存在单篇改动', () => {
  assert.equal(hasAnyOverride(null), false);
  assert.equal(hasAnyOverride({}), false);
  assert.equal(hasAnyOverride({ overrides: { card: {} } }), false);
  assert.equal(hasAnyOverride({ enabled: false }), true);
  assert.equal(hasAnyOverride({ order: ['card'] }), true);
  assert.equal(hasAnyOverride({ overrides: { card: { enabled: true } } }), true);
  assert.equal(hasAnyOverride({ overrides: { card: { fields: { intro_text: 'x' } } } }), true);
});
