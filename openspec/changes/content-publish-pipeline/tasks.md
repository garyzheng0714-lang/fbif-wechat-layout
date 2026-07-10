# Tasks: 公众号 → FoodTalks 网站内容自动化发布流水线

> ⚠️ 执行纪律（用户 2026-07-10 指令）：带 🔴 的任务包含对外真实调用（FoodTalks 写入 / 飞书发消息 / 微信 API），必须经用户批准后执行；其余任务是本地代码与文档工作。任何阶段声明完成前，先跑本组验收项。跨仓库任务已标注仓库。

## 1. Phase 0 — 安全与外部前提（人工 + 确认类，先做）

- [ ] 1.1 🔴 在飞书开放平台重置应用 `cli_aac5d107d9381bdf` 的 App Secret（对话中已明文暴露），新值只进服务器 env
- [ ] 1.2 向 FoodTalks 网站后端确认：正式环境 Base URL、服务级长效鉴权（替换抓包个人 token）、响应体结构与错误码、`publishStatus`/`isPublic`/`sourceType` 完整枚举
- [ ] 1.3 向 FoodTalks 网站后端索要（或抓包 CMS 编辑器补齐）图片上传接口——正文与封面转存的硬阻塞项；拿不到则评估自有 OSS+CDN 过渡方案（需先验证 CMS 不清洗外域 img src）
- [ ] 1.4 与运营确认 FBIF 订阅号日常发文动作是「群发」还是「发布」（决定 Phase 5 轮询可行性）；确认不在公众号后台启用「服务器配置」的红线共识
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

- [ ] 3.1 `render-service/render.mjs`：按 fbif 同款 globals 桩方式接入 `foodtalks-site.js` 渲染器，梳理其额外 DOM/全局依赖并补桩
- [ ] 3.2 `render-service/server.mjs`：packId 白名单加 `ft-default`，按模板路由；未知 packId 保持 400 显式失败
- [ ] 3.3 接通网站规则：渲染前拉 `GET /api/rules/active?account=ft-default` 灌入 `_activeConfig`（复用 fbif 链路的缓存与降级），页脚走 bottom_blocks 主路径
- [ ] 3.4 确认 CMS 存活开关（table `outerBorder`+`cmsSurvival`、图片 width 双写）在 API 渲染路径生效
- [ ] 3.5 新增 `tests/render-service-site.test.mjs`：样例 blocks → 断言网站模板特征、表格 CMS 存活（复用模拟清洗器）、页脚存在、fbif 输出逐字节不变
- [ ] 3.6 用一篇真实公众号文章（fetch-article → render ft-default）与人工 `/ft/` 排版结果做结构 diff，验证同源（复刻类任务以成品为真值）
- [ ] 3.7 `npm test` + `cd api && go test ./...` 全绿；部署（deploy.yml 无需新 env）；线上 curl 冒烟 ft-default
- [ ] 3.8 验收：线上 `/api/render packId=ft-default` 对样例文章出网站 HTML；红线 #19/#23 回归测试全绿

## 4. Phase 3 — 资讯订阅 MVP 闭环：手动 URL → 隐藏发布 → 文本通知（仓库 FBIF资讯订阅）

- [ ] 4.1 新包 `internal/foodtalks`：Client（CreateNews/UpdateNews）、News 结构体按 Phase 1 实测契约定义、401/403 凭证过期专项错误、env 配置（BASE_URL/TOKEN 缺失 fail loud）
- [ ] 4.2 `internal/foodtalks` 单测：请求体序列化、幂等锚点判断、错误映射（httptest 桩）
- [ ] 4.3 图片转存：复用 `imagecache.FetchAndStore` 下载 + 新增网站存储上传（按 Phase 0 拿到的接口），逐图串行重试一次，original→new 映射落 job
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
