// site-footer-state.js — FoodTalks 网站页脚面板的单篇 bottom_blocks-shaped 状态。
//
// 旧前台面板曾用三个 fbif_footer 片段语义 key 存储：
// ft.footer.enabled / ft.footer.segments / ft.footer.order。这里仅把它们作为一次性迁移输入；
// 迁移后面板状态统一落到一个 bottom_blocks override 形状对象里。

import {
  FOOTER_PRESETS_DEFAULT,
  FOOTER_SEGMENT_ORDER_DEFAULT,
} from './footer-segments-defaults.js';

export const FOOTER_LEGACY_STORAGE_KEYS = Object.freeze({
  enabled: 'ft.footer.enabled',
  segments: 'ft.footer.segments',
  order: 'ft.footer.order',
  signature: 'ft.footer.signature',
});

function isObj(v) {
  return !!v && typeof v === 'object' && !Array.isArray(v);
}

function safeStorage(storage) {
  return storage || (typeof localStorage !== 'undefined' ? localStorage : null);
}

export function footerStateStorageKey(accountId) {
  return 'ft.footer.bottom_blocks.override::' + String(accountId || 'default');
}

export function footerPresetList(footerLib = {}, fallbackPresets = FOOTER_PRESETS_DEFAULT) {
  return Array.isArray(footerLib.presets) && footerLib.presets.length ? footerLib.presets : fallbackPresets;
}

export function footerServerOrder(footerLib = {}, fallbackOrder = FOOTER_SEGMENT_ORDER_DEFAULT) {
  return Array.isArray(footerLib.segment_order) && footerLib.segment_order.length
    ? footerLib.segment_order.filter(k => k !== 'tip')
    : fallbackOrder.slice();
}

export function activeFooterPreset(footerLib = {}, fallbackPresets = FOOTER_PRESETS_DEFAULT) {
  const presets = footerPresetList(footerLib, fallbackPresets);
  return presets.find(p => p.id === footerLib.active_preset_id) || presets[0] || { segments: {} };
}

export function footerPanelSignature(footerLib = {}, fallbackPresets = FOOTER_PRESETS_DEFAULT, fallbackOrder = FOOTER_SEGMENT_ORDER_DEFAULT) {
  const active = activeFooterPreset(footerLib, fallbackPresets);
  const segmentKeys = Object.keys((footerLib && footerLib.segments) || {}).sort();
  return JSON.stringify({
    preset: (footerLib && footerLib.active_preset_id) || active.id || '',
    order: footerServerOrder(footerLib, fallbackOrder),
    segments: active.segments || {},
    keys: segmentKeys,
  });
}

// WHY: An empty object means site.html has not finished hydrating server rules.
// Treating that window as a real footer library would recalculate fallback
// signatures and delete legacy keys, burning the one-time migration chance for
// customized footer segment switches/order.
function isFooterLibReady(footerLib) {
  return isObj(footerLib) && Object.keys(footerLib).length > 0;
}

function defaultParts(footerLib, fallbackPresets, fallbackOrder) {
  const active = activeFooterPreset(footerLib, fallbackPresets);
  return {
    signature: footerPanelSignature(footerLib, fallbackPresets, fallbackOrder),
    order: ['tip', ...footerServerOrder(footerLib, fallbackOrder)],
    segments: { ...((active && active.segments) || {}) },
    enabled: !!(footerLib && footerLib.enabled),
  };
}

function normalizeOrder(order, defaultOrder) {
  const known = new Set(defaultOrder);
  const out = Array.isArray(order) && order.length ? order.filter(k => known.has(k)) : [];
  for (const key of defaultOrder) {
    if (!out.includes(key)) out.push(key);
  }
  return out;
}

function segmentsFromOverride(override, defaultSegments, defaultOrder) {
  const out = {};
  const ovBlocks = isObj(override && override.overrides) ? override.overrides : {};
  for (const key of [...defaultOrder, ...Object.keys(defaultSegments || {}), ...Object.keys(ovBlocks)]) {
    if (!key) continue;
    if (isObj(ovBlocks[key]) && typeof ovBlocks[key].enabled === 'boolean') {
      out[key] = ovBlocks[key].enabled;
    } else if (Object.prototype.hasOwnProperty.call(defaultSegments, key)) {
      out[key] = defaultSegments[key];
    }
  }
  return out;
}

function cleanFields(fields) {
  if (!isObj(fields)) return null;
  const out = {};
  for (const key of Object.keys(fields)) {
    if (fields[key] !== undefined) out[key] = fields[key];
  }
  return Object.keys(out).length ? out : null;
}

function normalizeOverrideBlock(block) {
  if (!isObj(block)) return {};
  const out = {};
  if (typeof block.enabled === 'boolean') out.enabled = block.enabled;
  const fields = cleanFields(block.fields);
  if (fields) out.fields = fields;
  return out;
}

function overrideFromParts({ enabled, segments, order, fields }) {
  const overrides = {};
  for (const key of [...(Array.isArray(order) ? order : []), ...Object.keys(segments || {})]) {
    if (!key || Object.prototype.hasOwnProperty.call(overrides, key)) continue;
    const block = { enabled: !!(segments && segments[key]) };
    const blockFields = cleanFields(isObj(fields) ? fields[key] : null);
    if (blockFields) block.fields = blockFields;
    overrides[key] = block;
  }
  return {
    enabled: !!enabled,
    order: Array.isArray(order) ? order.slice() : [],
    overrides,
  };
}

function stateFromParts(parts) {
  return {
    signature: parts.signature,
    override: overrideFromParts(parts),
  };
}

