// 图片宽度规则（网站渲染器用）— 纯函数、零依赖、可独立单测。
// 规则（用户 2026-05-30 确认，依据基石 news/62285 的 12 张正文图实测）：
//   竖图（高 > 宽）→ 400px；横图（含正方偏横）→ 600px；缺高度信息时默认 600。
// 基石 11 张 ratio<1 的横图全 600，唯一 ratio≈1.01 的竖图为 400。
// opts（可选，红线下放：由调用方从 config.image 解析后传入，缺省降级到 400/600）：
//   { portrait, landscape }。纯函数零依赖不变，规则值由外部注入，便于单测。
export function adaptiveImageWidth(naturalWidth, naturalHeight, opts) {
  const o = opts || {};
  const portrait = Number(o.portrait) > 0 ? Number(o.portrait) : 400;
  const landscape = Number(o.landscape) > 0 ? Number(o.landscape) : 600;
  const w = Number(naturalWidth || 0);
  const h = Number(naturalHeight || 0);
  if (!w || !h) return landscape;
  return h > w ? portrait : landscape;
}
