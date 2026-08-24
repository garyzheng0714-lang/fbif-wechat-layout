const DEFAULT_INTERVAL_MS = 5 * 60 * 1000;

// The build id is only for JS/CSS cache busting. It must not sit in the page
// URL, otherwise people bookmark /mp/fbif?v=<sha> and share a stale stamp.
export function stripBuildVersionSearch(search) {
  const raw = String(search || '');
  const params = new URLSearchParams(raw.startsWith('?') ? raw.slice(1) : raw);
  if (!params.has('v')) {
    return { changed: false, search: raw.startsWith('?') || raw === '' ? raw : `?${raw}` };
  }
  params.delete('v');
  const next = params.toString();
  return { changed: true, search: next ? `?${next}` : '' };
}

export function stripBuildVersionFromLocation({
  locationRef = globalThis.location,
  historyRef = globalThis.history,
} = {}) {
  if (!locationRef || typeof historyRef?.replaceState !== 'function') return false;
  const stripped = stripBuildVersionSearch(locationRef.search || '');
  if (!stripped.changed) return false;
  historyRef.replaceState(null, '', `${locationRef.pathname || ''}${stripped.search}${locationRef.hash || ''}`);
  return true;
}

export function decideVersionRefresh({ currentBuildId, latestBuildId, isDevBuild, hasUnsavedWork }) {
  if (isDevBuild || !latestBuildId || latestBuildId === currentBuildId) return 'none';
  return hasUnsavedWork ? 'prompt' : 'reload';
}

export function defaultHasUnsavedWork(win = window, doc = document) {
  try {
    if (typeof win._hasUnsavedEditorWork === 'function') {
      return !!win._hasUnsavedEditorWork();
    }
  } catch {}
  const preview = doc.getElementById('previewContent');
  const input = doc.getElementById('urlInput');
  return !!(
    win._copyInProgress
    || (preview && preview.style.display !== 'none')
    || (input && String(input.value || '').trim())
    || doc.body?.classList?.contains('has-preview')
  );
}

export function persistCurrentDraft(win = window) {
  try {
    const bridge = win._sessionRestore;
    const channel = bridge?.currentChannel?.();
    if (channel) bridge.capture?.(channel);
  } catch (error) {
    console.warn('[version-refresh] draft capture failed:', error);
  }
}

export function showVersionUpdateBanner(onRefresh, doc = document) {
  let banner = doc.getElementById('versionUpdateBanner');
  if (banner) return banner;

  banner = doc.createElement('div');
  banner.id = 'versionUpdateBanner';
  banner.className = 'version-update-banner';
  banner.setAttribute('role', 'status');
  banner.setAttribute('aria-live', 'polite');

  const message = doc.createElement('span');
  message.className = 'version-update-message';
  message.textContent = '新版本已准备好。为避免影响当前文章，请完成复制后再刷新。';

  const action = doc.createElement('button');
  action.type = 'button';
  action.className = 'version-update-action';
  action.textContent = '刷新更新';
  action.addEventListener('click', onRefresh, { once: true });

  banner.append(message, action);
  const spacer = doc.querySelector('.global-channel-bar .topbar-spacer');
  if (spacer) spacer.before(banner);
  else doc.body.prepend(banner);
  return banner;
}

export function createVersionRefreshController({
  currentBuildId,
  isDevBuild,
  loadVersionInfo,
  hasUnsavedWork = defaultHasUnsavedWork,
  showUpdate = showVersionUpdateBanner,
  persistDraft = persistCurrentDraft,
  reload = () => window.location.reload(),
  win = window,
  doc = document,
  intervalMs = DEFAULT_INTERVAL_MS,
} = {}) {
  let checkSequence = 0;
  let announcedBuild = '';
  let intervalID = null;

  const refreshNow = () => {
    persistDraft(win);
    reload();
  };

  async function checkNow() {
    const sequence = ++checkSequence;
    try {
      const latest = await loadVersionInfo();
      if (sequence !== checkSequence) return 'stale';
      const latestBuildId = String(latest?.build || latest?.commit || '');
      const action = decideVersionRefresh({
        currentBuildId,
        latestBuildId,
        isDevBuild,
        hasUnsavedWork: hasUnsavedWork(win, doc),
      });
      if (action === 'reload') {
        refreshNow();
      } else if (action === 'prompt' && announcedBuild !== latestBuildId) {
        announcedBuild = latestBuildId;
        showUpdate(refreshNow, doc);
      }
      return action;
    } catch {
      return 'error';
    }
  }

  const onFocus = () => { checkNow(); };
  const onVisibility = () => { if (!doc.hidden) checkNow(); };
  const onPageShow = (event) => { if (event.persisted) checkNow(); };

  function start() {
    win.addEventListener('focus', onFocus);
    doc.addEventListener('visibilitychange', onVisibility);
    win.addEventListener('pageshow', onPageShow);
    intervalID = win.setInterval(checkNow, intervalMs);
    return controller;
  }

  function stop() {
    win.removeEventListener('focus', onFocus);
    doc.removeEventListener('visibilitychange', onVisibility);
    win.removeEventListener('pageshow', onPageShow);
    if (intervalID !== null) win.clearInterval(intervalID);
    intervalID = null;
  }

  const controller = { checkNow, start, stop };
  return controller;
}

export function installVersionRefresh(options) {
  return createVersionRefreshController(options).start();
}
