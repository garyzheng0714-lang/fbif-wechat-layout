# FBIF 公众号排版 — Agent 规则

## 🔴 动手前先看这里：三系统边界（2026-08-06 用户裁定）

FBIF 内容链路由**三个互相独立的系统**组成，各有各的代码库、数据库和服务器。**唯一的通信方式是 HTTP API，不共享任何存储。**

| 系统 | 干什么 | 时效 | 数据库 | 域名 |
| --- | --- | --- | --- | --- |
| **本项目**（FBIF公众号排版） | **加工**：排版、风险检查、飞书审核、分发到公众号/FoodTalks | 收到即处理 | JSON 文件 | `fbifmp-layout.foodtalks.cn` |
| FoodTalks_Feed | **实时监控**：RSS/供应商资讯采集、分类、判断、筛选 | **实时**（持续轮询） | PostgreSQL（它自己的） | `feed.foodtalks.cn` |
| fbif-wechat-article | **归档监控**：微信公众号官方 API 归档、阅读/粉丝指标 | **T+1**，每天 09:00 CST 一次 | SQLite（它自己的） | `112.124.103.65:3002` |

**本项目的定位一句话：只做加工。** 两个监控端把「原文链接 + 规则编号 + 分类证据」推进来，本项目负责抓原文、排版、风险检查、送飞书审核、审核通过后分发。

**两个上游时效不同**：微信官方 API 只拿得到上一天发布的文章，所以那条线是每天同步一次；同一篇公众号文章走 `FoodTalks_Feed` 的 RSS 当天就到，走官方 API 要等第二天。同一篇文章可能先后从两条线进来，`source_key` 幂等保证只建一单。

### 三条不得跨越的线

1. **只接收链接**（红线 33）。`site-sync` 与 `official-sync` 对 `content_html` / `title` / `source_name` / `author` / `cover_url` 一律 400。上游两个监控端都已收敛到位（`6fd97c83`、`5f036db`），不得为任何调用方重开正文旁路。
2. **不碰监控端的代码库。** 上游怎么发现文章、怎么分类、怎么判断价值，都不是本项目该改的。看出问题就说出来，由他们改。反过来他们也不该改本项目。
3. **监控职责归监控端。** 服务健康探针、告警发送、失败计数属于监控端，本项目**只提供被探的健康端点**（`GET /api/publish/monitoring`），不主动探别人的死活。反例：`monitoring/watchdog.py` 曾住在本仓库探三个服务，其中两个是上游；配错了（3002 公网不通 + 探针统一发 `X-Publish-Sync-Token` 而 official 认 `API_KEY`）连续失败 715 次没人发现，因为不在负责人手上。该 watchdog 正在迁往监控端。

## Skill routing

When the user's request matches an available skill, ALWAYS invoke it using the Skill
tool as your FIRST action. Do NOT answer directly, do NOT use other tools first.
The skill has specialized workflows that produce better results than ad-hoc answers.

Key routing rules:
- Product ideas, "is this worth building", brainstorming → invoke office-hours
- Bugs, errors, "why is this broken", 500 errors → invoke investigate
- Ship, deploy, push, create PR → invoke ship
- QA, test the site, find bugs → invoke qa
- Code review, check my diff → invoke review
- Update docs after shipping → invoke document-release
- Weekly retro → invoke retro
- Design system, brand → invoke design-consultation
- Visual audit, design polish → invoke design-review
- Architecture review → invoke plan-eng-review
- Save progress, checkpoint, resume → invoke checkpoint
- Code quality, health check → invoke health

## Design System
Always read DESIGN.md before making any visual or UI decisions.
All font choices, colors, spacing, and aesthetic direction are defined there.
Do not deviate without explicit user approval.
In QA mode, flag any code that doesn't match DESIGN.md.

## 共用/隔离边界（新功能先过这个）
任何新功能动手前，先按 `docs/adr/2026-07-10-share-vs-isolate-boundary.md` 的四层模型落层：
**能力代码全渠道共用（差异只能以参数进入），视觉输出按渠道物理隔离，规则数据按账号显式配置（禁止靠默认继承兜底），操作交互全渠道统一。**
禁止中立层写账号字面量、渲染器互相 import、账号靠默认继承 `bottom_blocks`、复制助手函数，或把新逻辑内联进 app.html/site.html。

