/* GSAP 动效包装：rAF 失败兜底 + reduced-motion 辅助。
   GSAP 依赖 requestAnimationFrame；后台标签/被限流时 rAF 可能停摆，
   gsap.from() 目标会卡在不可见起始态。这些包装挂一个 setTimeout 兜底，
   到点把 tween 强制跳到终态，保证内容最终可见；正常标签里 tween 先完成，
   兜底空跑。若 GSAP 未加载（window.gsap 缺失）则不动画——调用方必须先
   同步设好 DOM 显隐，动画只作装饰（红线：复制/渲染等状态变更不依赖动画）。
   方案A「检视台」从 public/redesign/anim.js 提升为生产 ES 模块。 */

export const reduced = () =>
  !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches);

function failsafe(tween, vars) {
  if (!tween) return tween;
  const ms = (((vars && vars.delay) || 0) + ((vars && vars.duration) || 0.4)) * 1000 + 140;
  setTimeout(() => {
    try { if (tween.progress() < 1) tween.progress(1, false); } catch (e) {}
  }, ms);
  return tween;
}

export function gFrom(t, vars) { return failsafe(window.gsap ? gsap.from(t, vars) : null, vars); }
export function gTo(t, vars) { return failsafe(window.gsap ? gsap.to(t, vars) : null, vars); }
export function gFromTo(t, a, b) { return failsafe(window.gsap ? gsap.fromTo(t, a, b) : null, b); }

// 同时挂 window，供内联脚本 / 经典脚本 / 非模块消费方使用
window.gFrom = gFrom;
window.gTo = gTo;
window.gFromTo = gFromTo;
