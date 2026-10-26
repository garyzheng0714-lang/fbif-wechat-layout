// 网页复制层金样的采集小服务（只在本机用）。
// 浏览器里开着本地排版工具，harness.js 从这里取输入文件、把网页复制出来的 HTML 存回来。
// 用法见 tools/layout-parity/README.md。
import http from 'node:http';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(HERE, '../..');
const PORT = Number(process.env.PARITY_PORT || 19192);
const OUT = path.resolve(process.env.PARITY_OUT || path.join(ROOT, 'tests/fixtures/layout-parity'));
const FIXTURES = path.join(ROOT, 'tests/fixtures');

const send = (res, status, body, type = 'text/plain; charset=utf-8') => {
  res.writeHead(status, {
    'Content-Type': type,
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
    'Access-Control-Allow-Headers': 'Content-Type',
  });
  res.end(body);
};

const safeName = name => /^[a-z0-9._-]+$/i.test(name) ? name : null;

http.createServer(async (req, res) => {
  try {
    if (req.method === 'OPTIONS') return send(res, 204, '');
    const url = new URL(req.url, 'http://x');
    const [, kind, name] = url.pathname.split('/');
    if (req.method === 'GET' && kind === 'harness.js') {
      return send(res, 200, await fs.readFile(path.join(HERE, 'harness.js')), 'text/javascript; charset=utf-8');
    }
    if (req.method === 'GET' && kind === 'fixture' && safeName(name)) {
      return send(res, 200, await fs.readFile(path.join(FIXTURES, name)), 'application/octet-stream');
    }
    if (req.method === 'POST' && kind === 'save' && safeName(name)) {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      await fs.mkdir(OUT, { recursive: true });
      await fs.writeFile(path.join(OUT, name), Buffer.concat(chunks));
      return send(res, 200, 'ok');
    }
    // 假的图片转存上游：本机排版工具的 WECHAT_UPLOAD_ENDPOINT 指到这里，网页转存微信图、
    // 上传 Word 图都回一个假的 OSS 地址（金样里图片地址统一换成 IMG，真假不影响）。
    if (req.method === 'POST' && kind === 'api' && name === 'oss-upload') {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}');
      const results = {};
      let n = 0;
      const fake = () => `https://fbif-feishu-base.oss-cn-shanghai.aliyuncs.com/layout-parity/${Date.now()}-${n++}.png`;
      for (const key of Object.keys(body.base64_images || {})) results[key] = fake();
      for (const src of body.urls || []) results[src] = fake();
      return send(res, 200, JSON.stringify({ results }), 'application/json');
    }
    send(res, 404, 'not found');
  } catch (err) {
    send(res, 500, String(err && err.message || err));
  }
}).listen(PORT, '127.0.0.1', () => console.log(`layout-parity 采集服务 http://127.0.0.1:${PORT} → ${OUT}`));
