#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
FBIF 公众号官方端点响应归档工具

把微信官方 `material/batchget_material` 或 `freepublish/batchget` 的响应归档到
可查询、可去重、可断点续抓的 SQLite 库。两个端点都不是公众号历史已发布全量真相源。
凭据只从环境变量读，绝不写死。

设计要点：
- token 用 `stable_token`（force_refresh=false，可共存），不顶掉别人手里的 token。
- 每个端点独立保存 offset/count 游标，切换端点不会互相覆盖。
- 每次请求的完整解码响应逐页留档，拆出的 news_item 另存为查询索引。
- 去重靠主键 (article_id, idx)；多图文一条 item 拆成多条 news_item 入库。
- 批量操作按端点核对对象游标与 total_count，不拿跨端点文章行数冒充对象数。
- 出错大声失败：IP 没进白名单时打印明确指引并非零退出，绝不静默。

用法见同目录 README.md。
"""

import argparse
import json
import os
import sqlite3
import sys
import time
import urllib.request
import urllib.error

API_BASE = "https://api.weixin.qq.com/cgi-bin"
DEFAULT_DB = os.path.join(os.path.dirname(os.path.abspath(__file__)), "mp-archive.db")
PAGE_SIZE = 20          # freepublish/batchget 单页上限就是 20
HTTP_TIMEOUT = 30
SOURCES = ("material", "freepublish")


# ----------------------------------------------------------------------------
# HTTP 底座（单测里会被替换掉，业务逻辑与网络解耦）
# ----------------------------------------------------------------------------
def _post_json(url, payload):
    """POST 一个 JSON，返回解析后的 dict。网络/解析错误抛异常。"""
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url, data=data, headers={"Content-Type": "application/json"}, method="POST"
    )
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT) as resp:
        return json.loads(resp.read().decode("utf-8"))


def fetch_token(appid, secret):
    """
    取 stable_token（可共存，不清掉同 appid 其它系统的 token）。
    返回 (access_token, expires_in)；失败抛 WxError。
    """
    url = f"{API_BASE}/stable_token"
    body = {
        "grant_type": "client_credential",
        "appid": appid,
        "secret": secret,
        "force_refresh": False,
    }
    d = _post_json(url, body)
    if d.get("errcode"):
        raise WxError(d.get("errcode"), d.get("errmsg", ""))
    tok = d.get("access_token")
    if not tok:
        raise WxError(-1, f"响应无 access_token: {d}")
    return tok, int(d.get("expires_in", 7200))


def fetch_page(token, offset, count=PAGE_SIZE):
    """
    freepublish/batchget：只含该接口覆盖的“发布”对象，不覆盖公众号后台“群发”；
    返回数量不能解释为历史已发布文章总量。
    """
    url = f"{API_BASE}/freepublish/batchget?access_token={token}"
    body = {"offset": offset, "count": count, "no_content": 0}
    # 业务错误响应也交给 backfill 先归档，再由它抛出 WxError。
    return _post_json(url, body)


def fetch_material(token, offset, count=PAGE_SIZE):
    """
    material/batchget_material（图文素材库快照，不是已发布文章真相源）。
    返回 {total_count, item[]}；每条 item 用 `media_id` 作 id，并含 news_item[]。
    """
    url = f"{API_BASE}/material/batchget_material?access_token={token}"
    body = {"type": "news", "offset": offset, "count": count}
    # 业务错误响应也交给 backfill 先归档，再由它抛出 WxError。
    return _post_json(url, body)


class WxError(Exception):
    def __init__(self, code, msg):
        self.code = code
        self.msg = msg
        super().__init__(f"微信接口错误 errcode={code} errmsg={msg}")


class ArchiveConsistencyError(Exception):
    """端点分页响应与游标不一致，继续推进可能漏数据。"""


# ----------------------------------------------------------------------------
# 存储层（纯逻辑，单测直接用）
# ----------------------------------------------------------------------------
SCHEMA = """
CREATE TABLE IF NOT EXISTS articles (
    article_id          TEXT NOT NULL,   -- id：material 用 media_id / freepublish 用 article_id
    idx                 INTEGER NOT NULL,-- 多图文里的第几篇（0 起）
    source              TEXT,            -- 来源接口：material / freepublish
    title               TEXT,
    author              TEXT,
    digest              TEXT,            -- 摘要（微信原生）
    content_html        TEXT,            -- 正文 HTML 全文
    thumb_url           TEXT,            -- 封面图
    content_source_url  TEXT,            -- 「阅读原文」指向（转载常是原文）
    url                 TEXT,            -- 这篇文章的永久链接
    publish_time        INTEGER,         -- 接口对象 update_time；不是已核验发布时间
    is_deleted          INTEGER DEFAULT 0,
    raw_json            TEXT,            -- 该 news_item 原始 JSON，留档兜底
    fetched_at          INTEGER,
    PRIMARY KEY (article_id, idx)
);
CREATE TABLE IF NOT EXISTS endpoint_snapshots (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    source              TEXT NOT NULL,    -- material / freepublish
    offset              INTEGER NOT NULL, -- 本次请求的 offset
    requested_count     INTEGER NOT NULL, -- 本次请求的 count
    total_count         INTEGER,          -- 响应声明的端点对象数
    item_count          INTEGER NOT NULL, -- 本页实际返回的 item 数
    response_json       TEXT NOT NULL,    -- 该页完整解码响应
    fetched_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_endpoint_snapshots_source_offset
    ON endpoint_snapshots(source, offset);
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT
);
"""


def open_db(path):
    conn = sqlite3.connect(path)
    conn.executescript(SCHEMA)
    conn.commit()
    return conn


def meta_get(conn, key, default=None):
    row = conn.execute("SELECT value FROM meta WHERE key=?", (key,)).fetchone()
    return row[0] if row else default


def meta_set(conn, key, value):
    conn.execute(
        "INSERT INTO meta(key,value) VALUES(?,?) "
        "ON CONFLICT(key) DO UPDATE SET value=excluded.value",
        (key, str(value)),
    )


def _progress_key(source, name):
    if source not in SOURCES:
        raise ValueError(f"不支持的归档端点：{source}")
    return f"progress:{source}:{name}"


def progress_get(conn, source, name, default=None):
    """读取某个端点自己的 checkpoint；不继承旧版无来源 meta。"""
    return meta_get(conn, _progress_key(source, name), default)


def progress_set(conn, source, name, value):
    """写入某个端点自己的 checkpoint。"""
    meta_set(conn, _progress_key(source, name), value)


def reset_progress(conn, source):
    """只重置指定端点的游标，不删除已归档数据，也不影响另一端点。"""
    progress_set(conn, source, "next_offset", 0)
    progress_set(conn, source, "done", 0)
    conn.commit()


def store_endpoint_snapshot(conn, source, offset, requested_count, page, fetched_at):
    """保存一次端点请求的完整解码响应，返回新快照 id。"""
    page_dict = page if isinstance(page, dict) else {}
    raw_total = page_dict.get("total_count")
    try:
        total_count = int(raw_total) if raw_total is not None else None
    except (TypeError, ValueError):
        total_count = None
    items = page_dict.get("item")
    item_count = len(items) if isinstance(items, list) else 0
    cur = conn.execute(
        """INSERT INTO endpoint_snapshots
           (source, offset, requested_count, total_count, item_count,
            response_json, fetched_at)
           VALUES (?,?,?,?,?,?,?)""",
        (
            source,
            offset,
            requested_count,
            total_count,
            item_count,
            json.dumps(page, ensure_ascii=False),
            fetched_at,
        ),
    )
    return cur.lastrowid


def store_item(conn, item, now_ts, source="freepublish"):
    """
    把一个 item（可能多图文）拆成多条 news_item 入库。material 用 media_id、
    freepublish 用 article_id 作 id。返回 (inserted, skipped)。
    """
    article_id = str(item.get("article_id") or item.get("media_id") or "")
    if not article_id:
        raise ValueError("item 缺少 article_id/media_id")
    publish_time = int(item.get("update_time", 0)
                       or (item.get("content") or {}).get("update_time", 0))
    news = (item.get("content") or {}).get("news_item") or []
    inserted = skipped = 0
    for idx, n in enumerate(news):
        cur = conn.execute(
            """INSERT OR IGNORE INTO articles
               (article_id, idx, source, title, author, digest, content_html,
                thumb_url, content_source_url, url, publish_time,
                is_deleted, raw_json, fetched_at)
               VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                article_id, idx, source,
                n.get("title"), n.get("author"), n.get("digest"),
                n.get("content"), n.get("thumb_url"),
                n.get("content_source_url"), n.get("url"),
                publish_time,
                1 if n.get("is_deleted") else 0,
                json.dumps(n, ensure_ascii=False),
                now_ts,
            ),
        )
        if cur.rowcount > 0:
            inserted += 1
        else:
            skipped += 1
    return inserted, skipped


