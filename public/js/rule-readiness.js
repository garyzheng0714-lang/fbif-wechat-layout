const FINAL_RULE_STATE = 'resolved';
const RULE_STATES = new Set(['unresolved', 'loading', FINAL_RULE_STATE, 'stale-cache', 'failed']);

export function ruleStateAllowsFinalOutput(win = window) {
  return !win._activeRuleState || win._activeRuleState === FINAL_RULE_STATE;
}

export function retryRuleLoad(win = window) {
  try {
    const bridge = win._sessionRestore;
    const channel = bridge?.currentChannel?.();
    if (channel) bridge.capture?.(channel);
  } catch (error) {
    globalThis.console?.warn?.('[rules] retry draft capture failed:', error);
  }
  win.location.reload();
}

export function showRuleStatusBanner({
  message = '规则服务尚未确认，当前只显示本机缓存；复制和发布已暂停。',
  onRetry = () => retryRuleLoad(window),
  doc = document,
} = {}) {
  let banner = doc.getElementById('ruleStatusBanner');
  if (!banner) {
    banner = doc.createElement('div');
    banner.id = 'ruleStatusBanner';
    banner.className = 'version-update-banner rule-status-banner';
    banner.setAttribute('role', 'alert');
    banner.setAttribute('aria-live', 'assertive');

    const text = doc.createElement('span');
    text.className = 'version-update-message';
    text.dataset.role = 'rule-status-message';
    const action = doc.createElement('button');
    action.type = 'button';
    action.className = 'version-update-action';
    action.textContent = '重试加载';
    action.dataset.action = 'retry-rules';
    banner.append(text, action);

    const spacer = doc.querySelector('.global-channel-bar .topbar-spacer');
    if (spacer) spacer.before(banner);
    else doc.body.prepend(banner);
  }
  const text = banner.querySelector('[data-role="rule-status-message"]');
  if (text) text.textContent = message;
  const oldAction = banner.querySelector('[data-action="retry-rules"]');
  if (oldAction) {
    const action = oldAction.cloneNode(true);
    action.addEventListener('click', onRetry, { once: true });
    oldAction.replaceWith(action);
  }
  return banner;
}

export function setRuleReadinessState(state, {
  win = window,
  doc = document,
  onRetry = () => retryRuleLoad(win),
  error = '',
} = {}) {
  const normalized = RULE_STATES.has(state) ? state : 'failed';
  win._activeRuleState = normalized;
  const copyButton = doc.getElementById('copyBtn');
  const blocked = normalized !== FINAL_RULE_STATE;
  if (copyButton) {
    if (blocked) {
      copyButton.dataset.ruleBlocked = 'true';
      copyButton.disabled = true;
      copyButton.setAttribute('aria-disabled', 'true');
      copyButton.title = normalized === 'loading' ? '正在加载排版规则' : '规则未确认，暂不能复制';
    } else if (copyButton.dataset.ruleBlocked === 'true') {
      delete copyButton.dataset.ruleBlocked;
      copyButton.disabled = false;
      copyButton.removeAttribute('aria-disabled');
      copyButton.removeAttribute('title');
    }
  }

  const banner = doc.getElementById('ruleStatusBanner');
  if (normalized === FINAL_RULE_STATE || normalized === 'loading' || normalized === 'unresolved') {
    if (banner) banner.remove();
    return normalized;
  }
  const detail = String(error || '').trim();
  const prefix = normalized === 'stale-cache'
    ? '规则服务暂不可用，当前只显示本机缓存；复制和发布已暂停。'
    : '规则加载失败，复制和发布已暂停。';
  showRuleStatusBanner({
    message: detail ? `${prefix} ${detail}` : prefix,
    onRetry,
    doc,
  });
  return normalized;
}
