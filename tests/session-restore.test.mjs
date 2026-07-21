import test from 'node:test';
import assert from 'node:assert/strict';

import {
  captureChannel,
  clearChannel,
  restoreChannel,
  restorePending,
} from '../public/js/session-restore.js';

function memoryStorage() {
  const values = new Map();
  return {
    values,
    getItem(key) { return values.has(key) ? values.get(key) : null; },
    setItem(key, value) { values.set(key, String(value)); },
    removeItem(key) { values.delete(key); },
  };
}

function editorSnapshot(overrides = {}) {
  return {
    schema_version: 1,
    timestamp: Date.now(),
    closure_state: {
      article_html: '<p>visible article</p>',
      article_copy: '<p>copy article</p>',
      footer_html: '',
      footer_copy: '',
      last_render_input: { kind: 'md', payload: 'source article' },
      ...overrides.closure_state,
    },
    dom: {
      preview_visible: true,
      content_area_html: '<p>visible article</p>',
      ...overrides.dom,
    },
  };
}

function installBrowserState({ snapshot = editorSnapshot(), accountID = 'fbif', ruleVersion = 7, build = 'build-a' } = {}) {
  const previous = {
    window: globalThis.window,
    location: globalThis.location,
    sessionStorage: globalThis.sessionStorage,
    document: globalThis.document,
  };
  const store = memoryStorage();
  const restored = [];
  globalThis.location = { pathname: '/mp/' + accountID };
  globalThis.sessionStorage = store;
  globalThis.document = undefined;
  globalThis.window = {
    location: globalThis.location,
    __APP_BUILD_INFO: { build },
    _activeRulePack: { id: accountID },
    _activeConfig: {
      preset: { id: 'rule-a', version: ruleVersion },
      config: { font_size: 16, modules: { supporting: { enabled: true } } },
      behavior: { schema_version: 1, rules: [] },
    },
    _captureSessionSnapshot: () => snapshot,
    _restoreSessionSnapshot(value) { restored.push(value); return true; },
  };
  clearChannel('mp');
  return {
    store,
    restored,
    restore() {
      globalThis.window = previous.window;
      globalThis.location = previous.location;
      globalThis.sessionStorage = previous.sessionStorage;
      globalThis.document = previous.document;
    },
  };
}

function onlyDraft(store) {
  const entries = [...store.values.entries()].filter(([key]) => key.startsWith('fbif_draft:'));
  assert.equal(entries.length, 1);
  return { key: entries[0][0], envelope: JSON.parse(entries[0][1]) };
}

test('oversized session snapshot keeps preview but records copy artifact as unavailable', () => {
  const browser = installBrowserState({
    snapshot: editorSnapshot({ closure_state: { article_copy: 'x'.repeat(4 * 1024 * 1024 + 1) } }),
  });
  try {
    assert.equal(captureChannel('mp'), true);
    const { key, envelope } = onlyDraft(browser.store);
    assert.match(key, /^fbif_draft:mp:fbif:/);
    assert.equal(envelope.schema_version, 2);
    assert.equal(envelope.account_id, 'fbif');
    assert.equal(envelope.rule_revision, '7');
    assert.equal(envelope.renderer_build, 'build-a');
    assert.equal(envelope.degraded, true);
    assert.equal(envelope.editor_snapshot.dom.content_area_html, '<p>visible article</p>');
    assert.equal(envelope.editor_snapshot.closure_state.article_copy, '');
    assert.match(envelope.editor_snapshot.closure_state.copy_unavailable_reason, /重新导入/);
  } finally {
    browser.restore();
  }
});

test('matching account, rule, config and build restores the scoped document', async () => {
  const browser = installBrowserState();
  try {
    assert.equal(captureChannel('mp'), true);
    assert.equal(await restoreChannel('mp'), true);
    assert.equal(browser.restored.length, 1);
    assert.equal(browser.restored[0].closure_state.last_render_input.payload, 'source article');
  } finally {
    browser.restore();
  }
});

test('another account cannot see or restore the current account draft', async () => {
  const browser = installBrowserState();
  try {
    assert.equal(captureChannel('mp'), true);
    globalThis.window._activeRulePack = { id: 'wowfood' };
    assert.equal(await restoreChannel('mp'), false);
    assert.equal(browser.restored.length, 0);
    assert.equal(onlyDraft(browser.store).envelope.account_id, 'fbif');
  } finally {
    browser.restore();
  }
});

test('rule or build mismatch requires an explicit restore choice', async () => {
  const browser = installBrowserState();
  try {
    assert.equal(captureChannel('mp'), true);
    globalThis.window._activeConfig.preset.version = 8;
    globalThis.window.__APP_BUILD_INFO.build = 'build-b';
    assert.equal(await restoreChannel('mp'), false);
    assert.equal(browser.restored.length, 0);
    assert.deepEqual(globalThis.window._sessionRestoreLastRejection.reasons.sort(), ['版本', '规则']);
    assert.equal(await restorePending(false), true);
    assert.equal(browser.restored.length, 1);
  } finally {
    browser.restore();
  }
});

test('expired or tampered drafts are discarded instead of restored', async () => {
  for (const kind of ['expired', 'tampered']) {
    const browser = installBrowserState();
    try {
      assert.equal(captureChannel('mp'), true);
      const draft = onlyDraft(browser.store);
      if (kind === 'expired') draft.envelope.expires_at = new Date(Date.now() - 1).toISOString();
      else draft.envelope.artifact_hash = 'forged';
      browser.store.setItem(draft.key, JSON.stringify(draft.envelope));
      assert.equal(await restoreChannel('mp'), false, kind);
      assert.equal(browser.restored.length, 0, kind);
      assert.equal([...browser.store.values.keys()].some(key => key.startsWith('fbif_draft:')), false, kind);
    } finally {
      browser.restore();
    }
  }
});

test('sessionStorage quota failure leaves the active editor usable and reports capture failure', () => {
  const browser = installBrowserState();
  try {
    browser.store.setItem = () => {
      throw new DOMException('storage quota exceeded', 'QuotaExceededError');
    };
    assert.doesNotThrow(() => {
      assert.equal(captureChannel('mp'), false);
    });
    assert.equal(browser.restored.length, 0);
  } finally {
    browser.restore();
  }
});
