# Design System — 飞书 Universe Design（编辑器外壳）+ 微信/FoodTalks 正文复刻

> 编辑器**外壳**（顶栏、侧栏、卡片、按钮、弹窗）走飞书 Universe Design 冷中性灰体系，
> 全部引用 `public/css/feishu-tokens.css` 的 `--fs-*` token。
> **正文预览区**（`.wx-*` 微信复刻 / `#contentArea` FoodTalks 复刻）是内容契约红线，
> **不属于本设计系统**，见下方「WeChat Preview Area」「Content Contract」——像素级冻结。

## Visual Identity
- **Style**: 飞书 Universe Design — 冷中性灰阶、减线条用背景分区、克制精确
- **Font**: PingFang SC 打头（`--fs-font`）；正文区用各自的 mp-quote / FoodTalks 字体栈，不受影响
- **Tone**: 冷静、干净、有层次 — 极轻阴影托起 elevation，而非靠硬边框切块

## Color Palette（`--fs-*` token，定义在 `public/css/feishu-tokens.css`）

| Role | Token | Value |
|------|-------|-------|
| 品牌主蓝 | `--fs-brand` | `#1456F0` |
| 主蓝 hover / active | `--fs-brand-hover` / `--fs-brand-active` | `#0F4DD1` / `#0B3FBA` |
| 主蓝浅底（badge/icon 软底） | `--fs-brand-soft` | `#EBF1FF` |
| 主文字 | `--fs-text-1` | `#1F2329` |
| 次文字（描述/标签） | `--fs-text-2` | `#646A73` |
| 弱文字（占位/caption/状态） | `--fs-text-3` | `#8F959E` |
| 卡片 / 页面底 | `--fs-bg-card` | `#FFFFFF` |
| 嵌套分区底 | `--fs-bg-nested` | `#F7F8FA` |
| 凹槽 / sunken | `--fs-bg-sunken` | `#F2F3F5` |
| hover / press 叠加 | `--fs-bg-hover` / `--fs-bg-press` | `rgba(31,35,39,0.04)` / `.08` |
| 浮层遮罩 | `--fs-bg-overlay` | `rgba(0,0,0,0.45)` |
| 控件描边 | `--fs-border-control` | `#D9DCE0` |
| 卡片描边 | `--fs-border-card` | `#E5E6EB` |
| 分隔线 | `--fs-divider` | `rgba(31,35,39,0.08)` |
| 成功 / 警告 / 危险 | `--fs-success` / `--fs-warning` / `--fs-danger` | `#00A870` / `#FF8800` / `#F54A45` |

## Radius / Shadow / Motion（token）

- **圆角**: `--fs-r-xs/s/m/l` = `4 / 6 / 8 / 10px`，`--fs-r-full` = `9999px`。**只用这四级，不写体系外值（5/7/12px）**
- **阴影（极轻、有方向）**: `--fs-sh-s1`（卡片静止）/ `--fs-sh-s2`（hover 升起）/ `--fs-sh-s3`（弹窗浮层）。**不手写浓黑投影**
- **动效**: `--fs-ease-out` / `--fs-ease-spring`；时长 `--fs-t-fast/mid/slow` = `150/200/250ms`

## Component Rules

- **主按钮**: `--fs-brand` 底 + 白字，hover `--fs-brand-hover`，radius `--fs-r-s`，active `scale(0.95)`
- **次按钮 / 控件**: `--fs-bg-card` 底 + `--fs-border-card`/`--fs-border-control` 描边，hover `--fs-bg-hover`
- **卡片**: `--fs-bg-card` 底 + `--fs-sh-s1` 极轻阴影托起，hover 升 `--fs-sh-s2` + `translateY(-1px)`；**减线条——分区靠背景色差 + 阴影，不靠常驻边框**
- **输入框**: `--fs-border-control` 描边，focus = `--fs-brand` 边 + `0 0 0 2px var(--fs-brand-soft)` 聚焦环
- **弹窗浮层**: `--fs-bg-card` + `--fs-r-l` + `--fs-sh-s3`，遮罩 `--fs-bg-overlay`

## Do / Don't
- DO 用 `--fs-*` token，不硬编码颜色 / 圆角 / 阴影
- DO 减线条：能用 `--fs-bg-nested/sunken` 背景分区就不画灰边框
- DO 卡片配 `--fs-sh-s1~3` 极轻阴影拉 elevation
- DON'T 残留旧 Notion 体系：旧蓝 `#0075de`、暖橙 `#dd5b00`、暖灰 `#f0ebe5 / #a39e98 / #615d59`
- DON'T 手写浓黑投影 `rgba(0,0,0,0.28)`、体系外圆角 `5/7/12px`、`inset` 白高光 hack
- DON'T 把外壳 token 用到正文预览区（正文是独立契约，见下）

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
2. 多账号绑定，统一顶栏左侧账号菜单（单层）：账号切换走全局顶栏左上角头像菜单（`account-menu.js` 的 `mountAccountMenu`，挂载点 `#acctMenu`），列出全部账号，渠道（公众号 / 网站）降为菜单内的分组标签，不再是独立控件。当前编辑器保持单篇文章工作区，不再提供文章标签页。旧的"顶部圆角渠道胶囊 + 侧栏方角规则包分段"两层切换器已删除，勿恢复。
3. 规则服务端保存 + 本地降级（服务器 `/api/rules/*` 为准，包含参数配置与受限 `behavior` 行为配置；localStorage 只做兼容与离线降级）
4. 全局顶栏左侧显示账号下拉，不显示「FBIF文章排版工具」品牌文案；设置入口收进账号下拉菜单底部，右侧只保留复制全文。刷新按钮不显示。左侧底部不再显示当前规则卡片或设置入口
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
