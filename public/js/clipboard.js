// Clipboard copy — tries modern Clipboard API first, falls back to execCommand.

export async function copyByClipboardApi(html, plainText) {
  if (!(navigator.clipboard && typeof ClipboardItem !== 'undefined')) return false;
  await navigator.clipboard.write([new ClipboardItem({
    'text/html': new Blob([html], { type: 'text/html' }),
    'text/plain': new Blob([plainText], { type: 'text/plain' }),
  })]);
  return true;
}

export async function copyByClipboardEvent(html, plainText) {
  const handler = function(e) {
    e.clipboardData.setData('text/html', html);
    e.clipboardData.setData('text/plain', plainText);
    e.preventDefault();
  };
  document.addEventListener('copy', handler, true);
  const temp = document.createElement('div');
  temp.style.position = 'fixed';
  temp.style.left = '-99999px';
  temp.style.top = '0';
  temp.innerHTML = html;
  document.body.appendChild(temp);
  try {
    const range = document.createRange();
    range.selectNodeContents(temp);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    const ok = document.execCommand('copy');
    sel.removeAllRanges();
    return !!ok;
  } finally {
    document.body.removeChild(temp);
    document.removeEventListener('copy', handler, true);
  }
}
