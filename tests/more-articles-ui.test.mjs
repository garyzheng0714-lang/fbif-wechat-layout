import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  buildCoverPickerSources,
  coverPickerImageSource,
  shouldAutoFetchMoreArticleUrl,
} from '../public/js/more-articles-ui.js';

test('cover picker loads page-local article images directly', () => {
  assert.deepEqual(
    coverPickerImageSource('blob:http://127.0.0.1:19092/abc'),
    { src: 'blob:http://127.0.0.1:19092/abc', crossOrigin: false }
  );
  assert.deepEqual(
    coverPickerImageSource('/api/doc-cache/hash/image.png'),
    { src: '/api/doc-cache/hash/image.png', crossOrigin: false }
  );
});

test('cover picker loads same-origin absolute URLs directly', () => {
  const previous = globalThis.location;
  Object.defineProperty(globalThis, 'location', {
    value: { origin: 'http://127.0.0.1:19092' },
    configurable: true,
  });
  try {
    assert.deepEqual(
      coverPickerImageSource('http://127.0.0.1:19092/api/doc-cache/hash/image.png'),
      { src: 'http://127.0.0.1:19092/api/doc-cache/hash/image.png', crossOrigin: false }
    );
  } finally {
    if (previous === undefined) {
      delete globalThis.location;
    } else {
      Object.defineProperty(globalThis, 'location', { value: previous, configurable: true });
    }
  }
});

test('cover picker proxies remote article images for canvas access', () => {
  assert.deepEqual(
    coverPickerImageSource('https://mmbiz.qpic.cn/sz_mmbiz_jpg/cover/0?wx_fmt=jpeg'),
    {
      src: '/api/image-proxy?url=https%3A%2F%2Fmmbiz.qpic.cn%2Fsz_mmbiz_jpg%2Fcover%2F0%3Fwx_fmt%3Djpeg',
      crossOrigin: true,
    }
  );
  assert.deepEqual(
    coverPickerImageSource('//mmbiz.qpic.cn/sz_mmbiz_jpg/cover/0?wx_fmt=jpeg'),
    {
      src: '/api/image-proxy?url=https%3A%2F%2Fmmbiz.qpic.cn%2Fsz_mmbiz_jpg%2Fcover%2F0%3Fwx_fmt%3Djpeg',
      crossOrigin: true,
    }
  );
});

test('cover picker uses the current more-article media library before main article images', () => {
  const sources = buildCoverPickerSources(
    {
      cover_data_url: 'data:image/jpeg;base64,custom',
      imgurl: 'https://mmbiz.qpic.cn/cover.jpg?wx_fmt=jpeg',
      image_urls: [
        'https://mmbiz.qpic.cn/body-1.jpg?wx_fmt=jpeg',
        'https://mmbiz.qpic.cn/cover.jpg?wx_fmt=jpeg',
      ],
    },
    [
      'https://mmbiz.qpic.cn/body-2.jpg?wx_fmt=jpeg',
      'https://mmbiz.qpic.cn/body-1.jpg?wx_fmt=jpeg',
    ]
  );

  assert.deepEqual(sources, [
    'data:image/jpeg;base64,custom',
    'https://mmbiz.qpic.cn/cover.jpg?wx_fmt=jpeg',
    'https://mmbiz.qpic.cn/body-1.jpg?wx_fmt=jpeg',
    'https://mmbiz.qpic.cn/body-2.jpg?wx_fmt=jpeg',
  ]);
});

test('more article URL input auto-fetches when mobile paste only fires input', async () => {
  assert.equal(
    shouldAutoFetchMoreArticleUrl('https://mp.weixin.qq.com/s/mobile-input-only', ''),
    true
  );
  assert.equal(
    shouldAutoFetchMoreArticleUrl(
      'https://mp.weixin.qq.com/s/mobile-input-only',
      'https://mp.weixin.qq.com/s/mobile-input-only'
    ),
    false
  );

  const source = await readFile(new URL('../public/js/more-articles-ui.js', import.meta.url), 'utf8');
  const inputHandler = source.slice(source.indexOf("$('url').addEventListener('input'"));
  assert.match(inputHandler, /scheduleAutoFetch\(\)/);
});
