// Image upload — only base64 images (from DOCX) are uploaded to OSS.
// External URLs are used directly; mmbiz URLs are already on WeChat CDN.

export function isMmbizUrl(url) {
  return /^https?:\/\/mmbiz\.qpic\.cn\//i.test(url);
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

// Upload base64 images from DOCX to OSS. External URLs are used directly.
// mmbiz URLs are already on WeChat CDN — skip them (preserves GIF animations).
//
// Returns { articleCopy, footerCopy, total, failed, failedSrcs }.
// failedSrcs contains original src strings of images that failed even after retry.
export async function uploadNonCdnImages(articleCopy, footerCopy, { onProgress, onLog }) {
  const allHtml = articleCopy + '\n' + footerCopy;

  const tasks = [];
  let m, mmbizCount = 0;

  // base64 data URIs (from DOCX) — must upload to OSS
  const b64Re = /src="(data:image\/[^"]+)"/g;
  while ((m = b64Re.exec(allHtml)) !== null) {
    tasks.push({ type: 'base64', src: m[1], key: 'img_' + tasks.length });
  }

  // HTTP URLs — use directly, no upload needed
  // (mmbiz URLs are already on WeChat CDN; other external URLs work as-is)
  const urlRe = /src="(https?:\/\/[^"]+)"/g;
  while ((m = urlRe.exec(allHtml)) !== null) {
    if (isMmbizUrl(m[1])) mmbizCount++;
  }

  if (tasks.length === 0) {
    onLog && onLog('info', '无需上传', { mmbiz已有: mmbizCount });
    return { articleCopy, footerCopy, total: 0, failed: 0, failedSrcs: [] };
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

  const failedSrcs = failedTasks.map(t => t.src);
  if (failedTasks.length > 0) {
    onLog && onLog('warn', '上传完成，' + failedTasks.length + '张失败（已自动重试1次）');
  } else {
    onLog && onLog('info', '全部图片上传成功', { total: tasks.length });
  }

  return { articleCopy, footerCopy, total: tasks.length, failed: failedTasks.length, failedSrcs };
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
