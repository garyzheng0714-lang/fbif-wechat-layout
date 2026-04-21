// Sidebar UI for the "更多文章" section.
// - Cards are dynamic length (0..N).
// - Empty state shows a red-bordered prompt.
// - Each card can be reordered via drag, and deleted via × button.
// - A "+ 添加更多文章" button below the list appends a blank card.
// Called by: public/app.html inline <script type="module"> on startup.

import {
  loadCards,
  saveCards,
  resetCards,
  compositePreviewDataUrl,
  emptyCard,
} from './more-articles.js';

const SAVE_DEBOUNCE_MS = 400;

function debounce(fn, ms) {
  let t = null;
  return function (...args) {
    if (t) clearTimeout(t);
    t = setTimeout(() => fn.apply(this, args), ms);
  };
}

function isValidMpUrl(s) {
  if (!s) return false;
  s = s.trim();
  return /^https?:\/\/mp\.weixin\.qq\.com\//i.test(s) ||
         /^https?:\/\/([^/]+\.)?weixin\.qq\.com\//i.test(s);
}

// Per-URL in-flight + success cache. Re-pasting the same link resolves
// instantly; a pending fetch is shared, not re-fired.
const metaCache = new Map();
async function fetchArticleMetaCached(url) {
  if (metaCache.has(url)) return metaCache.get(url);
  const p = (async () => {
    const resp = await fetch('/api/fetch-article-meta', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url }),
    });
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({}));
      throw new Error(err.error || ('HTTP ' + resp.status));
    }
    return resp.json();
  })().catch(err => { metaCache.delete(url); throw err; });
  metaCache.set(url, p);
  return p;
}

async function resizeImageToDataUrl(file, maxEdge = 1600, quality = 0.85) {
  const bmp = await createImageBitmap(file);
  const w = bmp.width, h = bmp.height;
  const scale = Math.min(1, maxEdge / Math.max(w, h));
  const outW = Math.round(w * scale);
  const outH = Math.round(h * scale);
  const canvas = document.createElement('canvas');
  canvas.width = outW; canvas.height = outH;
  const ctx = canvas.getContext('2d');
  ctx.drawImage(bmp, 0, 0, outW, outH);
  if (bmp.close) bmp.close();
  return canvas.toDataURL('image/jpeg', quality);
}

// Fetch an existing image URL (data: or remote mmbiz via image-proxy) and
// re-encode to a resized data URL we can save in localStorage as the card
// cover. Same pipeline as resizeImageToDataUrl but sourced from a URL.
async function urlToResizedDataUrl(rawUrl, maxEdge = 1600, quality = 0.85) {
  const src = rawUrl.startsWith('data:')
    ? rawUrl
    : '/api/image-proxy?url=' + encodeURIComponent(rawUrl);
  const img = await new Promise((res, rej) => {
    const i = new Image();
    if (!rawUrl.startsWith('data:')) i.crossOrigin = 'anonymous';
    i.onload = () => res(i);
    i.onerror = () => rej(new Error('image load failed'));
    i.src = src;
  });
  const w = img.naturalWidth || img.width;
  const h = img.naturalHeight || img.height;
  const scale = Math.min(1, maxEdge / Math.max(w, h));
  const outW = Math.round(w * scale);
  const outH = Math.round(h * scale);
  const canvas = document.createElement('canvas');
  canvas.width = outW; canvas.height = outH;
  canvas.getContext('2d').drawImage(img, 0, 0, outW, outH);
  return canvas.toDataURL('image/jpeg', quality);
}

// FLIP (First-Last-Invert-Play): snapshot pre-mutation positions, run the
// DOM mutation, then transition every moved sibling back from its old spot.
// Used during drag-reorder so untouched cards slide into their new slots
// instead of jumping.
function flipReorder(container, mutator) {
  const children = Array.from(container.querySelectorAll('.ma-card'));
  const firstTops = new Map(children.map(el => [el, el.getBoundingClientRect().top]));
  mutator();
  children.forEach(el => {
    if (el.classList.contains('dragging')) return;
    const oldTop = firstTops.get(el);
    const newTop = el.getBoundingClientRect().top;
    const dy = oldTop - newTop;
    if (!dy) return;
    el.style.transition = 'none';
    el.style.transform = `translateY(${dy}px)`;
    requestAnimationFrame(() => {
      el.style.transition = 'transform 200ms cubic-bezier(0.2, 0.9, 0.3, 1)';
      el.style.transform = '';
    });
  });
}