## Critical Design Rules (DO NOT VIOLATE)
These have been explicitly confirmed by the user. Breaking any of these is unacceptable:

1. **预览页背景必须全白** — 不要加任何灰色背景，不要 #F7F7F7，不要 #EDEDED
2. **多账号绑定只用单层账号菜单，位置在左栏导入卡第一行** — 新增账号以 `api/account_catalog.go` 为单一真相源，并在 `api/rule_defaults.go` 成对登记 preset；改完运行 `npm run gen:registry` 生成 `public/js/rule-packs/registry.js`，不得手改生成文件。账号菜单统一用 `account-menu.js` 的 `mountAccountMenu` 挂到 `#acctMenu`，公众号 / 网站只作为菜单分组。同模板账号原地切换，跨模板账号整页 reload；不得恢复渠道胶囊与规则包分段组成的两层切换器。
   **左栏两段式（2026-08-03 用户裁定，取代 2026-08-02 标题栏方案）**：不渲染 `.rail-head` 或可见的「导入内容」标题容器；`.rail-body` 是 `--fs-bg-nested` 灰底工作区，导入卡与区块面板都必须是白卡；`.rail-foot` 放 `#userIdentity`，登录行必须贴紧工作区分隔线和右侧栏边，不留卡片式内缩缝隙，菜单向上展开。全局顶栏只留当前页面动作（复制全文 / 规则），不得把 `#acctMenu` 或 `#userIdentity` 搬回顶栏。导入组合框必须用 `aria-label="导入内容"` 保留可访问名称。守护：`tests/top-toolbar.test.mjs`、`tests/account-switcher.test.mjs`、`tests/auth-shell.test.mjs`。
   **导入进度是导入卡内部的状态行（2026-08-05 用户裁定）**：`#progress` 必须留在 `#dropZone` 内、排在提交按钮之后，背景透明由卡片提供白底，`14px` 水平内边距与输入框及动作按钮对齐；形态是 `3px` 细条 + 「状态文案 / 百分比」状态行，不确定态隐藏百分比。禁止恢复卡外那块自带 `--fs-bg-card` 的无圆角白底和满宽粗条。外壳侧写进度只能经 `engine.js` 的 `setShellProgress`（条宽、`aria-valuenow`、可见百分比必须一起更新），且快阶段不得先冲到 95% 以上。守护：`tests/import-progress-component.test.mjs`、`tests/e2e-progress-component.spec.mjs`。
   **已知取舍**：收起左栏会同时收走排版目标与登录身份，需要时展开左栏；不要为此在顶栏另做一份只读副本。
3. **规则服务端保存 + 本地降级** — 参数预设和 `behavior` 行为配置以服务器 `/api/rules/*` 为准，localStorage 只作为兼容与离线降级；不要再新增 `/api/config/*` 这类平行规则入口
4. **页面级动作在顶部工具栏** — 预览区下方不放操作按钮条。左栏底部的身份栏不在此列，它不是动作条。导入卡里没有提交按钮（见规则 32）。
5. **不要 TOC/目录** — 已删除
6. **底部栏复刻微信** — 白底，分隔线不贯穿（max-width 680px 居中），图标颜色 #7F7F7F
7. **文章标题左对齐** — 在文章正上方，27px 粗体
8. **复制后不自动打开外部网站** — 复制按钮只复制内容；微信公众号后台只能通过显式链接手动打开
9. **复制按钮必须立即反馈，但绝不交付缺图产物** — `copyContent()` 不得 `await` 图片上传或“更多文章”合成；这些准备工作只能提前或后台刷新复制层，点击后应立刻写剪贴板并显示复制结果。
   **唯一例外是“此刻还没有图可复制”（2026-08-05 用户裁定）**：服务端会把飞书云文档导出与 `.doc` 转换的 DOCX 图片剥离成 `/api/doc-cache/*` 占位，位图由 `materializeLazyImages` 后台回填。若复制产物里仍有站内占位地址（`/api/doc-cache/*`、`/assets/*`、`blob:`）且 `_uploadPromise && !_uploadDone`，必须立即挡回并显示「图片准备中」，回填完成后按钮自动恢复——这仍是瞬时返回，不是等待，规则不变。站内相对地址出了这个页面就取不到，交付一份微信必然缺图的剪贴板比让用户多等一秒更糟；`copyHTMLSource` 同理。
   **回填必须并发**（每张图立即启动独立读取任务，不设并发上限——`/api/doc-cache/*` 是廉价缓存字节读，不继承上传接口的并发限制）：串行取图会把“预览已出现”到“复制产物真的有图”的窗口拉到数秒，一篇 10 MB 正文图的飞书文档足够让用户在窗口内点中复制。守护：`tests/copy-behavior.test.mjs`、`tests/uploader.test.mjs`。
