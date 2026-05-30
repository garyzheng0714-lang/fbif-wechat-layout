// 图片宽度规则（网站渲染器用）— 纯函数、零依赖、可独立单测。
// 规则（用户 2026-05-30 确认，依据基石 news/62285 的 12 张正文图实测）：
//   竖图（高 > 宽）→ 400px；横图（含正方偏横）→ 600px；缺高度信息时默认 600。
// 基石 11 张 ratio<1 的横图全 600，唯一 ratio≈1.01 的竖图为 400。
export function adaptiveImageWidth(naturalWidth, naturalHeight) {
  const w = Number(naturalWidth || 0);
  const h = Number(naturalHeight || 0);
  if (!w || !h) return 600;
  return h > w ? 400 : 600;
}
