import assert from 'node:assert/strict';
import test from 'node:test';
import { parseHTML } from 'linkedom';

import {
  SESSION_STORAGE_KEY,
  ensureAuthenticatedSession,
  loadCurrentUser,
  mountUserIdentity,
} from '../public/js/auth-session.js';

function jsonResponse(payload, status = 200, headers = {}) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

test('unauthenticated redirect drops the build version from return_to', async () => {
  let assigned = '';
  const windowRef = {
    location: {
      pathname: '/mp/fbif',
      search: '?v=bff485e&account=legacy',
      hash: '',
      assign(target) { assigned = target; },
    },
  };
  const result = await ensureAuthenticatedSession({
    windowRef,
    documentRef: { body: { classList: { remove() {}, add() {} } }, querySelectorAll: () => [] },
    fetchImpl: async () => jsonResponse({ code: 'authentication_required' }, 401),
    releaseGate: false,
  });
  assert.equal(result, null);
  const returnTo = decodeURIComponent(String(assigned).split('return_to=')[1] || '');
  assert.equal(returnTo, '/mp/fbif?account=legacy');
});

test('loadCurrentUser sends the fragment fallback token and stores the renewed session', async () => {
  const stored = new Map([[SESSION_STORAGE_KEY, 'old-token']]);
  const storage = {
    getItem: key => stored.get(key) || null,
    setItem: (key, value) => stored.set(key, value),
    removeItem: key => stored.delete(key),
  };
  let requestHeaders;
  const user = {
    id: 'feishu:ou_test',
    name: '张三',
    auth_mode: 'feishu',
    capabilities: { feishu_documents: true },
  };
  const result = await loadCurrentUser({
    storage,
    fetchImpl: async (_url, options) => {
      requestHeaders = options.headers;
      return jsonResponse({ user, session_token: 'new-token' });
    },
  });
  assert.equal(requestHeaders['X-Session-Token'], 'old-token');
  assert.equal(stored.get(SESSION_STORAGE_KEY), 'new-token');
  assert.deepEqual(result, user);
});

test('loadCurrentUser returns null and clears an expired fallback token on 401', async () => {
  const stored = new Map([[SESSION_STORAGE_KEY, 'expired-token']]);
  const storage = {
    getItem: key => stored.get(key) || null,
    setItem: (key, value) => stored.set(key, value),
    removeItem: key => stored.delete(key),
  };
  const result = await loadCurrentUser({
    storage,
    fetchImpl: async () => jsonResponse({ code: 'authentication_required' }, 401),
  });
  assert.equal(result, null);
  assert.equal(stored.has(SESSION_STORAGE_KEY), false);
});

test('mountUserIdentity renders name, login mode and Feishu capability for the shell identity menu', () => {
  const { document, window } = parseHTML('<main><div id="identity"></div></main>');
  const user = {
    id: 'feishu:ou_test',
    name: '张三',
    avatar_url: '',
    auth_mode: 'feishu',
    role: 'super_admin',
    capabilities: { feishu_documents: true, rule_admin: true, rule_super_admin: true },
  };
  mountUserIdentity(document.getElementById('identity'), user, {
    documentRef: document,
    windowRef: window,
  });
  const text = document.getElementById('identity').textContent;
  assert.match(text, /张三/);
  assert.match(text, /飞书登录/);
  assert.match(text, /云文档已连接/);
  assert.match(text, /超级管理员/);
  assert.match(text, /规则后台/);
  assert.equal(document.querySelector('#adminLockBtn')?.closest('.user-identity-menu')?.getAttribute('role'), 'menu');
  assert.equal(document.querySelector('[data-user-avatar-fallback]').textContent, '张');
});

test('password identity keeps the name visible and states that cloud documents are unavailable', () => {
  const { document, window } = parseHTML('<main><div id="identity"></div></main>');
  mountUserIdentity(document.getElementById('identity'), {
    id: 'password:vendor',
    name: '外部编辑',
    auth_mode: 'password',
    capabilities: { feishu_documents: false },
  }, {
    documentRef: document,
    windowRef: window,
  });
  const text = document.getElementById('identity').textContent;
  assert.match(text, /外部编辑/);
  assert.match(text, /账号密码登录/);
  assert.match(text, /云文档不可用/);
  assert.equal(document.querySelector('#adminLockBtn'), null, '未授权账号不应显示规则后台入口');
  assert.equal(document.querySelector('[data-switch-feishu-login]'), null, '账号菜单不能主动要求改用飞书登录');
});

test('identity menu puts rule admin in the user popup and supports keyboard navigation', () => {
  const { document, window } = parseHTML('<main><div class="rail"><div id="identity"></div></div></main>');
  let focused = document.body;
  Object.defineProperty(document, 'activeElement', { configurable: true, get: () => focused });
  window.HTMLElement.prototype.focus = function focus() { focused = this; };
  mountUserIdentity(document.getElementById('identity'), {
    id: 'feishu:ou_admin',
    name: '管理员',
    auth_mode: 'feishu',
    role: 'admin',
    capabilities: { feishu_documents: true, rule_admin: true },
  }, { documentRef: document, windowRef: window });

  const trigger = document.querySelector('.user-identity-trigger');
  const adminButton = document.querySelector('#adminLockBtn');
  const logoutButton = document.querySelector('.user-identity-logout');
  const menu = document.querySelector('.user-identity-menu');
  assert.ok(adminButton);
  assert.equal(adminButton.closest('.user-identity-menu'), menu);
  assert.equal(menu.hidden, true);

  const open = new window.Event('keydown', { bubbles: true, cancelable: true });
  open.key = 'ArrowDown';
  trigger.dispatchEvent(open);
  assert.equal(menu.hidden, false);
  assert.equal(document.activeElement, adminButton, '键盘打开后优先聚焦规则后台');

  const down = new window.Event('keydown', { bubbles: true, cancelable: true });
  down.key = 'ArrowDown';
  adminButton.dispatchEvent(down);
  assert.equal(document.activeElement, logoutButton, '方向键可以移动到退出登录');

  const escape = new window.Event('keydown', { bubbles: true, cancelable: true });
  escape.key = 'Escape';
  logoutButton.dispatchEvent(escape);
  assert.equal(menu.hidden, true);
  assert.equal(document.activeElement, trigger, 'Escape 关闭后焦点回到身份触发器');
});
