import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('copy button does not auto-open WeChat backend', async () => {
  const html = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const rulePresets = await readFile(new URL('../public/js/rule-presets.js', import.meta.url), 'utf8');
  const claudeGuide = await readFile(new URL('../CLAUDE.md', import.meta.url), 'utf8');
  assert.doesNotMatch(html, /window\.open\s*\(/);
  assert.doesNotMatch(html, /正在打开微信后台/);
  assert.doesNotMatch(claudeGuide, /复制后自动打开微信后台/);
  assert.doesNotMatch(claudeGuide, /window\.open\s*\(/);
  assert.doesNotMatch(html, /直接把图片引用写入 HTML/);
  assert.doesNotMatch(html, /仅当 DOCX 图片/);
  assert.doesNotMatch(html, /服务端缓存地址/);
  assert.match(rulePresets, /loadLocalRuleConfig/);
  assert.match(rulePresets, /skip_upload_restored_v3/);
  assert.match(rulePresets, /window\.localStorage\.setItem\(LEGACY_SKIP_UPLOAD_KEY, 'true'\)/);
});

test('copy keeps legacy DOCX skip-upload behavior', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  assert.doesNotMatch(engine, /hasDeferredCopyImages/);
  assert.match(engine, /materializeLazyImages/);
  assert.match(engine, /const skipUploadForThisFile = !!window\._skipUpload && _sourceIsDocx;/);
  assert.match(engine, /return ok;/);
});

test('copy click does not wait on background image or more-articles preparation', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyFn = engine.slice(engine.indexOf('window.copyContent = async function()'));
  assert.doesNotMatch(copyFn, /await _uploadPromise/);
  assert.doesNotMatch(copyFn, /await ensureCompositeReady/);
  assert.doesNotMatch(copyFn, /上传更多文章/);
  assert.match(copyFn, /prepareMoreArticlesForCopy\('copy-click'\)/);
});

test('website copy blocks when WeChat image CDN URLs still remain', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyFn = engine.slice(engine.indexOf('window.copyContent = async function()'));
  assert.match(engine, /findWechatImageSrcs/);
  assert.match(engine, /_copyRequiresWechatImageTransfer/);
  assert.match(copyFn, /仍有微信图床图片未转存，已阻止复制/);
  assert.doesNotMatch(copyFn, /本次复制使用当前可粘贴内容/);
});

test('copy keeps the final WeChat cursor paragraph zero-height', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const copyFn = engine.slice(engine.indexOf('window.copyContent = async function()'));
  assert.ok(copyFn.includes('html += \'<p style="margin:0;padding:0;line-height:0;font-size:0;min-height:0;height:0;"><br/></p>\';'));
});

test('footer original and repost notices come from behavior rules', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  assert.match(engine, /getActiveRuleBehavior/);
  assert.match(engine, /footer_original_notice/);
  assert.match(engine, /footer_repost_notice/);
  assert.match(engine, /renderBehaviorTemplate/);
  assert.doesNotMatch(engine, /const after = '\* 本文为转载，不代表FBIF立场。'/);
});

test('deploy writes rule admin password to production env', async () => {
  const workflow = await readFile(new URL('../.github/workflows/deploy.yml', import.meta.url), 'utf8');
  assert.match(workflow, /ADMIN_PASSWORD:\s*\$\{\{ secrets\.ADMIN_PASSWORD \}\}/);
  assert.match(workflow, /ADMIN_PASSWORD GitHub secret is required/);
  assert.match(workflow, /ADMIN_PASSWORD=%s\\n/);
});
