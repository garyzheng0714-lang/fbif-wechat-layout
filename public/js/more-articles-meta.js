export function isValidMoreArticleUrl(value) {
  const url = String(value || '').trim();
  return /^https?:\/\/mp\.weixin\.qq\.com\//i.test(url) ||
         /^https?:\/\/([^/]+\.)?weixin\.qq\.com\//i.test(url);
}

export function shouldAutoFetchMoreArticleUrl(value, lastFetchedUrl = '') {
  const url = String(value || '').trim();
  return isValidMoreArticleUrl(url) && url !== lastFetchedUrl;
}

const metaCache = new Map();

export async function fetchMoreArticleMetaCached(url) {
  const normalized = String(url || '').trim();
  if (metaCache.has(normalized)) return metaCache.get(normalized);
  const job = (async () => {
    const resp = await fetch('/api/fetch-article-meta', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: normalized }),
    });
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({}));
      throw new Error(err.error || ('HTTP ' + resp.status));
    }
    const meta = await resp.json();
    if (!meta || (!meta.title && !meta.cover_url)) {
      throw new Error('未提取到标题或头图，请检查链接是否完整');
    }
    return meta;
  })().catch(err => {
    metaCache.delete(normalized);
    throw err;
  });
  metaCache.set(normalized, job);
  return job;
}
