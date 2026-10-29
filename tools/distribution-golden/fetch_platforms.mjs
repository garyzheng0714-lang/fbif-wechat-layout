// 抓编辑手发在头条号、百家号上的稿，按正文对上 FoodTalks 稿的任务编号（编辑常改标题，不按标题认）。
// 在 B 机用分发服务的镜像另起一个用完即删的容器跑，不碰分发服务正在用的浏览器、不登录任何号：
//   docker run --rm -m 1200m -v /opt/camoufox:/opt/camoufox:ro -v <本文件>:/app/fetch_platforms.mjs:ro -v <目录>:/out \
//     fbif-distribution-browser:latest node /app/fetch_platforms.mjs 2026-10-14
// <目录> 里先放好 fetch_sources.py 抓的 ft-<编号>.json；跑完多出 tt-<编号>.json、bjh.json（TestDistributionGoldenBuild 的输入）
// 和 posts.json（这段时间两个号发的每一篇：标题、封面、摘要、发文时间、对上的编号）。
// 头条号列表是主页自己发的带签名请求，截下来读；正文用不要签名的 m.toutiao.com 接口。
// 百家号作者主页对新浏览器弹百度安全验证（不绕），列表改用手机版作者页的接口，正文读文章页。
import { Camoufox } from 'camoufox';
import { readdirSync, readFileSync, writeFileSync } from 'node:fs';

const TOUTIAO_HOME = 'https://www.toutiao.com/c/user/token/CidFMKIyBwe8FZJXpzOu-pnDgmowUZ85JYItD0VcDws9xNYgvgDSy88aSQo8AAAAAAAAAAAAAFETq_l43qx0UFg72N8owjqQ3ZvJ_-8B5LfXqtOyPSrP2LVq7zf5uFX9vbG4BRXj9xS7EIiaoA4Yw8WD6gQiAQPq0yn3/?tab=article';
const BAIJIAHAO_UK = 'PZhsbUqOOYpW_7eomOENIw';
const OUT = '/out';
const since = Date.parse(`${process.argv[2]}T00:00:00+08:00`) / 1000;
if (!since) throw new Error('用法：node fetch_platforms.mjs <起始日期 YYYY-MM-DD>');

