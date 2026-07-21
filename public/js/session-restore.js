// session-restore.js
//
// Cross-channel editor draft preservation. Drafts are scoped to the exact
// channel/account/document and carry rule/build fingerprints, so a snapshot
// from another account or an older renderer can never be restored silently.

const DRAFT_KEY_PREFIX = 'fbif_draft:';
const INDEX_KEY_PREFIX = 'fbif_draft_index:';
const LEGACY_KEY_PREFIX = 'fbif_session_snapshot_';
const SCHEMA_VERSION = 2;
const EDITOR_SNAPSHOT_SCHEMA_VERSION = 1;
const MAX_BYTES = 4 * 1024 * 1024;
const DRAFT_TTL_MS = 24 * 60 * 60 * 1000;
const DEGRADED_COPY_MESSAGE = '文章内容过大，仅恢复了预览。请重新导入后再复制。';

let activeDocumentID = '';
let activeDocumentCreatedAt = '';
let pendingRestore = null;

function storage() {
  return globalThis.sessionStorage;
}

function encodeKeyPart(value) {
  return encodeURIComponent(String(value || '').trim());
}

function indexKey(channel, accountID) {
  return INDEX_KEY_PREFIX + encodeKeyPart(channel) + ':' + encodeKeyPart(accountID);
}

function draftKey(channel, accountID, documentID) {
  return DRAFT_KEY_PREFIX + encodeKeyPart(channel) + ':' + encodeKeyPart(accountID) + ':' + encodeKeyPart(documentID);
}

function currentChannel() {
  const p = globalThis.location?.pathname || globalThis.window?.location?.pathname || '';
  if (p.startsWith('/mp')) return 'mp';
  if (p.startsWith('/ft')) return 'ft';
  return null;
}

function stableSerialize(value, seen = new WeakSet()) {
  if (value === null || typeof value !== 'object') return JSON.stringify(value ?? null);
  if (seen.has(value)) return '"[Circular]"';
  seen.add(value);
  let result;
  if (Array.isArray(value)) {
    result = '[' + value.map(item => stableSerialize(item, seen)).join(',') + ']';
  } else {
    result = '{' + Object.keys(value).sort().map(key => (
      JSON.stringify(key) + ':' + stableSerialize(value[key], seen)
    )).join(',') + '}';
  }
  seen.delete(value);
  return result;
}

// beforeunload capture must stay synchronous, so use a deterministic lightweight
// fingerprint rather than asynchronous Web Crypto. Account, revision and build
// are checked alongside it; this is an identity guard, not a security signature.
function fingerprint(value) {
  const text = typeof value === 'string' ? value : stableSerialize(value);
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i += 1) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return 'fnv1a32-' + hash.toString(16).padStart(8, '0');
}

function rendererBuild() {
  const info = globalThis.window?.__APP_BUILD_INFO || {};
  return String(info.build || info.commit || info.asset_version || 'unknown');
}

function currentDraftContext(channel) {
  const win = globalThis.window;
  const accountID = String(win?._activeRulePack?.id || '').trim();
  if (!channel || !accountID) return null;
  const active = win?._activeConfig || {};
  const preset = active.preset || {};
  const configHash = fingerprint({ config: active.config || {}, behavior: active.behavior || {} });
  return {
    channel,
    accountID,
    ruleID: String(preset.id || 'local'),
    ruleRevision: String(preset.version || preset.revision || preset.updated_at || configHash),
    configHash,
    rendererBuild: rendererBuild(),
  };
}

function newDocumentID() {
  try {
    if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID();
  } catch {}
  return 'draft-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 12);
}

function safeGetEditorSnapshot() {
  const capture = globalThis.window?._captureSessionSnapshot;
  if (typeof capture !== 'function') return null;
  try {
    return capture();
  } catch (error) {
    console.warn('[session-restore] capture failed:', error);
    return null;
  }
}

function byteLength(text) {
  try {
    return new TextEncoder().encode(text).byteLength;
  } catch {
    return text.length * 2;
  }
}

function downgradeEditorSnapshot(snap) {
  return {
    schema_version: snap.schema_version,
    timestamp: snap.timestamp,
    closure_state: {
      article_html: '',
      article_copy: '',
      footer_html: '',
      footer_copy: '',
      copy_unavailable_reason: DEGRADED_COPY_MESSAGE,
      plain_text_copy: null,
      is_repost: false,
      source_is_docx: false,
      failed_srcs: [],
      upload_done: false,
    },
    dom: snap.dom,
    degraded: true,
    degraded_reason: DEGRADED_COPY_MESSAGE,
  };
}

function sourceHash(editorSnapshot) {
  const state = editorSnapshot?.closure_state || {};
  return fingerprint(state.last_render_input || state.article_session?.input || {
    title: editorSnapshot?.dom?.title_input_value || editorSnapshot?.dom?.article_title_text || '',
    importValue: editorSnapshot?.dom?.import_input_value || '',
  });
}

