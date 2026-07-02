import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const shared = await readFile(new URL('../public/js/templates/shared/table-renderer.js', import.meta.url), 'utf8');
const fbif = await readFile(new URL('../public/js/templates/fbif.js', import.meta.url), 'utf8');
const site = await readFile(new URL('../public/js/templates/foodtalks-site.js', import.meta.url), 'utf8');
const series = await readFile(new URL('../public/js/templates/foodtalks-series.js', import.meta.url), 'utf8');
const { renderTable } = await import('../public/js/templates/shared/table-renderer.js?test=' + Date.now());

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

test('共享表格默认按 DOCX colWidths 输出 colgroup（底层能力不是渠道 opt-in）', () => {
  const html = renderTable({
    colWidths: [1305, 1485, 2205, 3285],
    rows: [
      [{ runs: [{ type: 'txt', text: '图片' }] }, { runs: [{ type: 'txt', text: '渠道' }] }, { runs: [{ type: 'txt', text: '名称' }] }, { runs: [{ type: 'txt', text: '供货商' }] }],
      [{ runs: [{ type: 'txt', text: '' }] }, { runs: [{ type: 'txt', text: '几多全' }] }, { runs: [{ type: 'txt', text: '咸蛋黄香脆鱼皮110g' }] }, { runs: [{ type: 'txt', text: '安徽灵猫食品有限公司' }] }],
    ],
  }, {
    headerBg: '#01396a',
    headerColor: '#ffffff',
    borderColor: '#e5e7eb',
    fontSize: 15,
    padding: 8,
    renderers: { text: r => r.text || '' },
  });

  assert.match(html, /<colgroup>/, '有效 DOCX 列宽默认进入共享表格 HTML');
  assert.match(html, /<col width="16%" style="width: 15\.76%;"/, '第一列按 Word gridCol 比例，而非四列等宽');
  assert.match(html, /<col width="39%" style="width: 39\.67%;"/, '末列吸收整数舍入误差，合计 100');
  assert.doesNotMatch(html, /<col width="25%"/, '四列表格不得退化成等宽 25%');
});

test('共享表格只在有效列宽存在且未显式关闭时输出 colgroup', () => {
  const elem = {
    colWidths: [1305, 1485],
    rows: [[{ runs: [{ type: 'txt', text: 'A' }] }, { runs: [{ type: 'txt', text: 'B' }] }]],
  };
  const opts = {
    headerBg: '#01396a',
    headerColor: '#ffffff',
    borderColor: '#e5e7eb',
    fontSize: 15,
    padding: 8,
    renderers: { text: r => r.text || '' },
  };

  assert.doesNotMatch(renderTable({ ...elem, colWidths: [] }, opts), /<colgroup>/, '没有列宽时保持原行为');
  assert.doesNotMatch(renderTable({ ...elem, colWidths: [1305] }, opts), /<colgroup>/, '列宽数量不匹配时不猜测');
  assert.doesNotMatch(renderTable({ ...elem, colWidths: [1305, 0, 1485] }, opts), /<colgroup>/, '含无效列宽时不靠过滤后长度误判');
  assert.doesNotMatch(renderTable(elem, { ...opts, useColgroup: false }), /<colgroup>/, '特殊场景仍可显式关闭');
});
