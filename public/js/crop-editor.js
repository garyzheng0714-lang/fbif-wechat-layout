// Crop editor modal for "更多文章" card covers.
// Called by: public/js/more-articles-ui.js via the `openCropEditor` callback
// wired through public/app.html.
// Returns: Promise<{x, y, scale} | null>  (null = user cancelled)

import { PSD_BANNER_SPEC } from './more-articles.js';

export function openCropEditor(card) {
  return new Promise((resolve) => {
    const overlay = document.createElement('div');
    overlay.className = 'crop-modal';
    overlay.innerHTML = `
      <div class="crop-box">
        <div class="crop-title">调整裁剪 — 拖动图片，滚轮或滑块缩放</div>
        <div class="crop-stage" data-role="stage">
          <img class="crop-img" data-role="img" alt="" draggable="false">
          <div class="crop-tint"></div>
          <div class="crop-title-preview" data-role="tp"></div>
        </div>
        <div class="crop-controls">
          <label>缩放 <input type="range" data-role="scale" min="0.5" max="3" step="0.02" value="1"></label>
          <button type="button" class="crop-btn secondary" data-role="reset">重置</button>
        </div>
        <div class="crop-footer">
          <button type="button" class="crop-btn secondary" data-role="cancel">取消</button>
          <button type="button" class="crop-btn primary" data-role="ok">完成</button>
        </div>
      </div>
    `;
    document.body.appendChild(overlay);

    const $ = sel => overlay.querySelector(`[data-role="${sel}"]`);
    const stage = $('stage');
    const imgEl = $('img');
    const tp = $('tp');
    const scaleEl = $('scale');

    tp.textContent = (card.title || '').trim();

    const initialCrop = card.crop && typeof card.crop === 'object'
      ? { x: 0.5, y: 0.5, scale: 1, ...card.crop }
      : { x: 0.5, y: 0.5, scale: 1 };
    let crop = { ...initialCrop };
    scaleEl.value = String(crop.scale);

    // Custom uploaded cover > WeChat CDN via proxy (so <img> load succeeds
    // even when the CDN lacks CORS headers; the proxy sets ACAO: *).
    const src = card.cover_data_url
      ? card.cover_data_url
      : ('/api/image-proxy?url=' + encodeURIComponent(card.imgurl || ''));

    let imgW = 1000, imgH = 400;
    let imgReady = false;

    imgEl.onload = () => {
      imgW = imgEl.naturalWidth || imgW;
      imgH = imgEl.naturalHeight || imgH;
      // Stage aspect is fixed 10:3 (set via CSS) to mirror the final composite
      // card shape. Portrait sources are cover-cropped and the user can drag
      // to shift which band of the image is shown.
      imgReady = true;
      requestAnimationFrame(() => requestAnimationFrame(render));
    };
    imgEl.onerror = () => {
      imgReady = true;
      render();
    };
    imgEl.src = src;

    function render() {
      const rect = stage.getBoundingClientRect();
      const W = rect.width;
      const H = rect.height;
      if (W === 0 || H === 0) return;
      // Cover-crop baseline: image scaled to fully cover the 10:3 stage (the
      // final composite has the same shape). Portrait sources get top/bottom
      // cropped; user can drag to shift the focal point.
      const baseScale = imgW > 0 && imgH > 0
        ? Math.max(W / imgW, H / imgH)
        : 1;
      const userScale = Math.max(0.5, Math.min(3, crop.scale || 1));
      const finalScale = baseScale * userScale;
      const drawW = imgW * finalScale;
      const drawH = imgH * finalScale;
      const fx = Math.max(0, Math.min(1, crop.x));
      const fy = Math.max(0, Math.min(1, crop.y));
      let dx = W / 2 - drawW * fx;
      let dy = H / 2 - drawH * fy;
      dx = Math.min(0, Math.max(W - drawW, dx));
      dy = Math.min(0, Math.max(H - drawH, dy));
      // Back-solve normalized focal point after clamp so exported state is truthful.
      crop.x = drawW > 0 ? (W / 2 - dx) / drawW : 0.5;
      crop.y = drawH > 0 ? (H / 2 - dy) / drawH : 0.5;
      imgEl.style.width = drawW + 'px';
      imgEl.style.height = drawH + 'px';
      imgEl.style.transform = `translate(${dx}px, ${dy}px)`;

      // Title overlay: separate layer, never affected by image zoom. Mirrors
      // the PSD text layer geometry, scaled from the 1000×300 artboard.
      if (tp) {
        const SCALE = W / PSD_BANNER_SPEC.width;
        const title = PSD_BANNER_SPEC.title;
        tp.style.fontSize = Math.max(12, title.fontSize * SCALE) + 'px';
        tp.style.left = (title.x * SCALE) + 'px';
        tp.style.width = ((title.width + title.wrapTolerance) * SCALE) + 'px';
        tp.style.right = 'auto';
        tp.style.top = (title.y * SCALE) + 'px';
        tp.style.bottom = 'auto';
        tp.style.transform = 'none';
        tp.style.lineHeight = (title.lineHeight / title.fontSize);
        tp.style.fontWeight = String(title.fontWeight);
        tp.style.fontFamily = '"NotoSansHans", "Noto Sans CJK SC", "Noto Sans SC", "PingFang SC", "Microsoft YaHei", sans-serif';
      }
    }

    requestAnimationFrame(() => requestAnimationFrame(render));
    const onResize = () => render();
    window.addEventListener('resize', onResize);

    // ---- Drag to pan ----
    let dragging = false;
    let lastX = 0, lastY = 0;
    stage.addEventListener('pointerdown', e => {
      if (!imgReady) return;
      dragging = true;
      lastX = e.clientX;
      lastY = e.clientY;
      try { stage.setPointerCapture(e.pointerId); } catch {}
    });
    stage.addEventListener('pointermove', e => {
      if (!dragging) return;
      const ddx = e.clientX - lastX;
      const ddy = e.clientY - lastY;
      lastX = e.clientX;
      lastY = e.clientY;
      const rect = stage.getBoundingClientRect();
      const baseScale = imgW > 0 && imgH > 0
        ? Math.max(rect.width / imgW, rect.height / imgH)
        : 1;
      const finalScale = baseScale * Math.max(0.5, Math.min(3, crop.scale || 1));
      const drawW = imgW * finalScale;
      const drawH = imgH * finalScale;
      if (drawW > 0) crop.x -= ddx / drawW;
      if (drawH > 0) crop.y -= ddy / drawH;
      crop.x = Math.max(0, Math.min(1, crop.x));
      crop.y = Math.max(0, Math.min(1, crop.y));
      render();
    });
    const endDrag = (e) => {
      if (!dragging) return;
      dragging = false;
      try { stage.releasePointerCapture(e.pointerId); } catch {}
    };
    stage.addEventListener('pointerup', endDrag);
    stage.addEventListener('pointercancel', endDrag);

    // ---- Wheel zoom ----
    stage.addEventListener('wheel', e => {
      e.preventDefault();
      const delta = -e.deltaY * 0.0015;
      crop.scale = Math.max(0.5, Math.min(3, (crop.scale || 1) + delta));
      scaleEl.value = String(crop.scale);
      render();
    }, { passive: false });

    // ---- Slider zoom ----
    scaleEl.addEventListener('input', () => {
      crop.scale = parseFloat(scaleEl.value);
      render();
    });

    // ---- Reset ----
    $('reset').addEventListener('click', () => {
      crop = { x: 0.5, y: 0.5, scale: 1 };
      scaleEl.value = '1';
      render();
    });

    // ---- Close handlers ----
    const close = (result) => {
      window.removeEventListener('resize', onResize);
      document.removeEventListener('keydown', keyHandler);
      overlay.remove();
      resolve(result);
    };
    const keyHandler = (e) => {
      if (e.key === 'Escape') close(null);
      else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) close({ ...crop });
    };
    document.addEventListener('keydown', keyHandler);
    $('cancel').addEventListener('click', () => close(null));
    $('ok').addEventListener('click', () => close({ ...crop }));
    overlay.addEventListener('click', e => { if (e.target === overlay) close(null); });
  });
}
