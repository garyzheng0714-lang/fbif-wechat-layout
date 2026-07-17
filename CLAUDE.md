
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
反模式（出现即打回）：中立层写账号字面量；渲染器 import 渲染器；新账号不显式覆盖 `bottom_blocks` 等模块靠继承兜底；助手函数复制第 N 份；往 app.html/site.html 内联脚本里加新逻辑（外壳逻辑必须进独立 js 模块）。

## Critical Design Rules (DO NOT VIOLATE)
These have been explicitly confirmed by the user. Breaking any of these is unacceptable:

1. **预览页背景必须全白** — 不要加任何灰色背景，不要 #F7F7F7，不要 #EDEDED
2. **多账号绑定，统一顶栏账号菜单（单层）** — 公众号是规则应用对象，规则是可复用资产，新增账号的单一真相源是 `api/account_catalog.go`（改完跑 `npm run gen:registry` 重新生成 `public/js/rule-packs/registry.js`——它是生成文件勿手改，Go 启动期逐字节校验漂移即 fatal），并在 `api/rule_defaults.go` 成对登记 preset（`DefaultPresetID` 挂链有启动校验，2026-07-10 起）。账号切换走**单层顶栏头像菜单**（`public/js/account-menu.js` 的 `mountAccountMenu`，挂载点 `#acctMenu`）：列出全部账号，渠道（公众号 / 网站）降为菜单内的**分组标签**，不再是独立控件。点击同模板账号（如 `FBIF公众号`↔`WowFood爆品超市`，templateId 都是 `fbif`）原地切换；跨模板账号（`FoodTalks什么是系列` / `网站默认`）整页 reload 到其 URL（红线 #19 隔离）。跨渠道编辑器快照由 `session-restore.js` 的 `beforeunload` 兜底保存（无条件注册，不依赖旧 `.channel-switcher`）。**2026-06-02 用户裁定「不要两层」，推翻旧"圆角渠道胶囊 + 方角规则包分段 两层分离（方角 vs 圆角）"契约**——旧的两层切换器（顶部 `.channel-switcher` 圆胶囊、侧栏 `.account-switcher` 方角分段）已删除，不要恢复
3. **规则服务端保存 + 本地降级** — 参数预设和 `behavior` 行为配置以服务器 `/api/rules/*` 为准，localStorage 只作为兼容与离线降级；不要再新增 `/api/config/*` 这类平行规则入口
4. **所有按钮在顶部工具栏** — 底部不放按钮
5. **不要 TOC/目录** — 已删除
6. **底部栏复刻微信** — 白底，分隔线不贯穿（max-width 680px 居中），图标颜色 #7F7F7F
7. **文章标题左对齐** — 在文章正上方，27px 粗体
8. **复制后不自动打开外部网站** — 复制按钮只复制内容；微信公众号后台只能通过显式链接手动打开
9. **复制按钮必须立即反馈** — `copyContent()` 不得等待图片上传或“更多文章”合成；这些准备工作只能提前或后台刷新复制层，点击后应立刻写剪贴板并显示复制结果
10. **表单只填数字** — 不要 px/em 后缀，颜色用 color picker
11. **不要 phone-frame 边框** — 文章直接在白底上流动
12. **原创 DOCX 作者行规则** — 首个 `作者：` 行是文章正文起点；它之前的编辑前置材料默认不显示在编辑器里
13. **Mote 作者特殊排版** — 默认 `behavior` 下，`Mote莫特` / `Mote 莫特` 触发参考来源上方固定署名图；原创作者行颜色为 `#396FBC` 且不斜体

14. **更多文章是可选规则模块** — `modules.more_articles.enabled=false` 时新文章不渲染底部更多文章；`modules.more_articles.default_cards` 保存该规则的新文章默认推荐列表。当前文章仍可在侧边栏临时替换、删除、排序和裁剪卡片。
15. **更多文章选图使用当前卡片媒体库** — `/api/fetch-article-meta` 必须返回 `image_urls`；“替换图片”候选顺序为当前卡片自定义封面、抓取头图、当前卡片正文媒体图，主文章图片只做兜底。旧卡片缺少 `image_urls` 时点击替换要按卡片链接补抓，不要显示“本文暂无图片”。
16. **URL 导入图片说明组不可拆散** — `图片 -> 图注 -> 图片来源` 必须连续归为图注；`图片来源：` / `图源：` 不能被当成正文或文章级来源
17. **URL 导入作者来源固定在提示前** — 文章级 `来源：` / `作者：` / `编辑：` 只渲染一次，位置固定在标准 `提示：` 页脚前
18. **DOCX 连续段落尊重空段语义** — 连续非空正文段落在 Word 段距较小时保持紧凑；只有原文中间有空段时，下一段前才恢复标准 `wx-gap`

