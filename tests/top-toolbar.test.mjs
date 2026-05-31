import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

async function readShell(fileName) {
  return readFile(new URL(`../public/${fileName}`, import.meta.url), 'utf8');
}

function assertToolbarInGlobalHeader(html, label) {
  const headerStart = html.indexOf('<header class="global-channel-bar">');
  const headerEnd = html.indexOf('</header>', headerStart);
  const appLayoutStart = html.indexOf('<div class="app-layout">');
  const previewPanelStart = html.indexOf('<main class="preview-panel');
  const previewPanelEnd = html.indexOf('<div class="preview-scroll"', previewPanelStart);
  const header = html.slice(headerStart, headerEnd);
  const previewPanelTop = html.slice(previewPanelStart, previewPanelEnd);

  assert.ok(headerStart >= 0, `${label} should have the global channel bar`);
  assert.ok(headerEnd > headerStart, `${label} should close the global channel bar before the app layout`);
  assert.ok(headerEnd < appLayoutStart, `${label} header should sit above the app layout`);
  assert.match(header, /id="previewToolbar"/, `${label} toolbar should live in the global top bar`);
  assert.match(header, /id="adminLockBtn"/, `${label} rule admin button should live in the top bar`);
  assert.match(header, /id="copyBtn"/, `${label} copy button should live in the top bar`);
  assert.doesNotMatch(previewPanelTop, /id="previewToolbar"/, `${label} toolbar must not sit inside the article preview panel`);
}

test('app toolbar lives in the global top-right bar, not the article preview panel', async () => {
  assertToolbarInGlobalHeader(await readShell('app.html'), 'app.html');
});

test('site toolbar lives in the global top-right bar, not the article preview panel', async () => {
  assertToolbarInGlobalHeader(await readShell('site.html'), 'site.html');
});
