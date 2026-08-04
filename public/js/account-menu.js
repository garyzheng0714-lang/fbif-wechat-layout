// account-menu.js — 统一发布账号菜单（shadcn/Radix team-switcher 交互语法）。
//
// 把原来的两层切换器（顶部圆形「渠道」胶囊 + 侧栏方角「规则包」分段）合并成
// 一个头像菜单：列出全部账号，渠道（公众号 / 网站）降为菜单内的分组标签。
// 用户 2026-08-01 将它从全局身份位移动到导入任务框内，用来回答“这篇内容排版
// 到哪里”；登录用户身份成为顶栏左侧唯一 owner。菜单仍保持单层，数据源仍是
// registry.js（红线：账号唯一入口）。
//
// onSwitch(id) 由各页注入，承载页面级切换语义：
//   - app.html 传 switchAccount：同模板原地切（FBIF↔WowFood）/ 跨模板整页 reload。
//   - site.html 传 navigateToAccount：网站侧无同模板同伴，一律跳转到账号 URL。
// 跨渠道快照由 session-restore 的 beforeunload 兜底保存（location.href 触发）。

import { listRulePacks } from './rule-packs/registry.js';

const CHANNEL_LABEL = { mp: '公众号', ft: '网站' };
const CHANNEL_ORDER = ['mp', 'ft'];
const FALLBACK_AVATAR = '/fbif-avatar.webp';

// 微信公众号 logo（绿色对话气泡）的 path。公众号渠道（mp）的账号头像统一用它，替代各账号
// 的 FB!F 占位图；其余渠道（网站）仍用各自 avatarUrl。账号名已去掉「公众号」字样。
const WECHAT_MP_PATH =
  'M512 0a512 512 0 0 1 512 512l-0.256 16A512 512 0 0 1 0 512l0.256-16A512 512 0 0 1 512 0z m132.224 429.184c-111.168 0-198.656 75.136-198.656 167.232 0 92.48 87.488 167.36 198.656 167.36 23.296 0 46.848-5.888 70.144-11.52l64.192 34.752-17.6-57.728c46.72-34.752 81.664-80.96 81.664-132.864 0-92.16-93.376-167.232-198.4-167.232zM416.448 256c-128.384 0-233.6 86.336-233.6 196.16 0 63.36 34.88 115.456 93.376 155.84l-23.36 69.44 81.728-40.64c29.184 5.632 52.736 11.52 81.728 11.52l10.88-0.256 10.88-0.768a164.736 164.736 0 0 1-7.168-48.192c0-100.736 87.552-182.464 198.656-182.464 7.488 0 14.912 0.64 22.464 1.28C631.936 324.928 531.264 256 416.512 256z m163.456 265.664c17.6 0 29.184 11.52 29.184 22.976 0 11.52-11.712 22.976-29.184 22.976-11.712 0-23.296-11.52-23.296-23.04 0-11.456 11.584-22.912 23.296-22.912z m128.576 0c17.472 0 29.184 11.52 29.184 22.976 0 11.52-11.968 22.976-29.184 22.976h-0.064c-11.648 0-23.232-11.52-23.232-23.04 0-11.456 11.584-22.912 23.296-22.912zM340.288 354.176c17.536 0 29.184 11.456 29.184 28.8 0 17.088-11.52 28.8-29.184 28.8-17.6 0-35.2-11.52-35.2-28.8 0-17.344 17.6-28.8 35.2-28.8z m163.52-0.128c17.536 0 29.184 11.52 29.184 28.8s-11.648 28.8-29.184 28.8c-17.664 0-35.2-11.52-35.2-28.8 0.32-17.28 17.856-28.8 35.2-28.8z';

