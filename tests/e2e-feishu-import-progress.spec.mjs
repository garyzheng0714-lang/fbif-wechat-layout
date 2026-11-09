// 飞书云文档导入的浏览器级回归（2026-08-05）。
//
// 背景：8/5 中午一次「树以本分支为准」的 merge 把 7/30–7/31 的飞书快速导入体系整体
// 盖掉了——服务端异步导出作业（真实分阶段进度）、前端加权进度条、离屏 staging 整帧
// 上屏全部消失，退化成一个同步导出 + 干等的不确定动画，用户看到的是白屏、占位和
// 「已等待 N 秒」。这里守住恢复后的行为：
//   1. 走异步 job（POST /jobs → long-poll ?after=version → /result），不是同步导出；
//   2. 进度条按服务端真实阶段推进，且百分比只增不减；
//   3. 图片就绪前不展示残缺正文，就绪后正文与图片一次性出现。
//
// 飞书上游 API 由服务端调用且 base URL 硬编码，本地无凭据，因此这里打桩的是本工具
// 自己的 /api/feishu/* 响应；服务端 job 管理器本身由 api/feishu_export_jobs_test.go 覆盖。
import { test, expect } from '@playwright/test';
import JSZip from 'jszip';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const LOGIN = { username: 'e2e', name: 'E2E 测试账号' };
const DOCX_MIME = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
const IMAGE_COUNT = 24;
const ARTICLE_TEXT = 'FEISHU_ATOMIC_TEXT：飞书正文与图片必须一次出现';
const TINY_PNG_B64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+Xw4AAAAASUVORK5CYII=';

// 服务端把图片镜像到 OSS 后回写进 DOCX 的 URL 映射；这里用 data: 位图代表
// 「已镜像、无需浏览器再回源」的最终形态。
const MIRRORED = `data:image/png;base64,${TINY_PNG_B64}`;

function imageParagraph(index) {
  return `
    <w:p><w:r><w:drawing><wp:inline>
      <wp:extent cx="5486400" cy="3086100"/>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rId${index}"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>`;
}

async function buildMirroredDocx() {
  const zip = new JSZip();
  zip.file('[Content_Types].xml', `<?xml version="1.0" encoding="UTF-8"?>
    <Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
      <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
      <Default Extension="xml" ContentType="application/xml"/>
      <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
    </Types>`);
  zip.file('_rels/.rels', `<?xml version="1.0" encoding="UTF-8"?>
    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
      <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
    </Relationships>`);
  zip.file('word/_rels/document.xml.rels', `<?xml version="1.0" encoding="UTF-8"?>
    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
      ${Array.from({ length: IMAGE_COUNT }, (_, i) => (
        `<Relationship Id="rId${i + 1}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image${i + 1}.png"/>`
      )).join('')}
    </Relationships>`);
  zip.file('word/_fbif_imageUrls.json', JSON.stringify(Object.fromEntries(
    Array.from({ length: IMAGE_COUNT }, (_, i) => [`image${i + 1}.png`, MIRRORED]),
  )));
  zip.file('word/document.xml', `<?xml version="1.0" encoding="UTF-8"?>
    <w:document
      xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
      xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
      xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
      xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
      xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
      <w:body>
        <w:p><w:r><w:t>作者：E2E 编辑</w:t></w:r></w:p>
        <w:p><w:r><w:t>${ARTICLE_TEXT}</w:t></w:r></w:p>
        ${Array.from({ length: IMAGE_COUNT }, (_, i) => imageParagraph(i + 1)).join('')}
        <w:sectPr/>
      </w:body>
    </w:document>`);
  return zip.generateAsync({ type: 'nodebuffer', compression: 'DEFLATE' });
}

// 服务端真实上报的阶段序列（api/feishu_provider.go + doc_image_mirror.go）。
const SERVER_STAGES = [
  { phase: 'resolve', done: 1, total: 1 },
  { phase: 'create', done: 1, total: 1 },
  { phase: 'export_wait', done: 1, total: 1 },
  { phase: 'download', done: 500000, total: 1000000 },
  { phase: 'download', done: 1000000, total: 1000000 },
  { phase: 'optimize', done: 26, total: 26 },
  { phase: 'image_enumerate', done: IMAGE_COUNT, total: IMAGE_COUNT },
  { phase: 'image_upload', done: IMAGE_COUNT, total: IMAGE_COUNT },
  { phase: 'image_map', done: 1, total: 1 },
];