// Minimal modal for picking a cover image. Shows every img src from the
// currently-rendered article (via window._getArticleImageSrcs) as a grid,
// plus an "upload local" escape hatch. Resolves to { type: 'url', url } or
// { type: 'file' } or null (closed).
function openCoverPicker() {
  const srcs = (window._getArticleImageSrcs && window._getArticleImageSrcs()) || [];
  const overlay = document.createElement('div');
  overlay.className = 'ma-gallery-overlay';
  const safeAttr = s => String(s).replace(/"/g, '&quot;').replace(/</g, '&lt;');
  const tile = (src, i) => {
    const thumbSrc = src.startsWith('data:') || src.startsWith('/')
      ? src
      : '/api/image-proxy?url=' + encodeURIComponent(src);
    return `<button type="button" class="ma-gallery-tile" data-src-idx="${i}">` +
      `<img loading="lazy" src="${safeAttr(thumbSrc)}" alt="">` +
      `</button>`;
  };
  overlay.innerHTML = `
    <div class="ma-gallery-dialog" role="dialog" aria-modal="true">
      <div class="ma-gallery-head">
        <div class="ma-gallery-title">选择封面图</div>
        <button type="button" class="ma-gallery-close" aria-label="关闭">×</button>
      </div>
      <div class="ma-gallery-body">
        ${srcs.length === 0
          ? '<div class="ma-gallery-empty">本文暂无图片 — 请使用"上传本地图片"</div>'
          : `<div class="ma-gallery-hint">点击选择本文中的图片作为封面</div>
             <div class="ma-gallery-grid">${srcs.map(tile).join('')}</div>`}
      </div>
      <div class="ma-gallery-foot">
        <button type="button" class="ma-gallery-upload">上传本地图片</button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);

  return new Promise(resolve => {
    let done = false;
    const finish = (value) => {
      if (done) return;
      done = true;
      overlay.remove();
      document.removeEventListener('keydown', onKey);
      resolve(value);
    };
    const onKey = (e) => { if (e.key === 'Escape') finish(null); };
    document.addEventListener('keydown', onKey);
    overlay.addEventListener('click', e => { if (e.target === overlay) finish(null); });
    overlay.querySelector('.ma-gallery-close').addEventListener('click', () => finish(null));
    overlay.querySelector('.ma-gallery-upload').addEventListener('click', () => finish({ type: 'file' }));
    overlay.querySelectorAll('.ma-gallery-tile').forEach(btn => {
      btn.addEventListener('click', () => {
        const i = Number(btn.dataset.srcIdx);
        finish({ type: 'url', url: srcs[i] });
      });
    });
  });
}

export function initMoreArticlesEditor({ openCropEditor } = {}) {
  const container = document.getElementById('moreArticlesEditor');
  const resetBtn = document.getElementById('maResetBtn');
  if (!container) return;

  let cards = loadCards();
  // During drag we mutate the DOM directly (for FLIP animation) without
  // re-rendering; `draggedEl` tracks the live element being moved. The
  // `cards` array is only re-synced from the DOM order on drop.
  let draggedEl = null;

  function pushLivePreview() {
    if (window._updateMoreArticles) {
      window._updateMoreArticles(cards).catch(() => {});
    }
  }

  function persistAndRefresh() {
    saveCards(cards);
    renderAll();
    pushLivePreview();
  }

  function addCard() {
    cards = [...cards, emptyCard()];
    persistAndRefresh();
  }

  function removeCard(idx) {
    cards = cards.filter((_, i) => i !== idx);
    persistAndRefresh();
  }

  // ---- Rendering ----------------------------------------------------------

  function renderAll() {
    container.innerHTML = '';
    if (cards.length === 0) {
      container.appendChild(renderEmptyPlaceholder());
      return;
    }
    cards.forEach((_, i) => container.appendChild(renderCardEditor(i)));
    container.appendChild(renderAddButton());
  }

  function renderEmptyPlaceholder() {
    const el = document.createElement('div');
    el.className = 'ma-empty-placeholder';
    el.innerHTML = `
      <div class="ma-empty-icon">+</div>
      <div class="ma-empty-text">需要补充更多文章推荐</div>
      <button type="button" class="ma-empty-add">添加第一张卡片</button>
    `;
    el.querySelector('.ma-empty-add').addEventListener('click', addCard);
    return el;
  }

  function renderAddButton() {
    const el = document.createElement('button');
    el.className = 'ma-add-btn';
    el.type = 'button';
    el.textContent = '+ 添加更多文章';
    el.addEventListener('click', addCard);
    return el;
  }

  function renderCardEditor(index) {
    const state = cards[index];
    const root = document.createElement('div');
    root.className = 'ma-card';
    root.draggable = true;
    root.dataset.idx = String(index);
    root.innerHTML = `
      <div class="ma-card-head">
        <span class="ma-drag-handle" title="拖动排序">⠿</span>
        <span class="ma-card-num">#${index + 1}</span>
        <span class="ma-card-status" data-role="status"></span>
        <button type="button" class="ma-remove-btn" data-role="remove" title="删除这张卡片" draggable="false">×</button>
      </div>
      <div class="ma-row">
        <input type="url" class="ma-url" data-role="url" placeholder="粘贴公众号链接自动抓取" spellcheck="false" draggable="false">
      </div>
      <div class="ma-thumb" data-role="thumb" draggable="false"></div>
      <div class="ma-row ma-action-row">
        <button type="button" class="ma-action-btn" data-role="replace" title="从本文图片中选择或上传本地" draggable="false">替换图片</button>
        <button type="button" class="ma-action-btn" data-role="crop" title="调整裁剪位置" draggable="false">调整裁剪</button>
      </div>
      <input type="file" class="ma-file" data-role="file" accept="image/*" hidden>
    `;

    const $ = sel => root.querySelector(`[data-role="${sel}"]`);
    $('url').value = state.href || '';

    const setStatus = (text, kind) => {
      const el = $('status');
      el.textContent = text || '';
      el.className = 'ma-card-status' + (kind ? ' ' + kind : '');
    };

    const refreshThumb = async () => {
      const thumb = $('thumb');
      const hasAny = !!(state.href || state.imgurl || state.cover_data_url || state.title);
      if (!hasAny) {
        thumb.classList.add('empty');
        thumb.textContent = '粘贴文章链接自动抓取';
        return;
      }
      try {
        const url = await compositePreviewDataUrl(state);
        if (url) {
          thumb.classList.remove('empty');
          thumb.innerHTML = `<img alt="" src="${url.replace(/"/g, '&quot;')}">`;
        } else {
          thumb.classList.add('empty');
          thumb.textContent = '暂无头图';
        }
      } catch {
        thumb.classList.add('empty');
        thumb.textContent = '加载失败';
      }
    };

    const debouncedSave = debounce(() => {
      saveCards(cards);
      pushLivePreview();
      refreshThumb();
    }, SAVE_DEBOUNCE_MS);

    $('url').addEventListener('input', () => {
      state.href = $('url').value.trim();
      debouncedSave();
    });

    // Track the URL we last auto-fetched so repeated paste/input events for
    // the same string don't re-fire (cache still short-circuits them, but
    // avoiding the duplicate status flicker matters).
    let lastFetchedUrl = '';

    const triggerFetch = async () => {
      const url = $('url').value.trim();
      if (!isValidMpUrl(url)) {
        setStatus('需要公众号链接', 'err');
        return;
      }
      if (url === lastFetchedUrl) return;
      lastFetchedUrl = url;
      setStatus('抓取中…', 'info');
      try {
        const meta = await fetchArticleMetaCached(url);
        state.href = url;
        if (meta.title) state.title = meta.title;
        if (meta.cover_url) state.imgurl = meta.cover_url;
        state.cover_data_url = null;
        state.crop = { x: 0.5, y: 0.5, scale: 1 };
        state.composite_url = null;
        state.composite_hash = null;
        $('url').value = state.href;
        setStatus('✓ 已抓取', 'ok');
        saveCards(cards);
        pushLivePreview();
        await refreshThumb();
      } catch (err) {
        lastFetchedUrl = '';
        console.warn('[more-articles] fetch meta failed', err);
        setStatus('抓取失败: ' + err.message, 'err');
      }
    };
    $('url').addEventListener('keydown', e => {
      if (e.key === 'Enter') { e.preventDefault(); triggerFetch(); }
    });
    // Paste: run on a task (setTimeout 0), NOT a microtask — the browser
    // inserts the pasted text as a task AFTER the paste event fires, so a
    // microtask would still see `.value === ''` and silently skip the fetch.
    $('url').addEventListener('paste', () => {
      setTimeout(() => {
        const v = $('url').value.trim();
        if (isValidMpUrl(v)) triggerFetch();
      }, 0);
    });

    const applyPickedDataUrl = async (dataUrl) => {
      state.cover_data_url = dataUrl;
      state.composite_url = null;
      state.composite_hash = null;
      state.crop = { x: 0.5, y: 0.5, scale: 1 };
      setStatus('✓ 已替换', 'ok');
      saveCards(cards);
      pushLivePreview();
      await refreshThumb();
    };

    $('replace').addEventListener('click', async () => {
      const pick = await openCoverPicker();
      if (!pick) return;
      if (pick.type === 'file') { $('file').click(); return; }
      setStatus('处理图片中…', 'info');
      try {
        const dataUrl = await urlToResizedDataUrl(pick.url, 1600, 0.85);
        await applyPickedDataUrl(dataUrl);
      } catch (err) {
        setStatus('图片处理失败', 'err');
        console.warn('[more-articles] urlToResizedDataUrl failed', err);
      }
    });
    $('file').addEventListener('change', async () => {
      const f = $('file').files && $('file').files[0];
      if (!f) return;
      setStatus('处理图片中…', 'info');
      try {
        const dataUrl = await resizeImageToDataUrl(f, 1600, 0.85);
        await applyPickedDataUrl(dataUrl);
      } catch (err) {
        setStatus('图片处理失败', 'err');
        console.warn('[more-articles] resize failed', err);
      } finally {
        $('file').value = '';
      }
    });

    $('crop').addEventListener('click', async () => {
      if (!openCropEditor) { setStatus('裁剪功能未就绪', 'err'); return; }
      try {
        const result = await openCropEditor(state);
        if (result) {
          state.crop = result;
          state.composite_url = null;
          state.composite_hash = null;
          saveCards(cards);
          pushLivePreview();
          await refreshThumb();
        }
      } catch (err) {
        console.warn('[more-articles] crop failed', err);
      }
    });

    $('remove').addEventListener('click', () => {
      if (!confirm(`删除第 ${index + 1} 张卡片？`)) return;
      removeCard(index);
    });

    // ---- Drag & drop reorder (FLIP) ----------------------------------------
    // On every dragover we reorder the DOM in place (before/after based on
    // pointer midpoint) and run FLIP so the displaced siblings slide to
    // their new slots. We never re-render during drag. On drop we commit
    // the final DOM order back into the `cards` array and then re-render
    // once to refresh the #N labels.
    root.addEventListener('dragstart', e => {
      if (e.target && e.target.tagName === 'INPUT') { e.preventDefault(); return; }
      draggedEl = root;
      root.classList.add('dragging');
      try {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', 'reorder');
      } catch {}
    });
    root.addEventListener('dragend', () => {
      root.classList.remove('dragging');
      draggedEl = null;
    });
    root.addEventListener('dragover', e => {
      if (!draggedEl || draggedEl === root) return;
      e.preventDefault();
      try { e.dataTransfer.dropEffect = 'move'; } catch {}
      const rect = root.getBoundingClientRect();
      const goBefore = e.clientY < rect.top + rect.height / 2;
      const target = goBefore ? root : root.nextSibling;
      if (draggedEl === target || draggedEl.nextSibling === target) return;
      flipReorder(container, () => {
        container.insertBefore(draggedEl, target);
      });
    });
    root.addEventListener('drop', e => {
      e.preventDefault();
      if (!draggedEl) return;
      const orderIndexes = Array.from(container.querySelectorAll('.ma-card'))
        .map(el => Number(el.dataset.idx))
        .filter(n => Number.isFinite(n));
      const reordered = orderIndexes.map(i => cards[i]);
      // Sanity guard: only commit if we got exactly one entry per card.
      if (reordered.length === cards.length && reordered.every(c => c !== undefined)) {
        cards = reordered;
      }
      draggedEl = null;
      persistAndRefresh();
    });

    refreshThumb();
    return root;
  }

  if (resetBtn) {
    resetBtn.addEventListener('click', () => {
      if (!confirm('清空所有更多文章？')) return;
      resetCards();
      cards = [];
      persistAndRefresh();
    });
  }

  // Expose a hook so engine.js can force this sidebar to re-read localStorage
  // after it wipes the cards on a new article upload.
  window._refreshMoreArticlesSidebar = () => {
    cards = loadCards();
    renderAll();
  };

  renderAll();
}