function artifactHash(editorSnapshot) {
  const state = editorSnapshot?.closure_state || {};
  return fingerprint({
    article: state.article_copy || state.article_html || '',
    footer: state.footer_copy || state.footer_html || '',
    preview: editorSnapshot?.dom?.content_area_html || '',
  });
}

function makeEnvelope(context, editorSnapshot) {
  const now = Date.now();
  if (!activeDocumentID) activeDocumentID = newDocumentID();
  if (!activeDocumentCreatedAt) activeDocumentCreatedAt = new Date(now).toISOString();
  return {
    schema_version: SCHEMA_VERSION,
    document_id: activeDocumentID,
    channel: context.channel,
    account_id: context.accountID,
    rule_id: context.ruleID,
    rule_revision: context.ruleRevision,
    config_hash: context.configHash,
    renderer_build: context.rendererBuild,
    created_at: activeDocumentCreatedAt,
    expires_at: new Date(now + DRAFT_TTL_MS).toISOString(),
    source_hash: sourceHash(editorSnapshot),
    artifact_hash: artifactHash(editorSnapshot),
    editor_snapshot: editorSnapshot,
  };
}

function parseJSON(raw) {
  try { return JSON.parse(raw); } catch { return null; }
}

function readIndex(store, key) {
  const value = parseJSON(store.getItem(key));
  return value && typeof value.document_id === 'string' ? value : null;
}

export function captureChannel(channel) {
  const context = currentDraftContext(channel);
  if (!context) return false;
  const editorSnapshot = safeGetEditorSnapshot();
  if (!editorSnapshot) return false;
  if (!editorSnapshot.dom?.preview_visible) {
    clearChannel(channel);
    return false;
  }

  let envelope = makeEnvelope(context, editorSnapshot);
  let serialized = JSON.stringify(envelope);
  if (byteLength(serialized) > MAX_BYTES) {
    const degraded = downgradeEditorSnapshot(editorSnapshot);
    envelope = {
      ...makeEnvelope(context, degraded),
      degraded: true,
      degraded_reason: DEGRADED_COPY_MESSAGE,
    };
    serialized = JSON.stringify(envelope);
    console.warn('[session-restore] snapshot exceeds %d bytes, degraded to DOM-only', MAX_BYTES);
  }

  const store = storage();
  if (!store) return false;
  const pointerKey = indexKey(channel, context.accountID);
  const previous = readIndex(store, pointerKey);
  const key = draftKey(channel, context.accountID, envelope.document_id);
  try {
    // Draft first, pointer second: a quota failure cannot leave an index pointing
    // at a document that was never persisted.
    store.setItem(key, serialized);
    store.setItem(pointerKey, JSON.stringify({
      document_id: envelope.document_id,
      expires_at: envelope.expires_at,
    }));
    store.removeItem(LEGACY_KEY_PREFIX + channel);
    if (previous && previous.document_id !== envelope.document_id) {
      store.removeItem(draftKey(channel, context.accountID, previous.document_id));
    }
    return true;
  } catch (error) {
    console.warn('[session-restore] sessionStorage.setItem failed:', error);
    return false;
  }
}

function discardEnvelope(channel, accountID, documentID) {
  const store = storage();
  if (!store) return;
  try {
    store.removeItem(draftKey(channel, accountID, documentID));
    store.removeItem(indexKey(channel, accountID));
  } catch {}
}

function envelopeIntegrityOK(envelope) {
  const editorSnapshot = envelope?.editor_snapshot;
  return !!(
    editorSnapshot &&
    editorSnapshot.schema_version === EDITOR_SNAPSHOT_SCHEMA_VERSION &&
    envelope.source_hash === sourceHash(editorSnapshot) &&
    envelope.artifact_hash === artifactHash(editorSnapshot)
  );
}

function compatibilityMismatches(envelope, context) {
  const mismatches = [];
  if (envelope.renderer_build !== context.rendererBuild) mismatches.push('版本');
  if (envelope.rule_id !== context.ruleID || envelope.rule_revision !== context.ruleRevision) mismatches.push('规则');
  if (envelope.config_hash !== context.configHash) mismatches.push('配置');
  return Array.from(new Set(mismatches));
}

function restoreEnvelope(envelope) {
  const restore = globalThis.window?._restoreSessionSnapshot;
  if (typeof restore !== 'function') return false;
  try {
    const restored = restore(envelope.editor_snapshot) === true;
    if (restored) {
      activeDocumentID = envelope.document_id;
      activeDocumentCreatedAt = envelope.created_at;
    }
    return restored;
  } catch (error) {
    console.warn('[session-restore] restore failed:', error);
    return false;
  }
}

function removeRestoreNotice() {
  globalThis.document?.getElementById('draftRestoreNotice')?.remove();
}

export async function restorePending(useCurrentRules = false) {
  const pending = pendingRestore;
  if (!pending) return false;
  const restored = restoreEnvelope(pending.envelope);
  if (!restored) return false;
  pendingRestore = null;
  removeRestoreNotice();
  if (useCurrentRules && typeof globalThis.window?._engineApi?.rerender === 'function') {
    await globalThis.window._engineApi.rerender({ __detailedOutcome: true });
  }
  return true;
}

