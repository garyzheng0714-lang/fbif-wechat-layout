import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { charCount, counterState, createDistributePage, renderPlatform } from '../public/js/distribute-page.js';

async function waitFor(predicate, timeout = 1000) {
  const started = Date.now();
  while (!predicate()) {
    if (Date.now() - started > timeout) throw new Error('等待改稿页状态超时');
    await new Promise(resolve => setTimeout(resolve, 5));
  }
}

const longTitle = '这家老字号把茶做成零食，166年老店只开6家，年营收1.4亿';

function platformsView(overrides = {}) {
  return {
    job: { id: 196, title: longTitle, site_url: 'https://www.foodtalks.cn/news/63200' },
    platforms: [
      {
        account: 'recToutiao', name: '头条号', status: '待修改', tone: 'orange', editable: true, needs_fix: true,
        title: longTitle, title_min: 8, title_max: 20, title_suggested: '老茶铺把茶做成零食，年营收过亿', title_issue: true,
        summary: '祇园辻利把茶做成零食。', summary_max: 0,
        cover: { url: 'https://cdn.example.com/small.jpg', width: 300, height: 200 },
        cover_issue: '封面太小（至少 452×352，现在 300×200）', cover_min_width: 452, cover_min_height: 352,
        candidates: [{ url: 'https://cdn.example.com/big.jpg', width: 1080, height: 720 }],
        ...overrides,
      },
      {
        account: 'recBaijia', name: '百家号', status: '已发布', tone: 'green', editable: false, needs_fix: false,
        url: 'https://baijiahao.baidu.com/s?id=1', title: '', candidates: [],
      },
    ],
  };
}

test('改稿页：字数与服务端同口径（按字符、合并空白）', () => {
  assert.equal(charCount(' 头条  号 '), 4);
  assert.deepEqual(counterState(longTitle, 20, 8), { length: Array.from(longTitle).length, over: Array.from(longTitle).length - 20, text: `超出 ${Array.from(longTitle).length - 20} 字`, invalid: true });
  assert.equal(counterState('短标题', 20, 8).text, '至少 8 字');
  assert.equal(counterState('', 20).text, '不能为空');
  assert.equal(counterState('刚好合适的一句标题', 20, 8).invalid, false);
});

// 头条号标题英文字母、数字两个算一个字（与 api/distribution_platforms.go titleLength、分发服务 service/limits.mjs 同口径）：
// #289 编辑手发的 42 个字符按此数法正好 30 字，头条收了。
test('改稿页：头条号标题英文数字算半个字', () => {
  const toutiao = { halfWidth: true };
  const tt289 = 'Whole Foods Market发布2027年8大食品趋势！苦味升级、豆类热潮…';
  assert.equal(charCount('Innova揭晓2027全球食品饮料十大趋势，穿越二十年创新周期', true), 27);
  assert.equal(counterState('Innova揭晓2027全球食品饮料十大趋势，穿越二十年创新周期', 30, 15, toutiao).invalid, false);
  assert.equal(charCount(tt289, true), 30);
  assert.equal(counterState(tt289, 30, 15, toutiao).invalid, false);
  assert.deepEqual(counterState(tt289 + '好', 30, 15, toutiao), { length: 31, over: 1, text: '超出 1 字', invalid: true });
});

