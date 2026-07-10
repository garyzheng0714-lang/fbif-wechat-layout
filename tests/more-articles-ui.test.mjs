import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { parseHTML } from 'linkedom';
import {
  buildCoverPickerSources,
  coverPickerImageSource,
  initMoreArticlesEditor,
} from '../public/js/more-articles-ui.js';
import {
  isValidMoreArticleUrl,
  shouldAutoFetchMoreArticleUrl,
} from '../public/js/more-articles-meta.js';

test('cover picker loads page-local article images directly', () => {
  assert.deepEqual(
    coverPickerImageSource('blob:http://127.0.0.1:19092/abc'),
    { src: 'blob:http://127.0.0.1:19092/abc', crossOrigin: false }
  );
  assert.deepEqual(
    coverPickerImageSource('/api/doc-cache/hash/image.png'),
    { src: '/api/doc-cache/hash/image.png', crossOrigin: false }
  );
});

test('cover picker loads same-origin absolute URLs directly', () => {
  const previous = globalThis.location;
  Object.defineProperty(globalThis, 'location', {
    value: { origin: 'http://127.0.0.1:19092' },
    configurable: true,
  });
  try {
    assert.deepEqual(
      coverPickerImageSource('http://127.0.0.1:19092/api/doc-cache/hash/image.png'),
      { src: 'http://127.0.0.1:19092/api/doc-cache/hash/image.png', crossOrigin: false }
    );
  } finally {
    if (previous === undefined) {
      delete globalThis.location;
    } else {
      Object.defineProperty(globalThis, 'location', { value: previous, configurable: true });
    }
  }
});

test('cover picker proxies remote article images for canvas access', () => {
  assert.deepEqual(
    coverPickerImageSource('https://mmbiz.qpic.cn/sz_mmbiz_jpg/cover/0?wx_fmt=jpeg'),
    {
      src: '/api/image-proxy?url=https%3A%2F%2Fmmbiz.qpic.cn%2Fsz_mmbiz_jpg%2Fcover%2F0%3Fwx_fmt%3Djpeg',
      crossOrigin: true,
    }
  );
  assert.deepEqual(
    coverPickerImageSource('//mmbiz.qpic.cn/sz_mmbiz_jpg/cover/0?wx_fmt=jpeg'),
    {
      src: '/api/image-proxy?url=https%3A%2F%2Fmmbiz.qpic.cn%2Fsz_mmbiz_jpg%2Fcover%2F0%3Fwx_fmt%3Djpeg',
      crossOrigin: true,
    }
  );
});

test('cover picker uses the current more-article media library before main article images', () => {
  const sources = buildCoverPickerSources(
    {
      cover_data_url: 'data:image/jpeg;base64,custom',
      imgurl: 'https://mmbiz.qpic.cn/cover.jpg?wx_fmt=jpeg',
      image_urls: [
        'https://mmbiz.qpic.cn/body-1.jpg?wx_fmt=jpeg',
        'https://mmbiz.qpic.cn/cover.jpg?wx_fmt=jpeg',
      ],
    },
    [
      'https://mmbiz.qpic.cn/body-2.jpg?wx_fmt=jpeg',
      'https://mmbiz.qpic.cn/body-1.jpg?wx_fmt=jpeg',
    ]
  );

  assert.deepEqual(sources, [
    'data:image/jpeg;base64,custom',
    'https://mmbiz.qpic.cn/cover.jpg?wx_fmt=jpeg',
    'https://mmbiz.qpic.cn/body-1.jpg?wx_fmt=jpeg',
    'https://mmbiz.qpic.cn/body-2.jpg?wx_fmt=jpeg',
  ]);
});

test('more article URL input auto-fetches when mobile paste only fires input', async () => {
  assert.equal(
    shouldAutoFetchMoreArticleUrl('https://mp.weixin.qq.com/s/mobile-input-only', ''),
    true
  );
  assert.equal(
    shouldAutoFetchMoreArticleUrl(
      'https://mp.weixin.qq.com/s/mobile-input-only',
      'https://mp.weixin.qq.com/s/mobile-input-only'
    ),
    false
  );

  const source = await readFile(new URL('../public/js/more-articles-ui.js', import.meta.url), 'utf8');
  const inputHandler = source.slice(source.indexOf("$('url').addEventListener('input'"));
  assert.match(inputHandler, /scheduleAutoFetch\(\)/);
});

