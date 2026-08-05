import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { createAdminDeliveryController } from '../public/js/admin-delivery.js';

async function waitFor(predicate, timeout = 1000) {
  const started = Date.now();
  while (!predicate()) {
    if (Date.now() - started > timeout) throw new Error('等待发布链路状态超时');
    await new Promise(resolve => setTimeout(resolve, 5));
  }
}

test('发布链路：选择应用与群后保存，并可重新读取服务端结果', async () => {
  const { document, window } = parseHTML('<div id="overlay"></div>');
  const overlay = document.querySelector('#overlay');
  const calls = [];
  let persisted = { app_profile_id: '', chat_ids: [] };
  const request = async (path, init = {}) => {
    calls.push({ path, method: init.method || 'GET', body: init.body || '' });
    if (path === '/api/publish/delivery-settings' && init.method === 'PUT') {
      persisted = JSON.parse(init.body);
      return Response.json({
        settings: persisted,
        apps: [{ id: 'review-app', name: '内容审核应用' }],
      });
    }
    if (path === '/api/publish/delivery-settings') {
      return Response.json({
        settings: persisted,
        apps: [{ id: 'review-app', name: '内容审核应用' }],
      });
    }
    if (path === '/api/feishu/chats') {
      return Response.json({ chats: [{ chat_id: 'oc_review', name: '内容审核群' }] });
    }
    return Response.json({ error: 'not found' }, { status: 404 });
  };

  let controller;
  const rerender = () => {
    const footer = controller.footerState();
    overlay.innerHTML = `${controller.render()}<button type="button" data-delivery-action="save" ${footer.disabled ? 'disabled' : ''}>${footer.label}</button>`;
    controller.bind();
  };
  controller = createAdminDeliveryController({ getOverlay: () => overlay, rerender, request });
  rerender();
  await waitFor(() => calls.length >= 2 && !controller.footerState().message.includes('读取发布链路'));

  const appSelect = overlay.querySelector('[data-delivery-app]');
  assert.ok(appSelect, '成功读取应用后应显示选择控件');
  appSelect.querySelectorAll('option[selected]').forEach(option => option.removeAttribute('selected'));
  appSelect.querySelector('option[value="review-app"]').setAttribute('selected', '');
  appSelect.dispatchEvent(new window.Event('change'));

  const chatCheckbox = overlay.querySelector('[data-delivery-chat="oc_review"]');
  assert.ok(chatCheckbox, '成功读取群后应显示多选项');
  chatCheckbox.setAttribute('checked', '');
  chatCheckbox.dispatchEvent(new window.Event('change'));
  assert.equal(controller.footerState().disabled, false, '应用和群均选择后保存动作应可用');
  assert.equal(controller.footerState().dirty, true);

  overlay.querySelector('[data-delivery-action="save"]').click();
  await waitFor(() => calls.some(call => call.method === 'PUT') && controller.footerState().message.includes('已保存'));
  assert.deepEqual(persisted, { app_profile_id: 'review-app', chat_ids: ['oc_review'] });
  assert.equal(controller.footerState().dirty, false);
  assert.equal(controller.footerState().disabled, true, '保存回读后不应允许重复提交相同配置');

  const getCountBeforeRefresh = calls.filter(call => call.method === 'GET').length;
  overlay.querySelector('[data-delivery-action="refresh"]').click();
  await waitFor(() => calls.filter(call => call.method === 'GET').length >= getCountBeforeRefresh + 2 && !controller.footerState().message.includes('更新'));
  assert.equal(controller.footerState().dirty, false);
  assert.match(overlay.textContent, /内容审核应用/);
  assert.match(overlay.textContent, /内容审核群/);
});

test('发布链路：服务离线时不把未知状态误写成尚未配置', async () => {
  const { document } = parseHTML('<div id="overlay"></div>');
  const overlay = document.querySelector('#overlay');
  let controller;
  const rerender = () => {
    overlay.innerHTML = controller.render();
    controller.bind();
  };
  controller = createAdminDeliveryController({
    getOverlay: () => overlay,
    rerender,
    request: async () => { throw new TypeError('Failed to fetch'); },
  });
  rerender();
  await waitFor(() => controller.footerState().message.includes('发布服务不可达'));

  assert.equal(overlay.querySelectorAll('[data-delivery-issue="network"]').length, 1);
  assert.equal(overlay.textContent.match(/等待发布服务恢复/g)?.length, 2);
  assert.doesNotMatch(overlay.textContent, /尚未配置推送应用|Failed to fetch/);
  assert.equal(controller.footerState().disabled, true);
});
