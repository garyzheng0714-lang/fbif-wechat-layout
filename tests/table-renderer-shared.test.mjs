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
  assert.match(shared, /col width="\$\{cp\.ints\[i\]\}%"/);
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

test('outerBorder:网站表格外壳带显式边框——CMS 编辑器对无 border 属性表格加虚线辅助线且发布会带出', () => {
  const elem = { rows: [[{ runs: [{ type: 'txt', text: 'A' }] }]] };
  const opts = {
    headerBg: '#01396a', headerColor: '#ffffff', borderColor: '#e5e7eb',
    fontSize: 15, padding: 8, renderers: { text: r => r.text || '' },
  };

  const siteHtml = renderTable(elem, { ...opts, outerBorder: true });
  assert.match(siteHtml, /^<table border="1" /, 'border 属性是编辑器"有无边框表格"的判定依据,缺了就画虚线');
  assert.match(siteHtml, /<table[^>]*style="[^"]*border: 1px solid #e5e7eb;/, '外壳内联同色边框,collapse 合并后视觉零变化');

  const mpHtml = renderTable(elem, opts);
  assert.equal(mpHtml.startsWith('<table style="border-collapse: collapse; width: 100%; table-layout: fixed;">'), true,
    '不开 outerBorder(公众号路径)输出字节与改动前一致——红线#19 公众号侧不动');
});

test('outerBorder 调用面:两个 FoodTalks 网站渠道模板开启,公众号 fbif 不开启', () => {
  assert.match(site, /outerBorder: true/, '网站文章粘 FoodTalks CMS,必须带外框防虚线');
  assert.match(series, /outerBorder: true/, '系列文章同样粘 FoodTalks CMS');
  assert.doesNotMatch(fbif, /outerBorder/, '公众号粘微信编辑器,无此问题,输出保持字节不变');
});

// ── cmsSurvival:真值审计(foodtalks.cn/news/62475)证实 CMS 粘贴/编辑链路剥 td 内联样式与
// colgroup,但保留 HTML 属性(border/cellpadding/width/align/bgcolor)与内容层标签(strong、
// img style)。关键视觉必须属性+内容层双写,否则发布后表头褪色、格子不居中、列宽塌陷。──
const SURVIVAL_ELEM = {
  colWidths: [1305, 1485, 2205, 3285],
  rows: [
    [{ runs: [{ type: 'txt', text: '图片' }], header: true }, { runs: [{ type: 'txt', text: '渠道' }], header: true }, { runs: [{ type: 'txt', text: '名称' }], header: true }, { runs: [{ type: 'txt', text: '供货商' }], header: true }],
    [{ runs: [{ type: 'txt', text: '' }] }, { runs: [{ type: 'txt', text: '几多全' }] }, { runs: [{ type: 'txt', text: 'A' }] }, { runs: [{ type: 'txt', text: 'B' }] }],
  ],
};
const SURVIVAL_OPTS = {
  headerBg: '#01396a', headerColor: '#ffffff', borderColor: '#e5e7eb',
  fontSize: 15, padding: 8, renderers: { text: r => r.text || '' },
};

test('cmsSurvival:关键视觉写成 HTML 属性+内容层,td 样式被 CMS 剥掉后仍有兜底', () => {
  const html = renderTable(SURVIVAL_ELEM, { ...SURVIVAL_OPTS, outerBorder: true, cmsSurvival: true });
  assert.match(html, /<table border="1" cellpadding="8" /, 'cellpadding 属性兜底 td padding');
  assert.match(html, /<td align="center" bgcolor="#01396a" width="16%" style="[^"]*"><strong>图片<\/strong><\/td>/,
    '表头格:align/bgcolor/width 属性 + 文字内容层 strong(发布成品证实内容层能存活)');
  assert.match(html, /<td align="center" width="18%" style="[^"]*">几多全<\/td>/,
    '正文格:align+width 属性;不加 bgcolor,不包 strong');
  assert.match(html, /<td align="center" width="16%" style="[^"]*"><br><\/td>/,
    '空图片格同样带 align——发布成品里图片格正是因丢 text-align 而不居中');
});

test('cmsSurvival + bannerHeader(系列):横幅行与正文格带 valign/bgcolor 属性', () => {
  const html = renderTable({
    rows: [
      [{ runs: [{ type: 'txt', text: '信息表' }] }, { runs: [] }],
      [{ runs: [{ type: 'txt', text: 'K' }] }, { runs: [{ type: 'txt', text: 'V' }] }],
    ],
  }, { ...SURVIVAL_OPTS, headerBg: '#02396b', bannerHeader: true, verticalAlignMiddle: true, cmsSurvival: true });
  assert.match(html, /<td colspan="2" align="center" valign="middle" bgcolor="#02396b" style="[^"]*"><strong>信息表<\/strong><\/td>/);
  assert.match(html, /<td align="center" valign="middle" style="[^"]*">K<\/td>/, '正文格带 valign,无列宽时不输出 width 属性');
});

test('cmsSurvival 不开启(公众号路径)输出与开启前字节一致', () => {
  const html = renderTable(SURVIVAL_ELEM, SURVIVAL_OPTS);
  assert.equal(html.startsWith('<table style="border-collapse: collapse; width: 100%; table-layout: fixed;">'), true, 'table 标签无新增属性');
  assert.match(html, /<td style="[^"]*">图片<\/td>/, '表头格无属性、无 strong');
  assert.doesNotMatch(html, /align="center"|bgcolor|cellpadding|<strong>/, '防御层零泄漏到公众号输出');
});

test('cmsSurvival 调用面:site/series 开启,公众号 fbif 不开启', () => {
  assert.match(site, /cmsSurvival: true/);
  assert.match(series, /cmsSurvival: true/);
  assert.doesNotMatch(fbif, /cmsSurvival/, '公众号输出字节不变(红线#19)');
});