test('successful more-article metadata fetch commits the new card to the live preview', async () => {
  const { document, window } = parseHTML(`<!doctype html><html><body>
    <button id="maResetBtn" type="button"></button>
    <div id="moreArticlesEditor"></div>
  </body></html>`);
  const saved = new Map();
  const previewCalls = [];
  const storage = {
    getItem(key) { return saved.get(key) ?? null; },
    setItem(key, value) { saved.set(key, String(value)); },
    removeItem(key) { saved.delete(key); },
  };
  const globalKeys = ['window', 'document', 'localStorage', 'location', 'fetch', 'confirm'];
  const previous = Object.fromEntries(globalKeys.map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  const previousWarn = console.warn;
  console.warn = () => {};
  Object.assign(globalThis, {
    window,
    document,
    localStorage: storage,
    location: { pathname: '/mp/fbif', origin: 'http://localhost:19090' },
    confirm: () => true,
    fetch: async () => ({
      ok: true,
      async json() {
        return {
          source_url: 'https://mp.weixin.qq.com/s/live-preview-new',
          title: '新增文章必须实时出现在预览',
          cover_url: 'https://mmbiz.qpic.cn/live-preview-new.jpg',
          image_urls: ['https://mmbiz.qpic.cn/live-preview-body.jpg'],
        };
      },
    }),
  });
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true });
  window._activeConfig = {
    config: {
      more_articles_enabled: true,
      more_articles_default_cards: [{
        href: 'https://mp.weixin.qq.com/s/live-preview-old',
        title: '旧文章',
        imgurl: 'https://mmbiz.qpic.cn/live-preview-old.jpg',
      }],
    },
  };
  window._updateMoreArticles = async cards => { previewCalls.push(structuredClone(cards)); };

  try {
    initMoreArticlesEditor();
    const input = document.querySelector('.ma-url');
    assert.ok(input, '应渲染默认卡片链接框');
    input.value = 'https://mp.weixin.qq.com/s/live-preview-new';
    input.dispatchEvent(new window.Event('input', { bubbles: true }));
    await new Promise(resolve => setTimeout(resolve, 900));

    const latest = previewCalls.at(-1)?.[0];
    assert.equal(document.querySelector('.ma-card-status')?.textContent, '✓ 已抓取');
    assert.equal(latest?.href, 'https://mp.weixin.qq.com/s/live-preview-new');
    assert.equal(latest?.title, '新增文章必须实时出现在预览');
    assert.equal(latest?.imgurl, 'https://mmbiz.qpic.cn/live-preview-new.jpg');
    assert.deepEqual(latest?.image_urls, ['https://mmbiz.qpic.cn/live-preview-body.jpg']);
  } finally {
    console.warn = previousWarn;
    for (const [key, descriptor] of Object.entries(previous)) {
      if (descriptor === undefined) delete globalThis[key];
      else Object.defineProperty(globalThis, key, descriptor);
    }
  }
});

