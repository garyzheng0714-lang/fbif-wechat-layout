# fbif-wechat-layout

[![P0 契约](https://github.com/garyzheng0714-lang/fbif-wechat-layout/actions/workflows/p0-monitoring-contract.yml/badge.svg)](https://github.com/garyzheng0714-lang/fbif-wechat-layout/actions/workflows/p0-monitoring-contract.yml)
![类型](https://img.shields.io/badge/%E7%B1%BB%E5%9E%8B-%E5%86%85%E5%AE%B9%E6%8E%92%E7%89%88%E4%B8%8E%E5%8F%91%E5%B8%83-2563eb)
![Go](https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white)
![前端](https://img.shields.io/badge/%E5%89%8D%E7%AB%AF-Vanilla%20JS-f7df1e?logo=javascript&logoColor=111827)
![状态](https://img.shields.io/badge/%E7%8A%B6%E6%80%81-%E7%94%9F%E4%BA%A7%E4%BD%BF%E7%94%A8%E4%B8%AD-16a34a)
![README](https://img.shields.io/badge/README-%E4%B8%AD%E6%96%87-brightgreen)

把 DOCX、Markdown、剪贴板正文或文章链接，排成能在微信公众号和 FoodTalks CMS 里存活的 HTML；网站稿再走「隐藏草稿 → 飞书审核 → 公开」的受控流水线。

本仓库只做加工。资讯发现在 `FoodTalks_Feed`，微信归档在 `fbif-wechat-article`。三套代码和存储互相独立，只经 HTTP 通信。

> 事实以当前源码为准。本文核对至 2026-08-31（含 [回补终态审核卡封面 #137](https://github.com/garyzheng0714-lang/fbif-wechat-layout/pull/137)）。生产环境指向以现场 `/api/publish/readiness` 为准，不从文档推断。

## 功能要点

**导入与排版**

- 解析 DOCX / Markdown / 粘贴正文，保留标题、图片、列表、引用和参考来源。
- 粘贴公众号或网页链接后由 Go API 抓取；失败走备用读取路径。
- 飞书云文档走异步导出作业：服务端剥图并一次镜像到 OSS，前端 long-poll 真实进度，离屏备齐后整帧上屏。
- 粘贴即排版：导入框粘贴后按最终全文立即提交；手输用 `⌘/Ctrl + Enter`。
- 编辑器一次只处理一篇文章。五个账号、三套物理隔离的渲染器：公众号 `fbif`、网站 `foodtalks-site`、系列 `foodtalks-series`。

**复制与存活**

- 复制按钮立即写入当前可粘贴 HTML，不等待图片上传或「更多文章」合成。
- 公众号复制层遵守微信草稿 T0 约束（图床、位图占位、异步回写）。
- 网站表格双写 HTML 属性与内容层，尽量扛住 FoodTalks TinyMCE 洗样式。

**受控发布**

- 上游只交链接。`site-sync` / `official-sync` 带正文、标题、来源、作者或封面一律 400。
- 抓取 → 排版 → 转图 → AI 预填与风险检查 → FoodTalks 隐藏草稿 → 停在 `awaiting_review`。
- 默认要飞书卡片或管理员 `approve` 才公开。管理后台「发布链路 → 自动发布」打开后，新同步且风险为 `pass|warning` 的稿可由系统 `system:auto_publish` 上线；阻断级、同题已发布仍停待审核。
- 待审 / 已发 / 驳回 / 异常卡都带封面、标题、标签、摘要和预览/原文。自动发布若取消了待审核通知，终态卡会回补封面。
- 同日、同投递通道的稿可聚合成一张日汇总卡。审核群只发卡片，不发运维播报。

## 入口与账号

打开 `/` 会无条件跳到 `/mp/fbif`。账号菜单在左栏导入卡第一行，渠道只是分组：同模板原地切，跨模板整页 reload。

| 账号 | 路径 | 渲染器 |
| --- | --- | --- |
| FBIF食品饮料创新 | `/mp/fbif` | `fbif` |
| WowFood爆品超市 | `/mp/wowfood` | `fbif`（复用公众号模板，独立 preset） |
| FoodTalks什么是系列 | `/mp/foodtalks-series` | `foodtalks-series` |
| 网站默认 | `/ft/ft-default` | `foodtalks-site` |
| 网站·什么是系列 | `/ft/ft-series` | `foodtalks-site`（独立 preset） |

任意 `/ft/<id>` 回落到 `ft-default`。老地址 `/app.html`、`/mp` 仍可用。

账号先登记在 `api/account_catalog.go`，再 `npm run gen:registry` 生成 `public/js/rule-packs/registry.js`，生成文件不手改。

## 用法

### 交互式排版

1. 打开对应账号入口，用左栏菜单确认账号。
2. 上传 DOCX / Markdown，或把链接、正文、飞书文档贴进导入框（`@` 搜索飞书文档不会自动提交）。
3. 看预览：正文、图注、署名、页脚、「更多文章」。
4. 需要时裁剪图片、换「更多文章」封面，或在规则后台改主题 / 页脚 / 默认推荐。
5. 点复制，立刻粘到微信或 FoodTalks 后台。公众号后台只由页面上的显式链接打开，复制本身不跳转。

网站渠道没有胶囊和「更多文章」，输出带 inline style 的 HTML，给 FoodTalks 后台粘贴。

### 网站排版口径（已实现）

- 图：`figure.image > img.mc-img + figcaption`。横图 600；长图（高/宽 ≥ 1.7）固定 400；其他竖图按高 375 反推且宽不低于 300；自然宽不超过 300 的近正方小图按二维码宽 150。preset v19、前端与 `render.mjs` 离线 fallback 已同值。
- 图注 12px `#888888`；`图片 → 图注 → 图片来源` 连续归图注，不进正文或文章级来源。
- 标题：`一、二、三` → h2，`（一）` / `1、2、3` → h3；署名前缀不升标题；同文本 ≤16 字无序号标题全篇出现 ≥2 次当装饰行删除。
- 转载：AI 优先判定出处，代码兜底读头部/尾部「来源/出处」声明。开头已有网站署名则删文末公众号口径来源/作者，否则降级 14px 灰斜体。转载《标题》优先链公众号「阅读原文」；自家号转发且原文为空时先查 Feed 订阅，未订再 TikHub 广域搜，对不上不链回自家转载页。
- 访谈：两个及以上「角色：」前缀各现 ≥3 次时，提问方 FoodTalks 蓝+粗，回答方默认色+粗。
- 目录只在**发布组装层**写：正文非空白 ≥3000 且 h2+h3 合计 ≥5 时，给一级 h2 写哈希确定性 `mctoc_*` 并填 CMS `toc`。条目只收标题 1 / h2。交互式渲染器关闭随机目录。
- Mote / 莫特文发布时，正文里的公众号文章链尽量换成已发布 FoodTalks `/news/{id}`；找不到就留原链。交互复制仍可保留公众号链。
- AI 标签只在 `tag_type_id=6` 里等值匹配，最多 3 个，不自动建标签；命中公司库时在正文首次出现处加 `mc-company-link`，每篇最多 3 家。

### 快捷键

| 快捷键 | 操作 |
| --- | --- |
| `⌘/Ctrl + V` | 在导入框粘贴后自动排版（`@` 飞书搜索除外） |
| `⌘/Ctrl + Enter` | 提交导入框。已有预览时，全局 handler 还会先复制旧预览，等于「复制旧稿 + 提交新稿」，是已知缺口 |
| `Esc` | 从预览回到上传页 |

## 受控发布

```text
上游只交 URL
    → 抓取 / 排版 / 转图
    → AI 预填封面、摘要、标签（不改正文 HTML）
    → 风险检查
    → FoodTalks 隐藏草稿
    → awaiting_review
    → 飞书卡人工通过，或自动发布开关下的系统 approve
    → 公开
```

同一来源 URL 用 `source_key` 幂等只建一单。公开必须有持久 approve、时间、风险 `pass|warning`、版本匹配，否则 fail closed。CMS 手工稿优先，流水线库存不得覆盖。首次公开前会再查同题；命中或查询失败都停。`stageRepublish` 还没有这道硬门。

**审核卡（含 #137）**

- 待审、已发布、已驳回、异常都走同一套 hero：封面、粗标题、标签、摘要、预览/原文。旧稿没有 `ReviewCoverImgKey` 则无图单列，不留空图位。
- 待审核卡和终态卡共用封面上传入口。自动发布可能先取消待审核通知，终态卡会自行回补封面后再发出。
- 同日、同一投递应用和审核群的稿会聚合成日汇总卡，风险项放在文章列表下方。
- `card-refresh` 只换样式、不重跑检查。任务已有 `ReviewCardMsgIDs` 时：当前群有 id 就原位更新，没有就发新卡。只带旧字段 `ReviewCardMsgID` 的任务仍可能回退更新旧群。

**机器接入**

字段、状态码与幂等见 [发布接入契约](docs/publish-intake-contract.md)。鉴权头是 `X-Publish-Sync-Token`，对应 `PUBLISH_SYNC_SERVICE_TOKEN`。

**外部分发**

FoodTalks 收原创和转载。云梭只发已公开、审核版本有效且确认为自有原创的稿；目标账号服务端固定。没有 `content_pk` 只人工对账，不盲重发。

**监控**

本仓库提供 `GET /api/publish/monitoring`，不主动探外部服务。processing、通知 pending/sending、卡片缺失/失败或对账积压时 `ready=false`。外检在 `FoodTalks_Feed`。

## 快速开始

静态页（只看界面、不调后端）：

```bash
cd public
python3 -m http.server 8080
```

打开 `http://localhost:8080/app.html`。

完整能力（抓取、图片代理、旧 `.doc` 转换、发布接口）：

```bash
cd api
go run . -port 9000
curl http://127.0.0.1:9000/api/health
```

前端测试 `npm test`，Go 测试 `cd api && go test ./...`。

## 当前实现与边界

- **规则**存在 `/api/rules/*`。localStorage 只做离线降级。自建规则必须显式选继承基线，不再默认串到 FBIF。
- **底部区块数据**各渠道共用 `modules.bottom_blocks`，视觉仍由三套渲染器隔离。`site.html` 还是内联面板和原生拖拽，没有复用 `mountBlockPanel` + Sortable，交互尚未统一。
- **网站 URL 导入**一律按转载处理：先切微信导流尾，再出网站转载声明。`ft-default` 完整页脚在全新浏览器默认开启。
- **进 FoodTalks / 云梭的正文**一律切微信导流尾。已公开泄漏稿可用 `repair-wechat-trailer` 覆盖，不重审。
- **程序化渲染**：Bearer `RENDER_TOKEN` 的 `/api/render` 与 `/api/html-to-blocks` 复用同一套解析和渲染，支持 `fbif` 和 `ft-default`。后者只转换、不建发布任务。
- **JSON store** 带进程锁，同一 `PUBLISH_JOBS_PATH` 只能一个 API 进程写。
- **飞书大图导入**的生产耗时（约 10MB、多图）尚未做排期验收。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | 健康检查 |
| `POST` | `/api/fetch-article` | 抓取文章正文 |
| `POST` | `/api/fetch-article-meta` | 标题、封面、规范链接和正文媒体图，给「更多文章」选图 |
| `GET` | `/api/image-proxy` | 代理图片 |
| `POST` | `/api/oss-upload` | 图片上传；未配置上游时回原 base64 |
| `POST` | `/api/wechat-upload` | 旧名称兼容入口 |
| `POST` | `/api/doc-to-docx` | 旧 `.doc` 转 `.docx` |
| `GET` | `/api/doc-cache/{hash}/{filename}` | 转换过程缓存图 |
| `GET` | `/api/feishu/docs/search?q=&limit=` | 飞书云文档搜索，需登录 |
| `POST` | `/api/feishu/docs/export/jobs` | 创建异步导出作业 |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}?after=&wait_ms=` | long-poll 进度 |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}/result` | 取回已镜像图片的 DOCX |
| `DELETE` | `/api/feishu/docs/export/jobs/{job_id}` | 取消作业 |
| `POST` | `/api/feishu/docs/export` | 同步导出兼容口；交互导入禁止走这里 |
| `GET` | `/api/feishu/chats` | 当前用户所在群，供选审核群 |
| `GET` | `/auth/feishu/fbif/login` | 飞书 OAuth；`return_to` 由签名 state 约束 |
| `GET` | `/auth/feishu/fbif/callback` | OAuth 回调 |
| `POST` | `/auth/password/login` | 可选本地密码登录 |
| `POST` | `/auth/feishu/logout` | 退出 |
| `GET` | `/api/me` | 当前用户、角色与飞书能力 |
| `GET` | `/api/rules/active?account=<id>` | 当前规则；未传 account 时默认 FBIF |
| `GET` | `/api/rules/presets` | 规则预设目录 |
| `POST` | `/api/rules/admin/login` | 已退役，固定 `410 Gone` |
| `GET` / `POST` / `DELETE` | `/api/rules/admin/presets` | 管理员读写预设；写入带 `base_revision`，冲突 `409` |
| `PUT` | `/api/rules/admin/active` | 绑定账号当前规则 |
| `GET` / `PUT` | `/api/rules/admin/access-users` | 超级管理员改后台角色 |
| `POST` | `/api/render` | 程序化渲染，Bearer `RENDER_TOKEN` |
| `POST` | `/api/html-to-blocks` | HTML 转中立 Block[]，不建任务 |
| `POST` | `/api/publish/site-sync` | 只收链接 + 可选 `rule_pack_id` / 分类证据 / `content_source_url` |
| `POST` | `/api/publish/official-sync` | 同上；official 固定 `ordinary_confirmed` |
| `POST` | `/api/publish/sync-status` | 紧凑任务状态，不含正文 |
| `GET` | `/api/publish/monitoring` | 跨系统监控摘要 |
| `POST` | `/api/publish/monitor-alert` | 转发监控端告警到飞书私聊 |
| `GET` | `/api/publish/jobs` | 任务摘要 |
| `GET` | `/api/publish/jobs/{id}` | 任务详情 |
| `GET` | `/api/publish/readiness` | 部署只读验收 |
| `GET` / `PUT` | `/api/publish/delivery-settings` | 审核卡应用、目标群、自动发布开关 |
| `POST` | `/api/publish/jobs/{id}/review` | HTTP 审核默认关；approve 只落 `publicizing` 后立即返回 |
| `POST` | `/api/publish/jobs/{id}/retry` | 从失败阶段续跑 |
| `POST` | `/api/publish/jobs/{id}/unpublish` | 下线 |
| `POST` | `/api/publish/jobs/{id}/republish` | 重新公开已下线稿（尚无同题硬门） |
| `POST` | `/api/publish/jobs/{id}/reconcile` | 人工对账 CreateNews / UpdateNews |
| `POST` | `/api/publish/jobs/{id}/rerender-latest` | 用已存块按最新规则重排，不回源 |
| `POST` | `/api/publish/jobs/{id}/restage-current` | 丢掉他环境旧草稿号，在当前环境新建隐藏草稿 |
| `POST` | `/api/publish/jobs/{id}/card-refresh` | 按当前代码重出审核卡；终态也会回补封面 |
| `POST` | `/api/publish/jobs/{id}/risk-recheck` | 真正重跑风险检查，不是只换样式 |
| `POST` | `/api/publish/jobs/{id}/notification-replay` | 重放失败通知 |
| `POST` | `/api/publish/cms-resync` | 按北京日期补写已公开稿的 `isLatest` 与来源 ID |
| `POST` | `/api/publish/jobs/{id}/repair-wechat-trailer` | 单篇已公开稿切导流尾并覆盖 |
| `POST` | `/api/publish/repair-wechat-trailer` | 批量修复可安全覆盖的泄漏稿 |
| `POST` | `/api/publish/jobs/{id}/retry-external-distribution` | 确认旧云梭稿作废后重建分发意图 |
| `POST` | `/api/feishu/card-callback` | 仅 `FEISHU_CALLBACK_MODE=webhook` 时开放 |
| `GET` | `/preview/publish/{id}?t=...` | HMAC 审核预览 |

发布管理接口需管理员登录或 `PUBLISH_ADMIN_SERVICE_TOKEN`。同步令牌只能提交、读紧凑状态和监控，读不到完整任务或审核接口。

## 配置

完整机器可读合同在 `deploy/config/env-schema.json`。验证器只输出变量名，不输出值。生产缺 `RULE_SUPER_ADMIN_OPEN_IDS` 必须启动失败。

| 变量 | 说明 |
| --- | --- |
| `ENV` | 生产必须显式 `production` |
| `FBIF_CONFIG_SCHEMA_VERSION` | 必须为 `1` |
| `WECHAT_UPLOAD_ENDPOINT` | 图片上传上游；未设时网站 URL 转图会失败 |
| `LIBREOFFICE_URL` / `LIBREOFFICE_TOKEN` | 旧 `.doc` 转换 |
| `CLOUDCONVERT_API_KEY` | LibreOffice 不可用时的 fallback |
| `PUBLISH_ADMIN_SERVICE_TOKEN` | 运维令牌，不下发浏览器 |
| `RULE_SUPER_ADMIN_OPEN_IDS` | 超级管理员飞书 `open_id`，只放秘密配置 |
| `AUTH_SESSION_SECRET` / `AUTH_USERS_PATH` | 登录会话；生产密钥至少 24 字节 |
| `FEISHU_LOGIN_APP_ID` / `FEISHU_LOGIN_APP_SECRET` / `FEISHU_LOGIN_REDIRECT_URI` | 网页飞书登录 |
| `RULES_STORE_PATH` | 规则 JSON；默认 `data/rule-presets.json` |
| `RENDER_TOKEN` / `RENDER_SERVICE_URL` | 程序化排版 |
| `PUBLISH_JOBS_PATH` | 发布任务 JSON |
| `PUBLISH_SYNC_SERVICE_TOKEN` | Feed / 归档端的窄权限令牌 |
| `FOODTALKS_FEED_BASE_URL` | 查转载原文；默认 `https://feed.foodtalks.cn` |
| `TIKHUB_API_KEY` / `TIKHUB_API_BASE_URL` | 可选。Feed 未订该号且没有阅读原文时，按标题+出处广域搜；失败只降级 |
| `REDFOX_API_KEY` | 不再读取，只为旧 env 过渡保留 |
| `PUBLISH_HTTP_REVIEW_ENABLED` / `PUBLISH_HTTP_REVIEWER_OPEN_ID` | HTTP 审核应保持关闭 |
| `FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS` | 开启后，当前审核群内的卡片操作即有审核权 |
| `FOODTALKS_BASE_URL` / `FOODTALKS_TOKEN` | FoodTalks API |
| `FOODTALKS_FRONT_URL` / `FOODTALKS_ADMIN_ARTICLE_URL` / `FOODTALKS_CATEGORY_IDS` | 前台、后台与原创分类，须指向同一环境 |
| `FOODTALKS_REPOST_CATEGORY_IDS` | 转载分类，默认 `53` |
| `FOODTALKS_EXPECTED_TARGET` | 必须是 `test` 或 `production`，并与 URL 一致 |
| `DEPLOYMENT_WRITES_PAUSED` | 候选实例为 `1` 时暂停写入、恢复任务、通知 worker 和飞书长连 |
| `FEISHU_APP_ID` / `FEISHU_APP_SECRET` / `FEISHU_REVIEW_CHAT_ID` | 审核通知 |
| `FEISHU_NOTIFICATION_APPS_JSON` / `PUBLISH_DELIVERY_SETTINGS_PATH` | 多应用目录与投递选择（含自动发布） |
| `PUBLISH_MONITOR_FEISHU_USER_ID` | 监控告警私聊收件人 |
| `FEISHU_CALLBACK_MODE` | 必须显式 `websocket` 或 `webhook` |
| `FEISHU_REVIEWER_OPEN_IDS` | 审核人白名单。运行时可以「空白名单 + 群成员模式 + 当前群」授权卡片；env schema 仍要求非空，这项尚未收敛 |
| `ARK_API_KEY` / `ARK_MODEL_ID` / `ARK_BASE_URL` | AI 预填与风险检查；失败降级为 `warning`，不挡进人工审核 |
| `SITE_OWN_ACCOUNTS` | 自有账号清单，用于判原创/转载 |
| `YUNSUO_BASE_URL` / `YUNSUO_USER_CODE` / `YUNSUO_API_KEY` | 云梭；只发已审自有原创 |
| `YUNSUO_TARGET_ACCOUNTS_JSON` / `YUNSUO_CATEGORIES` | 服务端固定目标；callback 不接受任意账号 |

部署还用 GitHub Secrets：`SERVER_SSH_KEY`、`SERVER_KNOWN_HOSTS`、`SERVER_HOST`、`SERVER_USER`、`DEPLOY_PATH` 等。`SERVER_KNOWN_HOSTS` 必须人工核验，工作流不再 `ssh-keyscan`。不要把飞书、发布令牌或服务器密钥提交进仓库。

## 部署

PR 必须过无路径过滤的 `.github/workflows/p0-monitoring-contract.yml`。推 `main` 只触发 `.github/workflows/deploy.yml` 发版，不再重考、不装浏览器。

1. 用 Go 1.26.6 构建 `api/fbif-api`，写入 `public/version.json`。
2. 把 public、API、render service、Caddy、systemd、env schema 同步进隔离 release；候选 API 强制 `DEPLOYMENT_WRITES_PAUSED=1`。
3. 校验三类 env、单元和当前服务后，事务切换。
4. 核验公网 SHA、健康、鉴权和 `/api/publish/readiness`。
5. 写暂停窗口里幂等摘除已退役的本仓库 watchdog 与审核群监控播报；回滚也不恢复它们。
6. 验收后先关自动回滚，再解暂停。此后失败只向前修。
7. `finalize` 把产物留到 `/var/www/.fbif-layout-history/<deploy-id>/`，最多 5 份，不含密钥文件。

四个入口（`/`、index、app、version）须 `no-cache`。改权限后验 `/api/me`。

规则后台：超级管理员 open_id 只放 Secret → 登录后把已登录账号提成管理员 → 网页只认会话角色。运维脚本用独立的 `PUBLISH_ADMIN_SERVICE_TOKEN`。

## 已知未闭环

这些问题要另做功能修复和真实验收，不能靠文档宣称完成。完整验收阻断见 [FoodTalks URL 导入验收基准](docs/agents/foodtalks-url-import-acceptance.md)。

- `site.html` 页脚仍是内联原生拖拽，未复用 `mountBlockPanel` + Sortable。
- `stageRepublish` 没有同题硬门。
- 只带 legacy `ReviewCardMsgID` 的旧任务，切群后仍可能更新旧群。
- 群成员审核的运行时授权与 env schema「白名单必填」尚未收敛。
- 导入框 `⌘/Ctrl + Enter` 会同时复制旧预览并提交新内容。
- ADR 中立层仍留有 FBIF 专用兼容字面量（如 parser 读 legacy 协议名）。
- `figure/figcaption` 经 FoodTalks TinyMCE 的真实存活、以及 2026-08-14 排版事故批次中的表格 / 图题 / 小标题，仍缺整批实证。
- 微信历史文章总量仍是未完成、未核验；单一接口或单表计数都不是全量。

## 技术栈

- 前端：原生 HTML / CSS / JavaScript ES modules
- 后端：Go 1.26.6，标准库 `net/http`
- 渲染：`render-service/` Node headless
- 测试：`node --test`、Playwright、Go test；PR 考 JS 语法与 import cycle、Go fmt/vet/staticcheck/race/govulncheck、npm audit、ShellCheck、Python 编译与 env schema

## 项目结构

```text
.
├── public/
│   ├── index.html                  # 根重定向到 /mp/fbif
│   ├── app.html                    # 公众号工具 /mp/*
│   ├── site.html                   # 网站工具 /ft/*
│   ├── js/
│   │   ├── engine.js               # 复制与页脚协调
│   │   ├── editor-shell.js         # 外壳、账号、快捷键
│   │   ├── account-menu.js         # 单层账号菜单
│   │   ├── parser.js               # DOCX / Markdown
│   │   ├── templates/              # fbif / foodtalks-site / foodtalks-series，互不 import
│   │   └── rule-packs/registry.js  # 由 account_catalog.go 生成，勿手改
│   └── css/wx-theme.css
├── api/
│   ├── main.go                     # API + 静态文件 + 渠道路由
│   ├── account_catalog.go          # 账号目录
│   ├── publish_*.go                # 发布任务、审核卡、自动发布
│   ├── feishu_export_jobs.go       # 飞书异步导出
│   └── wechat_blocks.go            # 微信文章块
├── render-service/
├── deploy/                         # Caddy、systemd、env schema、事务脚本
├── tests/
├── DESIGN.md
└── README.md
```

## 维护

文档入口：[docs/INDEX.md](docs/INDEX.md)。稳定能力以 README、长期契约、ADR、`openspec/specs/` 和当前代码为准；`docs/worklogs/` 与 `openspec/changes/` 是过程记录。

界面约束见 [DESIGN.md](DESIGN.md)。编辑部元数据见 [editorial-norms-foodtalks.md](docs/editorial-norms-foodtalks.md)，FoodTalks API 差异见 [foodtalks-api-contract.md](docs/foodtalks-api-contract.md)。工程红线见 [CLAUDE.md](CLAUDE.md)。

`behavior` 只允许署名、来源、Mote 图、原创/转载页脚这类受限动作，不是任意 JavaScript。改网站渲染器时 `templates/fbif.js` 字节不动。
