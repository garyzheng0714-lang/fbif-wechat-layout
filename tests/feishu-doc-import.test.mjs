import assert from 'node:assert/strict';
import test from 'node:test';
import { parseHTML } from 'linkedom';

import {
  mountFeishuDocumentImport,
  parseFeishuDocumentUrl,
  readResponseBlobWithProgress,
} from '../public/js/feishu-doc-import.js';

function makeShell() {
  return parseHTML(`
    <main>
      <textarea id="urlInput"></textarea>
      <div id="feishuDocCapability"></div>
      <div id="dropZone">
        <span id="feishuDocTriggerWrap" class="import-feishu-action-wrap is-login-required" title="上传飞书云文档需登录FBIF内部账号">
          <button id="feishuDocTrigger" aria-expanded="false" aria-disabled="true" disabled>飞书云文档</button>
        </span>
        <div id="feishuDocSearch" hidden></div>
      </div>
      <div id="progress" style="display:none">
        <div id="progressFill"></div>
        <div id="progressText"></div>
        <div id="progressPercent"></div>
      </div>
    </main>
  `);
}

test('anonymous users see a disabled Feishu button with the internal-account login hint', () => {
  const { document, window } = makeShell();
  let calls = 0;
  mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: null,
    fetchImpl: async () => {
      calls++;
      throw new Error('must not be called');
    },
  });

  const trigger = document.getElementById('feishuDocTrigger');
  const wrapper = document.getElementById('feishuDocTriggerWrap');
  assert.equal(trigger.disabled, true);
  assert.equal(trigger.getAttribute('aria-disabled'), 'true');
  assert.equal(wrapper.classList.contains('is-login-required'), true);
  assert.equal(wrapper.title, '上传飞书云文档需登录FBIF内部账号');
  assert.equal(document.getElementById('feishuDocCapability').textContent, '粘贴后自动排版');
  trigger.click();
  assert.equal(calls, 0);
  assert.equal(document.getElementById('feishuDocSearch').hidden, true);
});

function input(window, node, value) {
  node.value = value;
  node.dispatchEvent(new window.Event('input', { bubbles: true }));
}

function jsonResponse(payload, status = 200) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

test('Feishu document URLs are parsed only from official hosts and supported document paths', () => {
  assert.deepEqual(
    parseFeishuDocumentUrl('https://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc?from=navigation'),
    {
      token: 'HosadQWiIoiX4CxG3g7cPXaUnwc',
      type: 'docx',
      title: '飞书文档',
      url: 'https://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc?from=navigation',
    },
  );
  assert.deepEqual(
    parseFeishuDocumentUrl('https://bytedance.larkoffice.com/wiki/HZQswdqYJirjPskb1wCczyb0nHb'),
    {
      token: 'HZQswdqYJirjPskb1wCczyb0nHb',
      type: 'wiki',
      title: '飞书文档',
      url: 'https://bytedance.larkoffice.com/wiki/HZQswdqYJirjPskb1wCczyb0nHb',
    },
  );
  assert.deepEqual(
    parseFeishuDocumentUrl('https://workspace.larksuite.com/docs/doccn_abcdef123456'),
    {
      token: 'doccn_abcdef123456',
      type: 'doc',
      title: '飞书文档',
      url: 'https://workspace.larksuite.com/docs/doccn_abcdef123456',
    },
  );
  assert.equal(parseFeishuDocumentUrl('https://foodtalks.feishu.cn.evil.test/docx/token1234'), null);
  assert.equal(parseFeishuDocumentUrl('http://foodtalks.feishu.cn/docx/token1234'), null);
  assert.equal(parseFeishuDocumentUrl('https://foodtalks.feishu.cn/base/token1234'), null);
  assert.equal(parseFeishuDocumentUrl('https://foodtalks.feishu.cn/docx/token1234/extra'), null);
  assert.equal(parseFeishuDocumentUrl('not a URL'), null);
});

