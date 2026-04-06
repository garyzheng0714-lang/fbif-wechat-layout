# Design System — Vercel-inspired + WeChat Replica

## Visual Identity
- **Style**: Vercel — monochrome, shadow-as-border, compressed typography
- **Font**: Geist Sans (headings/UI) + Geist Mono (stats/code labels)
- **OpenType**: `"liga"` enabled globally, `"tnum"` for numeric labels

## Color Palette

| Role | Value | Usage |
|------|-------|-------|
| Primary Text | `#171717` | Headings, body text, dark buttons |
| Secondary Text | `#666666` | Descriptions, labels |
| Muted Text | `#808080` | Placeholders, disabled |
| Background | `#ffffff` | Page, cards, surfaces |
| Surface Tint | `#fafafa` | Hover states, subtle backgrounds |
| Border (shadow) | `rgba(0,0,0,0.08)` | Shadow-as-border on all elements |
| Focus Ring | `hsla(212, 100%, 48%, 1)` | Focus state on inputs/buttons |
| Error | `#ff5b4f` | Error text, failed states |
| Divider | `#ebebeb` | Horizontal rules, separators |

## Typography

| Role | Size | Weight | Letter Spacing |
|------|------|--------|---------------|
| Page Title | 40px | 600 | -2.4px |
| Section Heading | 32px | 600 | -1.28px |
| Card Title | 24px | 600 | -0.96px |
| Body | 16px | 400 | normal |
| UI Label | 14px | 500 | normal |
| Caption / Stats | 12-13px | 500 | normal |
| Mono Label | 12px | 500 | uppercase, Geist Mono |

## Shadow System (NO traditional CSS borders)

| Level | Shadow | Usage |
|-------|--------|-------|
| Ring | `rgba(0,0,0,0.08) 0px 0px 0px 1px` | Default border for cards, inputs, buttons |
| Ring Hover | `rgba(0,0,0,0.15) 0px 0px 0px 1px` | Hover state |
| Subtle Card | Ring + `rgba(0,0,0,0.04) 0px 2px 2px` | Cards with minimal lift |
| Full Card | Ring + Subtle + `rgba(0,0,0,0.06) 0px 8px 16px -4px` | Panels, dropdowns |
| Focus | `hsla(212, 100%, 48%, 1) 0px 0px 0px 2px` | Keyboard/mouse focus |
| Bottom Border | `rgba(0,0,0,0.08) 0px 1px 0px 0px` | Toolbar bottom, list items |

## Component Rules

- **Buttons**: 6px radius, padding 8px 16px, weight 500
  - Primary: `#171717` bg, white text
  - Secondary: white bg, shadow-border ring
  - Pill/Badge: 9999px radius (tags only, NOT action buttons)
- **Cards**: 8px radius, shadow-ring border, white bg
- **Inputs**: 6px radius, shadow-ring border, focus = blue ring
- **Toggle**: 36x20px, #ebebeb off → #171717 on
- **Border Radius Scale**: 6px (buttons/inputs), 8px (cards), 9999px (badges)

## Do / Don't
- DO use shadow-as-border instead of CSS `border`
- DO use negative letter-spacing on headings (-2.4px at 40px, -1.28px at 32px)
- DO use 3 weights only: 400 (body), 500 (UI), 600 (headings)
- DON'T use backdrop-filter blur
- DON'T use traditional CSS borders on cards
- DON'T use warm colors (orange, yellow, green) in UI chrome
- DON'T use weight 700 on body text

---

## WeChat Preview Area (unchanged)

The article preview area replicates WeChat's actual appearance:
- **Background**: Pure white `#FFFFFF` (全白，不要灰色)
- **Title**: 27px, weight 700, color #222, 左对齐, PingFang SC font
- **Meta**: "FBIF食品饮料创新" (#576b95) + date (#b2b2b2)
- **Footer**: fixed bottom, white bg, centered 0.5px divider (#E5E5E5), icon color #7F7F7F
- **Content width**: max-width 680px centered
- **No phone-frame border**

## Critical Rules (from user)
1. 预览页背景必须全白
2. 保留模板切换（FBIF + Mote）
3. 设置纯前端 localStorage
4. 所有按钮在顶部工具栏
5. 不要 TOC/目录
6. 底部栏复刻微信（白底，分隔线不贯穿）
7. 文章标题左对齐 27px 粗体
8. 复制后自动打开微信后台
9. 表单只填数字
10. 不要 phone-frame 边框

## Decisions Log
| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-04-05 | 设置改为 localStorage | 用户要求：不走 API，前端直接编辑 |
| 2026-04-06 | 恢复模板切换 | 用户允许：FBIF/Mote 双模板 pill 按钮切换 |
| 2026-04-07 | Vercel 设计系统 | 用户要求：UI 外壳用 Vercel 风格，文章排版不变 |
| 2026-04-07 | CSS 架构升级 | 借鉴 doocs/md：CSS 变量 + 类 → 复制时内联 |
