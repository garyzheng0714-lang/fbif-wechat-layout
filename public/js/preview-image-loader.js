// Pre-decode local DOCX images while the article/footer HTML is being built.
// The decoded <img> nodes are then moved into the preview fragment, so the
// first visible article frame already contains real pixels.

export function localPreviewImageRefs(imageCache, articleHtml) {
  const refs = Object.values(imageCache && typeof imageCache === 'object' ? imageCache : {});
  const localRefs = refs.filter(ref => String(ref || '').startsWith('blob:'));
  if (typeof articleHtml !== 'string') return localRefs;

  const available = new Set(localRefs);
  const ordered = [];
  const imageSrc = /<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']/gi;
  let match;
  while ((match = imageSrc.exec(articleHtml)) !== null) {
    const src = match[1];
    if (!available.has(src)) continue;
    ordered.push(src);
  }
  return ordered;
}

export function articlePreviewImageRefs(articleHtml) {
  return articlePreviewImageRequests(articleHtml).map(request => request.src);
}

const IMAGE_REQUEST_ATTRIBUTES = [
  'crossorigin',
  'referrerpolicy',
  'srcset',
  'sizes',
  'fetchpriority',
];

function tagAttribute(tag, name) {
  const escaped = String(name).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = String(tag).match(new RegExp(
    `(?:\\s)${escaped}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|([^\\s"'=<>]+))`,
    'i',
  ));
  if (!match) return undefined;
  return match[1] ?? match[2] ?? match[3] ?? '';
}

// Keep the request-affecting attributes from each final <img> occurrence.
// Chromium can restart an already-decoded image when referrerpolicy,
// crossorigin, srcset or sizes is added later. Applying them before src keeps
// the decoded node stable when it is adopted into the detached preview.
export function articlePreviewImageRequests(articleHtml) {
  const requests = [];
  const imageTag = /<img\b[^>]*>/gi;
  let match;
  while ((match = imageTag.exec(String(articleHtml || ''))) !== null) {
    const tag = match[0];
    const src = tagAttribute(tag, 'src');
    if (!src) continue;
    const attributes = {};
    for (const name of IMAGE_REQUEST_ATTRIBUTES) {
      const value = tagAttribute(tag, name);
      if (value !== undefined) attributes[name] = value;
    }
    requests.push({ src, attributes });
  }
  return requests;
}

function createAbortError() {
  const error = new Error('图片准备已取消');
  error.name = 'AbortError';
  return error;
}

function throwIfAborted(signal) {
  if (signal?.aborted) throw createAbortError();
}

function awaitWithAbort(value, signal) {
  if (!signal || typeof signal.addEventListener !== 'function') return Promise.resolve(value);
  throwIfAborted(signal);
  return new Promise((resolve, reject) => {
    let settled = false;
    const finish = (callback, result) => {
      if (settled) return;
      settled = true;
      signal.removeEventListener?.('abort', onAbort);
      callback(result);
    };
    const onAbort = () => finish(reject, createAbortError());
    signal.addEventListener('abort', onAbort, { once: true });
    Promise.resolve(value).then(
      result => finish(resolve, result),
      error => finish(reject, error),
    );
  });
}

function boundedConcurrency(value, total, fallback = 6) {
  if (total <= 0) return 0;
  const requested = Number(value);
  return Math.min(
    Math.max(1, Number.isFinite(requested) ? Math.floor(requested) : fallback),
    total,
  );
}

function imageFactory(options = {}) {
  if (typeof options.createImage === 'function') return options.createImage;
  return typeof Image === 'function' ? () => new Image() : null;
}

function rememberPreparedImage(images, src, image) {
  const current = images.get(src);
  if (!current) {
    images.set(src, image);
    return;
  }
  if (Array.isArray(current)) {
    current.push(image);
    return;
  }
  images.set(src, [current, image]);
}

function normalizedImageRequest(value) {
  if (value && typeof value === 'object') {
    return {
      src: String(value.src || ''),
      attributes: value.attributes && typeof value.attributes === 'object'
        ? value.attributes
        : {},
    };
  }
  return { src: String(value || ''), attributes: {} };
}

function applyImageRequestAttributes(image, attributes) {
  for (const [name, value] of Object.entries(attributes || {})) {
    if (typeof image.setAttribute === 'function') {
      image.setAttribute(name, String(value));
      continue;
    }
    const property = name === 'crossorigin'
      ? 'crossOrigin'
      : name === 'referrerpolicy'
        ? 'referrerPolicy'
        : name === 'fetchpriority'
          ? 'fetchPriority'
          : name;
    image[property] = String(value);
  }
}

