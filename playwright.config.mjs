import { defineConfig } from '@playwright/test';

// 视觉回归（架构体检 S5，2026-07-10）：
// - webServer 自动起本地 Go API（复用已监听的 19090，不新占端口）；
// - toHaveScreenshot 像素基线对比。基线【不入库】（24 张 fullPage 共 ~23M，入库会让
//   git 历史随每次改版重建失控膨胀）——它是开发机上的跨会话本地防线，且像素绑定
//   macOS 字体渲染，不进 CI 部署门禁。
// - 动画一律禁用，降低像素抖动；单测内对日期等易变区用 mask；两个抓取场景已用
//   route 桩确定性化（不打真实外网）。
// 首次/改版后建基线：npm run test:e2e:baseline；日常对比：npm run test:e2e
export default defineConfig({
  testDir: './tests',
  testMatch: '**/e2e-regression.spec.mjs',
  timeout: 120000,
  retries: 0,
  snapshotPathTemplate: '{testDir}/e2e-baselines/{arg}{ext}',
  webServer: {
    command: 'cd api && ADMIN_PASSWORD=local-e2e-only go run . -port 19090',
    url: 'http://localhost:19090/api/health',
    reuseExistingServer: true,
    timeout: 90000,
  },
  expect: {
    toHaveScreenshot: {
      maxDiffPixelRatio: 0.02,
      animations: 'disabled',
    },
  },
  use: {
    baseURL: 'http://localhost:19090',
    screenshot: 'off',
    trace: 'off',
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
  reporter: [['list'], ['json', { outputFile: 'tests/e2e-screenshots/regression-results.json' }]],
});
