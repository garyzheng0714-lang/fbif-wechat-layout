import test from 'node:test';
import assert from 'node:assert/strict';
import { computeBannerTitleLayout, wrapBannerTitleLines } from '../public/js/more-articles.js';

const monoCtx = {
  measureText(text) {
    return { width: Array.from(String(text)).length };
  },
};

test('more articles title keeps punctuation off the second line start', () => {
  assert.deepEqual(
    wrapBannerTitleLines(monoCtx, 'ABCDE，FG', 5),
    ['ABCDE，', 'FG']
  );
  assert.deepEqual(
    wrapBannerTitleLines(monoCtx, 'ABCDE“FG', 5),
    ['ABCDE', '“FG']
  );
});

test('more articles single-line title is vertically centered in the PSD text box', () => {
  const layout = computeBannerTitleLayout(monoCtx, '单行标题');
  assert.deepEqual(layout.lines, ['单行标题']);
  assert.equal(layout.lineHeight, 70);
  assert.equal(layout.y, 115);
});

test('more articles two-line title keeps the PSD top anchor', () => {
  const layout = computeBannerTitleLayout(monoCtx, 'A'.repeat(900));
  assert.equal(layout.lines.length, 2);
  assert.equal(layout.y, 92);
});
