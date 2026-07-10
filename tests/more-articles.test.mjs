import test, { afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { computeBannerTitleLayout, computeCardHash, copyCompositeCacheKey, copySafeMoreArticleCard, ensureCompositeReady, ensureCopyCompositeDataUrl, loadCards, makeFreshUploadCards, mergeIntoFooter, saveCards, uploadDataUrl, wrapBannerTitleLines } from '../public/js/more-articles.js';

afterEach(() => {
  delete globalThis.window;
  delete globalThis.localStorage;
  delete globalThis.location;
});

const monoCtx = {
  measureText(text) {
    return { width: Array.from(String(text)).length };
  },
};

function moreArticlesSlice(html) {
  const idx = html.indexOf('/ 更多文章 /');
  assert.ok(idx >= 0, 'should contain the more-articles section');
  return html.slice(idx);
}

function cardSectionStyles(html) {
  return Array.from(moreArticlesSlice(html).matchAll(
    /<section style="([^"]*)"><p style="margin:0;padding:0;line-height:0;font-size:0;">/g
  )).map(match => match[1]);
}

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
  const styles = cardSectionStyles(html);

  assert.match(html, /<section style="text-align: center;" data-card-inset="8">/);
  assert.doesNotMatch(html, /<section style="text-align: center;margin-left: 8px;margin-right: 8px;">/);
  assert.equal(styles.length, 2);
  for (const style of styles) {
    assert.match(style, /margin:0;/);
    assert.match(style, /margin-left:8px;/);
    assert.match(style, /margin-right:8px;/);
  }
  assert.match(html, /display:block;line-height:0;font-size:0;width:100%;/);
  assert.match(html, /display:block;width: 661px !important; max-width: 100% !important; height: auto !important; margin: 0 auto; vertical-align: top;/);
  assert.match(styles[0], /margin-top:0px/);
  assert.match(styles[1], /margin-top:12px/);
  assert.doesNotMatch(html, /vertical-align:\s*baseline/i);
  assert.doesNotMatch(html, /line-height:\s*1\.6/i);
});

test('more articles merge still recognizes legacy 8px wrapper but outputs one 8px indent on cards', () => {
  const legacyFooter = [
    '<section data-role="paragraph">',
    '<section style="margin:20px 8px 20px;padding:0;text-align:center;"><strong>/ 更多文章 /</strong></section>',
    '<section style="text-align: center;margin-left: 8px;margin-right: 8px;"></section>',
    '</section>',
  ].join('\n');

  const html = mergeIntoFooter(legacyFooter, [
    { href: 'https://mp.weixin.qq.com/s/a', final_url: 'https://example.com/a.png' },
  ]);
  const styles = cardSectionStyles(html);

  assert.match(html, /<section style="text-align: center;" data-card-inset="8">/);
  assert.doesNotMatch(html, /<section style="text-align: center;margin-left: 8px;margin-right: 8px;">/);
  assert.deepEqual(styles, ['margin:0;margin-left:8px;margin-right:8px;padding:0;text-align:center;line-height:0;font-size:0;margin-top:0px;']);
  assert.match(html, /https:\/\/example\.com\/a\.png/);
});

