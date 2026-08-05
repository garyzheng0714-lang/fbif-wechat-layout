import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { parseHTML } from 'linkedom';
import { mountCopySplitButton } from '../public/js/copy-split.js';

const appShellCSS = await readFile(new URL('../public/css/app-shell.css', import.meta.url), 'utf8');
const editorShellJS = await readFile(new URL('../public/js/editor-shell.js', import.meta.url), 'utf8');

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

test('复制分体按钮在桌面和窄屏都由同一容器拥有高度', () => {
  assert.match(
    appShellCSS,
    /\.copy-split\s*\{[\s\S]*?--copy-split-control-size:\s*36px;[\s\S]*?height:\s*var\(--copy-split-control-size\);/,
    '分体按钮容器应拥有统一的桌面高度',
  );
  assert.match(
    appShellCSS,
    /\.global-channel-bar \.copy-split > \.copy-split-primary\s*\{[\s\S]*?height:\s*100%;/,
    '主按钮应继承分体容器高度',
  );
  assert.match(
    appShellCSS,
    /\.copy-split-toggle\s*\{[\s\S]*?height:\s*100%;/,
    '下拉按钮应继承分体容器高度',
  );
  assert.match(
    appShellCSS,
    /@media \(max-width:\s*820px\), \(pointer:\s*coarse\)\s*\{[\s\S]*?\.copy-split\s*\{\s*--copy-split-control-size:\s*44px;\s*\}[\s\S]*?\.copy-split-toggle\s*\{\s*width:\s*44px;\s*\}/,
    '窄屏和触屏应同步放大整个分体按钮，而不是只放大一半',
  );
});

test('复制分体按钮按下主操作时不破坏两段接缝', () => {
  assert.match(
    appShellCSS,
    /\.global-channel-bar \.copy-split > \.copy-split-primary:active\s*\{\s*transform:\s*none;\s*\}/,
    '主按钮按下时不得单独缩放并撕开分体按钮接缝',
  );
});

test('复制警告态统一覆盖分体按钮两段，不出现左橙右绿', () => {
  assert.match(appShellCSS, /\.copy-split\.is-warning \.copy-split-toggle\s*\{[\s\S]*?background:\s*var\(--fs-warning\);/);
  assert.match(appShellCSS, /\.btn-primary\.is-warning\s*\{\s*background:\s*var\(--fs-warning\);\s*\}/);
  assert.match(editorShellJS, /btn\.classList\.remove\('is-warning'\)/, '下一篇或返回时应清理主按钮警告态');
  assert.match(editorShellJS, /group\.classList\.remove\('is-warning'\)/, '下一篇或返回时应清理分体按钮警告态');
});

test('规则阻断的禁用态覆盖成功、警告和 hover 状态', () => {
  const mainWarning = appShellCSS.indexOf('.btn-primary.is-warning');
  const mainDisabled = appShellCSS.indexOf('.btn-primary:disabled');
  const toggleWarning = appShellCSS.indexOf('.copy-split.is-warning .copy-split-toggle');
  const toggleDisabled = appShellCSS.indexOf('.copy-split .copy-split-toggle:disabled');
  assert.ok(mainDisabled > mainWarning, '主按钮禁用态应写在成功/警告态之后');
  assert.ok(toggleDisabled > toggleWarning, '下拉按钮禁用态应写在成功/警告态之后');
  assert.match(appShellCSS, /\.btn-primary:disabled\s*\{[\s\S]*?cursor:\s*not-allowed;/);
  assert.match(appShellCSS, /\.copy-split \.copy-split-toggle:disabled\s*\{[\s\S]*?cursor:\s*not-allowed;/);
});

test('规则锁在 HTML 复制途中生效后，异步 finally 不得重新启用按钮', async () => {
  let resolveCopy;
  const { window, document } = fixture(() => new Promise(resolve => { resolveCopy = resolve; }));
  mountCopySplitButton({ windowRef: window, documentRef: document });
  document.getElementById('copyMenuBtn').click();
  document.getElementById('copyHtmlBtn').click();
  assert.equal(document.getElementById('copyHtmlBtn').dataset.copyPending, 'true');
  assert.equal(document.getElementById('copyHtmlBtn').getAttribute('aria-busy'), 'true');
  document.getElementById('copyHtmlBtn').dataset.ruleBlocked = 'true';
  resolveCopy(true);
  await Promise.resolve();
  await Promise.resolve();

  assert.equal(document.getElementById('copyHtmlBtn').disabled, true);
  assert.equal(document.getElementById('copyHtmlBtn').hasAttribute('aria-busy'), false);
});
