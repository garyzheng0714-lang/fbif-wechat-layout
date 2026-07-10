# WORKLOG：发布流水线迁入排版工具（架构反转，用户 2026-07-10 裁定）

## 用户裁定（原话要点）

> 「整个的架构，其实排版工具才是最主要的，我从来没有说过信息源一定要来源于 feed.foodtalks.cn。这两个工具打通只是发布的其中一种方式，另外还有多种方式：手动找到文章在排版工具排好版直接发布；直接监控对应的公众号一发就跟着发；我们自己的公众号一发就跟着发。**主体和绕不开的是排版工具**，资讯流只是获取信息的其中一个路径。」

**架构反转**：发布状态机、FoodTalks 客户端、飞书审核通知全部归**排版工具**（本仓库 Go api/）。资讯订阅降级为未来的「输入源之一」（它发现文章后调排版工具的发布 API）。原资讯订阅仓的 site 链路实现（internal/foodtalks + internal/publishing/site.go + api/publish_site.go，提交 279902e/f4a3cd4）**退役为参考实现**，待本仓库打通后清理。

## 目标（本次：测试环境打通）

`POST /api/publish/site-sync {url}` → 抓文章 → 排版(ft-default) → 图片转存 → FoodTalks 测试环境草稿进站 → 飞书群通知（应用 cli_aac5d107d9381bdf「FBIF文章排版工具」，已实测在审核群 oc_0e469b957145689516d8f542c175562b 里能发消息）→ **停在待审核**（红线：翻公开必须用户亲自裁决）→ `POST /api/publish/jobs/{id}/review {action}` approve/reject。

## 设计（实施前钉死）

### S1. 存储：JSON 文件（贴仓库范式，零新依赖）
- `data/publish-jobs.json`（路径随 RULES_STORE_PATH 同目录规则；env `PUBLISH_JOBS_PATH` 可覆盖）
- 结构：`{"next_id": N, "jobs": [{...}]}`；进程内 sync.Mutex + 临时文件原子替换写（同 rulePresetStore 模式）
- Job 除正文/站点锨点外，还持久 `source_aliases` / `approved_risk_version` / CreateNews 与 Publicize 两类 intent / 通知 outbox。
- source_key：长链唯一身份是 `__biz+mid+idx`，`sn/chksm/scene` 不参与去重；短链先用 slug，抓取后把页面 `og:url`/最终 URL 解出的长链身份原子加入 `source_aliases`。旧版含 `sn` 的已存 key 在比较时动态归一，避免升级反而重文。

### S2. 状态机（阶段进程内化——这是迁移的最大红利）
```
created → fetching → rendering → mirroring → enriching → assessing → staging → awaiting_review
        → [review approve] → publicizing → publicizing_updating → published
                                             └→ publicize_reconciliation_required
        → [review reject]  → rejected（终态；FoodTalks 侧留草稿不动）
任一步失败 → failed（failed_stage 续跑，retry 端点）
```
- fetching：**进程内**调 main.go 既有抓取链路（`handleFetchArticleWithStore` 的核心逻辑抽函数复用，勿复制粘贴第二份）→ title/source/blocks 落库。注意 fetch-article 现有实现返回 blocks JSON。
- rendering：走 `render_proxy.go` 既有转发内核（RENDER_SERVICE_URL + RENDER_TOKEN env 已有）传 packId=ft-default → content_html
- mirroring：**进程内**调 `handleImageUpload` 的上游代理逻辑（WECHAT_UPLOAD_ENDPOINT，urls 模式批量）→ 重写 content_html 的 img src + cover；任一图失败 fail loud 列 URL；封面无值用正文第一图兜底
- staging：foodtalks 客户端 CreateNews（publishStatus=0 草稿）；foodtalks_news_id 幂等锚点（非 0 走 UpdateNews）
- publicizing：approve CAS 同时绑定当前风险版本；sink 再复核 approve/reviewed_at/版本/pass|warning。外调前确定失败落 `failed(publicizing)` 并发结果通知，修复后可安全 retry。
- publicizing_updating：先持久 PUT intent 再外调。传输错、进程崩溃或成功后本地落盘不确定，一律进 `publicize_reconciliation_required` 并通知人工核对，不盲目重放 PUT。

### S3. FoodTalks 客户端：从资讯订阅仓 `internal/foodtalks/client.go` 搬入 `api/foodtalks.go`
- 改 package main；逻辑逐行保真（27 字段 News/id 二态 RawMessage/错误三分类/publishStatus 客户端自律 0/1/2/网关三头）
- env 同名：FOODTALKS_BASE_URL / FOODTALKS_TOKEN / FOODTALKS_ORIGIN（缺任一 fail loud）
- 契约唯一真相源 `docs/foodtalks-api-contract.md` 不变

### S4. 飞书通知：`api/feishu_notify.go` 极简实现（不引 SDK）
- tenant_access_token 获取 + 缓存（过期前刷新，简单 mutex）
- `POST /open-apis/im/v1/messages?receive_id_type=chat_id` 发文本
- env：FEISHU_APP_ID / FEISHU_APP_SECRET / FEISHU_REVIEW_CHAT_ID；任一缺失→通知禁用（WARN，不影响流水线）

### S5. HTTP 端点（挂 main.go mux，权限复用规则后台 ADMIN_PASSWORD 门）
- `POST /api/publish/site-sync` {url}：source_key 幂等（已有未终态 job 返回它）→ 建 job → goroutine 异步推进 → 返回 job JSON。权限：admin 密码头（与 /api/rules/admin 同机制——登录 M2 落地后换角色权限）
- `GET /api/publish/jobs/{id}`、`GET /api/publish/jobs?limit=20`
- `POST /api/publish/jobs/{id}/review` {action: approve|reject}：仅 awaiting_review 可操作；admin 密码门
- `POST /api/publish/jobs/{id}/retry`：failed 从 failed_stage 续跑

### S6. 测试（Go，httptest 桩全部外部调用）
- 状态机推进到 awaiting_review 停住（CreateNews 收到 publishStatus=0）
- **approve 前绝不公开**：无 review 动作时 UpdateNews 永不被调
- reject 终态不可再 approve；approve 后 UpdateNews 收 publishStatus=1
- failed 续跑幂等：blocks 不重抓、已转存图不重传、CreateNews 恰一次
- 同 source_key 不建新单；通知失败不 fail job
- JSON 存储并发安全（并行建单 id 不撞）

