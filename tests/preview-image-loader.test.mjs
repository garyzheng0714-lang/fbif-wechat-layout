import test from 'node:test';
import assert from 'node:assert/strict';
import {
  localPreviewImageRefs,
  preloadLocalPreviewImages,
} from '../public/js/preview-image-loader.js';

test('localPreviewImageRefs only keeps unique in-memory DOCX image refs', () => {
  assert.deepEqual(localPreviewImageRefs({
    a: 'blob:first',
    b: 'https://example.com/remote.png',
    c: 'blob:first',
    d: 'blob:second',
  }), ['blob:first', 'blob:second']);
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

test('preloadLocalPreviewImages fails open when an image cannot decode', async () => {
  const decoded = await preloadLocalPreviewImages({
    broken: 'blob:broken',
  }, {
    timeoutMs: 5,
    createImage: () => ({
      complete: false,
      naturalWidth: 0,
      onload: null,
      onerror: null,
      set src(value) { this.currentSrc = value; },
      decode: () => new Promise(() => {}),
    }),
  });

  assert.equal(decoded.size, 0);
});
