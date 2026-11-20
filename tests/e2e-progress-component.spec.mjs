// 导入进度条组件的浏览器级验收（2026-08-05 用户裁定）。
//
// 用户裁定进度必须是导入卡内部的状态行，而不是灰底工作区上一块无圆角白底 + 满卡宽
// 饱和蓝粗条。DOM/CSS 层的契约由 tests/import-progress-component.test.mjs 守；这里验
// 真实渲染下的几何、状态与无障碍表现——这些只有真跑浏览器才看得见。
import { test, expect } from '@playwright/test';
import { loginAs } from './e2e-login.mjs';

const BASE = `http://127.0.0.1:${Number(process.env.E2E_PORT || 19090)}`;
const LOGIN = { username: 'e2e', name: 'E2E 测试账号' };

async function shell(browser, { width = 1440, reducedMotion } = {}) {
  const context = await browser.newContext({
    viewport: { width, height: 900 },
    ...(reducedMotion ? { reducedMotion: 'reduce' } : {}),
  });
  await loginAs(context, LOGIN);
  const page = await context.newPage();
  await page.goto(`${BASE}/mp/fbif`, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => (
    typeof window._handleFile === 'function' && window._activeRuleState === 'resolved'
  ), { timeout: 15000 });
  return { context, page };
}

// 直接把进度 DOM 驱动到指定状态：本用例验的是组件在各状态下的渲染，
// 导入链路本身由 e2e-preview-speed / e2e-feishu-import-progress 覆盖。
async function driveProgress(page, { percent, text, indeterminate = false, complete = false }) {
  await page.evaluate(({ percent, text, indeterminate, complete }) => {
    const root = document.getElementById('progress');
    root.style.display = 'block';
    root.dataset.totalProgress = 'true';
    root.classList.toggle('is-indeterminate', indeterminate);
    root.classList.toggle('is-complete', complete);
    root.setAttribute('aria-valuenow', String(percent));
    document.getElementById('progressFill').style.width = percent + '%';
    document.getElementById('progressText').textContent = text;
    document.getElementById('progressPercent').textContent = percent + '%';
  }, { percent, text, indeterminate, complete });
  await page.waitForTimeout(80);
}

test('进度行落在导入卡内部，并与卡内其它行左右对齐', async ({ browser }) => {
  const { context, page } = await shell(browser);

  // 空闲态：进度不得占位
  await expect(page.locator('#progress'), '空闲态不得显示进度').toBeHidden();

  await driveProgress(page, { percent: 43, text: '正在准备正文图片 12/25…' });
  await expect(page.locator('#progress')).toBeVisible();

  const geo = await page.evaluate(() => {
    const p = document.getElementById('progress');
    const card = document.getElementById('dropZone');
    const actions = document.querySelector('.import-composer-actions');
    const bar = document.querySelector('.progress-bar');
    const r = el => el.getBoundingClientRect();
    return {
      progress: r(p).toJSON(),
      card: r(card).toJSON(),
      actions: r(actions).toJSON(),
      bar: r(bar).toJSON(),
      // 留白由 .import-progress 的 padding-bottom 提供，而 progress 自身的
      // rect 已包含该 padding；因此要用状态行文字的下沿来量呼吸空间。
      status: r(document.querySelector('.progress-status')).toJSON(),
      progressBg: getComputedStyle(p).backgroundColor,
      cardRadius: getComputedStyle(card).borderTopLeftRadius,
      barHeight: r(bar).height,
    };
  });

  // 几何包含关系：进度行必须整体在卡内（曾经它是卡外一块白底）
  expect(geo.progress.top, '进度行上边必须在卡内').toBeGreaterThan(geo.card.top);
  expect(geo.progress.bottom, '进度行下边必须在卡内').toBeLessThanOrEqual(geo.card.bottom);
  expect(geo.progress.left, '进度行左边必须在卡内').toBeGreaterThanOrEqual(geo.card.left);
  expect(geo.progress.right, '进度行右边必须在卡内').toBeLessThanOrEqual(geo.card.right);

  // 底部呼吸空间：状态行文字不得贴死卡片下沿
  expect(
    geo.card.bottom - geo.status.bottom,
    '状态行文字与卡片下沿之间需要留白',
  ).toBeGreaterThanOrEqual(8);

  // 与动作按钮同一条左边界（卡内每行 14px 水平内边距）
  expect(
    Math.abs(geo.bar.left - geo.actions.left),
    '进度条左边界应与动作按钮对齐',
  ).toBeLessThanOrEqual(1.5);
  expect(
    geo.bar.right,
    '进度条不得满卡宽溢出到卡片边缘',
  ).toBeLessThan(geo.card.right - 8);

  // 自身不得有背景（白底由卡片提供）
  expect(geo.progressBg, '进度行不得自带背景色').toMatch(/rgba\(0, 0, 0, 0\)|transparent/);
  // 细条，不是抢眼的粗条
  expect(geo.barHeight, '进度条应为细条').toBeLessThanOrEqual(4);
  await context.close();
});

