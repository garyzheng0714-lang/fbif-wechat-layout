// 规则工作台右侧「实时预览」（方案A「检视台」）。
// 改任意参数立刻反映在样张上，GSAP tween 高亮（reduced-motion / 无 GSAP 时降级为直接生效）。
// 读真实归一化 config（rule-document.js 字段名）；attribution_color 无真实 sink，
// 来源行颜色绑 link_color（与渲染器一致，见迁移计划字段映射）。
import { reduced } from './anim.js';

export function livePreviewHTML() {
  return `
    <aside class="admin-live" aria-label="排版样例">
      <div class="admin-live-head"><strong>排版样例</strong><span>修改后即时更新</span></div>
      <div class="admin-live-paper">
        <div class="admin-live-title" data-lv="title">食品创新，正在从概念走向日常</div>
        <p class="admin-live-p" data-lv="p1">新技术只有进入真实消费场景，才能形成稳定价值。排版规则需要让读者更轻松地理解内容。</p>
        <p class="admin-live-p" data-lv="p2">这段样例包含<strong data-lv="strong">加粗重点</strong>与<a data-lv="link">内容链接</a>，用于核对强调层级和颜色。</p>
        <div class="admin-live-figure"><span>产品应用场景</span></div>
        <p class="admin-live-cap" data-lv="cap">图：产品应用场景示例</p>
        <p class="admin-live-attr" data-lv="attr">来源：FoodTalks　作者：编辑部</p>
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
