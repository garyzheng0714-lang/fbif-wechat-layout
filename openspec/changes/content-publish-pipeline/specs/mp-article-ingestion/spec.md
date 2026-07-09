# mp-article-ingestion — 公众号已发文章发现与去重（实现在 FBIF资讯订阅 仓库）

## ADDED Requirements

### Requirement: 手动 URL 触发
系统 MUST 提供手动触发入口（`POST /api/publish/site-sync`，body 含公众号文章 URL）：校验 URL 为 `mp.weixin.qq.com` 域后创建 `target=foodtalks_site` 的任务。该入口是 MVP 主路径，且在自动发现上线后 MUST 永久保留为兜底。

#### Scenario: 贴链接建任务
- **WHEN** 已授权用户提交一条公众号文章 URL
- **THEN** 系统创建同步任务并开始自动推进，返回任务 id 供轮询

#### Scenario: 非公众号 URL 被拒
- **WHEN** 提交非 mp.weixin.qq.com 的 URL
- **THEN** 返回 400 与人话错误提示，不创建任务

### Requirement: freepublish 轮询自动发现
系统 SHALL 以定时轮询 `freepublish/batchget`（间隔可配，默认 15 分钟）发现新发布文章；MUST NOT 要求在公众号后台启用「服务器配置」事件推送（保护运营号自动回复等功能）。轮询上线前 MUST 先实测其对运营实际发布动作（群发 vs 发布）的覆盖度，实测不覆盖时降级为手动/半自动触发。

#### Scenario: 新文章被自动发现
- **WHEN** 公众号发布了一篇可被 batchget 拉到的新文章
- **THEN** 下一轮轮询为其创建同步任务，多图文消息按 article index 拆分为多个任务

#### Scenario: 轮询失败不扩散
- **WHEN** 微信接口报错（如 IP 不在白名单、频控）
- **THEN** 本轮跳过并记录含修复指引的错误日志，不影响手动触发路径

### Requirement: 去重
已处理文章 MUST 记录在去重存储（`source_key` 唯一索引：能取到 biz/mid/idx 时用之，否则 URL 归一化 hash）；轮询与手动触发 MUST 共用同一去重检查。

#### Scenario: 已同步文章不再重复入队
- **WHEN** 轮询再次返回已处理过的文章，或有人重复贴同一 URL
- **THEN** 不创建新任务，返回/展示既有任务状态
