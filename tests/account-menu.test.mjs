// 左栏导入卡账号菜单的行为契约（单层菜单是红线 #2 的唯一账号切换入口）。
// WHY：account-menu.js 此前只有源码 grep 断言（/is-active/ 等），逻辑写错也不报（ghost-pass）。
// 把分组/顺序/active 标记抽成纯函数 buildAccountGroups 后，这里锁住它的真实行为：
// 按渠道分组、CHANNEL_ORDER(mp→ft) 顺序、组内声明顺序、只标当前账号、空组过滤、头像降级。
// 交互层（开合/onSwitch 同账号守卫）依赖 DOM，仓库无 DOM harness，留作残余缺口。
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { buildAccountGroups, mountAccountMenu } from '../public/js/account-menu.js';

const packs = [
  { id: 'fbif', accountName: 'FBIF公众号', channel: 'mp', avatarUrl: '/a.webp' },
  { id: 'wowfood', accountName: 'WowFood爆品超市', channel: 'mp' }, // 无 avatarUrl → 降级
  { id: 'ft-default', accountName: '网站默认', channel: 'ft', avatarUrl: '/b.webp' },
];

test('buildAccountGroups 按 CHANNEL_ORDER(mp→ft) 分组、渠道做标签、组内保留声明顺序', () => {
  const groups = buildAccountGroups(packs, 'fbif');
  assert.deepEqual(groups.map(g => g.channel), ['mp', 'ft'], 'mp 组在 ft 组前');
  assert.deepEqual(groups.map(g => g.label), ['公众号', '网站']);
  assert.deepEqual(groups[0].items.map(i => i.id), ['fbif', 'wowfood'], '组内按声明顺序');
  assert.deepEqual(groups[1].items.map(i => i.id), ['ft-default']);
});

test('buildAccountGroups 只把当前账号标 active（且仅一个）', () => {
  const flat = buildAccountGroups(packs, 'wowfood').flatMap(g => g.items);
  assert.deepEqual(flat.filter(i => i.active).map(i => i.id), ['wowfood'], '只有当前账号 active');
  assert.equal(flat.find(i => i.id === 'fbif').active, false, '非当前账号 active=false');
});

test('buildAccountGroups activeId 不匹配任何账号时全部 active=false（不误标）', () => {
  const flat = buildAccountGroups(packs, 'nonexistent').flatMap(g => g.items);
  assert.ok(flat.every(i => !i.active), '无匹配则无人 active');
});

test('buildAccountGroups 过滤空渠道组', () => {
  const onlyMp = buildAccountGroups(packs.filter(p => p.channel === 'mp'), 'fbif');
  assert.deepEqual(onlyMp.map(g => g.channel), ['mp'], '无 ft 账号则不出 ft 组');
});

test('buildAccountGroups 头像缺失降级到 FALLBACK_AVATAR', () => {
  const wow = buildAccountGroups(packs, 'fbif')[0].items.find(i => i.id === 'wowfood');
  assert.equal(wow.avatarUrl, '/fbif-avatar.webp', '无 avatarUrl 用 fallback');
});

test('账号菜单打开后焦点进入当前项，方向键可移动，Escape 关闭并归还触发器', () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="acctMenu"></div></body></html>');
  let focused = document.body;
  Object.defineProperty(document, 'activeElement', { configurable: true, get: () => focused });
  window.HTMLElement.prototype.focus = function focus() { focused = this; };
  globalThis.document = document;
  globalThis.window = window;
  try {
    const container = document.getElementById('acctMenu');
    mountAccountMenu({ container, activeId: 'fbif', onSwitch() {} });

    container.querySelector('.acct-trigger').click();
    assert.equal(container.querySelector('.acct-trigger').getAttribute('aria-expanded'), 'true');
    assert.equal(document.activeElement?.getAttribute('role'), 'menuitemradio', '打开后焦点进入菜单项');
    assert.equal(document.activeElement?.getAttribute('aria-checked'), 'true', '优先聚焦当前账号');

    const firstFocused = document.activeElement;
    const down = new window.Event('keydown', { bubbles: true, cancelable: true });
    down.key = 'ArrowDown';
    firstFocused.dispatchEvent(down);
    assert.notEqual(document.activeElement, firstFocused, '方向键移动到下一账号');

    const escape = new window.Event('keydown', { bubbles: true, cancelable: true });
    escape.key = 'Escape';
    document.activeElement.dispatchEvent(escape);
    assert.equal(container.querySelector('.acct-trigger').getAttribute('aria-expanded'), 'false');
    assert.ok(document.activeElement?.classList.contains('acct-trigger'), '关闭后焦点回到触发器');
  } finally {
    delete globalThis.document;
    delete globalThis.window;
  }
});
