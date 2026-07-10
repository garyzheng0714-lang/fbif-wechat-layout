# Design: 公众号 → FoodTalks 网站内容自动化发布流水线

## Context

**现状链路**：公众号发文 → 人打开排版工具 `https://fbifmp-layout.foodtalks.cn/ft/<账号>` 粘贴原文链接 → 工具排出网站版 → 人复制 → 网站后台（TinyMCE CMS）粘贴 → 填标题/封面/分类 → 发布。

**目标链路**：公众号发文 →（轮询自动发现 或 人贴 URL）→ 流水线自动完成「抓取 → 网站排版 → 图片转存 → FoodTalks 隐藏发布」→ 飞书审核卡片 → 人点「通过」→ 文章公开 + 群通知。后期低风险文章跳过人工审核。

**三方已有资产（调研结论，全部已核实）**：

| 系统 | 已有能力 | 缺口 |
|---|---|---|
| 排版工具（本仓库，`fbifmp-layout.foodtalks.cn`） | `POST /api/fetch-article`（公众号 URL→Block[]）、`POST /api/html-to-blocks`、`POST /api/render`（Block[]→内联 HTML，Bearer `RENDER_TOKEN`，systemd `fbif-render` 已上线） | render 只支持 `packId=fbif` 微信模板；网站模板 `foodtalks-site.js` 未接入 |
| FBIF资讯订阅（`feed.foodtalks.cn`，Go+PG） | `publish_jobs` 状态机（created→optimizing→rendering→drafting→awaiting_confirm→publishing→终态，幂等续跑）、`internal/wechat`（token/传图/AddDraft/freepublish Publish+Status）、`internal/publishing`（LayoutClient 已会调排版工具两个接口）、`internal/imagecache`（qpic 防盗链下载）、`internal/feishu`（文本私信）、豆包 OptimizeClient、RBAC | 无 FoodTalks 客户端；无「拉取已发布文章」能力；无飞书卡片/群消息/回调；流水线只有 wechat_mp 方向 |
| FoodTalks 网站 | `POST/PUT api-we-test.foodtalks.cn/news/admin/news`（测试环境抓包文档），字段含 `publishStatus`/`isPublic`/`content`(HTML)/`coverImg`/`categoryIds` 等 | 正式环境地址未知；鉴权是抓包的个人登录 Bearer（会过期）；响应结构/枚举未知；**图片上传接口未知** |

**约束（不可violate）**：
- 本仓库红线 #19：`fbif.js` 在网站任务中字节级不动；网站与公众号渲染器物理隔离。
- 本仓库红线 #24：网站渠道表格必须走 `shared/table-renderer.js` 的 `outerBorder`+`cmsSurvival` 开关；发布进 CMS 的 HTML 要活过 CMS 清洗。
- 资讯订阅铁律：PG 迁移不自动、部署前手动跑；只新增不改既有逻辑；发布不可逆动作必须人工确认（本变更沿用：「公开翻转」前必须过审核闸门，直到分级自动化被显式开启）。
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

### D1. 编排中枢放在「FBIF资讯订阅」系统里，不新建第三个服务，不引入消息队列

**选择**：公众号→网站的流水线作为 `internal/publishing` 的新 target（`foodtalks_site`）实现，复用 `publish_jobs` 持久化状态机、`safe.Go` 异步推进、前端/API 轮询范式。

**理由**：ChatGPT 方案里的 Ingestion Service / Normalizer / Job Queue（Redis/BullMQ）等 6 层微服务，在资讯订阅系统里已经以单体形式全部存在（采集=ingest、状态机=publish_jobs、队列=PG 表 + safe.Go、通知=feishu 包）。为一条每天最多几篇文章的链路引入 Redis 队列纯属过度设计（规则二）。资讯订阅系统 2026-07-05 刚验证过同构反向链路（热点→公众号），部署、RBAC、模型中心、imagecache 全部现成。

**被否掉的方案**：
- *独立编排服务（ChatGPT 建议）*：多一个部署单元/凭证分发/可观测盲区，收益为零。
- *放在排版工具仓库*：排版工具定位是「无状态渲染能力」（本仓库 Go API 无数据库），塞进状态机会让它长出第二个身份；且微信/飞书客户端在资讯订阅侧都是现成的。
- *多维表格当队列（用户原始设想之一）*：两位顾问 AI 与本设计三方一致否决——无幂等、无事务、无重试语义；后期可作只读看板。

### D2. 排版仓库增量 = render-service 支持 `packId=ft-default`，渲染唯一真理源不变

