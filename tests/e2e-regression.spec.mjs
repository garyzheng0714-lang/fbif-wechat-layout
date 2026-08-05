/**
 * Playwright E2E Regression Test — 8 visual scenarios x 3 breakpoints + focused interaction regressions
 *
 * Run: npx playwright test tests/e2e-regression.spec.mjs --reporter=list
 *
 * Screenshots saved to: tests/e2e-screenshots/
 */

import { test, expect } from '@playwright/test';
import path from 'path';
import fs from 'fs';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const VISUAL_ENABLED = process.env.E2E_SKIP_VISUAL !== '1';
const DOCX_PATH = path.resolve('tests/fixtures/tea-pyramid.docx');
const SCREENSHOT_DIR = path.resolve('tests/e2e-screenshots');
const E2E_USERNAME = 'e2e';
const E2E_PASSWORD = 'e2e-local-session-password';

// Ensure screenshot directory
fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });

const BREAKPOINTS = [
  { name: 'desktop-1440', width: 1440, height: 900 },
  { name: 'tablet-768', width: 768, height: 1024 },
  { name: 'mobile-390', width: 390, height: 844 },
];

const RESULTS = [];

// ============ Helpers ============

async function newAuthenticatedContext(browser, options = {}) {
  const context = await browser.newContext(options);
  const response = await context.request.post(`${BASE}/auth/password/login`, {
    data: {
      username: E2E_USERNAME,
      password: E2E_PASSWORD,
      remember: false,
    },
  });
  if (!response.ok()) {
    const body = await response.text();
    await context.close();
    throw new Error(`E2E 登录失败（${response.status()}）：${body}`);
  }
  return context;
}

function screenshotName(scenario, bp) {
  return `${scenario}__${bp.name}.png`;
}

async function waitForAppReady(page) {
  // Wait for the app to fully load (script modules executed)
  await page.waitForSelector('.app-layout', { timeout: 15000 });
  // Wait a bit for dynamic JS to settle
  await page.waitForTimeout(2000);
}

async function captureAndCheck(page, scenario, bp, extraChecks = null, screenshotOptions = {}) {
  const name = screenshotName(scenario, bp);
  const filePath = path.join(SCREENSHOT_DIR, name);
  const fullPage = screenshotOptions.fullPage ?? true;
  if (VISUAL_ENABLED) {
    await page.screenshot({ path: filePath, fullPage });
    await expect(page).toHaveScreenshot(name, {
      fullPage,
      mask: [page.locator('input[type="date"]')],
    });
  }

  const result = {
    scenario,
    breakpoint: bp.name,
    screenshot: name,
    issues: [],
  };

  // Check for horizontal overflow
  const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
  const clientWidth = await page.evaluate(() => document.documentElement.clientWidth);
  if (scrollWidth > clientWidth + 5) {
    result.issues.push(`水平溢出: scrollWidth=${scrollWidth} > clientWidth=${clientWidth}`);
  }

  // Check for JS console errors
  const consoleErrors = [];
  page.on('console', msg => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });

  if (extraChecks) {
    await extraChecks(result);
  }

  RESULTS.push(result);
  return result;
}

// ============ Test Suite ============

