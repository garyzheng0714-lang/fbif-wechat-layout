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
  // y = title.y + (title.height - fontSize) / 2 = 92 + (116-48)/2 = 126.
  // We center the glyph (fontSize tall) inside the title box rather than
  // the line box (lineHeight tall) so the visible text lands on the canvas
  // vertical center instead of being shifted ~11px above it.
  assert.equal(layout.y, 126);
});

test('more articles two-line title keeps the PSD top anchor', () => {
  const layout = computeBannerTitleLayout(monoCtx, 'A'.repeat(900));
  assert.equal(layout.lines.length, 2);
  assert.equal(layout.y, 92);
});
