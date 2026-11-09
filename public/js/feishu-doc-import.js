import { authFetch, feishuGrantURL } from './auth-session.js';
import {
  bindImportProgressDom,
  createImportProgressTracker,
} from './import-progress.js';
import { stripBuildVersionSearch } from './version-refresh.js';

const DOCX_MIME = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
const FEISHU_DOCUMENT_HOST_SUFFIXES = ['feishu.cn', 'larkoffice.com', 'larksuite.com'];

// These are workflow work units, not a time estimate. Each phase advances only
// from a real server/client done÷total value; weights reflect the amount of
// independently verifiable work in that phase (bytes, ZIP entries, images, or
// parser/commit gates). Unknown totals deliberately hold still.
const SERVER_EXPORT_PHASES = [
  { id: 'resolve', weight: 1 },
  { id: 'create', weight: 1 },
  { id: 'export_wait', weight: 1 },
  { id: 'download', weight: 4 },
  { id: 'optimize', weight: 4 },
  { id: 'image_enumerate', weight: 2 },
  { id: 'image_upload', weight: 6 },
  { id: 'image_map', weight: 3 },
  { id: 'finalize', weight: 1 },
];
const SERVER_EXPORT_PHASE_IDS = SERVER_EXPORT_PHASES.map(phase => phase.id);
const FEISHU_IMPORT_PHASES = [
  ...SERVER_EXPORT_PHASES,
  { id: 'result-download', weight: 4 },
  { id: 'rules', weight: 1 },
  { id: 'parse', weight: 6 },
  { id: 'render', weight: 4 },
  { id: 'session', weight: 4 },
  { id: 'materialize', weight: 6 },
  { id: 'decode', weight: 6 },
  { id: 'refresh', weight: 3 },
  { id: 'copy-transfer', weight: 6 },
  { id: 'theme', weight: 1 },
  { id: 'preview-commit', weight: 2 },
  { id: 'copy-commit', weight: 3 },
];

const SERVER_PHASE_STATUS = {
  resolve: '正在确认飞书文档…',
  create: '正在创建 Word 导出任务…',
  export_wait: '飞书正在生成 Word…',
  download: '正在从飞书接收 Word…',
  optimize: '正在优化 Word 文档…',
  image_enumerate: '正在核对正文图片…',
  image_upload: '正在准备正文图片…',
  image_map: '正在写入图片索引…',
  finalize: 'Word 已生成，正在交给排版引擎…',
};

export function createFeishuImportProgressTracker({
  documentRef = globalThis.document,
} = {}) {
  const root = documentRef?.getElementById('progress');
  const render = bindImportProgressDom({
    root,
    fill: documentRef?.getElementById('progressFill'),
    text: documentRef?.getElementById('progressText'),
    percent: documentRef?.getElementById('progressPercent'),
  });
  return createImportProgressTracker({
    phases: FEISHU_IMPORT_PHASES,
    onChange: render,
  });
}

function safeExportJobPath(value, fallback) {
  const path = String(value || '').trim();
  if (/^\/api\/feishu\/docs\/export\/jobs\/[A-Za-z0-9_-]+(?:\/result)?$/.test(path)) {
    return path;
  }
  return fallback;
}

export function parseFeishuMention(value) {
  const normalized = String(value || '').trimStart();
  if (!normalized.startsWith('@')) return null;
  return normalized.slice(1).trim();
}

function isOfficialFeishuDocumentHost(hostname) {
  const normalized = String(hostname || '').toLowerCase().replace(/\.$/, '');
  return FEISHU_DOCUMENT_HOST_SUFFIXES.some(suffix => (
    normalized === suffix || normalized.endsWith(`.${suffix}`)
  ));
}

