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
