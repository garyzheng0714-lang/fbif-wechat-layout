// editor-shell.js — shared shell helpers for channel editor pages.
// Keeps page scripts focused on channel-specific wiring while common URL import,
// rule-pack path resolution, and copy feedback live behind one interface.
import { fetchWithTimeout } from './fetch-timeout.js';

export const DEFAULT_ACCOUNT_STORAGE_KEY = 'active_rule_pack';

export function readStoredRulePackID(storage, key = DEFAULT_ACCOUNT_STORAGE_KEY) {
  try {
    return (storage && storage.getItem(key)) || '';
  } catch {
    return '';
  }
}

export function resolveInitialRulePackID({
  locationLike,
  storage,
  storageKey = DEFAULT_ACCOUNT_STORAGE_KEY,
  rulePackModule,
  rulePackUrlModule,
  useChannelDefaultPath = false,
}) {
  const loc = locationLike || globalThis.location || { pathname: '', search: '' };
  const parsed = rulePackUrlModule.parsePath(loc.pathname || '');
  if (parsed && parsed.packId) {
    return useChannelDefaultPath
      ? rulePackModule.getRulePackByPath(loc.pathname || '').id
      : parsed.packId;
  }
  if (parsed && parsed.channel && useChannelDefaultPath) {
    return rulePackModule.getRulePackByPath(loc.pathname || '').id;
  }
  const storedID = readStoredRulePackID(storage, storageKey);
  return rulePackModule.getRulePackIDFromLocation({
    search: loc.search || (storedID ? '?account=' + storedID : ''),
  });
}

function defaultFetchErrorMessage(err) {
  return '提取失败: ' + ((err && err.message) || '抓取失败');
}

export function foodTalksFetchErrorMessage(err) {
  const raw = String((err && err.message) || '');
  return /失败|重试|为空|限流/.test(raw) && !/HTTP|exec|PATH|undefined|\$/.test(raw)
    ? raw
    : '文章抓取失败，请稍后重试';
}

function isSingleHttpUrl(text) {
  return /^https?:\/\/\S+$/i.test(String(text || '').trim());
}

// 从 /api/fetch-article 的 blocks 提取网站【摘要】候选（飞书反馈问题七，2026-06-11）。
// 成品规律（news/62139 实测）：FoodTalks 摘要 = 原文开头的短导语段（橙宝文首段
// 「1500多家星级酒店的选择…」原文即摘要，且摘要句不在正文重复出现）。规则：跳过
// 头图/图注，取第一个非空文本段；短（≤ maxLen 字）才用作摘要，长段（如上海艾录文
// 90 字导语，62303 摘要为编辑手写）返回空串 → 摘要框保持占位由编辑自填。
// opts.consume=true 时命中段从 blocks 原地移除（对齐 62139：摘要句不进正文流）。
export function extractSiteSummary(blocks, opts = {}) {
  const maxLen = Number(opts.maxLen) > 0 ? Number(opts.maxLen) : 50;
  for (let i = 0; i < (blocks || []).length; i++) {
    const b = blocks[i];
    if (!b || !b.k) continue;
    if (b.k === 'img' || b.k === 'cap' || b.k === 'video') continue;
    if (b.k === 'h') return ''; // 已进入正文章节仍无导语 → 不自动填
    if (b.k !== 'txt') continue;
    const text = (Array.isArray(b.runs)
      ? b.runs.filter(r => r && (r.type === 'txt' || r.type === 'link')).map(r => r.text || '').join('')
      : (b.text || '')).trim();
    if (!text) continue;
    if (text.length > maxLen) return '';
    if (opts.consume) blocks.splice(i, 1);
    return text;
  }
  return '';
}

