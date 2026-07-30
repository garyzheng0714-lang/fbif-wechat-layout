// Pre-decode local DOCX images while the article/footer HTML is being built.
// The decoded <img> nodes are then moved into the preview fragment, so the
// first visible article frame already contains real pixels.

export function localPreviewImageRefs(imageCache, articleHtml) {
  const refs = Object.values(imageCache && typeof imageCache === 'object' ? imageCache : {});
  const localRefs = [...new Set(refs.filter(ref => String(ref || '').startsWith('blob:')))];
  if (typeof articleHtml !== 'string') return localRefs;

  const available = new Set(localRefs);
  const ordered = [];
  const seen = new Set();
  const imageSrc = /<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']/gi;
  let match;
  while ((match = imageSrc.exec(articleHtml)) !== null) {
    const src = match[1];
    if (!available.has(src) || seen.has(src)) continue;
    seen.add(src);
    ordered.push(src);
  }
  return ordered;
}

export function articlePreviewImageRefs(articleHtml) {
  const refs = [];
  const seen = new Set();
  const imageSrc = /<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']/gi;
  let match;
  while ((match = imageSrc.exec(String(articleHtml || ''))) !== null) {
    const src = match[1];
    if (!src || seen.has(src)) continue;
    seen.add(src);
    refs.push(src);
  }
  return refs;
}

function decodeImage(src, createImage, timeoutMs = 0, signal) {
  return new Promise(resolve => {
    const image = createImage();
    let settled = false;
    let timer = null;
    let abortHandler = null;
    const finish = ready => {
      if (settled) return;
      settled = true;
      if (timer !== null) clearTimeout(timer);
      if (signal && abortHandler && typeof signal.removeEventListener === 'function') {
        signal.removeEventListener('abort', abortHandler);
      }
      image.onload = null;
      image.onerror = null;
      resolve(ready ? image : null);
    };
    const ready = () => image.complete && image.naturalWidth > 0;
    image.onerror = () => finish(false);
    image.decoding = 'async';
    if (signal && typeof signal.addEventListener === 'function') {
      abortHandler = () => {
        try { image.src = ''; } catch {}
        finish(false);
      };
      if (signal.aborted) {
        abortHandler();
        return;
      }
      signal.addEventListener('abort', abortHandler, { once: true });
    }
    if (Number.isFinite(Number(timeoutMs)) && Number(timeoutMs) > 0) {
      timer = setTimeout(() => {
        try { image.src = ''; } catch {}
        finish(false);
      }, Number(timeoutMs));
    }
    image.src = src;

    // `complete` and `naturalWidth` only prove that the browser knows the
    // resource and its dimensions. They do not prove that pixels are decoded
    // and paint-ready. When decode() exists, it is the sole success signal.
    if (typeof image.decode === 'function') {
      let decoded;
      try {
        decoded = image.decode();
      } catch {
        finish(false);
        return;
      }
      Promise.resolve(decoded).then(() => finish(ready()), () => finish(false));
      return;
    }

    // Old-browser fallback: load is the strongest signal available.
    image.onload = () => finish(ready());
    if (ready()) finish(true);
  });
}

export async function preloadLocalPreviewImages(imageCache, options = {}) {
  const refs = localPreviewImageRefs(imageCache, options.articleHtml);
  const createImage = typeof options.createImage === 'function'
    ? options.createImage
    : typeof Image === 'function'
      ? () => new Image()
      : null;
  if (!createImage || refs.length === 0) return new Map();

  const decoded = await Promise.all(refs.map(async src => {
    const image = await decodeImage(src, createImage);
    return image ? [src, image] : null;
  }));
  return new Map(decoded.filter(Boolean));
}

// Feishu preview readiness includes remote OSS URLs and materialized data URLs,
// not only local blob: references. Decode with bounded concurrency and return
// the actual <img> nodes so preview-dom can move them into the first visible
// frame without starting a second request.
export async function preloadArticlePreviewImages(articleHtml, options = {}) {
  const refs = articlePreviewImageRefs(articleHtml);
  const createImage = typeof options.createImage === 'function'
    ? options.createImage
    : typeof Image === 'function'
      ? () => new Image()
      : null;
  if (!createImage) {
    return {
      images: new Map(),
      total: refs.length,
      failedSrcs: refs,
    };
  }
  if (refs.length === 0) {
    return { images: new Map(), total: 0, failedSrcs: [] };
  }

  const images = new Map();
  const failedSrcs = [];
  let nextIndex = 0;
  let done = 0;
  const requestedConcurrency = Number(options.concurrency);
  const concurrency = Math.min(
    Math.max(1, Number.isFinite(requestedConcurrency) ? Math.floor(requestedConcurrency) : 6),
    refs.length,
  );
  async function worker() {
    while (nextIndex < refs.length) {
      const src = refs[nextIndex++];
      const image = await decodeImage(src, createImage, options.timeoutMs, options.signal);
      if (image) images.set(src, image);
      else failedSrcs.push(src);
      done++;
      if (typeof options.onProgress === 'function') options.onProgress(done, refs.length);
    }
  }
  await Promise.all(Array.from({ length: concurrency }, () => worker()));
  return { images, total: refs.length, failedSrcs };
}

// Detached decoded frames may be evicted while footer/config work finishes.
// Re-decode the same nodes immediately before DOM commit; already-resident
// images resolve quickly, while an evicted frame is restored before reveal.
export async function refreshLocalPreviewImages(preparedImages) {
  if (!(preparedImages instanceof Map) || preparedImages.size === 0) return preparedImages;
  await Promise.all(Array.from(preparedImages.entries()).map(async ([src, image]) => {
    if (!image || typeof image.decode !== 'function') return;
    try {
      await image.decode();
      if (!image.complete || image.naturalWidth <= 0) preparedImages.delete(src);
    } catch {
      preparedImages.delete(src);
    }
  }));
  return preparedImages;
}
