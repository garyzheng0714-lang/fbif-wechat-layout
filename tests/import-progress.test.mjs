import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { parseHTML } from 'linkedom';

import {
  bindImportProgressDom,
  createImportProgressTracker,
} from '../public/js/import-progress.js';

function makeTracker({
  phases = [
    { id: 'export', weight: 2 },
    { id: 'download', weight: 3 },
    { id: 'parse', weight: 1 },
  ],
} = {}) {
  const snapshots = [];
  const tracker = createImportProgressTracker({
    phases,
    onChange(snapshot) {
      snapshots.push({ ...snapshot });
    },
  });
  return { tracker, snapshots };
}

test('total import progress is calculated only from real phase done/total and explicit weights', () => {
  const { tracker } = makeTracker();

  tracker.updatePhase('export', 1, 2, { status: '正在生成 Word' });
  assert.equal(tracker.snapshot().percent, 17);

  tracker.completePhase('export', { status: 'Word 已生成' });
  assert.equal(tracker.snapshot().percent, 33);

  tracker.updatePhase('download', 5, 10, { status: '正在接收文档' });
  assert.equal(tracker.snapshot().percent, 58);

  tracker.updatePhase('download', 10, 10);
  assert.equal(tracker.snapshot().percent, 83);

  tracker.completePhase('parse');
  assert.equal(tracker.snapshot().percent, 99);
  assert.equal(tracker.snapshot().committed, false);
});

test('unknown totals may update the status but never invent elapsed-time progress', () => {
  const { tracker } = makeTracker();

  tracker.updatePhase('export', 64 * 1024, 0, { status: '已接收 64 KB' });
  const first = tracker.snapshot();
  tracker.updatePhase('export', 128 * 1024, undefined, { status: '已接收 128 KB' });
  const second = tracker.snapshot();

  assert.equal(first.percent, 0);
  assert.equal(second.percent, 0);
  assert.equal(second.status, '已接收 128 KB');
});

test('0-99 progress is monotonic, clamps invalid input, and cannot reach 100 before terminal commit', () => {
  const { tracker, snapshots } = makeTracker();

  tracker.updatePhase('download', 8, 10);
  tracker.updatePhase('download', 3, 10);
  tracker.updatePhase('download', 30, 10);
  tracker.updatePhase('parse', Number.NaN, 1);
  tracker.completePhase('export');
  tracker.completePhase('download');
  tracker.completePhase('parse');

  const values = snapshots.map(snapshot => snapshot.percent);
  assert.ok(values.every((value, index) => index === 0 || value >= values[index - 1]));
  assert.ok(values.every(value => Number.isInteger(value) && value >= 0 && value <= 99));
  assert.equal(tracker.snapshot().percent, 99);
});

test('cancelled or superseded import intent can never report 100', () => {
  const { tracker, snapshots } = makeTracker();
  tracker.completePhase('export');
  tracker.completePhase('download');
  tracker.completePhase('parse');
  tracker.cancel('superseded');

  assert.equal(tracker.commit(), false);
  assert.equal(tracker.snapshot().cancelled, true);
  assert.equal(tracker.snapshot().percent, 99);
  assert.equal(snapshots.some(snapshot => snapshot.percent === 100), false);
});

test('100 is rejected until preview and copy are committed, then emitted exactly once', () => {
  const { tracker, snapshots } = makeTracker({
    phases: [
      { id: 'prepare', weight: 8 },
      { id: 'preview-commit', weight: 1 },
      { id: 'copy-commit', weight: 1 },
    ],
  });
  tracker.completePhase('prepare');

  assert.equal(tracker.commit(), false);
  assert.equal(tracker.snapshot().percent < 100, true);

  tracker.completePhase('preview-commit');
  assert.equal(tracker.commit(), false);

  tracker.completePhase('copy-commit');
  assert.equal(tracker.snapshot().percent, 99);
  assert.equal(tracker.commit(), true);
  assert.equal(tracker.snapshot().percent, 100);
  assert.equal(tracker.snapshot().committed, true);
  assert.equal(tracker.commit(), true, 'terminal commit should be idempotent');
  assert.equal(snapshots.filter(snapshot => snapshot.percent === 100).length, 1);
});

test('all phases are required by default while explicitly optional phases do not block commit', () => {
  const { tracker } = makeTracker({
    phases: [
      { id: 'required' },
      { id: 'best-effort', required: false },
    ],
  });

  assert.equal(tracker.commit(), false);
  tracker.completePhase('required');
  assert.equal(tracker.commit(), true);
  assert.equal(tracker.snapshot().percent, 100);
});

