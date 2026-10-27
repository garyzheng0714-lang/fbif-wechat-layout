// 载体胶囊（文章 / 贴图，2026-10-27 用户裁定）：只在公众号编辑器，排在账号旁边；
// 文章 = 账号自己的模板，贴图 = templates/sticker.js；偏好存 localStorage，切账号、刷新都保留。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { parseHTML } from 'linkedom';
import { createCarrier, readStoredCarrier, CARRIER_STORAGE_KEY } from '../public/js/carrier.js';

function memoryStorage(initial = {}) {
  const items = new Map(Object.entries(initial));
  return {
    getItem: key => (items.has(key) ? items.get(key) : null),
    setItem: (key, value) => { items.set(key, String(value)); },
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function mounted(carrier) {
  const { document } = parseHTML('<!doctype html><html><body><div id="carrierSwitch"></div></body></html>');
  const container = document.getElementById('carrierSwitch');
  carrier.mount(container);
  const option = id => container.querySelector(`[data-carrier="${id}"]`);
  return { document, container, option };
}

function extractFunction(source, name) {
  const functionStart = source.indexOf(`function ${name}`);
  assert.notEqual(functionStart, -1, `${name} should exist`);
  const start = source.slice(functionStart - 6, functionStart) === 'async ' ? functionStart - 6 : functionStart;
  const bodyStart = source.indexOf(') {', start) + 2;
  let depth = 0;
  for (let i = bodyStart; i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}') depth--;
    if (depth === 0) return source.slice(start, i + 1);
  }
  assert.fail(`${name} function body should be complete`);
}

test('默认是文章；记住上次选的载体，存的值不认识就回到文章', () => {
  assert.equal(readStoredCarrier(memoryStorage()), 'article');
  assert.equal(readStoredCarrier(memoryStorage({ [CARRIER_STORAGE_KEY]: 'sticker' })), 'sticker');
  assert.equal(readStoredCarrier(memoryStorage({ [CARRIER_STORAGE_KEY]: 'video' })), 'article');
  assert.equal(readStoredCarrier(null), 'article');

  const restored = createCarrier({ storage: memoryStorage({ [CARRIER_STORAGE_KEY]: 'sticker' }) });
  assert.equal(restored.value, 'sticker');
  assert.equal(restored.templateFor('fbif'), 'sticker', '贴图与账号无关，固定用贴图模板');
  assert.equal(restored.templateFor('foodtalks-series'), 'sticker');

  const fresh = createCarrier({ storage: memoryStorage() });
  assert.equal(fresh.templateFor('fbif'), 'fbif', '文章用账号自己的模板');
  assert.equal(fresh.templateFor('foodtalks-series'), 'foodtalks-series');
});

test('胶囊是单选组：两个选项、当前项 aria-checked，点击切换并存下来、换一次重排一次', async () => {
  const storage = memoryStorage();
  const changes = [];
  const carrier = createCarrier({ storage, onChange: async () => { changes.push(carrier.value); return true; } });
  const { container, option } = mounted(carrier);

  assert.equal(container.getAttribute('role'), 'radiogroup');
  assert.deepEqual([...container.querySelectorAll('[role="radio"]')].map(b => b.textContent), ['文章', '贴图']);
  assert.equal(option('article').getAttribute('aria-checked'), 'true');
  assert.equal(option('sticker').getAttribute('aria-checked'), 'false');
  assert.equal(option('article').getAttribute('tabindex'), '0', '当前项可 Tab 进入');
  assert.equal(option('sticker').getAttribute('tabindex'), '-1');

  option('sticker').click();
  assert.equal(option('sticker').getAttribute('aria-checked'), 'true', '点下去立刻显示选中');
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(storage.getItem(CARRIER_STORAGE_KEY), 'sticker');
  assert.deepEqual(changes, ['sticker']);

  option('sticker').click();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(changes, ['sticker'], '点已选中的那项不重排');
});

test('键盘：方向键在两项间切换并移焦点，Home/End 到头尾', async () => {
  const carrier = createCarrier({ storage: memoryStorage(), onChange: async () => true });
  const { document, option } = mounted(carrier);
  const focused = [];
  for (const id of ['article', 'sticker']) option(id).focus = () => { focused.push(id); };
  const press = key => {
    const event = new document.defaultView.Event('keydown', { bubbles: true, cancelable: true });
    event.key = key;
    option(carrier.value).dispatchEvent(event);
    return event;
  };

  assert.equal(press('ArrowRight').defaultPrevented, true);
  assert.equal(carrier.value, 'sticker');
  assert.equal(option('sticker').getAttribute('aria-checked'), 'true');
  assert.deepEqual(focused, ['sticker'], '焦点跟着选中项走');
  press('ArrowRight');
  assert.equal(carrier.value, 'article', '到尾再按回到头');
  press('End');
  assert.equal(carrier.value, 'sticker');
  press('Home');
  assert.equal(carrier.value, 'article');
  assert.equal(press('Tab').defaultPrevented, false, '别的键不拦');
});

test('过期的切换不提交：快速 文章→贴图→文章，只有最后一次算数', async () => {
  const storage = memoryStorage();
  const pending = [];
  const carrier = createCarrier({
    storage,
    onChange: ({ stale }) => {
      const task = deferred();
      pending.push({ task, stale, target: carrier.value });
      return task.promise;
    },
  });

  const toSticker = carrier.select('sticker');
  const backToArticle = carrier.select('article');
  assert.equal(pending[0].stale(), true, '第一次切换在第二次开始时就过期了');
  assert.equal(pending[1].stale(), false);
  pending[1].task.resolve(true);
  pending[0].task.resolve(true);
  assert.equal(await toSticker, 'superseded');
  assert.equal(await backToArticle, 'committed');
  assert.equal(carrier.value, 'article');
  assert.equal(storage.getItem(CARRIER_STORAGE_KEY), 'article');
});

test('切换失败回到原载体，存的值也回去', async () => {
  const storage = memoryStorage();
  const carrier = createCarrier({ storage, onChange: async () => { throw new Error('模板加载失败'); } });
  const { option } = mounted(carrier);
  const warn = console.warn;
  console.warn = () => {};
  try {
    assert.equal(await carrier.select('sticker'), 'failed');
  } finally {
    console.warn = warn;
  }
  assert.equal(carrier.value, 'article');
  assert.equal(storage.getItem(CARRIER_STORAGE_KEY), 'article');
  assert.equal(option('article').getAttribute('aria-checked'), 'true');
});

test('app.html：胶囊在导入卡第一行、账号旁边，只有公众号编辑器有', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const site = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const row = app.slice(app.indexOf('<div class="import-destination">'), app.indexOf('data-url-focus-target'));
  assert.match(row, /id="acctMenu"[^>]*><\/div>\s*<div class="carrier-switch" id="carrierSwitch"><\/div>/);
  assert.match(app, /createCarrier\(/);
  assert.doesNotMatch(site, /carrier/i, '网站编辑器没有载体');
});

// loadTemplate 是 app.html 里账号切换与载体切换共用的那一步：账号给模板，载体决定实际装哪个。
function loadTemplateHarness({ carrierValue }) {
  return readFile(new URL('../public/app.html', import.meta.url), 'utf8').then(app => {
    const source = extractFunction(app, 'loadTemplate');
    const imports = new Map();
    const engineTemplates = [];
    const carrierState = { value: carrierValue };
    const deps = {
      currentTemplateId: 'fbif',
      carrier: { templateFor: id => (carrierState.value === 'sticker' ? 'sticker' : id) },
      syncEngineConfig() {},
      initApp() {},
      wrapEngineFunctions() {},
      window: { _engineApi: { async switchTemplate(template) { engineTemplates.push(template.id); } } },
      importTemplateModule(id) {
        const task = deferred();
        imports.set(id, task);
        return task.promise;
      },
    };
    const loadTemplate = new Function('deps', `with (deps) { ${source}; return loadTemplate; }`)(deps);
    return { deps, loadTemplate, imports, engineTemplates, carrierState };
  });
}

test('贴图载体下换账号仍装贴图模板，记下的是账号模板（切回文章时用）', async () => {
  const { deps, loadTemplate, imports, engineTemplates } = await loadTemplateHarness({ carrierValue: 'sticker' });
  const loading = loadTemplate('foodtalks-series', { rerender: false });
  assert.deepEqual([...imports.keys()], ['sticker']);
  imports.get('sticker').resolve({ default: { id: 'sticker' } });
  assert.equal(await loading, true);
  assert.deepEqual(engineTemplates, ['sticker']);
  assert.equal(deps.currentTemplateId, 'foodtalks-series');
});

test('加载模板时载体又切了：不把旧载体的模板写进引擎，按新载体重装', async () => {
  const { loadTemplate, imports, engineTemplates, carrierState } = await loadTemplateHarness({ carrierValue: 'article' });
  const loading = loadTemplate('fbif', { rerender: true });
  carrierState.value = 'sticker';
  imports.get('fbif').resolve({ default: { id: 'fbif' } });
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(engineTemplates, [], '旧载体的文章模板不得进引擎');
  imports.get('sticker').resolve({ default: { id: 'sticker' } });
  assert.equal(await loading, true);
  assert.deepEqual(engineTemplates, ['sticker']);
});
