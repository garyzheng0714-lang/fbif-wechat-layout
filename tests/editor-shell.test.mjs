import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

import {
  createUrlImportHandler,
  foodTalksFetchErrorMessage,
  resolveInitialRulePackID,
} from '../public/js/editor-shell.js';
import * as rulePackModule from '../public/js/rule-packs/registry.js';
import * as rulePackUrlModule from '../public/js/rule-packs/url.js';

test('editor shells both import the shared editor-shell helpers', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const site = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');

  assert.match(app, /import\(['"]\/js\/editor-shell\.js/);
  assert.match(site, /import\(['"]\/js\/editor-shell\.js/);
  assert.match(app, /createUrlImportHandler/);
  assert.match(site, /createUrlImportHandler/);
  assert.match(app, /bindUrlImportShortcuts/);
  assert.match(site, /bindUrlImportShortcuts/);
});

test('resolveInitialRulePackID preserves app path behavior', () => {
  const id = resolveInitialRulePackID({
    locationLike: { pathname: '/mp/foodtalks-series', search: '?account=fbif' },
    storage: null,
    rulePackModule,
    rulePackUrlModule,
  });

  assert.equal(id, 'foodtalks-series');
});

test('resolveInitialRulePackID falls back by channel for website shell', () => {
  const id = resolveInitialRulePackID({
    locationLike: { pathname: '/ft/default', search: '?account=fbif' },
    storage: null,
    rulePackModule,
    rulePackUrlModule,
    useChannelDefaultPath: true,
  });

  assert.equal(id, 'ft-default');
});

test('createUrlImportHandler passes source URL with structured blocks', async () => {
  const calls = [];
  const elements = new Map();
  const makeElement = value => ({ value, textContent: '', disabled: false, style: { display: '' } });
  elements.set('urlInput', makeElement('https://mp.weixin.qq.com/s/abc'));
  elements.set('urlFetchBtn', makeElement(''));
  elements.set('error', makeElement(''));

  const documentRef = {
    getElementById(id) {
      return elements.get(id);
    },
  };
  const fetchImpl = async () => ({
    ok: true,
    json: async () => ({ title: '标题', blocks: [{ type: 'p', text: '正文' }] }),
  });
  const handler = createUrlImportHandler({
    documentRef,
    fetchImpl,
    getHandleBlocks: () => (payload, options) => calls.push({ payload, options }),
    onArticleUrl(url) {
      calls.push({ currentUrl: url });
    },
  });

  await handler();

  assert.equal(calls[0].currentUrl, 'https://mp.weixin.qq.com/s/abc');
  assert.deepEqual(calls[1].payload, {
    title: '标题',
    blocks: [{ type: 'p', text: '正文' }],
    source: 'https://mp.weixin.qq.com/s/abc',
  });
  assert.deepEqual(calls[1].options, { isRepost: true });
});

test('foodTalksFetchErrorMessage keeps internal details out of the UI', () => {
  assert.equal(
    foodTalksFetchErrorMessage(new Error('exec /usr/bin/tool: PATH not found')),
    '文章抓取失败，请稍后重试',
  );
  assert.equal(
    foodTalksFetchErrorMessage(new Error('文章内容为空')),
    '文章内容为空',
  );
});
