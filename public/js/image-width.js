// 图片宽度规则（网站渲染器用）— 纯函数、零依赖、可独立单测。
// 规则（2026-06-11 按飞书反馈问题十一/十五 + 成品 news/62303 实测修订，推翻旧「竖图固定宽400」契约；
//       2026-08-14 按 news/62765 排版事故金标准补长图与二维码阈值修订）：
//   - 二维码（近正方形且原图宽 ≤ qrMaxNatural）→ 输出宽 150（62303 两枚二维码 161×163/265×266 均 150）
//     qrMaxNatural 默认 300：真二维码实测最大 266；62765 的 360×364 产品图曾被 500 阈值误判成 150
//   - 长图（高/宽 ≥ longMinRatio=1.7，表格/长截图/手机与社交截图）→ 固定宽 long=400
//     （62765 金标准：4.72 比例表格图 400；旧规则按「高固定 375」反推宽度缩成 79px 不可读；
//     62760 批量实测 9:16≈1.78 的 ins 截图被缩到 211px，故阈值取 1.7 盖住截图段，
//     人像/海报类竖图比例一般 ≤1.6 不受影响）
//   - 纵图（高 ≥ 宽，含 1:1）→ 长边（高）固定 375，宽按比例 = round(375 × 宽/高)，
//     且不低于 portraitMin=300（62760 实测 1.6 比例小红书截图被缩到 232px；
//     62303 实测 840×856 → 368、62765 金标准 320/375 档均在下限之上，不受影响）
//   - 横图（宽 > 高）→ 600；缺高度信息时默认 600
// opts（可选，红线下放：由调用方从 config.image 解析后传入，缺省降级到 375/300/600/150/300/400/1.7）：
//   { portrait, portraitMin, landscape, qr, qrMaxNatural, long, longMinRatio }。纯函数零依赖不变，规则值由外部注入，便于单测。
export function adaptiveImageWidth(naturalWidth, naturalHeight, opts) {
  const o = opts || {};
  const portrait = Number(o.portrait) > 0 ? Number(o.portrait) : 375;
  const portraitMin = Number(o.portraitMin) > 0 ? Number(o.portraitMin) : 300;
  const landscape = Number(o.landscape) > 0 ? Number(o.landscape) : 600;
  const qr = Number(o.qr) > 0 ? Number(o.qr) : 150;
  const qrMaxNatural = Number(o.qrMaxNatural) > 0 ? Number(o.qrMaxNatural) : 300;
  const long = Number(o.long) > 0 ? Number(o.long) : 400;
  const longMinRatio = Number(o.longMinRatio) > 0 ? Number(o.longMinRatio) : 1.7;
  const w = Number(naturalWidth || 0);
  const h = Number(naturalHeight || 0);
  if (!w || !h) return landscape;
  const ratio = h / w;
  // 二维码：近正方形小图（联系区微信码/群码实测 161×163、265×266）
  if (w <= qrMaxNatural && ratio >= 0.85 && ratio <= 1.18) return qr;
  // 长图：高宽比过大时按「高 375」反推会缩到不可读，改固定可读宽度
  if (ratio >= longMinRatio) return long;
  if (h >= w) return Math.max(Math.round(portrait * w / h), portraitMin);
  return landscape;
}
