---
name: "FBIF 内容排版与发布工具"
description: "以隐形检视台承载多渠道排版、预览、复制与受控发布"
colors:
  brand: "#1456F0"
  brand-hover: "#0F4DD1"
  brand-active: "#0B3FBA"
  brand-soft: "#EBF1FF"
  text-primary: "#1F2329"
  text-secondary: "#646A73"
  text-tertiary: "#6F757D"
  text-disabled: "#BBBFC4"
  surface: "#FFFFFF"
  surface-nested: "#F7F8FA"
  surface-sunken: "#F2F3F5"
  border-control: "#D9DCE0"
  border-card: "#E5E6EB"
  success: "#00A870"
  warning: "#FF8800"
  danger: "#F54A45"
  wechat-body: "#544545"
  wechat-author: "#396FBC"
  foodtalks-heading: "#01396A"
typography:
  title:
    fontFamily: "PingFang SC, -apple-system, BlinkMacSystemFont, Segoe UI, Microsoft YaHei, Noto Sans SC, sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: 1.35
    letterSpacing: "normal"
  body:
    fontFamily: "PingFang SC, -apple-system, BlinkMacSystemFont, Segoe UI, Microsoft YaHei, Noto Sans SC, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.6
    letterSpacing: "normal"
  label:
    fontFamily: "PingFang SC, -apple-system, BlinkMacSystemFont, Segoe UI, Microsoft YaHei, Noto Sans SC, sans-serif"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.45
    letterSpacing: "normal"
  mono:
    fontFamily: "JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.55
    letterSpacing: "normal"
rounded:
  xs: "4px"
  s: "6px"
  m: "8px"
  l: "10px"
  full: "9999px"
spacing:
  xxs: "4px"
  xs: "6px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
components:
  button-primary:
    backgroundColor: "{colors.brand}"
    textColor: "{colors.surface}"
    typography: "{typography.title}"
    rounded: "{rounded.s}"
    padding: "0 18px"
    height: "36px"
  button-primary-hover:
    backgroundColor: "{colors.brand-hover}"
    textColor: "{colors.surface}"
    typography: "{typography.title}"
    rounded: "{rounded.s}"
    padding: "0 18px"
    height: "36px"
  button-primary-active:
    backgroundColor: "{colors.brand-active}"
    textColor: "{colors.surface}"
    typography: "{typography.title}"
    rounded: "{rounded.s}"
    padding: "0 18px"
    height: "36px"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-primary}"
    typography: "{typography.title}"
    rounded: "{rounded.s}"
    padding: "0 14px"
    height: "36px"
  input-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-primary}"
    typography: "{typography.body}"
    rounded: "{rounded.s}"
    padding: "0 10px"
    height: "34px"
  account-trigger:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-primary}"
    typography: "{typography.title}"
    rounded: "{rounded.s}"
    padding: "0 10px"
    height: "36px"
  compact-card:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-primary}"
    rounded: "{rounded.m}"
    padding: "12px"
---

# Design System: FBIF 内容排版与发布工具

## Overview

**Creative North Star: "隐形检视台"**

这是给内容编辑在明亮办公环境中长时间使用的桌面工作台。冷白底、石墨文字与有限的飞书蓝让界面保持清醒；密度服务于连续处理稿件，不用大标题、品牌表演或页面级入场编排打断心流。

“检视台”意味着每个状态都可看见、每个动作都有短而真实的反馈；“隐形”意味着工具外壳永远让位于文章。顶栏只保留当前动作，左栏承载排版目标、导入、单篇配置与登录身份，中央纯白预览直接呈现最终渠道结果。

系统明确拒绝旧 Notion 暖色、花哨 AI 模板、重边框、过度圆角和装饰性动效。熟悉的控件、明确的状态与目标后台的真实输出，比新奇外观更重要。

**Key Characteristics:**

- 冷中性、单一行动蓝、纯白内容舞台
- 紧凑而不拥挤，主要外壳字号集中在 `12–15px`
- 色阶分区优先，阴影只表达真实 elevation
- 状态驱动的 `150–250ms` 动效，完整 reduced-motion 降级
- 编辑器外壳与微信 / FoodTalks 正文契约严格隔离

**The 双层契约 Rule.** `--fs-*` 只服务编辑器外壳；`.wx-*`、`#contentArea` 与各渠道渲染器是发布内容契约，任何一侧都不得借用另一侧的视觉规则。

## Colors

外壳采用 Restrained 策略：冷中性色承担绝大多数面积，飞书蓝只标记主动作、当前选择、焦点与进行中状态。

