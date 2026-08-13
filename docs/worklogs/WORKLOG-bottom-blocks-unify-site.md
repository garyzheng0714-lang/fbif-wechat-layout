# 工作文档 · 底部区块跨渠道统一（网站渠道收编）+ 平台化路线

> 开始：2026-07-02 ｜ 状态：**第①步（网站渠道收编）全部收官**。差距清单 1-3 全部完成并验收：T1 golden 双路径字节守卫（`b1a59ce`，破坏性验证有牙齿）→ T2 面板状态收敛 bottom_blocks 单合同（`db77b4e`，两轮 CTO 打回：规则数据分叉 + 空库窗口烧迁移，均修复并有回归测试）→ T3 图片片段单篇替换/素材库选图（`e9d3cf0`，真实浏览器选图闭环验收 PASS）。红线 #20/#22 已改写（CLAUDE.md + AGENTS.md）。执行模式：Codex 写码、Claude 任 CTO 审核（含对抗性验证与真实浏览器时序验收）。收官后用户指出交互仍未统一（开关位置）——并排审计共查出 4 项差异（开关在左应在右 / 冗余"显示/隐藏"标签 / 拖拽无 affordance / 替换按钮违反次按钮 token），`97a9f89` 一次修复并经并排截图逐项验收（教训已入长期记忆：统一类任务必须与基准并排逐项核对，功能对齐≠形态统一）。下一步：路线 ②账号数据化 → ③标杆文章提参 → ④排版 API，各自另开工作文档。

## 用户裁定（2026-07-02，两条，均已确认）

1. **网站渠道页脚面板统一为区块面板交互**（开关/拖拽/替换，与公众号 bottom_blocks 一致）。**推翻红线 #20 的"6 片段复选框"交互形态**；输出 HTML 字节保真不变（golden 守），只换编辑交互。落地后需改写 CLAUDE.md 红线 #20。
2. **平台化四步路线按序执行**：① 底部区块统一到网站渠道 → ② 账号目录数据化（加账号零代码，唯一真相从 `api/account_catalog.go` Go 代码变成服务端数据）→ ③ 标杆文章提参（喂 3-5 篇已发布文章自动生成规则草稿，运营只验收，规则后台萎缩成接入向导）→ ④ Headless 排版 API（渲染 pipeline 抽成可在 Node 运行的纯函数库 + 薄 API 服务，供资讯订阅/智能体调用；Go 继续做存储/抓取/代理）。
   - 附带裁定依据：**不换 Rust**——痛点是领域模型分裂（"底部区块"一个概念、两套半实现），不是语言问题。

## 第一性原理切分（本工程的架构北极星）

- **结构与交互全渠道统一**：区块类型系统（图片/文字/更多文章/二维码）、组合、排序、显隐、替换、单篇覆盖——只应存在一份（数据模型 = `modules.bottom_blocks`，编辑面板 = `public/js/blocks/block-panel.js mountBlockPanel`）。
- **视觉口径按渠道隔离**：渲染器（mp-blocks / foodtalks-site / foodtalks-series）各管各的字号颜色宽度；红线 #19 物理隔离保留在渲染层。
- 此前的病：把"结构"和"皮肤"一起隔离了，导致网站页脚长成第二套交互（红线 #20 复选框）。

## 现状盘点（2026-07-02 实测）

- `block-panel.js` 位于 `public/js/blocks/`，imports 全部中性（block-merge / article-footer-defaults / block-registry.gen / asset-picker / ui-icons），**挂到 site.html 不违反红线 #19**。
- ft-default 服务端已有自己的 `modules.bottom_blocks`（rich_html site footer），系列账号走 fbif_footer 覆盖；ft 渠道数据模型已半只脚在 bottom_blocks 上。
- **工作区有一批未提交的同方向进行中代码**（site.html +88 行、site.css +100 行、site-footer-panel/foodtalks-site-config-flow 测试 +56 行，npm test 698 全绿自洽）：
  - 复选框已换成 `.ft-switch` 开关（"本文显示"父开关，交互对齐公众号）
  - 侧栏片段行已支持拖拽（`ft-footer-seg-row draggable`）
  - 已建 fbif_footer segments → bottomBlocksOverride 桥接（`footerSegmentEnabledForBlock`；测试注释明言"bottom_blocks 是新主合同，fbif_footer 只是兼容回退"）
  - 关闭底部区域时正文流 tip 同步关闭（`disabledFooterSegments` 强制含 tip）
