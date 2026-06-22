import test, { afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { computeBannerTitleLayout, makeFreshUploadCards, mergeIntoFooter, wrapBannerTitleLines } from '../public/js/more-articles.js';

afterEach(() => {
  delete globalThis.window;
});

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
  assert.doesNotMatch(html, /1000个食品创新案例/, '旧 1000 案例标题不应进入最终页脚');
  assert.doesNotMatch(html, /promo-1000|免费领/, '旧 1000 案例图不应进入最终页脚');
  assert.doesNotMatch(html, /(?:<span\b[^>]*>\s*)?<br\s*\/?>\s*(?:<\/span>\s*)?<\/section>\s*$/i);
});

test('stock footer does not keep the removed 1000-case promo image slot', async () => {
  const footer = await readFile(new URL('../public/footer.html', import.meta.url), 'utf8');

  assert.equal((footer.match(/<img\b/g) || []).length, 4);
  assert.doesNotMatch(
    footer,
    /<section data-role="paragraph"[^>]*>\s*<\/section>\s*<section style="margin:0 8px 0;padding:0;text-align:center;line-height:0;font-size:0;"><img/i,
    '旧推广标题删除后，不应留下空段落后紧跟的孤立二维码图'
  );
});

test('legacy footer promo block is stripped during merge', () => {
  const legacyFooter = [
    '<section data-role="paragraph">',
    '<section style="margin:20px 8px 20px;padding:0;text-align:center;"><strong>/ 更多文章 /</strong></section>',
    '<section style="text-align: center;margin-left: 8px;margin-right: 8px;"></section>',
    '</section>',
    '<section style="margin:20px 8px 20px;padding:0;text-align:center;"><strong>/ 1000个食品创新案例 /</strong></section>',
    '<section style="margin:0 8px 0;padding:0;text-align:center;line-height:0;font-size:0;"><img src="/assets/fbif/promo-1000.webp"></section>',
  ].join('\n');

  const html = mergeIntoFooter(legacyFooter, [
    { href: 'https://mp.weixin.qq.com/s/a', final_url: 'https://example.com/a.png' },
  ]);

  assert.match(html, /\/ 更多文章 \//);
  assert.doesNotMatch(html, /1000个食品创新案例/);
  assert.doesNotMatch(html, /promo-1000/);
});

test('fresh more-articles cards seed from the active rule default cards', () => {
  globalThis.window = {
    _activeConfig: {
      config: {
        more_articles_enabled: true,
        more_articles_slots: '2',
        more_articles_default_cards: [
          {
            href: 'https://mp.weixin.qq.com/s/custom-a',
            imgurl: 'https://example.com/a.jpg',
            title: '自定义 A',
          },
          {
            href: 'https://mp.weixin.qq.com/s/custom-b',
            imgurl: 'https://example.com/b.jpg',
            title: '自定义 B',
          },
        ],
      },
    },
  };

  const cards = makeFreshUploadCards();

  assert.equal(cards.length, 2);
  assert.deepEqual(cards.map(card => card.href), [
    'https://mp.weixin.qq.com/s/custom-a',
    'https://mp.weixin.qq.com/s/custom-b',
  ]);
  assert.deepEqual(cards.map(card => card.composite_url), [null, null]);
  assert.deepEqual(cards.map(card => card.composite_hash), [null, null]);
});

test('fresh more-articles cards use active rule slots when count is omitted', () => {
  globalThis.window = {
    _activeConfig: {
      config: {
        more_articles_enabled: true,
        more_articles_slots: '4',
        more_articles_default_cards: [
          { href: 'https://mp.weixin.qq.com/s/one' },
        ],
      },
    },
  };

  const cards = makeFreshUploadCards();

  assert.equal(cards.length, 4);
  assert.equal(cards[0].href, 'https://mp.weixin.qq.com/s/one');
  assert.equal(cards[1].href, '');
  assert.equal(cards[2].href, '');
  assert.equal(cards[3].href, '');
});

test('fresh more-articles cards are empty when the active rule disables the section', () => {
  globalThis.window = {
    _activeConfig: {
      config: {
        more_articles_enabled: false,
        more_articles_default_cards: [
          { href: 'https://mp.weixin.qq.com/s/disabled' },
        ],
      },
    },
  };

  assert.deepEqual(makeFreshUploadCards(), []);
});

test('fresh more-articles cards pad explicit count after active rule defaults', () => {
  globalThis.window = {
    _activeConfig: {
      config: {
        more_articles_enabled: true,
        more_articles_default_cards: [
          { href: 'https://mp.weixin.qq.com/s/one' },
        ],
      },
    },
  };

  const cards = makeFreshUploadCards(3);

  assert.equal(cards.length, 3);
  assert.equal(cards[0].href, 'https://mp.weixin.qq.com/s/one');
  assert.equal(cards[1].href, '');
  assert.equal(cards[2].href, '');
});
