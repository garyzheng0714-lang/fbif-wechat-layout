import { test, expect } from '@playwright/test';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const E2E_USERNAME = 'e2e';
const E2E_PASSWORD = 'e2e-local-session-password';

async function login(page) {
  const response = await page.request.post(`${BASE}/auth/password/login`, {
    data: { username: E2E_USERNAME, password: E2E_PASSWORD, remember: false },
  });
  expect(response.ok(), `E2E 登录失败（${response.status()}）`).toBeTruthy();
}

test('公众号和网站入口地址栏不带构建号，旧收藏打开后也会清掉', async ({ page }) => {
  await login(page);

  await page.goto(`${BASE}/mp/fbif`, { waitUntil: 'networkidle' });
  await expect(page.locator('.app-layout')).toBeVisible();
  expect(page.url()).toBe(`${BASE}/mp/fbif`);
  expect(new URL(page.url()).searchParams.has('v')).toBe(false);

  await page.goto(`${BASE}/mp/fbif?v=bff485e863bfd353b13359b04e17e77db0cd5ed3`, { waitUntil: 'networkidle' });
  await expect(page.locator('.app-layout')).toBeVisible();
  expect(page.url()).toBe(`${BASE}/mp/fbif`);

  await page.goto(`${BASE}/ft/ft-default?v=oldbuild`, { waitUntil: 'networkidle' });
  await expect(page.locator('.app-layout')).toBeVisible();
  expect(page.url()).toBe(`${BASE}/ft/ft-default`);
});
