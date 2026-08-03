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

test('strict editor session keeps the shell hidden and sends anonymous users to login', async () => {
  const { document } = parseHTML('<html><body class="auth-pending"></body></html>');
  const navigations = [];
  const user = await ensureAuthenticatedSession({
    windowRef: {
      location: {
        pathname: '/mp/fbif',
        search: '?v=test',
        hash: '',
        assign: target => navigations.push(target),
      },
    },
    documentRef: document,
    fetchImpl: async () => jsonResponse({ code: 'authentication_required' }, 401),
  });
  assert.equal(user, null);
  assert.deepEqual(navigations, ['/login.html?return_to=%2Fmp%2Ffbif%3Fv%3Dtest']);
  assert.equal(document.body.classList.contains('auth-pending'), true);
  assert.equal(document.body.classList.contains('auth-unavailable'), false);
});

test('strict editor session fails closed when the account service is unavailable', async () => {
  const { document } = parseHTML('<html><body class="auth-pending auth-unavailable"></body></html>');
  await assert.rejects(
    ensureAuthenticatedSession({
      documentRef: document,
      fetchImpl: async () => jsonResponse({ code: 'account_service_failed' }, 503),
    }),
    /账号服务暂不可用/,
  );
  assert.equal(document.body.classList.contains('auth-pending'), false);
  assert.equal(document.body.classList.contains('auth-unavailable'), true);
});

test('mountUserIdentity renders name, login mode and Feishu capability in the left rail', () => {
  const { document, window } = parseHTML('<main><div id="identity"></div></main>');
  const user = {
    id: 'feishu:ou_test',
    name: '张三',
    avatar_url: '',
    auth_mode: 'feishu',
    capabilities: { feishu_documents: true },
  };
  mountUserIdentity(document.getElementById('identity'), user, {
    documentRef: document,
    windowRef: window,
  });
  const text = document.getElementById('identity').textContent;
  assert.match(text, /张三/);
  assert.match(text, /飞书登录/);
  assert.match(text, /云文档已连接/);
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
  assert.equal(document.querySelector('[data-switch-feishu-login]'), null, '顶栏账号菜单不能主动要求改用飞书登录');
});
