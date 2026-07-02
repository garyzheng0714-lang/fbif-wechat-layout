import test from 'node:test';
import assert from 'node:assert/strict';

import {
  FOOTER_LEGACY_STORAGE_KEYS,
  footerPanelSignature,
  footerStateStorageKey,
  readFooterPanelState,
  saveFooterPanelState,
} from '../public/js/site-footer-state.js';
import {
  FOOTER_PRESETS_DEFAULT,
  FOOTER_SEGMENT_ORDER_DEFAULT,
} from '../public/js/footer-segments-defaults.js';

function createStorage(entries = []) {
  const map = new Map(entries);
  const operations = [];
  return {
    getItem(key) {
      operations.push({ type: 'get', key });
      return map.has(key) ? map.get(key) : null;
    },
    setItem(key, value) {
      operations.push({ type: 'set', key, value: String(value) });
      map.set(key, String(value));
    },
    removeItem(key) {
      operations.push({ type: 'remove', key });
      map.delete(key);
    },
    operations() {
      return operations.slice();
    },
    clearOperations() {
      operations.length = 0;
    },
    snapshot() {
      return Object.fromEntries(map.entries());
    },
  };
}

function booleanSegments(segments) {
  return Object.fromEntries(Object.entries(segments).map(([id, enabled]) => [id, !!enabled]));
}

const FOOTER_LIB = Object.freeze({
  enabled: true,
  active_preset_id: 'fbif-original',
  segment_order: FOOTER_SEGMENT_ORDER_DEFAULT,
  segments: Object.freeze({
    tip: Object.freeze({ html_lines: ['tip'] }),
    biz_coop: Object.freeze({ html_lines: ['biz'] }),
    join_group: Object.freeze({ html_lines: ['group'] }),
    follow_account: Object.freeze({ html_lines: ['follow'] }),
    logo: Object.freeze({ html_lines: ['logo'] }),
    more_info: Object.freeze({ html_lines: ['more'] }),
  }),
  presets: Object.freeze([
    Object.freeze({
      ...FOOTER_PRESETS_DEFAULT[0],
      segments: Object.freeze({ ...FOOTER_PRESETS_DEFAULT[0].segments }),
    }),
  ]),
});

test('site footer state falls back to footer-segments-defaults source of truth', () => {
  const state = readFooterPanelState({ storage: createStorage() });

  assert.deepEqual(state.presets, FOOTER_PRESETS_DEFAULT);
  assert.deepEqual(state.order, ['tip', ...FOOTER_SEGMENT_ORDER_DEFAULT]);
  assert.deepEqual(state.segments, FOOTER_PRESETS_DEFAULT[0].segments);
});

test('site footer state migrates legacy ft.footer.* keys once and deletes them', () => {
  const accountId = 'ft-default';
  const oldSegments = {
    tip: 0,
    biz_coop: 1,
    join_group: 0,
    follow_account: 1,
    logo: 0,
    more_info: 1,
  };
  const oldOrder = ['tip', 'more_info', 'biz_coop', 'logo', 'join_group', 'follow_account'];
  const storage = createStorage([
    [FOOTER_LEGACY_STORAGE_KEYS.enabled, '1'],
    [FOOTER_LEGACY_STORAGE_KEYS.segments, JSON.stringify(oldSegments)],
    [FOOTER_LEGACY_STORAGE_KEYS.order, JSON.stringify(oldOrder)],
    [FOOTER_LEGACY_STORAGE_KEYS.signature, footerPanelSignature(FOOTER_LIB)],
  ]);

  const migrated = readFooterPanelState({ storage, accountId, footerLib: FOOTER_LIB });

  assert.equal(migrated.enabled, true);
  assert.deepEqual(migrated.segments, booleanSegments(oldSegments));
  assert.deepEqual(migrated.order, oldOrder);
  for (const key of Object.values(FOOTER_LEGACY_STORAGE_KEYS)) {
    assert.equal(storage.getItem(key), null, `${key} should be deleted after migration`);
  }

  const newRaw = storage.getItem(footerStateStorageKey(accountId));
  assert.ok(newRaw, 'migration should persist a bottom_blocks-shaped state object');
  assert.deepEqual(JSON.parse(newRaw).override, {
    enabled: true,
    order: oldOrder,
    overrides: Object.fromEntries(Object.entries(oldSegments).map(([id, enabled]) => [id, { enabled: !!enabled }])),
  });

  const beforeReload = storage.snapshot();
  const reloaded = readFooterPanelState({ storage, accountId, footerLib: FOOTER_LIB });

  assert.deepEqual(
    {
      enabled: reloaded.enabled,
      segments: reloaded.segments,
      order: reloaded.order,
    },
    {
      enabled: migrated.enabled,
      segments: migrated.segments,
      order: migrated.order,
    },
    'reloading after migration should be idempotent'
  );
  assert.deepEqual(storage.snapshot(), beforeReload, 'second load should not rewrite or resurrect legacy keys');
});

