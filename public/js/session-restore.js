// session-restore.js
//
// Cross-channel editor state preservation. When the top channel pill switches
// the user from /mp/* to /ft/* (or back), the destination page reloads — all
// in-memory editor state (engine.js closure variables, DOM contents) would
// otherwise be lost. This module bridges that gap via sessionStorage.
//
// Contract:
//   - engine.js exposes window._captureSessionSnapshot() / _restoreSessionSnapshot(snap).
//   - This module serializes those snapshots under per-channel keys.
//   - The top pill's switchChannel() calls captureCurrentChannel() then navigates.
//   - On the destination page, restoreCurrentChannel() runs after engine.js has
//     wired up its hooks (otherwise _restoreSessionSnapshot is undefined).
//
// Capacity guard: sessionStorage typically caps at 5–10 MB per origin. Large
// articles (heavy images encoded as base64 in `_articleCopy`) can blow past
// that. If serialized snapshot exceeds 4 MB, we degrade gracefully by storing
// only the DOM-visible portion (no closure copies) — preview survives but
// copy button will hand back stale/empty content until the user re-uploads.

const STORAGE_KEY_PREFIX = 'fbif_session_snapshot_';
const SCHEMA_VERSION = 1;
const MAX_BYTES = 4 * 1024 * 1024;

function keyFor(channel) {
  return STORAGE_KEY_PREFIX + channel;
}

function currentChannel() {
  const p = location.pathname;
  if (p.startsWith('/mp')) return 'mp';
  if (p.startsWith('/ft')) return 'ft';
  return null;
}

function safeGetSnapshot() {
  if (typeof window._captureSessionSnapshot !== 'function') return null;
  try {
    return window._captureSessionSnapshot();
  } catch (e) {
    console.warn('[session-restore] capture failed:', e);
    return null;
  }
}

function approxByteLength(obj) {
  try {
    return new Blob([JSON.stringify(obj)]).size;
  } catch {
    return JSON.stringify(obj).length * 2;
  }
}

function downgradeSnapshot(snap) {
  // Drop closure-only fields, keep DOM so the user at least sees the preview.
  // Copy button will operate on stale closure state (empty after restore),
  // forcing a re-upload — acceptable degradation for large articles.
  return {
    schema_version: snap.schema_version,
    timestamp: snap.timestamp,
    closure_state: {
      article_html: '',
      article_copy: '',
      footer_html: '',
      footer_copy: '',
      is_repost: false,
      source_is_docx: false,
      failed_srcs: [],
      upload_done: false,
    },
    batch_state: { file_meta: [], results: [] },
    dom: snap.dom,
    degraded: true,
  };
}

export function captureChannel(channel) {
  if (!channel) return false;
  const snap = safeGetSnapshot();
  if (!snap) return false;
  // Don't persist empty editors — saves quota and avoids noise.
  if (!snap.dom?.preview_visible && !snap.dom?.batch_visible) {
    sessionStorage.removeItem(keyFor(channel));
    return false;
  }
  let payload = snap;
  if (approxByteLength(payload) > MAX_BYTES) {
    payload = downgradeSnapshot(snap);
    console.warn('[session-restore] snapshot exceeds %d bytes, degraded to DOM-only', MAX_BYTES);
  }
  try {
    sessionStorage.setItem(keyFor(channel), JSON.stringify(payload));
    return true;
  } catch (e) {
    console.warn('[session-restore] sessionStorage.setItem failed:', e);
    return false;
  }
}

export function restoreChannel(channel) {
  if (!channel) return false;
  if (typeof window._restoreSessionSnapshot !== 'function') return false;
  let raw;
  try {
    raw = sessionStorage.getItem(keyFor(channel));
  } catch {
    return false;
  }
  if (!raw) return false;
  let snap;
  try {
    snap = JSON.parse(raw);
  } catch {
    sessionStorage.removeItem(keyFor(channel));
    return false;
  }
  if (!snap || snap.schema_version !== SCHEMA_VERSION) {
    sessionStorage.removeItem(keyFor(channel));
    return false;
  }
  try {
    return window._restoreSessionSnapshot(snap);
  } catch (e) {
    console.warn('[session-restore] restore failed:', e);
    return false;
  }
}

export function clearChannel(channel) {
  if (!channel) return;
  try { sessionStorage.removeItem(keyFor(channel)); } catch {}
}

// Wire up the top channel pill so clicks flush state before navigating.
// 跨渠道编辑器快照兜底：账号切换走统一顶栏菜单（account-menu.js），跨模板时用
// location.href 整页导航——这里在 beforeunload 时 captureChannel 保存当前渠道快照。
// 覆盖关 tab、手动改 URL、菜单导航等所有 in-tab 离开路径。旧的 .channel-switcher
// 锚点切换器已删（2026-06-02 单层账号菜单），不再有 click 拦截分支。
export function bindChannelSwitcher() {
  window.addEventListener('beforeunload', () => {
    const ch = currentChannel();
    if (ch) captureChannel(ch);
  });
}

// Auto-restore on load — call after engine.js has initialized.
export function restoreCurrentChannel() {
  const ch = currentChannel();
  if (!ch) return false;
  return restoreChannel(ch);
}

// Convenience export for inline scripts / debugging.
if (typeof window !== 'undefined') {
  window._sessionRestore = {
    capture: captureChannel,
    restore: restoreChannel,
    clear: clearChannel,
    bindSwitcher: bindChannelSwitcher,
    restoreCurrent: restoreCurrentChannel,
    currentChannel,
  };
}