export function bindRailDivider({
  documentRef = globalThis.document,
  windowRef = globalThis.window,
  layoutSelector = '.app-layout',
  railID = 'rail',
  dividerID = 'railDivider',
  buttonID = 'railToggleBtn',
  minWidth = 220,
  maxWidth = 420,
  collapseThreshold = 180,
} = {}) {
  const layout = documentRef.querySelector(layoutSelector);
  const rail = documentRef.getElementById(railID);
  const divider = documentRef.getElementById(dividerID);
  const button = documentRef.getElementById(buttonID);
  if (!layout || !rail || !divider || !button || !windowRef) return;

  let railWidth = rail.getBoundingClientRect().width || 300;
  let suppressClick = false;

  function clampWidth(width) {
    return Math.max(minWidth, Math.min(maxWidth, width));
  }

  function setCollapsed(collapsed) {
    layout.classList.toggle('rail-collapsed', collapsed);
    button.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
    button.setAttribute('aria-label', collapsed ? '展开左栏' : '收起左栏');
    button.title = collapsed ? '展开左栏' : '收起左栏';
  }

  function setRailWidth(width) {
    railWidth = clampWidth(width);
    layout.style.setProperty('--rail-current-w', railWidth + 'px');
    setCollapsed(false);
  }

  button.addEventListener('click', () => {
    if (suppressClick) {
      suppressClick = false;
      return;
    }
    setCollapsed(!layout.classList.contains('rail-collapsed'));
  });

  divider.addEventListener('pointerdown', event => {
    if (windowRef.matchMedia('(max-width: 820px)').matches) return;
    event.preventDefault();
    const startedOnButton = button.contains(event.target);
    const pointerID = event.pointerId;
    const startX = event.clientX;
    const startWidth = layout.classList.contains('rail-collapsed')
      ? 0
      : rail.getBoundingClientRect().width;
    let moved = false;

    divider.setPointerCapture(pointerID);
    documentRef.body.classList.add('is-resizing-rail');

    function moveRail(moveEvent) {
      const delta = moveEvent.clientX - startX;
      const nextWidth = startWidth + delta;
      if (Math.abs(delta) > 3) moved = true;
      if (nextWidth < collapseThreshold) {
        setCollapsed(true);
      } else {
        setRailWidth(nextWidth);
      }
    }

    function stopDrag() {
      divider.releasePointerCapture(pointerID);
      documentRef.body.classList.remove('is-resizing-rail');
      divider.removeEventListener('pointermove', moveRail);
      divider.removeEventListener('pointerup', stopDrag);
      divider.removeEventListener('pointercancel', stopDrag);
      if (!moved && startedOnButton) {
        setCollapsed(!layout.classList.contains('rail-collapsed'));
        suppressClick = true;
        windowRef.setTimeout(() => { suppressClick = false; }, 120);
        return;
      }
      if (moved) {
        suppressClick = true;
        windowRef.setTimeout(() => { suppressClick = false; }, 120);
      }
    }

    divider.addEventListener('pointermove', moveRail);
    divider.addEventListener('pointerup', stopDrag);
    divider.addEventListener('pointercancel', stopDrag);
  });
}

export function createUrlImportHandler({
  documentRef = globalThis.document,
  fetchImpl = globalThis.fetch,
  FileCtor = globalThis.File,
  DataTransferCtor = globalThis.DataTransfer,
  getHandleBlocks = () => globalThis.window && globalThis.window._handleBlocks,
  getHandleFile = () => globalThis.window && globalThis.window._handleFile,
  getAccountID = () => '',
  onArticleUrl = () => {},
  onBlocks = () => {},
  importText = null,
  errorMessage = defaultFetchErrorMessage,
} = {}) {
  return async function fetchArticleFromUrl() {
    const urlInput = documentRef.getElementById('urlInput');
    const btn = documentRef.getElementById('urlFetchBtn');
    const errorEl = documentRef.getElementById('error');
    const importZone = documentRef.getElementById('importZone');
    const docZone = documentRef.getElementById('docZone');
    const progress = documentRef.getElementById('progress');
    const progressFill = documentRef.getElementById('progressFill');
    const progressText = documentRef.getElementById('progressText');
    const rawValue = (urlInput && urlInput.value || '').trim();
    if (!rawValue) return;
    if (!isSingleHttpUrl(rawValue)) {
      if (typeof importText === 'function') {
        importText(rawValue);
      }
      return;
    }
    const url = rawValue;
    let handedOffToRenderer = false;
    const btnLabel = btn ? (btn.textContent || '导入') : '';
    function showFetchProgress() {
      if (importZone) importZone.hidden = false;
      if (docZone) docZone.hidden = true;
      if (progress) progress.style.display = 'block';
      if (progressFill) progressFill.style.width = '30%';
      if (progressText) progressText.textContent = '正在提取...';
    }
    function hideFetchProgress() {
      if (progress) progress.style.display = 'none';
      if (progressFill) progressFill.style.width = '0%';
    }
    if (errorEl) {
      errorEl.textContent = '';
      errorEl.style.display = 'none';
    }
    showFetchProgress();
    if (btn) {
      btn.disabled = true;
      btn.textContent = '排版中...';
    }
    try {
      onArticleUrl(url);
      const resp = await fetchWithTimeout('/api/fetch-article', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url, account: getAccountID() || undefined }),
      }, { fetchImpl, timeoutMs: 30000, timeoutMessage: '抓取超时，请检查网络后重试' });
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: 'HTTP ' + resp.status }));
        throw new Error(err.error || '抓取失败');
      }
      const payload = await resp.json();
      const { title, blocks, content } = payload;
      if (Array.isArray(blocks) && blocks.length > 0) {
        const handleBlocks = getHandleBlocks();
        if (!handleBlocks) throw new Error('blocks 路径未就绪');
        handedOffToRenderer = true;
        try { onBlocks(blocks, { title, url }); } catch { /* 摘要等附加填充失败不阻断导入 */ }
        handleBlocks({ title, blocks, source: url }, { isRepost: true });
      } else if (content) {
        const file = new FileCtor([content], (title || 'article') + '.md', { type: 'text/markdown' });
        const handleFile = getHandleFile();
        if (handleFile) {
          handedOffToRenderer = true;
          handleFile(file, { isRepost: true });
        } else {
          const fi = documentRef.getElementById('fileInput');
          const dt = new DataTransferCtor();
          dt.items.add(file);
          fi.files = dt.files;
          handedOffToRenderer = true;
          fi.dispatchEvent(new Event('change'));
        }
      } else {
        throw new Error('文章内容为空');
      }
    } catch (err) {
      if (errorEl) {
        errorEl.textContent = errorMessage(err);
        errorEl.style.display = 'block';
      }
      hideFetchProgress();
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.textContent = btnLabel || '导入';
      }
      if (!handedOffToRenderer) hideFetchProgress();
    }
  };
}

