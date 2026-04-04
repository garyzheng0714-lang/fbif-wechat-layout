// Image upload to WeChat CDN — dual-layer architecture
// Preview layer keeps original URLs; copy layer gets CDN URLs.

export function isMmbizUrl(url) {
  return /^https?:\/\/mmbiz\.qpic\.cn\//i.test(url);
}

// Upload non-CDN images (base64 from DOCX + external HTTP URLs).
// mmbiz URLs are already on WeChat CDN — skip them (preserves GIF animations).
//
// Mutates articleCopy/footerCopy strings by replacing original src with CDN URLs.
// Returns { articleCopy, footerCopy } with all replacements applied.
export async function uploadNonCdnImages(articleCopy, footerCopy, { onProgress, onLog }) {
  const allHtml = articleCopy + '\n' + footerCopy;

  const tasks = [];
  let m, mmbizCount = 0;

  // base64 data URIs (from DOCX) — must upload
  const b64Re = /src="(data:image\/[^"]+)"/g;
  while ((m = b64Re.exec(allHtml)) !== null) {
    tasks.push({ type: 'base64', src: m[1], key: 'img_' + tasks.length });
  }

  // HTTP URLs — only upload non-mmbiz (external images)
  const urlRe = /src="(https?:\/\/[^"]+)"/g;
  while ((m = urlRe.exec(allHtml)) !== null) {
    if (isMmbizUrl(m[1])) {
      mmbizCount++;
    } else {
      tasks.push({ type: 'url', src: m[1] });
    }
  }

  if (tasks.length === 0) {
    onLog && onLog('info', '无需上传', { mmbiz已有: mmbizCount });
    return { articleCopy, footerCopy };
  }

  const b64Count = tasks.filter(t => t.type === 'base64').length;
  const extCount = tasks.filter(t => t.type === 'url').length;
  onLog && onLog('info', '开始上传图片到微信CDN', { base64: b64Count, 外链: extCount, mmbiz跳过: mmbizCount });
  onProgress && onProgress(0, tasks.length);

  let done = 0, failed = 0;
  const CONCURRENCY = 5;
  let next = 0;

  async function worker() {
    while (next < tasks.length) {
      const task = tasks[next++];
      try {
        const body = task.type === 'base64'
          ? JSON.stringify({ base64_images: { [task.key]: task.src } })
          : JSON.stringify({ urls: [task.src] });
        const resp = await fetch('/api/wechat-upload', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body,
        });
        if (resp.ok) {
          const { results } = await resp.json();
          const cdnUrl = task.type === 'base64'
            ? (results[task.key] || Object.values(results)[0])
            : (results[task.src] || Object.values(results)[0]);
          if (cdnUrl) {
            articleCopy = articleCopy.split(task.src).join(cdnUrl);
            footerCopy = footerCopy.split(task.src).join(cdnUrl);
            onLog && onLog('info', '图片上传成功', { cdn: cdnUrl.substring(0, 60) + '...' });
          } else {
            failed++;
            onLog && onLog('error', '图片上传返回空', { src: task.src.substring(0, 80) });
          }
        } else {
          failed++;
          onLog && onLog('error', '图片上传HTTP错误', { status: resp.status, src: task.src.substring(0, 80) });
        }
      } catch (e) {
        failed++;
        onLog && onLog('error', '图片上传网络错误', { error: e.message });
      }
      done++;
      onProgress && onProgress(done, tasks.length);
    }
  }

  await Promise.all(Array.from({ length: Math.min(CONCURRENCY, tasks.length) }, () => worker()));

  if (failed > 0) {
    onLog && onLog('warn', '上传完成，' + failed + '张失败');
  } else {
    onLog && onLog('info', '全部图片上传成功', { total: tasks.length });
  }

  return { articleCopy, footerCopy, total: tasks.length, failed };
}
