// Pre-decode local DOCX images while the article/footer HTML is being built.
// The decoded <img> nodes are then moved into the preview fragment, so the
// first visible article frame already contains real pixels.
const DEFAULT_DECODE_TIMEOUT_MS = 1200;

export function localPreviewImageRefs(imageCache) {
  const refs = Object.values(imageCache && typeof imageCache === 'object' ? imageCache : {});
  return [...new Set(refs.filter(ref => String(ref || '').startsWith('blob:')))];
}

function decodeImage(src, createImage, timeoutMs) {
  return new Promise(resolve => {
    const image = createImage();
    let settled = false;
    const finish = ready => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      image.onload = null;
      image.onerror = null;
      resolve(ready ? image : null);
    };
    const ready = () => image.complete && image.naturalWidth > 0;
    const timer = setTimeout(() => finish(ready()), timeoutMs);
    image.onload = () => finish(ready());
    image.onerror = () => finish(false);
    image.decoding = 'async';
    image.src = src;
    if (ready()) {
      finish(true);
      return;
    }
    if (typeof image.decode === 'function') {
      image.decode().then(() => finish(ready()), () => {
        if (ready()) finish(true);
      });
    }
  });
}

export async function preloadLocalPreviewImages(imageCache, options = {}) {
  const refs = localPreviewImageRefs(imageCache);
  const createImage = typeof options.createImage === 'function'
    ? options.createImage
    : typeof Image === 'function'
      ? () => new Image()
      : null;
  if (!createImage || refs.length === 0) return new Map();

  const timeoutMs = Number.isFinite(Number(options.timeoutMs))
    ? Math.max(0, Number(options.timeoutMs))
    : DEFAULT_DECODE_TIMEOUT_MS;
  const decoded = await Promise.all(refs.map(async src => {
    const image = await decodeImage(src, createImage, timeoutMs);
    return image ? [src, image] : null;
  }));
  return new Map(decoded.filter(Boolean));
}
