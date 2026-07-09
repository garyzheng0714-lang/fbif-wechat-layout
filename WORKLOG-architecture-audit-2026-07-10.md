# WORKLOG — 排版工具架构体检与共用/隔离边界标准（2026-07-10）

## 目标（用户原话拆解）

1. 「总是出现莫名其妙的 bug，打补丁精疲力尽」→ 找出结构性根因，不是再修一个 bug
2. 「什么能力可以共用、什么规则无法共用，没有清晰边界和判断标准」→ 产出一个判断标准（原则法），替代现在 CLAUDE.md 红线清单的案例法
3. 「先把这个项目捋清楚优化好，它是地基能力」→ 架构地图 + 分级优化清单 + 有测试保护的安全收敛

优先级：本任务优先于 content-publish-pipeline 的全部 Phase（用户 2026-07-10 裁定）。
FoodTalks 冒烟测试（已批准）排在本任务之后。

## 已量化的事实（体检指标）

- 近 3 个月（2026-04-01 起）456 commits，其中 179 个 fix 类（39%）
- 高频改动热点：app.html 113 次、engine.js 94 次、app.css 54、site.html 53、foodtalks-site.js 50、fbif.js 43、api/main.go 39、rule_defaults.go 32
- 超 900 行红线文件：engine.js 1996、site.html 1852（内联脚本）、foodtalks-site.js 1151；贴线：app.html 919、admin-panel.js 895
- 测试：64 个测试文件共 ~12.9k 行（防线厚，但分布待映射）
- CodeGraph MCP 未连接（工具不可用），本次用并行 agent 人工建图

## 审查分工

- Agent A：巨石解剖（engine.js / app.html / site.html）——职责清单、window.* 全局状态清单、复制链路数据流
- Agent B：共用/隔离现状地图（templates 五个渲染器 + shared + blocks 中立层 + rule-behavior×3 + registry/rule_defaults 成对契约）
- Agent C：测试防线映射（64 测试守什么，高频 fix 路径哪里裸奔）
- 主线：179 个 fix 提交归因分类 + WORKLOG 踩坑模式提炼

## 状态

- [x] 体检指标量化
- [x] 四路审查完成（A 巨石解剖 / B 共用隔离地图 / C 测试防线 / 主线 fix 归因）
- [x] 根因归纳（见下「根因综合」）
- [x] 共用/隔离判断标准成文 → `docs/adr/2026-07-10-share-vs-isolate-boundary.md` + CLAUDE.md 摘要
- [x] 优化清单分级（见下）
- [x] 第一批安全收敛已执行并验证（CI 测试门禁 + DefaultPresetID 挂链启动校验 + CLAUDE.md 红线 #2 事实修正；npm test 759 绿、go test 全绿）
- [ ] 大手术清单待用户批准后立项执行

## 根因综合（为什么「总出莫名其妙的 bug」）

1. **测试防线没接上部署**（最大杠杆）：1.3 万行测试 CI 一个不跑，只 build 就上生产。→ 已修：deploy.yml 加 JS+Go 测试门禁。
2. **三个改动热点行为零覆盖**：app.html/site.html 内联脚本（合计 ~2300 行）与 engine.js 编排层没有任何测试执行过，守护全是「源码字符串存在性」断言。
3. **复制链路双真相源 + 补丁式异步守卫**：显示层与复制层分离，两路异步任务竞态改写复制层，靠两套代数守卫硬顶——每个守卫都是一次事故的疤痕。
4. **外壳逻辑双份维护**：app.html 与 site.html 28 个同名函数复制粘贴且已分叉（syncEngineConfig 15 行 vs 68 行）。
5. **20+ 个 window 全局当状态总线** + 「默认继承 FBIF 基线」的规则合并策略 → 渠道串台、时序 bug 无法静态追踪。
6. 部署把生产 Caddyfile 当补丁目标（~250 行注入脚本），同一路由问题连修 6 次；静态资源版本化不统一，缓存 bug 修了 5+ 次。

## 优化清单分级

### 已完成（2026-07-10 本批，全量测试保护）
1. CI 测试门禁（deploy.yml：npm test + go test 不过不部署）——验证方式：下次 push 观察 Actions 门禁生效
2. `validateAccountPresetLinkage` 启动校验 + 测试（DefaultPresetID 悬空从「人肉契约」升级为机器强制）
3. 共用/隔离判断标准 ADR + CLAUDE.md「新功能先过边界」规则
4. CLAUDE.md 红线 #2 过时事实修正（registry.js 是生成文件、真相源是 account_catalog.go）

