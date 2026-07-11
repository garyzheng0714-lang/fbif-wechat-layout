# FBIF 公众号历史文章库抓取器

用微信**官方**接口 `freepublish/batchget`（获取已发布文章列表）把一个公众号的全量历史
文章落成一个可查询、可去重、可断点续抓的 SQLite 库。凭据只走环境变量，绝不写死。

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

# 1) 先看接口报的总数（不写库）
python3 mp_archive.py --status         # 空库时游标为 0

# 2) 小批验证一页（确认字段完整）
python3 mp_archive.py --max-articles 20

# 3) 没问题就放开补全量（从新到旧，翻到完为止）
python3 mp_archive.py --sleep 1

# 中断了随时再跑同一条命令，会从断点(next_offset)接着抓，已抓的自动去重。
```

「慢慢补」：用 `--max-articles N` 每天只抓 N 篇，明天再跑接着抓，尊重每日配额。

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