test('改稿页：写清超了多少、给建议，选大图作封面后保存并同步', async () => {
  const { document, window } = parseHTML('<p id="article"></p><div id="root"></div>');
  globalThis.Event = window.Event;
  const root = document.querySelector('#root');
  const calls = [];
  let current = platformsView();
  const request = async (path, init = {}) => {
    calls.push({ path, method: init.method || 'GET', body: init.body });
    if (init.method === 'PUT') {
      current = platformsView();
      current.platforms[0] = { ...current.platforms[0], status: '同步中', tone: 'blue', editable: false, needs_fix: false, title: '老茶铺把茶做成零食，年营收过亿' };
      return Response.json(current);
    }
    return Response.json(current);
  };
  const page = createDistributePage({ root, articleEl: document.querySelector('#article'), search: '?job=196&t=sig', request, schedule: () => {} });
  await page.load();

  assert.equal(calls[0].path, '/api/publish/jobs/196/external-distribution/draft?t=sig');
  assert.match(document.querySelector('#article').innerHTML, /#196 这家老字号/);
  const toutiao = root.querySelector('[data-account="recToutiao"]');
  assert.match(toutiao.querySelector('.dist-status').textContent, /待修改/);
  const count = toutiao.querySelector('[data-count="title"]');
  assert.equal(count.textContent, `${Array.from(longTitle).length} / 20`);
  assert.ok(count.classList.contains('is-over'));
  assert.match(toutiao.querySelector('[data-hint="title"]').textContent, /超出 \d+ 字/);
  assert.match(toutiao.textContent, /建议：老茶铺把茶做成零食，年营收过亿/);
  assert.match(toutiao.textContent, /现在的封面太小，选一张大图或上传/);
  const small = toutiao.querySelector('[data-cover="https://cdn.example.com/small.jpg"]');
  assert.ok(small.disabled, '太小的图不能选');
  assert.equal(small.getAttribute('aria-checked'), 'false', '太小的现封面不画成选中');
  assert.match(root.querySelector('[data-account="recBaijia"]').innerHTML, /查看文章/);
  assert.equal(root.querySelector('[data-account="recBaijia"] form'), null, '已发布的平台不能再改');

  // 超限时点保存：不发请求，焦点回到标题。
  root.querySelector('[data-account="recToutiao"] form').dispatchEvent(new window.Event('submit', { cancelable: true }));
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(calls.filter(call => call.method === 'PUT').length, 0);

  root.querySelector('[data-account="recToutiao"] [data-use="title"]').click();
  assert.equal(root.querySelector('[data-account="recToutiao"] [name="title"]').value, '老茶铺把茶做成零食，年营收过亿');
  assert.ok(!root.querySelector('[data-account="recToutiao"] [data-count="title"]').classList.contains('is-over'));
  root.querySelector('[data-account="recToutiao"] [data-cover="https://cdn.example.com/big.jpg"]').click();
  assert.equal(root.querySelector('[data-account="recToutiao"] [data-cover="https://cdn.example.com/big.jpg"]').getAttribute('aria-checked'), 'true');
  assert.doesNotMatch(root.querySelector('[data-account="recToutiao"]').textContent, /封面太小/, '选了大图后不再提示');

  root.querySelector('[data-account="recToutiao"] form').dispatchEvent(new window.Event('submit', { cancelable: true }));
  await waitFor(() => calls.some(call => call.method === 'PUT'));
  const put = calls.find(call => call.method === 'PUT');
  assert.deepEqual(JSON.parse(put.body), { account: 'recToutiao', title: '老茶铺把茶做成零食，年营收过亿', cover_url: 'https://cdn.example.com/big.jpg' });
  await waitFor(() => /同步中/.test(root.querySelector('[data-account="recToutiao"] .dist-status').textContent));
});

test('改稿页：已发布但还没拿到文章链接，给平台后台看发布情况', () => {
  const manage = 'https://baijiahao.baidu.com/builder/rc/content';
  const html = renderPlatform({ account: 'recBaijia', name: '百家号', status: '已发布', tone: 'green', editable: false, manage_url: manage }, {}, longTitle);
  assert.match(html, new RegExp(`<a href="${manage}" target="_blank" rel="noopener">去百家号后台查看</a>`));
  const linked = renderPlatform({ account: 'recBaijia', name: '百家号', status: '已发布', tone: 'green', editable: false, url: 'https://baijiahao.baidu.com/s?id=1', manage_url: manage }, {}, longTitle);
  assert.doesNotMatch(linked, /后台查看/, '有文章链接就不再给后台');
});

// 2026-10-30 用户：「我们的原创文章发出来就自动同步……自动同步之后，把文档链接返回到卡片上」。按钮后半句由服务端给
// （同步至X：存草稿再公开发布）；读回对不上停在草稿（未发布）的给草稿编辑页。
test('改稿页：停在草稿的平台给草稿箱链接，按钮说同步', () => {
  const draftURL = 'https://baijiahao.baidu.com/builder/rc/edit?type=news&amp;article_id=1846000000000000001';
  const held = renderPlatform({
    account: 'recBaijia', name: '百家号', status: '未发布', tone: 'orange', editable: false,
    reason: '草稿与原文对不上，没有发布：小标题：送去 2 处，读回 1 处',
    draft_url: 'https://baijiahao.baidu.com/builder/rc/edit?type=news&article_id=1846000000000000001', action: '同步至百家号',
  }, {}, longTitle);
  assert.ok(held.includes(`<a href="${draftURL}" target="_blank" rel="noopener">去百家号草稿箱查看</a>`), held);
  assert.match(held, /草稿与原文对不上，没有发布/);
  assert.doesNotMatch(held, /后台查看/, '有草稿地址就不再给后台');
  const editing = renderPlatform({ ...platformsView().platforms[0], account: 'recBaijia', name: '百家号', action: '同步至百家号' }, {}, longTitle);
  assert.match(editing, /<button type="submit" class="dist-primary">保存并同步至百家号<\/button>/);
  const legacy = renderPlatform(platformsView().platforms[0], {}, longTitle);
  assert.match(legacy, /<button type="submit" class="dist-primary">保存并同步至头条号<\/button>/, '没给 action 时也说同步');
});

// 发布中、平台审核中还没出结果：隔一会儿再取，文章链接一出来就显示；有了结果就不再取。
test('改稿页：同步、发布、审核中自动再取，出结果就停', async () => {
  const { document } = parseHTML('<p id="article"></p><div id="root"></div>');
  const statuses = ['发布中', '平台审核中', '已发布'];
  let served = 0;
  const request = async () => {
    const view = platformsView();
    view.platforms[1] = { ...view.platforms[1], status: statuses[Math.min(served, statuses.length - 1)], editable: false };
    served += 1;
    return Response.json(view);
  };
  const pending = [];
  const page = createDistributePage({ root: document.querySelector('#root'), articleEl: document.querySelector('#article'), search: '?job=196&t=sig', request, schedule: fn => pending.push(fn) });
  await page.load();
  while (pending.length) {
    await pending.shift()();
  }
  assert.equal(served, 3, '发布中、审核中各再取一次，已发布就停');
});

test('改稿页：没有签名链接时说清楚从哪打开', async () => {
  const { document } = parseHTML('<div id="root"></div>');
  const root = document.querySelector('#root');
  const page = createDistributePage({
    root, search: '?job=196', schedule: () => {},
    request: async () => Response.json({ error: '请从审核卡里的链接打开' }, { status: 403 }),
  });
  await page.load();
  assert.match(root.textContent, /请从审核卡里的链接打开/);
});

function fileEvent(window, type, field, files) {
  const event = new window.Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, field, { value: { files, types: ['Files'], dropEffect: 'none' } });
  return event;
}

