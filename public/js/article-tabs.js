// article-tabs.js — workspace-strip article tabs for the editor shell.
//
// The editor still has one physical preview/copy surface. Each tab stores an
// engine snapshot, then restores that snapshot into the same surface when the
// user switches tabs.

const SCHEMA_VERSION = 1;
const STORAGE_PREFIX = 'fbif_article_tabs_v1::';
const MAX_TABS = 8;
const MAX_STORE_BYTES = 4 * 1024 * 1024;

function cleanTitle(value) {
  const text = String(value || '').replace(/\.\w+$/i, '').trim();
  return text || '新文章';
}

function uid() {
  return 'tab-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8);
}

function safeClone(value) {
  if (value == null) return value;
  try { return JSON.parse(JSON.stringify(value)); } catch { return null; }
}

function approxBytes(value) {
  const raw = JSON.stringify(value);
  try { return new Blob([raw]).size; } catch { return raw.length * 2; }
}

function snapshotTitle(snapshot) {
  const dom = snapshot && snapshot.dom;
  return cleanTitle(dom && (dom.article_title_text || dom.title_input_value || dom.title_display || dom.doc_name));
}

function snapshotHasPreview(snapshot) {
  const dom = snapshot && snapshot.dom;
  return !!(dom && (dom.preview_visible || dom.batch_visible));
}

function currentAccountID(windowRef) {
  return (windowRef._activeRulePack && windowRef._activeRulePack.id) || 'fbif';
}

function makeEmptySnapshot(documentRef, windowRef) {
  const date = documentRef.getElementById('wxDate');
  return {
    schema_version: 1,
    timestamp: Date.now(),
    closure_state: {
      article_html: '',
      article_copy: '',
      footer_html: '',
      footer_copy: '',
      is_repost: false,
      source_is_docx: false,
      copy_requires_wechat_image_transfer: false,
      failed_srcs: [],
      upload_done: false,
      last_render_input: null,
    },
    batch_state: { file_meta: [], results: [] },
    dom: {
      content_area_html: '',
      content_area_style: '',
      title_display: '',
      title_input_value: '',
      article_title_text: '',
      date_text: date ? date.textContent : '',
      preview_visible: false,
      empty_visible: true,
      footer_visible: false,
      batch_visible: false,
      batch_html: '',
      has_preview_class: false,
      import_visible: true,
      doc_visible: false,
      doc_name: '',
      doc_stat: '',
      stats_text: '',
      preview_scroll_top: 0,
    },
    article_state: {
      account_id: currentAccountID(windowRef),
      more_articles_cards: [],
      bottom_blocks_override: null,
    },
  };
}

