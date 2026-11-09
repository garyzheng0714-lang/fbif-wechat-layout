// 抓编辑手发在腾讯号上的稿（腾讯新闻上的文章页，不登录）。文章由人给：<编号>=<文章链接>
// （2026-11-09 是从腾讯新闻作者页的公开接口 i.news.qq.com/getSubNewsMixedList?guestSuid=8QMc3X9Y6YIavT7d 翻出 FBIF 腾讯号发的文章，按标题对上编号）。
// 文章页 HTML 里的 window.DATA 带着正文（originContent.text）、图（originAttribute）和作者（card.suid），普通 HTTP 就拿得到，在本机跑：
//   node tools/distribution-golden/fetch_tencent.mjs <目录> 358=https://news.qq.com/rain/a/20261109A0891V00 …
// 每篇写出 <目录>/tx-<编号>.json（TestDistributionGoldenBuild 的输入）：标题、正文、发文时间，和正文以外的设置（settings：分类、目录、声明……）。
// 正文按读者在腾讯新闻上看到的写：图在原文里是 <!--IMG_n--> 占位，换成 <img>；图片组件（data-widget="image"）里图下面的灰色小字就是腾讯号的图注，
// 写成 figure 里的 figcaption；去掉页面样式和视频截图上的播放图标。腾讯自己插的标记（广告位、段落位、实体词链接、小标题锚点）都是注释，生成样稿时去掉。
// 作者不是 FBIF 腾讯号的不写，报出来。
import { writeFileSync } from 'node:fs';

const FBIF = '8QMc3X9Y6YIavT7d'; // FBIF食品饮料创新 在腾讯新闻上的作者编号
const UA = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36';
const [out, ...args] = process.argv.slice(2);
const wanted = args.map((arg) => {
  const [job, url] = arg.split('=');
  const id = /\/a\/([0-9A-Z]{16})\b/.exec(url ?? '')?.[1];
  if (!/^\d+$/.test(job) || !id) throw new Error(`认不出「${arg}」：要写成 <编号>=https://news.qq.com/rain/a/<文章编号>`);
  return { job, id };
});
if (!out || !wanted.length) throw new Error('用法：node fetch_tencent.mjs <目录> <编号>=<腾讯新闻文章链接> …');

const escapeAttr = (s) => s.replace(/&/g, '&amp;').replace(/"/g, '&quot;');

// readerHTML 正文按读者看到的写：图换成 img、图片组件里的图注写成 figcaption。
function readerHTML(text, images) {
  const img = (key) => {
    const src = images[key]?.origUrl ?? images[key]?.imgurl0;
    if (!src) throw new Error(`正文里的 ${key} 在 originAttribute 里没有`);
    return `<img src="${escapeAttr(src)}">`;
  };
  return text
    .replace(/<style[\s\S]*?<\/style>/g, '')
    .replace(/<svg[\s\S]*?<\/svg>/g, '')
    .replace(
      /<span[^>]*data-widget="image"[^>]*>\s*<!--(IMG_\d+)-->\s*(?:<span[^>]*>\s*<!--NO_READ_BEGIN-->([\s\S]*?)<!--NO_READ_END-->\s*<\/span>)?\s*<\/span>/g,
      (_, key, caption) => `<figure>${img(key)}${caption?.trim() ? `<figcaption>${caption.trim()}</figcaption>` : ''}</figure>`,
    )
    .replace(/<!--(IMG_\d+)-->/g, (_, key) => img(key));
}

for (const { job, id } of wanted) {
  const url = `https://news.qq.com/rain/a/${id}`;
  const page = await (await fetch(url, { headers: { 'user-agent': UA } })).text();
  const raw = /window\.DATA\s*=\s*(\{.*?\});\s*\n/s.exec(page)?.[1];
  if (!raw) throw new Error(`#${job} ${url} 页面里没有 window.DATA，腾讯新闻可能改了页面`);
  const data = JSON.parse(raw);
  if (data.card?.suid !== FBIF) {
    console.log(`#${job} ${url} 作者是 ${data.card?.chlname ?? '不知道'}，不是 FBIF，不收`);
    continue;
  }
  const { originContent, originAttribute, ...settings } = data;
  if (!originContent?.text) throw new Error(`#${job} ${url} 没有正文`);
  const content = readerHTML(originContent.text, originAttribute ?? {});
  writeFileSync(`${out}/tx-${job}.json`, JSON.stringify({ id, url, title: data.title, content, created: data.pubtime, settings: { ...settings, directory: originContent.directory } }));
  console.log(`#${job} ${data.title}（${data.pubtime}）`);
  await new Promise((r) => setTimeout(r, 1500 + Math.random() * 1500));
}
