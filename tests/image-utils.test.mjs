import test from 'node:test';
import assert from 'node:assert/strict';
import { inferImageMimeFromBase64, inferWechatImageType, looksLikeGifSource } from '../public/js/image-utils.mjs';

test('looksLikeGifSource detects common gif URL variants', () => {
  assert.equal(looksLikeGifSource('https://example.com/a.gif'), true);
  assert.equal(looksLikeGifSource('https://mmbiz.qpic.cn/mmbiz_gif/abc/640'), true);
  assert.equal(looksLikeGifSource('https://mmbiz.qpic.cn/mmbiz_jpg/abc/640?wx_fmt=gif'), true);
  assert.equal(looksLikeGifSource('https://mmbiz.qpic.cn/mmbiz_png/abc/640?fmt=gif'), true);
  assert.equal(looksLikeGifSource('https://example.com/a.png?wx_fmt=png'), false);
  assert.equal(looksLikeGifSource('data:image/gif;base64,R0lGODlhAQABAIAAAAUEBA=='), true);
});

test('looksLikeGifSource handles encoded query params', () => {
  const encoded = 'https://example.com/cdn/image?redirect=https%3A%2F%2Fx.y%2Fz%3Fwx_fmt%3Dgif';
  assert.equal(looksLikeGifSource(encoded), true);
});

test('inferImageMimeFromBase64 prefers binary signature over file extension', () => {
  assert.equal(inferImageMimeFromBase64('R0lGODlhAQABAIAAAAUEBA==', 'png'), 'image/gif');
  assert.equal(inferImageMimeFromBase64('iVBORw0KGgoAAAANSUhEUgAA', 'jpg'), 'image/png');
  assert.equal(inferImageMimeFromBase64('/9j/4AAQSkZJRgABAQAAAQABAAD', 'gif'), 'image/jpeg');
  assert.equal(inferImageMimeFromBase64('UklGRlIAAABXRUJQVlA4', 'jpg'), 'image/webp');
});

test('inferImageMimeFromBase64 falls back to extension when signature unknown', () => {
  assert.equal(inferImageMimeFromBase64('AAAA', 'gif'), 'image/gif');
  assert.equal(inferImageMimeFromBase64('AAAA', 'unknown'), 'image/jpeg');
});

test('inferWechatImageType infers gif/png/jpg for wechat URLs', () => {
  assert.equal(inferWechatImageType('https://mmbiz.qpic.cn/mmbiz_gif/abc/0?from=appmsg'), 'gif');
  assert.equal(inferWechatImageType('https://mmbiz.qpic.cn/mmbiz_png/abc/0?from=appmsg'), 'png');
  assert.equal(inferWechatImageType('https://mmbiz.qpic.cn/mmbiz_jpg/abc/0?from=appmsg'), 'jpg');
  assert.equal(inferWechatImageType('https://x.com/a.jpg?wx_fmt=jpeg'), 'jpg');
  assert.equal(inferWechatImageType('https://x.com/a.bin'), '');
});
