# site-sync-pipeline — 公众号→网站同步流水线编排（实现在 FBIF资讯订阅 仓库）

## ADDED Requirements

### Requirement: 阶段状态机
`target=foodtalks_site` 的任务 MUST 按以下阶段持久化推进：`created → fetching → rendering → mirroring → staging → awaiting_review → publicizing → published`；驳回进 `rejected`，任一步失败进 `failed`。自动推进 MUST 停止在 `awaiting_review`（人审闸门），`publicizing` 只能由审核通过动作触发（分级自动化显式开启时除外）。既有 `wechat_mp` 链路代码 SHALL NOT 被修改。

#### Scenario: 自动推进到人审闸门
- **WHEN** 手动或轮询创建任务
- **THEN** 系统自动完成抓取、渲染、图片转存、隐藏发布，停在 awaiting_review 并发出审核通知

#### Scenario: 失败可续跑
- **WHEN** 任务在 mirroring 阶段失败后被重试
- **THEN** 从失败阶段继续（已抓取的 blocks、已转存的图片不重做），最终状态与一次成功等价

### Requirement: 排版调用链
fetching MUST 调排版工具 `POST /api/fetch-article`（URL→Block[]）；rendering MUST 调 `POST /api/render` 且 `packId=ft-default`。SHALL NOT 在资讯订阅侧自行实现任何网站排版规则（渲染唯一真理源在排版工具）。

#### Scenario: 排版服务不可达
- **WHEN** 排版工具 API 超时或 5xx
- **THEN** 任务 failed，错误信息指明「排版服务未就绪」，可重试

### Requirement: 风险审核提示
rendering 完成后系统 SHALL 调用 LLM 产出结构化风险提示（红线清单：功效表达、转载授权、竞品评价、数据来源），结果分级 `pass/warning/block` 存入任务并展示给审核人；本能力 MUST 只做提示与分级，发布决策权在人（或显式开启的自动化规则），LLM 输出解析失败 MUST 降级为 `warning` 并附原始返回。

#### Scenario: 风险提示随卡片展示
- **WHEN** 文章含疑似功效表达
- **THEN** 审核通知中列出具体风险条目与证据段落，任务仍正常停在 awaiting_review

### Requirement: 分级自动化（默认关闭）
系统 SHALL 支持可配置的自动公开策略：仅当开关开启且满足全部条件（原创、风险 `pass`、图片全部转存成功、staging 成功）时跳过人审直接 publicizing；`warning` MUST 人审，`block` MUST NOT 可被自动或一键通过。开关默认关闭。

#### Scenario: 默认全人审
- **WHEN** 自动化开关未开启（默认）
- **THEN** 所有任务（含 pass）都停在 awaiting_review 等人确认

#### Scenario: block 不可发布
- **WHEN** 风险等级为 block
- **THEN** 审核界面/卡片不提供「通过」动作，只能驳回或人工线下处理后重跑
