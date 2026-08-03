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

export async function loadOptionalCurrentUser({
  documentRef = globalThis.document,
  fetchImpl = globalThis.fetch,
  storage,
} = {}) {
  let user = null;
  try {
    user = await loadCurrentUser({ fetchImpl, storage });
  } catch {
    // The editor is intentionally public while the login rollout is paused.
    // Account-service failures must not block local files, text, or URL import.
  }
  documentRef?.body?.classList.remove('auth-pending', 'auth-unavailable');
  return user;
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
  menu.setAttribute('role', 'menu');
  menu.hidden = true;

  const profile = documentRef.createElement('div');
  profile.className = 'user-identity-profile';
  profile.append(makeAvatar(documentRef, user, true));
  const profileCopy = documentRef.createElement('div');
  profileCopy.className = 'user-identity-profile-copy';
  appendTextLine(documentRef, profileCopy, 'user-identity-profile-name', user.name || '未命名用户');
  appendTextLine(
    documentRef,
    profileCopy,
    'user-identity-mode',
    user.auth_mode === 'feishu' ? '飞书登录' : '账号密码登录',
  );
  profile.append(profileCopy);
  menu.append(profile);

  const capability = appendTextLine(
    documentRef,
    menu,
    `user-identity-status${user.capabilities?.feishu_documents ? '' : ' is-locked'}`,
    user.capabilities?.feishu_documents
      ? '飞书云文档已连接，可在输入框中使用 @ 搜索并导入。'
      : '飞书文档搜索、提取和生成功能不可用。',
  );
  capability.setAttribute('role', 'status');

  const divider = documentRef.createElement('div');
  divider.className = 'user-identity-divider';
  menu.append(divider);
  const logoutButton = documentRef.createElement('button');
  logoutButton.type = 'button';
  logoutButton.className = 'user-identity-logout';
  logoutButton.setAttribute('role', 'menuitem');
  logoutButton.textContent = '退出登录';
  logoutButton.addEventListener('click', async () => {
    logoutButton.disabled = true;
    try {
      await logoutSession({ fetchImpl, storage });
    } finally {
      navigate(windowRef, `/login.html?return_to=${encodeURIComponent(currentReturnTo(windowRef))}`);
    }
  });
  menu.append(logoutButton);

  function setOpen(open) {
    menu.hidden = !open;
    trigger.setAttribute('aria-expanded', String(open));
    container.closest?.('.rail')?.classList.toggle('identity-menu-open', open);
  }
  function onDocumentClick(event) {
    if (!container.contains(event.target)) setOpen(false);
  }
  function onDocumentKeydown(event) {
    if (event.key !== 'Escape' || menu.hidden) return;
    setOpen(false);
    trigger.focus();
  }
  trigger.addEventListener('click', () => setOpen(menu.hidden));
  documentRef.addEventListener('click', onDocumentClick);
  documentRef.addEventListener('keydown', onDocumentKeydown);
  container.append(trigger, menu);

  return {
    destroy() {
      documentRef.removeEventListener('click', onDocumentClick);
      documentRef.removeEventListener('keydown', onDocumentKeydown);
      container.closest?.('.rail')?.classList.remove('identity-menu-open');
      container.replaceChildren();
    },
  };
}
