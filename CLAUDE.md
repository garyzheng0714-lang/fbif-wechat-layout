# FBIF 公众号排版 — Agent 规则

硬约束见 [索引](docs/INDEX.md)；违规不可发布。

> 以本文件为准；「细则 §X」指 [docs/redlines.md](docs/redlines.md) 的同名小节（完整做法、守护测试），用户原话与事故经过在 [docs/adr/2026-10-20-rulings-backstory.md](docs/adr/2026-10-20-rulings-backstory.md)。

## 平台与叫法（2026-10-14 用户裁定）

| 叫法 | 是什么 | 自动开关 |
| --- | --- | --- |
| 公众号 | FBIF食品饮料创新等，文章源头 | — |
| **FoodTalks** | 自有平台 foodtalks.cn；代码与旧红线里的「网站」「site」「CMS」都指它 | 「自动发布」：管理后台「发布链路 → 自动发布」，保持开 |
| 第三方平台 | 头条号、百家号，经自己的分发服务存草稿再公开发布（代码里叫「外部分发/外发」，对用户说「同步」） | 「自动外发」：多维表格「平台账号」的「自动外发」列，2026-10-14 起暂停，2026-10-30 用户同意恢复：勾上的号，原创一上 FoodTalks 就自动同步、公开发布（勾哪个号由人在表里勾） |

对用户只说「FoodTalks」「第三方平台/头条号/百家号」，不说「网站」「外部平台」。用户的指令涉及哪个平台不清楚时先问，不要自己挑一个去关。

## 两条线：按载体分（2026-10-23 用户裁定；2026-10-27 贴图线归贴图发布项目）

|  | 文章 | 贴图（公众号图片消息/小绿书） |
| --- | --- | --- |
| 归谁管 | 排版工具 | 「公众号贴图发布」项目（`FBIF-公众号贴图发布`，A 机 `fbif-sticker`）：投稿、审核、建公众号贴图草稿、后台直接发的也自动进表、贴图群出卡片、同步到抖音小红书，多维表格是正本 |
| 来源公众号 | FBIF食品饮料创新 | 贴图发布项目账号表里的号，现在只有 FBIF会展服务号（即 FBIF食品创新展，`wxb9d97bb588327d66`）；FBIF食品饮料创新的贴图要发，在那张表加一行 |
| 监控源 | `FoodTalks_Feed`；`fbif-wechat-article` 的官方接口线照旧兜文章 | 不经 Feed：Feed 照旧把贴图记「图集/小绿书不自动投递」，排版工具机器入口照旧只收文章（红线 29/33） |
| 发到 | FoodTalks、百家号、头条号；以后大鱼号、知乎、领英 | 抖音图文、小红书（只同步展商介绍，规矩在贴图发布项目 README「同步到抖音、小红书」） |
| 平台上的账号 | FBIF 的号，「平台账号」表 | 各发各的号（用户选「各发各的号」）：贴图发布项目账号表「抖音账号」「小红书账号」列填「平台账号」的 record_id |
| 通知群 | 现在的审核群 | 贴图发布项目账号表「贴图群」（用户选「用现有的贴图群」） |

- 两条线只共用分发服务（平台文件、「平台账号」表、按幂等键记账）；文章的任务、审核卡、外发 lane、monitoring 在排版工具，贴图的任务、卡片、同步记录在贴图发布项目。改归的原因：10-26 贴图发布项目已在「已发布」卡上做好同步按钮，排版工具再从 Feed 绕一圈抓同一条，会一条贴图两张卡两个按钮，两边幂等键不同（`tietu.*` 与 `job-*`），分发服务认不出是同一条。排版工具不做贴图入口、不出贴图卡，`distribution_accounts.go` 只认文章号；贴图永不进 FoodTalks。
- 文章线谁发到哪个平台账号、通知哪个群，放分发服务的多维表格「平台账号」，一行一个平台账号；加平台、加账号只加行，不改代码。
- 抖音没有云端草稿（「暂存离开」只存在那台浏览器的本地存储里）：贴图发布项目交来一条，分发服务才填好、读回核对、点发布。原先人在「已发布」卡上点同步才交（用户选「卡片上点了才发」）；2026-10-27 用户：「现在有新文章过来，就自动同步，同步之后自动公开，不再是私密」，贴图发布项目账号表勾了「自动同步」的号，新发布的展商介绍一出「已发布」卡就自动交、公开发（看「同步仅自己可见」），老稿不补发、没成的等人点重新同步。
- 小红书也由机器发；怎么操作浏览器（真实键鼠、随机、放慢、看画面兜底）见分发工具 CLAUDE.md。

