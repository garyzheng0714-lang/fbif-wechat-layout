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

// WHY：编辑器 HTML 用 ?v=assetVersion 给每个 JS import 打版本，部署后 JS 必然刷新；但 <head>
// 里的 <link rel=stylesheet> 若是裸 URL（无 ?v=），CSS 会被浏览器/代理按旧缓存直接复用而卡在
// 旧版本——曾导致「导入区显示错乱」（旧 app-shell.css 被缓存）。Caddy 的 @fbifVersionedAssets
// 匹配 `path /js/* /css/*` + `query v=*`，本就为「带版本的 CSS」准备了不可变长缓存，只是 <link>
// 从未带上 ?v=。这条测试锁死：凡是给 JS 打版本的编辑器 HTML，必须同样给 CSS <link> 打版本，
// 否则 CSS 会重蹈陈旧缓存的覆辙。
for (const page of ['app.html', 'site.html']) {
  test(`${page} cache-busts its stylesheet <link> tags with the build version`, async () => {
    const html = await readFile(new URL(`../public/${page}`, import.meta.url), 'utf8');
    // 前提：该页确实用 assetVersion 给 JS import 打了版本（否则本约束不适用）。
    assert.match(html, /import\([^)]*\?v=['"]?\s*\+\s*assetVersion/, `${page} 应以 assetVersion 给 JS 打版本`);
    // 必须存在「给同源 stylesheet <link> 追加 ?v=assetVersion」的逻辑。
    assert.match(
      html,
      /querySelectorAll\(['"]link\[rel="stylesheet"\]['"]\)/,
      `${page} 必须遍历 stylesheet <link> 以打版本`,
    );
    assert.match(
      html,
      /setAttribute\(['"]href['"],\s*href \+ ['"]\?v=['"] \+ assetVersion\)/,
      `${page} 必须给 stylesheet <link> 追加 ?v=assetVersion（否则 CSS 会卡旧缓存）`,
    );
  });
}
