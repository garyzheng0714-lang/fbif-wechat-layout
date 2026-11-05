# Design: 公众号 → FoodTalks 网站内容自动化发布流水线

> **演进设计说明（2026-07-26 对齐）**：本文保留方案演进背景；当前不可逆公开动作一律受
> CLAUDE.md 红线 #26 约束，必须有持久化人工 `approve`，任何风险等级都不能自动绕过人审。
> 图片转存已统一走排版工具 `/api/oss-upload`，不再等待 FoodTalks 图片上传接口。

## Context

**现状链路**：公众号发文 → 人打开排版工具 `https://fbifmp-layout.foodtalks.cn/ft/<账号>` 粘贴原文链接 → 工具排出网站版 → 人复制 → 网站后台（TinyMCE CMS）粘贴 → 填标题/封面/分类 → 发布。

**目标链路**：公众号发文 →（轮询自动发现 或 人贴 URL）→ 流水线自动完成「抓取 → 网站排版 → 图片转存 → FoodTalks 隐藏发布」→ 飞书审核卡片 → 人点「通过」→ 文章公开 + 群通知。风险评估可自动化，但不得替代人工批准。

**三方已有资产（调研结论，全部已核实）**：

| 系统 | 已有能力 | 缺口 |
|---|---|---|
| 排版工具（本仓库，`fbifmp-layout.foodtalks.cn`） | `POST /api/fetch-article`（公众号 URL→Block[]）、`POST /api/html-to-blocks`、`POST /api/render`（Block[]→内联 HTML，Bearer `RENDER_TOKEN`，systemd `fbif-render` 已上线） | render 只支持 `packId=fbif` 微信模板；网站模板 `foodtalks-site.js` 未接入 |
| FBIF资讯订阅（`feed.foodtalks.cn`，Go+PG） | `publish_jobs` 状态机（created→optimizing→rendering→drafting→awaiting_confirm→publishing→终态，幂等续跑）、`internal/wechat`（token/传图/AddDraft/freepublish Publish+Status）、`internal/publishing`（LayoutClient 已会调排版工具两个接口）、`internal/imagecache`（qpic 防盗链下载）、`internal/feishu`（文本私信）、豆包 OptimizeClient、RBAC | 无 FoodTalks 客户端；无「拉取已发布文章」能力；无飞书卡片/群消息/回调；流水线只有 wechat_mp 方向 |
| FoodTalks 网站 | `POST/PUT api-we-test.foodtalks.cn/news/admin/news`（测试环境抓包文档），字段含 `publishStatus`/`isPublic`/`content`(HTML)/`coverImg`/`categoryIds` 等 | 正式环境地址、服务级鉴权及部分枚举仍待确认；图片转存已由排版工具 `/api/oss-upload` 解决 |

**约束（不可violate）**：
- 本仓库红线 #19：`fbif.js` 在网站任务中字节级不动；网站与公众号渲染器物理隔离。
- 本仓库红线 #24：网站渠道表格必须走 `public/js/templates/shared/table-renderer.js` 的 `outerBorder`+`cmsSurvival` 开关；发布进 CMS 的 HTML 要活过 CMS 清洗。
- 资讯订阅铁律：PG 迁移不自动、部署前手动跑；只新增不改既有逻辑；发布不可逆动作必须经过持久化人工确认，风险分级和配置开关都不能绕过审核闸门。
- 全局规则：规则数据不硬编码（分类 ID、页脚文案等走可配置）；密钥 env-only。
- **用户明确指令（2026-07-10）：本变更先只产出计划；任何对外调用（FoodTalks 冒烟、飞书发消息、微信拉文）都要等用户批准后按 tasks 执行。**

## Goals / Non-Goals

**Goals:**
- 公众号文章到网站文章全程自动，人只在飞书卡片上点「通过/驳回」。
- 网站排版结果与人工用排版工具 `/ft/` 渠道排出来的**同源**（同一 `foodtalks-site.js` 渲染器，不做第二套实现）。
- 所有文章先以「前台不可见」状态进站，审核通过才翻公开——误发不可能发生在审核之前。
- 幂等：同一篇公众号文章重复触发不产生第二篇网站文章。
- 触发方式可插拔：手动贴 URL（MVP）与轮询自动发现（Phase 5）共用同一条流水线。
- 飞书闭环：审核卡片（可交互）→ 发布结果群通知。

