# FBIF 公众号排版 — Agent 规则

硬约束见 [索引](docs/INDEX.md)；违规不可发布。

## 平台与叫法（2026-10-14 用户裁定：「他不叫网站，叫FoodTalks」）

| 叫法 | 是什么 | 自动开关 |
| --- | --- | --- |
| 公众号 | FBIF食品饮料创新等，文章源头 | — |
| **FoodTalks** | 自有平台 foodtalks.cn；代码与旧红线里的「网站」「site」「CMS」都指它 | 「自动发布」：管理后台「发布链路 → 自动发布」，保持开 |
| 第三方平台 | 头条号、百家号，经云梭推送（代码里叫「外部分发/外发」） | 「自动外发」：`YUNSUO_TARGET_ACCOUNTS_JSON` 的 `auto`，2026-10-14 起暂停 |

对用户只说「FoodTalks」「第三方平台/头条号/百家号」，不说「网站」「外部平台」。用户的指令涉及哪个平台不清楚时先问，不要自己挑一个去关。

## 三系统边界（2026-08-06 用户裁定）

| 系统 | 职责 | 时效/存储 |
| --- | --- | --- |
| 本项目 | 只做加工：抓原文、排版、风险检查、飞书审核、分发 | 即时；JSON |
| `FoodTalks_Feed` | RSS/供应商资讯实时监控、分类、筛选 | 实时；独立 PostgreSQL |
| `fbif-wechat-article` | 微信 API 归档、阅读/粉丝指标 | T+1 09:00 CST；独立 SQLite；地址由上游管理 |

三者代码库/数据库/服务器独立，**只经 HTTP API 通信，不共享存储**；同稿由 `source_key` 幂等只建一单。

1. 两个机器入口只收链接（红线 33），五个正文旁路字段必须 400。
2. 不改两个监控端代码；发现问题只报告给其负责人。
3. 健康探针、告警、失败计数归监控端；本项目仅提供 `GET /api/publish/monitoring`，不主动探外部服务（红线 30）。

## 工作入口

- 匹配 skill 时先完整读取；禁伪造调用。
- UI 前读 `DESIGN.md`，不得偏离；QA 报不一致。
- 新功能遵守 [四层边界](docs/adr/2026-07-10-share-vs-isolate-boundary.md)：能力共用（差异参数化）、视觉按渠道隔离、规则按账号显式配置、交互统一。禁中立层写账号字面量、渲染器互相 import、默认继承 `bottom_blocks`、复制 helper、向 `app.html/site.html` 内联新逻辑。

## 红线

