/* 方案A「检视台」外壳交互（净新增，不动 engine/模块接线）：
   文章渲染揭示动效 —— engine 把 #previewContent 从隐藏切到显示时，整块平滑渐入。
   容器级动画，绝不碰正文 wx 内部样式；reduced-motion / 无 GSAP 时跳过。
   （顶栏汉堡、预览·源码 seg、源码视图已按反馈移除，相应交互代码一并删除。） */
import { gFrom, reduced } from '/js/anim.js';

const pc = document.getElementById('previewContent');
if (pc) {
  let wasVisible = pc.style.display !== 'none';
  const obs = new MutationObserver(() => {
    const visible = pc.style.display !== 'none';
    if (visible && !wasVisible && window.gsap && !reduced()) {
      gFrom(pc, { y: 12, opacity: 0, duration: 0.34, ease: 'power3.out' });
      const wf = document.getElementById('wxFooter');
      if (wf && wf.style.display !== 'none') gFrom(wf, { y: 18, opacity: 0, duration: 0.32, delay: 0.08, ease: 'power3.out' });
    }
    wasVisible = visible;
  });
  obs.observe(pc, { attributes: true, attributeFilter: ['style'] });
}