**Non-Goals:**
- 不做「内容中台一稿多发」终态（ChatGPT 建议的版本 B）——那要求改变编辑部的写作源头流程，属于组织变革不是本期技术命题；本期公众号仍是内容源。
- 不做知乎等其他平台。
- 不做 LLM 全文改写；智能体审核（红线检查）本期只做「提示 + 分级」，不做自动拦截决策权。
- 不动公众号发布链路（资讯订阅已有 wechat_mp target）、不动排版工具人工前端。
- 不用多维表格做任务队列（可作后期看板，非本期）。

## Decisions

### D1. ~~编排中枢放在「FBIF资讯订阅」系统里~~ → **已被用户推翻（2026-07-10）：编排中枢归排版工具本体**

**用户裁定（原话要点）**：「整个的架构，排版工具才是最主要的，我从来没说信息源一定来源于 feed.foodtalks.cn。两个工具打通只是发布方式之一，还有：手动找文章在排版工具排好版直接发布；监控任意公众号一发就跟发；自己的公众号一发就跟发。**主体和绕不开的是排版工具**，资讯流只是获取信息的路径之一。」

**新选择**：发布状态机、FoodTalks 客户端、飞书审核通知全部实现在**排版工具本仓库**（Go api/，JSON 文件持久化贴仓库既有范式）；抓取/排版/图片转存全部**进程内复用**既有能力（不再有跨系统 HTTP 客户端层）。资讯订阅系统降级为未来的输入源之一（发现文章后调排版工具的发布 API）。设计详见 `docs/worklogs/WORKLOG-publish-pipeline-in-layout-2026-07-10.md`。

**旧方案的错误**：把「资讯订阅有现成基建」当成了架构理由，混淆了「复用工具」与「归属中枢」——排版工具是所有发布路径的必经主体，发布是它的输出能力（与「复制到公众号」同级），不是资讯流的下游附属。旧实现（资讯订阅仓 279902e 起）退役为参考实现，待清理。

**仍然成立的被否方案**：*独立编排服务*（多一个部署单元收益为零）；*多维表格当队列*（无幂等/事务/重试语义）。

### D2. 排版仓库增量 = render-service 支持 `packId=ft-default`，渲染唯一真理源不变

**选择**：`render-service/render.mjs` 按 fbif 同款方式接入 `public/js/templates/foodtalks-site.js`，`server.mjs` 的 packId 白名单放开 `ft-default`，Go 代理透传。输出契约：

```
POST /api/render
请求: { title, source, blocks: Block[], isRepost: bool, packId: "ft-default",
        withFooter?: bool }
响应: { html, meta: { title, coverCandidates: [...] } }
```

**必须带的网站契约**（这正是「不能让资讯订阅自己拼 HTML」的原因）：
- 表格走 `public/js/templates/shared/table-renderer.js` 的 `outerBorder`+`cmsSurvival`（属性+style 双写，红线 #24），图片按 `wpx` 输出 `width` 属性+style 双写。
- 页脚走 `modules.bottom_blocks` 主路径（`ft-default` 服务端规则默认，红线 #20/#22），规则从 `GET /api/rules/active?account=ft-default` 拉取（fbif 链路同款 60s 缓存 + 降级）。
- 公众号 fbif 模板路径零改动：不传新开关、输出字节不变；`tests/wechat-draft-survival.test.mjs` 与 `tests/site-footer-parity.test.mjs` 必须全绿。
- 与微信链路不同：**外链不降纯文本**（网站可以带外链）；`mp.weixin.qq.com` 临时链接（`chksm` 参数会过期）是否保留由行为规则决定，默认转纯文本并把原文链接放来源行。

**被否掉的方案**：
- *资讯订阅直接用 `articles.content_html` 简单清洗后发布*：绕过排版工具=第二套排版实现，网站排版规则（图注归组、来源行、页脚、表格契约）全部漂移，正是本项目历史上反复踩的坑。
- *headless 浏览器驱动 `/ft/` 页面*：agent-publish-pipeline D2 已否决过（重、慢、脆）。

### D3. FoodTalks 发布客户端：新包 `internal/foodtalks`，「隐藏进站、审核后翻公开」

**选择**：资讯订阅新增 `internal/foodtalks.Client`，直接按抓包文档封 HTTP（无 SDK 可站，接口就两个）：

