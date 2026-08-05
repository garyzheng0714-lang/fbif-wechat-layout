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

test('both editor shells put the publishing target inside the import card and the signed-in identity in the rail footer', () => {
  for (const shell of [app, site]) {
    const headerStart = shell.indexOf('<header class="global-channel-bar">');
    const headerEnd = shell.indexOf('</header>', headerStart);
    const dropZoneAt = shell.indexOf('id="dropZone"');
    const composerInputAt = shell.indexOf('data-url-focus-target');
    const railFootAt = shell.indexOf('<div class="rail-foot">');
    const railEndAt = shell.indexOf('</aside>');
    const identityAt = shell.indexOf('id="userIdentity"');
    const accountAt = shell.indexOf('id="acctMenu"');
    assert.ok(headerStart >= 0 && headerEnd > headerStart);
    const header = shell.slice(headerStart, headerEnd);
    assert.doesNotMatch(header, /id="userIdentity"/, 'the top bar keeps only page actions, not the signed-in identity');
    assert.doesNotMatch(header, /id="acctMenu"/, 'the top bar keeps only page actions, not the publishing target');
    assert.ok(
      accountAt > dropZoneAt && accountAt < composerInputAt,
      'publishing target should be the first row inside the import card, above the composer input',
    );
    assert.ok(
      railFootAt > 0 && identityAt > railFootAt && identityAt < railEndAt,
      'signed-in identity should live in the left rail footer',
    );
    assert.match(shell, /id="feishuDocCapability"/);
    assert.match(shell, /id="feishuDocSearch"/);
    assert.match(shell, /data-feishu-doc-trigger/);
    assert.match(shell, /mountUserIdentity\(document\.getElementById\('userIdentity'\), authUser\)/);
    assert.match(shell, /mountFeishuDocumentImport/);
  }
  const appToolbar = app.slice(app.indexOf('id="previewToolbar"'), app.indexOf('</header>'));
  const siteToolbar = site.slice(site.indexOf('id="previewToolbar"'), site.indexOf('</header>'));
  assert.match(appToolbar, /id="copyBtn"/, 'app copy action stays in the top bar');
  assert.match(siteToolbar, /id="copyBtn"/, 'site copy action stays in the top bar');
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
