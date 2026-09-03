# FBIF 内容排版与发布

![类型](https://img.shields.io/badge/%E7%B1%BB%E5%9E%8B-%E5%86%85%E5%AE%B9%E6%8E%92%E7%89%88%E4%B8%8E%E5%8F%91%E5%B8%83-2563eb)
![Go](https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white)
![前端](https://img.shields.io/badge/%E5%89%8D%E7%AB%AF-Vanilla%20JS-f7df1e?logo=javascript&logoColor=111827)
![状态](https://img.shields.io/badge/%E7%8A%B6%E6%80%81-%E7%94%9F%E4%BA%A7%E4%BD%BF%E7%94%A8%E4%B8%AD-16a34a)
![README](https://img.shields.io/badge/README-%E4%B8%AD%E6%96%87-brightgreen)

把 DOCX、Markdown、剪贴板正文或文章链接排成可直接进入微信公众号或 FoodTalks CMS 的 HTML，并为网站稿提供「隐藏草稿 → 飞书审核 → 公开」的受控流水线。编辑只需导入、核对、复制或送审；品牌样式、渠道兼容和发布风险由系统兜住。

本仓库只做加工。RSS/供应商监控在 `FoodTalks_Feed`，微信归档与阅读指标在 `fbif-wechat-article`。三者代码、数据库和服务器独立，只经 HTTP 通信；同一来源由 `source_key` 幂等只建一单。

## 功能亮点

- **多来源导入**：DOCX、旧版 `.doc`、Markdown、纯文本、网页链接、微信文章、飞书云文档走同一套解析与渲染。粘贴后按输入框最终全文立即排版；仅 `@` 搜索飞书文档不自动提交。
- **渠道隔离排版**：公众号走 `/mp/<packId>`（`app.html`），网站走 `/ft/<packId>`（`site.html`）。三套渲染器物理隔离，互不 import。左栏导入卡第一行是单层账号菜单，渠道只是分组。
- **图注连续成组**：`图片 → 图注 → 图片来源` 必须连在一起，不得拆进正文或文章级来源。URL 导入时，带独立样式的长图注（小字号、灰色、居中）会继续当作图注，不会因为偏长或句式像正文就被抬成段落；服务端已判定为正文的超限文本，网站渲染器也不会再二次提升为图注。
- **署名一次、位置固定**：识别来源 / 出处 / 出品、作者 / 撰文 / 撰稿 / 文、编辑 / 责编（中英文冒号与 `/|｜`）。转载稿在正文开头只渲染一次，原创稿仍在「提示：」前只渲染一次；有值才成行。正文来源优先，否则用公众号名称 `source_name`，不会把 `byline` 补成作者。
- **复制立即反馈**：点击复制立刻写入当前可粘贴 HTML，不跳转后台。图片上传和「更多文章」在后台准备；复制层仍有未转存图时提示「图片准备中」，完成后自动恢复。
- **微信草稿与 CMS 存活**：公众号复制层守住图床、位图占位和更多文章卡片，避免保存草稿时被清洗。网站表格用 HTML 属性 + 内容层双写，粘进 FoodTalks 后台后边框、对齐、表头和 Word 图宽仍在。
- **受控发布**：上游只交链接。系统抓取、排版、转存图、AI 预填与风险检查后写入 FoodTalks 隐藏草稿，停在待审核。默认要飞书卡片或管理员 `approve` 才公开；管理后台「发布链路 → 自动发布」打开后，新同步且风险为 `pass|warning` 的稿可由系统持久化批准后上线。已在待审核的稿不受开关影响。
- **审核卡能认出文章**：待审、已发、驳回、异常卡都有封面、粗标题、标签、摘要和预览 / 原文入口。终态卡复用待审时转存的封面，回调时不现场传图。
- **已发布可回源覆盖**：符合条件的已公开稿，审核卡上可点「重新同步至网站」：重新抓取公众号最新正文，按当前规则排版，覆盖同一篇 FoodTalks 资讯，不新建。结果写在按钮下方。后台已有人工改稿、手工发布稿或已发起外部分发时停止覆盖，并保留 CMS 现有元数据字段。
- **网站组装**：进 FoodTalks / 云梭的正文一律切除微信导流尾。目录在发布组装层写入：正文非空白字符 ≥3000 且 h2+h3 合计 ≥5 时生成确定性 `mctoc_*` 与 CMS toc，条目只收标题 1（h2）。标签最多 3 个，只在 `tag_type_id=6` 里等值匹配。Mote 系列正文里的公众号互推链会尽量换成已发布站内 `/news/{id}`。

## 给谁用

- **主用户**：FBIF 公众号编辑、FoodTalks 网站编辑。桌面浏览器里连续处理多篇稿，几分钟内得到可进目标后台的成品。
- **次要用户**：维护账号规则的内容负责人，以及在飞书卡片上做审核、下线、回源同步或外发确认的人。

当前 5 个账号：`FBIF食品饮料创新`、`WowFood爆品超市`（公众号，复用 fbif 模板）、`FoodTalks什么是系列`（公众号，独立 foodtalks-series 模板）、`网站默认 ft-default` 与 `网站·什么是系列 ft-series`（网站渠道，foodtalks-site 模板）。账号先登记在 `api/account_catalog.go`，再 `npm run gen:registry` 生成前端注册表，生成文件不手改。

## 入口

- 打开 `/` 无条件重定向到 `/mp/fbif`。
- 公众号：`/mp/<packId>`，由 `public/app.html` 服务。
- 网站：`/ft/<packId>`；任意 `/ft/<id>` fallback 到 `ft-default`。独立页面，无胶囊、无「更多文章」，输出 inline-style HTML 给 FoodTalks 后台粘贴。
- 账号切换在左栏导入卡第一行（`mountAccountMenu` → `#acctMenu`）。同模板原地切，跨模板 / 跨渠道整页 reload。顶栏只放当前页面动作。
- `/app.html`、`/mp` 仍是公众号工具的过渡 alias。

## 使用

### 交互式排版

1. 在左栏选账号，把 DOCX / Markdown / 链接 / 正文贴进导入框（`aria-label="导入内容"`）。粘贴后自动排版；手输用 `⌘/Ctrl+Enter` 提交。
2. 核对预览：标题、正文、图注、署名、表格、「更多文章」（公众号）或网站页脚。
3. 原创 DOCX 从首个 `作者：` 行起读，之前的编辑材料默认不进预览。作者行蓝色、不斜体。
4. URL 导入时，图注应是浅灰小字并贴在图下；文章级来源、作者、编辑只出现一次。
5. 按需裁剪图片、换「更多文章」封面或开关页脚。「替换图片」优先用该卡自己的媒体库；旧卡缺媒体时按链接补抓。
6. 点「复制全文」后立刻去目标后台粘贴。复制不打开后台；后台入口只在复制成功后以显式链接出现。
7. 在微信公众号或 FoodTalks CMS 做最后预览再发布。

### 机器送审

`FoodTalks_Feed` / `fbif-wechat-article` 只向 `POST /api/publish/site-sync` 或 `official-sync` 交链接，鉴权头 `X-Publish-Sync-Token`。`content_html` / `title` / `source_name` / `author` / `cover_url` 任一出现即 400。可选 `content_source_url` / `original_source_url` 只补出处，不替代抓取。字段与状态码见 [发布接入契约](docs/publish-intake-contract.md)。

审核人在飞书卡片上看封面和风险结论，通过、驳回、下线、重新公开或「重新同步至网站」。公开必须有持久 approve（人工，或自动发布开关下的 `system:auto_publish`）、时间、风险 `pass|warning` 且版本匹配，否则 fail closed。CMS 手工稿优先，流水线库存不得覆盖。

## 网站渠道排版口径

网站规则参考已移植的 [VanilMsou/FoodtalksArticleLayout](https://github.com/VanilMsou/FoodtalksArticleLayout)：

- `ft-default` 的 `<h2>` / `<h3>` 和正文 `<p>` 两端对齐；`ft-series` 在 `justify_paragraphs=false` 时去掉这层 inline style。
- 空行显式输出 `<p><br /></p>`。
- 图片与图注输出 `figure.image > img.mc-img + figcaption`。横图默认 600；长图（高/宽 ≥ 1.7）固定宽 400；其他竖图按高 375 反推且宽度不低于 300；自然宽不超过 300 的近正方小图按二维码宽 150。表格内 DOCX 图按 Word 显示宽度。
- 图注 12px、`#888888`，按关键词识别（图片来源 / 资料来源 / 数据来源 / 来源 / 制图 / 截图 / 图注 / 注 / Source / Photo / Image / Credit）。
- 标题等级：`一二三 + 中文标点` → h2，`（一）（二）` 或 `1、2、3` → h3；署名行前缀不升标题。同文本 ≤16 字无序号标题全篇出现 ≥2 次，整行当账号装饰删除。
- 转载稿开头已有网站署名时，删除文末公众号口径来源 / 作者；否则降级 14px 灰斜体。转载声明《标题》优先链到公众号「阅读原文」；自家号转发且阅读原文为空时先查 Feed 订阅，未订再 TikHub 广域搜，标题 + 出处都对上才用，核验不过不链回自家转载页。
- 访谈（两个及以上「角色：」前缀各现 ≥3 次）：提问方 FoodTalks 蓝 + 粗，回答方默认色 + 粗。
- 与 CJK 邻接的 ASCII 标点 `,:;?!().` 转中文形态，保留小数和域名里的点。
- DOCX 首个「作者：」行之前的前置材料默认剥离。
- 表格：蓝底白字表头 + 浅灰网格、全格居中、列宽按 Word gridCol；`border` / `cellpadding` / `align` / `bgcolor` / `width` 与表头 `<strong>` 双写，保证 CMS 清洗内联样式后不丢样。

## 当前状态

代码与长期契约核对至 **2026-09-03**（`main` @ `9e8a45e`，含 #144 长样式图注、#141 转载署名、#139/#140/#142 回源同步、#137 终态审核卡封面）。

- **底部区块数据已统一，网站交互尚未收口**：各渠道共用 `modules.bottom_blocks`，视觉仍由三套渲染器隔离。`site.html` 仍是内联面板和原生拖拽，没有复用 `mountBlockPanel` + Sortable，违反红线 22，不能把「数据共用」写成「交互已经统一」。
- **飞书云文档导入走异步作业**：导出、剥图与图片镜像在服务端完成，前端 long-poll 真实阶段进度；正文图一次批量镜像到 OSS。预览离屏备齐后整帧提交，导入期间旧稿保持可见。
- **程序化渲染**：Bearer 鉴权的 `/api/render` 与 `/api/html-to-blocks` 复用编辑器同一套解析与渲染，支持 `fbif` 和 `ft-default`。后者只转换、不建发布任务。
- **风险分层**：首屏最多 3 条显式 `confirmed` 实质项；转载授权 / system 全部折进待核。审核群只发卡片，不发运维播报。
- **外部分发正交**：FoodTalks 仍收原创与转载。只有已公开、审核版本有效且确认为自有原创的任务，才能经卡片确认后走云梭。没有 `content_pk` 只人工对账，不盲重发。
- **环境状态不写死**：代码支持 `test|production`。当前实例指向必须现场核验 `/api/publish/readiness`，不从 README 或旧日志推断。

**已知未闭环（不可靠文档宣称完成）：**

- `stageRepublish` 尚无同题硬门，暂不可发布。
- 网站页脚交互未复用 `mountBlockPanel` + Sortable。
- 只带 legacy `ReviewCardMsgID` 的旧任务切群后仍可能回退更新旧群。
- `FEISHU_REVIEWER_OPEN_IDS` 的 env schema 仍要求非空，与运行时「空白名单 + 群成员模式」尚未收敛。
- 已有预览时，在导入框按 `⌘/Ctrl+Enter` 会先复制旧预览再提交新内容。

完整验收阻断见 [FoodTalks URL 导入验收基准](docs/agents/foodtalks-url-import-acceptance.md)。这些问题要另做功能修复，不能靠文档闭环。

## 技术栈

- 前端：原生 HTML / CSS / JavaScript ES modules
- 后端：Go 1.26，标准库 `net/http`
- 测试：Node.js `node --test`、Playwright、Go test，并设覆盖率棘轮
- 质量门禁（PR 的 P0 工作流）：JS 语法与 import cycle、Go fmt/vet/staticcheck/race/govulncheck、npm audit、ShellCheck、Python 编译与 env schema；浏览器只考 Chrome
- 部署：推 `main` 只构建并事务上线，不再重考、不再装浏览器

## 项目结构

```text
.
├── public/                 # 编辑器静态资源
│   ├── index.html          # 根重定向到 /mp/fbif
│   ├── app.html            # 公众号工具（/mp/*）
│   ├── site.html           # 网站工具（/ft/*）
│   └── js/templates/       # fbif / foodtalks-site / foodtalks-series，互不 import
├── api/                    # Go API、规则、抓取、发布流水线、飞书
├── render-service/         # Node headless 渲染
├── deploy/                 # Caddy、systemd、env schema、事务脚本
├── tests/                  # 前端模块测试
├── DESIGN.md               # 设计与交互约束
└── docs/INDEX.md           # 文档入口
```

账号目录在 `api/account_catalog.go`，规则默认在 `api/rule_defaults.go`，微信块提取在 `api/wechat_blocks.go`，发布任务在 `api/publish_*.go`。更细的文件职责见仓库内注释与 [文档索引](docs/INDEX.md)。

## 快速开始

仅看界面、不依赖后端：

```bash
cd public
python3 -m http.server 8080
```

打开 `http://localhost:8080/app.html`。

跑抓取、图片代理、旧 `.doc` 转换：

```bash
cd api
go run . -port 9000
curl http://127.0.0.1:9000/api/health
```

前端模块测试：`npm test`。Go API 测试：`cd api && go test ./...`。

## 快捷键

| 快捷键 | 操作 |
| --- | --- |
| `⌘/Ctrl + Enter` | 提交导入框内容；已有预览时全局 handler 还会先复制旧预览 |
| `⌘/Ctrl + V` | 在导入框粘贴后自动排版（`@` 飞书搜索除外） |
| `Esc` | 从预览返回上传页 |

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | 健康检查 |
| `POST` | `/api/fetch-article` | 抓取文章正文并返回结构化内容或 Markdown |
| `POST` | `/api/fetch-article-meta` | 标题、封面、规范链接和正文媒体图，供「更多文章」选图 |
| `GET` | `/api/image-proxy` | 代理图片，绕过浏览器跨域 |
| `POST` | `/api/oss-upload` | 图片上传；未配置上游时返回原 base64 |
| `POST` | `/api/wechat-upload` | 兼容旧名称的图片上传入口 |
| `POST` | `/api/doc-to-docx` | 旧版 `.doc` 转 `.docx` |
| `GET` | `/api/doc-cache/{hash}/{filename}` | 读取转换过程中缓存的图片 |
| `GET` | `/api/feishu/docs/search?q=&limit=` | 按关键词搜索当前用户可见的飞书云文档 |
| `POST` | `/api/feishu/docs/export/jobs` | 创建飞书导出作业，立即返回 `job_id` |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}?after=&wait_ms=` | long-poll 作业进度，上报真实 `phase` / `done` / `total` |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}/result` | 取回已镜像正文图的 DOCX |
| `DELETE` | `/api/feishu/docs/export/jobs/{job_id}` | 取消作业 |
| `POST` | `/api/feishu/docs/export` | 同步导出兼容入口；交互式导入禁止使用 |
| `GET` | `/api/feishu/chats` | 当前用户所在群，供规则后台选审核群 |
| `GET` | `/auth/feishu/fbif/login` | 飞书 OAuth 登录 |
| `GET` | `/auth/feishu/fbif/callback` | 飞书 OAuth 回调 |
| `POST` | `/auth/password/login` | 可选的本地密码登录 |
| `POST` | `/auth/feishu/logout` | 使当前会话失效 |
| `GET` | `/api/me` | 当前用户、登录方式、角色与飞书能力 |
| `GET` | `/api/rules/active?account=<id>` | 读取指定账号当前规则；未传时默认 FBIF |
| `GET` | `/api/rules/presets` | 规则预设目录 |
| `POST` | `/api/rules/admin/login` | 已退役，固定 `410 Gone` |
| `GET` | `/api/rules/admin/presets` | 列出规则预设（管理员） |
| `POST` | `/api/rules/admin/presets` | 保存规则预设；过期写入 `409 revision_conflict` |
| `PUT` | `/api/rules/admin/active` | 绑定账号当前规则；过期写入 `409 binding_revision_conflict` |
| `DELETE` | `/api/rules/admin/presets/{id}` | 删除规则预设 |
| `GET` | `/api/rules/admin/access-users` | 超级管理员读取已登录账号与角色 |
| `PUT` | `/api/rules/admin/access-users/{id}` | 超级管理员授权 / 收回管理员 |
| `POST` | `/api/render` | 程序化渲染，需 `Authorization: Bearer $RENDER_TOKEN` |
| `POST` | `/api/html-to-blocks` | HTML 转中立 Block[]，需 Bearer token，不建任务 |
| `POST` | `/api/publish/site-sync` | 只收链接；带正文旁路字段一律 400 |
| `POST` | `/api/publish/official-sync` | 同样只收链接；官方通道固定 `ordinary_confirmed` |
| `POST` | `/api/publish/sync-status` | 批量紧凑任务状态，不返回正文 |
| `GET` | `/api/publish/monitoring` | 跨系统监控摘要；processing 或通知未完成时 `ready=false` |
| `POST` | `/api/publish/monitor-alert` | 转发监控端告警到独立飞书私聊 |
| `GET` | `/api/publish/jobs` | 发布任务摘要 |
| `GET` | `/api/publish/readiness` | 部署只读验收 |
| `GET` / `PUT` | `/api/publish/delivery-settings` | 审核卡飞书应用、目标群与自动发布开关 |
| `GET` | `/api/publish/jobs/{id}` | 发布任务详情 |
| `POST` | `/api/publish/jobs/{id}/review` | HTTP 审核默认关闭 |
| `POST` | `/api/publish/jobs/{id}/retry` | 从失败阶段续跑 |
| `POST` | `/api/publish/jobs/{id}/unpublish` | 请求下线 |
| `POST` | `/api/publish/jobs/{id}/republish` | 请求重新公开已下线稿（尚无同题硬门） |
| `POST` | `/api/publish/jobs/{id}/reconcile` | 人工处理不确定的 CreateNews / UpdateNews 结果 |
| `POST` | `/api/publish/jobs/{id}/rerender-latest` | 用已存正文块按最新规则重排，不回源 |
| `POST` | `/api/publish/jobs/{id}/restage-current` | 丢掉他环境旧草稿编号，在当前环境新建隐藏草稿 |
| `POST` | `/api/publish/jobs/{id}/card-refresh` | 用当前代码重出审核卡，只换样式不重跑检查 |
| `POST` | `/api/publish/jobs/{id}/risk-recheck` | 对待审任务重跑风险检查并更新卡片 |
| `POST` | `/api/publish/jobs/{id}/notification-replay` | 重放失败通知 |
| `POST` | `/api/publish/cms-resync` | 按北京日期补写已公开稿的 `isLatest` 与来源 ID |
| `POST` | `/api/publish/jobs/{id}/repair-wechat-trailer` | 对单篇已公开稿切除微信导流尾并覆盖 |
| `POST` | `/api/publish/repair-wechat-trailer` | 扫描并修复可安全覆盖的已公开泄漏稿 |
| `POST` | `/api/publish/jobs/{id}/retry-external-distribution` | 确认旧云梭稿作废后重建分发意图 |
| `POST` | `/api/feishu/card-callback` | 仅 `FEISHU_CALLBACK_MODE=webhook` 时开放 |
| `GET` | `/preview/publish/{id}?t=...` | HMAC 签名的审核预览 |

已发布稿的「重新同步至网站」走飞书卡片回调，不另开 HTTP 旁路。`card-refresh` 在任务已有 `ReviewCardMsgIDs` 时，当前群有 id 就原位更新、没有就发新卡。

## 配置

后端按需读取以下环境变量。完整机器可读合同在 `deploy/config/env-schema.json`。

| 变量 | 说明 |
| --- | --- |
| `ENV` | 生产必须显式 `production` |
| `FBIF_CONFIG_SCHEMA_VERSION` | 当前必须为 `1` |
| `WECHAT_UPLOAD_ENDPOINT` | 图片上传上游；未设置时仅 base64 预览降级 |
| `LIBREOFFICE_URL` / `LIBREOFFICE_TOKEN` | `.doc` 转 `.docx` |
| `CLOUDCONVERT_API_KEY` | LibreOffice 不可用时的 fallback |
| `PUBLISH_ADMIN_SERVICE_TOKEN` | 部署 / 运维调用发布管理接口的机器令牌 |
| `RULE_SUPER_ADMIN_OPEN_IDS` | 超级管理员飞书 `open_id` 白名单；生产缺失必须启动失败 |
| `AUTH_SESSION_SECRET` / `AUTH_USERS_PATH` | 登录会话密钥与账号存储 |
| `FEISHU_LOGIN_APP_ID` / `FEISHU_LOGIN_APP_SECRET` / `FEISHU_LOGIN_REDIRECT_URI` | 网页飞书登录 |
| `RULES_STORE_PATH` | 规则预设 JSON；默认 `data/rule-presets.json` |
| `RENDER_TOKEN` / `RENDER_SERVICE_URL` | 程序化排版鉴权与 Node 渲染服务 |
| `PUBLISH_JOBS_PATH` | 发布任务 JSON 存储 |
| `PUBLISH_SYNC_SERVICE_TOKEN` | 上游调用提交 / 状态 / 监控的窄权限令牌 |
| `FOODTALKS_FEED_BASE_URL` | 向 Feed 查转载原文；缺省 `https://feed.foodtalks.cn` |
| `TIKHUB_API_KEY` / `TIKHUB_API_BASE_URL` | 可选。Feed 未订该号时广域搜补「阅读原文」 |
| `FOODTALKS_BASE_URL` / `FOODTALKS_TOKEN` | FoodTalks API；缺失时发布任务显式失败 |
| `FOODTALKS_FRONT_URL` / `FOODTALKS_ADMIN_ARTICLE_URL` / `FOODTALKS_CATEGORY_IDS` | 前台、后台入口与原创分类，必须同一环境 |
| `FOODTALKS_REPOST_CATEGORY_IDS` | 转载分类，默认 `53` |
| `FOODTALKS_EXPECTED_TARGET` | 必须 `test` 或 `production`，并与 FoodTalks URL 一致 |
| `DEPLOYMENT_WRITES_PAUSED` | 候选实例为 `1` 时暂停发布 / rules 写入、恢复任务、通知 worker 与飞书长连 |
| `FEISHU_APP_ID` / `FEISHU_APP_SECRET` / `FEISHU_REVIEW_CHAT_ID` | 审核通知与长连接 |
| `FEISHU_CALLBACK_MODE` | 必须显式 `websocket` 或 `webhook` |
| `FEISHU_REVIEWER_OPEN_IDS` | 审核人白名单；部署 schema 目前仍要求非空 |
| `FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS` | 开启后，当前审核群内的卡片操作即有审核权限 |
| `ARK_API_KEY` / `ARK_MODEL_ID` / `ARK_BASE_URL` | AI 预填与风险检查；失败降级为 `warning`，不阻断人审 |
| `SITE_OWN_ACCOUNTS` | 自有账号清单，用于判定原创 / 转载 |
| `YUNSUO_BASE_URL` / `YUNSUO_USER_CODE` / `YUNSUO_API_KEY` | 云梭外发；只用于已审核自有原创 |
| `PUBLISH_MONITOR_FEISHU_USER_ID` | 监控告警私聊收件人 |

`REDFOX_API_KEY`、`LAYOUT_MONITOR_URL` 等是旧配置兼容键，本服务不再读取。不要把密钥提交到仓库。发布任务是 JSON 文件 + 进程锁，同一 `PUBLISH_JOBS_PATH` 只能由一个 API 进程写入。

## 部署

合并进 `main` 前，PR 必须过无路径过滤的 `.github/workflows/p0-monitoring-contract.yml`。推 `main` 只触发 `.github/workflows/deploy.yml`：

1. 用 Go 1.26.6 构建 `api/fbif-api`，写入 `public/version.json`。
2. 把 public、API、render service、Caddy、systemd、env schema 同步进隔离 release；候选 API env 强制 `DEPLOYMENT_WRITES_PAUSED=1`。
3. 校验三类 env、Caddy、systemd 后事务切换。
4. 核验公网 commit SHA、健康、鉴权与 `/api/publish/readiness`。
5. 写暂停窗口内 `prepare-monitoring` 只清理已退役的本仓库 watchdog 与审核群播报，不恢复外检。
6. 验收后先关自动回滚，再解暂停；之后失败只向前修。外检由 `FoodTalks_Feed` 负责。

规则后台：在 GitHub Secret `RULE_SUPER_ADMIN_OPEN_IDS` 维护超级管理员；对方登录后在「规则后台 → 权限管理」授予管理员。旧共享密码入口已关闭。

## 维护

- 文档入口：[docs/INDEX.md](docs/INDEX.md)。稳定能力以当前代码、README、长期契约、ADR 和 `openspec/specs/` 为准；`docs/worklogs/` 与 `openspec/changes/` 是过程记录。
- `DESIGN.md` 是界面与预览页的视觉契约。
- `behavior` 只允许署名 / 来源、Mote 图、原创 / 转载页脚等受限动作，不是任意 JavaScript。
- 新增账号必须先写入 `account_catalog.go` 并在 `rule_defaults.go` 成对登记，再跑 `npm run gen:registry`。
- URL / DOCX / 复制规则必须有测试。图注与长样式图注见 `api/wechat_blocks_test.go` 与 `tests/foodtalks-site-docx.test.mjs`；署名见 `tests/fbif-template.test.mjs`；回源同步见 `api/publish_source_resync_test.go`；审核卡封面见 `api/feishu_review_card_test.go`。
