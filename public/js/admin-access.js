import { authFetch } from './auth-session.js';

function esc(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function roleLabel(role) {
  if (role === 'super_admin') return '超级管理员';
  if (role === 'admin') return '管理员';
  return '普通成员';
}

export function renderRuleAdminAccessDenied(status, hint) {
  return `<div class="admin-dialog compact" role="dialog" aria-modal="true" aria-labelledby="adminTitle">
    <div class="admin-head"><div><div class="admin-title" id="adminTitle">规则后台</div><div class="admin-subtitle">${esc(hint)}</div></div><button type="button" class="admin-close" aria-label="关闭">×</button></div>
    <div class="admin-access-denied" role="status" aria-live="polite"><strong>当前账号没有规则后台权限</strong><p>${esc(status || '请让超级管理员在“权限管理”中授权后重新打开。')}</p></div>
  </div>`;
}

export function renderRuleAdminAccessLoading(hint) {
  return `<div class="admin-dialog compact" role="dialog" aria-modal="true" aria-labelledby="adminTitle">
    <div class="admin-head"><div><div class="admin-title" id="adminTitle">规则后台</div><div class="admin-subtitle">${esc(hint)}</div></div><button type="button" class="admin-close" aria-label="关闭">×</button></div>
    <div class="admin-access-loading" role="status" aria-live="polite">正在验证当前登录账号…</div>
  </div>`;
}

export function createAdminAccessController({
  getOverlay,
  getAuthUser,
  rerender,
  onMessage,
} = {}) {
  let users = [];
  let loading = false;
  let saving = false;
  let error = '';
  let generation = 0;

  const canManage = () => {
    const user = getAuthUser?.() || {};
    return String(user.role || '').trim() === 'super_admin' || user.capabilities?.rule_super_admin === true;
  };

  function render() {
    const body = loading && !users.length
      ? '<div class="admin-access-loading">正在读取已登录账号…</div>'
      : users.length
      ? users.map(user => {
        const role = String(user.role || 'user');
        const fixed = role === 'super_admin';
        return `<div class="admin-access-row">
          <div class="admin-access-user">
            ${user.avatar_url ? `<img src="${esc(user.avatar_url)}" alt="">` : '<span class="admin-access-avatar">用</span>'}
            <span><strong>${esc(user.name || '未命名用户')}</strong><small>${esc(user.auth_mode === 'feishu' ? '飞书登录' : '账号登录')}</small></span>
          </div>
          ${fixed ? `<span class="admin-access-role is-fixed">${roleLabel(role)}</span>` : `<select data-access-user="${esc(user.id)}" ${saving ? 'disabled' : ''} aria-label="${esc(user.name || '用户')}的后台角色">
            <option value="user" ${role === 'user' ? 'selected' : ''}>普通成员</option>
            <option value="admin" ${role === 'admin' ? 'selected' : ''}>管理员</option>
          </select>`}
        </div>`;
      }).join('')
      : '<div class="admin-access-empty">还没有其他账号登录过。让对方先登录一次，就会出现在这里。</div>';
    return `<section class="admin-access-view">
      <div class="admin-access-heading"><div><h3>权限管理</h3><p class="admin-panel-note">只有登录过本工具的账号会显示。你可以授权或收回“管理员”权限。</p></div><button type="button" class="admin-secondary" data-access-action="refresh" ${loading || saving ? 'disabled' : ''}>${loading ? '读取中…' : '刷新账号'}</button></div>
      <div class="admin-access-list">${body}</div>
      ${error ? `<div class="admin-delivery-error" role="alert">${esc(error)}</div>` : ''}
    </section>`;
  }

  async function load() {
    if (!canManage() || loading) return;
    const token = ++generation;
    loading = true;
    error = '';
    rerender?.();
    try {
      const response = await authFetch('/api/rules/admin/access-users', { cache: 'no-store' });
      const payload = await response.json().catch(() => ({}));
      if (token !== generation) return;
      if (!response.ok) throw new Error(payload.error || `读取账号失败（HTTP ${response.status}）`);
      users = Array.isArray(payload.users) ? payload.users : [];
    } catch (err) {
      if (token !== generation) return;
      error = err.message || '读取账号失败';
    } finally {
      if (token !== generation) return;
      loading = false;
      rerender?.();
    }
  }

  async function updateRole(userID, role) {
    if (!canManage() || saving) return;
    const token = ++generation;
    saving = true;
    error = '';
    rerender?.();
    try {
      const response = await authFetch('/api/rules/admin/access-users/' + encodeURIComponent(userID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ role }),
      });
      const payload = await response.json().catch(() => ({}));
      if (token !== generation) return;
      if (!response.ok) throw new Error(payload.error || `更新权限失败（HTTP ${response.status}）`);
      users = users.map(user => user.id === userID ? (payload.user || { ...user, role }) : user);
      onMessage?.('后台权限已更新');
    } catch (err) {
      if (token !== generation) return;
      error = err.message || '更新权限失败';
    } finally {
      if (token !== generation) return;
      saving = false;
      rerender?.();
    }
  }

  function bind() {
    const overlay = getOverlay?.();
    if (!overlay || !canManage()) return;
    overlay.querySelector('[data-access-action="refresh"]')?.addEventListener('click', load);
    overlay.querySelectorAll('[data-access-user]').forEach(select => {
      select.addEventListener('change', () => updateRole(select.dataset.accessUser, select.value));
    });
    if (!users.length && !loading) load();
  }

  function reset() {
    generation++;
    users = [];
    loading = false;
    saving = false;
    error = '';
  }

  return { canManage, render, bind, reset };
}
