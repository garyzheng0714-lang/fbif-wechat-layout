import importlib.util
import os
import pathlib
import tempfile
import unittest
from datetime import datetime, timezone
from types import SimpleNamespace
from unittest import mock


SPEC = importlib.util.spec_from_file_location("watchdog", pathlib.Path(__file__).with_name("watchdog.py"))
watchdog = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(watchdog)


class WatchdogTest(unittest.TestCase):
    def test_requires_two_failures_then_deduplicates_volatile_counts(self):
        now = datetime(2026, 7, 17, 1, 0, tzinfo=timezone.utc)
        first, kind, _ = watchdog.decide_notification({}, ["feed:poller_stale:181s"], now, False)
        self.assertEqual("", kind)
        second, kind, message = watchdog.decide_notification(first, ["feed:poller_stale:301s"], now, False)
        self.assertEqual("critical", kind)
        self.assertIn("连续失败：2 次", message)
        watchdog.record_notification(second, kind, now)
        third, kind, _ = watchdog.decide_notification(second, ["feed:poller_stale:421s"], now, False)
        self.assertEqual("", kind)
        self.assertEqual(second["notified_fingerprint"], third["fingerprint"])

    def test_recovery_and_daily_heartbeat(self):
        now = datetime(2026, 7, 17, 2, 0, tzinfo=timezone.utc)
        previous = {"incident_open": True, "consecutive_failures": 3}
        recovered, kind, message = watchdog.decide_notification(previous, [], now, False)
        self.assertEqual("recovery", kind)
        self.assertIn("已恢复", message)
        self.assertIn("审核卡全部送达", message)
        watchdog.record_notification(recovered, kind, now)
        _, kind, _ = watchdog.decide_notification(recovered, [], now, False)
        self.assertEqual("", kind)
        self.assertEqual("2026-07-17", recovered["last_heartbeat_date"])

    def test_before_0920_has_no_heartbeat(self):
        now = datetime(2026, 7, 17, 1, 19, tzinfo=timezone.utc)
        _, kind, _ = watchdog.decide_notification({}, [], now, False)
        self.assertEqual("", kind)

    def test_state_is_atomic_and_invalid_state_fails_closed_to_empty(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory, "state.json")
            watchdog.save_state(path, {"healthy": True})
            self.assertEqual({"healthy": True}, watchdog.load_state(path))
            path.write_text("{broken", encoding="utf-8")
            self.assertEqual({}, watchdog.load_state(path))

    def test_evaluate_requires_explicit_ready(self):
        self.assertEqual([], watchdog.evaluate("feed", {"ready": True}))
        self.assertEqual(["feed:not_ready"], watchdog.evaluate("feed", {}))
        self.assertEqual(["feed:gap:1"], watchdog.evaluate("feed", {"ready": False, "issues": ["gap:1"]}))

    def test_drill_fixture_sends_incident_then_recovery(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(
            os.environ,
            {
                "PUBLISH_SYNC_SERVICE_TOKEN": "service-token",
                "FEISHU_APP_ID": "app-id",
                "FEISHU_APP_SECRET": "app-secret",
                "PUBLISH_MONITOR_FEISHU_USER_ID": "test-monitor-user",
            },
            clear=True,
        ), mock.patch.object(watchdog, "send_feishu_app") as sender:
            args = SimpleNamespace(
                state=str(pathlib.Path(directory, "state.json")),
                timeout=1.0,
                layout_url="unused",
                feed_url="unused",
                official_url="unused",
                drill=True,
                fixture_ready=True,
                inject_issue="simulated outage",
            )
            self.assertEqual(1, watchdog.run(args))
            self.assertEqual(1, watchdog.run(args))
            self.assertEqual(1, sender.call_count)
            self.assertIn("[演练]", sender.call_args.args[3])
            args.inject_issue = ""
            self.assertEqual(0, watchdog.run(args))
            self.assertEqual(2, sender.call_count)
            self.assertIn("已恢复", sender.call_args.args[3])

    def test_feishu_sender_uses_private_user_id(self):
        calls = []

        def fake_post(url, payload, headers, timeout, name):
            calls.append((url, payload, headers, timeout, name))
            if name == "feishu_token":
                return {"code": 0, "tenant_access_token": "token"}
            return {"code": 0}

        with mock.patch.object(watchdog, "_post_json", side_effect=fake_post):
            watchdog.send_feishu_app("app-id", "app-secret", "test-monitor-user", "私聊测试", 1.0)

        self.assertEqual(2, len(calls))
        message_url, payload, _, _, _ = calls[1]
        self.assertTrue(message_url.endswith("?receive_id_type=user_id"))
        self.assertEqual("test-monitor-user", payload["receive_id"])


if __name__ == "__main__":
    unittest.main()
