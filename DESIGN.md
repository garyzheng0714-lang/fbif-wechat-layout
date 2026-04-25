# Design System — Notion-inspired + WeChat Replica

## Visual Identity
- **Style**: Notion — warm neutrals, whisper borders, approachable minimalism
- **Font**: Inter (400/500/600/700) + SF Mono (monospace labels)
- **Tone**: Warm, tactile, analog-feeling — not cold or sterile

## Color Palette

| Role | Value | Usage |
|------|-------|-------|
| Primary Text | `rgba(0,0,0,0.95)` | Headings, body — not pure black |
| Secondary Text | `#615d59` | Descriptions, labels |
| Muted Text | `#a39e98` | Placeholders, disabled, captions |
| Background | `#ffffff` | Page, cards, surfaces |
| Warm White | `#f6f5f4` | Alt background, section alternation |
| Border | `rgba(0,0,0,0.1)` | Whisper border — 1px solid throughout |
| Accent Blue | `#0075de` | CTA buttons, links, focus, toggle on |
| Active Blue | `#005bab` | Button hover/pressed |
| Success | `#1aae39` | Confirmation, done states |
| Warning | `#dd5b00` | Errors, attention |
| Badge Bg | `#f2f9ff` | Pill badge tinted blue surface |
| Badge Text | `#097fe8` | Pill badge text |

## Typography

| Role | Size | Weight | Letter Spacing |
|------|------|--------|---------------|
| Body | 16px | 400 | normal |
| UI Label | 14-15px | 500-600 | normal |
| Caption | 12px | 600 | 0.125px |
| Sidebar Label | 12px | 600 | 0.125px (warm gray) |

## Border & Shadow

- **Whisper Border**: `1px solid rgba(0,0,0,0.1)` — used everywhere
- **Card Shadow**: `rgba(0,0,0,0.04) 0px 4px 18px, rgba(0,0,0,0.02) 0px 0.8px 2.925px, rgba(0,0,0,0.01) 0px 0.175px 1.04px`
- **Focus Ring**: `0 0 0 2px rgba(0,117,222,0.15)` + blue border
- **Border Radius**: 4px (buttons/inputs), 8px (cards), 9999px (badges)

## Component Rules

- **Buttons**: 4px radius, 8px 16px padding
  - Primary: `#0075de` bg, white text, hover `#005bab`
  - Secondary: `rgba(0,0,0,0.05)` bg, near-black text
  - Active: `scale(0.95)` transform
- **Cards**: 8-12px radius, whisper border, multi-layer shadow
- **Inputs**: 4px radius, `1px solid #dddddd`, focus = blue ring
- **Toggle**: 36x20px, `rgba(0,0,0,0.1)` off → `#0075de` on

## Do / Don't
- DO use warm neutrals with yellow-brown undertones
- DO use `rgba(0,0,0,0.95)` for text, not pure `#000`
- DO use whisper borders `1px solid rgba(0,0,0,0.1)`
- DO use `#f6f5f4` for alternate section backgrounds
- DON'T use cold blue-grays
- DON'T use heavy borders or shadows (keep opacity < 0.05)
- DON'T use bright colors except `#0075de` for CTAs

---

## WeChat Preview Area (unchanged)
- **Background**: Pure white `#FFFFFF`
- **Title**: 27px, weight 700, color #222, PingFang SC
- **Meta**: "FBIF食品饮料创新" (#576b95) + date (#b2b2b2)
- **Footer**: sticky bottom, white bg, centered 0.5px divider (#E5E5E5)
- **Content width**: max-width 680px centered

## Current Rules
1. 预览页背景必须全白
2. 仅保留 FBIF 公众号排版（已移除 Mote 模板）
3. 规则服务端保存 + 本地降级（服务器 `/api/rules/*` 为准，localStorage 只做兼容与离线降级）
4. 所有按钮在顶部工具栏
5. 不要 TOC/目录
6. 底部栏复刻微信（白底，分隔线不贯穿）
7. 文章标题左对齐 27px 粗体
8. 复制后不自动打开微信后台；只显示复制成功提示
9. 表单只填数字
10. 不要 phone-frame 边框

规则说明：这里记录当前设计约束。旧需求不能因为出现在历史记录里就被当成当前规则。
