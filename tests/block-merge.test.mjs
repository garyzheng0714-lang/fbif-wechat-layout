// block-merge.test.mjs — 底部区块「两层合并」纯函数的意图测试。
// WHY：保证「后台永久默认 vs 前台单篇逐字段覆盖」语义——未覆盖项跟随后台默认、
// 已覆盖项保留并被标记、可恢复默认。这是整套底部模块化交互的契约底座。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resolveBlocks, flattenFields, hasAnyOverride, stripReadonlyOverride } from '../public/js/blocks/block-merge.js';

function baseModule() {
  return {
    schema_version: 2,
    enabled: true,
    active_preset_id: 'default',
    presets: [{
      id: 'default', name: '默认',
      order: ['intro', 'card', 'more'],
      blocks: {
        intro: { type: 'rich_html', enabled: true, fields: { html_lines: ['<p>hi</p>'] } },
        card: { type: 'text', display_name: '商务名片', enabled: false, fields: { content: '找小明' } },
        more: { type: 'more_articles', enabled: true, fields: {} },
      },
    }],
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
  assert.equal(card.displayName, '商务名片'); // 实例显示名只服务编辑器标题，不进入 fields
  // base.fields 覆盖 schema 默认；未在 base 设置的字段回落 schema 默认。
  assert.equal(card.fields.content.value, '找小明');
  assert.equal(card.fields.content.overridden, false);
  assert.equal(card.fields.font_size.value, 15);       // schema 默认
  assert.equal(card.fields.text_color.value, '#544545');
  assert.equal(card.fields.align.value, 'left');
  assert.equal(flattenFields(card).display_name, undefined);
});

test('字段覆盖：被改字段标 overridden 并取覆盖值，其余字段仍跟随默认', () => {
  const override = { overrides: { card: { fields: { content: '找小红' } } } };
  const r = resolveBlocks(baseModule(), override);
  const card = r.blocks.find(b => b.id === 'card');
  assert.equal(card.fields.content.value, '找小红');
  assert.equal(card.fields.content.overridden, true);
  assert.equal(card.fields.content.default, '找小明');   // 仍记得后台默认，供恢复
  assert.equal(card.fields.font_size.value, 15);
  assert.equal(card.fields.font_size.overridden, false);
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
  const r = resolveBlocks(baseModule(), { overrides: { card: { fields: { font_size: 20 } } } });
  const card = r.blocks.find(b => b.id === 'card');
  assert.deepEqual(flattenFields(card), {
    content: '找小明',
    html: '',
    font_size: 20,
    text_color: '#544545',
    background_color: '',
    italic: false,
    underline: false,
    bold: false,
    align: 'left',
  });
});