### Primary

- **行动蓝**（`brand`）：复制、保存、当前选择与键盘焦点；hover 和 active 只使用对应的 `brand-hover`、`brand-active`。
- **选择浅蓝**（`brand-soft`）：焦点环、选中行与轻量 badge 的低强调背景，不能扩张成装饰色块。

### Neutral

- **石墨主墨**（`text-primary`）：标题、控件值与关键状态。
- **说明灰**（`text-secondary`）：辅助说明和非主导标签。
- **可读弱灰**（`text-tertiary`）：占位、caption 与次级状态；该值是白底文字的最低可读档，不得退回更浅的 `#8F959E`。
- **冷白工作面**（`surface`）：页面、卡片与预览基底。
- **嵌套灰 / 凹槽灰**（`surface-nested`、`surface-sunken`）：静态分区、hover 与不可编辑区域；它们替代无意义的常驻边框。
- **控件线 / 卡片线**（`border-control`、`border-card`）：只在输入边界、分隔与可点击轮廓确有必要时出现。

### Semantic

- **成功绿**（`success`）：复制完成与同步完成。
- **警告橙**（`warning`）：需要注意但仍可继续的状态。
- **危险红**（`danger`）：失败、删除与阻断状态；颜色必须同时配合文字或图标。

### Channel content contracts

- 微信预览始终纯白；标题为 `27px / 700 / #222222`，正文使用 `wechat-body`、`15px`、`1.75` 行高，原创作者使用 `wechat-author` 且不斜体，图注为 `12px / #888888`。
- FoodTalks 内容由独立渲染器决定，标题使用 `foodtalks-heading`；网站署名固定为 `14px / #888888 / italic`，不得复用公众号 Mote 蓝色署名。
- 公众号贴图（载体选「贴图」时）由独立渲染器 `templates/sticker.js` 决定：复制与预览都是一个无样式 `<p>`，行间 `<br>`，空行是连着两个 `<br>`；预览按贴图默认正文 `17px / 1.75 / rgba(0,0,0,0.9)` 显示，样式只挂在预览容器上、不进剪贴板。

**The One Voice Rule.** 行动蓝在单屏只表达动作与状态，不用于装饰；预览内容的品牌色只能由对应渠道渲染器输出。

## Typography

**Display Font:** 无；工具界面不使用展示字体。

**Body Font:** PingFang SC，回退到系统 UI sans-serif。

**Label/Mono Font:** 普通标签沿用系统字体；规则 JSON、代码和值预览使用 JetBrains Mono 回退栈。

**Character:** 单一、熟悉、清晰。产品界面用固定字号而非 `clamp()`；层级主要靠字重、位置和留白，不靠夸张尺寸。

### Hierarchy

- **Headline** (`600`, `15px`, `1.35`): 弹窗标题和一级面板标题。
- **Title** (`600`, `14px`, `1.35`): 卡片标题、主要按钮和账号名。
- **Body** (`400`, `13px`, `1.6`): 说明、输入内容和常规操作文案；长说明限制在 `65–75ch`。
- **Label** (`500`, `12px`, `1.45`): 字段名、状态和 caption；不用全大写或宽字距制造 eyebrow。
- **Mono** (`400`, `12px`, `1.55`): 结构化规则、JSON 与技术值，只在管理员场景出现。

### Preview hierarchy

公众号和网站正文排版不继承外壳层级。微信公众号正文保持 `15px / 1.75`，文章标题保持左对齐 `27px / 700`；FoodTalks 标题、正文、引用与表格样式由其渠道渲染器和 CMS 存活契约单独维护。

**The Fixed Scale Rule.** 外壳字号固定在稳定的小步进上；响应式只改变结构与可用空间，不缩放标题制造假层级。

## Elevation

系统采用“色阶优先、阴影响应”的混合策略。静止分区先用 `surface`、`surface-nested`、`surface-sunken` 建立深度；阴影只在悬浮卡片、展开菜单、弹窗与明确 hover 升起时出现。

### Shadow Vocabulary

- **静止轻托** (`box-shadow: 0 1px 3px rgba(31,35,39,0.04), 0 1px 2px rgba(31,35,39,0.03)`): 文件条、轻量卡片与开关旋钮。
- **交互升起** (`box-shadow: 0 2px 8px rgba(31,35,39,0.06), 0 1px 3px rgba(31,35,39,0.04)`): hover、可选媒体与强调浮起。
- **浮层** (`box-shadow: 0 12px 32px rgba(31,35,39,0.12), 0 4px 12px rgba(31,35,39,0.06)`): 账号菜单、模态框与 toast；使用该阴影时不再叠加装饰性外边框。

