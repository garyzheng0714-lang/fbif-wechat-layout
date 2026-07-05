// render-service/server.mjs — 渲染服务 HTTP 壳。
// 只监听 127.0.0.1（对外由 Go API 的 /api/render 代理），双层 Bearer 校验。
// 端点：POST /render（渲染）、GET /healthz（存活，无鉴权）。
import { createServer } from 'node:http';
import { renderArticle } from './render.mjs';

const PORT = Number(process.env.RENDER_PORT || 19091);
const TOKEN = (process.env.RENDER_TOKEN || '').trim();
const MAX_BODY = 8 * 1024 * 1024; // 8MB，长文+多图 blocks 足够

function sendJSON(res, status, obj) {
  const body = JSON.stringify(obj);
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' });
  res.end(body);
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on('data', (c) => {
      size += c.length;
      if (size > MAX_BODY) {
        reject(Object.assign(new Error('请求体过大'), { status: 413 }));
        req.destroy();
        return;
      }
      chunks.push(c);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

const server = createServer(async (req, res) => {
  if (req.method === 'GET' && req.url === '/healthz') {
    return sendJSON(res, 200, { ok: true });
  }
  if (req.method !== 'POST' || req.url !== '/render') {
    return sendJSON(res, 404, { error: 'not found' });
  }
  // fail loud：缺 RENDER_TOKEN 拒答而非裸奔（同 ADMIN_PASSWORD 缺配置哲学）。
  if (!TOKEN) {
    return sendJSON(res, 503, { error: 'RENDER_TOKEN 未配置：渲染服务拒绝无鉴权提供服务' });
  }
  const auth = req.headers.authorization || '';
  if (auth !== 'Bearer ' + TOKEN) {
    return sendJSON(res, 401, { error: 'unauthorized' });
  }
  try {
    const raw = await readBody(req);
    let payload;
    try {
      payload = JSON.parse(raw);
    } catch {
      return sendJSON(res, 400, { error: '请求体不是合法 JSON' });
    }
    const packId = payload.packId || 'fbif';
    if (packId !== 'fbif') {
      return sendJSON(res, 400, { error: '暂只支持 packId=fbif，收到：' + packId });
    }
    const out = await renderArticle(payload);
    return sendJSON(res, 200, out);
  } catch (err) {
    const status = err && err.status ? err.status : 500;
    console.error('[render] 渲染失败：', err);
    return sendJSON(res, status, { error: String((err && err.message) || err) });
  }
});

server.listen(PORT, '127.0.0.1', () => {
  console.log(`[render] listening on 127.0.0.1:${PORT} (token ${TOKEN ? '已配置' : '缺失——请求将被拒'})`);
});
