# FBIF 公众号历史文章库抓取器

用微信**官方**接口把一个公众号的历史文章落成可查询、可去重、可断点续抓的 SQLite 库。
凭据只走环境变量，绝不写死。

## 两个源要一起抓（关键）

微信没有单一"全量历史"接口，两个接口**互补、几乎不重叠**，必须都抓才最全：

| 源(`--source`) | 接口 | 覆盖 | 说明 |
|---|---|---|---|
| `material`（默认） | 图文素材库 | **2015 → 2023-07，约 1.3 万篇** | 老档案主体，含完整正文 |
| `freepublish` | 已发表记录 | 2023-09 → 至今，约 300 篇 | 近年新文章走这条路 |

> ⚠️ **覆盖真相**：本库对 **2015-2021 接近全量**（1.3 万篇），对 **2022-2026 稀疏**（约 330 篇）——
> FBIF 约 2022 起改了发布方式，那之后的文章**两个官方接口都收不全**（是微信侧盲区，非脚本问题）。
> 要补 2022+ 需另想办法（mc-fb 已收录版 / 后台内容管理导出 / __biz 枚举）。

## 一次性前提：加 IP 白名单（只有你能做）

微信要求调用方服务器的**出口 IP 在公众号后台白名单里**。抓取跑在正式服务器
**121（`121.40.214.5`，即 `fbifmp-layout.foodtalks.cn`）**，所以：

> 公众号后台 → 设置与开发 → 基本配置 → **IP白名单** → 添加 `121.40.214.5` → 保存

没加之前跑脚本会明确报「IP 未加入白名单」并非零退出，不会静默。

## 开抓（在服务器 121 上）

```bash
cd /root/mp-archive     # 脚本已放这里
export WECHAT_MP_APPID=<公众号 AppID，见密钥文档>
export WECHAT_MP_SECRET=<公众号 AppSecret，见密钥文档，别写进任何文件>

# 1) 抓老档案主体（素材库，2015→2023，约 1.3 万篇；默认 --source material）
python3 mp_archive.py --sleep 0.5
python3 mp_archive.py --reset --sleep 0.5      # 抓完补一次 reset 兜底补漏

# 2) 并入近年新文章（freepublish，2023-09→今）到同一个库，自动去重不冲突
python3 mp_archive.py --source freepublish --reset --sleep 0.5

# 3) 随时看状态
python3 mp_archive.py --status

# 中断了随时再跑同一条命令，会从断点(next_offset)接着抓，已抓的自动去重。
```

两个源共用一个库、用 `source` 列区分；`--reset` 只重置游标不清库。
「慢慢补」：`--max-articles N` 每天只抓 N 篇，明天接着抓，尊重每日配额。

### ⚠️ 增量/补漏要从头扫（offset 竞态）

接口按 offset 分页，但**列表在实时变**：抓取期间新发一篇，它会插到 offset 0，把后面
整体挤下一位。若用 `next_offset` 断点续抓，最新那篇可能被跳过。所以：

- **冷启动全量**：断点续抓没问题，抓完**再跑一次 `--reset`** 兜底补漏（去重会跳过已有）。
- **日常增量/监控**：永远 `--reset` 从 offset 0 开始扫，靠去重跳过旧的、只捞新的——
  这也正是未来「公众号发一篇 → 自动同步」监控器该有的姿势（轮询 offset 0 + article_id 差集）。

## 库长什么样

SQLite 文件默认 `tools/mp-archive/mp-archive.db`（可用 `--db` 或 `MP_ARCHIVE_DB` 改）。
表 `articles`，主键 `(article_id, idx)`（多图文一条发布拆多篇）：

| 列 | 含义 |
|---|---|
| article_id / idx | 发布 id / 多图文里第几篇 |
| title / author / digest | 标题 / 作者 / 摘要（微信原生） |
| content_html | 正文 HTML 全文 |
| thumb_url / url | 封面 / 该文永久链接 |
| content_source_url | 「阅读原文」指向（转载常是原文） |
| publish_time | 发布时间戳（秒） |
| is_deleted | 是否已被删除 |
| raw_json | 该篇原始 JSON，兜底留档 |

查询示例：
```bash
sqlite3 mp-archive.db "SELECT datetime(publish_time,'unixepoch','+8 hours') t, title FROM articles ORDER BY publish_time DESC LIMIT 20;"
```

导进你们 MySQL 也容易（每行就是结构化字段 + 原始 JSON）。

## 注意事项

- **token 共存**：脚本用 `stable_token`（`force_refresh=false`），不会顶掉同一个 appid 下
  其它系统（比如 FoodTalks 后端）手里的 token。仍建议向后端确认该 appid 有没有别的占用。
- **端点**：`freepublish/batchget` 是「已发布」列表（update_time 新→旧），不是素材/草稿。
- **计数校验**：每次结束会打印 库内篇数 vs 接口 total_count，抓完若篇数偏少会告警。
- **只抓这一个号**：其它 FBIF 子号（论坛/食品创新展/短视频…）各是独立公众号，
  给出各自 AppID/密钥后，同一套脚本换环境变量即可复用，白名单同样加 121.40.214.5。

## 测试

```bash
python3 test_mp_archive.py     # 离线单测，不碰网络，白名单没加也能全绿
```
