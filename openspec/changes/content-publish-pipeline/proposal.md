# Proposal: 公众号 → FoodTalks 网站内容自动化发布流水线

## Why

FBIF 公众号发布的文章目前靠人工搬运到 foodtalks.cn：人打开排版工具粘贴 → 复制 → 去网站后台粘贴、传图、发布。两个平台都是自己的、网站有发布 API、排版工具已经 API 化（`/api/render` 2026-07-05 上线），中间缺的只是一条「发现新文章 → 网站排版 → 隐藏发布 → 飞书审核 → 公开 → 群通知」的编排链路。把这条链路打通后，人从搬运工变成审核员；后期低风险文章可以逐步跳过人工审核全自动发布。

**本变更是纯计划产物（用户明确要求：只写详尽计划，不执行任何实际发布/发消息操作）。所有对外调用（FoodTalks API 冒烟、飞书发消息、微信拉文章）都在 tasks 中列为待用户批准后执行的步骤。**

## 关键前提（调研已确认的事实）

| 事实 | 出处 |
|---|---|
| 排版工具已有 `POST /api/render`（Block[] → 内联样式 HTML）+ `POST /api/html-to-blocks` + `POST /api/fetch-article`（公众号 URL → Block[]），生产已部署（systemd `fbif-render`） | 本仓库 `WORKLOG-render-api.md`、`render-service/` |
| 但 render-service **只支持 `packId=fbif`（微信模板）**，网站模板 `foodtalks-site.js` 未接入 | `render-service/server.mjs:59` |
| 「FBIF资讯订阅」系统（`feed.foodtalks.cn`，Go + PG）已有完整发布流水线基建：`publish_jobs` 状态机、`internal/wechat`（silenceper SDK：token/传图/草稿/freepublish）、`internal/feishu`（文本私信）、`internal/imagecache`（qpic 防盗链下载）、豆包优化客户端；`target` 字段已预留 `foodtalks_site` | 资讯订阅仓库 `openspec/changes/archive/2026-07-05-agent-publish-pipeline/` |
| FoodTalks 发布 API：`POST/PUT https://api-we-test.foodtalks.cn/news/admin/news`（**测试环境**、抓包整理），有 `publishStatus` / `isPublic` 字段可支撑「先隐藏发布」；响应结构、字段枚举、图片上传接口**均未知** | 飞书 wiki `PM9EwLPrAiD6mQkuDYWcAOPUnWb` |
| FBIF 订阅号 AppID `wx72d6d6526f2c8e8b`（AppSecret 在飞书文档，已认证、有开发者凭证） | 飞书 docx `APh0dhTSCojMhZx0g6HcKcu6nkf` §5 |
| 飞书应用 `cli_aac5d107d9381bdf` 具备 im 发消息全套权限（含 send_as_bot / 群消息）+ bitable + docs；审核卡片回调所需的事件订阅需在开放平台另行配置 | 用户提供的权限清单 |

## What Changes

- **排版工具（本仓库）**：render-service 新增 `packId=ft-default` 支持——用 `foodtalks-site.js` 渲染器输出网站 HTML，必须带上 CMS 存活契约（表格 `outerBorder`+`cmsSurvival` 双写、图片 width 属性）与网站页脚 bottom_blocks；`fbif.js` 与微信链路字节级不动（红线 #19、#23）。
- **资讯订阅系统（跨仓库 `FBIF资讯订阅`）**：
  - 新增 `internal/foodtalks` 发布客户端（news/admin/news 创建/编辑，先隐藏后公开，幂等去重）。
  - `publish_jobs` 流水线新增 `foodtalks_site` 目标链路：公众号文章 URL → fetch-article → render(ft-default) → 图片转存 → 隐藏发布 → 飞书审核 → 公开 → 群通知。
  - `internal/wechat` 新增 `freepublish/batchget` 已发布文章轮询（自动发现新文章），配去重表；手动输入 URL 为兜底与 MVP 主路径。
  - `internal/feishu` 新增：审核交互卡片（通过/驳回按钮 + 预览链接）、卡片回调端点、发布结果群消息。
- **不改动**：公众号发布链路（已有 wechat_mp target）、排版工具人工前端、热点判定/采集逻辑一律不动。

## Capabilities

### New Capabilities

- `layout-render-site`: 排版工具 render API 的网站模板渲染能力——`packId=ft-default` 输入契约、foodtalks-site 渲染器接入、CMS 存活输出、网站页脚规则（**实现在本仓库**）。
- `foodtalks-site-publishing`: FoodTalks 网站发布集成——鉴权、隐藏态创建、编辑、公开翻转、幂等键、图片转存到网站可用存储（实现在资讯订阅仓库）。
- `mp-article-ingestion`: 公众号已发文章的发现与去重——freepublish 轮询 + 手动 URL 两种触发、去重表、多图文拆分（实现在资讯订阅仓库）。
- `site-sync-pipeline`: 公众号→网站同步流水线编排——阶段状态机、幂等重试、人工确认闸门、分级自动化策略（实现在资讯订阅仓库）。
- `feishu-review-gateway`: 飞书审核与通知——审核交互卡片、回调处理、发布结果群通知（实现在资讯订阅仓库）。

### Modified Capabilities

（无——本仓库现有 `wechat-draft-survival-copy`、`wechat-url-block-classification` 两个 spec 的需求不变；网站渲染是纯新增路径。）

## Impact

- **本仓库**：`render-service/render.mjs`、`server.mjs`（模板路由）、`api/render_proxy.go`（透传 packId）、`tests/`（网站渲染冒烟 + CMS 存活断言）。
- **资讯订阅仓库**：新包 `internal/foodtalks`、`internal/wechat` 扩展、`internal/publishing` 新链路、`internal/feishu` 卡片能力、新表迁移（去重表 + publish_jobs 扩列）、`internal/api` 回调与触发端点。
- **外部依赖 / 阻塞项**（Phase 0 必须先解决）：
  - FoodTalks API 正式环境地址与**服务级长效鉴权**（现在是抓包的个人登录 token，会过期）；响应结构与 `publishStatus`/`isPublic` 枚举；**图片上传接口**（正文 mmbiz 图片必须转存，文档中没有此接口——硬阻塞）。
  - 微信侧：`freepublish/batchget` 对「人工后台发布/群发」文章的覆盖度需实测（订阅号「群发」与「发布」是两种动作，前者可能拉不到）；服务器出口 IP 加公众号白名单。
  - 飞书侧：审核群建好后机器人入群拿 chat_id；卡片回调需配置事件订阅。
- **安全**：飞书 App Secret 已在对话中明文出现过——**上线前必须在开放平台重置**；FoodTalks 抓包 token、微信 AppSecret 一律 env-only，不进 git/日志/多维表格。