### 待批准的大手术（按投入产出排序，每项独立立项）
- **S1 外壳收敛**（中投入，直接消灭「改一处忘另一处」）：把 app.html/site.html 的 28 个复制粘贴同名函数抽成共享 shell 模块（两页 import 同一份），从此外壳 bug 只修一次。前置：先给现有行为补契约测试。
- **S2 复制链路状态机化**（高投入，根治复制类 bug）：把 engine.js 里 11 个复制状态变量 + 两套守卫收敛为单一 CopySession 对象（一次导入=一个不可变会话，天然免疫旧任务回写），并给 initApp 编排层建行为测试。
- **S3 规则合并显式化**（中投入，根治串台）：mergeRuleDefaults 从「默认继承 FBIF」改为「新账号必须显式声明每个模块的继承或覆盖」，缺声明启动报错。
- **S4 Caddyfile 声明式管理**（低投入）：仓库存完整 Caddyfile 模板，部署整文件替换 + validate，删掉 250 行注入脚本。
- **S5 视觉基线**（低投入）：e2e-regression 纳入可跑路径（npm run test:e2e），截图与基线做像素 diff；至少覆盖 fbif/site 两渠道首屏与文章页。
- **S6 小额清理**：助手函数家族去重（compileRegex 等 4 份→1 份）；author-card-autofill 硬编码迁出中立层；KNOWN_CHANNELS 并入生成链。

## 结论

「没有安全感」的来源不是代码烂，而是**防线和边界都存在但没接电**：测试写了 CI 不跑，隔离原则有但靠人记，契约多数靠注释。第一批收敛把「接电」成本最低的三处（CI 门禁、挂链校验、边界标准成文）做掉了；剩余安全感缺口集中在 S1/S2（外壳与复制链路），需要用户批准立项。

## 发现（实时回写）

### 主线：179 个 fix 提交归因聚类（已完成）

| 类别 | 约占 | 代表提交 | 结构性根因判断 |
|---|---|---|---|
| A. 内容分类启发式（标题/图注/正文/引用区误判） | ~25 条 | e9da55d、389a177、222f375、764fd56、a1d2d15 | 问题域本身是概率题（从视觉信号猜语义），每篇新文章都可能出新反例；需要「真实负样例回归库」让每次误判变成永久测试，而不是一次性补丁 |
| B. 页脚/底部区块 | ~20 条 | 66535c8（isRepost 竞态/跨文章 job 污染）、4a70909、d895545 | 全局可变状态 + 多渠道分支 + 默认继承，是纯架构问题 |
| C. 微信编辑器黑盒逆向（粘贴/保存存活） | ~18 条 | 2368cff、70391ee、9a79a20、ff8132d | 外部黑盒不可消除；wechat-draft-survival 模拟器是正解，需确认覆盖完整 |
| D. 渠道串台/隔离泄漏 | ~8 条 | a896172（ft-default 混进 mp 列表）、ffc2c66（空串 fallback 公众号页脚）、d895545 | **「默认 fallback 到 fbif 基线」的设计让新渠道天然带着公众号的影子**——用户「边界不清」直觉的实证 |
| E. 部署/缓存基础设施 | ~20 条 | Caddy 路由同一问题连修 6 次（b32813a→e418c15→c7f90e7→8cb25e4→a68aaad→c72c993）；no-cache 头修 5+ 次 | 部署靠对生产 Caddyfile 做注入式 patch，脆弱；静态资源无统一版本化方案，缓存 bug 反复回来 |
| F. UI/交互打磨 | ~25 条 | 导入面板系列、选择器系列 | 多为设计迭代非 bug，不计入架构问题 |
| G. 上传/图片链路 | ~12 条 | 78b2f65（并发 20→5）、dbccc9f | 并发/超时参数试错式收敛 |
| H. 复制链路内容丢失（早期） | ~6 条 | 9a79a20（FONT_STACK 引号未转义）、bf9185f（变量改名 undefined） | 无 lint/类型检查，低级错误能上生产 |

另发现：两对完全同标题的重复提交（d74dbea/703f09f、e9e34ac/16f8145），流程侧存在 revert-重做痕迹。

### Agent C：测试防线映射（已完成）——「没有安全感」的最直接解释

1. **CI 只 build 不 test**：`.github/workflows/` 仅 deploy.yml；grep 确认 `npm test`/`go test`/`playwright test` 一次都没出现。61 个 JS 测试（~12.9k 行）+ 15 个 Go 测试全部只能本地手动跑——**测试挂了照样部署**。
2. **三大改动热点行为零覆盖**：app.html 内联 ~740 行脚本（113 次改动）、site.html 内联 ~1560 行（53 次）、engine.js 编排层（94 次，1996 行）——没有任何测试执行它们；所谓覆盖全是 `readFile`+正则断言「源码里存在这行字」，不验证行为。engine.js 的 re-export 子模块各有单测，但 initApp 内 100+ 个闭包（复制链路编排、footer override、转载改写）一行没被执行过。
3. **唯一的浏览器级测试连本地都不跑**：e2e-regression.spec.mjs 是 `.spec.mjs`，npm test 的 glob `tests/*.test.mjs` 不匹配；且截图只存盘不做基线对比。视觉层（app.css 54 次改动）没有任何会自动失败的守护。
4. **golden 快照只有 fbif 模板全量**（DOCX→HTML+复制层，tea-pyramid 一个夹具）+ site footer 一段；foodtalks-site 主体、series、mp-blocks、site-blocks 无字节级快照。
5. 反向奇观：homepage-performance.test.mjs 用字符串断言守 deploy.yml 的缓存头——JS 测试在守 CI 配置，而 CI 不守任何测试。