1. **预览全白**：预览页/正文区禁灰底（含 `#F7F7F7/#EDEDED`）。
2. **单层账号菜单**：位于左栏导入卡首行；`account_catalog.go` 是真相源，`rule_defaults.go` 登记 preset 后跑 `npm run gen:registry`，禁手改 registry。`mountAccountMenu` 挂 `#acctMenu`；渠道仅分组，同模板就地切、跨模板 reload，禁两层切换。2026-08-03 裁定：无 `.rail-head`/可见标题；`.rail-body` 灰底白卡；`.rail-foot` 紧贴工作区分隔线和右栏，放 `#userIdentity`、菜单上开；顶栏仅动作；输入有 `aria-label="导入内容"`。2026-08-05 裁定：`#progress` 在 `#dropZone` 动作行后，透明底、14px 横距、3px 条+状态/百分比；不确定态藏百分比；仅 `setShellProgress` 同步条宽/`aria-valuenow`/百分比，快阶段<95%；收栏同步收账号/身份，顶栏不复制。
3. **规则存储**：`/api/rules/*` 为准；localStorage 仅兼容/离线降级；禁新增 `/api/config/*` 平行入口。
4. **页面动作只在顶栏**：预览下禁动作条；身份栏不算动作；导入卡无提交按钮（见 32）。
5. **交互式排版无目录**：公众号/FoodTalks 渲染器禁随机 `mctoc_*`；自动发布例外见 39。
6. **微信式底栏**：白底；分隔线 max-width 680px 居中且不贯穿；图标 `#7F7F7F`。
7. **文章标题**：正文正上方左对齐，27px 粗体。
8. **复制不跳转**：只复制；公众号后台只由用户点显式链接打开。
9. **复制立即反馈且不缺图**：`copyContent()` 不得 await 上传/更多文章；准备工作提前或后台完成。2026-08-05 例外：复制层仍含 `/api/doc-cache/*`、`/assets/*` 或 `blob:` 且 `_uploadPromise && !_uploadDone` 时立即提示“图片准备中”，完成后自动恢复；`copyHTMLSource` 同理。`materializeLazyImages` 每图立即并发读取且不设并发上限。
10. **表单**：尺寸只填数字，不带 px/em；颜色用 color picker。
11. **无 phone-frame**：文章直接在白底流动。
12. **原创 DOCX 起点**：首个 `作者：` 行是正文起点，之前编辑材料默认隐藏。
13. **Mote 署名**：默认 `behavior` 中 `Mote莫特/Mote 莫特` 触发参考来源上方固定署名图；公众号原创作者行 `#396FBC`、不斜体。
14. **更多文章可选**：`enabled=false` 不渲染；`default_cards` 是新稿默认，单稿可换/删/排/裁。
15. **更多文章选图**：`fetch-article-meta` 返回 `image_urls`；依次选卡片封面、抓取头图、该卡正文媒体，主稿图仅兜底；旧卡缺媒体按链接补抓。
16. **图片说明不可拆**：`图片→图注→图片来源` 连续归图注；`图片来源/图源` 不得进正文或文章级来源。
17. **URL 署名（2026-09-01 用户裁定）**：来源/出处/出品、作者/撰文/撰稿/文、编辑/责编兼容中英文冒号与 `/|｜`；转载稿归一后在正文开头只渲染一次，原创稿仍在“提示：”前只渲染一次；来源、作者、编辑依次独立成行（有值才显示）。正文来源优先，否则用 `source_name`；禁将 `byline` 补成作者。署名公众号蓝、1.75 行高、常规字形、连续行无额外空行；传递链保留 `source_name`。
18. **DOCX 空段语义**：连续正文按 Word 小段距紧凑；仅原文空段使下一段恢复 `wx-gap`。
19. **渠道物理隔离**：FoodTalks 渲染器、behavior、页脚、署名与公众号分离，双方渲染器禁互相 import；网站署名 `#888`、14px、斜体；改网站时 `templates/fbif.js` 字节不动。
20. **网站页脚**：完整输出 `tip/biz_coop/join_group/follow_account/logo/more_info`；主路径 `bottom_blocks.rich_html`，`fbif_footer+buildFooterHtml` 仅等字节回退。单稿只用 `site-footer-state.js` override；规则未就绪禁本地写；换图写 `overrides[seg].fields` 并保留 `{{asset#id}}`。
21. **网站默认包**：`site.html` 用 `getRulePackByPath(location.pathname)`；任意 `/ft/<id>` fallback 到 `ft-default`（`foodtalks-site`），禁裸用 `parsed.packId` 回落公众号。
22. **全账号统一区块交互**：统一 `mountBlockPanel`、显式 `bottom_blocks`、`Sortable.min.js`；网站系列仅复用中性 `block-render/asset-library`，禁 `mp-blocks`。**当前 `site.html` 仍内联原生 DnD，违反本条，暂不可发布。**
23. **微信草稿 T0 存活**：更多文章 `linktype=image` 的 `imgurl` 必须微信图床；卡片 `<img src>` 只能微信图床或 base64 位图，OSS/外站 HTTP 不得是唯一安全图，禁 SVG data，空占位用 PNG data。三渲染器 wrapper 锚点一致；`_footerCopy` 仅由 `footerCopyGate` 重建，旧快照/后台任务不得覆盖新会话。权威契约：[wechat-draft-survival-copy](openspec/specs/wechat-draft-survival-copy/spec.md)。
24. **CMS 表格存活**：site/series 用共享 `table-renderer.js` 的 `outerBorder+cmsSurvival`，align/valign/bgcolor/width、cellpadding、表头 strong 与样式双写；图片按 `wpx` 双写 width；fbif 不启用且字节不动。见 [契约](docs/worklogs/WORKLOG-table-cms-survival.md)。
25. **异步 latest-wins（2026-07-10 审计）**：各类导入、重渲染、更多文章元数据、复制准备、重试、规则保存、账号切换均先取版本门；每个 await 后及提交 DOM/全局/持久化前复核；新入口废止竞争旧入口。账号切换须规则/模板/配置/菜单同提交，失败保留原账号。
26. **发布安全与恢复（2026-07-10 审计；2026-08-14 手工优先裁定；2026-08-24 自动发布开关）**：公开须持久 approve（人工，或管理后台「发布链路 → 自动发布」打开后的系统 `system:auto_publish`）+ 时间、风险 `pass|warning`、版本匹配；否则 fail closed。开关默认关；只作用于新同步稿第一次进审核（`auto_publish_eligible`：从未发卡、从未审核），已在待审核的、回源重排或重新暂存的老稿一律不自动公开（2026-10-08 #19 事故）。阻断级、同题已发布仍停待审核，不自动翻开。群卡保留，已发布卡可下线。外调前落 intent；不确定结果进 reconciliation，禁盲重试；`source_key` 永久去重。飞书验 token/审核人、清洗外部文本、持久 outbox；回调只落决策，发布异步可恢复。JSON store 仅单实例。CMS 手工稿为准，库存禁覆盖；卡片前标题查重标红，`stagePublicize` 落 intent 前复查，命中/查询失败 fail closed；仅修流水线未人工改稿。**`stageRepublish` 尚无同题硬门，暂不可发布。**
27. **部署事务（2026-07-10 审计）**：候选以 `DEPLOYMENT_WRITES_PAUSED=1` 启动，暂停发布/rules 写入、恢复任务、通知 worker、飞书长连；公网 SHA/健康/鉴权/管理员 readiness 全过才 release。接受后先停自动回滚再解暂停；解暂停后失败只向前修。`FOODTALKS_EXPECTED_TARGET=test|production` 必须与 API/前台/后台一致。
28. **微信历史全量口径（2026-07-15，最高优先级）**：`273/311`、单接口/表/素材库数均非总量。完成多响应/多表按 msgid/msg_data_id/序号/article_id/URL/日期去重及分页/时间/字段审计前，只能写“未完成/未核验”；原始响应/字段全落库，飞书历史同步关闭。
29. **已发布监控独立（2026-07-16）**：08:30–18:30 `freepublish` 最新页轮询不得被 draft/素材失败阻断；“群发”另走持久幂等路径。启用统一服务器配置前审计回调、自动回复、菜单；小绿书/图集/未知类型 fail closed。
30. **P0 监控（2026-07-17；2026-08-06 归属修订）**：任意改动须通过无 paths 过滤的 `.github/workflows/p0-monitoring-contract.yml`，禁删测/skip/降级。`GET /api/publish/monitoring` 必须如实涵盖同步缺口、45 分钟未审核、通知/卡片错误、重复 source_key、轮询陈旧、积压、FoodTalks 凭证失效（`foodtalks_credential_invalid`，最近一次鉴权结论为 401 即告警，不等积压；2026-09-28）；第三方平台失败按 lane 失败时间算近期（云梭补查会刷新任务时间，2026-10-14）；processing 或通知 pending/sending、卡缺失/失败均 `ready=false`。外检 watchdog 已迁 `FoodTalks_Feed`；本仓库只 `cleanup_legacy_watchdog` 且回滚不恢复，禁重建外检。
31. **飞书文档异步导入（2026-08-05 恢复上线）**：交互导入只走 export jobs + `after/wait_ms` long-poll，逐阶段报真实 done/total；服务端一次 `base64_images` 镜像 OSS，浏览器禁逐图回源 doc-cache；250ms 递增退避；预解码+离屏 staging 整帧提交，导入时保留旧稿。禁退回同步入口，禁删 `feishu_export_jobs.go/doc_image_mirror.go/preview-dom.js/preview-image-loader.js/feishu-doc-ready.js/import-progress.js`。冲突逐文件裁决并核对 `git diff --diff-filter=D <父> <merge>`，禁整树取一侧。
32. **粘贴即排版（2026-08-06 用户裁定）**：粘贴后按输入框最终全文立即提交，URL/飞书链接/正文/Markdown 同等；追加粘贴整稿重排；仅 `@` 搜索不自动。禁恢复 `#importSubmitBtn/.import-submit-button`；动作行只留“粘贴后自动排版”；手输以 Cmd/Ctrl+Enter 提交。
33. **机器发布只收链接（2026-08-06 用户裁定；2026-08-18 补阅读原文）**：`site-sync/official-sync` 只收 `url` 并走网页同一抓取解析；`content_html/title/source_name/author/cover_url` 任一出现均 400；body≤64KB，共用 `rejectedPublishContentFields`。可选 `content_source_url`/`original_source_url` 只补出处，不替代抓取。official 固定 `ordinary_confirmed`。禁恢复任何 HTTP 正文旁路。鉴权头 `X-Publish-Sync-Token`，双方统一 `PUBLISH_SYNC_SERVICE_TOKEN`；旧 `X-Admin-Password/LAYOUT_SYNC_TOKEN/LAYOUT_ADMIN_PASSWORD` 废弃。例外：Bearer `RENDER_TOKEN` 的 `/api/html-to-blocks` 仅转换、不建任务。
34. **审核卡可识别文章（2026-08-06 用户裁定）**：待审/已发/驳回/异常均有 hero（封面、粗标题、标签、摘要、预览/原文链接）；终态禁仅 info。封面复用 `ReviewCoverImgKey`，旧稿无值则无图单列；回调须同步返回新卡，禁现场传图。`ReviewCardMsgIDs` 已存在时，当前群无映射必须发新卡，禁回退 legacy id；群成员回调只认当前 chat，历史群仅静态白名单。样式变更后用管理员或 `PUBLISH_ADMIN_SERVICE_TOKEN` 调 `card-refresh` 原位更新；只换样式，不重跑检查。聚合卡每篇的按钮行（2026-10-13 用户裁定）：最小号按钮，发布/重新发布 · 原文（公众号链接，不叫「公众号原文」）· 重新同步至FoodTalks · 下线（最右）；不用折叠按钮（飞书折叠按钮不能调小，与最小号按钮不等高）；重新同步结果在文章块底部单独一行、横跨整块，不在图片下也不在按钮行里。审核卡过了飞书 14 天可更新期限（230031）时，发布生命周期通知与外发卡片一样记 `expired`：不重试、不进死信、不补发新卡（2026-10-14：此前 30 条进死信，监控一直误报）。
35. **风险分层（2026-08-08 用户裁定）**：首屏最多 3 条显式 `confirmed` 实质项；转载授权/system 全部折叠待核。口径只在 `publishRiskItemConfirmed/splitPublishRiskItems`；未知 certainty→`unverified`。无 confirmed：pass 无项才“检查通过”，有待核说“未发现可证实的实质性错误”，block/未知分别说“风险阻断/检查未完成”且仅二者有状态标签。待核数只在折叠标题；待审 info compact。两层共用官方无序列表，项内 `<br>`。prompt factual 优先，headline≤100 字并截断/回退。
36. **审核群只发卡片（2026-08-09 用户裁定）**：禁运维播报；`cleanup_group_broadcast` 只清理且回滚不恢复。监控仍由外部读取 `GET /api/publish/monitoring`。例外（2026-10-12 用户裁定：「在卡片里@人可能收不到……在这个卡片消息的话题下创建一个对话，@对应的人」）：外发待修改提醒以富文本回复进审核卡的话题，每轮只发开头 @ 一条与改好收尾一条；校对提醒（红线 44）同样只回复进卡片话题，同一组错误只发一条。
37. **外部平台只发已审核原创（2026-08-11 裁定；2026-08-13 修订；2026-10-10 头条自动；2026-10-12 百家号自动、各平台分开）**：FoodTalks 仍收原创/转载。外发入口仅在已公开、审核版本有效且原创时显示；目标账号服务端固定，callback 禁带账号；自有来源/作者即原创，不等风险/AI；转载标记隐藏；点击再确认并记 actor。**各平台分开（2026-10-12 用户裁定：「他们这个发布不应该是分开的吗」）**：每个账号一条 lane（`external_distribution_lanes`，各自冻结 payload+intent、`content_pk`、状态、链接），某平台失败不挡其他平台；旧单轮字段读盘即迁移。**卡片**（2026-10-12 用户裁定：「分发区域可以做成一个专门的区域，不用事先隐藏起来……每个分发平台对应一张卡片」；同日「上半部分这三个是不变的……一般只有一篇是原创……为两篇预留足够的空间」「同一篇文章可能会同时发到……五六家平台……做一个循环容器组件」）：审核卡最底下常显「分发到其他平台」区（区标题上方不画分割线，只多留间距；2026-10-13 用户：「这根线可以不要」），只有原创有；每篇原创一个与上方文章块同款的细框块（灰框、16px 圆角，点空白处在侧边栏打开改稿页），多篇时块首行是 36px 方形封面 +「#编号 标题」；块内每个平台一行（循环，加平台只多一行）：图标、名字、文章链接（平台标题与本文不同才写出）、靠右的纯色状态字或「同步至X/重新同步至X」按钮，下一行灰字原因（受理前失败写红字「上次同步失败」）；按钮只推这一个平台（callback 带平台位置 `slot`，不带账号 id），已受理的不碰。**推送前检查与改稿**（同日用户：「标题超限……应该支持在群里 @ 对应的人」「能否直接在卡片里改好？……提示清楚要把什么改成什么」）：云梭受理后改不了，所以每个平台推之前按 profile 查标题/摘要字数与封面尺寸，不过就记 `external_distribution_drafts`（不建 lane、不占名额，自动外发跳过直到文章或人改的值变了），这个平台的块变成根级 form：逐项写「标题超出 N 字：X最多 M 字，现在 K 字」、现在的值、预填建议（先标点截短，再 AI 缩写，只走 `ai_models.go`）的输入框、从正文大图里点选封面，「保存并同步至X」服务端重查、过了就推；只改推出去的版本，公众号/网站不动。卡片里做不了的（上传封面）走侧边栏改稿页 `/distribute.html`（applink `sidebar-semi`，飞书登录 + 每篇 HMAC 签名或规则管理员），与卡片表单共用 `submitExternalDraftEdit`；封面「选一张或上传」（同日用户：「就是“选一张封面”或者“上传”」「我可以粘贴、上传文件或者拖动文件进去」「传上去之后直接绑定、自动同步过去了」）：上传格可点选、拖入（整页接住，落在哪个平台给哪个）、粘贴，传上去即转存 OSS 并换成这个平台的封面，页面里已改好且合规的标题/摘要随图一起提交，问题都清了就推，文字没过只换封面。不做「把图发到话题」（同日用户否决）。**提醒**（同日用户：「在这个卡片消息的话题下创建一个对话，@对应的人，告诉他下一步要做什么……给他选项、给他选择」）：管理后台「发布链路 → 分发负责人」选审核群成员（按推送应用存 open_id，换应用要重选）；有待修改时以富文本（post）回复进审核卡的话题、真 @ 负责人：逐项写超了多少与平台上限，再给可选做法（用卡片里预填的建议或自己改到 N 字内；点选正文大图或上传一张，传上去就换上），最后写点哪个按钮；同一轮只发这一条，进度看卡片，全部改好收尾一句「都改好了，正在同步至X」；没设负责人只记不发；失败计入 monitoring。**自动外发**（2026-10-14 用户裁定暂停：「我让你停的是头条号和百家号这些第三方平台」，排版成熟前不自动推；两个账号已去掉 `auto`、存量补发待办已作废，恢复须用户同意；FoodTalks 自动发布不受影响）：`YUNSUO_TARGET_ACCOUNTS_JSON` 中 `"auto":true` 的账号在落为已发布的同一次写入里记 `external_distribution_auto_pending`，由 `runExternalAutoDistribution` 只推还没有 lane 的 auto 账号（actor `system:auto_distribute`）；有 lane 的（含失败、云梭在重试、人工已发）永不自动再推；名额按平台各算（日上限取平台规则，头条号 5 篇，人工也计入），新稿优先；存量用 `POST /api/publish/external-distribution/auto-backfill`（先 `dry_run`）补发，每平台每天≤3 篇、9–20 点、间隔≥1h；去掉 `auto` 即关闭并作废待办。**人工已发**：2026-03～08 头条号是人工发的，补发前必须先用 `POST /api/publish/jobs/{id}/external-distribution/manual` 登记（`url` 只认平台域名，或 `lookup` 自动找；先 `dry_run`），否则会重复。**文章链接**：推送成功后头条号经头条搜索+TikHub 核对（作者为自家号、标题对上、发文时间在推送之后）回填，退避 6 次、7 天内；百家号搜索有验证码不绕过，只能登记；云梭状态接口不返回文章链接，已发布还没链接时卡片与侧边栏给「去X后台查看」（平台内容管理页，profile `ManageURL`；2026-10-13 用户：「我也查看不了发布情况」）。云梭状态机（每个 lane）：先冻结 payload+intent；无 `content_pk` 只人工对账，禁盲发；有 id 只轮询（15 分钟轮询超时转对账、及失败的，45 天内每 30 分钟补查云梭迟到结果，只查不推；超时时云梭回执仍是 status=0「待发布」的，卡片与侧边栏写「排队中」、说明每 30 分钟自动查；status=4（平台弹验证码、要在云梭插件里养号）写「要养号」；查询被拒/没回执/没单号/过了补查期才写「待核对」叫人去云梭后台，2026-10-14 用户问 #282「这个待核对是什么意思」）。云梭开放接口只有推送与查状态，**没有修改、取消、撤回接口**（[官方文档](https://www.yunsuofabu.com/products.html)，2026-10-14 核对），推出去的只能在云梭控制台处理；仅明确受理前失败可重试。独立 worker，持久 card outbox 原位更新（审核卡过了飞书 14 天可更新期限即 230031 时记 `expired`：不重试、不进死信、不补发新卡，补发旧稿的状态只在 lane 上）；两套 outbox 串行且发前重读，callback 只 toast；测试仅 fake/本地验证。
38. **网站/云梭一律切微信导流尾（2026-08-13 用户裁定）**：与原创无关；`isRepost` 只影响来源类型/提示。`stageRender` 固定切尾；云梭 BlocksJSON 在 Go 切尾并经 `publishHTMLWechatTrailerLeak` fail closed；泄漏标记不得误伤合法网站页脚。已公开稿用 `repair-wechat-trailer` 覆盖，不重审。
39. **网站图宽/目录/标签/分类（2026-08-14 用户裁定，62765 金标准；2026-08-17 按排版规范修订并升版 v19）**：长图比≥1.7 固定 400px，竖图宽≥300，二维码自然宽阈值 300（preset v19 + rule-presets.js/render.mjs 离线 fallback + fixture 均已同值）。目录按排版规范（2026-08-17 用户裁定：**所有文章一视同仁，不分原创/转载**，历史转载没目录是人工漏掉；2026-08-24：目录只收标题1）：正文非空白字符 ≥3000 且 h2+h3 合计 ≥5 时在发布组装层写哈希确定性 `mctoc_*` 和 CMS toc（条目只收标题1 / h2；标题2 是「1、2、3」或「（一）」→ h3，不进目录；**Mote 文章例外（2026-09-29 用户裁定，63116）：标题2 以 level=2 进目录**）；渲染器仍关闭 mctoc。所有 `job.Tags` 可解析，Mote/系列优先；最多 3 个，仅 `tag_type_id=6` 规范化等值匹配，禁创建（系列标签例外，见下）；系列标签链（2026-10-08 用户裁定：通配但简单）：段首「这是我/这篇是/本文为 … `XX系列`（的）第N篇」声明段（开头、文末都算，后面可接别的话，任意系列名，含「“男人味”系列」这种引号只包前半截的写法）把 `XX系列` 链到系列标签 `/news/tag/{tagId}`，原文已链公众号合集或旧链接时整条替换；正文中途提到的「XX系列第N篇」不加链；正文里链到公众号合集（appmsgalbum）且锚文字是「XX系列」的，有同名标签就把 href 换成标签页（2026-10-08）；系列标签按 `siteSeriesTags` 依次找：同名 → 本文已挂的唯一「…系列」标签（编辑起名常与文中不同，61483「北海道伴手礼系列」实挂「日本伴手礼系列」）→ 名字以系列名结尾的唯一标签 → 都没有时仅 Mote 文段首声明按声明名新建并挂到文章标签（Mote 之后、总数仍≤3；2026-10-08 用户裁定：新系列不靠人判断），合集锚文字/正文提到/查询出错不新建；存量修复只链已有标签。存量（流水线稿与编辑手工稿）用 `POST /api/publish/news/{newsId}/repair-links`（`?dry_run=1` 只报告）修。栏目/活动标签（首发栏目名、`FBIF\d{4}`、会后报告、参展企业、Wow 奖）`publishSiteTagBlocked` 双侧拦截（发布解析 + 词表注入 prompt 前），品类词/品牌名不拦。原创分类 `FOODTALKS_CATEGORY_IDS=52,58`，转载 `FOODTALKS_REPOST_CATEGORY_IDS=53`。
40. **网站转载署名与出处（2026-08-17 运营反馈批次，62813 实证）**：① 出处判定 AI 优先，`publishDeclaredSource` 确定性兜底——头部前 12 个文本块与尾部 35% 区域的「来源/出处｜：X」「本文来自/为 X」声明抽机构名，非自家即转载，防天下网商类误判原创进独家专栏。①b 转载稿开头署名一律「本文为{来源}原创，作者：…，转载请联系出处。」：开头只有「作者：」行也补来源（与文末转载声明同源，跳过「来源：XX报告《》」这类数据出处），紧跟的编辑/翻译/编译/采访行并入（2026-10-14：近一月自动发布的转载稿 32 篇开头只有作者行，24 篇被编辑在 FoodTalks 后台手改成此口径，新规则 24/24 逐字一致）；已发布的旧署名稿用 `POST /api/publish/jobs/{id}/source-resync`（先 `?dry_run=1`；与卡片「重新同步至FoodTalks」同一路径，FoodTalks 后台改过正文就停）补。② 头部前 5 元素与尾部区域的裸署名行（来源/作者/编辑/文…，冒号或竖线分隔）：开头已有网站署名则删除文末公众号口径来源/作者（2026-08-18，避免与开头重复）；否则降级 `footer-note` 14px 灰斜体。署名前缀同时进 `caption_lead_regex` 防误升 h2。③ 同文本 ≤16 字无序号标题全篇出现 ≥2 次判账号装饰行整体删除（天下网商×3 案例，成品口径）。④ 转载声明《标题》超链接优先公众号「阅读原文」地址（`OriginalSourceURL`，fetch 阶段抓 `msg_source_url`）；自家号转发且阅读原文为空时先查 Feed 订阅列表，Feed 没命中（含已订该号但按标题对不上）再 TikHub 广域搜（搜一搜）：标题+出处都对上直接用（Feed/网页检索找到的公众号原文再抓页取真标题，#114 多了「笑死！」）；FBIF 改过标题的，作者是来源号的候选抓回原文逐段比对正文（探针过六成）才用，并把原文真标题记为 `OriginalSourceTitle`，声明《》里用它（2026-10-14：#255/#274/#275/#277 编辑都手改成原标题，新逻辑 4/4 逐字一致；标题搜不到再按正文第一句搜；已发布稿用 `repair-links` 只换声明这一句，编辑改过这句的不动）；禁红狐/按号拉列表；核验不过则不链、标题沿用本稿，禁止回退自家转载页。外号原文或自家原创仍用提交链接。⑤ 访谈（规范(6)，62807 实证）：判据=两个及以上「角色：」前缀各现 ≥3 次；**提问方** FoodTalks 蓝+粗、回答方默认色+粗——提问方按 书名号>问号率（差≥0.25）>首现 确定性打分，不再按「书名号才上蓝」；提问方前缀独立成段时与下段提问内容合并（提问方+提问内容间不空行）。
41. **公司库自动关联（2026-08-17 用户裁定，规范 step2-(5)）**：候选=AI 标签内容词；`GET /company/companies/search`（前台公开接口，字段 id/cnName/shortCnName/urlCode；文档的 `/company/admin/companies/page` 对服务账号 Access denied 勿用）检索后与全称/官方简称表**规范化等值**匹配，多条优先 cnName 含「中国」，仍歧义放弃。正文首次出现处注入 `mc-company-link`（形态对齐小工具箱成品 48190：data-id/name/code、新窗口；href=`/company/<urlCode>`，数字 id 打不开公司页），标题/图注/既有链接内禁注入，每公司限 1 处、每篇 ≤3 家；`relatedCompanyIds` 同步写 id。检索失败只降级不 fail。禁部分包含匹配、禁自建公司。
42. **Mote 系列链接改站内（2026-08-24 用户裁定，金标准 62285 Tazo→/news/62236）**：仅作者是 Mote/莫特的网站发布组装。正文里指向 `mp.weixin.qq.com` 文章的超链接，换成已发布 FoodTalks `/news/{id}`（前台域名走 `FOODTALKS_FRONT_URL`）；锚文字与 `target`/`rel` 不动。先用任务库 `source_key`/`aliases`/`source_url` 对已发布单，找不到再取公众号标题并 `SearchNewsByTitle` 等值匹配；只认已发布、不是本稿：同题唯一直接用；网站重复稿（同题多篇）或编辑往标题里加了字（公众号标题是站内标题的子序列，如 Tea Forté）时，取发布时间离公众号发文最近且 7 天内的一篇，不知道发文时间就放弃。标题与发文时间先直连取，读不到再走 TikHub `wechat_mp/v2/fetch_article_detail`（0.01$/次，进程内按链接缓存）——不带 chksm 的 `__biz/mid/idx/sn` 长链微信只回验证页（2026-10-08 实测，63149）。找不到或查询失败保留原链，不 fail job。存量 Mote 稿（含编辑手工稿，按网站作者或任务库判定 Mote）同样用 `repair-links` 修。禁改 JS 渲染器；交互复制仍可保留公众号链。
43. **多平台分发版式（2026-10-12 用户裁定，金标准=头条号「FBIF食品饮料创新」2026-03～08 人工稿 16 篇）**：云梭外发是独立渠道规则，不沿用网站样式，分两层（2026-10-12 用户裁定「底层规则……再往下每个平台自己独特的规则」）：通用层 `api/yunsuo_distribution_rules.go`，平台层 `api/yunsuo_platform_profiles.go`（小标题标签、图注形式、标题字数与云梭标题档、封面下限、日上限、文章域名与链接查找；账号可用 `platform` 显式指定，不填按名字认头条/百家），加平台只加 profile、不改通用层。通用层：署名按原文分行、不补句号；文末「本文为{来源}原创，作者：X，转载请联系授权。」；图注用平台原生 `pgc-img-caption`，说明与图片来源以「；」并成一条（≤50 字）；标题1→`h1.pgc-h-forward-slash`，标题2→加粗段；段间无空行，正文无链接、无裸网址；去作者名片（参考来源上方/之后的无图注图，无参考来源时文末无图注宽条图）；封面=正文第一张图并从正文拿掉（云梭会把 cover 插到正文顶部）；第一张图不到平台封面下限（头条号 452×352）时留在正文、退回公众号封面；还差一点（放大 ≤1.5 倍即达标，如 #183 的 780×332）就自动放大一份转存作这个平台的封面（`publish_external_cover.go`，2026-10-12 用户：「直接把我们原先的头条号封面改成对应的那个」），差得多才记为待修改、在卡片里选正文大图或侧边栏上传（#183 受理后失败只能人工处理）；短标题在标点处断开。头条号标题最多 30 字、标点空格都算（2026-10-13 用户裁定），用云梭「标题」档（15–40）原文，线上头条稿全是这一档，不是 8–20 字短标题；平台上限改了，已记待修改的稿按新上限自动重查。
44. **确定错误的校对提醒（2026-10-13 用户裁定：「置信度几乎100%的错误，比如还有什么错别字之类的……在卡片的话题下，@对应的人来查看的。但是不耽误同步。但是需要提醒确认。注意，模棱两可的，这些可以不提醒」；同日「你可以先@郑梽煌」）**：候选=风险检查里 `certainty=confirmed` 的 factual/naming/number/integrity 条目；审核卡发出后用 Pro 档逐条复核（`editorial/proofread_prompt.md`，只走 `ai_models.go`），只放行能写出唯一改法「X 应为 Y」的确定错误（错别字、多字漏字、专有名称写错且正文别处写对、常识年份等），补年份/改绝对日期/建议核实、两处对不上但判断不了哪处对、要补假设才算得出的、只是标点、段落重复/导流话术、引述原话一律不提醒；服务端再校验 X 逐字在正文和原句里且不跨行、Y≠X、不只差标点、是小改动（编辑距离≤2 或≤X 长度的三分之一；2026-10-13 生产 dry_run 43 篇：真错都是改一两个字，整块替换的都是模型在猜）、每篇≤5 处。有确定错误就以富文本回复进审核卡话题、真 @ 管理后台「发布链路 → 校对负责人」（与分发负责人分开存，按推送应用存 open_id）：每处一行粗体「X」应为「Y」、下一行原句，末句「不影响同步和发布，确认后改正即可。」；不改正文、风险结论与审核卡，不挡审核/发布/外发；同一组错误只提醒一次，重跑风险检查后错误变了才再提醒；驳回/放弃/重复稿不提醒，等审核卡发出前挂着不算积压；没设负责人只记不发；持久 outbox，失败退避重试、死信计入 monitoring（`proofread_reminder`，`api/publish_proofread.go`）。只在风险检查写入时自动触发，存量稿用 `POST /api/publish/jobs/{id}/proofread-reminder`（先 `?dry_run=1`）。

