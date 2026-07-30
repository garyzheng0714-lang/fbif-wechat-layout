import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const app = fs.readFileSync(path.join(root, 'public/app.html'), 'utf8');
const site = fs.readFileSync(path.join(root, 'public/site.html'), 'utf8');
const login = fs.readFileSync(path.join(root, 'public/login.html'), 'utf8');
const loginCSS = fs.readFileSync(path.join(root, 'public/css/auth-login.css'), 'utf8');
const caddy = fs.readFileSync(path.join(root, 'deploy/caddy/fbifmp-layout.caddy'), 'utf8');

test('both editor shells put user identity in the left rail and keep the global right toolbar unchanged', () => {
  for (const shell of [app, site]) {
    const railStart = shell.indexOf('class="rail-body');
    const identityAt = shell.indexOf('id="userIdentity"');
    const importAt = shell.indexOf('id="importZone"');
    assert.ok(railStart >= 0 && identityAt > railStart && identityAt < importAt);
    assert.match(shell, /id="feishuDocCapability"/);
    assert.match(shell, /id="feishuDocSearch"/);
    assert.match(shell, /输入 @ 搜索飞书文档/);
    assert.match(shell, /loadOptionalCurrentUser\(\)/);
    assert.match(shell, /if \(authUser\) authSession\.mountUserIdentity\(document\.getElementById\('userIdentity'\), authUser\)/);
    assert.doesNotMatch(shell, /ensureAuthenticatedSession\(\)/);
    assert.match(shell, /mountFeishuDocumentImport/);
  }
  const appToolbar = app.slice(app.indexOf('id="previewToolbar"'), app.indexOf('</header>'));
  const siteToolbar = site.slice(site.indexOf('id="previewToolbar"'), site.indexOf('</header>'));
  assert.doesNotMatch(appToolbar, /userIdentity|user-avatar|退出登录/);
  assert.doesNotMatch(siteToolbar, /userIdentity|user-avatar|退出登录/);
});

test('login page reuses the approved production mascot gate with the password-account variant', () => {
  assert.match(login, /\/assets\/door-login\.webp/);
  assert.match(login, /\/assets\/fbif-login-logo\.png/);
  assert.match(login, />使用飞书登录</);
  assert.match(login, /id="passwordLoginForm"/);
  assert.match(login, /autocomplete="username"/);
  assert.match(login, /autocomplete="current-password"/);
  assert.match(login, /class="auth-pill-field/);
  assert.match(login, /30 天内保持登录/);
  assert.match(login, /data-account-help/);
  assert.match(login, /不能搜索、提取或生成飞书云文档/);
  assert.doesNotMatch(login, /注册账号/);
});

test('password login keeps the mascot illustration at the approved half-card width', () => {
  assert.match(loginCSS, /@media \(min-width:\s*1024px\)[\s\S]*?\.auth-login-card\s*\{[^}]*grid-template-columns:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\);/);
  assert.doesNotMatch(loginCSS, /\.auth-login-card\s*\{[^}]*grid-template-columns:\s*2fr 3fr;/s);
  assert.match(loginCSS, /\.auth-pill-field\s*\{[^}]*height:\s*48px;[^}]*border-radius:\s*999px;/s);
  assert.match(loginCSS, /\.auth-password-submit\s*\{[^}]*height:\s*48px;[^}]*border-radius:\s*999px;[^}]*background:\s*#1f2329;/s);
});

test('production routes login APIs to Go while keeping the browser callback static', () => {
  assert.match(caddy, /@fbifAuth path \/auth\/feishu\/\* \/auth\/password\/login/);
  assert.match(caddy, /handle @fbifAuth\s*\{\s*reverse_proxy localhost:9001\s*\}/s);
  assert.match(caddy, /@fbifAppShell path[^\n]*\/login\.html[^\n]*\/auth-callback\.html/);
  assert.doesNotMatch(caddy, /@fbifAuth path[^\n]*\/auth-callback/);
});