## 分发框架（2026-10-24 用户裁定，迁移中）

迁移顺序、边界细则与 Codex 复核意见见 [ADR](docs/adr/2026-10-24-distribution-framework.md)；没迁完的步骤以下面红线为准，迁完一步就在同一次提交里改掉对应红线。

- Feed 只找稿。排版工具只管内容（正文块、第三方通用规则、审核卡、FoodTalks、分发记录），不写任何第三方平台的写法和限制。分发服务只管平台，一个平台一个文件。多维表格放平台账号、分发记录、小配置，真相仍在服务器 JSON。
- 排版工具与分发服务之间只传外发通用版正文块，账号一律用「平台账号」的 record_id；排版工具缓存分发服务给的平台限制和 FBIF 中台配置包里的账号，两边连不上都不影响看卡、改稿。
- 排版工具的飞书应用只有排版工具连长连接（卡片回调）；贴图发布项目用自己的飞书应用「贴图发布」。各服务的配置从 FBIF 中台的配置包读（2026-10-25 统一配置中台，One-Api `docs/hub/`），排版工具不通知分发服务刷新。
- 已迁各步（账号、删云梭、格式器、接口说明书、贴图线、存完就发）：外发账号只在「平台账号」表、经中台配置包长等待读（`api/hub_bundle.go`），不轮询；平台限制只经分发服务 `GET /v1/platforms` 取并缓存；外发代码不写平台名（`TestDistributionCodeNamesNoPlatform`）；与分发服务的接口以它生成的 `docs/api.md` 为准，本仓库存 `api/testdata/distribution-api.json` 并有测试核对。每步经过见细则 §分发框架。

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
4. 任务状态推给资讯订阅：提交时带 `status_callback_url`（只许与 `FOODTALKS_FEED_BASE_URL` 同源）的任务，`sync-status` 摘要每变一次签名 POST 过去（Standard Webhooks，密钥用 `PUBLISH_SYNC_SERVICE_TOKEN`）；约定只在 [docs/publish-status-callback.md](docs/publish-status-callback.md)。细则 §三系统边界 4
5. 叫醒资讯订阅的看门狗：`alert_issues` 的类别多了或少了才 POST `/monitor-alert/wake`，只在 production 开；何时重算只由事件定，不另设定时（`api/publish_monitor_wake.go`）。细则 §三系统边界 5

## 工作入口

- 匹配 skill 时先完整读取；禁伪造调用。
- UI 前读 `DESIGN.md`，不得偏离；QA 报不一致。
- 新功能遵守 [四层边界](docs/adr/2026-07-10-share-vs-isolate-boundary.md)：能力共用（差异参数化）、视觉按渠道隔离、规则按账号显式配置、交互统一。禁中立层写账号字面量、渲染器互相 import、默认继承 `bottom_blocks`、复制 helper、向 `app.html/site.html` 内联新逻辑。

## 红线

