# 任务状态推送约定（排版服务 → 资讯订阅）

> 读者：`FoodTalks_Feed`（资讯订阅，feed.foodtalks.cn）的开发者或维护它的 agent。
> 2026-11-08 起。代码在 `api/publish_status_callback.go`，测试 `api/publish_status_callback_test.go`；改了代码要同步改这份。
> 文章怎么交进来见 [发布接入契约](publish-intake-contract.md)，这一份只讲「交进来以后，状态怎么推回去」。§1–§7 是文章任务；快讯任务（2026-11-29 起）见 §8，签名、重试、怎么回全都一样。

## 一句话

调 `site-sync`（或 `official-sync`）时多带一个 `status_callback_url`，之后这篇任务在 `sync-status` 里看到的那份状态每变一次，排版服务就往这个地址 POST 一次，带签名。不带这个字段，一切和以前一样。`sync-status` 保留不动，兜底回查照用。

## 1. 登记：提交时带上回调地址

在原来的请求体里加一个字段：

```json
{
  "url": "https://mp.weixin.qq.com/s/xxxx",
  "content_kind": "article",
  "classification": "ordinary_confirmed",
  "classifier_version": "feed-v3",
  "status_callback_url": "https://feed.foodtalks.cn/api/internal/layout-status"
}
```

| 规则 | 说明 |
| --- | --- |
| 可选 | 不带 = 不推，请求、响应、任务和以前一模一样 |
| 只许指向资讯订阅 | 协议、域名、端口要和排版服务配置里的 `FOODTALKS_FEED_BASE_URL`（现在是 `https://feed.foodtalks.cn`）一样；路径随你定。别的地址、带用户名密码的、带 `#` 的一律 **400**，任务不建 |
| 长度 | 最多 2048 字节 |
| 两个入口都认 | `site-sync`、`official-sync` 同一套规则 |
| 已有任务也能登记 | 同一原文再提交（响应 `existing: true`）时带上地址，一样登记到那条任务上 |
| 一条任务一个地址 | 后登记的地址替换先登记的 |

登记成功时响应里多一个 `status_callback_url`，原样回显你登记的地址。**没看到这个字段，就说明排版服务没认这个字段**（比如它还是旧版本），请照旧靠回查。

登记那一刻会先推一次当前状态（任务可能在你拿到响应之前就已经往前走了几步），之后每变一次推一次。同一个地址重复登记不会再推，除非上次已经放弃（见 §5）或你回过 410（见 §6）。

## 2. 推送：请求长什么样

```
POST <status_callback_url>
Content-Type: application/json
User-Agent: FBIF-layout-status-callback/1
webhook-id: layout-job-72-5
webhook-timestamp: 1762567200
webhook-signature: v1,K5oZfzN95Z9UVu1EsfQmfVNQhnkZ2pj9o9NDN/H/pI4=
```

