import test from 'node:test';
import assert from 'node:assert/strict';
import {
  articlePreviewImageRequests,
  articlePreviewImageRefs,
  localPreviewImageRefs,
  preloadArticlePreviewImages,
  preloadLocalPreviewImages,
  refreshLocalPreviewImages,
} from '../public/js/preview-image-loader.js';

test('article preview requests preserve final fetch attributes for every image occurrence', () => {
  assert.deepEqual(articlePreviewImageRequests([
    '<img src="https://oss.example/hero.png" referrerpolicy="no-referrer"',
    ' crossorigin="anonymous" srcset="hero@2x.png 2x" sizes="100vw">',
  ].join('')), [{
    src: 'https://oss.example/hero.png',
    attributes: {
      crossorigin: 'anonymous',
      referrerpolicy: 'no-referrer',
      srcset: 'hero@2x.png 2x',
      sizes: '100vw',
    },
  }]);
});

test('articlePreviewImageRefs keeps every rendered image in document order', () => {
  assert.deepEqual(articlePreviewImageRefs([
    '<img src="https://oss.example/hero.png">',
    '<img src="data:image/png;base64,AQID">',
    '<img src="https://oss.example/hero.png">',
  ].join('')), [
    'https://oss.example/hero.png',
    'data:image/png;base64,AQID',
    'https://oss.example/hero.png',
  ]);
});

test('localPreviewImageRefs preserves one local reference per rendered occurrence', () => {
  assert.deepEqual(localPreviewImageRefs({
    a: 'blob:first',
    b: 'https://example.com/remote.png',
    c: 'blob:first',
    d: 'blob:second',
  }), ['blob:first', 'blob:first', 'blob:second']);
});

test('localPreviewImageRefs only decodes blob refs actually rendered, including repeats in order', () => {
  assert.deepEqual(localPreviewImageRefs({
    unused: 'blob:unused-large-image',
    second: 'blob:second',
    first: 'blob:first',
  }, '<section><img src="blob:first"><img src="blob:second"><img src="blob:first"></section>'), [
    'blob:first',
    'blob:second',
    'blob:first',
  ]);
});

test('preloadLocalPreviewImages bounds concurrency and prepares every repeated occurrence independently', async () => {
  let active = 0;
  let maxActive = 0;
  let created = 0;
  const createImage = () => ({
    id: ++created,
    complete: false,
    naturalWidth: 0,
    onload: null,
    onerror: null,
    set src(value) { this.currentSrc = value; },
    async decode() {
      active++;
      maxActive = Math.max(maxActive, active);
      await new Promise(resolve => setTimeout(resolve, 5));
      active--;
      this.complete = true;
      this.naturalWidth = 640;
    },
  });

  const decoded = await preloadLocalPreviewImages({
    first: 'blob:first',
    second: 'blob:second',
    third: 'blob:third',
  }, {
    articleHtml: [
      '<img src="blob:first">',
      '<img src="blob:second">',
      '<img src="blob:first">',
      '<img src="blob:third">',
    ].join(''),
    createImage,
    timeoutMs: 100,
    concurrency: 2,
  });

  assert.equal(decoded.size, 3);
  assert.ok(maxActive > 1, `expected parallel decode, observed ${maxActive}`);
  assert.ok(maxActive <= 2, `expected bounded decode, observed ${maxActive}`);
  assert.equal(created, 4);
  assert.equal(decoded.get('blob:first').length, 2);
  assert.notEqual(decoded.get('blob:first')[0], decoded.get('blob:first')[1]);
  assert.equal(decoded.get('blob:first')[0].naturalWidth, 640);
});

test('preloadLocalPreviewImages does not treat dimensions or load as decoded pixels', async () => {
  let releaseDecode;
  let settled = false;
  const image = {
    complete: true,
    naturalWidth: 640,
    onload: null,
    onerror: null,
    set src(value) { this.currentSrc = value; },
    decode: () => new Promise(resolve => { releaseDecode = resolve; }),
  };
  const pending = preloadLocalPreviewImages({
    hero: 'blob:hero',
  }, {
    articleHtml: '<img src="blob:hero">',
    createImage: () => image,
  }).then(result => {
    settled = true;
    return result;
  });

  await Promise.resolve();
  image.onload?.();
  await Promise.resolve();
  assert.equal(settled, false);

  releaseDecode();
  const decoded = await pending;
  assert.equal(decoded.get('blob:hero'), image);
});

test('preloadLocalPreviewImages fails open when an image decode rejects', async () => {
  const decoded = await preloadLocalPreviewImages({
    broken: 'blob:broken',
  }, {
    articleHtml: '<img src="blob:broken">',
    createImage: () => ({
      complete: false,
      naturalWidth: 0,
      onload: null,
      onerror: null,
      set src(value) { this.currentSrc = value; },
      decode: async () => { throw new Error('broken image'); },
    }),
  });

  assert.equal(decoded.size, 0);
});