19. **FoodTalks 网站规则与公众号物理隔离** — 渲染器（`public/js/templates/foodtalks-site.js`）、行为模板（`public/js/rule-behavior-foodtalks.js`）、页脚模块、署名颜色全套分离。公众号 Mote `#396FBC` 蓝色斜体不复用到网站；网站署名走 `#888888` 14px 斜体（2026-05-28 经 5 篇真实原生文章复核修正：`#7f7f7f` 是公众号搬运文痕迹色，非 FoodTalks 编辑部原生口径，依据见 `docs/superpowers/specs/2026-05-28-unified-rule-engine-design.md` §1.2）；公众号渲染器与网站渲染器之间不互相 import。修改任一侧规则不能波及另一侧——`public/js/templates/fbif.js` 在网站任务中必须字节级不动。

20. **页脚库（footer library）— 完整输出 + 区块面板交互**（2026-05-31 用户裁定完整输出；**2026-07-02 用户裁定交互统一，推翻旧"6 片段复选框"形态**——网站与公众号共用同一套区块交互语言：父开关「本文显示」+ 片段开关行 + 拖拽排序 + 图片片段"替换图片/素材库选图" + 预设按钮组）。网站文章底部页脚由本工具**完整输出**，6 个片段：`tip`(提示声明，走正文流) / `biz_coop` / `join_group` / `follow_account` / `logo` / `more_info`。固定资源定义在 `rule-behavior-foodtalks.js` 的 `FOOTER_SEGMENTS`；**渲染主路径是 `modules.bottom_blocks`（rich_html 区块），`fbif_footer`+`buildFooterHtml` 只是兼容回退，两路径字节一致由 `tests/site-footer-parity.test.mjs` golden 守**。面板单篇状态收敛为 bottom_blocks override 单合同（`public/js/site-footer-state.js`，localStorage key=`ft.footer.bottom_blocks.override::<账号>`；旧 `ft.footer.*` 键一次性迁移后删除；**服务端规则未就绪时读取零副作用**——空库窗口曾烧掉一次性迁移，勿回退）。图片片段的单篇换图存 `overrides[seg].fields`（保留 `{{asset#id}}` 引用形态，素材库更新仍生效；选回默认即删 field）。依据见 `WORKLOG-bottom-blocks-unify-site.md`。

21. **网站渠道默认走 ft-default 规则包** — site.html 通过 `getRulePackByPath(location.pathname)` 做渠道感知 fallback：`/ft/<任意 id>` 都会落到该渠道默认包 `ft-default`（templateId 是 `foodtalks-site`）。**禁止**裸用 `parsed.packId`——`/ft/default` 这种链接里的 `default` 在 registry 不存在，裸用会 fallback 到公众号 `fbif`，整套网站模板失效。

22. **所有账号（含网站渠道）共用同一 `bottom_blocks` 底部结构与交互语言，差异只在服务端默认与各渠道渲染器**（2026-06-03 用户裁定公众号内统一；**2026-07-02 扩展到网站渠道**：结构与交互全渠道统一、视觉口径按渠道隔离——红线 #19 的物理隔离保留在渲染层）。FBIF / WowFood / **FoodTalks系列** 都走统一 `mountBlockPanel`（`app.html` 已删 `isSeries` 守卫）；每账号在 `api/rule_defaults.go` 的 config 显式覆盖 `modules.bottom_blocks`（系列=`defaultSeriesBottomBlocksModule`：作者/BD名片/群二维码为 `image` 块满宽 banner，作者默认空，more_articles 默认隐藏可显隐），**不覆盖则经 `mergeRuleDefaults` 继承 FBIF 基线→错渲 FBIF 页脚**。系列页脚渲染走 `foodtalks-series.js` 的 `renderBottomFooter()`+`seriesBlockRenderers`（复用本模板 `imageSection`，**满宽 100% 系列口径，区别于 FBIF mp-blocks 的 661px**；红线#19：只 import 中性 `block-render`/`asset-library`，**不碰 `mp-blocks`**）。旧 `ftSeriesFooterSection`（侧栏作者/BD/群下拉）+ `public/js/ft-series-footer-ui.js`（2026-06-07 B 线已删除）+ `rule-behavior-foodtalks-series.js` 的 `buildSeriesFooterElems`（函数已删除，该文件仅保留 `classifySeries` 等系列分类能力）已退役，**勿当历史功能恢复**。字节保真由 `tests/series-blocks-fidelity.test.mjs` golden 守。拖拽排序统一用 vendor 的 SortableJS（`public/vendor/Sortable.min.js`，仅 app.html 引入；`block-panel.js`/`more-articles-ui.js` 拖完按 DOM 顺序回写，脏 id 过滤防卡片消失）。