test('more articles merge ignores unrelated centered sections before the anchor', () => {
  const footer = [
    '<section style="text-align: center;">不是更多文章</section>',
    '<section data-role="paragraph">',
    '<section style="margin:20px 8px 20px;padding:0;text-align:center;"><strong>/ 更多文章 /</strong></section>',
    '<section style="text-align: center;" data-card-inset="8"></section>',
    '</section>',
  ].join('\n');

  const html = mergeIntoFooter(footer, [
    { href: 'https://mp.weixin.qq.com/s/a', final_url: 'https://example.com/a.png' },
  ]);

  assert.match(html, /不是更多文章/);
  assert.ok(html.indexOf('https://example.com/a.png') > html.indexOf('/ 更多文章 /'));
  assert.deepEqual(cardSectionStyles(html), ['margin:0;margin-left:8px;margin-right:8px;padding:0;text-align:center;line-height:0;font-size:0;margin-top:0px;']);
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

test('fresh more-articles cards do not pad rule defaults with empty legacy slots', () => {
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

  assert.equal(cards.length, 1);
  assert.equal(cards[0].href, 'https://mp.weixin.qq.com/s/one');
});

test('loading legacy more-article state drops empty cards padded after real recommendations', () => {
  const storedCards = [
    { href: 'https://mp.weixin.qq.com/s/a', title: '文章 A' },
    { href: 'https://mp.weixin.qq.com/s/b', title: '文章 B' },
    { href: '', title: '', imgurl: '', image_urls: [] },
    { href: '', title: '', imgurl: '', image_urls: [] },
  ];
  globalThis.localStorage = {
    getItem(key) {
      return String(key).startsWith('more_articles_v1::')
        ? JSON.stringify(storedCards)
        : null;
    },
    setItem() {},
    removeItem() {},
  };

  const cards = loadCards();

  assert.equal(cards.length, 2);
  assert.deepEqual(cards.map(card => card.href), [
    'https://mp.weixin.qq.com/s/a',
    'https://mp.weixin.qq.com/s/b',
  ]);
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

test('more articles upload rejects echoed data URLs because WeChat save strips linked data images', async (t) => {
  const originalFetch = globalThis.fetch;
  const dataUrl = 'data:image/png;base64,AAAA';
  globalThis.fetch = async () => new Response(JSON.stringify({
    results: { more_article_test: dataUrl },
  }), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  });
  t.after(() => { globalThis.fetch = originalFetch; });

  await assert.rejects(
    () => uploadDataUrl(dataUrl, 'more_article_test'),
    /remote image URL/
  );
});

test('more articles copy-safe fallback only uses T0-safe card images for linked cards', () => {
  const readyCard = {
    href: 'https://mp.weixin.qq.com/s/a',
    imgurl: 'https://mmbiz.qpic.cn/a.jpg',
    composite_url: 'https://oss.example/more-article.png',
    copy_data_url: 'data:image/png;base64,copy-composite',
    title: '带标题的合成图',
  };
  readyCard.copy_data_key = copyCompositeCacheKey(readyCard);
  assert.deepEqual(
    copySafeMoreArticleCard(readyCard).final_url,
    'data:image/png;base64,copy-composite',
    '带标题卡片必须优先使用本地合成位图，不能回退成无文字的原始封面'
  );

  assert.equal(
    copySafeMoreArticleCard({
      href: 'https://mp.weixin.qq.com/s/a',
      imgurl: 'https://mmbiz.qpic.cn/a.jpg',
      composite_url: 'data:image/png;base64,preview',
      title: '带标题的预览合成图',
    }).final_url,
    '',
    '带标题卡片不能信任没有内容指纹的旧 data URL'
  );

  assert.equal(
    copySafeMoreArticleCard({
      href: 'https://mp.weixin.qq.com/s/a',
      imgurl: 'https://mmbiz.qpic.cn/a.jpg',
      composite_url: 'https://oss.example/more-article.png',
      title: '缺少本地合成图',
    }).final_url,
    '',
    '带标题卡片缺少本地合成位图时必须 fail closed，不能退回原始封面'
  );

  assert.equal(
    copySafeMoreArticleCard({
      imgurl: 'https://mmbiz.qpic.cn/a.jpg',
      composite_url: 'https://oss.example/more-article.png',
    }).final_url,
    'https://mmbiz.qpic.cn/a.jpg'
  );

  const unsafe = copySafeMoreArticleCard({
    href: 'https://mp.weixin.qq.com/s/a',
    imgurl: 'data:image/png;base64,preview',
    composite_url: '',
  });
  assert.equal(unsafe.imgurl, '');
  assert.equal(unsafe.final_url, '');
});

test('more articles upload keeps a bitmap composite for the WeChat copy layer', async (t) => {
  const originalImage = globalThis.Image;
  const originalDocument = globalThis.document;
  const originalFetch = globalThis.fetch;
  const dataUrl = 'data:image/png;base64,copy-layer-composite';
  const uploadedUrl = 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/uploaded-composite.png';
  const filledText = [];
  const canvasSizes = [];

  class FakeImage {
    constructor() {
      this.naturalWidth = 1000;
      this.naturalHeight = 300;
      this.width = 1000;
      this.height = 300;
    }
    set src(value) {
      this._src = value;
      queueMicrotask(() => this.onload && this.onload());
    }
    get src() { return this._src; }
  }

  globalThis.Image = FakeImage;
  globalThis.document = {
    createElement(tag) {
      assert.equal(tag, 'canvas');
      return {
        width: 0,
        height: 0,
        getContext() {
          return {
            fillStyle: '',
            font: '',
            textBaseline: '',
            fillRect() {},
            drawImage() {},
            measureText(text) {
              return { width: Array.from(String(text)).length * 24 };
            },
            fillText(text) {
              filledText.push(text);
            },
          };
        },
        toDataURL() {
          canvasSizes.push([this.width, this.height]);
          return dataUrl;
        },
      };
    },
  };
  globalThis.fetch = async (_url, options = {}) => {
    const payload = JSON.parse(String(options.body || '{}'));
    const key = Object.keys(payload.base64_images || {})[0];
    return new Response(JSON.stringify({
      results: { [key]: uploadedUrl },
    }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  };
  t.after(() => {
    globalThis.Image = originalImage;
    globalThis.document = originalDocument;
    globalThis.fetch = originalFetch;
  });

  const [prepared] = await ensureCompositeReady([{
    href: 'https://mp.weixin.qq.com/s/a',
    imgurl: 'https://mmbiz.qpic.cn/a.jpg',
    title: '必须显示的标题',
  }]);

  assert.equal(prepared.composite_url, uploadedUrl);
  assert.equal(prepared.final_url, uploadedUrl);
  assert.equal(prepared.copy_data_url, dataUrl, '上传成功后仍必须保留本地合成位图给公众号复制层');
  assert.deepEqual(canvasSizes, [[1000, 300]], '更多文章合成图必须保持 1000x300 横幅比例');
  assert.deepEqual(filledText, ['必须显示的标题']);
  assert.equal(copySafeMoreArticleCard(prepared).final_url, dataUrl);
});

test('cached more-article OSS composite still rebuilds a bitmap for the copy layer', async (t) => {
  const originalImage = globalThis.Image;
  const originalDocument = globalThis.document;
  const originalFetch = globalThis.fetch;
  const dataUrl = 'data:image/png;base64,cached-copy-layer-composite';
  const filledText = [];
  let uploadCalled = false;

  class FakeImage {
    constructor() {
      this.naturalWidth = 1000;
      this.naturalHeight = 300;
      this.width = 1000;
      this.height = 300;
    }
    set src(value) {
      this._src = value;
      queueMicrotask(() => this.onload && this.onload());
    }
    get src() { return this._src; }
  }

  globalThis.Image = FakeImage;
  globalThis.document = {
    createElement(tag) {
      assert.equal(tag, 'canvas');
      return {
        width: 0,
        height: 0,
        getContext() {
          return {
            fillStyle: '',
            font: '',
            textBaseline: '',
            fillRect() {},
            drawImage() {},
            measureText(text) {
              return { width: Array.from(String(text)).length * 24 };
            },
            fillText(text) {
              filledText.push(text);
            },
          };
        },
        toDataURL() {
          return dataUrl;
        },
      };
    },
  };
  globalThis.fetch = async () => {
    uploadCalled = true;
    throw new Error('cached composite must not upload again');
  };
  t.after(() => {
    globalThis.Image = originalImage;
    globalThis.document = originalDocument;
    globalThis.fetch = originalFetch;
  });

  const card = {
    href: 'https://mp.weixin.qq.com/s/a',
    imgurl: 'https://mmbiz.qpic.cn/a.jpg',
    title: '缓存也必须显示标题',
    crop: { x: 0.5, y: 0.5, scale: 1 },
    composite_url: 'https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/cached-composite.png',
  };
  const hash = await computeCardHash(card);

  const [prepared] = await ensureCompositeReady([{ ...card, composite_hash: hash }]);

  assert.equal(prepared.composite_url, card.composite_url);
  assert.equal(prepared.final_url, card.composite_url);
  assert.equal(prepared.copy_data_url, dataUrl, '命中 OSS 缓存时也必须补齐复制层本地位图');
  assert.equal(uploadCalled, false, '已有 OSS 缓存时不应重复上传');
  assert.deepEqual(filledText, ['缓存也必须显示标题']);
  assert.equal(copySafeMoreArticleCard(prepared).final_url, dataUrl);
});

test('more articles copy bitmap is invalidated when card content changes', async (t) => {
  const originalImage = globalThis.Image;
  const originalDocument = globalThis.document;
  const dataUrls = [
    'data:image/png;base64,old-copy-layer-composite',
    'data:image/png;base64,new-copy-layer-composite',
  ];
  const filledText = [];

  class FakeImage {
    constructor() {
      this.naturalWidth = 1000;
      this.naturalHeight = 300;
      this.width = 1000;
      this.height = 300;
    }
    set src(value) {
      this._src = value;
      queueMicrotask(() => this.onload && this.onload());
    }
    get src() { return this._src; }
  }

  globalThis.Image = FakeImage;
  globalThis.document = {
    createElement(tag) {
      assert.equal(tag, 'canvas');
      return {
        width: 0,
        height: 0,
        getContext() {
          return {
            fillStyle: '',
            font: '',
            textBaseline: '',
            fillRect() {},
            drawImage() {},
            measureText(text) {
              return { width: Array.from(String(text)).length * 24 };
            },
            fillText(text) {
              filledText.push(text);
            },
          };
        },
        toDataURL() {
          return dataUrls.shift();
        },
      };
    },
  };
  t.after(() => {
    globalThis.Image = originalImage;
    globalThis.document = originalDocument;
  });

  const original = {
    href: 'https://mp.weixin.qq.com/s/a',
    imgurl: 'https://mmbiz.qpic.cn/a.jpg',
    title: '旧标题',
  };
  await ensureCopyCompositeDataUrl(original);
  assert.equal(copySafeMoreArticleCard(original).final_url, 'data:image/png;base64,old-copy-layer-composite');

  const changed = { ...original, title: '新标题' };
  assert.equal(
    copySafeMoreArticleCard(changed).final_url,
    '',
    '标题变更后旧 copy_data_url/key 不能继续被信任'
  );
  await ensureCopyCompositeDataUrl(changed);
  assert.equal(copySafeMoreArticleCard(changed).final_url, 'data:image/png;base64,new-copy-layer-composite');
  assert.deepEqual(filledText, ['旧标题', '新标题']);
});

test('more articles copy bitmap is invalidated when custom cover data changes after the shared prefix', async (t) => {
  const originalImage = globalThis.Image;
  const originalDocument = globalThis.document;
  const dataUrls = [
    'data:image/png;base64,cover-a-composite',
    'data:image/png;base64,cover-b-composite',
  ];

  class FakeImage {
    constructor() {
      this.naturalWidth = 1000;
      this.naturalHeight = 300;
      this.width = 1000;
      this.height = 300;
    }
    set src(value) {
      this._src = value;
      queueMicrotask(() => this.onload && this.onload());
    }
    get src() { return this._src; }
  }

  globalThis.Image = FakeImage;
  globalThis.document = {
    createElement(tag) {
      assert.equal(tag, 'canvas');
      return {
        width: 0,
        height: 0,
        getContext() {
          return {
            fillStyle: '',
            font: '',
            textBaseline: '',
            fillRect() {},
            drawImage() {},
            measureText(text) {
              return { width: Array.from(String(text)).length * 24 };
            },
            fillText() {},
          };
        },
        toDataURL() {
          return dataUrls.shift();
        },
      };
    },
  };
  t.after(() => {
    globalThis.Image = originalImage;
    globalThis.document = originalDocument;
  });

  const sharedPrefix = 'data:image/png;base64,' + 'A'.repeat(180);
  const coverA = sharedPrefix + '1111';
  const coverB = sharedPrefix + '2222';
  assert.equal(coverA.length, coverB.length);
  assert.equal(coverA.slice(0, 120), coverB.slice(0, 120));

  const first = {
    href: 'https://mp.weixin.qq.com/s/a',
    imgurl: 'https://mmbiz.qpic.cn/a.jpg',
    title: '同标题',
    cover_data_url: coverA,
  };
  await ensureCopyCompositeDataUrl(first);
  assert.equal(copySafeMoreArticleCard(first).final_url, 'data:image/png;base64,cover-a-composite');

  const second = { ...first, cover_data_url: coverB };
  assert.notEqual(copyCompositeCacheKey(first), copyCompositeCacheKey(second));
  assert.equal(
    copySafeMoreArticleCard(second).final_url,
    '',
    '自定义封面 data URL 只有尾部变化时，也不能复用旧 copy PNG'
  );
  await ensureCopyCompositeDataUrl(second);
  assert.equal(copySafeMoreArticleCard(second).final_url, 'data:image/png;base64,cover-b-composite');
});

test('more articles copy bitmap survives localStorage write failure via session cache', async (t) => {
  const originalImage = globalThis.Image;
  const originalDocument = globalThis.document;
  const originalLocalStorage = globalThis.localStorage;
  const dataUrl = 'data:image/png;base64,session-cache-copy-layer';
  const storedCard = {
    href: 'https://mp.weixin.qq.com/s/session-cache',
    imgurl: 'https://mmbiz.qpic.cn/session-cache.jpg',
    title: '写不进 localStorage 也要显示标题',
  };
  const storedJson = JSON.stringify([storedCard]);

  class FakeImage {
    constructor() {
      this.naturalWidth = 1000;
      this.naturalHeight = 300;
      this.width = 1000;
      this.height = 300;
    }
    set src(value) {
      this._src = value;
      queueMicrotask(() => this.onload && this.onload());
    }
    get src() { return this._src; }
  }

  globalThis.Image = FakeImage;
  globalThis.document = {
    createElement(tag) {
      assert.equal(tag, 'canvas');
      return {
        width: 0,
        height: 0,
        getContext() {
          return {
            fillStyle: '',
            font: '',
            textBaseline: '',
            fillRect() {},
            drawImage() {},
            measureText(text) {
              return { width: Array.from(String(text)).length * 24 };
            },
            fillText() {},
          };
        },
        toDataURL() {
          return dataUrl;
        },
      };
    },
  };
  globalThis.localStorage = {
    getItem(key) {
      return String(key).startsWith('more_articles_v1::') ? storedJson : null;
    },
    setItem() {
      throw new Error('QuotaExceededError');
    },
    removeItem() {},
  };
  t.after(() => {
    globalThis.Image = originalImage;
    globalThis.document = originalDocument;
    globalThis.localStorage = originalLocalStorage;
  });

  const [card] = loadCards();
  await ensureCopyCompositeDataUrl(card);
  saveCards([card]);

  const [loadedAgain] = loadCards();
  assert.equal(
    copySafeMoreArticleCard(loadedAgain).final_url,
    dataUrl,
    'localStorage 写失败后，当前会话仍必须用内存缓存保留带标题合成图'
  );
});
