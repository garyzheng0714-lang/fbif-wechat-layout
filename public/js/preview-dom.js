// Preview DOM lifecycle helpers.
//
// The article and footer deliberately remain direct children of #contentArea:
// wrapping either section would change long-standing template selectors. A
// comment node is the zero-layout boundary that lets footer-only edits preserve
// every article node (especially decoded <img> elements).
const FOOTER_MARKER = 'fbif-preview-footer';
const renderedFooterHtml = new WeakMap();

function fragmentFromHtml(html) {
  const template = document.createElement('template');
  template.innerHTML = String(html || '');
  return template.content;
}

function findFooterMarker(container) {
  if (!container) return null;
  for (const node of container.childNodes) {
    if (node.nodeType === Node.COMMENT_NODE && node.nodeValue === FOOTER_MARKER) {
      return node;
    }
  }
  return null;
}

function usePreparedImages(content, preparedImages) {
  if (!(preparedImages instanceof Map) || preparedImages.size === 0) return;
  const offsets = new Map();
  for (const placeholder of content.querySelectorAll('img')) {
    const src = placeholder.getAttribute('src') || '';
    const stored = preparedImages.get(src);
    const offset = offsets.get(src) || 0;
    const prepared = Array.isArray(stored) ? stored[offset] : stored;
    if (!prepared) continue;
    for (const attr of Array.from(prepared.attributes)) {
      if (attr.name !== 'src' && !placeholder.hasAttribute(attr.name)) {
        prepared.removeAttribute(attr.name);
      }
    }
    for (const attr of Array.from(placeholder.attributes)) {
      if (attr.name !== 'src' && prepared.getAttribute(attr.name) !== attr.value) {
        prepared.setAttribute(attr.name, attr.value);
      }
    }
    placeholder.replaceWith(prepared);
    if (!Array.isArray(stored) || offset + 1 >= stored.length) preparedImages.delete(src);
    else offsets.set(src, offset + 1);
  }
}

export function collectReusableArticleImages(container) {
  const reusable = new Map();
  if (!container) return reusable;
  const marker = findFooterMarker(container);
  for (const child of container.childNodes) {
    if (child === marker) break;
    if (child.nodeType !== Node.ELEMENT_NODE) continue;
    const images = child.matches('img')
      ? [child]
      : Array.from(child.querySelectorAll('img'));
    for (const image of images) {
      const src = image.getAttribute('src') || '';
      if (!src || reusable.has(src) || !image.complete || image.naturalWidth <= 0) continue;
      reusable.set(src, image);
    }
  }
  return reusable;
}

export function preparePreviewContent(articleHtml, footerHtml, { preparedImages } = {}) {
  const content = document.createDocumentFragment();
  content.append(fragmentFromHtml(articleHtml));
  content.append(document.createComment(FOOTER_MARKER));
  content.append(fragmentFromHtml(footerHtml));
  usePreparedImages(content, preparedImages);
  return {
    content,
    images: Array.from(content.querySelectorAll('img')),
    footerHtml: String(footerHtml || ''),
  };
}

// A detached decoded image can restart its request when it first becomes
// connected (especially with no-store responses). Feishu therefore builds its
// final nodes inside an invisible, connected staging root while the old article
// remains visible. The exact staged nodes are decoded and later moved directly
// into #contentArea in one synchronous commit.
export function prepareConnectedPreviewContent(articleHtml, footerHtml) {
  const staging = document.createElement('div');
  staging.setAttribute('aria-hidden', 'true');
  staging.dataset.previewStaging = 'true';
  staging.style.position = 'fixed';
  staging.style.inset = '0 auto auto 0';
  staging.style.width = '680px';
  staging.style.visibility = 'hidden';
  staging.style.pointerEvents = 'none';
  staging.style.zIndex = '-2147483648';
  staging.style.contain = 'layout style paint';
  document.body.append(staging);
  staging.innerHTML = `${String(articleHtml || '')}<!--${FOOTER_MARKER}-->${String(footerHtml || '')}`;
  return {
    content: null,
    staging,
    images: Array.from(staging.querySelectorAll('img')),
    footerHtml: String(footerHtml || ''),
  };
}

export function validatePreparedPreview(prepared) {
  const images = Array.isArray(prepared?.images) ? prepared.images : [];
  const failedImages = images.filter(image => !image?.complete || image.naturalWidth <= 0);
  return {
    total: images.length,
    ready: images.length - failedImages.length,
    failedImages,
  };
}

export function indexPreparedPreviewImages(prepared) {
  const indexed = new Map();
  const images = Array.isArray(prepared?.images) ? prepared.images : [];
  for (const image of images) {
    const src = image?.getAttribute?.('src') || '';
    if (!src) continue;
    const current = indexed.get(src);
    if (!current) {
      indexed.set(src, image);
    } else if (Array.isArray(current)) {
      current.push(image);
    } else {
      indexed.set(src, [current, image]);
    }
  }
  return indexed;
}

export function commitPreparedPreviewContent(container, prepared) {
  if (!container || (!prepared?.content && !prepared?.staging)) return [];
  if (prepared.staging) {
    container.replaceChildren(...Array.from(prepared.staging.childNodes));
    prepared.staging.remove();
    prepared.staging = null;
  } else {
    container.replaceChildren(prepared.content);
  }
  renderedFooterHtml.set(container, String(prepared.footerHtml || ''));
  return Array.isArray(prepared.images) ? prepared.images : [];
}

export function discardPreparedPreviewContent(prepared) {
  if (!prepared?.staging) return;
  prepared.staging.remove();
  prepared.staging = null;
}

export function renderPreviewContent(container, articleHtml, footerHtml, { preparedImages } = {}) {
  if (!container) return [];
  const prepared = preparePreviewContent(articleHtml, footerHtml, { preparedImages });
  const images = commitPreparedPreviewContent(container, prepared);
  return images;
}

export function updatePreviewFooter(container, footerHtml) {
  if (!container) return { preservedArticle: false, images: [] };
  const marker = findFooterMarker(container);
  if (!marker) return { preservedArticle: false, images: [] };

  const nextHtml = String(footerHtml || '');
  if (renderedFooterHtml.get(container) === nextHtml) {
    return { preservedArticle: true, images: [] };
  }

  while (marker.nextSibling) marker.nextSibling.remove();
  const footer = fragmentFromHtml(nextHtml);
  const images = Array.from(footer.querySelectorAll('img'));
  container.append(footer);
  renderedFooterHtml.set(container, nextHtml);
  return { preservedArticle: true, images };
}

export function adoptPreviewContent(container, footerHtml) {
  if (!findFooterMarker(container)) return false;
  renderedFooterHtml.set(container, String(footerHtml || ''));
  return true;
}
