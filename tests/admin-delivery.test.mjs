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
  assert.deepEqual(persisted, { app_profile_id: 'review-app', chat_ids: ['oc_review'], auto_publish: false });
  assert.equal(controller.footerState().dirty, false);
  assert.equal(controller.footerState().disabled, true, '保存回读后不应允许重复提交相同配置');

  const getCountBeforeRefresh = calls.filter(call => call.method === 'GET').length;
  overlay.querySelector('[data-delivery-action="refresh"]').click();
  await waitFor(() => calls.filter(call => call.method === 'GET').length >= getCountBeforeRefresh + 2 && !controller.footerState().message.includes('更新'));
  assert.equal(controller.footerState().dirty, false);
  assert.match(overlay.textContent, /内容审核应用/);
  assert.match(overlay.textContent, /内容审核群/);
});

test('发布链路：打开自动发布后保存到服务端', async () => {
  const { document } = parseHTML('<div id="overlay"></div>');
  const overlay = document.querySelector('#overlay');
  const calls = [];
  let persisted = { app_profile_id: 'review-app', chat_ids: ['oc_review'], auto_publish: false };
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
  await waitFor(() => overlay.querySelector('[data-delivery-auto-publish]') && !controller.footerState().message.includes('读取发布链路'));

  const toggle = overlay.querySelector('[data-delivery-auto-publish]');
  assert.equal(toggle.getAttribute('aria-checked'), 'false');
  toggle.click();
  await waitFor(() => overlay.querySelector('[data-delivery-auto-publish]')?.getAttribute('aria-checked') === 'true');
  assert.equal(controller.footerState().dirty, true);
  assert.equal(controller.footerState().disabled, false);

  overlay.querySelector('[data-delivery-action="save"]').click();
  await waitFor(() => persisted.auto_publish === true && controller.footerState().dirty === false);
  assert.equal(persisted.app_profile_id, 'review-app');
  assert.deepEqual(persisted.chat_ids, ['oc_review']);
});

test('发布链路：群列表失败时仍能保存自动发布', async () => {
  const { document } = parseHTML('<div id="overlay"></div>');
  const overlay = document.querySelector('#overlay');
  const calls = [];
  let persisted = { app_profile_id: 'review-app', chat_ids: ['oc_review'], auto_publish: false };
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
      return Response.json({ error: '飞书云文档服务暂不可用' }, { status: 502 });
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
  await waitFor(() => overlay.querySelector('[data-delivery-auto-publish]') && overlay.textContent.includes('读取飞书群列表失败'));

  assert.equal(controller.footerState().disabled, true, '未改动时不应保存');
  overlay.querySelector('[data-delivery-auto-publish]').click();
  await waitFor(() => overlay.querySelector('[data-delivery-auto-publish]')?.getAttribute('aria-checked') === 'true');
  assert.equal(controller.footerState().disabled, false, '群列表失败时仍应能保存自动发布');
  assert.match(controller.footerState().message, /仍用已保存的审核群/);

  overlay.querySelector('[data-delivery-action="save"]').click();
  await waitFor(() => persisted.auto_publish === true);
  assert.deepEqual(persisted.chat_ids, ['oc_review']);
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
  assert.equal(overlay.textContent.match(/等待发布服务恢复/g)?.length, 3);
  assert.doesNotMatch(overlay.textContent, /尚未配置推送应用|Failed to fetch/);
  assert.equal(controller.footerState().disabled, true);
});

test('发布链路：分发负责人从审核群成员里选，单独保存', async () => {
  const { document, window } = parseHTML('<div id="overlay"></div>');
  const overlay = document.querySelector('#overlay');
  const calls = [];
  let savedOwners = [];
  const settings = { app_profile_id: 'review-app', chat_ids: ['oc_review'], auto_publish: false };
  const request = async (path, init = {}) => {
    calls.push({ path, method: init.method || 'GET', body: init.body || '' });
    if (path === '/api/publish/delivery-settings') {
      return Response.json({ settings, apps: [{ id: 'review-app', name: '内容审核应用' }] });
    }
    if (path === '/api/feishu/chats') {
      return Response.json({ chats: [{ chat_id: 'oc_review', name: '内容审核群' }] });
    }
    if (path === '/api/publish/delivery-settings/distribution-owners' && init.method === 'PUT') {
      const ids = JSON.parse(init.body).open_ids;
      savedOwners = ids.map(id => ({ open_id: id, name: id === 'ou_a' ? '王编辑' : '李编辑' }));
      return Response.json({ settings, distribution_owners: { owners: savedOwners, stale: false } });
    }
    if (path === '/api/publish/delivery-settings/distribution-owners') {
      return Response.json({
        owners: savedOwners, stale: false,
        candidates: [{ open_id: 'ou_a', name: '王编辑' }, { open_id: 'ou_b', name: '李编辑' }],
      });
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
  await waitFor(() => overlay.querySelector('[data-delivery-owner="ou_a"]'));
  assert.match(overlay.textContent, /分发负责人/);
  assert.equal(controller.footerState().dirty, false);

  const owner = overlay.querySelector('[data-delivery-owner="ou_a"]');
  owner.setAttribute('checked', '');
  owner.dispatchEvent(new window.Event('change'));
  assert.equal(controller.footerState().dirty, true);
  assert.equal(controller.footerState().disabled, false);

  overlay.querySelector('[data-delivery-action="save"]').click();
  await waitFor(() => controller.footerState().message.includes('已保存'));
  const puts = calls.filter(call => call.method === 'PUT');
  assert.deepEqual(puts.map(call => call.path), ['/api/publish/delivery-settings/distribution-owners'], '只改负责人时不重写其他设置');
  assert.deepEqual(JSON.parse(puts[0].body), { open_ids: ['ou_a'] });
  assert.equal(controller.footerState().dirty, false);
});

test('发布链路：成员读不到时不挡其他设置保存', async () => {
  const { document } = parseHTML('<div id="overlay"></div>');
  const overlay = document.querySelector('#overlay');
  const request = async path => {
    if (path === '/api/publish/delivery-settings') {
      return Response.json({ settings: { app_profile_id: 'review-app', chat_ids: ['oc_review'] }, apps: [{ id: 'review-app', name: '内容审核应用' }] });
    }
    if (path === '/api/feishu/chats') return Response.json({ chats: [{ chat_id: 'oc_review', name: '内容审核群' }] });
    return Response.json({ error: '读取群成员失败 code=99991672' }, { status: 503 });
  };
  let controller;
  const rerender = () => {
    overlay.innerHTML = controller.render();
    controller.bind();
  };
  controller = createAdminDeliveryController({ getOverlay: () => overlay, rerender, request });
  rerender();
  await waitFor(() => overlay.textContent.includes('读取审核群成员失败'));
  assert.equal(controller.footerState().message, '当前发布链路已保存');
});
