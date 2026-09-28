> 2026-09-28 起界面设计以 Su Design 的 DESIGN.md 为准，本文里的视觉、组件库与设计稿规则作废。

# WORKLOG：作者规则选择器——「已套用」标签升级为显式可选（2026-07-11）

> **历史实现记录。** 下文“当前仅 Mote”指 2026-07-11 的 profile 目录；已知的 Markdown 作者双渲染没有当前复测证据，保持“未排期”，仅在再次复现或用户明确授权时进入功能修复。

## 目标（用户裁定）

用户提出三层模型：**平台 → 账号 → 板块（作者）**。前两层已成立（渠道/账号）；
第三层的数据已存在（`article_footer_defaults.profiles`，如「Mote 作者默认」按作者自动套用），
但**纯自动、无手动入口**——自动识别失败时整套规则静默消失（2026-07-11 上限事故的放大器）。

用户选定方案（AskUserQuestion 拍板）：**升级现有「已套用：Mote 作者默认」灰字标签为下拉选择器**
（拒绝顶部新增独立模块——符合 DESIGN.md「最少新概念」）。

交互契约：
- 选项：「自动识别（显示当前匹配结果）」/「通用默认」/ 各已配置 profile（当时仅 Mote）
- 默认跟随自动识别；手动选择覆盖自动结果；新文章导入重置回自动
- 手选 profile = 完整套用该作者规范：面板开关组（artifactVisibility）+ 正文行为（参考来源上方署名图）
- 手选「通用默认」= 作者相关特殊规则全关（即使自动识别到 Mote）
- 不伪造作者行：手选 Mote 只注入行为身份（author_normalized），不改正文显示的作者名
- 无 profiles 配置的账号（网站渠道等）：UI 零变化（不渲染选择器）

## 落层（ADR 四层模型）

- L1 中立层 `article-footer-defaults.js`：profile 选择逻辑参数化（`profileOverride`：
  `undefined`=自动 / `'none'`=通用默认 / `'<id>'`=强制该 profile）+ 列表/身份解析纯函数
- L2 渲染器 `fbif.js`：render 时按 selection 注入行为身份；renderBottomFooter 透传 override
- L4 交互 `block-panel.js`：badge → select，一套实现全渠道（网站 profiles 空则不渲染）
- 状态流 `engine.js`：`articleFooterProfileOverride` 与既有 `articleArtifactOverride` 同款
  流转（session/input/window/_engineApi setter/rerender option/新文章重置）
- 红线 #25：改选走 `_engineRerender` 版本门（与开关覆盖同一条已验证链路）

## 实现记录（全部完成）

- [x] L1 `article-footer-defaults.js`：`resolveArticleFooterSelection`（统一解析生效 profile +
      行为身份 authorNormalizedOverride）、`listArticleFooterProfiles`、
      `normalizeArticleFooterProfileOverride`；prepare/resolveBottomBlocksForArticle 透传
      `profileOverride`。override 指向已删 profile 回退自动（规则改名不炸旧会话）。
- [x] `fbif.js`：render 按 selection 注入 `context.article.author_normalized`（手选 profile
      注入别名身份 → 署名图等按作者触发的行为生效；手选 none 置空中和；不改 author 显示名，
      **不伪造作者行**）；artifactDefaults 改由 selection.profile 直取（等价旧匹配）；
      三个 process* 入口 + renderBottomFooter 透传（window._articleFooterProfileOverride 兜底，
      与 _bottomBlocksOverride 同款模式）。
- [x] `engine.js`：`articleFooterProfileOverride` 全程与 articleArtifactOverride 同款流转
      （input/session/window/rerender option/`setArticleFooterProfileOverride` setter 走版本门，
      红线 #25）。新文章导入天然重置（opts 不带 → undefined=自动）。
- [x] `block-panel.js`：`renderProfileSelect` 替换「已套用」badge（有 profiles 才渲染，
      网站渠道零变化）；选项=自动·<当前匹配> / 通用默认 / 各 profile；linkedom 兼容用
      option[selected] 设初值；`.bp-profile-select` 样式贴 feishu-tokens。
- [x] `app.html`：挂载 getArticleFooterProfileOverride / onArticleFooterProfileChange 回调对。

## 验证记录

- [x] 单测：L1 7 条新用例 + 面板 3 条（识别失败兜底手选、无 profiles 不渲染、选项与默认值）；
      既有「已套用」断言按新行为升级。全量 `npm test` 873 全绿。
- [x] 真实浏览器（localhost:19090/mp/fbif）：
      ① 可识别文档 → 选择器显示「自动 · Mote 作者默认」，署名图/开关在；
      ② 手选「通用默认」→ 署名图消失+开关组消失（识别到 Mote 也强制关，中和生效）；
      ③ 手选回「Mote 作者默认」→ 全套恢复；切回「自动」→ 正确回自动匹配；
      ④ 导入无作者行新文章 → override 重置、显示「自动 · 通用默认」；
      ⑤ 识别失败手选 Mote → 署名图出现在参考来源上方、开关组出现、正文 0 条作者行（不伪造）；
      ⑥ 视觉 zoom 验收：与面板控件同设计语言、右对齐、自解释（Rams #4/#10）。
- [x] 生产部署验证（a147af9，deploy run 29139606735 success）：version.json/4 个改动 JS 哈希
      一致；生产页面真机冒烟三态全过（自动·Mote → 通用默认中和 → 切回自动恢复）。

## 细节打磨（2026-07-11 第二轮，用户反馈「下拉可以再优化细节」）

真人视角逐态审查后一次修完四项：
1. 手选态加「本篇例外」小字标记（`.bp-profile-note`）——与块开关偏离默认的既有交互语言一致；
2. `appearance:none` + 自绘 Lucide chevron-down（data URI）——跨平台观感统一（原生箭头
   Windows Chrome 渲染成系统灰盒）；padding 舒展为 4px 24px 4px 8px；
3. `title` 悬停提示「本文作者规则：<当前值>」——自解释 + 长名截断兜底；
4. max-width 190px + ellipsis。
验证：873 测试全绿（+2 断言）；真机 hover 态截图确认边框变蓝、标记与箭头正常。

## 已知未修（用户未授权范围）

MD 粘贴路径「作者：」行**双渲染**（顶部 + 原位各一次）：跨版本验证为既有问题
（d999c07 / 90e6a0e 均复现，非选择器改动引入）。URL 导入路径有
`suppressTopAttribution: true` 抑制顶部重复，`processMd` 漏了同款处理；DOCX 主流程
不受影响（consumeAuthorLine 吃掉作者行）。候选修复=processMd 同样 suppress（作者行
保持原位口径），属行为变更；未获用户明确裁定前不实施。

## 时间线

- 2026-07-11 方案拍板（升级标签），开工；同日细节打磨完成。