10. **表单只填数字** — 不要 px/em 后缀，颜色用 color picker
11. **不要 phone-frame 边框** — 文章直接在白底上流动
12. **原创 DOCX 作者行规则** — 首个 `作者：` 行是文章正文起点；它之前的编辑前置材料默认不显示在编辑器里
13. **Mote 作者特殊排版** — 默认 `behavior` 下，`Mote莫特` / `Mote 莫特` 触发参考来源上方固定署名图；原创作者行颜色为 `#396FBC` 且不斜体

14. **更多文章是可选规则模块** — `modules.more_articles.enabled=false` 时新文章不渲染底部更多文章；`modules.more_articles.default_cards` 保存该规则的新文章默认推荐列表。当前文章仍可在侧边栏临时替换、删除、排序和裁剪卡片。
15. **更多文章选图使用当前卡片媒体库** — `/api/fetch-article-meta` 必须返回 `image_urls`；“替换图片”候选顺序为当前卡片自定义封面、抓取头图、当前卡片正文媒体图，主文章图片只做兜底。旧卡片缺少 `image_urls` 时点击替换要按卡片链接补抓，不要显示“本文暂无图片”。
16. **URL 导入图片说明组不可拆散** — `图片 -> 图注 -> 图片来源` 必须连续归为图注；`图片来源：` / `图源：` 不能被当成正文或文章级来源
17. **URL 导入作者来源固定在提示前** — `来源/出处/出品`、`作者/撰文/撰稿/文`、`编辑/责编` 支持中英文冒号及 `/|｜` 分隔，归一为来源行和作者/编辑行，只在标准 `提示：` 前渲染一次。正文显式来源优先，否则才用 `source_name`；不得把结构化 `byline` 擅自补成作者。署名行用公众号蓝、`1.75` 行高、正常字形且连续行不加空行；`editor-shell.js` 到 `fbif.js` 必须保留 `source_name`。
18. **DOCX 连续段落尊重空段语义** — 连续非空正文段落在 Word 段距较小时保持紧凑；只有原文中间有空段时，下一段前才恢复标准 `wx-gap`

19. **FoodTalks 网站规则与公众号物理隔离** — 渲染器（`public/js/templates/foodtalks-site.js`）、行为模板（`public/js/rule-behavior-foodtalks.js`）、页脚模块、署名颜色全套分离。公众号原创 Mote 作者行使用 `#396FBC` 且不斜体；网站署名独立使用 `#888888`、14px、斜体。公众号渲染器与网站渲染器之间不互相 import。修改任一侧规则不能波及另一侧——`public/js/templates/fbif.js` 在网站任务中必须字节级不动。

20. **页脚库完整输出，交互统一走区块面板** — FoodTalks 网站页脚由工具完整输出 `tip / biz_coop / join_group / follow_account / logo / more_info` 六段。主路径是 `modules.bottom_blocks` 的 `rich_html` 区块；`fbif_footer` + `buildFooterHtml` 只作兼容回退，字节一致由 `tests/site-footer-parity.test.mjs` 守护。单篇状态只使用 `site-footer-state.js` 的 bottom-blocks override；服务端规则未就绪时不得产生本地写副作用。换图写入 `overrides[seg].fields` 并保留 `{{asset#id}}` 引用。

21. **网站渠道默认走 ft-default 规则包** — site.html 通过 `getRulePackByPath(location.pathname)` 做渠道感知 fallback：`/ft/<任意 id>` 都会落到该渠道默认包 `ft-default`（templateId 是 `foodtalks-site`）。**禁止**裸用 `parsed.packId`——`/ft/default` 这种链接里的 `default` 在 registry 不存在，裸用会 fallback 到公众号 `fbif`，整套网站模板失效。