function uploadPage(onUpload) {
  const { document, window } = parseHTML('<p id="article"></p><div id="root"></div>');
  globalThis.Event = window.Event;
  const root = document.querySelector('#root');
  const calls = [];
  const request = async (path, init = {}) => {
    calls.push({ path, method: init.method || 'GET', body: init.body });
    if (path.includes('/cover')) return onUpload(init.body);
    return Response.json(platformsView());
  };
  const page = createDistributePage({ root, articleEl: document.querySelector('#article'), search: '?job=196&t=sig', request, schedule: () => {} });
  return { document, window, root, calls, page };
}

const pngFile = (name = 'cover.png') => new File([new Uint8Array([137, 80, 78, 71])], name, { type: 'image/png' });

test('改稿页：上传格可点选、拖入、粘贴；传上去就换上，改好的标题一起提交', async () => {
  const uploads = [];
  const { document, window, root, page } = uploadPage(form => {
    uploads.push(form);
    const view = platformsView();
    view.platforms[0] = { ...view.platforms[0], status: '排队中', tone: 'blue', editable: false, needs_fix: false };
    return Response.json(view);
  });
  await page.load();
  const tile = root.querySelector('[data-account="recToutiao"] .dist-cover-upload');
  assert.match(tile.textContent, /上传图片/);
  assert.match(tile.textContent, /可拖入或粘贴/);

  // 先用建议标题（合乎要求），再粘贴一张图：标题随图一起提交。
  root.querySelector('[data-account="recToutiao"] [data-use="title"]').click();
  root.querySelector('[data-account="recToutiao"] [name="title"]').focus();
  document.dispatchEvent(fileEvent(window, 'paste', 'clipboardData', [pngFile('pasted.png')]));
  await waitFor(() => uploads.length === 1);
  assert.equal(uploads[0].get('account'), 'recToutiao');
  assert.equal(uploads[0].get('title'), '老茶铺把茶做成零食，年营收过亿');
  assert.equal(uploads[0].get('file').name, 'pasted.png');
  await waitFor(() => /排队中/.test(root.querySelector('[data-account="recToutiao"] .dist-status').textContent));
  assert.equal(root.querySelector('[data-account="recToutiao"] form'), null, '传上去就同步，不用再点保存');
});

