# 微信公众号官方数据归档、覆盖审计与新文章待审同步

- 日期：2026-07-17
- 状态：**历史方案，已被后续合同取代**
- 发布目标：本期只允许 FoodTalks 测试站；切正式站需另行明确授权

> 2026-08-14 现行边界：本项目两个机器入口只接收链接，正文旁路已撤销；外检 watchdog 已迁到 `FoodTalks_Feed`，本仓库只提供被探端点。下文的“内容权威顺序”、测试站状态与白名单描述只代表 2026-07-17 方案，当前以 `CLAUDE.md`、`docs/publish-intake-contract.md` 和 readiness 为准。

## 一、目标与边界

本期建立两条物理解耦的链路：

1. feed 负责分钟级发现经正向证据确认的普通公众号文章，可靠投递给排版工具。
2. `fbif-wechat-article`（下称归档服务）负责微信官方接口原始数据归档、各接口增量采集与覆盖审计。

排版工具负责 canonical 身份收敛、排版、图片转存、AI 预填、风险检查、FoodTalks 测试站草稿和飞书人工审核。任何自动路径只能到待审核；公开必须由持久化、版本匹配的白名单审核人决定。

明确不做：

- 不接管微信公众号群发事件回调。
- 不开发知乎等第二发布平台。
- 不把 2015-2025-10 写成 API 可补齐范围。
- 不开启未经覆盖审计和用户确认的飞书 Base 同步。
- 不承诺供应商端到端 exactly-once、历史全量或长期绝对时延。

## 二、不可变安全合同

### 1. 文章身份与内容来源

- 所有入口在创建可发布 job 前必须收敛到 canonical article identity。
- 长链以 `__biz + mid + idx` 为稳定身份；短链必须先解析 canonical URL，不能先建临时业务 job。
- canonical key 在持久层永久唯一；重复输入返回同一主 job。
- 重复输家使用 `superseded_duplicate` 并记录 winner job，不得进入普通 failed/retry。
- 内容权威顺序为：微信官方 API 正文 > 供应商完整正文 > 网页抓取正文。
- 记录 content source、content hash 和版本；高权威内容晚到不得静默覆盖已审核版本。

### 2. 类型 fail closed

- 类型至少包含 `ordinary_confirmed`、`image_post_confirmed`、`unknown`、`classification_error`。
- 只有普通图文正向证据成立时自动投递。
- 空正文、解析失败、冲突信号和未知结构全部进入 held，并保存 classifier version、证据与原因。
- feed 和排版入口均执行 fail-closed 校验。

### 3. 当时的审核与服务鉴权

- `FoodTalks_Feed` / 归档服务使用独立 `PUBLISH_SYNC_SERVICE_TOKEN`，只允许 submit 和紧凑 status-read。
- 机器凭证不得调用完整 job、review、管理员 retry、reconcile 或 rules/admin。
- 当时方案要求 `FEISHU_REVIEWER_OPEN_IDS` 非空；2026-08-06 后运行时另支持“群成员模式 + 当前审核群”，但部署 schema 仍要求白名单非空，当前以根 README/CLAUDE 为准。
- HTTP review 默认关闭；应急开启时必须使用服务端配置的白名单实名并使 readiness=false。
- FoodTalks 公开只允许持久 approve、reviewed_at、`pass|warning` 风险、审核风险版本匹配同时成立时发生。

### 4. 外部副作用与恢复

- FoodTalks CreateNews/UpdateNews 外调前先持久化 intent。
- 结果不确定进入 reconciliation，禁止普通 retry 和自动重放。
- 同步状态 API 返回 `retryable`、`retry_block_reason`、`reconcile_required` 和允许动作；前端不猜状态。
- JSON store 阶段同一时刻只能有一个 writable API/worker；候选 writes-paused 启动，通过 readiness 后再解暂停。

### 5. feed 事务 outbox

- canonical 文章首次入库、规则匹配和专用 sync outbox 在同一数据库事务提交。
- 唯一键覆盖 rule generation、canonical article identity 和 target。
- 启用规则在同一事务保存 baseline/high-water mark，历史文章不自动回灌。
- 重复 webhook 只刷新文章，不重复创建 outbox。
- 禁用后不创建新任务；未领取项取消；已领取项按 after-disable 留痕；再启用创建新 generation。

### 6. 官方数据口径与配额

