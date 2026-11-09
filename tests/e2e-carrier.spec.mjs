/**
 * 载体胶囊「文章 | 贴图」真实浏览器验证（2026-10-27 用户裁定）。
 * 贴图编辑器只认一个段落里的 <br>：贴图载体复制出的 text/html 必须是 <p>甲<br><br>乙</p>，
 * 文章载体的排版与复制保持原样；切换不重新导入，刷新、切账号都留在用户选的载体。
 */
import { test, expect } from '@playwright/test';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const E2E_USERNAME = 'e2e';

const SAMPLE = '“这是啥？”\n\n“马冬莓。”\n\n……行了，别问了。\n\n大九酿造展位：3C馆 K30（3楼，「马冬莓」等你！）';
const STICKER_HTML = '<p>“这是啥？”<br><br>“马冬莓。”<br><br>……行了，别问了。<br><br>大九酿造展位：3C馆 K30（3楼，「马冬莓」等你！）</p>';

async function newAuthenticatedContext(browser) {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'],
  });
  const resp = await context.request.post(`${BASE}/auth/dev-login`, {
    data: { username: E2E_USERNAME },
  });
  if (!resp.ok()) throw new Error(`E2E 登录失败（${resp.status()}）：${await resp.text()}`);
  return context;
}

async function openEditor(page, route = '/mp/fbif') {
  await page.goto(BASE + route, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => window._activeRuleState === 'resolved' && !!document.querySelector('#carrierSwitch [role="radio"]'));
}

async function pasteSample(page) {
  await page.evaluate(value => navigator.clipboard.writeText(value), SAMPLE);
  await page.locator('#urlInput').focus();
  await page.keyboard.press('ControlOrMeta+V');
  await expect(page.locator('#contentArea')).toContainText('马冬莓', { timeout: 15000 });
}

const option = (page, name) => page.getByRole('radio', { name });

async function copyAndRead(page) {
  await page.locator('#copyBtn').click();
  await expect(page.locator('#copyBtn')).toContainText('已复制');
  return page.evaluate(async () => {
    const [item] = await navigator.clipboard.read();
    return {
      copied: window.getClipboardHtml(),
      html: await (await item.getType('text/html')).text(),
      text: await (await item.getType('text/plain')).text(),
    };
  });
}

// 预览里引擎留了一个页脚位置的注释标记，不是内容。
function previewHtml(page) {
  return page.locator('#contentArea').evaluate(el => el.innerHTML.replace(/<!--[\s\S]*?-->/g, ''));
}

async function expectStickerPreview(page) {
  await expect(page.locator('#contentArea')).toHaveAttribute('data-template', 'sticker');
  await expect.poll(() => previewHtml(page)).toBe(STICKER_HTML);
  await expect(page.locator('.wx-header')).toBeHidden();
  await expect(page.locator('#wxFooter')).toBeHidden();
}

test('胶囊在导入卡第一行、账号旁边，默认文章；只有公众号编辑器有', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page);

  const row = page.locator('.import-destination');
  await expect(row.locator('#acctMenu + #carrierSwitch')).toHaveAttribute('role', 'radiogroup');
  await expect(option(page, '文章')).toHaveAttribute('aria-checked', 'true');
  await expect(option(page, '贴图')).toHaveAttribute('aria-checked', 'false');
  const chip = await page.locator('#acctMenu .acct-trigger').boundingBox();
  const capsule = await page.locator('#carrierSwitch').boundingBox();
  expect(Math.abs((chip.y + chip.height / 2) - (capsule.y + capsule.height / 2))).toBeLessThan(1.5);
  expect(capsule.x).toBeGreaterThan(chip.x + chip.width);
  const card = await page.locator('#dropZone').boundingBox();
  expect(capsule.x + capsule.width).toBeLessThanOrEqual(card.x + card.width);

  await page.locator('#railToggleBtn').click();
  await expect(page.locator('#acctMenu .acct-trigger')).not.toBeInViewport();
  await expect(page.locator('#carrierSwitch')).not.toBeInViewport();
  await page.locator('#railToggleBtn').click();

  await page.goto(BASE + '/ft/default', { waitUntil: 'networkidle' });
  await expect(page.locator('#carrierSwitch')).toHaveCount(0);
  await context.close();
});

test('贴图：不重新导入就按原文重排成一个 <p>，复制写的就是它；切回文章与原来逐字一样', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page);
  await pasteSample(page);
  const articlePreview = await page.locator('#contentArea').innerHTML();
  const articleCopy = await copyAndRead(page);
  expect(articleCopy.copied).toContain('<section');

  const imports = [];
  page.on('request', request => { if (/\/api\/(fetch-article|feishu)/.test(request.url())) imports.push(request.url()); });
  await option(page, '贴图').click();
  await expectStickerPreview(page);
  const stickerCopy = await copyAndRead(page);
  expect(stickerCopy.copied).toBe(STICKER_HTML);
  expect(stickerCopy.html).toContain(STICKER_HTML);
  expect(stickerCopy.html).not.toMatch(/<section|style=/);
  expect(stickerCopy.text).toBe(SAMPLE);

  await option(page, '文章').click();
  await expect(page.locator('#contentArea')).toHaveAttribute('data-template', 'fbif');
  await expect.poll(() => page.locator('#contentArea').innerHTML()).toBe(articlePreview);
  const articleAgain = await copyAndRead(page);
  expect(articleAgain.copied).toBe(articleCopy.copied);
  expect(imports).toEqual([]);
  await context.close();
});

test('快速连点只认最后一次；键盘方向键也能切', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page);
  await pasteSample(page);

  await page.evaluate(() => {
    const radio = name => document.querySelector(`#carrierSwitch [data-carrier="${name}"]`);
    radio('sticker').click();
    radio('article').click();
    radio('sticker').click();
  });
  await expectStickerPreview(page);
  await expect(option(page, '贴图')).toHaveAttribute('aria-checked', 'true');

  await option(page, '贴图').focus();
  await page.keyboard.press('ArrowLeft');
  await expect(option(page, '文章')).toBeFocused();
  await expect(option(page, '文章')).toHaveAttribute('aria-checked', 'true');
  await expect(page.locator('#contentArea')).toHaveAttribute('data-template', 'fbif');
  await expect(page.locator('#contentArea section').first()).toBeVisible();
  await context.close();
});

test('刷新后留在贴图，恢复的预览与复制都是贴图；切账号也留在贴图', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page);
  await pasteSample(page);
  await option(page, '贴图').click();
  await expectStickerPreview(page);

  await page.reload({ waitUntil: 'networkidle' });
  await page.waitForFunction(() => window._activeRuleState === 'resolved' && !!document.querySelector('#carrierSwitch [role="radio"]'));
  await expect(option(page, '贴图')).toHaveAttribute('aria-checked', 'true');
  await expectStickerPreview(page);
  const restored = await copyAndRead(page);
  expect(restored.copied).toBe(STICKER_HTML);
  expect(restored.text).toBe(SAMPLE);

  await page.locator('#acctMenu .acct-trigger').click();
  await page.locator('#acctMenu [data-account-id="foodtalks-series"]').click();
  await expect(page).toHaveURL(/foodtalks-series/);
  await expect(page.locator('#acctMenu .acct-trigger')).toHaveAttribute('aria-label', /什么是/);
  await expect(option(page, '贴图')).toHaveAttribute('aria-checked', 'true');
  await expectStickerPreview(page);

  await option(page, '文章').click();
  await expect(page.locator('#contentArea')).toHaveAttribute('data-template', 'foodtalks-series');
  await context.close();
});