test('site footer state does not consume legacy keys while footer library is not ready', () => {
  const accountId = 'ft-default';
  const oldSegments = {
    tip: 0,
    biz_coop: 1,
    join_group: 0,
    follow_account: 1,
    logo: 0,
    more_info: 1,
  };
  const oldOrder = ['tip', 'more_info', 'biz_coop', 'logo', 'join_group', 'follow_account'];
  const storage = createStorage([
    [FOOTER_LEGACY_STORAGE_KEYS.enabled, '1'],
    [FOOTER_LEGACY_STORAGE_KEYS.segments, JSON.stringify(oldSegments)],
    [FOOTER_LEGACY_STORAGE_KEYS.order, JSON.stringify(oldOrder)],
    [FOOTER_LEGACY_STORAGE_KEYS.signature, footerPanelSignature(FOOTER_LIB)],
  ]);
  const initialSnapshot = storage.snapshot();

  // WHY: Empty footer libraries are a transient async hydration state, not a
  // real schema change. These reads must not spend the one-time legacy
  // migration or write the new override key before server rules arrive.
  for (let i = 0; i < 3; i += 1) {
    storage.clearOperations();
    const state = readFooterPanelState({ storage, accountId, footerLib: {} });
    assert.deepEqual(state.presets, FOOTER_PRESETS_DEFAULT);
    assert.deepEqual(state.order, ['tip', ...FOOTER_SEGMENT_ORDER_DEFAULT]);
    assert.deepEqual(state.segments, FOOTER_PRESETS_DEFAULT[0].segments);
    assert.deepEqual(storage.operations(), [], 'empty footerLib read should not touch storage');
    assert.deepEqual(storage.snapshot(), initialSnapshot, 'empty footerLib read should not mutate storage');
    assert.equal(storage.snapshot()[footerStateStorageKey(accountId)], undefined, 'empty footerLib read should not write the new-format override key');
  }

  // WHY: The same untouched legacy data must still be available once the real
  // server footer library is ready, so the migration can happen exactly once
  // and subsequent ready reads remain stable.
  const migrated = readFooterPanelState({ storage, accountId, footerLib: FOOTER_LIB });

  assert.equal(migrated.enabled, true);
  assert.deepEqual(migrated.segments, booleanSegments(oldSegments));
  assert.deepEqual(migrated.order, oldOrder);
  for (const key of Object.values(FOOTER_LEGACY_STORAGE_KEYS)) {
    assert.equal(storage.getItem(key), null, `${key} should be deleted after footer library is ready`);
  }

  const rawState = JSON.parse(storage.getItem(footerStateStorageKey(accountId)));
  assert.equal(rawState.signature, footerPanelSignature(FOOTER_LIB));
  assert.deepEqual(rawState.override, {
    enabled: true,
    order: oldOrder,
    overrides: Object.fromEntries(Object.entries(oldSegments).map(([id, enabled]) => [id, { enabled: !!enabled }])),
  });

  const beforeReload = storage.snapshot();
  storage.clearOperations();
  readFooterPanelState({ storage, accountId, footerLib: FOOTER_LIB });

  assert.deepEqual(storage.snapshot(), beforeReload, 'migration should remain idempotent after reload');
  assert.deepEqual(
    storage.operations().filter(op => op.type !== 'get'),
    [],
    'idempotent reload should not write or remove keys'
  );
});

