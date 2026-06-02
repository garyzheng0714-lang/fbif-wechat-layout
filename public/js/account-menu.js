// account-menu.js — 统一顶栏账号菜单（Google 头像菜单式）。
//
// 把原来的两层切换器（顶部圆形「渠道」胶囊 + 侧栏方角「规则包」分段）合并成
// 一个顶栏头像菜单：列出全部账号，渠道（公众号 / 网站）降为菜单内的分组标签。
// 用户 2026-06-02 裁定「不要两层」——单层账号菜单取代两层（推翻旧红线 #2 的
// 圆角/方角分层契约）。数据源仍是 registry.js（红线：账号唯一入口）。
//
// onSwitch(id) 由各页注入，承载页面级切换语义：
//   - app.html 传 switchAccount：同模板原地切（FBIF↔WowFood）/ 跨模板整页 reload。
//   - site.html 传 navigateToAccount：网站侧无同模板同伴，一律跳转到账号 URL。
// 跨渠道快照由 session-restore 的 beforeunload 兜底保存（location.href 触发）。

import { listRulePacks } from './rule-packs/registry.js';

const CHANNEL_LABEL = { mp: '公众号', ft: '网站' };
const CHANNEL_ORDER = ['mp', 'ft'];
const FALLBACK_AVATAR = '/fbif-avatar.webp';

function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// 纯函数：把账号包按渠道分组（CHANNEL_ORDER 顺序、过滤空组），并标出当前账号。
// 抽出来便于行为测试——锁住分组顺序/成员/active 标记，避免源码 grep 的 ghost-pass
// （render 只是把它转 HTML）。
export function buildAccountGroups(packs, activeId) {
  return CHANNEL_ORDER
    .map(ch => ({
      channel: ch,
      label: CHANNEL_LABEL[ch] || ch,
      items: (packs || [])
        .filter(p => p.channel === ch)
        .map(p => ({
          id: p.id,
          name: p.accountName,
          avatarUrl: p.avatarUrl || FALLBACK_AVATAR,
          active: p.id === activeId,
        })),
    }))
    .filter(g => g.items.length);
}

// 渲染并挂载账号菜单。返回 { setActive(id) } 供页面在原地切换后更新高亮/触发器。
export function mountAccountMenu({ container, activeId, onSwitch }) {
  if (!container) return { setActive() {} };
  const packs = listRulePacks();
  let isOpen = false;

  const activePack = () => packs.find(p => p.id === activeId) || packs[0];

  function render() {
    const cur = activePack();
    const groups = buildAccountGroups(packs, activeId);
    container.innerHTML =
      `<button type="button" class="acct-trigger" aria-haspopup="menu" aria-expanded="${isOpen}">` +
        `<img class="acct-trigger-avatar" src="${esc(cur.avatarUrl || FALLBACK_AVATAR)}" alt="">` +
        `<span class="acct-trigger-name">${esc(cur.accountName)}</span>` +
        `<svg class="acct-trigger-caret" width="12" height="12" viewBox="0 0 24 24" aria-hidden="true">` +
          `<path d="M6 9l6 6 6-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>` +
      `</button>` +
      `<div class="acct-panel" role="menu"${isOpen ? '' : ' hidden'}>` +
        groups.map(g =>
          `<div class="acct-group-label">${esc(g.label)}</div>` +
          g.items.map(it =>
            `<button type="button" class="acct-item${it.active ? ' is-active' : ''}" role="menuitemradio"` +
              ` aria-checked="${it.active}" data-account-id="${esc(it.id)}">` +
              `<img class="acct-item-avatar" src="${esc(it.avatarUrl)}" alt="">` +
              `<span class="acct-item-name">${esc(it.name)}</span>` +
              (it.active
                ? `<svg class="acct-item-check" width="16" height="16" viewBox="0 0 24 24" aria-hidden="true">` +
                  `<path d="M5 12l5 5L20 7" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/></svg>`
                : '') +
            `</button>`
          ).join('')
        ).join('') +
      `</div>`;
    bind();
  }

  function bind() {
    const trigger = container.querySelector('.acct-trigger');
    if (trigger) trigger.addEventListener('click', (e) => { e.stopPropagation(); toggle(); });
    container.querySelectorAll('[data-account-id]').forEach(btn => {
      btn.addEventListener('click', () => {
        const id = btn.dataset.accountId;
        close();
        if (id !== activeId) onSwitch(id);
      });
    });
  }

  function close() { if (isOpen) { isOpen = false; render(); } }
  function toggle() { isOpen = !isOpen; render(); }

  document.addEventListener('click', (e) => { if (!container.contains(e.target)) close(); });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') close(); });

  render();
  return {
    setActive(id) { activeId = id; render(); },
  };
}