export function bindUrlImportShortcuts({
  documentRef = globalThis.document,
  fetchArticle,
  importText = null,
  ignoreClassNames = [],
  focusTargetSelector = '',
} = {}) {
  const urlInput = documentRef.getElementById('urlInput');

  function submitImportValue(value) {
    const text = String(value || '').trim();
    if (!text) return false;
    if (isSingleHttpUrl(text)) {
      if (urlInput) urlInput.value = text;
      fetchArticle();
      return true;
    }
    if (typeof importText === 'function') {
      importText(text);
      return true;
    }
    return false;
  }

  if (focusTargetSelector && urlInput && typeof documentRef.querySelectorAll === 'function') {
    documentRef.querySelectorAll(focusTargetSelector).forEach(target => {
      target.addEventListener('click', event => {
        if (event.target === urlInput) return;
        urlInput.focus();
        if (urlInput.value && typeof urlInput.select === 'function') urlInput.select();
      });
    });
  }

  documentRef.addEventListener('keydown', event => {
    const preview = documentRef.getElementById('previewContent');
    const hasPreview = preview && preview.style.display !== 'none';
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter' && hasPreview) {
      event.preventDefault();
      if (globalThis.window && globalThis.window.copyContent) globalThis.window.copyContent();
    }
  });
  documentRef.addEventListener('paste', event => {
    if (!urlInput) return;
    const ae = documentRef.activeElement;
    if (ae === urlInput) return;
    if (ae && ae.classList && ignoreClassNames.some(name => ae.classList.contains(name))) return;
    const tag = ae && ae.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA') return;
    const text = (event.clipboardData || globalThis.window.clipboardData).getData('text').trim();
    if (text && submitImportValue(text)) {
      event.preventDefault();
    }
  });
  if (!urlInput) return;
  urlInput.addEventListener('paste', () => {
    setTimeout(() => {
      const value = (urlInput.value || '').trim();
      submitImportValue(value);
    }, 0);
  });
  urlInput.addEventListener('keydown', event => {
    const isMultiline = urlInput.tagName === 'TEXTAREA';
    if (event.key === 'Enter' && (!isMultiline || event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      submitImportValue(urlInput.value);
    }
  });
}

// 复制后的「去后台粘贴」引导（概念 A：复制键就地变身 + 旁边内联出现后台入口）。
// 两个后台是「粘贴目标入口」——应用基础设施地址、几乎不变，按用户 2026-06-02 裁定写死，
// 不进规则后台（区别于页脚/正则/颜色等排版规则数据）。
const PASTE_BACKENDS = {
  weixin: { label: '公众号后台', url: 'https://mp.weixin.qq.com/' },
  foodtalks: { label: '管理后台', url: 'https://admin-we.foodtalks.cn/#/wel/index' },
};

