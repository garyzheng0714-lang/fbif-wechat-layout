import test from 'node:test';
import assert from 'node:assert/strict';
import {
  localPreviewImageRefs,
  preloadLocalPreviewImages,
  refreshLocalPreviewImages,
} from '../public/js/preview-image-loader.js';

test('localPreviewImageRefs only keeps unique in-memory DOCX image refs', () => {
  assert.deepEqual(localPreviewImageRefs({
    a: 'blob:first',
    b: 'https://example.com/remote.png',
    c: 'blob:first',
    d: 'blob:second',
  }), ['blob:first', 'blob:second']);
});

test('localPreviewImageRefs only decodes blob refs actually rendered in the article', () => {
  assert.deepEqual(localPreviewImageRefs({
    unused: 'blob:unused-large-image',
    second: 'blob:second',
    first: 'blob:first',
  }, '<section><img src="blob:first"><img src="blob:second"></section>'), [
    'blob:first',
    'blob:second',
  ]);
});

test('preloadLocalPreviewImages decodes local images concurrently', async () => {
  let active = 0;
  let maxActive = 0;
  const createImage = () => ({
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
  }, { createImage, timeoutMs: 100 });

  assert.equal(decoded.size, 3);
  assert.ok(maxActive > 1, `expected parallel decode, observed ${maxActive}`);
  assert.equal(decoded.get('blob:first').naturalWidth, 640);
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

test('refreshLocalPreviewImages re-decodes detached nodes immediately before commit', async () => {
  let calls = 0;
  const image = {
    complete: true,
    naturalWidth: 640,
    async decode() { calls++; },
  };
  const prepared = new Map([['blob:hero', image]]);

  const refreshed = await refreshLocalPreviewImages(prepared);

  assert.equal(refreshed, prepared);
  assert.equal(calls, 1);
  assert.equal(refreshed.get('blob:hero'), image);
});
