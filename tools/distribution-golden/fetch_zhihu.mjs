// 抓编辑手发在知乎号上的稿（知乎专栏文章）。知乎不登录不让搜、作者文章列表要登录，所以文章由人给：<编号>=<文章链接>
// （2026-11-09 是按标题在百度 site:zhuanlan.zhihu.com 搜出来的）。在 B 机用分发服务的镜像另起一个用完即删的容器跑，
// 不碰分发服务正在用的浏览器、不登录任何号；文章页先过知乎自己的 JS 校验（zse-ck，不是验证码），真浏览器自己就过了：
//   docker run --rm -m 1200m -v /opt/camoufox:/opt/camoufox:ro -v <本文件>:/app/fetch_zhihu.mjs:ro -v <目录>:/out \
//     fbif-distribution-browser:latest node /app/fetch_zhihu.mjs 345=https://zhuanlan.zhihu.com/p/2098040357401507691 …
// 每篇写出 <目录>/zh-<编号>.json（TestDistributionGoldenBuild 的输入）：文章页 js-initialData 里这篇的标题、正文 HTML、发文时间，
// 和正文以外的设置（settings：封面、话题、专栏、转载与评论设置、创作声明……）。
// 作者不是 FBIF 机构号的不写，报出来。
import { Camoufox } from 'camoufox';
import { writeFileSync } from 'node:fs';

const OUT = '/out';
const AUTHOR = 'fbifshi-pin-yin-liao-chuang-xin-92'; // https://www.zhihu.com/org/fbifshi-pin-yin-liao-chuang-xin-92
const wanted = process.argv.slice(2).map((arg) => {
  const [job, url] = arg.split('=');
  const id = /zhuanlan\.zhihu\.com\/p\/(\d+)/.exec(url ?? '')?.[1];
  if (!/^\d+$/.test(job) || !id) throw new Error(`认不出「${arg}」：要写成 <编号>=https://zhuanlan.zhihu.com/p/<文章编号>`);
  return { job, id };
});
if (!wanted.length) throw new Error('用法：node fetch_zhihu.mjs <编号>=<知乎文章链接> …');

const browser = await Camoufox({ headless: true, executable_path: process.env.CAMOUFOX_EXECUTABLE, os: 'windows', locale: 'zh-CN,zh', geoip: false, exclude_addons: ['UBO'], i_know_what_im_doing: true });
const page = await browser.newPage();
await page.route(/\.(mp4|webm)(\?|$)/, (r) => r.abort());
for (const { job, id } of wanted) {
  const url = `https://zhuanlan.zhihu.com/p/${id}`;
  await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 60000 });
  await page.waitForSelector('#js-initialData', { state: 'attached', timeout: 30000 });
  const data = JSON.parse(await page.locator('#js-initialData').textContent());
  const article = data.initialState?.entities?.articles?.[id];
  if (!article?.content) throw new Error(`#${job} ${url} 页面里没有这篇的正文，知乎可能改了页面`);
  const author = article.author?.urlToken ?? article.author?.url_token ?? '';
  if (author !== AUTHOR) {
    console.log(`#${job} ${url} 作者是 ${author || '不知道'}，不是 FBIF，不收`);
    continue;
  }
  // 正文以外编辑在发文页上定的：封面、话题、专栏、转载与评论设置、创作声明等，一并留下，比对排版时也看这些。
  const { content, ...settings } = article;
  writeFileSync(`${OUT}/zh-${job}.json`, JSON.stringify({ id, url, title: article.title, content, created: article.created, settings }));
  console.log(`#${job} ${article.title}（${new Date(article.created * 1000).toISOString().slice(0, 10)}）`);
  await page.waitForTimeout(2000 + Math.random() * 2000);
}
await browser.close();
