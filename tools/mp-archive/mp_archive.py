#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
FBIF 公众号历史文章库 — 微信官方接口抓取器

用官方 `freepublish/batchget`（获取已发布文章列表）把某个公众号的全量历史文章
落成一个可查询、可去重、可断点续抓的 SQLite 库。凭据只从环境变量读，绝不写死。

设计要点：
- token 用 `stable_token`（force_refresh=false，可共存），不顶掉别人手里的 token。
- 从新到旧分页（offset/count），游标存库里，随时中断随时接着抓（"慢慢补"）。
- 去重靠主键 (article_id, idx)；多图文一条 item 拆成多条 news_item 入库。
- 批量操作带计数校验：开始记 total_count，结束核对入库数。
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
    freepublish/batchget：只含「通过发布 API 发表」的记录——对 FBIF 这种大量后台
    人工群发的号是**不完整子集**（实测仅 262，漏掉绝大部分）。保留仅作对照，默认不用。
    """
    url = f"{API_BASE}/freepublish/batchget?access_token={token}"
    body = {"offset": offset, "count": count, "no_content": 0}
    d = _post_json(url, body)
    if d.get("errcode"):
        raise WxError(d.get("errcode"), d.get("errmsg", ""))
    return d


def fetch_material(token, offset, count=PAGE_SIZE):
    """
    material/batchget_material（图文素材库）：FBIF 全量文章的真实所在（实测 6483 组图文，
    含完整正文、可追溯到更早年份）。返回同样是 {total_count, item[]}，但每条 item 用
    `media_id` 作 id、含 news_item[] 完整正文。这是建库的**主源**。
    """
    url = f"{API_BASE}/material/batchget_material?access_token={token}"
    body = {"type": "news", "offset": offset, "count": count}
    d = _post_json(url, body)
    if d.get("errcode"):
        raise WxError(d.get("errcode"), d.get("errmsg", ""))
    return d


class WxError(Exception):
    def __init__(self, code, msg):
        self.code = code
        self.msg = msg
        super().__init__(f"微信接口错误 errcode={code} errmsg={msg}")


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
    publish_time        INTEGER,         -- update_time，秒级时间戳
    is_deleted          INTEGER DEFAULT 0,
    raw_json            TEXT,            -- 该 news_item 原始 JSON，留档兜底
    fetched_at          INTEGER,
    PRIMARY KEY (article_id, idx)
);
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


def store_item(conn, item, now_ts, source="freepublish"):
    """
    把一个 item（可能多图文）拆成多条 news_item 入库。material 用 media_id、
    freepublish 用 article_id 作 id。返回 (inserted, skipped)。
    """
    article_id = str(item.get("article_id") or item.get("media_id") or "")
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
    从新到旧分页补抓。offset 游标存 meta.next_offset，可断点续传。
    source: material（主源，全量）/ freepublish（对照子集）。
    max_articles: 本次最多处理多少「篇」(news_item)，达到即停（限速慢慢补）。None=不限。
    返回统计 dict。
    """
    if fetch_fn is None:
        fetch_fn = fetch_material if source == "material" else fetch_page
    offset = int(meta_get(conn, "next_offset", "0")) if resume else 0
    total_count = None
    ins = skp = pages = seen = 0

    while True:
        page = fetch_fn(token, offset, PAGE_SIZE)
        total_count = int(page.get("total_count", 0))
        items = page.get("item") or []
        if not items:
            meta_set(conn, "done", "1")
            break

        for item in items:
            i, s = store_item(conn, item, now_ts(), source)
            ins += i
            skp += s
            seen += (i + s)
        pages += 1
        offset += len(items)          # 官方以 item 数推进 offset
        meta_set(conn, "next_offset", offset)
        meta_set(conn, "total_count", total_count)
        conn.commit()

        if offset >= total_count:
            meta_set(conn, "done", "1")
            break
        if max_articles is not None and ins >= max_articles:
            break
        if sleep_sec:
            time.sleep(sleep_sec)

    meta_set(conn, "last_run_ts", now_ts())
    conn.commit()
    return {
        "pages": pages, "seen": seen, "inserted": ins, "skipped": skp,
        "next_offset": offset, "total_count": total_count,
        "done": meta_get(conn, "done") == "1",
    }


