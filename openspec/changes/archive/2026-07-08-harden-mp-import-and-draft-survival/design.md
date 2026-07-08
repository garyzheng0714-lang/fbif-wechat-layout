## Context

本轮多智能体评审继续否决上一版方案。上一版虽然说了“不要只看字号/颜色”，但没有把内部边界讲清楚，实施时仍可能退化成更多 `if font-size/color/center` 分支。

当前 URL 导入的根问题在 `api/wechat_blocks.go`：walker 一边遍历 DOM，一边直接把当前块 `emit()` 成 `h` / `txt` / `cap` / `img`。图片节点一出现就打开全局 `captionContext`，后面的短文本就被放进“可能是图注”的漏斗里。这个布尔状态无法表达“上一张图是内容图、装饰分隔器、编号图还是未知图”，也无法表达“这一行是标题组的一部分，而不是图片说明”。

两篇新病样的共同结构不是“字号特殊”，而是“图片角色判断错了”：

- `gtyfKcOPo6SlcCjYvChhjw`：低矮编号/分隔图后接 `年收入超过11亿元`，再接标题续行和正文。它是章节标题组件，不是正文。
- `katP22I4qo5CEFWQC5YeVw`：内容图后接 `图源大众点评`，这是图注；后面低矮分隔图后接 `重生之泰奶又又又火了`，这是下一节标题。当前模型把两种“图片后文字”混成一种。

更多文章问题的根因也不是“上传慢”，而是时间一致性：用户点击复制得到的是 T0 的 HTML；微信外链图片转存是异步的；用户立刻保存草稿时，微信服务端清洗看到的是 T0 或 T0 后很短时间的 DOM。只要复制层依赖“稍后外链会转存好”，就会出现先消失后恢复。

## Goals / Non-Goals

**Goals:**

- 把 URL 导入内部改成明确三层：`ObservedBlock -> ArticleProfile -> RoleDecision -> Block`。公共 JSON `Block` 合同不变。
- 先收集事实，再推导文章本地基准，再识别角色组；最终分类必须来自角色关系，不来自单块局部猜测。
- 用真实 fixture + 变形测试证明：同类结构换文案、换 wrapper、换样式顺序、换字号单位后仍稳定。
- 让公众号更多文章复制层在 T0 就满足微信保存草稿清洗规则，不能依赖外链异步转存。
- 保持复制按钮即时反馈：点击复制不等待图片上传或更多文章合成。

**Non-Goals:**

- 不引入 OCR、截图像素识别、ML 分类器、远程服务或用户可配置阈值。
- 不改前端 `Block` JSON 合同，不改 DOCX 标题检测路径。
- 不触碰 FoodTalks 网站渲染器和网站表格 CMS 契约。
- 不保存整篇 3MB 级微信 HTML fixture；只保存能决定语义的最小上下文片段。

## Decisions

### 决策 1：内部三层模型是硬边界，不是描述性建议

实施必须把现有“遍历时直接判定”改为内部三层：

1. `ObservedBlock`：只记录输入事实，不下结论。包括 DOM 顺序、raw kind、文本/runs、tag/classes、style 摘要、文本特征、图片 `data-w/data-h/data-ratio`、是否同块含图、是否空白、是否来源/参考标记、祖先容器信号。
2. `ArticleProfile`：从整篇 `ObservedBlock` 推导文章本地上下文。包括正文流候选、重复正文样式、强图注样式、参考来源区候选、图片后常见从属说明形态。这里仍不输出 `h/cap/txt`。
3. `RoleDecision`：用有限 lookahead/lookbehind 识别组，再输出角色和原因。核心组包括 `MediaGroup`、`SectionHeaderGroup`、`RefGroup`、`StandaloneHeading`、`BodyFlow`、`Unknown`。

最终才把 `RoleDecision` 转成现有 `Block`。这样代码边界会阻止后续再把某个病样直接塞进 `emit()` 分支。

### 决策 2：图片先是“媒体候选”，不是立刻开启图注上下文

图片出现时不再立即打开全局 `captionContext`。图片先成为媒体候选，等后续 1-3 个 meaningful block 出现后再判定：

- `MediaGroup`：内容图片 + 来源/说明/小字灰字等从属图注。
- `SectionHeaderGroup`：装饰/编号/分隔图片 + 一到两行独立标题 + 后续正文流。
- `UnknownMedia`：证据不足时保守输出图片，并让后续文本走普通判断，不强行吞成图注。

