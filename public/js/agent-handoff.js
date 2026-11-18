// 智能体交接（红线 51）：同事在 Codex、Claude Code 里让智能体排好一篇，智能体给的链接是
// /<渠道>/<账号>?agent=<编号>。打开时按智能体交来的同一份输入重新导入，和人自己操作一模一样：
// 链接 = 粘贴链接，文字 = 粘贴文字，Markdown = 上传 .md，Word = 上传 Word。
// 不存成品、不另排一套：交接只换输入从哪来，排版、校对、复制全走编辑器原来那条路。

const ID_PATTERN = /^ag_[A-Za-z0-9_-]{22}$/;

// takeAgentHandoffID 取出地址里的编号并从地址栏去掉：刷新时按上次的稿子恢复，不再导一遍。
export function takeAgentHandoffID(windowRef = globalThis.window) {
  let url;
  try {
    url = new URL(windowRef.location.href);
  } catch {
    return '';
  }
  const id = url.searchParams.get('agent');
  if (id === null) return '';
  url.searchParams.delete('agent');
  try {
    windowRef.history.replaceState(windowRef.history.state, '', url.pathname + url.search + url.hash);
  } catch {}
  return ID_PATTERN.test(id) ? id : '';
}

async function readError(res) {
  try {
    const body = await res.json();
    if (body && typeof body.error === 'string' && body.error) return body.error;
  } catch {}
  return '';
}

// runAgentHandoff 导入智能体排的那一篇。返回 'none' | 'imported' | 'failed'。
//   submit(value)    粘贴链接、文字的那条路（editor-shell bindUrlImportShortcuts 的 submit）
//   importText(text) 粘贴文字（submit 不收时用：框里 @ 开头会被当成飞书文档搜索）
//   uploadFile(file) 上传文件的那条路（engine.js 的 _uploadFiles）
//   ensureArticle()  公众号编辑器切回「文章」：智能体排的是文章，不是贴图
// 没取到时写在导入卡下的错误条（#error，和链接抓不到同一处）。
export async function runAgentHandoff({
  windowRef = globalThis.window,
  documentRef = globalThis.document,
  fetchImpl = globalThis.fetch,
  submit,
  importText,
  uploadFile,
  ensureArticle = null,
} = {}) {
  const showError = message => {
    const errorEl = documentRef && documentRef.getElementById('error');
    if (!errorEl) return;
    errorEl.textContent = message;
    errorEl.style.display = 'block';
  };
  const id = takeAgentHandoffID(windowRef);
  if (!id) return 'none';
  const base = '/api/agent/results/' + id;
  try {
    const res = await fetchImpl(base, { credentials: 'same-origin', cache: 'no-store' });
    if (!res.ok) {
      showError((await readError(res)) || '智能体排的这一篇打不开，请在智能体里重新排');
      return 'failed';
    }
    const record = await res.json();
    if (typeof ensureArticle === 'function') await ensureArticle();
    switch (record.kind) {
      case 'url':
        submit(record.url);
        return 'imported';
      case 'text':
        if (!submit(record.text)) importText(record.text);
        return 'imported';
      case 'markdown':
        uploadFile(new File([record.text], record.file_name, { type: 'text/markdown' }));
        return 'imported';
      case 'word': {
        const file = await fetchImpl(base + '/file', { credentials: 'same-origin', cache: 'no-store' });
        if (!file.ok) {
          showError((await readError(file)) || 'Word 原件没取到，请在智能体里重新排');
          return 'failed';
        }
        uploadFile(new File([await file.blob()], record.file_name));
        return 'imported';
      }
    }
    showError('智能体排的这一篇打不开，请在智能体里重新排');
    return 'failed';
  } catch {
    showError('智能体排的这一篇没取到，请重新打开智能体给的链接');
    return 'failed';
  }
}