test('DOM binding exposes one honest total percent through width, text, and progressbar ARIA', () => {
  const { document } = parseHTML(`
    <div id="progress">
      <div id="progressFill"></div>
      <span id="progressText"></span>
      <strong id="progressPercent"></strong>
    </div>
  `);
  const root = document.getElementById('progress');
  const fill = document.getElementById('progressFill');
  const text = document.getElementById('progressText');
  const percent = document.getElementById('progressPercent');
  const render = bindImportProgressDom({ root, fill, text, percent });

  render({
    percent: 42,
    status: '正在准备正文图片',
    visible: true,
    committed: false,
  });

  assert.equal(root.style.display, 'block');
  assert.equal(root.getAttribute('role'), 'progressbar');
  assert.equal(root.getAttribute('aria-valuemin'), '0');
  assert.equal(root.getAttribute('aria-valuemax'), '100');
  assert.equal(root.getAttribute('aria-valuenow'), '42');
  assert.equal(fill.style.width, '42%');
  assert.equal(text.textContent, '正在准备正文图片');
  assert.equal(percent.textContent, '42%');
  assert.equal(root.classList.contains('is-indeterminate'), false);
});

test('both editor channels include the same explicit percent and accessible progressbar contract', async () => {
  for (const page of ['app.html', 'site.html']) {
    const html = await readFile(new URL(`../public/${page}`, import.meta.url), 'utf8');
    assert.match(html, /id="progress"[^>]*role="progressbar"/, `${page} must expose progressbar semantics`);
    assert.match(html, /id="progressPercent"/, `${page} must show the total percentage`);
  }
});

test('engine commits the visible preview and copy state before the tracker may emit 100', async () => {
  const source = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const prepareAt = source.indexOf('preparedPreview = prepareConnectedPreviewContent(');
  const validateAt = source.indexOf('validatePreparedPreview(preparedPreview)', prepareAt);
  const copyPrepareAt = source.indexOf('preparedClipboardArtifact = await prepareClipboardArtifact(', validateAt);
  const activateAt = source.indexOf('activate();', copyPrepareAt);
  const previewCommitAt = source.indexOf("completePhase('preview-commit'", activateAt);
  const copyCommitAt = source.indexOf("completePhase('copy-commit'", previewCommitAt);
  const totalCommitAt = source.indexOf('importProgress.commit(', copyCommitAt);
  assert.ok(
    prepareAt >= 0
      && validateAt > prepareAt
      && copyPrepareAt > validateAt
      && activateAt > copyPrepareAt
      && previewCommitAt > activateAt
      && copyCommitAt > previewCommitAt
      && totalCommitAt > copyCommitAt,
    'detached preview and clipboard artifact must be verified before one synchronous activation and 100%',
  );

  const renderAt = source.indexOf('const renderedImages = opts?.preparedPreview');
  const summaryAt = source.indexOf('showImportSummary(importTitle, stats, {', renderAt);
  assert.ok(renderAt >= 0 && summaryAt > renderAt, 'the new DOM must be committed before progress can be hidden');
});

test('tracked Feishu copy uses a precomputed clipboard artifact instead of processing on click', async () => {
  const source = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  assert.match(source, /const preparedArtifact = \([\s\S]{0,260}_preparedClipboardArtifact\.sourceHash === artifactHash/);
  assert.match(source, /let html = preparedArtifact\?\.html \|\| sourceArtifact/);
  assert.match(source, /if \(!preparedArtifact\) \{[\s\S]*?processForCopy/);
});

test('optional CDN enhancement failure does not reject safe bitmap copy fallback before final validation', async () => {
  const source = await readFile(new URL('../public/js/engine.js', import.meta.url), 'utf8');
  const transferResultAt = source.indexOf('const copyReady = await uploadNonCdnImages(');
  const finalSafetyAt = source.indexOf('preparedClipboardArtifact = await prepareClipboardArtifact(', transferResultAt);
  const between = source.slice(transferResultAt, finalSafetyAt);
  assert.ok(transferResultAt >= 0 && finalSafetyAt > transferResultAt);
  assert.match(between, /copyReady\.failed > 0[\s\S]*?最终复制安全校验/);
  assert.doesNotMatch(
    between,
    /copyReady\.failed > 0[\s\S]{0,300}?throw new Error/,
    '公众号安全 bitmap data: 降级必须交给最终复制产物校验，不能因可选 OSS 增强失败而中止',
  );
});