def db_count(conn):
    return conn.execute("SELECT COUNT(*) FROM articles").fetchone()[0]


# ----------------------------------------------------------------------------
# CLI
# ----------------------------------------------------------------------------
def cmd_status(conn):
    print("=== 库状态 ===")
    print("已入库 news_item 篇数 :", db_count(conn))
    print("接口 total_count      :", meta_get(conn, "total_count", "?"))
    print("下一页 offset(游标)   :", meta_get(conn, "next_offset", "0"))
    print("是否抓完(done)        :", meta_get(conn, "done", "0"))
    print("上次运行              :", meta_get(conn, "last_run_ts", "-"))


def whitelist_hint(err):
    if isinstance(err, WxError) and err.code == 40164:
        print("\n" + "=" * 60, file=sys.stderr)
        print("⛔ IP 未加入公众号白名单，无法调用接口。", file=sys.stderr)
        print(f"   微信原文：{err.msg}", file=sys.stderr)
        print("   请在公众号后台 → 设置与开发 → 基本配置 → IP白名单，", file=sys.stderr)
        print("   把上面报错里的那个 IP 加进去，保存后重跑本脚本。", file=sys.stderr)
        print("=" * 60, file=sys.stderr)


def main(argv=None):
    ap = argparse.ArgumentParser(description="FBIF 公众号历史文章库抓取器")
    ap.add_argument("--db", default=os.environ.get("MP_ARCHIVE_DB", DEFAULT_DB),
                    help="SQLite 库路径（默认脚本同目录 mp-archive.db）")
    ap.add_argument("--source", choices=["material", "freepublish"],
                    default="material",
                    help="抓取源：material=图文素材库(全量,默认) / freepublish=发表记录(子集,对照用)")
    ap.add_argument("--max-articles", type=int, default=None,
                    help="本次最多抓多少篇（限速慢慢补；不填=一直抓到完）")
    ap.add_argument("--sleep", type=float, default=1.0,
                    help="每页之间停顿秒数，默认 1s，别把配额打爆")
    ap.add_argument("--reset", action="store_true",
                    help="重置游标从头(offset=0)再抓一遍（已入库的会被去重跳过）")
    ap.add_argument("--status", action="store_true", help="只看库状态，不抓")
    args = ap.parse_args(argv)

    conn = open_db(args.db)
    if args.status:
        cmd_status(conn)
        return 0
    if args.reset:
        meta_set(conn, "next_offset", "0")
        meta_set(conn, "done", "0")
        conn.commit()

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

    before = db_count(conn)
    try:
        stat = backfill(conn, token, max_articles=args.max_articles,
                        sleep_sec=args.sleep, source=args.source)
    except WxError as e:
        whitelist_hint(e)
        print(f"❌ 抓取中断：{e}", file=sys.stderr)
        return 4

    after = db_count(conn)
    print("=== 本次抓取完成 ===")
    print(f"翻页 {stat['pages']} 页，处理 {stat['seen']} 篇，"
          f"新入库 {stat['inserted']}，去重跳过 {stat['skipped']}")
    print(f"库内篇数 {before} → {after}；游标 next_offset={stat['next_offset']}")
    print(f"接口 total_count={stat['total_count']}，是否抓完={stat['done']}")
    # 计数校验（CLAUDE.md 批量操作强制校验）
    if stat["done"] and stat["total_count"]:
        # total_count 是「item(发布次)」数，多图文会 >= 它，仅作下限提示
        if after < stat["total_count"]:
            print(f"⚠️ 入库篇数({after}) < total_count({stat['total_count']})，"
                  f"请检查是否有页抓失败。", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