### Motion

常规状态变化使用 `150ms`，结构切换使用 `200ms`，较长反馈上限 `250ms`；默认缓动为 `cubic-bezier(0.22, 1, 0.36, 1)`。账号菜单轻微下移展开，拖拽区通过边界和背景响应，文章渲染只做容器级渐入。动画永远建立在已经可见、已经提交的状态上。

**The State-Only Motion Rule.** 动效只解释状态变化、空间关系或操作结果；不得延迟复制、发布决策或内容可见性。`prefers-reduced-motion` 下所有动效降为瞬时切换或极短淡入。

## Components

### Buttons

- **Shape:** 紧凑矩形，圆角使用 `rounded.s`；常规高度 `36px`，小按钮 `34px`。
- **Primary:** 行动蓝底、白字，只用于当前页面最重要且安全边界清楚的动作；复制按钮固定在顶部工具栏并立即反馈成功态。
- **Secondary:** 冷白底、必要时用控件线或 inset 边界；hover 用中性背景，不与主按钮争夺注意力。
- **States:** hover、focus-visible、active、disabled、loading、success 必须有明确差异；active 可使用 `scale(0.95–0.98)`，但 reduced-motion 下取消位移和缩放。

### Left rail, account menu and navigation

- 左栏两段式：`surface-nested` 灰底工作区 + `56px` 身份栏，不再显示“导入内容”标题容器。工作区里的每个模块都是白卡（`surface` + `border-card` + 静止轻托），灰底只做分区。（2026-08-03 用户裁定）
- 排版目标是导入卡的第一行——工具贴着它作用的对象；单层“头像 + 账号名 + 箭头”pill，菜单按“公众号 / 网站”分组。同模板账号原地切换，跨模板或跨渠道整页切换。禁止恢复“渠道胶囊 + 规则包分段”的两层切换器。
- 账号 pill 同一行靠右是载体分段开关「文章 | 贴图」（2026-10-27 用户裁定），只在公众号编辑器出现：它换的是排成什么载体，不是第二层账号；`26px` 高、`rounded.full`、凹槽灰轨道，选中项是白底加静止轻托的升起片，键盘方向键切换；收起左栏时与账号一起收走。
- 登录身份在左栏底部，身份行贴紧工作区分隔线和右侧栏边，不留卡片式内缩缝隙；菜单向上展开。顶栏高 `56px`，只保留当前页面动作（复制全文 / 规则）。（2026-08-03 用户裁定）
- 层级固定为：顶栏 `60`、账号菜单 `200`、toast `300`、模态框 `420`。账号菜单落在会滚动的工作区里，必须自带高度上限与内部滚动，不得被滚动容器裁切。
- 已知取舍：收起左栏会同时收走排版目标与登录身份；这两项由展开左栏恢复，不在顶栏另做只读副本。

### Import and fields

- 导入卡自上而下是：排版目标 → 正文 / 链接输入 → 已选文件行 → 动作行（上传文件 / 飞书云文档 + 状态文案）→ 导入进度行。文件拖放与输入是同一张卡里的两个清楚入口，可同时在场。
- 导入进度是这张卡内部的状态行，不是工作区里的第二张卡。（2026-08-05 用户裁定）它自身背景透明、白底由卡片提供，水平内边距与输入框和动作按钮对齐（`14px`），底部保留呼吸空间；形态是 `3px` 细胶囊条加一行“状态文案（左）/ 百分比（右）”，百分比用等宽数字。进度只能反映真实完成量：分母未知时隐藏百分比，且解析等快阶段不得先冲到 95% 以上把主要等待压进末尾。禁止让它自带 `surface` 背景——那会在灰底工作区上露出一块无圆角白块。
- 文件选中后是一条 `38px` 行：类型标签、完整可辨文件名、大小、移除按钮，不靠悬停浮层，也不顶掉输入区。
- 导入没有提交按钮：粘贴即排版（2026-08-06 用户裁定）。动作行末尾常驻状态文案“粘贴后自动排版”，说明这条约定；手打输入的键盘出口是 `Cmd/Ctrl+Enter`。不要再引入行动蓝的“导入排版”主按钮与它的互斥显隐规则。
- 输入框使用 `border-control` 与 `rounded.s`，focus 用行动蓝边界加浅蓝焦点环。占位符使用 `text-tertiary`，不得用不可读浅灰。
- 表单视觉值只展示数字；不要把 `px`、`em` 等单位塞进用户输入框，颜色使用 color picker。

