// Sidebar UI for the "更多文章" section.
// - Cards are dynamic length (0..N).
// - Empty state shows a red-bordered prompt.
// - Each card can be reordered via drag, and deleted via × button.
// - A "+ 添加更多文章" button below the list appends a blank card.
// Called by: public/app.html inline <script type="module"> after an article exists.

import {
  loadCards,
  saveCards,
  resetCards,
  compositePreviewDataUrl,
  emptyCard,
  makeFreshUploadCards,
} from './more-articles.js';
import {
  fetchMoreArticleMetaCached,
  isValidMoreArticleUrl,
  shouldAutoFetchMoreArticleUrl,
} from './more-articles-meta.js';

const SAVE_DEBOUNCE_MS = 400;

function debounce(fn, ms) {
  let t = null;
  return function (...args) {
    if (t) clearTimeout(t);
    t = setTimeout(() => fn.apply(this, args), ms);
  };
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

export function coverPickerImageSource(rawUrl) {
  const url = String(rawUrl || '');
  const absoluteUrl = url.startsWith('//') ? 'https:' + url : url;
  if (
    absoluteUrl.startsWith('data:') ||
    absoluteUrl.startsWith('blob:') ||
    (absoluteUrl.startsWith('/') && !absoluteUrl.startsWith('//')) ||
    (typeof location !== 'undefined' && absoluteUrl.startsWith(location.origin + '/'))
  ) {
    return { src: absoluteUrl, crossOrigin: false };
  }
  return {
    src: '/api/image-proxy?url=' + encodeURIComponent(absoluteUrl),
    crossOrigin: true,
  };
}

export function buildCoverPickerSources(card = {}, articleSrcs = []) {
  const srcs = [];
  const seen = new Set();
  const add = (raw) => {
    let src = String(raw || '').trim();
    if (!src) return;
    if (src.startsWith('//')) src = 'https:' + src;
    if (seen.has(src)) return;
    seen.add(src);
    srcs.push(src);
  };

  add(card.cover_data_url);
  add(card.imgurl);
  if (Array.isArray(card.image_urls)) {
    card.image_urls.forEach(add);
  }
  if (Array.isArray(articleSrcs)) {
    articleSrcs.forEach(add);
  }
  return srcs;
}

// Fetch an existing image URL and re-encode to a resized data URL we can save
// in localStorage as the card cover. Page-local image refs (blob: and
// /api/doc-cache/*) must load directly; only remote article images need the
// CORS proxy.
async function urlToResizedDataUrl(rawUrl, maxEdge = 1600, quality = 0.85) {
  const imageSource = coverPickerImageSource(rawUrl);
  const img = await new Promise((res, rej) => {
    const i = new Image();
    if (imageSource.crossOrigin) i.crossOrigin = 'anonymous';
    i.onload = () => res(i);
    i.onerror = () => rej(new Error('image load failed'));
    i.src = imageSource.src;
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

// Minimal modal for picking a cover image. Current-card media comes first; the
// currently-rendered article's images stay as a fallback for legacy workflows.
// Resolves to { type: 'url', url } or { type: 'file' } or null (closed).
function openCoverPicker(card = {}) {
  const articleSrcs = (window._getArticleImageSrcs && window._getArticleImageSrcs()) || [];
  const srcs = buildCoverPickerSources(card, articleSrcs);
  const overlay = document.createElement('div');
  overlay.className = 'ma-gallery-overlay';
  const safeAttr = s => String(s).replace(/"/g, '&quot;').replace(/</g, '&lt;');
  const tile = (src, i) => {
    const thumbSrc = coverPickerImageSource(src).src;
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
          ? '<div class="ma-gallery-empty">暂无可选图片 — 请使用"上传本地图片"</div>'
          : `<div class="ma-gallery-hint">点击选择图片作为封面</div>
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
  // 拖拽排序交给 SortableJS（见 renderAll 末尾 + commitDragOrder）；maSortable 持当前实例，重渲前销毁。
  let maSortable = null;

  // SortableJS 拖完一次性按 DOM 顺序回写 cards。每张卡片渲染时写死 dataset.idx，
  // Sortable 只移动 DOM 不动 dataset，所以按当前 DOM 顺序映射回 cards[原idx] 即新顺序。
  // 严格校验（无重复、无缺项、长度一致）后才落盘；异常则还原 DOM 且绝不 saveCards（显式失败）。
  function commitDragOrder() {
    const reordered = Array.from(container.querySelectorAll('.ma-card'))
      .map(el => cards[Number(el.dataset.idx)]);
    const wellFormed = reordered.length === cards.length
      && reordered.every(c => c !== undefined)
      && new Set(reordered).size === cards.length;
    if (!wellFormed) {
      console.warn('[more-articles] 拖拽顺序异常，已放弃本次重排并还原');
      renderAll();
      return;
    }
    const changed = reordered.some((c, i) => c !== cards[i]);
    if (changed) {
      cards = reordered;
      persistAndRefresh();
    }
  }

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
    // Default state uses the active rule preset's placeholder slot count.
    // Seed on demand so first-visit, 清空, and delete-to-zero all converge to
    // the same recommended-articles default. Saved cards that are all empty
    // (legacy state from before defaults existed) get reseeded too — empty
    // placeholders are functionally identical to "no cards saved".
    const allEmpty = cards.length > 0 && cards.every(c =>
      !c.href && !c.imgurl && !c.title && !c.cover_data_url
    );
    if (cards.length === 0 || allEmpty) {
      cards = makeFreshUploadCards();
      saveCards(cards);
      pushLivePreview();
    }
    if (maSortable) { try { maSortable.destroy(); } catch {} maSortable = null; }
    container.innerHTML = '';
    cards.forEach((_, i) => container.appendChild(renderCardEditor(i)));
    container.appendChild(renderAddButton());
    // 拖拽排序：grip 为唯一把手；只对 .ma-card 排序（“+ 添加”按钮不参与）。
    if (window.Sortable && cards.length > 1) {
      maSortable = window.Sortable.create(container, {
        handle: '.ma-drag-handle',
        draggable: '.ma-card',
        animation: 150,
        forceFallback: true,
        onEnd: commitDragOrder,
      });
    }
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

    const hasCroppableCover = () => !!(state.cover_data_url || state.imgurl);
    const syncCropButton = () => {
      const btn = $('crop');
      const canCrop = hasCroppableCover();
      btn.disabled = !canCrop;
      btn.title = canCrop ? '调整裁剪位置' : '请先抓取头图或替换图片';
    };

    const refreshThumb = async () => {
      syncCropButton();
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

    // Track the URL we last auto-fetched so repeated paste/input events for
    // the same string don't re-fire (cache still short-circuits them, but
    // avoiding the duplicate status flicker matters).
    let lastFetchedUrl = '';

    const triggerFetch = async () => {
      const url = $('url').value.trim();
      if (!isValidMoreArticleUrl(url)) {
        setStatus('需要公众号链接', 'err');
        return;
      }
      if (url === lastFetchedUrl) return;
      lastFetchedUrl = url;
      setStatus('抓取中…', 'info');
      try {
        const meta = await fetchMoreArticleMetaCached(url);
        state.href = meta.source_url || url;
        if (meta.title) state.title = meta.title;
        if (meta.cover_url) state.imgurl = meta.cover_url;
        state.image_urls = Array.isArray(meta.image_urls) ? meta.image_urls : [];
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

    const scheduleAutoFetch = debounce(() => {
      if (shouldAutoFetchMoreArticleUrl($('url').value, lastFetchedUrl)) {
        triggerFetch();
      }
    }, SAVE_DEBOUNCE_MS);

    $('url').addEventListener('input', () => {
      state.href = $('url').value.trim();
      debouncedSave();
      scheduleAutoFetch();
    });

    $('url').addEventListener('keydown', e => {
      if (e.key === 'Enter') { e.preventDefault(); triggerFetch(); }
    });
    // Paste: run on a task (setTimeout 0), NOT a microtask — the browser
    // inserts the pasted text as a task AFTER the paste event fires, so a
    // microtask would still see `.value === ''` and silently skip the fetch.
    $('url').addEventListener('paste', () => {
      setTimeout(() => {
        const v = $('url').value.trim();
        if (isValidMoreArticleUrl(v)) triggerFetch();
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

    const ensureImageLibrary = async () => {
      if (Array.isArray(state.image_urls) && state.image_urls.length > 0) return;
      const url = (state.href || $('url').value || '').trim();
      if (!isValidMoreArticleUrl(url)) return;
      setStatus('加载图片库中…', 'info');
      try {
        const meta = await fetchMoreArticleMetaCached(url);
        state.href = meta.source_url || url;
        if (meta.title && !state.title) state.title = meta.title;
        if (meta.cover_url && !state.imgurl) state.imgurl = meta.cover_url;
        state.image_urls = Array.isArray(meta.image_urls) ? meta.image_urls : [];
        $('url').value = state.href;
        saveCards(cards);
        await refreshThumb();
        setStatus(state.image_urls.length > 0 ? `✓ 已加载${state.image_urls.length}张图片` : '', state.image_urls.length > 0 ? 'ok' : '');
      } catch (err) {
        setStatus('', '');
        console.warn('[more-articles] fetch image library failed', err);
      }
    };

    $('replace').addEventListener('click', async () => {
      await ensureImageLibrary();
      const pick = await openCoverPicker(state);
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
      if (!hasCroppableCover()) {
        setStatus('请先抓取头图或替换图片', 'err');
        syncCropButton();
        return;
      }
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

    // 拖拽排序由容器级 SortableJS 统一处理（见 renderAll + commitDragOrder），卡片本身不再绑原生 DnD。

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
