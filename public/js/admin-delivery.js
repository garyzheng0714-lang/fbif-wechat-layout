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

function issue(kind, title, message) {
  return { kind, title, message };
}

function isDeliveryIssue(value) {
  return !!(value && typeof value === 'object' && value.kind && value.title && value.message);
}

export function deliveryFailureIssue(error, fallback = '读取发布链路失败') {
  if (isDeliveryIssue(error)) return error;
  const detail = String(error?.message || error || '').trim();
  if (error instanceof TypeError || /failed to fetch|networkerror|load failed|fetch failed/i.test(detail)) {
    return issue(
      'network',
      '无法连接发布服务',
      '请确认本地预览或服务器仍在运行，然后重新读取。现有发布配置没有被改动。',
    );
  }
  return issue('server', fallback, detail || '服务暂时没有返回结果，请稍后重新读取。');
}

export async function deliveryResponseIssue(response, fallback) {
  const payload = await response.json().catch(() => ({}));
  if (response.status === 401) {
    return issue('auth', '登录状态已失效', '请重新登录后再配置发布链路。');
  }
  if (response.status === 403 && fallback.includes('群列表')) {
    return issue(
      'feishu-login',
      '需要使用飞书账号登录',
      '当前账号不能读取飞书群列表。请使用飞书账号登录后读取可加入的审核群；关闭后台，在左栏退出后即可切换登录方式。',
    );
  }
  if (response.status === 403) {
    return issue('permission', '当前账号没有配置权限', payload.error || '请联系超级管理员调整权限。');
  }
  return issue('server', fallback, payload.error || `服务返回 HTTP ${response.status}，请稍后重新读取。`);
}

function normalizeSettings(value) {
  const chatIDs = Array.isArray(value?.chat_ids) ? value.chat_ids : [];
  return {
    app_profile_id: String(value?.app_profile_id || '').trim(),
    chat_ids: [...new Set(chatIDs.map(id => String(id || '').trim()).filter(Boolean))],
    auto_publish: value?.auto_publish === true,
  };
}

function sameSettings(left, right) {
  const a = normalizeSettings(left);
  const b = normalizeSettings(right);
  return a.app_profile_id === b.app_profile_id
    && a.auto_publish === b.auto_publish
    && a.chat_ids.length === b.chat_ids.length
    && a.chat_ids.every(id => b.chat_ids.includes(id));
}

function renderIssue(problem, { retry = true } = {}) {
  if (!problem) return '';
  return `<div class="admin-delivery-issue" role="alert" data-delivery-issue="${esc(problem.kind)}">
    <div class="admin-delivery-issue-copy">
      <strong>${esc(problem.title)}</strong>
      <p>${esc(problem.message)}</p>
    </div>
    ${retry ? '<button type="button" class="admin-secondary admin-delivery-retry" data-delivery-action="refresh">重新读取</button>' : ''}
  </div>`;
}

