// Image upload — base64 images (from DOCX) are uploaded to OSS.
// Website-channel WeChat image CDN URLs are optionally re-hosted to our CDN.

const WECHAT_IMAGE_HOSTS = new Set([
  'mmbiz.qpic.cn',
  'mmecoa.qpic.cn',
  'mmbiz.qlogo.cn',
  'wx.qlogo.cn',
]);

export function isMmbizUrl(url) {
  try {
    const u = new URL(String(url || ''));
    return /^https?:$/i.test(u.protocol) && WECHAT_IMAGE_HOSTS.has(u.hostname.toLowerCase());
  } catch {
    return false;
  }
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
// WeChat image URLs are skipped unless transferMmbiz=true (preserves GIF animations
// for the WeChat account channel; re-hosts them for the website channel).
//
// Returns { articleCopy, footerCopy, total, failed, failedSrcs }.
// failedSrcs contains original src strings of images that failed even after retry.
export async function uploadNonCdnImages(articleCopy, footerCopy, { onProgress, onLog, onMaterialized, transferMmbiz = false } = {}) {
  const materialized = await materializeLazyImages(articleCopy, footerCopy, { onLog });
  articleCopy = materialized.articleCopy;
  footerCopy = materialized.footerCopy;
  const lazyFailedSrcs = materialized.failedSrcs || [];

  // Website channel: re-host WeChat image CDN images on our CDN. Pull each URL through
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
      const urlList = [...mmbizUrls];
      onLog && onLog('info', '网站渠道：转存微信图床到 CDN', { count: urlList.length });
      onProgress && onProgress(0, urlList.length);
      // 把 mmbiz URL 列表整体交给后端 /api/oss-upload 的 urls 模式：后端在服务器内部
      // 并发「取图 + 传 OSS」，直接返回 {mmbiz_url: oss_url}。比前端逐张「取图→base64
      // →上传」快一个量级——后端服务器并发取 mmbiz 不触发微信反爬限流（浏览器并发才
      // 会），且服务器到 OSS 走内网。实测 19 张 ~1.3s（前端逐张串行 ~20s）。
      try {
        const resp = await fetch('/api/oss-upload', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ urls: urlList }),
        });
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const { results } = await resp.json();
        let done = 0;
        for (const url of urlList) {
          const ossUrl = results && results[url];
          if (ossUrl) {
            articleCopy = articleCopy.split(url).join(ossUrl);
            footerCopy = footerCopy.split(url).join(ossUrl);
          } else {
            onLog && onLog('error', '微信图床转存失败（保留原链接）', { url: url.slice(0, 60) });
            mmbizFailedSrcs.push(url);
          }
          done++;
          onProgress && onProgress(done, urlList.length);
        }
      } catch (err) {
        // 整体请求失败：全部保留原 mmbiz 链接，标记失败（可重试）。
        onLog && onLog('error', '微信图床批量转存请求失败', { err: String(err) });
        for (const url of urlList) mmbizFailedSrcs.push(url);
        onProgress && onProgress(urlList.length, urlList.length);
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
