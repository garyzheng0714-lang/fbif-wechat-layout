/**
 * 标点共享边界的浏览器验收（生产实现之前编写）。
 * 失效清单：拆 run 序号；变长/变短转换使样式错位；跨 run 引号/数字/URL；
 * link/img 被略过后误接文字；br 未重置行首；表格逐 run 转换；网站导入提前套公众号口径。
 * 重跑：E2E_SKIP_VISUAL=1 npx playwright test tests/e2e-punctuation-shared.spec.mjs --workers=1
 * 每例输出 JSON、实际渲染 HTML 与浏览器截图，放在 Playwright test-results/e2e 附件目录。
 */
import { test, expect } from '@playwright/test';
import fs from 'node:fs/promises';
import JSZip from 'jszip';

// render-service 的 Node bootstrap 会设置这四个 globals；退出本文件时恢复，避免污染其后浏览器用例。
const rendererGlobalDescriptors = new Map(['window', 'localStorage', 'DOMParser', 'fetch'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
test.afterAll(() => {
  for (const [key, descriptor] of rendererGlobalDescriptors) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else delete globalThis[key];
  }
});

const channels = [
  { id: 'fbif', route: '/mp/fbif', site: false },
  { id: 'foodtalks-site', route: '/ft/ft-default', site: true },
  { id: 'foodtalks-series', route: '/mp/foodtalks-series', site: false },
];
const txt = (text, extra = {}) => ({ type: 'txt', text, ...extra });
const link = text => ({ type: 'link', text, href: 'https://example.test/tea?q=1.5' });
const cases = [
  { name: '一至三位序号', runs: [txt('1. 金骏眉：花果香型。\n12. 红茶：清香型。\n123. 绿茶：清新型。')],
    expected: '1. 金骏眉：花果香型。\n12. 红茶：清香型。\n123. 绿茶：清新型。' },
  { name: '拆 run 序号与省略号', runs: [txt('3', { bold: true }), txt('. 铁观音茶：兰花香型...')],
    expected: '3. 铁观音茶：兰花香型……', site: '3. 铁观音茶：兰花香型...', bold: '3' },
  { name: '句中数字、年份与小数', runs: [txt('达到100.下一步继续；2024.这一年持续增长1.5倍。')],
    expected: '达到100。下一步继续；2024。这一年持续增长1.5倍。' },
  { name: '跨 run 引号与省略号', runs: [txt('他说"清香,', { bold: true }), txt('尾韵持久"，还说"好茶"...')],
    expected: '他说“清香，尾韵持久”，还说“好茶”……', site: '他说"清香，尾韵持久"，还说"好茶"...', bold: '他说“清香，', siteBold: '他说"清香，' },
  { name: '省略号自身跨 run', runs: [txt('中文.'), txt('..后续说明。', { bold: true })],
    expected: '中文……后续说明。', site: '中文...后续说明。', bold: '后续说明。', siteBold: '..后续说明。' },
  { name: '缩短括号空格与 run 样式', runs: [txt('3', { bold: true }), txt('. 铁观音茶 ( '), txt('兰花香型', { italic: true }), txt(' ) 香气悠长。')],
    expected: '3. 铁观音茶（兰花香型）香气悠长。', site: '3. 铁观音茶 （ 兰花香型 ） 香气悠长。', bold: '3', italic: '兰花香型' },
  { name: '英文括号与中文括号配对', runs: [txt('新品 (Tea) 上市，配方 (低糖 Tea) 保留。')],
    expected: '新品（Tea）上市，配方（低糖 Tea）保留。', site: '新品 (Tea) 上市，配方 （低糖 Tea） 保留。' },
  { name: '英文名称数字跨 run', runs: [txt('中文说明 Co.'), txt(', Ltd.', { bold: true }), txt(' 价格35,'), txt('000', { italic: true }), txt('，增长1.'), txt('5倍。')],
    expected: '中文说明 Co., Ltd. 价格35,000，增长1.5倍。', bold: ', Ltd.' },
  { name: 'URL 跨 run', runs: [txt('茶叶链接 https://example.test/'), txt('中文,a', { bold: true }), txt('?q=茶:叶'), txt('，下一步。')],
    expected: '茶叶链接 https://example.test/中文,a?q=茶:叶，下一步。', bold: '中文,a' },
  { name: '技术片段边缘空格不得被括号整理吞掉', runs: [txt('参考 https://a.test/中文（ 说明')],
    expected: '参考 https://a.test/中文（ 说明' },
  { name: '代码、HTML 标签与邮箱', runs: [txt('茶叶代码 `中文,a:b()`，标签 <span title="中文,a">，邮箱 tea.name+zh@example.test。')],
    expected: '茶叶代码 `中文,a:b()`，标签 <span title="中文,a">，邮箱 tea.name+zh@example.test。' },
  { name: 'link 的文字参与上下文', runs: [txt('达到'), link('100'), txt('.下一步持续推进。')],
    expected: '达到100。下一步持续推进。', href: 'https://example.test/tea?q=1.5' },
  { name: 'link 不得从上下文消失', runs: [txt('1'), link('茶'), txt('.叶香清新。')],
    expected: '1茶。叶香清新。', href: 'https://example.test/tea?q=1.5' },
  { name: 'link 引号参与状态但自身不改', runs: [txt('他说"'), link('好茶"'), txt('，后来"再喝"。')],
    expected: '他说“好茶"，后来“再喝”。', site: '他说"好茶"，后来"再喝"。', href: 'https://example.test/tea?q=1.5' },
  { name: '图片不得拼出虚假序号', runs: [txt('1'), { type: 'img', file: 'inline.png', src: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jN1kAAAAASUVORK5CYII=' }, txt('.金骏眉的香气说明。')],
    expected: '1。金骏眉的香气说明。' },
  { name: '网站完整省略号和破折号', runs: [txt('中文...继续说明--保持原稿。')],
    expected: '中文……继续说明——保持原稿。', site: '中文...继续说明--保持原稿。' },
  { name: '低中文占比网站仍看相邻中文', runs: [txt('This is a long English sentence with context: 中文,后续。')],
    expected: 'This is a long English sentence with context: 中文,后续。', site: 'This is a long English sentence with context： 中文，后续。' },
  { name: '表格拆 run 序号和 br', table: true,
    runs: [txt('前文达到100'), { type: 'br' }, txt('3', { bold: true }), txt('. 铁观音茶：兰花香型...')],
    expected: '前文达到100\n3. 铁观音茶：兰花香型……', site: '前文达到100\n3. 铁观音茶：兰花香型...', bold: '3' },
  { name: '网站连续标点一次到位', runs: [txt('茶叶味道如何?!')], expected: '茶叶味道如何？！' },
  { name: '网站标点簇不改变英文结句', runs: [txt('中文!! The next part.')],
    expected: '中文!! The next part.', site: '中文！！ The next part.' },
  { name: '混合括号影响邻近逗号时一次到位', runs: [txt('English,(Tea）')],
    expected: 'English,(Tea）', site: 'English，（Tea）' },
  { name: '硬段落不能拼出英文保护区', runs: [txt('中文说明A,\n\nB开始')],
    expected: '中文说明A，\n\nB开始', site: '中文说明A,\n\nB开始' },
  { name: '括号英文保护不能跨硬段落', runs: [txt('中文说明(A!\n\nB?)')],
    expected: '中文说明（A！', site: '中文说明(A!' },
  { name: '表格连续 br 是硬段落', table: true, runs: [txt('中文说明A,'), { type: 'br' }, { type: 'br' }, txt('B开始')],
    expected: '中文说明A，\n\nB开始', site: '中文说明A,\n\nB开始' },
  { name: '网站不可只改只读链接的半对括号', siteOnly: true, runs: [txt('中文('), link('茶)')],
    expected: '中文(茶)', href: 'https://example.test/tea?q=1.5' },
  { name: '生成提示声明的 rest_runs 同样转换', siteOnly: true, runs: [txt('正文介绍茶叶风味。')],
    config: { modules: { bottom_blocks: { enabled: false }, fbif_footer: { enabled: true, active_segments: { tip: true } } }, behavior: { templates: { site_tip_suffix: '新品,茶叶' } } },
    expected: '新品，茶叶' },
];

async function openEditor(page, channel) {
  const response = await page.request.post('/auth/password/login', {
    data: { username: 'e2e', password: 'e2e-local-session-password', remember: false },
  });
  expect(response.ok()).toBeTruthy();
  await page.goto(channel.route, { waitUntil: 'networkidle' });
  await expect(page.locator('.app-layout')).toBeVisible();
  await page.waitForFunction(() => typeof window.fetchArticle === 'function');
}

async function attachResult(page, testInfo, name, data, html = null) {
  // 预览淡入由 GSAP 驱动，截图的 animations:'disabled' 只覆盖 CSS/Web Animations。
  const preview = page.locator('#previewContent');
  if (await preview.count()) await expect(preview).toHaveCSS('opacity', '1');
  const jsonPath = testInfo.outputPath(`${name}.json`);
  const htmlPath = testInfo.outputPath(`${name}.html`);
  const shotPath = testInfo.outputPath(`${name}.png`);
  await fs.writeFile(jsonPath, JSON.stringify(data, null, 2));
  await fs.writeFile(htmlPath, html || await page.content());
  await page.screenshot({ path: shotPath, fullPage: true, animations: 'disabled' });
  await testInfo.attach(`${name}-json`, { path: jsonPath, contentType: 'application/json' });
  await testInfo.attach(`${name}-html`, { path: htmlPath, contentType: 'text/html' });
  await testInfo.attach(`${name}-screenshot`, { path: shotPath, contentType: 'image/png' });
}

for (const channel of channels) {
  test(`${channel.id}：真实模板共享标点边界`, async ({ page }, testInfo) => {
    await openEditor(page, channel);
    const selected = cases.filter(item => !item.siteOnly || channel.site);
    const outputs = await page.evaluate(async ({ id, cases }) => {
      const template = (await import(`/js/templates/${id}.js`)).default;
      const config = { modules: { bottom_blocks: { enabled: false }, fbif_footer: { enabled: false } } };
      return cases.map(item => {
        const block = item.table
          ? { k: 'table', rows: [[{ runs: item.runs, header: false }]] }
          : { k: 'txt', runs: item.runs };
        const result = template.processBlocks({ title: '标点边界验收', blocks: [block] }, { config: item.config || config, isRepost: false });
        const html = result.lines.join('\n');
        const node = document.createElement('div');
        node.innerHTML = html;
        const bold = Array.from(node.querySelectorAll('strong, b, .wx-bold, [style*="font-weight: bold"], [style*="font-weight:bold"]')).map(el => el.textContent);
        const italic = Array.from(node.querySelectorAll('em, i, [style*="font-style: italic"]')).map(el => el.textContent);
        const hrefs = Array.from(node.querySelectorAll('a')).map(el => el.getAttribute('href'));
        node.querySelectorAll('br').forEach(br => br.replaceWith('\n'));
        return { name: item.name, text: node.textContent, html, bold, italic, hrefs };
      });
    }, { id: channel.id, cases: selected });

    const report = outputs.map((out, index) => ({ ...out, input: selected[index], expected: channel.site ? selected[index].site ?? selected[index].expected : selected[index].expected }));
    const articleCSS = await fs.readFile('public/css/wx-theme.css', 'utf8');
    const reportHtml = `<!doctype html><meta charset="utf-8"><title>标点验收</title><style>${articleCSS}</style><main>` +
      report.map(row => `<article><h2>${channel.id}：${row.name}</h2>${row.html}</article>`).join('\n') + '</main>';
    await page.setContent(reportHtml);
    await attachResult(page, testInfo, `${channel.id}-matrix`, report, reportHtml);
    for (const row of report) {
      expect.soft(row.text, row.name).toContain(row.expected);
      if (row.input.bold) expect.soft(row.bold, `${row.name} 保留加粗归属`).toContain(channel.site ? row.input.siteBold ?? row.input.bold : row.input.bold);
      if (row.input.italic && channel.site) expect.soft(row.italic).toContain(row.input.italic);
      if (row.input.href && channel.site) expect.soft(row.hrefs).toContain(row.input.href);
    }
  });
}

const importParagraphs = [
  [txt('作者：测试编辑')],
  [txt('正文介绍茶叶风味，以下记录三种标点表现。')],
  [txt('3', { bold: true }), txt('. 铁观音茶：兰花香型...')],
  [txt('他说"清香,', { bold: true }), txt('尾韵持久"，还说"好茶"...')],
  [txt('新品 (Tea) 上市，配方 (低糖 Tea) 保留。')],
  [txt('达到100.下一步继续；2024.这一年持续增长1.5倍。')],
  [txt('茶叶味道如何?!')],
  [txt('中文!! The next part.')],
  [txt('English,(Tea）')],
];

async function importFile(format) {
  if (format === 'md') return {
    name: '标点合并验收.md', mimeType: 'text/markdown',
    buffer: Buffer.from(importParagraphs.map(runs => runs.map(run => run.bold ? `**${run.text}**` : run.text).join('')).join('\n\n')),
  };
  const escape = text => text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const zip = new JSZip();
  zip.file('[Content_Types].xml', '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>');
  zip.file('_rels/.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>');
  zip.file('word/_rels/document.xml.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>');
  zip.file('word/document.xml', '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>' +
    importParagraphs.map(runs => '<w:p>' + runs.map(run => `<w:r>${run.bold ? '<w:rPr><w:b/></w:rPr>' : ''}<w:t xml:space="preserve">${escape(run.text)}</w:t></w:r>`).join('') + '</w:p>').join('') + '</w:body></w:document>');
  return { name: '标点合并验收.docx', mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', buffer: await zip.generateAsync({ type: 'nodebuffer' }) };
}

for (const channel of channels) {
  for (const format of ['md', 'docx']) {
    test(`${channel.id}：${format} 实际上传遵守渠道标点策略`, async ({ page }, testInfo) => {
      await openEditor(page, channel);
      const file = await importFile(format);
      await page.locator('#fileInput').setInputFiles(file);
      const content = page.locator('#contentArea');
      await expect(content).toContainText('铁观音茶', { timeout: 20000 });
      const text = await content.textContent();
      const html = await content.innerHTML();
      const report = { channel: channel.id, format, input: importParagraphs, text, html };
      await attachResult(page, testInfo, `${channel.id}-${format}-import`, report);
      await testInfo.attach(`输入-${format}`, { body: file.buffer, contentType: file.mimeType });
      expect.soft(text).toContain(channel.site ? '3. 铁观音茶：兰花香型...' : '3. 铁观音茶：兰花香型……');
      expect.soft(text).toContain(channel.site ? '他说"清香，尾韵持久"，还说"好茶"...' : '他说“清香，尾韵持久”，还说“好茶”……');
      expect.soft(text).toContain(channel.site ? '新品 (Tea) 上市，配方 （低糖 Tea） 保留。' : '新品（Tea）上市，配方（低糖 Tea）保留。');
      expect.soft(text).toContain('达到100。下一步继续；2024。这一年持续增长1.5倍。');
      if (channel.site) {
        expect.soft(text).toContain('茶叶味道如何？！');
        expect.soft(text).toContain('中文！！ The next part.');
        expect.soft(text).toContain('English，（Tea）');
      }
    });
  }
}

for (const channel of channels) {
  test(`${channel.id}：正文输入遵守渠道标点策略`, async ({ page }, testInfo) => {
    await openEditor(page, channel);
    const input = importParagraphs.map(runs => runs.map(run => run.text).join('')).join('\n\n');
    await page.locator('#urlInput').fill(input);
    await page.locator('#urlInput').press('ControlOrMeta+Enter');
    const content = page.locator('#contentArea');
    await expect(content).toContainText('铁观音茶', { timeout: 20000 });
    const text = await content.textContent();
    await attachResult(page, testInfo, `${channel.id}-text-import`, { channel: channel.id, input, text, html: await content.innerHTML() });
    expect.soft(text).toContain(channel.site ? '3. 铁观音茶：兰花香型...' : '3. 铁观音茶：兰花香型……');
    expect.soft(text).toContain(channel.site ? '他说"清香，尾韵持久"，还说"好茶"...' : '他说“清香，尾韵持久”，还说“好茶”……');
    expect.soft(text).toContain(channel.site ? '新品 (Tea) 上市，配方 （低糖 Tea） 保留。' : '新品（Tea）上市，配方（低糖 Tea）保留。');
    if (channel.site) {
      expect.soft(text).toContain('茶叶味道如何？！');
      expect.soft(text).toContain('中文！！ The next part.');
      expect.soft(text).toContain('English，（Tea）');
    }
  });
}

for (const channel of [channels[0], channels[1]]) {
  for (const ruleSource of ['snapshot', 'fallback']) {
    test(`${channel.id}：自动发布 ${ruleSource} 快照与浏览器正文一致`, async ({ page }, testInfo) => {
      await openEditor(page, channel);
      // exact 模式直接使用本地快照，不访问规则服务或外部平台。
      const renderer = await import('../render-service/render.mjs');
      const packId = channel.site ? 'ft-default' : 'fbif';
      const fixture = JSON.parse(await fs.readFile(`tests/fixtures/render-service-rules-${packId}.json`, 'utf8'));
      const rule = ruleSource === 'snapshot'
        ? { config: fixture.preset.config, behavior: fixture.preset.behavior }
        : renderer.fallbackRuleForPack(packId);
      const report = [];
      for (const item of cases.filter(row => !row.siteOnly || channel.site)) {
        const blocks = item.table ? [{ k: 'table', rows: [[{ runs: item.runs, header: false }]] }] : [{ k: 'txt', runs: item.runs }];
        const config = item.config || { ...rule.config, behavior: rule.behavior };
        const behavior = config.behavior || rule.behavior || {};
        const snapshot = { account_id: packId, preset_id: `${packId}-e2e`, revision: 1, config, behavior,
          config_hash: renderer.renderRuleConfigHash(config, behavior) };
        const machine = await renderer.renderArticle({ packId, title: '标点边界验收', blocks, isRepost: false,
          withFooter: !!item.config, ruleMode: 'exact', ruleSnapshot: snapshot });
        const row = await page.evaluate(async ({ id, config, behavior, blocks, machineHTML, name }) => {
          const template = (await import(`/js/templates/${id}.js`)).default;
          const { applyActiveRuleConfig } = await import('/js/rule-presets.js');
          const flat = applyActiveRuleConfig(config, { id: 'punctuation-e2e', config, raw_config: config, behavior });
          const browser = template.processBlocks({ title: '标点边界验收', blocks }, { config: { ...flat, behavior }, isRepost: false });
          const browserHTML = browser.lines.join('\n');
          const visible = html => {
            const node = document.createElement('div');
            node.innerHTML = html;
            node.querySelectorAll('br').forEach(br => br.replaceWith('\n'));
            return node.textContent.replace(/\s+/g, ' ').trim();
          };
          return { name, machineHTML, browserHTML, machineText: visible(machineHTML), browserText: visible(browserHTML) };
        }, { id: channel.id, config, behavior, blocks, machineHTML: machine.html, name: item.name });
        row.expected = (channel.site ? item.site ?? item.expected : item.expected).replace(/\s+/g, ' ').trim();
        report.push(row);
      }
      const html = '<!doctype html><meta charset="utf-8"><title>自动发布与浏览器正文比对</title>' + report.map(row => `<article><h2>${row.name}</h2><h3>自动发布</h3>${row.machineHTML}<h3>浏览器</h3>${row.browserHTML}</article>`).join('\n');
      await page.setContent(html);
      await attachResult(page, testInfo, `${channel.id}-render-service-${ruleSource}`, report, html);
      for (const row of report) {
        expect.soft(row.machineText, row.name).toBe(row.browserText);
        expect.soft(row.machineText, row.name).toContain(row.expected);
      }
    });
  }
}
