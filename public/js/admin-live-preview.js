// 规则工作台右侧「实时预览」（方案A「检视台」）。
// 改任意参数立刻反映在样张上，GSAP tween 高亮（reduced-motion / 无 GSAP 时降级为直接生效）。
// 读真实归一化 config（rule-document.js 字段名）；attribution_color 无真实 sink，
// 来源行颜色绑 link_color（与渲染器一致，见迁移计划字段映射）。
import { reduced } from './anim.js';

export function livePreviewHTML() {
  return `
    <aside class="admin-live" aria-label="实时预览">
      <div class="admin-live-head"><span class="admin-live-dot"></span>实时预览</div>
      <div class="admin-live-paper">
        <div class="admin-live-title" data-lv="title">占位标题：实时预览排版效果</div>
        <p class="admin-live-p" data-lv="p1">这里是占位正文，用于演示当前规则的字号、行高与段距。调整左侧参数会立刻反映在这段文字上。</p>
        <p class="admin-live-p" data-lv="p2">占位段落含<strong data-lv="strong">加粗重点</strong>与<a data-lv="link">蓝色链接</a>，便于核对强调与链接颜色。</p>
        <div class="admin-live-figure"><span>配图</span></div>
        <p class="admin-live-cap" data-lv="cap">配图说明占位文本</p>
        <p class="admin-live-attr" data-lv="attr">来源：占位来源　作者：占位作者</p>
      </div>
    </aside>`;
}

const px = (v, d) => ((v == null || v === '') ? d : v) + 'px';

export function applyLivePreview(overlay, rawConfig, opts = {}) {
  const root = overlay && overlay.querySelector('.admin-live-paper');
  if (!root) return;
  const c = rawConfig || {};
  const q = (s) => root.querySelector(s);
  const title = q('[data-lv="title"]'), p1 = q('[data-lv="p1"]'), p2 = q('[data-lv="p2"]');
  const cap = q('[data-lv="cap"]'), attr = q('[data-lv="attr"]');
  if (title) title.style.fontSize = px(c.heading_size, '18');
  [p1, p2].forEach((p) => {
    if (!p) return;
    p.style.fontSize = px(c.font_size, '15');
    p.style.lineHeight = c.line_height || '1.75';
    p.style.letterSpacing = (c.letter_spacing || '0') + 'em';
    p.style.color = c.text_color || '#544545';
    p.style.marginLeft = px(c.paragraph_margin_x, '8');
    p.style.marginRight = px(c.paragraph_margin_x, '8');
    p.style.marginBottom = px(c.paragraph_gap, '20');
  });
  const link = q('[data-lv="link"]'); if (link) link.style.color = c.link_color || '#0070C0';
  const strong = q('[data-lv="strong"]'); if (strong) strong.style.color = c.text_color || '#544545';
  if (cap) { cap.style.fontSize = px(c.caption_font_size, '12'); cap.style.color = c.muted_color || '#888888'; }
  if (attr) { attr.style.fontSize = px(c.attribution_font_size, '15'); attr.style.color = c.link_color || '#0070C0'; }

  if (opts.tween && window.gsap && !reduced()) {
    const tgt = (opts.target && q('[data-lv="' + opts.target + '"]')) || root;
    try {
      gsap.fromTo(tgt, { backgroundColor: 'rgba(20,86,240,0.12)' }, { backgroundColor: 'rgba(20,86,240,0)', duration: 0.6, ease: 'power2.out' });
    } catch (e) {}
  }
}

// 字段 key → 实时预览高亮目标（与样张元素 data-lv 对应；映射归这个模块，避免散落在调用方）
export function targetForKey(key) {
  if (/heading/.test(key)) return 'title';
  if (/caption|muted/.test(key)) return 'cap';
  if (/attribution/.test(key)) return 'attr';
  if (/link/.test(key)) return 'link';
  return 'p1';
}
