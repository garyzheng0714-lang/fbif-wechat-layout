// render-service/layout-server.mjs — 排版接口的 Node 排版进程（HTTP 壳）。
// 只监听 127.0.0.1，只给本机 Go 调（POST /api/v1/layout 的排版一步，见 api/layout_api.go）；Bearer RENDER_TOKEN。
// 端点：POST /layout（排一篇，载体与回包见 layout.mjs）、GET /healthz（存活，无鉴权）。
// 与 server.mjs（/render，Feed 在用）是两个进程，互不影响；排队只在 Go 那边，这里一次一篇。
import { createServer } from 'node:http';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { loadLayoutEngine, rendererBuild, LayoutError, DEFAULT_PUBLIC_DIR } from './layout.mjs';

const EXPECTED_CONFIG_SCHEMA_VERSION = '1';
export const MAX_LAYOUT_BODY = 24 * 1024 * 1024; // Go 收的 Word 拆掉图片后 ≤16MB，base64 后再留余量

function sendJSON(res, status, obj) {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' });
  res.end(JSON.stringify(obj));
}

function readBody(req, limit) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on('data', (c) => {
      size += c.length;
      if (size > limit) {
        reject(new LayoutError('too_large', '请求体超过 ' + Math.round(limit / 1024 / 1024) + 'MB', 413));
        req.destroy();
        return;
      }
      chunks.push(c);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

// 拆出来方便测：layout 是 loadLayoutEngine 给的 layout(request)。
export function createLayoutHandler({ layout, token, configSchemaVersion, build, limit = MAX_LAYOUT_BODY }) {
  return async function handle(req, res) {
    if (req.method === 'GET' && req.url === '/healthz') {
      return sendJSON(res, 200, { ok: true, build, config_schema_version: configSchemaVersion });
    }
    if (req.method !== 'POST' || req.url !== '/layout') {
      return sendJSON(res, 404, { code: 'not_found', error: 'not found' });
    }
    // 缺 RENDER_TOKEN 拒答，不裸奔。
    if (!token) return sendJSON(res, 503, { code: 'render_unconfigured', error: 'RENDER_TOKEN 未配置' });
    if ((req.headers.authorization || '') !== 'Bearer ' + token) {
      return sendJSON(res, 401, { code: 'unauthorized', error: 'unauthorized' });
    }
    let request;
    try {
      request = JSON.parse(await readBody(req, limit));
    } catch (err) {
      if (err instanceof LayoutError) return sendJSON(res, err.status, { code: err.code, error: err.message });
      return sendJSON(res, 400, { code: 'bad_request', error: '请求体不是合法 JSON' });
    }
    try {
      return sendJSON(res, 200, await layout(request));
    } catch (err) {
      if (err instanceof LayoutError) return sendJSON(res, err.status, { code: err.code, error: err.message });
      console.error('[layout] 排版失败：', err);
      return sendJSON(res, 500, { code: 'render_failed', error: String((err && err.message) || err) });
    }
  };
}

// 规则每篇现取（运营激活新规则后下一篇立刻生效），拿不到就抛错，layout.mjs 会回 rules_unavailable。
export function rulesFetcher(rulesOrigin, realFetch = globalThis.fetch) {
  const origin = String(rulesOrigin).replace(/\/$/, '');
  return async function fetchRules(account) {
    const resp = await realFetch(origin + '/api/rules/active?account=' + encodeURIComponent(account), {
      cache: 'no-store',
      headers: { 'Cache-Control': 'no-cache' },
      signal: AbortSignal.timeout(5000),
    });
    if (!resp.ok) throw new Error('规则接口 HTTP ' + resp.status);
    return resp.text();
  };
}

async function main() {
  const env = process.env;
  const configSchemaVersion = String(
    env.FBIF_CONFIG_SCHEMA_VERSION || (env.NODE_ENV === 'production' ? '' : EXPECTED_CONFIG_SCHEMA_VERSION),
  ).trim();
  if (configSchemaVersion !== EXPECTED_CONFIG_SCHEMA_VERSION) {
    throw new Error(`FBIF_CONFIG_SCHEMA_VERSION=${JSON.stringify(configSchemaVersion)}; expected ${EXPECTED_CONFIG_SCHEMA_VERSION}`);
  }
  const port = Number(env.LAYOUT_PORT || 19098);
  const token = String(env.RENDER_TOKEN || '').trim();
  const publicDir = env.PUBLIC_DIR || DEFAULT_PUBLIC_DIR;
  const build = await rendererBuild(publicDir);
  // 必须先取走真 fetch：loadLayoutEngine 会把全局 fetch 换成只认站内地址的假 fetch。
  const fetchRules = rulesFetcher(env.RULES_API_ORIGIN || 'http://127.0.0.1:9000', globalThis.fetch.bind(globalThis));
  const engine = await loadLayoutEngine({
    publicDir,
    fetchRules,
    origin: (env.ASSET_ORIGIN || 'https://fbifmp-layout.foodtalks.cn').replace(/\/$/, ''),
    build,
  });
  const server = createServer(createLayoutHandler({ layout: engine.layout, token, configSchemaVersion, build }));
  server.requestTimeout = 0; // 长稿可能排一两分钟，超时由 Go 那边管
  server.listen(port, '127.0.0.1', () => {
    console.log(`[layout] listening on 127.0.0.1:${port} build=${build} (token ${token ? '已配置' : '缺失——请求将被拒'})`);
  });
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((err) => {
    console.error('[layout] 启动失败：', err);
    process.exit(1);
  });
}
