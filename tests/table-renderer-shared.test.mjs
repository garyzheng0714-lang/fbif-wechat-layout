import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const shared = await readFile(new URL('../public/js/templates/shared/table-renderer.js', import.meta.url), 'utf8');
const fbif = await readFile(new URL('../public/js/templates/fbif.js', import.meta.url), 'utf8');
const site = await readFile(new URL('../public/js/templates/foodtalks-site.js', import.meta.url), 'utf8');
const series = await readFile(new URL('../public/js/templates/foodtalks-series.js', import.meta.url), 'utf8');

test('三套模板通过中性 shared/table-renderer 渲染表格', () => {
  assert.match(shared, /export function renderTable/);
  assert.match(shared, /export function renderTableCellRuns/);
  for (const src of [fbif, site, series]) {
    assert.match(src, /shared\/table-renderer\.js/);
    assert.match(src, /renderSharedTable/);
    assert.doesNotMatch(src, /function renderTableCellRuns/);
  }
});

test('共享表格模块仍支持系列横幅表头和 colgroup', () => {
  assert.match(shared, /bannerHeader/);
  assert.match(shared, /colgroupHTML/);
  assert.match(shared, /col width="\$\{ints\[i\]\}%"/);
});