- 配额按官方 endpoint 独立持久化 used/limit/reserve/date，不再用一个 aggregate 计数分配所有接口。
- 每个 endpoint 独立预算；全局 MaxCalls 只作为进程负载保险。
- read/share 的 D-1 请求安排在北京时间 08:00 后。
- `is_delay=true` 的原始响应照常归档，但标记 deferred，不推进 cursor 或 coverage；有界重试后仍延迟则告警。
- 保留生产现有 cursor，按缺口审计续跑，不重置为从头回扫。
- 任何“全量已核验”结论必须满足多接口原始响应、稳定键关联、分页/时间/字段覆盖审计和用户显式确认。

### 7. 可观测性

- feed 保存 discovery、outbox、layout job、review、publish 的持久事件轨迹。
- 排版工具提供按 job IDs 批量查询的紧凑状态 API，不返回正文、blocks、图片映射或模型 raw。
- 外部 watchdog 独立检查进程/主机存活、计划任务、各 endpoint last success、deferred age、outbox age 和 reconciliation backlog。
- 供应商发现时延与内部处理时延分别统计。

## 三、实施工作块

### A. 排版工具安全与幂等基础

1. 最小权限服务鉴权、审核白名单 fail closed、HTTP review 默认关闭。
2. 紧凑状态 API 和服务端动作矩阵。
3. canonical identity、duplicate winner、内容 provenance 与版本冻结。
4. 单进程锁、readiness 和 writes-paused 部署门禁。

### B. feed 自动同步与同步中心

1. PostgreSQL `sync_rules`、generation、sync items/outbox/events 及原子迁移。
2. 普通图文正向分类与 held 流程。
3. 专用投递 worker，调用排版工具最小权限接口。
4. 同步中心采用现有 React、Tailwind、shadcn 设计系统，主体为高密度状态流水；目标固定显示 FoodTalks Test。
5. 页面只在后端返回 `retryable=true` 时显示安全重试；reconciliation 只显示人工对账指引。

### C. 官方归档端采集修复

1. quota/runtime/collector 按 endpoint 联动重构。
2. `is_delay` deferred 与 08:00 后重试。
3. 沿现有 cursor 续跑，生成各接口缺口和预计完成日期。
4. 每日精确播报 endpoint 调用量、窗口进度、deferred、缺口和配额余量。

### D. 部署与运行验证

1. 三仓测试、并发幂等、崩溃注入、浏览器桌面/移动验证。
2. 候选环境固定 `FOODTALKS_EXPECTED_TARGET=test` 且 writes paused。
3. 公网 SHA、健康、鉴权、readiness 全部通过后接受 release，再解暂停。
4. 外部 watchdog 做真实停进程演练。

## 四、验收标准

1. 同一真实文章以长链参数变体、短链、两个短链、feed、归档服务和手工入口并发提交，最终只有一个 canonical job、一个 FoodTalks 草稿、最多一张可操作审核卡；重复项指向 winner 且不可重试。
2. 正常文章可投递；图集、小绿书、空正文、非法 HTML、冲突或未知类型均 held/skipped，不创建 FoodTalks 草稿。
3. 在文章/outbox 事务边界、CreateNews intent、公开 intent 和通知发送窗口注入崩溃，恢复后不丢单、不重复外调。
4. 机器凭证可 submit/status-read，但 review/retry/reconcile/rules-admin 均拒绝；空审核白名单 readiness=false，非白名单审核不改变任务。
5. retry 只在 `retryable=true` 出现；reconciliation、duplicate、待审和终态不显示普通重试。
6. 一个 endpoint 用尽配额不阻塞其他 endpoint；`is_delay` 不推进 cursor；部署从当前生产 cursor 续跑。
7. 覆盖审计未完成或未获用户确认时，状态保持“历史全量未完成/未核验”，`BaseSyncAllowed=false`。抽查 10 篇只作为字段正确性样本。
8. 外部发现时延与内部处理时延分开统计；正常依赖可用时，feed 事务提交到可操作审核卡 P95 不超过 5 分钟、P99 不超过 10 分钟。08:30 到 09:15 只作为真实场景演练。
9. 外部 watchdog 能在采集进程死亡、主机调度停止、endpoint 停摆和 outbox 积压时独立告警。
10. 所有草稿和公开操作只指向 FoodTalks 测试站；任一 API/前台/后台地址混入正式站，readiness=false。

## 五、实施顺序

1. 先完成 A 的安全、身份、状态和部署合同。
2. A 的接口稳定后，B 后端与 C 并行。
3. B 后端状态模型稳定后实现同步中心 UI。
4. 全量自动化测试、候选环境和测试站端到端验收通过后再合并部署。
