function isMenuOpen(menu) {
  return !!menu && !menu.hidden;
}

export function mountCopySplitButton({
  windowRef = globalThis.window,
  documentRef = globalThis.document,
  rootID = 'copySplit',
  triggerID = 'copyMenuBtn',
  menuID = 'copyMenu',
  htmlButtonID = 'copyHtmlBtn',
  successID = 'copySuccess',
  successTimeoutMs = 10000,
} = {}) {
  if (!windowRef || !documentRef) return null;

  const root = documentRef.getElementById(rootID);
  const trigger = documentRef.getElementById(triggerID);
  const menu = documentRef.getElementById(menuID);
  const htmlButton = documentRef.getElementById(htmlButtonID);
  if (!root || !trigger || !menu || !htmlButton) return null;
  if (root.dataset.copySplitMounted === 'true') return null;
  root.dataset.copySplitMounted = 'true';

  const menuItems = () => Array.from(menu.querySelectorAll('[role="menuitem"]:not([disabled])'));

  function closeMenu({ restoreFocus = false } = {}) {
    menu.hidden = true;
    root.classList.remove('is-open');
    trigger.setAttribute('aria-expanded', 'false');
    if (restoreFocus && typeof trigger.focus === 'function') trigger.focus();
  }

  function openMenu({ focusFirst = false } = {}) {
    menu.hidden = false;
    root.classList.add('is-open');
    trigger.setAttribute('aria-expanded', 'true');
    if (focusFirst) {
      const first = menuItems()[0];
      if (first && typeof first.focus === 'function') first.focus();
    }
  }

  function toggleMenu() {
    if (isMenuOpen(menu)) closeMenu({ restoreFocus: true });
    else openMenu();
  }

  function showHTMLCopyFeedback(ok) {
    const success = documentRef.getElementById(successID);
    if (!success) return;
    if (!success.dataset.okText) success.dataset.okText = success.textContent || '已复制到剪贴板';
    success.textContent = ok ? '已复制 HTML 源码' : '复制 HTML 失败';
    success.classList.toggle('copy-success--warn', !ok);
    success.style.display = 'block';
    windowRef.setTimeout(() => {
      success.style.display = 'none';
      success.textContent = success.dataset.okText;
      success.classList.remove('copy-success--warn');
    }, successTimeoutMs);
  }

  trigger.addEventListener('click', event => {
    event.preventDefault();
    event.stopPropagation();
    toggleMenu();
  });

  trigger.addEventListener('keydown', event => {
    if (event.key !== 'ArrowDown' && event.key !== 'Enter' && event.key !== ' ') return;
    event.preventDefault();
    openMenu({ focusFirst: true });
  });

  menu.addEventListener('keydown', event => {
    const items = menuItems();
    const current = items.indexOf(documentRef.activeElement);
    let next = current;
    if (event.key === 'ArrowDown') next = current < 0 ? 0 : (current + 1) % items.length;
    else if (event.key === 'ArrowUp') next = current < 0 ? items.length - 1 : (current - 1 + items.length) % items.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = items.length - 1;
    else if (event.key === 'Escape') {
      event.preventDefault();
      closeMenu({ restoreFocus: true });
      return;
    } else if (event.key === 'Tab') {
      closeMenu();
      return;
    } else {
      return;
    }
    event.preventDefault();
    if (items[next] && typeof items[next].focus === 'function') items[next].focus();
  });

  documentRef.addEventListener('keydown', event => {
    if (event.key === 'Escape' && isMenuOpen(menu)) {
      event.preventDefault();
      closeMenu({ restoreFocus: true });
    }
  });

  documentRef.addEventListener('click', event => {
    if (isMenuOpen(menu) && !root.contains(event.target)) closeMenu();
  });

  htmlButton.addEventListener('click', async event => {
    event.preventDefault();
    event.stopPropagation();
    if (htmlButton.disabled || typeof windowRef.copyHTMLSource !== 'function') return;
    htmlButton.dataset.copyPending = 'true';
    htmlButton.disabled = true;
    htmlButton.setAttribute('aria-busy', 'true');
    try {
      const ok = await windowRef.copyHTMLSource();
      showHTMLCopyFeedback(ok !== false);
    } catch {
      showHTMLCopyFeedback(false);
    } finally {
      delete htmlButton.dataset.copyPending;
      htmlButton.removeAttribute('aria-busy');
      if (htmlButton.dataset.ruleBlocked !== 'true') htmlButton.disabled = false;
      closeMenu({ restoreFocus: true });
    }
  });

  return { openMenu, closeMenu };
}