function readJson(storage, key, fallback) {
  try {
    const raw = storage && storage.getItem(key);
    return raw == null ? fallback : JSON.parse(raw);
  } catch {
    return fallback;
  }
}

function removeLegacyKeys(storage) {
  if (!storage) return;
  for (const key of Object.values(FOOTER_LEGACY_STORAGE_KEYS)) {
    try { storage.removeItem(key); } catch {}
  }
}

function readLegacyState(storage, defaults) {
  if (!storage) return null;
  const hasLegacy = Object.values(FOOTER_LEGACY_STORAGE_KEYS).some(key => {
    try { return storage.getItem(key) != null; } catch { return false; }
  });
  if (!hasLegacy) return null;

  const storedSignature = (() => {
    try { return storage.getItem(FOOTER_LEGACY_STORAGE_KEYS.signature); } catch { return null; }
  })();
  if (storedSignature !== defaults.signature) {
    return stateFromParts(defaults);
  }

  const order = readJson(storage, FOOTER_LEGACY_STORAGE_KEYS.order, []);
  const segments = readJson(storage, FOOTER_LEGACY_STORAGE_KEYS.segments, {});
  const enabledLS = (() => {
    try { return storage.getItem(FOOTER_LEGACY_STORAGE_KEYS.enabled); } catch { return null; }
  })();
  const legacySegments = isObj(segments) ? { ...segments } : {};
  for (const key of defaults.order) {
    if (
      !Object.prototype.hasOwnProperty.call(legacySegments, key)
      && Object.prototype.hasOwnProperty.call(defaults.segments, key)
    ) {
      legacySegments[key] = defaults.segments[key];
    }
  }
  return stateFromParts({
    signature: defaults.signature,
    order: normalizeOrder(order, defaults.order),
    segments: legacySegments,
    enabled: enabledLS === null ? defaults.enabled : enabledLS === '1',
  });
}

function normalizeStoredState(raw, defaults) {
  if (!isObj(raw) || raw.signature !== defaults.signature || !isObj(raw.override)) {
    return stateFromParts(defaults);
  }
  const override = raw.override;
  return {
    signature: defaults.signature,
    override: {
      enabled: typeof override.enabled === 'boolean' ? override.enabled : defaults.enabled,
      order: normalizeOrder(override.order, defaults.order),
      overrides: isObj(override.overrides)
        ? Object.fromEntries(Object.entries(override.overrides).map(([key, block]) => [key, normalizeOverrideBlock(block)]))
        : {},
    },
  };
}

function writeState(storage, accountId, state) {
  if (!storage) return;
  try {
    storage.setItem(footerStateStorageKey(accountId), JSON.stringify(state));
  } catch {}
}

export function readFooterPanelState({
  storage = safeStorage(),
  accountId = 'default',
  footerLib = {},
  fallbackPresets = FOOTER_PRESETS_DEFAULT,
  fallbackOrder = FOOTER_SEGMENT_ORDER_DEFAULT,
} = {}) {
  const footerLibReady = isFooterLibReady(footerLib);
  const defaults = defaultParts(footerLib, fallbackPresets, fallbackOrder);
  if (!footerLibReady) {
    // WHY: Real-browser timing verification on 2026-07-02 found that site.html
    // can call this during the empty-library loading window. Deleting legacy
    // user data against a fallback signature here would burn the one-time
    // migration before readiness is confirmed.
    const state = stateFromParts(defaults);
    const order = normalizeOrder(state.override.order, defaults.order);
    const segments = segmentsFromOverride(state.override, defaults.segments, defaults.order);
    return {
      footerLib,
      enabled: !!state.override.enabled,
      segments,
      order,
      presets: footerPresetList(footerLib, fallbackPresets),
      override: state.override,
      signature: state.signature,
    };
  }

  const key = footerStateStorageKey(accountId);
  const raw = readJson(storage, key, null);
  const legacy = readLegacyState(storage, defaults);
  const shouldDeleteLegacy = legacy != null;
  const state = legacy && !isObj(raw)
    ? legacy
    : normalizeStoredState(raw, defaults);

  if (shouldDeleteLegacy) removeLegacyKeys(storage);
  if (legacy && !isObj(raw)) {
    writeState(storage, accountId, state);
  } else if (!isObj(raw) || JSON.stringify(state) !== JSON.stringify(raw)) {
    writeState(storage, accountId, state);
  }

  const order = normalizeOrder(state.override.order, defaults.order);
  const segments = segmentsFromOverride(state.override, defaults.segments, defaults.order);
  return {
    footerLib,
    enabled: !!state.override.enabled,
    segments,
    order,
    presets: footerPresetList(footerLib, fallbackPresets),
    override: state.override,
    signature: state.signature,
  };
}

export function saveFooterPanelState({
  storage = safeStorage(),
  accountId = 'default',
  footerLib = {},
  enabled,
  segments,
  order,
  fields,
  fallbackPresets = FOOTER_PRESETS_DEFAULT,
  fallbackOrder = FOOTER_SEGMENT_ORDER_DEFAULT,
} = {}) {
  const defaults = defaultParts(footerLib, fallbackPresets, fallbackOrder);
  const cleanOrder = normalizeOrder(order, defaults.order);
  const state = stateFromParts({
    signature: defaults.signature,
    enabled,
    segments: isObj(segments) ? { ...segments } : defaults.segments,
    order: cleanOrder,
    fields: isObj(fields) ? { ...fields } : {},
  });
  writeState(storage, accountId, state);
  removeLegacyKeys(storage);
  return state.override;
}
