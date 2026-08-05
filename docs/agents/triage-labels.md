# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

> **状态戳（source / date / status）**：来源 mattpocock/skills triage 约定；2026-07-26 用
> `gh label list` 核对。本仓库已有 GitHub 默认标签和 `wontfix`，但右列 4 个 canonical 标签
> （`needs-triage` / `needs-info` / `ready-for-agent` / `ready-for-human`）仍未创建；skill
> 执行 `gh issue edit --add-label` 前需先建好对应标签，否则会失败。

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

Edit the right-hand column to match whatever vocabulary you actually use.
