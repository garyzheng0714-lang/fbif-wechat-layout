import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

import {
  commitPreparedPreviewContent,
  collectReusableArticleImages,
  indexPreparedPreviewImages,
  prepareConnectedPreviewContent,
  preparePreviewContent,
  renderPreviewContent,
  validatePreparedPreview,
} from '../public/js/preview-dom.js';
import { preloadArticlePreviewImages } from '../public/js/preview-image-loader.js';

test('rerender reuses every paint-ready article image, including OSS and data URLs', () => {
  const { document, window } = parseHTML(`
    <div id="contentArea">
      <img id="oss" src="https://oss.example/hero.png">
      <img id="data" src="data:image/png;base64,AQID">
      <img id="pending" src="https://oss.example/pending.png">
    </div>
  `);
  const previousNode = globalThis.Node;
  globalThis.Node = window.Node;
  try {
    const oss = document.getElementById('oss');
    const data = document.getElementById('data');
    const pending = document.getElementById('pending');
    Object.defineProperties(oss, {
      complete: { value: true },
      naturalWidth: { value: 800 },
    });
    Object.defineProperties(data, {
      complete: { value: true },
      naturalWidth: { value: 640 },
    });
    Object.defineProperties(pending, {
      complete: { value: false },
      naturalWidth: { value: 0 },
    });

    const reusable = collectReusableArticleImages(document.getElementById('contentArea'));

    assert.equal(reusable.get('https://oss.example/hero.png'), oss);
    assert.equal(reusable.get('data:image/png;base64,AQID'), data);
    assert.equal(reusable.has('https://oss.example/pending.png'), false);
  } finally {
    globalThis.Node = previousNode;
  }
});

test('atomic preview prepares and consumes one paint-ready node for every repeated image src', async () => {
  const { document, window } = parseHTML('<div id="contentArea"></div>');
  const previousDocument = globalThis.document;
  const previousNode = globalThis.Node;
  globalThis.document = document;
  globalThis.Node = window.Node;
  try {
    let created = 0;
    const progress = [];
    const preloaded = await preloadArticlePreviewImages(
      [
        '<p><img src="https://oss.example/repeated.png" alt="A"></p>',
        '<p><img src="https://oss.example/repeated.png" alt="B"></p>',
      ].join(''),
      {
        createImage() {
          const image = document.createElement('img');
          image.dataset.prepared = String(++created);
          Object.defineProperties(image, {
            complete: { value: true },
            naturalWidth: { value: 800 },
            decode: { value: async () => {} },
          });
          return image;
        },
        onProgress(done, total) {
          progress.push([done, total]);
        },
      },
    );

    assert.equal(preloaded.total, 2, 'total counts rendered nodes, not unique URLs');
    assert.equal(created, 2, 'each repeated occurrence must have its own decoded node');
    assert.deepEqual(progress.at(-1), [2, 2]);
    assert.equal(preloaded.images.get('https://oss.example/repeated.png').length, 2);

    const prepared = preloaded.images.get('https://oss.example/repeated.png');

    renderPreviewContent(
      document.getElementById('contentArea'),
      [
        '<p><img src="https://oss.example/repeated.png" alt="A"></p>',
        '<p><img src="https://oss.example/repeated.png" alt="B"></p>',
      ].join(''),
      '',
      { preparedImages: preloaded.images },
    );

    const rendered = Array.from(document.querySelectorAll('#contentArea img'));
    assert.equal(rendered.length, 2);
    assert.equal(rendered[0], prepared[0]);
    assert.equal(rendered[1], prepared[1]);
    assert.equal(rendered[0].getAttribute('alt'), 'A');
    assert.equal(rendered[1].getAttribute('alt'), 'B');
    assert.equal(preloaded.images.has('https://oss.example/repeated.png'), false);
  } finally {
    globalThis.document = previousDocument;
    globalThis.Node = previousNode;
  }
});

test('detached preview validation cannot replace the current article before commit', () => {
  const { document, window } = parseHTML('<div id="contentArea"><p id="old">上一篇</p></div>');
  const previousDocument = globalThis.document;
  const previousNode = globalThis.Node;
  globalThis.document = document;
  globalThis.Node = window.Node;
  try {
    const prepared = preparePreviewContent(
      '<p>新文章<img src="https://oss.example/not-ready.png"></p>',
      '<p>新页脚</p>',
    );
    const result = validatePreparedPreview(prepared);

    assert.equal(result.total, 1);
    assert.equal(result.ready, 0);
    assert.equal(document.getElementById('old').textContent, '上一篇');

    const image = prepared.images[0];
    Object.defineProperties(image, {
      complete: { value: true },
      naturalWidth: { value: 800 },
    });
    assert.equal(validatePreparedPreview(prepared).ready, 1);

    commitPreparedPreviewContent(document.getElementById('contentArea'), prepared);
    assert.equal(document.getElementById('old'), null);
    assert.match(document.getElementById('contentArea').textContent, /新文章/);
  } finally {
    globalThis.document = previousDocument;
    globalThis.Node = previousNode;
  }
});

test('detached preview indexes the exact final nodes for the last decode barrier', () => {
  const { document, window } = parseHTML('<div></div>');
  const previousDocument = globalThis.document;
  const previousNode = globalThis.Node;
  globalThis.document = document;
  globalThis.Node = window.Node;
  try {
    const prepared = preparePreviewContent(
      '<img src="oss:repeat"><img src="oss:repeat"><img src="oss:other">',
      '',
    );
    const indexed = indexPreparedPreviewImages(prepared);

    assert.deepEqual(indexed.get('oss:repeat'), prepared.images.slice(0, 2));
    assert.equal(indexed.get('oss:other'), prepared.images[2]);
  } finally {
    globalThis.document = previousDocument;
    globalThis.Node = previousNode;
  }
});

test('connected staging keeps the old preview until one synchronous direct-child commit', () => {
  const { document, window } = parseHTML(
    '<html><body><div id="contentArea"><p id="old">上一篇</p></div></body></html>',
  );
  const previousDocument = globalThis.document;
  const previousNode = globalThis.Node;
  globalThis.document = document;
  globalThis.Node = window.Node;
  try {
    const prepared = prepareConnectedPreviewContent(
      '<article id="new">新文章<img src="oss:hero"></article>',
      '<footer id="new-footer">新页脚</footer>',
    );

    assert.equal(document.getElementById('old').textContent, '上一篇');
    assert.equal(prepared.staging.isConnected, true);
    assert.equal(prepared.images.length, 1);

    commitPreparedPreviewContent(document.getElementById('contentArea'), prepared);

    assert.equal(document.getElementById('old'), null);
    assert.equal(document.getElementById('new').parentElement.id, 'contentArea');
    assert.equal(document.getElementById('new-footer').parentElement.id, 'contentArea');
    assert.equal(document.querySelector('[data-preview-staging]'), null);
  } finally {
    globalThis.document = previousDocument;
    globalThis.Node = previousNode;
  }
});
