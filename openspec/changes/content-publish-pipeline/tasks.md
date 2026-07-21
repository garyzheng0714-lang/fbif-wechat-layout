# Tasks: 公众号 → FoodTalks 网站内容自动化发布流水线

> ⚠️ 执行纪律（用户 2026-07-10 指令）：带 🔴 的任务包含对外真实调用（FoodTalks 写入 / 飞书发消息 / 微信 API），必须经用户批准后执行；其余任务是本地代码与文档工作。任何阶段声明完成前，先跑本组验收项。跨仓库任务已标注仓库。

## 1. Phase 0 — 安全与外部前提（人工 + 确认类，先做）

- [x] 1.1 ~~重置飞书 App Secret~~（2026-07-10 **用户裁定不重置、直接使用**；new值仍遵守 env-only 纪律，不进 git/日志）
- [ ] 1.2 向 FoodTalks 网站后端确认：正式环境 Base URL、服务级长效鉴权（替换抓包个人 token）、响应体结构与错误码、`publishStatus`/`isPublic`/`sourceType` 完整枚举（部分已由 Phase 1 实测覆盖，见 docs/foodtalks-api-contract.md §9 余项）
- [x] 1.3 图片转存通道（2026-07-10 实测解除硬阻塞，用户指路）：FoodTalks API 不校验图片域名、前台原样引用外域图；走排版工具既有 `POST /api/oss-upload`（urls 模式，服务器内网并发，19 张 ~1.3s，上游 localhost:9000 python 服务）；权衡与实测记录见契约 §8
- [x] 1.4 运营发文动作（2026-07-10 用户答复：**群发/发布都有、不固定**）→ Phase 5 自动发现不能假设 freepublish/batchget 全覆盖，按「拉到多少算多少 + 手动贴 URL 永久兜底」设计；6.1 实测覆盖度仍必做。「不启用服务器配置」红线共识维持
- [ ] 1.5 确认资讯订阅生产服务器出口 IP，加入 FBIF 订阅号 IP 白名单（微信公众平台后台）
- [ ] 1.6 建飞书审核群，把应用机器人拉进群；🔴 调 `GET /open-apis/im/v1/chats` 获取群 chat_id 存入配置
- [ ] 1.7 建议项：把飞书文档里明文的微信 AppSecret 挪到密钥管理（提醒用户，不代做）

## 2. Phase 1 — FoodTalks 发布 API 冒烟（🔴 全组需用户批准，测试环境）

- [x] 2.1 🔴 用测试环境创建一篇标记明显的测试文章（title 含 [SYNC-TEST]），尝试隐藏组合（isPublic=0 / publishStatus 非 1），记录完整响应体（2026-07-10：文章 7240/7241；🚨 isPublic=0 不隐藏，隐藏靠 publishStatus=0/2）
- [x] 2.2 🔴 验证隐藏态：未登录浏览器检查首页/列表/搜索/直链均不可见；截图留档（2026-07-10：草稿/下线态直链 404、列表无；截图见会话记录）
- [x] 2.3 🔴 PUT 编辑同一篇（改标题与 content），验证编辑生效且不产生新文章（2026-07-10：7240 就地更新，PUT 为全量覆盖语义）
- [x] 2.4 🔴 翻转公开再翻回隐藏，验证可见性开关双向可用；测试完把文章下线/删除（2026-07-10：1↔2/3 双向可用；单向门 1↛0；两篇均已下线）
- [x] 2.5 将实测结论（枚举语义、响应结构、错误码、幂等表现）回填 `design.md` D3 字段映射表，形成《FoodTalks API 实测契约》小节（2026-07-10 完成）
- [x] 2.6 验收：冒烟脚本（curl 或 Go 小程序）与实测记录入库 `docs/`，测试环境无遗留可见测试文章（2026-07-10：`docs/foodtalks-api-contract.md` 入库；列表页复核无 [SYNC-TEST] 残留）

## 3. Phase 2 — 排版工具支持网站模板渲染（本仓库）

