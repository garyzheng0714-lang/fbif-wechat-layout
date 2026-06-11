// 图片宽度规则（网站渲染器用）— 纯函数、零依赖、可独立单测。
// 规则（2026-06-11 按飞书反馈问题十一/十五 + 成品 news/62303 实测修订，推翻旧「竖图固定宽400」契约）：
//   - 二维码（近正方形且原图宽 ≤ qrMaxNatural）→ 输出宽 150（62303 两枚二维码 161×163/265×266 均 150）
//   - 纵图（高 ≥ 宽，含 1:1）→ 长边（高）固定 375，宽按比例 = round(375 × 宽/高)
//     （62303 实测 840×856 → 368×375；反馈原文「比例为1:1或更小比例的纵图时，长设置为375」）
//   - 横图（宽 > 高）→ 600；缺高度信息时默认 600
// opts（可选，红线下放：由调用方从 config.image 解析后传入，缺省降级到 375/600/150/500）：
//   { portrait, landscape, qr, qrMaxNatural }。纯函数零依赖不变，规则值由外部注入，便于单测。
export function adaptiveImageWidth(naturalWidth, naturalHeight, opts) {
  const o = opts || {};
  const portrait = Number(o.portrait) > 0 ? Number(o.portrait) : 375;
  const landscape = Number(o.landscape) > 0 ? Number(o.landscape) : 600;
  const qr = Number(o.qr) > 0 ? Number(o.qr) : 150;
  const qrMaxNatural = Number(o.qrMaxNatural) > 0 ? Number(o.qrMaxNatural) : 500;
  const w = Number(naturalWidth || 0);
  const h = Number(naturalHeight || 0);
  if (!w || !h) return landscape;
  const ratio = h / w;
  // 二维码：近正方形小图（联系区微信码/群码实测 161×163、265×266）
  if (w <= qrMaxNatural && ratio >= 0.85 && ratio <= 1.18) return qr;
  if (h >= w) return Math.round(portrait * w / h);
  return landscape;
}
