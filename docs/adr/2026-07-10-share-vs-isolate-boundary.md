# ADR: 共用 vs 隔离的判断标准（四层模型）

- 日期：2026-07-10
- 状态：已采纳（用户发起架构体检后成文；依据 WORKLOG-architecture-audit-2026-07-10.md 四路实证）
- 背景：此前「什么能共用、什么必须隔离」散落在 CLAUDE.md 红线 #19/#20/#22/#24 里，是案例法不是原则法。加功能时没有判断标准，靠人记红线，漏一次串台一次（近 3 个月约 8 个渠道串台类 fix）。

## 一句话版

**能力代码全渠道共用（差异只能以参数进入），视觉输出按渠道物理隔离，规则数据按账号显式配置（禁止靠默认继承兜底），操作交互全渠道统一。**

## 四层模型

| 层 | 内容 | 共用还是隔离 | 判据（为什么） |
|---|---|---|---|
| **L1 中立能力层** | parser、docx-article-model、heading-detect、punctuation、blocks/block-render、blocks/rich-text-render、blocks/block-merge、asset-library、templates/shared/table-renderer | **全渠道共用** | 纯算法，与「发布到哪」无关。渠道差异**只允许以参数注入**（scope、renderer 表、cmsSurvival 开关）；文件里出现任何账号/渠道字符串字面量即违规 |
| **L2 渲染器层** | templates/fbif.js、foodtalks-site.js、foodtalks-series.js、mp-blocks.js、site-blocks.js | **按渠道物理隔离** | 视觉真值来自**各发布平台的已发布成品**（微信编辑器清洗规则 ≠ FoodTalks CMS 清洗规则 ≠ 系列满宽口径），真值源不同就永远不能共享实现。渲染器之间禁止互相 import；共性只能下沉到 L1 且必须参数化（table-renderer 是范本） |
| **L3 规则数据层** | api/rule_defaults.go 各账号 config/behavior、/api/rules/* 服务端预设 | **按账号配置，显式继承** | 数据可以像（如 WowFood 继承 FBIF 行为），但**每一处继承必须是显式声明**；规则库 schema v3 用受限 `baseline_id` 指向已登记的内置基线，保存与启动期都会拒绝缺失/未知基线；「不写就默认继承 FBIF」已禁止 |
| **L4 交互语言层** | block-panel（mountBlockPanel）、账号菜单、编辑器外壳、拖拽排序 | **全渠道统一** | 用户的操作心智模型必须一处学会到处适用（2026-07-02 用户裁定网站与公众号共用同一套区块交互语言）。差异只允许出现在「面板里列出哪些块」（数据），不允许出现在「面板怎么操作」（交互） |

## 新功能落层决策流程

拿到一个新需求，先问它是什么：

1. **用户的操作方式**（新面板/按钮/拖拽/菜单）→ L4，一套实现挂 accountId/scope 参数，禁止 per-渠道分叉。
2. **输出的样子**（HTML 结构/样式/尺寸口径）→ L2，写进对应渠道渲染器；如果多个渠道都要，下沉 L1 参数化，**绝不**让渲染器 import 渲染器。
3. **可配的内容**（文案/颜色/开关/正则/页脚素材）→ L3，进服务端规则后台；新账号需要不同值时**显式覆盖**，不写 = 你确认过继承值对该账号成立并留注释。
4. **纯算法**（解析/识别/转换）→ L1；写之前先查 4 个 rule-behavior/布局文件里是否已有同名助手（历史上 compileRegex 被复制了 3 份）。

## 反模式清单（出现即打回）

- L1 文件里出现 `accountId === 'xxx'` / 渠道字面量（现存唯一违例：blocks/author-card-autofill.js:69，已标记待清理）。
- 渲染器 import 另一个渲染器，或从 mp 渲染器复制代码块到 site 渲染器（应下沉 L1）。
- 新账号依赖 mergeRuleDefaults 默认继承而不显式覆盖 `modules.bottom_blocks` / `footer_enabled` / `article_footer_defaults`。
- 把 behParamStr/compileRegex 这类助手复制第 N 份而不是抽到共享模块。
- 在 app.html 或 site.html 的内联脚本里新增逻辑（两文件已有 28 个复制粘贴的同名函数在分叉，见体检报告）——新外壳逻辑必须进独立 js 模块被两页共同 import。

## 边界靠什么强制（按强度排序，弱的要补强）

1. 机器强制（最强）：registry.js/block-registry.gen.js 生成 + 启动期逐字节校验；DefaultPresetID 挂链校验；规则库 schema v3 `baseline_id` 的保存/启动校验（v1/v2 存量自建规则一次性标记为历史 `default` 基线）。
2. 架构强制：L1 依赖注入（不 import 渲染器）；渲染器零互相 import。
3. 测试强制：golden/fidelity/parity/draft-survival 套件 + CI 测试门禁（2026-07-10 起测试不过不部署）。
4. 文档纪律（最弱，只作提醒）：CLAUDE.md 红线 + 文件头注释。新契约优先往 1–3 沉，不要停在 4。
