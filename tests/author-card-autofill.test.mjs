// FoodTalks 系列作者名片自动补充。
// WHY：作者名片是每篇文章的确定性素材位；识别到作者应自动填，识别不到应清空旧稿残留。

import test from 'node:test';
import assert from 'node:assert/strict';

import {
  applySeriesAuthorCardAutofill,
  resolveSeriesAuthorCardRef,
} from '../public/js/blocks/author-card-autofill.js';

test('resolveSeriesAuthorCardRef: Ian 通过别名命中 Lan 名片', () => {
  assert.equal(resolveSeriesAuthorCardRef('Ian', {}), '{{asset#ft_series_card_lan}}');
});

test('applySeriesAuthorCardAutofill: 新文章识别 Ian 时写入作者名片覆盖', () => {
  const applied = applySeriesAuthorCardAutofill({
    accountId: 'foodtalks-series',
    result: { authorName: 'Ian' },
    config: {},
    override: { overrides: { bd_card: { fields: { image: '{{asset#ft_series_card_eileen}}' } } } },
  });
  assert.equal(applied.ref, '{{asset#ft_series_card_lan}}');
  assert.equal(applied.override.overrides.author_card.fields.image, '{{asset#ft_series_card_lan}}');
  assert.equal(applied.override.overrides.bd_card.fields.image, '{{asset#ft_series_card_eileen}}', '其他区块覆盖不应被动');
});

test('applySeriesAuthorCardAutofill: 识别不到作者时清空旧作者名片，避免串稿', () => {
  const applied = applySeriesAuthorCardAutofill({
    accountId: 'foodtalks-series',
    result: { authorName: 'Unknown' },
    config: {},
    override: { overrides: { author_card: { fields: { image: '{{asset#ft_series_card_lan}}' } } } },
  });
  assert.deepEqual(applied.override, {}, '新文章未命中作者名片时，不应沿用上一篇作者名片');
});

test('applySeriesAuthorCardAutofill: 非 FoodTalks 系列账号不改底部覆盖', () => {
  const original = { overrides: { author_card: { fields: { image: '{{asset#ft_series_card_lan}}' } } } };
  const applied = applySeriesAuthorCardAutofill({
    accountId: 'fbif',
    result: { authorName: 'Ian' },
    config: {},
    override: original,
  });
  assert.deepEqual(applied.override, original);
  assert.equal(applied.changed, false);
});