test('site footer legacy migration preserves server defaults for missing segment keys', () => {
  const accountId = 'ft-default';
  const storage = createStorage([
    [FOOTER_LEGACY_STORAGE_KEYS.enabled, '1'],
    [FOOTER_LEGACY_STORAGE_KEYS.segments, JSON.stringify({ tip: 0, biz_coop: 0 })],
    [FOOTER_LEGACY_STORAGE_KEYS.order, JSON.stringify(['tip', 'biz_coop'])],
    [FOOTER_LEGACY_STORAGE_KEYS.signature, footerPanelSignature(FOOTER_LIB)],
  ]);

  const migrated = readFooterPanelState({ storage, accountId, footerLib: FOOTER_LIB });

  assert.equal(migrated.segments.tip, false, 'explicit old segment override should win');
  assert.equal(migrated.segments.biz_coop, false, 'explicit old segment override should win');
  assert.equal(
    migrated.segments.join_group,
    FOOTER_PRESETS_DEFAULT[0].segments.join_group,
    'missing old segment should keep server preset default'
  );
  assert.equal(
    migrated.segments.follow_account,
    FOOTER_PRESETS_DEFAULT[0].segments.follow_account,
    'missing old segment should keep server preset default'
  );
});

test('site footer state preserves per-segment fields and keeps reloads idempotent', () => {
  const accountId = 'ft-default';
  const storage = createStorage();
  const order = ['tip', 'logo', 'biz_coop', 'join_group', 'follow_account', 'more_info'];
  const segments = { tip: true, logo: true, biz_coop: false, join_group: true, follow_account: true, more_info: true };
  const fields = {
    logo: {
      html_lines: ['<p><img src="{{asset#ft_footer_group_qr}}" alt="new logo" /></p>'],
    },
    more_info: {},
  };

  // WHY: 网站 /ft 底部图片替换是单篇 field override，不是规则默认。
  // 保存后必须只在对应 block 写 fields，空 fields 不应制造无意义 diff。
  const saved = saveFooterPanelState({ storage, accountId, footerLib: FOOTER_LIB, enabled: true, segments, order, fields });

  assert.deepEqual(saved.overrides.logo, {
    enabled: true,
    fields: {
      html_lines: ['<p><img src="{{asset#ft_footer_group_qr}}" alt="new logo" /></p>'],
    },
  });
  assert.deepEqual(saved.overrides.more_info, { enabled: true }, 'empty fields should be removed before writing');

  const raw = JSON.parse(storage.getItem(footerStateStorageKey(accountId)));
  assert.deepEqual(raw.override.overrides.logo.fields, fields.logo);
  assert.equal(raw.override.overrides.biz_coop.fields, undefined, 'segments without replacements should not get fields');

  const beforeReload = storage.snapshot();
  storage.clearOperations();
  const reloaded = readFooterPanelState({ storage, accountId, footerLib: FOOTER_LIB });

  assert.deepEqual(reloaded.override.overrides.logo.fields, fields.logo);
  assert.deepEqual(reloaded.segments, segments);
  assert.deepEqual(storage.snapshot(), beforeReload, 'reading an already-normalized fields override should not rewrite storage');
  assert.deepEqual(
    storage.operations().filter(op => op.type !== 'get'),
    [],
    'idempotent fields reload should not write or remove keys'
  );
});

test('site footer state deletes restored-default fields without dropping enabled/order', () => {
  const accountId = 'ft-default';
  const storage = createStorage();
  const order = ['tip', 'logo', 'biz_coop', 'join_group', 'follow_account', 'more_info'];
  const segments = { tip: true, logo: true, biz_coop: true, join_group: true, follow_account: true, more_info: true };

  saveFooterPanelState({
    storage,
    accountId,
    footerLib: FOOTER_LIB,
    enabled: true,
    segments,
    order,
    fields: { logo: { html_lines: ['<p><img src="{{asset#ft_footer_group_qr}}" /></p>'] } },
  });

  // WHY: “恢复默认”应删除本篇 field diff，让渲染重新跟随后台默认素材；
  // 但不能顺手改掉本文的开关和排序状态。
  const restored = saveFooterPanelState({
    storage,
    accountId,
    footerLib: FOOTER_LIB,
    enabled: true,
    segments,
    order,
    fields: { logo: {} },
  });

  assert.equal(restored.overrides.logo.fields, undefined);
  assert.equal(restored.overrides.logo.enabled, true);
  assert.deepEqual(restored.order, order);

  const reloaded = readFooterPanelState({ storage, accountId, footerLib: FOOTER_LIB });
  assert.equal(reloaded.override.overrides.logo.fields, undefined);
  assert.equal(reloaded.override.overrides.logo.enabled, true);
  assert.deepEqual(reloaded.order, order);
});
