import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

import {
  addDefaultMoreArticleCard,
  createMoreArticlesRuleEditor,
  removeDefaultMoreArticleCard,
  replaceDefaultMoreArticleCard,
  sameMoreArticleCardSnapshot,
  toggleMoreArticlesEnabled,
} from '../public/js/more-articles-rule-editor.js';
import { normalizeRuleConfig } from '../public/js/rule-presets.js';

class FakeElement {
  constructor() {
    this.html = '';
    this.listeners = {};
  }

  set innerHTML(value) {
    this.html = String(value || '');
  }

  get innerHTML() {
    return this.html;
  }

  addEventListener(type, handler) {
    this.listeners[type] = handler;
  }
}

function withFakeDocument(fn) {
  const previousDocument = globalThis.document;
  globalThis.document = {
    createElement() {
      return new FakeElement();
    },
  };
  try {
    return fn();
  } finally {
    globalThis.document = previousDocument;
  }
}

function fakeActionTarget(action) {
  return {
    closest(selector) {
      if (selector === '[data-action]') return { dataset: { action } };
      return null;
    },
  };
}

test('more articles rule helpers update config without mutating the source object', () => {
  const source = {
    more_articles_enabled: false,
    more_articles_default_cards: [{
      href: ' https://mp.weixin.qq.com/s/test ',
      title: ' 原标题 ',
      imgurl: ' //mmbiz.qpic.cn/a.jpg ',
      image_urls: ['//mmbiz.qpic.cn/a.jpg', '//mmbiz.qpic.cn/a.jpg'],
      crop: { x: 2, y: -1, scale: 9 },
    }],
  };

  const enabled = toggleMoreArticlesEnabled(source, true);
  assert.equal(enabled.more_articles_enabled, true);
  assert.equal(source.more_articles_enabled, false);

  const added = addDefaultMoreArticleCard(source);
  assert.equal(added.more_articles_default_cards.length, 2);
  assert.equal(source.more_articles_default_cards.length, 1);
  assert.deepEqual(added.more_articles_default_cards[1], {
    href: '',
    title: '未命名文章',
    imgurl: '',
    image_urls: [],
    cover_data_url: null,
    crop: { x: 0.5, y: 0.5, scale: 1 },
  });

  const replaced = replaceDefaultMoreArticleCard(source, 0, {
    href: 'https://mp.weixin.qq.com/s/replaced',
    title: ' 新标题 ',
    imgurl: 'https://mmbiz.qpic.cn/replaced.jpg',
    image_urls: ['https://mmbiz.qpic.cn/replaced.jpg'],
  });
  assert.equal(replaced.more_articles_default_cards[0].title, '新标题');
  assert.equal(source.more_articles_default_cards[0].title, ' 原标题 ');

  const removed = removeDefaultMoreArticleCard(added, 0);
  assert.equal(removed.more_articles_default_cards.length, 1);
  assert.equal(added.more_articles_default_cards.length, 2);
});

test('more articles rule editor emits only the more_articles patch when adding a card', () => {
  const base = {
    banner_title_x: '61',
    more_articles_enabled: true,
    more_articles_default_cards: [],
  };
  let patch = null;

  withFakeDocument(() => {
    const editor = createMoreArticlesRuleEditor({
      config: base,
      onChange(next) {
        patch = next;
      },
    });
    editor.listeners.click({ target: fakeActionTarget('add-card') });
  });

  assert.ok(patch);
  assert.equal(Object.hasOwn(patch, 'more_articles_enabled'), true);
  assert.equal(Object.hasOwn(patch, 'more_articles_default_cards'), true);
  assert.equal(Object.hasOwn(patch, 'banner_title_x'), false);
});

test('adding a default card survives parent normalizeRuleConfig merge', () => {
  const base = normalizeRuleConfig({
    banner_title_x: '61',
    more_articles_enabled: true,
    more_articles_default_cards: [],
  });
  let patch = null;

  withFakeDocument(() => {
    const editor = createMoreArticlesRuleEditor({
      config: base,
      onChange(next) {
        patch = next;
      },
    });
    editor.listeners.click({ target: fakeActionTarget('add-card') });
  });

  const merged = normalizeRuleConfig({ ...base, ...patch });
  assert.equal(merged.banner_title_x, '61');
  assert.equal(merged.more_articles_default_cards.length, 1);
  assert.equal(merged.more_articles_default_cards[0].title, '未命名文章');
});

test('more article fetch snapshot detects edits beyond the URL', () => {
  const requestCard = {
    href: ' https://mp.weixin.qq.com/s/test ',
    title: '抓取前标题',
    imgurl: 'https://mmbiz.qpic.cn/a.jpg',
    image_urls: ['https://mmbiz.qpic.cn/a.jpg'],
    crop: { x: 0.5, y: 0.5, scale: 1 },
  };
  const editedTitle = {
    ...requestCard,
    title: '用户手动改过的标题',
  };
  const sameAfterNormalization = {
    ...requestCard,
    href: 'https://mp.weixin.qq.com/s/test',
  };

  assert.equal(sameMoreArticleCardSnapshot(requestCard, sameAfterNormalization), true);
  assert.equal(sameMoreArticleCardSnapshot(requestCard, editedTitle), false);
});

test('more articles rule editor source exposes non-technical card editing controls', async () => {
  const source = await readFile(new URL('../public/js/more-articles-rule-editor.js', import.meta.url), 'utf8');

  assert.match(source, /createMoreArticlesRuleEditor/);
  assert.match(source, /data-role="ma-rule-url"/);
  assert.match(source, /data-action="fetch-meta"/);
  assert.match(source, /fetchMoreArticleMetaCached/);
  assert.match(source, /保存后，新导入文章会默认使用这组/);
  assert.match(source, /ma-rule-editor/);
  assert.match(source, /ma-rule-toggle-row/);
  assert.match(source, /ma-rule-field/);
  assert.doesNotMatch(source, /<section class="admin-rule-group/);
});