test('more-article metadata is latest-wins and stays bound to card identity across delete/reorder', async () => {
  const { document, window } = parseHTML(`<!doctype html><html><body>
    <button id="maResetBtn" type="button"></button>
    <div id="moreArticlesEditor"></div>
  </body></html>`);
  const saved = new Map();
  const pending = new Map();
  const previewCalls = [];
  const storage = {
    getItem(key) { return saved.get(key) ?? null; },
    setItem(key, value) { saved.set(key, String(value)); },
    removeItem(key) { saved.delete(key); },
  };
  const globalKeys = ['window', 'document', 'localStorage', 'location', 'fetch', 'confirm'];
  const previous = Object.fromEntries(globalKeys.map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  const previousWarn = console.warn;
  console.warn = () => {};
  Object.assign(globalThis, {
    window,
    document,
    localStorage: storage,
    location: { pathname: '/mp/fbif', origin: 'http://localhost:19090' },
    confirm: () => true,
    fetch: async (_endpoint, options) => {
      const url = JSON.parse(options.body).url;
      return new Promise(resolve => pending.set(url, resolve));
    },
  });
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true });
  let sortableOnEnd = null;
  window.Sortable = {
    create(_container, options) {
      sortableOnEnd = options.onEnd;
      return { destroy() {} };
    },
  };
  window._activeConfig = {
    config: {
      more_articles_enabled: true,
      more_articles_default_cards: [{
        href: 'https://mp.weixin.qq.com/s/latest-wins-initial',
        title: '初始文章',
        imgurl: 'https://mmbiz.qpic.cn/latest-wins-initial.jpg',
      }],
    },
  };
  window._updateMoreArticles = async cards => { previewCalls.push(structuredClone(cards)); };

  const respond = (url, suffix) => pending.get(url)({
    ok: true,
    async json() {
      return {
        source_url: url,
        title: `文章 ${suffix}`,
        cover_url: `https://mmbiz.qpic.cn/latest-wins-${suffix}.jpg`,
        image_urls: [`https://mmbiz.qpic.cn/latest-wins-${suffix}-body.jpg`],
      };
    },
  });
  const flush = async () => {
    for (let i = 0; i < 8; i++) await Promise.resolve();
    await new Promise(resolve => setTimeout(resolve, 0));
  };

  try {
    initMoreArticlesEditor();
    const input = document.querySelector('.ma-url');
    const urlA = 'https://mp.weixin.qq.com/s/latest-wins-a';
    const urlB = 'https://mp.weixin.qq.com/s/latest-wins-b';
    const pressEnter = () => {
      const event = new window.Event('keydown', { bubbles: true });
      Object.defineProperty(event, 'key', { value: 'Enter' });
      input.dispatchEvent(event);
    };
    input.value = urlA;
    input.dispatchEvent(new window.Event('input', { bubbles: true }));
    pressEnter();
    input.value = urlB;
    input.dispatchEvent(new window.Event('input', { bubbles: true }));
    pressEnter();
    assert.ok(pending.has(urlA));
    assert.ok(pending.has(urlB));

    respond(urlB, 'B');
    await flush();
    respond(urlA, 'A');
    await flush();

    const latest = previewCalls.at(-1)?.[0];
    assert.equal(input.value, urlB, '旧响应不得改回输入框');
    assert.equal(latest?.href, urlB, '预览必须保留最新链接');
    assert.equal(latest?.title, '文章 B');
    const storedCards = JSON.parse(saved.get('more_articles_v1::fbif'));
    assert.equal(storedCards[0].href, urlB, '持久化状态必须保留最新链接');
    assert.equal(storedCards[0].title, '文章 B');

    // 删除时 C 还在请求；随后新卡复用 index=0 并完成 D。C 后返不得落到 D。
    const urlC = 'https://mp.weixin.qq.com/s/latest-wins-deleted-c';
    input.value = urlC;
    input.dispatchEvent(new window.Event('input', { bubbles: true }));
    pressEnter();
    document.querySelector('.ma-remove-btn').dispatchEvent(new window.Event('click', { bubbles: true }));
    const replacementInput = document.querySelector('.ma-url');
    const urlD = 'https://mp.weixin.qq.com/s/latest-wins-replacement-d';
    replacementInput.value = urlD;
    replacementInput.dispatchEvent(new window.Event('input', { bubbles: true }));
    const replacementEnter = new window.Event('keydown', { bubbles: true });
    Object.defineProperty(replacementEnter, 'key', { value: 'Enter' });
    replacementInput.dispatchEvent(replacementEnter);
    respond(urlD, 'D');
    await flush();
    respond(urlC, 'C');
    await flush();
    const afterReuse = JSON.parse(saved.get('more_articles_v1::fbif'));
    assert.equal(replacementInput.value, urlD);
    assert.equal(afterReuse[0].href, urlD, '删除卡的旧响应不得污染复用下标的新卡');
    assert.equal(afterReuse[0].title, '文章 D');

    // 拖拽只改变位置，不改变卡片身份；E 后返应追随原卡到新的 index=1。
    document.querySelector('.ma-add-btn').dispatchEvent(new window.Event('click', { bubbles: true }));
    assert.equal(typeof sortableOnEnd, 'function');
    const sortableCards = Array.from(document.querySelectorAll('.ma-card'));
    const trackedInput = sortableCards[0].querySelector('.ma-url');
    const urlE = 'https://mp.weixin.qq.com/s/latest-wins-reordered-e';
    trackedInput.value = urlE;
    trackedInput.dispatchEvent(new window.Event('input', { bubbles: true }));
    const trackedEnter = new window.Event('keydown', { bubbles: true });
    Object.defineProperty(trackedEnter, 'key', { value: 'Enter' });
    trackedInput.dispatchEvent(trackedEnter);
    document.getElementById('moreArticlesEditor').insertBefore(sortableCards[1], sortableCards[0]);
    sortableOnEnd();
    respond(urlE, 'E');
    await flush();
    const afterReorder = JSON.parse(saved.get('more_articles_v1::fbif'));
    assert.equal(afterReorder[1].href, urlE, '响应应跟随卡片对象，不得按旧下标写到别的卡');
    assert.equal(afterReorder[1].title, '文章 E');
    assert.notEqual(afterReorder[0].href, urlE);

    // 同 URL 的底层请求会复用缓存 Promise；清空再粘回必须用新版本重新订阅，
    // 不能因旧 in-flight 标记跳过并永久停在“抓取中”。
    const retryInput = document.querySelector('.ma-url');
    const urlF = 'https://mp.weixin.qq.com/s/latest-wins-clear-and-retry-f';
    retryInput.value = urlF;
    retryInput.dispatchEvent(new window.Event('input', { bubbles: true }));
    const firstFEnter = new window.Event('keydown', { bubbles: true });
    Object.defineProperty(firstFEnter, 'key', { value: 'Enter' });
    retryInput.dispatchEvent(firstFEnter);
    retryInput.value = '';
    retryInput.dispatchEvent(new window.Event('input', { bubbles: true }));
    retryInput.value = urlF;
    retryInput.dispatchEvent(new window.Event('input', { bubbles: true }));
    const retryFEnter = new window.Event('keydown', { bubbles: true });
    Object.defineProperty(retryFEnter, 'key', { value: 'Enter' });
    retryInput.dispatchEvent(retryFEnter);
    respond(urlF, 'F');
    await flush();
    const afterRetry = JSON.parse(saved.get('more_articles_v1::fbif'));
    assert.equal(retryInput.value, urlF);
    assert.equal(afterRetry[0].href, urlF);
    assert.equal(afterRetry[0].title, '文章 F', 'A→清空→A 必须由最新版本提交缓存响应');
    assert.equal(document.querySelector('.ma-card-status')?.textContent, '✓ 已抓取');
  } finally {
    // 等 input 防抖任务收尾后再恢复全局，避免测试结束后异步访问旧 window。
    await new Promise(resolve => setTimeout(resolve, 450));
    console.warn = previousWarn;
    for (const [key, descriptor] of Object.entries(previous)) {
      if (descriptor === undefined) delete globalThis[key];
      else Object.defineProperty(globalThis, key, descriptor);
    }
  }
});

