// Image upload — only base64 images (from DOCX) are uploaded to OSS.
// External URLs are used directly; mmbiz URLs are already on WeChat CDN.

export function isMmbizUrl(url) {
  return /^https?:\/\/mmbiz\.qpic\.cn\//i.test(url);
}

function arrayBufferToBase64(buffer) {
  const bytes = new Uint8Array(buffer);
  const chunkSize = 0x8000;
  let binary = '';
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

// Fetch a blob: or server cache URL and encode it as a base64 data URL so the
// skip-upload copy path can paste usable image bytes instead of page-local URLs.
async function imageRefToDataUrl(src) {
  const resp = await fetch(src);
  if (!resp.ok) throw new Error('fetch ' + src + ' -> ' + resp.status);
  const blob = await resp.blob();
  const mime = blob.type || 'image/png';
  const base64 = arrayBufferToBase64(await blob.arrayBuffer());
  return 'data:' + mime + ';base64,' + base64;
}

// Fetch a mmbiz (WeChat CDN) image THROUGH the server image-proxy and encode it
// as base64. Direct fetch is blocked by WeChat anti-hotlinking (Referer check);
// /api/image-proxy injects the right Referer server-side and adds CORS so the
// bytes are readable here. Used by the website channel, which must re-host every
// image on its own CDN — pasting raw mmbiz URLs into foodtalks.cn would hotlink
// and render the "未经允许不可引用" placeholder.
async function mmbizUrlToDataUrl(url) {
  const proxied = '/api/image-proxy?url=' + encodeURIComponent(url);
  const resp = await fetch(proxied);
  if (!resp.ok) throw new Error('proxy ' + url + ' -> ' + resp.status);
  const blob = await resp.blob();
  const mime = blob.type || 'image/png';
  const base64 = arrayBufferToBase64(await blob.arrayBuffer());
  return 'data:' + mime + ';base64,' + base64;
}

// Upload a single base64 image to OSS. Returns OSS URL or null on failure.
async function uploadOne(task) {
  const body = JSON.stringify({ base64_images: { [task.key]: task.src } });
  const resp = await fetch('/api/oss-upload', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
  });
  if (!resp.ok) return null;
  const { results } = await resp.json();
  return results[task.key] || Object.values(results)[0] || null;
}

export async function materializeLazyImages(articleCopy, footerCopy, { onProgress, onLog } = {}) {
  const allHtml = articleCopy + '\n' + footerCopy;
  let m;

  // Materialize in-browser and server-cached image refs to base64 at copy time.
  //  - blob: URLs  → in-memory Blobs created by parser.js for local .docx
  //  - /api/doc-cache/* → server-side cache for images stripped from .doc→.docx
  //    conversions (preserves original bytes, parallel download, fast text render)
  // In both cases a one-time fetch + FileReader roundtrip yields the data URL
  // that the OSS upload path can consume.
  const lazyRe = /src=(["'])(blob:[^"']+|\/api\/doc-cache\/[^"']+)\1/g;
  const lazyUrls = new Set();
  while ((m = lazyRe.exec(allHtml)) !== null) lazyUrls.add(m[2]);
  onProgress && onProgress(0, lazyUrls.size);

  let done = 0;
  const failedSrcs = [];
  for (const src of lazyUrls) {
    try {
      const dataUrl = await imageRefToDataUrl(src);
      articleCopy = articleCopy.split(src).join(dataUrl);
      footerCopy = footerCopy.split(src).join(dataUrl);
    } catch (err) {
      onLog && onLog('error', '图片转 base64 失败', { url: src.slice(0, 60), err: String(err) });
      failedSrcs.push(src);
    }
    done++;
    onProgress && onProgress(done, lazyUrls.size);
  }

  return { articleCopy, footerCopy, total: lazyUrls.size, failed: failedSrcs.length, failedSrcs };
}