**选择**：`render-service/render.mjs` 按 fbif 同款方式接入 `public/js/templates/foodtalks-site.js`，`server.mjs` 的 packId 白名单放开 `ft-default`，Go 代理透传。输出契约：

```
POST /api/render
请求: { title, source, blocks: Block[], isRepost: bool, packId: "ft-default",
        withFooter?: bool }
响应: { html, meta: { title, coverCandidates: [...] } }
```

**必须带的网站契约**（这正是「不能让资讯订阅自己拼 HTML」的原因）：
- 表格走 `shared/table-renderer.js` 的 `outerBorder`+`cmsSurvival`（属性+style 双写，红线 #24），图片按 `wpx` 输出 `width` 属性+style 双写。
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

正文 `<img>` 与封面的 mmbiz.qpic.cn URL 有防盗链且不受我们控制，网站端必须转存：

```
render 输出 HTML → 枚举 img src（mmbiz/外站）
  → imagecache.FetchAndStore 下载（已有 qpic 防盗链 UA/referer 处理）
  → 上传 FoodTalks 存储（接口待 Phase 0 抓包/索要——硬阻塞项）
  → 重写 src → content 落库
```

- 逐图串行 + 失败重试一次（沿用 agent-publish-pipeline 的微信频控经验）。
- 任何一张正文图转存失败 → job `failed` 并列出失败 URL（显式失败），不允许「个别图裸奔外链」的半成品进站。
- `original_url → new_url` 映射存 job 记录，便于追溯与重跑跳过已传图。

**被否掉的方案**：*直接引用 mmbiz 外链*（防盗链裂图）；*先上自己 OSS 再说*（FoodTalks CMS 前台是否允许任意 OSS 域名未知，且多一跳存储；若网站上传接口短期拿不到，OSS+CDN 才作为过渡方案启用，并在 Open Questions 中单列）。

### D5. 触发：轮询 `freepublish/batchget` 为主，**不启用微信「服务器配置」事件推送**；手动贴 URL 是 MVP 主路径与永久兜底

**选择**：
- **手动触发（Phase 3，MVP）**：资讯订阅新增 `POST /api/publish/site-sync`（body: 公众号文章 URL），直接建 job 跑流水线。这条不依赖微信任何接口，先把 90% 的价值（排版/转图/发布/审核）拿到手。
- **自动发现（Phase 5）**：poller 定时（建议 15 分钟）调 `freepublish/batchget` 拉最近已发布列表 → 与去重表（`article_id`/URL hash 唯一索引）比对 → 新文章逐篇（多图文按 index 拆分）建 job。
- **明确不做**：在公众号后台启用「服务器配置」接收事件推送。

**理由（这是对用户原始设想「事件订阅推送到多维表格」的关键反驳）**：
1. 微信没有「后台人工发布成功」的对外事件；`PUBLISHJOBFINISH` 只回推 API 发起的发布。
2. **启用服务器配置后，公众号的自动回复、部分后台功能会被开发者模式接管——FBIF 是运营中的大号，这个副作用不可接受**（需与运营确认，但默认按不可接受设计）。
3. 轮询只需 AppSecret + IP 白名单，不动公众号任何运营配置，风险为零；15 分钟延迟对「同步到网站」完全无感。
- **需实测的覆盖度风险**：公众号后台「群发」和「发布」是两个动作，`freepublish/batchget` 官方口径只覆盖「发布」体系。若实测发现运营日常用的「群发」文章拉不到 → 自动发现降级为「运营在飞书群贴链接/贴进表单」的半自动，手动触发路径本来就在，链路不受影响。这是 Phase 5 第一个验证项。

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

**风险审核节点**：`rendering` 后调豆包出 `risk_notes`（复用 OptimizeClient 骨架，prompt 换成红线清单：功效表达/转载授权/竞品评价/数据来源），结果只进卡片展示。分级自动化（Phase 6）：`pass` 且原创且图片全成功 → 可配置跳过 `awaiting_review` 直接 `publicizing`；`warning` 必须人审；`block` 只能驳回或人工处理。默认全人审。

### D8. 安全与凭证

- 飞书 App Secret（`cli_aac5d107d9381bdf`）已在对话/文档明文出现 → **上线前在开放平台重置**，新值只进服务器 env（600 权限）。
- 微信 AppSecret 同理 env-only；建议用户择机把飞书文档里的明文挪走。
- FoodTalks token env-only；接口文档里的抓包 token 视为已泄露级别，正式上线换服务级凭证。
- 所有出站调用打审计日志（job_id、动作、操作人），但日志不含任何 secret。

