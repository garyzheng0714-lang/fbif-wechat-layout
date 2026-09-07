# FBIF 公众号排版

[![P0 监控契约](https://github.com/garyzheng0714-lang/fbif-wechat-layout/actions/workflows/p0-monitoring-contract.yml/badge.svg)](https://github.com/garyzheng0714-lang/fbif-wechat-layout/actions/workflows/p0-monitoring-contract.yml)
![类型](https://img.shields.io/badge/%E7%B1%BB%E5%9E%8B-%E5%86%85%E5%AE%B9%E6%8E%92%E7%89%88%E4%B8%8E%E5%8F%91%E5%B8%83-2563eb)
![Go](https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white)
![前端](https://img.shields.io/badge/%E5%89%8D%E7%AB%AF-Vanilla%20JS-f7df1e?logo=javascript&logoColor=111827)
![状态](https://img.shields.io/badge/%E7%8A%B6%E6%80%81-%E7%94%9F%E4%BA%A7%E4%BD%BF%E7%94%A8%E4%B8%AD-16a34a)
![README](https://img.shields.io/badge/README-%E4%B8%AD%E6%96%87-brightgreen)

把 DOCX、Markdown、剪贴板正文或文章链接，排成可粘进微信公众号 / FoodTalks CMS 的 HTML；网站稿另走「隐藏草稿 → 飞书审核 → 公开」的受控流水线。

本仓库只做加工。RSS 监控在 `FoodTalks_Feed`，微信归档在 `fbif-wechat-article`。三者独立部署，只经 HTTP 通信，同稿用 `source_key` 幂等建一单。

## 功能要点

- **多种导入**：DOCX / Markdown / 纯文本、公众号或网页链接、飞书云文档（`@` 搜索或粘贴链接）。粘贴后按输入框全文立即排版。编辑器一次只处理一篇文章。
- **微信抓取**：去掉过期 `poc_token`，微信链接改用移动端 User-Agent；若落到验证码页，从页面抽出原文地址再抓。图注组 `图片 → 图注 → 图片来源` 连续保留，长图注不因样式被误判成正文。
- **双渠道排版**：公众号走 `/mp/<packId>`，网站走 `/ft/<packId>`。三套渲染器物理隔离，账号规则服务端绑定。
- **复制即用**：点击复制立即写入当前可粘贴 HTML；图片上传和「更多文章」在后台准备，不阻塞按钮。
- **受控发布**：上游只交链接。抓取 → 排版 → 转存图 → 风险检查 → FoodTalks 隐藏草稿 → 飞书审核。默认可人审公开；管理后台打开「自动发布」后，新同步且风险为 `pass|warning` 的稿可由系统批准上线。
- **发布后维护**：已发布稿可从原公众号回补覆盖（CMS 已有人工改稿则停）；可切微信导流尾、重出审核卡、结案失败单。

## 渠道与入口

打开 `/` 会无条件跳到 `/mp/fbif`。账号切换在左栏导入卡第一行，渠道只是菜单分组。

| 渠道 | 路径 | 页面 | 账号 |
| --- | --- | --- | --- |
| 公众号 | `/mp/<packId>` | `public/app.html` | `FBIF食品饮料创新`、`WowFood爆品超市`、`FoodTalks什么是系列` |
| 网站 | `/ft/<packId>` | `public/site.html` | `网站默认`（`ft-default`）、`网站·什么是系列`（`ft-series`） |

- 同模板账号原地切换，跨模板 / 跨渠道整页 reload。顶栏只放当前页面动作。
- `/app.html`、`/mp` 仍可作为公众号入口的过渡别名。
- 任意 `/ft/<id>` 回落到 `ft-default`，不会误用公众号规则包。
- 账号目录真相源是 `api/account_catalog.go`；改完后跑 `npm run gen:registry`，不要手改 `public/js/rule-packs/registry.js`。

## 快速开始

静态页面（只看界面、不测抓取）：

```bash
cd public
python3 -m http.server 8080
```

打开 `http://localhost:8080/app.html`。

完整后端（抓取、图片代理、旧 `.doc` 转换）：

```bash
cd api
go run . -port 9000
curl http://127.0.0.1:9000/api/health
```

编辑器入口：`http://127.0.0.1:9000/mp/fbif` 或 `http://127.0.0.1:9000/ft/ft-default`。

## 使用流程

1. 上传 DOCX / Markdown，或粘贴链接 / 正文。手输用 `⌘/Ctrl+Enter` 提交；粘贴会按输入框最终全文立即排版（`@` 飞书搜索除外）。
2. 核对预览：标题、正文、图注、署名、页脚。公众号还可核对「更多文章」卡片。
3. 按需裁剪图片、改页脚或换推荐卡。卡片「替换图片」优先用该卡自己的媒体库；旧卡缺媒体时会按链接补抓。
4. 点复制后立刻去目标后台粘贴。复制不会跳转；公众号后台只由页面上的显式链接打开。
5. 网站自动发布不经过复制：上游交链接后，成品进隐藏草稿，等飞书卡片或管理员审核。

### 快捷键

| 快捷键 | 操作 |
| --- | --- |
| `⌘/Ctrl + Enter` | 提交导入框。已有预览时，全局 handler 还会先复制旧预览，因此在导入框内会变成「复制旧稿 + 提交新稿」 |
| `⌘/Ctrl + V` | 在导入框粘贴后自动排版（`@` 飞书搜索除外） |
| `Esc` | 从预览返回上传页 |

## 当前能力

核对至 2026-09-07，以当前 `main` 代码为准。

### 排版与导入

- 解析 DOCX / Markdown，保留标题、正文、图片、超链接、列表、引用和参考来源。粘贴正文走同一套解析与渲染。
- 旧版 `.doc` 可经 LibreOffice 或 CloudConvert 转成 `.docx` 再解析。
- 飞书云文档走异步导出作业：服务端导出 Word、剥图并一次性镜像到 OSS；前端 long-poll 真实阶段进度；预览离屏备齐后整帧提交，导入期间旧稿保持可见。
- 原创 DOCX 以首个 `作者：` 行为正文起点；连续正文保持 Word 小段距，只有原文空段才恢复标准段后间距。
- URL 署名识别 `来源 / 出处 / 出品`、`作者 / 撰文 / 撰稿 / 文`、`编辑 / 责编`，分隔符支持 `：` `:` `/` `|` `｜`。转载稿移到正文开头，原创稿仍在「提示：」前；来源、作者、编辑独立成行，只渲染一次。正文有来源优先，否则用抓取到的 `source_name`，不会把 `byline` 补成作者。
- 默认行为下，作者为 `Mote莫特` / `Mote 莫特` 时，在「参考来源」上方插入固定署名图。
- 「更多文章」由规则开关控制；`default_cards` 只是新稿默认，单稿可换、删、排、裁。

### 微信文章抓取

- 抓取请求会去掉 `poc_token`（微信验证链接里的一次性令牌过期后会拦正文）。
- 微信文章改用移动端 MicroMessenger User-Agent；普通网页仍用桌面 Chrome UA。
- 若响应落在 `wappoc_appmsgcaptcha` 一类验证页，会抽出 `target_url`，去掉 `poc_token` 后回退再抓。
- 图片说明组必须连续渲染为图注；`图片来源` / `图源` 不得进正文或文章级来源。带样式的长图注也按图注保留。

### 网站渠道

- 横图默认宽 600；长图（高/宽 ≥ 1.7）固定 400；其他竖图按高 375 反推且宽度不低于 300；自然宽不超过 300 的近正方小图按二维码宽 150。preset v19、前端 fallback 与离线渲染已同值。
- 表格用共享 `table-renderer.js` 双写 HTML 属性与内容层，粘进 FoodTalks 后台被清洗内联样式后仍保留边框、对齐、表头和 Word 图片尺寸。
- 目录只在**发布组装层**写入：正文非空白字符 ≥ 3000 且 h2+h3 合计 ≥ 5 时，给标题 1（h2）打哈希确定性 `mctoc_*` 并写 CMS `toc`。标题 2（「1、2、3」或「（一）」→ h3）不进目录。交互式渲染器仍关闭随机目录。原创与转载一视同仁。
- 转载判定 AI 优先，`publishDeclaredSource` 作确定性兜底。转载声明《标题》优先链到公众号「阅读原文」；自家号转发且阅读原文为空时，先查 Feed 订阅列表，未订再走 TikHub 广域搜，标题和出处都对上才用。
- 访谈（两个及以上「角色：」前缀各现 ≥ 3 次）：提问方 FoodTalks 蓝加粗，回答方默认色加粗。
- 发布组装会按 AI 标签检索公司库，正文首次出现处注入最多 3 条 `mc-company-link`；作者为 Mote/莫特时，把正文里的公众号文章链换成已发布 FoodTalks `/news/{id}`，找不到就留原链。
- 进 FoodTalks 和云梭的正文一律切除微信导流尾。已公开泄漏稿可用 `repair-wechat-trailer` 覆盖，不重审。

### 发布流水线

- `site-sync` / `official-sync` 只收 `url`；`content_html` / `title` / `source_name` / `author` / `cover_url` 任一出现即 400。字段与状态码见 [发布接入契约](docs/publish-intake-contract.md)。
- 默认停在 `awaiting_review`。公开必须有持久 approve（人工，或自动发布开关打开后的 `system:auto_publish`）、时间、风险 `pass|warning`、版本匹配。开关默认关，只作用于新同步稿。
- 阻断级风险、同题已发布仍停待审核。CMS 手工稿为准，库存不得覆盖。`stagePublicize` 落公开意图前再查同题，命中或查询失败都 fail closed。
- 待审 / 已发 / 驳回 / 异常卡都有封面、粗标题、标签、摘要和预览/原文入口。首屏最多 3 条已证实实质项；待核收进完整报告。审核群只发卡片。
- 已发布稿可从飞书卡触发原文回补：重新抓取、排版后覆盖同一篇 FoodTalks 资讯；CMS 已有人工改稿则停止；PUT 字段沿用 CMS 现值。结果写在卡片操作下方。
- 外部分发只对已公开、审核版本有效的自有原创开放。云梭先冻结 payload；没有 `content_pk` 只人工对账，不盲发。
- 程序化渲染：Bearer `RENDER_TOKEN` 的 `/api/render`、`/api/html-to-blocks` 复用同一套解析与渲染，支持 `fbif` 和 `ft-default`。后者只转换、不建发布任务。

### 已知未闭环

这些问题仍在代码里，不能靠文档宣称完成：

- `site.html` 仍内联原生拖拽，没有复用 `mountBlockPanel` + Sortable（红线 22）。
- `stageRepublish` 尚无同题硬门。
- 只带 legacy `ReviewCardMsgID` 的旧任务，切群后仍可能回退更新旧群。
- 运行时允许「空白名单 + 群成员模式 + 当前审核群」授权卡片操作，但部署 env schema 仍要求 `FEISHU_REVIEWER_OPEN_IDS` 非空。
- 导入框 `⌘/Ctrl+Enter` 会同时复制旧预览并提交新内容。
- 真实 10MB 级多图飞书文档的生产耗时、以及 FoodTalks TinyMCE 对 `figure/figcaption` 的整批存活，仍缺闭环验收。

实例当前指向 `test` 还是 `production`，只能现场看 `/api/publish/readiness`，不要从 README 推断。

## 技术栈

| 层 | 实现 |
| --- | --- |
| 前端 | 原生 HTML / CSS / JavaScript ES modules |
| 后端 | Go 1.26.6，标准库 `net/http` |
| 渲染服务 | Node headless（`render-service/`） |
| 测试 | `node --test`、Playwright、Go test |
| 质量门禁 | PR 跑 `.github/workflows/p0-monitoring-contract.yml`（无路径过滤）；推 `main` 只构建并事务上线 |

```text
.
├── public/                 # 编辑器静态页与前端模块
│   ├── app.html            # 公众号工具（/mp/*）
│   ├── site.html           # 网站工具（/ft/*）
│   └── js/templates/       # fbif / foodtalks-site / foodtalks-series，互不 import
├── api/                    # Go API、规则、发布流水线、飞书
├── render-service/         # 程序化渲染
├── deploy/                 # Caddy、systemd、env schema、事务脚本
├── tests/                  # 前端模块测试
├── DESIGN.md               # 设计系统
└── docs/INDEX.md           # 文档入口
```

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | 健康检查 |
| `POST` | `/api/fetch-article` | 抓取文章正文；微信链接会去掉 `poc_token` 并用移动端 UA |
| `POST` | `/api/fetch-article-meta` | 标题、封面、规范链接和正文媒体图，供「更多文章」选图 |
| `GET` | `/api/image-proxy` | 代理图片，绕过浏览器跨域 |
| `POST` | `/api/oss-upload` | 图片上传；未配置上游时返回原 base64 |
| `POST` | `/api/wechat-upload` | 上传入口旧名 |
| `POST` | `/api/doc-to-docx` | 旧版 `.doc` 转 `.docx` |
| `GET` | `/api/doc-cache/{hash}/{filename}` | 读取转换过程缓存图 |
| `GET` | `/api/feishu/docs/search?q=&limit=` | 搜索当前用户可见的飞书云文档；需飞书登录 |
| `POST` | `/api/feishu/docs/export/jobs` | 创建异步导出作业 |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}?after=&wait_ms=` | long-poll 作业进度 |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}/result` | 取回已镜像图片的 DOCX |
| `DELETE` | `/api/feishu/docs/export/jobs/{job_id}` | 取消作业 |
| `POST` | `/api/feishu/docs/export` | 同步导出兼容入口；交互导入禁止使用 |
| `GET` | `/api/feishu/chats` | 当前用户所在群，供规则后台选审核群 |
| `GET` | `/auth/feishu/fbif/login` | 飞书 OAuth 登录 |
| `GET` | `/auth/feishu/fbif/callback` | 飞书 OAuth 回调 |
| `POST` | `/auth/password/login` | 可选本地密码登录 |
| `POST` | `/auth/feishu/logout` | 退出并清除会话 cookie |
| `GET` | `/api/me` | 当前用户、登录方式、角色与飞书能力 |
| `GET` | `/api/rules/active?account=<id>` | 读取账号当前规则；未传默认 FBIF |
| `GET` | `/api/rules/presets` | 规则预设目录 |
| `POST` | `/api/rules/admin/login` | 已退役，固定 `410 Gone` |
| `GET` / `POST` | `/api/rules/admin/presets` | 列出 / 保存预设；需管理员，写入带 `base_revision` |
| `PUT` | `/api/rules/admin/active` | 绑定账号当前规则 |
| `DELETE` | `/api/rules/admin/presets/{id}` | 删除预设 |
| `GET` | `/api/rules/admin/access-users` | 超级管理员读账号与角色 |
| `PUT` | `/api/rules/admin/access-users/{id}` | 超级管理员授权 / 收回管理员 |
| `POST` | `/api/render` | 程序化渲染，需 `Authorization: Bearer $RENDER_TOKEN` |
| `POST` | `/api/html-to-blocks` | HTML 转中立 Block[]，需 Bearer token，不建任务 |
| `POST` | `/api/publish/site-sync` | 只收链接；可选 `content_source_url` 只补「阅读原文」 |
| `POST` | `/api/publish/official-sync` | 同样只收链接；official 固定 `ordinary_confirmed` |
| `POST` | `/api/publish/sync-status` | 紧凑任务状态，不返回正文 |
| `GET` | `/api/publish/monitoring` | 跨系统监控摘要；processing / 通知未完成 / 卡失败均 `ready=false` |
| `POST` | `/api/publish/monitor-alert` | 转发监控端告警到独立飞书私聊；本服务不主动外检 |
| `GET` | `/api/publish/jobs` | 任务摘要 |
| `GET` | `/api/publish/jobs/{id}` | 任务详情 |
| `GET` | `/api/publish/readiness` | 部署只读验收 |
| `GET` / `PUT` | `/api/publish/delivery-settings` | 审核卡飞书应用、目标群与自动发布开关 |
| `POST` | `/api/publish/jobs/{id}/review` | HTTP 审核默认关闭 |
| `POST` | `/api/publish/jobs/{id}/retry` | 从失败阶段续跑 |
| `POST` | `/api/publish/jobs/{id}/unpublish` | 请求下线 |
| `POST` | `/api/publish/jobs/{id}/republish` | 请求重新公开已下线稿（尚无同题硬门） |
| `POST` | `/api/publish/jobs/{id}/reconcile` | 处理 CreateNews / UpdateNews 不确定态 |
| `POST` | `/api/publish/jobs/{id}/rerender-latest` | 用已存正文块按最新规则重排，不回源 |
| `POST` | `/api/publish/jobs/{id}/restage-current` | 丢掉他环境旧草稿号，在当前环境新建隐藏草稿 |
| `POST` | `/api/publish/jobs/{id}/abandon` | 无法继续的失败单结案 |
| `POST` | `/api/publish/jobs/{id}/ack-cms-published` | 网站已手工公开时，只记本地已发布并更新卡片，不覆盖正文 |
| `POST` | `/api/publish/jobs/{id}/card-refresh` | 按当前代码重出审核卡，默认原位更新 |
| `POST` | `/api/publish/jobs/{id}/risk-recheck` | 对待审任务重跑风险检查 |
| `POST` | `/api/publish/jobs/{id}/notification-replay` | 重放失败通知 |
| `POST` | `/api/publish/cms-resync` | 按北京日期补写已公开稿的 `isLatest` 与来源 ID |
| `POST` | `/api/publish/jobs/{id}/repair-wechat-trailer` | 单篇已公开稿切微信导流尾 |
| `POST` | `/api/publish/repair-wechat-trailer` | 扫描并修复可安全覆盖的泄漏稿 |
| `POST` | `/api/publish/jobs/{id}/retry-external-distribution` | 确认旧云梭稿作废后重建分发意图 |
| `POST` | `/api/feishu/card-callback` | 仅 `FEISHU_CALLBACK_MODE=webhook` 时开放 |
| `GET` | `/preview/publish/{id}?t=...` | HMAC 签名审核预览 |

发布管理接口需要管理员登录或 `PUBLISH_ADMIN_SERVICE_TOKEN`。机器同步用 `X-Publish-Sync-Token`（`PUBLISH_SYNC_SERVICE_TOKEN`）。原文回补走飞书卡片，不另开 HTTP 入口。

## 配置

完整机器可读合同在 `deploy/config/env-schema.json`。生产缺 `RULE_SUPER_ADMIN_OPEN_IDS` 必须启动失败。常用变量：

| 变量 | 说明 |
| --- | --- |
| `ENV` | 生产必须显式为 `production` |
| `FBIF_CONFIG_SCHEMA_VERSION` | 当前必须为 `1` |
| `WECHAT_UPLOAD_ENDPOINT` | 图片上传上游；未设置时仅 base64 预览 |
| `LIBREOFFICE_URL` / `LIBREOFFICE_TOKEN` | `.doc` 转换 |
| `CLOUDCONVERT_API_KEY` | LibreOffice 不可用时的 fallback |
| `AUTH_SESSION_SECRET` / `AUTH_USERS_PATH` | 登录会话与账号存储 |
| `FEISHU_LOGIN_APP_ID` / `FEISHU_LOGIN_APP_SECRET` / `FEISHU_LOGIN_REDIRECT_URI` | 网页飞书登录 |
| `RULES_STORE_PATH` | 规则 JSON；默认 `data/rule-presets.json` |
| `RENDER_TOKEN` / `RENDER_SERVICE_URL` | 程序化排版 |
| `PUBLISH_JOBS_PATH` | 发布任务 JSON；同一路径只能一个 API 进程写 |
| `PUBLISH_SYNC_SERVICE_TOKEN` | 上游提交、紧凑状态、监控端点 |
| `PUBLISH_ADMIN_SERVICE_TOKEN` | 部署 / 运维管理接口 |
| `FOODTALKS_FEED_BASE_URL` | 查转载原文；默认 `https://feed.foodtalks.cn` |
| `TIKHUB_API_KEY` / `TIKHUB_API_BASE_URL` | Feed 未订该号时的广域搜补链；失败只降级 |
| `FOODTALKS_BASE_URL` / `FOODTALKS_TOKEN` | FoodTalks API |
| `FOODTALKS_FRONT_URL` / `FOODTALKS_ADMIN_ARTICLE_URL` | 前台 / 后台文章入口 |
| `FOODTALKS_CATEGORY_IDS` | 原创分类，默认 `52,58` |
| `FOODTALKS_REPOST_CATEGORY_IDS` | 转载分类，默认 `53` |
| `FOODTALKS_EXPECTED_TARGET` | 必须为 `test` 或 `production`，并与 URL 一致 |
| `DEPLOYMENT_WRITES_PAUSED` | 候选实例为 `1` 时暂停写入 |
| `FEISHU_APP_ID` / `FEISHU_APP_SECRET` / `FEISHU_REVIEW_CHAT_ID` | 审核通知 |
| `FEISHU_CALLBACK_MODE` | 必须显式 `websocket` 或 `webhook` |
| `FEISHU_REVIEWER_OPEN_IDS` | 审核人白名单；部署 schema 仍要求非空 |
| `FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS` | 开启后，当前审核群内的卡片操作即有审核权 |
| `ARK_API_KEY` / `ARK_MODEL_ID` | AI 预填与风险检查；失败降为 `warning` |
| `SITE_OWN_ACCOUNTS` | 自有账号清单，用于判断原创 / 转载 |
| `YUNSUO_BASE_URL` / `YUNSUO_USER_CODE` / `YUNSUO_API_KEY` | 云梭外发，仅已审核自有原创 |
| `RULE_SUPER_ADMIN_OPEN_IDS` | 超级管理员飞书 `open_id` 白名单 |

`REDFOX_API_KEY`、`LAYOUT_MONITOR_URL` 等旧键不再读取，只为旧 `publish.env` 过渡保留。不要把密钥提交到仓库。验证器只输出变量名，不输出值。

## 测试

```bash
npm test
cd api && go test ./...
```

PR 必须通过无路径过滤的 P0 工作流：Go fmt/vet/staticcheck/race/govulncheck、JS 语法与 import cycle、npm audit、ShellCheck、Python 编译与 env schema。浏览器 e2e 只考 Chrome，安装结果按 lockfile 缓存。

## 部署

推 `main` 触发 `.github/workflows/deploy.yml`，不再重跑考试、不装浏览器：

1. 用 Go 1.26.6 构建 `api/fbif-api`，写入 `public/version.json`。
2. 把静态资源、API、render service、Caddy、systemd、env schema 同步进隔离 release；候选 API 强制 `DEPLOYMENT_WRITES_PAUSED=1`。
3. 校验三类 env、单元和当前服务后，事务切换 live。
4. 验公网 commit SHA、健康、鉴权和 `/api/publish/readiness`。
5. 写暂停窗口内只清理已退役的本仓库 watchdog 与审核群监控播报；外检由 `FoodTalks_Feed` 负责。回滚也不恢复这两类单元。
6. 验收后先关自动回滚，再解暂停；之后失败只向前修。
7. `finalize` 保留最多 5 份历史快照，不含 `.env` / `publish.env` / `render-service.env`。

四个入口（`/`、index / app / version）须 `no-cache`。改权限后验 `/api/me`。

规则后台：在 GitHub Secret `RULE_SUPER_ADMIN_OPEN_IDS` 维护超级管理员；其登录后在「规则后台 → 权限管理」把已登录账号升为管理员。旧共享密码接口已关闭。

## 维护说明

- 文档入口：[`docs/INDEX.md`](docs/INDEX.md)。稳定能力以当前代码、README、长期契约、ADR 和 `openspec/specs/` 为准；`docs/worklogs/` 与 `openspec/changes/` 是过程记录。
- `DESIGN.md` 是界面与预览页的设计约束；改 UI 前先读，不得偏离。
- `behavior` 只允许署名 / 来源、Mote 图、原创 / 转载页脚等受限动作，不是任意 JavaScript。
- 复制兼容处理在 `engine.js`、`clipboard.js`、`css-inline.js`、`uploader.js`、`more-articles.js`。`copyContent()` 不得等待上传或卡片合成。
- 微信块由 `api/wechat_blocks.go` 输出；`source_name` 必须原字段名传到公众号渲染器。相关测试：`api/wechat_blocks_test.go`、`api/article_meta_test.go`、`tests/editor-shell.test.mjs`、`tests/fbif-template.test.mjs`。
- 新增账号：先写入 `api/account_catalog.go` 与 `api/rule_defaults.go`，再 `npm run gen:registry`。`FoodTalks什么是系列` 必须显式覆盖 `modules.bottom_blocks`，否则会错渲 FBIF 页脚。