export function mountArticleTabs({
  container,
  refreshButton,
  windowRef = window,
  documentRef = document,
  maxTabs = MAX_TABS,
} = {}) {
  if (!container) return null;

  let tabs = [];
  let activeID = '';

  function storageKey() {
    const path = windowRef.location ? windowRef.location.pathname : 'app';
    return STORAGE_PREFIX + path + '::' + currentAccountID(windowRef);
  }

  function captureSnapshot() {
    if (typeof windowRef._captureSessionSnapshot !== 'function') return null;
    try { return windowRef._captureSessionSnapshot(); } catch { return null; }
  }

  function restoreSnapshot(snapshot) {
    if (typeof windowRef._restoreSessionSnapshot !== 'function') return false;
    return windowRef._restoreSessionSnapshot(snapshot || makeEmptySnapshot(documentRef, windowRef));
  }

  function activeTab() {
    return tabs.find(tab => tab.id === activeID) || null;
  }

  function hasLivePreview() {
    const preview = documentRef.getElementById('previewContent');
    return !!(preview && preview.style.display !== 'none');
  }

  function captureActive() {
    const tab = activeTab();
    if (!tab) return false;
    const snapshot = captureSnapshot();
    if (!snapshot) return false;
    tab.snapshot = snapshot;
    tab.title = snapshotTitle(snapshot) || tab.title;
    tab.updated_at = Date.now();
    return true;
  }

  function save() {
    const payload = { schema_version: SCHEMA_VERSION, active_id: activeID, tabs };
    try {
      if (approxBytes(payload) <= MAX_STORE_BYTES) {
        windowRef.sessionStorage.setItem(storageKey(), JSON.stringify(payload));
      } else {
        windowRef.sessionStorage.removeItem(storageKey());
      }
    } catch {}
  }

  function load() {
    try {
      const raw = windowRef.sessionStorage.getItem(storageKey());
      if (!raw) return false;
      const payload = JSON.parse(raw);
      if (!payload || payload.schema_version !== SCHEMA_VERSION || !Array.isArray(payload.tabs)) return false;
      tabs = payload.tabs
        .filter(tab => tab && tab.id)
        .slice(0, maxTabs)
        .map(tab => ({
          id: String(tab.id),
          title: cleanTitle(tab.title),
          snapshot: safeClone(tab.snapshot),
          pending: !!tab.pending,
          updated_at: Number(tab.updated_at) || Date.now(),
        }));
      activeID = payload.active_id && tabs.some(tab => tab.id === payload.active_id)
        ? payload.active_id
        : (tabs[0] && tabs[0].id) || '';
      return tabs.length > 0;
    } catch {
      return false;
    }
  }

  function addTab(title, snapshot = null) {
    if (tabs.length >= maxTabs) tabs.shift();
    const tab = {
      id: uid(),
      title: cleanTitle(title),
      snapshot,
      pending: !snapshot,
      updated_at: Date.now(),
    };
    tabs.push(tab);
    activeID = tab.id;
    return tab;
  }

  function ensureInitialTab() {
    if (tabs.length) return;
    const snapshot = captureSnapshot();
    if (snapshot && snapshotHasPreview(snapshot)) {
      addTab(snapshotTitle(snapshot), snapshot);
    } else {
      addTab('新文章', makeEmptySnapshot(documentRef, windowRef));
    }
  }

  function updateRefreshState() {
    if (!refreshButton) return;
    const snapshot = activeTab() && activeTab().snapshot;
    const canRefresh = !!(snapshot && snapshot.closure_state && snapshot.closure_state.last_render_input);
    refreshButton.disabled = !canRefresh;
    refreshButton.setAttribute('aria-disabled', canRefresh ? 'false' : 'true');
  }

  function render() {
    ensureInitialTab();
    container.innerHTML = '';
    const list = documentRef.createElement('div');
    list.className = 'article-tab-list';
    list.setAttribute('role', 'tablist');
    list.setAttribute('aria-label', '文章标签');

    tabs.forEach((tab, index) => {
      const item = documentRef.createElement('div');
      item.className = 'article-tab-item' + (tab.id === activeID ? ' is-active' : '') + (tab.pending ? ' is-pending' : '');

      const main = documentRef.createElement('button');
      main.type = 'button';
      main.className = 'article-tab-main';
      main.id = 'article-tab-' + tab.id;
      main.setAttribute('role', 'tab');
      main.setAttribute('aria-selected', tab.id === activeID ? 'true' : 'false');
      main.setAttribute('aria-controls', 'previewContent');
      main.tabIndex = tab.id === activeID ? 0 : -1;
      main.title = tab.title;
      main.textContent = tab.title;
      main.addEventListener('click', () => switchTo(tab.id));
      main.addEventListener('keydown', event => handleTabKey(event, index));

      const close = documentRef.createElement('button');
      close.type = 'button';
      close.className = 'article-tab-close';
      close.setAttribute('aria-label', '关闭 ' + tab.title);
      close.title = '关闭';
      close.textContent = '×';
      close.addEventListener('click', event => {
        event.stopPropagation();
        closeTab(tab.id);
      });

      item.appendChild(main);
      item.appendChild(close);
      list.appendChild(item);
    });

    const add = documentRef.createElement('button');
    add.type = 'button';
    add.className = 'article-tab-add';
    add.title = '新建空白文章';
    add.setAttribute('aria-label', '新建空白文章');
    add.textContent = '+';
    add.addEventListener('click', () => newBlankTab());
    list.appendChild(add);

    container.appendChild(list);
    updateRefreshState();
  }

  function focusTab(index) {
    const main = container.querySelectorAll('.article-tab-main')[index];
    if (main) main.focus();
  }

  function handleTabKey(event, index) {
    if (!tabs.length) return;
    let next = index;
    if (event.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length;
    else if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = tabs.length - 1;
    else if (event.key === 'Delete') {
      event.preventDefault();
      closeTab(tabs[index].id);
      return;
    } else {
      return;
    }
    event.preventDefault();
    switchTo(tabs[next].id);
    focusTab(next);
  }

  function switchTo(id) {
    if (!id || id === activeID) return;
    captureActive();
    activeID = id;
    const tab = activeTab();
    restoreSnapshot(tab && tab.snapshot);
    render();
    save();
  }

  function newBlankTab() {
    captureActive();
    const tab = addTab('新文章', makeEmptySnapshot(documentRef, windowRef));
    restoreSnapshot(tab.snapshot);
    render();
    save();
  }

  function closeTab(id) {
    const index = tabs.findIndex(tab => tab.id === id);
    if (index < 0) return;
    const closingActive = tabs[index].id === activeID;
    if (!closingActive) {
      tabs.splice(index, 1);
      render();
      save();
      return;
    }
    tabs.splice(index, 1);
    if (!tabs.length) {
      const blank = addTab('新文章', makeEmptySnapshot(documentRef, windowRef));
      restoreSnapshot(blank.snapshot);
    } else {
      const next = tabs[Math.min(index, tabs.length - 1)];
      activeID = next.id;
      restoreSnapshot(next.snapshot);
    }
    render();
    save();
  }

  function prepareForNewArticle({ title } = {}) {
    captureActive();
    const current = activeTab();
    const currentHasPreview = hasLivePreview() || snapshotHasPreview(current && current.snapshot);
    if (current && !currentHasPreview) {
      current.title = cleanTitle(title);
      current.pending = true;
      current.updated_at = Date.now();
    } else {
      addTab(cleanTitle(title), null);
    }
    render();
    save();
  }

  function refreshActive() {
    if (!windowRef._engineApi || typeof windowRef._engineApi.rerender !== 'function') return;
    if (refreshButton) refreshButton.classList.add('is-refreshing');
    Promise.resolve(windowRef._engineApi.rerender()).finally(() => {
      if (refreshButton) refreshButton.classList.remove('is-refreshing');
      captureActive();
      render();
      save();
    });
  }

  windowRef.addEventListener('fbif:article-previewed', event => {
    const tab = activeTab() || addTab(event.detail && event.detail.title, null);
    const snapshot = captureSnapshot();
    if (snapshot) tab.snapshot = snapshot;
    tab.title = cleanTitle((event.detail && event.detail.title) || snapshotTitle(snapshot));
    tab.pending = false;
    tab.updated_at = Date.now();
    render();
    save();
  });

  windowRef.addEventListener('fbif:article-restored', () => {
    updateRefreshState();
  });

  windowRef.addEventListener('beforeunload', () => {
    captureActive();
    save();
  });

  if (refreshButton) refreshButton.addEventListener('click', refreshActive);

  load();
  ensureInitialTab();
  if (tabs.length && activeTab() && activeTab().snapshot) restoreSnapshot(activeTab().snapshot);
  render();

  return {
    prepareForNewArticle,
    captureActive: () => { const ok = captureActive(); save(); return ok; },
    refreshActive,
    switchTo,
    closeTab,
  };
}
