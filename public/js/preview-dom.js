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

export function renderPreviewContent(container, articleHtml, footerHtml) {
  if (!container) return [];
  const content = document.createDocumentFragment();
  content.append(fragmentFromHtml(articleHtml));
  content.append(document.createComment(FOOTER_MARKER));
  content.append(fragmentFromHtml(footerHtml));
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