test('more articles editor uses shared SVG icons instead of character glyph controls', async () => {
  const source = await readFile(new URL('../public/js/more-articles-ui.js', import.meta.url), 'utf8');
  assert.match(source, /createIcon/);
  assert.doesNotMatch(source, />⠿</);
  assert.doesNotMatch(source, />×</);
});

test('empty more article thumbnail does not reuse the global empty-state class', async () => {
  const [source, appCss, shellCss] = await Promise.all([
    readFile(new URL('../public/js/more-articles-ui.js', import.meta.url), 'utf8'),
    readFile(new URL('../public/app.css', import.meta.url), 'utf8'),
    readFile(new URL('../public/css/app-shell.css', import.meta.url), 'utf8'),
  ]);

  assert.match(shellCss, /\.empty\s*\{[\s\S]*?position:\s*absolute/);
  assert.match(source, /classList\.add\('is-empty'\)/);
  assert.match(source, /classList\.remove\('is-empty'\)/);
  assert.match(appCss, /\.ma-thumb\.is-empty\s*\{/);
  assert.doesNotMatch(source, /classList\.(?:add|remove)\('empty'\)/);
  assert.doesNotMatch(appCss, /\.ma-thumb\.empty\s*\{/);
});

test('more article URL helper accepts only WeChat article hosts', () => {
  assert.equal(isValidMoreArticleUrl('https://mp.weixin.qq.com/s/example'), true);
  assert.equal(isValidMoreArticleUrl('https://mp.weixin.qq.com/s?__biz=example'), true);
  assert.equal(isValidMoreArticleUrl('https://weixin.qq.com/s/example'), true);
  assert.equal(isValidMoreArticleUrl('https://mp.weixin.qq.com/not-an-article'), false);
  assert.equal(isValidMoreArticleUrl('https://foo.weixin.qq.com/s/example'), false);
  assert.equal(isValidMoreArticleUrl('https://not-wechat.example.com/s/example'), false);
});