- [x] 3.1 `render-service/render.mjs`：按 fbif 同款 globals 桩方式接入 `foodtalks-site.js` 渲染器，梳理其额外 DOM/全局依赖并补桩（2026-07-10：既有桩已够——linkedom DOMParser + window/localStorage/fetch；渠道差异收敛为 CHANNELS 参数表，零 packId 字面量分支）
- [x] 3.2 `render-service/server.mjs`：packId 白名单加 `ft-default`，按模板路由；未知 packId 保持 400 显式失败（2026-07-10：白名单与 CHANNELS 表同源 SUPPORTED_PACK_IDS；本地 curl 冒烟 401/400/200 三态验证）
- [x] 3.3 接通网站规则：渲染前拉 `GET /api/rules/active?account=ft-default` 灌入 `_activeConfig`（复用 fbif 链路的缓存与降级），页脚走 bottom_blocks 主路径（2026-07-10：缓存改按账号分 key 的 Map，交错渲染互不污染有测试守）
- [x] 3.4 确认 CMS 存活开关（table `outerBorder`+`cmsSurvival`、图片 width 双写）在 API 渲染路径生效（2026-07-10：headless 输出实测 table border/cellpadding + td align/bgcolor + strong 齐全）
- [x] 3.5 新增 `tests/render-service-site.test.mjs`：样例 blocks → 断言网站模板特征、表格 CMS 存活（模拟清洗器复演真值审计口径）、页脚存在、fbif 输出逐字节不变（2026-07-10：golden 由重构前代码生成 `tests/golden/render-service-fbif.html`；npm test 792 全绿）
- [x] 3.6 用一篇真实公众号文章（fetch-article → render ft-default）与人工 `/ft/` 排版结果做结构 diff，验证同源（2026-07-10：真值文章 62285/星巴克茶品牌，188 blocks；正文中段 498 标签与浏览器人工链路逐组指纹 100% 一致；头（Mote作者行）尾（版权声明段）两处差异以已发布成品 ft-groundtruth/62285.gt.html 为真值裁决——成品两段皆有、API 输出两段皆有且逐标签同构，浏览器本地缺失系本地种子规则页脚关闭+作者行提取进 CMS 表单所致，非渲染不同源）
- [x] 3.7 `npm test` + `cd api && go test ./...` 全绿；部署（deploy.yml 无需新 env）；线上 curl 冒烟 ft-default（2026-07-10：CI 测试门禁全绿，run 29062786816 部署成功，生产 version=7fad2fc；render 服务生产端口 19096）
- [x] 3.8 验收：线上 `/api/render packId=ft-default` 对样例文章出网站 HTML；红线 #19/#23 回归测试全绿（2026-07-10：真实文章 62285 blocks 生产重放，输出 30078 字节与本地逐字节同长、imgN17/headingN10 一致、Mote 署名 #888888 同构成品；未知 packId 400；fbif 输出正常；红线测试随 npm test 792 全绿。备查：手造非契约形态 blocks 时 ft 模板静默出空而 fbif 宽容输出——两渲染器既有差异，真实链路 blocks 均出自 fetch-article/html-to-blocks 不受影响）

## 4. Phase 3 — 排版工具本体 MVP：手动 URL → 隐藏发布 → 文本/卡片通知（本仓库）

> 2026-07-10 架构裁定：排版工具是发布编排中枢，资讯订阅只作为未来输入源。
> 4.1/4.2 的资讯订阅实现仅保留为参考；4.3 起的权威实现均在本仓库 `api/`。

