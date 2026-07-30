// Feishu document readiness gate.
//
// A Feishu export can contain server-cached image references. This module first
// materializes those references for display/copy, then validates the exact
// connected staging nodes after the engine decodes them. The article cannot
// reach the visible preview until both barriers and latest-wins checks pass.

function staleResult() {
  return {
    stale: true,
    articleHtml: '',
    previewFooterHtml: '',
    displayFooterHtml: '',
    footerCopy: '',
    preparedImages: new Map(),
    replacements: new Map(),
    imageRefs: [],
  };
}

const FOOTER_SPLIT_MARKER = '<!--fbif-feishu-visible-copy-footer-split-->';

function splitMaterializedFooters(value, previewFooterHtml, footerHtml, replacements) {
  const materialized = String(value || '');
  const markerAt = materialized.indexOf(FOOTER_SPLIT_MARKER);
  if (markerAt >= 0) {
    return {
      previewFooterHtml: materialized.slice(0, markerAt),
      footerCopy: materialized.slice(markerAt + FOOTER_SPLIT_MARKER.length),
    };
  }

  // Dependency-injected materializers used by older hosts may not echo the
  // delimiter. Reuse their single footer when both layers started identical;
  // otherwise apply the verified replacement map to the visible layer.
  if (String(previewFooterHtml || '') === String(footerHtml || '')) {
    return { previewFooterHtml: materialized, footerCopy: materialized };
  }
  const replaceRefs = html => String(html || '').replace(
    /src=(["'])([^"']+)\1/g,
    (full, quote, src) => {
      const replacement = replacements instanceof Map ? replacements.get(src) : '';
      return replacement ? `src=${quote}${replacement}${quote}` : full;
    },
  );
  return {
    previewFooterHtml: replaceRefs(previewFooterHtml),
    footerCopy: materialized,
  };
}

function visibleImageRefs(articleHtml, previewFooterHtml) {
  const refs = [];
  const imageSrc = /<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']/gi;
  const visibleHtml = `${String(articleHtml || '')}\n${String(previewFooterHtml || '')}`;
  let match;
  while ((match = imageSrc.exec(visibleHtml)) !== null) {
    if (match[1]) refs.push(match[1]);
  }
  return refs;
}

function missingPreparedImageRefs(refs, preparedImages) {
  if (!(preparedImages instanceof Map)) return refs.slice();
  const offsets = new Map();
  const missing = [];
  for (const src of refs) {
    const stored = preparedImages.get(src);
    const index = offsets.get(src) || 0;
    const image = Array.isArray(stored)
      ? stored[index]
      : index === 0
        ? stored
        : null;
    offsets.set(src, index + 1);
    if (!image || !image.complete || image.naturalWidth <= 0) missing.push(src);
  }
  return missing;
}

function missingPreparedImageNodes(refs, preparedImages) {
  if (!(preparedImages instanceof Map)) return refs.slice();
  const offsets = new Map();
  const missing = [];
  for (const src of refs) {
    const stored = preparedImages.get(src);
    const index = offsets.get(src) || 0;
    const image = Array.isArray(stored)
      ? stored[index]
      : index === 0
        ? stored
        : null;
    offsets.set(src, index + 1);
    if (!image) missing.push(src);
  }
  return missing;
}

function canceled(isStale, signal) {
  return isStale() || signal?.aborted;
}

async function runLatest(task, isStale, signal) {
  try {
    const result = await task();
    return canceled(isStale, signal) ? staleResult() : result;
  } catch (error) {
    if (canceled(isStale, signal) || error?.name === 'AbortError') return staleResult();
    throw error;
  }
}

export async function prepareFeishuArticlePreview(articleHtml, {
  footerHtml = '',
  previewFooterHtml,
  displayFooterHtml,
  materialize,
  isStale = () => false,
  onProgress = () => {},
  signal,
} = {}) {
  if (typeof materialize !== 'function') {
    throw new Error('飞书图片准备能力尚未就绪，请稍后重试');
  }
  if (isStale()) return staleResult();

  const visibleFooterHtml = displayFooterHtml ?? previewFooterHtml ?? footerHtml;
  const combinedFooters = `${String(visibleFooterHtml || '')}${FOOTER_SPLIT_MARKER}${String(footerHtml || '')}`;
  const materialized = await runLatest(() => materialize(articleHtml, combinedFooters, {
      timeoutMs: 90000,
      concurrency: 6,
      signal,
      onProgress(done, total) {
        if (!canceled(isStale, signal)) onProgress({ phase: 'materialize', done, total });
      },
    }), isStale, signal);
  if (materialized?.stale) return materialized;
  if (materialized.failed > 0) {
    throw new Error(`飞书文档有 ${materialized.failed} 张图片下载失败，已保留当前预览，请重试`);
  }

  const readyHtml = materialized.articleCopy;
  const footers = splitMaterializedFooters(
    materialized.footerCopy,
    visibleFooterHtml,
    footerHtml,
    materialized.replacements,
  );
  const imageRefs = visibleImageRefs(readyHtml, footers.previewFooterHtml);

  return {
    stale: false,
    articleHtml: readyHtml,
    previewFooterHtml: footers.previewFooterHtml,
    displayFooterHtml: footers.previewFooterHtml,
    footerCopy: footers.footerCopy,
    preparedImages: new Map(),
    replacements: materialized.replacements instanceof Map
      ? materialized.replacements
      : new Map(),
    imageRefs,
  };
}

// Run this only after every other asynchronous preparation step (especially
// copy-image transfer). It is the last paint-readiness barrier immediately
// before the engine builds and atomically commits the detached preview.
export async function finalizeFeishuArticlePreview(prepared, {
  refresh,
  isStale = () => false,
  onProgress = () => {},
  signal,
  concurrency = 6,
} = {}) {
  if (!prepared || prepared.stale || canceled(isStale, signal)) return staleResult();
  // Recompute from the exact normalized HTML the engine is about to commit.
  // The earlier list is diagnostic only; normalization or footer visibility may
  // legitimately change the occurrence set between preparation and this gate.
  const imageRefs = visibleImageRefs(
    prepared.articleHtml,
    prepared.previewFooterHtml ?? prepared.displayFooterHtml,
  );
  if (imageRefs.length === 0) {
    onProgress({ phase: 'refresh', done: 0, total: 0 });
    return { ...prepared, stale: false, imageRefs };
  }
  if (typeof refresh !== 'function') {
    throw new Error('飞书图片最终校验能力尚未就绪，请稍后重试');
  }

  // The exact detached nodes may temporarily become incomplete if a final
  // request-affecting attribute changed. Presence is the precondition here;
  // refresh() is the real decode barrier and the strict readiness check runs
  // immediately afterwards.
  const beforeRefreshMissing = missingPreparedImageNodes(imageRefs, prepared.preparedImages);
  if (beforeRefreshMissing.length > 0) {
    throw new Error(`飞书文档有 ${beforeRefreshMissing.length} 张图片尚未准备完成，已保留当前预览，请重试`);
  }
  const refreshed = await runLatest(() => refresh(prepared.preparedImages, {
      concurrency,
      signal,
      refs: imageRefs,
      report: true,
      onProgress(done, total) {
        if (!canceled(isStale, signal)) onProgress({ phase: 'refresh', done, total });
      },
    }), isStale, signal);
  if (refreshed?.stale) return refreshed;
  if (refreshed?.failedSrcs?.length > 0) {
    throw new Error(`飞书文档有 ${refreshed.failedSrcs.length} 张图片最终校验失败，已保留当前预览，请重试`);
  }
  const preparedImages = refreshed?.images instanceof Map
    ? refreshed.images
    : prepared.preparedImages;
  const afterRefreshMissing = missingPreparedImageRefs(imageRefs, preparedImages);
  if (afterRefreshMissing.length > 0) {
    throw new Error(`飞书文档有 ${afterRefreshMissing.length} 张图片最终校验失败，已保留当前预览，请重试`);
  }
  return {
    ...prepared,
    stale: false,
    preparedImages,
    imageRefs,
  };
}