1. **预览全白**：预览页/正文区禁灰底（含 `#F7F7F7/#EDEDED`）。
2. **单层账号菜单**：在左栏导入卡首行；`account_catalog.go` 是真相源，`rule_defaults.go` 登记 preset 后跑 `npm run gen:registry`，禁手改 registry；禁两层切换（同一行靠右的「文章 | 贴图」是载体开关，见 48）。左栏与进度条布局见细则 §2。
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
13. **Mote 署名**：默认 `behavior` 中 `Mote莫特/Mote 莫特` 触发参考来源上方固定署名图；公众号原创作者行 `#396FBC`、不斜体。Mote 二级标题 16px（2026-11-02 用户裁定：「二级目录可以设置为16号字号」「序号1开始的」「只是mote」）：挂在「一、」下的「1. 2. 3.」「（一）」是二级，与网站同一套判定（`heading-detect.js` `headingLevels`），前面没有「一、」的那组序号仍是一级；字号挂在规则「文章默认方案 → Mote 作者默认」的「二级标题字号」（`subheading_size`），别的作者没设就不变，写进标题行内样式、复制进公众号后台照样 16px。
14. **更多文章可选**：`enabled=false` 不渲染；`default_cards` 是新稿默认，单稿可换/删/排/裁。
15. **更多文章选图**：`fetch-article-meta` 返回 `image_urls`；依次选卡片封面、抓取头图、该卡正文媒体，主稿图仅兜底；旧卡缺媒体按链接补抓。
16. **图片说明不可拆**：`图片→图注→图片来源` 连续归图注；`图片来源/图源` 不得进正文或文章级来源。
17. **URL 署名（2026-09-01 用户裁定）**：来源/出处/出品、作者/撰文/撰稿/文、编辑/责编兼容中英文冒号与 `/|｜`；转载稿归一后在正文开头只渲染一次，原创稿仍在“提示：”前只渲染一次；来源、作者、编辑依次独立成行（有值才显示）。正文来源优先，否则用 `source_name`；禁将 `byline` 补成作者。署名公众号蓝、1.75 行高、常规字形、连续行无额外空行；传递链保留 `source_name`。
18. **DOCX 空段语义**：用空行分段的稿连续正文按小段距紧凑、原文空段才留 `wx-gap`；靠段距分段的稿每段留 `wx-gap`；判定只在 `paragraph-breaks.js`；公众号原文紧贴的几段 FoodTalks 也排紧（`tightBefore`）。细则 §18
19. **渠道物理隔离**：FoodTalks 与公众号的渲染器、behavior、页脚、署名分开，双方渲染器禁互相 import，改网站时 `templates/fbif.js` 字节不动；原文字色不进 FoodTalks、公众号排版和外发，只留加粗。细则 §19
20. **网站页脚**：完整输出 `tip/biz_coop/join_group/follow_account/logo/more_info`；主路径 `bottom_blocks.rich_html`，`fbif_footer+buildFooterHtml` 仅等字节回退。单稿只用 `site-footer-state.js` override；规则未就绪禁本地写；换图写 `overrides[seg].fields` 并保留 `{{asset#id}}`。
21. **网站默认包**：`site.html` 用 `getRulePackByPath(location.pathname)`；任意 `/ft/<id>` fallback 到 `ft-default`（`foodtalks-site`），禁裸用 `parsed.packId` 回落公众号。
22. **全账号统一区块交互**：统一 `mountBlockPanel`、显式 `bottom_blocks`、`Sortable.min.js`；网站系列仅复用中性 `block-render/asset-library`，禁 `mp-blocks`。**当前 `site.html` 仍内联原生 DnD，违反本条，暂不可发布。**
23. **微信草稿 T0 存活**：更多文章 `linktype=image` 的 `imgurl` 必须微信图床；卡片 `<img src>` 只能微信图床或 base64 位图，OSS/外站 HTTP 不得是唯一安全图，禁 SVG data，空占位用 PNG data。三渲染器 wrapper 锚点一致；`_footerCopy` 仅由 `footerCopyGate` 重建，旧快照/后台任务不得覆盖新会话。权威契约：[wechat-draft-survival-copy](openspec/specs/wechat-draft-survival-copy/spec.md)。
24. **CMS 表格存活**：site/series 用共享 `table-renderer.js` 的 `outerBorder+cmsSurvival`，align/valign/bgcolor/width、cellpadding、表头 strong 与样式双写；图片按 `wpx` 双写 width；fbif 不启用且字节不动。见 [契约](docs/worklogs/WORKLOG-table-cms-survival.md)。
25. **异步 latest-wins（2026-07-10 审计）**：各类导入、重渲染、更多文章元数据、复制准备、重试、规则保存、账号切换均先取版本门；每个 await 后及提交 DOM/全局/持久化前复核；新入口废止竞争旧入口。账号切换须规则/模板/配置/菜单同提交，失败保留原账号。
26. **发布安全与恢复**：公开须持久 approve（人工，或「自动发布」开关的 `system:auto_publish`）+ 风险 `pass|warning` + 版本匹配，否则 fail closed；自动发布只给第一次进审核的新稿；外调前落 intent，结果不确定进 reconciliation、禁盲重试；`source_key` 永久去重；CMS 手工稿为准。**`stageRepublish` 尚无同题硬门，暂不可发布。** 细则 §26
27. **部署事务（2026-07-10 审计）**：候选以 `DEPLOYMENT_WRITES_PAUSED=1` 启动，暂停发布/rules 写入、恢复任务、通知 worker、飞书长连；公网 SHA/健康/鉴权和本机管理员 readiness（服务令牌只在服务器的中台文件，事务脚本 `readiness` 动作取）全过才 release。接受后先停自动回滚再解暂停；解暂停后失败只向前修。`FOODTALKS_EXPECTED_TARGET=test|production` 必须与 API/前台/后台一致。
28. **微信历史全量口径（2026-07-15，最高优先级）**：`273/311`、单接口/表/素材库数均非总量。完成多响应/多表按 msgid/msg_data_id/序号/article_id/URL/日期去重及分页/时间/字段审计前，只能写“未完成/未核验”；原始响应/字段全落库，飞书历史同步关闭。
29. **已发布监控独立（2026-07-16）**：08:30–18:30 `freepublish` 最新页轮询不得被 draft/素材失败阻断；“群发”另走持久幂等路径。启用统一服务器配置前审计回调、自动回复、菜单；小绿书/图集/未知类型 fail closed。
30. **P0 监控**：任意改动须通过无 paths 过滤的 `.github/workflows/p0-monitoring-contract.yml`，禁删测/skip/降级；`GET /api/publish/monitoring` 如实报各类缺口；外检 watchdog 已迁 `FoodTalks_Feed`，本仓库禁重建。细则 §30
31. **飞书文档异步导入**：交互导入只走 export jobs + long-poll，服务端一次镜像图片；禁退回同步入口、禁删相关文件；合并冲突逐文件裁决，禁整树取一侧。细则 §31
32. **粘贴即排版（2026-08-06 用户裁定）**：粘贴后按输入框最终全文立即提交，URL/飞书链接/正文/Markdown 同等；粘贴的文字按 Markdown 认行首记号与行内链接、加粗（AI 答复点复制按钮得到的就是 Markdown；标题、引用记号去掉，无序列表换成「•」，网址不转全角），选中复制网页时浏览器纯文字里没有的列表圆点、序号从 text/html 的 `<li>` 补回框里，两种复制排出来一样（2026-10-29 用户：「感觉就很奇怪」）；追加粘贴整稿重排；仅 `@` 搜索不自动。禁恢复 `#importSubmitBtn/.import-submit-button`；动作行只留“粘贴后自动排版”；手输以 Cmd/Ctrl+Enter 提交。
33. **机器发布只收链接**：`site-sync/official-sync` 只收 `url`，五个正文旁路字段一律 400，禁恢复任何 HTTP 正文旁路；鉴权头 `X-Publish-Sync-Token`（`PUBLISH_SYNC_SERVICE_TOKEN`）；例外只有 `/api/html-to-blocks`、`/api/layout/v1/*`（46）、`/mcp`（51），都只转换或排版、不建任务。细则 §33
34. **审核卡可识别文章**：各状态都有 hero（封面、标题、标签、摘要、链接），回调同步返回新卡、禁现场传图；样式变了用 `card-refresh` 原位更新；一张卡最多 200 个元素，超了先去图标、再把分发区收成一行字，加平台前先跑卡片上限测试；过飞书 14 天期限记 `expired`、不重试。细则 §34
35. **风险分层**：首屏最多 3 条 `confirmed` 实质项，其余折叠（已证实≤10、待核≤6）；正文全文送模型；口径只在 `publishRiskItemConfirmed/splitPublishRiskItems`，模型回答只按 `decodePublishRiskOutput` 解析。细则 §35
36. **审核群只发卡片（2026-08-09 用户裁定）**：禁运维播报；`cleanup_group_broadcast` 只清理且回滚不恢复。监控仍由外部读取 `GET /api/publish/monitoring`。例外（2026-10-12 用户裁定：「在卡片里@人可能收不到……在这个卡片消息的话题下创建一个对话，@对应的人」）：外发待修改提醒以富文本回复进审核卡的话题，每轮只发开头 @ 一条与改好收尾一条；校对提醒（红线 44）同样只回复进卡片话题，同一组错误只发一条。
37. **外部平台只发已审核原创**：外发只给已公开、审核版本有效的原创，创新周报不外发；原创只认明说，判定只问 `publishArticleProvenance`；每个平台账号一条 lane、互不阻挡，经分发服务存草稿→读回核对→发布→跟到结果，结果按 `outcome` 认、不按 HTTP 状态猜，同一幂等键绝不发两遍；没成的按退避自己重推到 7 天，号没登录的等登好了自己接着推；「平台账号」是「人工发」的号系统不推，卡片显示人发了没有（查得到才列，2026-11-21 腾讯号）；真下线是对外动作，先问用户。卡片、改稿、提醒、人工已发、云梭旧 lane 见细则 §37
38. **FoodTalks/第三方平台一律切微信导流尾**：与原创无关；`stageRender` 固定切尾，外发正文经 `publishHTMLWechatTrailerLeak` fail closed，不得误伤网站页脚；已公开稿用 `repair-wechat-trailer`。细则 §38
39. **网站图宽/目录/标签/分类**：所有文章一视同仁，正文≥3000 字且 h2+h3≥5 才在发布组装层写目录，只收标题1（Mote 例外收标题2）；标签最多 3 个、只等值匹配、禁新建（Mote 系列标签例外）；栏目/活动标签双侧拦截；存量用 `repair-links`。细则 §39
40. **网站转载署名与出处**：出处按公众号「转载」原号 > AI > 声明兜底判定，原创要明说（37）；开头署名与文末转载声明用同一个判定，转载稿开头一律「本文为{来源}原创，作者：…」；裸署名行去重或降级；转载声明链接优先阅读原文，核验不过不链；访谈稿提问方蓝加粗。存量用 `repair-links`、`source-resync`。细则 §40
41. **公司库自动关联**：AI 标签词经 `/company/companies/search` 规范化等值匹配，正文首次出现处注入 `mc-company-link`，每篇≤3 家；禁部分包含匹配、禁自建公司，检索失败只降级。细则 §41
42. **Mote 系列链接改站内**：只对 Mote 稿，正文里的公众号文章链接换成已发布的 FoodTalks `/news/{id}`，找不到或出错保留原链、不 fail；禁改 JS 渲染器；存量用 `repair-links`。细则 §42
43. **多平台分发版式**：外发是独立渠道规则，不沿用网站样式；各平台一样的规则只在 `api/distribution_rules.go`、落进外发通用版正文块，平台写法与限制只在分发服务；块格式只在 `api/article_blocks.go`；样稿考卷块一变就失败，看过确认才 `GOLDEN_UPDATE=1`。通用层、封面、标题、图注、动图压缩见细则 §43
44. **确定错误的校对提醒**：只提醒能写出唯一改法「X 应为 Y」的确定错误，每篇≤10 处，回复进审核卡话题，原创 @ 作者编辑、其余 @ 校对负责人；不改正文、不挡发布，同一组错误只提醒一次。细则 §44
45. **每周手改清单**：每周一私信「手改周报接收人」，只列同类手改≥3 篇、规则后来改好的不列，不发审核群；只读 FoodTalks；每周只发一次，宁漏不重。细则 §45
46. **排版接口**：`/api/layout/v1/*` 只排版、不建发布单；成品与网页「导入→复制」逐字一样（共用 `engine.js`），禁服务端另写一套；`tests/layout-parity.test.mjs` 变红先在网页确认再重抓样稿，禁改测试迁就；钥匙只在中台「接口调用方」表。细则 §46
47. **FoodTalks食品资讯公众号**：账号 `foodtalks-mp` 与什么是系列共用模板、差别只在规则（`api/rule_foodtalks_mp.go`），什么是系列的规则不动；FoodTalks 文章链接经管理接口取正文，读不到就报错；小标题与网站同一套判定。细则 §47
48. **公众号「文章 | 贴图」载体**：公众号编辑器导入卡首行的载体开关；文章模式各账号模板一字不动，贴图走独立渲染器 `templates/sticker.js`、不读账号规则，复制只出 `<p>甲<br><br>乙</p>`，预览与贴图编辑器 1:1；排版接口不出贴图；禁把贴图做成账号。细则 §48
49. **网页校对**：公众号编辑器「AI 校对」卡，排好自动查，与发布流水线同一套提示词和模型；只查，不建单、不发卡；改法要人点、不自动套用，标记不进预览 DOM 和复制内容；禁另写提示词或另选模型。细则 §49
50. **公众号滑动图**：只有公众号排成滑动图（共用 `templates/shared/image-swipe.js`），FoodTalks、外发、排版接口照旧竖排并去掉滑动提示；提示语认法 Go、网页各一份，都用 `api/testdata/swipe-hints.json` 考。细则 §50
51. **智能体入口**：`/mcp` 只排版和校对、给编辑器链接；禁存草稿、发布、碰 FoodTalks 或第三方平台，禁另写排版或校对，日志和回包不出令牌；本项目自己当授权服务器，只放飞书登录的同事；提示词只写一份。细则 §51

