# foodtalks-site-publishing — FoodTalks 网站发布集成（实现在 FBIF资讯订阅 仓库）

## ADDED Requirements

### Requirement: 文章以隐藏态进站
发布客户端创建文章时 MUST 使用经冒烟测试验证的「前台完全不可见」字段组合（`publishStatus`/`isPublic` 待 Phase 1 确认）；在审核闸门通过之前，SHALL NOT 以任何前台可见状态创建文章。

#### Scenario: 隐藏创建
- **WHEN** 流水线执行 staging 阶段调用 `POST /news/admin/news`
- **THEN** 文章创建成功并回填 news_id，且在网站首页、列表页、搜索与直链（未登录）均不可见

#### Scenario: 审核通过后翻公开
- **WHEN** 审核人确认通过
- **THEN** 客户端以 `PUT /news/admin/news` 携带已有 id 翻转可见性字段，文章前台可见

### Requirement: 幂等发布
同一 `source_key`（公众号文章唯一键）MUST 只对应一篇网站文章：创建前 MUST 检查 job 的 `foodtalks_news_id` 回填锚点，已存在则走 UPDATE；`source_key` 在任务表上 MUST 有唯一索引。

#### Scenario: 重复触发不重复建文
- **WHEN** 同一公众号 URL 被触发两次（含失败重试）
- **THEN** FoodTalks 侧只存在一篇对应文章，第二次触发复用或更新第一篇

### Requirement: 图片全量转存
进站 HTML 的正文图片与封面 MUST 全部转存为网站可用存储的 URL；任一正文图片转存失败时任务 MUST 进入 failed 并列出失败 URL，SHALL NOT 让 mmbiz/外站 URL 混入已发布内容。

#### Scenario: mmbiz 图片重写
- **WHEN** 渲染产物含 `mmbiz.qpic.cn` 图片
- **THEN** 发布进站的 content 中所有 img src 均为网站可用存储域名，且 original→new 映射记录在任务中

#### Scenario: 转存失败显式失败
- **WHEN** 某张图片下载或上传失败且重试一次仍失败
- **THEN** 任务进入 failed，错误信息包含该图片 URL，不产生半成品文章

### Requirement: 凭证 env-only 与过期识别
FoodTalks 鉴权 token MUST 仅存在于服务器 env（不进 git/DB/日志）；客户端收到 401/403 时 MUST 将任务置为 failed 且错误信息为可执行的人话提示（如「FoodTalks 凭证过期，需换发」），SHALL NOT 自动无限重试。

#### Scenario: token 过期
- **WHEN** FoodTalks API 返回 401
- **THEN** 任务 failed，提示凭证过期，且同一任务不再自动重试直到人工处理