```go
type Client interface {
    CreateNews(ctx, n News) (id int64, err error)   // POST /news/admin/news, id=""
    UpdateNews(ctx, n News) error                    // PUT  /news/admin/news, id=<已有>
}
```

**字段映射表（公众号文章 → FoodTalks News 请求体）**：

| FoodTalks 字段 | 取值 | 说明 |
|---|---|---|
| `id` | `""` 创建 / 已有 id 编辑 | 回填进 publish_jobs 作幂等锚点 |
| `title` | 公众号文章标题（LLM 可微调，人可改） | |
| `coverImg` | 公众号封面图转存网站存储后的 URL | 冒烟测试确认允许的域名/尺寸 |
| `content` | `/api/render packId=ft-default` 输出的 HTML，图片已重写为网站存储 URL | |
| `publishStatus` | **已实测（2026-07-10 冒烟，详见 `docs/foodtalks-api-contract.md`）**：0=草稿（进站态，外界不可见）；1=已发布；2=下线（也不可见，可再翻回 1）。⚠️ 单向门：发布过就回不到 0，驳回回炉用 2 | 进站 POST 用 0，审核通过 PUT 翻 1 |
| `isPublic` | 🚨 实测**不是可见性开关**（=0 时未登录直链完整可见），真实语义待网站后端确认；同步链路先恒传 1 | 「有料」标签来自 categoryIds，与此无关 |
| `sourceType`/`sourceId`/`sourceName`/`author` | 转载/原创口径映射，枚举待冒烟确认 | 原创=FBIF，转载带原来源 |
| `summary` | 公众号摘要（digest），LLM 兜底生成 | |
| `primaryCategoryId`/`categoryIds` | 走可配置规则（管理后台/env），**不硬编码 58/52** | 全局规则：规则数据可配 |
| `publishTime` | 公众号发布时间 `YYYY-MM-DD HH:mm:ss` | |
| `seoTitle` | `<title>-FoodTalks全球食品资讯` 模板，可配置 | |
| `language` | `ZH` | |

**幂等**：`publish_jobs` 加 `source_key`（`wechat_mp:{url_hash}`，能拿到 biz/mid/idx 时用之）唯一索引 + `foodtalks_news_id` 回填锚点。创建前查锚点：已有 id → 走 UPDATE 不再 CREATE。这与 push_log 的 `bitable_record_id` 回填模式同构。

**鉴权（最大外部风险，Phase 0 解决）**：现文档的 Bearer 是个人登录 token。上线前必须从网站后端团队拿到：正式环境 Base URL、服务级长效 token（或专用机器账号 + 登录续期方案）、响应体结构与错误码。token 过期的降级行为：job 进 `failed`（错误信息人话提示「FoodTalks 凭证过期」），不静默重试打爆接口。

**FoodTalks API 实测契约（2026-07-10 冒烟，Phase 1 完成）**：完整版见 `docs/foodtalks-api-contract.md`（唯一真相源），客户端实现要点：
1. 请求必须带 `Origin`+`Referer`+浏览器形 UA，否则 nginx 层 403（HTML 错误页非 JSON）。
2. 响应统一 `{"code":0,"msg":"success","data":<newsId>}`；**业务失败也是 HTTP 200**（`code!=0` + 中文 `msg`），客户端必须判 `code` 不能信 HTTP 状态码。
3. API 不校验 `publishStatus` 枚举范围（乱值也 success），客户端只准用 0/1/2。
4. PUT 是全量覆盖语义，需带完整字段集；编辑就地生效不产生新文章。
5. 隐藏进站→审核→公开走法已在测试环境全流程走通（文章 7241）：POST(0 草稿) → PUT(1 发布) → PUT(2 下线)。

### D4. 图片策略：mmbiz 全量转存网站可用存储，禁止外链裸奔

正文 `<img>` 与封面的 mmbiz.qpic.cn URL 有防盗链且不受我们控制，网站端必须转存。
**2026-07-10 实测定型（用户裁定走排版工具 OSS，硬阻塞解除）**：FoodTalks API 不校验图片域名、前台原样引用外域图（实测见 `docs/foodtalks-api-contract.md` §8），排版工具既有 `POST /api/oss-upload`（urls 模式）就是转存通道——服务器内网并发「取 mmbiz + 传 OSS」，实测 19 张 ~1.3s，零新开发：

