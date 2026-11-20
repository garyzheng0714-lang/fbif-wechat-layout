import { test, expect } from '@playwright/test';
import { loginAs } from './e2e-login.mjs';
import JSZip from 'jszip';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const LOGIN = { username: 'e2e', name: 'E2E 测试账号' };
const DOCX_MIME = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
const IMAGE_COUNT = 24;
const IMAGE_DELAY_MS = 600;
const IMAGE_DELAY_STEP_MS = 25;
const PREVIEW_TEXT = 'PREVIEW_ATOMIC_TEXT：正文与图片必须一次出现';
const TINY_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+Xw4AAAAASUVORK5CYII=',
  'base64',
);

function imageParagraph(index) {
  return `
    <w:p><w:r><w:drawing><wp:inline>
      <wp:extent cx="5486400" cy="3086100"/>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rId${index}"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>`;
}

async function buildStrippedDocx() {
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
      ${Array.from({ length: IMAGE_COUNT }, (_, index) => (
        `<Relationship Id="rId${index + 1}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image${index + 1}.png"/>`
      )).join('')}
    </Relationships>`);
  zip.file('word/_fbif_imageUrls.json', JSON.stringify(Object.fromEntries(
    Array.from({ length: IMAGE_COUNT }, (_, index) => [
      `image${index + 1}.png`,
      `/api/doc-cache/preview-speed/image${index + 1}.png`,
    ]),
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
        <w:p><w:r><w:t>${PREVIEW_TEXT}</w:t></w:r></w:p>
        ${Array.from({ length: IMAGE_COUNT }, (_, index) => imageParagraph(index + 1)).join('')}
        <w:sectPr/>
      </w:body>
    </w:document>`);
  return zip.generateAsync({ type: 'nodebuffer', compression: 'DEFLATE' });
}

async function authenticatedContext(browser) {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'],
  });
  try {
    await loginAs(context, LOGIN);
  } catch (error) {
    await context.close();
    throw error;
  }
  return context;
}

test('7 月 31 日原子预览：24 张剥图并发准备、显示真实进度、完整后整篇出现', async ({ browser }) => {
  const fixture = await buildStrippedDocx();
  const context = await authenticatedContext(browser);
  const page = await context.newPage();
  const requestStartedAt = [];
  const requestFinishedAt = [];
  await page.route('**/api/doc-cache/preview-speed/**', async route => {
    const match = route.request().url().match(/image(\d+)\.png/);
    const index = Number(match?.[1] || 1);
    requestStartedAt.push(Date.now());
    await new Promise(resolve => setTimeout(resolve, IMAGE_DELAY_MS + index * IMAGE_DELAY_STEP_MS));
    await route.fulfill({
      status: 200,
      contentType: 'image/png',
      headers: { 'cache-control': 'no-store' },
      body: TINY_PNG,
    });
    requestFinishedAt.push(Date.now());
  });

  await page.goto(`${BASE}/mp/fbif`, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => (
    typeof window._handleFile === 'function'
    && typeof window.copyContent === 'function'
    && window._activeRuleState === 'resolved'
  ), { timeout: 15000 });
  await page.evaluate(() => { window.__previewUploadStartedAt = performance.now(); });
  await page.locator('#fileInput').setInputFiles({
    name: 'preview-atomic-24-images.docx',
    mimeType: DOCX_MIME,
    buffer: fixture,
  });

  // 全套 CI 会同时解析其他 DOCX，先等待本用例真正进入图片阶段；并发性从
  // 第一张请求开始计时，不能把共享 runner 的解析排队误判成图片串行。
  await expect.poll(() => requestStartedAt.length, {
    message: 'DOCX 必须进入图片准备阶段',
    timeout: 10000,
  }).toBeGreaterThan(0);
  await expect.poll(() => requestStartedAt.length, {
    message: '第一张图片启动后，其余 23 张必须立即并发启动',
    timeout: 1000,
  }).toBe(IMAGE_COUNT);
  expect(
    requestStartedAt.at(-1) - requestStartedAt[0],
    '24 张图片请求的启动时间差必须小于 1 秒',
  ).toBeLessThan(1000);
  await expect(page.locator('#progress'), '图片准备期间必须显示导入进度').toBeVisible();
  await expect(page.locator('#contentArea'), '图片未齐时不得提前展示残缺正文').not.toContainText(PREVIEW_TEXT);
  // 分母是本次真正要准备的图片总数：正文 24 张之外还有页脚图，它们同样要在
  // 上屏前就绪。这里只要求分母真实（≥ 正文张数），不写死成 24。
  await expect(page.locator('#progressText'), '进度文案必须真实报告图片完成数').toContainText(
    /正在准备正文图片 \d+\/\d+/,
    { timeout: 2500 },
  );
  const preparationTotal = Number(
    (await page.locator('#progressText').textContent())?.match(/\d+\/(\d+)/)?.[1] || 0,
  );
  expect(preparationTotal, '准备阶段分母必须覆盖全部正文图片').toBeGreaterThanOrEqual(IMAGE_COUNT);

  await expect(page.locator('#contentArea'), '24 张图片齐备后正文须在 3 秒内完整出现').toContainText(
    PREVIEW_TEXT,
    { timeout: 3000 },
  );
  const expectedDataUrl = `data:image/png;base64,${TINY_PNG.toString('base64')}`;
  const firstPaint = await page.evaluate(({ imageCount, expectedSrc }) => {
    const imgs = [...document.querySelectorAll('#contentArea img')]
      .filter(img => img.getAttribute('src') === expectedSrc);
    return {
      elapsedMs: performance.now() - window.__previewUploadStartedAt,
      count: imgs.length,
      ready: imgs.filter(img => img.complete && img.naturalWidth > 0).length,
      pendingRefs: [...document.querySelectorAll('#contentArea img')]
        .filter(img => img.getAttribute('src')?.includes('/api/doc-cache/')).length,
      expected: imageCount,
    };
  }, { imageCount: IMAGE_COUNT, expectedSrc: expectedDataUrl });
  expect(firstPaint.elapsedMs, '24 张图并发准备后完整预览仍须 ≤3s').toBeLessThanOrEqual(3000);
  expect(requestFinishedAt, '正文出现前每张图片请求必须完成').toHaveLength(IMAGE_COUNT);
  expect(firstPaint.count, '完整预览必须包含 fixture 的全部 24 张图').toBe(IMAGE_COUNT);
  expect(firstPaint.ready, '预览首帧的 24 张图必须全部可显示').toBe(IMAGE_COUNT);
  expect(firstPaint.pendingRefs, '预览首帧不得残留 doc-cache 占位地址').toBe(0);

  await page.locator('#copyBtn').click();
  await expect(page.locator('#copyBtn'), '完整预览出现后复制必须立即成功，不再进入图片准备中').toContainText('已复制');

  console.log(
    `[preview-atomic] complete=${firstPaint.elapsedMs.toFixed(1)}ms`
    + ` images=${firstPaint.ready}/${IMAGE_COUNT}`
    + ` min-delay=${IMAGE_DELAY_MS}ms requests=${requestStartedAt.length}`
    + ' progress=visible copy=已复制 pending-refs=0',
  );
  await context.close();
});
