# Design System — FBIF 微信排版工具

## Product Context
- **What this is:** 内部编辑工具，将 DOCX/Markdown 转换为微信公众号格式 HTML
- **Who it's for:** FBIF 编辑团队（食品饮料行业内容生产者），每天高频使用
- **Space/industry:** 食品饮料行业媒体（参考 foodtalks.cn 视觉语言）
- **Project type:** 内部工具（效率优先，零装饰噪音）

## Aesthetic Direction
- **Direction:** Industrial/Utilitarian — 功能优先，一切服务于效率
- **Decoration level:** minimal — 无装饰元素，排版和间距做所有视觉工作
- **Mood:** 专业、克制、高效。像 Linear 或 Figma 那样，打开就能干活
- **Reference sites:** foodtalks.cn（整体基调）

## Typography
- **Display/Hero:** System font stack — 内部工具无需品牌字体，零加载延迟
- **Body:** System font stack — 一致性和速度
- **UI/Labels:** Same as body
- **Data/Tables:** `"SF Mono", "Menlo", "Consolas", monospace` — 状态栏和日志
- **Code:** `"SF Mono", "Menlo", "Consolas", monospace`
- **Loading:** 系统字体，无需加载
- **Font stack:** `-apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", sans-serif`
- **Scale:**
  - H1: 22px / 700 (页面标题)
  - H2: 16px / 600 (区域标题)
  - Body: 14px / 400 (正文)
  - Caption: 12px / 400 (辅助文字)
  - Mono: 12px / 400 (状态栏、版本号)

## Color
- **Approach:** restrained — 单一主色 + 中性色，颜色只用在需要注意力的地方
- **Primary:** `#1D4ED8` — 靛蓝，比原 #0070C0 更沉稳现代，用于主按钮和交互元素
- **Primary hover:** `#1E40AF`
- **Primary light:** `#EFF6FF` — 悬停背景、选中态
- **Background:** `#F8F9FA` — 极浅灰，比原 #f0f2f5 更轻
- **Surface:** `#FFFFFF` — 卡片和内容区
- **Text primary:** `#111827` — 高对比主文字
- **Text secondary:** `#6B7280` — 辅助说明
- **Text muted:** `#9CA3AF` — 占位符、禁用态
- **Border:** `#E5E7EB`
- **Border light:** `#F3F4F6`
- **Toolbar:** `#1F2937` 背景 / `#F9FAFB` 文字 — 深色工具栏与内容区分界
- **Semantic:**
  - Success: `#059669` / bg `#ECFDF5` — 复制成功
  - Warning: `#D97706` / bg `#FFFBEB` — 处理中
  - Error: `#DC2626` / bg `#FEF2F2` — 失败
  - Info: `#0284C7` / bg `#F0F9FF` — 提示

## Spacing
- **Base unit:** 4px
- **Density:** compact — 编辑工具不需要大留白，操作区域要近
- **Scale:** 2xs(2px) xs(4px) sm(8px) md(16px) lg(24px) xl(32px) 2xl(48px)

## Layout
- **Approach:** grid-disciplined — 内部工具用严格对齐
- **Max content width:** 800px (上传页) / 840px (预览区)
- **Border radius:** sm:4px, md:6px, lg:8px — 比原 12-16px 更克制，工具感

## Motion
- **Approach:** minimal-functional — 仅功能性过渡，不拖沓
- **Easing:** ease-out (所有过渡)
- **Duration:** 150ms (统一) — 快速反馈
- **Hover:** 按钮 scale(0.98) active 态，无 hover 缩放动画

## Interaction Design

### 页面结构：两页合一
- **去掉 index.html 跳转**：模板选择、文件上传、URL 抓取合并到一个页面
- **模板切换：** 顶部 pill 按钮（FBIF / Mote），不是卡片跳转
- **记住上次选择：** localStorage 存储用户偏好

### 输入区：统一智能输入
- 文件拖拽和 URL 输入在同一区域，不再用"或"分隔
- 拖拽区 padding 从 60px 缩到 36px
- 支持 ⌘V 全局粘贴：检测剪贴板是 URL 时自动触发抓取
- 支持多文件批量拖拽

### 工具栏：按频率分层
- **深色背景** (`#1F2937`)，和内容区形成明确分界
- **左侧：** 返回按钮 + 文件名（只读显示，不是输入框）+ 模板 chip（可点击切换）
- **右侧：** 页脚按钮（图标）+ 缩放按钮（图标）+ 复制按钮（唯一蓝色主按钮）
- **重试按钮：** 仅在有失败图片时出现
- 标题输入从工具栏移出（标题来自文档，极少需要编辑）

### 复制反馈
- 按钮变绿（`#059669`）并保持，文字变为"✓ 已复制"
- 工具栏下方出现引导条："已复制到剪贴板 — 打开微信公众号后台，粘贴到编辑器即可"
- 不使用 toast 弹窗（减少一次关闭操作）

### 键盘快捷键
- `⌘ + Enter` — 复制到公众号（预览页最高频操作）
- `⌘V` — 自动检测粘贴内容，URL 自动抓取
- `Esc` — 从预览返回上传
- `Tab` — 切换模板（FBIF ↔ Mote）

### 用户偏好持久化（localStorage）
- 上次选择的模板
- 页脚开关状态
- 缩放比例

## 注意事项
- **文章内容渲染不在此设计系统范围内**，文章正文样式由各排版模板（fbif.js / mote.js）定义，需匹配微信公众号实际显示效果
- **不使用 AI slop 模式：** 不用紫色渐变、不用三列图标网格、不用居中一切、不用装饰性圆点
- **不使用过度圆角：** 所有圆角不超过 8px（pill 按钮除外）

## Decisions Log
| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-04-05 | 初始设计系统创建 | 基于 foodtalks.cn 视觉语言 + 内部工具效率优先原则 |
| 2026-04-05 | 两页合一交互重构 | 减少 67% 点击次数，模板选择从页面跳转改为 pill 切换 |
| 2026-04-05 | 深色工具栏 | 参考 foodtalks.cn 深色顶栏，工具区与内容区分界更清晰 |
| 2026-04-05 | 主色从 #0070C0 改为 #1D4ED8 | 更沉稳现代，对齐 foodtalks.cn 蓝色基调 |
| 2026-04-05 | 添加键盘快捷键 | 编辑团队高频使用，⌘Enter 复制是核心提效 |