- 该批代码可能属于另一并行会话（同日已发生过一次 rebase 合并：c537f1c inset 重构等 4 commits）。**在归属确认前不大改 site.html，避免会话间冲突。**

## 差距清单（现状 → 目标"网站与公众号同一套区块面板"）

1. site.html 侧栏仍是内联自绘的 `ft-footer-seg` 行，未复用 `mountBlockPanel`——最终应收敛（或明确裁定：网站保留轻量版但交互语义与公众号逐项对齐：开关形态 ✓ 拖拽 ✓ 图片替换 ✗ 素材库选图 ✗）。
2. fbif_footer（页脚库 6 片段）与 bottom_blocks 双合同并存，桥接是过渡态；终态应 bottom_blocks 单合同，fbif_footer 退役（迁移 localStorage `ft.footer.*`）。
3. 字节保真 golden：现有 `buildFooterHtml(segments)` 输出 vs bottom_blocks 渲染输出需逐字节一致的守卫测试（对照 `tests/series-blocks-fidelity.test.mjs` 的做法）。
4. 落地后改写红线 #20（交互形态条款），红线 #22 补"网站渠道已收编"。

## 收官阶段执行模式（2026-07-02 用户指令：Codex 写码、本会话任 CTO 审核）

- 串行派单：T1 golden 双路径守卫（安全网先行）→ T2 单合同收敛收尾 → T3 图片替换/素材库对齐 → 收官（红线改写+终验+部署）。三个任务都碰 site.html 面，禁止并行。
- T2 前置审计结论（2026-07-02）：fbif_footer 前台直接依赖面仅 11 处（site.html 4 / foodtalks-site.js 5 / rule-behavior-foodtalks.js 2 / 规则后台编辑器 0）。**CTO 范围裁定：T2 v1 只收敛前台消费面**（site.html 读写走 bottom_blocks 语义，fbif_footer 在 loadRules 做一次性映射），服务端规则数据形态不动——不迁移生产数据，风险可控；服务端数据层退役另立后续任务。
- T1 关键预期风险：双路径可能**实际不一致**——已要求 Codex 不许"顺手修"，输出差异报告由 CTO 裁定。
- T1 审核结果：通过（破坏性验证：注入 1 字节漂移 → 7 测试全红；还原全绿）。双路径实测逐字节一致，commit `b1a59ce`。
- T2 审核记录（两次打回，教训沉淀）：
  1. **规则数据分叉**：新模块复制了一份 FOOTER_PRESETS（且已漂移：1/0 vs 布尔、名称不同）。追查发现复制源是 site.html 存量 `FT_FOOTER_FALLBACK_PRESETS`——存量病，趁收敛消灭；返工后 import 单一来源 `footer-segments-defaults.js`。
  2. **空库窗口烧掉一次性迁移**（真实浏览器时序验收发现，单测测不到）：`hydrateActiveRulePreset` fire-and-forget，`loadTemplate → syncEngineConfig → readFooterState` 先跑，空 footerLib 算出"空库签名"→ 判旧键失配 → 删旧键写默认，用户自定义必丢。修复裁定：footerLib 空对象 = 未就绪 → 读取零副作用（不迁移/不删键/不落盘）。**教训：涉及"一次性迁移"的代码必须做真实加载时序验收，删除旧数据前先确认判定输入已就绪。**

## 收官前清单（历史快照，现已完成）

- [x] 确认 site.html 代码归属并收尾提交
- [x] 差距 1-3 逐项落地，并用 golden 守住双路径字节一致
- [x] 真实浏览器验证 `/ft` 页面
- [x] 改写红线 #20/#22，并更新本文件顶部状态
- [x] 后续路线分别进入独立工作记录；是否继续以当前产品规则为准，不从本历史清单自动恢复任务

## 时间线

- 07-02 用户裁定方向与优先级；完成现状盘点与差距清单；发现同方向未提交进行中代码，暂停对 site.html 的直接改动待归属确认。