```json
{
  "type": "publish.job.status",
  "timestamp": "2026-11-08T02:00:00Z",
  "data": {
    "job_id": 72,
    "seq": 5,
    "job": { "id": 72, "source_key": "...", "stage": "awaiting_review", "...": "..." }
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `type` | 固定 `publish.job.status` |
| `timestamp` | 这一版状态记下的时刻（UTC，RFC3339），等于 `data.job.updated_at` |
| `data.job_id` | 任务编号，和 `site-sync` 响应里的 `job.id` 一样 |
| `data.seq` | 这条任务的状态版本号，从 1 起，每变一次加 1，只增不减 |
| `data.job` | **和 `POST /api/publish/sync-status` 返回的 `jobs[]` 里的一项完全一样**（同一个结构 `publishSyncJobSummary`），你现有的解析代码直接用 |

请求体不超过几 KB，不含正文、blocks、图片映射、风险模型原文。

### 什么算「变了」

拿 `data.job` 这份摘要比，除了 `updated_at` 以外任何一个字段变了就算变了：阶段、失败阶段、错误、标题、FoodTalks 资讯 id、文章链接、发布/下线时间、审核人与动作、可做的动作等。只改了摘要里没有的东西（比如飞书卡片的封面、外发记录）不推。

**还没推出去又变了，只推最新的一版**：几步连着走得很快时，中间状态可能合成一次。所以 `seq` 可能跳号（比如收到 3 接着收到 5），这是正常的。终态一定会推到。

### `stage` 的取值

和 `sync-status` 一样，常见的依次是：

| stage | 意思 |
| --- | --- |
| `created` → `fetching` → `rendering` → `mirroring` → `enriching` → `assessing` → `staging` → `staging_creating` | 排版、转存图、AI 预填、风险检查、写 FoodTalks 隐藏草稿，都是排版服务自己在跑 |
| `awaiting_review` | 草稿已在 FoodTalks，等飞书审核 |
| `publicizing` → `publicizing_updating` → `published` | 审核通过（人点或自动发布），正在公开 → 已公开 |
| `rejected` | 审核驳回（终态） |
| `superseded_duplicate` | 同一篇已有别的任务在发（终态），`superseded_by_job_id` 是赢的那条 |
| `failed` | 某一步失败，`failed_stage` 是从哪步续跑，`retryable` 表示能不能重试；**不是终态**，人重试后会继续变 |
| `abandoned` | 失败单结案（终态） |
| `unpublishing` → `unpublishing_updating` → `offline` | 已发布后被下线 |
| `republishing` → `republishing_updating` → `published` | 下线后重新发布 |
| `reconciliation_required`、`publicize_reconciliation_required`、`unpublish_reconciliation_required`、`republish_reconciliation_required` | 外部结果说不准，等人核对（`reconcile_required: true`） |
| `assessing_review` | 老稿补跑风险检查，跑完回 `awaiting_review` |

`published`、`offline` 之后也可能再变（下线、重新发布），所以别因为到了终态就把这条任务的回调忘掉。

## 3. 签名：怎么认出是排版服务发的

照业内通用的 [Standard Webhooks](https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md) 规范（Svix、OpenAI 等都用这一套）的对称签名：

```
签名原文 = webhook-id + "." + webhook-timestamp + "." + 请求体原样字节
webhook-signature = "v1," + base64( HMAC-SHA256(密钥, 签名原文) )
```

- **密钥就是两边现在共用的 `PUBLISH_SYNC_SERVICE_TOKEN`**（FBIF 中台登记的那把，资讯订阅调排版服务时放在 `X-Publish-Sync-Token` 头里的同一个值），按 UTF-8 字节直接当 HMAC 密钥，不做 base64 解码。没有新建密钥。
- 推送请求里**不带这把密钥本身**，只带签名；所以就算地址填错，密钥也不会泄露。
- base64 是带 `=` 补位的标准 base64。
- `webhook-timestamp` 是这一次发送的 Unix 秒数（每次重试都会变）；验签时请拒绝与本机时间相差超过 5 分钟的请求，防重放。
- 中台换这把密钥时，两边大约 1–3 分钟内先后换上，这段时间验不过的推送会按 §5 自动重试，不用特别处理。

Go 里可以直接用官方库（[`libraries/go`](https://github.com/standard-webhooks/standard-webhooks/tree/main/libraries/go)）：

```go
wh, _ := standardwebhooks.NewWebhookRaw([]byte(os.Getenv("PUBLISH_SYNC_SERVICE_TOKEN")))
body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
if err := wh.Verify(body, r.Header); err != nil { // 含 5 分钟时间窗
    http.Error(w, "bad signature", http.StatusUnauthorized)
    return
}
```

不想加依赖，自己写也就几行：

```go
id, ts, sig := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), r.Header.Get("webhook-signature")
// 先检查 ts 与当前时间相差不超过 5 分钟
mac := hmac.New(sha256.New, []byte(token))
mac.Write([]byte(id + "." + ts + "."))
mac.Write(body)
want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
ok := hmac.Equal([]byte(sig), []byte(want))
```

排版服务的测试 `TestStatusCallbackSignatureMatchesStandardWebhooks` 用官方库自己的测试数据核过，结果一字不差。

## 4. 你那边要怎么回

| 你回 | 排版服务怎么做 |
| --- | --- |
| `2xx`（200–299） | 算送达，这一版不再推 |
| `410 Gone` | 这条任务以后不再推到这个地址（比如你那边已经不跟这条了）；重新提交并带上地址才恢复 |
| 别的（含 3xx、4xx、5xx、超时 15 秒、连不上） | 算失败，按 §5 重试；3xx 不跟跳转 |

接收方请做到：

1. **先验签，再处理**；验不过回 401。
2. **按 `webhook-id` 去重**：同一版重试时 `webhook-id` 和请求体都不变（只有时间戳和签名变）。
3. **按 `seq` 只认更大的**：同一条任务记下处理过的最大 `seq`，收到不大于它的直接回 2xx 丢掉。重试、并发都可能让旧的一版晚到。
4. **快速回 2xx**：落库后就回，别在请求里做耗时的事；15 秒没回算超时。

## 5. 重试与放弃

- 失败后依次隔 **5 秒、30 秒、2 分钟、10 分钟、30 分钟、1 小时、2 小时** 再试（各加最多 20% 随机抖动），你回了更长的 `Retry-After` 就照它等（最多 2 小时）。
- 同一版**连续 8 次**都不成（约 3 小时 43 分）就放弃这一版。这条任务状态再变时，新的一版重新从头推。
- 放弃会记在任务上，排版服务的 `GET /api/publish/monitoring` 在 7 天内给 warning `status_callback_dead_letters:<条数>`（只是 warning，不影响 `ready`，不是 P0）。
- 排版服务重启（每次部署都会）后，没送达的那一版会补推一次；送达过的不重推。
- 部署候选实例暂停写入时（`DEPLOYMENT_WRITES_PAUSED=1`）不推，正式起来后补推。

所以你那边保留一个很慢的兜底回查（`sync-status`）就够了：推送放弃、或者你自己漏收时，兜底能把状态补齐。

## 6. 负载

每条任务从提交到发布大约推 10–15 次（排版各步骤 + 审核 + 公开；估算），之后只有下线、重新发布、失败重试这类变化才推。生产任务库 2026-10-26～11-08 每天新建 3–4 条任务，所以每天大约 30–60 次请求，每次几 KB。排版服务这边一个 goroutine 串行发，同一时间只有一个请求在路上。

反过来，资讯订阅现在每次回查 `sync-status`，排版服务都要把整个任务库（生产 2026-11-08 是 23 MB）读一遍；改成推送后回查可以放得很慢，两边都省。

## 7. 安全边界

- 回调地址只能是资讯订阅自己的域名（§1），排版服务不会按调用方给的任意地址往外发请求。
- 推送请求不带共享密钥本身（§3）。
- 不跟跳转（§4）。

## 8. 快讯任务的推送（2026-11-29 起）

快讯从 `POST /api/publish/flash-sync` 交进来（[发布接入契约](publish-intake-contract.md) §10），带上 `status_callback_url` 就推。代码在 `api/flash_jobs.go`（摘要和事件类型）、`api/publish_status_callback.go`（发送器，和文章任务共用一个实现），测试 `api/flash_test.go`。

### 8.1 推到哪

**`https://feed.foodtalks.cn/api/internal/flash-status`**。资讯订阅的 `/api/internal/layout-status` 只认 `publish.job.status`，别的事件当场回 400，所以快讯不往那里推。地址规则和 §1 一样（只许资讯订阅的域名，最长 2048 字节，后登记的替换先登记的）。