// 渠道 → [主目标, 次目标]。主目标 = 该渠道文章应粘贴去的后台（公众号渠道→公众号后台，
// 网站渠道→管理后台）；次目标作一条轻文字链一并给出，满足「两个地址都给到」。
export function pasteTargetsForChannel(channel) {
  return channel === 'ft'
    ? [PASTE_BACKENDS.foodtalks, PASTE_BACKENDS.weixin]
    : [PASTE_BACKENDS.weixin, PASTE_BACKENDS.foodtalks];
}

export function wrapCopyFeedback({
  windowRef = globalThis.window,
  documentRef = globalThis.document,
  buttonID = 'copyBtn',
  successID = 'copySuccess',
  resetText = '复制全文',
  hintID = 'copyPasteHint',
  pasteTargets = [],
  timeoutMs = 10000,
} = {}) {
  if (!windowRef || !windowRef.copyContent) return;

  // 复位：按钮回「复制全文」、收起后台入口、隐藏底部 pill。挂到 window 上，供新文章
  // 渲染(showPreview)与返回(goBack)调用——实现「状态保持到下一次动作才复位」。
  function resetCopyFeedback() {
    const btn = documentRef.getElementById(buttonID);
    if (btn) { btn.textContent = resetText; btn.classList.remove('copied'); }
    const hint = documentRef.getElementById(hintID);
    if (hint) hint.style.display = 'none';
    const success = documentRef.getElementById(successID);
    if (success) success.style.display = 'none';
  }
  windowRef._resetCopyFeedback = resetCopyFeedback;

  // 在复制键右侧内联渲染后台入口：主目标「去X后台 ↗」+ 次目标轻文字链。
  // 仅 target=_blank 的 <a>，只有用户手动点击才打开外部后台（守红线 #8）。
  function showPasteHint(btn) {
    if (!btn || !pasteTargets.length) return;
    let hint = documentRef.getElementById(hintID);
    if (!hint) {
      hint = documentRef.createElement('span');
      hint.id = hintID;
      hint.className = 'copy-paste-hint';
      btn.insertAdjacentElement('afterend', hint);
    }
    hint.title = '已复制，可在编辑器中粘贴 ⌘/Ctrl + V'; // 粘贴说明走最轻的 hover 提示
    hint.innerHTML = pasteTargets.map((t, i) =>
      `<a class="copy-paste-link${i === 0 ? ' is-primary' : ''}" href="${t.url}"`
      + ` target="_blank" rel="noopener">${i === 0 ? '去' + t.label + ' ↗' : t.label}</a>`
    ).join('<span class="copy-paste-sep">·</span>');
    hint.style.display = 'inline-flex';
  }

  const originalCopy = windowRef.copyContent;
  windowRef.copyContent = async function copyWithFeedback() {
    const btn = documentRef.getElementById(buttonID);
    const ok = await originalCopy();
    if (ok === false) return false;
    // 底部 pill 维持原有 10s 自动消失；按钮变身 + 后台入口则保持到下次动作。
    const success = documentRef.getElementById(successID);
    if (success) {
      // 有图片未上传成功时，底部提示走警告文案，别给「成功」假象（A 线 P0：消灭复制假成功）。
      const warnCount = windowRef._copyImageWarning || 0;
      if (!success.dataset.okText) success.dataset.okText = success.textContent || '已复制到剪贴板';
      success.textContent = warnCount
        ? `已复制，但 ${warnCount} 张图未上传成功，粘贴后可能缺失，请点「重试上传」`
        : success.dataset.okText;
      success.classList.toggle('copy-success--warn', !!warnCount);
      success.style.display = 'block';
      setTimeout(() => { if (success) success.style.display = 'none'; }, timeoutMs);
    }
    showPasteHint(btn);
    return ok;
  };
}

export function wrapBackFeedback({
  windowRef = globalThis.window,
  documentRef = globalThis.document,
  successID = 'copySuccess',
} = {}) {
  if (!windowRef || !windowRef.goBack) return;
  const originalGoBack = windowRef.goBack;
  windowRef.goBack = function goBackWithFeedbackReset() {
    originalGoBack();
    if (typeof windowRef._resetCopyFeedback === 'function') {
      windowRef._resetCopyFeedback();
      return;
    }
    const success = documentRef.getElementById(successID);
    if (success) success.style.display = 'none';
  };
}
