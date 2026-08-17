import test from 'node:test';
import assert from 'node:assert/strict';
import { adaptiveImageWidth } from '../public/js/image-width.js';

// 当前规则（2026-08-14）：横图 600；长图（高/宽≥1.7）400；普通竖图按高 375
// 反推且宽不低于 300；自然宽≤300 的近正方小图按二维码 150；缺高度默认 600。

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

// 2026-08-14 排版事故金标准（news/62765 正确版）：
//   - 1280×6046（data-ratio 4.72 表格长图）旧规则输出 79px 不可读，金标准 400
//   - 360×364 产品图（无糖抹茶粉）曾被 qrMaxNatural=500 误判成二维码 150，金标准按纵图比例
//   - 真二维码（≤266）与普通纵图（320/375 档）行为不变
test('长图（高/宽 ≥ 1.7）→ 固定 400，不再按高 375 反推（62765 表格图曾缩成 79，62760 的 9:16 ins 截图曾缩成 211）', () => {
  assert.equal(adaptiveImageWidth(1280, 6046), 400); // 4.72 表格长图
  assert.equal(adaptiveImageWidth(1000, 2000), 400); // 2.0 长截图
  assert.equal(adaptiveImageWidth(1080, 1920), 400); // 1.78 手机/社交截图
  assert.equal(adaptiveImageWidth(700, 1150), Math.max(Math.round(375 * 700 / 1150), 300)); // 1.64 仍走纵图
});

test('纵图宽度下限 300（62760 的 1.6 比例小红书截图曾缩成 232）', () => {
  assert.equal(adaptiveImageWidth(900, 1440), 300); // 1.6 → 反推 234，抬到下限
  assert.equal(adaptiveImageWidth(840, 856), 368); // 下限之上不受影响（62303 基石）
  assert.equal(adaptiveImageWidth(900, 1440, { portraitMin: 250 }), 250 > Math.round(375 * 900 / 1440) ? 250 : Math.round(375 * 900 / 1440));
});

test('近正方形中图（>300）不再误判二维码 → 纵图比例（62765 产品图 360×364 曾输出 150）', () => {
  assert.equal(adaptiveImageWidth(360, 364), Math.round(375 * 360 / 364));
});

test('长图规则值可由 opts 注入', () => {
  assert.equal(adaptiveImageWidth(1280, 6046, { long: 500 }), 500);
  assert.equal(adaptiveImageWidth(1000, 1900, { longMinRatio: 1.8 }), 400);
});

test('规则值可由 opts 注入（红线下放）', () => {
  assert.equal(adaptiveImageWidth(800, 800, { portrait: 300 }), 300);
  assert.equal(adaptiveImageWidth(161, 163, { qr: 120 }), 120);
  assert.equal(adaptiveImageWidth(1080, 540, { landscape: 500 }), 500);
});
