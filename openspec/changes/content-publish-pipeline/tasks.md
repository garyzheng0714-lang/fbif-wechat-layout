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

## 4. Phase 3 — 资讯订阅 MVP 闭环：手动 URL → 隐藏发布 → 文本通知（仓库 FBIF资讯订阅）

- [x] 4.1 新包 `internal/foodtalks`：Client（CreateNews/UpdateNews）、News 结构体按 Phase 1 实测契约定义、401/403 凭证过期专项错误、env 配置（BASE_URL/TOKEN 缺失 fail loud）（2026-07-10 资讯订阅仓 04d39ac：27 字段契约对齐、id 二态 RawMessage、publishStatus 客户端自律 0/1/2、网关/凭证/业务错误三分类）
- [x] 4.2 `internal/foodtalks` 单测：请求体序列化、幂等锚点判断、错误映射（httptest 桩）（2026-07-10 同提交：9 项全绿，含「HTTP 200 业务失败」真实样例与枚举拒发零请求断言）
- [ ] 4.3 图片转存：调排版工具 `POST /api/oss-upload` urls 模式一次批量（服务端已内网并发，无需 imagecache 逐图下载），响应缺失的 URL 重试一次仍缺则 job failed；original→new 映射落 job；封面 coverImg 同通道（2026-07-10 方案已实测定型，见 design D4 与契约 §8）
- [ ] 4.4 `publish_jobs` 迁移：新增 `source_key`（唯一索引）、`source_url`、`foodtalks_news_id`、`review_card_msg_id`、`risk_notes` 列；版本化迁移脚本
- [ ] 4.5 `internal/publishing` 新增 site 链路 stage 序列（fetching→rendering→mirroring→staging→awaiting_review→publicizing）；推进器按 target 分发，wechat_mp 路径零改动（diff 审查确认）
- [ ] 4.6 LayoutClient 扩展：Render 支持 packId 参数（ft-default）
- [ ] 4.7 风险提示节点：复用 OptimizeClient 骨架新建红线检查 prompt（功效表达/转载授权/竞品评价/数据来源），输出 pass/warning/block + 条目证据，解析失败降级 warning
- [ ] 4.8 API：`POST /api/publish/site-sync`（URL 校验 + 去重 + 建 job）、`GET /api/publish/jobs/{id}`（复用）、`POST /api/publish/jobs/{id}/review`（通过/驳回，RBAC 白名单）
- [ ] 4.9 飞书文本通知（降级路径）：awaiting_review 时向审核群发文本（标题/类型/风险/预览与原文链接）；publicizing 终态发结果通知；群维度发送能力补进 `internal/feishu`
- [ ] 4.10 单测：状态机推进/续跑幂等/同 source_key 二次触发不建新文/block 不可通过
- [ ] 4.11 🔴 测试环境全链路真机验收：贴真实公众号 URL → 隐藏文章生成 → 群收到通知 → 前端/接口点通过 → 文章公开 → 群收到结果；再触发同 URL 验证幂等
- [ ] 4.12 🔴 生产部署：手动 psql 跑迁移 → 部署 → env 配齐（FOODTALKS_*、RENDER 相关已有）→ 用一篇真实文章走通全链路（停在隐藏态，人工确认后公开）
- [ ] 4.13 验收：MVP 链路生产可用；误发不可能（公开只能经审核动作）；WORKLOG 实时回写

## 5. Phase 4 — 飞书交互卡片审核（仓库 FBIF资讯订阅）

- [ ] 5.1 按 feishu-cardkit 规范搭审核卡片 JSON（标题/来源/类型/风险列表/预览与原文按钮/「通过并公开」「驳回」）；🔴 真机预览卡片渲染
- [ ] 5.2 配置飞书事件订阅（`card.action.trigger`）：评估回调 URL vs 长连接模式，按部署环境落地
- [ ] 5.3 回调处理端点:校验操作人白名单 → 执行通过/驳回 → 更新原卡片为终态（操作人+链接）→ 防重复操作
- [ ] 5.4 替换 Phase 3 文本降级路径为卡片（文本能力保留作故障降级）
- [ ] 5.5 🔴 真机验收：卡片按钮走完通过/驳回/无权限三条路径，卡片状态更新正确
- [ ] 5.6 验收:审核动作全程不离开飞书；无权限点击不改状态

## 6. Phase 5 — 公众号自动发现（仓库 FBIF资讯订阅）

- [ ] 6.1 🔴 实测 `freepublish/batchget`：拉 FBIF 订阅号最近已发布列表，核对运营最近 N 篇（群发/发布两种动作）是否都在——结论写入 design Open Questions
- [ ] 6.2 `internal/wechat` 增加 `BatchGetPublished`（分页、频控退避）
- [ ] 6.3 去重表迁移 + poller 定时任务（间隔可配默认 15 分钟）：新文章逐篇（多图文拆 index）建 job；轮询失败记录人话日志不扩散
- [ ] 6.4 🔴 灰度：自动发现只建 job 到 awaiting_review（与手动一致），观察一周误报/漏报后再考虑默认开启
- [ ] 6.5 验收：公众号新发一篇 → 15 分钟内审核群收到卡片，无重复任务

## 7. Phase 6 — 分级自动化（仓库 FBIF资讯订阅，默认关）

- [ ] 7.1 自动公开策略开关（管理后台/env 可配）：原创 + pass + 图片全成功 + staging 成功 → 跳过人审直接 publicizing；warning 必人审；block 禁止通过
- [ ] 7.2 自动发布的文章在群通知中显著标注「自动发布」，附一键「翻回隐藏」入口（事后可逆）
- [ ] 7.3 🔴 用历史已审文章回放验证策略精度后，与用户共同决定是否开启
- [ ] 7.4 验收：开关关闭时行为与 Phase 5 完全一致；开启后 block/warning 路径不受影响

## 8. 收尾

- [ ] 8.1 两仓库 WORKLOG / CLAUDE.md 规则区同步（新端点、新红线：网站发布必经隐藏态与审核闸门）
- [ ] 8.2 运营交接文档：审核卡片怎么用、驳回后流程、故障时如何手动兜底
- [ ] 8.3 openspec 归档本变更（specs 同步进 openspec/specs/）
