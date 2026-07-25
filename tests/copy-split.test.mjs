import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { mountCopySplitButton } from '../public/js/copy-split.js';

function fixture(copyHTMLSource = async () => true) {
  const { window, document } = parseHTML(`<!doctype html><html><body>
    <div class="copy-split" id="copySplit">
      <button id="copyBtn"><span data-copy-label>复制全文</span></button>
      <button id="copyMenuBtn" aria-expanded="false" aria-controls="copyMenu"></button>
      <div id="copyMenu" role="menu" hidden>
        <button id="copyHtmlBtn" role="menuitem">复制 HTML</button>
      </div>
    </div>
    <div id="copySuccess">已复制到剪贴板</div>
  </body></html>`);
  window.copyHTMLSource = copyHTMLSource;
  window.setTimeout = () => 1;
  document.defaultView.HTMLElement.prototype.focus = function focus() {
    document._focused = this;
  };
  return { window, document };
}

test('复制分裂按钮由独立箭头展开，Escape 关闭并把焦点还给箭头', () => {
  const { window, document } = fixture();
  mountCopySplitButton({ windowRef: window, documentRef: document });
  const trigger = document.getElementById('copyMenuBtn');
  const menu = document.getElementById('copyMenu');

  trigger.click();
  assert.equal(menu.hidden, false);
  assert.equal(trigger.getAttribute('aria-expanded'), 'true');
  const escape = new window.Event('keydown', { bubbles: true });
  Object.defineProperty(escape, 'key', { value: 'Escape' });
  document.dispatchEvent(escape);
  assert.equal(menu.hidden, true);
  assert.equal(trigger.getAttribute('aria-expanded'), 'false');
  assert.equal(document._focused, trigger);
});

test('复制 HTML 调用最终 HTML 源码复制并关闭菜单', async () => {
  let calls = 0;
  const { window, document } = fixture(async () => { calls += 1; return true; });
  mountCopySplitButton({ windowRef: window, documentRef: document });
  document.getElementById('copyMenuBtn').click();
  document.getElementById('copyHtmlBtn').click();
  await Promise.resolve();
  await Promise.resolve();

  assert.equal(calls, 1);
  assert.equal(document.getElementById('copyMenu').hidden, true);
  assert.match(document.getElementById('copySuccess').textContent, /HTML/);
});