这才是第一性原理：图注是“依附内容图的从属说明”，标题是“开启新章节的边界文本”。低矮图片、字号、颜色只能作为证据，不能单独定罪。

### 决策 3：决策原因必须可测试

`RoleDecision` 应带内部 reason，例如：

- `source-prefix-after-content-image`
- `same-block-image-caption`
- `marker-title-body`
- `ref-section`
- `body-flow`
- `body-like-ending`
- `unknown-fallback`

这些 reason 不需要暴露给前端，但测试可以通过内部 helper 或只在 Go 测试中使用的路径验证。否则测试只能断言目标文字是 `h`，很容易又变成文案白名单。

### 决策 4：显式标签也不能无条件胜出

微信文章里 `<h1>` 不总是正文小标题。病样中 `作者 | 曾有为` 使用了 `<h1>`，但它是作者信息，不应进入正文标题序列。显式 `<h1>`-`<h6>` 仍是强信号，但必须经过前置区/作者来源/参考区/正文流位置判断，不能无条件覆盖角色关系。

### 决策 5：测试要证明结构稳定，不只证明三句话修好了

必须新增三类测试：

- 真实最小 fixture：保留前一内容图、图注、分隔器、标题、后一段正文，不裁到只剩目标节点。
- 变形测试：替换标题文案、移除 URL、增加 wrapper 层、调整 style 顺序、px/pt 单位变化，结果不变。
- 负向对照：同样短文本在内容图后是图注，在分隔器后才可能是标题；分隔器后完整句子/长段落不自动升标题。

禁止用整篇 HTML golden 作为主断言；主断言应是块序列、目标文本 kind、内部 reason 或安全谓词。

### 决策 6：更多文章复制层的 T0 安全谓词

公众号更多文章 copy-safe 层必须满足：

- 已填充卡片的 `a[linktype=image].imgurl` 是微信图床 URL。
- 卡片 `<img src>` 在 T0 就可存活：微信图床 URL，或位图 `data:image/png|jpeg|jpg|gif|webp`。
- OSS/外站 HTTP 不能作为公众号更多文章卡片唯一 copy-safe `src`，因为 immediate save 不能假设它已经转存。
- 后台 job 只能把复制层从一个安全状态更新到另一个安全状态；不能写入需要微信异步转存才安全的中间态。

点击复制时如果合成图未就绪，优先用原始微信封面保证存活；后台预热完成后下一次复制可以恢复更好的合成图。草稿存活优先于标题叠字视觉。

### 决策 7：非微信文章卡片必须有明确降级策略

当前代码允许非微信远程原图作为 `imgurl` 兜底，这和“imgurl 必须微信图床”的草稿存活契约冲突。本变更默认策略：

- 如果没有微信图床封面，不输出伪安全的 `linktype=image` 卡片。
- 降级为安全占位或普通链接形态，并在后续 UI/日志中提示需要选择微信素材。
- 不新增后台配置开关。

## Risks / Trade-offs

- [极短或图片密集文章无法推导正文基准] → 使用 `Unknown` fallback，不能强判；保留现有局部启发式作为低优先级兼容路径。
- [视觉上标题和 callout 完全一致] → 只能按是否形成 `SectionHeaderGroup` 裁决；不存在读心规则。
- [实现复杂度上升] → 范围限定在 URL 导入内部；公共输出合同不变，前端无需改。
- [点击复制过早时更多文章视觉降级] → 先用 mmbiz 原封面保草稿存活；后台完成后下一次复制再恢复叠字图。
- [data URL 剪贴板变大] → 默认用 mmbiz 原封面；只有已有位图 data 合成图且满足安全谓词时才用。

## Migration Plan

- 无数据迁移；变更只影响公众号 URL 导入分类器和公众号更多文章复制层。
- 回滚方式：还原 `api/wechat_blocks.go` 邻近内部模型改动和 `public/js/more-articles.js` / `public/js/engine.js` copy-safe 改动。
- 验证命令：`cd api && go test ./...`、`npm test`。
- 发布前真实验收：导入两篇 URL，确认目标标题/图注；复制到公众号后台，分别“粘贴后立即保存”和“等待后保存”，更多文章都不能消失。

## Open Questions

- 非微信文章卡片没有 mmbiz 封面时，UI 是否需要立刻阻止复制，还是允许用普通链接/占位降级？本方案建议降级，不允许伪装成 `linktype=image`。
