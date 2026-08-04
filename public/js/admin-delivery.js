// admin-delivery.js — 发布审核的应用 / 多群配置视图。
//
// 这个视图只负责选择和保存。凭证、群权限与任务级投递快照都在服务端处理；
// 浏览器只拿到应用名称和当前飞书登录用户可见的群目录。
import { authFetch } from './auth-session.js';

function esc(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

async function responseError(response, fallback) {
  const payload = await response.json().catch(() => ({}));
  if (response.status === 401) return '后台需要先完成飞书登录';
  if (response.status === 403) return payload.error || '当前登录账号没有读取飞书群列表的权限';
  return payload.error || `${fallback}（HTTP ${response.status}）`;
}

export function createAdminDeliveryController({ getOverlay, rerender, onMessage } = {}) {
  let generation = 0;
  let attempted = false;
  let loading = false;
  let saving = false;
  let error = '';
  let apps = [];
  let chats = [];
  let settings = { app_profile_id: '', chat_ids: [] };

  function reset() {
    generation += 1;
    attempted = false;
    loading = false;
    saving = false;
    error = '';
    apps = [];
    chats = [];
    settings = { app_profile_id: '', chat_ids: [] };
  }

  function render() {
    const selected = new Set(Array.isArray(settings.chat_ids) ? settings.chat_ids : []);
    const appOptions = apps.length
      ? apps.map(app => `<option value="${esc(app.id)}" ${app.id === settings.app_profile_id ? 'selected' : ''}>${esc(app.name || app.id)}</option>`).join('')
      : '<option value="">暂无可用推送应用</option>';
    let chatHTML = '';
    if (loading && !chats.length) {
      chatHTML = '<div class="admin-delivery-empty">正在读取当前账号加入的群…</div>';
    } else if (!chats.length) {
      chatHTML = '<div class="admin-delivery-empty">当前账号没有可选择的群，或飞书群列表权限尚未开通。</div>';
    } else {
      chatHTML = chats.map(chat => {
        const id = String(chat.chat_id || '').trim();
        const name = String(chat.name || id || '未命名群').trim();
        return `<label class="admin-delivery-chat">
          <input type="checkbox" data-delivery-chat="${esc(id)}" ${selected.has(id) ? 'checked' : ''}>
          <span class="admin-delivery-chat-copy"><span>${esc(name)}</span>${chat.external ? '<small>外部群</small>' : ''}</span>
        </label>`;
      }).join('');
    }
    return `<section class="admin-delivery-view">
      <div class="admin-delivery-heading">
        <div><h3>发布链路</h3><p class="admin-panel-note">审核卡会由所选应用推送到所选群。每篇已生成任务会继续使用创建时的目标。</p></div>
        <button type="button" class="admin-secondary admin-delivery-refresh" data-delivery-action="refresh" ${loading || saving ? 'disabled' : ''}>${loading ? '读取中…' : '刷新群列表'}</button>
      </div>
      <div class="admin-delivery-section">
        <label class="admin-field admin-delivery-app-field"><span>推送应用</span><select data-delivery-app ${saving ? 'disabled' : ''}>${appOptions}</select></label>
        <div class="admin-delivery-section-title"><span>审核群</span><small>可多选</small></div>
        <div class="admin-delivery-chat-list">${chatHTML}</div>
      </div>
      ${error ? `<div class="admin-delivery-error" role="alert">${esc(error)}</div>` : ''}
      <div class="admin-delivery-actions"><button type="button" class="admin-primary" data-delivery-action="save" ${saving || loading || !apps.length || !chats.length ? 'disabled' : ''}>${saving ? '保存中…' : '保存发布链路'}</button></div>
    </section>`;
  }

  async function load() {
    if (loading) return;
    const token = ++generation;
    attempted = true;
    loading = true;
    error = '';
    rerender?.();
    try {
      const [settingsResponse, chatsResponse] = await Promise.all([
        authFetch('/api/publish/delivery-settings', { cache: 'no-store' }),
        authFetch('/api/feishu/chats', { cache: 'no-store' }),
      ]);
      if (token !== generation) return;
      if (!settingsResponse.ok) throw new Error(await responseError(settingsResponse, '读取发布链路失败'));
      if (!chatsResponse.ok) throw new Error(await responseError(chatsResponse, '读取飞书群列表失败'));
      const delivery = await settingsResponse.json();
      const chatPayload = await chatsResponse.json();
      apps = Array.isArray(delivery.apps) ? delivery.apps : [];
      settings = delivery.settings || { app_profile_id: '', chat_ids: [] };
      chats = Array.isArray(chatPayload.chats) ? chatPayload.chats : [];
    } catch (loadError) {
      if (token !== generation) return;
      error = loadError?.message || '读取发布链路失败';
    } finally {
      if (token !== generation) return;
      loading = false;
      rerender?.();
    }
  }

  async function save() {
    if (saving || loading) return;
    const overlay = getOverlay?.();
    if (!overlay) return;
    const appID = overlay.querySelector('[data-delivery-app]')?.value || '';
    const chatIDs = [...overlay.querySelectorAll('[data-delivery-chat]:checked')].map(input => input.dataset.deliveryChat).filter(Boolean);
    const token = ++generation;
    saving = true;
    error = '';
    onMessage?.('正在保存发布链路…');
    rerender?.();
    try {
      const response = await authFetch('/api/publish/delivery-settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ app_profile_id: appID, chat_ids: chatIDs }),
      });
      if (token !== generation) return;
      if (!response.ok) throw new Error(await responseError(response, '保存发布链路失败'));
      const payload = await response.json();
      settings = payload.settings || { app_profile_id: appID, chat_ids: chatIDs };
      onMessage?.('发布链路已保存；新任务将按当前应用和群发送');
    } catch (saveError) {
      if (token !== generation) return;
      error = saveError?.message || '保存发布链路失败';
      onMessage?.(error);
    } finally {
      if (token !== generation) return;
      saving = false;
      rerender?.();
    }
  }

  function bind() {
    const overlay = getOverlay?.();
    if (!overlay) return;
    overlay.querySelector('[data-delivery-action="refresh"]')?.addEventListener('click', load);
    overlay.querySelector('[data-delivery-action="save"]')?.addEventListener('click', save);
    if (!attempted) load();
  }

  return { render, bind, reset };
}