test('password login shows a real locked state and never calls the Feishu API', async () => {
  const { document, window } = makeShell();
  let calls = 0;
  mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'password',
      capabilities: { feishu_documents: false },
    },
    fetchImpl: async () => {
      calls++;
      throw new Error('must not be called');
    },
    debounceMs: 0,
  });
  assert.equal(document.getElementById('feishuDocTrigger').disabled, false);
  assert.equal(document.getElementById('feishuDocTriggerWrap').hasAttribute('title'), false);
  assert.equal(document.getElementById('feishuDocCapability').textContent, '粘贴后自动排版');
  input(window, document.getElementById('urlInput'), '@新品');
  await new Promise(resolve => setTimeout(resolve, 5));
  assert.equal(calls, 0);
  assert.equal(document.getElementById('feishuDocSearch').hidden, false);
  assert.match(document.getElementById('feishuDocSearch').textContent, /需要使用飞书登录/);
});

test('password login recognizes a Feishu document link and offers Feishu login instead of generic URL import', () => {
  const { document, window } = makeShell();
  let calls = 0;
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'password',
      capabilities: { feishu_documents: false },
    },
    fetchImpl: async () => {
      calls++;
      throw new Error('must not be called');
    },
  });

  const handled = controller.importValue(
    'https://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc?from=navigation',
  );

  assert.equal(handled, true);
  assert.equal(calls, 0);
  assert.equal(document.getElementById('feishuDocSearch').hidden, false);
  assert.match(document.getElementById('feishuDocSearch').textContent, /导入飞书文档需要使用飞书登录/);
  controller.destroy();
});

test('Feishu login imports a pasted docx link without running a search first', async () => {
  const { document, window } = makeShell();
  const calls = [];
  let handedOff;
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'feishu',
      capabilities: { feishu_documents: true },
    },
    fetchImpl: async (url, options = {}) => {
      calls.push([String(url), options]);
      return new Response(new Blob(['PK\u0003\u0004docx-test']), {
        status: 200,
        headers: {
          'Content-Type': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
          'Content-Disposition': "attachment; filename*=UTF-8''%E4%B8%AD%E5%9B%BD%E8%B1%86%E8%85%90.docx",
        },
      });
    },
    getHandleFile: () => async (file, options) => {
      handedOff = { file, options };
      return true;
    },
    FileCtor: window.File,
  });
  const link = 'https://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc?from=navigation';
  document.getElementById('urlInput').value = link;

  assert.equal(controller.importValue(link), true);
  await new Promise(resolve => setTimeout(resolve, 12));

  assert.equal(calls.length, 1);
  assert.equal(calls[0][0], '/api/feishu/docs/export');
  assert.deepEqual(JSON.parse(calls[0][1].body), {
    token: 'HosadQWiIoiX4CxG3g7cPXaUnwc',
    type: 'docx',
    title: '飞书文档',
    url: link,
  });
  assert.equal(handedOff.file.name, '中国豆腐.docx');
  assert.deepEqual(handedOff.options.sourceDocument, {
    token: 'HosadQWiIoiX4CxG3g7cPXaUnwc',
    type: 'docx',
    title: '飞书文档',
    url: link,
  });
  assert.equal(document.getElementById('urlInput').value, '');
  controller.destroy();
});

test('unrelated links stay available to the generic URL importer', () => {
  const { document, window } = makeShell();
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'feishu',
      capabilities: { feishu_documents: true },
    },
  });

  assert.equal(controller.importValue('https://mp.weixin.qq.com/s/example'), false);
  controller.destroy();
});

test('Feishu login searches by @keyword and exports the selected document through _handleFile', async () => {
  const { document, window } = makeShell();
  const calls = [];
  let handedOff;
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'feishu',
      capabilities: { feishu_documents: true },
    },
    fetchImpl: async (url, options = {}) => {
      calls.push([url, options]);
      if (String(url).startsWith('/api/feishu/docs/search')) {
        return jsonResponse({
          documents: [{
            token: 'doxcn_test',
            type: 'docx',
            title: '2026 新品趋势',
            owner_name: '张三',
            modified_at: '2026-07-29T08:00:00Z',
          }],
        });
      }
      return new Response(new Blob(['PK\u0003\u0004docx-test']), {
        status: 200,
        headers: {
          'Content-Type': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
          'Content-Disposition': "attachment; filename*=UTF-8''2026%20%E6%96%B0%E5%93%81%E8%B6%8B%E5%8A%BF.docx",
        },
      });
    },
    getHandleFile: () => async (file, options) => {
      handedOff = { file, options };
      return true;
    },
    FileCtor: window.File,
    debounceMs: 0,
  });
  input(window, document.getElementById('urlInput'), '@新品');
  await new Promise(resolve => setTimeout(resolve, 8));
  const result = document.querySelector('[data-feishu-doc-result]');
  assert.ok(result);
  assert.match(result.textContent, /2026 新品趋势/);
  result.click();
  await new Promise(resolve => setTimeout(resolve, 8));

  assert.equal(calls.length, 2);
  assert.match(calls[0][0], /^\/api\/feishu\/docs\/search\?q=/);
  assert.equal(calls[1][0], '/api/feishu/docs/export');
  assert.equal(handedOff.file.name, '2026 新品趋势.docx');
  assert.equal(handedOff.options.source, 'feishu-doc-import');
  assert.equal(handedOff.options.isRepost, false);
  assert.equal(document.getElementById('urlInput').value, '');
  controller.destroy();
});