```
render 输出 HTML → 枚举 img src（mmbiz/外站）
  → 调排版工具 POST /api/oss-upload {urls:[...]}（一次批量，返回 {原url: oss_url}）
  → 重写 src → content 落库（封面 coverImg 同通道）
```

- 整批一次调用（上游本就并发），失败 URL 在响应缺失即视为失败，重试一次。
- 任何一张正文图转存失败 → job `failed` 并列出失败 URL（显式失败），不允许「个别图裸奔外链」的半成品进站。
- `original_url → new_url` 映射存 job 记录，便于追溯与重跑跳过已传图。

**被否掉的方案**：*直接引用 mmbiz 外链*（防盗链裂图）；*索要 FoodTalks 上传接口*（实测证明不需要——API 不校验域名；已发布成品里的 static.foodtalks.cn 是 CMS 编辑器粘贴时的二次转存，API 路径不触发）。
**记录在案的权衡**：图片资产落我方 OSS（fbif-gary-img-hz）而非 FoodTalks 存储，桶停用则文章图全挂；长期可与网站后端商定归一，不阻塞 MVP。

### D5. 触发：多发现路径物理解耦；手动贴 URL 是永久兜底

**选择**：
- **手动触发（Phase 3，MVP）**：资讯订阅新增 `POST /api/publish/site-sync`（body: 公众号文章 URL），直接建 job 跑流水线。这条不依赖微信任何接口，先把 90% 的价值（排版/转图/发布/审核）拿到手。
- **“发布”自动发现（Phase 5）**：08:30–18:30 的 poller 独立调用 `freepublish/batchget` 最新页，与持久去重表比对后逐篇建 job；draft / 素材接口失败不得阻断这条轮询。
- **“群发”自动发现**：不能由 `freepublish` 代替，必须作为另一条持久、幂等的发现路径接入，并与发布路径在 canonical identity 层收敛。
- **类型闸门**：小绿书 / 图集或信号冲突时 fail closed；只有正向证据确认普通文章才自动投递。
- **服务器配置**：不是永久禁止，但启用公众号统一“服务器配置”前必须核实现有回调、自动回复和菜单影响；官方群发结果事件若接入，只能作为独立持久发现路径，禁止直接覆盖现有配置。

**理由**：
1. 公众号后台“群发”和 `freepublish`“发布”是不同覆盖路径，单靠任一接口都不能宣称完整。
2. 已发布轮询是 P0 真相路径之一，必须独立执行并把失败、陈旧和缺口暴露给监控。
3. 公众号统一“服务器配置”可能影响既有自动回复、菜单和回调，只有完成现状审计与迁移方案后才能改。
4. 手动 URL 不依赖微信列表接口，始终作为缺口处理与应急入口。

### D6. 飞书审核：应用机器人交互卡片 + 回调端点；通知与审核分层

**选择**：用用户提供的飞书应用（`cli_aac5d107d9381bdf`，im 权限齐全）做**应用机器人**，不用自定义 webhook 机器人：

- **审核卡片**（job 到 `awaiting_confirm` 时发到审核群）：标题/来源/类型（原创・转载）/风险提示列表 + 「查看排版预览」「查看公众号原文」链接 + 「通过并公开」「驳回」按钮。卡片 JSON 按 `feishu-cardkit` skill 的生产规范搭。
- **回调**：卡片按钮走 `card.action.trigger` 事件。资讯订阅 server 暴露回调端点（或用 lark 长连接模式，Phase 4 依部署环境定）；回调校验操作人（RBAC：审核人白名单）→ 翻转 `isPublic` → 原卡片更新为「已发布 by 谁」→ 群发布结果通知。
- **发布结果群通知**：发布成功/失败都发（含网站链接/失败原因）。
- **chat_id 获取流程**：机器人拉进审核群 → `GET /open-apis/im/v1/chats` 列机器人所在群 → 取目标群 `oc_` id → 存 env/配置。
- **降级路径**：Phase 3 MVP 先用纯文本群消息（复用现成 `internal/feishu` 文本能力 + 新增群维度发送），审核动作走资讯订阅前端确认按钮；Phase 4 才上交互卡片。这样 MVP 不被「事件订阅配置」阻塞。

**被否掉的方案**：*自定义 webhook 机器人*（无法做按钮回调、无操作人身份、无审计）；*审核动作放多维表格*（状态源分裂，D1 已否）。

