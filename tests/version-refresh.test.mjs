import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createVersionRefreshController,
  decideVersionRefresh,
  stripBuildVersionFromLocation,
  stripBuildVersionSearch,
} from '../public/js/version-refresh.js';

test('build version is stripped from page search without dropping other params', () => {
  assert.deepEqual(stripBuildVersionSearch(''), { changed: false, search: '' });
  assert.deepEqual(stripBuildVersionSearch('?account=fbif'), { changed: false, search: '?account=fbif' });
  assert.deepEqual(stripBuildVersionSearch('?v=bff485e'), { changed: true, search: '' });
  assert.deepEqual(stripBuildVersionSearch('?v=bff485e&account=legacy'), { changed: true, search: '?account=legacy' });
  assert.deepEqual(stripBuildVersionSearch('?account=legacy&v=bff485e'), { changed: true, search: '?account=legacy' });
});

test('page location drops the build version stamp for bookmarks', () => {
  const calls = [];
  const locationRef = {
    pathname: '/mp/fbif',
    search: '?v=bff485e863bfd353b13359b04e17e77db0cd5ed3',
    hash: '',
  };
  const historyRef = {
    replaceState(_state, _title, url) { calls.push(url); },
  };
  assert.equal(stripBuildVersionFromLocation({ locationRef, historyRef }), true);
  assert.deepEqual(calls, ['/mp/fbif']);
  locationRef.search = '';
  assert.equal(stripBuildVersionFromLocation({ locationRef, historyRef }), false);
  assert.deepEqual(calls, ['/mp/fbif']);
});

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
