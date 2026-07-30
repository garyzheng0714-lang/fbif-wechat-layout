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

function decodeImage(src, createImage) {
  return new Promise(resolve => {
    const image = createImage();
    let settled = false;
    const finish = ready => {
      if (settled) return;
      settled = true;
      image.onload = null;
      image.onerror = null;
      resolve(ready ? image : null);
    };
    const ready = () => image.complete && image.naturalWidth > 0;
    image.onerror = () => finish(false);
    image.decoding = 'async';
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
