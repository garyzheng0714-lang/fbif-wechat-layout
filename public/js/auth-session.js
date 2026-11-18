// 登录走 FBIF 统一登录（后端 api/auth_login.go）：本项目没有登录页、不存令牌，会话只在 HttpOnly cookie 里。
// 没登录时去 /auth/login?next=（飞书客户端里直接走飞书登录），后端再送去登录中心；登录完回到 next。
// 飞书登录了还没授权云文档的，接着自动去授权（autoGrantFeishu）。

import { openAgentConnect } from './agent-connect.js';

export async function authFetch(input, init = {}, {
  fetchImpl = globalThis.fetch,
} = {}) {
  return fetchImpl(input, {
    ...init,
    credentials: 'same-origin',
  });
}

export async function loadCurrentUser({
  fetchImpl = globalThis.fetch,
} = {}) {
  const response = await authFetch('/api/auth/me', { method: 'GET', cache: 'no-store' }, { fetchImpl });
  if (response.status === 401) return null;
  if (!response.ok) {
    throw new Error('账号服务暂不可用');
  }
  const payload = await response.json();
  return payload.user || null;
}

function currentReturnTo(windowRef) {
  const location = windowRef?.location;
  if (!location) return '/mp/fbif';
  const params = new URLSearchParams(location.search || '');
  params.delete('v');
  params.delete('feishu_grant');
  const search = params.toString();
  return `${location.pathname || '/mp/fbif'}${search ? `?${search}` : ''}${location.hash || ''}`;
}

function inFeishu(windowRef) {
  return /Lark|Feishu/i.test(String(windowRef?.navigator?.userAgent || ''));
}

// loginURL 是没登录时去的地方：浏览器里去登录中心（账号密码、飞书都在那页），飞书客户端里直接走飞书登录、不用点。
export function loginURL(windowRef = globalThis.window) {
  const start = inFeishu(windowRef) ? '/auth/feishu/fbif/login' : '/auth/login';
  return `${start}?next=${encodeURIComponent(currentReturnTo(windowRef))}`;
}

// feishuGrantURL 是授权飞书的地址：授权一次，之后服务端替他续，回到当前页面。
export function feishuGrantURL(windowRef = globalThis.window) {
  return `/auth/feishu-grant?next=${encodeURIComponent(currentReturnTo(windowRef))}`;
}

const FEISHU_GRANT_RESULTS = {
  denied: '飞书授权没完成：你没有同意授权',
  failed: '飞书授权没成功，请稍后再试',
  mismatch: '授权的飞书账号和登录的不是同一个人，请用自己的飞书账号授权',
  unavailable: '这个账号授权不了飞书',
};

// takeFeishuGrantResult 读授权回来时地址上的 feishu_grant=（没成功的那几种），读到就从地址上去掉，返回要提示的话。
export function takeFeishuGrantResult(windowRef = globalThis.window) {
  const location = windowRef?.location;
  if (!location) return '';
  const params = new URLSearchParams(location.search || '');
  const result = params.get('feishu_grant');
  if (!result) return '';
  params.delete('feishu_grant');
  const search = params.toString();
  windowRef.history?.replaceState?.(windowRef.history.state, '', `${location.pathname}${search ? `?${search}` : ''}${location.hash || ''}`);
  return FEISHU_GRANT_RESULTS[result] || '';
}

function navigate(windowRef, target) {
  if (windowRef?.location?.assign) {
    windowRef.location.assign(target);
  } else if (windowRef?.location) {
    windowRef.location.href = target;
  }
}

const AUTO_GRANT_KEY = 'fbif_feishu_grant_auto_at';
const AUTO_GRANT_QUIET_MS = 10 * 60 * 1000;

// autoGrantFeishu：飞书登录的人默认授权云文档（2026-11-11 用户：「飞书云文档授权应该是默认授权的」）——还没授权就直接去授权页，
// 飞书客户端里不用点，浏览器里飞书问一次同不同意，之后服务端替他续。刚从授权页回来没授权成（地址上带 feishu_grant=）不再跳、
// 菜单里写原因；同一个标签页 10 分钟内只自动跳一次，记不下来就不跳，免得来回跳。
function autoGrantFeishu(windowRef, user) {
  if (user.auth_mode !== 'feishu' || user.feishu !== 'required' || !user.capabilities?.feishu_documents) return false;
  if (new URLSearchParams(windowRef?.location?.search || '').has('feishu_grant')) return false;
  try {
    const store = windowRef.sessionStorage;
    const now = Date.now();
    if (now - Number(store.getItem(AUTO_GRANT_KEY) || 0) < AUTO_GRANT_QUIET_MS) return false;
    store.setItem(AUTO_GRANT_KEY, String(now));
  } catch {
    return false;
  }
  navigate(windowRef, feishuGrantURL(windowRef));
  return true;
}