### D7. 状态机：`publish_jobs` 平行新增 site 链路的 stage 序列，不碰 wechat_mp 分支

**选择**：`target='foodtalks_site'` 的 job 走：

```
created → fetching（fetch-article 拉 blocks）
        → rendering（render packId=ft-default）
        → mirroring（图片转存+重写）
        → staging（FoodTalks 隐藏态创建/更新，回填 news_id）
        → awaiting_review（飞书卡片已发，人审闸门——自动推进到此为止）
        → [人通过] → publicizing（PUT 翻 isPublic）→ published
        → [人驳回] → rejected
任一步失败 → failed（错误落 job，可从失败阶段幂等续跑）
```

新增列：`source_key`（唯一索引）、`source_url`、`foodtalks_news_id`、`review_card_msg_id`、`risk_notes`。版本化迁移，生产 PG 手动跑（铁律）。推进器按 target 分发到两套 stage 序列，`wechat_mp` 代码路径零改动（外科手术）。

**风险审核节点**：`rendering` 后调豆包出 `risk_notes`（复用 OptimizeClient 骨架，prompt 换成红线清单：功效表达/转载授权/竞品评价/数据来源），结果用于卡片展示、排队和人工决策。`pass|warning` 都必须在审核版本匹配后取得持久化人工 `approve` 才能进入 `publicizing`；`block`、缺失或未知结果一律 fail closed。Phase 6 只允许自动评估、排序、提醒和准备审核材料，不允许自动公开。

### D8. 安全与凭证

- 飞书 App Secret（`cli_aac5d107d9381bdf`）已在对话/文档明文出现 → **上线前在开放平台重置**，新值只进服务器 env（600 权限）。
- 微信 AppSecret 同理 env-only；建议用户择机把飞书文档里的明文挪走。
- FoodTalks token env-only；接口文档里的抓包 token 视为已泄露级别，正式上线换服务级凭证。
- 所有出站调用打审计日志（job_id、动作、操作人），但日志不含任何 secret。

### D9. 审计整改边界：先封堵运行时风险，结构迁移独立实施

2026-07-21 的代码内整改已经覆盖出站请求、资源上限、复制与会话一致性、规则版本、并发背压、通知恢复、部署门禁和 CI/浏览器证据。以下边界仍是生产事实，不能因测试闭环而写成“已具备高可用”：

- 发布任务、规则和 outbox 仍以原子 JSON 文件持久化，并依赖“同一路径单个 Go API 写进程”契约；它不支持多实例并发写。迁移到 SQLite WAL 或正式数据库必须作为独立变更，包含旧数据导入校验、备份、灰度、回滚及灾难恢复演练。
- 有界 worker pool 提供进程内背压和 panic 隔离，但不是持久队列，也没有跨进程 lease。进程崩溃后的安全性仍依赖已持久 intent、CAS 和 reconciliation，不能把它描述为 exactly-once。
- 规则 revision/hash 已成为渲染、复制和发布的不可变依赖；版本不一致时必须 fail-closed，不允许回退到“最新规则猜测执行”。
- sessionStorage 只承担小型短期快照；大稿件与离线恢复迁到 IndexedDB 前，容量不足只能安全降级，不能承诺完整恢复。
- 当前管理面仍是共享管理员凭证；企业身份、细粒度 RBAC、字段级操作审计以及独立供应商的 synthetic/dead-man 监控需要单独的身份与运维方案。
- `DocumentState` 单一不可变状态和 10 万任务规模基线属于后续重构/容量项目。当前版本以版本门、latest-wins、有限队列和故障注入守住正确性，但不据此声明完成结构性重构。

因此，本轮允许发布的结论是“代码本地可封堵的高风险项已整改并有自动化证据”；SQLite、RBAC、独立外部监控、IndexedDB 和大规模容量验证继续留在 tasks 6B.8–6B.10，完成前不得宣传为生产能力。

## Risks / Trade-offs