test('preloadArticlePreviewImages reports failures, bounds concurrency, and keeps occurrence nodes', async () => {
  const progress = [];
  let active = 0;
  let maxActive = 0;
  let created = 0;
  const result = await preloadArticlePreviewImages(
    [
      '<img src="https://oss.example/good.png">',
      '<img src="https://oss.example/broken.png">',
      '<img src="https://oss.example/good.png">',
    ].join(''),
    {
      concurrency: 2,
      onProgress(done, total) { progress.push([done, total]); },
      createImage: () => ({
        id: ++created,
        complete: false,
        naturalWidth: 0,
        onload: null,
        onerror: null,
        set src(value) { this.currentSrc = value; },
        async decode() {
          active++;
          maxActive = Math.max(maxActive, active);
          await new Promise(resolve => setTimeout(resolve, 2));
          active--;
          if (this.currentSrc.includes('broken')) throw new Error('broken');
          this.complete = true;
          this.naturalWidth = 640;
        },
      }),
    },
  );

  assert.equal(result.total, 3);
  assert.equal(result.images.size, 1);
  assert.deepEqual(result.failedSrcs, ['https://oss.example/broken.png']);
  assert.deepEqual(progress.at(-1), [3, 3]);
  assert.equal(created, 3);
  assert.ok(maxActive <= 2);
  const repeated = result.images.get('https://oss.example/good.png');
  assert.equal(repeated.length, 2);
  assert.notEqual(repeated[0], repeated[1]);
});

test('preloadArticlePreviewImages applies request attributes before src and decode', async () => {
  const events = [];
  const image = {
    complete: false,
    naturalWidth: 0,
    onload: null,
    onerror: null,
    setAttribute(name, value) {
      events.push(`${name}=${value}`);
    },
    set src(value) {
      events.push(`src=${value}`);
    },
    async decode() {
      events.push('decode');
      this.complete = true;
      this.naturalWidth = 1001;
    },
  };

  const result = await preloadArticlePreviewImages(
    '<img src="https://oss.example/mote.png" referrerpolicy="no-referrer">',
    { createImage: () => image },
  );

  assert.equal(result.failedSrcs.length, 0);
  assert.deepEqual(events, [
    'referrerpolicy=no-referrer',
    'src=https://oss.example/mote.png',
    'decode',
  ]);
});

test('preloadArticlePreviewImages aborts promptly and never starts more than the bounded workers', async () => {
  const controller = new AbortController();
  let created = 0;
  const pending = preloadArticlePreviewImages(
    Array.from({ length: 8 }, (_, index) => `<img src="https://oss.example/${index}.png">`).join(''),
    {
      concurrency: 2,
      signal: controller.signal,
      createImage: () => ({
        complete: false,
        naturalWidth: 0,
        onload: null,
        onerror: null,
        set src(value) {
          this.currentSrc = value;
          if (value) created++;
        },
        decode: () => new Promise(() => {}),
      }),
    },
  );
  await Promise.resolve();
  controller.abort();

  await assert.rejects(pending, error => error?.name === 'AbortError');
  assert.equal(created, 2, 'only active bounded workers may start image work before abort');
});

test('refreshLocalPreviewImages re-decodes every occurrence with bounded concurrency', async () => {
  let active = 0;
  let maxActive = 0;
  let calls = 0;
  const makeImage = () => ({
    complete: true,
    naturalWidth: 640,
    async decode() {
      calls++;
      active++;
      maxActive = Math.max(maxActive, active);
      await new Promise(resolve => setTimeout(resolve, 3));
      active--;
    },
  });
  const prepared = new Map([
    ['blob:hero', [makeImage(), makeImage(), makeImage()]],
    ['blob:other', [makeImage(), makeImage()]],
  ]);

  const refreshed = await refreshLocalPreviewImages(prepared, {
    concurrency: 2,
    refs: ['blob:hero', 'blob:other', 'blob:hero', 'blob:other', 'blob:hero'],
  });

  assert.equal(refreshed, prepared);
  assert.equal(calls, 5);
  assert.ok(maxActive <= 2, `expected at most 2 refreshes, observed ${maxActive}`);
});

test('refreshLocalPreviewImages stops latest-wins work promptly on AbortSignal', async () => {
  const controller = new AbortController();
  let started = 0;
  const makeImage = () => ({
    complete: true,
    naturalWidth: 640,
    decode() {
      started++;
      return new Promise(() => {});
    },
  });
  const prepared = new Map([[
    'blob:hero',
    Array.from({ length: 6 }, () => makeImage()),
  ]]);
  const pending = refreshLocalPreviewImages(prepared, {
    concurrency: 2,
    signal: controller.signal,
    refs: Array.from({ length: 6 }, () => 'blob:hero'),
  });
  await Promise.resolve();
  controller.abort();

  await assert.rejects(pending, error => error?.name === 'AbortError');
  assert.equal(started, 2);
});