23. **微信复制安全契约（保存草稿存活）** — 粘进公众号编辑器的 HTML 必须能活过「粘贴转存 + 保存草稿清洗」两道微信侧处理（2026-07-02 根治「保存草稿后更多文章消失」，根因与规则依据见 `WORKLOG-draft-save-regression.md`；2026-07-08 补齐 T0 立即保存安全谓词，见 `WORKLOG-mp-import-draft-survival-2026-07-08.md`）：① `<a linktype="image">` 的 `imgurl` 是微信服务端校验属性，粘贴时编辑器只转存 `<img src>`、从不改写 `imgurl`，`imgurl` 非微信图床（mmbiz/qpic/qlogo）时保存草稿会**整卡清除**——更多文章卡片 `imgurl` 必须用原始 mmbiz 封面（`more-articles.js wechatSafeCardImgurl`），无微信图床封面时不得伪装成 `linktype=image` 安全卡；② 公众号更多文章卡片的 `<img src>` 在 T0 必须已经可存活：微信图床 URL 或 base64 **位图**（png/jpg/gif/webp），OSS/外站 HTTP 不能作为立即保存草稿时唯一可见图，**SVG data: 不被转存、保存必丢**——空卡占位图必须是 PNG data:（`more-articles-placeholder.js`），勿改回 SVG；③ 更多文章 wrapper 锚点必须在 `more-articles.js`/`mp-blocks.js`/`foodtalks-series.js` 三处字节一致；④ 复制层 `_footerCopy` 只允许权威重建路径回写（`copy-session.js` 的 `footerCopyGate`），后台任务和旧 session 快照恢复都必须重新投影到 T0 安全状态，慢速完成的旧上传任务不得覆盖成品。以上由 `tests/wechat-draft-survival.test.mjs` 的微信草稿保存模拟器与 `tests/copy-session.test.mjs` 的版本门行为测试共同守护——改动复制层/卡片 HTML 前先跑它们。

24. **网站渠道表格 CMS 存活契约**（2026-07-06 真值审计 foodtalks.cn/news/62475 + TinyMCE 沙盒真实粘贴实测）— FoodTalks CMS 粘贴会剥光 table/td 的内联样式，只保留 HTML 属性（border/cellpadding/align/bgcolor/width）与内容层（strong、img 的属性和 style）。因此 site/series 表格必须走 `shared/table-renderer.js` 的 `outerBorder`+`cmsSurvival` 开关（td 的 align/valign/bgcolor/width 属性、table cellpadding、表头文字包 strong——与 inline style 双写，预览视觉零变化）；表格内图片按 parser 的 `wpx`（Word 显示宽度 EMU/9525）输出 `width` 属性+style 双写，保证"Word 里设多大、发布后就多大"。公众号 fbif 不传这些开关，输出字节不变（红线 #19）。守护：`tests/table-renderer-shared.test.mjs`、`tests/foodtalks-site-docx.test.mjs`；改表格输出前先读 `WORKLOG-table-cms-survival.md`。

25. **异步编辑必须 latest-wins（2026-07-10 对抗式审计）** — URL 导入、文件/区块导入、重渲染、更多文章元数据、复制准备、重试、规则保存和账号切换都必须在意图开始时取得版本门，并在每个 `await` 后、提交 DOM/全局状态/持久化前确认仍是最新意图；任一新入口必须主动废止会与之竞争的旧入口。账号切换是事务：规则、模板、配置和菜单只能一起提交，失败必须保持原账号，不得出现半切换。守护：`tests/editor-shell.test.mjs`、`tests/copy-session.test.mjs`、`tests/active-rule-selector.test.mjs`、`tests/account-switcher.test.mjs` 及浏览器回归。

