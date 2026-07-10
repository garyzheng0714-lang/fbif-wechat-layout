# 规则迁移与更多文章同步 UI 审计（2026-07-10）

## 范围

- 规则库 v2 → v3 的基线确认面板。
- 更多文章新增、删除、排序、重复 URL 与慢请求乱序后的实时预览同步。
- 宽度 320、768、1024 px；页面背景、可点击状态、溢出与焦点顺序。

## 真实页面结论

| 视口 | 结果 | 关键观测 |
| --- | --- | --- |
| 320 × 900 | 通过 | 无横向溢出；账号、基线和确认控件自然换行；未选基线时确认动作禁用；正文从约 174px 处开始 |
| 768 × 900 | 修复后通过 | 初测发现 `.admin-layout` 的 `1fr 1fr` 行定义把顶部导航拉成半屏；改为 `auto minmax(0, 1fr)` 后导航高约 67px、正文从约 156px 开始，无空白挤压 |
| 1024 × 900 | 通过 | 桌面侧栏布局保持，主内容无横向溢出，迁移面板层级与按钮状态清楚 |

## 行为验收

- 历史自建规则只展示存储中的原始覆盖，不把默认 FBIF 基线冒充成用户配置。
- 可选基线按同一渠道族过滤；必须先选基线、再明确确认，服务端才接受迁移。
- 管理员在保存过程中切换账号、关闭面板或再次保存时，旧请求不能回写新账号 UI；若旧 POST 已成功，只提示“已保存但未应用”，不做错误的本地提交。
- 更多文章元数据按卡片对象绑定，删除后下标复用、拖拽换序、A/B 乱序、A→清空→A 的相同 Promise 复用都不能让旧结果覆盖当前卡片。
- 预览页保持纯白背景；没有恢复 phone-frame、底部按钮或双层账号切换器。

## 证据

本地验收截图（临时产物，不进仓）：

- `/tmp/fbif-ui-audit/migration-320-fixed.png`
- `/tmp/fbif-ui-audit/migration-768-fixed.png`
- `/tmp/fbif-ui-audit/migration-1024-fixed.png`

自动回归：`tests/admin-panel-flow.test.mjs`、`tests/admin-rule-state.test.mjs`、`tests/rule-admin.test.mjs`、`tests/more-articles-ui.test.mjs`、`tests/e2e-regression.spec.mjs`。