// 已打开的标签页切回来时查一次登录：别处退出后，这里不用等用户点什么才发现。
// 只在 visibilitychange（变可见）和 pageshow（整页从缓存还原）时各问一次，不轮询；
// 只有 401 才跳登录，断网、超时、5xx 都当没发生；上一次还没回来就不再发。返回卸载函数。
export function watchLoginOnReturn({
  windowRef = globalThis.window,
  documentRef = globalThis.document,
  fetchImpl = globalThis.fetch,
} = {}) {
  if (!documentRef?.addEventListener || !windowRef?.addEventListener) return () => {};
  let inflight = false;
  const check = async () => {
    if (inflight) return;
    inflight = true;
    try {
      const response = await authFetch('/api/auth/me', { method: 'GET', cache: 'no-store' }, { fetchImpl });
      if (response.status === 401) navigate(windowRef, loginURL(windowRef));
    } catch {
      // 断网、超时：不能当没登录
    } finally {
      inflight = false;
    }
  };
  const onVisible = () => {
    if (documentRef.visibilityState === 'visible') check();
  };
  const onPageShow = (event) => {
    if (event?.persisted) check();
  };
  documentRef.addEventListener('visibilitychange', onVisible);
  windowRef.addEventListener('pageshow', onPageShow);
  return () => {
    documentRef.removeEventListener('visibilitychange', onVisible);
    windowRef.removeEventListener('pageshow', onPageShow);
  };
}

let stopWatching = null;

export async function ensureAuthenticatedSession({
  windowRef = globalThis.window,
  documentRef = globalThis.document,
  fetchImpl = globalThis.fetch,
} = {}) {
  try {
    const user = await loadCurrentUser({ fetchImpl });
    if (!user) {
      navigate(windowRef, loginURL(windowRef));
      return null;
    }
    if (autoGrantFeishu(windowRef, user)) return null;
    stopWatching ??= watchLoginOnReturn({ windowRef, documentRef, fetchImpl });
    documentRef?.body?.classList.remove('auth-pending');
    return user;
  } catch (error) {
    documentRef?.body?.classList.remove('auth-pending');
    documentRef?.body?.classList.add('auth-unavailable');
    throw error;
  }
}

export async function logoutSession({
  fetchImpl = globalThis.fetch,
} = {}) {
  await authFetch('/auth/feishu/logout', { method: 'POST' }, { fetchImpl });
}

function makeAvatar(documentRef, user, large = false) {
  const holder = documentRef.createElement('span');
  holder.className = `user-identity-avatar${large ? ' is-large' : ''}${user.auth_mode === 'password' ? ' is-password' : ''}`;
  const fallback = documentRef.createElement('span');
  fallback.dataset.userAvatarFallback = '';
  fallback.textContent = Array.from(String(user.name || '用户').trim())[0] || '用';
  holder.append(fallback);
  if (user.avatar_url) {
    const image = documentRef.createElement('img');
    image.src = user.avatar_url;
    image.alt = '';
    image.referrerPolicy = 'no-referrer';
    image.addEventListener('load', () => { fallback.hidden = true; });
    image.addEventListener('error', () => { image.remove(); fallback.hidden = false; });
    holder.append(image);
  }
  return holder;
}

function appendTextLine(documentRef, parent, className, text) {
  const line = documentRef.createElement('div');
  line.className = className;
  line.textContent = text;
  parent.append(line);
  return line;
}

function ruleAdminRoleLabel(user) {
  const role = String(user?.role || '').trim();
  if (role === 'super_admin' || user?.capabilities?.rule_super_admin === true) return '超级管理员';
  if (role === 'admin' || user?.capabilities?.rule_admin === true) return '管理员';
  return '';
}

