// Image upload to WeChat CDN — dual-layer architecture
// Preview layer keeps original URLs; copy layer gets CDN URLs.

export function isMmbizUrl(url) {
  return /^https?:\/\/mmbiz\.qpic\.cn\//i.test(url);
}

// Batch upload images to WeChat CDN. Returns results map { originalSrc: cdnUrl }.
async function uploadBatch(tasks) {
  const base64_images = {};
  const urls = [];
  for (const t of tasks) {
    if (t.type === 'base64') base64_images[t.key] = t.src;
    else urls.push(t.src);
  }
  const body = JSON.stringify(
    Object.keys(base64_images).length > 0 ? { base64_images } : { urls }
  );
  const resp = await fetch('/api/wechat-upload', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
  });
  if (!resp.ok) return {};
  const { results } = await resp.json();
  return results || {};
}

// Upload non-CDN images (base64 from DOCX + external HTTP URLs).
// mmbiz URLs are already on WeChat CDN — skip them (preserves GIF animations).
//
// Returns { articleCopy, footerCopy, total, failed, failedSrcs }.
export async function uploadNonCdnImages(articleCopy, footerCopy, { onProgress, onLog }) {
  const allHtml = articleCopy + '\n' + footerCopy;

  const b64Tasks = [];
  const urlTasks = [];
  let m, mmbizCount = 0;

  // base64 data URIs (from DOCX) — must upload
  const b64Re = /src="(data:image\/[^"]+)"/g;
  while ((m = b64Re.exec(allHtml)) !== null) {
    b64Tasks.push({ type: 'base64', src: m[1], key: 'img_' + b64Tasks.length });
  }

  // HTTP URLs — only upload non-mmbiz (external images)
  const urlRe = /src="(https?:\/\/[^"]+)"/g;
  while ((m = urlRe.exec(allHtml)) !== null) {
    if (isMmbizUrl(m[1])) {
      mmbizCount++;
    } else {
      urlTasks.push({ type: 'url', src: m[1] });
    }
  }

  const total = b64Tasks.length + urlTasks.length;
  if (total === 0) {
    onLog && onLog('info', '无需上传', { mmbiz已有: mmbizCount });
    return { articleCopy, footerCopy, total: 0, failed: 0, failedSrcs: [] };
  }

  onLog && onLog('info', '开始上传图片到微信CDN', { base64: b64Tasks.length, 外链: urlTasks.length, mmbiz跳过: mmbizCount });
  onProgress && onProgress(0, total);

  const failedSrcs = [];
  let done = 0;

  // Split into batches of 10 — server-side compression keeps upload data small
  const BATCH_SIZE = 10;
  const allTasks = [...b64Tasks, ...urlTasks];
  const batches = [];
  for (let i = 0; i < allTasks.length; i += BATCH_SIZE) {
    batches.push(allTasks.slice(i, i + BATCH_SIZE));
  }

  // Process batches sequentially to avoid overloading WeChat API
  for (const batch of batches) {
    let results = {};
    try {
      results = await uploadBatch(batch);
    } catch (e) {
      try { results = await uploadBatch(batch); } catch {}
    }

    for (const task of batch) {
      const key = task.type === 'base64' ? task.key : task.src;
      const cdnUrl = results[key];
      if (cdnUrl) {
        articleCopy = articleCopy.split(task.src).join(cdnUrl);
        footerCopy = footerCopy.split(task.src).join(cdnUrl);
      } else {
        failedSrcs.push(task.src);
      }
      done++;
      onProgress && onProgress(done, total);
    }
  }

  if (failedSrcs.length > 0) {
    onLog && onLog('warn', '上传完成，' + failedSrcs.length + '张失败');
  } else {
    onLog && onLog('info', '全部图片上传成功', { total });
  }

  return { articleCopy, footerCopy, total, failed: failedSrcs.length, failedSrcs };
}

// Retry specific failed images.
export async function retryFailedImages(articleCopy, footerCopy, failedSrcs, { onProgress, onLog }) {
  const tasks = failedSrcs.map((src, i) => {
    if (src.startsWith('data:')) return { type: 'base64', src, key: 'retry_' + i };
    return { type: 'url', src };
  });

  let results = {};
  try { results = await uploadBatch(tasks); } catch {}
  // Retry once
  const firstFailed = tasks.filter(t => !results[t.type === 'base64' ? t.key : t.src]);
  if (firstFailed.length > 0) {
    try {
      const retry = await uploadBatch(firstFailed);
      Object.assign(results, retry);
    } catch {}
  }

  let done = 0;
  const stillFailed = [];
  for (const task of tasks) {
    const key = task.type === 'base64' ? task.key : task.src;
    const cdnUrl = results[key];
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
