#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
mp_archive 离线单测：mock 掉 fetch_page，验证解析/去重/游标/多图文/计数。
不碰网络，白名单没加也能全绿。跑：python3 test_mp_archive.py
"""
import os
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import mp_archive as M


def _news_item(title, url, deleted=False):
    return {
        "title": title, "author": "FBIF", "digest": title + "摘要",
        "content": f"<p>{title} 正文HTML</p>",
        "content_source_url": "https://orig/" + title,
        "thumb_url": "https://mmbiz.qpic.cn/" + title + ".jpg",
        "url": url, "is_deleted": deleted,
    }


def _item(article_id, update_time, news):
    return {"article_id": article_id, "update_time": update_time,
            "content": {"news_item": news}}


class FakeWx:
    """按 offset 返回预置页；记录被请求的 offset 序列。"""
    def __init__(self, total_count, pages):
        self.total_count = total_count
        self.pages = pages          # {offset: [item,...]}
        self.calls = []

    def fetch_page(self, token, offset, count=M.PAGE_SIZE):
        self.calls.append(offset)
        return {"total_count": self.total_count,
                "item": self.pages.get(offset, [])}


class TestArchive(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.db = os.path.join(self.tmp, "t.db")
        self.conn = M.open_db(self.db)

    def test_store_multi_news_item(self):
        """多图文一条 item 拆成多篇，主键 (article_id, idx)。"""
        it = _item("A1", 1000, [_news_item("头条", "u0"),
                                 _news_item("次条", "u1")])
        ins, skp = M.store_item(self.conn, it, 123)
        self.assertEqual((ins, skp), (2, 0))
        rows = self.conn.execute(
            "SELECT article_id, idx, title, content_html FROM articles "
            "ORDER BY idx").fetchall()
        self.assertEqual(rows[0][:3], ("A1", 0, "头条"))
        self.assertEqual(rows[1][:3], ("A1", 1, "次条"))
        self.assertIn("正文HTML", rows[0][3])

    def test_material_media_id_and_source(self):
        """material 源用 media_id 作 id，source 列写 material，正文入库。"""
        it = {"media_id": "M9", "update_time": 1600,
              "content": {"news_item": [_news_item("素材篇", "mu")]}}
        ins, skp = M.store_item(self.conn, it, 1, source="material")
        self.assertEqual((ins, skp), (1, 0))
        row = self.conn.execute(
            "SELECT article_id, source, title FROM articles").fetchone()
        self.assertEqual(row, ("M9", "material", "素材篇"))

    def test_dedup_on_reinsert(self):
        """同 (article_id, idx) 再插被 INSERT OR IGNORE 跳过。"""
        it = _item("A1", 1000, [_news_item("头条", "u0")])
        self.assertEqual(M.store_item(self.conn, it, 1)[0], 1)
        ins, skp = M.store_item(self.conn, it, 2)
        self.assertEqual((ins, skp), (0, 1))
        self.assertEqual(M.db_count(self.conn), 1)

    def test_is_deleted_flag(self):
        it = _item("D1", 1, [_news_item("被删", "u", deleted=True)])
        M.store_item(self.conn, it, 1)
        v = self.conn.execute(
            "SELECT is_deleted FROM articles WHERE article_id='D1'").fetchone()[0]
        self.assertEqual(v, 1)

    def test_backfill_paging_and_cursor(self):
        """两页抓完，游标推进到 total，done=1，offset 序列正确。"""
        pages = {
            0: [_item("A", 30, [_news_item("a", "ua")]),
                _item("B", 29, [_news_item("b", "ub")])],
            2: [_item("C", 28, [_news_item("c", "uc")])],
        }
        fake = FakeWx(total_count=3, pages=pages)
        stat = M.backfill(self.conn, "tok", sleep_sec=0, fetch_fn=fake.fetch_page)
        self.assertEqual(stat["inserted"], 3)
        self.assertEqual(stat["next_offset"], 3)
        self.assertTrue(stat["done"])
        self.assertEqual(fake.calls, [0, 2])    # 第二页从 offset=2 起
        self.assertEqual(M.db_count(self.conn), 3)

    def test_backfill_resume(self):
        """断点续传：第一次限 1 篇停下，第二次接着抓且不重复。"""
        pages = {
            0: [_item("A", 30, [_news_item("a", "ua")]),
                _item("B", 29, [_news_item("b", "ub")])],
            2: [_item("C", 28, [_news_item("c", "uc")])],
        }
        fake = FakeWx(total_count=3, pages=pages)
        s1 = M.backfill(self.conn, "tok", max_articles=1, sleep_sec=0,
                        fetch_fn=fake.fetch_page)
        self.assertFalse(s1["done"])            # 没抓完
        self.assertEqual(s1["next_offset"], 2)  # 第一页(2篇)后就到量停
        s2 = M.backfill(self.conn, "tok", sleep_sec=0,
                        fetch_fn=fake.fetch_page)   # 续
        self.assertTrue(s2["done"])
        self.assertEqual(s2["inserted"], 1)     # 只新增 C，A/B 去重
        self.assertEqual(M.db_count(self.conn), 3)

    def test_reset_cursor_but_dedup(self):
        """--reset 从头再抓，已入库的全去重，篇数不变。"""
        pages = {0: [_item("A", 30, [_news_item("a", "ua")])]}
        fake = FakeWx(total_count=1, pages=pages)
        M.backfill(self.conn, "tok", sleep_sec=0, fetch_fn=fake.fetch_page)
        M.meta_set(self.conn, "next_offset", "0")   # 模拟 --reset
        M.meta_set(self.conn, "done", "0")
        stat = M.backfill(self.conn, "tok", sleep_sec=0, resume=True,
                          fetch_fn=fake.fetch_page)
        self.assertEqual(stat["inserted"], 0)
        self.assertEqual(stat["skipped"], 1)
        self.assertEqual(M.db_count(self.conn), 1)

    def test_token_whitelist_error_surfaces(self):
        """40164 应抛 WxError 带 code，供 CLI 打印白名单指引。"""
        def boom(url, payload):
            return {"errcode": 40164, "errmsg": "invalid ip x.x.x.x not in whitelist"}
        M._post_json = boom
        with self.assertRaises(M.WxError) as ctx:
            M.fetch_token("appid", "secret")
        self.assertEqual(ctx.exception.code, 40164)


if __name__ == "__main__":
    unittest.main(verbosity=2)
