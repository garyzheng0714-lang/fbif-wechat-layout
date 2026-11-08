# 发布接入契约（上游监控端 → 排版服务）

> 读者：**本仓库之外的调用方**——`FoodTalks_Feed`（实时线）与 `fbif-wechat-article`（T+1 归档线）的开发者或维护它们的 agent。
> 本仓库自己的端点全量清单在 [README 的 API 段](../README.md#api)；这一份只讲「别人怎么把文章交给排版服务」。
> 契约以当前 `api/publish_http.go`、`api/publish_service_auth.go` 与 `api/publish_pipeline.go` 为准；本页字段与状态码逐条核对至 2026-08-06。

## 0. 先理解边界：你交链接，我抓正文

FBIF 内容链路是**三个互相独立的系统**，只通过 HTTP 通信，不共享任何存储。排版服务（本仓库）**只做加工**：抓原文 → 解析 → 排版 → 转存图 → 风险检查 → 写 FoodTalks 隐藏草稿 → 停在 `awaiting_review` 等飞书人审。

**发现文章是你的职责，获取和排版是我的职责。** 你告诉我"哪篇文章值得发"，我自己去把它读回来。

## 1. 红线：只接收链接

`POST /api/publish/site-sync` 与 `POST /api/publish/official-sync` **只收 `url`**。以下五个字段一旦非空，整个请求 **400 拒绝**，任务不会创建：

`content_html` · `title` · `source_name` · `author` · `cover_url`

**为什么是显式 400 而不是静默忽略**：静默忽略更危险——你会以为自己传的正文生效了，而实际排版用的是另一份内容，两边对不上账却没有任何信号。

**为什么不让你传正文**：`publishContentRank` 里外部提交的内容优先级高于自抓（`supplier`/`official_api` > `web_fetch`）。一旦带正文，流水线就不再回源，抓取路径上的规则包解析选项（`articleBlockOptionsForAccount`）不参与，排版质量无法保证。同一篇原文的对照实测：**传正文 0 个小标题，只传链接 7 个**。

> 生产任务 #72（4.7 万字长文零小标题）来自这条通道，但它具体崩在哪一步没有复现、没有留存样本——不要在任何文档或注释里把成因写死到某一行代码。

请求体上限 **64KB**。超限同样 400。

## 2. 鉴权

```
X-Publish-Sync-Token: <本服务的 PUBLISH_SYNC_SERVICE_TOKEN>
```

- 无 token 或 token 错 → **401**。
- **`X-Admin-Password` 自 `f591217`（2026-08-04 会话角色改造）起已不被接受。** 如果你的服务还在发这个头，投递会静默停摆——表现是"我这边一直在发，那边一条任务都没有"。
- 这个令牌是窄权限的：只能提交、读紧凑状态、读监控摘要。**读不到**完整任务详情、审核、重试、对账和规则管理接口。

## 3. 请求字段

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `url` | ✅ | 原文链接。排版服务从这里现抓。**只接受 `mp.weixin.qq.com`**，见下方 §3.1。 |
| `content_source_url` / `original_source_url` | 可选 | 公众号「阅读原文」（官方字段名 `content_source_url`）。**不是正文**，只补转载声明出处。须是 http(s)。网页抓到的阅读原文优先；网页为空时用这个。自家号转发且两边都空时，服务端先问 Feed `/api/internal/original-article`，没命中再 TikHub 广域搜（改过标题的按正文核验，并用原文真标题写转载声明）；都失败则转载声明不链回自家页。 |
| `content_kind` | 机器提交必填 | 必须是 `"article"`。 |
| `classification` | 机器提交必填 | 必须是 `"ordinary_confirmed"`。 |
| `classifier_version` | 机器提交必填 | 你的分类器版本号，用于事后追溯是哪一版判的。格式 `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`。 |
| `rule_pack_id` | 可选 | 网站规则包 id，默认 `ft-default`。必须是 `api/account_catalog.go` 里登记的 `ft` 渠道 + `foodtalks-site` 模板的包，否则 400。兼容更短的字段名 `rule_id`。 |
| `rerender_latest` | 可选 | 对已存在的任务用新规则包重排。 |
| `refetch_latest` | 可选 | 对已存在的任务重新回源抓取再重排。 |
| `status_callback_url` | 可选 | 任务状态每变一次就带签名 POST 到这里，只许指向资讯订阅自己的域名；不带就和以前一样，靠 `sync-status` 回查。字段、签名、重试见 [任务状态推送约定](publish-status-callback.md)。 |

**"机器提交"的判定**：请求带了 `X-Publish-Sync-Token` 头即为机器提交，三项类型证据一个都不能少。

### 3.1 `url` 只接受微信公众号文章链接

`normalizeSiteSourceKey` 强制校验，**不满足直接 400，不建任务**：

- host 必须是 `mp.weixin.qq.com`（其他域名 → `仅支持微信公众号文章链接（mp.weixin.qq.com）`）
- scheme 必须 `http` / `https`，不得带用户信息或自定义端口
- path 必须是 `/s`（长链）或 `/s/<slug>`（短链），其余 → `仅支持微信公众号文章路径 /s 或 /s/<slug>`
- 长链必须带齐 `__biz`、`mid`、`idx`，缺任一 → `长链缺少文章身份参数 __biz/mid/idx`

> **这条对实时线尤其重要**：`FoodTalks_Feed` 采集面比这宽（RSS、供应商资讯、非微信站点）。**非微信来源投不进这个入口**——不是 bug，是当前实现边界。这类内容走 `/api/html-to-blocks`（§7），它不创建发布任务。

### 类型证据为什么 fail closed

公众号的"小绿书"和图集（`newspic`）不是普通图文，排出来是废稿。排版服务**不猜**——你必须正向声明"我确认这是普通图文"，声明不了就投不进来。判不准时宁可不投。

## 4. 两个入口的区别

两者的红线、字段、鉴权**完全相同**，唯一差别是类型证据的来源：

| 入口 | 用途 | classification |
| --- | --- | --- |
| `POST /api/publish/site-sync` | 实时线（`FoodTalks_Feed`）。RSS / 「今天看啥」聚合源（`JINTIANKANSHA_*`）/ 供应商资讯发现新文章即投。 | 按你传的值 |
| `POST /api/publish/official-sync` | T+1 归档线（`fbif-wechat-article`）。 | **服务端钉死为 `ordinary_confirmed`**，因为调用方已用官方 `article_type=news` 确认过；`classifier_version` 缺省为 `wechat-official-api-v1` |

## 5. 响应与状态码

成功：

```json
{ "job": { "id": 72, "source_key": "...", "source_url": "...", "rule_pack_id": "ft-default", ... },
  "existing": false, "rerendered": false }
```

| 码 | 含义 | 你该怎么办 |
| --- | --- | --- |
| `201` | 新任务已创建 | 记下 `job.id`。带了 `status_callback_url` 的，状态变了会推给你（响应里回显这个字段即已登记，见 [任务状态推送约定](publish-status-callback.md)）；`/api/publish/sync-status` 照旧可批量查 |
| `200` | **同一原文已存在任务**（`existing: true`） | 正常幂等命中，不是错误，不要重投 |
| `400` | 传了被拒字段 / 缺 `url` / body 超 64KB / 规则包不存在 | 读响应里的中文说明，它会点名是哪几个字段 |
| `401` | token 缺失或错误 | 检查是不是还在发 `X-Admin-Password` |
| `409` | 同一原文已绑定别的规则包 | 要换规则包必须同时带 `rerender_latest=true` |
| `422` | 类型证据不合格 | `content_kind`/`classification` 不对，或 `classifier_version` 不合格式 |
| `503` | 部署候选实例写入暂停中（`DEPLOYMENT_WRITES_PAUSED=1`） | 稍后重试；这是部署事务的正常中间态 |

## 6. 幂等

`source_key` 由链接归一得到并**永久去重**。你的重试逻辑可以放心"宁可多投一次"，不需要自己维护去重表；**两条线先后投同一篇也安全**——同一篇公众号文章走 feed 的 RSS 当天就到，走官方 API 要等第二天，两次会命中同一个 key。

但**长链和短链的去重时机不同**，这会影响你看到的响应：

| 形态 | 归一方式 | 行为 |
| --- | --- | --- |
| **长链** `/s?__biz=&mid=&idx=` | 只保留 `__biz`/`mid`/`idx` 三个身份参数并按 key 排序；`sn` 等分享上下文全丢 | 提交即幂等。同文不同参数顺序、带不带 `sn`，都命中同一 key |
| **短链** `/s/<slug>` | slug 只在单条分享链接内稳定，**两个不同 slug 可能指向同一篇** | 提交时**同步抓取页面**解出 canonical `__biz/mid/idx`，再原子建单 |

短链的两个后果，调用方要预期：

1. **响应更慢。** 抓取发生在建单之前，不是建单之后异步跑。
2. **抓不动就直接报错，不建任务。** 解不出规范身份 → `短链未解析出 __biz/mid/idx 文章规范身份`；抓到了但没标题/正文块 → `短链解析结果缺少标题或正文内容块，未创建发布任务`。

这是刻意设计：**绝不先创建临时 job、事后把冲突输家打成 failed**——那会在 FoodTalks 上留下两篇文章。能给长链就给长链。

## 7. 唯一的例外：`/api/html-to-blocks`

`POST /api/html-to-blocks`（`Authorization: Bearer $RENDER_TOKEN`）**不在只接收链接的红线内**——它是 HTML → 中立 Block[] 的纯转换工具，调用方 `FoodTalks_Feed` 排版的是自有资讯，根本不存在可抓的微信原文链接。

关键区别：**它不创建发布任务**，不进审核流水线，不会发到任何渠道。它只是把 HTML 转成结构化数据还给你。

## 8. 排查

| 现象 | 先查 |
| --- | --- |
| 一直在发，那边一条任务都没有 | 401——是不是还在发 `X-Admin-Password`（见 §2） |
| 400 且提示点名了字段 | 你的 payload 里还留着正文/标题/封面字段（见 §1） |
| 422 | 三项类型证据（见 §3） |
| 任务建了但停在 `fetching` 失败 | 原文抓不到（付费墙、删文、反爬）。这是排版服务这边的事，把 job id 给过来 |
| 卡片发出去了但样式是旧的 | 用 `POST /api/publish/jobs/{id}/card-refresh` 刷新，需管理员或 `PUBLISH_ADMIN_SERVICE_TOKEN`（同步令牌不可用） |

## 9. 不属于你的事

- **不要改本仓库的代码。** 看出问题就说出来，由本仓库改；反过来本仓库也不改你的。
- **健康探测归监控端。** 本仓库只提供被探端点 `GET /api/publish/monitoring`，不主动探别人的死活。外检看门狗 `fbif-link-watchdog` 住在 `FoodTalks_Feed`。
