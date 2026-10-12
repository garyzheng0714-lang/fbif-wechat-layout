# fbif-wechat-layout

![类型](https://img.shields.io/badge/%E7%B1%BB%E5%9E%8B-%E5%86%85%E5%AE%B9%E6%8E%92%E7%89%88%E4%B8%8E%E5%8F%91%E5%B8%83-2563eb)
![Go](https://img.shields.io/badge/Go-1.26.9-00ADD8?logo=go&logoColor=white)
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
- 账号切换统一走**左栏导入卡第一行的单层账号菜单**（`public/js/account-menu.js` 的 `mountAccountMenu`，挂载点 `#acctMenu`），渠道只是菜单内分组；同模板账号原地切换，跨模板 / 跨渠道账号整页 reload。顶栏只保留当前页面动作。
- 老 URL（`/app.html`、`/mp`）仍可用，作为公众号工具的过渡 alias。

**网站工具的排版规则**（参考 [VanilMsou/FoodtalksArticleLayout](https://github.com/VanilMsou/FoodtalksArticleLayout) 已移植）：

- `ft-default` 的 `<h2>` / `<h3>` 和正文 `<p>` 输出 `text-align: justify`；`ft-series` 在 `justify_paragraphs=false` 时去掉这层 inline style
- 空行显式输出 `<p><br /></p>`（`ft-default` 同时保留两端对齐样式）
- 图片与图注输出为 `figure.image > img.mc-img + figcaption`；横图默认 600，长图（高/宽 ≥ 1.7）固定宽 400，其他竖图按高 375 反推且宽度不低于 300，自然宽不超过 300 的近正方小图按二维码宽 150 输出；表格内 DOCX 图片按 Word 显示宽度输出
- 图片注释使用 12px、`#888888`，按关键词智能识别（图片来源/资料来源/数据来源/来源/制图/截图/图注/注/Source/Photo/Image/Credit）
- 参考来源使用 `#888888`，自动补 `[1]` `[2]` `[3]` 编号
- 标题自动等级识别：`一二三 + 中文标点` → h2、`（一）（二）` → h3、9 个关键词（背景/简介/总结/分析/案例/介绍/概述/结论/展望）+ 短句 → h2；署名行前缀（作者/编辑/出品/撰文…）不升标题；同文本 ≤16 字无序号标题全篇重复 ≥2 次判账号装饰行整体删除（2026-08-17）
- 转载稿开头已有「本文为X原创，作者：Y」时删除文末公众号口径来源/作者行；没有开头署名时仍降级 14px 灰斜体。转载声明《标题》超链接优先公众号「阅读原文」；自家号转发且阅读原文为空时先查 Feed 订阅列表，未订该号再 TikHub 广域搜（搜一搜），标题+出处都对上才用，核验不过不链回自家转载页（2026-08-18；补链 2026-08-25）
- 访谈自动识别（两个及以上「角色：」前缀各现 ≥3 次）：提问方 FoodTalks 蓝并加粗、回答方默认色加粗；提问方按书名号/问号率/首现判定；提问方前缀独立成段时与提问内容合并（2026-08-17）
- 中文标点转换：跟 CJK 字符邻接的 ASCII 标点 `,:;?!().` 转为中文形态，保留小数和域名里的点
- 连续独立链接段之间不插空行
- DOCX 首个"作者："行之前的前置材料默认剥离
- 表格：蓝底白字表头 + 浅灰网格线、全格居中、列宽按 Word gridCol 比例；关键视觉以 HTML 属性 + 内容层双写（`border`/`cellpadding`/`align`/`bgcolor`/`width` 属性、表头 `<strong>`），保证粘进 FoodTalks 后台被清洗内联样式后不丢样；表格内图片按 Word 里设置的显示宽度统一尺寸（2026-07-06）

## 当前状态（代码与长期契约核对至 2026-08-14）

- **账号目录单一真相源**：账号先登记在 `api/account_catalog.go`，再运行 `npm run gen:registry` 生成 `public/js/rule-packs/registry.js`；生成文件不手改。账号与默认 preset 在启动时做成对校验。
- **底部区块数据已统一，网站交互尚未收口**：各渠道共用 `modules.bottom_blocks`，视觉输出仍由三套渲染器物理隔离；但 `site.html` 仍是内联面板和原生拖拽，没有复用 `mountBlockPanel` + Sortable，违反红线 22，不能把“数据共用”写成“交互已经统一”。
- **FoodTalks URL 导入与页脚默认值**：网站渠道的公众号 URL 导入一律按转载处理，先移除微信原文导流尾，再输出网站转载声明；`ft-default` 的完整页脚在全新浏览器中默认开启，不依赖历史 localStorage 状态。
- **微信草稿存活保护**：公众号复制层对更多文章卡片、微信图床属性、位图占位和异步回写做 T0 安全约束，防止内容粘贴后在“保存草稿”阶段被微信清洗掉。
- **FoodTalks CMS 存活保护**：网站表格同时输出 HTML 属性、内容层和 inline style，保证 TinyMCE 清洗样式后仍保留边框、对齐、表头和 Word 图片尺寸。
- **飞书云文档导入走异步作业**：导出、剥图与图片镜像在服务端作业里完成，前端用 long-poll 拿真实阶段进度；正文图一次性批量镜像到 OSS。预览在离屏 staging 备齐并解码后整帧提交，导入期间旧文章保持可见。真实 10MB 级多图文档的生产耗时仍是未排期验收项，触发条件是下一次生产多图导入验收。
- **程序化渲染**：Bearer 鉴权的 `/api/render` 和 `/api/html-to-blocks` 复用交互式编辑器的同一套解析、规则与渲染实现，支持 `fbif` 和 `ft-default`。
- **受控发布流水线**：提交公众号文章 URL 后，系统依次抓取、渲染、转存图片、AI 预填与风险检查、写入 FoodTalks 隐藏草稿，并停在 `awaiting_review`。默认只有飞书卡片或管理员审核的 `approve` 能翻公开；管理后台「发布链路」打开自动发布后，新同步且风险为 `pass|warning` 的稿会由系统持久化 approve 后直接上线，群卡和下线按钮仍保留。审核请求只持久化 `publicizing` 决策并立即返回，FoodTalks 请求由可恢复后台 worker 执行。
- **上游只交链接**：`site-sync` 与 `official-sync` 只接收链接、规则包和分类证据；正文、标题、来源、作者或封面一旦出现就显式 400。详细字段、状态码与幂等见 [发布接入契约](docs/publish-intake-contract.md)。
- **审核卡与风险分层**：待审、已发布、已驳回、异常卡都保留封面、标题、标签、摘要和预览/原文入口。只有显式 `confirmed` 的实质性问题进首屏，其余待核提示收进完整报告；审核群只发卡片。`card-refresh` 可原位重建历史卡；任务已经使用 `ReviewCardMsgIDs` 多群映射时，切群后新群没有自己的 message id 就发新卡。只带 legacy `ReviewCardMsgID` 的旧任务仍可能回退更新旧群，是待修兼容风险。
- **FoodTalks 内容组装**：原创与转载分别走独立分类配置；所有文章的 AI 标签都在 `tag_type_id=6` 中按规范化名称等值解析、从不新建；Mote 文章再优先加 Mote/系列标签，最多 3 个，只有 Mote 新系列（段首「这是我XX系列的第N篇」）会自动建系列标签。系列声明段与链到公众号合集的「XX系列」链到系列标签页；Mote 正文里的公众号文章链换成站内已发布稿，找不到才留公众号链。进 FoodTalks 和云梭的正文都切除微信导流尾；正文非空白字符 ≥3000 且 h2+h3 ≥5 时在发布组装层写确定性目录。
- **外部分发是正交子状态机**：FoodTalks 继续接收原创与转载；只有已审核、已在 FoodTalks 公开且确认为自有原创的任务，才能经云梭分发。每个平台账号单独推送、单独记状态（`external_distribution_lanes`），一个平台失败不挡其他平台；版式分通用层与平台层（`yunsuo_platform_profiles.go`）。`YUNSUO_TARGET_ACCOUNTS_JSON` 里标 `"auto":true` 的账号（目前是头条号、百家号）在原创公开后自动推送，名额按平台各算（头条号每天最多 5 篇）；卡片上的「多平台分发」折叠区列出各平台状态与文章链接，按钮推还没推过的或受理前失败的平台。存量原创用 `auto-backfill` 补发，每个平台每天最多 3 篇、白天间隔 1 小时；已有记录的平台（含人工登记的已发稿）不再推。头条号文章链接在推送成功后经头条搜索 + TikHub 核对回填。未拿到 `content_pk` 的不确定结果只进人工对账，不盲重发。
- **手工发布优先且同题防重**：运营在 CMS 手工发布/编辑的稿件不用流水线库存覆盖。待审卡会 best effort 标红同题已发布稿；首次审核公开的 `stagePublicize` 在写入公开意图前再硬检，命中或 CMS 查询失败都 fail closed。
- **环境状态不写死在文档**：代码同时支持 `test|production`，当前生产实例实际指向必须现场核验 `/api/publish/readiness` 与对应 URL，不从 README 或旧日志推断。
- **已知未闭环（不可发布）**：至少包括图宽 preset 迁移与离线 fallback 仍是旧口径、同题硬闸未覆盖 `stageRepublish`、网站页脚交互未复用 `mountBlockPanel` + Sortable、ADR 中立层仍保留 FBIF 专用兼容字面量、legacy 审核卡切群可能更新旧群、群成员审核的运行时与 env schema 要求未收敛，以及导入框快捷键会同时复制旧预览并提交新内容。完整验收阻断见 [FoodTalks URL 导入验收基准](docs/agents/foodtalks-url-import-acceptance.md)；这些问题要另做功能修复与真实验收，不能靠文档宣称完成。
- **部署门禁**：`main` 部署串行排队；全量检查通过后，API、静态资源、render service、env schema、systemd 单元与 Caddy 先进入隔离 staging，再事务切换。写暂停窗口只清理已退役的本仓库 watchdog 与审核群监控播报；外检由 `FoodTalks_Feed` 负责。

## 功能

- 解析 DOCX / Markdown，保留标题、正文、图片、超链接、列表、引用和参考来源等结构。
- 粘贴正文会作为临时 TXT / Markdown 文档进入同一套解析、标点和渲染链路。
- 粘贴微信公众号文章或网页链接后，由 Go API 抓取并转换为结构化内容；失败时回退到备用读取路径。
- 导入飞书云文档：在导入框输入 `@` 搜索，或直接粘贴飞书文档链接。服务端以异步作业导出 Word、剥离正文图并一次性批量镜像到 OSS，前端按作业上报的真实阶段推进进度条；正文与图片在离屏准备就绪后整帧上屏，导入期间旧文章保持可见。需使用飞书登录。
- URL 导入会保留微信文章的图片说明组：`图片 -> 图注 -> 图片来源` 必须连续渲染为浅灰图注，不得把 `图片来源：` 当成正文或文章来源。
- 使用 `public/js/templates/fbif.js` 和 `public/css/wx-theme.css` 生成 FBIF 公众号样式。
- 区分预览层与复制层：预览可使用原图；复制按钮点击时立即写入当前可粘贴 HTML，CSS 内联、微信兼容结构和已准备好的图片结果会进入复制层。
- 支持图片代理、裁剪编辑、格式检测、上传重试和“更多文章”卡片合成；“更多文章”链接会抓取标题、头图和正文媒体图，选图弹窗优先使用当前卡片自己的媒体库；图片与“更多文章”合成结果在后台准备，不能阻塞复制按钮。
- “更多文章”是规则里的可选模块：规则后台可开启/关闭，并用卡片编辑器维护新文章默认推荐列表；JSON 中对应 `modules.more_articles.enabled` 和 `modules.more_articles.default_cards`。
- “更多文章”URL 粘贴支持移动端 input-only 行为；输入合法微信文章链接后会自动抓取，重复输入同一链接不闪烁状态。
- 支持旧版 `.doc` 经 LibreOffice 服务或 CloudConvert 转换为 `.docx` 后继续解析。
- 编辑器一次只处理一篇文章；支持品牌页脚开关和“更多文章”组合页脚。
- 支持多账号规则绑定，当前 5 个账号：`FBIF食品饮料创新`、`WowFood爆品超市`（公众号，复用 fbif 模板）、`FoodTalks什么是系列`（公众号，独立 foodtalks-series 模板）、`网站默认 ft-default`与`网站·什么是系列 ft-series`（网站渠道，foodtalks-site 模板），各有独立服务端默认规则绑定；规则库 schema v4 为每条规则保存受限 `baseline_id`，并为账号绑定保存单调修订号。自建规则必须显式选择继承基线，不再默认串到 FBIF；规则配置可按模块组织 `theme`、`supporting`、`more_articles`、`workflow` 等参数，并保存渠道独立的 `behavior`，本地设置仅作离线降级。
- 原创 DOCX 以首个 `作者：` 行作为正文起点；该行之前的编辑前置材料不进入编辑器预览。
- DOCX 连续正文段落会保留紧凑排版；只有原文空段分隔的位置才恢复标准段后间距。
- URL 导入会识别文章级 `来源 / 出处 / 出品`、`作者 / 撰文 / 撰稿 / 文`、`编辑 / 责编`，分隔符支持 `：`、`:`、`/`、`|`、`｜`。转载稿把这些信息移到正文开头，原创稿仍放在正文末尾、标准 FBIF `提示：` 页脚前；两者都只渲染一次，来源、作者、编辑依次独立成行（有值才显示）。正文有显式来源时优先使用；没有时以微信抓取结构里的 `source_name`（公众号名称）补 `来源：`，不会把结构化 `byline` 擅自补成作者。署名行使用公众号蓝色、`1.75` 行高和正常字形，不斜体，连续署名行之间不额外空行。
- 正文最后一段（包括参考来源末行）与标准 FBIF `提示：` 页脚之间保留标准段落间距，避免正文尾部贴住页脚。
- 默认行为规则下，作者为 `Mote莫特` 或 `Mote 莫特` 时，会在“参考来源”上方自动插入固定署名图片；后台 `代码化规则` 可调整作者/来源/页脚文案和这类受限插图动作。
- 支持内部内容发布任务：同一来源 URL 幂等建单，任务失败可从失败阶段续跑；封面、摘要优先用公众号原封面和原摘要（`msg_desc`），AI 只在原文缺值时兜底并预填标签、作者和优化说明，不允许修改正文 HTML。

## 技术栈

- 前端：原生 HTML / CSS / JavaScript ES modules
- 后端：Go 1.26，标准库 `net/http`
- 测试：Node.js `node --test`、Playwright、Go test，并设 JavaScript/Go 覆盖率棘轮门槛
- 质量门禁（PR 的 P0 工作流）：JS 语法与 import cycle、Go fmt/vet/staticcheck/race/govulncheck、npm audit、ShellCheck、Python 编译与 env schema；浏览器只考 Chrome，安装结果按 lockfile 缓存
- 部署：推 `main` 只构建并事务上线，不再重考、不再装浏览器

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
│   │   ├── account-menu.js         # 左栏导入卡的单层账号菜单（mountAccountMenu）
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
│   │   ├── admin-panel.js          # 飞书会话与角色权限保护的规则后台 UI
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
│   ├── feishu_docs_http.go         # 飞书云文档搜索与异步导出作业路由
│   ├── feishu_export_jobs.go       # 导出作业管理器：阶段进度、long-poll、限流与过期回收
│   ├── doc_image_mirror.go         # 剥离图批量镜像到 OSS 并重写 DOCX 图片 URL
│   └── feishu_*.go                 # 飞书登录、审核通知、卡片回调与长连接
├── render-service/                 # Node headless 渲染服务
├── deploy/
│   ├── caddy/                       # 声明式 Caddy vhost
│   ├── systemd/                     # API / renderer 可版本化服务单元
│   ├── config/                      # env-schema.json 与无密钥示例
│   └── scripts/                     # 事务部署与 env schema 验证器
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
| `GET` | `/api/feishu/docs/search?q=&limit=` | 按关键词搜索当前飞书登录用户可见的云文档，供编辑器 `@` 导入下拉使用；需飞书登录 |
| `POST` | `/api/feishu/docs/export/jobs` | 创建飞书云文档导出作业，立即返回 `job_id` 与 `status_url` / `result_url`；导出、剥图与图片镜像在后台执行 |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}?after=&wait_ms=` | long-poll 读取作业进度：服务端在 `version` 超过 `after` 时立即返回，逐阶段上报真实 `phase`/`done`/`total`（resolve、create、export_wait、download、optimize、image_enumerate、image_upload、image_map） |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}/result` | 取回作业产物 DOCX；正文图已镜像成公网 URL，响应头带 `X-FBIF-Images-Mirrored` 与 `X-FBIF-Image-Count` |
| `DELETE` | `/api/feishu/docs/export/jobs/{job_id}` | 取消作业；前端切换或放弃导入时调用，服务端也会自行过期回收 |
| `POST` | `/api/feishu/docs/export` | 同步导出兼容入口（无进度）；交互式导入禁止使用，只走上面的异步作业接口 |
| `GET` | `/api/feishu/chats` | 读取当前飞书登录用户所在群列表，供规则后台选择审核群；需飞书登录 |
| `GET` | `/auth/feishu/fbif/login` | 发起飞书 OAuth 登录；`return_to` 由签名 state 约束 |
| `GET` | `/auth/feishu/fbif/callback` | 飞书 OAuth 回调，换取用户会话后回到受控站内路径 |
| `POST` | `/auth/password/login` | 可选的本地密码账号登录；账号只从受保护的 Base64 配置同步 |
| `POST` | `/auth/feishu/logout` | 使当前会话失效并清除会话 cookie |
| `GET` | `/api/me` | 读取当前登录用户、登录方式、角色与飞书能力 |
| `GET` | `/api/rules/active?account=<id>` | 读取指定账号（公众号或网站，`account=fbif/wowfood/foodtalks-series/ft-default/ft-series`）当前规则；未传 `account` 时默认读取 FBIF |
| `GET` | `/api/rules/presets` | 读取可供编辑器使用的规则预设目录 |
| `POST` | `/api/rules/admin/login` | 已退役的共享密码入口，固定返回 `410 Gone`；后台只认登录会话和角色 |
| `GET` | `/api/rules/admin/presets` | 列出规则预设，需已登录且角色为管理员/超级管理员 |
| `POST` | `/api/rules/admin/presets` | 保存规则预设，需已登录且角色为管理员/超级管理员，以及客户端读到的 `base_revision`（新建为 `0`）；过期写入返回 `409 revision_conflict` 和变化字段 |
| `PUT` | `/api/rules/admin/active` | 绑定某个账号当前应用的规则，请求为 `{ "account_id": "...", "id": "...", "base_revision": 1 }`，其中修订号来自账号的 `active_revision`；过期写入返回 `409 binding_revision_conflict` |
| `DELETE` | `/api/rules/admin/presets/{id}` | 删除规则预设，需已登录且角色为管理员/超级管理员 |
| `GET` | `/api/rules/admin/access-users` | 超级管理员读取已登录账号列表与后台角色 |
| `PUT` | `/api/rules/admin/access-users/{id}` | 超级管理员授权/收回管理员角色；超级管理员只能通过 `RULE_SUPER_ADMIN_OPEN_IDS` 配置 |
| `POST` | `/api/render` | 程序化渲染，需 `Authorization: Bearer $RENDER_TOKEN` |
| `POST` | `/api/html-to-blocks` | HTML 转中立 Block[]，需 Bearer token |
| `POST` | `/api/publish/site-sync` | **只接收文章链接**：`url` + 可选 `rule_pack_id` + 分类证据。可选 `content_source_url` 只补「阅读原文」出处。带 `content_html` / `title` / `source_name` / `author` / `cover_url` 一律 400（见下方红线）。机器调用需 `X-Publish-Sync-Token` 与普通图文正向分类证据、有效 classifier version |
| `POST` | `/api/publish/official-sync` | 同样只接收链接，红线与判定同 site-sync；机器调用需要服务令牌与 fail-closed 证据 |
| `POST` | `/api/publish/sync-status` | 批量读取同步中心所需的紧凑任务状态；服务令牌或管理员鉴权，不返回正文、blocks 或模型原文 |
| `GET` | `/api/publish/monitoring` | 跨系统监控摘要；服务令牌或管理员鉴权。任一任务仍在 processing、审核通知仍 pending/sending、审核卡缺失或失败/对账积压时均 `ready=false`；最近一次 FoodTalks 调用被 401 拒绝时报 `foodtalks_credential_invalid`（进 `alert_issues`，换 token 后下一次成功调用即清除）；进程存活不等于业务完成 |
| `POST` | `/api/publish/monitor-alert` | 监控端用同步服务令牌提交结构化告警，本服务只负责转发到独立飞书私聊；不主动外检 |
| `GET` | `/api/publish/jobs` | 查询发布任务摘要，需管理员/超级管理员登录，或部署专用 `PUBLISH_ADMIN_SERVICE_TOKEN` |
| `GET` | `/api/publish/readiness` | 部署/运维只读验收：生成物存在/一致性、存储、目标环境、分类、渲染、飞书、AI 与 worker 暂停状态；需管理员角色或部署专用服务令牌，且只返回布尔/模式信息 |
| `GET` / `PUT` | `/api/publish/delivery-settings` | 读取/更新审核卡的飞书应用与目标群；管理员权限，写入还需飞书登录与当前群可见性 |
| `GET` | `/api/publish/jobs/{id}` | 查询发布任务详情，需管理员/超级管理员登录，或部署专用服务令牌 |
| `POST` | `/api/publish/jobs/{id}/review` | HTTP 审核默认关闭；仅显式开启、管理员鉴权且固定审核人位于飞书白名单时可用。approve 原子写入 `publicizing` 后立即返回，后台异步翻公开 |
| `POST` | `/api/publish/jobs/{id}/retry` | 从失败阶段续跑任务 |
| `POST` | `/api/publish/jobs/{id}/unpublish` | 按当前生命周期版本请求下线，外部 PUT 由后台 worker 执行 |
| `POST` | `/api/publish/jobs/{id}/republish` | 按当前生命周期版本请求重新公开已下线稿 |
| `POST` | `/api/publish/jobs/{id}/reconcile` | 人工处理 CreateNews / UpdateNews 结果不确定态：绑定已有草稿、确认未创建，或确认公开结果；需管理员角色或部署专用服务令牌 |
| `POST` | `/api/publish/jobs/{id}/rerender-latest` | 按任务编号用已存正文块走最新规则重排，不回源抓微信。短链过期时 `site-sync?rerender_latest` 会 400，存量待审单走这里。需管理员或部署专用令牌 |
| `POST` | `/api/publish/jobs/{id}/restage-current` | 丢掉不属于当前环境的旧草稿编号，再重排并在当前环境新建隐藏草稿。切正式站前的存量待审单必须走这里，禁止把测试站 id 写到正式站。需管理员或部署专用令牌 |
| `POST` | `/api/publish/jobs/{id}/card-refresh` | 用当前代码重出审核卡。任务已有 `ReviewCardMsgIDs` 映射时，当前群有 id 就原位更新、没有就发新卡；只带 legacy `ReviewCardMsgID` 的旧任务仍可能回退更新旧群。需管理员或部署专用令牌 |
| `POST` | `/api/publish/jobs/{id}/risk-recheck` | 对待审任务用当前 prompt 真正重跑风险检查并更新卡片；不等同于只换样式的 `card-refresh` |
| `POST` | `/api/publish/jobs/{id}/notification-replay` | 重放失败通知 outbox；只救通知投递，不重跑业务阶段 |
| `POST` | `/api/publish/cms-resync` | 按北京日期补写已公开稿的 `isLatest` 与来源 ID；不改本地状态机 |
| `POST` | `/api/publish/jobs/{id}/repair-wechat-trailer` | 对单篇已公开稿按当前规则切除微信导流尾并覆盖 FoodTalks，不重开审核 |
| `POST` | `/api/publish/repair-wechat-trailer` | 扫描并修复流水线可安全覆盖的已公开泄漏稿；CMS 人工稿优先 |
| `POST` | `/api/publish/jobs/{id}/sync-wechat-meta` | 已公开稿的封面/摘要与公众号对齐，只替换没人手改过的字段 |
| `POST` | `/api/publish/news/{id}/repair-links` | 按资讯 id 补链接（流水线稿与编辑手工稿都行）：系列声明/合集链→系列标签页，Mote 公众号文章链→站内稿；只链已有标签，`?dry_run=1` 只报告 |
| `POST` | `/api/publish/jobs/{id}/retry-external-distribution` | 管理员确认对账中的旧云梭稿作废后，只为对账中的平台按当前网站稿重新推送 |
| `POST` | `/api/publish/external-distribution/auto-backfill` | 把 `since`（RFC3339）之后公开、还有 auto 账号没有记录的原创记为补发待办，按各平台补发配额逐日推送；`dry_run:true` 只列名单 |
| `POST` | `/api/publish/jobs/{id}/external-distribution/manual` | 管理员登记某平台人工已发（`account_id` + 平台域名 `url`，或 `lookup:true` 自动找头条号），或给已发布的平台补链接；登记后自动外发不再推这个平台；`dry_run:true` 只报告 |
| `POST` | `/api/feishu/card-callback` | 仅 `FEISHU_CALLBACK_MODE=webhook` 时开放；长连接模式返回 404 |
| `GET` | `/preview/publish/{id}?t=...` | HMAC 签名的审核预览入口；优先现铸 FoodTalks 官方预览 token，失败时回落本地审核页 |

## 配置

后端按需读取以下环境变量：

| 变量 | 说明 |
| --- | --- |
| `ENV` | 运行环境；生产必须显式为 `production`，此时账号/区块生成注册表缺失或不可读会拒绝启动 |
| `FBIF_CONFIG_SCHEMA_VERSION` | 运行配置结构版本；当前必须为 `1`，API 生产启动、renderer 健康检查和 publish readiness 都会 fail closed |
| `WECHAT_UPLOAD_ENDPOINT` | 图片上传代理上游；生产由部署托管为本机 `127.0.0.1:9000` 上传服务。未设置时仅保留 base64 预览降级，网站 URL 图片转存会明确失败 |
| `LIBREOFFICE_URL` | `.doc` 转 `.docx` 的 LibreOffice 转换服务地址 |
| `LIBREOFFICE_TOKEN` | LibreOffice 转换服务鉴权 token |
| `CLOUDCONVERT_API_KEY` | LibreOffice 不可用时的 CloudConvert fallback |
| `PUBLISH_ADMIN_SERVICE_TOKEN` | 部署/运维调用发布管理接口的机器服务令牌；不作为网页登录密码，也不下发到浏览器 |
| `RULE_SUPER_ADMIN_OPEN_IDS` | 超级管理员飞书 `open_id` 白名单，逗号分隔；真实身份值只放秘密配置，不写入仓库文档 |
| `AUTH_SESSION_SECRET` / `AUTH_USERS_PATH` | 登录会话签名密钥与账号 JSON 存储；生产密钥至少 24 字节，账号文件默认与规则存储同目录 |
| `FEISHU_LOGIN_APP_ID` / `FEISHU_LOGIN_APP_SECRET` / `FEISHU_LOGIN_REDIRECT_URI` | 网页飞书登录应用及回调；生产必须完整配置 |
| `FEISHU_LOGIN_SCOPES` / `AUTH_PASSWORD_USERS_B64` | 可选登录 scope 覆盖与本地密码账号种子；scope 必须保留 `offline_access`，账号种子只放秘密配置 |
| `RULES_STORE_PATH` | 规则预设 JSON 文件路径；未设置时使用 `data/rule-presets.json` |
| `RENDER_TOKEN` / `RENDER_SERVICE_URL` | 程序化排版 API 鉴权与 Node 渲染服务地址 |
| `RENDER_PORT` / `RULES_API_ORIGIN` / `ASSET_ORIGIN` / `RENDER_BUILD_SHA` | renderer 的端口、规则 API、素材域名和可追溯构建 SHA；生产由流水线生成 `render-service.env` |
| `PUBLISH_JOBS_PATH` | 发布任务 JSON 存储路径；默认跟随规则存储目录 |
| `PUBLISH_SYNC_SERVICE_TOKEN` | `FoodTalks_Feed` 与 `fbif-wechat-article` 调用发布提交、紧凑状态和监控端点的独立服务令牌；不能访问完整任务、审核、重试、对账或规则管理接口 |
| `FOODTALKS_FEED_BASE_URL` | 向 Feed 查转载原文；缺省 `https://feed.foodtalks.cn`；鉴权共用上面的同步令牌 |
| `TIKHUB_API_KEY` / `TIKHUB_API_BASE_URL` | 可选，按次计费（0.01$/次）。Feed 未订该公众号、自家号转发又没填「阅读原文」时，用 TikHub 广域搜（搜一搜）按标题+出处补微信链接；Mote 正文的公众号长链微信只回验证页时，用文章详情接口取标题与发文时间，换成站内稿；国内默认 `https://api.tikhub.dev`。失败只降级 |
| `REDFOX_API_KEY` / `REDFOX_API_BASE_URL` | 旧红狐兼容键；本服务不再读取，只为旧 `publish.env` 过渡保留 |
| `PUBLISH_HTTP_REVIEW_ENABLED` / `PUBLISH_HTTP_REVIEWER_OPEN_ID` | HTTP 审核应保持默认关闭；仅应急启用时，固定审核人仍必须命中 `FEISHU_REVIEWER_OPEN_IDS` |
| `FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS` | 群成员审核模式（2026-08-06 用户裁定）：开启后，来自 `FEISHU_REVIEW_CHAT_ID` 审核群内的卡片操作即有审核权限（依据：只有群成员能点到群消息里的按钮，事件 `open_chat_id` 由飞书服务端注入）；卡片被转发到其他会话后群 id 不同，一律拒绝。白名单继续兼容 |
| `FOODTALKS_BASE_URL` / `FOODTALKS_TOKEN` | FoodTalks API 与凭证；缺失时发布任务显式失败 |
| `FOODTALKS_ORIGIN` | 测试环境浏览器头模式开关；正式白名单模式保持为空 |
| `FOODTALKS_FRONT_URL` / `FOODTALKS_ADMIN_ARTICLE_URL` / `FOODTALKS_CATEGORY_IDS` | 前台、后台文章入口与发布分类；三类 URL 必须指向同一目标环境 |
| `FOODTALKS_REPOST_CATEGORY_IDS` | 转载稿的 FoodTalks 分类，逗号分隔；默认 `53`，不再与原创共用独家专栏分类 |
| `FOODTALKS_EXPECTED_TARGET` | 显式声明 `test` 或 `production`；readiness 会拒绝缺失、未知或与 FoodTalks URL 不一致的配置。任务在草稿进站时记录 `staging_target`，环境守卫拒绝拿 A 环境的资讯 id 去 B 环境读写（切环境后旧任务须重新暂存才能发布，见 `api/publish_target_guard.go`） |
| `DEPLOYMENT_WRITES_PAUSED` | 部署候选实例专用；为 `1` 时暂停发布/rules 写入口、恢复任务、通知 worker 与飞书长连接，验收通过后由事务脚本清零 |
| `FEISHU_APP_ID` / `FEISHU_APP_SECRET` / `FEISHU_REVIEW_CHAT_ID` | 审核通知和长连接回调 |
| `FEISHU_NOTIFICATION_APPS_JSON` / `PUBLISH_DELIVERY_SETTINGS_PATH` | 可选的多飞书应用目录，以及审核卡投递选择的 JSON 存储路径 |
| `PUBLISH_MONITOR_FEISHU_USER_ID` | 监控端调用 `/api/publish/monitor-alert` 时的飞书私聊转发收件人 `user_id`；与审核群分开，本服务不自主外检 |
| `FEISHU_CALLBACK_MODE` | 必须显式为 `websocket` 或 `webhook`；推荐生产使用 `websocket`，此时公网 Webhook 固定返回 404；缺失/非法时 readiness 失败 |
| `FEISHU_VERIFICATION_TOKEN` | Webhook 回调来源校验；长连接模式不使用 |
| `FEISHU_REVIEWER_OPEN_IDS` | 可执行审核的飞书 `open_id` 白名单，逗号分隔。运行时允许“空白名单 + 群成员模式 + 当前审核群”授权卡片操作，HTTP 审核仍必须命中白名单；但当前 env schema 把该变量列为 required，空值会在部署预检直接失败。因此生产配置仍须非空，schema 与运行时授权口径尚待收敛 |
| `MIMO_API_KEY` / `MIMO_BASE_URL` | AI 元数据预填（看图选封面）与发布风险检查，走 Xiaomi MiMo Responses 接口；缺 Key 时 readiness 报 `enrich_mode=fallback`，预填走规则兜底、风险结论 fail-safe 降级为 `warning` |
| `MIMO_PRO_MODEL` / `MIMO_FLASH_MODEL` | 档位模型名，留空即 `mimo-v2.6-pro` / `mimo-v2.6-flash`。分工（2026-10-07 用户裁定）：看图（预填选封面）与翻译等批量任务用 Flash，风险检查等高价值任务用 Pro |
| `ARK_*` | 旧豆包/DeepSeek 配置，已不读取，仅为旧 `publish.env` 兼容保留 |
| `SITE_OWN_ACCOUNTS` | 自有账号清单，用于代码侧确定原创 / 转载；留空使用内置清单 |
| `YUNSUO_BASE_URL` / `YUNSUO_USER_CODE` / `YUNSUO_API_KEY` | 云梭 API、用户码与凭证；只用于已审核自有原创的外部分发 |
| `YUNSUO_TARGET_ACCOUNTS_JSON` / `YUNSUO_CATEGORIES` | 服务端固定的目标账号和分类；卡片 callback 不接受任意 `account_id`；账号加 `"auto":true` 即原创公开后自动外发；可选 `"platform":"toutiao"`/`"baijiahao"` 指定平台规则（不填按名字认） |
| `LAYOUT_MONITOR_URL` / `FEED_MONITOR_URL` / `OFFICIAL_MONITOR_URL` | 旧 watchdog 迁移后的兼容键；本服务不读取、不使用它们发探针，只为旧 `publish.env` 过渡保留 |

环境变量的完整机器可读合同在 `deploy/config/env-schema.json`，三份无密钥示例分别对应流水线 API env、服务器 `publish.env` 和 renderer env。部署会在改写 live 路径前检查未知键、重复键、必填空值和 schema 版本；验证器只输出变量名，不输出值。

部署工作流还会使用 `SERVER_SSH_KEY`、`SERVER_KNOWN_HOSTS`、`SERVER_HOST`、`SERVER_USER`、`DEPLOY_PATH`、`PUBLISH_ADMIN_SERVICE_TOKEN`、`RULE_SUPER_ADMIN_OPEN_IDS` 等 GitHub Secrets。`SERVER_KNOWN_HOSTS` 必须通过可信渠道取得并人工核验服务器公钥，内容采用 OpenSSH `known_hosts` 格式；工作流不会再用 `ssh-keyscan` 临时信任首次看到的主机。生产 `/var/www/api/publish.env` 还必须显式设置 `FEISHU_CALLBACK_MODE=websocket`（如确需 Webhook 才改为 `webhook`）。网页规则后台不再使用共享密码；不要把飞书、发布服务令牌、LibreOffice、CloudConvert 或服务器密钥提交到仓库。

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
| `⌘/Ctrl + Enter` | 提交导入框内容；已有预览时全局 handler 还会先复制旧预览，因此在导入框内使用会触发“复制旧稿 + 提交新稿”的实现缺口 |
| `⌘/Ctrl + V` | 在导入框粘贴链接、正文或 Markdown 后自动排版（`@`飞书搜索除外） |
| `Esc` | 从预览返回上传页 |

## 部署

合并进 `main` 前，PR 必须过 `.github/workflows/p0-monitoring-contract.yml`（无路径过滤）。推送到 `main` 只触发 `.github/workflows/deploy.yml` 发版，不再重跑考试、不装浏览器：

1. 使用已修复已知标准库漏洞的 Go 1.26.9 构建 `api/fbif-api`，并写入 `public/version.json`。
2. 把 `public/`、API、render service、Caddy、systemd 单元、env schema 与候选 env 同步进隔离 release；候选 API env 强制 `DEPLOYMENT_WRITES_PAUSED=1`，不会消费任务或接收写操作。
3. 在任何 live 切换前验证三类 env、Caddy、systemd 单元和当前服务；然后事务式切换 API、renderer、静态资源、单元、Caddy 与配置 schema。
4. 验证公网 commit SHA、健康、鉴权和 `/api/publish/readiness`；readiness 同时锁定配置版本、两个生成注册表哈希、JSON store、独立服务令牌、HTTP 审核关闭、可用审核权限来源、FoodTalks 目标、分类、渲染、飞书与 MiMo 配置（`enrich_mode=mimo`）。
5. 写暂停窗口内执行 `prepare-monitoring`：幂等摘除历史 `fbif-monitor-watchdog` 与审核群 08:30 监控播报单元，不安装、启用或验收外检 timer。任何可回滚验收失败都恢复上一 release，但回滚也不恢复这两类已退役单元。
6. 验收后先关闭自动回滚，再解除 worker 暂停。此时外部副作用可能恢复，失败只允许向前修复；跨系统外检由 `FoodTalks_Feed` 的 `fbif-link-watchdog` 负责。
7. `finalize` 将已验收产物保留到 `/var/www/.fbif-layout-history/<deploy-id>/`，最多 5 份；快照包含 API、renderer、public、Caddy、systemd 和 env schema，明确不含 `.env`、`publish.env` 或 `render-service.env`。快照成功后才删除短期事务备份。

`FOODTALKS_EXPECTED_TARGET` 必须与 API、前台、后台 URL 指向同一环境；实例当前目标只能从实时 readiness 核验，README 不固化生产值。`PUBLISH_SYNC_SERVICE_TOKEN`、`PUBLISH_MONITOR_FEISHU_USER_ID` 或可用审核权限来源缺失、HTTP 审核意外开启时，readiness 均失败。运行时审核权限来源可以是非空 `FEISHU_REVIEWER_OPEN_IDS`，也可以是群成员模式配合当前审核群；但部署 env schema 目前仍要求 `FEISHU_REVIEWER_OPEN_IDS` 非空，候选环境不能只配群成员模式。这项 schema/运行时差异尚未收敛。发布任务 JSON store 带进程锁，只允许一个 API 实例持有；横向扩容前必须迁移到支持事务与锁的存储。

规则后台权限配置流程：

1. 在 GitHub Secret `RULE_SUPER_ADMIN_OPEN_IDS` 中维护超级管理员飞书 `open_id`；人员变更只更新秘密配置，不把真实身份值写入文档。
2. 超级管理员登录后打开“规则后台 → 权限管理”，把已经登录过的账号切换为“管理员”。
3. 规则后台所有网页请求复用登录会话；旧的密码接口不再提供后台权限。
4. 部署/运维脚本使用独立的 `PUBLISH_ADMIN_SERVICE_TOKEN`，它与网页角色权限分离。

## 维护说明

- 文档入口见 [`docs/INDEX.md`](docs/INDEX.md)。[`docs/worklogs/`](docs/worklogs/) 和 `openspec/changes/` 是过程记录，不作为当前规则的单一真相源；是否仍在推进要结合文件状态、issue tracker 和当前代码判断。稳定能力以 README、长期契约、ADR、`openspec/specs/` 和当前代码为准。
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
- URL 导入结构化块由 `api/wechat_blocks.go` 输出，`public/js/editor-shell.js` 必须把 API 的 `source_name` 原字段名传给 `public/js/templates/fbif.js`。图片说明组必须保留为 `cap`；文章级署名别名和分隔符按“功能”一节归一，显式来源优先，缺失时才用公众号名称兜底；来源、作者、编辑在转载正文开头、原创标准页脚前只渲染一次。正文尾段即使没有后续正文块，也必须带 `wx-gap`，给标准 `提示：` 页脚留出段落间距。
- 排版契约需要测试保护：`api/wechat_blocks_test.go` 覆盖连续图注和图片来源，`tests/editor-shell.test.mjs` 锁定 `source_name` 传递，`tests/fbif-template.test.mjs` 覆盖图注样式、署名别名与分隔符、公众号名称兜底、作者来源去重、转载文首/原创页脚位置和 DOCX 连续段落紧凑规则。
- FoodTalks 编辑部元数据口径见 [`docs/editorial-norms-foodtalks.md`](docs/editorial-norms-foodtalks.md)；API 环境差异、可见性状态与未解限制见 [`docs/foodtalks-api-contract.md`](docs/foodtalks-api-contract.md)。
