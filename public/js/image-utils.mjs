const EXT_TO_MIME = {
  gif: 'image/gif',
  png: 'image/png',
  webp: 'image/webp',
  svg: 'image/svg+xml',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
};

function safeDecode(value) {
  if (!value) return '';
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export function looksLikeGifSource(src) {
  if (typeof src !== 'string') return false;
  const raw = src.trim();
  if (!raw) return false;

  const lower = raw.toLowerCase();
  if (lower.startsWith('data:image/gif')) return true;
  if (lower.includes('/mmbiz_gif/')) return true;
  if (/\.gif(?:$|[?#])/i.test(lower)) return true;

  const decoded = safeDecode(lower);
  if (/[?&](wx_fmt|fmt|format|tp|type|mime|ext)=gif(?:&|$)/i.test(decoded)) return true;

  try {
    const u = new URL(raw, 'https://dummy.local');
    for (const k of ['wx_fmt', 'fmt', 'format', 'tp', 'type', 'mime', 'ext']) {
      const v = u.searchParams.get(k);
      if (v && v.toLowerCase() === 'gif') return true;
    }
  } catch {
    // keep regex-based result
  }
  return false;
}

export function inferImageMimeFromBase64(base64Data, ext) {
  const sig = (base64Data || '').slice(0, 32);
  if (sig.startsWith('R0lGOD')) return 'image/gif';
  if (sig.startsWith('iVBORw0KGgo')) return 'image/png';
  if (sig.startsWith('/9j/')) return 'image/jpeg';
  if (sig.startsWith('UklGR')) return 'image/webp';
  return EXT_TO_MIME[(ext || '').toLowerCase()] || 'image/jpeg';
}