test('不确定态隐藏百分比，超长文案不挤压百分比', async ({ browser }) => {
  const { context, page } = await shell(browser);

  await driveProgress(page, { percent: 0, text: '飞书正在生成 Word…', indeterminate: true });
  const indeterminate = await page.evaluate(() => {
    const el = document.getElementById('progressPercent');
    return getComputedStyle(el).visibility;
  });
  expect(indeterminate, '分母未知时不得显示会误导的百分比读数').toBe('hidden');
  await expect(page.locator('#progressText')).toContainText('飞书正在生成 Word');

  await driveProgress(page, {
    percent: 61,
    text: '正在准备正文图片 15/25…（一段异常长的状态文案用于检验换行与溢出表现，不应把右侧百分比挤出卡片）',
  });
  const longText = await page.evaluate(() => {
    const card = document.getElementById('dropZone');
    const pct = document.getElementById('progressPercent');
    const txt = document.getElementById('progressText');
    const cs = getComputedStyle(txt);
    return {
      pctRight: pct.getBoundingClientRect().right,
      cardRight: card.getBoundingClientRect().right,
      textLines: Math.round(txt.getBoundingClientRect().height / parseFloat(cs.lineHeight)),
      whiteSpace: cs.whiteSpace,
      pctVisible: getComputedStyle(pct).visibility,
      horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
    };
  });
  expect(longText.pctRight, '百分比必须留在卡内').toBeLessThanOrEqual(longText.cardRight);
  expect(longText.textLines, '状态文案必须单行截断，不得换行挤压百分比').toBe(1);
  expect(longText.whiteSpace).toBe('nowrap');
  expect(longText.pctVisible, '确定态百分比必须可见').toBe('visible');
  expect(longText.horizontalOverflow, '不得产生横向溢出').toBe(false);
  await context.close();
});

test('窄屏 390 无横向溢出，reduced-motion 关闭进度动画', async ({ browser }) => {
  const narrow = await shell(browser, { width: 390 });
  await driveProgress(narrow.page, { percent: 43, text: '正在准备正文图片 12/25…' });
  const narrowState = await narrow.page.evaluate(() => {
    const card = document.getElementById('dropZone');
    const p = document.getElementById('progress');
    return {
      overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
      progressInsideCard: (
        p.getBoundingClientRect().right <= card.getBoundingClientRect().right + 0.5
        && p.getBoundingClientRect().bottom <= card.getBoundingClientRect().bottom + 0.5
      ),
    };
  });
  expect(narrowState.overflow, '窄屏不得横向溢出').toBe(false);
  expect(narrowState.progressInsideCard, '窄屏进度行仍须在卡内').toBe(true);
  await narrow.context.close();

  const reduced = await shell(browser, { reducedMotion: true });
  await driveProgress(reduced.page, { percent: 0, text: '飞书正在生成 Word…', indeterminate: true });
  const anim = await reduced.page.evaluate(() => {
    const cs = getComputedStyle(document.getElementById('progressFill'));
    return { animationName: cs.animationName, transitionDuration: cs.transitionDuration };
  });
  expect(anim.animationName, 'reduced-motion 下不得跑不确定态位移动画').toBe('none');
  expect(parseFloat(anim.transitionDuration), 'reduced-motion 下条宽过渡应取消').toBeLessThan(0.01);
  await reduced.context.close();
});
