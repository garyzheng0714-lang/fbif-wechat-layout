# WORKLOG：大修后回归排查——Mote 名片开关消失 + DOCX 前置材料未截断（2026-07-11）

## 目标

用户报告两个生产回归（视为同一事故排查）：
1. 底部区域面板里「Mote 名片」（参考文献前署名图）开关消失，只剩 Bobo 名片等。
2. DOCX 导入没有截断首个「作者：」行之前的编辑前置材料（封面/推荐语/正文标记全进了正文）——违反红线 #12。
   同时预览里参考来源上方的 Mote 署名图也没了——违反红线 #13。

出事文章：「法国第一茶叶品牌，靠"教人喝茶"，开出133家店、1年卖7亿」（Mote 茶叶系列第 22 篇），
从左侧「导入内容」面板以文件形式导入（UI 显示 DOC 徽标）。

验收标准：找到根因；三个症状全部修复并在真实浏览器验证；补上防回归测试；解释清楚「为什么大修后才出现」。

## 已确认的事实（截至排查中）

- 三个症状高度可能同根：**「作者：Mote莫特」这一行没被识别为作者行**。
  - 作者行没识别 → `classifyDocxArticle` 不剥离前置（红线 #12 失效）；
  - `article.author_normalized` 为空 → behavior 条件 `eq mote莫特` 不触发 → 参考来源上方署名图消失（红线 #13 失效）;
  - footer profile 匹配不到「Mote 作者默认」→ `block-panel.js artifactControlIDs` 返回空 → 面板「参考文献前署名图」开关消失。
  - 佐证：用户截图里「作者：Mote莫特」以普通黑色正文渲染（未被吃掉、未变 #396FBC 蓝），说明识别环节整个没跑通。
- `npm test` 861 个测试全绿（含 tea-pyramid golden）。
- 用真实 9MB 样例 `public/__test-tea-pyramid.docx`（系列第 9 篇，.docx 原生）在最新代码下 Node 复现：
  **正常**——前置全剥离、作者行识别渲染成功。⇒ .docx 原生解析主链路没坏。
- `frontMatterScanLimit=25`（只扫前 25 个非空段找作者行）：第 9 篇作者行是第 16 个非空段，擦边通过。
  用户第 22 篇从截图看前置只有约 6 个非空段，不像超限。待用真实文件确认。
- `.doc` 老格式（OLE2）走 `engine.js convertDocToDocxFile` → 服务端 LibreOffice 转 .docx → 再解析。
  转换产物的样式/run 结构可能与原生 docx 不同 —— 重点嫌疑链路。
- 服务端默认规则里没有 `docx_author_line_regex` 参数 ⇒ 前端走内置 fallback `/^作者[：:]\s*(.+)$/`。
  但生产规则库（/api/rules）里若存了坏值会覆盖 fallback —— 待查生产规则数据。

## 大修版图（用户所指「底层架构大更新」候选）

- 2026-07-10 架构体检 S1–S6（86e1dae..90e6a0e）：S6 行为助手收敛 rule-behavior-utils.js、S3 规则合并基线按 preset 归属。
- 2a18ed2 harden async editing（#21）。
- 936dec4 统一导入入口（2026-06-24）：文件/正文/链接合一，「系统自动识别输入类型」。
- 9da3b58 抽共享内容分类层 docx-article-model.js（旧 stripDocxPreambleBeforeAuthor 收敛于此）。

## ★ 根因（已实证，2026-07-11）

`docx-article-model.js` 作者行扫描有硬上限 `frontMatterScanLimit=25`（只扫前 25 个非空段）。
作者行掉出窗口 → `author` 为空 → 三症状连锁：
1. 前置材料不剥离（红线 #12 失效）；
2. `author_normalized` 空 → behavior 不触发 → 参考来源上方 Mote 署名图消失（红线 #13 失效）；
3. footer profile 匹配不到「Mote 作者默认」→ 面板「参考文献前署名图」开关消失。

实证：tea-pyramid 样例（作者行=第 16 个非空段）正常；人工插入前置段推到第 26 个 → 三症状
全部复现，与用户截图逐项吻合（作者行渲染成普通黑色正文）。