### 8.2 和文章任务一样的地方

**签名完全一样**：同一把密钥 `PUBLISH_SYNC_SERVICE_TOKEN`，同一种 Standard Webhooks 签名（§3），同样的请求头、`User-Agent`、15 秒超时、不跟跳转；你回什么怎么算（§4）、重试和放弃（§5）、重启后补推、暂停写入时不推，也都一样。你现在验 `layout-status` 的那段验签代码可以原样用。

### 8.3 不一样的地方

| | 文章任务 | 快讯任务 |
| --- | --- | --- |
| `type` | `publish.job.status` | **`publish.flash.status`** |
| `webhook-id` | `layout-job-<编号>-<版>` | **`layout-flash-<编号>-<版>`** |
| `data.job_id` | 文章任务编号 | 快讯任务编号。**两边各编各的号，会重号**，按 `type` 分开存 |
| `data.job` | `publishSyncJobSummary` | 下面这个结构 `flashJobSummary` |

```json
{
  "type": "publish.flash.status",
  "timestamp": "2026-11-29T02:10:00Z",
  "data": {
    "job_id": 12,
    "seq": 6,
    "job": {
      "id": 12,
      "article_id": "0e6a6bf3-…",
      "source_url": "https://www.example.com/news/123",
      "stage": "published",
      "title": "乐乐茶全线降价超30%",
      "foodtalks_flash_id": 1541,
      "flash_url": "https://www.foodtalks.cn/flash/1541",
      "published_at": "2026-11-29T02:10:00Z",
      "reviewed_by": "ou_xxx",
      "created_at": "2026-11-29T01:58:00Z",
      "updated_at": "2026-11-29T02:10:00Z"
    }
  }
}
```

