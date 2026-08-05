#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
mp_archive 离线单测：mock 掉 fetch_page，验证解析/去重/分源游标/原始响应/多图文/计数。
不碰网络，白名单没加也能全绿。跑：python3 test_mp_archive.py
"""
import json
import os
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

    def test_progress_is_scoped_by_source(self):
        """切换端点不能覆盖另一端点的断点。"""
        material_pages = {
            0: [_item("M-A", 30, [_news_item("ma", "uma")]),
                _item("M-B", 29, [_news_item("mb", "umb")])],
            2: [_item("M-C", 28, [_news_item("mc", "umc")])],
        }
        material = FakeWx(total_count=3, pages=material_pages)
        first = M.backfill(
            self.conn, "tok", max_articles=1, sleep_sec=0,
            source="material", fetch_fn=material.fetch_page,
        )
        self.assertEqual(first["next_offset"], 2)

        freepublish = FakeWx(
            total_count=1,
            pages={0: [_item("F-A", 20, [_news_item("fa", "ufa")])]},
        )
        M.backfill(
            self.conn, "tok", sleep_sec=0, resume=False,
            source="freepublish", fetch_fn=freepublish.fetch_page,
        )

        resumed = FakeWx(total_count=3, pages=material_pages)
        second = M.backfill(
            self.conn, "tok", sleep_sec=0,
            source="material", fetch_fn=resumed.fetch_page,
        )
        self.assertEqual(resumed.calls, [2])
        self.assertTrue(second["done"])

    def test_reset_only_affects_selected_source(self):
        """重置 freepublish 时保留 material 的游标和完成状态。"""
        M.progress_set(self.conn, "material", "next_offset", 40)
        M.progress_set(self.conn, "material", "done", 1)
        M.progress_set(self.conn, "freepublish", "next_offset", 12)
        M.progress_set(self.conn, "freepublish", "done", 1)

        M.reset_progress(self.conn, "freepublish")

        self.assertEqual(M.progress_get(
            self.conn, "material", "next_offset"), "40")
        self.assertEqual(M.progress_get(self.conn, "material", "done"), "1")
        self.assertEqual(M.progress_get(
            self.conn, "freepublish", "next_offset"), "0")
        self.assertEqual(M.progress_get(self.conn, "freepublish", "done"), "0")

    def test_legacy_global_cursor_is_not_reused_for_a_source(self):
        """旧版无来源游标无法证明归属，升级后应安全地从端点头部重扫。"""
        M.meta_set(self.conn, "next_offset", 99)
        M.meta_set(self.conn, "done", 1)
        fake = FakeWx(
            total_count=1,
            pages={0: [_item("M-A", 20, [_news_item("ma", "uma")])]},
        )

        M.backfill(
            self.conn, "tok", sleep_sec=0,
            source="material", fetch_fn=fake.fetch_page,
        )

        self.assertEqual(fake.calls, [0])
        self.assertEqual(M.meta_get(self.conn, "next_offset"), "99")

    def test_full_endpoint_response_is_archived(self):
        """逐页保存顶层和父 item 字段，不能只留下拆出的 news_item。"""
        page = {
            "total_count": 1,
            "request_id": "req-123",
            "item": [{
                "article_id": "A1",
                "update_time": 1000,
                "parent_only": {"audit": "keep-me"},
                "content": {"news_item": [_news_item("头条", "u0")]},
            }],
        }
        fake = FakeWx(total_count=1, pages={})

        def fetch_page(token, offset, count=M.PAGE_SIZE):
            fake.calls.append(offset)
            return page

        M.backfill(
            self.conn, "tok", sleep_sec=0,
            source="freepublish", fetch_fn=fetch_page,
        )

        row = self.conn.execute(
            "SELECT source, offset, requested_count, total_count, response_json "
            "FROM endpoint_snapshots"
        ).fetchone()
        self.assertEqual(row[:4], ("freepublish", 0, M.PAGE_SIZE, 1))
        archived = json.loads(row[4])
        self.assertEqual(archived["request_id"], "req-123")
        self.assertEqual(
            archived["item"][0]["parent_only"]["audit"], "keep-me")

    def test_endpoint_error_response_is_archived_and_fails(self):
        """微信业务错误也要先留原始响应，再非零失败且不推进游标。"""
        error_page = {
            "errcode": 45009,
            "errmsg": "reach max api daily quota limit",
        }

        with self.assertRaises(M.WxError) as ctx:
            M.backfill(
                self.conn, "tok", sleep_sec=0,
                source="freepublish",
                fetch_fn=lambda token, offset, count: error_page,
            )

        self.assertEqual(ctx.exception.code, 45009)
        row = self.conn.execute(
            "SELECT response_json FROM endpoint_snapshots"
        ).fetchone()
        self.assertEqual(json.loads(row[0]), error_page)
        self.assertEqual(M.progress_get(
            self.conn, "freepublish", "next_offset", "0"), "0")
        self.assertNotEqual(M.progress_get(
            self.conn, "freepublish", "done", "0"), "1")

    def test_raw_response_survives_derived_index_failure(self):
        """派生 articles 解析失败时，已收到的官方页响应仍须持久保存。"""
        malformed_page = {
            "total_count": 1,
            "item": [{
                "article_id": "A1",
                "update_time": "not-an-integer",
                "content": {"news_item": [_news_item("头条", "u0")]},
            }],
        }

        with self.assertRaises(M.ArchiveConsistencyError):
            M.backfill(
                self.conn, "tok", sleep_sec=0,
                source="freepublish",
                fetch_fn=lambda token, offset, count: malformed_page,
            )

        self.conn.close()
        self.conn = M.open_db(self.db)
        self.assertEqual(M.snapshot_count(self.conn, "freepublish"), 1)
        self.assertEqual(M.db_count(self.conn, "freepublish"), 0)

    def test_fetch_failure_persists_non_done_state(self):
        """请求失败或进程退出后，上一轮 done=1 不得继续假绿。"""
        M.progress_set(self.conn, "material", "done", 1)
        self.conn.commit()

        def fail_before_response(token, offset, count):
            raise M.WxError(50002, "request failed")

        with self.assertRaises(M.WxError):
            M.backfill(
                self.conn, "tok", sleep_sec=0,
                source="material", fetch_fn=fail_before_response,
            )

        self.conn.close()
        self.conn = M.open_db(self.db)
        self.assertEqual(M.progress_get(
            self.conn, "material", "done", "0"), "0")

    def test_unexpected_empty_page_fails_closed_and_keeps_snapshot(self):
        """游标未到 total_count 却返回空页时，不得假装抓完。"""
        M.progress_set(self.conn, "material", "done", 1)
        fake = FakeWx(total_count=3, pages={})

        with self.assertRaises(M.ArchiveConsistencyError):
            M.backfill(
                self.conn, "tok", sleep_sec=0,
                source="material", fetch_fn=fake.fetch_page,
            )

        self.assertEqual(M.snapshot_count(self.conn, "material"), 1)
        self.assertEqual(M.progress_get(
            self.conn, "material", "next_offset", "0"), "0")
        self.assertNotEqual(M.progress_get(
            self.conn, "material", "done", "0"), "1")

    def test_db_count_can_be_scoped_by_source(self):
        M.store_item(
            self.conn,
            _item("M-A", 10, [_news_item("ma", "uma")]),
            1,
            source="material",
        )
        M.store_item(
            self.conn,
            _item("F-A", 9, [_news_item("fa", "ufa")]),
            1,
            source="freepublish",
        )
        self.assertEqual(M.db_count(self.conn), 2)
        self.assertEqual(M.db_count(self.conn, "material"), 1)
        self.assertEqual(M.db_count(self.conn, "freepublish"), 1)

    def test_reset_cursor_but_dedup(self):
        """--reset 从头再抓，已入库的全去重，篇数不变。"""
        pages = {0: [_item("A", 30, [_news_item("a", "ua")])]}
        fake = FakeWx(total_count=1, pages=pages)
        M.backfill(self.conn, "tok", sleep_sec=0, fetch_fn=fake.fetch_page)
        M.reset_progress(self.conn, "material")
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