export function parseFeishuDocumentUrl(value) {
  const raw = String(value || '').trim();
  if (!raw || /\s/.test(raw)) return null;
  let parsed;
  try {
    parsed = new URL(raw);
  } catch {
    return null;
  }
  if (
    parsed.protocol !== 'https:'
    || parsed.username
    || parsed.password
    || parsed.port
    || !isOfficialFeishuDocumentHost(parsed.hostname)
  ) return null;
  const segments = parsed.pathname.split('/').filter(Boolean);
  if (segments.length !== 2) return null;
  const pathKind = String(segments[0] || '').toLowerCase();
  const type = pathKind === 'docs' ? 'doc' : pathKind;
  const token = String(segments[1] || '');
  if (
    !['doc', 'docx', 'wiki'].includes(type)
    || !/^[A-Za-z0-9_-]{4,128}$/.test(token)
  ) return null;
  return { token, type, title: '飞书文档', url: parsed.href };
}

function responseErrorMessage(payload, fallback) {
  if (payload?.code === 'feishu_login_required') return '飞书云文档功能需要用 FBIF 的飞书账号登录';
  if (payload?.code === 'feishu_grant_required') return '要先授权飞书';
  if (payload?.code === 'authentication_required') return '登录状态已失效，请重新登录';
  if (payload?.code === 'login_service_unavailable') return '登录服务暂时连不上，请稍后重试';
  if (payload?.code === 'feishu_export_timeout') return '飞书生成 Word 超时，请稍后重试';
  if (payload?.code === 'feishu_export_too_large') return '文档或图片过多，飞书无法导出，请精简后重试';
  if (payload?.code === 'feishu_document_permission_denied') return '当前账号没有导出这篇文档的权限';
  if (payload?.code === 'feishu_document_not_found') return '文档已删除或不存在';
  return payload?.error || fallback;
}

// 抛出的错误带上服务端 code，界面据此区分「飞书授权失效」等需要用户动作的情况。
function responseError(payload, fallback) {
  const error = new Error(responseErrorMessage(payload, fallback));
  if (payload?.code) error.code = String(payload.code);
  return error;
}

async function readError(response, fallback) {
  const payload = await response.json().catch(() => null);
  return responseError(payload, fallback);
}

function filenameFromDisposition(header, fallbackTitle) {
  const value = String(header || '');
  const encoded = value.match(/filename\*=UTF-8''([^;]+)/i);
  if (encoded) {
    try {
      return decodeURIComponent(encoded[1].trim());
    } catch {
      // Fall through to the safe title.
    }
  }
  const plain = value.match(/filename="?([^";]+)"?/i);
  if (plain && plain[1] && plain[1] !== 'feishu-document.docx') return plain[1].trim();
  const title = String(fallbackTitle || '飞书文档').trim().replace(/[\r\n/\\]/g, ' ');
  return `${title || '飞书文档'}.docx`;
}

function formatModifiedAt(raw) {
  if (!raw) return '';
  const value = new Date(raw);
  if (Number.isNaN(value.getTime())) return '';
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(value);
}

function isFeishuCapable(user) {
  return user?.auth_mode === 'feishu' && user?.capabilities?.feishu_documents === true;
}

export async function readResponseBlobWithProgress(response, {
  BlobCtor = globalThis.Blob,
  onProgress = () => {},
} = {}) {
  const total = Number(response?.headers?.get?.('Content-Length')) || 0;
  const reader = response?.body?.getReader?.();
  if (!reader || typeof BlobCtor !== 'function') {
    const blob = await response.blob();
    onProgress({ loaded: blob.size, total: total || blob.size });
    return blob;
  }

  const chunks = [];
  let loaded = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (!value?.byteLength) continue;
    chunks.push(value);
    loaded += value.byteLength;
    onProgress({ loaded, total });
  }
  return new BlobCtor(chunks, {
    type: response.headers.get('Content-Type') || DOCX_MIME,
  });
}

