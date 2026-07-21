#!/usr/bin/env python3
"""Validate a systemd EnvironmentFile against the repository manifest.

Only variable names are ever printed; values may contain production secrets.
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


def read_environment(path: pathlib.Path) -> dict[str, str]:
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
        if key in values:
            fail(f"duplicate variable: {key}")
        values[key] = visible_value(value)
    return values


def validate(schema: dict[str, Any], group_name: str, values: dict[str, str]) -> None:
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
        if isinstance(contract, dict) and contract.get("required") and not values.get(key, "")
    )
    if missing:
        fail("missing or empty required variables: " + ", ".join(missing))
    version_key = group.get("version_key")
    if version_key and values.get(version_key) != str(schema["schema_version"]):
        fail(f"config schema version mismatch: {version_key}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("schema")
    parser.add_argument("group")
    parser.add_argument("environment_file")
    args = parser.parse_args()
    try:
        schema_path = pathlib.Path(args.schema)
        environment_path = pathlib.Path(args.environment_file)
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        values = read_environment(environment_path)
        validate(schema, args.group, values)
    except (OSError, json.JSONDecodeError, ValueError) as error:
        print(f"env validation failed [{args.group}]: {error}", file=sys.stderr)
        return 1
    print(f"env validation ok [{args.group}]: {len(values)} variables")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
