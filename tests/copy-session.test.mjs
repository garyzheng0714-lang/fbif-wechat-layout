// 契约测试：copy-session.js —— 复制链路竞态守卫（S2）。
// 意图：守卫的语义是「慢速旧异步任务不得把结果写进新文章的复制层」（跨文章污染，
// 真实事故见 66535c8）。此前靠两个裸计数器 + 每个重置点手工成对递增维持；本测试
// 锁定统一抽象的三条硬契约：capture 后 invalidate 必 stale、独立 gate 互不影响、
// 文章级重置一次作废全部 gate。改弱任何一条都会让跨文章污染类 bug 复活。
import test from 'node:test';
import assert from 'node:assert/strict';

const { createVersionGate, createCopySession } = await import('../public/js/copy-session.js');

test('capture 后未失效 → 不 stale；invalidate 后 → 所有在途 capture 变 stale', () => {
  const gate = createVersionGate('t');
  const a = gate.capture();
  assert.equal(a.stale(), false);
  gate.invalidate();
  assert.equal(a.stale(), true, '失效后旧任务必须被拒绝回写');
  const b = gate.capture();
  assert.equal(b.stale(), false, '失效后的新 capture 是新版本，不受旧失效影响');
});

test('多个在途 capture 同时失效（一次 invalidate 拒绝全部旧任务）', () => {
  const gate = createVersionGate('t');
  const a = gate.capture();
  const b = gate.capture();
  gate.invalidate();
  assert.equal(a.stale(), true);
  assert.equal(b.stale(), true);
});

test('两个 gate 相互独立：作废更多文章任务不影响 footer 写权（反向亦然）', () => {
  const s = createCopySession();
  const footer = s.footerCopyGate.capture();
  const ma = s.moreArticlesGate.capture();
  s.moreArticlesGate.invalidate();
  assert.equal(ma.stale(), true);
  assert.equal(footer.stale(), false, 'prepare 新一轮不应作废进行中的 footer 上传回写权之外的语义');
});

test('resetForNewArticle 一次作废全部 gate（新文章导入后旧任务全体免疫）', () => {
  const s = createCopySession();
  const footer = s.footerCopyGate.capture();
  const ma = s.moreArticlesGate.capture();
  const preview = s.moreArticlesPreviewGate.capture();
  s.resetForNewArticle();
  assert.equal(footer.stale(), true);
  assert.equal(ma.stale(), true);
  assert.equal(preview.stale(), true);
});

// 机制防回退：engine.js 不得再出现裸计数器守卫（有人绕过 gate 加回 _footerCopyGen
// 之类的数字比较，就会重新引入「重置点忘记成对递增」的漏洞类）。
test('engine.js 已无裸计数器守卫', async () => {
  const { readFile } = await import('node:fs/promises');
  const engine = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  // 匹配使用形态（声明/递增/数字比较），注释中提及历史名不算回退。
  assert.ok(!/let _footerCopyGen|_footerCopyGen\+\+|===? footerGenAtStart/.test(engine), 'engine.js 不应再有 _footerCopyGen 裸计数器用法');
  assert.ok(!/let _moreArticlesCopySeq|_moreArticlesCopySeq\+\+|!== _moreArticlesCopySeq/.test(engine), 'engine.js 不应再有 _moreArticlesCopySeq 裸计数器用法');
  assert.ok(engine.includes('copy-session.js'), 'engine.js 应使用 copy-session.js 的版本门');
});
