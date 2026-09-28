# WORKLOG：编辑器外壳整体迁到 Su Design（2026-09-28 起，进行中）

> 状态：进行中。分支 `feat/su-design-migration`。当前真值以代码与 CLAUDE.md 为准；本文记录决定、约束与阶段出口，完成后改为历史记录。

## 决定（用户 2026-09-28）

- 界面「都对齐 Su Design」：技术栈、组件、风格以 Su Design 的 DESIGN.md 为唯一标准，走「完全换血」，不半迁移。
- 迁移细节「你自己看着来」：由执行方按下面的约束自行拆分推进，每阶段单独过 CI、真实浏览器验收后合并。
- **整个布局都重做**（用户 2026-09-28）：不复刻旧的「左栏导入卡 + 中间预览 + 顶栏动作」，每一页从 Su Design 的模板重新排（编辑器按「画布 + 检查器」：空状态时主输入卡在中轴，导入后预览纸是唯一重心、设置收进右侧检查器）。旧红线 2 里的布局与视觉数值随之作废，只留「单层账号菜单、渠道只分组、跨模板整页跳转、粘贴即排版、无提交按钮」这些行为约束。
- **不局限于 Su Design 现有组件，例如标签要有多种颜色**（用户 2026-09-28）：缺的能力补进 Su Design，而不是在项目里自造或引入别的组件库。多色标签与 Su Design 现行「标签不用彩色底、颜色只在状态圆点上」「绝对禁止：用彩色底块表示分类」冲突；按其冲突裁定，用户当前明确要求优先，改动落在 Su Design：`Tag` 增加多色变体与一套分类色板设计变量（浅色、深色都要过对比度），同步改 DESIGN.md 对应条款。

## 不能动的东西

1. **正文渲染与复制链路字节不变**：`public/js/templates/*`、`parser/punctuation/docx-*`、`css-inline`、`wechat-normalize`、`copy-session`、`clipboard`、`uploader`、`more-articles*`（非 UI 部分）、`css/wx-theme.css`、`footer.html`。golden（`tests/golden/*`）、`render-service-site`、`site-footer-parity` 守护。
2. **核心模块继续以原始 ES 模块部署在 `PUBLIC_DIR/js/**`**：render-service 运行时直接 import 它们（`render-service/render.mjs`），Go 启动时逐字节校验 `js/rule-packs/registry.js` 与 `js/blocks/block-registry.gen.js`。Vite 只打包外壳，不打包、不替换这些文件。
3. **URL 即状态**：`/mp/<id>`、`/ft/<id>` 保留；跨渠道切换整页跳转（草稿快照依赖 beforeunload）。
4. **业务交互红线照搬**：粘贴即排版（32）、复制立即反馈且不跳转（8、9）、异步 latest-wins（25）、飞书异步导入与图片预解码能力（31）、表单尺寸只填数字（10）。视觉数值类红线按 Su Design 改写。

## 架构

- 新增 `web/`：Vite + React 19 + TypeScript strict + Tailwind v4 + `@su`（shadcn CLI 安装），构建到 `public/ui/`（不用 `/assets/`：该路径已被正文素材和 `uploader.js` 占用）。
- 核心在运行时加载：外壳用 `import(/* @vite-ignore */ '/js/engine.js?v=' + build)`，沿用 `version.json` 与 `?v=` 缓存链，渲染器不改一行。
- **引擎视图接缝**（关键一步）：`engine.js` 的 `initApp` 现在直接抓约 30 个页面 id 写 DOM。先把这些写入收成一个视图适配器接口（进度、错误、预览提交、复制状态、导入摘要……），旧页面给一个 DOM 适配器保持现状，React 外壳给自己的适配器。`#contentArea` 始终是 React 不管理的空容器（引擎按 ref 写入）。`initApp` 只调一次（防 StrictMode 双绑）。
- **服务端页面鉴权**：现在 `/mp`、`/ft` 与 `app.html/site.html` 未登录也返回 200，只靠前端跳转，违反全局规则。迁移时在 Go 侧对编辑器页面做会话校验，未登录 302 到登录页；Caddy 不再直出编辑器 HTML。
- 缓存：`/ui/*` hash 产物 immutable；入口 HTML no-cache（补上 `site.html`）。
- 部署与 CI：`deploy.yml`、`browser-contracts` 增加 `npm ci && vite build`；部署后新增 `/mp/fbif` 入口 bundle 可加载的验收；`tests/deploy-transaction.test.mjs` 同步。

## 阶段与出口

| 阶段 | 内容 | 出口 |
| --- | --- | --- |
| 0 | Su Design 补 5 个缺失组件：取色器、图片裁剪、文件拖放区、拖拽排序列表、区块内容编辑器；`Tag` 增加多色变体与分类色板，改 DESIGN.md 对应条款（在 Su Design 仓库，另一会话提交后再动） | 组件上线，`npx shadcn add @su/<名字>` 可装 |
| 1 | `web/` 地基 + 构建/部署/CI/缓存 + 服务端页面鉴权 + 登录页（`@su/sign-in-card`） | 线上登录走新页面；未登录访问编辑器 302 |
| 2 | 引擎视图接缝：抽 DOM 适配器；把 `app.html/site.html` 内联的 `switchAccount/loadTemplate/refreshRuleConsumers/applyConfig` 抽成模块；改写按源码抠函数的测试 | 旧页面行为与字节不变，全测通过 |
| 3 | 公众号编辑器（`/mp/*`）React 外壳 | e2e 全绿；改前改后、浅深色截图 |
| 4 | 网站编辑器（`/ft/*`），顺带去掉原生 DnD（红线 22） | 同上 |
| 5 | 规则后台（替换 wangEditor、gsap、Sortable 等 vendor 全局） | 同上；删除旧外壳 CSS/JS 与 vendor |

## 迁移接触点（2026-09-28 只读调研）

- Go 按文件名直出页面：`api/main.go:1821-1842`；缓存规则 `api/main.go:267-313`，Caddy `deploy/caddy/fbifmp-layout.caddy`。
- 拼接式动态 import + 顶层 await 共 41 处（`engine.js`、三个模板、两页内联脚本），Vite 无法静态分析——核心保持运行时加载。
- 引擎与外壳的 window 契约：`_engineApi/_handleFile/_handleText/_handleBlocks/copyContent/copyHTMLSource/_updateFooter/_updateMoreArticles/_captureSessionSnapshot/...`，外壳写 `_activeRulePack/_activeConfig/_bottomBlocksOverride/...`；事件 `fbif:*`。
- 测试耦合：15 个 node 测试读 `app.html/site.html` 源码；`account-switcher`、`foodtalks-site-config-flow` 抠内联函数执行；`copy-behavior`、`copy-session` 抠 `engine.js` 内部函数执行（阶段 2 之前不要重命名或拆 `engine.js`）；7 个 e2e 依赖现有 id。
- 已是死代码：`active-rule-selector.js` 的 `#activeRuleCard/#activeRuleSelect` 渲染（两页都没有这些节点）。
- 模块双实例：同一模块以 `?v=` 与裸 URL 各加载一次（`rule-presets`、`registry`、`uploader`……），打包外壳时注意去重不改变行为。