# ----------------------------------------------------------------------------
# 补抓主流程
# ----------------------------------------------------------------------------
def now_ts():
    return int(time.time())


def backfill(conn, token, max_articles=None, sleep_sec=1.0, resume=True,
             source="material", fetch_fn=None):
    """
    从新到旧分页归档。每个 source 的 offset 独立存入 meta，可断点续传。
    source: material（素材库快照）/ freepublish（“发布”对象快照）。
    max_articles: 本次最多处理多少「篇」(news_item)，达到即停（限速慢慢补）。None=不限。
    返回统计 dict。
    """
    if source not in SOURCES:
        raise ValueError(f"不支持的归档端点：{source}")
    if fetch_fn is None:
        fetch_fn = fetch_material if source == "material" else fetch_page
    offset = int(progress_get(
        conn, source, "next_offset", "0")) if resume else 0
    # 一旦开始新一轮请求，旧 done 就不能继续代表当前轮状态；异常/崩溃时保持非完成。
    progress_set(conn, source, "done", 0)
    conn.commit()
    total_count = None
    ins = skp = pages = seen = endpoint_items = 0

    while True:
        page = fetch_fn(token, offset, PAGE_SIZE)
        fetched_at = now_ts()
        store_endpoint_snapshot(
            conn, source, offset, PAGE_SIZE, page, fetched_at)
        pages += 1
        page_dict = page if isinstance(page, dict) else {}
        raw_total = page_dict.get("total_count")
        try:
            total_count = int(raw_total) if raw_total is not None else None
        except (TypeError, ValueError):
            total_count = None
        progress_set(
            conn, source, "total_count",
            total_count if total_count is not None else "")
        # 原始页先独立持久化；之后的派生索引失败或进程退出都不能把它回滚掉。
        conn.commit()

        if not isinstance(page, dict):
            raise ArchiveConsistencyError(
                f"{source} 在 offset={offset} 返回的顶层不是 JSON object"
            )
        if page.get("errcode"):
            raise WxError(page.get("errcode"), page.get("errmsg", ""))
        if raw_total is None:
            raise ArchiveConsistencyError(
                f"{source} 在 offset={offset} 的响应缺少 total_count"
            )
        if total_count is None:
            raise ArchiveConsistencyError(
                f"{source} 在 offset={offset} 的 total_count 不是整数"
            )
        raw_items = page.get("item")
        if raw_items is not None and not isinstance(raw_items, list):
            raise ArchiveConsistencyError(
                f"{source} 在 offset={offset} 的 item 不是数组"
            )
        items = raw_items or []
        if not items:
            if total_count is not None and offset < total_count:
                raise ArchiveConsistencyError(
                    f"{source} 在 offset={offset} 返回空页，但 "
                    f"total_count={total_count}；为避免漏页，未推进游标"
                )
            progress_set(conn, source, "done", 1)
            break

        for item in items:
            try:
                i, s = store_item(conn, item, fetched_at, source)
            except (TypeError, ValueError) as exc:
                raise ArchiveConsistencyError(
                    f"{source} 在 offset={offset} 的派生文章索引失败：{exc}"
                ) from exc
            ins += i
            skp += s
            seen += (i + s)
        endpoint_items += len(items)
        offset += len(items)          # 官方以 item 数推进 offset
        progress_set(conn, source, "next_offset", offset)
        conn.commit()

        if total_count is not None and offset >= total_count:
            progress_set(conn, source, "done", 1)
            break
        if max_articles is not None and seen >= max_articles:
            break
        if sleep_sec:
            time.sleep(sleep_sec)

    progress_set(conn, source, "last_run_ts", now_ts())
    conn.commit()
    return {
        "pages": pages, "endpoint_items": endpoint_items,
        "seen": seen, "inserted": ins, "skipped": skp,
        "next_offset": offset, "total_count": total_count,
        "done": progress_get(conn, source, "done") == "1",
    }