test('恢复默认：清空覆盖即回到后台默认（card 文字回 找小明）', () => {
  const card = resolveBlocks(baseModule(), {}).blocks.find(b => b.id === 'card');
  assert.equal(card.fields.content.value, '找小明');
  assert.equal(card.fields.content.overridden, false);
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

test('frontendEditable 透传：块级 frontend_editable 反映到解析结果（缺省 false=前端只读）', () => {
  // WHY：前端简化后据此判定哪块出换图入口；缺省只读，避免脏数据误开放结构编辑。
  const mod = {
    schema_version: 2, active_preset_id: 'default',
    presets: [{ id: 'default', order: ['a', 'b'], blocks: {
      a: { type: 'image', enabled: true, frontend_editable: true, fields: { image: '' } },
      b: { type: 'image', enabled: true, fields: { image: '' } },
    } }],
  };
  const r = resolveBlocks(mod, {});
  assert.equal(r.blocks.find(x => x.id === 'a').frontendEditable, true);
  assert.equal(r.blocks.find(x => x.id === 'b').frontendEditable, false); // 未标记=只读
});

test('stripReadonlyOverride：剥离结构性(enabled/order)与只读块的存量覆盖，仅留 editable 块 fields，幂等', () => {
  // WHY：前端简化后结构/排序/显隐归后台；老文章存量覆盖须确定性剥离，否则渲出与后台默认不一致且无 UI 可恢复。
  const stale = {
    enabled: false,             // 整段显隐 → 删（移后台）
    order: ['x', 'y'],          // 排序 → 删（移后台）
    overrides: {
      a: { enabled: true, fields: { image: '{{asset#x}}' } }, // editable：删块级 enabled、留 fields
      b: { fields: { image: '{{asset#y}}' } },                // 只读块 → 整条删
      c: { enabled: false },                                  // editable 但无 fields → 删
    },
  };
  const out = stripReadonlyOverride(stale, new Set(['a', 'c']));
  assert.deepEqual(out, { overrides: { a: { fields: { image: '{{asset#x}}' } } } });
  assert.deepEqual(stripReadonlyOverride(out, new Set(['a', 'c'])), out); // 幂等
  assert.deepEqual(stripReadonlyOverride(stale, new Set()), {});          // 全只读 → 空（不落盘）
});

// ── schema_version:2 双预设兼容契约（codex 评审 P1 第一步：锁住 v1/v2 并存） ──

function v2Module() {
  return {
    schema_version: 2,
    enabled: true,
    active_preset_id: 'original',
    presets: [
      {
        id: 'original', name: '原创',
        order: ['note', 'sp', 'card'],
        blocks: {
          note: { type: 'text', enabled: true, fields: { content: '原创声明' } },
          sp: { type: 'spacer', enabled: true, fields: { height: 16 } },
          card: { type: 'image', display_name: '名片', enabled: true, fields: { image: '{{asset#x}}' } },
        },
      },
      {
        id: 'repost', name: '转载',
        order: ['note', 'card'],
        blocks: {
          note: { type: 'text', enabled: true, fields: { content: '转载声明' } },
          card: { type: 'image', display_name: '名片', enabled: true, fields: { image: '{{asset#x}}' } },
        },
      },
    ],
    // 顶层 order/blocks 在 v2 应被忽略（故意放干扰数据，验证不泄漏）
    order: ['ignored'],
    blocks: { ignored: { type: 'text', enabled: true, fields: {} } },
  };
}

test('v2：按 presetId 选 active 预设的 order/blocks（original）', () => {
  const r = resolveBlocks(v2Module(), null, { presetId: 'original' });
  assert.deepEqual(r.blocks.map(b => b.id), ['note', 'sp', 'card']);
  assert.equal(r.blocks.find(b => b.id === 'note').fields.content.value, '原创声明');
});

test('v2：presetId=repost 读转载预设（不同 order/内容）', () => {
  const r = resolveBlocks(v2Module(), null, { presetId: 'repost' });
  assert.deepEqual(r.blocks.map(b => b.id), ['note', 'card']);
  assert.equal(r.blocks.find(b => b.id === 'note').fields.content.value, '转载声明');
});

test('v2：缺省 presetId 用 active_preset_id', () => {
  const r = resolveBlocks(v2Module(), null);
  assert.deepEqual(r.blocks.map(b => b.id), ['note', 'sp', 'card']); // active=original
});

test('v2：未知 presetId 回落第一个预设，不抛错', () => {
  const r = resolveBlocks(v2Module(), null, { presetId: 'nope' });
  assert.deepEqual(r.blocks.map(b => b.id), ['note', 'sp', 'card']);
});

test('v2：顶层 order/blocks 被忽略（只认 presets[]）', () => {
  const r = resolveBlocks(v2Module(), null, { presetId: 'original' });
  assert.ok(!r.blocks.find(b => b.id === 'ignored'));
});

test('v2：单篇 fields 覆盖仍作用于 active 预设的块', () => {
  const r = resolveBlocks(v2Module(), { overrides: { note: { fields: { content: '改了' } } } }, { presetId: 'original' });
  assert.equal(r.blocks.find(b => b.id === 'note').fields.content.value, '改了');
  assert.equal(r.blocks.find(b => b.id === 'note').fields.content.overridden, true);
});

test('v2：schema_version 为字符串 "2" 也识别（容错，前后端一致）', () => {
  const mod = { ...v2Module(), schema_version: '2' };
  const r = resolveBlocks(mod, null, { presetId: 'repost' });
  assert.deepEqual(r.blocks.map(b => b.id), ['note', 'card']);
});

test('v2：缺 active_preset_id 且无 opts 时安全回落首个预设（渲染鲁棒不崩）', () => {
  const mod = { ...v2Module() };
  delete mod.active_preset_id;
  const r = resolveBlocks(mod, null);
  assert.deepEqual(r.blocks.map(b => b.id), ['note', 'sp', 'card']); // 回落首个=original
});

test('hasAnyOverride 正确判定是否存在单篇改动', () => {
  assert.equal(hasAnyOverride(null), false);
  assert.equal(hasAnyOverride({}), false);
  assert.equal(hasAnyOverride({ overrides: { card: {} } }), false);
  assert.equal(hasAnyOverride({ enabled: false }), true);
  assert.equal(hasAnyOverride({ order: ['card'] }), true);
  assert.equal(hasAnyOverride({ overrides: { card: { enabled: true } } }), true);
  assert.equal(hasAnyOverride({ overrides: { card: { fields: { content: 'x' } } } }), true);
});
