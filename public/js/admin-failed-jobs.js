// admin-failed-jobs.js — 管理后台「失败任务」：列出停在 failed 的发布任务，点重试或放弃。
//
// 只调现成的 /api/publish/jobs/{id}/retry 与 /abandon；能不能重试、能不能放弃由服务端判定
// （retryable / abandonable），这里只照着显示按钮。
import { authFetch } from './auth-session.js';

function esc(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

const STAGE_LABELS = {
  created: '建单',
  fetching: '抓原文',
  rendering: '排版',
  mirroring: '搬图',
  enriching: '预填摘要标签',
  assessing: '风险检查',
  assessing_review: '风险检查',
  staging: '存进 FoodTalks',
  publicizing: '公开发布',
  unpublishing: '下线',
  republishing: '重新发布',
};

export function failedStageLabel(stage) {
  return `${STAGE_LABELS[stage] || stage || '处理'}失败`;
}

function formatTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const parts = Object.fromEntries(new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  }).formatToParts(date).map(part => [part.type, part.value]));
  return `${parts.month}-${parts.day} ${parts.hour}:${parts.minute}`;
}

async function responseError(response, fallback) {
  const payload = await response.json().catch(() => ({}));
  if (response.status === 401) return '登录状态已失效，请重新登录';
  if (response.status === 403) return '当前账号没有发布管理权限';
  return payload.error || `${fallback}（HTTP ${response.status}）`;
}

export function createAdminFailedJobsController({
  getOverlay, rerender, onMessage, request = authFetch, confirm = text => window.confirm(text),
} = {}) {
  let generation = 0;
  let attempted = false;
  let loading = false;
  let loadError = '';
  let jobs = [];
  let pendingID = 0;

  function reset() {
    generation += 1;
    attempted = false;
    loading = false;
    loadError = '';
    jobs = [];
    pendingID = 0;
  }

  function renderJob(job) {
    const busy = pendingID === job.id;
    const title = job.title || job.source_url || '（还没抓到标题）';
    const titleHTML = job.source_url
      ? `<a href="${esc(job.source_url)}" target="_blank" rel="noopener noreferrer">${esc(title)}</a>`
      : esc(title);
    const meta = [failedStageLabel(job.failed_stage), formatTime(job.created_at) && `${formatTime(job.created_at)} 进来`]
      .filter(Boolean).join(' · ');
    const disabled = pendingID !== 0 ? ' disabled' : '';
    return `<li class="admin-failed-job" data-failed-job="${job.id}">
      <div class="admin-failed-job-copy">
        <strong><span class="admin-failed-job-id">#${job.id}</span>${titleHTML}</strong>
        <span class="admin-failed-job-meta">${esc(meta)}</span>
        ${job.error ? `<p class="admin-failed-job-error" title="${esc(job.error)}">${esc(job.error)}</p>` : ''}
      </div>
      <div class="admin-failed-job-actions">
        ${job.abandonable ? `<button type="button" class="admin-secondary danger" data-failed-action="abandon" data-job-id="${job.id}"${disabled}>放弃</button>` : ''}
        ${job.retryable ? `<button type="button" class="admin-primary" data-failed-action="retry" data-job-id="${job.id}"${disabled}>${busy ? '处理中…' : '重试'}</button>` : ''}
      </div>
    </li>`;
  }

  function render() {
    let content;
    if (loading && !jobs.length) {
      content = '<div class="admin-delivery-empty" role="status"><strong>正在读取失败任务…</strong></div>';
    } else if (loadError) {
      content = `<div class="admin-delivery-issue" role="alert">
        <div class="admin-delivery-issue-copy"><strong>读取失败任务失败</strong><p>${esc(loadError)}</p></div>
        <button type="button" class="admin-secondary" data-failed-action="refresh">重新读取</button>
      </div>`;
    } else if (!jobs.length) {
      content = '<div class="admin-delivery-empty"><strong>没有失败的任务</strong></div>';
    } else {
      content = `<ul class="admin-failed-job-list">${jobs.map(renderJob).join('')}</ul>`;
    }
    return `<section class="admin-delivery-view" aria-labelledby="admin-failed-jobs-title">
      <div class="admin-section-heading admin-delivery-heading">
        <h3 id="admin-failed-jobs-title">失败任务${jobs.length ? ` · ${jobs.length}` : ''}</h3>
      </div>
      ${content}
    </section>`;
  }

  async function load() {
    const token = ++generation;
    attempted = true;
    loading = true;
    loadError = '';
    rerender?.();
    try {
      const response = await request('/api/publish/failed-jobs', { cache: 'no-store' });
      if (token !== generation) return;
      if (!response.ok) throw new Error(await responseError(response, '读取失败任务失败'));
      const payload = await response.json();
      if (token !== generation) return;
      jobs = Array.isArray(payload.jobs) ? payload.jobs : [];
    } catch (error) {
      if (token !== generation) return;
      loadError = String(error?.message || error || '服务暂时没有返回结果');
    }
    loading = false;
    rerender?.();
  }

  async function act(action, id) {
    const job = jobs.find(item => item.id === id);
    if (!job || pendingID) return;
    if (action === 'abandon' && !confirm(`确定放弃 #${id}「${job.title || job.source_url || ''}」？放弃后这篇不再处理，也不会发布。`)) return;
    const token = generation;
    pendingID = id;
    rerender?.();
    let message;
    try {
      const response = await request(`/api/publish/jobs/${id}/${action}`, {
        method: 'POST',
        ...(action === 'abandon'
          ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ reason: '在管理后台放弃' }) }
          : {}),
      });
      if (!response.ok) throw new Error(await responseError(response, action === 'retry' ? '重试失败' : '放弃失败'));
      message = action === 'retry' ? `#${id} 已重新开始处理` : `#${id} 已放弃`;
    } catch (error) {
      message = `#${id} ${action === 'retry' ? '重试' : '放弃'}没成功：${error?.message || error}`;
    }
    if (token !== generation) return;
    pendingID = 0;
    onMessage?.(message);
    await load();
    onMessage?.(message);
  }

  function bind() {
    const overlay = getOverlay?.();
    if (!overlay) return;
    overlay.querySelectorAll('[data-failed-action]').forEach(button => button.addEventListener('click', () => {
      const action = button.dataset.failedAction;
      if (action === 'refresh') load();
      else act(action, Number(button.dataset.jobId));
    }));
    if (!attempted) load();
  }

  return { render, bind, reset };
}
