import { defineConfig } from '@playwright/test';

const e2ePort = Number(process.env.E2E_PORT || 19090);
const baseURL = `http://127.0.0.1:${e2ePort}`;
const crossBrowserEnabled = process.env.E2E_CROSS_BROWSER === '1';
const smokeTests = /(?:1-初始空状态|2-上传DOCX全流程|8-网站渠道视图) \[desktop-1440\]/;
const e2ePasswordUsers = Buffer.from(JSON.stringify([
  {
    username: 'e2e-editor',
    password: 'e2e-local-only-password',
    name: 'E2E 编辑',
  },
])).toString('base64');

const projects = [
  {
    name: 'chromium',
    use: { browserName: 'chromium' },
  },
];

// 本地视觉基线绑定 Chromium/macOS 字体渲染。CI 额外用 Firefox/WebKit
// 跑桌面关键路径，验证解析、上传与渠道入口没有浏览器专属故障。
if (crossBrowserEnabled) {
  projects.push(
    {
      name: 'firefox-smoke',
      grep: smokeTests,
      use: { browserName: 'firefox' },
    },
    {
      name: 'webkit-smoke',
      grep: smokeTests,
      use: { browserName: 'webkit' },
    },
  );
}

// 视觉回归（架构体检 S5，2026-07-10）：
// - webServer 自动起当前工作树的 Go API；默认不复用已有端口，避免静默测到别的 worktree。
// - toHaveScreenshot 像素基线对比。基线【不入库】（24 张页面截图共 ~23M，入库会让
//   git 历史随每次改版重建失控膨胀）——它是开发机上的跨会话本地防线，且像素绑定
//   macOS 字体渲染，不进 CI 部署门禁。
// - 动画一律禁用，降低像素抖动；单测内对日期等易变区用 mask；两个抓取场景已用
//   route 桩确定性化（不打真实外网）。
// - CI 用 E2E_SKIP_VISUAL=1 跑完整交互与溢出检查；像素基线仍绑定本机渲染。
// 首次/改版后建基线：npm run test:e2e:baseline；日常对比：npm run test:e2e。
// 默认 updateSnapshots=none，干净 checkout 缺基线会失败，不会拿候选自己当基线。
export default defineConfig({
  testDir: './tests',
  testMatch: '**/e2e-regression.spec.mjs',
  timeout: 120000,
  retries: process.env.CI ? 1 : 0,
  outputDir: 'test-results/e2e',
  updateSnapshots: process.env.E2E_UPDATE_BASELINE === '1' ? 'all' : 'none',
  snapshotPathTemplate: '{testDir}/e2e-baselines/{arg}{ext}',
  webServer: {
    command: `cd api && ADMIN_PASSWORD=local-e2e-only AUTH_SESSION_SECRET=e2e-local-session-secret-at-least-32-bytes AUTH_PASSWORD_USERS_B64=${e2ePasswordUsers} go run . -port ${e2ePort}`,
    url: `${baseURL}/api/health`,
    reuseExistingServer: false,
    timeout: 90000,
  },
  expect: {
    toHaveScreenshot: {
      maxDiffPixelRatio: 0.02,
      animations: 'disabled',
    },
  },
  use: {
    baseURL,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    video: process.env.CI ? 'retain-on-failure' : 'off',
  },
  projects,
  reporter: [['list'], ['json', { outputFile: 'tests/e2e-screenshots/regression-results.json' }]],
});
