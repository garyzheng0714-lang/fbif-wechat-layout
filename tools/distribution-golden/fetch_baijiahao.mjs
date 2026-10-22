// 在 B 机分发服务的浏览器里抓编辑手发在百家号上的正文（百家号对 curl 弹验证码，浏览器里能直接读）。
// 用法：docker cp 到 fbif-distribution-browser:/app/ 后
//   docker exec fbif-distribution-browser node /app/fetch_baijiahao.mjs '{"286":"标题",...}'
// 按标题在作者主页里找文章，结果写 /tmp/bjh.json：{"<编号>": {url, title, html}}。
import { chromium } from 'playwright-core';
import { writeFileSync } from 'node:fs';

const AUTHOR_HOME = 'https://author.baidu.com/home?app_id=1725988015909385';
const wanted = JSON.parse(process.argv[2]);
const squash = (text) => String(text ?? '').replace(/[\s\p{P}\p{S}]+/gu, '');

const browser = await chromium.connectOverCDP('http://127.0.0.1:9222');
const page = await browser.contexts()[0].newPage();
const out = {};
try {
  await page.goto(AUTHOR_HOME, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await page.waitForSelector('div.sfi-article', { timeout: 30000 });
  for (let i = 0; i < 8; i++) {
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await page.waitForTimeout(1500);
  }
  const list = await page.$$eval('div.sfi-article', (items) => items.map((d) => ({ title: d.getAttribute('title'), url: d.getAttribute('url') })));
  for (const [job, title] of Object.entries(wanted)) {
    const hit = list.find((a) => squash(a.title).slice(0, 12) === squash(title).slice(0, 12));
    if (!hit) {
      console.log(job, '百家号上没找到', title);
      continue;
    }
    await page.goto(hit.url, { waitUntil: 'domcontentloaded', timeout: 45000 });
    await page.waitForSelector('[data-testid="article"]', { timeout: 30000 });
    await page.waitForTimeout(1500);
    const html = await page.$eval('[data-testid="article"]', (el) => el.innerHTML);
    const pageTitle = (await page.$eval('h1', (el) => el.innerText).catch(() => hit.title)).trim();
    out[job] = { url: hit.url, title: pageTitle, html };
    console.log(job, '找到', hit.url);
    await page.waitForTimeout(2000);
  }
} finally {
  await page.close();
  writeFileSync('/tmp/bjh.json', JSON.stringify(out));
  process.exit(0);
}
