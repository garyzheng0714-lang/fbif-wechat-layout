// asset-library.js — resolve structured asset references in rule modules.
// Kept standalone so renderers can use asset ids without importing each other.

const TRANSPARENT_PLACEHOLDER =
  'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==';

function isPlainObject(value) {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function cleanID(value) {
  return String(value == null ? '' : value).trim();
}

function isSafeAssetURL(value) {
  const url = String(value == null ? '' : value).trim();
  if (!url || /[\x00-\x20\x7F"'<>\\]/.test(url)) return false;
  if (url.startsWith('/')) return !url.startsWith('//');
  try {
    const parsed = new URL(url);
    return parsed.protocol === 'https:';
  } catch {
    return false;
  }
}

function assetAllowedInScope(asset, scope) {
  const assetScope = cleanID(asset && asset.scope) || 'global';
  const targetScope = cleanID(scope) || 'global';
  return assetScope === 'global' || assetScope === targetScope;
}

function normalizeAssets(assetLibrary) {
  if (Array.isArray(assetLibrary)) return assetLibrary.filter(isPlainObject);
  if (isPlainObject(assetLibrary) && Array.isArray(assetLibrary.assets)) {
    return assetLibrary.assets.filter(isPlainObject);
  }
  return [];
}

function warnMissingAsset(ref) {
  if (typeof console !== 'undefined' && console.warn) {
    console.warn('[asset-library] unresolved asset ref:', ref);
  }
}

function findAsset(assets, ref, scope) {
  const id = cleanID(ref.id);
  const usage = cleanID(ref.usage);
  for (const asset of assets) {
    if (!assetAllowedInScope(asset, scope)) continue;
    if (id && cleanID(asset.id) === id) return asset;
    if (!id && usage && cleanID(asset.usage) === usage) return asset;
  }
  return null;
}

function refFromToken(token) {
  const match = String(token || '').match(/^\{\{asset(?::([a-z0-9_-]+)|#([a-z0-9_-]+))\}\}$/i);
  if (!match) return null;
  return match[2] ? { id: match[2] } : { usage: match[1] };
}

function assetURL(asset, ref) {
  const url = String(asset && asset.url || '').trim();
  if (isSafeAssetURL(url)) return url;
  warnMissingAsset(ref);
  return TRANSPARENT_PLACEHOLDER;
}

function dimensionAttr(asset, key) {
  const n = Number(asset && asset[key]);
  return Number.isFinite(n) && n > 0 ? String(Math.round(n)) : '';
}

function appendMissingDimensionAttrs(imgTag, asset) {
  let out = imgTag;
  const width = dimensionAttr(asset, 'width');
  const height = dimensionAttr(asset, 'height');
  const insert = [];
  if (width && !/\swidth\s*=/i.test(out)) insert.push(`width="${width}"`);
  if (height && !/\sheight\s*=/i.test(out)) insert.push(`height="${height}"`);
  if (!insert.length) return out;
  return out.replace(/\s*\/?>$/, match => ' ' + insert.join(' ') + match);
}

function resolveImgTag(tag, assets, scope) {
  const srcMatch = tag.match(/\ssrc\s*=\s*(["'])(\{\{asset(?::[a-z0-9_-]+|#[a-z0-9_-]+)\}\})\1/i);
  if (!srcMatch) return tag;
  const ref = refFromToken(srcMatch[2]);
  const asset = ref ? findAsset(assets, ref, scope) : null;
  if (!asset) {
    warnMissingAsset(ref || srcMatch[2]);
  }
  const url = asset ? assetURL(asset, ref) : TRANSPARENT_PLACEHOLDER;
  const withSrc = tag.replace(srcMatch[0], ` src=${srcMatch[1]}${url}${srcMatch[1]}`);
  return asset ? appendMissingDimensionAttrs(withSrc, asset) : withSrc;
}

export function resolveAssetRefs(htmlLines, assetLibrary, scope = 'global') {
  const lines = Array.isArray(htmlLines) ? htmlLines : [];
  const assets = normalizeAssets(assetLibrary);
  return lines.map(line => {
    let out = String(line == null ? '' : line);
    out = out.replace(/<img\b[^>]*\ssrc\s*=\s*["']\{\{asset(?::[a-z0-9_-]+|#[a-z0-9_-]+)\}\}["'][^>]*>/gi,
      tag => resolveImgTag(tag, assets, scope));
    return out.replace(/\{\{asset(?::([a-z0-9_-]+)|#([a-z0-9_-]+))\}\}/gi, token => {
      const ref = refFromToken(token);
      const asset = ref ? findAsset(assets, ref, scope) : null;
      if (!asset) {
        warnMissingAsset(ref || token);
        return TRANSPARENT_PLACEHOLDER;
      }
      return assetURL(asset, ref);
    });
  });
}