22. **所有账号（含网站渠道）共用同一 `bottom_blocks` 底部结构与交互语言，差异只在服务端默认与各渠道渲染器** — 区块交互统一走 `mountBlockPanel`；每个账号必须在 `api/rule_defaults.go` 显式覆盖 `modules.bottom_blocks`，不得依赖 FBIF 基线兜底。FoodTalks 系列由独立渲染器输出满宽页脚，只能复用中性 `block-render` / `asset-library`，不得引入 `mp-blocks`。拖拽排序统一使用 `public/vendor/Sortable.min.js`；系列字节保真由 `tests/series-blocks-fidelity.test.mjs` 守护。

23. **微信复制层在 T0 即可保存草稿** — 更多文章 `linktype="image"` 卡片的 `imgurl` 必须是微信图床；没有微信图床封面时不得伪装成安全卡。卡片 `<img src>` 必须是微信图床或 base64 位图，OSS / 外站 HTTP 不能是立即保存时唯一安全图，SVG data 不可用，空卡占位必须是 PNG data。三套卡片渲染器的 wrapper 锚点保持一致；`_footerCopy` 只能经 `copy-session.js` 的 `footerCopyGate` 权威重建，后台任务和旧快照不得覆盖新会话。改复制层前必须通过 `tests/wechat-draft-survival.test.mjs`、`tests/copy-session.test.mjs`；完整契约见 `openspec/specs/wechat-draft-survival-copy/spec.md`。

24. **网站表格须经 CMS 清洗后存活** — FoodTalks CMS 会剥除 table/td 内联样式；site/series 必须用 `public/js/templates/shared/table-renderer.js` 的 `outerBorder`+`cmsSurvival`，把 align/valign/bgcolor/width、cellpadding、表头 strong 与样式双写。图片按 parser `wpx` 双写 width；fbif 不开启、字节不变。守护：`tests/table-renderer-shared.test.mjs`、`tests/foodtalks-site-docx.test.mjs`；详见 `WORKLOG-table-cms-survival.md`。

25. **异步编辑必须 latest-wins（2026-07-10 对抗式审计）** — URL 导入、文件/区块导入、重渲染、更多文章元数据、复制准备、重试、规则保存和账号切换都必须在意图开始时取得版本门，并在每个 `await` 后、提交 DOM/全局状态/持久化前确认仍是最新意图；任一新入口必须主动废止会与之竞争的旧入口。账号切换是事务：规则、模板、配置和菜单只能一起提交，失败必须保持原账号，不得出现半切换。守护：`tests/editor-shell.test.mjs`、`tests/copy-session.test.mjs`、`tests/active-rule-selector.test.mjs`、`tests/account-switcher.test.mjs` 及浏览器回归。

26. **网站发布安全与恢复契约（2026-07-10 对抗式审计）** — FoodTalks 不可逆发布只能发生在持久化的人工 `approve`、审核时间、风险结论为 `pass|warning` 且审核版本匹配之后；`block`、缺失或未知风险一律 fail closed。FoodTalks `POST` 前必须先持久化创建意图，结果不确定时进入 reconciliation，不得盲重试制造重复稿；`source_key` 永久去重。飞书回调必须校验 verification token 与审核人白名单，外部文本进入卡片前必须清洗；审核通知走持久 outbox，审核回调只接受决策，发布异步执行并支持重启恢复。当前 JSON store 仅支持单进程，部署不得横向启动多个 API 实例；多实例前必须迁移到带事务/锁的存储。

27. **部署候选必须暂停写入并通过 readiness（2026-07-10 对抗式审计）** — 候选 API 以 `DEPLOYMENT_WRITES_PAUSED=1` 启动，发布/rules 写入口、恢复任务、通知 worker 与飞书长连接都暂停；公网 SHA、健康、鉴权及管理员 `/api/publish/readiness` 全通过后才接受 release。接受后先关闭自动回滚再解暂停；解暂停后外部副作用可能恢复，失败只能向前修复，不得自动切回旧版制造重复发布。FoodTalks 目标必须用 `FOODTALKS_EXPECTED_TARGET=test|production` 显式声明并与 API/前台/后台 URL 一致。

28. **微信公众号历史已发布全量口径（2026-07-15，最高优先级）** — `273/311` 及任一单接口、单表或素材库行数都不是历史总量。完成多官方 API 原始响应与本地多表按 `msgid`、`msg_data_id`、文章序号、`article_id` / URL、发布日期关联去重，并通过分页、时间和字段覆盖审计前，只能写“未完成/未核验”。所有官方响应与可获取字段默认原样落库；飞书历史同步保持关闭；素材库不是已发布真相源。

