// 端到端测试的「登录」：生产里入口（Caddy 问 FBIF 登录中心）把这是谁写进 X-FBIF-User 交给 Go；
// 测试直接起 Go、不经入口，所以给浏览器上下文的每个请求都带上这个头，当成 account:<username> 的账号密码登录。
// 角色照旧住在用户库（第一次带头的请求建记录）。
const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;

export function fbifUserHeader({ username, name = username }) {
  const user = { id: `account:${username}`, name, company_code: 'account', apps: ['layout'] };
  return Buffer.from(JSON.stringify(user)).toString('base64url');
}

export async function loginAs(context, { username, name } = {}) {
  await context.setExtraHTTPHeaders({ 'X-FBIF-User': fbifUserHeader({ username, name }) });
  const response = await context.request.get(`${BASE}/api/auth/me`);
  if (!response.ok()) throw new Error(`E2E 登录失败（${response.status()}）：${await response.text()}`);
}
