import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('homepage defers the more-articles editor until an article exists', async () => {
  const html = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');

  assert.match(html, /async function ensureMoreArticlesEditor\(\)/);
  assert.match(html, /fbif:article-previewed['"], activateArticleSidebar/);
  assert.match(html, /fbif:article-restored['"], activateArticleSidebar/);
  assert.match(html, /articleEditorActivated/);
  assert.doesNotMatch(
    html,
    /const \{ initMoreArticlesEditor \} = await import\([\s\S]*?initMoreArticlesEditor\(\{ openCropEditor \}\);/,
    '空首页不应启动更多文章编辑器，否则默认卡片图片会拖慢首屏',
  );
});

// WHY：带版本 JS/CSS、vendor、图片必须有长缓存覆盖头（>Cache-Control 强制覆盖站点级
// no-cache），否则首屏每次全量回源。S4(2026-07-10) 起 Caddy 路由的唯一真相源是仓库的
// deploy/caddy/fbifmp-layout.caddy（不再是 deploy.yml 注入脚本），断言随真相源迁移。
test('Caddy vhost overrides no-cache headers for cacheable assets', async () => {
  const vhost = await readFile(new URL('../deploy/caddy/fbifmp-layout.caddy', import.meta.url), 'utf8');

  assert.ok(vhost.includes('header @fbifVersionedAssets >Cache-Control "public, max-age=31536000, immutable"'));
  assert.ok(vhost.includes('header @fbifVendorAssets >Cache-Control "public, max-age=31536000, immutable"'));
  assert.ok(vhost.includes('header @fbifImageAssets >Cache-Control "public, max-age=86400"'));
});

// WHY：新增 Go API 不应再依赖人工白名单；/api 统一交给 Go，签名预览仍是
// 非 API 的显式路由。两者都必须在 SPA fallback 前，避免返回 index.html 假 200。
test('Caddy sends signed previews to Go without login and everything else to Go behind the login gateway', async () => {
  const vhost = await readFile(new URL('../deploy/caddy/fbifmp-layout.caddy', import.meta.url), 'utf8');
  const program = vhost.match(/@fbifProgram path (.+)\n/);
  assert.ok(program && program[1].split(/\s+/).includes('/preview/*'), '签名预览路由必须显式登记为不过登录');
  const proxyAt = vhost.indexOf('handle @fbifProgram');
  const gatewayAt = vhost.indexOf('import fbif_login 127.0.0.1:9001');
  assert.ok(proxyAt > 0 && gatewayAt > proxyAt, '预览路由必须写在入口拦截前');
  assert.match(vhost.slice(proxyAt, gatewayAt), /reverse_proxy 127\.0\.0\.1:9001/);
  assert.doesNotMatch(vhost, /^\thandle \{/m, '不再有静态兜底：/api 和页面都经入口交给 Go，未知 API 由 Go 回 JSON 404');
});

// 2026-10-28 用户：「这个网站打开会闪一下。很恶心。」逐帧录下来是三处：
// ① 运行时给 <link rel=stylesheet> 改 href 补 ?v=，浏览器先卸掉旧样式表再下新的，整页变成无样式的原始网页；
//    裸 CSS 已由 Caddy 回 no-cache（每次向服务器确认），不会卡旧缓存，所以不再改 href。
// ② 一登录就露出页面，账号、载体、复制按钮、上次的稿子再一样样冒出来；现在启动完才摘 boot-pending。
// ③ 恢复上次的稿子时放了「新文章出现」的渐入。
test('Caddy serves unversioned CSS and JS with no-cache so editors never need to rewrite their hrefs', async () => {
  const vhost = await readFile(new URL('../deploy/caddy/fbifmp-layout.caddy', import.meta.url), 'utf8');
  assert.match(vhost, /@fbifStaticCode path \*\.css \*\.js\s+header @fbifStaticCode Cache-Control "no-cache"/);
});

for (const page of ['app.html', 'site.html']) {
  test(`${page} keeps its stylesheets loaded and shows the page only once startup is done`, async () => {
    const html = await readFile(new URL(`../public/${page}`, import.meta.url), 'utf8');
    assert.doesNotMatch(
      html,
      /link\.setAttribute\(['"]href['"]/,
      `${page} 不得在运行时改样式表地址：改了就先卸掉全部样式，整页闪成原始网页`,
    );
    assert.match(html, /<body class="auth-pending boot-pending">/, `${page} 打开时先藏着，启动完再露`);
    const restoreAt = html.indexOf('await restoreCurrentChannel();');
    const revealAt = html.indexOf("document.body.classList.remove('boot-pending');");
    assert.ok(restoreAt > 0 && revealAt > restoreAt, `${page} 要等上次的稿子恢复完才露出页面`);
    assert.match(
      html,
      /stripBuildVersionFromLocation\(\)/,
      `${page} 必须清掉地址栏里的构建号，避免收藏带上 ?v=`,
    );
    assert.doesNotMatch(
      html,
      /searchParams\.set\(['"]v['"]/,
      `${page} 不得把构建号写进页面地址栏`,
    );
  });
}

test('a stuck startup still shows the editor after login, and restoring the last draft does not animate', async () => {
  const css = await readFile(new URL('../public/css/auth-editor.css', import.meta.url), 'utf8');
  assert.match(css, /body\.boot-pending > :not\(script\)/);
  assert.match(css, /body\.boot-pending:not\(\.auth-pending\) > :not\(script\) \{\s*animation: boot-pending-timeout 0s 5s forwards;/);
  assert.match(css, /@keyframes boot-pending-timeout \{\s*to \{ visibility: visible; \}/);
  const shell = await readFile(new URL('../public/js/app-shell-ui.js', import.meta.url), 'utf8');
  assert.match(shell, /classList\.contains\('boot-pending'\)/);
});