| 字段 | 什么时候有 | 说明 |
| --- | --- | --- |
| `id` | 一直 | 快讯任务编号 |
| `article_id` | 一直 | 你交进来的资讯编号，原样 |
| `source_url` | 交的时候带了才有 | 你交进来的 `url` |
| `stage` | 一直 | 见 §8.4 |
| `title` | 稿子写出来以后 | 快讯标题；编辑在卡上改了字，这里跟着变（也会推一版） |
| `foodtalks_flash_id` | **`published` 时一定有** | FoodTalks 快讯编号 |
| `flash_url` | **`published` 时一定有** | FoodTalks 前台网址 `https://www.foodtalks.cn/flash/<编号>` |
| `published_at` | `published` 时有 | 发上去的时刻（UTC） |
| `reviewed_by` | `published`、`rejected` 时有 | 点按钮的人的飞书 open_id |
| `skip_reason` | `skipped` 时有 | 为什么不写 |
| `duplicate_of` | 认出网站已有同一件事时有 | 网站上已有的那条（快讯网址，或模型认出的资讯标题） |
| `failed_stage`、`error` | `failed` 时有；重试中途也可能有 | 哪一步、什么原因 |
| `created_at`、`updated_at` | 一直 | UTC，RFC3339 |

请求体只有几百字节，不带稿子正文、检查结果、原文。要全文用 `GET /api/publish/flash-jobs/{id}`。

### 8.4 `stage` 的取值

| 取值 | 意思 | 之后 |
| --- | --- | --- |
| `queued` | 收下了 | → `writing` |
| `writing` | 正在取原文、读规范、写稿 | → `checking`，或 `skipped` |
| `checking` | 正在对原文、查风险（编辑在卡上改了字也回到这里重查） | → `awaiting_review` |
| `awaiting_review` | 审核卡已发，等人点 | → `publishing`、`rejected`，或改字后回 `checking` |
| `publishing` | 人点了通过，正在发到 FoodTalks | → `published`，或发布前查到同题的一条 → `skipped` |
| `published` | **已发到 FoodTalks 快讯，前台看得到**（发完读回核对过才算） | 终态 |
| `rejected` | 人在卡上点了驳回 | 终态。**不带理由**：卡上现在没有填理由的地方，只给 `reviewed_by`；要理由再加 |
| `skipped` | 不写或不发，原因在 `skip_reason`（§8.3、接入契约 §10.4） | 终态 |
| `failed` | 某一步出错放弃了，`failed_stage` 是哪一步 | 管理员重试后回到那一步 |

出错重试的中途 `stage` 不变，`error` 会出现、消失，各推一版；你只需要看 `stage`。

### 8.5 负载

一条快讯从交进来到发布大约推 6–8 次。按每天交 10–20 篇算，每天 100–200 次请求，每次几百字节。

