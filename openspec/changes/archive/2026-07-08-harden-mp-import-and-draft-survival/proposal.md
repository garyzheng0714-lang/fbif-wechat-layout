## Why

公众号 URL 导入和复制链路又在真实文章上回归。根因不是某个字号、颜色或上传接口坏了，而是现有系统缺少两条稳定边界：URL 导入没有先恢复文章内角色关系就直接分类；复制层没有保证 T0 HTML 在微信立即保存草稿时已经安全。

## What Changes

- URL 导入内部建立 `ObservedBlock -> ArticleProfile -> RoleDecision -> Block` 三层模型：
  - `ObservedBlock` 只记录事实，不判标题/图注/正文。
  - `ArticleProfile` 推导文章本地正文流、图注从属样式、参考区候选。
  - `RoleDecision` 识别 `MediaGroup`、`SectionHeaderGroup`、`RefGroup`、`BodyFlow` 等角色组，再输出现有 `Block`。
- 图片不再一出现就无条件打开图注上下文；先判断它是内容图片、章节分隔器还是未知媒体。
- 两篇真实病样以最小 HTML fixture 固化，并加入变形测试，防止硬编码文案、URL、固定字号阈值：
  - `https://mp.weixin.qq.com/s/gtyfKcOPo6SlcCjYvChhjw`
  - `https://mp.weixin.qq.com/s/katP22I4qo5CEFWQC5YeVw`
- 公众号更多文章复制层改为 T0 安全：卡片 `imgurl` 必须是微信图床；卡片 `<img src>` 必须是微信图床或位图 data URL；OSS/外站 HTTP 不得作为立即保存草稿的必要条件。
- 微信草稿保存模拟器扩展为 immediate/delayed 两种时序，immediate 模式不得假设外链 HTTP 已转存。

## Capabilities

### New Capabilities
- `wechat-url-block-classification`: 公众号 URL 导入时，基于文章内事实、文章本地基准和角色组，把原始微信 HTML 转成稳定的 `h` / `txt` / `cap` / `img` / `table` 等块。
- `wechat-draft-survival-copy`: 公众号复制层输出必须在微信编辑器粘贴后的任意保存时刻稳定存活，特别覆盖更多文章卡片和底部区块。

### Modified Capabilities
- 无既有主 spec；相关行为此前只存在于代码、测试和历史 OpenSpec 变更中，本次首次按能力成文。

## Impact

- 代码：`api/wechat_blocks.go` 或邻近小文件中的 URL 导入内部模型；`public/js/more-articles.js` / `public/js/engine.js` 的公众号更多文章 copy-safe 输出。
- 测试：`api/wechat_blocks_test.go` 增加真实 fixture、变形测试和负向结构对照；`tests/wechat-draft-survival.test.mjs` 增加 immediate/delayed 模拟；`tests/copy-behavior.test.mjs` 增加运行时复制不等待和安全谓词守卫。
- 不改前端 `Block` JSON 合同，不改 DOCX 路径，不改 FoodTalks 网站渲染器，不新增规则后台参数。
