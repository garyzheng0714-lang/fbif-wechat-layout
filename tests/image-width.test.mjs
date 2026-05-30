import test from 'node:test';
import assert from 'node:assert/strict';
import { adaptiveImageWidth } from '../public/js/image-width.js';

// 规则：竖图(高>宽)400，横图600，缺高度默认600。
// 样本取自基石 news/62285 真实图片（data-w × data-ratio 反推高度）。

test('横图（宽>高）→ 600（基石 11 张正文横图）', () => {
  // ratio 0.28~0.75 的横图，基石全 600。取 w=1080 反推 h。
  for (const ratio of [0.28, 0.36, 0.49, 0.56, 0.66, 0.67, 0.72, 0.75]) {
    const w = 1080, h = Math.round(w * ratio);
    assert.equal(adaptiveImageWidth(w, h), 600, `ratio=${ratio} 横图应 600`);
  }
});

test('竖图（高>宽）→ 400（基石奥普拉图 623×632, ratio≈1.01）', () => {
  assert.equal(adaptiveImageWidth(623, 632), 400);
});

test('正方偏竖（高略大于宽）→ 400', () => {
  assert.equal(adaptiveImageWidth(600, 601), 400);
});

test('正方（高=宽）→ 600（不算竖图）', () => {
  assert.equal(adaptiveImageWidth(600, 600), 600);
});

test('缺宽或高 → 默认 600', () => {
  assert.equal(adaptiveImageWidth(0, 0), 600);
  assert.equal(adaptiveImageWidth(600, 0), 600);
  assert.equal(adaptiveImageWidth(0, 600), 600);
  assert.equal(adaptiveImageWidth(undefined, undefined), 600);
});