## 守护索引
每条红线由哪些测试守着，见细则 §守护索引；改哪条红线的行为，先跑那几条。

## 运行/规则卫生

- 后台循环多久跑一次只在 FBIF 中台总账本「定时任务和回调」改（2026-11-03 用户定，One-Api `docs/hub/轮询和定时任务.md`）：中台下发进 `/etc/fbif-hub/env/layout.env`，启动时读（`api/job_intervals.go`，启动日志 `schedule:` 一行列出现在按多少跑）；代码里的值只是没下发时的默认，和表里一样；不许写死、不许在 `.env`、规则库、管理后台另设一处。本文件写的「每 2 分钟」「每分钟」这类都是默认值。新加循环先在表里登记一行，再照这个文件加一个变量。
- 只有 `main` 一个分支（2026-10-08 用户裁定）：禁止建分支/PR 分支，改动本地跑完 Go/Node 全测后直接提交推 `main`（即触发 deploy）。未合并的旧工作已存为 tag `archive/codex-unify-punctuation`、`archive/su-design-migration`。
- 仓库公开（2026-11-12 用户定，为了 Actions 免费）：代码、全部历史和 Actions 日志谁都能看；密钥只在中台和 GitHub Secrets、日志只打印变量名，测试数据只用已公开发表的内容，不放客户名单、内部表格数据和真实账号。
- 运行见 [README](README.md)。生产缺 `RULE_SUPER_ADMIN_OPEN_IDS` 必须启动失败；推 `main` 触发 deploy；四个入口（`/`、index/app/version）须 `no-cache`；改权限后验 `/api/auth/me`。
- 登录由入口统一拦截（FBIF 统一登录 v5）：Go 只读 `X-FBIF-User`（`api/internal/login`）、飞书授权用 `api/internal/feishugrant`，两份模板原样复制不改，接线只在 `api/auth_login.go`；不过登录的路径只写在 `deploy/caddy/fbifmp-layout.caddy` 的 `import fbif_login` 前和 `programPathPrefixes` 这一张单子里。细则 §登录
- 只写当前行为；偏好标来源/日期/状态。`behavior` 仅限署名/来源、Mote 图、原创/转载页脚，禁任意 JS。
- 复制禁导航；后台工作不得塞进按钮或改成准备态。
- URL/DOCX/复制须有测试；新解析规则跑 Go/Node 全测并以真实浏览器验样例。
- 模型分工（2026-10-07 用户裁定）：看图与翻译等批量任务用 `mimo-v2.6-flash`，复杂/高价值任务（风险检查）用 `mimo-v2.6-pro`；唯一入口 `api/ai_models.go` 选档，禁业务文件写模型名或另接供应商。缩写标题、摘要、图注这类改一小段字的走 `aiRewriteCall`（Chat Completions 带 `thinking.type=disabled`）：MiMo V2.6 默认先思考，Responses 接口不认关思考的参数，2026-11-03 #340 缩一条图注想了 80 秒、两条把 2 分钟耗光都没缩成；关掉后一条 2–7 秒。
- 网站封面/摘要与公众号保持一致（2026-10-07 用户裁定）：封面用公众号原封面、摘要用公众号摘要（`msg_desc`），AI 只在原文缺值时兜底；兜底候选只含原文配图，网站页脚名片/二维码永不作封面（#263）。存量已发布稿用 `POST /api/publish/jobs/{id}/sync-wechat-meta` 对齐，只替换没人手改过的字段。
