// 入口真跑一遍（FBIF 统一登录 v5）：用本机 caddy 加载 deploy/caddy/fbifmp-layout.caddy 和登录中心的入口片段，
// 假登录中心（带 cookie who=ok 就放行、写 X-FBIF-User: REAL）和假 Go（回路径和收到的头），核对哪些路径过登录、
// 不过登录的路径收不到冒充的头、飞书授权拿得到中心给的人（2026-11-20 本机实测：handle 里 forward_auth 排在
// request_header 前，没包 route 时中心给的人被删掉）。本机没装 caddy、找不到入口片段就跳过。
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdtemp, readFile, rm, writeFile, mkdir } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const repo = fileURLToPath(new URL('..', import.meta.url));
const snippetPath = [
  process.env.FBIF_LOGIN_SNIPPET,
  join(repo, '..', 'FBIF-登录中心', 'deploy', 'gateway.caddy'),
  '/etc/caddy/snippets/fbif-login.caddy',
].find(path => path && existsSync(path));
const caddyAvailable = spawnSync('caddy', ['version']).status === 0;
const skip = !caddyAvailable ? '本机没装 caddy' : !snippetPath ? '找不到登录中心的入口片段' : false;

function listen(handler) {
  return new Promise(resolve => {
    const server = createServer(handler);
    server.listen(0, '127.0.0.1', () => resolve(server));
  });
}

