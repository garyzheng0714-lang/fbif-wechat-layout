# FBIF 公众号排版 — Agent 规则

硬约束见 [索引](docs/INDEX.md)；违规不可发布。

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
26. **发布安全与恢复（2026-07-10 审计；2026-08-14 手工优先裁定；2026-08-24 自动发布开关）**：公开须持久 approve（人工，或管理后台「发布链路 → 自动发布」打开后的系统 `system:auto_publish`）+ 时间、风险 `pass|warning`、版本匹配；否则 fail closed。开关默认关；只作用于新同步稿，已在待审核的不受影响。阻断级、同题已发布仍停待审核，不自动翻开。群卡保留，已发布卡可下线。外调前落 intent；不确定结果进 reconciliation，禁盲重试；`source_key` 永久去重。飞书验 token/审核人、清洗外部文本、持久 outbox；回调只落决策，发布异步可恢复。JSON store 仅单实例。CMS 手工稿为准，库存禁覆盖；卡片前标题查重标红，`stagePublicize` 落 intent 前复查，命中/查询失败 fail closed；仅修流水线未人工改稿。**`stageRepublish` 尚无同题硬门，暂不可发布。**
27. **部署事务（2026-07-10 审计）**：候选以 `DEPLOYMENT_WRITES_PAUSED=1` 启动，暂停发布/rules 写入、恢复任务、通知 worker、飞书长连；公网 SHA/健康/鉴权/管理员 readiness 全过才 release。接受后先停自动回滚再解暂停；解暂停后失败只向前修。`FOODTALKS_EXPECTED_TARGET=test|production` 必须与 API/前台/后台一致。
28. **微信历史全量口径（2026-07-15，最高优先级）**：`273/311`、单接口/表/素材库数均非总量。完成多响应/多表按 msgid/msg_data_id/序号/article_id/URL/日期去重及分页/时间/字段审计前，只能写“未完成/未核验”；原始响应/字段全落库，飞书历史同步关闭。
29. **已发布监控独立（2026-07-16）**：08:30–18:30 `freepublish` 最新页轮询不得被 draft/素材失败阻断；“群发”另走持久幂等路径。启用统一服务器配置前审计回调、自动回复、菜单；小绿书/图集/未知类型 fail closed。
30. **P0 监控（2026-07-17；2026-08-06 归属修订）**：任意改动须通过无 paths 过滤的 `.github/workflows/p0-monitoring-contract.yml`，禁删测/skip/降级。`GET /api/publish/monitoring` 必须如实涵盖同步缺口、45 分钟未审核、通知/卡片错误、重复 source_key、轮询陈旧、积压；processing 或通知 pending/sending、卡缺失/失败均 `ready=false`。外检 watchdog 已迁 `FoodTalks_Feed`；本仓库只 `cleanup_legacy_watchdog` 且回滚不恢复，禁重建外检。
31. **飞书文档异步导入（2026-08-05 恢复上线）**：交互导入只走 export jobs + `after/wait_ms` long-poll，逐阶段报真实 done/total；服务端一次 `base64_images` 镜像 OSS，浏览器禁逐图回源 doc-cache；250ms 递增退避；预解码+离屏 staging 整帧提交，导入时保留旧稿。禁退回同步入口，禁删 `feishu_export_jobs.go/doc_image_mirror.go/preview-dom.js/preview-image-loader.js/feishu-doc-ready.js/import-progress.js`。冲突逐文件裁决并核对 `git diff --diff-filter=D <父> <merge>`，禁整树取一侧。
32. **粘贴即排版（2026-08-06 用户裁定）**：粘贴后按输入框最终全文立即提交，URL/飞书链接/正文/Markdown 同等；追加粘贴整稿重排；仅 `@` 搜索不自动。禁恢复 `#importSubmitBtn/.import-submit-button`；动作行只留“粘贴后自动排版”；手输以 Cmd/Ctrl+Enter 提交。
33. **机器发布只收链接（2026-08-06 用户裁定；2026-08-18 补阅读原文）**：`site-sync/official-sync` 只收 `url` 并走网页同一抓取解析；`content_html/title/source_name/author/cover_url` 任一出现均 400；body≤64KB，共用 `rejectedPublishContentFields`。可选 `content_source_url`/`original_source_url` 只补出处，不替代抓取。official 固定 `ordinary_confirmed`。禁恢复任何 HTTP 正文旁路。鉴权头 `X-Publish-Sync-Token`，双方统一 `PUBLISH_SYNC_SERVICE_TOKEN`；旧 `X-Admin-Password/LAYOUT_SYNC_TOKEN/LAYOUT_ADMIN_PASSWORD` 废弃。例外：Bearer `RENDER_TOKEN` 的 `/api/html-to-blocks` 仅转换、不建任务。
34. **审核卡可识别文章（2026-08-06 用户裁定）**：待审/已发/驳回/异常均有 hero（封面、粗标题、标签、摘要、预览/原文链接）；终态禁仅 info。封面复用 `ReviewCoverImgKey`，旧稿无值则无图单列；回调须同步返回新卡，禁现场传图。`ReviewCardMsgIDs` 已存在时，当前群无映射必须发新卡，禁回退 legacy id；群成员回调只认当前 chat，历史群仅静态白名单。样式变更后用管理员或 `PUBLISH_ADMIN_SERVICE_TOKEN` 调 `card-refresh` 原位更新；只换样式，不重跑检查。
35. **风险分层（2026-08-08 用户裁定）**：首屏最多 3 条显式 `confirmed` 实质项；转载授权/system 全部折叠待核。口径只在 `publishRiskItemConfirmed/splitPublishRiskItems`；未知 certainty→`unverified`。无 confirmed：pass 无项才“检查通过”，有待核说“未发现可证实的实质性错误”，block/未知分别说“风险阻断/检查未完成”且仅二者有状态标签。待核数只在折叠标题；待审 info compact。两层共用官方无序列表，项内 `<br>`。prompt factual 优先，headline≤100 字并截断/回退。
36. **审核群只发卡片（2026-08-09 用户裁定）**：禁运维播报；`cleanup_group_broadcast` 只清理且回滚不恢复。监控仍由外部读取 `GET /api/publish/monitoring`。
37. **外部平台只发已审核原创（2026-08-11 裁定；2026-08-13 修订）**：FoodTalks 仍收原创/转载。外发按钮仅在已公开、审核版本有效且原创时显示；目标账号服务端固定，callback 禁带账号；自有来源/作者即原创，不等风险/AI；转载标记隐藏；点击再确认并记 actor。云梭独立状态机：先冻结 payload+intent；无 `content_pk` 只人工对账，禁盲发；有 id 只轮询；仅明确受理前失败可重试。独立 worker，持久 card outbox 原位更新；两套 outbox 串行且发前重读，callback 只 toast；仅 fake/本地验证。
38. **网站/云梭一律切微信导流尾（2026-08-13 用户裁定）**：与原创无关；`isRepost` 只影响来源类型/提示。`stageRender` 固定切尾；云梭 BlocksJSON 在 Go 切尾并经 `publishHTMLWechatTrailerLeak` fail closed；泄漏标记不得误伤合法网站页脚。已公开稿用 `repair-wechat-trailer` 覆盖，不重审。
39. **网站图宽/目录/标签/分类（2026-08-14 用户裁定，62765 金标准；2026-08-17 按排版规范修订并升版 v19）**：长图比≥1.7 固定 400px，竖图宽≥300，二维码自然宽阈值 300（preset v19 + rule-presets.js/render.mjs 离线 fallback + fixture 均已同值）。目录按排版规范（2026-08-17 用户裁定：**所有文章一视同仁，不分原创/转载**，历史转载没目录是人工漏掉；2026-08-24：目录只收标题1）：正文非空白字符 ≥3000 且 h2+h3 合计 ≥5 时在发布组装层写哈希确定性 `mctoc_*` 和 CMS toc（条目只收标题1 / h2；标题2 是「1、2、3」或「（一）」→ h3，不进目录）；渲染器仍关闭 mctoc。所有 `job.Tags` 可解析，Mote/系列优先；最多 3 个，仅 `tag_type_id=6` 规范化等值匹配，禁创建；栏目/活动标签（首发栏目名、`FBIF\d{4}`、会后报告、参展企业、Wow 奖）`publishSiteTagBlocked` 双侧拦截（发布解析 + 词表注入 prompt 前），品类词/品牌名不拦。原创分类 `FOODTALKS_CATEGORY_IDS=52,58`，转载 `FOODTALKS_REPOST_CATEGORY_IDS=53`。
40. **网站转载署名与出处（2026-08-17 运营反馈批次，62813 实证）**：① 出处判定 AI 优先，`publishDeclaredSource` 确定性兜底——头部前 12 个文本块与尾部 35% 区域的「来源/出处｜：X」「本文来自/为 X」声明抽机构名，非自家即转载，防天下网商类误判原创进独家专栏。② 头部前 5 元素与尾部区域的裸署名行（来源/作者/编辑/文…，冒号或竖线分隔）：开头已有网站署名则删除文末公众号口径来源/作者（2026-08-18，避免与开头重复）；否则降级 `footer-note` 14px 灰斜体。署名前缀同时进 `caption_lead_regex` 防误升 h2。③ 同文本 ≤16 字无序号标题全篇出现 ≥2 次判账号装饰行整体删除（天下网商×3 案例，成品口径）。④ 转载声明《标题》超链接优先公众号「阅读原文」地址（`OriginalSourceURL`，fetch 阶段抓 `msg_source_url`）；自家号转发且阅读原文为空时先查 Feed 订阅列表，未订该号再 TikHub 广域搜（搜一搜），标题+出处都对上才用；禁红狐/按号拉列表；核验不过则不链，禁止回退自家转载页。外号原文或自家原创仍用提交链接。⑤ 访谈（规范(6)，62807 实证）：判据=两个及以上「角色：」前缀各现 ≥3 次；**提问方** FoodTalks 蓝+粗、回答方默认色+粗——提问方按 书名号>问号率（差≥0.25）>首现 确定性打分，不再按「书名号才上蓝」；提问方前缀独立成段时与下段提问内容合并（提问方+提问内容间不空行）。
41. **公司库自动关联（2026-08-17 用户裁定，规范 step2-(5)）**：候选=AI 标签内容词；`GET /company/companies/search`（前台公开接口，字段 id/cnName/shortCnName/urlCode；文档的 `/company/admin/companies/page` 对服务账号 Access denied 勿用）检索后与全称/官方简称表**规范化等值**匹配，多条优先 cnName 含「中国」，仍歧义放弃。正文首次出现处注入 `mc-company-link`（形态对齐小工具箱成品 48190：data-id/name/code、新窗口；href=`/company/<urlCode>`，数字 id 打不开公司页），标题/图注/既有链接内禁注入，每公司限 1 处、每篇 ≤3 家；`relatedCompanyIds` 同步写 id。检索失败只降级不 fail。禁部分包含匹配、禁自建公司。
42. **Mote 系列链接改站内（2026-08-24 用户裁定，金标准 62285 Tazo→/news/62236）**：仅作者是 Mote/莫特的网站发布组装。正文里指向 `mp.weixin.qq.com` 文章的超链接，换成已发布 FoodTalks `/news/{id}`（前台域名走 `FOODTALKS_FRONT_URL`）；锚文字与 `target`/`rel` 不动。先用任务库 `source_key`/`aliases`/`source_url` 对已发布单，找不到再抓公众号标题并 `SearchNewsByTitle` 等值匹配；只认已发布、唯一命中、不是本稿。找不到或查询失败保留原链，不 fail job。禁改 JS 渲染器；交互复制仍可保留公众号链。

