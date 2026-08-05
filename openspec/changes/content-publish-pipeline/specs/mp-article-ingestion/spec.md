# mp-article-ingestion — 公众号已发文章发现与去重（实现在排版工具本仓库）

## ADDED Requirements

### Requirement: 手动 URL 触发
系统 MUST 提供手动触发入口（`POST /api/publish/site-sync`，body 含公众号文章 URL）：校验 URL 为 `mp.weixin.qq.com` 域后创建 `target=foodtalks_site` 的任务。该入口是 MVP 主路径，且在自动发现上线后 MUST 永久保留为兜底。

#### Scenario: 贴链接建任务
- **WHEN** 已授权用户提交一条公众号文章 URL
- **THEN** 系统创建同步任务并开始自动推进，返回任务 id 供轮询

#### Scenario: 非公众号 URL 被拒
- **WHEN** 提交非 mp.weixin.qq.com 的 URL
- **THEN** 返回 400 与人话错误提示，不创建任务

### Requirement: freepublish “发布”轮询独立运行
系统 SHALL 在 08:30–18:30 独立轮询 `freepublish/batchget` 最新页，发现该接口覆盖的“发布”对象。任何 draft / 素材接口失败都 MUST NOT 阻断或跳过已发布轮询。系统 MUST 持久化原始响应、cursor、端点内分页状态、最后成功时间和错误；该接口的 `total_count`、对象数或拆出的文章数 MUST NOT 表述为历史文章总量，也 MUST NOT 被解释为覆盖后台“群发”。

#### Scenario: 新文章被自动发现
- **WHEN** 公众号发布了一篇可被 batchget 拉到的新文章
- **THEN** 下一轮轮询为其创建同步任务，多图文消息按 article index 拆分为多个任务

#### Scenario: 轮询失败不扩散
- **WHEN** 微信接口报错（如 IP 不在白名单、频控）
- **THEN** 手动触发路径继续可用，但轮询失败被持久记录并让业务监控保持非绿色，系统按有界退避重试，不得静默跳过

#### Scenario: draft 接口失败
- **WHEN** 同一轮中的 draft 或素材补充接口失败
- **THEN** 系统仍然执行 `freepublish` 最新页轮询，并独立提交其中确认的普通文章

### Requirement: “群发”使用独立发现路径
系统 MUST 把公众号后台“群发”与 `freepublish`“发布”视为两条不同覆盖路径。群发发现 MUST 作为独立、持久、幂等的来源接入，并在 canonical identity 层与其他入口收敛；在这条路径尚未接通或出现缺口时，监控 MUST 保持非绿色，手动 URL 只作应急兜底，不能把缺口解释为“没有新文章”。

若评估接入官方群发结果事件，启用公众号统一“服务器配置”前 MUST 核实现有回调、自动回复和菜单影响，MUST NOT 直接覆盖现有配置。

#### Scenario: freepublish 没有群发文章
- **WHEN** 运营通过后台“群发”发布文章，而 `freepublish/batchget` 未返回该文
- **THEN** 系统不得宣称发现完整；独立群发路径或监控明确暴露缺口

### Requirement: 文章类型 fail closed
自动发现入口 MUST 只投递有正向证据确认的普通图文。小绿书、图集、空正文、解析失败、冲突信号与未知结构 MUST 进入 held，并持久化 classifier version、证据和原因。

#### Scenario: 无法确认普通文章
- **WHEN** 类型信号只能表明可能是小绿书 / 图集，或分类信号冲突
- **THEN** 不创建可推进的发布任务，记录 held 原因并让监控可见

### Requirement: 去重
已处理文章 MUST 记录在去重存储（`source_key` 唯一索引：能取到 biz/mid/idx 时用之，否则 URL 归一化 hash）；轮询与手动触发 MUST 共用同一去重检查。

#### Scenario: 已同步文章不再重复入队
- **WHEN** 轮询再次返回已处理过的文章，或有人重复贴同一 URL
- **THEN** 不创建新任务，返回/展示既有任务状态

### Requirement: 多图文批次完整性必须 fail closed
系统 MUST 以微信 `biz + mid` 作为同批标识，并检查已见 `idx` 从 1 到最大值连续。任一索引缺失，或最后一篇发现后尚未经过 10 分钟稳定窗口，监控 MUST 返回非绿色。收到第一篇 SHALL NOT 被解释为整批完成。

#### Scenario: 第二篇先到
- **WHEN** 系统只收到同批 `idx=2`
- **THEN** 监控明确报告缺 `idx=1`，不得恢复绿色

#### Scenario: 中间文章迟到
- **WHEN** 系统收到同批 `idx=1,2,4`，尚未收到 `idx=3`
- **THEN** 监控持续报告批次索引缺口；`idx=3` 到达且稳定窗口结束前仍不得恢复
