# 2026-07-08 公众号 URL 导入与更多文章保存草稿回归

## 背景

这次回归有三个表象：

1. `年收入超过11亿元` 被当成正文或图注，没有进入标题组。
2. `重生之泰奶又又又火了` 被当成图片标注。
3. 更多文章粘贴到公众号后台后立即保存会短暂消失，等待一会又恢复。

前一版方案被多智能体评审否决的原因是：虽然口头说“不只看字号/颜色”，代码边界仍可能退回到更多局部规则。最终方案改成先恢复文章内角色关系，再输出旧的 `h/txt/cap/img` 块。

## 根因

URL 导入的旧模型是一边遍历 DOM，一边直接决定当前块是标题、正文还是图注。图片节点一出现就打开全局 caption context，导致“内容图片后图源”和“分隔/编号图后章节标题”被混成同一种“图片后短文字”。

更多文章的问题不是上传慢，而是时间一致性。复制按钮写入的是 T0 HTML；微信编辑器异步转存外链图片；用户立即保存草稿时，服务端清洗看到的可能还是 T0 DOM。只要复制层依赖“稍后 OSS/外站图会被转存好”，就会出现先消失后恢复。

## 修复边界

服务端 URL 导入改成三层：

- `ObservedBlock`：只记录 DOM 顺序、标签、样式、文本、图片比例等事实，不判角色。
- `ArticleProfile`：基于整篇文章推导正文流、参考区、图注候选、标题信号等文章本地上下文。
- `RoleDecision`：带 `Group`/`Reason`，识别 `MediaGroup`、`SectionHeaderGroup`、`RefGroup`、`BodyFlow` 等，最后再转换成现有公开 `Block`。

章节标题组现在按组裁决：分隔/编号图后最多收集两行短标题，要求后面接正文流，并且组内要有明确标题信号，或图片本身是极扁的分隔标记。review 后补了两个边界：极扁内容图后的灰色小字仍是图注；缺尺寸的装饰 marker 只有在后续文字有标题信号并接正文时才进入标题组。这样能覆盖“弱题眼 + 强标题续行”和“超扁分隔图 + 普通短标题”，但不会把普通内容图片后的短图注直接升成标题。

更多文章复制层改成 T0 安全谓词：

- `a[linktype=image].imgurl` 必须是微信图床封面。
- 卡片 `<img src>` 必须是微信图床或位图 data URL。
- OSS/外站 HTTP 不能作为公众号复制层立即保存时唯一安全图。
- 没有微信图床封面的卡片不得伪装成 `linktype=image` 安全卡。
- 后台预热或上传完成只能把 `_footerCopy` 从一个安全状态更新到另一个安全状态。
- 恢复旧会话快照时也必须重新投影到 copy-safe footer，不能信任历史 `footer_copy`。

取舍：公众号复制层在 T0 存活优先。若合成图只拿到 OSS/外站 HTTP 结果，复制层会降级用原始 mmbiz 封面；预览层仍可以显示标题叠字/自定义封面。只有微信图床或位图 data URL 形态的合成图才允许进入复制层。显式“重试上传”路径也必须经过同一 copy-safe 投影，不能绕过主后台路径。

## 性能结论

这次不会明显拖慢预览渲染。新增分类发生在 URL 导入解析阶段，对已抓到的 blocks 做线性扫描，不触发浏览器布局测量，不发额外网络请求，也不在预览重绘时反复执行。真实 UI 导入两篇用户样例耗时约 3.3s 和 4.3s，主要成本仍是抓取微信原文和媒体信息。

## 验证

- `cd api && go test ./...`
- `npm test`
- Playwright 真实 UI 导入：
  - `gtyfKcOPo6SlcCjYvChhjw`：`年收入超过11亿元` 和 `但却是餐饮界最熟悉的“陌生人”` 都渲染为 `.wx-th`，后续正文为 `.wx-p`。
  - `katP22I4qo5CEFWQC5YeVw`：`图源大众点评` 渲染为 `.wx-tc`，`重生之泰奶又又又火了` 渲染为 `.wx-th`，后续正文为 `.wx-p`。
- Playwright 复制检查：剪贴板 HTML 中 3 个更多文章 `linktype=image` 卡片均满足 `imgurl=mmbiz` 且 `src=bitmap data`，没有 OSS/外站 HTTP。
- TDD 红灯记录：
  - `TestFlatContentImageWithShortCaptionIsNotMarkerHeading` 初始失败：`flat content image must not become marker solely by extreme ratio; kinds=[txt img h txt]`。
  - `TestDimensionlessDecorativeMarkerWithHeadingSignalCreatesHeadingGroup` 初始失败：`dimensionless decorative marker with heading signal should produce heading ... kinds=[txt img cap txt]`。
  - `TestRoleDecisionReasonsExposeStructureForReportedFixtures` 初始编译失败：`RoleDecision has no field or method Group`。
  - JS review 后新增的 `retry upload also re-projects footer copy through copy-safe cards` 初始失败，因为 `retryUpload` 直接 `_footerCopy = result.footerCopy`。
  - JS review 后新增的 `restored session footer copy is re-projected before entering the copy layer` 初始失败，因为 `_restoreSessionSnapshot` 直接 `_footerCopy = cs.footer_copy || ''`。
  - 复审后补强 `restoreSessionSnapshot behavior rewrites legacy unsafe footer_copy to a copy-safe footer`，真实执行恢复函数并确认旧 OSS 卡片图不会进入恢复后的复制层。
- 独立 review 记录：
  - Go review 指出极扁内容图误升标题、缺尺寸 marker 漏识别、角色组证据不足；已通过新增 Go 测试和 `RoleDecision.Group` 修正。
  - JS review 指出 FoodTalks 网站判断使用不存在的 `channel === 'site'`、`retryUpload` 漏投影、旧 session snapshot 可能绕过投影；已通过 `isWebsiteTemplate()`、retry copy-safe 写回和 restore copy-safe 写回修正。
  - 完成度 review 指出 4.5 不能自动勾选；本轮保持为真实后台人工/授权验收项。

未在本轮自动操作真实登录的微信公众号后台保存草稿；这会改动真实后台草稿，需要用户明确授权后再做人工/半自动验收。