排除项（都已验证正常）：生产部署=本地 HEAD（version.json + 6 个关键 JS 哈希一致）；
生产规则数据完好（docx_author_line_regex、Mote profiles、behavior 条件都在）；
.doc 徽标是烟雾弹（engine.js:521 对 .docx 也显示「DOC」）；npm test 861 全绿。

为什么「大修后频发」：25 上限不是大修引入（2026-06-01 抽共享层之前逐字存在），
但编辑部文档前置材料变长（第 22 篇新增「封面」区块+更多候选标题），第 9 篇作者行已在
第 16 位擦线，超限从偶发变高频。属「旧缺陷 × 新文档形态」而非「大修删功能」。

## 修复方案（进行中）

扫描循环遇到编辑部结构标记（正文/标题/封面/推荐语/校对/引言 整行）→ 重置扫描计数
（标记本身证明仍在前置区）。无标记的文档维持 25 上限（防误剥正文中段「作者：」）。
标记正则 + 扫描上限均走 behavior.params 可配（红线：规则数据不硬编码），
前端 fallback 常量 + 服务端默认成对登记。

## 修复实现（已完成，commit d999c07）

- `public/js/docx-article-model.js`：扫描窗口改为「超限关窗、结构标记重开」——
  整行「正文/标题/封面/推荐语/校对」命中即重置计数并重开窗口；无标记文档维持原上限
  （第 25 段行为与旧版逐字一致，防误剥正文中段「作者：」）。「引言」不入默认集
  （可出现在正文起点附近，有误重开风险，且对救活前置作者行无贡献）。
- `public/js/templates/fbif.js`：注入 `docx_front_matter_marker_regex` /
  `docx_front_matter_scan_limit`（新增 behaviorNumberParam 助手），fallback const 兜底。
- `api/rule_behavior.go`：两个新参数成对登记进服务端默认 behavior（规则后台可配）。
- 共享层修复对网站渠道 DOCX 同样生效（同一分类层）；系列 firstImage 信号不受影响。

## 验证记录

- [x] 根因定位（实证复现：作者行第 26 个非空段 → 三症状齐发；≤25 正常）
- [x] 修复 + params 下放
- [x] 防回归测试 3 条（超限+标记→识别剥离；超限无标记→不误剥；标记 opts 可配）
- [x] `npm test` 864 全绿（tea-pyramid / 网站 / 系列 golden 逐字节不变）；`go test ./...` 全绿
- [x] 真实浏览器验证（localhost:19090/mp/fbif，构造「作者行第 28 位」9MB docx 走
      `_handleFile` 真实上传管线）：前置全剥离 ✓、作者行蓝色 ✓、参考来源上方 Mote
      署名图 ✓（肉眼截图确认）、面板「参考文献前署名图」开关 + 「已套用: Mote 作者默认」✓
- [x] 回归面盘点：MD 路径全篇识别作者无窗口限制、URL 导入走服务端 wechat_blocks 独立
      体系（有自测）、系列 firstImage 有独立可配上限——同型问题仅 DOCX 前置扫描，已修
- [x] 生产部署验证：deploy run 29138308510 success；生产 version.json=d999c07、
      docx-article-model.js / fbif.js 哈希与本地一致；生产页面实际加载的 JS 已含
      `DEFAULT_FRONT_MATTER_MARKER` / `scanWindowOpen` 修复代码；/api/health ok

## 结论：这是不是「大修删了功能」？

不是。三症状同根于一个**从最初实现就存在**的扫描上限（旧版 fbif.js 逐字相同的
`nonEmptySeen >= 25` break）。大修各环节（共享层抽取/S1-S6/统一导入）在此链路上
有 golden 逐字节守护、无功能丢失。「大修后频发」的真实机制：编辑部文档前置材料
变长（第 22 篇新增「封面」区块+更多候选标题），把旧阈值缺陷从偶发擦线推成高频触发，
时间上恰与大修窗口重叠。教训：阈值型静默降级（超限后不报错、直接当无作者处理）
是「间歇性回归」错觉的温床——本次修复后超限文档仍可能存在（无任何标记的超长前置），
后续若复发优先查 `docx_front_matter_scan_limit`。

## 时间线

- 2026-07-11 排查开始。锁定三症状同根假设；.docx 原生链路 Node 复现正常；转向 UI 真实验证与 .doc 转换链路。