## 守护索引

- 2/32：`top-toolbar/account-switcher/auth-shell/import-progress-component/e2e-progress-component/editor-shell/e2e-paste-autoformat`。
- 9/23/25：`copy-behavior/uploader/wechat-draft-survival/copy-session/active-rule-selector`。
- 19–24：`site-footer-parity/series-blocks-fidelity/foodtalks-site-config-flow/table-renderer-shared/foodtalks-site-docx`。
- 26：`api/publish_duplicate_guard_test.go`、`api/publish_auto_publish_test.go`；27/36：`tests/deploy-transaction.test.mjs`。
- 30：`.github/workflows/p0-monitoring-contract.yml`。
- 31：`e2e-preview-speed/e2e-feishu-import-progress/preview-dom/preview-image-loader/feishu-doc-ready/import-progress`、`api/feishu_export_jobs_test.go`。
- 33：`api/publish_http_test.go` 中 `TestPublishOfficialSyncFetchesFromURL` 及三个 reject 测试。
- 34：`api/feishu_review_card_test.go`、`api/feishu_review_card_icons_test.go`（聚合卡：编辑选的方形封面 + 四个独立单行块、按钮 Lucide/微信图标、已分发稿仍可重新同步、同步结果单独一行）；35：`api/publish_risk_layers_test.go`；30 第三方失败近期口径：`TestPublishMonitoringExternalFailureRecencyUsesLaneFailureTime`；过期卡与补同步：`api/publish_source_resync_test.go`（`TestPublishNotificationExpiredCardIsTerminal`、`TestSourceResyncAdminEndpoint`）。
- 37：`api/publish_external_{distribution,auto,lanes,links}_test.go`（含 `TestExternalCardExpiredIsTerminal`、`TestExternalQueuedAtYunsuoShowsQueuedNotReconcile`）、`api/publish_external_drafts_test.go`（推送前检查、卡片改稿、话题提醒 `TestExternalReminder*`、侧边栏 API、负责人）（含侧边栏上传带标题 `TestExternalSidebarUploadCarriesTypedTitle`、封面自动放大 `TestExternalCoverAutoUpscalesNearMiss`）、`api/feishu_review_card_test.go`（`TestReviewCardDistributionSectionShowsPlatformBlocks`、`TestReviewCardDistributionFixForm`）、`tests/{distribute-page,admin-delivery}.test.mjs`；38：`foodtalks-site-behavior`、`api/{wechat_trailer,yunsuo,publish_trailer_repair}_test.go`；39：`tests/image-width.test.mjs`、`api/publish_site_enhance_test.go`。
- 40：`tests/{foodtalks-site-docx,foodtalks-site-behavior,heading-detect,site-interview}.test.mjs`、`api/publish_site_enhance_test.go`（declared-source/provenance/render-source 用例）、`api/wechat_blocks_test.go`（meta 三值）、`api/publish_original_url.go`、`api/publish_tikhub_worklist.go`（改标题转载找原文：`TestDefaultLookupWechatFindsRetitledOriginalByBody`、`TestDefaultLookupWechatFallsBackToLeadSentence`、`TestResolveRepostOriginalSearchesWechatEvenWhenFeedHasSource`；声明用原文标题：`tests/render-service-site.test.mjs`）。
- 41：`api/publish_company_links_test.go`、`api/company_match_smoke_test.go`（2026-08-17 生产检索快照）。
- 42：`api/publish_mote_series_links_test.go`；40④ 存量声明修复：`TestRepairNewsLinksRepostNoticeOriginalTitle`；39/42 存量修复与 TikHub 取标题：`api/publish_series_tag_repair_test.go`。
- 43：`api/yunsuo_test.go`（`TestYunsuoDistribution*`、`TestYunsuoShortTitleBreaksAtPunctuation`、`TestYunsuoCoverRespectsToutiaoMinimum`）、`api/publish_external_distribution_test.go`（`TestExternalDistributionLanesFailIndependently`）。
- 44：`api/publish_proofread_test.go`（`TestProofreadReminder*`、`TestParsePublishProofreadKeepsOnlyDefiniteGroundedFixes`、`TestProofreadOwnersSavedSeparately`）、`tests/admin-delivery.test.mjs`。

