// distribute-page.js — 外发平台改稿页（飞书侧边栏打开）。
//
// 2026-10-12 用户裁定：卡片里改不了的（上传封面）做一个页面，从审核卡链接在飞书侧边栏打开，
// 改完状态同步回卡片。每个平台一节：状态；还能改的给标题、摘要、封面，字数实时计数，超了标红，
// 有建议就给「用这个」；「保存并同步」交给服务端同一个修改入口（与卡片表单共用规则）。
// 上传封面只是多一张可选的图，点保存才推送。
import { authFetch, ensureAuthenticatedSession } from './auth-session.js';

const TONES = new Set(['green', 'blue', 'orange', 'red', 'grey']);
const POLL_MS = 4000;
const POLL_LIMIT = 45; // 约 3 分钟后不再自动刷新

function esc(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// charCount 与服务端同口径：按字符（rune）数，合并空白。
export function charCount(value) {
  return Array.from(String(value || '').replace(/\s+/g, ' ').trim()).length;
}

export function counterState(value, max, min = 0) {
  const length = charCount(value);
  if (max > 0 && length > max) return { length, over: length - max, text: `超出 ${length - max} 字`, invalid: true };
  if (min > 0 && length > 0 && length < min) return { length, over: 0, text: `至少 ${min} 字`, invalid: true };
  if (length === 0) return { length, over: 0, text: '不能为空', invalid: true };
  return { length, over: 0, text: '', invalid: false };
}

function statusMarkup(platform) {
  const tone = TONES.has(platform.tone) ? platform.tone : 'grey';
  return `<span class="dist-status is-${tone}">${esc(platform.status)}</span>`;
}

function coverChoices(platform, selected) {
  const choices = [];
  const seen = new Set();
  if (platform.cover?.url) {
    choices.push({ ...platform.cover, current: true });
    seen.add(platform.cover.url);
  }
  for (const candidate of platform.candidates || []) {
    if (!seen.has(candidate.url)) {
      seen.add(candidate.url);
      choices.push(candidate);
    }
  }
  if (selected && !seen.has(selected.url)) choices.push(selected);
  return choices;
}

function coverTooSmall(platform, image) {
  return !!(image.width && image.height && (image.width < platform.cover_min_width || image.height < platform.cover_min_height));
}

function sameTitle(a, b) {
  return String(a || '').replace(/\s+/g, '') === String(b || '').replace(/\s+/g, '');
}

// renderPlatform 一个平台一节。articleTitle 是本文标题：平台上的标题与它相同就不再重复写。
export function renderPlatform(platform, draft = {}, articleTitle = '') {
  const head = `<div class="dist-platform-head"><h2>${esc(platform.name)}</h2>${statusMarkup(platform)}</div>`;
  if (!platform.editable) {
    const ownTitle = platform.title && !sameTitle(platform.title, articleTitle) ? platform.title : '';
    const lines = [];
    if (platform.url) lines.push(`<a href="${esc(platform.url)}" target="_blank" rel="noopener">${esc(ownTitle || '查看文章')}</a>`);
    else if (ownTitle) lines.push(`<span>${esc(ownTitle)}</span>`);
    if (platform.reason) lines.push(`<span>${esc(platform.reason)}</span>`);
    return `<section class="dist-platform" data-slot="${platform.slot}">${head}${lines.length ? `<p class="dist-meta">${lines.join('')}</p>` : ''}</section>`;
  }
  // 能改但不是待修改（没推过、受理前失败）：失败原因写在名字下面。
  const reasonLine = platform.reason && !platform.needs_fix ? `<p class="dist-meta"><span>${esc(platform.reason)}</span></p>` : '';
  const slot = platform.slot;
  const title = draft.title ?? platform.title;
  const titleState = counterState(title, platform.title_max, platform.title_min);
  const titleSuggest = platform.title_suggested && platform.title_suggested !== title
    ? `<div class="dist-suggest"><span class="dist-suggest-text">建议：${esc(platform.title_suggested)}</span><button type="button" class="dist-link" data-use="title">用这个</button></div>`
    : '';
  let summaryField = '';
  if (platform.summary_max > 0 || platform.summary_issue) {
    const summary = draft.summary ?? platform.summary;
    const summaryState = counterState(summary, platform.summary_max);
    const summarySuggest = platform.summary_suggested && platform.summary_suggested !== summary
      ? `<div class="dist-suggest"><span class="dist-suggest-text">建议：${esc(platform.summary_suggested)}</span><button type="button" class="dist-link" data-use="summary">用这个</button></div>`
      : '';
    summaryField = `<div class="dist-field">
      <div class="dist-field-label"><label for="summary-${slot}">摘要</label><span class="dist-count${summaryState.invalid ? ' is-over' : ''}" data-count="summary">${summaryState.length} / ${platform.summary_max}</span></div>
      <textarea id="summary-${slot}" name="summary" rows="3" aria-invalid="${summaryState.invalid}">${esc(summary)}</textarea>
      <p class="dist-hint" data-hint="summary"${summaryState.invalid ? '' : ' hidden'}>${esc(summaryState.text)}</p>
      ${summarySuggest}
    </div>`;
  }
  const selectedURL = draft.coverURL ?? platform.cover?.url ?? '';
  const choices = coverChoices(platform, draft.uploaded);
  let coverOK = !platform.cover_issue || selectedURL !== platform.cover?.url;
  const tiles = choices.map(image => {
    const small = coverTooSmall(platform, image);
    // 太小的图不能选，也不画成选中，免得看着像已经选好了。
    const selected = image.url === selectedURL && !small;
    if (image.url === selectedURL && small) coverOK = false;
    const size = image.width && image.height ? `${image.width}×${image.height}` : '尺寸未知';
    const label = image.current ? '现在' : image.uploaded ? '上传' : '';
    return `<button type="button" class="dist-cover${selected ? ' is-selected' : ''}${small ? ' is-small' : ''}" role="radio" aria-checked="${selected}" data-cover="${esc(image.url)}"${small ? ' disabled' : ''}>
      <img src="${esc(image.url)}" alt="" loading="lazy" referrerpolicy="no-referrer">
      <span class="dist-cover-size">${label ? `${label} · ` : ''}${esc(size)}</span>
    </button>`;
  }).join('');
  const coverHint = coverOK ? '' : `<p class="dist-hint">${platform.cover?.url ? '现在的封面太小' : '还没有封面'}，选一张大图或上传</p>`;
  const requirement = platform.cover_min_width ? `至少 ${platform.cover_min_width}×${platform.cover_min_height}` : '';
  return `<section class="dist-platform" data-slot="${slot}">
    ${head}${reasonLine}
    <form class="dist-form" novalidate>
      <div class="dist-field">
        <div class="dist-field-label"><label for="title-${slot}">标题</label><span class="dist-count${titleState.invalid ? ' is-over' : ''}" data-count="title">${titleState.length} / ${platform.title_max}</span></div>
        <textarea id="title-${slot}" name="title" rows="2" aria-invalid="${titleState.invalid}">${esc(title)}</textarea>
        <p class="dist-hint" data-hint="title"${titleState.invalid ? '' : ' hidden'}>${esc(titleState.text)}</p>
        ${titleSuggest}
      </div>
      ${summaryField}
      <div class="dist-field">
        <div class="dist-field-label"><span id="cover-label-${slot}">封面</span><span class="dist-count${coverOK ? '' : ' is-over'}">${esc(requirement)}</span></div>
        <div class="dist-covers" role="radiogroup" aria-labelledby="cover-label-${slot}">
          ${tiles}
          <label class="dist-cover dist-cover-upload">
            <input type="file" accept="image/jpeg,image/png" data-upload>
            <span>上传图片</span>
          </label>
        </div>
        ${coverHint}
      </div>
      <button type="submit" class="dist-primary">保存并同步至${esc(platform.name)}</button>
      <p class="dist-result" role="status"${draft.result ? '' : ' hidden'}>${esc(draft.result || '')}</p>
    </form>
  </section>`;
}

function readQuery(search) {
  const params = new URLSearchParams(search);
  return { job: String(params.get('job') || '').trim(), token: String(params.get('t') || '').trim() };
}

export function createDistributePage({ root, articleEl, search, request = authFetch, schedule = setTimeout } = {}) {
  const { job, token } = readQuery(search);
  const base = `/api/publish/jobs/${encodeURIComponent(job)}/external-distribution/`;
  const query = token ? `?t=${encodeURIComponent(token)}` : '';
  let view = null;
  let polls = 0;
  const drafts = new Map(); // slot → { title, summary, coverURL, uploaded, result }

  function draftFor(slot) {
    if (!drafts.has(slot)) drafts.set(slot, {});
    return drafts.get(slot);
  }

  function showState(message) {
    root.innerHTML = `<p class="dist-state" role="status">${esc(message)}</p>`;
  }

  function render() {
    if (!view) return;
    if (articleEl) {
      const title = `#${view.job?.id ?? job} ${view.job?.title || ''}`.trim();
      articleEl.innerHTML = view.job?.site_url
        ? `<a href="${esc(view.job.site_url)}" target="_blank" rel="noopener">${esc(title)}</a>`
        : esc(title);
    }
    const platforms = Array.isArray(view.platforms) ? view.platforms : [];
    root.innerHTML = platforms.length
      ? platforms.map(platform => renderPlatform(platform, draftFor(platform.slot), view.job?.title)).join('')
      : '<p class="dist-state">没有配置分发平台</p>';
    bind();
  }

  async function errorText(response) {
    const payload = await response.json().catch(() => ({}));
    if (response.status === 403) return '请从审核卡里的链接打开';
    if (response.status === 404) return '文章不存在';
    return payload.error || `服务返回 ${response.status}，请稍后再试`;
  }

  function maybePoll() {
    const busy = (view?.platforms || []).some(platform => platform.status === '同步中' || platform.status === '排队中');
    if (!busy || polls >= POLL_LIMIT) return;
    polls += 1;
    schedule(() => load({ quiet: true }), POLL_MS);
  }

  async function load({ quiet = false } = {}) {
    if (!job) {
      showState('链接不完整，请从审核卡里重新打开');
      return;
    }
    if (!quiet) showState('正在读取…');
    try {
      const response = await request(`${base}draft${query}`, { cache: 'no-store' });
      if (!response.ok) {
        if (!quiet) showState(await errorText(response));
        return;
      }
      view = await response.json();
      render();
      maybePoll();
    } catch {
      if (!quiet) showState('连不上服务，请稍后刷新');
    }
  }

  function sectionOf(slot) {
    return root.querySelector(`.dist-platform[data-slot="${slot}"]`);
  }

  function refreshCounter(section, platform, field) {
    const input = section.querySelector(`[name="${field}"]`);
    const max = field === 'title' ? platform.title_max : platform.summary_max;
    const min = field === 'title' ? platform.title_min : 0;
    const state = counterState(input.value, max, min);
    const count = section.querySelector(`[data-count="${field}"]`);
    count.textContent = `${state.length} / ${max}`;
    count.classList.toggle('is-over', state.invalid);
    input.setAttribute('aria-invalid', String(state.invalid));
    const hint = section.querySelector(`[data-hint="${field}"]`);
    hint.textContent = state.text;
    hint.hidden = !state.invalid;
    return state;
  }

  async function save(platform, section) {
    const draft = draftFor(platform.slot);
    const titleState = refreshCounter(section, platform, 'title');
    const summaryInput = section.querySelector('[name="summary"]');
    const summaryState = summaryInput ? refreshCounter(section, platform, 'summary') : { invalid: false };
    const result = section.querySelector('.dist-result');
    if (titleState.invalid || summaryState.invalid) {
      section.querySelector('[aria-invalid="true"]')?.focus();
      return;
    }
    const body = { slot: platform.slot, title: section.querySelector('[name="title"]').value };
    if (summaryInput) body.summary = summaryInput.value;
    if (draft.coverURL && draft.coverURL !== platform.cover?.url) body.cover_url = draft.coverURL;
    const button = section.querySelector('.dist-primary');
    button.disabled = true;
    button.textContent = '正在保存…';
    try {
      const response = await request(`${base}draft${query}`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
      });
      if (!response.ok) {
        result.textContent = await errorText(response);
        result.hidden = false;
        result.classList.add('is-error');
        button.disabled = false;
        button.textContent = `保存并同步至${platform.name}`;
        return;
      }
      view = await response.json();
      drafts.delete(platform.slot);
      const next = (view.platforms || []).find(item => item.slot === platform.slot);
      draftFor(platform.slot).result = next?.needs_fix
        ? `已保存，还要改：${next.reason || ''}`
        : next?.editable ? '已保存，正在排队同步' : `已保存，${next?.status || '正在同步'}`;
      polls = 0;
      render();
      maybePoll();
    } catch {
      result.textContent = '连不上服务，没有保存，请稍后再试';
      result.hidden = false;
      result.classList.add('is-error');
      button.disabled = false;
      button.textContent = `保存并同步至${platform.name}`;
    }
  }

  async function upload(platform, section, file) {
    const draft = draftFor(platform.slot);
    draft.title = section.querySelector('[name="title"]').value;
    const summaryInput = section.querySelector('[name="summary"]');
    if (summaryInput) draft.summary = summaryInput.value;
    const tile = section.querySelector('.dist-cover-upload span');
    tile.textContent = '正在上传…';
    const form = new FormData();
    form.append('slot', String(platform.slot));
    form.append('file', file);
    try {
      const response = await request(`${base}cover${query}`, { method: 'POST', body: form });
      if (!response.ok) {
        draft.result = await errorText(response);
        render();
        sectionOf(platform.slot)?.querySelector('.dist-result')?.classList.add('is-error');
        return;
      }
      const payload = await response.json();
      view = payload;
      draft.uploaded = { ...payload.uploaded, uploaded: true };
      draft.coverURL = payload.uploaded?.url;
      draft.result = '';
      render();
    } catch {
      draft.result = '连不上服务，图片没有上传';
      render();
    }
  }

  function bind() {
    for (const platform of view.platforms || []) {
      if (!platform.editable) continue;
      const section = sectionOf(platform.slot);
      if (!section) continue;
      const draft = draftFor(platform.slot);
      for (const field of ['title', 'summary']) {
        const input = section.querySelector(`[name="${field}"]`);
        input?.addEventListener('input', () => {
          draft[field] = input.value;
          refreshCounter(section, platform, field);
        });
      }
      section.querySelectorAll('[data-use]').forEach(button => button.addEventListener('click', () => {
        const field = button.dataset.use;
        draft[field] = field === 'title' ? platform.title_suggested : platform.summary_suggested;
        render();
        sectionOf(platform.slot)?.querySelector(`[name="${field}"]`)?.focus();
      }));
      section.querySelectorAll('[data-cover]').forEach(button => button.addEventListener('click', () => {
        draft.title = section.querySelector('[name="title"]').value;
        const summaryInput = section.querySelector('[name="summary"]');
        if (summaryInput) draft.summary = summaryInput.value;
        draft.coverURL = button.dataset.cover;
        render();
      }));
      section.querySelector('[data-upload]')?.addEventListener('change', event => {
        const file = event.target.files?.[0];
        if (file) upload(platform, section, file);
      });
      section.querySelector('.dist-form')?.addEventListener('submit', event => {
        event.preventDefault();
        save(platform, section);
      });
    }
  }

  return { load, render, state: () => ({ view, drafts }) };
}

async function boot() {
  const root = document.getElementById('distBody');
  if (!root) return;
  const user = await ensureAuthenticatedSession().catch(() => null);
  if (!user) return;
  createDistributePage({ root, articleEl: document.getElementById('distArticle'), search: location.search }).load();
}

if (typeof window !== 'undefined' && typeof document !== 'undefined' && document.getElementById?.('distBody')) {
  boot();
}
