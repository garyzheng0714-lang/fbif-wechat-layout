# site-sync-pipeline — 公众号→网站同步流水线编排（实现在排版工具本仓库）

## ADDED Requirements

### Requirement: 阶段状态机
`target=foodtalks_site` 的任务 MUST 按以下阶段持久化推进：`created → fetching → rendering → mirroring → staging → awaiting_review → publicizing → published`；驳回进 `rejected`，任一步失败进 `failed`。自动推进 MUST 停止在 `awaiting_review`（人审闸门）。`publicizing` 只能由与当前审核版本匹配、已持久化且记录审核时间的人工 `approve` 触发；配置、风险等级或重试都不得绕过。既有 `wechat_mp` 链路代码 SHALL NOT 被修改。

#### Scenario: 自动推进到人审闸门
- **WHEN** 手动或轮询创建任务
- **THEN** 系统自动完成抓取、渲染、图片转存、隐藏发布，停在 awaiting_review 并发出审核通知

#### Scenario: 失败可续跑
- **WHEN** 任务在 mirroring 阶段失败后被重试
- **THEN** 从失败阶段继续（已抓取的 blocks、已转存的图片不重做），最终状态与一次成功等价

### Requirement: 排版调用链
机器提交 MUST 只需提供公众号原文 URL，且 MUST 携带已确认的 `content_kind=article`、`classification=ordinary_confirmed` 和合法 `classifier_version`；排版工具负责从 URL 抓取正文，资讯订阅侧 SHALL NOT 把正文 HTML 作为必填输入。`POST /api/publish/site-sync` 的 `rule_pack_id` 是可选的唯一规则包 ID，缺省为 `ft-default`；工具 MUST 按 `account_catalog` 的网站账号映射将其贯穿抓取、渲染和任务记录。fetching MUST 调排版工具既有 URL→Block[] 内核；rendering MUST 调 `POST /api/render` 并透传对应 `packId`。SHALL NOT 在资讯订阅侧自行实现任何网站排版规则（渲染唯一真理源在排版工具）。

#### Scenario: 监控只传原文链接
- **WHEN** 监控方提交 URL、普通文章分类证据和可选 `rule_pack_id`
- **THEN** 工具从原文链接抓取、按选定规则包排版、创建网站隐藏草稿并停在 `awaiting_review`，不得自动公开

#### Scenario: 对已有任务从原文回源重排
- **WHEN** 调用方提交同一原文 URL、`refetch_latest=true` 和可选规则包
- **THEN** 工具必须重新调用 URL→Block[] 抓取内核，替换旧的 supplier 正文，更新同一隐藏草稿并重新停在 `awaiting_review`；不得创建重复任务或自动公开

#### Scenario: 排版服务不可达
- **WHEN** 排版工具 API 超时或 5xx
- **THEN** 任务 failed，错误信息指明「排版服务未就绪」，可重试

### Requirement: 风险审核提示
rendering 完成后系统 SHALL 调用 LLM 产出结构化风险提示（红线清单：功效表达、转载授权、竞品评价、数据来源），结果分级 `pass/warning/block` 存入任务并展示给审核人；本能力 MUST 只做提示、排序和分级，发布决策权始终在人。LLM 输出解析失败 MUST 降级为 `warning` 并附原始返回，且仍须人工审核。

#### Scenario: 风险提示随卡片展示
- **WHEN** 文章含疑似功效表达
- **THEN** 审核通知中列出具体风险条目与证据段落，任务仍正常停在 awaiting_review

### Requirement: 审核辅助自动化不得授予公开权限
系统 MAY 自动评估风险、准备审核材料、排序和提醒，但 `pass|warning` 都 MUST 停在 `awaiting_review` 等待人工批准；`block`、缺失或未知风险 MUST fail closed。不存在自动公开开关。

#### Scenario: pass 仍须人审
- **WHEN** 原创文章风险为 pass、图片转存和 staging 均成功
- **THEN** 任务仍停在 awaiting_review，直到收到匹配当前审核版本的持久化人工 approve

#### Scenario: block 不可发布
- **WHEN** 风险等级为 block
- **THEN** 审核界面/卡片不提供「通过」动作，只能驳回或人工线下处理后重跑

#### Scenario: 旧审核决定不能复用
- **WHEN** 文章内容或规则版本在人工 approve 后发生变化
- **THEN** 旧决定失效，任务回到 awaiting_review，不能进入 publicizing

### Requirement: 业务闭环健康不得假绿
跨系统健康的绿色 MUST 表示：当日已发现批次完整且稳定、每篇普通文章已到 `awaiting_review` 或终态、排版任务无 processing、审核通知 outbox 已清空且审核卡无缺失。HTTP 进程存活、已经提交任务或尚未超过超时 SHALL NOT 单独构成绿色证据。

#### Scenario: 排版仍在进行
- **WHEN** 任一当日普通文章仍在 fetching/rendering/mirroring/enriching/assessing
- **THEN** 跨系统监控返回非绿色，不发送恢复或每日心跳

#### Scenario: 审核卡仍在发送
- **WHEN** 文章已到 awaiting_review，但审核通知仍 pending/sending 或卡片缺失
- **THEN** 跨系统监控返回非绿色，直到审核卡持久 outbox 清空

### Requirement: 幂等任务持续自动补偿
资讯订阅向排版工具的投递 MUST 使用永久 `source_key` 幂等，并在网络、限流、服务端或配置错误后持续退避重试，不得因固定重试次数耗尽而转为永久人工停摆。同步 worker MUST 提供独立心跳；单次 panic MUST 被隔离在迭代边界且下一轮继续执行；整个 worker 循环意外退出时 MUST 由进程内 supervisor 自动重启。

#### Scenario: 下游长时间失败后恢复
- **WHEN** 排版接口连续失败超过五次后恢复
- **THEN** 持久任务无需人工操作继续投递，并最终只产生一个排版任务

#### Scenario: worker 单轮 panic
- **WHEN** 同步 worker 单轮发生 panic，HTTP API 进程仍存活
- **THEN** panic 被记录，下一轮继续；worker 心跳缺失或过期期间监控保持非绿色

#### Scenario: worker 整体循环 panic
- **WHEN** worker 的调度循环在迭代边界之外 panic，HTTP API 进程仍存活
- **THEN** supervisor 记录堆栈并在 1 秒后重启整个循环，不允许网页存活而 worker 永久退出
