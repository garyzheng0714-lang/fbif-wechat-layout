#!/usr/bin/env python3
"""FBIF 公众号监控独立看门狗。

该进程由 systemd timer 启动，不依赖排版 API 的 worker。它从外部网络检查
排版、feed 和微信官方数据服务，并直接使用飞书应用凭证向指定 user_id 私聊告警。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re
import tempfile
import time
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone
from typing import Any


FEISHU_TOKEN_URL = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
FEISHU_MESSAGE_URL = "https://open.feishu.cn/open-apis/im/v1/messages"
SHANGHAI = timezone(timedelta(hours=8))
VOLATILE_NUMBER = re.compile(r"\d+")


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: ANN001
        return None


NO_REDIRECT_OPENER = urllib.request.build_opener(_NoRedirectHandler())


def fetch_json(name: str, url: str, headers: dict[str, str], timeout: float) -> dict[str, Any]:
    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with NO_REDIRECT_OPENER.open(request, timeout=timeout) as response:
            raw = response.read((1 << 20) + 1)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{name}:http_{error.code}") from error
    except Exception as error:
        raise RuntimeError(f"{name}:unreachable:{type(error).__name__}") from error
    if len(raw) > 1 << 20:
        raise RuntimeError(f"{name}:response_too_large")
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"{name}:invalid_json") from error
    if not isinstance(payload, dict):
        raise RuntimeError(f"{name}:invalid_payload")
    return payload


def evaluate(name: str, payload: dict[str, Any]) -> list[str]:
    if payload.get("ready") is True:
        return []
    issues = payload.get("issues")
    if not isinstance(issues, list) or not issues:
        return [f"{name}:not_ready"]
    return [f"{name}:{clean_text(str(issue), 240)}" for issue in issues]


def clean_text(value: str, max_chars: int) -> str:
    return " ".join(value.split())[:max_chars]


def fingerprint(issues: list[str]) -> str:
    # 秒数、计数会自然变化；告警指纹只关心故障类别，避免每两分钟刷屏。
    stable = [VOLATILE_NUMBER.sub("#", issue) for issue in sorted(set(issues))]
    encoded = json.dumps(stable, ensure_ascii=False, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def load_state(path: pathlib.Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
        return value if isinstance(value, dict) else {}
    except (OSError, json.JSONDecodeError):
        return {}


def save_state(path: pathlib.Path, state: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent, delete=False) as handle:
        json.dump(state, handle, ensure_ascii=False, sort_keys=True)
        handle.write("\n")
        temporary = pathlib.Path(handle.name)
    temporary.replace(path)


def _post_json(
    url: str,
    payload: dict[str, Any],
    headers: dict[str, str],
    timeout: float,
    name: str,
) -> dict[str, Any]:
    request = urllib.request.Request(
        url,
        data=json.dumps(payload, ensure_ascii=False).encode(),
        headers={"Content-Type": "application/json; charset=utf-8", **headers},
        method="POST",
    )
    try:
        with NO_REDIRECT_OPENER.open(request, timeout=timeout) as response:
            raw = response.read(8192)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{name}:http_{error.code}") from error
    try:
        result = json.loads(raw)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"{name}:invalid_json") from error
    if not isinstance(result, dict):
        raise RuntimeError(f"{name}:invalid_payload")
    return result


def send_feishu_app(app_id: str, app_secret: str, user_id: str, message: str, timeout: float) -> None:
    if not app_id or not app_secret or not user_id:
        raise RuntimeError("feishu_reporter_not_configured")
    token_result = _post_json(
        FEISHU_TOKEN_URL,
        {"app_id": app_id, "app_secret": app_secret},
        {},
        timeout,
        "feishu_token",
    )
    token = str(token_result.get("tenant_access_token", ""))
    if int(token_result.get("code", 0)) != 0 or not token:
        raise RuntimeError(f"feishu_token:code_{token_result.get('code')}")
    result = _post_json(
        FEISHU_MESSAGE_URL + "?receive_id_type=user_id",
        {
            "receive_id": user_id,
            "msg_type": "text",
            "content": json.dumps({"text": message[:15000]}, ensure_ascii=False),
        },
        {"Authorization": f"Bearer {token}"},
        timeout,
        "feishu_message",
    )
    if int(result.get("code", 0)) != 0:
        raise RuntimeError(f"feishu_message:code_{result.get('code')}")


def heartbeat_due(previous: dict[str, Any], now: datetime) -> bool:
    local = now.astimezone(SHANGHAI)
    return (local.hour, local.minute) >= (9, 20) and previous.get("last_heartbeat_date") != local.date().isoformat()


def decide_notification(
    previous: dict[str, Any], issues: list[str], now: datetime, drill: bool
) -> tuple[dict[str, Any], str, str]:
    issues = sorted(set(issues))
    current_fingerprint = fingerprint(issues)
    previous_failures = int(previous.get("consecutive_failures", 0) or 0)
    consecutive_failures = previous_failures + 1 if issues else 0
    incident_open = bool(previous.get("incident_open"))
    state = {
        **previous,
        "checked_at": now.astimezone(timezone.utc).isoformat(),
        "healthy": not issues,
        "issues": issues,
        "fingerprint": current_fingerprint,
        "consecutive_failures": consecutive_failures,
        "incident_open": incident_open,
    }
    prefix = "[演练]" if drill else ""
    checked_at = now.astimezone(SHANGHAI).strftime("%Y-%m-%d %H:%M:%S")

    if issues and consecutive_failures >= 2 and (
        not incident_open or previous.get("notified_fingerprint") != current_fingerprint
    ):
        message = (
            f"{prefix}[FBIF 公众号监控 P0] 异常\n"
            f"连续失败：{consecutive_failures} 次\n检查时间：{checked_at}\n"
            + "\n".join(f"- {issue}" for issue in issues)
        )
        return state, "critical", message
    if not issues and incident_open:
        return (
            state,
            "recovery",
            f"{prefix}[FBIF 公众号监控 P0] 已恢复\n检查时间：{checked_at}\n"
            "监控已确认的当日文章批次完整、普通文章全部进入审核、审核卡全部送达；三条探针无未完成项。",
        )
    if not issues and heartbeat_due(previous, now):
        return (
            state,
            "heartbeat",
            f"{prefix}[FBIF 公众号监控 P0] 每日心跳\n检查时间：{checked_at}\n"
            "监控已确认的当日文章批次完整、普通文章全部进入审核、审核卡全部送达；三条探针无未完成项。",
        )
    return state, "", ""


def record_notification(state: dict[str, Any], kind: str, now: datetime) -> None:
    if kind == "critical":
        state["incident_open"] = True
        state["notified_fingerprint"] = state["fingerprint"]
    elif kind == "recovery":
        state["incident_open"] = False
        state["notified_fingerprint"] = ""
        # 恢复通知本身已经是当天的绿色业务确认；不得在下一次两分钟轮询中
        # 紧接着再发一条“每日心跳”，覆盖用户刚看到的事故恢复语义。
        state["last_heartbeat_date"] = now.astimezone(SHANGHAI).date().isoformat()
    elif kind == "heartbeat":
        state["last_heartbeat_date"] = now.astimezone(SHANGHAI).date().isoformat()
    state["last_notification_kind"] = kind
    state["last_notification_at"] = now.astimezone(timezone.utc).isoformat()


def run(args: argparse.Namespace) -> int:
    now = datetime.now(timezone.utc)
    state_path = pathlib.Path(args.state)
    previous = load_state(state_path)
    service_token = os.getenv("PUBLISH_SYNC_SERVICE_TOKEN", "").strip()
    reporter = (
        os.getenv("FEISHU_APP_ID", "").strip(),
        os.getenv("FEISHU_APP_SECRET", "").strip(),
        os.getenv("PUBLISH_MONITOR_FEISHU_USER_ID", "").strip(),
    )
    issues: list[str] = []
    if not service_token:
        issues.append("watchdog:service_token_missing")
    if not all(reporter):
        issues.append("watchdog:feishu_reporter_not_configured")
    if args.fixture_ready and not args.drill:
        print("watchdog: --fixture-ready is only allowed with --drill")
        return 2

    if not args.fixture_ready:
        checks = [
            ("layout", args.layout_url),
            ("feed", args.feed_url),
            ("official", args.official_url),
        ]
        for name, url in checks:
            try:
                issues.extend(evaluate(name, fetch_json(name, url, {"X-Publish-Sync-Token": service_token}, args.timeout)))
            except RuntimeError as error:
                issues.append(str(error))
    if args.inject_issue:
        if not args.drill:
            print("watchdog: --inject-issue is only allowed with --drill")
            return 2
        issues.append("drill:" + clean_text(args.inject_issue, 240))

    state, kind, message = decide_notification(previous, issues, now, args.drill)
    reporter_failed = not all(reporter)
    if kind and not reporter_failed:
        try:
            send_feishu_app(*reporter, message, args.timeout)
            record_notification(state, kind, now)
        except Exception as error:  # 告警发送失败本身必须让 unit 进入 failed，而非伪装成功。
            reporter_failed = True
            state["notification_error"] = clean_text(f"{type(error).__name__}:{error}", 300)
    elif kind:
        state["notification_error"] = "feishu_reporter_not_configured"
    else:
        state.pop("notification_error", None)

    save_state(state_path, state)
    if reporter_failed:
        print("watchdog: notification guarantee unavailable")
        return 2
    if issues:
        print("watchdog: unhealthy: " + "; ".join(sorted(set(issues))))
        return 1
    print("watchdog: healthy")
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--layout-url",
        default=os.getenv("LAYOUT_MONITOR_URL", "https://fbifmp-layout.foodtalks.cn/api/publish/monitoring"),
    )
    parser.add_argument(
        "--feed-url",
        default=os.getenv("FEED_MONITOR_URL", "https://feed.foodtalks.cn/health/sync"),
    )
    parser.add_argument(
        "--official-url",
        default=os.getenv("OFFICIAL_MONITOR_URL", "http://112.124.103.65:3002/api/wechat/official/monitoring"),
    )
    parser.add_argument("--state", default="/var/lib/fbif-monitor-watchdog/state.json")
    parser.add_argument("--timeout", type=float, default=12.0)
    parser.add_argument("--drill", action="store_true")
    parser.add_argument("--fixture-ready", action="store_true")
    parser.add_argument("--inject-issue", default="")
    return parser.parse_args()


if __name__ == "__main__":
    raise SystemExit(run(parse_args()))