function freePort() {
  return new Promise(resolve => {
    const server = createServer();
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

test('入口：页面和接口过登录，别的程序的路径不过且收不到冒充的头，飞书授权拿得到中心给的人', { skip }, async t => {
  const center = await listen((req, res) => {
    if (req.url.startsWith('/_fbif/verify')) {
      if ((req.headers.cookie || '').includes('who=ok')) {
        res.writeHead(200, { 'X-FBIF-User': 'REAL' }).end();
      } else {
        res.writeHead(401).end('center-401');
      }
      return;
    }
    res.writeHead(200).end(`center:${req.url}`);
  });
  const app = await listen((req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json' })
      .end(JSON.stringify({ app: req.url, user: req.headers['x-fbif-user'] ?? null }));
  });
  const dir = await mkdtemp(join(tmpdir(), 'fbif-layout-gateway-'));
  await mkdir(join(dir, 'www', 'js'), { recursive: true });
  await writeFile(join(dir, 'www', 'js', 'a.js'), 'js');
  await writeFile(join(dir, 'www', 'app.html'), 'SHELL');
  await writeFile(join(dir, 'www', 'distribute.html'), 'DIST');
  const port = await freePort();
  const centerAddr = `127.0.0.1:${center.address().port}`;
  const appAddr = `127.0.0.1:${app.address().port}`;
  const snippet = (await readFile(snippetPath, 'utf8')).replaceAll('127.0.0.1:19160', centerAddr);
  const site = (await readFile(join(repo, 'deploy', 'caddy', 'fbifmp-layout.caddy'), 'utf8'))
    .replace('fbifmp-layout.foodtalks.cn {', `http://127.0.0.1:${port} {`)
    .replaceAll('127.0.0.1:19160', centerAddr)
    .replaceAll('127.0.0.1:9001', appAddr)
    .replaceAll('/var/www/wechat-layout', join(dir, 'www'));
  await writeFile(join(dir, 'snippet.caddy'), snippet);
  await writeFile(join(dir, 'site.caddy'), site);
  await writeFile(join(dir, 'Caddyfile'),
    `{\n\tadmin off\n\tauto_https off\n}\nimport ${join(dir, 'snippet.caddy')}\nimport ${join(dir, 'site.caddy')}\n`);

  const caddy = spawn('caddy', ['run', '--adapter', 'caddyfile', '--config', join(dir, 'Caddyfile')], { stdio: 'ignore' });
  t.after(async () => {
    caddy.kill();
    center.close();
    app.close();
    await rm(dir, { recursive: true, force: true });
  });

  const base = `http://127.0.0.1:${port}`;
  const get = async (path, { cookie, forged = true, headers = {}, method = 'GET' } = {}) => {
    const h = { ...headers };
    if (forged) h['X-FBIF-User'] = 'FORGED';
    if (cookie) h.Cookie = cookie;
    const res = await fetch(base + path, { method, headers: h, redirect: 'manual' });
    return { status: res.status, body: await res.text() };
  };
  for (let i = 0; ; i += 1) {
    try { await fetch(`${base}/js/a.js`); break; } catch (error) {
      if (i > 40) throw error;
      await new Promise(resolve => setTimeout(resolve, 100));
    }
  }
  const atApp = (r, user) => {
    assert.equal(r.status, 200, r.body);
    assert.equal(JSON.parse(r.body).user, user, r.body);
  };

  // 别的程序调的路径：不过登录，冒充的头删掉。
  for (const path of ['/api/health', '/api/feishu/card-callback', '/api/layout/v1/render', '/mcp', '/oauth/token',
    '/.well-known/oauth-authorization-server', '/preview/publish/1', '/api/doc-cache/ab/1.png', '/api/agent/uploads/x']) {
    atApp(await get(path), null);
  }
  atApp(await get('/api/publish/site-sync', { method: 'POST' }), null);
  // 带程序钥匙的不过登录；不带的照常过登录。
  atApp(await get('/api/publish/sync-status', { headers: { 'X-Publish-Sync-Token': 'x' }, method: 'POST' }), null);
  atApp(await get('/api/publish/jobs', { headers: { 'X-Publish-Admin-Token': 'x' } }), null);
  atApp(await get('/api/oss-upload', { headers: { Authorization: 'Bearer x' }, method: 'POST' }), null);
  // 以前不登录也能调、现在要登录的接口（2026-11-20 逐个查过）：只有登录后的编辑器页面调；
  // 排版程序取规则直连本机 127.0.0.1:9001，不经入口。
  for (const path of ['/api/image-proxy?url=x', '/api/fetch-article', '/api/fetch-article-meta', '/api/rules/active', '/api/rules/presets']) {
    assert.equal((await get(path)).status, 401, `${path} 没登录要被入口挡住`);
  }
  for (const path of ['/api/publish/sync-status', '/api/oss-upload', '/api/auth/me', '/oauth/authorize', '/mp/fbif', '/app.html', '/']) {
    assert.equal((await get(path)).status, 401, `${path} 没登录要被入口挡住`);
  }
  // 登了：中心给的人到 Go，冒充的不算。
  for (const path of ['/api/auth/me', '/oauth/authorize', '/mp/fbif', '/app.html', '/auth/feishu-grant', '/auth/feishu/fbif/qr-callback?code=1']) {
    atApp(await get(path, { cookie: 'who=ok' }), 'REAL');
  }
  assert.equal((await get('/auth/feishu-grant')).status, 401, '飞书授权也要先登录');
  // 登录、退出、我是谁归登录中心；登录健康不对外。
  for (const path of ['/auth/login', '/auth/feishu/logout', '/auth/feishu/fbif/login', '/api/me']) {
    assert.equal((await get(path)).body, `center:${path}`);
  }
  assert.equal((await get('/internal/auth/health')).status, 404);
  // 停用名单提醒：POST 这一条进 Go（冒充的头删掉），别的 /internal/auth/* 和 GET 照旧给登录中心。
  atApp(await get('/internal/auth/blocklist-changed', { method: 'POST' }), null);
  assert.equal((await get('/internal/auth/blocklist-changed')).body, 'center:/internal/auth/blocklist-changed');
  assert.equal((await get('/internal/auth/accounts-changed', { method: 'POST' })).body, 'center:/internal/auth/accounts-changed');
  // 静态文件照旧公开（审核预览页、飞书侧边栏要用）。
  assert.equal((await get('/js/a.js', { forged: false })).body, 'js');
  assert.equal((await get('/distribute.html', { forged: false })).body, 'DIST');
});
