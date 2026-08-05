// 导入进度条组件契约（2026-08-05 用户裁定）。
//
// 背景：进度条曾经是灰底工作区上一块「无圆角、无描边、无内边距的白底」，贴在导入卡
// 下方，配一条满卡宽的饱和蓝粗条；同时本地上传路径把解析阶段直接冲到 97–98%，出现
// 「1/13 图 → 98%」这种自相矛盾的读数。用户裁定：进度必须是导入卡内部的状态行。
//
// 这里锁住三件事，任何一条被改回去都会红：
//   1. 结构：progress 在导入卡内部，且是「细条 + 状态行（文案 / 百分比）」；
//   2. 视觉：progress 不得自带 background（卡片负责白底），必须有水平内边距对齐卡内其它行；
//   3. 诚实：外壳侧写进度只能经 setShellProgress，条宽、aria 值与可见百分比一起更新。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const readPublic = name => readFile(new URL(`../public/${name}`, import.meta.url), 'utf8');

async function assertProgressInsideImportCard(file) {
  const html = await readPublic(file);
  const cardStart = html.indexOf('class="import-composer dropzone upload-zone"');
  const cardEnd = html.indexOf('<input type="file" id="fileInput">', cardStart);
  const submitAt = html.indexOf('id="importSubmitBtn"', cardStart);
  const progressAt = html.indexOf('class="progress import-progress"', cardStart);

  assert.ok(cardStart >= 0, `${file} 应保留统一导入卡`);
  assert.ok(cardEnd > cardStart, `${file} 导入卡应以隐藏 file input 收尾`);
  assert.ok(progressAt > submitAt, `${file} 进度应排在提交按钮之后`);
  assert.ok(
    progressAt < cardEnd,
    `${file} 进度必须在导入卡内部——它曾是卡外一块无圆角白底，用户已裁定不要`,
  );

  const progressBlock = html.slice(progressAt, html.indexOf('</div>', html.indexOf('progress-status', progressAt)) + 6);
  assert.match(progressBlock, /class="progress-bar"/, `${file} 进度需要细条轨道`);
  assert.match(progressBlock, /id="progressFill"/, `${file} 进度需要填充条`);
  assert.match(progressBlock, /class="progress-status"/, `${file} 文案与百分比必须同处状态行`);
  assert.match(progressBlock, /id="progressText"/, `${file} 需要状态文案`);
  assert.match(progressBlock, /id="progressPercent"/, `${file} 需要可见百分比`);
  // 旧的 .progress-heading（「总进度」+ 百分比 的独立标题行）已废弃；
  // 但 aria-label="导入总进度" 是可访问名称，必须保留。
  assert.doesNotMatch(
    progressBlock,
    /class="progress-heading"/,
    `${file} 不再使用独立的「总进度」标题行；状态行只放文案与百分比`,
  );
  assert.match(
    html.slice(progressAt, progressAt + 200),
    /aria-label="导入总进度"/,
    `${file} 进度条必须保留可访问名称`,
  );
  assert.match(
    html.slice(progressAt, progressAt + 400),
    /role="progressbar"[\s\S]*aria-valuemin="0"[\s\S]*aria-valuemax="100"/,
    `${file} 进度必须保留可访问的 progressbar 语义`,
  );
}

test('app.html / site.html: 导入进度是导入卡内部的状态行', async () => {
  await assertProgressInsideImportCard('app.html');
  await assertProgressInsideImportCard('site.html');
});

test('app-shell.css: 进度行不得自带白底，必须与卡内其它行对齐', async () => {
  const css = await readPublic('css/app-shell.css');
  const start = css.indexOf('.import-progress {');
  assert.ok(start >= 0, 'CSS 应定义 .import-progress');
  const rule = css.slice(start, css.indexOf('}', start));

  assert.doesNotMatch(
    rule,
    /background:\s*var\(--fs-bg-card\)/,
    '进度行不得自带卡片白底——它在灰底工作区上会露出一块无圆角白块（用户已裁定）',
  );
  assert.match(rule, /background:\s*transparent/, '进度行背景应显式透明，由导入卡提供白底');
  assert.match(
    rule,
    /padding:\s*0\s+14px/,
    '进度行需要 14px 水平内边距，与输入框和动作按钮对齐；卡片本身没有 padding',
  );

  const barStart = css.indexOf('.progress-bar {');
  const barRule = css.slice(barStart, css.indexOf('}', barStart));
  const height = Number((barRule.match(/height:\s*(\d+)px/) || [])[1]);
  assert.ok(
    Number.isFinite(height) && height <= 4,
    `进度条应为细条（≤4px），当前 ${height}px；满宽饱和粗条会抢过正文预览`,
  );

  // 分母未知时不得显示会误导的百分比读数。
  assert.match(
    css,
    /\.import-progress\.is-indeterminate\s+\.progress-percent\s*\{[^}]*visibility:\s*hidden/,
    '不确定态必须隐藏百分比，不许编造读数',
  );
  // 长文案不得把百分比挤进文字中间。
  const textStart = css.indexOf('.progress-text {');
  const textRule = css.slice(textStart, css.indexOf('}', textStart));
  assert.match(textRule, /text-overflow:\s*ellipsis/, '状态文案需截断，保证百分比占住右端');
  assert.match(textRule, /white-space:\s*nowrap/, '状态文案不得换行挤压百分比');
});

test('engine.js: 外壳进度只经 setShellProgress，条宽与可见百分比同步', async () => {
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');

  const helperStart = engine.indexOf('function setShellProgress(');
  assert.ok(helperStart >= 0, 'engine 必须提供统一的 setShellProgress');
  const helper = engine.slice(helperStart, engine.indexOf('\n  }', helperStart));
  assert.match(helper, /progressFill\.style\.width/, 'setShellProgress 应写条宽');
  assert.match(helper, /aria-valuenow/, 'setShellProgress 应同步 aria 值');
  assert.match(helper, /progressPercentEl/, 'setShellProgress 必须同步可见百分比，否则数字会停在旧值');

  // 除 setShellProgress 内部，任何地方都不得裸写条宽——否则百分比会和条宽脱节。
  const rawWrites = [...engine.matchAll(/progressFill\.style\.width/g)];
  assert.equal(
    rawWrites.length,
    1,
    `progressFill.style.width 只应出现在 setShellProgress 内（当前 ${rawWrites.length} 处）`,
  );

  // 图片准备与解码是导入主要耗时，必须占进度条主段；解析阶段不得直接冲到 97%+。
  assert.match(
    engine,
    /LOCAL_PHASE_RANGE\s*=\s*\{\s*materialize:\s*\[40,\s*75\],\s*decode:\s*\[75,\s*95\]\s*\}/,
    '本地上传的图片准备/解码应占 40→95%，避免出现「1/13 图 → 98%」的矛盾读数',
  );
  const earlyJumps = [...engine.matchAll(/setShellProgress\((9[5-9])\)/g)].map(m => m[1]);
  assert.deepEqual(
    earlyJumps,
    [],
    `解析阶段不得直接跳到 ${earlyJumps.join('/')}%——主要等待还在后面`,
  );
});