test('飞书导入走异步作业：真实分阶段进度推进，正文与图片一次性出现', async ({ browser }) => {
  const fixture = await buildMirroredDocx();
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'],
  });
  const loginResponse = await context.request.post(`${BASE}/auth/dev-login`, { data: LOGIN });
  expect(loginResponse.ok(), 'E2E 登录必须成功').toBeTruthy();
  const page = await context.newPage();

  const calls = [];
  let stageIndex = -1;

  // 让本地开发登录的账号具备飞书能力、且已授权飞书，才能走 @ 搜索入口。
  await page.route('**/api/auth/me', async route => {
    const response = await route.fetch();
    const body = await response.json().catch(() => ({}));
    const user = { ...(body.user || {}) };
    user.auth_mode = 'feishu';
    user.feishu = 'ok';
    user.capabilities = { ...(user.capabilities || {}), feishu_documents: true };
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...body, user }) });
  });

  await page.route('**/api/feishu/docs/search**', async route => {
    calls.push('search');
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ documents: [{ token: 'dox_e2e', type: 'docx', title: '飞书测试文档' }] }),
    });
  });

  // 同步导出端点必须没人再走它——走了就说明 job 体系又掉了。
  await page.route(url => /\/api\/feishu\/docs\/export$/.test(url.pathname), async route => {
    calls.push('LEGACY_SYNC_EXPORT');
    await route.fulfill({ status: 500, body: 'legacy sync export must not be used' });
  });

  await page.route('**/api/feishu/docs/export/jobs', async route => {
    calls.push('create-job');
    await route.fulfill({
      status: 202,
      contentType: 'application/json',
      body: JSON.stringify({
        job_id: 'job_e2e',
        state: 'running',
        phase: 'queued',
        done: 0,
        total: 0,
        version: 1,
        status_url: '/api/feishu/docs/export/jobs/job_e2e',
        result_url: '/api/feishu/docs/export/jobs/job_e2e/result',
      }),
    });
  });

  await page.route('**/api/feishu/docs/export/jobs/job_e2e?**', async route => {
    stageIndex += 1;
    calls.push(`status:${stageIndex}`);
    // 每次 long-poll 推进一个真实阶段，模拟服务端 version 递增。
    await new Promise(resolve => setTimeout(resolve, 120));
    if (stageIndex >= SERVER_STAGES.length) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          job_id: 'job_e2e', state: 'completed', phase: 'image_map', done: 1, total: 1,
          version: stageIndex + 2, result_url: '/api/feishu/docs/export/jobs/job_e2e/result',
        }),
      });
      return;
    }
    const stage = SERVER_STAGES[stageIndex];
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        job_id: 'job_e2e', state: 'running', ...stage, version: stageIndex + 2,
        result_url: '/api/feishu/docs/export/jobs/job_e2e/result',
      }),
    });
  });

  await page.route('**/api/feishu/docs/export/jobs/job_e2e/result', async route => {
    calls.push('result');
    await route.fulfill({
      status: 200,
      contentType: DOCX_MIME,
      headers: { 'content-disposition': 'attachment; filename="feishu.docx"' },
      body: fixture,
    });
  });

  await page.goto(`${BASE}/mp/fbif`, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => (
    typeof window._handleFile === 'function' && window._activeRuleState === 'resolved'
  ), { timeout: 15000 });

  // 记录进度条文案与百分比的真实推进序列。
  await page.evaluate(() => {
    window.__progress = [];
    const text = document.getElementById('progressText');
    const fill = document.getElementById('progressFill');
    const record = () => window.__progress.push({
      text: text.textContent,
      width: parseFloat(fill.style.width) || 0,
    });
    new MutationObserver(record).observe(text, { childList: true, characterData: true, subtree: true });
    new MutationObserver(record).observe(fill, { attributes: true, attributeFilter: ['style'] });
  });

  await page.locator('#urlInput').fill('@飞书测试文档');
  await page.locator('[data-feishu-doc-result]').first().click({ timeout: 10000 });

  // 图片就绪前不得展示残缺正文。
  await expect(page.locator('#progress'), '导入期间必须显示进度').toBeVisible();
  await expect(page.locator('#contentArea'), '图片未齐时不得提前展示残缺正文')
    .not.toContainText(ARTICLE_TEXT);

  await expect(page.locator('#contentArea'), '准备完成后正文须完整出现')
    .toContainText(ARTICLE_TEXT, { timeout: 30000 });

  const progress = await page.evaluate(() => window.__progress);
  const widths = progress.map(entry => entry.width).filter(w => w > 0);
  const texts = progress.map(entry => entry.text);

  expect(calls, '必须走异步 job，不得回退同步导出端点').not.toContain('LEGACY_SYNC_EXPORT');
  expect(calls, '必须创建导出作业').toContain('create-job');
  expect(calls, '必须取作业结果').toContain('result');
  expect(calls.filter(c => c.startsWith('status:')).length, '必须 long-poll 多个真实阶段')
    .toBeGreaterThanOrEqual(5);

  // 服务端阶段文案必须真的显示给用户，而不是一句「已等待 N 秒」。
  const joined = texts.join(' | ');
  expect(joined, '必须显示服务端真实阶段文案').toMatch(/飞书正在生成 Word|正在从飞书接收 Word|正在优化 Word 文档|正在准备正文图片/);
  expect(joined, '不得回到无信息的干等文案').not.toMatch(/已等待 \d+ 秒/);

  // 进度只增不减。
  for (let i = 1; i < widths.length; i++) {
    expect(widths[i], `进度条不得回退：${widths[i - 1]}% → ${widths[i]}%`).toBeGreaterThanOrEqual(widths[i - 1]);
  }
  expect(Math.max(...widths), '完成时进度必须到 100%').toBe(100);

  const paint = await page.evaluate(() => {
    const imgs = [...document.querySelectorAll('#contentArea img')];
    return {
      total: imgs.length,
      ready: imgs.filter(img => img.complete && img.naturalWidth > 0).length,
      placeholders: imgs.filter(img => (img.getAttribute('src') || '').includes('/api/doc-cache/')).length,
    };
  });
  expect(paint.placeholders, '首帧不得残留 doc-cache 占位').toBe(0);
  expect(paint.ready, '首帧全部图片必须已解码可显示').toBe(paint.total);
  expect(paint.total, '正文图片必须全部在首帧').toBeGreaterThanOrEqual(IMAGE_COUNT);

  await page.locator('#copyBtn').click();
  await expect(page.locator('#copyBtn'), '预览完整后复制必须立即成功').toContainText('已复制');

  console.log(
    `[feishu-import] stages=${calls.filter(c => c.startsWith('status:')).length}`
    + ` images=${paint.ready}/${paint.total} placeholders=${paint.placeholders}`
    + ` maxWidth=${Math.max(...widths)}% monotonic=yes copy=已复制`,
  );
  await context.close();
});