- [x] 4.1 新包 `internal/foodtalks`：Client（CreateNews/UpdateNews）、News 结构体按 Phase 1 实测契约定义、401/403 凭证过期专项错误、env 配置（BASE_URL/TOKEN 缺失 fail loud）（2026-07-10 资讯订阅仓 04d39ac：27 字段契约对齐、id 二态 RawMessage、publishStatus 客户端自律 0/1/2、网关/凭证/业务错误三分类）
- [x] 4.2 `internal/foodtalks` 单测：请求体序列化、幂等锚点判断、错误映射（httptest 桩）（2026-07-10 同提交：9 项全绿，含「HTTP 200 业务失败」真实样例与枚举拒发零请求断言）
- [x] 4.3 图片转存：`stageMirror` 整批调用既有 OSS 通道，缺失项重试一次；映射与封面落 job，失败显式停单
- [x] 4.4 任务持久化：按本仓既有范式使用原子 JSON store，落 `source_key` / URL / blocks / image map / FoodTalks id / CreateNews intent / 风险与审核字段；启动损坏 fail loud
- [x] 4.5 发布状态机：created→fetching→rendering→mirroring→enriching→assessing→staging→awaiting_review→publicizing；failed/retry、reconciliation 与重启恢复均有 CAS 护栏
- [x] 4.6 渲染服务支持 `packId=ft-default`，未知 packId 400，网站与公众号 behavior 物理隔离
- [x] 4.7 风险提示节点：覆盖功效表达/转载授权/竞品评价/数据来源，输出 pass/warning/block + 证据；解析/调用失败降级 warning；block 在卡片和核心状态机双重禁止通过
- [x] 4.8 API：`POST /api/publish/site-sync`、任务列表/详情、review/retry/reconcile；HTTP 走 ADMIN_PASSWORD，飞书回调走 verification token + reviewer open_id 白名单
- [x] 4.9 飞书通知：待审优先交互卡片，失败降级文本；发布/驳回终态通知保留；风险、预览、原文与后台入口齐全
- [x] 4.10 对抗单测：状态机推进、source_key 永久去重、CreateNews 不确定态、CAS review/retry、重启恢复、缺风险 fail-closed、block 不可通过
- [ ] 4.11 🔴 测试环境全链路真机验收：贴真实公众号 URL → 隐藏文章生成 → 群收到通知 → 前端/接口点通过 → 文章公开 → 群收到结果；再触发同 URL 验证幂等
- [ ] 4.12 🔴 生产部署：备份并校验 JSON store → 事务化部署与健康验收 → env 配齐（FOODTALKS_*、FEISHU_*、ARK_*、RENDER）→ 用一篇真实文章走通全链路（停在隐藏态，人工确认后公开）
- [ ] 4.13 验收：MVP 链路生产可用；误发不可能（公开只能经审核动作）；WORKLOG 实时回写

## 5. Phase 4 — 飞书交互卡片审核（本仓库）

- [ ] 5.1 按 feishu-cardkit 规范搭审核卡片 JSON（标题/来源/类型/风险列表/预览与原文按钮/「通过并公开」「驳回」）；🔴 真机预览卡片渲染
- [x] 5.2 `card.action.trigger` 同时实现 Webhook 备用通道与飞书 SDK 长连接主通道；两者共用业务核心
- [x] 5.3 回调处理：verification token fail-closed、审核人 open_id 白名单、CAS 通过/驳回、终态卡与重复点击收敛
- [x] 5.4 待审通知优先卡片，文本能力保留为故障降级
- [ ] 5.5 🔴 真机验收：卡片按钮走完通过/驳回/无权限三条路径，卡片状态更新正确
- [ ] 5.6 验收:审核动作全程不离开飞书；无权限点击不改状态

## 6. Phase 5 — 公众号自动发现（仓库 FBIF资讯订阅）

- [ ] 6.1 🔴 实测 `freepublish/batchget`：拉 FBIF 订阅号最近已发布列表，核对运营最近 N 篇（群发/发布两种动作）是否都在——结论写入 design Open Questions
- [ ] 6.2 `internal/wechat` 增加 `BatchGetPublished`（分页、频控退避）
- [ ] 6.3 去重表迁移 + poller 定时任务（间隔可配默认 15 分钟）：新文章逐篇（多图文拆 index）建 job；轮询失败记录人话日志不扩散
- [ ] 6.4 🔴 灰度：自动发现只建 job 到 awaiting_review（与手动一致），观察一周误报/漏报后再考虑默认开启
- [ ] 6.5 验收：公众号新发一篇 → 15 分钟内审核群收到卡片，无重复任务

### 6A. P0 假正常事故加固（2026-07-20）