export function mountFeishuDocumentImport({
  documentRef = globalThis.document,
  windowRef = globalThis.window,
  user,
  fetchImpl,
  FileCtor = globalThis.File,
  BlobCtor = globalThis.Blob,
  AbortControllerCtor = globalThis.AbortController,
  createProgressTracker,
  getHandleFile = () => windowRef?._handleFile,
  getAccountID = () => '',
  onArticleIntent = () => {
    if (windowRef?.fetchArticle?.invalidate) windowRef.fetchArticle.invalidate();
    if (windowRef?._engineApi?.invalidatePendingWork) windowRef._engineApi.invalidatePendingWork();
  },
  debounceMs = 240,
} = {}) {
  const input = documentRef?.getElementById('urlInput');
  const panel = documentRef?.getElementById('feishuDocSearch');
  const feishuTrigger = documentRef?.getElementById('feishuDocTrigger');
  const capability = documentRef?.getElementById('feishuDocCapability');
  const dropZone = documentRef?.getElementById('dropZone');
  const selection = documentRef?.getElementById('feishuDocSelection');
  const selectionTitle = documentRef?.getElementById('feishuDocSelectionTitle');
  const selectionStatus = documentRef?.getElementById('feishuDocSelectionStatus');
  const selectionCancel = documentRef?.getElementById('feishuDocSelectionCancel');
  const progress = documentRef?.getElementById('progress');
  const progressFill = documentRef?.getElementById('progressFill');
  const progressText = documentRef?.getElementById('progressText');
  if (!input || !panel) return { invalidate() {}, destroy() {} };

  input.setAttribute('aria-autocomplete', 'list');
  input.setAttribute('aria-controls', panel.id || 'feishuDocSearch');
  input.setAttribute('aria-expanded', 'false');

  const capable = isFeishuCapable(user);
  let generation = 0;
  let timer = null;
  let documents = [];
  let activeIndex = -1;
  let destroyed = false;
  let activeExportController = null;
  let activeProgressTracker = null;
  let activeExportJobURL = '';

  if (capability) {
    capability.textContent = capable ? '粘贴后自动排版' : '飞书文档需飞书登录';
    capability.classList.toggle('is-locked', !capable);
  }
  feishuTrigger?.classList.toggle('is-restricted', !capable);
  if (!capable && feishuTrigger) feishuTrigger.title = '需要使用飞书登录';

  const doFetch = fetchImpl
    ? (url, options) => fetchImpl(url, options)
    : (url, options) => authFetch(url, options);

  function stale(ticket, accountID) {
    return destroyed || ticket !== generation || String(getAccountID() || '') !== accountID;
  }

  function setImportProgress({ text, percent = null, indeterminate = false, visible = true } = {}) {
    if (!progress) return;
    progress.style.display = visible ? 'block' : 'none';
    progress.classList.toggle('is-indeterminate', visible && indeterminate);
    if (progressText && text) progressText.textContent = text;
    if (progressFill) {
      if (Number.isFinite(percent)) {
        progressFill.style.width = `${Math.max(0, Math.min(100, percent))}%`;
      } else if (indeterminate) {
        progressFill.style.width = '34%';
      }
    }
  }

  function stopImportProgress() {
    if (!progress) return;
    progress.classList.remove('is-indeterminate');
    progress.style.display = 'none';
    if (progressFill) progressFill.style.width = '0%';
  }

  function showPanel() {
    panel.hidden = false;
    dropZone?.classList.add('has-feishu-search');
    input.setAttribute('aria-expanded', 'true');
    feishuTrigger?.setAttribute('aria-expanded', 'true');
  }

  function hidePanel() {
    panel.hidden = true;
    dropZone?.classList.remove('has-feishu-search');
    input.setAttribute('aria-expanded', 'false');
    feishuTrigger?.setAttribute('aria-expanded', 'false');
    panel.replaceChildren();
    documents = [];
    activeIndex = -1;
  }

  function showSelectedDocument(document, status = '正在从飞书生成 Word…') {
    if (!selection) return;
    if (selectionTitle) selectionTitle.textContent = document?.title || '飞书文档';
    if (selectionStatus) selectionStatus.textContent = status;
    selection.hidden = false;
    dropZone?.classList.add('is-feishu-selected');
  }

  function updateSelectedDocumentStatus(status) {
    if (selectionStatus && status) selectionStatus.textContent = status;
  }

  function hideSelectedDocument() {
    if (selection) selection.hidden = true;
    dropZone?.classList.remove('is-feishu-selected');
  }

  function appendHighlightedText(node, value, query) {
    const text = String(value || '');
    const needle = String(query || '').trim();
    if (!needle) {
      node.textContent = text;
      return;
    }
    const normalizedText = text.toLocaleLowerCase('zh-CN');
    const normalizedNeedle = needle.toLocaleLowerCase('zh-CN');
    let cursor = 0;
    for (;;) {
      const index = normalizedText.indexOf(normalizedNeedle, cursor);
      if (index < 0) break;
      if (index > cursor) node.append(documentRef.createTextNode(text.slice(cursor, index)));
      const mark = documentRef.createElement('mark');
      mark.textContent = text.slice(index, index + needle.length);
      node.append(mark);
      cursor = index + needle.length;
    }
    if (cursor < text.length) node.append(documentRef.createTextNode(text.slice(cursor)));
  }

  function renderMessage(title, detail = '', state = '') {
    panel.replaceChildren();
    const box = documentRef.createElement('div');
    box.className = `feishu-doc-message${state ? ` is-${state}` : ''}`;
    const heading = documentRef.createElement('div');
    heading.className = 'feishu-doc-message-title';
    heading.textContent = title;
    box.append(heading);
    if (detail) {
      const copy = documentRef.createElement('div');
      copy.className = 'feishu-doc-message-detail';
      copy.textContent = detail;
      box.append(copy);
    }
    panel.append(box);
    showPanel();
  }

  function renderLoginPrompt(titleText, detailText, linkText, href) {
    panel.replaceChildren();
    const box = documentRef.createElement('div');
    box.className = 'feishu-doc-locked';
    const copy = documentRef.createElement('div');
    copy.className = 'feishu-doc-locked-copy';
    const title = documentRef.createElement('strong');
    title.textContent = titleText;
    const detail = documentRef.createElement('span');
    detail.textContent = detailText;
    copy.append(title, detail);
    const link = documentRef.createElement('a');
    link.href = href;
    link.textContent = linkText;
    box.append(copy, link);
    panel.append(box);
    showPanel();
  }

  function currentReturnTo() {
    const location = windowRef?.location;
    const stripped = stripBuildVersionSearch(location?.search || '');
    return `${location?.pathname || '/mp/fbif'}${stripped.search}`;
  }

  // 不是用 FBIF 飞书登录的（账号密码、别家企业）：改用飞书登录，登录中心认完回到这页。
  function renderLocked(action = '搜索') {
    renderLoginPrompt(
      `${action}飞书文档需要用 FBIF 的飞书账号登录`,
      '仍可上传文件、粘贴正文和文章链接。',
      '改用飞书登录',
      `/auth/feishu/fbif/login?next=${encodeURIComponent(currentReturnTo())}`,
    );
  }

  // 还没授权飞书，或授权被撤销、知识库权限不够：去授权一次，之后服务端自动续期。
  function renderGrant(error) {
    const wiki = error?.code === 'feishu_wiki_access_denied';
    renderLoginPrompt(
      wiki ? '飞书拒绝访问这篇知识库文档' : '要先授权飞书',
      wiki ? error.message : '授权一次，之后自动续期。',
      wiki ? '重新授权飞书…' : '授权飞书…',
      feishuGrantURL(windowRef),
    );
  }

  function needsGrant(error) {
    return error?.code === 'feishu_grant_required' || error?.code === 'feishu_wiki_access_denied';
  }

  function updateActiveResult(nextIndex) {
    const results = Array.from(panel.querySelectorAll('[data-feishu-doc-result]'));
    if (!results.length) {
      activeIndex = -1;
      return;
    }
    activeIndex = Math.max(0, Math.min(nextIndex, results.length - 1));
    results.forEach((result, index) => {
      result.classList.toggle('is-active', index === activeIndex);
      result.setAttribute('aria-selected', String(index === activeIndex));
    });
    results[activeIndex]?.scrollIntoView?.({ block: 'nearest' });
  }

  // 服务端每推进一个阶段就会提高 job 的 version，前端据此把真实 done/total
  // 折算进加权总进度。未知分母的阶段故意不动，不编造百分比。
  function updateServerProgress(tracker, status) {
    if (!tracker || !status) return;
    const phase = String(status.phase || '').trim();
    const phaseIndex = SERVER_EXPORT_PHASE_IDS.indexOf(phase);
    if (phaseIndex >= 0) {
      for (let index = 0; index < phaseIndex; index++) {
        tracker.completePhase(SERVER_EXPORT_PHASE_IDS[index]);
      }
      tracker.updatePhase(phase, status.done, status.total, {
        status: SERVER_PHASE_STATUS[phase] || '正在生成 Word…',
      });
    } else if (phase && phase !== 'queued') {
      // Tests and future API versions can expose a finer-grained phase. A
      // custom tracker may know it; the production tracker keeps the same
      // honest export_wait denominator when it does not.
      try {
        tracker.updatePhase(phase, status.done, status.total, {
          status: '飞书正在生成 Word…',
        });
      } catch {
        tracker.updatePhase('export_wait', status.done, status.total, {
          status: '飞书正在生成 Word…',
        });
      }
    } else {
      tracker.updatePhase('resolve', 0, 0, { status: '任务已创建，等待处理…' });
    }
    if (status.state === 'completed') {
      for (const id of SERVER_EXPORT_PHASE_IDS) {
        tracker.completePhase(id, {
          status: id === 'finalize' ? SERVER_PHASE_STATUS.finalize : undefined,
        });
      }
    }
  }

  async function cancelExportJob(jobURL) {
    if (!jobURL) return;
    try {
      await doFetch(jobURL, { method: 'DELETE' });
    } catch {
      // Best effort: the request AbortController already prevents stale UI
      // commits, while the server independently expires abandoned jobs.
    }
  }

  // 异步导出作业：创建后用 long-poll(?after=version&wait_ms) 等服务端推进，
  // 有新进度立刻返回，不是定时轮询——所以进度实时且不额外等待。
  async function startExportJob(document, controller, tracker, ticket, accountID) {
    const created = await doFetch('/api/feishu/docs/export/jobs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      signal: controller?.signal,
      body: JSON.stringify({
        token: document.token,
        type: document.type,
        title: document.title,
        url: document.url || undefined,
      }),
    });
    if (!created.ok) throw await readError(created, '飞书文档导入失败');
    const initial = await created.json();
    if (stale(ticket, accountID)) return null;
    const jobID = String(initial.job_id || '');
    if (!/^[A-Za-z0-9_-]+$/.test(jobID)) throw new Error('飞书导出任务信息无效，请重试');
    const fallbackStatusURL = `/api/feishu/docs/export/jobs/${encodeURIComponent(jobID)}`;
    const statusURL = safeExportJobPath(initial.status_url, fallbackStatusURL);
    const resultURL = safeExportJobPath(initial.result_url, `${statusURL}/result`);
    activeExportJobURL = statusURL;
    let status = initial;
    for (;;) {
      if (stale(ticket, accountID)) return null;
      updateServerProgress(tracker, status);
      if (status.state === 'completed') break;
      if (status.state === 'failed' || status.state === 'canceled') {
        throw responseError(status, '飞书文档导入失败');
      }
      const after = Math.max(0, Number(status.version) || 0);
      const response = await doFetch(`${statusURL}?after=${after}&wait_ms=25000`, {
        method: 'GET',
        signal: controller?.signal,
      });
      if (!response.ok) throw await readError(response, '读取导入进度失败');
      status = await response.json();
    }
    if (stale(ticket, accountID)) return null;
    const result = await doFetch(safeExportJobPath(status.result_url, resultURL), {
      method: 'GET',
      signal: controller?.signal,
    });
    if (!result.ok) throw await readError(result, '获取 Word 文档失败');
    return result;
  }

  async function chooseDocument(document, ticket = ++generation) {
    const accountID = String(getAccountID() || '');
    if (activeExportController) activeExportController.abort();
    if (activeProgressTracker) activeProgressTracker.cancel('superseded');
    if (activeExportJobURL) void cancelExportJob(activeExportJobURL);
    const controller = typeof AbortControllerCtor === 'function' ? new AbortControllerCtor() : null;
    activeExportController = controller;
    const tracker = typeof createProgressTracker === 'function' ? createProgressTracker() : null;
    activeProgressTracker = tracker;
    activeExportJobURL = '';
    let completed = false;
    const waitStartedAt = Date.now();
    const setIntervalFn = windowRef?.setInterval?.bind(windowRef) || globalThis.setInterval;
    const clearIntervalFn = windowRef?.clearInterval?.bind(windowRef) || globalThis.clearInterval;
    let waitTimer = null;
    onArticleIntent({ source: 'feishu-doc-import', document });
    hidePanel();
    showSelectedDocument(document);
    // 有 tracker 时全程走真实加权进度；没有（降级/测试）才退回不确定动画。
    if (tracker) tracker.updatePhase('resolve', 0, 0, { status: '正在创建飞书导入任务…' });
    else {
      setImportProgress({
        text: '飞书正在生成 Word…',
        indeterminate: true,
      });
      if (typeof setIntervalFn === 'function') {
        waitTimer = setIntervalFn(() => {
          if (stale(ticket, accountID)) return;
          const elapsedSeconds = Math.max(1, Math.round((Date.now() - waitStartedAt) / 1000));
          setImportProgress({
            text: `飞书正在生成 Word，已等待 ${elapsedSeconds} 秒…`,
            indeterminate: true,
          });
        }, 1000);
      }
    }
    try {
      const response = tracker
        ? await startExportJob(document, controller, tracker, ticket, accountID)
        : await doFetch('/api/feishu/docs/export', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          signal: controller?.signal,
          body: JSON.stringify({
            token: document.token,
            type: document.type,
            title: document.title,
            url: document.url || undefined,
          }),
        });
      if (!response) return false;
      if (stale(ticket, accountID)) return false;
      if (!response.ok) {
        throw await readError(response, '飞书文档导入失败');
      }
      if (waitTimer && typeof clearIntervalFn === 'function') {
        clearIntervalFn(waitTimer);
        waitTimer = null;
      }
      updateSelectedDocumentStatus('Word 已生成，正在接收正文…');
      if (!tracker) {
        setImportProgress({
          text: 'Word 已生成，正在接收优化后的正文…',
          percent: 15,
        });
      }
      const blob = await readResponseBlobWithProgress(response, {
        BlobCtor,
        onProgress({ loaded, total }) {
          if (stale(ticket, accountID)) return;
          if (tracker) {
            tracker.updatePhase('result-download', loaded, total, {
              status: total > 0
                ? `正在接收 Word ${Math.round((loaded / total) * 100)}%`
                : `正在接收 Word ${Math.max(1, Math.round(loaded / 1024))} KB`,
            });
          } else if (total > 0) {
            const ratio = Math.min(1, loaded / total);
            const downloadPercent = Math.round(ratio * 100);
            setImportProgress({
              text: `正在接收文档 ${downloadPercent}%`,
              percent: 15 + Math.round(ratio * 65),
            });
          } else {
            setImportProgress({
              text: `正在接收文档 ${Math.max(1, Math.round(loaded / 1024))} KB`,
              indeterminate: true,
            });
          }
        },
      });
      if (stale(ticket, accountID)) return false;
      if (tracker) tracker.completePhase('result-download', { status: 'Word 已接收，正在解析…' });
      updateSelectedDocumentStatus('Word 已生成，正在排版…');
      const filename = filenameFromDisposition(response.headers.get('Content-Disposition'), document.title);
      const file = new FileCtor([blob], filename, { type: DOCX_MIME });
      const handleFile = getHandleFile();
      if (typeof handleFile !== 'function') throw new Error('排版引擎尚未就绪，请稍后重试');
      if (stale(ticket, accountID)) return false;
      hidePanel();
      if (!tracker) {
        setImportProgress({
          text: '正在提取正文并排版…',
          percent: 82,
        });
      }
      const rendered = await handleFile(file, {
        isRepost: false,
        source: 'feishu-doc-import',
        importProgress: tracker,
        activationDelayMs: 0,
        signal: controller?.signal,
        isStale: () => stale(ticket, accountID),
        sourceDocument: {
          token: document.token,
          type: document.type,
          title: document.title,
          url: document.url || '',
        },
      });
      if (stale(ticket, accountID)) return false;
      if (rendered !== true) {
        throw new Error('文档解析或排版失败，请检查文档后重试');
      }
      input.value = '';
      hideSelectedDocument();
      completed = true;
      return true;
    } catch (error) {
      if (stale(ticket, accountID)) return false;
      tracker?.cancel('failed');
      stopImportProgress();
      hideSelectedDocument();
      if (needsGrant(error)) renderGrant(error);
      else renderMessage('导入失败', error?.message || '飞书文档导入失败', 'error');
      return false;
    } finally {
      if (waitTimer && typeof clearIntervalFn === 'function') clearIntervalFn(waitTimer);
      if (activeExportController === controller) activeExportController = null;
      if (activeProgressTracker === tracker && completed) activeProgressTracker = null;
      if (completed) activeExportJobURL = '';
    }
  }

  function renderResults(results, query, ticket) {
    panel.replaceChildren();
    const heading = documentRef.createElement('div');
    heading.className = 'feishu-doc-search-head';
    heading.textContent = `${results.length} 个结果`;
    panel.append(heading);
    if (!results.length) {
      renderMessage('没有找到对应文档', '请换一个文档名称或关键词。', 'empty');
      return;
    }
    const list = documentRef.createElement('div');
    list.className = 'feishu-doc-results';
    list.setAttribute('role', 'listbox');
    results.forEach((document, index) => {
      const button = documentRef.createElement('button');
      button.type = 'button';
      button.className = 'feishu-doc-result';
      button.dataset.feishuDocResult = '';
      button.setAttribute('role', 'option');
      button.setAttribute('aria-selected', 'false');
      const icon = documentRef.createElement('span');
      icon.className = 'feishu-doc-result-icon';
      icon.setAttribute('aria-hidden', 'true');
      icon.textContent = '文';
      const copy = documentRef.createElement('span');
      copy.className = 'feishu-doc-result-copy';
      const title = documentRef.createElement('span');
      title.className = 'feishu-doc-result-title';
      appendHighlightedText(title, document.title || '未命名文档', query);
      const meta = documentRef.createElement('span');
      meta.className = 'feishu-doc-result-meta';
      meta.textContent = [document.owner_name, formatModifiedAt(document.modified_at)].filter(Boolean).join(' · ') || '飞书云文档';
      copy.append(title, meta);
      button.append(icon, copy);
      button.addEventListener('mouseenter', () => updateActiveResult(index));
      button.addEventListener('click', () => chooseDocument(document, ++generation));
      list.append(button);
    });
    panel.append(list);
    showPanel();
    updateActiveResult(Math.min(activeIndex < 0 ? 0 : activeIndex, results.length - 1));
  }

  async function search(query, ticket) {
    const accountID = String(getAccountID() || '');
    renderMessage('正在搜索飞书文档…', query, 'loading');
    try {
      const response = await doFetch(`/api/feishu/docs/search?q=${encodeURIComponent(query)}&limit=8`, {
        method: 'GET',
      });
      if (stale(ticket, accountID)) return;
      if (!response.ok) {
        throw await readError(response, '飞书文档搜索失败');
      }
      const payload = await response.json();
      if (stale(ticket, accountID)) return;
      documents = Array.isArray(payload.documents) ? payload.documents : [];
      renderResults(documents, query, ticket);
    } catch (error) {
      if (stale(ticket, accountID)) return;
      if (needsGrant(error)) renderGrant(error);
      else renderMessage('搜索失败', error?.message || '飞书文档搜索失败', 'error');
    }
  }

  function invalidate() {
    generation++;
    hideSelectedDocument();
    if (activeProgressTracker) {
      activeProgressTracker.cancel('superseded');
      activeProgressTracker = null;
    }
    // cancel() emits the tracker's last (<100) snapshot, so hide only after
    // that emission; otherwise a canceled import can make the bar reappear.
    stopImportProgress();
    if (activeExportJobURL) {
      void cancelExportJob(activeExportJobURL);
      activeExportJobURL = '';
    }
    if (activeExportController) {
      activeExportController.abort();
      activeExportController = null;
    }
    if (timer) {
      windowRef.clearTimeout(timer);
      timer = null;
    }
  }

  function importValue(value) {
    const document = parseFeishuDocumentUrl(value);
    if (!document) return false;
    invalidate();
    if (!capable) {
      renderLocked('导入');
      return true;
    }
    void chooseDocument(document, ++generation);
    return true;
  }

  function onInput() {
    invalidate();
    const query = parseFeishuMention(input.value);
    if (query === null) {
      hidePanel();
      return;
    }
    if (!capable) {
      renderLocked();
      return;
    }
    if (!query) {
      renderMessage('搜索飞书云文档', '继续输入文档名称或关键词。', 'empty');
      return;
    }
    const ticket = generation;
    timer = windowRef.setTimeout(() => {
      timer = null;
      search(query, ticket);
    }, debounceMs);
  }

  function openFeishuSearch() {
    input.focus();
    const currentValue = String(input.value || '');
    if (!currentValue.trim()) {
      input.value = '@';
      input.dispatchEvent(new windowRef.Event('input', { bubbles: true }));
      return;
    }
    if (parseFeishuMention(currentValue) !== null) {
      onInput();
      return;
    }
    renderMessage('搜索飞书云文档', '请先清空当前输入内容，再选择飞书文档。', 'empty');
  }

  function onKeydown(event) {
    if (panel.hidden || parseFeishuMention(input.value) === null) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopImmediatePropagation();
      hidePanel();
      return;
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      event.stopImmediatePropagation();
      const step = event.key === 'ArrowDown' ? 1 : -1;
      updateActiveResult(activeIndex < 0 ? 0 : activeIndex + step);
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      event.stopImmediatePropagation();
      if (activeIndex >= 0 && documents[activeIndex]) {
        chooseDocument(documents[activeIndex], ++generation);
      }
    }
  }

  function onExternalArticleInput(event) {
    if (event?.detail?.origin === 'feishu-doc-import') return;
    invalidate();
    hidePanel();
  }

  function onDocumentClick(event) {
    if (panel.hidden) return;
    const target = event?.target;
    const path = typeof event?.composedPath === 'function' ? event.composedPath() : [];
    if (target === input || panel.contains(target) || path.includes(panel) || feishuTrigger?.contains(target)) return;
    invalidate();
    hidePanel();
  }

  function onSelectionCancel(event) {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    invalidate();
    input.focus();
    if (parseFeishuMention(input.value) !== null) onInput();
  }

  input.addEventListener('input', onInput);
  input.addEventListener('keydown', onKeydown, true);
  feishuTrigger?.addEventListener('click', openFeishuSearch);
  selectionCancel?.addEventListener('click', onSelectionCancel);
  documentRef.addEventListener('click', onDocumentClick);
  windowRef.addEventListener?.('fbif:article-input-started', onExternalArticleInput);

  return {
    importValue,
    invalidate,
    destroy() {
      destroyed = true;
      invalidate();
      input.removeEventListener('input', onInput);
      input.removeEventListener('keydown', onKeydown, true);
      feishuTrigger?.removeEventListener('click', openFeishuSearch);
      selectionCancel?.removeEventListener('click', onSelectionCancel);
      documentRef.removeEventListener('click', onDocumentClick);
      windowRef.removeEventListener?.('fbif:article-input-started', onExternalArticleInput);
      hidePanel();
      hideSelectedDocument();
    },
  };
}
