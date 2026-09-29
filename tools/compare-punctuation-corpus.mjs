#!/usr/bin/env node
// 本地迁移审计，不访问 CMS、不写原稿。运行：node tools/compare-punctuation-corpus.mjs
// 写代码前确认的失效点：缺快照/旧算法/新 policy 不得报空成功；同稿多份快照
// 不重复计数；HTML 实体须解码、嵌套单元格不能重复算；产物中的原文必须转义。
// 已排版 HTML 无法重建原始 DOCX/Markdown 的 run 边界，不能证明入口完全等价。
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';
import { DOMParser, parseHTML } from 'linkedom';

const ROOT = fileURLToPath(new URL('../', import.meta.url));
const BASELINE_REF = '4e6e911';
const BASELINE_ROOT = path.join(ROOT, 'docs/agents/baselines');
const OUTPUT_ROOT = path.join(ROOT, 'artifacts/punctuation-review/corpus');
const BLOCK_SELECTOR = 'p,h1,h2,h3,h4,h5,h6,td,th';
const LIMITATIONS = [
  '样本是本机已有的已排版 HTML 快照，不是未经处理的原始稿。',
  '按文章 ID 去重，优先 cms-content.html，其次前台快照、job-content.html 和其他 HTML。',
  '逐段提取 p/h1–h6/td/th 的可见文字；有段落子元素的容器不重复计数。',
  '此处比较整段纯文本算法，不复原历史 run 边界、链接保护、样式、完整渲染或发布链路。',
  '默认共享口径仅作对照，不表示应将网站切成默认口径。差异为零也不证明原稿迁移完全等价。',
  '默认算法迁移单独比较固定基线与当前 convertText；计数表示输出漂移，是否缺陷须逐项核对。',
  '所有含稿件内容的结果只写入已被 Git 忽略的 artifacts/，不修改、提交或发布原稿。',
];

const sha256 = text => createHash('sha256').update(text).digest('hex');
const relative = filename => path.relative(ROOT, filename).split(path.sep).join('/');
const escape = value => String(value).replace(/[&<>"']/g, char => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[char]));

async function htmlFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const filename = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await htmlFiles(filename));
    else if (entry.isFile() && entry.name.endsWith('.html')) files.push(filename);
  }
  return files.sort();
}

function snapshotRank(filename) {
  const name = path.basename(filename);
  if (name === 'cms-content.html') return 0;
  if (name === 'live-article-content.html') return 1;
  if (name === 'job-content.html') return 2;
  return 3;
}

function selectSnapshots(files) {
  const grouped = new Map();
  for (const filename of files) {
    const articleId = relative(filename).match(/(?:^|\/)(\d{5,})(?=[-/.])/u)?.[1];
    if (!articleId) continue;
    const choices = grouped.get(articleId) || [];
    choices.push(filename);
    grouped.set(articleId, choices);
  }
  return [...grouped].sort(([a], [b]) => Number(a) - Number(b)).map(([articleId, choices]) => {
    choices.sort((a, b) => snapshotRank(a) - snapshotRank(b) || a.localeCompare(b, 'en'));
    return { articleId, filename: choices[0], alternateSnapshots: choices.slice(1).map(relative) };
  });
}

function loadOldWebsiteConverter() {
  const source = execFileSync('git', ['show', `${BASELINE_REF}:public/js/templates/foodtalks-site.js`], {
    cwd: ROOT, encoding: 'utf8', maxBuffer: 1024 * 1024,
  });
  const start = source.indexOf('function isCjk(ch) {');
  const end = source.indexOf('\nfunction isImageCaption(', start);
  if (start < 0 || end <= start) throw new Error('无法从固定基线提取网站旧算法，请人工核对基线。');
  const code = source.slice(start, end);
  if (!code.includes('function normalizeChinesePunctuation(')) throw new Error('固定基线缺少旧标点算法。');
  const convert = vm.runInNewContext(`${code}\nnormalizeChinesePunctuation;`, {}, { timeout: 1000 });
  if (typeof convert !== 'function') throw new Error('网站旧算法提取结果不是函数。');
  return { convert, sha256: sha256(code) };
}

