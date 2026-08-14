# 文档索引

本页是项目文档入口。第一次接手时，先读项目概览和当前契约；只有追溯背景时才读 WORKLOG 或 OpenSpec 变更记录。

## 先读

- [README](../README.md)：产品用途、入口、功能、运行、测试和部署。
- [PRODUCT](../PRODUCT.md)：用户、产品目标和不可牺牲的体验原则。
- [DESIGN](../DESIGN.md)：编辑器外壳与公众号 / FoodTalks 内容的视觉契约。
- [CLAUDE](../CLAUDE.md)：项目级工程红线和维护约束。

事实优先级为：当前源码、配置与测试 > README 和长期契约 > ADR > 历史 WORKLOG / OpenSpec 变更记录。生产状态必须重新用真实环境核验，不能从旧日志或旧截图推断。

## 长期契约

### 架构与规则边界

- **三系统边界**（2026-08-06 用户裁定，真身在 [CLAUDE](../CLAUDE.md) 顶部）：FBIF 内容链路由三个互相独立的系统组成——本项目**只做加工**，`FoodTalks_Feed` 做**实时监控**，`fbif-wechat-article` 做 **T+1 归档监控**。只经 HTTP 通信，不共享存储；本项目不碰监控端代码，也不主动探别人的死活。
- [发布接入契约](publish-intake-contract.md)：**上游怎么把文章交进来**——只接收链接的红线、鉴权头、类型证据、`mp.weixin.qq.com` 约束、状态码与长/短链幂等差异。改 `POST /api/publish/{site,official}-sync` 必须同步改这份。
- [飞书云文档导入与 Markdown 输入](feishu-document-import.md)：2026-07-28 三路调研（项目链路 / 开源方案 / 飞书 API）的持久事实，含飞书导出 docx 主路线、授权 scope、已知丢失项与 Markdown 补齐未决事项。
- [共用与隔离的四层模型](adr/2026-07-10-share-vs-isolate-boundary.md)
- [素材引用统一使用 `{{asset#id}}`](adr/2026-06-03-asset-reference-id.md)
- [微信公众号标题识别规范](../openspec/specs/wechat-heading-detection/spec.md)
- [微信公众号 URL 块分类规范](../openspec/specs/wechat-url-block-classification/spec.md)
- [微信草稿存活复制规范](../openspec/specs/wechat-draft-survival-copy/spec.md)

### FoodTalks

- [FoodTalks URL 导入验收标准](agents/foodtalks-url-import-acceptance.md)
- [FoodTalks 正文排版评测集](agents/foodtalks-2026-07-23-body-layout-evaluation-set.md)：当前状态和未通过项以文件顶部结论为准。
- [FoodTalks 编辑部发布规范](editorial-norms-foodtalks.md)
- [FoodTalks 发布 API 实测契约](foodtalks-api-contract.md)

### 公众号数据与监控

- 历史已发布文章总量仍是**未完成、未核验**状态；任何单一官方接口、单张表或素材库行数都不能称为“历史全量”。
- `freepublish` 只覆盖“发布”对象，不能代表后台“群发”；素材库是资产来源，不是已发布文章真相源。多源原始响应尚未按稳定标识完成关联、去重、分页、时间与字段覆盖审计。
- 飞书多维表格自动同步在审计完成并经用户确认前保持关闭；已发布轮询必须独立于草稿接口执行，小绿书 / 图集无法可靠判成普通文章时必须 fail closed。
- P0 监控契约以 [CLAUDE](../CLAUDE.md) 的红线 #28–#30、#33–#34、无 paths 过滤的 `.github/workflows/p0-monitoring-contract.yml` 和当前代码为准。旧的 [历史文章库 WORKLOG](worklogs/WORKLOG-wechat-mp-archive-2026-07-12.md) 已明确撤销“接口计数就是全量”的旧结论。
- **外检看门狗不在本仓库**（2026-08-06 迁往 `FoodTalks_Feed`，服务名 `fbif-link-watchdog`）。本仓库只提供被探端点 `GET /api/publish/monitoring`；部署链路里 `cleanup_legacy_watchdog` 只负责摘干净生产上的残留单元，回滚不恢复。不要在本仓库重建任何外检探针。
- P0 workflow 的 job 名 `backend-and-watchdog` 是 `main` 分支保护的必需检查且 `enforce_admins=true`，名字里的 watchdog 已是历史。**改名会让 PR 永久 BLOCKED**，必须同步改分支保护配置。

### Agent 协作

- [领域文档读取约定](agents/domain.md)
- [Issue tracker 约定](agents/issue-tracker.md)
- [Triage 标签映射](agents/triage-labels.md)
- **当前开放交接**：[最近排版稿正文错误 · 统一修复](agents/baselines/broken-layout-batch-2026-08-14/HANDOFF.md)（2026-08-14，快照已冻、尚未改代码；总表 `INDEX.md`）

## 历史材料

- [`docs/worklogs/`](worklogs/) 保存问题排查、验收和交接记录，不是当前规则；是否仍在推进，要同时看文件顶部状态、当前工作区、issue tracker 和代码证据。
- `openspec/changes/` 保存设计演进、事故复盘和任务记录；稳定能力以 `openspec/specs/` 为准。
- WORKLOG 或历史 tasks 中的“待办 / 未决 / 待验证”只有在当前长期文档、issue tracker 或代码状态仍有对应证据时，才视为开放事项。
