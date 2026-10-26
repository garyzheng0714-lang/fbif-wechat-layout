// 在网页编辑器里跑：照人的操作导入一篇、点复制，把剪贴板里的 HTML 存成金样。
// 由 server.mjs 提供，浏览器控制台里 `await import('http://127.0.0.1:19192/harness.js')` 后调
// `await window.__layoutCapture({ account, input })`。用法见 README.md。
const HELPER = 'http://127.0.0.1:19192';

// 图片地址两边一定不同（网页转 blob/data，接口转存 OSS），金样里统一换成 IMG。
export function normalizeImageSources(html) {
  return String(html).replace(/(<img\b[^>]*?\ssrc=")[^"]*(")/gi, '$1IMG$2');
}

function installClipboardRecorder() {
  window.__copied = [];
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      write: async items => {
        for (const item of items) {
          if (item.types.includes('text/html')) {
            window.__copied.push(await (await item.getType('text/html')).text());
          }
        }
      },
      writeText: async () => {},
      readText: async () => '',
    },
  });
}

async function fixtureFile(name, type) {
  const resp = await fetch(`${HELPER}/fixture/${name}`);
  if (!resp.ok) throw new Error(`取不到输入文件 ${name}：HTTP ${resp.status}`);
  return new File([await resp.blob()], name, { type });
}

async function save(name, body) {
  const resp = await fetch(`${HELPER}/save/${name}`, { method: 'POST', body });
  if (!resp.ok) throw new Error(`存不下 ${name}：HTTP ${resp.status}`);
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

async function waitFor(check, what, timeoutMs = 45000) {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const value = await check();
    if (value) return value;
    await sleep(300);
  }
  throw new Error(`等不到${what}`);
}

const MIME = {
  docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  markdown: 'text/markdown',
};

async function importInput(input) {
  if (input.kind === 'docx' || input.kind === 'markdown') {
    const fileInput = document.getElementById('fileInput');
    const dt = new DataTransfer();
    dt.items.add(await fixtureFile(input.file, MIME[input.kind]));
    fileInput.files = dt.files;
    fileInput.dispatchEvent(new Event('change', { bubbles: true }));
    return;
  }
  // 文字和链接都从导入框进，和人粘贴后自动提交走同一个入口（editor-shell.js）。
  await waitFor(() => typeof window.fetchArticle === 'function', '导入框就绪');
  document.getElementById('urlInput').value = input.value;
  await window.fetchArticle();
}

// 采集时浏览器窗口常在后台，后台页面的 requestAnimationFrame 不触发，预览整帧提交就卡住。
// 换成定时器只改时机，不改排出来的 HTML。
async function ensureAnimationFrames() {
  const fired = await Promise.race([
    new Promise(resolve => requestAnimationFrame(() => resolve(true))),
    sleep(300).then(() => false),
  ]);
  if (!fired) {
    window.requestAnimationFrame = cb => setTimeout(() => cb(performance.now()), 16);
    // 同理，后台页面里 image.decode() 不结束；改成「加载完就算解码好」。
    HTMLImageElement.prototype.decode = function decodeWhenLoaded() {
      return new Promise((resolve, reject) => {
        if (this.complete && this.naturalWidth > 0) return resolve();
        this.addEventListener('load', () => resolve(), { once: true });
        this.addEventListener('error', () => reject(new Error('图片加载失败')), { once: true });
      });
    };
  }
}

window.__layoutCaptureStart = args => {
  window.__layoutCaptureResult = null;
  window.__layoutCapture(args).then(
    result => { window.__layoutCaptureResult = { ok: true, ...result }; },
    err => { window.__layoutCaptureResult = { ok: false, error: String(err && err.message || err) }; },
  );
};

// footerVisibilityOverride：复制前先用编辑器自己的「本篇底部区块显隐」开关（如关掉公众号的更多文章），
// 接口排公众号时同样关掉这一块，金样才和接口逐字可比。
window.__layoutCapture = async ({ account, input, footerVisibilityOverride }) => {
  await ensureAnimationFrames();
  await waitFor(() => window._activeRuleState === 'resolved', '规则加载');
  installClipboardRecorder();
  await importInput(input);
  await waitFor(() => {
    const el = document.getElementById('previewContent');
    const area = document.getElementById('contentArea');
    return el && el.style.display !== 'none' && area && area.textContent.trim().length > 0;
  }, '预览出来');
  if (footerVisibilityOverride) await window._engineApi.setFooterVisibilityOverride(footerVisibilityOverride);
  const html = await waitFor(async () => {
    const before = window.__copied.length;
    const ok = await window.copyContent();
    if (ok && window.__copied.length > before) return window.__copied[window.__copied.length - 1];
    await sleep(700);
    return null;
  }, '复制成功', 60000);
  const name = `${account}--${input.id}`;
  await save(`${name}.html`, normalizeImageSources(html));
  const rules = await (await fetch(`/api/rules/active?account=${encodeURIComponent(account)}`, { cache: 'no-store' })).text();
  await save(`rules--${account}.json`, rules);
  if (input.kind === 'url') {
    const payload = await (await fetch('/api/fetch-article', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: input.value, account }),
    })).text();
    await save(`${name}.fetch.json`, payload);
  }
  return { name, bytes: html.length, images: (html.match(/<img\b/gi) || []).length };
};