## 守护索引

- 2/32：`top-toolbar/account-switcher/auth-shell/import-progress-component/e2e-progress-component/editor-shell/e2e-paste-autoformat`。
- 9/23/25：`copy-behavior/uploader/wechat-draft-survival/copy-session/active-rule-selector`。
- 19–24：`site-footer-parity/series-blocks-fidelity/foodtalks-site-config-flow/table-renderer-shared/foodtalks-site-docx`。
- 26：`api/publish_duplicate_guard_test.go`、`api/publish_auto_publish_test.go`；27/36：`tests/deploy-transaction.test.mjs`。
- 30：`.github/workflows/p0-monitoring-contract.yml`。
- 31：`e2e-preview-speed/e2e-feishu-import-progress/preview-dom/preview-image-loader/feishu-doc-ready/import-progress`、`api/feishu_export_jobs_test.go`。
- 33：`api/publish_http_test.go` 中 `TestPublishOfficialSyncFetchesFromURL` 及三个 reject 测试。
- 34：`api/feishu_review_card_test.go`；35：`api/publish_risk_layers_test.go`。
- 38：`foodtalks-site-behavior`、`api/{wechat_trailer,yunsuo,publish_trailer_repair}_test.go`；39：`tests/image-width.test.mjs`、`api/publish_site_enhance_test.go`。
- 40：`tests/{foodtalks-site-docx,foodtalks-site-behavior,heading-detect,site-interview}.test.mjs`、`api/publish_site_enhance_test.go`（declared-source/provenance/render-source 用例）、`api/wechat_blocks_test.go`（meta 三值）、`api/publish_original_url.go`、`api/publish_tikhub_worklist.go`。
- 41：`api/publish_company_links_test.go`、`api/company_match_smoke_test.go`（2026-08-17 生产检索快照）。
- 42：`api/publish_mote_series_links_test.go`。

## 运行/规则卫生

- 运行见 [README](README.md)。生产缺 `RULE_SUPER_ADMIN_OPEN_IDS` 必须启动失败；推 `main` 触发 deploy；四个入口（`/`、index/app/version）须 `no-cache`；改权限后验 `/api/me`。
- 只写当前行为；偏好标来源/日期/状态。`behavior` 仅限署名/来源、Mote 图、原创/转载页脚，禁任意 JS。
- 复制禁导航；后台工作不得塞进按钮或改成准备态。
- URL/DOCX/复制须有测试；新解析规则跑 Go/Node 全测并以真实浏览器验样例。