### Cards and containers

- 卡片圆角只用 `4 / 6 / 8 / 10px`；默认无阴影或只用静止轻托。
- 分区优先用中性背景和节奏，不用嵌套卡片、彩色侧边条或“描边 + 宽软阴影”的 ghost-card。
- 底部区块面板跨账号共享结构与交互语言，排序使用明确拖拽把手；各渠道视觉输出仍由独立渲染器决定。

### Modals and feedback

- 模态框使用原生可理解的标题、内容、顶部关闭与底部动作；白色表面、`10px` 圆角、浮层阴影和半透明遮罩。
- Toast 位于底部居中，只传达已经完成的结果，不承载需要用户决策的信息。
- 错误状态必须给出可执行文案；不能只变红，也不能向普通编辑暴露内部堆栈或配置细节。

### Preview surfaces

- 公众号文章直接在纯白页面流动，内容宽度最大 `680px`；无 phone-frame 边框、无灰色画布、无目录。
- FoodTalks 预览最大宽度 `865px`，只模拟目标网站；其正文样式、表格 CMS 存活属性和署名规则不进入公众号渲染器。
- 所有文章级作者、来源与编辑信息由渲染契约控制位置和次数，外壳不得用 CSS 临时修饰或重复生成。

### Responsive behavior

- `1024px` 以下左栏收窄；`820px` 以下左栏整体（工作区 + 身份栏）堆叠到预览上方，身份栏按内容收窄；`520px` 以下压缩顶栏水平留白，但保留复制动作。
- 响应式改变布局，不改变内容契约，也不通过流体大字制造层级。

## Do's and Don'ts

### Do:

- **Do** 让纯白预览成为页面焦点，并用冷中性色阶组织周围工具。
- **Do** 复用 `public/css/feishu-tokens.css` 的 `--fs-*` token；新增外壳样式先映射到现有色彩、圆角、阴影与动效词汇。
- **Do** 把所有主操作放在顶部工具栏，并让复制点击立即写入剪贴板和显示结果。
- **Do** 为 hover、focus-visible、active、disabled、loading、error 和 success 补齐一致状态。
- **Do** 让异步交互遵守 latest-wins，并在每个 `await` 后、提交 UI 前验证意图版本。
- **Do** 为每段动效提供 `prefers-reduced-motion` 降级，且先保证内容默认可见。
- **Do** 保持公众号、FoodTalks 网站与 FoodTalks 系列渲染器物理隔离。
- **Do** 用真实微信公众号 / FoodTalks CMS 粘贴、保存结果验证正文视觉，而不是只检查本地截图。

### Don't:

- **Don't** 使用**旧 Notion 暖色体系**：`#DD5B00`、`#F0EBE5`、`#A39E98`、`#615D59`、`#0075DE`。这些历史值不是可复用先例。
- **Don't** 制造**花哨 AI 模板感**：禁止渐变文字、玻璃拟态、装饰性动效、重复卡片阵列、全页 eyebrow 或编号脚手架。
- **Don't** 使用**重边框 / 硬切块**：禁止大量常驻描边、浓黑阴影、彩色侧边条，以及描边与 `16px+` 宽软阴影同时出现。
- **Don't** 形成**喧宾夺主的工具壳**：外壳不得用大字、饱和色块或复杂动效抢过正文预览。
- **Don't** 引入**不透明的自动化**：禁止让复制等待后台准备、让旧异步结果覆盖新意图，或未经持久化人工批准自动公开。
- **Don't** 给预览页添加 `#F7F7F7`、`#EDEDED` 或任何灰色画布；预览背景必须纯白。
- **Don't** 恢复 phone-frame 边框、交互式编辑器 TOC、底部操作按钮或两层账号切换器。自动发布组装层为符合条件的原创稿写 CMS `toc` 元数据不在此列。
- **Don't** 把外壳 token 用进正文，或让公众号与网站渲染器互相 import。
- **Don't** 使用 `12px+` 的体系外卡片圆角；卡片上限是 `10px`，pill 只用于标签、开关和明确的圆形控制。
- **Don't** 自定义滚动条作为风格表达；现有 `.scroll-y` 规则属于待收敛实现，不应继续扩散。
- **Don't** 用 modal 代替可以内联完成的选择、编辑或状态反馈。
- **Don't** 通过颜色单独表达成功、警告、错误或选择状态。
