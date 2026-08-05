export const SESSION_STORAGE_KEY = 'fbif.auth.session_token';

function usableStorage(storage) {
  if (storage) return storage;
  try {
    return globalThis.sessionStorage || null;
  } catch {
    return null;
  }
}

export function getStoredSessionToken(storage) {
  try {
    return usableStorage(storage)?.getItem(SESSION_STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

export function storeSessionToken(token, storage) {
  const target = usableStorage(storage);
  if (!target) return;
  try {
    if (token) target.setItem(SESSION_STORAGE_KEY, token);
    else target.removeItem(SESSION_STORAGE_KEY);
  } catch {
    // Cookie is still the primary session transport. Storage failure only
    // disables the WebView fallback and must not break the editor.
  }
}

export async function authFetch(input, init = {}, {
  fetchImpl = globalThis.fetch,
  storage,
} = {}) {
  const token = getStoredSessionToken(storage);
  const headers = { ...(init.headers || {}) };
  if (token) headers['X-Session-Token'] = token;
  return fetchImpl(input, {
    ...init,
    headers,
    credentials: 'same-origin',
  });
}

export async function loadCurrentUser({
  fetchImpl = globalThis.fetch,
  storage,
} = {}) {
  const response = await authFetch('/api/me', { method: 'GET' }, { fetchImpl, storage });
  if (response.status === 401) {
    storeSessionToken('', storage);
    return null;
  }
  if (!response.ok) {
    throw new Error('账号服务暂不可用');
  }
  const payload = await response.json();
  const renewedToken = String(payload.session_token || response.headers.get('X-Session-Token') || '');
  if (renewedToken) storeSessionToken(renewedToken, storage);
  return payload.user || null;
}

function currentReturnTo(windowRef) {
  const location = windowRef?.location;
  if (!location) return '/mp/fbif';
  return `${location.pathname || '/mp/fbif'}${location.search || ''}${location.hash || ''}`;
}

function navigate(windowRef, target) {
  if (windowRef?.location?.assign) {
    windowRef.location.assign(target);
  } else if (windowRef?.location) {
    windowRef.location.href = target;
  }
}

export async function ensureAuthenticatedSession({
  windowRef = globalThis.window,
  documentRef = globalThis.document,
  fetchImpl = globalThis.fetch,
  storage,
} = {}) {
  try {
    const user = await loadCurrentUser({ fetchImpl, storage });
    if (!user) {
      const returnTo = currentReturnTo(windowRef);
      navigate(windowRef, `/login.html?return_to=${encodeURIComponent(returnTo)}`);
      return null;
    }
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
  storage,
} = {}) {
  try {
    await authFetch('/auth/feishu/logout', { method: 'POST' }, { fetchImpl, storage });
  } finally {
    storeSessionToken('', storage);
  }
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

export function mountUserIdentity(container, user, {
  documentRef = globalThis.document,
  windowRef = globalThis.window,
  fetchImpl = globalThis.fetch,
  storage,
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
  appendTextLine(
    documentRef,
    summary,
    'user-identity-capability',
    `${user.auth_mode === 'password' ? '账号登录' : '飞书登录'} · ${
      user.capabilities?.feishu_documents ? '云文档已连接' : '云文档不可用'
    }`,
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
  capability.className = `user-identity-status${user.capabilities?.feishu_documents ? '' : ' is-locked'}`;
  const statusDot = documentRef.createElement('span');
  statusDot.className = 'user-identity-status-dot';
  statusDot.setAttribute('aria-hidden', 'true');
  const statusText = documentRef.createElement('span');
  statusText.textContent = user.capabilities?.feishu_documents
    ? '飞书云文档已连接'
    : '飞书云文档不可用';
  capability.append(statusDot, statusText);
  capability.setAttribute('role', 'status');
  capability.setAttribute('aria-live', 'polite');
  menu.append(capability);

  const divider = documentRef.createElement('div');
  divider.className = 'user-identity-divider';
  menu.append(divider);

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
    const actionDivider = documentRef.createElement('div');
    actionDivider.className = 'user-identity-divider';
    menu.append(actionDivider);
    adminButton.addEventListener('click', () => setOpen(false));
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
      await logoutSession({ fetchImpl, storage });
    } finally {
      navigate(windowRef, `/login.html?return_to=${encodeURIComponent(currentReturnTo(windowRef))}`);
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
