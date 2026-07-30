// Feishu document readiness gate.
//
// A Feishu export can contain server-cached image references. The article must
// not reach the visible preview until those references are copy-safe and every
// article image has decoded successfully. Keeping this orchestration separate
// makes the latest-wins checks and progress contract directly testable.

function staleResult() {
  return {
    stale: true,
    articleHtml: '',
    preparedImages: new Map(),
    replacements: new Map(),
  };
}

export async function prepareFeishuArticlePreview(articleHtml, {
  materialize,
  preload,
  isStale = () => false,
  onProgress = () => {},
  signal,
} = {}) {
  if (typeof materialize !== 'function' || typeof preload !== 'function') {
    throw new Error('飞书图片准备能力尚未就绪，请稍后重试');
  }
  if (isStale()) return staleResult();

  const materialized = await materialize(articleHtml, '', {
    timeoutMs: 90000,
    concurrency: 6,
    signal,
    onProgress(done, total) {
      if (!isStale()) onProgress({ phase: 'download', done, total });
    },
  });
  if (isStale()) return staleResult();
  if (materialized.failed > 0) {
    throw new Error(`飞书文档有 ${materialized.failed} 张图片下载失败，已保留当前预览，请重试`);
  }

  const readyHtml = materialized.articleCopy;
  const preloaded = await preload(readyHtml, {
    concurrency: 6,
    timeoutMs: 90000,
    signal,
    onProgress(done, total) {
      if (!isStale()) onProgress({ phase: 'decode', done, total });
    },
  });
  if (isStale()) return staleResult();
  if (preloaded.failedSrcs.length > 0) {
    throw new Error(`飞书文档有 ${preloaded.failedSrcs.length} 张图片无法显示，已保留当前预览，请重试`);
  }

  return {
    stale: false,
    articleHtml: readyHtml,
    preparedImages: preloaded.images,
    replacements: materialized.replacements instanceof Map
      ? materialized.replacements
      : new Map(),
  };
}
