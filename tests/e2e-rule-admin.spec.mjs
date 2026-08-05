/**
 * 规则后台端到端覆盖
 *
 * 背景（2026-08-05）：e2e-regression 的「7-规则后台视图」用的是无 rule_admin 角色的
 * 密码账号，入口不可见时整段 `if (adminEntryVisible)` 直接跳过——后台从未被端到端
 * 验证过，规则改动可以在测试全绿的情况下把后台改坏。这个文件补上真正的覆盖：
 * 提权到管理员、打开后台、走完保存链路，并检查权限门对普通账号仍然关着。
 *
 * Run: npx playwright test tests/e2e-rule-admin.spec.mjs --reporter=list
 */

import { test, expect } from '@playwright/test';
import fs from 'fs';
import path from 'path';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const AUTH_USERS_PATH = path.resolve('test-results/e2e-data/auth-users.json');

const ADMIN = { username: 'e2e-admin', password: 'e2e-local-admin-password' };
const PLAIN = { username: 'e2e', password: 'e2e-local-session-password' };

// 密码账号配置里没有角色字段，角色住在用户库 JSON 里。首次登录写入记录后把它改成
// admin，用户库每次读盘，重新登录即可拿到 rule_admin。生产的超级管理员仍然只能来自
// RULE_SUPER_ADMIN_OPEN_IDS，这里提到的 admin 拿不到 rule_super_admin。
async function signIn(context, credentials) {
  const response = await context.request.post(`${BASE}/auth/password/login`, {
    data: { ...credentials, remember: false },
  });
  if (!response.ok()) throw new Error(`登录失败（${response.status()}）：${await response.text()}`);
}