test('Feishu import waits for the renderer and preserves the query when rendering fails', async () => {
  const { document, window } = makeShell();
  let resolveRender;
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'feishu',
      capabilities: { feishu_documents: true },
    },
    fetchImpl: async (url) => {
      if (String(url).startsWith('/api/feishu/docs/search')) {
        return jsonResponse({ documents: [{ token: 'dox_a', type: 'docx', title: 'A' }] });
      }
      return new Response(new Blob(['PK\u0003\u0004docx-test']), {
        status: 200,
        headers: {
          'Content-Type': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
          'Content-Length': '13',
        },
      });
    },
    getHandleFile: () => () => new Promise(resolve => {
      resolveRender = resolve;
    }),
    FileCtor: window.File,
    debounceMs: 0,
  });

  input(window, document.getElementById('urlInput'), '@A');
  await new Promise(resolve => setTimeout(resolve, 8));
  document.querySelector('[data-feishu-doc-result]').click();
  await new Promise(resolve => setTimeout(resolve, 8));

  assert.equal(document.getElementById('urlInput').value, '@A');
  assert.equal(document.getElementById('progress').style.display, 'block');
  resolveRender(false);
  await new Promise(resolve => setTimeout(resolve, 8));

  assert.equal(document.getElementById('urlInput').value, '@A');
  assert.match(document.getElementById('feishuDocSearch').textContent, /导入失败/);
  controller.destroy();
});

test('changing intent after renderer handoff aborts the same engine signal and cannot complete the old import', async () => {
  const { document, window } = makeShell();
  let handedOptions;
  let resolveRender;
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'feishu',
      capabilities: { feishu_documents: true },
    },
    fetchImpl: async (url) => {
      if (String(url).startsWith('/api/feishu/docs/search')) {
        return jsonResponse({ documents: [{ token: 'dox_old', type: 'docx', title: '旧文档' }] });
      }
      return new Response(new Blob(['PK\u0003\u0004docx-test']), {
        status: 200,
        headers: { 'Content-Type': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' },
      });
    },
    getHandleFile: () => (file, options) => {
      handedOptions = options;
      return new Promise(resolve => { resolveRender = resolve; });
    },
    FileCtor: window.File,
    debounceMs: 0,
  });

  input(window, document.getElementById('urlInput'), '@旧文档');
  await new Promise(resolve => setTimeout(resolve, 8));
  document.querySelector('[data-feishu-doc-result]').click();
  await new Promise(resolve => setTimeout(resolve, 8));

  assert.equal(handedOptions.signal.aborted, false);
  assert.equal(handedOptions.isStale(), false);
  controller.invalidate();
  assert.equal(handedOptions.signal.aborted, true);
  assert.equal(handedOptions.isStale(), true);

  resolveRender(true);
  await new Promise(resolve => setTimeout(resolve, 8));
  assert.equal(document.getElementById('urlInput').value, '@旧文档');
  controller.destroy();
});

test('Feishu export download reports real byte progress', async () => {
  const updates = [];
  const response = new Response(new Uint8Array([1, 2, 3, 4, 5, 6]), {
    headers: {
      'Content-Length': '6',
      'Content-Type': 'application/octet-stream',
    },
  });
  const blob = await readResponseBlobWithProgress(response, {
    onProgress(update) {
      updates.push(update);
    },
  });

  assert.equal(blob.size, 6);
  assert.ok(updates.length >= 1);
  assert.deepEqual(updates.at(-1), { loaded: 6, total: 6 });
});

