// fetch-timeout.js — 给 fetch 套统一超时。网络挂起 / 部署重启 / CDN 卡死时，
// 不让 UI 永久转圈：超时即 AbortController.abort()，抛出带中文 message 的 Error，
// 调用方据此恢复按钮、给提示或回退本地缓存。
// 接受自定义 fetchImpl，兼容 editor-shell 等注入了 fetch 的调用方与测试 mock。

export function fetchWithTimeout(url, options = {}, {
  timeoutMs = 20000,
  timeoutMessage = '请求超时，请稍后重试',
  fetchImpl,
} = {}) {
  const doFetch = fetchImpl || globalThis.fetch;
  const controller = new AbortController();
  const externalSignal = options && options.signal;
  let timedOut = false;
  const abortFromCaller = () => controller.abort(externalSignal && externalSignal.reason);
  if (externalSignal && typeof externalSignal.addEventListener === 'function') {
    if (externalSignal.aborted) abortFromCaller();
    else externalSignal.addEventListener('abort', abortFromCaller, { once: true });
  }
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  return Promise.resolve(doFetch(url, { ...options, signal: controller.signal }))
    .catch((err) => {
      if (err && err.name === 'AbortError' && timedOut) throw new Error(timeoutMessage);
      throw err;
    })
    .finally(() => {
      clearTimeout(timer);
      if (externalSignal && typeof externalSignal.removeEventListener === 'function') {
        externalSignal.removeEventListener('abort', abortFromCaller);
      }
    });
}