## 运行/规则卫生

- 只有 `main` 一个分支（2026-10-08 用户裁定）：禁止建分支/PR 分支，改动本地跑完 Go/Node 全测后直接提交推 `main`（即触发 deploy）。未合并的旧工作已存为 tag `archive/codex-unify-punctuation`、`archive/su-design-migration`。
- 运行见 [README](README.md)。生产缺 `RULE_SUPER_ADMIN_OPEN_IDS` 必须启动失败；推 `main` 触发 deploy；四个入口（`/`、index/app/version）须 `no-cache`；改权限后验 `/api/me`。
- 只写当前行为；偏好标来源/日期/状态。`behavior` 仅限署名/来源、Mote 图、原创/转载页脚，禁任意 JS。
- 复制禁导航；后台工作不得塞进按钮或改成准备态。
- URL/DOCX/复制须有测试；新解析规则跑 Go/Node 全测并以真实浏览器验样例。
- 模型分工（2026-10-07 用户裁定）：看图与翻译等批量任务用 `mimo-v2.6-flash`，复杂/高价值任务（风险检查）用 `mimo-v2.6-pro`；唯一入口 `api/ai_models.go` 选档，禁业务文件写模型名或另接供应商。
- 网站封面/摘要与公众号保持一致（2026-10-07 用户裁定）：封面用公众号原封面、摘要用公众号摘要（`msg_desc`），AI 只在原文缺值时兜底；兜底候选只含原文配图，网站页脚名片/二维码永不作封面（#263）。存量已发布稿用 `POST /api/publish/jobs/{id}/sync-wechat-meta` 对齐，只替换没人手改过的字段。