29. **已发布监控不得依赖草稿（2026-07-16）** — 08:30–18:30 的 `freepublish` 最新页轮询必须独立执行，任何 draft / 素材失败不得阻断；“群发”须走另一条持久、幂等的发现路径。启用统一“服务器配置”前必须审计现有回调、自动回复和菜单；小绿书、图集或类型不明一律 fail closed。

30. **FBIF 公众号监控是 P0 上线红线（2026-07-17，2026-08-06 修订看门狗归属）** — 任意改动必须通过无 paths 过滤的 `.github/workflows/p0-monitoring-contract.yml`，失败不得合并或部署。本仓库这一侧守的是**加工链路**的 fail closed：同步缺口、45 分钟未到审核态、通知/卡片错误、重复 `source_key`、轮询陈旧和队列积压，全部由 `GET /api/publish/monitoring` 如实上报（任一任务仍在 processing、通知仍 pending/sending、审核卡缺失或失败时 `ready=false`；进程存活不等于业务完成）。禁止删测、skip、降级或忽略故障来“修绿”。
    **外检看门狗不在本仓库**（2026-08-06 迁往监控端 `FoodTalks_Feed`，服务名 `fbif-link-watchdog`）。原来住在这里的 `monitoring/watchdog.py` 每两分钟外检 layout/feed/official 三个服务，其中两个是上游；它配错了（3002 端口公网不通 + 三条探针统一发 `X-Publish-Sync-Token` 而 official 认自己的 `API_KEY`）连续失败 715 次无人处理——探针不该住在被探对象手里。本仓库**只提供被探端点，不主动探别人的死活**；部署链路里 `prepare-monitoring` 已改为幂等的 `cleanup_legacy_watchdog`，只负责把生产上残留的单元和 `/opt/fbif-monitor-watchdog` 摘干净，且回滚不恢复。不要在本仓库重建任何外检探针。

31. **飞书云文档导入必须走异步作业 + 服务端批量镜像（2026-08-05 恢复上线）** — 交互式导入只走 `POST /api/feishu/docs/export/jobs` 加 long-poll `?after=<version>&wait_ms=`，服务端逐阶段上报真实 `done/total`（resolve→create→export_wait→download→optimize→image_enumerate→image_upload→image_map）；正文图必须在服务端一次 `base64_images` 批量镜像到 OSS，不得让浏览器逐张回源 `/api/doc-cache/*`。导出轮询 `250ms` 加递增退避，不得改回固定 `1s`。预览走 `preview-image-loader` 预解码加 `preview-dom` 离屏 staging 整帧提交，导入期间旧文章保持可见。**禁止**退回同步导出端点，或删除 `api/feishu_export_jobs.go`、`api/doc_image_mirror.go`、`public/js/preview-dom.js`、`public/js/preview-image-loader.js`、`public/js/feishu-doc-ready.js`、`public/js/import-progress.js`。
   **处理 merge 冲突不得整树取一侧**（如「树以本分支为准」），必须逐文件裁决，合并后核对 `git diff --diff-filter=D <父> <merge>` 无意外删除——上面这套体系连同守护测试就是被 `32e8dcd` 这样整树删掉的，测试一起消失所以 CI 不红。守护：`tests/e2e-preview-speed.spec.mjs`、`tests/e2e-feishu-import-progress.spec.mjs`、`api/feishu_export_jobs_test.go`、`tests/preview-dom.test.mjs`、`tests/preview-image-loader.test.mjs`、`tests/feishu-doc-ready.test.mjs`、`tests/import-progress.test.mjs`。

32. **粘贴即排版，导入卡没有提交按钮（2026-08-06 用户裁定）** — 往导入框粘贴内容，`bindUrlImportShortcuts` 立即按框里的**最终全文**提交：链接（含飞书文档链接，走 `tryImportValue`）和正文/Markdown 一视同仁，追加粘贴则整篇重排。唯一不自动的是 `@` 开头的飞书文档搜索（由 `shouldSubmitValue` 挡下）。`#importSubmitBtn` / `.import-submit-button` 已删除，**不得恢复**；动作行末尾只留状态文案 `粘贴后自动排版`。删按钮后手打输入的键盘出口是 `Cmd/Ctrl+Enter`（design.md 否决项：键盘必须能完成核心操作），改导入卡时不得连它一起删。这条行为原本就是自动的，在 `a9bd5d1`「polish rail import composer」改版时被换成必须点击的按钮，不得重复该退化。守护：`tests/editor-shell.test.mjs`、`tests/top-toolbar.test.mjs`、`tests/import-progress-component.test.mjs`、`tests/e2e-paste-autoformat.spec.mjs`。