## Risks / Trade-offs

- [FoodTalks「隐藏态」语义与假设不符（比如 isPublic=0 仍出现在列表/搜索）] → **已应验并已解决（2026-07-10 冒烟）**：isPublic=0 确实不隐藏（未登录直链完整可见）；正确的隐藏进站态是 `publishStatus=0`（草稿），全流程已走通，见 `docs/foodtalks-api-contract.md`。
- [FoodTalks 图片上传接口拿不到] → 硬阻塞，Phase 0 通过抓包 CMS 编辑器或直接询问网站后端解决；过渡方案（自有 OSS+CDN）需先确认 CMS 不清洗外域 img src。
- [抓包 token 过期导致线上任务集体失败] → 客户端对 401/403 专项识别，job 错误信息直接写「凭证过期，联系 ×× 换发」；Phase 0 争取长效服务凭证是正解。
- [freepublish/batchget 拉不到「群发」文章] → 自动发现降级半自动（群里贴链接），流水线本体不受影响（D5 已设计为触发可插拔）。
- [公众号临时链接（含 chksm）过期导致 fetch-article 失败] → 轮询路径用 batchget 返回的永久 URL；手动路径提示尽快处理；失败可重试。
- [渲染器改版让 render-service 网站模板桩失效] → 网站渲染冒烟测试进本仓库 `npm test`（与 fbif 同款守护）；CMS 存活断言复用 `tests/table-renderer-shared.test.mjs` 的模拟清洗器。
- [卡片回调依赖飞书事件订阅配置，部署复杂] → Phase 3 MVP 用文本通知 + 前端确认兜底，卡片是增强不是阻塞。
- [重复发布事故] → source_key 唯一索引 + news_id 回填锚点双保险；tasks 里含「同 URL 触发两次只产生一篇」验收项。
- [无人值守误发] → 本变更所有对外写操作集中在 tasks 的显式验证步骤，用户批准后才执行；流水线上线后，公开动作永远在审核闸门之后。

## Migration Plan

1. **Phase 0（人工前置，用户醒来后）**：重置飞书 App Secret；向网站后端索要正式环境/服务级 token/图片上传接口/枚举表；与运营确认公众号日常用「群发」还是「发布」；建审核群、机器人入群。
2. **Phase 1**：FoodTalks 测试环境冒烟（创建隐藏文章→前台验证不可见→PUT 编辑→翻公开→再翻回/下线），产出《字段枚举与响应结构》文档回填本设计。
3. **Phase 2（本仓库）**：render-service 接 ft-default → 本地渲染真实公众号样例 → 与人工 `/ft/` 排版结果 diff 验证同源 → 测试进 CI → 部署。
4. **Phase 3（资讯订阅，MVP 闭环）**：`internal/foodtalks` + 流水线 site 链路 + 手动 URL 端点 + 文本群通知；PG 迁移手动跑；测试环境全链路真机验收。
5. **Phase 4**：飞书交互卡片 + 回调 + 结果通知，替换文本降级路径。
6. **Phase 5**：freepublish 轮询自动发现（先实测覆盖度）。
7. **Phase 6**：分级自动化开关（默认关，积累人审数据后再开）。
8. **回滚**：全部是新端点/新 target/新按钮，回滚=停 poller+下端点，表保留无害；网站侧误发可 PUT 翻回隐藏。

## Open Questions

- FoodTalks 正式环境 Base URL、服务级鉴权方案？（Phase 0，问网站后端）
- 图片上传到 FoodTalks 存储的接口？（Phase 0，抓包或索要——当前最大阻塞）
- ~~`publishStatus` 的「前台完全不可见」正确组合？~~ **已实测**：0=草稿/2=下线均不可见，1=发布可见，单向门 1↛0（`docs/foodtalks-api-contract.md`）。仍待后端确认：`isPublic` 真实语义、`sourceType`/`isLatest`/`commentEnable` 枚举、publishStatus 2 vs 3 官方口径、「有料」categoryId 表。
- FBIF 订阅号运营日常动作是「群发」还是「发布」？决定 Phase 5 自动发现的可行性。（Phase 0，问运营）
- 网站文章的 `categoryIds` 由谁定：固定默认？LLM 按内容选？人在审核卡片上改？（MVP 先可配置默认值，卡片改分类留到 Phase 4+）
- 资讯订阅生产环境出口 IP 是否已在公众号 IP 白名单？（agent-publish-pipeline 用的是测试号 Ecooo，正式号白名单需另配）
- 审核群 chat_id（等群建好、机器人入群后获取）。
