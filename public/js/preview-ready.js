// 预览就绪门（恢复 2026-07-31 的「原子预览」契约，8d2fdbc）。
//
// 服务端会把 .doc→.docx 转换与飞书云文档导出的正文图剥离成 /api/doc-cache/*
// 占位。正文含站内占位时，先把所有图片同时回填成 data: 位图并解码成功，
// 再让正文和图片一次性显示；准备期间只显示导入进度，绝不展示残缺预览。

const IMG_SRC_RE = /<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']/gi;

// 站内占位 = 图片字节不在预览页本地、必须经网络回填才可显示的引用。
// blob: 与 /assets/ 不算：前者在本页内存里即时可显示，后者是磁盘静态文件。
function isPendingPreviewRef(src) {
  const value = String(src || '').trim();
  if (!value) return false;
  return /^(?:https?:\/\/[^/]+)?\/api\/(?:doc-cache\/|image-proxy(?:\?|$))/i.test(value);
}

export function pendingPreviewRefs(articleHtml) {
  const refs = [];
  const seen = new Set();
  const re = new RegExp(IMG_SRC_RE.source, 'gi');
  let match;
  while ((match = re.exec(String(articleHtml || ''))) !== null) {
    const src = match[1];
    if (!src || seen.has(src) || !isPendingPreviewRef(src)) continue;
    seen.add(src);
    refs.push(src);
  }
  return refs;
}

function dataImageRefs(articleHtml) {
  const refs = [];
  const seen = new Set();
  const re = new RegExp(IMG_SRC_RE.source, 'gi');
  let match;
  while ((match = re.exec(String(articleHtml || ''))) !== null) {
    const src = match[1];
    if (!src || seen.has(src) || !/^data:image\//i.test(src)) continue;
    seen.add(src);
    refs.push(src);
  }
  return refs;
}

function decodeOne(src, createImage, timeoutMs) {
  return new Promise(resolve => {
    const image = createImage();
    let settled = false;
    let timer = null;
    const finish = ready => {
      if (settled) return;
      settled = true;
      if (timer !== null) clearTimeout(timer);
      image.onload = null;
      image.onerror = null;
      resolve(!!ready);
    };
    image.onerror = () => finish(false);
    image.decoding = 'async';
    if (Number.isFinite(Number(timeoutMs)) && Number(timeoutMs) > 0) {
      timer = setTimeout(() => finish(false), Number(timeoutMs));
    }
    image.src = src;
    // complete/naturalWidth 只说明浏览器认识这份字节；decode() 才保证像素
    // 真正解码完成、提交 DOM 后能立即上屏。
    const ready = () => image.complete && image.naturalWidth > 0;
    if (typeof image.decode === 'function') {
      image.decode().then(() => finish(ready()), () => finish(ready()));
    } else if (ready()) {
      finish(true);
    } else {
      image.onload = () => finish(ready());
    }
  });
}

// 回填产物（data: 位图）逐张预解码。每张图同时启动一个本地解码任务；
// 返回失败清单交上层裁决。
export async function decodePreviewImages(articleHtml, options = {}) {
  const refs = dataImageRefs(articleHtml);
  const createImage = typeof options.createImage === 'function'
    ? options.createImage
    : typeof Image === 'function'
      ? () => new Image()
      : null;
  if (refs.length === 0 || !createImage) {
    return { total: refs.length, failedSrcs: [] };
  }
  const timeoutMs = Number.isFinite(Number(options.timeoutMs)) ? Number(options.timeoutMs) : 20000;
  const failedSrcs = [];
  let done = 0;
  await Promise.all(refs.map(async src => {
    const ok = await decodeOne(src, createImage, timeoutMs);
    if (!ok) failedSrcs.push(src);
    done++;
    if (typeof options.onProgress === 'function') options.onProgress(done, refs.length);
  }));
  return { total: refs.length, failedSrcs };
}

function staleResult(articleHtml) {
  return { stale: true, changed: false, articleHtml, replacements: new Map() };
}

// 主入口：正文没有站内占位 → 原样放行（本地 blob:/远程图不归这道门管）；
// 有占位 → materialize 全部回填 + 解码确认，之后才把就绪正文交还调用方。
export async function preparePreviewArticle(articleHtml, {
  materialize,
  createImage,
  isStale = () => false,
  onProgress = () => {},
} = {}) {
  const html = String(articleHtml || '');
  const pending = pendingPreviewRefs(html);
  if (pending.length === 0) {
    return { stale: false, changed: false, articleHtml: html, replacements: new Map() };
  }
  if (typeof materialize !== 'function') {
    throw new Error('图片准备能力尚未就绪，请稍后重试');
  }
  if (isStale()) return staleResult(html);

  const materialized = await materialize(html, '', {
    onProgress(done, total) {
      if (!isStale()) onProgress({ phase: 'download', done, total });
    },
  });
  if (isStale()) return staleResult(html);
  if (materialized.failed > 0) {
    throw new Error('有 ' + materialized.failed + ' 张图片下载失败，请重新导入');
  }

  const readyHtml = materialized.articleCopy;
  const decoded = await decodePreviewImages(readyHtml, {
    createImage,
    onProgress(done, total) {
      if (!isStale()) onProgress({ phase: 'decode', done, total });
    },
  });
  if (isStale()) return staleResult(html);
  if (decoded.failedSrcs.length > 0) {
    throw new Error('有 ' + decoded.failedSrcs.length + ' 张图片无法显示，请重新导入');
  }

  return {
    stale: false,
    changed: true,
    articleHtml: readyHtml,
    replacements: materialized.replacements instanceof Map
      ? materialized.replacements
      : new Map(),
  };
}
