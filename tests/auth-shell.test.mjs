import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const app = fs.readFileSync(path.join(root, 'public/app.html'), 'utf8');
const site = fs.readFileSync(path.join(root, 'public/site.html'), 'utf8');
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

test('本项目没有登录页：登录页只在登录中心，旧的登录页、回调页都删了', () => {
  for (const file of ['public/login.html', 'public/auth-callback.html', 'public/js/login.js', 'public/css/auth-login.css']) {
    assert.equal(fs.existsSync(path.join(root, file)), false, `${file} 不该再有`);
  }
});

test('登录、退出、登录健康都归入口和登录中心，站点不再把 /auth/* 转给 Go（飞书授权两条除外）', () => {
  assert.match(caddy, /^\timport fbif_login 127\.0\.0\.1:9001$/m);
  assert.doesNotMatch(caddy, /@fbifAuth|@fbifLoginHealth|\/auth\/center|backchannel-logout/);
  const directives = caddy.split('\n').filter(line => !line.trim().startsWith('#')).join('\n');
  // /internal/auth/* 只有停用名单提醒这一条交给 Go（智能体的名单马上拉），别的归登录中心。
  const internalPaths = [...directives.matchAll(/\/internal\/auth\/[\w\/-]*/g)].map(m => m[0]);
  assert.deepEqual([...new Set(internalPaths)], ['/internal/auth/blocklist-changed']);
  const authPaths = [...directives.replaceAll('/internal/auth/', '').matchAll(/\/auth\/[\w\/-]*/g)].map(m => m[0]);
  assert.deepEqual([...new Set(authPaths)].sort(), ['/auth/feishu-grant', '/auth/feishu/fbif/qr-callback']);
  assert.doesNotMatch(caddy, /login\.html|auth-callback|\/auth\/password/);
});
