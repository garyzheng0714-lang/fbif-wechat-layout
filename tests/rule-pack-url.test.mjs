import test from 'node:test';
import assert from 'node:assert/strict';
import { parsePath, buildPath } from '../public/js/rule-packs/url.js';

test('parsePath /mp/fbif returns channel=mp, packId=fbif', () => {
  assert.deepEqual(parsePath('/mp/fbif'), { channel: 'mp', packId: 'fbif' });
});

test('parsePath /mp/foodtalks-series handles hyphenated IDs', () => {
  assert.deepEqual(parsePath('/mp/foodtalks-series'), { channel: 'mp', packId: 'foodtalks-series' });
});

test('parsePath /ft/default returns channel=ft', () => {
  assert.deepEqual(parsePath('/ft/default'), { channel: 'ft', packId: 'default' });
});

test('parsePath /mp returns channel=mp, packId=null (no ID provided)', () => {
  assert.deepEqual(parsePath('/mp'), { channel: 'mp', packId: null });
});

test('parsePath /mp/ (trailing slash) is treated the same as /mp', () => {
  assert.deepEqual(parsePath('/mp/'), { channel: 'mp', packId: null });
});

test('parsePath returns null for non-channel paths', () => {
  assert.equal(parsePath('/'), null);
  assert.equal(parsePath('/app.html'), null);
  assert.equal(parsePath('/api/health'), null);
  assert.equal(parsePath(''), null);
});

test('parsePath rejects unknown channels', () => {
  assert.equal(parsePath('/xx/fbif'), null);
  assert.equal(parsePath('/admin/fbif'), null);
});

test('parsePath ignores trailing path segments', () => {
  assert.deepEqual(parsePath('/mp/fbif/extra'), { channel: 'mp', packId: 'fbif' });
});

test('buildPath constructs /mp/fbif', () => {
  assert.equal(buildPath('mp', 'fbif'), '/mp/fbif');
});

test('buildPath URL-encodes the packId', () => {
  assert.equal(buildPath('mp', 'foo bar'), '/mp/foo%20bar');
});

test('buildPath throws on unknown channel', () => {
  assert.throws(() => buildPath('xx', 'fbif'), /channel/);
});

test('buildPath throws on empty packId', () => {
  assert.throws(() => buildPath('mp', ''), /packId/);
  assert.throws(() => buildPath('mp', null), /packId/);
});
