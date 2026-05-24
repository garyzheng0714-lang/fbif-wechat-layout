import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('rule admin styles live outside app.css and app.css stays below the project line cap', async () => {
  const app = await readFile(new URL('../public/app.html', import.meta.url), 'utf8');
  const appCss = await readFile(new URL('../public/app.css', import.meta.url), 'utf8');
  const adminCss = await readFile(new URL('../public/admin-panel.css', import.meta.url), 'utf8');

  assert.match(app, /<link rel="stylesheet" href="\/admin-panel\.css">/);
  assert.ok(appCss.split(/\r?\n/).length < 900, 'public/app.css must stay below 900 lines');
  assert.doesNotMatch(appCss, /Rule admin panel/);
  assert.match(adminCss, /Rule admin panel/);
  assert.match(adminCss, /\.admin-overlay/);
  assert.match(adminCss, /\.admin-dialog/);
});