// 身份菜单固定在左栏底部并向上展开，所以静止态的箭头朝上；展开时 CSS 旋转 180° 指回收起方向。
function makeCaretIcon(documentRef) {
  const namespace = 'http://www.w3.org/2000/svg';
  const icon = documentRef.createElementNS(namespace, 'svg');
  icon.setAttribute('viewBox', '0 0 20 20');
  icon.setAttribute('width', '14');
  icon.setAttribute('height', '14');
  icon.setAttribute('fill', 'none');
  const path = documentRef.createElementNS(namespace, 'path');
  path.setAttribute('d', 'm6 12 4-4 4 4');
  path.setAttribute('stroke', 'currentColor');
  path.setAttribute('stroke-width', '1.7');
  path.setAttribute('stroke-linecap', 'round');
  path.setAttribute('stroke-linejoin', 'round');
  icon.append(path);
  return icon;
}

function makeRuleAdminIcon(documentRef) {
  const namespace = 'http://www.w3.org/2000/svg';
  const icon = documentRef.createElementNS(namespace, 'svg');
  icon.setAttribute('viewBox', '0 0 24 24');
  icon.setAttribute('width', '18');
  icon.setAttribute('height', '18');
  icon.setAttribute('fill', 'none');
  icon.setAttribute('aria-hidden', 'true');
  const paths = [
    ['M4 21v-7', 'M4 10V3', 'M12 21v-9', 'M12 8V3', 'M20 21v-5', 'M20 12V3'],
    ['M1 14h6', 'M9 8h6', 'M17 16h6'],
  ];
  paths.flat().forEach(d => {
    const path = documentRef.createElementNS(namespace, 'path');
    path.setAttribute('d', d);
    path.setAttribute('stroke', 'currentColor');
    path.setAttribute('stroke-width', '1.7');
    path.setAttribute('stroke-linecap', 'round');
    path.setAttribute('stroke-linejoin', 'round');
    icon.append(path);
  });
  return icon;
}

// Lucide「bot」。
function makeAgentIcon(documentRef) {
  const namespace = 'http://www.w3.org/2000/svg';
  const icon = documentRef.createElementNS(namespace, 'svg');
  icon.setAttribute('viewBox', '0 0 24 24');
  icon.setAttribute('width', '18');
  icon.setAttribute('height', '18');
  icon.setAttribute('fill', 'none');
  icon.setAttribute('aria-hidden', 'true');
  const shapes = [
    ['path', { d: 'M12 8V4H8' }],
    ['rect', { width: '16', height: '12', x: '4', y: '8', rx: '2' }],
    ['path', { d: 'M2 14h2' }],
    ['path', { d: 'M20 14h2' }],
    ['path', { d: 'M15 13v2' }],
    ['path', { d: 'M9 13v2' }],
  ];
  shapes.forEach(([tag, attrs]) => {
    const shape = documentRef.createElementNS(namespace, tag);
    Object.entries(attrs).forEach(([key, value]) => shape.setAttribute(key, value));
    shape.setAttribute('stroke', 'currentColor');
    shape.setAttribute('stroke-width', '1.7');
    shape.setAttribute('stroke-linecap', 'round');
    shape.setAttribute('stroke-linejoin', 'round');
    icon.append(shape);
  });
  return icon;
}

function makeActionChevronIcon(documentRef) {
  const namespace = 'http://www.w3.org/2000/svg';
  const icon = documentRef.createElementNS(namespace, 'svg');
  icon.setAttribute('viewBox', '0 0 16 16');
  icon.setAttribute('width', '14');
  icon.setAttribute('height', '14');
  icon.setAttribute('fill', 'none');
  icon.setAttribute('aria-hidden', 'true');
  const path = documentRef.createElementNS(namespace, 'path');
  path.setAttribute('d', 'm6 3 5 5-5 5');
  path.setAttribute('stroke', 'currentColor');
  path.setAttribute('stroke-width', '1.6');
  path.setAttribute('stroke-linecap', 'round');
  path.setAttribute('stroke-linejoin', 'round');
  icon.append(path);
  return icon;
}

// feishuState 把「我是谁」里的飞书授权状态（user.feishu）换成菜单上的说法；grant=要给「授权飞书」。
function feishuState(user) {
  if (!user.capabilities?.feishu_documents || user.feishu === 'none') {
    return { ok: false, grant: false, short: '云文档不可用', long: '飞书云文档不可用' };
  }
  if (user.feishu === 'ok') {
    return { ok: true, grant: false, short: '云文档已连接', long: '飞书云文档已连接' };
  }
  return { ok: false, grant: true, short: '云文档未授权', long: '飞书云文档未授权' };
}

