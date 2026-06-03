/* 方案A「检视台」外壳交互（净新增，不动 engine/模块接线）：
   - 移动端左栏抽屉开合（#railToggle / #railToggle2 / #scrimRail）
   - 预览 / 源码 段切换（源码视图显示复制层 HTML = window.getClipboardHtml()）
   - 段选 pill 滑动定位（GSAP，reduced-motion 降级）
   DOM 显隐先同步设好，GSAP 只作装饰。 */
import { gFrom, reduced } from '/js/anim.js';

/* ---- 移动端左栏抽屉 ---- */
function toggleRail(force) {
  const rail = document.getElementById('rail');
  const scrim = document.getElementById('scrimRail');
  if (!rail) return;
  const open = force != null ? force : !rail.classList.contains('open');
  rail.classList.toggle('open', open);
  if (scrim) scrim.classList.toggle('show', open);
}
['railToggle', 'railToggle2'].forEach((id) => {
  const b = document.getElementById(id);
  if (b) b.addEventListener('click', () => toggleRail());
});
{ const s = document.getElementById('scrimRail'); if (s) s.addEventListener('click', () => toggleRail(false)); }

/* ---- 预览 / 源码 段切换 ---- */
let view = 'preview';
function moveSegPill() {
  const active = document.querySelector('.seg-btn.active');
  const pill = document.getElementById('segPill');
  if (!active || !pill) return;
  const x = active.offsetLeft - 3;
  if (window.gsap && !reduced()) {
    gsap.to(pill, { x, width: active.offsetWidth, duration: 0.28, ease: 'power3.out' });
  } else {
    pill.style.transform = `translateX(${x}px)`;
    pill.style.width = active.offsetWidth + 'px';
  }
}
function setView(v) {
  if (v === view) return;
  view = v;
  document.querySelectorAll('.seg-btn').forEach((b) => b.classList.toggle('active', b.dataset.view === v));
  moveSegPill();
  const pc = document.getElementById('previewContent');
  const wf = document.getElementById('wxFooter');
  const sv = document.getElementById('sourceView');
  if (!sv) return;
  if (v === 'source') {
    const code = document.getElementById('sourceCode');
    let html = '';
    try { html = (window.getClipboardHtml && window.getClipboardHtml()) || ''; } catch (e) {}
    if (code) code.textContent = html || '（复制层尚未生成——先上传或粘贴一篇文章再看源码）';
    if (pc) { pc.dataset.prevDisplay = pc.style.display || ''; pc.style.display = 'none'; }
    if (wf) wf.style.opacity = '0.4';
    sv.style.display = 'block';
    if (window.gsap && !reduced()) gFrom(sv, { opacity: 0, y: 8, duration: 0.24, ease: 'power2.out' });
  } else {
    sv.style.display = 'none';
    if (wf) wf.style.opacity = '';
    if (pc) {
      pc.style.display = pc.dataset.prevDisplay || '';
      if (window.gsap && !reduced() && pc.style.display !== 'none') gFrom(pc, { opacity: 0, y: 8, duration: 0.24, ease: 'power2.out' });
    }
  }
}
document.querySelectorAll('.seg-btn').forEach((b) => b.addEventListener('click', () => setView(b.dataset.view)));
setTimeout(moveSegPill, 60);
window.addEventListener('resize', moveSegPill);

/* ---- 文章渲染揭示动效（容器级，绝不碰正文 wx 内部样式）----
   engine 把 #previewContent 从隐藏切到显示时，整块平滑渐入；reduced-motion / 无 GSAP 时跳过。 */
(function revealOnLoad() {
  const pc = document.getElementById('previewContent');
  if (!pc) return;
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
})();
