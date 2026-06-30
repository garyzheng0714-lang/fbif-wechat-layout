import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

const {
  articleFooterDefaultsSeedForAccount,
  bindArticleFooterDefaultsEditor,
} = await import('../public/js/admin-article-footer-defaults.js?test=' + Date.now());

function bottomBlocks() {
  return {
    schema_version: 2,
    enabled: true,
    active_preset_id: 'original',
    presets: [{
      id: 'original',
      order: ['tip', 'byline', 'more_articles'],
      blocks: {
        tip: { type: 'text', display_name: '提示声明', enabled: true, fields: { content: '提示：' } },
        byline: { type: 'image', display_name: 'Bobo 名片', enabled: true, fields: { image: '{{asset#fbif_bobo_card}}' } },
        more_articles: { type: 'more_articles', display_name: '更多文章', enabled: true, fields: { title: '/ 更多文章 /' } },
      },
    }],
  };
}

test('文章默认方案新建规则按账号 seed：只有 FBIF 默认启用 Mote 方案', () => {
  const fbif = articleFooterDefaultsSeedForAccount('fbif');
  assert.equal(fbif.enabled, true);
  assert.deepEqual(fbif.profiles[0].block_visibility, { byline: false, more_articles: true });
  assert.deepEqual(fbif.profiles[0].artifact_visibility, { mote_ref_author_card: false });

  for (const account of ['wowfood', 'foodtalks-series', 'ft-default', 'ft-series']) {
    const seeded = articleFooterDefaultsSeedForAccount(account);
    assert.equal(seeded.enabled, false, `${account} 不应继承 FBIF 的 Mote 作者默认`);
    assert.deepEqual(seeded.profiles, []);
  }
});

test('非 FBIF 空文章默认方案编辑时不临时补 Mote profile', () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="overlay"><div data-role="article-footer-defaults-editor"></div></div></body></html>');
  const overlay = document.getElementById('overlay');
  let module = articleFooterDefaultsSeedForAccount('wowfood');

  bindArticleFooterDefaultsEditor({
    overlay,
    getModule: () => module,
    setModule: next => { module = next; },
    getBottomBlocks: bottomBlocks,
  });

  assert.doesNotMatch(overlay.textContent, /Mote/);
  const enabled = overlay.querySelector('[data-afd-enabled]');
  enabled.checked = true;
  enabled.dispatchEvent(new window.Event('change', { bubbles: true }));

  assert.equal(module.enabled, true);
  assert.equal(module.profiles[0].id, 'author_default');
  assert.equal(module.profiles[0].name, '新作者默认');
  assert.notEqual(module.profiles[0].id, 'mote_author');
});

test('文章默认方案后台是多方案通用编辑器，不写死 Mote 模块', () => {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="overlay"><div data-role="article-footer-defaults-editor"></div></div></body></html>');
  const overlay = document.getElementById('overlay');
  let module = {
    schema_version: 1,
    enabled: true,
    author_aliases: {
      mote: ['Mote莫特', 'Mote 莫特'],
      alice: ['Alice'],
    },
    profiles: [
      { id: 'mote_author', name: 'Mote 作者默认', match: { author_alias_group: 'mote' }, block_visibility: { byline: false, more_articles: true } },
      { id: 'alice_author', name: 'Alice 默认', match: { author_alias_group: 'alice' }, block_visibility: { byline: true, more_articles: false } },
    ],
  };

  bindArticleFooterDefaultsEditor({
    overlay,
    getModule: () => module,
    setModule: next => { module = next; },
    getBottomBlocks: bottomBlocks,
  });

  assert.match(overlay.textContent, /启用文章默认方案/);
  assert.match(overlay.textContent, /方案名称/);
  assert.match(overlay.textContent, /作者匹配/);
  assert.doesNotMatch(overlay.innerHTML, /<span>Mote 作者默认<\/span>/);

  const select = overlay.querySelector('[data-afd-profile]');
  assert.ok(select, '后台应提供方案选择');
  assert.equal(select.querySelectorAll('option').length, 2);

  const aliceOption = Array.from(select.querySelectorAll('option')).find(option => option.getAttribute('value') === 'alice_author');
  assert.ok(aliceOption, '应包含 Alice 方案');
  aliceOption.selected = true;
  select.dispatchEvent(new window.Event('change', { bubbles: true }));
  const nameInput = overlay.querySelector('[data-afd-name]');
  const aliasInput = overlay.querySelector('[data-afd-aliases]');
  assert.equal(nameInput.value, 'Alice 默认');
  assert.equal(aliasInput.value, 'Alice');

  nameInput.value = 'Alice 特例默认';
  nameInput.dispatchEvent(new window.Event('input', { bubbles: true }));
  assert.equal(module.profiles.find(p => p.id === 'alice_author').name, 'Alice 特例默认');

  const add = overlay.querySelector('[data-afd-add-profile]');
  assert.ok(add, '后台应能新增作者默认方案');
  add.dispatchEvent(new window.Event('click', { bubbles: true }));
  assert.equal(module.profiles.length, 3);
  assert.ok(module.profiles[2].match.author_alias_group, '新增方案应自动创建作者匹配组');
  assert.ok(Array.isArray(module.author_aliases[module.profiles[2].match.author_alias_group]));
});