export function mountUserIdentity(container, user, {
  documentRef = globalThis.document,
  windowRef = globalThis.window,
  fetchImpl = globalThis.fetch,
} = {}) {
  if (!container || !user) return { destroy() {} };
  container.replaceChildren();
  container.classList.add('user-identity');

  const trigger = documentRef.createElement('button');
  trigger.type = 'button';
  trigger.className = 'user-identity-trigger';
  trigger.setAttribute('aria-label', `当前用户：${user.name || '未命名用户'}`);
  trigger.setAttribute('aria-haspopup', 'menu');
  trigger.setAttribute('aria-expanded', 'false');
  trigger.append(makeAvatar(documentRef, user));

  const summary = documentRef.createElement('span');
  summary.className = 'user-identity-summary';
  appendTextLine(documentRef, summary, 'user-identity-name', user.name || '未命名用户');
  const feishu = feishuState(user);
  appendTextLine(
    documentRef,
    summary,
    'user-identity-capability',
    `${user.auth_mode === 'password' ? '账号登录' : '飞书登录'} · ${feishu.short}`,
  );
  trigger.append(summary);
  const caret = documentRef.createElement('span');
  caret.className = 'user-identity-caret';
  caret.setAttribute('aria-hidden', 'true');
  caret.append(makeCaretIcon(documentRef));
  trigger.append(caret);

  const menu = documentRef.createElement('div');
  menu.className = 'user-identity-menu';
  const menuID = `${container.id || 'userIdentity'}Menu`;
  menu.id = menuID;
  menu.setAttribute('role', 'menu');
  menu.setAttribute('aria-label', '个人身份菜单');
  menu.hidden = true;
  trigger.setAttribute('aria-controls', menuID);

  const profile = documentRef.createElement('div');
  profile.className = 'user-identity-profile';
  profile.append(makeAvatar(documentRef, user, true));
  const profileCopy = documentRef.createElement('div');
  profileCopy.className = 'user-identity-profile-copy';
  appendTextLine(documentRef, profileCopy, 'user-identity-profile-name', user.name || '未命名用户');
  const profileMeta = documentRef.createElement('div');
  profileMeta.className = 'user-identity-profile-meta';
  const loginMode = documentRef.createElement('span');
  loginMode.className = 'user-identity-mode';
  loginMode.textContent = user.auth_mode === 'feishu' ? '飞书登录' : '账号密码登录';
  profileMeta.append(loginMode);
  const adminRole = ruleAdminRoleLabel(user);
  if (adminRole) {
    const roleBadge = documentRef.createElement('span');
    roleBadge.className = 'user-identity-role';
    roleBadge.textContent = adminRole;
    profileMeta.append(roleBadge);
  }
  profileCopy.append(profileMeta);
  profile.append(profileCopy);
  menu.append(profile);

  const capability = documentRef.createElement('div');
  capability.className = `user-identity-status${feishu.ok ? '' : ' is-locked'}`;
  const statusDot = documentRef.createElement('span');
  statusDot.className = 'user-identity-status-dot';
  statusDot.setAttribute('aria-hidden', 'true');
  const statusText = documentRef.createElement('span');
  // 刚从飞书授权页回来、没授权成：状态行写原因，菜单打开一次让他看到（「授权飞书…」还在，可以再点）。
  const grantResult = takeFeishuGrantResult(windowRef);
  statusText.textContent = grantResult || feishu.long;
  if (grantResult) capability.classList.add('is-locked');
  capability.append(statusDot, statusText);
  capability.setAttribute('role', 'status');
  capability.setAttribute('aria-live', 'polite');
  menu.append(capability);

  if (feishu.grant) {
    const grantButton = documentRef.createElement('button');
    grantButton.type = 'button';
    grantButton.className = 'user-identity-grant';
    grantButton.setAttribute('role', 'menuitem');
    grantButton.textContent = '授权飞书…';
    grantButton.addEventListener('click', () => {
      setOpen(false);
      navigate(windowRef, feishuGrantURL(windowRef));
    });
    menu.append(grantButton);
  }

  const divider = documentRef.createElement('div');
  divider.className = 'user-identity-divider';
  menu.append(divider);

  // 接入智能体（红线 51）：只给能接的人（飞书登录的同事）；点了打开窗口拿提示词。
  if (user.capabilities?.agent) {
    const agentButton = documentRef.createElement('button');
    agentButton.type = 'button';
    agentButton.className = 'user-identity-action';
    agentButton.setAttribute('role', 'menuitem');
    agentButton.setAttribute('data-agent-connect', '');
    agentButton.append(makeAgentIcon(documentRef));
    const agentCopy = documentRef.createElement('span');
    agentCopy.className = 'user-identity-action-copy';
    appendTextLine(documentRef, agentCopy, 'user-identity-action-label', '接入智能体');
    agentButton.append(agentCopy, makeActionChevronIcon(documentRef));
    agentButton.addEventListener('click', () => {
      setOpen(false);
      openAgentConnect({ documentRef, fetchImpl });
    });
    menu.append(agentButton);
  }

  if (adminRole) {
    const adminButton = documentRef.createElement('button');
    adminButton.type = 'button';
    adminButton.id = 'adminLockBtn';
    adminButton.className = 'user-identity-action';
    adminButton.setAttribute('role', 'menuitem');
    adminButton.setAttribute('aria-label', '打开规则后台');
    adminButton.append(makeRuleAdminIcon(documentRef));
    const adminCopy = documentRef.createElement('span');
    adminCopy.className = 'user-identity-action-copy';
    appendTextLine(documentRef, adminCopy, 'user-identity-action-label', '规则后台');
    appendTextLine(documentRef, adminCopy, 'user-identity-action-meta', '管理排版规则与权限');
    adminButton.append(adminCopy, makeActionChevronIcon(documentRef));
    menu.append(adminButton);
    adminButton.addEventListener('click', () => setOpen(false));
  }

  if (menu.querySelector('.user-identity-action')) {
    const actionDivider = documentRef.createElement('div');
    actionDivider.className = 'user-identity-divider';
    menu.append(actionDivider);
  }

  const logoutButton = documentRef.createElement('button');
  logoutButton.type = 'button';
  logoutButton.className = 'user-identity-logout';
  logoutButton.setAttribute('role', 'menuitem');
  logoutButton.setAttribute('aria-label', '退出登录');
  logoutButton.textContent = '退出登录';
  logoutButton.addEventListener('click', async () => {
    logoutButton.disabled = true;
    setOpen(false);
    try {
      await logoutSession({ fetchImpl });
    } finally {
      navigate(windowRef, `/auth/login?next=${encodeURIComponent(currentReturnTo(windowRef))}`);
    }
  });
  menu.append(logoutButton);

  function menuItems() {
    return [...menu.querySelectorAll('[role="menuitem"]:not([disabled])')];
  }

  function focusMenuItem(last = false) {
    const items = menuItems();
    if (!items.length) return;
    items[last ? items.length - 1 : 0]?.focus();
  }

  function setOpen(open, focusTarget = '') {
    menu.hidden = !open;
    trigger.setAttribute('aria-expanded', String(open));
    container.closest?.('.rail')?.classList.toggle('identity-menu-open', open);
    if (open && focusTarget) focusMenuItem(focusTarget === 'last');
  }
  function onDocumentClick(event) {
    if (!container.contains(event.target)) setOpen(false);
  }
  function onDocumentKeydown(event) {
    if (event.key !== 'Escape' || menu.hidden) return;
    setOpen(false);
    trigger.focus();
  }
  function onTriggerKeydown(event) {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    setOpen(true, event.key === 'ArrowUp' ? 'last' : 'first');
  }
  function onMenuKeydown(event) {
    const items = menuItems();
    const current = items.indexOf(event.currentTarget);
    if (event.key === 'Escape') {
      event.preventDefault();
      setOpen(false);
      trigger.focus();
      return;
    }
    if (current < 0) return;
    let next = current;
    if (event.key === 'ArrowDown') next = (current + 1) % items.length;
    else if (event.key === 'ArrowUp') next = (current - 1 + items.length) % items.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = items.length - 1;
    else return;
    event.preventDefault();
    items[next]?.focus();
  }
  trigger.addEventListener('click', () => setOpen(menu.hidden));
  trigger.addEventListener('keydown', onTriggerKeydown);
  menu.querySelectorAll('[role="menuitem"]').forEach(item => item.addEventListener('keydown', onMenuKeydown));
  documentRef.addEventListener('click', onDocumentClick);
  documentRef.addEventListener('keydown', onDocumentKeydown);
  container.append(trigger, menu);
  if (grantResult) setOpen(true);

  return {
    destroy() {
      documentRef.removeEventListener('click', onDocumentClick);
      documentRef.removeEventListener('keydown', onDocumentKeydown);
      trigger.removeEventListener('keydown', onTriggerKeydown);
      menu.querySelectorAll('[role="menuitem"]').forEach(item => item.removeEventListener('keydown', onMenuKeydown));
      container.closest?.('.rail')?.classList.remove('identity-menu-open');
      container.replaceChildren();
    },
  };
}
