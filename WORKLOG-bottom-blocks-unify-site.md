# 工作文档 · 底部区块跨渠道统一（网站渠道收编）+ 平台化路线

> 开始：2026-07-02 ｜ 状态：**方向已由用户裁定，现状盘点完成；site.html 存在一批同方向的未提交进行中代码（可能属另一会话），等待归属确认后收尾**

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

## 后续步骤（待归属确认后执行）

- [ ] 确认 site.html 未提交代码归属 → 收尾提交（含 698 测试）
- [ ] 差距 1-3 逐项落地，每项独立提交 + golden 守
- [ ] 真实浏览器验证 /ft 页面（UI 验证协议）
- [ ] 改写红线 #20/#22，更新本文件状态
- [ ] 路线 ②③④ 各自单独开工作文档

## 时间线

- 07-02 用户裁定方向与优先级；完成现状盘点与差距清单；发现同方向未提交进行中代码，暂停对 site.html 的直接改动待归属确认。
