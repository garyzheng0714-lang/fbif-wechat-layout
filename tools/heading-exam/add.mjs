// 把一篇公众号原文收进标题考卷（api/testdata/heading-exam）。用法见同目录 README。
//   node tools/heading-exam/add.mjs <编号> <公众号原文链接> [FoodTalks 正文 JSON]
// 原文去掉脚本、样式后存成 <编号>.html.gz；给了 FoodTalks 管理接口读回的正文（/news/admin/news/{id} 原样），
// 就把编辑改定稿的小标题写成 <编号>.answer.txt，没给就要手写这份答案。
import { writeFile, readFile } from 'node:fs/promises';
import { gzipSync } from 'node:zlib';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { outlineFromHTML } from './outline.mjs';

const EXAM = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../api/testdata/heading-exam');
const [key, link, answerJSON] = process.argv.slice(2);
if (!key || !link) {
  console.error('用法：node tools/heading-exam/add.mjs <编号> <公众号原文链接> [FoodTalks 正文 JSON]');
  process.exit(2);
}

// 任务库里存的长链接是 http + %3D%3D，微信只回「未知错误」；换成 https、参数按 __biz/mid/idx/sn/chksm 排。
function wechatURL(raw) {
  const u = new URL(raw.replace(/^http:/, 'https:'));
  if (u.pathname !== '/s' || !u.searchParams.get('__biz')) return u.toString();
  const q = ['__biz', 'mid', 'idx', 'sn', 'chksm'].filter(k => u.searchParams.get(k))
    .map(k => `${k}=${u.searchParams.get(k)}`);
  return `https://mp.weixin.qq.com/s?${q.join('&')}`;
}

const res = await fetch(wechatURL(link), {
  headers: { 'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36' },
});
const page = await res.text();
if (!page.includes('id="js_content"')) {
  console.error(`抓回来的不是文章页（${res.status}，${page.length} 字节），换个链接再试`);
  process.exit(1);
}
const slim = page
  .replace(/<script\b[\s\S]*?<\/script>/gi, '')
  .replace(/<style\b[\s\S]*?<\/style>/gi, '')
  .replace(/<!--[\s\S]*?-->/g, '');
await writeFile(path.join(EXAM, `${key}.html.gz`), gzipSync(slim, { level: 9 }));
console.log(`原文 → ${key}.html.gz（${Math.round(slim.length / 1024)} KB）`);

if (answerJSON) {
  const body = JSON.parse(await readFile(answerJSON, 'utf8'));
  const content = body && body.data && body.data.content;
  if (!content) {
    console.error('FoodTalks 正文 JSON 里没有 data.content');
    process.exit(1);
  }
  const lines = outlineFromHTML(content);
  await writeFile(path.join(EXAM, `${key}.answer.txt`), lines.join('\n') + '\n');
  console.log(`编辑改定稿的小标题 ${lines.length} 个 → ${key}.answer.txt`);
} else {
  console.log(`没给 FoodTalks 正文：手写 ${key}.answer.txt，一行一个小标题，写成「h2 标题」「h3 标题」`);
}