33. **对外发布接口只接收链接（2026-08-06 用户裁定，红线）** — `POST /api/publish/site-sync` 与 `POST /api/publish/official-sync` **只收 `url`**，正文由排版服务自己从原文现抓，走与网页工具粘链接完全同一条解析路径。`content_html` / `title` / `source_name` / `author` / `cover_url` 五个字段一律 **400 显式拒绝**（不是静默忽略——调用方会以为自己传的正文生效了），body 上限 `64KB`，判定共用 `rejectedPublishContentFields` 一份，不得复制第二份。两个入口的差异只剩类型证据：official-sync 把 `classification` 钉成 `ordinary_confirmed`（调用方已用官方 `article_type=news` 确认），防小绿书/图集误投。
   **不得把正文旁路接回任何 HTTP 入口**：`submitStructuredContent` / `submitOfficial*` / `submitSupplier*` / `officialPlainTextBlocks` 已无生产调用方，只余单测；`publishContentRank` 里 `official_api`/`supplier` 优先级高于自抓的 `web_fetch`，接回去就是「投什么发什么」。用户报告的任务 #72 排版崩坏（长文零小标题、正文段落被排成灰色小字图注）即出自该通道（具体崩在哪一步未复现，无留存样本，**不要在文档里把成因写死**）。
   **机器提交鉴权是 `X-Publish-Sync-Token`**；`X-Admin-Password` 自 `f591217`（2026-08-04 会话角色改造）起已不被认，调用方 `fbif-wechat-article-official-api` 需配 `LAYOUT_SYNC_TOKEN`（= 本服务 `PUBLISH_SYNC_SERVICE_TOKEN`），旧的 `LAYOUT_ADMIN_PASSWORD` 已废弃。守护：`tests` 侧见 `api/publish_http_test.go` 的 `TestPublishOfficialSyncFetchesFromURL`（断言 `fetchCalls==1`，正文旁路一接回就红）、`TestPublishOfficialSyncRejectsCallerSuppliedContent`、`TestPublishSiteSyncRejectsCallerSuppliedContent`、`TestPublishMachineSyncRejectsActiveHTMLAtTheDoor`。
   **例外**：`POST /api/html-to-blocks`（Bearer `RENDER_TOKEN`）不在此列——它是 HTML→Block 的纯转换工具，调用方 `FoodTalks_Feed` 排版的是自有资讯，根本不存在可抓的微信原文链接。它不创建发布任务。

34. **审核卡四种形态都必须能认出是哪篇文章（2026-08-06 用户裁定）** — 待审核 / 已发布 / 已驳回 / 异常四张卡全部渲染 `reviewCardHeroBlock`：封面图、粗体标题、标签、摘要、`预览效果 · 公众号原文` 链接缺一不可。**不得**让终态卡退化成只有 `reviewCardInfoBlock`（来源/作者/时间 + 任务号两行灰字）——卡片在群里长期留存，是事后回看「到底发了什么」的唯一入口，压成一个编号等于信息归零。封面复用待审核时存下的 `publishJob.ReviewCoverImgKey`（卡片回调必须同步返回新卡，不能在回调里现传图片）；旧任务没有该值时退化为无封面单列，不留空图位。守护：`api/feishu_review_card_test.go`。
   **改完卡片要把历史任务的卡也刷一遍**：已发出的卡不会自己变样，群里回看到的仍是旧信息。用 `POST /api/publish/jobs/{id}/card-refresh`（管理员或 `PUBLISH_ADMIN_SERVICE_TOKEN`，同步服务令牌不可用）；有 message_id 的群走原位更新，不刷屏。它与 `notification-replay` 分工不同：那个只救躺在 dead letter 里的失败通知，救不了「发成功但样式过期」。**card-refresh 只换样式，不重跑检查**：旧任务的 `RiskItems` 没有 `certainty`、没有 `RiskHeadline`，刷新后一律落折叠层，首屏合成「未发现可证实的实质性错误」——这对它们是准确的（当时就没查事实错误），但别指望刷一下就能把旧文章里的硬伤翻出来。