test('a newer import intent invalidates an older Feishu export before renderer handoff', async () => {
  const { document, window } = makeShell();
  let resolveExport;
  let exportSignal;
  let handoffCount = 0;
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'feishu',
      capabilities: { feishu_documents: true },
    },
    fetchImpl: async (url, options = {}) => {
      if (String(url).startsWith('/api/feishu/docs/search')) {
        return jsonResponse({ documents: [{ token: 'dox_a', type: 'docx', title: 'A' }] });
      }
      exportSignal = options.signal;
      return new Promise(resolve => {
        resolveExport = () => resolve(new Response(new Blob(['PK\u0003\u0004old']), {
          status: 200,
          headers: { 'Content-Disposition': "attachment; filename*=UTF-8''A.docx" },
        }));
      });
    },
    getHandleFile: () => () => { handoffCount++; },
    FileCtor: window.File,
    debounceMs: 0,
  });
  input(window, document.getElementById('urlInput'), '@A');
  await new Promise(resolve => setTimeout(resolve, 8));
  document.querySelector('[data-feishu-doc-result]').click();
  await new Promise(resolve => setTimeout(resolve, 1));
  controller.invalidate();
  assert.equal(exportSignal?.aborted, true);
  resolveExport();
  await new Promise(resolve => setTimeout(resolve, 8));
  assert.equal(handoffCount, 0);
});

test('a pasted wiki link uses the real export job progress and hands one tracker to the renderer', async () => {
  const { document, window } = makeShell();
  const intervalCallbacks = [];
  window.setInterval = callback => {
    intervalCallbacks.push(callback);
    return intervalCallbacks.length;
  };
  window.clearInterval = () => {};

  const trackerCalls = [];
  let commits = 0;
  const tracker = {
    updatePhase(id, done, total, options) {
      trackerCalls.push({ kind: 'update', id, done, total, options });
    },
    completePhase(id, options) {
      trackerCalls.push({ kind: 'complete', id, options });
    },
    cancel(reason) {
      trackerCalls.push({ kind: 'cancel', reason });
    },
    commit() {
      commits++;
      return true;
    },
  };
  const calls = [];
  let handedOff;
  let statusReads = 0;
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'feishu',
      capabilities: { feishu_documents: true },
    },
    fetchImpl: async (url, options = {}) => {
      calls.push([String(url), options]);
      if (url === '/api/feishu/docs/export/jobs') {
        return jsonResponse({
          job_id: 'job_1',
          state: 'queued',
          phase: 'queued',
          done: 0,
          total: 0,
          unit: '',
          version: 1,
          status_url: 'https://evil.example/steal-session',
          result_url: 'https://evil.example/steal-result',
        }, 202);
      }
      if (String(url).startsWith('/api/feishu/docs/export/jobs/job_1?')) {
        statusReads++;
        if (statusReads === 1) {
          return jsonResponse({
            job_id: 'job_1',
            state: 'running',
            phase: 'poll',
            done: 2,
            total: 4,
            unit: 'attempt',
            version: 2,
          });
        }
        return jsonResponse({
          job_id: 'job_1',
          state: 'completed',
          phase: 'optimize',
          done: 1,
          total: 1,
          unit: 'document',
          version: 3,
          result_url: 'https://evil.example/steal-result',
        });
      }
      if (url === '/api/feishu/docs/export/jobs/job_1/result') {
        return new Response(new Uint8Array([1, 2, 3, 4, 5, 6]), {
          status: 200,
          headers: {
            'Content-Type': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
            'Content-Length': '6',
            'Content-Disposition': "attachment; filename*=UTF-8''progress.docx",
          },
        });
      }
      throw new Error(`unexpected request: ${url}`);
    },
    createProgressTracker: () => tracker,
    getHandleFile: () => async (file, options) => {
      handedOff = { file, options };
      return true;
    },
    FileCtor: window.File,
    debounceMs: 0,
  });

  const link = 'https://bytedance.larkoffice.com/wiki/HZQswdqYJirjPskb1wCczyb0nHb?from=navigation';
  document.getElementById('urlInput').value = link;
  assert.equal(controller.importValue(link), true);
  await new Promise(resolve => setTimeout(resolve, 20));

  const beforeTimerTicks = trackerCalls.length;
  for (const callback of intervalCallbacks) {
    callback();
    callback();
    callback();
  }
  assert.equal(
    trackerCalls.length,
    beforeTimerTicks,
    'elapsed-time timers may update copy, but must never invent percentage progress',
  );

  assert.ok(calls.some(([url]) => url === '/api/feishu/docs/export/jobs'));
  const createCall = calls.find(([url]) => url === '/api/feishu/docs/export/jobs');
  assert.deepEqual(JSON.parse(createCall[1].body), {
    token: 'HZQswdqYJirjPskb1wCczyb0nHb',
    type: 'wiki',
    title: '飞书文档',
    url: link,
  });
  assert.equal(calls.some(([url]) => url.startsWith('/api/feishu/docs/search')), false);
  assert.equal(statusReads, 2);
  assert.ok(calls.some(([url]) => url === '/api/feishu/docs/export/jobs/job_1/result'));
  assert.equal(calls.some(([url]) => url.startsWith('https://evil.example/')), false);
  assert.ok(trackerCalls.some(call => (
    call.kind === 'update'
    && call.id === 'poll'
    && call.done === 2
    && call.total === 4
  )));
  assert.ok(trackerCalls.some(call => (
    call.kind === 'update'
    && call.id === 'optimize'
    && call.done === 1
    && call.total === 1
  )));
  assert.ok(trackerCalls.some(call => (
    call.kind === 'update'
    && call.id === 'result-download'
    && call.done === 6
    && call.total === 6
  )));
  assert.equal(handedOff.file.name, 'progress.docx');
  assert.equal(handedOff.options.importProgress, tracker);
  assert.equal(commits, 0, 'the importer cannot report 100 before preview/copy commit');
  controller.destroy();
});

