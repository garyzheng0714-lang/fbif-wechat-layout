import { fetchWithTimeout } from './fetch-timeout.js';

export function isValidMoreArticleUrl(value) {
  const url = String(value || '').trim();
  try {
    const parsed = new URL(url);
    const protocol = parsed.protocol.toLowerCase();
    const host = parsed.hostname.toLowerCase();
    return (protocol === 'http:' || protocol === 'https:') &&
           (host === 'mp.weixin.qq.com' || host === 'weixin.qq.com') &&
           (parsed.pathname === '/s' || parsed.pathname.startsWith('/s/'));
  } catch {
    return false;
  }
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
    const resp = await fetchWithTimeout('/api/fetch-article-meta', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: normalized }),
    }, { timeoutMs: 15000, timeoutMessage: '获取文章信息超时，请重试' });
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
