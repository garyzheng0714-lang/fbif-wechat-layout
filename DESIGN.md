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
- **Original Author Line**: `作者：` attribution uses `#396FBC`, normal style, not italic
- **Footer**: sticky bottom, white bg, centered 0.5px divider (#E5E5E5)
- **Content width**: max-width 680px centered

## WeChat Article Content Contract

These values are product contracts, not incidental CSS. Any parser, renderer, copy, or deploy change must keep them covered by tests and browser verification.

| Role | Contract |
|------|----------|
| Body text | `15px`, color `#544545`, line-height `1.75`, paragraph side margin `8px` |
| DOCX paragraph rhythm | Consecutive non-empty body paragraphs with compact Word spacing stay tight; a blank paragraph in the source restores the standard `wx-gap` before the next paragraph |
| Image block | Centered, `line-height: 0`, `font-size: 0`, image `display: block` |
| Image caption | `12px`, color `#888888`, line-height `1.75`, centered |
| Image source | Treat `图片来源：` / `图源：` after an image as image caption metadata, not body text and not article-level source |
| Article attribution | URL-imported `来源：` / `作者：` / `编辑：` renders once at the article tail before the standard `提示：` footer |
| Attribution style | `15px`, color `#0070C0`, line-height `1.75`; original DOCX author additionally uses `#396FBC`, normal style |

## Current Rules
1. 预览页背景必须全白
2. 仅保留 FBIF 公众号排版（已移除 Mote 模板）
3. 规则服务端保存 + 本地降级（服务器 `/api/rules/*` 为准，localStorage 只做兼容与离线降级）
4. 所有按钮在顶部工具栏
5. 不要 TOC/目录
6. 底部栏复刻微信（白底，分隔线不贯穿）
7. 文章标题左对齐 27px 粗体
8. 复制后不自动打开微信后台；只显示复制成功提示
9. 复制按钮必须立即反馈；图片上传和“更多文章”合成只能提前或后台进行，不能把按钮文案切成上传/准备状态
10. 表单只填数字
11. 不要 phone-frame 边框
12. 原创 DOCX 的首个 `作者：` 行是正文起点，它之前的编辑前置材料不显示在编辑器里
13. `Mote莫特` / `Mote 莫特` 作者在参考来源上方插入固定署名图；原创作者行 `#396FBC` 且不斜体
14. 更多文章是可选规则模块；`modules.more_articles.enabled=false` 时新文章不渲染底部更多文章，`modules.more_articles.default_cards` 保存新文章默认推荐列表，当前文章仍可在侧边栏临时替换、删除、排序和裁剪卡片
15. 更多文章选图使用当前卡片媒体库；`/api/fetch-article-meta` 必须返回 `image_urls`，替换图片候选顺序为当前卡片自定义封面、抓取头图、当前卡片正文媒体图，主文章图片只做兜底；旧卡片缺少 `image_urls` 时点击替换要按卡片链接补抓
16. URL 导入的图片说明组必须保留浅灰图注样式；`图片来源：` 不能掉成正文
17. URL 导入的文章级来源、作者、编辑固定在标准 `提示：` 前，且只出现一次
18. DOCX 连续非空正文段落保留紧凑节奏；原文空段分隔才恢复标准段后间距

规则说明：这里记录当前设计约束。旧需求不能因为出现在历史记录里就被当成当前规则。
