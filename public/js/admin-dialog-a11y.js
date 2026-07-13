// 规则后台对话框的焦点与背景隔离：让 aria-modal 的声明与真实键盘行为一致。

const FOCUSABLE = 'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, a[href], [tabindex]:not([tabindex="-1"])';

export function createAdminDialogA11y({ document, overlay, opener, onEscape }) {
  const inertSiblings = [...document.body.children].filter(el => el !== overlay && !el.hasAttribute('inert'));
  inertSiblings.forEach(el => el.setAttribute('inert', ''));
  document.body.classList.add('admin-panel-open');
  let released = false;

  const focusableElements = () => [...overlay.querySelectorAll(FOCUSABLE)]
    .filter(el => !el.closest('[hidden]') && el.getAttribute('aria-hidden') !== 'true');

  function onKey(event) {
    if (event.key === 'Escape') {
      event.preventDefault();
      onEscape();
      return;
    }
    if (event.key !== 'Tab') return;
    const items = focusableElements();
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1], active = document.activeElement;
    if (event.shiftKey && (active === first || !overlay.contains(active))) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && (active === last || !overlay.contains(active))) {
      event.preventDefault();
      first.focus();
    }
  }
  document.addEventListener('keydown', onKey);

  return {
    focusSelectorFor(el) {
      if (!el || !overlay.contains(el)) return '';
      if (el.classList?.contains('admin-close')) return '.admin-close';
      if (el.matches?.('.admin-login-form input')) return '.admin-login-form input';
      for (const attr of ['data-view', 'data-rule-tab', 'data-action', 'data-role']) {
        const value = el.getAttribute?.(attr);
        if (value) return `[${attr}="${String(value).replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"]`;
      }
      return '';
    },
    focusAfterRender(preferred, fallback) {
      if (overlay.contains(document.activeElement)) return;
      const target = (preferred && overlay.querySelector(preferred)) ||
        (fallback && overlay.querySelector(fallback)) || focusableElements()[0];
      target?.focus();
    },
    release() {
      if (released) return;
      released = true;
      document.removeEventListener('keydown', onKey);
      inertSiblings.forEach(el => el.removeAttribute('inert'));
      document.body.classList.remove('admin-panel-open');
      if (opener?.isConnected || document.contains(opener)) opener?.focus();
    },
  };
}

export function bindRovingTabs(buttons, onActivate) {
  buttons.forEach((button, index) => {
    button.addEventListener('click', () => onActivate(button));
    button.addEventListener('keydown', event => {
      let next = index;
      if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (index + 1) % buttons.length;
      else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (index - 1 + buttons.length) % buttons.length;
      else if (event.key === 'Home') next = 0;
      else if (event.key === 'End') next = buttons.length - 1;
      else return;
      event.preventDefault();
      buttons[next].focus();
      buttons[next].click();
    });
  });
}
