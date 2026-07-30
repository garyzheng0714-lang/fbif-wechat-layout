import {
  loadCurrentUser,
  storeSessionToken,
} from './auth-session.js';

const form = document.getElementById('passwordLoginForm');
const usernameInput = document.getElementById('loginUsername');
const passwordInput = document.getElementById('loginPassword');
const rememberInput = document.getElementById('rememberUsername');
const submitButton = document.getElementById('passwordLoginButton');
const feishuButton = document.getElementById('feishuLoginButton');
const visibilityButton = document.getElementById('passwordVisibility');
const errorElement = document.getElementById('loginError');
const accountHelp = document.getElementById('accountHelp');
const accountFields = Array.from(document.querySelectorAll('.auth-pill-field'));

function safeReturnTo() {
  const raw = new URLSearchParams(location.search).get('return_to') || '/mp/fbif';
  return raw.startsWith('/') && !raw.startsWith('//') && !/[\\\r\n]/.test(raw)
    ? raw
    : '/mp/fbif';
}

function showError(message, credentialsInvalid = false) {
  errorElement.textContent = message;
  errorElement.hidden = !message;
  accountFields.forEach(field => field.classList.toggle('is-invalid', credentialsInvalid));
}

function initialErrorMessage() {
  const code = new URLSearchParams(location.search).get('error');
  if (code === 'denied') return '已取消飞书授权，请重新登录';
  if (code === 'unauthorized_tenant') return '当前飞书账号无权访问，请联系管理员';
  if (code) return '飞书登录失败，请重新尝试';
  return '';
}

async function redirectIfAuthenticated() {
  try {
    const user = await loadCurrentUser();
    if (user) location.replace(safeReturnTo());
  } catch {
    // The login form stays available when the session check is temporarily unavailable.
  }
}

feishuButton.addEventListener('click', () => {
  feishuButton.disabled = true;
  location.href = `/auth/feishu/fbif/login?return_to=${encodeURIComponent(safeReturnTo())}`;
});

visibilityButton.addEventListener('click', () => {
  const show = passwordInput.type === 'password';
  passwordInput.type = show ? 'text' : 'password';
  visibilityButton.setAttribute('aria-label', show ? '隐藏密码' : '显示密码');
  visibilityButton.setAttribute('aria-pressed', String(show));
  passwordInput.focus();
});

for (const input of [usernameInput, passwordInput]) {
  input.addEventListener('input', () => showError(''));
}

for (const trigger of document.querySelectorAll('[data-account-help]')) {
  trigger.addEventListener('click', () => {
    accountHelp.hidden = false;
  });
}

form.addEventListener('submit', async event => {
  event.preventDefault();
  const username = usernameInput.value.trim();
  const password = passwordInput.value;
  if (!username || !password) {
    showError('请输入账号和密码');
    return;
  }
  showError('');
  submitButton.disabled = true;
  submitButton.textContent = '正在登录…';
  try {
    const response = await fetch('/auth/password/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password, remember: rememberInput.checked }),
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(payload.error || '登录失败，请稍后再试');
      error.credentialsInvalid = response.status === 401;
      throw error;
    }
    storeSessionToken(payload.session_token || '');
    location.replace(safeReturnTo());
  } catch (error) {
    showError(error?.message || '登录失败，请重试', Boolean(error?.credentialsInvalid));
    passwordInput.select();
  } finally {
    submitButton.disabled = false;
    submitButton.textContent = '登录';
  }
});

showError(initialErrorMessage());
redirectIfAuthenticated();
