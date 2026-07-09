# feishu-review-gateway — 飞书审核与通知（实现在 FBIF资讯订阅 仓库）

## ADDED Requirements

### Requirement: 审核通知
任务到达 `awaiting_review` 时系统 MUST 向审核群发出通知，内容至少包含：标题、来源与原创/转载类型、风险提示列表、排版预览方式、公众号原文链接。MVP 阶段 SHALL 允许以文本消息 + 前端确认页降级实现；Phase 4 起 MUST 升级为交互卡片（「通过并公开」「驳回」按钮）。

#### Scenario: 卡片送达审核群
- **WHEN** 任务进入 awaiting_review
- **THEN** 审核群收到含上述要素的消息，任务记录回填消息 id

### Requirement: 卡片回调与权限
交互卡片按钮 MUST 走 `card.action.trigger` 回调处理：校验操作人在审核人白名单（RBAC）后才执行通过/驳回；通过 → 触发 publicizing 并把原卡片更新为终态（含操作人与网站链接）；驳回 → 任务进 rejected 并更新卡片。非白名单操作 MUST 被拒绝且不改变任务状态。

#### Scenario: 审核人点通过
- **WHEN** 白名单内审核人点击「通过并公开」
- **THEN** 文章翻转公开，原卡片更新为「已发布 · 操作人 · 网站链接」，不再可重复操作

#### Scenario: 无权限点击
- **WHEN** 白名单外成员点击按钮
- **THEN** 返回无权限提示，任务与卡片状态不变

### Requirement: 发布结果群通知
publicizing 成功或失败后系统 MUST 向指定群发送结果通知：成功含网站文章链接，失败含人话原因。通知发送失败 SHALL NOT 回滚发布结果，但 MUST 记录错误日志。

#### Scenario: 发布成功通知
- **WHEN** 文章成功公开
- **THEN** 群内收到含 foodtalks.cn 文章链接的通知

### Requirement: 凭证与配置安全
飞书 App Secret MUST 仅存服务器 env；对话中已暴露过的 Secret MUST 在上线前于开放平台重置。审核群 chat_id MUST 通过「机器人入群 + 查询机器人所在群列表」获取后进入配置，SHALL NOT 硬编码。

#### Scenario: Secret 重置后上线
- **WHEN** 流水线首次部署到生产
- **THEN** 使用的是重置后的新 Secret，旧 Secret 已失效