## 纪律
- 现有前端（app.html/site.html）与所有既有 API 路径零改动；红线 #19/#23 不碰
- 凭证 env-only；ADMIN_PASSWORD 机制复用不新增平行鉴权
- `cd api && go test ./...` + npm test 全绿；单文件 ≤900 行
- **翻公开只能由 review approve 触发**（用户红线，测试也要锁这条）

## 进度
- [x] 设计钉死（本文档）；飞书机器人「FBIF文章排版工具」在审核群发消息已实测成功
- [x] S1–S5 实施 + S6 测试（2026-07-10 完成，`cd api && go test ./...` 全绿 + 根 `npm test` 792 全绿；public/ 零字节改动）
  - S1 `api/publish_jobs.go`（JSON 存储，mutex + 临时文件原子替换，PUBLISH_JOBS_PATH 可覆盖、默认随 rules 存储同目录）
  - S2 `api/publish_pipeline.go`（状态机 + normalizeSiteSourceKey + 各阶段默认实现进程内复用既有内核）
  - S3 `api/foodtalks.go`（客户端逐行搬入，标识符加 foodTalks 前缀防单包冲突）
  - S4 `api/feishu_notify.go`（tenant_access_token 缓存 + 群文本；env 缺失静默禁用）
  - S5 `api/publish_http.go`（/api/publish/site-sync、jobs、jobs/{id}、review、retry；全端点 ADMIN_PASSWORD 门）
  - S6 `api/publish_pipeline_test.go` + `api/publish_http_test.go` + `api/feishu_notify_test.go`（16 个新测试，外部调用全 httptest 桩；红线「无 approve 则 UpdateNews 永不被调」由 FoodTalks 桩 forbidPublish 守卫 + 专门测试双保险锁死）
- [x] 测试环境全链路真机（2026-07-10 部署 5ecb1b4 后生产服务实跑）：job #1（星巴克短链）fetching→rendering→mirroring→staging 全通 → FoodTalks 测试环境草稿 **news 7243** → 停在 awaiting_review（前台 404 复核 ✓）→ 群通知已发（无 WARN 日志）→ **等用户当审核人裁决 approve/reject**
- [x] 部署踩坑根治：deploy.yml 每次部署整文件重写 /var/www/api/.env，手工键必被抹——流水线配置改放独立 `/var/www/api/publish.env` + systemd drop-in（fbif-api.service.d/publish-env.conf 加 EnvironmentFile），部署不再破坏。切正式环境只改 publish.env 四行（BASE_URL/TOKEN/ORIGIN/FRONT_URL）+ restart
- [ ] 正式环境接入（等 IP 白名单：**121.40.214.5**，用户下午通知后端；token 已有 ai_news 50 年）
- [ ] 资讯订阅仓参考实现退役清理（design.md D1 已反转回写 89295c6）

## M4 提前：飞书卡片审核交互（2026-07-10 用户提出「点按钮回调接收事件」）

