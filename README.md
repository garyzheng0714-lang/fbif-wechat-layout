# fbif-wechat-layout

![类型](https://img.shields.io/badge/%E7%B1%BB%E5%9E%8B-%E5%86%85%E5%AE%B9%E6%8E%92%E7%89%88%E4%B8%8E%E5%8F%91%E5%B8%83-2563eb)
![Go](https://img.shields.io/badge/Go-1.26.1-00ADD8?logo=go&logoColor=white)
![前端](https://img.shields.io/badge/%E5%89%8D%E7%AB%AF-Vanilla%20JS-f7df1e?logo=javascript&logoColor=111827)
![状态](https://img.shields.io/badge/%E7%8A%B6%E6%80%81-%E7%94%9F%E4%BA%A7%E4%BD%BF%E7%94%A8%E4%B8%AD-16a34a)
![README](https://img.shields.io/badge/README-%E4%B8%AD%E6%96%87-brightgreen)

FBIF 内容排版与发布工具，用于把 DOCX、Markdown、剪贴板正文或文章链接转换成适配微信公众号或 FoodTalks CMS 的 HTML，并提供“公众号文章 → FoodTalks 隐藏草稿 → 飞书审核 → 人工公开”的受控发布流水线。

## 仓库定位

- **分类**：内容工具 / 多渠道排版 / 受控发布流水线。
- **服务对象**：FBIF 公众号编辑、FoodTalks 网站编辑和需要统一文章样式与审核流程的内容运营人员。
- **服务宿主**：交互式输出面向微信公众号编辑器和 FoodTalks CMS；程序化接口供内部内容流水线调用。本仓库不是浏览器插件，也不是飞书多维表格插件。

## 渠道与账号入口

排版工具进入方式：

- 打开 `/` 会**无条件重定向到 `/mp/fbif`**（FBIF 公众号编辑器），不再有渠道选择门户。
- 公众号渠道走 `/mp/<packId>`（由 `public/app.html` 服务）：FBIF、WowFood爆品超市、FoodTalks什么是系列。
- 网站渠道走 `/ft/<packId>`（由 `public/site.html` 服务）：FoodTalks 网站默认包 `ft-default`，进入路径 `/ft/ft-default`，独立页面、无胶囊、无"更多文章"、输出 inline-style HTML 给 FoodTalks 网站后台粘贴。
- 账号切换统一走**单层顶栏头像菜单**（`public/js/account-menu.js` 的 `mountAccountMenu`，挂载点 `#acctMenu`），渠道降为菜单内分组标签；同模板账号原地切换，跨模板 / 跨渠道账号整页 reload。
- 老 URL（`/app.html`、`/mp`）仍可用，作为公众号工具的过渡 alias。

**网站工具的排版规则**（参考 [VanilMsou/FoodtalksArticleLayout](https://github.com/VanilMsou/FoodtalksArticleLayout) 已移植）：

- `<h2>` / `<h3>` 纯标签输出，无 inline style
- 正文 `<p>` 无 style，空行用显式 `<p><br /></p>`
- 图片 600px 居中（极端狭长图自适应：宽高比 ≥3→280px / ≥2.2→360px / ≥1.6→480px）
- 图片注释 `<span style="font-size: 12px; color: #7f7f7f;">`，13 个关键词智能识别（图片来源/资料来源/数据来源/来源/制图/截图/图注/注/Source/Photo/Image/Credit）
- 参考文献 `<span style="font-size: 16px; color: #7f7f7f;">`，自动补 `[1]` `[2]` `[3]` 编号
- 标题自动等级识别：`一二三 + 中文标点` → h2、`（一）（二）` → h3、9 个关键词（背景/简介/总结/分析/案例/介绍/概述/结论/展望）+ 短句 → h2
- 中文标点转换：跟 CJK 字符邻接的 ASCII 标点 `,:;?!().` 转为中文形态，保留小数和域名里的点
- 连续独立链接段之间不插空行
- DOCX 首个"作者："行之前的前置材料默认剥离
- 表格：蓝底白字表头 + 浅灰网格线、全格居中、列宽按 Word gridCol 比例；关键视觉以 HTML 属性 + 内容层双写（`border`/`cellpadding`/`align`/`bgcolor`/`width` 属性、表头 `<strong>`），保证粘进 FoodTalks 后台被清洗内联样式后不丢样；表格内图片按 Word 里设置的显示宽度统一尺寸（2026-07-06）

## 最新状态（2026-07-10）

- **账号目录单一真相源**：账号先登记在 `api/account_catalog.go`，再运行 `npm run gen:registry` 生成 `public/js/rule-packs/registry.js`；生成文件不手改。账号与默认 preset 在启动时做成对校验。
- **统一底部区块模型**：公众号与网站渠道共用 `modules.bottom_blocks` 数据结构和区块面板交互，但视觉输出仍由 `fbif`、`foodtalks-series`、`foodtalks-site` 三套渲染器物理隔离。旧 `fbif_footer` 只保留兼容回退，不再是网站页脚主路径。
- **微信草稿存活保护**：公众号复制层对更多文章卡片、微信图床属性、位图占位和异步回写做 T0 安全约束，防止内容粘贴后在“保存草稿”阶段被微信清洗掉。
- **FoodTalks CMS 存活保护**：网站表格同时输出 HTML 属性、内容层和 inline style，保证 TinyMCE 清洗样式后仍保留边框、对齐、表头和 Word 图片尺寸。
- **程序化渲染**：Bearer 鉴权的 `/api/render` 和 `/api/html-to-blocks` 复用交互式编辑器的同一套解析、规则与渲染实现，支持 `fbif` 和 `ft-default`。
- **受控发布流水线**：手动提交公众号文章 URL 后，系统依次抓取、渲染、转存图片、AI 预填与风险检查、写入 FoodTalks 隐藏草稿，并停在 `awaiting_review`；只有飞书卡片或管理员审核的 `approve` 动作能翻公开。审核请求只持久化 `publicizing` 决策并立即返回，FoodTalks 请求由可恢复后台 worker 执行。
- **编辑部规范数据化**：摘要、标签、作者和原创 / 转载判定已按历史发布样本沉淀为声明式规范与封闭标签词表。AI 只负责预填和出处抽取；完整评测仍在进行，且 FoodTalks 的标签 PUT 缺陷修复前不会把标签写入正式请求。
- **发布状态边界**：测试环境已经完成隐藏草稿、审核卡片与状态机全链路验证；正式环境接入仍等待外部白名单和服务级凭证，不能把测试闭环写成正式生产已上线。
- **部署门禁**：`main` 部署串行排队；完整 Node/Go 测试通过后，API、静态资源、render service、env 与 Caddy 全部先进入隔离 staging，再事务切换。服务或公网 SHA/健康/鉴权验收失败会恢复上一版。

## 功能

- 解析 DOCX / Markdown，保留标题、正文、图片、超链接、列表、引用和参考来源等结构。
- 粘贴正文会作为临时 TXT / Markdown 文档进入同一套解析、标点和渲染链路。
- 粘贴微信公众号文章或网页链接后，由 Go API 抓取并转换为结构化内容；失败时回退到备用读取路径。
- URL 导入会保留微信文章的图片说明组：`图片 -> 图注 -> 图片来源` 必须连续渲染为浅灰图注，不得把 `图片来源：` 当成正文或文章来源。
- 使用 `public/js/templates/fbif.js` 和 `public/css/wx-theme.css` 生成 FBIF 公众号样式。
- 区分预览层与复制层：预览可使用原图；复制按钮点击时立即写入当前可粘贴 HTML，CSS 内联、微信兼容结构和已准备好的图片结果会进入复制层。
- 支持图片代理、裁剪编辑、格式检测、上传重试和“更多文章”卡片合成；“更多文章”链接会抓取标题、头图和正文媒体图，选图弹窗优先使用当前卡片自己的媒体库；图片与“更多文章”合成结果在后台准备，不能阻塞复制按钮。
- “更多文章”是规则里的可选模块：规则后台可开启/关闭，并用卡片编辑器维护新文章默认推荐列表；JSON 中对应 `modules.more_articles.enabled` 和 `modules.more_articles.default_cards`。
- “更多文章”URL 粘贴支持移动端 input-only 行为；输入合法微信文章链接后会自动抓取，重复输入同一链接不闪烁状态。
- 支持旧版 `.doc` 经 LibreOffice 服务或 CloudConvert 转换为 `.docx` 后继续解析。
- 支持多文件队列处理、品牌页脚开关和“更多文章”组合页脚。
- 支持多账号规则绑定，当前 5 个账号：`FBIF食品饮料创新`、`WowFood爆品超市`（公众号，复用 fbif 模板）、`FoodTalks什么是系列`（公众号，独立 foodtalks-series 模板）、`网站默认 ft-default`与`网站·什么是系列 ft-series`（网站渠道，foodtalks-site 模板），各有独立服务端默认规则绑定；规则库 schema v3 为每条规则保存受限 `baseline_id`，自建规则必须显式选择继承基线，不再默认串到 FBIF；规则配置可按模块组织 `theme`、`supporting`、`more_articles`、`workflow` 等参数，并保存渠道独立的 `behavior`，本地设置仅作离线降级。
- 原创 DOCX 以首个 `作者：` 行作为正文起点；该行之前的编辑前置材料不进入编辑器预览。
- DOCX 连续正文段落会保留紧凑排版；只有原文空段分隔的位置才恢复标准段后间距。
- URL 导入的文章级 `来源：`、`作者：`、`编辑：` 会收集到正文末尾，并在标准 FBIF `提示：` 页脚前只渲染一次。
- 正文最后一段（包括参考来源末行）与标准 FBIF `提示：` 页脚之间保留标准段落间距，避免正文尾部贴住页脚。
- 默认行为规则下，作者为 `Mote莫特` 或 `Mote 莫特` 时，会在“参考来源”上方自动插入固定署名图片；后台 `代码化规则` 可调整作者/来源/页脚文案和这类受限插图动作。
- 支持内部内容发布任务：同一来源 URL 幂等建单，任务失败可从失败阶段续跑；AI 只预填封面、摘要、标签、作者和优化说明，不允许修改正文 HTML。

## 技术栈

- 前端：原生 HTML / CSS / JavaScript ES modules
- 后端：Go 1.26，标准库 `net/http`
- 测试：Node.js `node --test`、Go test
- 部署：GitHub Actions、rsync、systemd API 服务

## 项目结构

```text
.
├── public/
│   ├── index.html                  # 根重定向页（无条件跳转到 /mp/fbif）
│   ├── app.html                    # 公众号排版工具页（路径 /mp/*）
│   ├── site.html                   # 网站排版工具页（路径 /ft/*）
│   ├── site.css                    # 门户 + 网站工具页特有样式
│   ├── app.css                     # 页面样式
│   ├── footer.html                 # 页脚模板
│   ├── css/wx-theme.css            # 微信内容样式
│   ├── js/
│   │   ├── engine.js               # UI 协调、批处理、页脚、复制流程
│   │   ├── editor-shell.js         # 编辑器外壳、初始账号解析、快捷键
│   │   ├── account-menu.js         # 单层顶栏头像账号菜单（mountAccountMenu）
│   │   ├── session-restore.js      # 跨渠道编辑器快照保存与恢复
│   │   ├── parser.js               # DOCX / Markdown 解析
│   │   ├── uploader.js             # 图片上传与重试
│   │   ├── clipboard.js            # 剪贴板复制
│   │   ├── css-inline.js           # 复制前 CSS 内联
│   │   ├── crop-editor.js          # 图片裁剪编辑
│   │   ├── more-articles*.js       # “更多文章”配置与合成
│   │   ├── asset-library.js        # 总素材库 {{asset#id}} 引用解析
│   │   ├── blocks/                 # 底部区块统一模型（block-render/-merge/-panel；block-registry.gen.js 由 npm run gen:blocks 生成）
│   │   ├── rule-presets.js         # 规则参数 schema、行为加载和 API 封装
│   │   ├── rule-behavior.js        # 公众号受限行为规则 schema、校验和执行
│   │   ├── rule-behavior-foodtalks*.js  # 网站 / 系列受限行为规则（与公众号物理隔离，红线#19）
│   │   ├── rule-packs/registry.js  # 账号包注册表（go run . -emit-registry 生成，勿手改）
│   │   ├── admin-panel.js          # 密码保护的规则后台 UI（admin-* 系列子模块）
│   │   └── templates/              # 渲染器：fbif（公众号）/ foodtalks-site（网站）/ foodtalks-series（系列）+ mp-blocks / site-blocks 底部，三套互不 import
│   ├── fonts/                      # Noto Sans Hans 字体文件和许可证
│   └── version.json                # 部署版本信息
├── api/
│   ├── main.go                     # API + 静态文件服务 + 渠道路由
│   ├── html2md.go                  # HTML 转 Markdown 辅助
│   ├── account_catalog.go          # 账号目录（accountCatalog()，registry.js 的 -emit-registry 源）
│   ├── rule_defaults.go            # 各账号 / preset 服务端默认成对登记（红线#2/#22）
│   ├── rule_presets.go             # 规则预设存储与后台 API
│   ├── rule_bottom_blocks.go       # 统一 bottom_blocks 默认与校验（强制 schema_version=2）
│   ├── block_type_catalog.go       # 底部区块类型目录（-emit-blocks 源）
│   ├── rule_asset_library.go       # 素材库服务端存储
│   ├── rule_behavior.go            # 默认行为规则和服务端合并逻辑
│   ├── rule_config_validation.go   # 规则字段、模块、行为和取值校验
│   ├── wechat_blocks.go            # 微信文章块提取
│   ├── publish_*.go                # FoodTalks 发布任务、状态机、审核、预览与 AI 元数据
│   ├── foodtalks.go                # FoodTalks API 客户端与环境差异合同
│   └── feishu_*.go                 # 审核通知、卡片回调与长连接
├── render-service/                 # Node headless 渲染服务
├── deploy/caddy/                   # 声明式 Caddy vhost
├── tests/                          # 前端模块测试
├── DESIGN.md                       # 设计系统和交互约束
├── package.json
└── README.md
```

## 快速开始

### 仅运行静态页面

适合查看界面和测试不依赖后端 API 的流程：

```bash
cd public
python3 -m http.server 8080
```

访问：

```text
http://localhost:8080/app.html
```

### 运行 Go API 与静态文件服务

适合测试文章抓取、图片代理、旧版 `.doc` 转换等后端能力：

```bash
cd api
go run . -port 9000
```

健康检查：

```bash
curl http://127.0.0.1:9000/api/health
```

## 使用流程

1. 上传 DOCX / Markdown，或粘贴文章链接 / 正文。
2. 检查预览层中的正文、图片、引用和“更多文章”卡片。
3. 原创 DOCX 默认读取首个 `作者：` 行作为作者信息；作者行会显示为蓝色、非斜体，不要在正文中重复保留。
4. URL 导入文章时，图片来源应显示为浅灰小字号图注；文章级来源、作者、编辑应在提示页脚前只出现一次。
5. 按需裁剪图片、调整排版偏好或切换页脚。“更多文章”的“替换图片”会从该卡片对应文章的媒体库里选图；旧卡片缺少媒体库时，点击替换会自动按卡片链接补抓一次。
6. 点击复制按钮后立即切换到微信公众号编辑器粘贴；图片上传和“更多文章”合成会提前或后台刷新复制层，不应让复制按钮等待。
7. 在微信公众号后台做最后预览和发布前检查。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | 健康检查 |
| `POST` | `/api/fetch-article` | 抓取文章正文并返回结构化内容或 Markdown |
| `POST` | `/api/fetch-article-meta` | 获取文章标题、封面、规范链接和正文媒体图列表，用于“更多文章”卡片与选图弹窗 |
| `GET` | `/api/image-proxy` | 代理图片，绕过浏览器跨域限制 |
| `POST` | `/api/oss-upload` | 图片上传入口；未配置上游时返回原 base64 |
| `POST` | `/api/wechat-upload` | 兼容旧名称的图片上传入口 |
| `POST` | `/api/doc-to-docx` | 旧版 `.doc` 转 `.docx` |
| `GET` | `/api/doc-cache/{hash}/{filename}` | 读取转换过程中缓存的图片 |
| `GET` | `/api/rules/active?account=<id>` | 读取指定账号（公众号或网站，`account=fbif/wowfood/foodtalks-series/ft-default`）当前规则；未传 `account` 时默认读取 FBIF |
| `POST` | `/api/rules/admin/login` | 校验规则后台密码；成功返回 `{ "ok": true }` |
| `GET` | `/api/rules/admin/presets` | 列出规则预设，需 `X-Admin-Password` |
| `POST` | `/api/rules/admin/presets` | 保存规则预设，需 `X-Admin-Password` |
| `PUT` | `/api/rules/admin/active` | 绑定某个公众号当前应用的规则，支持 `{ "account_id": "...", "id": "..." }`，需 `X-Admin-Password` |
| `DELETE` | `/api/rules/admin/presets/{id}` | 删除规则预设，需 `X-Admin-Password` |
| `POST` | `/api/render` | 程序化渲染，需 `Authorization: Bearer $RENDER_TOKEN` |
| `POST` | `/api/html-to-blocks` | HTML 转中立 Block[]，需 Bearer token |
| `POST` | `/api/publish/site-sync` | 提交公众号 URL 发布任务，需 `X-Admin-Password` |
| `GET` | `/api/publish/jobs` | 查询发布任务摘要，需管理员密码 |
| `GET` | `/api/publish/readiness` | 部署/运维只读验收：存储、目标环境、分类、渲染、飞书、AI 与 worker 暂停状态；需管理员密码且只返回布尔/模式信息 |
| `GET` | `/api/publish/jobs/{id}` | 查询发布任务详情，需管理员密码 |
| `POST` | `/api/publish/jobs/{id}/review` | 审核通过或驳回；approve 原子写入 `publicizing` 后立即返回，后台异步翻公开 |
| `POST` | `/api/publish/jobs/{id}/retry` | 从失败阶段续跑任务 |
| `POST` | `/api/publish/jobs/{id}/reconcile` | 人工处理 CreateNews / UpdateNews 结果不确定态：绑定已有草稿、确认未创建，或确认公开结果；需管理员密码 |
| `POST` | `/api/feishu/card-callback` | 飞书审核卡片 Webhook 备用通道 |
| `GET` | `/preview/publish/{id}?t=...` | HMAC 签名的审核预览入口；优先现铸 FoodTalks 官方预览 token，失败时回落本地审核页 |

## 配置

后端按需读取以下环境变量：

| 变量 | 说明 |
| --- | --- |
| `WECHAT_UPLOAD_ENDPOINT` | 图片上传代理上游；未设置时返回原始 base64，预览仍可用 |
| `LIBREOFFICE_URL` | `.doc` 转 `.docx` 的 LibreOffice 转换服务地址 |
| `LIBREOFFICE_TOKEN` | LibreOffice 转换服务鉴权 token |
| `CLOUDCONVERT_API_KEY` | LibreOffice 不可用时的 CloudConvert fallback |
| `ADMIN_PASSWORD` | 规则后台密码；未设置时登录返回 503，后台写操作不可用 |
| `RULES_STORE_PATH` | 规则预设 JSON 文件路径；未设置时使用 `data/rule-presets.json` |
| `RENDER_TOKEN` / `RENDER_SERVICE_URL` | 程序化排版 API 鉴权与 Node 渲染服务地址 |
| `PUBLISH_JOBS_PATH` | 发布任务 JSON 存储路径；默认跟随规则存储目录 |
| `FOODTALKS_BASE_URL` / `FOODTALKS_TOKEN` | FoodTalks API 与凭证；缺失时发布任务显式失败 |
| `FOODTALKS_ORIGIN` | 测试环境浏览器头模式开关；正式白名单模式保持为空 |
| `FOODTALKS_FRONT_URL` / `FOODTALKS_ADMIN_ARTICLE_URL` / `FOODTALKS_CATEGORY_IDS` | 前台、后台文章入口与发布分类；三类 URL 必须指向同一目标环境 |
| `FOODTALKS_EXPECTED_TARGET` | 显式声明 `test` 或 `production`；readiness 会拒绝缺失、未知或与 FoodTalks URL 不一致的配置 |
| `DEPLOYMENT_WRITES_PAUSED` | 部署候选实例专用；为 `1` 时暂停发布/rules 写入口、恢复任务、通知 worker 与飞书长连接，验收通过后由事务脚本清零 |
| `FEISHU_APP_ID` / `FEISHU_APP_SECRET` / `FEISHU_REVIEW_CHAT_ID` | 审核通知和长连接回调 |
| `FEISHU_VERIFICATION_TOKEN` | Webhook 回调来源校验；长连接模式不使用 |
| `FEISHU_REVIEWER_OPEN_IDS` | 可执行卡片审核的飞书 `open_id` 白名单，逗号分隔；空值时 Webhook/长连接卡片审核全部 fail-closed，`ADMIN_PASSWORD` 保护的 HTTP 管理审核不受影响 |
| `ARK_API_KEY` / `ARK_MODEL_ID` / `ARK_BASE_URL` | AI 元数据预填与发布风险检查；缺失或失败时风险结论 fail-safe 降级为 `warning`，不阻断进入人工审核 |
| `ARK_MODEL_ID_B` / `ARK_JUDGE_MODEL_ID` | 可选双模型预填与独立判决；未配置 B 时保持单模型，判决失败回落主模型方案 |
| `SITE_OWN_ACCOUNTS` | 自有账号清单，用于代码侧确定原创 / 转载；留空使用内置清单 |

部署工作流还会使用 `SERVER_SSH_KEY`、`SERVER_HOST`、`SERVER_USER`、`DEPLOY_PATH`、`ADMIN_PASSWORD` 等 GitHub Secrets。生产规则后台密码只存放在 GitHub Secret 和本地忽略提交的 `.env` 中；不要把微信、LibreOffice、CloudConvert、服务器密钥或后台密码提交到仓库。

发布任务当前采用 JSON 文件 + 进程内互斥锁，部署约束是同一 `PUBLISH_JOBS_PATH` 只能由一个 API 进程写入；如扩为多实例，必须先迁移到带唯一约束和事务的共享数据库，不能让多个进程直接共写该文件。

## 脚本与测试

前端模块测试：

```bash
npm test
```

Go API 测试：

```bash
cd api
go test ./...
```

## 快捷键

| 快捷键 | 操作 |
| --- | --- |
| `⌘ + Enter` | 复制到公众号编辑器 |
| `⌘V` | 在上传页粘贴 URL 时自动抓取 |
| `Esc` | 从预览返回上传页 |

## 部署

推送到 `main` 会触发 `.github/workflows/deploy.yml`：

1. 执行 `npm ci && npm test`、隔离端口 Playwright 25 个功能场景、`cd api && go test ./...`，任何失败都会阻止部署；视觉基线只能用显式本地命令更新。
2. 使用 Go 1.26 构建 `api/fbif-api`，并写入 `public/version.json`。
3. 把 `public/`、API、render service、Caddy 与 env 同步进隔离 release；候选 env 强制 `DEPLOYMENT_WRITES_PAUSED=1`，不会消费任务或接收写操作。
4. 原子切换当前 release，验证 systemd、Caddy、公网 commit SHA、健康与鉴权，并用管理员鉴权的 `/api/publish/readiness` 检查 JSON store、显式 FoodTalks 目标、分类、渲染、飞书与双模型配置。
5. 任何候选验收失败都恢复上一 release；验收成功后确认 release、关闭自动回滚，再把 pause 清零并重启 API。
6. 解暂停后再次检查健康与 readiness。此时外部副作用可能恢复，只允许向前修复，不自动切回旧版制造重复发布。

当前生产配置显式锁定 `FOODTALKS_EXPECTED_TARGET=test`；切换正式 FoodTalks 是单独的外部环境变更，必须同时更新 API、前台、后台 URL 与凭证后再通过 readiness。`FEISHU_REVIEWER_OPEN_IDS` 为空时卡片审核保持 fail-closed，管理员 HTTP 审核仍可用。

生产规则后台密码变更流程：

1. 更新仓库的 GitHub Actions Secret：`ADMIN_PASSWORD`。
2. 同步更新本地 `.env`，便于本机冒烟验证；`.env` 已被 `.gitignore` 忽略。
3. 重跑 `Deploy to server` 工作流或推送 `main` 触发部署。
4. 用 `/api/rules/admin/login` 验证新密码返回 `200`，并用旧密码确认返回 `401`。

## 维护说明

- `DESIGN.md` 记录界面和公众号预览页的设计约束。
- 渲染器按渠道分三套且物理隔离（红线#19）：公众号默认走 `fbif`，网站走 `foodtalks-site`，FoodTalks系列走 `foodtalks-series`，底部分别由 `mp-blocks`（661px）/ `site-blocks`（满宽）渲染。各渠道功能应以对应渲染器为准，不要用 `fbif.js` 描述网站 / 系列行为。
- 规则配置的字段 schema、默认值、前端校验和服务端 API 封装集中在 `public/js/rule-presets.js`；受限行为规则集中在 `public/js/rule-behavior.js`。服务端规则持久化、账号绑定和写入校验集中在 `api/rule_presets.go`、`api/rule_behavior.go`、`api/rule_config_validation.go`；各账号 / preset 的默认成对登记在 `api/rule_defaults.go` 与 `api/account_catalog.go`，底部区块默认与校验在 `api/rule_bottom_blocks.go`（统一 `schema_version=2`）。
- `behavior` 不是任意 JavaScript。第一阶段只允许 schema 校验过的参数、条件、动作和模板槽位，用于作者/来源行、Mote 署名图、原创/转载页脚提示等轻量业务规则；解析、上传、复制、重试和图片处理仍必须留在代码里并由测试保护。
- 账号包入口集中在 `public/js/rule-packs/registry.js`（由 `go run . -emit-registry` 从 `api/account_catalog.go` 生成，勿手改）；新增账号必须先进入 `account_catalog.go` 并在 `api/rule_defaults.go` 成对登记账号 / preset（id 字节一致，红线#2）。`FoodTalks什么是系列` 走独立 `foodtalks-series` 渲染器与独立 `bottom_blocks` 默认（满宽，作者 / BD / 群二维码为 image 块），**不复用 FBIF 模板或页脚**；`api/rule_defaults.go` 必须显式覆盖其 `modules.bottom_blocks`，否则继承 FBIF 基线会错渲 FBIF 页脚（红线#22）。
- 复制到微信前的兼容处理集中在 `engine.js`、`clipboard.js`、`css-inline.js`、`uploader.js` 和 `more-articles.js`。`copyContent()` 不应等待图片上传或“更多文章”合成；后台准备完成后刷新复制层，下一次复制自然使用新结果。
- 默认“更多文章”列表由规则配置驱动：`public/js/rule-presets.js` 负责 JSON 读写，`public/js/more-articles-config.js` 负责卡片归一化，`public/js/more-articles-rule-editor.js` 负责规则后台卡片编辑器，`public/js/more-articles.js` 在新文章导入时按当前规则生成默认卡片。
- “更多文章”选图链路由 `api/main.go` 的 `/api/fetch-article-meta` 返回 `image_urls`，`public/js/more-articles.js` 持久化到卡片状态，`public/js/more-articles-ui.js` 合并为选图候选并处理 URL 输入自动抓取。候选顺序为自定义封面、抓取头图、当前卡片正文媒体图、主文章图片兜底；旧卡片缺少 `image_urls` 时，点击替换会按卡片链接补抓一次。
- DOCX 作者规则集中在 `public/js/templates/fbif.js`：首个 `作者：` 行之前内容默认隐藏；`Mote莫特` / `Mote 莫特` 会触发参考来源上方的固定署名图片。
- DOCX 段距规则也在 `public/js/templates/fbif.js`：连续非空正文段落在 Word 段距较小时保持紧凑；如果中间出现空段，则下一段前恢复标准 `wx-gap`。
- URL 导入结构化块由 `api/wechat_blocks.go` 输出，`public/js/templates/fbif.js` 渲染。图片说明组必须保留为 `cap`；文章级来源、作者、编辑必须在正文尾部、标准页脚前只渲染一次；正文尾段即使没有后续正文块，也必须带 `wx-gap`，给标准 `提示：` 页脚留出段落间距。
- 排版契约需要测试保护：`api/wechat_blocks_test.go` 覆盖连续图注和图片来源，`tests/fbif-template.test.mjs` 覆盖图注样式、作者来源去重、页脚位置和 DOCX 连续段落紧凑规则。
- FoodTalks 编辑部元数据口径见 [`docs/editorial-norms-foodtalks.md`](docs/editorial-norms-foodtalks.md)；API 环境差异、可见性状态与未解限制见 [`docs/foodtalks-api-contract.md`](docs/foodtalks-api-contract.md)。
