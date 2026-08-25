// 规则后台工作台外壳：只在首次进入时建 dialog，之后只补内容区和页脚。

export function bindAdminWorkbenchShell(overlay, { onClose, onViewChange } = {}) {
  if (!overlay || overlay.dataset.adminShellBound === '1') return;
  overlay.dataset.adminShellBound = '1';
  overlay.addEventListener('click', event => {
    if (event.target.closest('.admin-close')) {
      onClose?.();
      return;
    }
    const viewBtn = event.target.closest('[data-view]');
    if (!viewBtn || !overlay.querySelector('.admin-side-nav')?.contains(viewBtn)) return;
    const next = viewBtn.dataset.view;
    if (!next) return;
    onViewChange?.(next);
  });
}

export function ensureAdminWorkbench(overlay) {
  const existing = overlay.querySelector('.admin-dialog.admin-workbench');
  if (existing) return existing;
  overlay.innerHTML = `
    <div class="admin-dialog admin-workbench is-entering" role="dialog" aria-modal="true" aria-labelledby="adminTitle">
      <div class="admin-head">
        <div>
          <div class="admin-title" id="adminTitle">规则后台</div>
          <div class="admin-subtitle" data-role="admin-subtitle"></div>
        </div>
        <button type="button" class="admin-close" aria-label="关闭">×</button>
      </div>
      <div class="admin-layout">
        <div data-role="admin-nav"></div>
        <div class="admin-body"></div>
      </div>
      <div class="admin-foot">
        <div class="admin-status" role="status" aria-live="polite"></div>
        <span class="admin-dirty-chip" data-role="dirty-chip" hidden></span>
        <div data-role="admin-foot-action"></div>
      </div>
    </div>`;
  const dialog = overlay.querySelector('.admin-dialog');
  const dropEntering = () => dialog.classList.remove('is-entering');
  dialog.addEventListener('animationend', dropEntering, { once: true });
  if (typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    dropEntering();
  }
  return dialog;
}

export function syncAdminWorkbenchNav(overlay, { activeView, navHTML, wantAccess }) {
  const mount = overlay.querySelector('[data-role="admin-nav"]');
  if (!mount) return;
  const nav = mount.querySelector('.admin-side-nav');
  const hasAccess = !!(nav && nav.querySelector('[data-view="access"]'));
  if (!nav || wantAccess !== hasAccess) {
    mount.innerHTML = navHTML;
    return;
  }
  nav.querySelectorAll('[data-view]').forEach(btn => {
    const on = btn.dataset.view === activeView;
    btn.classList.toggle('active', on);
    if (on) btn.setAttribute('aria-current', 'page');
    else btn.removeAttribute('aria-current');
  });
}

export function patchAdminWorkbenchFooter(overlay, { message, actionHTML, tracksDraft, changes }) {
  const statusEl = overlay.querySelector('.admin-foot .admin-status');
  if (statusEl) statusEl.textContent = message || '';
  const chip = overlay.querySelector('[data-role="dirty-chip"]');
  if (chip) {
    if (tracksDraft) {
      chip.hidden = false;
      chip.classList.toggle('dirty', !!changes);
      chip.textContent = changes ? changes + ' 项未保存' : '已保存';
    } else {
      chip.hidden = true;
      chip.classList.remove('dirty');
      chip.textContent = '';
    }
  }
  const actionMount = overlay.querySelector('[data-role="admin-foot-action"]');
  if (actionMount) actionMount.innerHTML = actionHTML || '';
}
