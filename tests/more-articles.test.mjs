import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { computeBannerTitleLayout, mergeIntoFooter, wrapBannerTitleLines } from '../public/js/more-articles.js';

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

test('more articles cards use block image layout and explicit card spacing', async () => {
  const footer = await readFile(new URL('../public/footer.html', import.meta.url), 'utf8');
  const html = mergeIntoFooter(footer, [
    { href: 'https://mp.weixin.qq.com/s/a', final_url: 'https://example.com/a.png' },
    { href: 'https://mp.weixin.qq.com/s/b', final_url: 'https://example.com/b.png' },
  ]);

  assert.match(html, /display:block;line-height:0;font-size:0;width:100%;/);
  assert.match(html, /display:block;width: 661px !important; max-width: 100% !important; height: auto !important; margin: 0 auto; vertical-align: top;/);
  assert.match(html, /margin-top:12px/);
  assert.doesNotMatch(html, /vertical-align:\s*baseline/i);
  assert.doesNotMatch(html, /line-height:\s*1\.6/i);
});

test('footer section titles keep symmetric image rhythm with block images', async () => {
  const footer = await readFile(new URL('../public/footer.html', import.meta.url), 'utf8');
  const html = mergeIntoFooter(footer, [
    { href: 'https://mp.weixin.qq.com/s/a', final_url: 'https://example.com/a.png' },
  ]);

  assert.match(html, /margin:20px 8px 20px;padding:0;text-align:center;"><span[^>]*><strong>\/ 更多文章 \/<\/strong>/);
  assert.match(html, /margin:20px 8px 20px;padding:0;text-align:center;"><span[^>]*><strong>\/ 1000个食品创新案例 \/<\/strong>/);
  assert.match(html, /\/ 1000个食品创新案例 \/<\/strong>[\s\S]*<section style="margin:0 8px 0;padding:0;text-align:center;line-height:0;font-size:0;"><img/);
  assert.doesNotMatch(html, /(?:<span\b[^>]*>\s*)?<br\s*\/?>\s*(?:<\/span>\s*)?<\/section>\s*$/i);
});
