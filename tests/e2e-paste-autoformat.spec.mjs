/**
 * 「粘贴链接就自动排版」真实浏览器验证（2026-08-06）。
 * 真按 Cmd/Ctrl+V，不模拟事件；断言全程没有点过任何按钮。
 */
import { test, expect } from '@playwright/test';
import { loginAs } from './e2e-login.mjs';
import path from 'path';
import fs from 'fs';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const E2E_USERNAME = 'e2e';
const SHOT_DIR = path.resolve('tests/e2e-screenshots');
fs.mkdirSync(SHOT_DIR, { recursive: true });

const ARTICLE = {
  title: '粘贴即排版：一条链接就够了',
  source_name: 'FBIF食品饮料创新',
  blocks: [
    { k: 'txt', runs: [{ type: 'txt', text: '来源：轻食online' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '作者：RR' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '编辑：TiTi' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '这一段来自被拦截的抓取接口，用来确认排版真的跑完了。' }] },
    { k: 'txt', runs: [{ type: 'txt', text: '第二段正文，验证多段落也正常落到预览区。' }] },
  ],
};

async function newAuthenticatedContext(browser) {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'],
  });
  await loginAs(context, { username: E2E_USERNAME });
  return context;
}

async function openEditor(page, route = '/mp/fbif') {
  await page.route('**/api/fetch-article', r => r.fulfill({
    status: 200,
    contentType: 'application/json; charset=utf-8',
    body: JSON.stringify(ARTICLE),
  }));
  await page.goto(BASE + route, { waitUntil: 'networkidle' });
  await page.waitForSelector('.app-layout');
  await page.waitForFunction(() => typeof window.fetchArticle === 'function');
  // 任何一次真实点击都会让本用例失去意义，直接把点击计数暴露出来。
  await page.evaluate(() => {
    window.__clicks = 0;
    document.addEventListener('click', () => { window.__clicks += 1; }, true);
  });
}

async function pasteInto(page, selector, text) {
  await page.evaluate(value => navigator.clipboard.writeText(value), text);
  await page.locator(selector).focus();
  await page.keyboard.press('ControlOrMeta+V');
}

test('粘贴一条链接后不点任何按钮，文章自动排版出来', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page);

  await pasteInto(page, '#urlInput', 'https://mp.weixin.qq.com/s/paste-auto-format');

  await expect(page.locator('#previewContent')).toContainText(ARTICLE.title, { timeout: 15000 });
  await expect(page.locator('#previewContent')).toContainText('这一段来自被拦截的抓取接口');
  const previewText = await page.locator('#previewContent').innerText();
  expect(previewText).toMatch(/来源：轻食online[\s\S]*作者：RR[\s\S]*编辑：TiTi[\s\S]*这一段来自被拦截的抓取接口/);
  expect(await page.evaluate(() => window.__clicks)).toBe(0);

  await page.screenshot({ path: path.join(SHOT_DIR, 'paste-autoformat-link.png'), fullPage: false });
  await context.close();
});

// 交互层全渠道统一（ADR：能力共用、视觉隔离、交互统一），网站渠道走同一份 bindUrlImportShortcuts。
test('网站渠道粘贴链接同样自动排版', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page, '/ft/default');

  await pasteInto(page, '#urlInput', 'https://www.foodtalks.cn/news/62285');

  await expect(page.locator('#previewContent')).toContainText(ARTICLE.title, { timeout: 15000 });
  expect(await page.evaluate(() => window.__clicks)).toBe(0);

  await page.screenshot({ path: path.join(SHOT_DIR, 'paste-autoformat-site.png'), fullPage: false });
  await context.close();
});

test('粘贴正文同样自动排版，页面上没有导入按钮', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page);

  await pasteInto(page, '#urlInput', '茶饮行业在 2026 年出现了新的分层。\n第二段：低糖产品成为货架主力。');

  await expect(page.locator('#previewContent')).toContainText('茶饮行业在 2026 年出现了新的分层', { timeout: 15000 });
  await expect(page.locator('#previewContent')).toContainText('低糖产品成为货架主力');
  expect(await page.evaluate(() => window.__clicks)).toBe(0);
  await expect(page.locator('#importSubmitBtn')).toHaveCount(0);

  await page.screenshot({ path: path.join(SHOT_DIR, 'paste-autoformat-body.png'), fullPage: false });
  await context.close();
});

// 删掉「导入排版」按钮后，手打输入的键盘路径必须还在（design.md 否决项：键盘无法完成核心操作）。
test('手打正文用 Cmd/Ctrl+Enter 仍能提交', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page);

  await page.locator('#urlInput').focus();
  await page.keyboard.type('手动敲进去的一段正文。');
  await page.keyboard.press('ControlOrMeta+Enter');

  await expect(page.locator('#previewContent')).toContainText('手动敲进去的一段正文', { timeout: 15000 });
  expect(await page.evaluate(() => window.__clicks)).toBe(0);
  await context.close();
});

test('追加粘贴按框里的最终全文重排', async ({ browser }) => {
  const context = await newAuthenticatedContext(browser);
  const page = await context.newPage();
  await openEditor(page);

  await pasteInto(page, '#urlInput', '第一次粘进来的开头段。');
  await expect(page.locator('#previewContent')).toContainText('第一次粘进来的开头段', { timeout: 15000 });

  // 光标停在末尾，再粘一段：预览应同时包含两段。
  await page.locator('#urlInput').press('End');
  await page.keyboard.press('Enter');
  await pasteInto(page, '#urlInput', '第二次追加进来的补充段。');

  await expect(page.locator('#previewContent')).toContainText('第二次追加进来的补充段', { timeout: 15000 });
  await expect(page.locator('#previewContent')).toContainText('第一次粘进来的开头段');
  expect(await page.evaluate(() => window.__clicks)).toBe(0);
  await context.close();
});
