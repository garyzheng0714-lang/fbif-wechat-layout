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
- Job 字段：id/source_key/source_url/stage/failed_stage/error/title/source_name/blocks_json/content_html/cover_url/image_map/foodtalks_news_id/article_url/created_at/updated_at
- source_key 归一化与幂等语义照抄资讯订阅仓 `internal/publishing/site.go` 的 `NormalizeSiteSourceKey`（支持 /s/ 短链 + biz/mid/idx/sn 长链；拒绝无定位参数形态）

### S2. 状态机（阶段进程内化——这是迁移的最大红利）
```
created → fetching → rendering → mirroring → staging → awaiting_review
        → [review approve] → publicizing → published
        → [review reject]  → rejected（终态；FoodTalks 侧留草稿不动）
任一步失败 → failed（failed_stage 续跑，retry 端点）
```
- fetching：**进程内**调 main.go 既有抓取链路（`handleFetchArticleWithStore` 的核心逻辑抽函数复用，勿复制粘贴第二份）→ title/source/blocks 落库。注意 fetch-article 现有实现返回 blocks JSON。
- rendering：走 `render_proxy.go` 既有转发内核（RENDER_SERVICE_URL + RENDER_TOKEN env 已有）传 packId=ft-default → content_html
- mirroring：**进程内**调 `handleImageUpload` 的上游代理逻辑（WECHAT_UPLOAD_ENDPOINT，urls 模式批量）→ 重写 content_html 的 img src + cover；任一图失败 fail loud 列 URL；封面无值用正文第一图兜底
- staging：foodtalks 客户端 CreateNews（publishStatus=0 草稿）；foodtalks_news_id 幂等锚点（非 0 走 UpdateNews）
- publicizing：UpdateNews 全量覆盖翻 publishStatus=1（**唯一不可逆动作，只能由 review approve 触发**）；article_url = FOODTALKS_FRONT_URL + /news/<id>
- 通知：awaiting_review 与终态各发一条群文本；发送失败只 WARN 不 fail job

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
- **回调端点** `POST /api/feishu/card-callback`：URL 验证 challenge 原样回；verification token 校验（env `FEISHU_VERIFICATION_TOKEN`，未配置时 WARN 放行——首次配置后必须回填）；`card.action.trigger` → operator.open_id + action.value → 复用 review 核心逻辑（从 HTTP handler 抽公共函数，**审核语义与权限红线不变**：仅 awaiting_review 可操作、approve 是唯一公开路径）；非待审态回 toast「已被处理」+ 当前终态卡防重复点击。
- **发送**：staging 成功发 interactive 卡片，构建/发送失败降级回文本通知（现有能力保留）。
- **审核人身份**：operator.open_id 落 job（新字段 reviewed_by/reviewed_at），留痕进终态卡。
- **外部配置**（代码上线后）：开放平台应用后台「事件与回调」配回调地址 `https://fbifmp-layout.foodtalks.cn/api/feishu/card-callback` + 订阅 card.action.trigger + 取 Verification Token 回填 publish.env。Caddy /api/* 已通配转 9001，无需改配置。

### M4 实施（2026-07-10 完成；`cd api && go test ./...` 全绿 + 根 `npm test` 792 全绿 + 新文件 gofmt 干净 + public/ 零字节改动）
- **文件**：新增 `api/feishu_review_card.go`（pending 蓝卡 + done 绿/红终态卡构建）、`api/feishu_card_callback.go`（`POST /api/feishu/card-callback`：challenge 握手 / verification token 校验 / card.action.trigger → review 核心）、`api/feishu_card_callback_test.go`（7 个新测试）；改 `api/publish_jobs.go`（+reviewed_by/reviewed_at）、`api/publish_pipeline.go`（review 核心加 reviewedBy 参数落留痕；+notifyCard 依赖；staging 通知位点升级 notifyReviewPending：卡片优先、失败降级文本）、`api/feishu_notify.go`（抽 sendMessage 内核 + sendCard interactive）、`api/publish_http.go`（review 调用传 reviewedBy=""）、`api/main.go`（注册回调路由）。
- **review 核心归一**：HTTP 端点与卡片回调共用 `publishPipeline.review(ctx, id, action, reviewedBy)` 一个函数——仅 awaiting_review 可操作、approve 仍是翻公开唯一路径；红线测试（ftStub forbidPublish 守卫 + TestPublishNoPublicizeWithoutApprove）继续全绿。
- **卡片契约**（cardkit 红线，TestReviewPendingCardContract 字符串断言锁死）：schema 2.0 + update_multi；无 emoji；「通过并发布」primary_filled+done_outlined+confirm（文案明说不可逆）、「驳回」danger+confirm（照规范 03 §9 权威示例不带 icon）、「查看原文」default+preview_outlined open_url；横排按钮 column_set（schema 2.0 禁 action/note）；终态卡 person capsule 留痕（HTTP 审核无 open_id 时退化「经管理后台」纯文本）。
- **回调安全模型**：无 ADMIN_PASSWORD 门（飞书服务器调用）；FEISHU_VERIFICATION_TOKEN 比对（url_verification 顶层 token / 事件 header.token 双形态），不匹配 401；未配置 WARN 放行（仅首次配置窗口，配好必须回填 publish.env）；业务性失败（任务不存在/已被处理）回 200+toast 防飞书重试轰炸；防重复点击：终态回「已被处理」+当前终态卡（update_multi 群内旧卡一并刷新）；非终态非待审（failed/publicizing 半途）只回 toast 指路管理后台、不动卡片。
- **实施决策**：① 卡片发送成功不再发文本（同一事件不双通知），失败才降级——测试锁定；② published/rejected 既有文本通知位点保留（HTTP 审核路径的既有能力，卡片回调场景终态卡已原地刷新）；③ job_id 回传兼容数字/字符串两种 JSON 形态（coerceFeishuJobID）。
- **与下方「预览链接」裁定的衔接（✅ 2026-07-10 已随 M5 实施）**：卡片第三键已换成「预览效果」（open_url=预览签名链接），原文链接移入卡片信息色块一行——单点改了 `buildReviewPendingCard`，回调与安全模型未动。详见「M5 + 预览页实施」节。
- [ ] 开放平台外部配置（用户照做，见上方「外部配置」行）

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
7. **进程重启不自动续跑**：文件持久化保证 failed/awaiting_review 不丢，但重启后不自动 spawn（谨慎优先——尤其 publicizing 半途的单，由人经 retry/review 显式推进）。
8. **列表端点是摘要视图**：GET /api/publish/jobs 清空 blocks_json/content_html/image_map 大字段，全量走详情端点。
9. **gofmt 基线冲突**：仓库既有文件（account_catalog.go 等）在当前 gofmt 下本就不干净（中文注释 `//（` 风格）；新增文件全部 gofmt 干净，render_proxy.go 仅保留基线原有两行注释原样（不顺手重排既有代码）。
10. **飞书通知位点**：待审核（staging 成功）/ 已发布（publicize 成功）/ 已驳回（reject）三处；failed 不通知（施工图口径「awaiting_review 与终态各一条」）。测试注入 notify fake，另有 httptest 桩直测 token 缓存与消息形态。
11. **（M5）未落 cover_candidates 字段**：施工图 M5 落库行列了 cover_candidates，实施时不存——候选=正文图列表，随时可从 content_html 用 extractImgSrcs 推导（enrichCandidateImages），存第二份就是冗余漂移面；前端 M6 九宫格选图从详情端点的 content_html 提取即可。
12. **（M5）偏离记录 4「Summary 暂空」作废**：buildSiteNews 的 Summary/NewsTags/Author 现由 enriching 阶段的 AI 预填（或规则兜底）供给。
13. **（预览页）域名取法**：Go 侧此前没有站点域名常量；预览链接 base 复用既有 env `WECHAT_UPLOAD_ORIGIN`（deploy.yml 已写死为 https://fbifmp-layout.foodtalks.cn）→ 缺失时默认常量兜底，不新增平行 env（最少新概念）。
14. **（预览页）failed 不可预览**：施工图「awaiting_review 及之后」按字面实施为 {awaiting_review, publicizing, published, rejected} 白名单；failed 的 failed_stage 可能停在 fetching/rendering（content_html 未定型半成品），不给看。
15. **（M5）ARK Base URL 可 env 覆盖**（`ARK_BASE_URL`，默认火山北京域名）：与 FOODTALKS_BASE_URL 同模式，测试 httptest 桩靠它注入，生产不用配。
