import test from 'node:test';
import assert from 'node:assert/strict';
import { adaptiveImageWidth } from '../public/js/image-width.js';

// 规则（2026-06-11 飞书反馈问题十一/十五 + 成品 news/62303 实测）：
//   横图(宽>高)600；纵图(高≥宽,含1:1)长边375、宽按比例；二维码(近正方小图)150；缺高度默认600。

test('横图（宽>高）→ 600（基石 11 张正文横图）', () => {
  // ratio 0.28~0.75 的横图，基石全 600。取 w=1080 反推 h。
  for (const ratio of [0.28, 0.36, 0.49, 0.56, 0.66, 0.67, 0.72, 0.75]) {
    const w = 1080, h = Math.round(w * ratio);
    assert.equal(adaptiveImageWidth(w, h), 600, `ratio=${ratio} 横图应 600`);
  }
});

test('纵图（高≥宽）→ 高 375、宽按比例（62303 实测 840×856 → 368）', () => {
  assert.equal(adaptiveImageWidth(840, 856), 368);
});

test('1:1 大图 → 375（反馈问题十一：1:1 也按纵图处理）', () => {
  assert.equal(adaptiveImageWidth(1080, 1080), 375);
  assert.equal(adaptiveImageWidth(1200, 1200), 375);
});

test('正方偏竖大图（高略大于宽）→ 宽按比例', () => {
  assert.equal(adaptiveImageWidth(623, 632), Math.round(375 * 623 / 632));
});

test('二维码（近正方形小图）→ 150（62303 两枚二维码 161×163 / 265×266）', () => {
  assert.equal(adaptiveImageWidth(161, 163), 150);
  assert.equal(adaptiveImageWidth(265, 266), 150);
  assert.equal(adaptiveImageWidth(300, 300), 150);
});

test('小横图（非近正方形）不按二维码处理 → 600（62303 logo 286×190 走横图）', () => {
  assert.equal(adaptiveImageWidth(286, 190), 600);
});

test('缺宽或高 → 默认 600', () => {
  assert.equal(adaptiveImageWidth(0, 0), 600);
  assert.equal(adaptiveImageWidth(600, 0), 600);
  assert.equal(adaptiveImageWidth(0, 600), 600);
  assert.equal(adaptiveImageWidth(undefined, undefined), 600);
});

test('规则值可由 opts 注入（红线下放）', () => {
  assert.equal(adaptiveImageWidth(800, 800, { portrait: 300 }), 300);
  assert.equal(adaptiveImageWidth(161, 163, { qr: 120 }), 120);
  assert.equal(adaptiveImageWidth(1080, 540, { landscape: 500 }), 500);
});