test.describe.serial('FBIF 排版工具 — 全状态回归测试', () => {
  // ---- Scenario 1: 初始空状态 ----
  for (const bp of BREAKPOINTS) {
    test(`1-初始空状态 [${bp.name}]`, async ({ browser }) => {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();
      const consoleErrors = [];
      page.on('console', msg => {
        if (msg.type() === 'error') consoleErrors.push(msg.text());
      });

      await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      // Verify empty state is visible
      const emptyState = page.locator('#emptyState');
      await expect(emptyState).toBeVisible();

      // Verify preview content is hidden
      const previewContent = page.locator('#previewContent');
      await expect(previewContent).toBeHidden();

      // Verify copy button exists
      await expect(page.locator('#copyBtn')).toBeVisible();

      // Verify sidebar upload zone exists
      await expect(page.locator('#dropZone')).toBeVisible();

      const result = await captureAndCheck(page, '1-empty-state', bp);

      if (consoleErrors.length > 0) {
        result.issues.push(`控制台错误: ${consoleErrors.join('; ')}`);
      }

      console.log(`[${bp.name}] Scenario 1 完成: ${result.issues.length} 个问题`);
      await context.close();
    });
  }

  // ---- Scenario 2: 上传 DOCX 全流程 ----
  for (const bp of BREAKPOINTS) {
    test(`2-上传DOCX全流程 [${bp.name}]`, async ({ browser }) => {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();
      const consoleErrors = [];
      page.on('console', msg => {
        if (msg.type() === 'error') consoleErrors.push(msg.text());
      });

      await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      // Upload DOCX
      const fileInput = page.locator('#fileInput');
      await fileInput.setInputFiles(DOCX_PATH);

      // Wait for parsing to complete (empty state should disappear)
      await page.waitForFunction(() => {
        const el = document.getElementById('previewContent');
        return el && el.style.display !== 'none';
      }, { timeout: 30000 });

      // Wait a bit for rendering
      await page.waitForTimeout(2000);

      // Verify content is showing
      const previewContent = page.locator('#previewContent');
      await expect(previewContent).toBeVisible();

      // Verify empty state is hidden
      const emptyState = page.locator('#emptyState');
      await expect(emptyState).toBeHidden();

      // Verify article title is populated (or at least the content area has content)
      const contentArea = page.locator('#contentArea');
      const hasContent = await contentArea.evaluate(el => el.innerHTML.length > 0);
      expect(hasContent).toBeTruthy();

      const result = await captureAndCheck(page, '2-docx-upload', bp);

      if (consoleErrors.length > 0) {
        result.issues.push(`控制台错误: ${consoleErrors.join('; ')}`);
      }

      console.log(`[${bp.name}] Scenario 2 完成: ${result.issues.length} 个问题`);
      await context.close();
    });
  }

  // ---- Scenario 3: URL 导入全流程 ----
  for (const bp of BREAKPOINTS) {
    test(`3-URL导入全流程 [${bp.name}]`, async ({ browser }) => {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();
      const consoleErrors = [];
      page.on('console', msg => {
        if (msg.type() === 'error') consoleErrors.push(msg.text());
      });

      // S5：拦截后端抓取接口返回确定性错误桩——本场景测的是「导入失败的前端
      // 流程与错误 UI」，真实外网请求时长不可控（曾把 toHaveScreenshot 拖到
      // 120s 超时），且外网状态会让视觉基线不稳定。
      await page.route('**/api/fetch-article*', route => route.fulfill({
        status: 422,
        contentType: 'application/json; charset=utf-8',
        body: JSON.stringify({ error: '无法抓取该链接（E2E 测试桩）' }),
      }));

      await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      // Type URL and fetch。当前 UI 没有独立抓取按钮（旧 #urlFetchBtn 已随导入改版
      // 删除）：粘贴到 #urlInput 自动触发导入，等价入口是页面暴露的 window.fetchArticle()。
      const urlInput = page.locator('#urlInput');
      await urlInput.fill('https://mp.weixin.qq.com/s/test-article');
      await page.evaluate(() => window.fetchArticle());

      // Wait for the deterministic error state to render
      await page.locator('#error').waitFor({ state: 'visible', timeout: 15000 }).catch(() => {});
      await page.waitForTimeout(1000);

      // Check for error display (expected since URL is fake)
      const errorEl = page.locator('#error');
      const errorText = await errorEl.textContent();
      const isErrorVisible = await errorEl.isVisible();

      const result = await captureAndCheck(page, '3-url-import', bp);
      result.fetchResult = isErrorVisible ? 'error' : 'success';
      result.fetchMessage = errorText;

      if (consoleErrors.length > 0) {
        result.issues.push(`控制台错误: ${consoleErrors.join('; ')}`);
      }

      console.log(`[${bp.name}] Scenario 3 完成: ${result.fetchResult}, ${result.issues.length} 个问题`);
      await context.close();
    });
  }

  // ---- Scenario 4: 排版完成状态 ----
  for (const bp of BREAKPOINTS) {
    test(`4-排版完成状态 [${bp.name}]`, async ({ browser }) => {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();
      const consoleErrors = [];
      page.on('console', msg => {
        if (msg.type() === 'error') consoleErrors.push(msg.text());
      });

      await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      // Upload DOCX to get to "done" state
      const fileInput = page.locator('#fileInput');
      await fileInput.setInputFiles(DOCX_PATH);

      // Wait for content to appear
      await page.waitForFunction(() => {
        const el = document.getElementById('previewContent');
        return el && el.style.display !== 'none';
      }, { timeout: 30000 });

      await page.waitForTimeout(3000);

      const result = await captureAndCheck(page, '4-layout-done', bp);

      // Verify footer is visible
      const wxFooter = page.locator('#wxFooter');
      const footerVisible = await wxFooter.isVisible();
      result.footerVisible = footerVisible;

      // Check bottom bar styling (white background, centered divider)
      if (footerVisible) {
        const footerBg = await wxFooter.evaluate(el => getComputedStyle(el).backgroundColor);
        result.footerBg = footerBg;
        // Should be white-ish (rgb(255, 255, 255) or similar)
        if (!footerBg.includes('255, 255, 255') && !footerBg.includes('transparent')) {
          result.issues.push(`底部栏背景不是白色: ${footerBg}`);
        }
      }

      if (consoleErrors.length > 0) {
        result.issues.push(`控制台错误: ${consoleErrors.join('; ')}`);
      }

      console.log(`[${bp.name}] Scenario 4 完成: footer=${footerVisible}, ${result.issues.length} 个问题`);
      await context.close();
    });
  }

  // ---- Scenario 5: 复制成功状态 ----
  for (const bp of BREAKPOINTS) {
    test(`5-复制成功状态 [${bp.name}]`, async ({ browser }) => {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
        permissions: ['clipboard-read', 'clipboard-write'],
      });
      const page = await context.newPage();
      const consoleErrors = [];
      page.on('console', msg => {
        if (msg.type() === 'error') consoleErrors.push(msg.text());
      });

      await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      // Upload DOCX first
      const fileInput = page.locator('#fileInput');
      await fileInput.setInputFiles(DOCX_PATH);

      await page.waitForFunction(() => {
        const el = document.getElementById('previewContent');
        return el && el.style.display !== 'none';
      }, { timeout: 30000 });

      await page.waitForTimeout(3000);

      // Click copy button
      const copyBtn = page.locator('#copyBtn');
      await copyBtn.click();

      // Wait for copy success indicator
      await page.waitForTimeout(2000);

      const result = await captureAndCheck(page, '5-copy-success', bp);

      // Check if copy success message appeared
      const copySuccess = page.locator('#copySuccess');
      const successVisible = await copySuccess.isVisible();
      result.copySuccessVisible = successVisible;

      // Check button feedback
      const btnText = await copyBtn.textContent();
      result.copyBtnText = btnText;
      const copyWarning = await page.evaluate(() => window._copyImageWarning || 0);
      expect(copyWarning).toBe(0);
      expect(btnText).toBe('✓ 已复制');

      if (consoleErrors.length > 0) {
        result.issues.push(`控制台错误: ${consoleErrors.join('; ')}`);
      }

      console.log(`[${bp.name}] Scenario 5 完成: success=${successVisible}, btn="${btnText}", ${result.issues.length} 个问题`);
      await context.close();
    });
  }

  // ---- Scenario 6: 抓取失败状态 ----
  for (const bp of BREAKPOINTS) {
    test(`6-抓取失败状态 [${bp.name}]`, async ({ browser }) => {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();
      const consoleErrors = [];
      page.on('console', msg => {
        if (msg.type() === 'error') consoleErrors.push(msg.text());
      });

      // S5：拦截后端抓取接口返回确定性 500——本场景测「抓取失败的错误 UI」，
      // 真实打无效域名的时长与文案都不可控（同场景 3 的教训）。
      await page.route('**/api/fetch-article*', route => route.fulfill({
        status: 500,
        contentType: 'application/json; charset=utf-8',
        body: JSON.stringify({ error: '抓取服务暂时不可用（E2E 测试桩）' }),
      }));

      await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      // Type a WeChat-shaped URL（非公众号域名会被自动识别成正文文字而非链接）and fetch.
      // 旧 #urlFetchBtn 已删：等价入口是 window.fetchArticle()。
      const urlInput = page.locator('#urlInput');
      await urlInput.fill('https://mp.weixin.qq.com/s/e2e-fetch-error');
      await page.evaluate(() => window.fetchArticle());

      // Wait for the deterministic error state to render
      await page.locator('#error').waitFor({ state: 'visible', timeout: 15000 }).catch(() => {});
      await page.waitForTimeout(1000);

      const result = await captureAndCheck(page, '6-fetch-error', bp);

      // Verify error message is displayed
      const errorEl = page.locator('#error');
      const errorVisible = await errorEl.isVisible();
      const errorText = await errorEl.textContent();
      result.errorVisible = errorVisible;
      result.errorText = errorText;

      if (!errorVisible) {
        result.issues.push('抓取失败后未显示错误信息');
      }

      if (consoleErrors.length > 0) {
        result.issues.push(`控制台错误: ${consoleErrors.join('; ')}`);
      }

      console.log(`[${bp.name}] Scenario 6 完成: error="${errorText}", ${result.issues.length} 个问题`);
      await context.close();
    });
  }

  // ---- Scenario 7: 规则后台视图 ----
  for (const bp of BREAKPOINTS) {
    test(`7-规则后台视图 [${bp.name}]`, async ({ browser }) => {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();
      const consoleErrors = [];
      page.on('console', msg => {
        if (msg.type() === 'error') consoleErrors.push(msg.text());
      });

      await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      // Click the rule settings entry inside the signed-in identity menu.
      await page.locator('.rail-foot .user-identity-trigger').click();
      const adminBtn = page.locator('.user-identity-menu #adminLockBtn');
      const adminEntryVisible = await adminBtn.isVisible();
      if (adminEntryVisible) {
        await adminBtn.click();
        await page.waitForTimeout(2000);
      }

      // 768px 正好是 admin-panel.css 的响应式边界。页面启动时会把裸 CSS URL 换成
      // 带构建号的 URL；换链期间旧缓存可能仍把 overlay 当成 app-layout 内的绝对定位，
      // 即使 padding/max-width 已经相同，截图仍会随机上下偏移一个顶栏高度。这里等待
      // 最终样式合同全部落稳：overlay 固定铺满视口，tablet 抽屉保留 12px 安全边距。
      if (bp.width === 768 && adminEntryVisible) {
        await expect(page.locator('.admin-overlay')).toHaveCSS('padding', '12px');
        await expect(page.locator('.admin-overlay')).toHaveCSS('position', 'fixed');
        await expect(page.locator('.admin-overlay')).toHaveCSS('top', '0px');
        await expect(page.locator('.admin-dialog')).toHaveCSS('max-width', '744px');
        await expect.poll(async () => (await page.locator('.admin-dialog').boundingBox())?.y)
          .toBe(12);
      }

      // 固定定位的浮层不能用 fullPage 拼接截图：页面比 viewport 高时，Chromium 会把
      // fixed 元素随机放在首屏或下一拼接片，造成 60px 假差异。规则后台只验可见视口。
      const result = await captureAndCheck(page, '7-rule-admin', bp, null, { fullPage: false });

      // Check if admin panel is open
      const adminPanel = page.locator('#adminPanel, .admin-panel, [class*="admin"]');
      const adminVisible = await adminPanel.first().isVisible().catch(() => false);
      result.adminVisible = adminVisible;

      if (consoleErrors.length > 0) {
        result.issues.push(`控制台错误: ${consoleErrors.join('; ')}`);
      }

      console.log(`[${bp.name}] Scenario 7 完成: admin=${adminVisible}, ${result.issues.length} 个问题`);
      await context.close();
    });
  }

  // ---- Scenario 8: 网站渠道视图 ----
  for (const bp of BREAKPOINTS) {
    test(`8-网站渠道视图 [${bp.name}]`, async ({ browser }) => {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();
      const consoleErrors = [];
      page.on('console', msg => {
        if (msg.type() === 'error') consoleErrors.push(msg.text());
      });

      // Navigate to FoodTalks site channel
      await page.goto(BASE + '/ft/default', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      const result = await captureAndCheck(page, '8-site-channel', bp);

      // Verify FoodTalks channel is active：旧 [data-channel] 渠道 tab 已随两层切换器
      // 删除（红线 #2），现在验证顶栏账号菜单显示的是网站渠道账号。
      const acctName = await page.locator('.acct-trigger-name').textContent().catch(() => '');
      result.ftChannelActive = /网站/.test(acctName || '');

      // Verify sidebar has site-specific class
      const sidebar = page.locator('.sidebar');
      const sidebarClass = await sidebar.getAttribute('class');
      result.sidebarClass = sidebarClass;

      if (consoleErrors.length > 0) {
        result.issues.push(`控制台错误: ${consoleErrors.join('; ')}`);
      }

      console.log(`[${bp.name}] Scenario 8 完成: ft=${result.ftChannelActive}, ${result.issues.length} 个问题`);
      await context.close();
    });
  }

  // ---- Scenario 9: 更多文章编辑必须实时反映到右侧预览 ----
  test('9-更多文章链接抓取后实时更新预览', async ({ browser }) => {
    const context = await newAuthenticatedContext(browser, { viewport: { width: 1440, height: 900 } });
    const page = await context.newPage();
    const nextURL = 'https://mp.weixin.qq.com/s/e2e-live-preview';
    const nextCover = 'https://example.com/e2e-live-preview.png';
    const tinyPNG = Buffer.from(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+Xw4AAAAASUVORK5CYII=',
      'base64',
    );

    // 抓取元数据与远程封面均走确定性桩：本场景只验证「左侧成功后右侧立即换卡」，
    // 不把微信限流或图片 CORS 混进交互合同。
    await page.route('**/api/fetch-article-meta', route => route.fulfill({
      status: 200,
      contentType: 'application/json; charset=utf-8',
      body: JSON.stringify({
        source_url: nextURL,
        title: '新增文章必须实时出现在右侧预览',
        cover_url: nextCover,
        image_urls: [nextCover],
      }),
    }));
    await page.route('**/api/image-proxy?**', route => route.fulfill({
      status: 200,
      contentType: 'image/png',
      body: tinyPNG,
    }));

    await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
    await waitForAppReady(page);
    await page.locator('#fileInput').setInputFiles(DOCX_PATH);
    await page.waitForFunction(() => {
      const el = document.getElementById('previewContent');
      return el && el.style.display !== 'none';
    }, { timeout: 30000 });

    const urlInputs = page.locator('#moreArticlesEditor .ma-url');
    await expect(urlInputs).toHaveCount(3);
    const oldThirdLink = page.locator('#contentArea a[href*="2652906407"]');
    await expect(oldThirdLink).toHaveCount(1);
    const oldPreviewSrc = await oldThirdLink.locator('img').getAttribute('src');

    await urlInputs.nth(2).fill(nextURL);
    const thirdCard = page.locator('#moreArticlesEditor .ma-card').nth(2);
    await expect(thirdCard.locator('.ma-card-status')).toHaveText('✓ 已抓取', { timeout: 20000 });

    const nextLink = page.locator(`#contentArea a[href="${nextURL}"]`);
    await expect(nextLink).toHaveCount(1);
    const nextPreviewSrc = await nextLink.locator('img').getAttribute('src');
    expect(nextPreviewSrc).toMatch(/^data:image\/png;base64,/);
    expect(nextPreviewSrc).not.toBe(oldPreviewSrc);
    const savedCards = await page.evaluate(() => JSON.parse(localStorage.getItem('more_articles_v1::fbif') || '[]'));
    expect(savedCards[2]?.title).toBe('新增文章必须实时出现在右侧预览');

    await context.close();
  });

  // ---- Scenario 10: 浏览器拒绝两种剪贴板写法时必须明确失败 ----
  test('10-剪贴板权限失败不得显示复制成功', async ({ browser }) => {
    const context = await newAuthenticatedContext(browser, { viewport: { width: 1440, height: 900 } });
    const page = await context.newPage();

    await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
    await waitForAppReady(page);
    await page.locator('#fileInput').setInputFiles(DOCX_PATH);
    await page.waitForFunction(() => {
      const el = document.getElementById('previewContent');
      return el && el.style.display !== 'none';
    }, { timeout: 30000 });

    await page.evaluate(() => {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          write: async () => { throw new DOMException('clipboard denied', 'NotAllowedError'); },
        },
      });
      Object.defineProperty(document, 'execCommand', {
        configurable: true,
        value: () => false,
      });
    });

    const copyButton = page.locator('#copyBtn');
    await copyButton.click();
    await expect(copyButton).toHaveText('复制失败');
    await expect(copyButton).not.toHaveClass(/\bcopied\b/);
    await expect(page.locator('#copySuccess')).toBeHidden();
    await expect(page.locator('#copyPasteHint')).toHaveCount(0);
    const copyLog = await page.evaluate(() => (
      window.getConvLog().findLast(entry => entry.msg === '复制失败')
    ));
    expect(copyLog).toMatchObject({ level: 'error', msg: '复制失败' });
    expect(copyLog.detail.artifactHash).toMatch(/^[0-9a-f]{8}$/);

    await context.close();
  });

  // ---- Scenario 11: 1440px 桌面在浏览器 125%/150% 缩放后的 CSS viewport ----
  for (const zoom of [125, 150]) {
    test(`11-浏览器缩放 ${zoom}% 无水平溢出`, async ({ browser }) => {
      const scale = zoom / 100;
      const bp = {
        name: `desktop-zoom-${zoom}`,
        width: Math.floor(1440 / scale),
        height: Math.floor(900 / scale),
      };
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();

      await page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
      await waitForAppReady(page);
      await page.locator('#fileInput').setInputFiles(DOCX_PATH);
      await page.waitForFunction(() => {
        const el = document.getElementById('previewContent');
        return el && el.style.display !== 'none';
      }, { timeout: 30000 });

      await expect(page.locator('.global-channel-bar')).toBeVisible();
      await expect(page.locator('#copyBtn')).toBeVisible();
      const result = await captureAndCheck(page, `11-browser-zoom-${zoom}`, bp);
      expect(result.issues).toEqual([]);

      await context.close();
    });
  }

  // ---- Scenario 12: 规则 API 弱网期间输出保持 fail-closed，响应后自动解锁 ----
  test('12-规则 API 延迟期间禁用复制并在解析后恢复', async ({ browser }) => {
    const context = await newAuthenticatedContext(browser, { viewport: { width: 1440, height: 900 } });
    const page = await context.newPage();
    let releaseRuleRequest;
    let markRuleRequest;
    const ruleRequestHeld = new Promise(resolve => { releaseRuleRequest = resolve; });
    const ruleRequestSeen = new Promise(resolve => { markRuleRequest = resolve; });

    await page.route('**/api/rules/active?**', async route => {
      markRuleRequest();
      await ruleRequestHeld;
      await route.continue();
    });

    const navigation = page.goto(BASE + '/mp/fbif', { waitUntil: 'networkidle' });
    await ruleRequestSeen;
    try {
      await expect(page.locator('#copyBtn')).toBeDisabled();
      await expect.poll(() => page.evaluate(() => window._activeRuleState)).toBe('loading');
    } finally {
      releaseRuleRequest();
    }
    await navigation;
    await waitForAppReady(page);
    await expect.poll(() => page.evaluate(() => window._activeRuleState)).toBe('resolved');
    await expect(page.locator('#copyBtn')).toBeEnabled();

    await context.close();
  });

  // ---- Scenario 13: 网站分裂按钮复制当前最终 HTML 源码 ----
  test('13-网站复制 HTML 写入真实剪贴板并保留 CMS 结构', async ({ browser }) => {
    const context = await newAuthenticatedContext(browser, {
      viewport: { width: 1440, height: 900 },
      permissions: ['clipboard-read', 'clipboard-write'],
    });
    const page = await context.newPage();

    await page.goto(BASE + '/ft/ft-default', { waitUntil: 'networkidle' });
    await waitForAppReady(page);
    await page.locator('#fileInput').setInputFiles(DOCX_PATH);
    await page.waitForFunction(() => {
      const el = document.getElementById('previewContent');
      return el && el.style.display !== 'none';
    }, { timeout: 30000 });

    await page.locator('#copyMenuBtn').click();
    await expect(page.locator('#copyMenuBtn')).toHaveAttribute('aria-expanded', 'true');
    await expect(page.getByRole('menuitem', { name: '复制 HTML' })).toBeVisible();
    await page.getByRole('menuitem', { name: '复制 HTML' }).click();

    await expect(page.locator('#copyMenu')).toBeHidden();
    await expect(page.locator('#copySuccess')).toHaveText('已复制 HTML 源码');
    const copied = await page.evaluate(() => navigator.clipboard.readText());
    expect(copied).toMatch(/^\s*</);
    expect(copied).toContain('<h2');
    expect(copied).toContain('<strong>');
    expect(copied).toContain('<a ');
    expect(copied).toContain('<figure class="image">');
    expect(copied).toMatch(/<img\b[^>]*class="mc-img"/);
    expect(copied).toContain('<figcaption>');
    expect(copied).not.toMatch(/<script\b/i);

    await context.close();
  });

  test('14-网站复制分体按钮在桌面、平板和手机断点保持统一几何', async ({ browser }) => {
    for (const bp of BREAKPOINTS) {
      const context = await newAuthenticatedContext(browser, {
        viewport: { width: bp.width, height: bp.height },
      });
      const page = await context.newPage();
      await page.goto(BASE + '/ft/ft-default', { waitUntil: 'networkidle' });
      await waitForAppReady(page);

      const geometry = await page.evaluate(() => {
        const root = document.getElementById('copySplit').getBoundingClientRect();
        const primary = document.getElementById('copyBtn').getBoundingClientRect();
        const toggle = document.getElementById('copyMenuBtn').getBoundingClientRect();
        return {
          rootHeight: root.height,
          primaryHeight: primary.height,
          toggleHeight: toggle.height,
          primaryBottom: primary.bottom,
          toggleBottom: toggle.bottom,
        };
      });
      const expectedHeight = bp.width <= 820 ? 44 : 36;
      expect(geometry.rootHeight).toBe(expectedHeight);
      expect(geometry.primaryHeight).toBe(expectedHeight);
      expect(geometry.toggleHeight).toBe(expectedHeight);
      expect(geometry.primaryBottom).toBe(geometry.toggleBottom);
      await expect(page.locator('#copyBtn')).toHaveAttribute('aria-label', '复制全文');

      await context.close();
    }
  });

  test('15-网站规则加载期间锁定整个复制分体按钮', async ({ browser }) => {
    const context = await newAuthenticatedContext(browser, { viewport: { width: 768, height: 1024 } });
    const page = await context.newPage();
    let releaseRuleRequest;
    let markRuleRequest;
    const ruleRequestHeld = new Promise(resolve => { releaseRuleRequest = resolve; });
    const ruleRequestSeen = new Promise(resolve => { markRuleRequest = resolve; });

    await page.route('**/api/rules/active?**', async route => {
      markRuleRequest();
      await ruleRequestHeld;
      await route.continue();
    });

    const navigation = page.goto(BASE + '/ft/ft-default', { waitUntil: 'networkidle' });
    await ruleRequestSeen;
    try {
      await expect(page.locator('#copyBtn')).toBeDisabled();
      await expect(page.locator('#copyMenuBtn')).toBeDisabled();
      await expect(page.locator('#copyHtmlBtn')).toBeDisabled();
      await expect(page.locator('#copyMenu')).toBeHidden();
    } finally {
      releaseRuleRequest();
    }
    await navigation;
    await waitForAppReady(page);
    await expect(page.locator('#copyBtn')).toBeEnabled();
    await expect(page.locator('#copyMenuBtn')).toBeEnabled();
    await expect(page.locator('#copyHtmlBtn')).toBeEnabled();

    await context.close();
  });
});

// ---- Generate report after all tests ----
test.afterAll(async () => {
  const reportPath = path.join(SCREENSHOT_DIR, 'regression-report.json');
  fs.writeFileSync(reportPath, JSON.stringify(RESULTS, null, 2), 'utf-8');
  console.log(`\n=== 测试报告已保存: ${reportPath} ===`);
});
