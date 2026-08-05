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
- P0 监控契约以 [CLAUDE](../CLAUDE.md) 的红线 #28–#30、无 paths 过滤的 `.github/workflows/p0-monitoring-contract.yml` 和当前代码为准。旧的 [历史文章库 WORKLOG](../WORKLOG-wechat-mp-archive-2026-07-12.md) 已明确撤销“接口计数就是全量”的旧结论。

### Agent 协作

- [领域文档读取约定](agents/domain.md)
- [Issue tracker 约定](agents/issue-tracker.md)
- [Triage 标签映射](agents/triage-labels.md)

## 历史材料

- 根目录 `WORKLOG-*.md` 是问题排查、验收和交接记录，不是当前规则；是否仍在推进，要同时看文件顶部状态、当前工作区、issue tracker 和代码证据。
- `openspec/changes/` 保存设计演进、事故复盘和任务记录；稳定能力以 `openspec/specs/` 为准。
- WORKLOG 或历史 tasks 中的“待办 / 未决 / 待验证”只有在当前长期文档、issue tracker 或代码状态仍有对应证据时，才视为开放事项。
