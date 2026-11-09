import assert from 'node:assert/strict';
import test from 'node:test';
import { parseHTML } from 'linkedom';

import {
  ensureAuthenticatedSession,
  feishuGrantURL,
  loadCurrentUser,
  mountUserIdentity,
} from '../public/js/auth-session.js';

function jsonResponse(payload, status = 200, headers = {}) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

function locationRef(search = '?v=bff485e&account=legacy', userAgent = 'Mozilla/5.0 Chrome/130') {
  const assigned = [];
  return {
    assigned,
    windowRef: {
      navigator: { userAgent },
      location: {
        pathname: '/mp/fbif',
        search,
        hash: '',
        assign(target) { assigned.push(target); },
      },
      history: {
        state: null,
        replaced: '',
        replaceState(_state, _title, url) { this.replaced = url; },
      },
    },
  };
}

const bodyRef = { body: { classList: { remove() {}, add() {} } } };

test('没登录时去统一登录，next 不带版本号', async () => {
  const { windowRef, assigned } = locationRef();
  const result = await ensureAuthenticatedSession({
    windowRef,
    documentRef: bodyRef,
    fetchImpl: async () => jsonResponse({ error: 'login_required', login_url: '/auth/login' }, 401),
  });
  assert.equal(result, null);
  assert.deepEqual(assigned, [`/auth/login?next=${encodeURIComponent('/mp/fbif?account=legacy')}`]);
});

test('飞书客户端里没登录直接走飞书登录，不用再点', async () => {
  const { windowRef, assigned } = locationRef('', 'Mozilla/5.0 Lark/7.30 Feishu');
  await ensureAuthenticatedSession({
    windowRef,
    documentRef: bodyRef,
    fetchImpl: async () => jsonResponse({ error: 'login_required' }, 401),
  });
  assert.deepEqual(assigned, [`/auth/feishu/fbif/login?next=${encodeURIComponent('/mp/fbif')}`]);
});

test('登录服务连不上不当没登录：不跳登录页，报错', async () => {
  const { windowRef, assigned } = locationRef();
  await assert.rejects(ensureAuthenticatedSession({
    windowRef,
    documentRef: bodyRef,
    fetchImpl: async () => jsonResponse({ error: 'login_service_unavailable' }, 503),
  }));
  assert.deepEqual(assigned, []);
});

test('loadCurrentUser 只带同源 cookie、不带令牌，读 /api/auth/me', async () => {
  let requested;
  const user = { id: 'FJY632B9P3E:on_test', name: '张三', auth_mode: 'feishu', feishu: 'ok', capabilities: { feishu_documents: true } };
  const result = await loadCurrentUser({
    fetchImpl: async (url, options) => {
      requested = { url, options };
      return jsonResponse({ user });
    },
  });
  assert.equal(requested.url, '/api/auth/me');
  assert.equal(requested.options.credentials, 'same-origin');
  assert.equal(requested.options.headers, undefined);
  assert.deepEqual(result, user);
});

test('授权飞书地址带回当前页面', () => {
  const { windowRef } = locationRef('?v=abc&account=foodtalks-mp&feishu_grant=denied');
  assert.equal(feishuGrantURL(windowRef), `/auth/feishu-grant?next=${encodeURIComponent('/mp/fbif?account=foodtalks-mp')}`);
});

test('mountUserIdentity renders name, login mode and Feishu capability for the shell identity menu', () => {
  const { document, window } = parseHTML('<main><div id="identity"></div></main>');
  const user = {
    id: 'feishu:ou_test',
    name: '张三',
    avatar_url: '',
    auth_mode: 'feishu',
    role: 'super_admin',
    feishu: 'ok',
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
  assert.equal(document.querySelector('.user-identity-grant'), null, '已授权的不给「授权飞书」');
  assert.match(text, /超级管理员/);
  assert.match(text, /规则后台/);
  assert.equal(document.querySelector('#adminLockBtn')?.closest('.user-identity-menu')?.getAttribute('role'), 'menu');
  assert.equal(document.querySelector('[data-user-avatar-fallback]').textContent, '张');
});

test('password identity keeps the name visible and states that cloud documents are unavailable', () => {
  const { document, window } = parseHTML('<main><div id="identity"></div></main>');
  mountUserIdentity(document.getElementById('identity'), {
    id: 'account:vendor',
    name: '外部编辑',
    auth_mode: 'password',
    feishu: 'none',
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
  assert.equal(document.querySelector('.user-identity-grant'), null, '授权不了飞书的不给「授权飞书」');
});

test('没授权飞书的人菜单里有「授权飞书…」，点了去授权、回到当前页面', () => {
  const { document } = parseHTML('<main><div id="identity"></div></main>');
  const { windowRef, assigned } = locationRef('?account=fbif');
  mountUserIdentity(document.getElementById('identity'), {
    id: 'FJY632B9P3E:on_new',
    name: '新同事',
    auth_mode: 'feishu',
    feishu: 'required',
    capabilities: { feishu_documents: true },
  }, { documentRef: document, windowRef });
  const text = document.getElementById('identity').textContent;
  assert.match(text, /云文档未授权/);
  const grant = document.querySelector('.user-identity-grant');
  assert.equal(grant.getAttribute('role'), 'menuitem');
  assert.equal(grant.textContent, '授权飞书…');
  assert.equal(document.querySelector('.user-identity-menu').hidden, true);
  grant.click();
  assert.deepEqual(assigned, [`/auth/feishu-grant?next=${encodeURIComponent('/mp/fbif?account=fbif')}`]);
});

test('从飞书授权页回来没授权成：菜单打开、写原因，地址上的结果去掉', () => {
  const { document } = parseHTML('<main><div id="identity"></div></main>');
  const { windowRef } = locationRef('?account=fbif&feishu_grant=mismatch');
  mountUserIdentity(document.getElementById('identity'), {
    id: 'FJY632B9P3E:on_new',
    name: '新同事',
    auth_mode: 'feishu',
    feishu: 'required',
    capabilities: { feishu_documents: true },
  }, { documentRef: document, windowRef });
  assert.equal(document.querySelector('.user-identity-menu').hidden, false);
  assert.match(document.querySelector('.user-identity-status').textContent, /不是同一个人/);
  assert.equal(windowRef.history.replaced, '/mp/fbif?account=fbif');
});

test('退出登录走统一登录的退出，再回登录页', async () => {
  const { document } = parseHTML('<main><div id="identity"></div></main>');
  const { windowRef, assigned } = locationRef('?account=fbif');
  const calls = [];
  mountUserIdentity(document.getElementById('identity'), {
    id: 'account:rec1',
    name: '外部编辑',
    auth_mode: 'password',
    feishu: 'none',
    capabilities: { feishu_documents: false },
  }, {
    documentRef: document,
    windowRef,
    fetchImpl: async (url, options) => {
      calls.push([url, options.method]);
      return new Response(null, { status: 204 });
    },
  });
  document.querySelector('.user-identity-logout').click();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(calls, [['/auth/feishu/logout', 'POST']]);
  assert.deepEqual(assigned, [`/auth/login?next=${encodeURIComponent('/mp/fbif?account=fbif')}`]);
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
    feishu: 'ok',
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
