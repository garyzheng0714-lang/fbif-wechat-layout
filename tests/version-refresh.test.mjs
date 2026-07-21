import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createVersionRefreshController,
  decideVersionRefresh,
} from '../public/js/version-refresh.js';

test('version refresh policy reloads only clean editors', () => {
  assert.equal(decideVersionRefresh({ currentBuildId: 'a', latestBuildId: 'a', isDevBuild: false, hasUnsavedWork: true }), 'none');
  assert.equal(decideVersionRefresh({ currentBuildId: 'a', latestBuildId: 'b', isDevBuild: false, hasUnsavedWork: false }), 'reload');
  assert.equal(decideVersionRefresh({ currentBuildId: 'a', latestBuildId: 'b', isDevBuild: false, hasUnsavedWork: true }), 'prompt');
  assert.equal(decideVersionRefresh({ currentBuildId: 'a', latestBuildId: 'b', isDevBuild: true, hasUnsavedWork: false }), 'none');
});

test('dirty editor gets a user-controlled update prompt and persists before refresh', async () => {
  const events = [];
  let refreshAction;
  const controller = createVersionRefreshController({
    currentBuildId: 'old',
    isDevBuild: false,
    loadVersionInfo: async () => ({ build: 'new' }),
    hasUnsavedWork: () => true,
    showUpdate(action) { events.push('prompt'); refreshAction = action; },
    persistDraft() { events.push('persist'); },
    reload() { events.push('reload'); },
    win: {},
    doc: {},
  });

  assert.equal(await controller.checkNow(), 'prompt');
  assert.deepEqual(events, ['prompt']);
  refreshAction();
  assert.deepEqual(events, ['prompt', 'persist', 'reload']);
});

test('clean editor reloads automatically without showing a prompt', async () => {
  const events = [];
  const controller = createVersionRefreshController({
    currentBuildId: 'old',
    isDevBuild: false,
    loadVersionInfo: async () => ({ build: 'new' }),
    hasUnsavedWork: () => false,
    showUpdate() { events.push('prompt'); },
    persistDraft() { events.push('persist'); },
    reload() { events.push('reload'); },
    win: {},
    doc: {},
  });

  assert.equal(await controller.checkNow(), 'reload');
  assert.deepEqual(events, ['persist', 'reload']);
});

test('older overlapping version check cannot override the latest intent', async () => {
  const resolvers = [];
  const events = [];
  const controller = createVersionRefreshController({
    currentBuildId: 'old',
    isDevBuild: false,
    loadVersionInfo: () => new Promise(resolve => resolvers.push(resolve)),
    hasUnsavedWork: () => false,
    showUpdate() { events.push('prompt'); },
    persistDraft() { events.push('persist'); },
    reload() { events.push('reload'); },
    win: {},
    doc: {},
  });

  const first = controller.checkNow();
  const second = controller.checkNow();
  resolvers[1]({ build: 'old' });
  assert.equal(await second, 'none');
  resolvers[0]({ build: 'new' });
  assert.equal(await first, 'stale');
  assert.deepEqual(events, []);
});