test('改稿页：拖入图片传给落点所在的平台；标题还超着就只带图', async () => {
  const uploads = [];
  const { document, window, root, page } = uploadPage(form => {
    uploads.push(form);
    const view = platformsView({ cover_issue: '', cover: { url: 'https://cdn.example.com/up.jpg', width: 900, height: 600 }, reason: '标题超出 10 字' });
    return Response.json(view);
  });
  await page.load();
  const covers = root.querySelector('[data-account="recToutiao"] .dist-covers');
  covers.dispatchEvent(fileEvent(window, 'dragover', 'dataTransfer', []));
  assert.ok(root.querySelector('[data-account="recToutiao"] .dist-cover-upload').classList.contains('is-dragover'), '拖到上方时上传格高亮');
  const drop = fileEvent(window, 'drop', 'dataTransfer', [pngFile('dropped.png')]);
  covers.dispatchEvent(drop);
  assert.ok(drop.defaultPrevented, '不让浏览器把图片打开');
  await waitFor(() => uploads.length === 1);
  assert.equal(uploads[0].get('file').name, 'dropped.png');
  assert.equal(uploads[0].get('title'), null, '超限的标题不带');
  await waitFor(() => /封面已换上，还要改：标题超出 10 字/.test(root.querySelector('[data-account="recToutiao"] .dist-result')?.textContent || ''));
  assert.equal(root.querySelector('[data-account="recToutiao"] [name="title"]').value, longTitle, '没提交的标题留在输入框里');
  void document;
});

test('改稿页：传不上去的原因写在封面下面；不是 JPG/PNG 不发请求', async () => {
  const { document, window, root, calls, page } = uploadPage(() => Response.json({ error: '这张图 400×300，头条号封面至少 452×352' }, { status: 400 }));
  await page.load();
  document.dispatchEvent(fileEvent(window, 'paste', 'clipboardData', [new File(['gif'], 'a.gif', { type: 'image/gif' })]));
  await waitFor(() => /只支持 JPG、PNG 图片/.test(root.querySelector('[data-account="recToutiao"] [role="alert"]')?.textContent || ''));
  assert.equal(calls.filter(call => call.path.includes('/cover')).length, 0);
  const input = root.querySelector('[data-account="recToutiao"] [data-upload]');
  Object.defineProperty(input, 'files', { value: [pngFile()] });
  input.dispatchEvent(new window.Event('change'));
  await waitFor(() => /头条号封面至少 452×352/.test(root.querySelector('[data-account="recToutiao"] [role="alert"]')?.textContent || ''));
  // 粘贴文字不拦：照常进输入框。
  const textPaste = new window.Event('paste', { bubbles: true, cancelable: true });
  Object.defineProperty(textPaste, 'clipboardData', { value: { files: [] } });
  document.dispatchEvent(textPaste);
  assert.equal(textPaste.defaultPrevented, false);
});