// 头像渲染：公众号渠道（mp）统一用微信 logo（同款 path），传入头像类名做尺寸/圆角；
// 其余渠道用各自 avatarUrl 图片。trigger 与 item 共用，保证当前账号头像也随渠道变。
function avatarHtml(channel, avatarUrl, cls) {
  if (channel === 'mp') {
    return `<svg class="${esc(cls)}" viewBox="0 0 1024 1024" aria-hidden="true"><path fill="#08A128" d="${WECHAT_MP_PATH}"/></svg>`;
  }
  return `<img class="${esc(cls)}" src="${esc(avatarUrl || FALLBACK_AVATAR)}" alt="">`;
}

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
  const panelID = (container.id || 'acct-menu') + '-panel';

  const activePack = () => packs.find(p => p.id === activeId) || packs[0];

  function menuItems() {
    return [...container.querySelectorAll('[role="menuitemradio"], [role="menuitem"]')];
  }

  function focusAfterRender(target) {
    if (!target) return;
    if (target === 'trigger') {
      container.querySelector('.acct-trigger')?.focus();
      return;
    }
    const items = menuItems();
    if (!items.length) return;
    const item = target === 'last'
      ? items[items.length - 1]
      : target === 'active'
      ? items.find(el => el.getAttribute('aria-checked') === 'true') || items[0]
      : items[0];
    item?.focus();
  }

  function render(focusTarget = '') {
    const cur = activePack();
    const groups = buildAccountGroups(packs, activeId);
    container.innerHTML =
      `<button type="button" class="acct-trigger" aria-label="排版到 ${esc(cur.accountName)}" aria-haspopup="menu" aria-controls="${esc(panelID)}" aria-expanded="${isOpen}">` +
        avatarHtml(cur.channel, cur.avatarUrl, 'acct-trigger-avatar') +
        `<span class="acct-trigger-name">${esc(cur.accountName)}</span>` +
        `<svg class="acct-trigger-caret" width="12" height="12" viewBox="0 0 24 24" aria-hidden="true">` +
          `<path d="M6 9l6 6 6-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>` +
      `</button>` +
      `<div class="acct-panel" id="${esc(panelID)}" role="menu" aria-label="账号"${isOpen ? '' : ' hidden'}>` +
        groups.map(g =>
          `<div class="acct-group-label">${esc(g.label)}</div>` +
          g.items.map(it =>
            `<button type="button" class="acct-item${it.active ? ' is-active' : ''}" role="menuitemradio" tabindex="-1"` +
              ` aria-checked="${it.active}" data-account-id="${esc(it.id)}">` +
              avatarHtml(g.channel, it.avatarUrl, 'acct-item-avatar') +
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
    focusAfterRender(focusTarget);
  }

  function bind() {
    const trigger = container.querySelector('.acct-trigger');
    if (trigger) {
      trigger.addEventListener('click', (e) => { e.stopPropagation(); toggle(); });
      trigger.addEventListener('keydown', (e) => {
        if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
        e.preventDefault();
        isOpen = true;
        render(e.key === 'ArrowUp' ? 'last' : 'first');
      });
    }
    container.querySelectorAll('[data-account-id]').forEach(btn => {
      btn.addEventListener('click', () => {
        const id = btn.dataset.accountId;
        close(id === activeId);
        if (id !== activeId) onSwitch(id);
      });
    });
    menuItems().forEach(item => item.addEventListener('keydown', onMenuKey));
  }

  function onMenuKey(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close(true);
      return;
    }
    const items = menuItems();
    const current = items.indexOf(e.currentTarget);
    if (current < 0) return;
    let next = current;
    if (e.key === 'ArrowDown') next = (current + 1) % items.length;
    else if (e.key === 'ArrowUp') next = (current - 1 + items.length) % items.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = items.length - 1;
    else return;
    e.preventDefault();
    items[next]?.focus();
  }

  function close(restoreFocus = false) {
    if (!isOpen) return;
    isOpen = false;
    render(restoreFocus ? 'trigger' : '');
  }
  function toggle() {
    isOpen = !isOpen;
    render(isOpen ? 'active' : 'trigger');
  }

  document.addEventListener('click', (e) => { if (!container.contains(e.target)) close(); });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') close(true); });
  document.addEventListener('focusin', (e) => { if (isOpen && !container.contains(e.target)) close(); });

  render();
  return {
    setActive(id) { activeId = id; render(); },
  };
}
