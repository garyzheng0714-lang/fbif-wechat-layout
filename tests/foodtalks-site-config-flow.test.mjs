import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test.before(async () => {
  // 集成测试同样走源码 grep —— jsdom 不是项目依赖
});

test('engine.js: setActiveConfig exists and writes to _activeConfig', async () => {
  const src = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(src, /setActiveConfig\s*[=:(]/, 'engine.js 必须导出 setActiveConfig');
  assert.match(src, /_activeConfig/, 'engine.js 必须有 _activeConfig 字段');
});

test('engine.js: all 5 process call sites pass config', async () => {
  const src = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  const processDocxCalls = [...src.matchAll(/template\.processDocx\s*\([^)]*\)/g)];
  assert.ok(processDocxCalls.length >= 2, `processDocx 调用点至少 2 处，实际 ${processDocxCalls.length}`);
  for (const m of processDocxCalls) {
    assert.match(m[0], /config/, `processDocx 调用 "${m[0]}" 必须含 config`);
  }
  const processMdCalls = [...src.matchAll(/template\.processMd\s*\([^)]*\)/g)];
  for (const m of processMdCalls) {
    assert.match(m[0], /config/, `processMd 调用 "${m[0]}" 必须含 config`);
  }
  const processBlocksCalls = [...src.matchAll(/template\.processBlocks\s*\([^)]*\)/g)];
  for (const m of processBlocksCalls) {
    assert.match(m[0], /config/, `processBlocks 调用 "${m[0]}" 必须含 config`);
  }
});

test('fbif.js: 3 process entries accept config param', async () => {
  const src = await readFile(
    new URL('../public/js/templates/fbif.js', import.meta.url),
    'utf8'
  );
  assert.match(src, /processDocx\s*\(\s*\{[^}]*config[^}]*\}/, 'fbif processDocx 签名含 config');
  assert.match(src, /processMd\s*\([^)]*config[^)]*\)/, 'fbif processMd 签名含 config');
  assert.match(src, /processBlocks\s*\([^)]*config[^)]*\)/, 'fbif processBlocks 签名含 config');
});

test('foodtalks-site.js: 3 process entries accept config param', async () => {
  const src = await readFile(
    new URL('../public/js/templates/foodtalks-site.js', import.meta.url),
    'utf8'
  );
  // processDocx: config 在解构签名里
  assert.match(src, /processDocx\s*\(\s*\{[^}]*config[^}]*\}/, 'foodtalks-site processDocx 签名含 config');
  // processMd / processBlocks: config 通过 opts 参数传入，函数体内提取
  assert.match(src, /processMd[\s\S]{0,200}config\s*=/, 'foodtalks-site processMd 须从 opts 提取 config');
  assert.match(src, /processBlocks[\s\S]{0,200}config\s*=/, 'foodtalks-site processBlocks 须从 opts 提取 config');
});