- [x] 6A.1 feed 健康增加同步 worker 心跳、同批 `idx` 连续性、10 分钟稳定窗口和“所有普通文章已到审核”硬门禁
- [x] 6A.2 排版健康在任一 processing 或审核通知 pending 时立即非绿色
- [x] 6A.3 幂等投递取消固定重试上限；单轮 worker panic 后下一轮继续，整个 worker 循环 panic 后由 supervisor 重启
- [x] 6A.4 看门狗恢复后不再立即重复发每日心跳；绿色文案明确批次、审核态和审核卡均完成
- [x] 6A.5 CI 增加部分批次、未完成审核、worker panic、重试耗尽和通知状态故障注入
- [ ] 6A.6 接入独立于当前供应商的第二发现源（优先评估 10:38 已有消息的飞书 Dudu 群），消除“供应商整体漏掉单篇 idx=1 时我方无信号”的物理盲区

### 6B. 安全与稳定性审计整改（2026-07-21）

- [x] 6B.1 所有服务端出站 HTTP 统一执行 URL、端口、DNS 结果和每跳重定向校验；响应体限长，图片代理增加域名白名单、MIME 与文件特征双重验证，并补表驱动与模糊测试
- [x] 6B.2 高成本入口统一增加鉴权/同源校验、请求体与 DOCX 解压上限、限流、并发门和有界缓存；后台任务使用有界 worker pool，panic 不再击穿 worker
- [x] 6B.3 复制与会话恢复改为 fail-closed：剪贴板拒绝不得显示成功；快照带文章/账号/规则/构建指纹和版本门；过期异步任务不得覆盖新文章
- [x] 6B.4 规则与绑定增加 schema 校验、精确 revision/hash 和乐观并发；生成目录损坏在生产启动与 readiness 中显式失败
- [x] 6B.5 发布、审核、通知、监控和恢复路径补齐超时、CAS、持久 outbox、抖动退避、死信/重放、严重度语义和两阶段部署验收
- [x] 6B.6 CI 增加 JavaScript 语法/循环依赖/覆盖率/依赖审计、Go vet/staticcheck/race/govulncheck、Shell/Python/env 校验；浏览器矩阵覆盖 Chromium 全量与 Firefox/WebKit 核心冒烟，并保留失败证据
- [x] 6B.7 删除已不可达的多文件批处理 UI、状态和文档描述，产品契约统一为“一次处理一篇文章”
- [ ] 6B.8 独立迁移：将发布任务、outbox、规则与大对象从单进程 JSON/sessionStorage 迁到 SQLite WAL（或正式数据库）与 IndexedDB；需要迁移校验、备份和回滚演练，不能夹带在安全补丁中
- [ ] 6B.9 独立治理：接入企业身份、细粒度 RBAC、完整操作审计与独立供应商的外部 synthetic/dead-man 监控
- [ ] 6B.10 独立重构与压测：收敛不可变 `DocumentState`，建立 10 万任务规模基线与持续 fuzz/故障注入预算

## 7. Phase 6 — 分级自动化（仓库 FBIF资讯订阅，默认关）

- [ ] 7.1 自动公开策略开关（管理后台/env 可配）：原创 + pass + 图片全成功 + staging 成功 → 跳过人审直接 publicizing；warning 必人审；block 禁止通过
- [ ] 7.2 自动发布的文章在群通知中显著标注「自动发布」，附一键「翻回隐藏」入口（事后可逆）
- [ ] 7.3 🔴 用历史已审文章回放验证策略精度后，与用户共同决定是否开启
- [ ] 7.4 验收：开关关闭时行为与 Phase 5 完全一致；开启后 block/warning 路径不受影响

## 8. 收尾

- [ ] 8.1 两仓库 WORKLOG / CLAUDE.md 规则区同步（新端点、新红线：网站发布必经隐藏态与审核闸门）
- [ ] 8.2 运营交接文档：审核卡片怎么用、驳回后流程、故障时如何手动兜底
- [ ] 8.3 openspec 归档本变更（specs 同步进 openspec/specs/）
