import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_BEHAVIOR,
  applyBehaviorToElems,
  normalizeAuthorName,
  normalizeRuleBehavior,
  renderBehaviorTemplate,
  validateRuleBehavior,
} from '../public/js/rule-behavior.js';

test('default behavior includes the current Mote and footer contracts', () => {
  assert.equal(DEFAULT_BEHAVIOR.schema_version, 1);
  assert.ok(DEFAULT_BEHAVIOR.params.mote_ref_image_url.required);
  assert.match(DEFAULT_BEHAVIOR.params.mote_ref_image_url.default, /^https:\/\//);
  assert.ok(DEFAULT_BEHAVIOR.templates.author_line.includes('{{ article.author }}'));
  assert.ok(DEFAULT_BEHAVIOR.templates.footer_repost_notice.includes('转载'));
});

test('behavior validation rejects missing required params', () => {
  const behavior = normalizeRuleBehavior({
    schema_version: 1,
    params: {
      mote_ref_image_url: { type: 'url', required: true, default: '' },
    },
  });
  const result = validateRuleBehavior(behavior);
  assert.equal(result.ok, false);
  assert.match(result.errors.join('\n'), /mote_ref_image_url/);
});

test('behavior validation rejects script-like templates and unsafe URLs', () => {
  const result = validateRuleBehavior({
    params: {
      mote_ref_image_url: { type: 'url', required: true, default: 'http://example.com/a.png' },
    },
    templates: {
      author_line: '<span onclick="alert(1)">作者：{{ article.author }}</span>',
    },
  });
  assert.equal(result.ok, false);
  assert.match(result.errors.join('\n'), /URL 参数/);
  assert.match(result.errors.join('\n'), /模板包含不允许/);
});

test('behavior template slots interpolate only explicit context paths', () => {
  const rendered = renderBehaviorTemplate('作者：{{ article.author }} / {{ account.name }}', {
    article: { author: 'Mote莫特' },
    account: { name: 'FBIF公众号' },
  });
  assert.equal(rendered, '作者：Mote莫特 / FBIF公众号');
});

test('behavior action inserts the Mote author image before reference heading', () => {
  const elems = [
    { k: 'txt', runs: [{ type: 'txt', text: '正文' }] },
    { k: 'refH', text: '参考来源' },
  ];
  const out = applyBehaviorToElems(elems, {
    article: { author_normalized: normalizeAuthorName('Mote 莫特') },
  }, DEFAULT_BEHAVIOR);
  assert.equal(out.length, 3);
  assert.equal(out[1].k, 'img');
  assert.equal(out[1].src, DEFAULT_BEHAVIOR.params.mote_ref_image_url.default);
  assert.equal(out[2].k, 'refH');
});
