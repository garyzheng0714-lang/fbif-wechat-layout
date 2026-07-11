# WORKLOG — FBIF 公众号历史文章库（微信官方接口抓取）

- 开始：2026-07-12
- 触发需求：用户「我需要有一个 FBIF 公众号历史文章的库，从新到旧慢慢补」
- 负责人：Claude（用户睡觉中，自主执行；不再打断审批）

## 目标（验收条件）

1. 用微信公众号**官方**接口 `freepublish/batchget`（合法正门，非爬 HTML）把
   `FBIF食品饮料创新` 订阅号（AppID/密钥见密钥文档，环境变量注入）的**全量已发布历史文章**
   抓下来，落成一个**可查询、可去重、可断点续抓**的库。
2. 每篇存全量：`article_id / 标题 / 作者 / 摘要 / 正文HTML / 封面 / 原文链接 / 发布时间 / 原始JSON`。
3. 从新到旧分页，**可限速、可断点续传**（今天抓一批、明天接着抓），尊重微信每日配额。
4. 批量操作带**计数校验**（开始记 total_count、结束核对入库数）。

## 关键事实 / 决策

- **能抓**：2026-07-12 在正式服务器 121（121.40.214.5，即 `fbifmp-layout.foodtalks.cn`）
  实测 `cgi-bin/token` 返回 `40164 invalid ip 121.40.214.5, not in whitelist`。
  这个错误码只在 **AppID/AppSecret 校验通过之后**才出现 → **凭据有效、接口可用**，
  唯一门槛是 IP 白名单。
- **唯一阻塞（用户动作）**：需在公众号后台把 `121.40.214.5` 加进「IP 白名单」。
  只有用户能操作，Claude 不代做。加完即可跑通。
- **服务器归属**：121 就是发布流水线（`fbif-api :9001`）所在的正式机，白名单加一个
  IP 同时解锁「历史补抓」和「未来新文章自动监控」（后者本质就是定时轮询同一个接口）。
- **token 共存风险**：微信 access_token 按 appid 全局唯一。若 FoodTalks 后端已在用同一
  appid，独立拉 token 会互相顶掉。**对策：用 `cgi-bin/stable_token`（force_refresh=false，
  可共存版）**，不清掉别人的 token。仍建议向后端确认该 appid 是否有其它系统在用。
- **库反查不出微信链接**：mc-fb 的 `news_news` 无任何 url 字段，`content` 是 FoodTalks
  加工版；所以必须走官方接口重抓，才能建原始文章库。
- **抓取范围**：用户裁定「先只抓这一个号」。其它 FBIF 子号（论坛/食品创新展/短视频…）
  各是独立公众号、各需自己的 AppID/密钥，以后逐个给、同一套代码复用。
- **存储选型**：Python3 + 内置 sqlite3，零第三方依赖（api 服务本身不用 sqlite，走 JSON
  文件存储，故不污染其构建）。库文件 `.db` 可查询、可导入 MySQL。
- **端点选型**：`freepublish/batchget`（已发布列表，update_time 新→旧）而非
  `material/batchget_material`（素材/草稿，不等于已发布）。
  注意：一个 `item`（article_id）可能是多图文，含多条 `news_item`，逐条入库，主键 (article_id, idx)。

## 交付物

- `tools/mp-archive/mp_archive.py`：抓取器（stable_token + 分页 + 断点续传 + 去重 + 计数校验）。
- `tools/mp-archive/test_mp_archive.py`：离线单测（mock 接口响应，验证解析/去重/游标/多图文/计数）。
- `tools/mp-archive/README.md`：用法 + 白名单说明 + 每日配额建议。

## 状态

- [x] 可行性实测（凭据有效、就差白名单）— 已验证
- [x] 抓取器代码 — 写完
- [x] 离线单测 — 7 项全绿（解析/去重/游标/多图文/`--reset`/40164 上抛）
- [x] **生产链路验证** — 脚本已放 121:/root/mp-archive，用真凭据跑通到微信真实接口，
      命中 40164 → 打印白名单指引 → 非零退出（码 3）。除"真正拉数据"外整条链路已验证。
- [ ] **真正拉数据 — 阻塞于用户加 IP 白名单 `121.40.214.5`**（诚实标注：未验证）
- [ ] 全量补抓 + 入库计数核对 — 待白名单后执行（命令见 README「开抓」）

## 待办 / 风险

- 白名单加好后：先 `--status` 看 total_count，再 `--max-articles` 小批验证一页，
  确认字段完整后放开补全量；每批结束核对 `COUNT(*)` vs `total_count`。
- 向 FoodTalks 后端确认 appid `wx72d6...` 是否已被其它系统占用 token。
- 已删除文章（`is_deleted`）：入库但打标，不当有效内容。
