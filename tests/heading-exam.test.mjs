// 标题考卷的网站一半（Go 那一半见 api/heading_exam_test.go，收卷见 tools/heading-exam/README.md）：
// 每篇存的正文块按 FoodTalks 网站默认规则排一遍，读出小标题，和 <编号>.answer.txt（编辑改定稿，或用户说的样子）比。
// 还对不上的记在 <编号>.gap.txt；差别一变就失败。看过确认（变好了，或是编辑个例）才用 GOLDEN_UPDATE=1 重写。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, readdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { decode, outlineFromHTML } from '../tools/heading-exam/outline.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const EXAM = path.join(ROOT, 'api/testdata/heading-exam');
const UPDATE = !!process.env.GOLDEN_UPDATE;

const { loadLayoutEngine } = await import(path.join(ROOT, 'render-service/layout.mjs'));
const engine = await loadLayoutEngine({
  publicDir: path.join(ROOT, 'public'),
  fetchRules: account => readFile(path.join(ROOT, `tests/fixtures/layout-parity/rules--${account}.json`), 'utf8'),
  origin: 'http://127.0.0.1:19191',
  build: 'test',
});

const keys = (await readdir(EXAM)).filter(f => f.endsWith('.blocks.json')).map(f => f.slice(0, -'.blocks.json'.length)).sort();

async function layout(key, account) {
  const a = JSON.parse(await readFile(path.join(EXAM, `${key}.blocks.json`), 'utf8'));
  const meta = { title: a.title, source: '', source_name: a.source_name || '' };
  const out = await engine.layout({
    account, title: '', repost: null,
    input: { kind: 'blocks', ...meta, byline: a.byline || '', blocks: a.blocks, fetch_meta: meta },
  });
  return out.html;
}

const siteOutline = async key => outlineFromHTML(await layout(key, 'ft-default'));

// FoodTalks食品资讯的标题1 是序号图 + 标题，序号图的 alt 就是标题（正文里紧挨着的两个标题1 合成一个、空格接起来）。
async function mpTitles(key) {
  return [...(await layout(key, 'foodtalks-mp')).matchAll(/<img\b[^>]*\balt="([^"]+)"/g)].map(m => decode(m[1]));
}

// 网站稿里的标题1（h2），去掉「一、」编号；中间没有字也没有图的两个 h2 合成一个，和 FoodTalks食品资讯一样。
async function siteTitlesAsMp(key) {
  const html = await layout(key, 'ft-default');
  const out = [];
  let prevEnd = -1;
  for (const m of html.matchAll(/<(h[2-4])\b[^>]*>([\s\S]*?)<\/\1>/gi)) {
    const text = decode(m[2].replace(/<[^>]+>/g, '')).replace(/\s+/g, ' ').trim().replace(/^[一二三四五六七八九十]+\s*[、.．]\s*/, '');
    const between = prevEnd < 0 ? null : html.slice(prevEnd, m.index);
    const adjacent = between !== null && !/<img\b/i.test(between) && !decode(between.replace(/<[^>]+>/g, '')).trim();
    if (m[1].toLowerCase() !== 'h2' || !text) { prevEnd = -1; continue; }
    if (adjacent) out[out.length - 1] += ' ' + text;
    else out.push(text);
    prevEnd = m.index + m[0].length;
  }
  return out;
}

// 按顺序对齐两份清单：同一行算对上；同一句只是级别不同算「级别不同」；其余分成编辑有我们没有、我们有编辑没有。
function gapLines(answer, ours) {
  const n = answer.length, m = ours.length;
  const lcs = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) {
    lcs[i][j] = answer[i] === ours[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
  }
  const missing = [], extra = [];
  let i = 0, j = 0;
  while (i < n || j < m) {
    if (i < n && j < m && answer[i] === ours[j]) { i++; j++; continue; }
    if (j >= m || (i < n && lcs[i + 1][j] >= lcs[i][j + 1])) missing.push(answer[i++]);
    else extra.push(ours[j++]);
  }
  const text = line => line.slice(3);
  const out = [];
  for (const line of missing) {
    const k = extra.findIndex(e => text(e) === text(line));
    if (k >= 0) {
      out.push(`级别不同：${text(line)}（编辑 ${line.slice(0, 2)}，我们 ${extra[k].slice(0, 2)}）`);
      extra.splice(k, 1);
    } else {
      out.push(`编辑有、我们没有：${line}`);
    }
  }
  for (const line of extra) out.push(`我们有、编辑没有：${line}`);
  return out;
}

const results = [];
for (const key of keys) {
  test(`标题考卷 ${key}：网站上的小标题与答案的差别没变`, async () => {
    const answer = (await readFile(path.join(EXAM, `${key}.answer.txt`), 'utf8')).split('\n').map(s => s.trim()).filter(s => s && !s.startsWith('#'));
    const gap = gapLines(answer, await siteOutline(key));
    results.push({ key, gap: gap.length });
    const gapPath = path.join(EXAM, `${key}.gap.txt`);
    const encoded = gap.length ? gap.join('\n') + '\n' : '';
    if (UPDATE) {
      await writeFile(gapPath, encoded);
      return;
    }
    const recorded = await readFile(gapPath, 'utf8').catch(() => null);
    assert.ok(recorded !== null, `没有 ${key}.gap.txt：GOLDEN_UPDATE=1 node --test tests/heading-exam.test.mjs 生成后看过再提交`);
    assert.equal(encoded, recorded, `${key} 网站上的小标题和答案的差别变了。看过确认后用 GOLDEN_UPDATE=1 node --test tests/heading-exam.test.mjs 重写`);
  });
}

for (const key of keys) {
  test(`标题考卷 ${key}：FoodTalks食品资讯的大标题和网站的标题1 一样`, async () => {
    assert.deepEqual(await mpTitles(key), await siteTitlesAsMp(key));
  });
}

test('标题考卷：每篇都有答案', async () => {
  for (const key of keys) {
    await assert.doesNotReject(readFile(path.join(EXAM, `${key}.answer.txt`), 'utf8'), `${key} 缺 answer.txt`);
  }
});

test.after(() => {
  const clean = results.filter(r => r.gap === 0).length;
  console.log(`标题考卷：${results.length} 篇里 ${clean} 篇网站上的小标题和答案完全一样`);
});
