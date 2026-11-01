import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { createAdminFailedJobsController, failedStageLabel } from '../public/js/admin-failed-jobs.js';

async function waitFor(predicate, timeout = 1000) {
  const started = Date.now();
  while (!predicate()) {
    if (Date.now() - started > timeout) throw new Error('等待失败任务状态超时');
    await new Promise(resolve => setTimeout(resolve, 5));
  }
}

function mount({ jobs, confirm = () => true }) {
  const { document } = parseHTML('<div id="overlay"></div>');
  const overlay = document.querySelector('#overlay');
  const calls = [];
  let list = jobs;
  const request = async (path, init = {}) => {
    calls.push({ path, method: init.method || 'GET', body: init.body || '' });
    if (path === '/api/publish/failed-jobs') return Response.json({ jobs: list });
    const match = path.match(/^\/api\/publish\/jobs\/(\d+)\/(retry|abandon)$/);
    if (match && init.method === 'POST') {
      list = list.filter(job => job.id !== Number(match[1]));
      return Response.json({ job: { id: Number(match[1]) } });
    }
    return Response.json({ error: 'not found' }, { status: 404 });
  };
  const messages = [];
  let controller;
  const rerender = () => { overlay.innerHTML = controller.render(); controller.bind(); };
  controller = createAdminFailedJobsController({ getOverlay: () => overlay, rerender, request, confirm, onMessage: text => messages.push(text) });
  rerender();
  return { overlay, calls, messages };
}

const mirrorJob = {
  id: 296, title: '“包装界奥斯卡”Pentawards2026', source_url: 'https://mp.weixin.qq.com/s/x',
  failed_stage: 'mirroring', error: '以下图片重试后仍转存失败', created_at: '2026-10-18T02:00:00Z',
  retryable: true, abandonable: true,
};

test('失败任务：列出失败稿，点重试调现成的 retry 接口并刷新列表', async () => {
  const { overlay, calls, messages } = mount({ jobs: [mirrorJob] });
  await waitFor(() => overlay.querySelector('[data-failed-job="296"]'));
  assert.match(overlay.textContent, /#296/);
  assert.match(overlay.textContent, /搬图失败 · 10-18 10:00 进来/);
  assert.match(overlay.textContent, /以下图片重试后仍转存失败/);
  overlay.querySelector('[data-failed-action="retry"]').click();
  await waitFor(() => overlay.textContent.includes('没有失败的任务'));
  const post = calls.find(call => call.method === 'POST');
  assert.equal(post.path, '/api/publish/jobs/296/retry');
  assert.ok(messages.includes('#296 已重新开始处理'));
});

test('失败任务：放弃要先确认，取消就不调接口', async () => {
  let asked = '';
  let answer = false;
  const { overlay, calls } = mount({ jobs: [mirrorJob], confirm: text => { asked = text; return answer; } });
  await waitFor(() => overlay.querySelector('[data-failed-action="abandon"]'));
  overlay.querySelector('[data-failed-action="abandon"]').click();
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.match(asked, /确定放弃 #296/);
  assert.equal(calls.filter(call => call.method === 'POST').length, 0, '取消确认不应放弃');
  answer = true;
  overlay.querySelector('[data-failed-action="abandon"]').click();
  await waitFor(() => calls.some(call => call.method === 'POST'));
  const post = calls.find(call => call.method === 'POST');
  assert.equal(post.path, '/api/publish/jobs/296/abandon');
  assert.equal(JSON.parse(post.body).reason, '在管理后台放弃');
});

test('失败任务：服务端说不能重试、不能放弃的，不出对应按钮', async () => {
  const { overlay } = mount({ jobs: [{ ...mirrorJob, id: 12, failed_stage: 'unpublishing', retryable: false, abandonable: false }] });
  await waitFor(() => overlay.querySelector('[data-failed-job="12"]'));
  assert.equal(overlay.querySelector('[data-failed-action="retry"]'), null);
  assert.equal(overlay.querySelector('[data-failed-action="abandon"]'), null);
  assert.equal(failedStageLabel('unpublishing'), '下线失败');
});
