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

test('deploy overrides existing Caddy no-cache headers for cacheable assets', async () => {
  const workflow = await readFile(new URL('../.github/workflows/deploy.yml', import.meta.url), 'utf8');

  assert.ok(workflow.includes('header @fbifVersionedAssets >Cache-Control \\"public, max-age=31536000, immutable\\"'));
  assert.ok(workflow.includes('header @fbifVendorAssets >Cache-Control \\"public, max-age=31536000, immutable\\"'));
  assert.ok(workflow.includes('header @fbifImageAssets >Cache-Control \\"public, max-age=86400\\"'));
});