def db_count(conn, source=None):
    if source is None:
        return conn.execute("SELECT COUNT(*) FROM articles").fetchone()[0]
    if source not in SOURCES:
        raise ValueError(f"不支持的归档端点：{source}")
    return conn.execute(
        "SELECT COUNT(*) FROM articles WHERE source=?", (source,)
    ).fetchone()[0]


def snapshot_count(conn, source):
    if source not in SOURCES:
        raise ValueError(f"不支持的归档端点：{source}")
    return conn.execute(
        "SELECT COUNT(*) FROM endpoint_snapshots WHERE source=?", (source,)
    ).fetchone()[0]


# ----------------------------------------------------------------------------
# CLI
# ----------------------------------------------------------------------------
def cmd_status(conn):
    print("=== 归档库状态（各端点独立）===")
    print("全部已索引 news_item 篇数 :", db_count(conn))
    for source in SOURCES:
        print(f"\n[{source}]")
        print("已索引 news_item 篇数 :", db_count(conn, source))
        print("完整响应快照页数      :", snapshot_count(conn, source))
        print("端点 total_count      :",
              progress_get(conn, source, "total_count", "?"))
        print("下一页 offset(游标)   :",
              progress_get(conn, source, "next_offset", "0"))
        print("是否抓完(done)        :",
              progress_get(conn, source, "done", "0"))
        print("上次运行              :",
              progress_get(conn, source, "last_run_ts", "-"))
    if any(meta_get(conn, key) is not None
           for key in ("next_offset", "total_count", "done", "last_run_ts")):
        print("\n提示：检测到旧版无来源 checkpoint；因归属不可核验，"
              "新版保留但不会继承。")