async function promoteToRuleAdmin(context) {
  await signIn(context, ADMIN);
  for (let attempt = 0; attempt < 40; attempt++) {
    if (fs.existsSync(AUTH_USERS_PATH)) break;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  const doc = JSON.parse(fs.readFileSync(AUTH_USERS_PATH, 'utf8'));
  const target = doc.users.find(user => user.username === ADMIN.username || user.id === `password:${ADMIN.username}`);
  if (!target) throw new Error(`用户库里没有 ${ADMIN.username}：${JSON.stringify(doc.users.map(u => u.id))}`);
  if (target.role !== 'admin') {
    target.role = 'admin';
    fs.writeFileSync(AUTH_USERS_PATH, JSON.stringify(doc, null, 2));
  }
  await signIn(context, ADMIN);
  const me = await (await context.request.get(`${BASE}/api/me`)).json();
  expect(me.user?.capabilities?.rule_admin, '提权后必须拿到 rule_admin').toBe(true);
}

async function openAdminPanel(page) {
  await page.goto(`${BASE}/mp/fbif`, { waitUntil: 'networkidle' });
  await page.waitForSelector('.app-layout', { timeout: 15000 });
  await page.locator('.rail-foot .user-identity-trigger').click();
  const entry = page.locator('.user-identity-menu #adminLockBtn');
  await expect(entry, '管理员必须能在身份菜单里看到规则后台入口').toBeVisible();
  await entry.click();
  await expect(page.locator('.admin-dialog')).toBeVisible({ timeout: 15000 });
}

function collectPageProblems(page, sink) {
  page.on('console', msg => {
    if (msg.type() !== 'error') return;
    // 与下方 response 豁免对齐：/api/feishu/chats 对密码账号 403 是既定降级路径，
    // 浏览器仍会为它记一条 Failed to load resource 的 console error，不算问题。
    const sourceUrl = (msg.location() && msg.location().url) || '';
    if (sourceUrl.includes('/api/feishu/chats')) return;
    sink.push(`[console] ${msg.text().slice(0, 200)}`);
  });
  page.on('pageerror', error => sink.push(`[pageerror] ${String(error).slice(0, 200)}`));
  page.on('response', response => {
    // 飞书群列表对密码账号返回 403 是既定降级路径，界面会提示改用飞书登录。
    if (response.status() >= 400 && !response.url().includes('/api/feishu/chats')) {
      sink.push(`[HTTP ${response.status()}] ${response.request().method()} ${response.url().replace(BASE, '').slice(0, 100)}`);
    }
  });
}

async function gotoSection(page, name) {
  await page.locator('.admin-dialog button', { hasText: new RegExp(`^${name}$`) }).first().click();
  await expect(page.locator('.admin-dialog')).toBeVisible();
}

// 「正文字号」这类步进字段没有稳定的 id/name，按标签文字定位到同一控件组。
async function stepperValue(page, label) {
  return page.evaluate(text => {
    for (const el of document.querySelectorAll('.admin-dialog *')) {
      if (el.children.length && (el.textContent || '').trim().startsWith(text)) {
        const input = el.querySelector('input');
        if (input) return input.value;
      }
    }
    return null;
  }, label);
}

async function bumpStepper(page, label) {
  return page.evaluate(text => {
    for (const el of document.querySelectorAll('.admin-dialog *')) {
      if (el.children.length && (el.textContent || '').trim().startsWith(text)) {
        const plus = [...el.querySelectorAll('button')].find(b => (b.textContent || '').trim() === '+');
        const input = el.querySelector('input');
        if (plus && input) { plus.click(); return input.value; }
      }
    }
    return null;
  }, label);
}

test.describe('规则后台 — 端到端覆盖', () => {
  test('普通账号看不到规则后台入口，后台接口也拒绝', async ({ browser }) => {
    const context = await browser.newContext();
    await signIn(context, PLAIN);
    const me = await (await context.request.get(`${BASE}/api/me`)).json();
    expect(me.user?.capabilities?.rule_admin, '普通账号不得有 rule_admin').toBeFalsy();

    const forbidden = await context.request.get(`${BASE}/api/rules/admin/presets`);
    expect(forbidden.status(), '普通账号读后台规则必须被拒').toBeGreaterThanOrEqual(400);

    const page = await context.newPage();
    await page.goto(`${BASE}/mp/fbif`, { waitUntil: 'networkidle' });
    await page.waitForSelector('.app-layout', { timeout: 15000 });
    await page.locator('.rail-foot .user-identity-trigger').click();
    await expect(page.locator('.user-identity-menu #adminLockBtn')).toBeHidden();
    await context.close();
  });

  test('管理员打开后台：四个分区都渲染，无脚本错误', async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    await promoteToRuleAdmin(context);
    const page = await context.newPage();
    const problems = [];
    collectPageProblems(page, problems);

    await openAdminPanel(page);
    for (const section of ['账号', '规则', '素材库', '发布链路']) {
      await gotoSection(page, section);
      const bodyText = await page.locator('.admin-dialog').innerText();
      expect(bodyText.replace(/\s+/g, '').length, `「${section}」分区不得是空白`).toBeGreaterThan(40);
    }
    expect(problems, `后台不得有脚本错误或失败请求：${problems.join(' | ')}`).toEqual([]);
    await context.close();
  });

  test('保存链路：改参数 → 样例更新 → 保存并应用 → 重开仍是新值', async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    await promoteToRuleAdmin(context);
    const page = await context.newPage();
    const problems = [];
    collectPageProblems(page, problems);

    await openAdminPanel(page);
    await gotoSection(page, '规则');

    const before = await stepperValue(page, '正文字号');
    expect(before, '必须能读到正文字号').not.toBeNull();

    const sampleFontSize = () => page.evaluate(() => {
      const p = document.querySelector('.admin-live-paper .admin-live-p');
      return p ? getComputedStyle(p).fontSize : null;
    });
    const sampleBefore = await sampleFontSize();
    expect(sampleBefore, '右侧排版样例必须存在').not.toBeNull();

    const after = await bumpStepper(page, '正文字号');
    expect(Number(after), '点 + 必须让字号加一').toBe(Number(before) + 1);
    await expect.poll(sampleFontSize, { message: '样例必须跟着参数即时更新' }).not.toBe(sampleBefore);

    await page.locator('.admin-dialog button', { hasText: /保存并应用/ }).last().click();
    await expect(page.locator('.admin-dialog')).toContainText(/已保存并应用/, { timeout: 15000 });

    await page.reload({ waitUntil: 'networkidle' });
    await openAdminPanel(page);
    await gotoSection(page, '规则');
    expect(await stepperValue(page, '正文字号'), '保存后的值必须持久化').toBe(after);

    // 复原，避免给后续用例留下改过的基线。
    await page.evaluate(() => {
      for (const el of document.querySelectorAll('.admin-dialog *')) {
        if (el.children.length && (el.textContent || '').trim().startsWith('正文字号')) {
          const minus = [...el.querySelectorAll('button')].find(b => (b.textContent || '').trim() === '−');
          if (minus) { minus.click(); return; }
        }
      }
    });
    await page.locator('.admin-dialog button', { hasText: /保存并应用/ }).last().click();
    await expect(page.locator('.admin-dialog')).toContainText(/已保存并应用/, { timeout: 15000 });
    expect(problems, `保存链路不得有脚本错误或失败请求：${problems.join(' | ')}`).toEqual([]);
    await context.close();
  });

  test('复制当前规则：生成可区分的新规则并持久化', async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    await promoteToRuleAdmin(context);
    const page = await context.newPage();
    await openAdminPanel(page);
    await gotoSection(page, '规则');

    const selector = page.locator('.admin-dialog select').first();
    const before = await selector.locator('option').allTextContents();
    await page.locator('.admin-dialog button', { hasText: '复制当前规则' }).first().click();
    await expect(page.locator('.admin-dialog')).toContainText(/已复制为新规则/, { timeout: 15000 });

    const after = await selector.locator('option').allTextContents();
    expect(after.length, '复制必须新增一条规则').toBe(before.length + 1);

    // 名称必须能和原规则区分开：下拉里两条一模一样的名字，用户无从判断在编辑哪条。
    const names = after.map(name => name.replace(/\s*·\s*正在使用\s*$/, '').trim());
    expect(new Set(names).size, `复制出的规则不得与原规则同名：${names.join(' / ')}`).toBe(names.length);

    await page.reload({ waitUntil: 'networkidle' });
    await openAdminPanel(page);
    await gotoSection(page, '规则');
    expect((await page.locator('.admin-dialog select').first().locator('option').allTextContents()).length,
      '复制结果必须持久化').toBe(after.length);
    await context.close();
  });

  test('底部结构：区块可渲染并能上下调序', async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    await promoteToRuleAdmin(context);
    const page = await context.newPage();
    const problems = [];
    collectPageProblems(page, problems);

    await openAdminPanel(page);
    await gotoSection(page, '规则');
    await page.locator('.admin-dialog button', { hasText: /^底部$/ }).first().click();

    const editor = page.locator('.admin-dialog [data-role="block-editor"]');
    await expect(editor, '底部结构必须有区块编辑器').toBeVisible();
    const cards = editor.locator('[data-id]');
    const count = await cards.count();
    expect(count, '页脚默认结构不得为空').toBeGreaterThan(1);

    const labelOf = async (index) => (await cards.nth(index).innerText()).replace(/\s+/g, ' ').trim().slice(0, 40);
    const firstBefore = await labelOf(0);
    const secondBefore = await labelOf(1);

    await cards.nth(1).locator('button', { hasText: '↑' }).first().click();
    await expect.poll(async () => labelOf(0)).toBe(secondBefore);
    expect(await labelOf(1), '被换下去的区块必须落到第二位').toBe(firstBefore);

    expect(problems, `底部结构编辑不得报错：${problems.join(' | ')}`).toEqual([]);
    await context.close();
  });

  test('网站渠道后台可打开且认得当前账号', async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    await promoteToRuleAdmin(context);
    const page = await context.newPage();
    const problems = [];
    collectPageProblems(page, problems);

    await page.goto(`${BASE}/ft/ft-default`, { waitUntil: 'networkidle' });
    await page.waitForSelector('.app-layout', { timeout: 15000 });
    await page.locator('.rail-foot .user-identity-trigger').click();
    const entry = page.locator('.user-identity-menu #adminLockBtn');
    await expect(entry).toBeVisible();
    await entry.click();
    await expect(page.locator('.admin-dialog')).toBeVisible({ timeout: 15000 });
    await expect(page.locator('.admin-dialog'), '后台副标题必须落在网站账号上').toContainText(/网站/);

    expect(problems, `网站渠道后台不得报错：${problems.join(' | ')}`).toEqual([]);
    await context.close();
  });
});