function showRestoreNotice(mismatches) {
  const doc = globalThis.document;
  if (!doc || doc.getElementById('draftRestoreNotice')) return;
  const banner = doc.createElement('div');
  banner.id = 'draftRestoreNotice';
  banner.className = 'version-update-banner';
  banner.setAttribute('role', 'status');
  banner.setAttribute('aria-live', 'polite');

  const message = doc.createElement('span');
  message.className = 'version-update-message';
  message.textContent = '检测到旧草稿，但它使用了不同的' + mismatches.join('、') + '，未自动恢复。';

  const restoreOriginal = doc.createElement('button');
  restoreOriginal.type = 'button';
  restoreOriginal.className = 'version-update-action secondary';
  restoreOriginal.textContent = '恢复原稿';
  restoreOriginal.addEventListener('click', () => { restorePending(false); }, { once: true });

  const rerender = doc.createElement('button');
  rerender.type = 'button';
  rerender.className = 'version-update-action';
  rerender.textContent = '按当前规则重排';
  rerender.addEventListener('click', () => { restorePending(true); }, { once: true });

  banner.append(message, restoreOriginal, rerender);
  const spacer = doc.querySelector('.global-channel-bar .topbar-spacer');
  if (spacer) spacer.before(banner);
  else doc.body?.prepend(banner);
}

async function waitForActiveRules() {
  const ready = globalThis.window?._activeRuleReadyPromise;
  if (!ready || typeof ready.then !== 'function') return;
  try { await ready; } catch {}
}

export async function restoreChannel(channel) {
  await waitForActiveRules();
  const context = currentDraftContext(channel);
  const restore = globalThis.window?._restoreSessionSnapshot;
  if (!context || typeof restore !== 'function') return false;
  const store = storage();
  if (!store) return false;
  try { store.removeItem(LEGACY_KEY_PREFIX + channel); } catch {}

  let pointer;
  try { pointer = readIndex(store, indexKey(channel, context.accountID)); } catch { return false; }
  if (!pointer) return false;
  const key = draftKey(channel, context.accountID, pointer.document_id);
  let envelope;
  try { envelope = parseJSON(store.getItem(key)); } catch { return false; }
  if (!envelope || envelope.schema_version !== SCHEMA_VERSION ||
      envelope.channel !== channel || envelope.account_id !== context.accountID ||
      envelope.document_id !== pointer.document_id || !envelopeIntegrityOK(envelope)) {
    discardEnvelope(channel, context.accountID, pointer.document_id);
    return false;
  }
  const expiresAt = Date.parse(envelope.expires_at);
  if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
    discardEnvelope(channel, context.accountID, pointer.document_id);
    return false;
  }

  const mismatches = compatibilityMismatches(envelope, context);
  if (mismatches.length) {
    pendingRestore = { envelope, mismatches };
    globalThis.window._sessionRestoreLastRejection = {
      document_id: envelope.document_id,
      reasons: mismatches.slice(),
    };
    showRestoreNotice(mismatches);
    return false;
  }
  return restoreEnvelope(envelope);
}

export function clearChannel(channel) {
  if (!channel) return;
  const accountID = String(globalThis.window?._activeRulePack?.id || '').trim();
  const store = storage();
  if (!store) return;
  try {
    store.removeItem(LEGACY_KEY_PREFIX + channel);
    if (accountID) {
      const pointerKey = indexKey(channel, accountID);
      const pointer = readIndex(store, pointerKey);
      if (pointer) store.removeItem(draftKey(channel, accountID, pointer.document_id));
      store.removeItem(pointerKey);
    }
  } catch {}
  activeDocumentID = '';
  activeDocumentCreatedAt = '';
  pendingRestore = null;
  removeRestoreNotice();
}

export function clearCurrentChannel() {
  const channel = currentChannel();
  if (!channel) return false;
  clearChannel(channel);
  return true;
}

export function bindChannelSwitcher() {
  const win = globalThis.window;
  if (!win || win._sessionRestoreBound) return;
  win._sessionRestoreBound = true;
  win.addEventListener('beforeunload', () => {
    const channel = currentChannel();
    if (channel) captureChannel(channel);
  });
  win.addEventListener('fbif:article-input-started', () => {
    activeDocumentID = newDocumentID();
    activeDocumentCreatedAt = new Date().toISOString();
    pendingRestore = null;
    removeRestoreNotice();
  });
  win.addEventListener('fbif:article-cleared', () => {
    clearCurrentChannel();
  });
}

export async function restoreCurrentChannel() {
  const channel = currentChannel();
  if (!channel) return false;
  return restoreChannel(channel);
}

if (typeof globalThis.window !== 'undefined') {
  globalThis.window._sessionRestore = {
    capture: captureChannel,
    restore: restoreChannel,
    restorePending,
    clear: clearChannel,
    clearCurrent: clearCurrentChannel,
    bindSwitcher: bindChannelSwitcher,
    restoreCurrent: restoreCurrentChannel,
    currentChannel,
  };
}