export function createAdminDeliveryController({ getOverlay, rerender, onMessage, request = authFetch } = {}) {
  let generation = 0;
  let attempted = false;
  let loading = false;
  let saving = false;
  let settingsIssue = null;
  let chatsIssue = null;
  let saveIssue = null;
  let notice = '';
  let apps = [];
  let chats = [];
  let settings = normalizeSettings();
  let savedSettings = normalizeSettings();

  function reset() {
    generation += 1;
    attempted = false;
    loading = false;
    saving = false;
    settingsIssue = null;
    chatsIssue = null;
    saveIssue = null;
    notice = '';
    apps = [];
    chats = [];
    settings = normalizeSettings();
    savedSettings = normalizeSettings();
  }

  function selectedVisibleChatIDs() {
    const available = new Set(chats.map(chat => String(chat.chat_id || '').trim()).filter(Boolean));
    return settings.chat_ids.filter(id => available.has(id));
  }

  function chatsUnchangedAndKnown() {
    return savedSettings.chat_ids.length > 0
      && settings.chat_ids.length === savedSettings.chat_ids.length
      && settings.chat_ids.every(id => savedSettings.chat_ids.includes(id));
  }

  function footerState() {
    const appAvailable = apps.some(app => String(app.id || '').trim() === settings.app_profile_id);
    const reuseSavedChats = !!chatsIssue && chatsUnchangedAndKnown();
    const selectedChats = reuseSavedChats ? savedSettings.chat_ids : selectedVisibleChatIDs();
    const dirty = !sameSettings(settings, savedSettings);
    const chatsBlockSave = !!chatsIssue && !reuseSavedChats;
    let message = notice;
    if (saving) message = '正在保存发布链路…';
    else if (loading) message = chats.length || apps.length ? '正在更新发布链路…' : '正在读取发布链路…';
    else if (settingsIssue?.kind === 'network' && chatsIssue?.kind === 'network') message = '发布服务不可达；恢复服务后重新读取';
    else if (settingsIssue) message = '推送应用读取失败，暂不能保存';
    else if (chatsBlockSave) message = '审核群读取失败，暂不能改群';
    else if (!apps.length) message = '需要先在服务器配置推送应用';
    else if (!chats.length && !reuseSavedChats) message = '当前没有可选择的审核群';
    else if (!appAvailable) message = '请选择推送应用';
    else if (!selectedChats.length) message = '至少选择一个审核群';
    else if (saveIssue) message = saveIssue.title;
    else if (dirty && reuseSavedChats) message = '群列表暂时读不到，保存后仍用已保存的审核群';
    else if (dirty) message = '有未保存的发布链路更改';
    else message = message || '当前发布链路已保存';
    return {
      dirty,
      disabled: loading || saving || !!settingsIssue || chatsBlockSave || !appAvailable || !selectedChats.length || !dirty,
      label: saving ? '保存中…' : '保存发布链路',
      message,
    };
  }

  function renderAppStep(globalNetworkIssue) {
    let content = '';
    if (globalNetworkIssue) {
      content = '<div class="admin-delivery-empty"><strong>等待发布服务恢复</strong><p>连接恢复并重新读取后，这里会显示可用的推送应用。</p></div>';
    } else if (loading && !apps.length) {
      content = '<div class="admin-delivery-empty" role="status"><strong>正在读取应用配置…</strong><p>读取完成后会显示可用于发送审核卡的应用。</p></div>';
    } else if (settingsIssue && !globalNetworkIssue) {
      content = renderIssue(settingsIssue);
    } else if (!apps.length) {
      content = '<div class="admin-delivery-empty"><strong>尚未配置推送应用</strong><p>请先在服务器配置至少一个推送应用，完成后重新读取。</p></div>';
    } else {
      const hasSelectedApp = apps.some(app => String(app.id || '').trim() === settings.app_profile_id);
      const options = `${hasSelectedApp ? '' : '<option value="" selected>请选择推送应用</option>'}${apps.map(app => {
        const id = String(app.id || '').trim();
        return `<option value="${esc(id)}" ${id === settings.app_profile_id ? 'selected' : ''}>${esc(app.name || id)}</option>`;
      }).join('')}`;
      content = `<label class="admin-delivery-control">
        <span>选择应用</span>
        <select data-delivery-app ${loading || saving || settingsIssue ? 'disabled' : ''}>${options}</select>
      </label>`;
    }
    return `<li class="admin-delivery-step">
      <span class="admin-delivery-step-index" aria-hidden="true">1</span>
      <div class="admin-delivery-step-content">
        <div class="admin-delivery-step-heading">
          <div><h4>推送应用</h4><p>选择负责发送审核卡的飞书应用；应用凭证只保存在服务器。</p></div>
        </div>
        ${content}
      </div>
    </li>`;
  }

  function renderChatStep(globalNetworkIssue) {
    const selected = new Set(settings.chat_ids);
    let content = '';
    if (globalNetworkIssue) {
      content = '<div class="admin-delivery-empty"><strong>等待发布服务恢复</strong><p>连接恢复并重新读取后，这里会显示当前飞书账号可加入的群。</p></div>';
    } else if (loading && !chats.length) {
      content = '<div class="admin-delivery-empty" role="status"><strong>正在读取审核群…</strong><p>只会列出当前飞书账号可以访问的群。</p></div>';
    } else if (chatsIssue) {
      content = renderIssue(chatsIssue);
    } else if (!chats.length) {
      content = '<div class="admin-delivery-empty"><strong>没有可选择的审核群</strong><p>请确认当前飞书账号已加入目标群，并已授予群列表读取权限。</p></div>';
    } else {
      const rows = chats.map(chat => {
        const id = String(chat.chat_id || '').trim();
        const name = String(chat.name || id || '未命名群').trim();
        return `<label class="admin-delivery-chat">
          <input type="checkbox" data-delivery-chat="${esc(id)}" ${selected.has(id) ? 'checked' : ''} ${loading || saving ? 'disabled' : ''}>
          <span class="admin-delivery-chat-copy"><span>${esc(name)}</span>${chat.external ? '<small>外部群</small>' : ''}</span>
        </label>`;
      }).join('');
      content = `<fieldset class="admin-delivery-chat-list"><legend class="admin-sr-only">审核群（可多选）</legend>${rows}</fieldset>`;
    }
    const refresh = attempted && !loading && !settingsIssue && !chatsIssue
      ? '<button type="button" class="admin-secondary admin-delivery-refresh" data-delivery-action="refresh">更新群列表</button>'
      : '';
    return `<li class="admin-delivery-step">
      <span class="admin-delivery-step-index" aria-hidden="true">2</span>
      <div class="admin-delivery-step-content">
        <div class="admin-delivery-step-heading">
          <div><h4>审核群</h4><p>可多选；新任务生成的审核卡会同时发送到这些群。</p></div>
          ${refresh}
        </div>
        ${content}
      </div>
    </li>`;
  }

  function renderAutoPublishStep() {
    const on = settings.auto_publish === true;
    const disabled = loading || saving || !!settingsIssue;
    return `<li class="admin-delivery-step">
      <span class="admin-delivery-step-index" aria-hidden="true">3</span>
      <div class="admin-delivery-step-content">
        <div class="admin-delivery-step-heading">
          <div>
            <h4>自动发布</h4>
            <p>打开后，新同步过来的文章会直接上线，不必再点卡片通过。群里仍发卡片，也可以从卡片下线。已经在待审核的稿不受影响。</p>
          </div>
          <button type="button" class="toggle-switch admin-toggle${on ? ' on' : ''}" data-delivery-auto-publish role="switch" aria-checked="${on ? 'true' : 'false'}" aria-label="同步后自动发布"${disabled ? ' disabled' : ''}>
            <span class="toggle-knob"></span>
          </button>
        </div>
      </div>
    </li>`;
  }

  function render() {
    const globalNetworkIssue = settingsIssue?.kind === 'network' && chatsIssue?.kind === 'network'
      ? settingsIssue
      : null;
    return `<section class="admin-delivery-view" aria-labelledby="admin-delivery-title">
      <div class="admin-section-heading admin-delivery-heading">
        <h3 id="admin-delivery-title">发布链路</h3>
        <p class="admin-panel-note">配置新任务的审核卡发送目标和是否自动上线。已经生成的任务仍使用创建时保存的应用和群，不会被这里的修改覆盖。</p>
      </div>
      ${globalNetworkIssue ? renderIssue(globalNetworkIssue) : ''}
      <ol class="admin-delivery-steps">
        ${renderAppStep(globalNetworkIssue)}
        ${renderChatStep(globalNetworkIssue)}
        ${renderAutoPublishStep()}
      </ol>
      ${saveIssue ? renderIssue(saveIssue, { retry: false }) : ''}
    </section>`;
  }

  async function readEndpoint(path, fallback) {
    try {
      const response = await request(path, { cache: 'no-store' });
      if (!response.ok) throw await deliveryResponseIssue(response, fallback);
      return await response.json();
    } catch (error) {
      throw deliveryFailureIssue(error, fallback);
    }
  }

  async function load() {
    if (loading || saving) return;
    const token = ++generation;
    attempted = true;
    loading = true;
    settingsIssue = null;
    chatsIssue = null;
    saveIssue = null;
    notice = '';
    rerender?.();
    const [deliveryResult, chatsResult] = await Promise.allSettled([
      readEndpoint('/api/publish/delivery-settings', '读取推送应用失败'),
      readEndpoint('/api/feishu/chats', '读取飞书群列表失败'),
    ]);
    if (token !== generation) return;
    if (deliveryResult.status === 'fulfilled') {
      const delivery = deliveryResult.value || {};
      apps = Array.isArray(delivery.apps) ? delivery.apps : [];
      settings = normalizeSettings(delivery.settings);
      savedSettings = normalizeSettings(delivery.settings);
    } else {
      settingsIssue = deliveryFailureIssue(deliveryResult.reason, '读取推送应用失败');
    }
    if (chatsResult.status === 'fulfilled') {
      const chatPayload = chatsResult.value || {};
      chats = Array.isArray(chatPayload.chats) ? chatPayload.chats : [];
    } else {
      chatsIssue = deliveryFailureIssue(chatsResult.reason, '读取飞书群列表失败');
    }
    loading = false;
    rerender?.();
  }

  async function save() {
    const state = footerState();
    if (state.disabled) return;
    const token = ++generation;
    saving = true;
    saveIssue = null;
    notice = '';
    onMessage?.('正在保存发布链路…');
    rerender?.();
    try {
      const response = await request('/api/publish/delivery-settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(settings),
      });
      if (token !== generation) return;
      if (!response.ok) throw await deliveryResponseIssue(response, '保存发布链路失败');
      const payload = await response.json();
      settings = normalizeSettings(payload.settings || settings);
      savedSettings = normalizeSettings(settings);
      notice = '发布链路已保存；新任务会按当前应用、审核群和发布方式执行';
      onMessage?.(notice);
    } catch (error) {
      if (token !== generation) return;
      saveIssue = deliveryFailureIssue(error, '保存发布链路失败');
      onMessage?.(saveIssue.title);
    } finally {
      if (token !== generation) return;
      saving = false;
      rerender?.();
    }
  }

  function bind() {
    const overlay = getOverlay?.();
    if (!overlay) return;
    overlay.querySelectorAll('[data-delivery-action="refresh"]').forEach(button => button.addEventListener('click', load));
    overlay.querySelector('[data-delivery-action="save"]')?.addEventListener('click', save);
    overlay.querySelector('[data-delivery-app]')?.addEventListener('change', event => {
      settings = normalizeSettings({ ...settings, app_profile_id: event.target.value });
      notice = '';
      saveIssue = null;
      rerender?.();
    });
    overlay.querySelectorAll('[data-delivery-chat]').forEach(input => input.addEventListener('change', () => {
      const selected = [...overlay.querySelectorAll('[data-delivery-chat]:checked')]
        .map(item => item.dataset.deliveryChat)
        .filter(Boolean);
      settings = normalizeSettings({ ...settings, chat_ids: selected });
      notice = '';
      saveIssue = null;
      rerender?.();
    }));
    overlay.querySelector('[data-delivery-auto-publish]')?.addEventListener('click', event => {
      if (event.currentTarget.disabled) return;
      settings = normalizeSettings({ ...settings, auto_publish: !settings.auto_publish });
      notice = '';
      saveIssue = null;
      rerender?.();
    });
    if (!attempted) load();
  }

  return { render, bind, reset, footerState };
}