35. **审核卡的检查信息分两层，实质性错误上首屏（2026-08-08 用户裁定）** — 首屏红块（`reviewCardConfirmedRiskBlock`）只放**已证实的实质性问题**，最多 3 条；转载授权无论 `certainty` 是什么、以及 `system` 类，一律沉到折叠的「点此查看完整检查报告（N 条待核提示）」。判定口径在 `publishRiskItemConfirmed` / `splitPublishRiskItems` 一处，不得在卡片层另写一份。
    **`certainty` 只认显式 `confirmed`**：解析层对缺失、拼错、未来新值一律收敛为 `unverified`（`parsePublishRiskAssessment`）。猜测冒充已证实的错误被推到审核人眼前，比漏报更糟。
    **没有已证实问题时首屏不许谎称干净**：pass 无条目才说「检查通过」口径的话；有待核提示说「未发现可证实的实质性错误」；`block` 必须显示「风险阻断」（header 是红的、首屏说没事等于教人无视阻断结论）；级别未知显示「检查未完成」。状态标签只留给阻断与检查未完成——正常态挂标签既和紧随其后的结论复读，又和标题下的蓝色文章标签形状雷同。
    **同义信息只说一遍**：待核条数由折叠条标题承担（首屏与 headline 都不复读）；待审核卡的 `reviewCardInfoBlock` 传 `compact=true`，不重复 header 副标题已有的环境与任务号。
    **条目排版用飞书官方无序列表，项内换行必须 `<br>`**：单个 `\n` 是软换行，官方文档明说渲染时可能被忽略，实测就是被忽略的——「原句」会粘到上一句尾巴上。首屏与折叠区共用 `reviewCardRiskItemList`，语法只维护一份。守护：`api/publish_risk_layers_test.go`。
    **对应的 PRO 在 `publishRiskSystemPrompt`**：五类检查里 `factual`（事实与常识）是最高优先级，`headline` ≤100 字且代码层硬截断、模型不给时由 `publishRiskHeadlineOrFallback` 合成。

## Server
- Go API server 同时提供静态文件（../public）和 API
- 图片上传需要 `WECHAT_UPLOAD_ENDPOINT` 环境变量
- 规则后台使用飞书登录会话与角色权限；超级管理员来自 `RULE_SUPER_ADMIN_OPEN_IDS`，部署/运维另用 `PUBLISH_ADMIN_SERVICE_TOKEN`
- 规则预设默认写入 `data/rule-presets.json`；可用 `RULES_STORE_PATH` 覆盖
- 启动：`cd api && go run . -port 19090`

## Deploy
- 推送 `main` 触发 `.github/workflows/deploy.yml`
- 部署会把 `LIBREOFFICE_URL`、`LIBREOFFICE_TOKEN`、`CLOUDCONVERT_API_KEY`、`PUBLISH_ADMIN_SERVICE_TOKEN`、`RULE_SUPER_ADMIN_OPEN_IDS` 写入服务器 `/var/www/api/.env`
- `RULE_SUPER_ADMIN_OPEN_IDS` 缺失时生产启动必须失败，不能让正式端进入“没有可识别超级管理员”的状态
- 部署工作流必须确保 `/`、`/index.html`、`/app.html`、`/version.json` 响应头为 `Cache-Control: no-cache`
- 修改正式后台权限时，更新 `RULE_SUPER_ADMIN_OPEN_IDS` 或由超级管理员在“权限管理”中授权，再重跑部署/刷新会话验证 `/api/me` 与后台接口

## Rule Hygiene
- Rules in this file must describe current behavior, not historical requests.
- Mutable product preferences need source/date/status before being treated as stable.
- `behavior` 是受限规则文档，不是任意 JS 执行入口；只用于作者/来源行、Mote 署名图、原创/转载页脚提示等轻量业务规则。
- Copy behavior must not navigate away from the editor unless the current user request explicitly asks for it.
- Copy speed is a product contract: do not hide background work behind the copy button or change the button text to upload/preparation states.
- URL 导入、DOCX 段距和复制链路的排版契约必须有测试保护；新增解析规则时至少跑 `cd api && go test ./...`、`npm test`，并用真实浏览器验证用户给出的样例文章。

## Agent docs
- Issues：`docs/agents/issue-tracker.md`
- 五类 triage：`docs/agents/triage-labels.md`
- 单上下文与 ADR：`docs/agents/domain.md`