- [FoodTalks「隐藏态」语义与假设不符（比如 isPublic=0 仍出现在列表/搜索）] → **已应验并已解决（2026-07-10 冒烟）**：isPublic=0 确实不隐藏（未登录直链完整可见）；正确的隐藏进站态是 `publishStatus=0`（草稿），全流程已走通，见 `docs/foodtalks-api-contract.md`。
- [FoodTalks 图片上传接口拿不到] → **已解除（2026-07-10）**：按 D4 使用排版工具 `/api/oss-upload` 转存到我方 OSS；FoodTalks API 与前台引用行为已实测，长期归一网站存储不阻塞当前链路。
- [抓包 token 过期导致线上任务集体失败] → 客户端对 401/403 专项识别，job 错误信息直接写「凭证过期，联系 ×× 换发」；Phase 0 争取长效服务凭证是正解。
- [`freepublish/batchget` 拉不到“群发”文章] → 这是已确认的覆盖边界，不得降级成“拉到多少算多少”；群发必须有独立发现路径，缺口期间由手动 URL 兜底且监控保持非绿色。
- [公众号临时链接（含 chksm）过期导致 fetch-article 失败] → 轮询路径用 batchget 返回的永久 URL；手动路径提示尽快处理；失败可重试。
- [渲染器改版让 render-service 网站模板桩失效] → 网站渲染冒烟测试进本仓库 `npm test`（与 fbif 同款守护）；CMS 存活断言复用 `tests/table-renderer-shared.test.mjs` 的模拟清洗器。
- [卡片回调依赖飞书事件订阅配置，部署复杂] → Phase 3 MVP 用文本通知 + 前端确认兜底，卡片是增强不是阻塞。
- [重复发布事故] → source_key 唯一索引 + news_id 回填锚点双保险；tasks 里含「同 URL 触发两次只产生一篇」验收项。
- [无人值守误发] → 本变更所有对外写操作集中在 tasks 的显式验证步骤，用户批准后才执行；流水线上线后，公开动作永远在审核闸门之后。

## Migration Plan

1. **Phase 0（人工前置）**：按用户 2026-07-10 裁定不代为重置飞书 App Secret，继续遵守 env-only；向网站后端索要正式环境、服务级 token 和剩余枚举表；与运营确认公众号日常用「群发」还是「发布」；建审核群、机器人入群。图片转存已由 D4 解决。
2. **Phase 1**：FoodTalks 测试环境冒烟（创建隐藏文章→前台验证不可见→PUT 编辑→翻公开→再翻回/下线），产出《字段枚举与响应结构》文档回填本设计。
3. **Phase 2（本仓库）**：render-service 接 ft-default → 本地渲染真实公众号样例 → 与人工 `/ft/` 排版结果 diff 验证同源 → 测试进 CI → 部署。
4. **Phase 3（资讯订阅，MVP 闭环）**：`internal/foodtalks` + 流水线 site 链路 + 手动 URL 端点 + 文本群通知；PG 迁移手动跑；测试环境全链路真机验收。
5. **Phase 4**：飞书交互卡片 + 回调 + 结果通知，替换文本降级路径。
6. **Phase 5**：独立 freepublish “发布”轮询 + 独立“群发”发现路径 + canonical 去重与类型 fail-closed。
7. **Phase 6**：风险提示、排队和提醒自动化；历史回放只校准提示质量，公开动作仍必须人工批准。
8. **回滚**：全部是新端点/新 target/新按钮，回滚=停 poller+下端点，表保留无害；网站侧误发可 PUT 翻回隐藏。

## Open Questions

- FoodTalks 正式环境 Base URL、服务级鉴权方案？（Phase 0，问网站后端）
- ~~图片上传到 FoodTalks 存储的接口？~~ **已不阻塞**：当前统一走排版工具 `/api/oss-upload`；网站存储归一是长期优化，不是发布前提。
- ~~`publishStatus` 的「前台完全不可见」正确组合？~~ **已实测**：0=草稿/2=下线均不可见，1=发布可见，单向门 1↛0（`docs/foodtalks-api-contract.md`）。仍待后端确认：`isPublic` 真实语义、`sourceType`/`isLatest`/`commentEnable` 枚举、publishStatus 2 vs 3 官方口径、「有料」categoryId 表。
- ~~FBIF 订阅号运营日常动作是「群发」还是「发布」？~~ 已确认两者都有且不固定；Phase 5 必须覆盖两条独立路径。
- 网站文章的 `categoryIds` 由谁定：固定默认？LLM 按内容选？人在审核卡片上改？（MVP 先可配置默认值，卡片改分类留到 Phase 4+）
- 资讯订阅生产环境出口 IP 是否已在公众号 IP 白名单？（正式号白名单需另配）
- 审核群 chat_id（等群建好、机器人入群后获取）。
