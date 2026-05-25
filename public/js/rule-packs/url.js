// rule-packs/url.js — 纯 URL 解析工具，不依赖 DOM/location。
// 渠道路径形如 /mp/<packId> 或 /ft/<packId>。

const KNOWN_CHANNELS = Object.freeze(new Set(['mp', 'ft']));

export function parsePath(pathname) {
  if (typeof pathname !== 'string' || pathname === '') return null;
  const segments = pathname.split('/').filter(Boolean);
  if (segments.length === 0) return null;
  const channel = segments[0];
  if (!KNOWN_CHANNELS.has(channel)) return null;
  const packId = segments[1] && segments[1].length > 0 ? decodeURIComponent(segments[1]) : null;
  return { channel, packId };
}

export function buildPath(channel, packId) {
  if (!KNOWN_CHANNELS.has(channel)) {
    throw new Error(`buildPath: unknown channel "${channel}"; expected one of ${[...KNOWN_CHANNELS].join(', ')}`);
  }
  if (!packId || typeof packId !== 'string') {
    throw new Error(`buildPath: packId is required, got ${JSON.stringify(packId)}`);
  }
  return `/${channel}/${encodeURIComponent(packId)}`;
}

export const CHANNELS = Object.freeze([...KNOWN_CHANNELS]);
