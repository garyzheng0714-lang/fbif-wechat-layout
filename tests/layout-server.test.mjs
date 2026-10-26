// 排版进程的 HTTP 壳（render-service/layout-server.mjs）：鉴权、错误码、体积上限，以及真起一个进程排一篇。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const PARITY = path.join(ROOT, 'tests/fixtures/layout-parity');
const { createLayoutHandler, rulesFetcher } = await import(path.join(ROOT, 'render-service/layout-server.mjs'));
const { LayoutError } = await import(path.join(ROOT, 'render-service/layout.mjs'));

async function listen(handler) {
  const server = createServer(handler);
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  return { server, base: `http://127.0.0.1:${server.address().port}` };
}

async function freePort() {
  const { server } = await listen(() => {});
  const { port } = server.address();
  await new Promise(r => server.close(r));
  return port;
}

function post(base, body, token = 'tok') {
  return fetch(base + '/layout', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) },
    body: typeof body === 'string' ? body : JSON.stringify(body),
  });
}

test('排版进程：鉴权、错误码、体积上限都按约定回', async (t) => {
  const layout = async (request) => {
    if (request.account === 'boom') throw new Error('炸了');
    if (request.account === 'nope') throw new LayoutError('unknown_account', '没有这个账号', 400);
    return { html: '<p>ok</p>', account: request.account };
  };
  const { server, base } = await listen(createLayoutHandler({ layout, token: 'tok', configSchemaVersion: '1', build: 'b1', limit: 1024 }));
  t.after(() => server.close());

  assert.deepEqual(await (await fetch(base + '/healthz')).json(), { ok: true, build: 'b1', config_schema_version: '1' });
  assert.equal((await post(base, { account: 'fbif' }, '')).status, 401);
  assert.equal((await post(base, { account: 'fbif' }, 'wrong')).status, 401);
  assert.equal((await fetch(base + '/render', { method: 'POST' })).status, 404);

  const ok = await post(base, { account: 'fbif' });
  assert.equal(ok.status, 200);
  assert.deepEqual(await ok.json(), { html: '<p>ok</p>', account: 'fbif' });

  const bad = await post(base, '{not json');
  assert.equal(bad.status, 400);
  assert.equal((await bad.json()).code, 'bad_request');

  const known = await post(base, { account: 'nope' });
  assert.equal(known.status, 400);
  assert.deepEqual(await known.json(), { code: 'unknown_account', error: '没有这个账号' });

  const crash = await post(base, { account: 'boom' });
  assert.equal(crash.status, 500);
  assert.equal((await crash.json()).code, 'render_failed');

  const big = await post(base, { account: 'fbif', pad: 'x'.repeat(4096) }).catch(err => err);
  if (big instanceof Error) assert.match(String(big.cause || big), /socket|reset|closed|EPIPE/i);
  else assert.equal(big.status, 413);
});

test('排版进程：没配 RENDER_TOKEN 一律拒答', async (t) => {
  const { server, base } = await listen(createLayoutHandler({ layout: async () => ({}), token: '', configSchemaVersion: '1', build: 'b' }));
  t.after(() => server.close());
  const res = await post(base, { account: 'fbif' });
  assert.equal(res.status, 503);
  assert.equal((await res.json()).code, 'render_unconfigured');
});

test('排版进程：规则每篇现取，规则接口不通就报错', async () => {
  const calls = [];
  const fetchRules = rulesFetcher('http://rules.local/', async (url) => {
    calls.push(url);
    return url.includes('wowfood') ? new Response('{"preset":{}}') : new Response('', { status: 500 });
  });
  assert.equal(await fetchRules('wowfood'), '{"preset":{}}');
  await assert.rejects(fetchRules('fbif'), /HTTP 500/);
  assert.deepEqual(calls, ['http://rules.local/api/rules/active?account=wowfood', 'http://rules.local/api/rules/active?account=fbif']);
});

test('排版进程：真起一个进程，排出来与网页逐字一样', async (t) => {
  const { server: rules, base: rulesOrigin } = await listen(async (req, res) => {
    const account = new URL(req.url, 'http://x').searchParams.get('account');
    try {
      const body = await readFile(path.join(PARITY, `rules--${account}.json`));
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(body);
    } catch {
      res.writeHead(404);
      res.end();
    }
  });
  t.after(() => rules.close());

  const port = await freePort();
  const child = spawn(process.execPath, [path.join(ROOT, 'render-service/layout-server.mjs')], {
    env: {
      PATH: process.env.PATH,
      LAYOUT_PORT: String(port),
      RENDER_TOKEN: 'tok',
      RULES_API_ORIGIN: rulesOrigin,
      ASSET_ORIGIN: 'http://127.0.0.1:19191', // 样稿在这个地址抓的
      PUBLIC_DIR: path.join(ROOT, 'public'),
      RENDER_BUILD_SHA: 'test-build',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  t.after(() => child.kill());
  let log = '';
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('排版进程没起来：' + log)), 20000);
    const onData = (chunk) => {
      log += chunk;
      if (log.includes('listening')) { clearTimeout(timer); resolve(); }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', onData);
    child.on('exit', code => { clearTimeout(timer); reject(new Error(`排版进程退出 ${code}：${log}`)); });
  });

  const base = `http://127.0.0.1:${port}`;
  assert.equal((await (await fetch(base + '/healthz')).json()).build, 'test-build');
  const text = '雀巢宣布收购某植物基品牌\n交易金额未披露，预计明年完成交割。\n双方表示将继续保留原品牌团队。';
  for (const account of ['fbif', 'ft-default']) {
    const res = await post(base, { account, title: '', repost: null, input: { kind: 'text', text } });
    assert.equal(res.status, 200, await res.clone().text());
    const out = await res.json();
    const want = await readFile(path.join(PARITY, `${account}--text.html`), 'utf8');
    assert.equal(out.html.replace(/(<img\b[^>]*?\ssrc=")[^"]*(")/gi, '$1IMG$2'), want);
    assert.equal(out.build, 'test-build');
  }
  const missing = await post(base, { account: 'nope', input: { kind: 'text', text } });
  assert.equal(missing.status, 400);
  assert.equal((await missing.json()).code, 'unknown_account');
});
