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
  for (const placeholder of content.querySelectorAll('img')) {
    const src = placeholder.getAttribute('src') || '';
    const prepared = preparedImages.get(src);
    if (!prepared || !prepared.complete || prepared.naturalWidth <= 0) continue;
    for (const attr of Array.from(prepared.attributes)) {
      if (attr.name !== 'src' && !placeholder.hasAttribute(attr.name)) {
        prepared.removeAttribute(attr.name);
      }
    }
    for (const attr of Array.from(placeholder.attributes)) {
      if (attr.name !== 'src') prepared.setAttribute(attr.name, attr.value);
    }
    placeholder.replaceWith(prepared);
    preparedImages.delete(src);
  }
}

export function renderPreviewContent(container, articleHtml, footerHtml, { preparedImages } = {}) {
  if (!container) return [];
  const content = document.createDocumentFragment();
  content.append(fragmentFromHtml(articleHtml));
  content.append(document.createComment(FOOTER_MARKER));
  content.append(fragmentFromHtml(footerHtml));
  usePreparedImages(content, preparedImages);
  const images = Array.from(content.querySelectorAll('img'));
  container.replaceChildren(content);
  renderedFooterHtml.set(container, String(footerHtml || ''));
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