26. **网站发布安全与恢复契约（2026-07-10 对抗式审计）** — FoodTalks 不可逆发布只能发生在持久化的人工 `approve`、审核时间、风险结论为 `pass|warning` 且审核版本匹配之后；`block`、缺失或未知风险一律 fail closed。FoodTalks `POST` 前必须先持久化创建意图，结果不确定时进入 reconciliation，不得盲重试制造重复稿；`source_key` 永久去重。飞书回调必须校验 verification token 与审核人白名单，外部文本进入卡片前必须清洗；审核通知走持久 outbox，审核回调只接受决策，发布异步执行并支持重启恢复。当前 JSON store 仅支持单进程，部署不得横向启动多个 API 实例；多实例前必须迁移到带事务/锁的存储。

27. **部署候选必须暂停写入并通过 readiness（2026-07-10 对抗式审计）** — 候选 API 以 `DEPLOYMENT_WRITES_PAUSED=1` 启动，发布/rules 写入口、恢复任务、通知 worker 与飞书长连接都暂停；公网 SHA、健康、鉴权及管理员 `/api/publish/readiness` 全通过后才接受 release。接受后先关闭自动回滚再解暂停；解暂停后外部副作用可能恢复，失败只能向前修复，不得自动切回旧版制造重复发布。FoodTalks 目标必须用 `FOODTALKS_EXPECTED_TARGET=test|production` 显式声明并与 API/前台/后台 URL 一致。

28. **微信公众号历史已发布全量口径（2026-07-15，最高优先级）** — `freepublish/batchget` 的发布对象数和从中拆出的文章记录数都不是已核验历史全量。完成多官方接口、多表、稳定标识关联以及分页/时间/字段覆盖审计前，只能写“未完成/未核验”；飞书历史同步保持关闭。

29. **已发布监控不得依赖草稿（2026-07-16）** — `freepublish` 最新页轮询必须独立执行，任何 draft 失败不得阻断；群发与 freepublish 是不同覆盖路径，禁止用单接口宣称覆盖群发。图集/小绿书继续 fail closed。

30. **FBIF 公众号监控是 P0 上线红线（2026-07-17，最高优先级）** — 任意改动必须通过无 paths 过滤的 `.github/workflows/p0-monitoring-contract.yml`。生产独立 systemd 看门狗每两分钟检查 layout/feed/official，连续两次异常飞书告警、恢复通知、每天 09:20 存活心跳；告警 reporter 不可用本身即事故。门禁必须 fail closed 覆盖当天文章→同步单缺口、45 分钟未到审核态、通知错误/缺卡、重复 source_key、轮询/补漏陈旧及持久队列积压；禁止删测、skip、降级或忽略当前故障来“修绿”。

## Server
- Go API server 同时提供静态文件（../public）和 API
- 图片上传需要 `WECHAT_UPLOAD_ENDPOINT` 环境变量
- 规则后台需要 `ADMIN_PASSWORD` 环境变量；生产值只能放 GitHub Secret 和本地忽略提交的 `.env`
- 规则预设默认写入 `data/rule-presets.json`；可用 `RULES_STORE_PATH` 覆盖
- 启动：`cd api && go run . -port 19090`

## Deploy
- 推送 `main` 触发 `.github/workflows/deploy.yml`
- 部署会把 `LIBREOFFICE_URL`、`LIBREOFFICE_TOKEN`、`CLOUDCONVERT_API_KEY`、`ADMIN_PASSWORD` 写入服务器 `/var/www/api/.env`
- `ADMIN_PASSWORD` 缺失时部署必须失败，不能让正式端进入“规则后台未配置密码”状态
- 部署工作流必须确保 `/`、`/index.html`、`/app.html`、`/version.json` 响应头为 `Cache-Control: no-cache`
- 修改正式后台密码时，更新 GitHub Secret 后重跑部署，并用 `/api/rules/admin/login` 做 200/401 双向验证

## Rule Hygiene
- Rules in this file must describe current behavior, not historical requests.
- Mutable product preferences need source/date/status before being treated as stable.
- `behavior` 是受限规则文档，不是任意 JS 执行入口；只用于作者/来源行、Mote 署名图、原创/转载页脚提示等轻量业务规则。
- Copy behavior must not navigate away from the editor unless the current user request explicitly asks for it.
- Copy speed is a product contract: do not hide background work behind the copy button or change the button text to upload/preparation states.
- URL 导入、DOCX 段距和复制链路的排版契约必须有测试保护；新增解析规则时至少跑 `cd api && go test ./...`、`npm test`，并用真实浏览器验证用户给出的样例文章。

## Agent skills

### Issue tracker

Issues are tracked in this repo's GitHub Issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Five canonical triage roles, each labeled with its default name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