def whitelist_hint(err):
    if isinstance(err, WxError) and err.code == 40164:
        print("\n" + "=" * 60, file=sys.stderr)
        print("⛔ IP 未加入公众号白名单，无法调用接口。", file=sys.stderr)
        print(f"   微信原文：{err.msg}", file=sys.stderr)
        print("   请在公众号后台 → 设置与开发 → 基本配置 → IP白名单，", file=sys.stderr)
        print("   把上面报错里的那个 IP 加进去，保存后重跑本脚本。", file=sys.stderr)
        print("=" * 60, file=sys.stderr)


def main(argv=None):
    ap = argparse.ArgumentParser(description="FBIF 公众号官方端点响应归档工具")
    ap.add_argument("--db", default=os.environ.get("MP_ARCHIVE_DB", DEFAULT_DB),
                    help="SQLite 库路径（默认脚本同目录 mp-archive.db）")
    ap.add_argument("--source", choices=["material", "freepublish"],
                    default="material",
                    help="归档端点：material=图文素材库快照(默认) / "
                         "freepublish=“发布”对象快照；都不是历史已发布全量")
    ap.add_argument("--max-articles", type=int, default=None,
                    help="本次最多抓多少篇（限速慢慢补；不填=一直抓到完）")
    ap.add_argument("--sleep", type=float, default=1.0,
                    help="每页之间停顿秒数，默认 1s，别把配额打爆")
    ap.add_argument("--reset", action="store_true",
                    help="只重置所选端点的游标，从 offset=0 重扫；不清库")
    ap.add_argument("--status", action="store_true", help="只看库状态，不抓")
    args = ap.parse_args(argv)

    conn = open_db(args.db)
    if args.status:
        cmd_status(conn)
        return 0
    if args.reset:
        reset_progress(conn, args.source)

    appid = os.environ.get("WECHAT_MP_APPID", "").strip()
    secret = os.environ.get("WECHAT_MP_SECRET", "").strip()
    if not appid or not secret:
        print("❌ 缺少环境变量 WECHAT_MP_APPID / WECHAT_MP_SECRET", file=sys.stderr)
        return 2

    try:
        token, _ = fetch_token(appid, secret)
    except WxError as e:
        whitelist_hint(e)
        if not (isinstance(e, WxError) and e.code == 40164):
            print(f"❌ 取 token 失败：{e}", file=sys.stderr)
        return 3
    except (urllib.error.URLError, OSError) as e:
        print(f"❌ 取 token 网络错误：{e}", file=sys.stderr)
        return 3

    before = db_count(conn, args.source)
    try:
        stat = backfill(conn, token, max_articles=args.max_articles,
                        sleep_sec=args.sleep, source=args.source)
    except WxError as e:
        whitelist_hint(e)
        print(f"❌ 抓取中断：{e}", file=sys.stderr)
        return 4
    except ArchiveConsistencyError as e:
        print(f"❌ 分页一致性校验失败：{e}", file=sys.stderr)
        return 5

    after = db_count(conn, args.source)
    print("=== 本次抓取完成 ===")
    print(f"端点 {args.source}：保存 {stat['pages']} 页完整响应，"
          f"处理 {stat['endpoint_items']} 个对象 / {stat['seen']} 篇，"
          f"新入库 {stat['inserted']}，去重跳过 {stat['skipped']}")
    print(f"该端点索引篇数 {before} → {after}；"
          f"游标 next_offset={stat['next_offset']}")
    print(f"接口 total_count={stat['total_count']}，是否抓完={stat['done']}")
    # 端点对象游标和文章索引行数口径不同，禁止拿后者证明分页完整。
    if (stat["done"] and stat["total_count"] is not None
            and stat["next_offset"] < stat["total_count"]):
        print("❌ done=1 但端点对象游标尚未达到 total_count。", file=sys.stderr)
        return 5
    return 0


if __name__ == "__main__":
    sys.exit(main())
