# FBIF 公众号官方接口响应归档辅助工具

用微信**官方**接口把指定端点的完整解码响应落成可查询、可去重、可断点续抓的 SQLite 快照库。
凭据只走环境变量，绝不写死。这个工具不是“公众号历史全量抓取器”。

## 数据边界（先读）

两个源可以分别归档，但不能相加后当成历史已发布真相：

| 源(`--source`) | 接口 | 覆盖 | 说明 |
|---|---|---|---|
| `material`（默认） | `material/batchget_material` | 素材库快照 | 是资产来源，不是已发布文章真相源 |
| `freepublish` | `freepublish/batchget` | “发布”对象快照 | 不覆盖公众号后台“群发”，`total_count` 不是历史文章总量 |

- 历史已发布总量当前是**未完成 / 未核验**状态。
- 当前单表主键 `(article_id, idx)` 只能做本工具内去重，不能替代按 `msgid`、`msg_data_id`、文章序号、
  `article_id` / URL、发布日期等稳定标识进行的多源关联与覆盖审计。
- 每次请求的顶层响应、父 `item` 和子 `news_item` 字段都会逐页完整留档；端点内分页完成、
  数据库行数或年份密度都不能证明历史全量。
- 飞书多维表格自动同步继续关闭；素材库记录不进入已发布文章自动同步。

## 前提：调用主机已在 IP 白名单

微信要求调用方服务器的**出口 IP 在公众号后台白名单里**。运行前先核对实际主机、当前出口 IP
和现有公众号回调 / token 使用方；不要从这份历史说明推断哪台机器仍在白名单。

> 公众号后台 → 设置与开发 → 基本配置 → **IP白名单**

没加之前跑脚本会明确报「IP 未加入白名单」并非零退出，不会静默。

## 采集端点快照

```bash
cd tools/mp-archive
export WECHAT_MP_APPID=<公众号 AppID，见密钥文档>
export WECHAT_MP_SECRET=<公众号 AppSecret，见密钥文档，别写进任何文件>

# 1) 归档素材库端点（默认 --source material）
python3 mp_archive.py --sleep 0.5
python3 mp_archive.py --reset --sleep 0.5      # 只重置 material，从端点头部补扫

# 2) 归档 freepublish 端点到同一个物理库
python3 mp_archive.py --source freepublish --reset --sleep 0.5

# 3) 随时看两个端点各自的状态
python3 mp_archive.py --status

# 中断了随时再跑同一条命令，会从断点(next_offset)接着抓，已抓的自动去重。
```

两个源共用一个物理库、用 `source` 列区分，但 checkpoint 完全独立；切换端点不会串用
`next_offset`、`done` 或 `total_count`。这只是存储安排，不表示两源已完成稳定身份关联。
`--reset` 只重置当前 `--source` 的游标，不清库，也不影响另一端点。
「慢慢补」：`--max-articles N` 每天只抓 N 篇，明天接着抓，尊重每日配额。

### 从旧版数据库升级

旧版把两个端点共用的游标写在 `meta.next_offset` 等无来源键里，无法可靠判断它属于哪个端点。
新版会保留这些旧键供审计，但**不会继承**；每个端点第一次运行会从 offset 0 安全重扫，
已有文章索引会去重。可用 `python3 mp_archive.py --status` 查看两个端点的新 checkpoint
和是否检测到旧 checkpoint。

### ⚠️ 增量/补漏要从头扫（offset 竞态）

接口按 offset 分页，但**列表在实时变**：抓取期间新发一篇，它会插到 offset 0，把后面
整体挤下一位。若用 `next_offset` 断点续抓，最新那篇可能被跳过。所以：

- **单端点首次归档**：断点续抓后再跑一次 `--reset`，用于补扫该端点头部；不能称为“历史全量”。
- **`freepublish` 日常增量**：从 offset 0 开始扫，靠去重跳过旧记录；该轮询必须独立于 draft /
  material 接口执行，但仍只覆盖“发布”，不能代表后台“群发”。

## 库长什么样

SQLite 文件默认 `tools/mp-archive/mp-archive.db`（可用 `--db` 或 `MP_ARCHIVE_DB` 改）。

### `endpoint_snapshots`：审计真值

每次官方端点请求写一行，不只保存拆出的文章：

| 列 | 含义 |
|---|---|
| source / offset / requested_count | 请求的端点、offset 和 count |
| total_count / item_count | 响应声明的端点对象数 / 本页实际对象数 |
| response_json | 该页完整解码响应，包含顶层、父 `item`、全部子字段和微信业务错误响应 |
| fetched_at | 实际抓取时间 |

原始页先独立提交，再派生 `articles` 索引；即使索引解析失败，已收到的官方响应也不会被回滚。

### `articles`：便于查询的派生索引

主键 `(article_id, idx)`（一个接口对象中的多图文拆成多行）：

| 列 | 含义 |
|---|---|
| article_id / idx | 本工具采用的接口对象 id / 对象里第几篇；不是跨来源统一身份 |
| title / author / digest | 标题 / 作者 / 摘要（微信原生） |
| content_html | 正文 HTML 全文 |
| thumb_url / url | 封面 / 该文永久链接 |
| content_source_url | 「阅读原文」指向（转载常是原文） |
| publish_time | **历史列名**；实际保存父对象的 `update_time`（秒），不是已核验发布时间 |
| is_deleted | 是否已被删除 |
| raw_json | 该篇 `news_item` JSON；完整端点响应以 `endpoint_snapshots` 为准 |

查询示例：
```bash
sqlite3 mp-archive.db "SELECT datetime(publish_time,'unixepoch','+8 hours') t, title FROM articles ORDER BY publish_time DESC LIMIT 20;"
```

导进你们 MySQL 也容易（每行就是结构化字段 + 原始 JSON）。

## 注意事项

- **token 共存**：脚本用 `stable_token`（`force_refresh=false`），不会顶掉同一个 appid 下
  其它系统（比如 FoodTalks 后端）手里的 token。仍建议向后端确认该 appid 有没有别的占用。
- **端点**：`freepublish/batchget` 是“发布”对象列表（update_time 新→旧），不覆盖后台“群发”；
  `material` 是素材库，不能作为已发布真相源。
- **计数校验**：每个端点独立核对 `next_offset` 与该端点 `total_count`；中途空页会 fail closed，
  保存异常响应后非零退出。端点对象数、拆出的文章行数和跨来源库总行数口径不同，
  程序不会再拿文章行数证明分页完整，更不能把它们当作公众号历史总量。
- **只抓这一个号**：其它 FBIF 子号（论坛/食品创新展/短视频…）各是独立公众号，
  给出各自 AppID/密钥后，同一套脚本可复用，但每个 AppID 必须指定独立 `--db`，
  不能只换环境变量后写进同一库；白名单以实际调用主机出口 IP 为准。

## 测试

```bash
python3 test_mp_archive.py     # 17 个离线单测，不碰网络，白名单没加也能全绿
```