// Upload base64 images from DOCX to OSS. External URLs are used directly.
// mmbiz URLs are already on WeChat CDN — skip them (preserves GIF animations).
//
// Returns { articleCopy, footerCopy, total, failed, failedSrcs }.
// failedSrcs contains original src strings of images that failed even after retry.
export async function uploadNonCdnImages(articleCopy, footerCopy, { onProgress, onLog, onMaterialized, transferMmbiz = false } = {}) {
  const materialized = await materializeLazyImages(articleCopy, footerCopy, { onLog });
  articleCopy = materialized.articleCopy;
  footerCopy = materialized.footerCopy;
  const lazyFailedSrcs = materialized.failedSrcs || [];

  // Website channel: re-host mmbiz images on our CDN. Pull each mmbiz URL through
  // the server proxy → base64, so the upload pass below re-hosts it and replaces
  // the mmbiz src. The WeChat channel keeps transferMmbiz=false: mmbiz URLs work
  // as-is inside mp.weixin.qq.com and re-hosting would strip GIF animation.
  const mmbizFailedSrcs = [];
  if (transferMmbiz) {
    const mmbizUrls = new Set();
    let mm;
    const mmRe = /src="(https?:\/\/[^"]+)"/g;
    const combined = articleCopy + '\n' + footerCopy;
    while ((mm = mmRe.exec(combined)) !== null) {
      if (isMmbizUrl(mm[1])) mmbizUrls.add(mm[1]);
    }
    if (mmbizUrls.size > 0) {
      onLog && onLog('info', '网站渠道：转存 mmbiz 图到 CDN', { count: mmbizUrls.size });
      // 并发取图转 base64（IO 密集，可并行）；字符串替换收集后串行做（避免并发改
      // articleCopy 引用导致丢更新）。并发上限防止一次性打爆 image-proxy。
      const urlList = [...mmbizUrls];
      const MMBIZ_CONCURRENCY = Math.min(urlList.length, 6);
      const fetched = []; // { url, dataUrl }
      let done = 0;
      onProgress && onProgress(0, urlList.length);
      let next = 0;
      async function mmbizWorker() {
        while (next < urlList.length) {
          const url = urlList[next++];
          try {
            const dataUrl = await mmbizUrlToDataUrl(url);
            fetched.push({ url, dataUrl });
          } catch (err) {
            onLog && onLog('error', 'mmbiz 取图失败', { url: url.slice(0, 60), err: String(err) });
            mmbizFailedSrcs.push(url);
          }
          done++;
          onProgress && onProgress(done, urlList.length);
        }
      }
      await Promise.all(Array.from({ length: MMBIZ_CONCURRENCY }, () => mmbizWorker()));
      // 串行替换：每个 url 全局替换为其 base64，无并发竞态。
      for (const { url, dataUrl } of fetched) {
        articleCopy = articleCopy.split(url).join(dataUrl);
        footerCopy = footerCopy.split(url).join(dataUrl);
      }
    }
  }

  const preUploadFailed = lazyFailedSrcs.concat(mmbizFailedSrcs);
  onMaterialized && onMaterialized({
    articleCopy,
    footerCopy,
    failed: preUploadFailed.length,
    failedSrcs: preUploadFailed,
  });

  const tasks = [];
  let m, mmbizCount = 0;

  // Refresh combined HTML if any lazy / mmbiz → data: substitutions happened.
  const refreshedHtml = articleCopy + '\n' + footerCopy;

  // base64 data URIs (from DOCX, materialized lazy refs, and any mmbiz we just
  // converted above) — upload to OSS
  const b64Re = /src="(data:image\/[^"]+)"/g;
  while ((m = b64Re.exec(refreshedHtml)) !== null) {
    tasks.push({ type: 'base64', src: m[1], key: 'img_' + tasks.length });
  }

  // HTTP URLs — use directly, no upload needed
  // (any remaining mmbiz URLs are kept as-is; other external URLs work as-is)
  const urlRe = /src="(https?:\/\/[^"]+)"/g;
  while ((m = urlRe.exec(refreshedHtml)) !== null) {
    if (isMmbizUrl(m[1])) mmbizCount++;
  }

  if (tasks.length === 0) {
    const earlyFailed = lazyFailedSrcs.concat(mmbizFailedSrcs);
    onLog && onLog('info', '无需上传', { mmbiz剩余: mmbizCount });
    return {
      articleCopy,
      footerCopy,
      total: 0,
      failed: earlyFailed.length,
      failedSrcs: earlyFailed,
    };
  }

  onLog && onLog('info', '开始上传Base64图片到OSS', { base64: tasks.length, 外链直接使用: true, mmbiz跳过: mmbizCount });
  onProgress && onProgress(0, tasks.length);

  let done = 0;
  const failedTasks = [];
  const CONCURRENCY = Math.min(tasks.length, 50);
  let next = 0;

  async function worker() {
    while (next < tasks.length) {
      const task = tasks[next++];
      let cdnUrl = null;
      try {
        cdnUrl = await uploadOne(task);
      } catch {}

      // Auto-retry once on failure
      if (!cdnUrl) {
        try {
          cdnUrl = await uploadOne(task);
        } catch {}
      }

      if (cdnUrl) {
        articleCopy = articleCopy.split(task.src).join(cdnUrl);
        footerCopy = footerCopy.split(task.src).join(cdnUrl);
        onLog && onLog('info', '图片上传成功', { cdn: cdnUrl.substring(0, 60) + '...' });
      } else {
        failedTasks.push(task);
        onLog && onLog('error', '图片上传失败(已重试)', { src: task.src.substring(0, 80) });
      }
      done++;
      onProgress && onProgress(done, tasks.length);
    }
  }

  await Promise.all(Array.from({ length: Math.min(CONCURRENCY, tasks.length) }, () => worker()));

  const failedSrcs = lazyFailedSrcs.concat(mmbizFailedSrcs, failedTasks.map(t => t.src));
  if (failedSrcs.length > 0) {
    onLog && onLog('warn', '上传完成，' + failedSrcs.length + '张失败（已自动重试1次）');
  } else {
    onLog && onLog('info', '全部图片上传成功', { total: tasks.length });
  }

  return { articleCopy, footerCopy, total: tasks.length, failed: failedSrcs.length, failedSrcs };
}

// Retry specific failed base64 images. Call with the failedSrcs from a previous upload.
export async function retryFailedImages(articleCopy, footerCopy, failedSrcs, { onProgress, onLog }) {
  const tasks = failedSrcs.map((src, i) => ({ type: 'base64', src, key: 'retry_' + i }));

  let done = 0;
  const stillFailed = [];

  for (const task of tasks) {
    let cdnUrl = null;
    try { cdnUrl = await uploadOne(task); } catch {}
    if (!cdnUrl) {
      try { cdnUrl = await uploadOne(task); } catch {}
    }
    if (cdnUrl) {
      articleCopy = articleCopy.split(task.src).join(cdnUrl);
      footerCopy = footerCopy.split(task.src).join(cdnUrl);
      onLog && onLog('info', '重试上传成功', { cdn: cdnUrl.substring(0, 60) + '...' });
    } else {
      stillFailed.push(task.src);
      onLog && onLog('error', '重试仍然失败', { src: task.src.substring(0, 80) });
    }
    done++;
    onProgress && onProgress(done, tasks.length);
  }

  return { articleCopy, footerCopy, failed: stillFailed.length, failedSrcs: stillFailed };
}
