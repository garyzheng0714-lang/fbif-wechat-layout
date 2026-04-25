import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('copy button does not auto-open WeChat backend', async () => {
  const html = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const claudeGuide = await readFile(new URL('../CLAUDE.md', import.meta.url), 'utf8');
  assert.doesNotMatch(html, /window\.open\s*\(/);
  assert.doesNotMatch(html, /正在打开微信后台/);
  assert.doesNotMatch(claudeGuide, /复制后自动打开微信后台/);
  assert.doesNotMatch(claudeGuide, /window\.open\s*\(/);
  assert.doesNotMatch(html, /仅当 DOCX 图片/);
  assert.doesNotMatch(html, /服务端缓存地址/);
  assert.match(html, /loadSkipUploadPref/);
  assert.match(html, /skip_upload_restored_v3/);
  assert.match(html, /localStorage\.setItem\('skip_upload', 'true'\)/);
});

test('copy keeps legacy DOCX skip-upload behavior', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  assert.doesNotMatch(engine, /hasDeferredCopyImages/);
  assert.match(engine, /const skipUploadForThisFile = !!window\._skipUpload && _sourceIsDocx;/);
  assert.match(engine, /const skipUpload = !!window\._skipUpload && _sourceIsDocx;/);
  assert.match(engine, /return ok;/);
});