function loadOldDefaultConverter() {
  const source = execFileSync('git', ['show', `${BASELINE_REF}:public/js/punctuation.js`], {
    cwd: ROOT, encoding: 'utf8', maxBuffer: 1024 * 1024,
  });
  const code = source.replace(/^export function /gm, 'function ');
  const convert = vm.runInNewContext(`${code}\nconvertText;`, {}, { timeout: 1000 });
  if (typeof convert !== 'function') throw new Error('固定基线缺少默认标点算法。');
  return { convert, sha256: sha256(source) };
}

function paragraphText(element) {
  const clone = element.cloneNode(true);
  for (const unsafe of clone.querySelectorAll('script,style,template')) unsafe.remove();
  for (const br of clone.querySelectorAll('br')) br.replaceWith('\n');
  return clone.textContent.replace(/\r\n?/g, '\n').trim();
}

function paragraphs(html) {
  const { document } = parseHTML(html);
  return [...document.querySelectorAll(BLOCK_SELECTOR)]
    .filter(element => !element.querySelector(BLOCK_SELECTOR))
    .map((element, index) => ({ index: index + 1, tag: element.localName, text: paragraphText(element) }))
    .filter(item => item.text);
}

function differenceKinds(text, oldText, nextText) {
  if (oldText === nextText) return [];
  const kinds = [];
  if (/[()（）]/u.test(text)) kinds.push('括号成对处理');
  if (/\.{3,}|-{2,}/u.test(text)) kinds.push('连续省略号或破折号');
  if (/https?:\/\/|`|<[^<>]+>|\b[\w.+-]+@[\w-]+\./u.test(text)) kinds.push('受保护文本');
  if (/(?:^|\n)[ \t\u3000\u00a0]*\d{1,3}\.(?!\d)/u.test(text)) kinds.push('行首序号');
  return kinds.length ? kinds : ['其他上下文差异，需核对'];
}

function reportHTML(report) {
  const counts = report.counts;
  const rows = report.articles.map(article => {
    const differences = article.differences.map(diff => `<tr>
<th scope="row">${escape(diff.tag)} #${diff.index}<br>${diff.siteChanged ? '网站迁移变化' : ''}${diff.defaultRegressionChanged ? '<br>默认算法迁移变化' : ''}${!diff.siteChanged && !diff.defaultRegressionChanged ? '仅渠道口径不同' : ''}<br>${escape(diff.kinds.join('、'))}</th>
<td>${escape(diff.sourceText)}</td><td>${escape(diff.oldWebsite)}</td>
<td>${escape(diff.sharedWebsite)}</td><td>${escape(diff.oldDefaultText)}</td><td>${escape(diff.sharedDefault)}</td></tr>`).join('\n');
    return `<details${article.siteChanged || article.defaultRegressionChanged ? ' open' : ''}><summary>文章 ${escape(article.articleId)} · ${article.paragraphs} 段 · 网站迁移变化 ${article.siteChanged} · 默认算法迁移变化 ${article.defaultRegressionChanged} · 新渠道口径差异 ${article.defaultChanged}</summary>
<p>${escape(article.source)}</p>${differences ? `<table><thead><tr><th>位置</th><th>快照原文</th><th>旧网站算法</th><th>新网站口径</th><th>旧默认算法</th><th>新默认算法</th></tr></thead><tbody>${differences}</tbody></table>` : '<p>未发现差异。</p>'}</details>`;
  }).join('\n');
  return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>标点迁移审计</title>
<body><h1>标点迁移审计</h1><p>${counts.articles} 篇快照，${counts.paragraphs} 个非空文本块；网站迁移变化 ${counts.siteChanged} 段，默认算法迁移变化 ${counts.defaultRegressionChanged} 段，新渠道口径差异 ${counts.defaultChanged} 段。</p>
<h2>样本与限制</h2><ul>${LIMITATIONS.map(text => `<li>${escape(text)}</li>`).join('')}</ul>
<p>旧算法：${escape(BASELINE_REF)}；当前工作区 HEAD：${escape(report.head)}；生成时间：${escape(report.generatedAt)}。</p>
<h2>逐稿对比</h2>${rows}</body></html>\n`;
}

async function main() {
  const files = await htmlFiles(BASELINE_ROOT);
  const snapshots = selectSnapshots(files);
  if (!snapshots.length) throw new Error('本机没有可审计的文章 HTML 快照，不能生成通过结论。');
  const old = loadOldWebsiteConverter();
  const oldDefault = loadOldDefaultConverter();
  // 与 render-service 同款最小浏览器环境；导入模板时规则/CSS 请求只返回离线失败。
  // 审计不需要网络，覆盖 fetch 后直到进程退出都不恢复外网访问。
  const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };
  globalThis.window = { __APP_BUILD_INFO: { build: 'punctuation-audit' }, localStorage: storage };
  globalThis.localStorage = storage;
  globalThis.DOMParser = DOMParser;
  globalThis.fetch = async () => ({ ok: false, text: async () => '', json: async () => ({}) });
  const [{ convertText }, { default: template }] = await Promise.all([
    import('../public/js/punctuation.js'), import('../public/js/templates/foodtalks-site.js'),
  ]);
  if (!template.punctuation || template.punctuation.context !== 'adjacent') {
    throw new Error('网站模板尚未声明共享标点 policy，不能使用默认转换器冒充网站迁移审计。');
  }
  const articles = [];
  for (const snapshot of snapshots) {
    const html = await readFile(snapshot.filename, 'utf8');
    const blocks = paragraphs(html);
    if (!blocks.length) throw new Error(`快照没有可比较的文本块：${relative(snapshot.filename)}`);
    const differences = [];
    let siteChanged = 0, defaultChanged = 0, defaultRegressionChanged = 0;
    for (const block of blocks) {
      const oldWebsite = old.convert(block.text);
      const sharedWebsite = convertText(block.text, template.punctuation);
      const oldDefaultText = oldDefault.convert(block.text);
      const sharedDefault = convertText(block.text);
      if (oldWebsite !== sharedWebsite) siteChanged++;
      if (sharedDefault !== sharedWebsite) defaultChanged++;
      if (oldDefaultText !== sharedDefault) defaultRegressionChanged++;
      if (oldWebsite !== sharedWebsite || sharedDefault !== sharedWebsite || oldDefaultText !== sharedDefault) differences.push({
        index: block.index, tag: block.tag, sourceText: block.text,
        oldWebsite, sharedWebsite, oldDefaultText, sharedDefault,
        siteChanged: oldWebsite !== sharedWebsite,
        defaultRegressionChanged: oldDefaultText !== sharedDefault,
        kinds: differenceKinds(block.text, oldWebsite, sharedWebsite),
      });
    }
    articles.push({
      articleId: snapshot.articleId, source: relative(snapshot.filename), sourceSha256: sha256(html),
      alternateSnapshots: snapshot.alternateSnapshots,
      paragraphs: blocks.length, siteChanged, defaultChanged, defaultRegressionChanged, differences,
    });
  }
  const algorithmFiles = (await readdir(path.join(ROOT, 'public/js')))
    .filter(name => /^punctuation.*\.js$/u.test(name)).sort().map(name => `public/js/${name}`);
  algorithmFiles.push('public/js/templates/foodtalks-site.js');
  const fingerprints = Object.fromEntries(await Promise.all(algorithmFiles.map(async filename => [
    filename, sha256(await readFile(path.join(ROOT, filename))),
  ])));
  const report = {
    generatedAt: new Date().toISOString(),
    head: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: ROOT, encoding: 'utf8' }).trim(),
    command: 'node tools/compare-punctuation-corpus.mjs', baselineRef: BASELINE_REF,
    oldAlgorithmSha256: old.sha256, oldDefaultAlgorithmSha256: oldDefault.sha256,
    fingerprints, websitePolicy: template.punctuation,
    limitations: LIMITATIONS, candidateSnapshots: files.length,
    counts: {
      articles: articles.length,
      paragraphs: articles.reduce((sum, article) => sum + article.paragraphs, 0),
      siteChanged: articles.reduce((sum, article) => sum + article.siteChanged, 0),
      defaultChanged: articles.reduce((sum, article) => sum + article.defaultChanged, 0),
      defaultRegressionChanged: articles.reduce((sum, article) => sum + article.defaultRegressionChanged, 0),
    },
    articles,
  };
  await mkdir(OUTPUT_ROOT, { recursive: true });
  await writeFile(path.join(OUTPUT_ROOT, 'report.json'), `${JSON.stringify(report, null, 2)}\n`);
  await writeFile(path.join(OUTPUT_ROOT, 'report.html'), reportHTML(report));
  console.log(JSON.stringify({ ...report.counts, report: relative(path.join(OUTPUT_ROOT, 'report.html')) }, null, 2));
}

await main();
