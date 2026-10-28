// 导入框里粘进来的链接画成蓝色下划线（2026-10-28 用户：「这个输入框里，也要显示超链接的样式」）。
// 框后面垫一层同样的字，链接那几段单独包起来；哪几个字算链接与排版挂链接同一个认法。

import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';
import { writeImportLinks } from '../public/js/clipboard-links.js';
import { linkSegments, mountImportLinks } from '../public/js/import-links.js';
import { buildClipboardArticleBlocks } from '../public/js/parser.js';

const TEXT = '来f展，2027年4月21-23日在杭州。欢迎免费报名观展！\n\n看官网了解更多。';
const LINKS = [
  { text: '免费报名观展', href: 'https://mp.weixin.qq.com/s/1DgVTUgu0K-_h1gS0BnKWg' },
  { text: '官网', href: 'https://www.foodtalks.cn/' },
];

function page() {
  const { document, window } = parseHTML('<!doctype html><html><body><div class="import-composer-input"><textarea id="urlInput"></textarea></div></body></html>');
  window.getComputedStyle = () => ({ paddingRight: '14px', borderLeftWidth: '0px', borderRightWidth: '0px' });
  window.ResizeObserver = class { observe() {} };
  const input = document.getElementById('urlInput');
  mountImportLinks(input);
  const box = input.parentElement;
  return {
    input,
    hasLinks: () => box.classList.contains('has-links'),
    shownLinks: () => [...box.querySelectorAll('.import-textarea-link')].map(span => span.textContent),
    shownText: () => box.querySelector('.import-textarea-links').textContent.replace(/​$/, ''),
  };
}

const tick = () => new Promise(resolve => setTimeout(resolve, 0));

test('框里的字按锚文字切成段：链接那段带地址，换行原样留着', () => {
  assert.deepEqual(linkSegments(TEXT, LINKS), [
    { text: '来f展，2027年4月21-23日在杭州。欢迎' },
    { text: '免费报名观展', href: LINKS[0].href },
    { text: '！\n' },
    { text: '\n' },
    { text: '看' },
    { text: '官网', href: LINKS[1].href },
    { text: '了解更多。' },
  ]);
});

test('框里画成链接的字，就是排出来带链接的字', () => {
  const shown = linkSegments(TEXT, LINKS).filter(segment => segment.href);
  const laidOut = buildClipboardArticleBlocks(TEXT, { links: LINKS }).blocks
    .flatMap(block => block.runs.filter(run => run.type === 'link'))
    .map(run => ({ text: run.text, href: run.href }));
  assert.deepEqual(shown, laidOut);
});

test('粘进带链接的字：垫层与框里的字一字不差，链接那几个字单独标出来', async () => {
  const view = page();
  view.input.value = TEXT;
  writeImportLinks(view.input, LINKS);
  await tick();
  assert.equal(view.hasLinks(), true);
  assert.deepEqual(view.shownLinks(), ['免费报名观展', '官网']);
  assert.equal(view.shownText(), TEXT);
});

test('改了锚文字的那条不再是链接；一个链接都不剩就不垫', async () => {
  const view = page();
  view.input.value = TEXT;
  writeImportLinks(view.input, LINKS);
  await tick();

  view.input.value = TEXT.replace('免费报名观展', '免费报名');
  assert.deepEqual(view.shownLinks(), ['官网']);
  assert.equal(view.shownText(), TEXT.replace('免费报名观展', '免费报名'));

  view.input.value = '整框被别处换掉了';
  assert.equal(view.hasLinks(), false);
  assert.equal(view.shownText(), '');
});

test('没有链接时框照常显示，不垫', () => {
  const view = page();
  view.input.value = TEXT;
  assert.equal(view.hasLinks(), false);
  assert.deepEqual(view.shownLinks(), []);
});
