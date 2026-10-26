#!/usr/bin/env python3
"""Validate a systemd EnvironmentFile against the repository manifest.

Only variable names are ever printed; values may contain production secrets.

叠加文件（--overlay，可重复）：服务在主文件之后还读的文件，比如 FBIF 中台下发的
/etc/fbif-hub/env/layout.env。语义和 systemd 一样，后读的同名为准：必填和 schema 版本按
「主文件 + 叠加文件」合并后的值查；未知变量、重复变量只查主文件（叠加文件里有别组的变量是正常的）。
叠加文件不存在直接失败，不静默跳过。
"""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import sys
from typing import Any


KEY_PATTERN = re.compile(r"^[A-Z][A-Z0-9_]*$")


def fail(message: str) -> None:
    raise ValueError(message)


def visible_value(raw: str) -> str:
    value = raw.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
        return value[1:-1].strip()
    return value


def read_environment(path: pathlib.Path, allow_duplicates: bool = False) -> dict[str, str]:
    values: dict[str, str] = {}
    for line_number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw_line.strip()
        if not line or line.startswith("#") or line.startswith(";"):
            continue
        if "=" not in line:
            fail(f"line {line_number}: expected NAME=value")
        key, value = line.split("=", 1)
        key = key.strip()
        if not KEY_PATTERN.fullmatch(key):
            fail(f"line {line_number}: invalid variable name")
        if key in values and not allow_duplicates:
            fail(f"duplicate variable: {key}")
        values[key] = visible_value(value)
    return values


def read_overlay(path: pathlib.Path) -> dict[str, str]:
    # 叠加文件同名多行也和 systemd 一样取最后一行；格式错误照样失败，报错带上是哪个文件。
    if not path.exists():
        fail(f"overlay file not found: {path}")
    try:
        return read_environment(path, allow_duplicates=True)
    except ValueError as error:
        raise ValueError(f"overlay {path}: {error}") from None


def validate(
    schema: dict[str, Any],
    group_name: str,
    values: dict[str, str],
    merged: dict[str, str] | None = None,
) -> None:
    effective = values if merged is None else merged
    groups = schema.get("groups")
    if not isinstance(schema.get("schema_version"), int) or not isinstance(groups, dict):
        fail("invalid schema document")
    group = groups.get(group_name)
    if not isinstance(group, dict) or not isinstance(group.get("variables"), dict):
        fail(f"unknown schema group: {group_name}")
    variables = group["variables"]
    unknown = sorted(set(values) - set(variables))
    if unknown and not group.get("allow_unknown", False):
        fail("unknown variables: " + ", ".join(unknown))
    missing = sorted(
        key for key, contract in variables.items()
        if isinstance(contract, dict) and contract.get("required") and not effective.get(key, "")
    )
    if missing:
        fail("missing or empty required variables: " + ", ".join(missing))
    version_key = group.get("version_key")
    if version_key and effective.get(version_key) != str(schema["schema_version"]):
        fail(f"config schema version mismatch: {version_key}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("schema")
    parser.add_argument("group")
    parser.add_argument("environment_file")
    parser.add_argument(
        "--overlay",
        action="append",
        default=[],
        metavar="FILE",
        help="在主文件之后读的 EnvironmentFile，同名以它为准；可重复，按给出顺序叠加",
    )
    args = parser.parse_args()
    try:
        schema_path = pathlib.Path(args.schema)
        environment_path = pathlib.Path(args.environment_file)
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        values = read_environment(environment_path)
        merged = dict(values)
        overlay_counts = []
        for overlay in args.overlay:
            overlay_path = pathlib.Path(overlay)
            overlay_values = read_overlay(overlay_path)
            merged.update(overlay_values)
            overlay_counts.append((overlay_path, len(overlay_values)))
        validate(schema, args.group, values, merged)
    except (OSError, json.JSONDecodeError, ValueError) as error:
        print(f"env validation failed [{args.group}]: {error}", file=sys.stderr)
        return 1
    summary = f"env validation ok [{args.group}]: {len(values)} variables"
    for overlay_path, count in overlay_counts:
        summary += f", overlay {overlay_path}: {count} variables"
    print(summary)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
