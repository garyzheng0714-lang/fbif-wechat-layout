// editor-shell.js — shared shell helpers for channel editor pages.
// Keeps page scripts focused on channel-specific wiring while common URL import,
// rule-pack path resolution, and copy feedback live behind one interface.

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

export function createUrlImportHandler({
  documentRef = globalThis.document,
  fetchImpl = globalThis.fetch,
  FileCtor = globalThis.File,
  DataTransferCtor = globalThis.DataTransfer,
  getHandleBlocks = () => globalThis.window && globalThis.window._handleBlocks,
  getHandleFile = () => globalThis.window && globalThis.window._handleFile,
  getAccountID = () => '',
  onArticleUrl = () => {},
  errorMessage = defaultFetchErrorMessage,
} = {}) {
  return async function fetchArticleFromUrl() {
    const urlInput = documentRef.getElementById('urlInput');
    const btn = documentRef.getElementById('urlFetchBtn');
    const errorEl = documentRef.getElementById('error');
    const url = (urlInput && urlInput.value || '').trim();
    if (!url) return;
    if (errorEl) {
      errorEl.textContent = '';
      errorEl.style.display = 'none';
    }
    if (btn) {
      btn.disabled = true;
      btn.textContent = '提取中...';
    }
    try {
      onArticleUrl(url);
      const resp = await fetchImpl('/api/fetch-article', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url, account: getAccountID() || undefined }),
      });
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: 'HTTP ' + resp.status }));
        throw new Error(err.error || '抓取失败');
      }
      const payload = await resp.json();
      const { title, blocks, content } = payload;
      if (Array.isArray(blocks) && blocks.length > 0) {
        const handleBlocks = getHandleBlocks();
        if (!handleBlocks) throw new Error('blocks 路径未就绪');
        handleBlocks({ title, blocks, source: url }, { isRepost: true });
      } else if (content) {
        const file = new FileCtor([content], (title || 'article') + '.md', { type: 'text/markdown' });
        const handleFile = getHandleFile();
        if (handleFile) {
          handleFile(file, { isRepost: true });
        } else {
          const fi = documentRef.getElementById('fileInput');
          const dt = new DataTransferCtor();
          dt.items.add(file);
          fi.files = dt.files;
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
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.textContent = '提取';
      }
    }
  };
}

export function bindUrlImportShortcuts({
  documentRef = globalThis.document,
  fetchArticle,
  ignoreClassNames = [],
} = {}) {
  documentRef.addEventListener('keydown', event => {
    const preview = documentRef.getElementById('previewContent');
    const hasPreview = preview && preview.style.display !== 'none';
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter' && hasPreview) {
      event.preventDefault();
      if (globalThis.window && globalThis.window.copyContent) globalThis.window.copyContent();
    }
  });
  documentRef.addEventListener('paste', event => {
    const urlInput = documentRef.getElementById('urlInput');
    const ae = documentRef.activeElement;
    if (ae === urlInput) return;
    if (ae && ae.classList && ignoreClassNames.some(name => ae.classList.contains(name))) return;
    const tag = ae && ae.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA') return;
    const text = (event.clipboardData || globalThis.window.clipboardData).getData('text').trim();
    if (text && /^https?:\/\//i.test(text)) {
      event.preventDefault();
      urlInput.value = text;
      fetchArticle();
    }
  });
  const urlInput = documentRef.getElementById('urlInput');
  if (!urlInput) return;
  urlInput.addEventListener('paste', () => {
    setTimeout(() => {
      const value = (urlInput.value || '').trim();
      if (/^https?:\/\//i.test(value)) fetchArticle();
    }, 0);
  });
  urlInput.addEventListener('keydown', event => {
    if (event.key === 'Enter') {
      event.preventDefault();
      fetchArticle();
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