### Agent A：巨石解剖（已完成）——「莫名其妙 bug」的机理

1. **`initApp()` 单函数约 1500 行**（engine.js 482–1996，占全文件 75%），十几种职责共享同一闭包变量池：格式转换/UI 状态/解析管线/footer 组装/图片上传/session 快照/复制链路。`_isRepost` 被 5 处读写，注释里反复自我告诫「绝不在 await 后回读全局 _isRepost」——坑已踩过的直接证据。
2. **复制链路双真相源**：显示层（DOM）与复制层（闭包 `_articleCopy`/`_footerCopy`）分离，复制层被两路异步任务（图片上传、更多文章合成）在渲染后持续改写，靠 `_footerCopyGen` 数字守卫 + `_moreArticlesCopySeq` 两套补丁式守卫防错位。一次复制横跨 11 个闭包变量 + 4 个 window 全局 + 2 套守卫。
3. **20+ 个 `window._xxx` 隐式全局当跨文件状态总线**：`_activeConfig` 被 7 个文件读写且与 export 的 `getActiveConfig` 双通道并存；`_skipUpload` 三处写一处读；`_engineApi` 被组装两次并自我 spread（1216/1986）。模块用 ESM 加载，但状态传递全靠 window——「模块边界是 ESM 的，数据流是隐式全局的」。
4. **app.html 与 site.html 有 28 个同名函数是复制粘贴**（规则选择器/版本热更新/配置读写整套外壳双份维护），部分逐字一致（escText），部分已分叉（syncEngineConfig 一个 15 行一个 68 行；renderActiveRuleCard 文案/按钮漂移）。改一处必须记得改另一处——「打补丁精疲力尽」的直接来源。
5. engine.js 还是 parser/punctuation 的**唯一 re-export 转发枢纽**（隐藏中心依赖点），下游都从 engine 拿符号。

### Agent B：共用/隔离现状地图（已完成）——边界比想象中健康，但薄弱点明确

**比预期健康的部分（要保护，别推翻）**：
- 账号身份契约已是**机器强制**：registry.js 是 `account_catalog.go` 的生成文件（`npm run gen:registry`），Go 启动期 `validateRegistryConsistency` 逐字节比对、漂移即 log.Fatal——CLAUDE.md 红线 #2 的描述已过时（比文档说的强）。
- 渲染器隔离靠**依赖注入**成立：block-render/rich-text-render 收 renderer 表参数、从不 import 渲染器；渲染器之间零互相 import（红线 #19 是架构事实不只是纪律）。
- 中立层基本真中立（asset-library 用 scope 参数、block-panel 用 accountId 参数），唯一例外：`blocks/author-card-autofill.js:69` 硬编码 `accountId !== 'foodtalks-series'`。

**真实的债**：
1. **「不显式覆盖就继承 FBIF 基线」的 mergeRuleDefaults 是串台之源**：WowFood/系列/网站系列三个账号都靠显式覆盖打补丁堵串台，rule_presets.go:135 还有为 article_footer_defaults 写的定点兜底——串台模式反复出现才需要逐字段补丁。
2. **助手函数家族 4 份复制**：behParamStr/behTemplate/compileRegex/runsToText/elemText 在 rule-behavior-foodtalks.js、-series.js、foodtalks-site.js、site-contact-layout.js 各一份；compileSiteRegex 与 compileSeriesRegex 除函数名外字节相同——无去重机制，可独立漂移。
3. **读同一份 config.behavior 存在两套范式**：FBIF 声明式（getPath/normalizeRuleBehavior）vs FoodTalks 命令式（behParamStr/classifySeries），零共享。
4. **图片区块渲染 3 份实现**（mp-blocks 661px / site-blocks 满宽 hex / series 内联满宽 100%）——红线 #19 刻意隔离的代价，属「显式择一」而非失误，但意味着区块类型每加一种要写三遍。
5. **无机器强制的薄弱边界**：DefaultPresetID↔preset 存在性无校验（写错则悬空）；builtinVersionForDefaultRule switch 漏 case 静默拿通用版本号；rule-packs/url.js 的 KNOWN_CHANNELS 硬编码非生成。

### 新增账号登记点（实测）：情形 A（复用渲染器）3 个 Go 文件 + 重新生成 registry + 测试；关键人肉契约=「必须显式覆盖 bottom_blocks 等模块否则串台」。