test('superseding a Feishu export job cancels its tracker and never commits 100', async () => {
  const { document, window } = makeShell();
  let releaseStatus;
  const trackerCalls = [];
  let handoffs = 0;
  const tracker = {
    updatePhase() {},
    completePhase() {},
    cancel(reason) {
      trackerCalls.push(['cancel', reason]);
      document.getElementById('progress').style.display = 'block';
    },
    commit() {
      trackerCalls.push(['commit']);
      return true;
    },
  };
  const controller = mountFeishuDocumentImport({
    documentRef: document,
    windowRef: window,
    user: {
      auth_mode: 'feishu',
      capabilities: { feishu_documents: true },
    },
    fetchImpl: async (url) => {
      if (String(url).startsWith('/api/feishu/docs/search')) {
        return jsonResponse({
          documents: [{ token: 'dox_old', type: 'docx', title: '旧文档' }],
        });
      }
      if (url === '/api/feishu/docs/export/jobs') {
        return jsonResponse({
          job_id: 'job_old',
          state: 'queued',
          phase: 'queued',
          done: 0,
          total: 0,
          version: 1,
          status_url: '/api/feishu/docs/export/jobs/job_old',
          result_url: '/api/feishu/docs/export/jobs/job_old/result',
        }, 202);
      }
      if (String(url).startsWith('/api/feishu/docs/export/jobs/job_old?')) {
        return new Promise(resolve => {
          releaseStatus = () => resolve(jsonResponse({
            job_id: 'job_old',
            state: 'completed',
            phase: 'optimize',
            done: 1,
            total: 1,
            version: 2,
            result_url: '/api/feishu/docs/export/jobs/job_old/result',
          }));
        });
      }
      if (url === '/api/feishu/docs/export/jobs/job_old' ) {
        return jsonResponse({ state: 'canceled' });
      }
      throw new Error(`unexpected request: ${url}`);
    },
    createProgressTracker: () => tracker,
    getHandleFile: () => async () => {
      handoffs++;
      return true;
    },
    FileCtor: window.File,
    debounceMs: 0,
  });

  input(window, document.getElementById('urlInput'), '@旧文档');
  await new Promise(resolve => setTimeout(resolve, 8));
  document.querySelector('[data-feishu-doc-result]').click();
  await new Promise(resolve => setTimeout(resolve, 4));
  controller.invalidate();
  releaseStatus?.();
  await new Promise(resolve => setTimeout(resolve, 8));

  assert.ok(trackerCalls.some(([kind]) => kind === 'cancel'));
  assert.equal(trackerCalls.some(([kind]) => kind === 'commit'), false);
  assert.equal(handoffs, 0);
  assert.equal(document.getElementById('progress').style.display, 'none');
});