const text = (html) => String(html ?? '').replace(/<[^>]+>/g, '').replace(/&[a-z#0-9]+;/gi, '').replace(/[\s\p{P}\p{S}]+/gu, '');
const grams = (s) => new Set(Array.from({ length: Math.max(0, s.length - 11) }, (_, i) => s.slice(i, i + 12)));

// 每篇 FoodTalks 稿取正文的 12 字片段，编辑稿里出现得最多、过三成的那篇算对上。
const sources = readdirSync(OUT).filter((n) => /^ft-\d+\.json$/.test(n)).map((n) => {
  const news = JSON.parse(readFileSync(`${OUT}/${n}`, 'utf8'));
  return { job: n.slice(3, -5), title: news.title, grams: grams(text(news.content).slice(0, 3000)) };
});
const match = (html) => {
  const body = text(html);
  let best = { job: '', score: 0 };
  for (const s of sources) {
    let hit = 0;
    for (const g of s.grams) if (body.includes(g)) hit++;
    const score = s.grams.size ? hit / s.grams.size : 0;
    if (score > best.score) best = { job: s.job, score };
  }
  return best.score >= 0.3 ? best : { job: '', score: best.score };
};

const browser = await Camoufox({ headless: true, executable_path: process.env.CAMOUFOX_EXECUTABLE, os: 'windows', locale: 'zh-CN,zh', geoip: false, exclude_addons: ['UBO'], i_know_what_im_doing: true });
const page = await browser.newPage();
await page.route(/\.(mp4|webm)(\?|$)/, (r) => r.abort());
const posts = { toutiao: [], baijiahao: [] };

// 头条号：滚主页文章栏，直到翻到起始日期之前。
const feed = [];
page.on('response', async (res) => {
  if (!res.url().includes('/api/pc/list/user/feed?category=pc_profile_article')) return;
  try {
    feed.push(...((await res.json()).data ?? []));
  } catch {}
});
await page.goto(TOUTIAO_HOME, { waitUntil: 'domcontentloaded', timeout: 60000 });
await page.waitForTimeout(6000);
for (let i = 0; i < 30 && !(feed.length && Math.min(...feed.map((a) => Number(a.publish_time))) < since); i++) {
  await page.mouse.wheel(0, 4000);
  await page.waitForTimeout(2500);
}
for (const item of feed) {
  const id = String(item.group_id ?? item.item_id ?? '');
  if (!id || Number(item.publish_time) < since || posts.toutiao.some((p) => p.id === id)) continue;
  const info = (await (await page.request.get(`https://m.toutiao.com/i${id}/info/v2/`)).json()).data ?? {};
  const cover = item.middle_image?.url ?? item.image_list?.[0]?.url ?? '';
  posts.toutiao.push({
    id, url: `https://www.toutiao.com/article/${id}/`, title: info.title ?? item.title, abstract: item.abstract ?? '',
    published_at: new Date(Number(item.publish_time) * 1000).toISOString(), cover, covers: (item.image_list ?? []).length,
    original: info.is_original ?? null, html: info.content ?? '', ...match(info.content),
  });
  await page.waitForTimeout(800);
}

// 百家号：手机版作者页的动态接口，按 ctime 往前翻。先开过一篇百家号文章页才回列表（只开过百度首页、作者页回空），开的是 #299 的编辑稿。
await page.goto('https://baijiahao.baidu.com/s?id=1879451990379714672', { waitUntil: 'domcontentloaded', timeout: 45000 });
await page.waitForTimeout(3000);
let ctime = '';
for (let pageNo = 0; pageNo < 10; pageNo++) {
  const api = `https://mbd.baidu.com/webpage?tab=article&num=20&uk=${BAIJIAHAO_UK}&type=newhome&action=dynamic&format=jsonp&otherext=h5_20231020&Tenger-Mhor=${ctime ? `&ctime=${ctime}` : ''}`;
  const raw = await (await page.request.get(api, { headers: { referer: 'https://author.baidu.com/' } })).text();
  if (!/^\w+\(/.test(raw)) throw new Error(`百家号作者页接口第 ${pageNo + 1} 页回的不是数据：${raw.slice(0, 120)}`);
  const data = JSON.parse(raw.slice(raw.indexOf('(') + 1, raw.lastIndexOf(')'))).data ?? {};
  const list = data.list ?? [];
  for (const entry of list) {
    const it = entry.itemData ?? {};
    if (it.type !== 'news' || Number(it.publish_at) < since) continue;
    posts.baijiahao.push({
      id: String(it.shoubai_c_articleid ?? ''), url: it.url, title: it.title,
      published_at: new Date(Number(it.publish_at) * 1000).toISOString(),
      cover: it.imgSrc?.[0]?.content_original ?? '', covers: (it.imgSrc ?? []).length, cover_layout: it.cover_layout ?? '',
      first_publish: it.first_exclusive_publish_v2 ?? '', location: [it.iptopoi?.Province, it.iptopoi?.City].filter(Boolean).join(' '),
    });
  }
  const oldest = Math.min(...list.map((e) => Number(e.itemData?.publish_at ?? Infinity)));
  if (!data.hasMore && !data.has_more) break;
  if (!list.length || oldest < since) break;
  ctime = data.query?.ctime ?? list.at(-1)?.dynamic_ctime_raw ?? '';
  if (!ctime) break;
  await page.waitForTimeout(1500);
}
for (const post of posts.baijiahao) {
  await page.goto(post.url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await page.waitForSelector('[data-testid="article"]', { timeout: 30000 });
  await page.waitForTimeout(1500);
  post.html = await page.$eval('[data-testid="article"]', (el) => el.innerHTML);
  post.title = (await page.$eval('h1', (el) => el.innerText).catch(() => post.title)).trim();
  post.summary = await page.evaluate(() => /导读\n([^\n]+)/.exec(document.body.innerText)?.[1] ?? '');
  Object.assign(post, match(post.html));
  await page.waitForTimeout(2000);
}
await browser.close();

const bjh = {};
for (const p of posts.toutiao.filter((p) => p.job)) {
  writeFileSync(`${OUT}/tt-${p.job}.json`, JSON.stringify({ group_id: p.id, title: p.title, content: p.html }));
}
for (const p of posts.baijiahao.filter((p) => p.job)) bjh[p.job] = { url: p.url, title: p.title, html: p.html };
writeFileSync(`${OUT}/bjh.json`, JSON.stringify(bjh));
for (const list of Object.values(posts)) for (const p of list) delete p.html;
writeFileSync(`${OUT}/posts.json`, JSON.stringify(posts, null, 1));
for (const [platform, list] of Object.entries(posts)) {
  for (const p of list) console.log(platform, p.published_at.slice(0, 16), p.job ? `#${p.job}` : `(没对上 ${p.score.toFixed(2)})`, p.title);
}
