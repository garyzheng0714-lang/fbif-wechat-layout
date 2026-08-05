import { test, expect } from '@playwright/test';
import JSZip from 'jszip';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const LOGIN = { username: 'e2e', password: 'e2e-local-session-password', remember: false };
const DOCX_MIME = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
const IMAGE_COUNT = 4;
const IMAGE_DELAY_MS = 1200;
const PREVIEW_TEXT = 'PREVIEW_SPEED_TEXT：正文必须先出现';
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
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const response = await context.request.post(`${BASE}/auth/password/login`, { data: LOGIN });
  if (!response.ok()) {
    const body = await response.text();
    await context.close();
    throw new Error(`E2E 登录失败（${response.status()}）：${body}`);
  }
  return context;
}

test('剥图 DOCX 正文快速出现、图片完整后淡入且复制不缺图', async ({ browser }) => {
  const fixture = await buildStrippedDocx();
  const context = await authenticatedContext(browser);
  const page = await context.newPage();
  const requestStartedAt = [];

  await page.addInitScript(() => {
    window.__docImageLoadTimes = [];
    document.addEventListener('load', event => {
      const target = event.target;
      if (target instanceof HTMLImageElement && target.getAttribute('src')?.includes('/api/doc-cache/preview-speed/')) {
        window.__docImageLoadTimes.push(performance.now());
      }
    }, true);
  });
  await page.route('**/api/doc-cache/preview-speed/**', async route => {
    requestStartedAt.push(Date.now());
    await new Promise(resolve => setTimeout(resolve, IMAGE_DELAY_MS));
    await route.fulfill({
      status: 200,
      contentType: 'image/png',
      headers: { 'cache-control': 'no-store' },
      body: TINY_PNG,
    });
  });

  await page.goto(`${BASE}/mp/fbif`, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => (
    typeof window._handleFile === 'function'
    && typeof window.copyContent === 'function'
    && window._activeRuleState === 'resolved'
  ), { timeout: 15000 });
  await page.evaluate(() => { window.__previewUploadStartedAt = performance.now(); });
  await page.locator('#fileInput').setInputFiles({
    name: 'preview-speed-stripped.docx',
    mimeType: DOCX_MIME,
    buffer: fixture,
  });

  await expect(page.locator('#contentArea'), '正文必须在 3 秒内出现').toContainText(PREVIEW_TEXT, { timeout: 3000 });
  const firstPaint = await page.evaluate(({ imageCount }) => {
    const imgs = [...document.querySelectorAll('#contentArea img[src*="/api/doc-cache/preview-speed/"]')];
    return {
      elapsedMs: performance.now() - window.__previewUploadStartedAt,
      imageLoads: [...window.__docImageLoadTimes],
      count: imgs.length,
      unready: imgs.filter(img => !img.complete || img.naturalWidth === 0).length,
      loading: imgs.filter(img => img.classList.contains('wx-img-loading')).length,
      transparentPlaceholders: imgs.filter(img => {
        const style = getComputedStyle(img);
        return style.minHeight === '140px'
          && style.opacity === '0'
          && style.backgroundColor === 'rgba(0, 0, 0, 0)'
          && style.animationName === 'none';
      }).length,
      expected: imageCount,
    };
  }, { imageCount: IMAGE_COUNT });
  expect(firstPaint.elapsedMs, '正文预览 ≤3s').toBeLessThanOrEqual(3000);
  expect(firstPaint.imageLoads, '正文出现必须早于任一 doc-cache 图片 load').toHaveLength(0);
  expect(firstPaint.unready, '正文出现时每张慢图仍未就绪').toBe(IMAGE_COUNT);
  expect(firstPaint.count, 'fixture 中每张剥图都进入预览').toBe(IMAGE_COUNT);
  expect(firstPaint.loading, '每张慢图都保持完整后淡入的加载类').toBe(IMAGE_COUNT);
  expect(firstPaint.transparentPlaceholders, '慢图保持透明占位，不显示灰色骨架').toBe(IMAGE_COUNT);

  await page.locator('#copyBtn').click();
  await expect(page.locator('#copyBtn'), '回填完成前复制必须瞬时挡回').toContainText('图片准备中');

  await expect.poll(async () => page.evaluate(expected => {
    const imgs = [...document.querySelectorAll('#contentArea img[src*="/api/doc-cache/preview-speed/"]')];
    return {
      ready: imgs.filter(img => img.complete && img.naturalWidth > 0).length,
      loading: imgs.filter(img => img.classList.contains('wx-img-loading')).length,
      opaque: imgs.filter(img => getComputedStyle(img).opacity === '1').length,
      loads: window.__docImageLoadTimes.length,
      expected,
    };
  }, IMAGE_COUNT), '图片到达后淡入并移除加载类').toEqual({
    ready: IMAGE_COUNT,
    loading: 0,
    opaque: IMAGE_COUNT,
    loads: IMAGE_COUNT,
    expected: IMAGE_COUNT,
  });

  console.log(
    `[preview-speed] text=${firstPaint.elapsedMs.toFixed(1)}ms`
    + ` before-images=${firstPaint.imageLoads.length === 0}`
    + ` transparent=${firstPaint.transparentPlaceholders}/${IMAGE_COUNT}`
    + ` delay=${IMAGE_DELAY_MS}ms requests=${requestStartedAt.length}`
    + ' copy-feedback=图片准备中 images-ready=4/4',
  );
  await context.close();
});