function decodeImage(requestValue, createImage, timeoutMs = 0, signal) {
  const request = normalizedImageRequest(requestValue);
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
    applyImageRequestAttributes(image, request.attributes);
    image.src = request.src;

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

async function preloadImageOccurrences(requestValues, options = {}) {
  const createImage = imageFactory(options);
  const requests = requestValues.map(normalizedImageRequest);
  const total = requests.length;
  options.onProgress?.(0, total);
  throwIfAborted(options.signal);
  if (!createImage) {
    options.onProgress?.(total, total);
    return {
      images: new Map(),
      total,
      failedSrcs: requests.map(request => request.src),
    };
  }
  if (total === 0) {
    return { images: new Map(), total: 0, failedSrcs: [] };
  }

  const decodedOccurrences = new Array(total);
  let nextIndex = 0;
  let done = 0;
  const concurrency = boundedConcurrency(options.concurrency, total);
  async function worker() {
    for (;;) {
      throwIfAborted(options.signal);
      const index = nextIndex++;
      if (index >= total) return;
      const image = await decodeImage(
        requests[index],
        createImage,
        options.timeoutMs,
        options.signal,
      );
      throwIfAborted(options.signal);
      decodedOccurrences[index] = image;
      done++;
      options.onProgress?.(done, total);
    }
  }
  await Promise.all(Array.from({ length: concurrency }, () => worker()));
  throwIfAborted(options.signal);

  const images = new Map();
  const failedSrcs = [];
  decodedOccurrences.forEach((image, index) => {
    const src = requests[index].src;
    if (image) rememberPreparedImage(images, src, image);
    else failedSrcs.push(src);
  });
  return { images, total, failedSrcs };
}

export async function preloadLocalPreviewImages(imageCache, options = {}) {
  const refs = localPreviewImageRefs(imageCache, options.articleHtml);
  const result = await preloadImageOccurrences(refs, options);
  return result.images;
}

// Feishu preview readiness includes remote OSS URLs and materialized data URLs,
// not only local blob: references. Decode with bounded concurrency and return
// the actual <img> nodes so preview-dom can move them into the first visible
// frame without starting a second request.
export async function preloadArticlePreviewImages(articleHtml, options = {}) {
  const requests = articlePreviewImageRequests(articleHtml);
  return preloadImageOccurrences(requests, options);
}

// Detached decoded frames may be evicted while footer/config work finishes.
// Re-decode the same nodes immediately before DOM commit; already-resident
// images resolve quickly, while an evicted frame is restored before reveal.
export async function refreshLocalPreviewImages(preparedImages, options = {}) {
  const requestedRefs = Array.isArray(options.refs) ? options.refs : null;
  if (!(preparedImages instanceof Map) || (preparedImages.size === 0 && !requestedRefs?.length)) {
    if (options.report === true) {
      options.onProgress?.(0, 0);
      return { images: preparedImages instanceof Map ? preparedImages : new Map(), total: 0, failedSrcs: [] };
    }
    return preparedImages;
  }

  const occurrences = [];
  if (requestedRefs) {
    const offsets = new Map();
    for (const src of requestedRefs) {
      const prepared = preparedImages.get(src);
      const index = offsets.get(src) || 0;
      const image = Array.isArray(prepared)
        ? prepared[index]
        : index === 0
          ? prepared
          : null;
      occurrences.push({ src, image, index });
      offsets.set(src, index + 1);
    }
  } else {
    for (const [src, prepared] of preparedImages.entries()) {
      const nodes = Array.isArray(prepared) ? prepared : [prepared];
      nodes.forEach((image, index) => occurrences.push({ src, image, index }));
    }
  }
  const total = occurrences.length;
  const failed = [];
  const completed = new Array(total);
  let done = 0;
  let nextIndex = 0;
  options.onProgress?.(0, total);
  throwIfAborted(options.signal);
  const concurrency = boundedConcurrency(options.concurrency, total);
  async function worker() {
    for (;;) {
      throwIfAborted(options.signal);
      const occurrenceIndex = nextIndex++;
      if (occurrenceIndex >= total) return;
      const occurrence = occurrences[occurrenceIndex];
      const { image } = occurrence;
      let ready = !!image && image.complete && image.naturalWidth > 0;
      if (image && typeof image.decode === 'function') {
        try {
          await awaitWithAbort(image.decode(), options.signal);
          throwIfAborted(options.signal);
          ready = image.complete && image.naturalWidth > 0;
        } catch (error) {
          if (error?.name === 'AbortError' || options.signal?.aborted) throw createAbortError();
          ready = false;
        }
      }
      completed[occurrenceIndex] = ready;
      done++;
      options.onProgress?.(done, total);
    }
  }
  await Promise.all(Array.from({ length: concurrency }, () => worker()));
  throwIfAborted(options.signal);
  completed.forEach((ready, index) => {
    if (!ready) failed.push(occurrences[index]);
  });

  if (failed.length > 0) {
    const failedBySrc = new Map();
    for (const occurrence of failed) {
      const indexes = failedBySrc.get(occurrence.src) || new Set();
      indexes.add(occurrence.index);
      failedBySrc.set(occurrence.src, indexes);
    }
    for (const [src, indexes] of failedBySrc.entries()) {
      const prepared = preparedImages.get(src);
      if (Array.isArray(prepared)) {
        const remaining = prepared.filter((_, index) => !indexes.has(index));
        if (remaining.length === 0) preparedImages.delete(src);
        else preparedImages.set(src, remaining.length === 1 ? remaining[0] : remaining);
      } else {
        preparedImages.delete(src);
      }
    }
  }

  if (options.report === true) {
    return {
      images: preparedImages,
      total,
      failedSrcs: failed.map(item => item.src),
    };
  }
  return preparedImages;
}
