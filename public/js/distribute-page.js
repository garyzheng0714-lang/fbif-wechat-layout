// distribute-page.js — 外发平台改稿页（飞书侧边栏打开）。
//
// 2026-10-12 用户裁定：卡片里改不了的（上传封面）做一个页面，从审核卡链接在飞书侧边栏打开，
// 改完状态同步回卡片。每个平台一节：状态；还能改的给标题、摘要、封面，字数实时计数，超了标红，
// 有建议就给「用这个」；「保存并同步」交给服务端同一个修改入口（与卡片表单共用规则）。
// 上传封面：点选、拖入或粘贴图片都行，传上去就换成这个平台的封面，页面里已改好的标题、摘要
// 一起提交，问题都清了就同步（同日用户：「传上去之后直接绑定、自动同步过去了」「我可以粘贴、
// 上传文件或者拖动文件进去」）。
import { authFetch, ensureAuthenticatedSession } from './auth-session.js';

const TONES = new Set(['green', 'blue', 'orange', 'red', 'grey']);
const UPLOAD_TYPES = new Set(['image/jpeg', 'image/png']);
const POLL_MS = 4000;
const POLL_LIMIT = 45; // 约 3 分钟后不再自动刷新

function esc(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// charCount 与服务端同口径：合并空白后按字符（rune）数；halfWidth（分发服务给的标题数法是 ascii_half，
// 服务端经 title_half_width 带来）时英文字母、数字等半角字符两个算一个字（api/distribution_platforms.go titleLength）。
export function charCount(value, halfWidth = false) {
  const chars = Array.from(String(value || '').replace(/\s+/g, ' ').trim());
  if (!halfWidth) return chars.length;
  const ascii = chars.filter(ch => ch.codePointAt(0) < 0x80).length;
  return chars.length - ascii + Math.floor(ascii / 2);
}

export function counterState(value, max, min = 0, { halfWidth = false } = {}) {
  const chars = charCount(value);
  const length = charCount(value, halfWidth);
  if (max > 0 && length > max) return { length, over: length - max, text: `超出 ${length - max} 字`, invalid: true };
  if (min > 0 && chars > 0 && chars < min) return { length, over: 0, text: `至少 ${min} 字`, invalid: true };
  if (chars === 0) return { length, over: 0, text: '不能为空', invalid: true };
  return { length, over: 0, text: '', invalid: false };
}

function titleCounter(platform) {
  return { halfWidth: Boolean(platform.title_half_width) };
}

function statusMarkup(platform) {
  const tone = TONES.has(platform.tone) ? platform.tone : 'grey';
  return `<span class="dist-status is-${tone}">${esc(platform.status)}</span>`;
}

function coverChoices(platform) {
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
  return choices;
}

function coverTooSmall(platform, image) {
  return !!(image.width && image.height && (image.width < platform.cover_min_width || image.height < platform.cover_min_height));
}

// actionText 提交按钮「保存并……」：服务端给（存草稿至头条号）。
function actionText(platform) {
  return `保存并${platform.action || `存草稿至${platform.name}`}`;
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
    // 只存了草稿：去草稿编辑页看；已发布但还没拿到文章链接：去平台后台看发布情况。
    if (!platform.url && platform.draft_url) lines.push(`<a href="${esc(platform.draft_url)}" target="_blank" rel="noopener">去${esc(platform.name)}草稿箱查看</a>`);
    else if (!platform.url && platform.manage_url) lines.push(`<a href="${esc(platform.manage_url)}" target="_blank" rel="noopener">去${esc(platform.name)}后台查看</a>`);
    if (platform.reason) lines.push(`<span>${esc(platform.reason)}</span>`);
    return `<section class="dist-platform" data-account="${esc(platform.account)}">${head}${lines.length ? `<p class="dist-meta">${lines.join('')}</p>` : ''}</section>`;
  }
  // 能改但不是待修改（没推过、受理前失败）：失败原因写在名字下面。
  const reasonLine = platform.reason && !platform.needs_fix ? `<p class="dist-meta"><span>${esc(platform.reason)}</span></p>` : '';
  const key = esc(platform.account);
  const title = draft.title ?? platform.title;
  const titleState = counterState(title, platform.title_max, platform.title_min, titleCounter(platform));
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
      <div class="dist-field-label"><label for="summary-${key}">摘要</label><span class="dist-count${summaryState.invalid ? ' is-over' : ''}" data-count="summary">${summaryState.length} / ${platform.summary_max}</span></div>
      <textarea id="summary-${key}" name="summary" rows="3" aria-invalid="${summaryState.invalid}">${esc(summary)}</textarea>
      <p class="dist-hint" data-hint="summary"${summaryState.invalid ? '' : ' hidden'}>${esc(summaryState.text)}</p>
      ${summarySuggest}
    </div>`;
  }
  const selectedURL = draft.coverURL ?? platform.cover?.url ?? '';
  const choices = coverChoices(platform);
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
  // 上传失败的原因写在封面下面（图太小、格式不对、没传上去）。
  const coverHint = draft.coverError
    ? `<p class="dist-hint" role="alert">${esc(draft.coverError)}</p>`
    : coverOK ? '' : `<p class="dist-hint">${platform.cover?.url ? '现在的封面太小' : '还没有封面'}，选一张大图或上传</p>`;
  const requirement = platform.cover_min_width ? `至少 ${platform.cover_min_width}×${platform.cover_min_height}` : '';
  return `<section class="dist-platform" data-account="${key}">
    ${head}${reasonLine}
    <form class="dist-form" novalidate>
      <div class="dist-field">
        <div class="dist-field-label"><label for="title-${key}">标题</label><span class="dist-count${titleState.invalid ? ' is-over' : ''}" data-count="title">${titleState.length} / ${platform.title_max}</span></div>
        <textarea id="title-${key}" name="title" rows="2" aria-invalid="${titleState.invalid}">${esc(title)}</textarea>
        <p class="dist-hint" data-hint="title"${titleState.invalid ? '' : ' hidden'}>${esc(titleState.text)}</p>
        ${titleSuggest}
      </div>
      ${summaryField}
      <div class="dist-field">
        <div class="dist-field-label"><span id="cover-label-${key}">封面</span><span class="dist-count${coverOK ? '' : ' is-over'}">${esc(requirement)}</span></div>
        <div class="dist-covers" role="radiogroup" aria-labelledby="cover-label-${key}">
          ${tiles}
          <label class="dist-cover dist-cover-upload${draft.uploading ? ' is-busy' : ''}">
            <input type="file" accept="image/jpeg,image/png" data-upload${draft.uploading ? ' disabled' : ''}>
            <span class="dist-upload-label">${draft.uploading ? '正在上传…' : '上传图片'}</span>
            <span class="dist-upload-hint"${draft.uploading ? ' hidden' : ''}>可拖入或粘贴</span>
          </label>
        </div>
        ${coverHint}
      </div>
      <button type="submit" class="dist-primary">${esc(actionText(platform))}</button>
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
  const drafts = new Map(); // 账号编号 → { title, summary, coverURL, uploading, coverError, result }

  function draftFor(account) {
    if (!drafts.has(account)) drafts.set(account, {});
    return drafts.get(account);
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
      ? platforms.map(platform => renderPlatform(platform, draftFor(platform.account), view.job?.title)).join('')
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
    const busy = (view?.platforms || []).some(platform => ['存草稿中', '排队中'].includes(platform.status));
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

  function sectionOf(account) {
    return root.querySelector(`.dist-platform[data-account="${account}"]`);
  }

  function refreshCounter(section, platform, field) {
    const input = section.querySelector(`[name="${field}"]`);
    const max = field === 'title' ? platform.title_max : platform.summary_max;
    const min = field === 'title' ? platform.title_min : 0;
    const state = counterState(input.value, max, min, field === 'title' ? titleCounter(platform) : {});
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
    const draft = draftFor(platform.account);
    const titleState = refreshCounter(section, platform, 'title');
    const summaryInput = section.querySelector('[name="summary"]');
    const summaryState = summaryInput ? refreshCounter(section, platform, 'summary') : { invalid: false };
    const result = section.querySelector('.dist-result');
    if (titleState.invalid || summaryState.invalid) {
      section.querySelector('[aria-invalid="true"]')?.focus();
      return;
    }
    const body = { account: platform.account, title: section.querySelector('[name="title"]').value };
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
        button.textContent = actionText(platform);
        return;
      }
      view = await response.json();
      drafts.delete(platform.account);
      const next = (view.platforms || []).find(item => item.account === platform.account);
      draftFor(platform.account).result = next?.needs_fix
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
      button.textContent = actionText(platform);
    }
  }

  // typedText 页面里改过、且合乎要求的标题/摘要，随图一起提交；没改或不合要求的不带。
  function typedText(platform, section) {
    const out = {};
    for (const field of ['title', 'summary']) {
      const input = section.querySelector(`[name="${field}"]`);
      if (!input || input.value === (platform[field] ?? '')) continue;
      const max = field === 'title' ? platform.title_max : platform.summary_max;
      const min = field === 'title' ? platform.title_min : 0;
      if (!counterState(input.value, max, min, field === 'title' ? titleCounter(platform) : {}).invalid) out[field] = input.value;
    }
    return out;
  }

  async function upload(platform, file) {
    const section = sectionOf(platform.account);
    if (!section || !file) return;
    const draft = draftFor(platform.account);
    if (draft.uploading) return;
    draft.title = section.querySelector('[name="title"]').value;
    const summaryInput = section.querySelector('[name="summary"]');
    if (summaryInput) draft.summary = summaryInput.value;
    if (!UPLOAD_TYPES.has(file.type)) {
      draft.coverError = '只支持 JPG、PNG 图片';
      render();
      return;
    }
    const text = typedText(platform, section);
    const form = new FormData();
    form.append('account', platform.account);
    for (const [field, value] of Object.entries(text)) form.append(field, value);
    form.append('file', file);
    draft.uploading = true;
    draft.coverError = '';
    draft.result = '';
    render();
    try {
      const response = await request(`${base}cover${query}`, { method: 'POST', body: form });
      draft.uploading = false;
      if (!response.ok) {
        draft.coverError = await errorText(response);
        render();
        return;
      }
      view = await response.json();
      // 一起提交的字段以服务端为准；没带上的（还不合要求）留着人接着改。
      const next = (view.platforms || []).find(item => item.account === platform.account);
      const kept = {};
      if (!('title' in text) && draft.title !== next?.title) kept.title = draft.title;
      if (!('summary' in text) && draft.summary !== undefined && draft.summary !== next?.summary) kept.summary = draft.summary;
      drafts.set(platform.account, kept);
      kept.result = next?.needs_fix
        ? `封面已换上，还要改：${next.reason || ''}`
        : next?.editable ? '封面已换上，正在排队同步' : `封面已换上，${next?.status || '正在同步'}`;
      polls = 0;
      render();
      maybePoll();
    } catch {
      draft.uploading = false;
      draft.coverError = '连不上服务，图片没有上传';
      render();
    }
  }

  // 粘贴、拖入的图交给哪个平台：只有一个能改的就是它；否则是刚操作过的那个，再否则第一个缺封面的。
  let activeAccount = null;
  function uploadTarget(fromNode) {
    const editable = (view?.platforms || []).filter(platform => platform.editable);
    const section = fromNode?.closest?.('.dist-platform');
    const account = section ? section.dataset.account : activeAccount;
    return editable.find(platform => platform.account === account)
      || (editable.length === 1 ? editable[0] : editable.find(platform => platform.cover_issue))
      || null;
  }

  function imageFile(list) {
    return Array.from(list || []).find(file => String(file.type || '').startsWith('image/')) || null;
  }

  function hasFiles(event) {
    return Array.from(event.dataTransfer?.types || []).includes('Files');
  }

  function setDragging(account) {
    root.querySelectorAll('.dist-platform').forEach(section => {
      section.querySelector('.dist-cover-upload')?.classList.toggle('is-dragover', account != null && section.dataset.account === account);
    });
  }

  // 整页接住拖入的文件：落在哪个平台就传给哪个，不让浏览器把图片打开、离开这一页。
  const doc = root.ownerDocument;
  doc?.addEventListener('dragover', event => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    const target = uploadTarget(event.target);
    if (event.dataTransfer) event.dataTransfer.dropEffect = target ? 'copy' : 'none';
    setDragging(target ? target.account : null);
  });
  doc?.addEventListener('dragleave', event => {
    if (!event.relatedTarget) setDragging(null);
  });
  doc?.addEventListener('drop', event => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    setDragging(null);
    const target = uploadTarget(event.target);
    const file = imageFile(event.dataTransfer?.files);
    if (target && file) upload(target, file);
  });
  doc?.addEventListener('paste', event => {
    const file = imageFile(event.clipboardData?.files);
    if (!file) return; // 粘贴文字照常进输入框
    const target = uploadTarget(doc.activeElement);
    if (!target) return;
    event.preventDefault();
    upload(target, file);
  });

  function bind() {
    for (const platform of view.platforms || []) {
      if (!platform.editable) continue;
      const section = sectionOf(platform.account);
      if (!section) continue;
      const draft = draftFor(platform.account);
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
        sectionOf(platform.account)?.querySelector(`[name="${field}"]`)?.focus();
      }));
      section.querySelectorAll('[data-cover]').forEach(button => button.addEventListener('click', () => {
        draft.title = section.querySelector('[name="title"]').value;
        const summaryInput = section.querySelector('[name="summary"]');
        if (summaryInput) draft.summary = summaryInput.value;
        draft.coverURL = button.dataset.cover;
        draft.coverError = '';
        render();
      }));
      section.querySelector('[data-upload]')?.addEventListener('change', event => {
        const file = event.target.files?.[0];
        if (file) upload(platform, file);
      });
      section.addEventListener('focusin', () => { activeAccount = platform.account; });
      section.addEventListener('pointerdown', () => { activeAccount = platform.account; });
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
