import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

import { collectReusableArticleImages } from '../public/js/preview-dom.js';

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
