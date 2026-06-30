import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_BEHAVIOR,
  applyBehaviorToElems,
  isSafeBehaviorURL,
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
      mote_ref_image_url: { type: 'url', required: true, default: 'https://example.com/a.png" onerror="alert(1)' },
    },
    templates: {
      author_line: '<span onclick="alert(1)">作者：{{ article.author }}</span>',
    },
  });
  assert.equal(result.ok, false);
  assert.match(result.errors.join('\n'), /URL 参数/);
  assert.match(result.errors.join('\n'), /模板包含不允许/);
});

test('behavior URL validation rejects attribute-breaking sources', () => {
  assert.equal(isSafeBehaviorURL('/assets/mote.png'), true);
  assert.equal(isSafeBehaviorURL('https://example.com/assets/mote.png'), true);
  assert.equal(isSafeBehaviorURL('https://example.com/a.png" onerror="alert(1)'), false);
  assert.equal(isSafeBehaviorURL('//example.com/a.png'), false);
});

test('behavior validation matches server-side action and output contracts', () => {
  const result = validateRuleBehavior({
    actions: [{
      when: 'is_mote_author',
      type: 'insert_image_before',
      target: 'first_ref_heading',
      src_param: 'missing_image',
    }],
    templates: {
      author_line: '作者：' + '很长'.repeat(50),
    },
    output_contracts: {
      author_line: { max_length: 80, allow_html: true },
    },
  });
  const errors = result.errors.join('\n');
  assert.equal(result.ok, false);
  assert.match(errors, /不存在的参数：missing_image/);
  assert.match(errors, /模板不允许开启 HTML：author_line/);
  assert.match(errors, /模板过长：author_line/);
});

test('behavior validation rejects malformed top-level sections', () => {
  const result = validateRuleBehavior({
    params: [],
    templates: [],
    output_contracts: [],
    conditions: {},
    actions: {},
    extra: true,
  });
  const errors = result.errors.join('\n');
  assert.equal(result.ok, false);
  assert.match(errors, /行为参数必须是对象/);
  assert.match(errors, /行为模板必须是对象/);
  assert.match(errors, /行为输出约束必须是对象/);
  assert.match(errors, /行为条件必须是数组/);
  assert.match(errors, /行为动作必须是数组/);
  assert.match(errors, /未知行为规则字段：extra/);
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
  assert.equal(out[1].className, 'wx-mote-author');
  assert.equal(out[1].role, 'mote-author-card');
  assert.equal(out[2].k, 'refH');
});

test('behavior action can hide the Mote author image with article artifact override', () => {
  const elems = [
    { k: 'txt', runs: [{ type: 'txt', text: '正文' }] },
    { k: 'refH', text: '参考来源' },
  ];
  const out = applyBehaviorToElems(elems, {
    article: { author_normalized: normalizeAuthorName('Mote莫特') },
  }, DEFAULT_BEHAVIOR, {
    articleArtifactOverride: { mote_ref_author_card: false },
  });

  assert.equal(out.length, 2);
  assert.equal(out[0].k, 'txt');
  assert.equal(out[1].k, 'refH');
});

test('legacy Mote behavior action without id still honors the article artifact override', () => {
  const legacyBehavior = {
    ...DEFAULT_BEHAVIOR,
    actions: DEFAULT_BEHAVIOR.actions.map(({ id: _id, ...action }) => action),
  };
  const elems = [
    { k: 'txt', runs: [{ type: 'txt', text: '正文' }] },
    { k: 'refH', text: '参考来源' },
  ];
  const out = applyBehaviorToElems(elems, {
    article: { author_normalized: normalizeAuthorName('Mote莫特') },
  }, legacyBehavior, {
    articleArtifactOverride: { mote_ref_author_card: 'hide' },
  });

  assert.equal(out.length, 2);
  assert.equal(out[0].k, 'txt');
  assert.equal(out[1].k, 'refH');
});

test('behavior action moves target blankBefore onto the inserted image without mutating input', () => {
  const ref = { k: 'refH', text: '参考来源', blankBefore: true };
  const elems = [
    { k: 'txt', runs: [{ type: 'txt', text: '正文' }] },
    ref,
  ];
  const out = applyBehaviorToElems(elems, {
    article: { author_normalized: normalizeAuthorName('Mote莫特') },
  }, DEFAULT_BEHAVIOR);

  assert.equal(out[1].k, 'img');
  assert.equal(out[1].blankBefore, true);
  assert.equal(out[2].k, 'refH');
  assert.equal(out[2].blankBefore, false);
  assert.equal(ref.blankBefore, true, '原始 elem 不应被行为层原地修改');
});

test('behavior action does not invent blankBefore when the target did not have it', () => {
  const out = applyBehaviorToElems([
    { k: 'txt', runs: [{ type: 'txt', text: '正文' }] },
    { k: 'refH', text: '参考来源' },
  ], {
    article: { author_normalized: normalizeAuthorName('Mote莫特') },
  }, DEFAULT_BEHAVIOR);

  assert.equal(out[1].k, 'img');
  assert.equal(Object.prototype.hasOwnProperty.call(out[1], 'blankBefore'), false);
  assert.equal(out[2].k, 'refH');
});
