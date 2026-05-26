import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

// 该模板用 dynamic import + engine.js 依赖，无法在 Node 里完整加载。
// 第一版用源码 grep 校验关键输出形态契约；M2 之后用 jsdom 跑真实渲染。

let templateSource;
test.before(async () => {
  templateSource = await readFile(
    new URL('../public/js/templates/foodtalks-site.js', import.meta.url),
    'utf8'
  );
});

test('foodtalks-site template declares correct id and formats', () => {
  assert.match(templateSource, /export default \{/);
  assert.match(templateSource, /id:\s*['"]foodtalks-site['"]/);
  assert.match(templateSource, /processDocx\s*\(/);
  assert.match(templateSource, /processMd\s*\(/);
  assert.match(templateSource, /processBlocks\s*\(/);
});

test('foodtalks-site template outputs h2/h3 without inline style', () => {
  assert.match(templateSource, /<h2>\$\{[^}]+\}<\/h2>/);
  assert.match(templateSource, /<h3>\$\{[^}]+\}<\/h3>/);
});

test('foodtalks-site template uses explicit <p><br /></p> as blank line', () => {
  // 标准 XHTML 自闭合形式：<p><br /></p>
  assert.match(templateSource, /BLANK_LINE\s*=\s*['"]<p><br \/><\/p>['"]/);
});

test('foodtalks-site template renders images at 600px centered', () => {
  assert.match(templateSource, /text-align:\s*center/);
  assert.match(templateSource, /width:\s*600px/);
  assert.match(templateSource, /max-width:\s*100%/);
});

test('foodtalks-site template renders image captions with 12px gray inline style', () => {
  assert.match(templateSource, /font-size:\s*12px;\s*color:\s*#7f7f7f/);
});

test('foodtalks-site template renders references with 16px gray inline style', () => {
  assert.match(templateSource, /font-size:\s*16px;\s*color:\s*#7f7f7f/);
});

test('foodtalks-site template auto-numbers references with [1] [2] [3]', () => {
  assert.match(templateSource, /ensureReferenceNumber/);
});

test('foodtalks-site template has adaptiveImageWidth with 280/360/480/600 thresholds', () => {
  assert.match(templateSource, /adaptiveImageWidth/);
  assert.match(templateSource, /\b280\b/);
  assert.match(templateSource, /\b360\b/);
  assert.match(templateSource, /\b480\b/);
});

test('foodtalks-site template uses aspect ratio thresholds 3 / 2.2 / 1.6', () => {
  assert.match(templateSource, /ratio\s*>=\s*3/);
  assert.match(templateSource, /ratio\s*>=\s*2\.2/);
  assert.match(templateSource, /ratio\s*>=\s*1\.6/);
});
