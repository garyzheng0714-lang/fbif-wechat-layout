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

test('editor shells bind upload before remote rule hydration settles', async () => {
  for (const name of ['app.html', 'site.html']) {
    const source = await readFile(new URL('../public/' + name, import.meta.url), 'utf8');
    const initIndex = source.indexOf('await loadTemplate(currentTemplateId)');
    assert.ok(initIndex > 0, `${name} should initialize its template`);

    const beforeInit = source.slice(0, initIndex);
    assert.doesNotMatch(
      beforeInit,
      /const\s+initialRule\s*=\s*await\s+loadActiveRulePreset/,
      `${name} must not block upload binding on the active rule request`
    );
    assert.doesNotMatch(
      beforeInit,
      /let\s+activeRuleCatalog\s*=\s*await\s+loadRuleCatalog/,
      `${name} must not block upload binding on the rule catalog request`
    );
    assert.match(
      beforeInit,
      /hydrateActiveRulePreset\(activeRulePack\.id\)\.catch/,
      `${name} should hydrate the active rule in the background`
    );
    assert.match(
      beforeInit,
      /refreshActiveRuleCatalog\(activeRulePack\.id\)\.catch/,
      `${name} should hydrate the rule catalog in the background`
    );
  }
});

test('editor shells load JSZip from the same-origin vendor asset', async () => {
  for (const name of ['app.html', 'site.html']) {
    const source = await readFile(new URL('../public/' + name, import.meta.url), 'utf8');
    assert.match(source, /<script src="\/vendor\/jszip\.min\.js"><\/script>/);
    assert.doesNotMatch(source, /cdn\.jsdelivr\.net\/npm\/jszip/);
  }
});

test('editor shells do not block startup on external font stylesheets', async () => {
  for (const name of ['app.html', 'site.html']) {
    const source = await readFile(new URL('../public/' + name, import.meta.url), 'utf8');
    assert.doesNotMatch(source, /fontsource/);
    assert.doesNotMatch(source, /cdn\.jsdelivr\.net/);
  }
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