### 设计（钉死）
- **审核卡（pending）**：schema 2.0、`config.update_multi:true`、**禁 emoji**（cardkit skill 红线）；header 蓝 + text_tag「待审核」黄；body=grey-100 信息色块（标题/来源公众号/任务号/目标环境/进站时间）+ 按钮行：「通过并发布」primary_filled + confirm 二次确认（文案明说「发布后公开可见且发布动作不可逆」）｜「驳回」danger + confirm｜「查看原文」default open_url。behaviors callback value=`{"action":"approve|reject","job_id":N}`。
- **终态卡**：approve→绿 header「已发布」+ `<person>` 操作留痕 + 时间 + 「查看文章」open_url(article_url)；reject→红 header「已驳回」+ 留痕。回调响应直接带新卡（3 秒内 `{toast, card}`），群内所有人可见（update_multi）。
- **回调端点** `POST /api/feishu/card-callback`：URL 验证 challenge 原样回；verification token 校验（env `FEISHU_VERIFICATION_TOKEN`，缺失或不匹配均 fail-closed）；`card.action.trigger` → operator.open_id + action.value，并要求审核人命中 `FEISHU_REVIEWER_OPEN_IDS` 白名单，再复用 review 核心逻辑（从 HTTP handler 抽公共函数，**审核语义与权限红线不变**：仅 awaiting_review 可操作、approve 是唯一公开路径）；非待审态回 toast「已被处理」+ 当前终态卡防重复点击。
- **发送**：staging 成功发 interactive 卡片，构建/发送失败降级回文本通知（现有能力保留）。
- **审核人身份**：operator.open_id 落 job（新字段 reviewed_by/reviewed_at），留痕进终态卡。
- **外部配置**（代码上线后）：开放平台应用后台「事件与回调」配回调地址 `https://fbifmp-layout.foodtalks.cn/api/feishu/card-callback` + 订阅 card.action.trigger + 取 Verification Token 回填 publish.env。Caddy /api/* 已通配转 9001，无需改配置。

### M4 实施（2026-07-10 完成；`cd api && go test ./...` 全绿 + 根 `npm test` 792 全绿 + 新文件 gofmt 干净 + public/ 零字节改动）
- **文件**：新增 `api/feishu_review_card.go`（pending 蓝卡 + done 绿/红终态卡构建）、`api/feishu_card_callback.go`（`POST /api/feishu/card-callback`：challenge 握手 / verification token 校验 / card.action.trigger → review 核心）、`api/feishu_card_callback_test.go`（7 个新测试）；改 `api/publish_jobs.go`（+reviewed_by/reviewed_at）、`api/publish_pipeline.go`（review 核心加 reviewedBy 参数落留痕；+notifyCard 依赖；staging 通知位点升级 notifyReviewPending：卡片优先、失败降级文本）、`api/feishu_notify.go`（抽 sendMessage 内核 + sendCard interactive）、`api/publish_http.go`（review 调用传 reviewedBy=""）、`api/main.go`（注册回调路由）。
- **review 核心归一**：HTTP 端点与卡片回调共用 `publishPipeline.review(ctx, id, action, reviewedBy)` 一个函数——仅 awaiting_review 可操作、approve 仍是翻公开唯一路径；红线测试（ftStub forbidPublish 守卫 + TestPublishNoPublicizeWithoutApprove）继续全绿。
- **卡片契约**（cardkit 红线，TestReviewPendingCardContract 字符串断言锁死）：schema 2.0 + update_multi；无 emoji；「通过并发布」primary_filled+done_outlined+confirm（文案明说不可逆）、「驳回」danger+confirm（照规范 03 §9 权威示例不带 icon）、「查看原文」default+preview_outlined open_url；横排按钮 column_set（schema 2.0 禁 action/note）；终态卡 person capsule 留痕（HTTP 审核无 open_id 时退化「经管理后台」纯文本）。
- **回调安全模型**：无 ADMIN_PASSWORD 门（飞书服务器调用）；FEISHU_VERIFICATION_TOKEN 比对（url_verification 顶层 token / 事件 header.token 双形态），缺失或不匹配均 401；`FEISHU_REVIEWER_OPEN_IDS` 缺失或未命中均拒绝审核；业务性失败（任务不存在/已被处理）回 200+toast 防飞书重试轰炸；防重复点击：终态回「已被处理」+当前终态卡（update_multi 群内旧卡一并刷新）；非终态非待审（failed/publicizing 半途）只回 toast 指路管理后台、不动卡片。
- **实施决策**：① 卡片发送成功不再发文本（同一事件不双通知），失败才降级——测试锁定；② published/rejected 既有文本通知位点保留（HTTP 审核路径的既有能力，卡片回调场景终态卡已原地刷新）；③ job_id 回传兼容数字/字符串两种 JSON 形态（coerceFeishuJobID）。
- **与下方「预览链接」裁定的衔接（✅ 2026-07-10 已随 M5 实施）**：卡片第三键已换成「预览效果」（open_url=预览签名链接），原文链接移入卡片信息色块一行——单点改了 `buildReviewPendingCard`，回调与安全模型未动。详见「M5 + 预览页实施」节。
- [ ] 开放平台外部配置（用户照做，见上方「外部配置」行；⚠️ 2026-07-10 用户已在后台把「订阅方式」选为**长连接**——见下方 M4b，Webhook 的回调地址/verification token 配置只在切回 Webhook 模式时才需要）

### M4b：卡片回调升级长连接（WebSocket）模式（2026-07-10 完成）

用户在飞书开放平台已选「使用长连接接收回调」，Webhook 模式升级为长连接，用官方 Go SDK。

- **SDK**：`github.com/larksuite/oapi-sdk-go/v3` **v3.9.9**（go.mod 直接依赖；连带 indirect：gorilla/websocket v1.5.0、gogo/protobuf v1.3.2）。类型签名全部经 go doc 查证：`larkws.NewClient(appID, appSecret, WithEventHandler, WithAutoReconnect(true), WithOnReady/OnReconnecting/OnReconnected/OnError)` + 阻塞式 `Start(ctx)`；handler=`dispatcher.NewEventDispatcher("","").OnP2CardActionTrigger(...)`；事件 `callback.CardActionTriggerEvent`（`Event.Operator.OpenID` string / `Event.Action.Value` map[string]interface{}）；响应 `callback.CardActionTriggerResponse{Toast, Card{Type:"raw", Data: 卡片 map}}`。
- **两模式并存**：Webhook 端点（`/api/feishu/card-callback`）保留作备用通道，**生效哪条由飞书后台「事件与回调 → 订阅方式」单选决定**（选长连接时飞书不再调 Webhook）。长连接由 SDK 建连时用 app_id/app_secret 内置鉴权，无需 FEISHU_VERIFICATION_TOKEN（那是 Webhook 验来源用的，保留）。
- **业务段归一**：Webhook 原 `handleReviewCardAction` 的「事件解析后业务段」抽成两模式共用的 `publishPipeline.reviewCardAction(ctx, operatorOpenID, rawValue)`（feishu_card_callback.go，返回中立 `reviewCardOutcome{toastType, toastContent, card}`），Webhook/长连接各自薄壳包装成协议响应——审核语义与权限红线仍只在 `publishPipeline.review` 定义一次，**review 核心与卡片构建零改动（只消费）**。
- **文件**：新增 `api/feishu_ws.go`（`maybeStartFeishuWS(publishPipe)`：FEISHU_APP_ID/SECRET 齐了 goroutine 启动、缺失 log 一行跳过；连接建立/断开/重连/事件处理结果全打人话日志）+ `api/feishu_ws_test.go`（3 个测试：事件体→业务参数解析含 job_id 数字/字符串两形态、空/残缺事件不 panic、长连接 approve 事件走同一 review 核心落 published+留痕+绿终态卡，全程桩不连真实飞书）；改 `api/feishu_card_callback.go`（抽 reviewCardAction）、`api/main.go`（启动处调 maybeStartFeishuWS）、`api/go.mod`/`go.sum`。
- **验证**：`cd api && go test ./...` 全绿 + go vet 干净 + 根 `npm test` 792 全绿 + 新改文件 gofmt 干净 + public/ 零字节改动 + 单文件 ≤900 行；既有红线测试（forbidPublish 守卫、TestPublishNoPublicizeWithoutApprove、Webhook 7 测试）全绿未动。
- **实施偏离**：施工图写 `maybeStartFeishuWS()` 无参，实施为 `maybeStartFeishuWS(p *publishPipeline)`——业务段需要 pipeline，从 main.go 启动处传入（不设包级全局变量）。
- **遗留**：生产 publish.env 已有 FEISHU_APP_ID/SECRET（M4 通知在用），部署重启后长连接自动生效；断线期间飞书侧事件是否补推由飞书平台语义决定，Webhook 备用通道兜底需人工在后台切回。

### M4c：审核异步化 + 持久通知 outbox（2026-07-10 对抗审查收口）

- **3 秒响应合同**：Webhook / 长连接的 approve 只用存储 CAS 把 `awaiting_review` 原子改为 `publicizing`、落 `review_action=approve` / 审核人/时间 / `approved_risk_version`，然后立即回「已受理，正在发布」无按钮进度卡，并明示「最终结果以随后终态卡为准」；不在回调请求内等待 FoodTalks。后台 worker 必须先 CAS 落 `publicizing_updating` + PUT intent 才调 FoodTalks；只有尚未落 PUT intent 的 `publicizing` 能安全恢复。
- **通知 outbox**：job 持久化 `notification_kind/state/seq/attempts/error/sent_at`。进入待审、发布成功、驳回、公开前确定失败、公开结果不确定时，业务状态与通知事件同一次 JSON 原子写；发送前 CAS `pending→sending`，成功按 `seq` ack 清空，失败回 `pending` 并指数退避重试。启动时把崩溃遗留 `sending` 重置并重发。published / rejected 优先发无审核按钮的绿/红终态卡，发卡失败再降级文本。语义为 **at-least-once**：极端「飞书已收、本地 ack 失败」可能重复，但不会漏掉「发布中」之后的最终结果。
- **审核安全**：Webhook token 未配置也 fail-closed；卡片审核人必须命中 `FEISHU_REVIEWER_OPEN_IDS`，空白名单全部拒绝（ADMIN_PASSWORD 管理审核不受影响）。卡片 value 带 `review_version`，版本绑定当前风险级别、证据条目与说明；历史补检或重评变更结论后旧卡自动失效。
- **风险闸门**：风险检查是持久 `assessing` 阶段，失败降级 warning；只有 pass/warning 可人工 approve，block/空值/未知值均 fail-closed。block 卡不渲染通过按钮，正文在风险检查前后逐字节不变。

### 预览链接（2026-07-10 用户裁定：审核必须给未上架的预览链接，不能盲审）
- 排版工具自己出预览页：`GET /preview/publish/{id}?t=<HMAC 签名>`——渲染 job 的 content_html（就是将要发布的字节）+ 标题 + AI 预填参数（封面/摘要/标签/作者）侧栏 + 顶部醒目横幅「预览 · 未发布」。白底、680px 内容宽度近似 FoodTalks 前台阅读版式。
- 签名：`t = hex(HMAC-SHA256(ADMIN_PASSWORD, "publish-preview:" + id))`——免登录可分享给审核人，但不可遍历、不可伪造；未发布内容不裸奔。
- 卡片按钮改为：「通过并发布」「驳回」「预览效果」（open_url 指预览链接，取代「查看原文」的主位；原文链接移入卡片正文一行）。发布面板与 feed 徽标详情同样引用该预览链接。
- 不采用 FoodTalks CMS 后台预览：依赖对方功能与账号体系，跳出飞书体验差且不可控。

## M5：AI 预填发布参数（2026-07-10 用户新增）

用户原话要点：模型审核任务多加一个——选合适封面图、摘要、标签、文章作者等参数信息**预填好**；模型还要输出一段**非常清晰直白简短的优化说明**（格式哪里不规范、怎么调整的）。

### 设计（钉死）
- 新阶段 `enriching`：mirroring 之后 staging 之前（此时图已 OSS 化，模型看的封面候选=最终 URL）。
- 模型：豆包 Doubao-Seed-2.1-pro（ARK responses API，多模态）。输入：文章标题+纯文本正文（截断）+正文图 URL 列表（多模态看图）。输出 JSON：`{cover_url(从候选选一), summary(80-120字), tags([3-5个]), author(从正文抽取，如 Mote莫特), notes(优化说明：2-4 句人话，说明参数怎么选的+发现的格式问题)}`。
- **边界**：预填只产元数据，绝不碰正文 content（无篡改面）；M3 排版修补另做（届时上三道铁闸）。
- 降级：模型失败/超时/解析失败 → 元数据用规则兜底（封面=正文第一图、摘要=正文前 100 字、author=source_name、tags 空、notes="AI 预填未执行"），WARN 不 fail job——AI 是加速器不是闸门。
- env：`ARK_API_KEY`（publish.env）、`ARK_MODEL_ID`（默认 ep-20260623134011-qljpn）；调用系统 prompt 须含「请用简体中文回复」。
- 落库：job 加 cover_candidates/summary/tags/author/ai_notes 字段；buildSiteNews 消费（Summary/Author/NewsTags/CoverImg）。
- 卡片与前端展示 ai_notes + 预填参数；审核人可在前端改（改动优先于 AI 值）。

### M5 + 预览页实施（2026-07-10 完成；`cd api && go test ./...` 全绿 + 根 `npm test` 792 全绿 + 新改文件 gofmt 干净 + public/ 零字节改动 + 单文件 ≤900 行）
- [x] enriching 阶段：新增 `api/publish_enrich.go`（ARK responses API 客户端 + 解析容错 + 规则兜底）；`publish_pipeline.go` 在 mirroring→staging 之间插 `stageEnrich`（pipeline 加 `enrich` 注入字段，默认 `arkEnrichArticle`）。**stageEnrich 永不因 AI fail job**：未装配(nil)/Key 缺失/调用失败/解析失败全走 `fallbackEnrichment`（封面维持 mirroring 已定第一图、摘要=正文纯文本前 100 字、author=source_name、tags 空、notes="AI 预填未执行（原因一句话）"）+ WARN 照常推进。
- [x] ARK 调用：`POST {ARK_BASE_URL:-https://ark.cn-beijing.volces.com}/api/v3/responses`，Bearer `ARK_API_KEY`，model `ARK_MODEL_ID`（默认 ep-20260623134011-qljpn）；system 提示含「请用简体中文回复」；输入=标题+剥标签正文（6000 字截断）+正文图 input_image（≤8 张，OSS URL）；输出严格 JSON，解析剥 markdown 围栏 + 截取首尾大括号；**cover_url 必须原样命中候选列表否则弃用**（防幻觉 URL 混进发布参数）。
- [x] job 字段 summary/tags/author/ai_notes（`publish_jobs.go`）+ buildSiteNews 消费（Summary/NewsTags 直取；Author 优先 AI 识别名、空则回落 source_name——与 M5 前行为一致；CoverImg 用可能被 AI 覆盖的 cover_url）。列表端点保留这些小字段（大字段照旧清空）。
- [x] 预览页：新增 `api/publish_preview.go`，`GET /preview/publish/{id}?t=hex(HMAC-SHA256(ADMIN_PASSWORD,"publish-preview:"+id))`；签名错/任务不存在/阶段太早**同一个 404**（hmac.Equal 常时比对，不给探测面）；无 ADMIN_PASSWORD → 503；仅 awaiting_review/publicizing/published/rejected 可预览（failed 半成品不给看）。页面纯服务端拼：白底、680px 居中、通栏警示黄横幅、h1 标题（转义）、content_html 原样、页尾 AI 参数（封面缩略/摘要/标签/作者/优化说明，文本全转义）+ noindex 头。已用 Playwright 截图目检版式。
- [x] 卡片（`feishu_review_card.go`）：第三键「查看原文」→「预览效果」（open_url=预览签名链接）；原文降为信息色块一行（markdown 链接「打开公众号原文」）；信息区加作者/标签/摘要行；新增 AI 优化说明 grey-100 色块段（notes 空时不渲染，老任务兼容）。回调与安全模型未动。
- [x] 测试：新增 `api/publish_enrich_test.go`（6 个：AI 注入且 content_html 逐字节不变/ARK 500 兜底不 fail/Key 缺失兜底/解析容错+幻觉封面弃用/buildSiteNews 消费/剥标签内核）+ `api/publish_preview_test.go`（3 个：签名门/页面契约与标题转义/阶段门+503）+ 更新 TestReviewPendingCardContract（预览效果+签名链接+AI 说明段+「查看原文」不得再现）。红线测试（forbidPublish 守卫、TestPublishNoPublicizeWithoutApprove、5 端点 401）全绿未动。
- [ ] 生产配置：publish.env 加 `ARK_API_KEY`（`ARK_MODEL_ID` 可省用默认）+ restart——未配置时流水线照跑、参数走规则兜底

## M6：前端交互设计（2026-07-10 用户命题「排版工具前端怎么展示 + feed 里点发布到网站的交互」）

按 DESIGN.md（真实动作出发/最少新概念/按钮在顶栏红线 #4/右滑抽屉既有交互语言）：
- **排版工具·编辑视角**：网站渠道（/ft/*）顶栏在「复制全文」旁加同级主按钮「发布到网站」——发布是与复制同本质的输出动作，不新增页面。点击 → 右滑「发布面板」（复用规则工作台抽屉模式）：文章预览确认 + AI 预填表单（封面九宫格从正文图选/摘要/标签/作者，全可改，显示 AI 优化说明）→「提交审核」。提交后面板显示任务状态流。已在编辑器排好的内容直接走 html-to-blocks→流水线（不再重新抓 URL）。
- **排版工具·任务状态**：顶栏「发布任务」入口 → 右滑任务列表抽屉（状态徽标/失败原因人话/重试按钮）；审核动作主入口仍是飞书卡片（列表只看和重试，不做第二审核入口——最少新概念）。
- **feed.foodtalks.cn 视角**：文章卡片/详情的既有操作区加同级动作「发布到网站」→ feed 后端仅调排版工具 `POST /api/publish/site-sync`（触发器角色，符合架构裁定）→ 界面显示「已提交，任务 #N，审核在飞书群」+ 状态徽标轮询排版工具 jobs API。注意临时链接过期实测坑：发现即触发可行，过期失败给人话提示。
- 交互设计稿：Artifact「发布交互设计」（三场景线框）。

## 踩坑 / 实施偏离记录（2026-07-10）

1. **fetching 无需再抽函数**：`handleFetchArticleWithStore` 的核心 `fetchDirectBlocksWithOptions`（含限流重试）本来就是独立内部函数、handler 已是薄壳。唯一改动：给它加第 4 个返回值 `author`（`extractArticleBlocks` 一直在产出公众号名，此前被丢弃），流水线用作 `source_name`；handler 调用处只是丢弃该值，HTTP 输出逐字节不变（既有 main_upload_test / main_routes_test 全绿佐证）。
2. **mirroring 抽取形态**：`setUploadProxySourceHeaders`（副作用式设头）改造为 `uploadProxySourceHeaders`（纯计算）+ `newUploadProxyRequest`（请求构造内核，handler 与流水线共用）；Content-Type 无条件 Set、Origin/Referer 条件 Set 的历史语义逐条保持。流水线侧 Origin 取 `WECHAT_UPLOAD_ORIGIN` env（无浏览器请求可推导）。
3. **rendering 抽取形态**：`render_proxy.go` 抽 `newRenderServiceRequest` + `renderServiceHTTPClient`，handler 的 500/502 错误映射分支保持原状；流水线传 `Bearer $RENDER_TOKEN`、packId=ft-default、isRepost=true（与资讯订阅仓 LayoutClient.RenderPack 同参数形态）。
4. **Summary 暂空**：施工图 S1 字段清单无 digest（参考实现的 job.Digest 来自 feed 数据，本仓无此来源），buildSiteNews 的 Summary 先留空，待接摘要来源再补。
5. **鉴权门覆盖所有端点**：施工图只在 site-sync/review 明写 admin 门，实施时 GET jobs / retry 一并上门（管理面不裸奔；测试断言 5 端点无密码全 401）。
6. **failed 不算终态**：terminal=published/rejected；同 source_key 重复提交幂等返回 failed 单（用户走 retry），终态后重提才建新单。
7. **进程重启按副作用边界分类恢复**：尚未发生不可逆外调的计算阶段、草稿更新阶段，以及未持久 PUT intent 的 `publicizing` 可自动续跑；`staging_creating` 与已持久 intent 的 `publicizing_updating` 一律转人工 reconciliation，不猜测外部结果、不盲重放请求。
8. **列表端点是摘要视图**：GET /api/publish/jobs 清空 blocks_json/content_html/image_map 大字段，全量走详情端点。
9. **gofmt 基线冲突**：仓库既有文件（account_catalog.go 等）在当前 gofmt 下本就不干净（中文注释 `//（` 风格）；新增文件全部 gofmt 干净，render_proxy.go 仅保留基线原有两行注释原样（不顺手重排既有代码）。
10. **飞书通知位点**：待审核（staging 成功）/ 已发布（publicize 成功）/ 已驳回（reject）之外，公开请求发出前的确定失败发 `publicize_failed`，请求结果不确定发 `publicize_uncertain` 并要求人工 reconciliation。普通上游 failed 仍不打群；一旦已发「发布中」，后续一定有明确结果通知。测试注入 notify fake，另有 httptest 桩直测 token 缓存与消息形态。
11. **（M5）未落 cover_candidates 字段**：施工图 M5 落库行列了 cover_candidates，实施时不存——候选=正文图列表，随时可从 content_html 用 extractImgSrcs 推导（enrichCandidateImages），存第二份就是冗余漂移面；前端 M6 九宫格选图从详情端点的 content_html 提取即可。
12. **（M5）偏离记录 4「Summary 暂空」作废**：buildSiteNews 的 Summary/NewsTags/Author 现由 enriching 阶段的 AI 预填（或规则兜底）供给。
13. **（预览页）域名取法**：Go 侧此前没有站点域名常量；预览链接 base 复用既有 env `WECHAT_UPLOAD_ORIGIN`（deploy.yml 已写死为 https://fbifmp-layout.foodtalks.cn）→ 缺失时默认常量兜底，不新增平行 env（最少新概念）。
14. **（预览页）failed 不可预览**：施工图「awaiting_review 及之后」按字面实施为 {awaiting_review, publicizing, published, rejected} 白名单；failed 的 failed_stage 可能停在 fetching/rendering（content_html 未定型半成品），不给看。
15. **（M5）ARK Base URL 可 env 覆盖**（`ARK_BASE_URL`，默认火山北京域名）：与 FOODTALKS_BASE_URL 同模式，测试 httptest 桩靠它注入，生产不用配。

## M7：编辑部规范倒推 AI 预填提示词（2026-07-10 用户命题「摘要太长；连库看近一月历史文章，倒推转载/原创/摘要/标签规范成底层提示词；要测试准确率；换模型影响要小」）

数据源：阿里云 RDS `mc-fb` 库（只读账号 rss_readonly，凭证在服务器 `/root/.mcfb.cnf`，chmod 600，不进 git）。
核心表：`news_news`（文章主表）/ `news_news_tag`（关联 {news_id, tag_id, tag_name}）/ `basic_tag`（标签库，新闻标签 tag_type_id=6）。

### 数据结论（2026-06-10 ~ 07-10，publish_status=1 共 163 篇）

- **source_type 语义实锤**：0=原创/自有（FBIF食品饮料创新 63、FoodTalks、FBIF食品创新展），1=转载（零售圈/联商网/小食代/咖门…）。93:70。→ 判定是确定性规则（自家号清单），不该让模型猜（规则五）。
- **摘要**：avg 29 字；70% ≤30 字、84% ≤40 字、94% ≤60 字。形态=一句话钩子（提问 26/163、金句、数据点睛、原文引语），句号结尾为主。**绝不是内容概括**——当前 prompt 写死「80-120 字」正是用户嫌太长的根因。
- **标签**：每篇 1-3 个为主（1 个 64 篇/2 个 48 篇/3 个 18 篇；4 个仅 5 篇）。粒度=品牌名/品类/公司/栏目名（喜茶、饮料、FBIF2026会后报告），**无抽象概念标签**（「热点营销」「品牌破圈」这类不出现）。全部关联 basic_tag 词表（近一年实际用过 2063 个，≥5 次的 235 个）→ 打标签应做成**封闭词表选择题**（准确率+换模型鲁棒性双赢）。
- **作者**：132/163 为空——有明确个人署名才填（Mote/何丹琳/李达），机构名偶用。62523 正文有「作者：陈新生」但编辑部没填 → 预填仍抽取（给审核人看），但评测只对「编辑部有填」的样本计精确率。
- **封面**：162/163 是 static.foodtalks.cn（CMS 转存）；我们 API 路径发的 OSS 图是库里唯一外域。风险已记录（契约 §8）。
- **newsTags 500 之谜疑似解**：库里标签是 {tag_id, tag_name} 对象——API 大概率要对象数组不是字符串数组。→ 测试环境草稿实验验证（D 项）。
- **buildSiteNews 漏填 SourceName**：foodTalksNews 有该字段但没赋值，编辑部 163/163 篇都有来源名 → 补。

### 施工项

- [x] A. 数据导出：`api/editorial/tag_vocab.json`（500 条含 tag_id）；`tools/enrich-eval/eval_set.json`（40 篇=30 最新+10 有署名，25 原创/15 转载）
- [x] B. 规范文档 `docs/editorial-norms-foodtalks.md`
- [x] C. 代码：norms_prompt.md（go:embed）+ editorial_norms.go 组装 prompt（{{VOCAB}} 注入前 300 高频标签）；enrich 输出加 source_name；job 加 ai_source；siteSourceFor 确定性判定（SITE_OWN_ACCOUNTS env）；buildSiteNews 补 SourceName/SourceType、author 不再回落 source_name；fallback 摘要 100→40 字、author 空；预览页加「来源」行；全部 api 测试绿
- [x] D. newsTags 实验完成——**结构破解：[{tagId,tagName}] 对象数组且 tagId 必须真实存在（POST ✅）；但后端 PUT 带标签必 500 且污染文章成「任何 PUT 都 500」**（翻公开会卡死）→ 修复前恒空不变，完整矩阵进契约 §9，转后端修（实验文章 7246/7247/7248 停留草稿态无法 API 下线，请后端清理）
- [ ] E. 准确率评测（服务器 121 装 Go1.22.5 跑，本地到 ARK 必超时勿在 Mac 跑）：
  - 第 1 轮（24 样本/16 超时）：摘要长度/风格/标签数量 100%、出处抽取 11/11=100%、作者 3/6、标签命中 14/24=58%、编造 0
  - 修正：①ARK 客户端超时 120s→300s（16 篇超时全是 context deadline，长文思考超 120s；生产同样受益——超时走兜底=浪费 AI）②作者匹配改「一方包含另一方」（编辑部对同一人有 Mote莫特/Mote/mote 三种写法，AI 照抄正文不算错）③标签命中降级为仅报告（**后端反馈标签走单独接口、先不标注**——用户 2026-07-10 转达；且真值混栏目标签 AI 按规范不产出）
  - 第 2 轮（40 样本/0 超时——300s 修复生效）：摘要长度/风格/标签数量 100%、出处抽取 13/14=93%、编造 0、作者 6/10=60%（4 miss：2 个是编辑部把自家商务稿记机构名的记账偏好、2 个真漏抽——中文尾部署名+英文 Author 形态）
  - 修正④：prompt 作者规则补「文首文末都找、认 文｜X / Author: X / By X、编辑排版审核不算作者」；生产主路径已是页面结构化直取（见下节），AI 评测口径=兜底路径质量
  - 第 3 轮：作者 7/10=70%（3 miss：2 个编辑部记账偏好/漏抄全名、1 个真值不在正文）→ 修正⑤：prompt 补「照抄冒号后完整署名」；评测作者口径改「真值在正文里才计分」（与出处同口径，62411 商仲永不在正文属不可抽取）
  - **第 4 轮（终局）：全部门禁 PASS**——摘要长度/风格/标签数量 100%、作者 8/9=89%（唯一 miss=62405 编辑部把 Edited by 记成作者，模型守「编辑不算」规范反被扣，认）、出处 93%、编造 0、标签命中 52% 仅报告。评测器 t.Errorf 门禁全绿（PASS 1396s）
- [x] E 收尾：四轮迭代全达标（见上）；评测口径两次修正都与出处指标同构，非放水
- [x] F. 契约文档更新 + 飞书 API 文档同步 + 部署验证（7d4b318→31a6d17 均绿，服务健康长连接在线）

### 2026-07-10 傍晚追加（用户实测反馈驱动）

- **预览空白根治**：Caddy vhost 是路径白名单，/preview/* 没登记 → 落静态兜底回 index.html 空壳。补登记 /preview/* /api/publish/* /api/feishu/*（deploy/caddy/fbifmp-layout.caddy，仓库=真相源，已热修+提交）。教训进配置注释：**新增 9001 端点必须同步登记 vhost 白名单**。
- **卡片带封面图**（用户要求）：feishuNotifier.uploadImageByURL（im/v1/images 转存拿 img_key）→ 待审卡顶部 img 元素；失败降级不带图 WARN，不阻塞。pipeline 新增 coverImgKey 注入点。
- **卡片带管理后台链接**（用户要求）：信息区「去后台编辑这篇文章」行；FOODTALKS_ADMIN_ARTICLE_URL env 模板（{id} 占位），默认后台首页 https://admin-we-test.foodtalks.cn/#/ ——编辑页路由从后台 JS 包挖到 cms/:siteCode/news/edit/:id 但 siteCode 是运行时值不猜（不编造 URL 红线），**待用户贴一条真实编辑页 URL 即配深链**。正式后台 https://admin-we.foodtalks.cn/#/（用户 2026-07-10 提供）。
- **为什么用自建预览页而非网站草稿链接**（用户问）：草稿 publishStatus=0 前台内容层 404（契约 §3 实测）、API 不返回预览链接、CMS 后台预览要登录会话——飞书卡片场景要免登录即点即看，故用 HMAC 签名预览页；后台编辑链接作为补充（有账号者去改）。
- **并行工作流协同记录**：本仓另一代理同日在写发布安全护栏（publish_risk 红线风险提示 / staging reconciliation / recoverJobs 启动扫描），与 M7 改到同一批文件；等其收敛到全量测试绿后合车提交（31a6d17），未互相覆盖。

### 作者改结构化直取（2026-07-10 用户裁定「从公众号里直接提取，准确度更高」）

排查中抓到暗伤：施工图偏离记录 1 说「extractArticleBlocks 一直在产出公众号名」**与代码不符**——那条链路两处 return 都是空串，生产 job #1/#2 的 source_name 一直是空的（siteSourceFor 会把 FBIF 自家文章误判成转载）。修正：

- 真实页面实测锚点：公众号名 `var nickname = htmlDecode("...")`；作者署名 `<meta name="author">` 与 `var author = "..."` 双写（未署名文章两处都是空串——空也是有效信号）。
- 新增 `extractWechatArticleMeta`（wechat_blocks.go，确定性正则直取）；`fetchDirectBlocksWithOptions` 第 4/5 返回值改为 (sourceName, byline)；stageFetch 落 j.SourceName + j.Author=byline。
- **优先级合同：结构化署名 > AI 正文识别**——stageEnrich 只在 j.Author 为空时才用 AI 值（TestEnrichKeepsStructuredByline 守）；AI 作者评测退化为「兜底路径质量」口径（评测集是 CMS 文章无公众号 meta，正好只测兜底路径）。
- 单测：TestExtractWechatArticleMeta（htmlDecode 包裹/var 兜底/未署名空值三形态）。

### newsTags 谜底闭环（2026-07-10 下午）
字符串数组 500 → 库表启发对象数组 → POST [{tagId,tagName}] 成功但 PUT 必 500 且污染文章 → **后端裁定：标签是单独接口，news payload 本就不带标签**。恒空是正确终态（不是权宜）；AI 标签只做审核展示。实验文章 7246/7247/7248 留测试环境草稿态待后端清理。

## M8：官方预览链接 + DeepSeek 双模型判决（2026-07-10 晚，用户连发三题）

- **官方预览（用户「发布预览会有专门的预览链接/二维码」）**：从后台 JS（懒加载块 src_views_radar_news_detail_vue）挖到铸 token 接口 `POST /news/admin/news/{id}/preview/token`（注意是 POST，GET 404；响应 data={id,token}）；前台路由 `/preview/news/:id?token=`，数据接口是 `GET /news/news/{id}?previewToken=`（页面把 query 的 token 改名 previewToken 转发——直接拿 token 调数据接口会「不存在该资讯」，踩坑记录）。实装：卡片「预览效果」的签名链接现在 302 到官方预览页（**每次点击现铸新 token，规避「短期失效」**）；铸造失败/?local=1 回落本地 AI 参数页。已验证：任务 2 → 302 test.foodtalks.cn/preview/news/7245?token=新token。
- **双模型判决（用户提供 DeepSeekV4-pro ep-20260710175216-hb4qf，「两个一起跑，单独判决节点公平对比，判决节点用 DeepSeek」）**：ARK_MODEL_ID_B 配置即启用——豆包+DeepSeek 并行出案 → 判决节点（ARK_JUDGE_MODEL_ID，缺省=B）收到**匿名**方案一/二+编辑部规范，逐字段裁决（字段只能整体取自一案防混拼幻觉；cover 仍过候选白名单+证据校验）；verdict 并入 notes（卡片可见「双模型判决」结论）。降级链：一侧挂→用另一侧；判决挂→用主模型——多模型不是新故障点。测试锁死：判决输入必含匿名两案+规范、不得泄漏模型身份（ep-A/ep-B/豆包/DeepSeek 字样都不许出现）。
- **生产配置**：publish.env += ARK_MODEL_ID_B / ARK_JUDGE_MODEL_ID（都是 DeepSeek 端点）。
- **并行工作流协同（第二回合）**：风险评估流水线（assessing/assessing_review 阶段+启动风险补检）上线后，重启把历史 awaiting_review 任务迁去补检、几分钟自愈回 awaiting_review（risk=warning）——期间预览 404 属过渡态非 bug。其前端规则面板 WIP 未提交（npm 红），本次提交用 stash 隔离出 api 子集验证后推送（注意 account_catalog.go+registry.js 是逐字节校验对，必须同车）。
- **待用户动作**：①开放平台给应用 cli_aac5d107d9381bdf 开 im:resource:upload 权限（封面进卡片用，日志有直达链接）②群里随便点一次卡片按钮（会被白名单拒绝，正常）→ 从日志收割你在本应用下的 open_id → 配 FEISHU_REVIEWER_OPEN_IDS 重启，卡片审核解锁。
- **评测第 5 轮（双模型判决模式）结果：全部门禁 PASS**（2086s）——摘要长度/风格/标签数量 100%、作者 8/9=89%、出处 93%、编造 0，与第 4 轮单模型基线持平（门禁指标已在天花板）；标签命中 50%/词表率 74% 较单模型微降（judge 偶尔采纳偏离词表的一案，仅报告项）。结论：双模型的价值不在均值提升，在**每篇都有第二意见+判决透明度**（verdict 上卡片）与单点模型故障韧性；代价=每篇 3 次调用、耗时 +50%。生产已启用。

## 2026-07-10 对抗审查最终收口

### 不可逆发布 sink

- `approve` 只持久化人工决策、审核时间和当时的风险版本；FoodTalks `PUT publishStatus=1` 仍由后台 worker 执行。
- sink 在外调前重新检查：人工 approve 存在、`reviewed_at` 存在、风险版本未变化、风险结论严格为 `pass|warning`。`block`、空值与未知枚举全部 fail-closed。
- CreateNews / Publicize 都先持久化 intent；网络结果不确定时转 reconciliation，禁止自动重试。`source_key` 与抓取后发现的 URL aliases 永久幂等。
- 审核通知采用持久 outbox；发布前确定失败发送 `publicize_failed`，结果不确定发送 `publicize_uncertain`，避免群里只留下“发布中”。

### 官方预览与双模型

- 本地 HMAC 预览会优先现铸 FoodTalks 官方预览 URL；只接受配置目标域名上的 `https` URL，路径/查询按组件编码，响应 `no-store` 且页面不发送 referrer。官方 token 获取失败时才回落本地白底预览。
- AI 预填支持主模型 A、备选模型 B 与独立 judge。judge 只能在 A/B 两个已有方案中选字段，不得凭空生成第三套正文或元数据；自由文本仅限审核说明。readiness 要求生产部署使用 `dual` 模式。

### 部署暂停与 readiness

- 候选 API 必须以 `DEPLOYMENT_WRITES_PAUSED=1` 启动：发布/rules 写路由、业务写方法、恢复任务、通知 outbox worker 与飞书长连接全部暂停，避免新旧版本同时写 JSON store。
- 管理员鉴权的 `GET /api/publish/readiness` 只返回布尔值和模式，不泄露凭证；校验 publish store 可读、FoodTalks API/前台/后台/期望目标一致、分类非空、render 可用、飞书通知配置、审核白名单状态与 enrich 模式。
- 部署在 pause 状态完成公网 SHA、健康、鉴权和 readiness 验收后才接受 release；接受后禁止自动回滚，再解 pause 重启并复验。解 pause 后若失败只允许向前修复，避免旧版重复消费已经恢复的外部副作用。

### 当前外部配置边界

- 生产服务器的 FoodTalks 配置已显式声明 `FOODTALKS_EXPECTED_TARGET=test`，并补齐测试后台入口与分类；这是对现状的安全固化，不代表获准切到正式 FoodTalks。
- `FEISHU_REVIEWER_OPEN_IDS` 仍未配置。飞书卡片审核因此按设计拒绝；`ADMIN_PASSWORD` 保护的管理审核可用。白名单只能由用户提供授权审核人的真实 open_id，代码不得猜测或放宽。

## 抓取链路裁定（2026-07-11 用户拍板「就用主链路」）

- **调研结论**：x-reader（github.com/runesleo/x-reader）微信打法=Jina→Playwright 真浏览器降级，思路成熟；但输出 Markdown 丢排版视觉信号（字号/颜色/加粗/居中），喂不进我们的角色分类器——整搬会塌排版保真度（代码里旧回退被弃用正是此因）。正确借法是「Playwright 拿原始 HTML 喂现有解析器」，代价=服务器 +300MB Chromium+新运维面。
- **用户裁定：不建浏览器回退，主链路（直接抓取+3 次退避重试）即最终方案。** 实测依据：正常使用（贴新鲜文章链接）三篇全部一次成功；触发反爬的只有边缘链接（隔天过期的 chksm 临时长链、scene=21 二跳关联阅读链）——这类失败属预期，报错文案已是人话（「链接可能已过期或被限流」），用户重贴新鲜链接即可。
- 勿翻案重做：除非未来出现「新鲜链接也频繁被拦」的实证，才重启浏览器回退议题。

## 审核白名单裁定（2026-07-11 用户拍板「白名单不需要」，已上线 ef79035）

- FEISHU_REVIEWER_OPEN_IDS 未配置 = 审核群成员即审核人（卡片只发指定群+飞书来源鉴权+风险版本绑定+防重复点击都在）；配置则收紧为白名单（原 fail-closed 设计保留为可选硬化）。推翻并行工作流的 fail-closed 默认——显式冲突以用户裁定为准。
