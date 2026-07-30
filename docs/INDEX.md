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
- [飞书云文档导入契约](feishu-document-import.md)：`@` 搜索、真实总进度、非白屏原子提交、任务 API 与验收门禁。
- [微信公众号标题识别规范](../openspec/specs/wechat-heading-detection/spec.md)
- [微信公众号 URL 块分类规范](../openspec/specs/wechat-url-block-classification/spec.md)
- [微信草稿存活复制规范](../openspec/specs/wechat-draft-survival-copy/spec.md)

### FoodTalks

- [FoodTalks URL 导入验收标准](agents/foodtalks-url-import-acceptance.md)
- [FoodTalks 正文排版评测集](agents/foodtalks-2026-07-23-body-layout-evaluation-set.md)：当前状态和未通过项以文件顶部结论为准。
- [FoodTalks 编辑部发布规范](editorial-norms-foodtalks.md)
- [FoodTalks 发布 API 实测契约](foodtalks-api-contract.md)

### Agent 协作

- [领域文档读取约定](agents/domain.md)
- [Issue tracker 约定](agents/issue-tracker.md)
- [Triage 标签映射](agents/triage-labels.md)

## 历史材料

- 根目录 `WORKLOG-*.md` 是问题排查、验收和交接记录，不是当前规则；是否仍在推进，要同时看文件顶部状态、当前工作区、issue tracker 和代码证据。
- `openspec/changes/` 保存设计演进、事故复盘和任务记录；稳定能力以 `openspec/specs/` 为准。
- WORKLOG 或历史 tasks 中的“待办 / 未决 / 待验证”只有在当前长期文档、issue tracker 或代码状态仍有对应证据时，才视为开放事项。
