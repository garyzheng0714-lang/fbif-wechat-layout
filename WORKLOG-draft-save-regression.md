# 工作文档 · 「保存草稿后更多文章消失」回归根治 + 防回归体系

> 开始：2026-07-02 ｜ 状态：**已修复，本地真实浏览器验证通过；待部署后用户在真实微信后台做 30 秒终验**
> 触发：用户报告——6-30 修完"更多文章双缩进"后，新出现"微信后台保存草稿后，底部更多文章内容直接消失"；且强烈不满"修好的问题反复回归"，要求根治 + 防回归，而不是再打补丁。

## 目标（验收条件）

1. 钉死"保存草稿后更多文章消失"的根因（有证据链，不是猜测）。
2. 修复后在真实链路验证（复制层 HTML 实测 + 尽可能真实微信编辑器验证）。
3. 把"微信复制安全契约"固化成自动化测试：同类问题（复制层含 data:/blob:/本地引用、合并锚点失配、预览层污染复制层）以后一旦引入立即测试红灯，不再靠用户发现。
4. 文档与代码一致后才声明完成。

## 已确认事实（证据链）

1. **嫌疑窗口**：6-30 两个相邻 commit——`1f41f5c`（Fix WeChat copy image safety）+ `46b2c2b`（Fix more articles double indent）。此前无"保存草稿后消失"的修复记录（git log / gh issues 均无），确认是新回归而非旧病复发。
2. **服务端上传是"假上传"**：`api/main.go handleImageUpload`——`WECHAT_UPLOAD_ENDPOINT` 未配置时，`/api/oss-upload` 把 base64 **原样回显**当作结果（代码注释明言依赖"WeChat re-hosts on paste"）。
3. **生产环境没有配 WECHAT_UPLOAD_ENDPOINT**：`.github/workflows/deploy.yml` 写入 `.env` 的键只有 LIBREOFFICE_URL/TOKEN、CLOUDCONVERT_API_KEY、ADMIN_PASSWORD、PUBLIC_DIR、WECHAT_UPLOAD_ORIGIN。本地 `.env` 同样没有。**即：线上所有"图片上传"从来都是 base64 回显，历史上更多文章合成图以 data: URL 形态进剪贴板，靠微信编辑器粘贴时自动转存 base64 存活。**（.env 里的 OSS_* 键服务端无代码消费，是死配置。）
4. **1f41f5c 与上述契约直接冲突**：`uploadDataUrl` 改为"结果非 http(s) 远程 URL 一律 throw"、`uploadOne` 返回 null → 在回显环境下**所有合成图上传必然失败**→ `ensureCompositeReady` 降级 `final_url = imgurl`（无标题叠字的 mmbiz 原始封面）；`copySafeMoreArticleCard` 同理只保留远程 URL。
5. **预览层污染复制层的旁路仍在**：`engine.js window._updateFooter`（页脚/底部块面板变更回调）执行 `_footerCopy = _footerHtml`——把含 data: canvas 合成图的**预览层**直接写入复制层，且不重跑 prepareMoreArticlesForCopy。用户截图里微信编辑器中卡片**带标题叠字**（= canvas 合成图 = data: URL），与此路径吻合。
6. **双任务竞态**：预览时 `_uploadPromise`（文章图上传，启动时捕获旧 footerCopy）与 `_moreArticlesCopyJob`（更多文章预热）都会回写 `_footerCopy`，完成顺序决定最终值；job1 晚于 job2 完成时会用过期底部覆盖成品。
7. css-inline 剥 class（含 `js_jump_icon h5_image_link`）是老行为，早于回归窗口，排除主嫌。

## 实测结论（Playwright 真实浏览器 + 生产端点等价环境）

对 HEAD 与回归前（`98b786c`）分别跑同一探针（DOCX 导入 → 三个时机复制 → dump `getClipboardHtml()`）：

- **生产 `/api/oss-upload` 实际返回真实阿里云 OSS URL**（`fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com`）——生产服务器在 deploy.yml 之外配置了 `WECHAT_UPLOAD_ENDPOINT`（curl 实测证实）。本地 `.env` 没有 → 本地开发行为与生产不同（本地回显 base64），排障时注意。
- 用户实际时序（预热完成后复制）下，回归前后复制层**全文逐字节 diff 只有一处**：wrapper 少了 `margin-left/right: 8px`。卡片两个版本相同：`<a linktype="image" imgurl="OSS外链"><img src="OSS外链合成图">`。**46b2c2b（双缩进修复）洗脱嫌疑。**
- 秒复制时序：回归前 = data: 合成图（imgurl+src 都是 data:）；回归后 = 原始 mmbiz 封面。

## 根因（钉死）

微信侧两道处理决定粘贴内容存活：
1. **粘贴阶段**：编辑器把 `<img src>` 的 base64 位图立即本地转存 mmbiz、外链 http 图异步拉取转存；**SVG data: 不转存**；**`<a linktype="image">` 的 `imgurl` 属性从不被改写**。
2. **保存草稿阶段**：服务端清洗——src 残留 data:/blob:/相对路径的 img 被删；**imgurl 非微信图床的图片链接卡片被整卡清除**。

「更多文章消失、正文图从不丢」的差异正是 `imgurl`：正文 `<img>` 没有 imgurl，粘贴转存后安全；更多文章卡片 `<a imgurl="OSS外链">` 粘贴后 imgurl 原样保留 → 保存草稿整卡被清。回归前用户高频路径拿到的是 data: 形态（imgurl 也是 data:，随粘贴阶段整体转存重建而存活）；`1f41f5c` 以"图片安全"为名把复制层强制推向 OSS 外链形态，反而把卡片推进了清洗规则——**修复方向弄反了**。当时没有留工作文档，动机不可考——这正是"反复回归"的机制性根源。

## 修复清单（2026-07-02，全部已实现并验证）

1. `more-articles.js wechatSafeCardImgurl`：卡片 `imgurl` 恒用原始 mmbiz 封面（次优：远程 http 原图），合成图只放 `<img src>`。**根因主修。**
2. `more-articles.js ensureCompositeReady`：合成上传失败时降级保留 data: PNG 合成图（微信粘贴可转存），不再丢掉标题叠字。
3. `more-articles-placeholder.js`（新）：空卡占位图 SVG → PNG data:（SVG 微信不转存、保存必丢，破坏"微信内替换图片"流程）。
4. `engine.js _footerCopyGen` 代数守卫：慢速完成的旧上传任务（_uploadPromise/retry）不得覆盖更多文章预热成品（双任务竞态收口）。
5. `engine.js _updateFooter`：写入预览层后立即重建复制安全层（预览 data: 污染旁路收口）。
6. `engine.js findUnsafeCopyImageRefs`：公众号渠道 `<img src>` 的 base64 位图不再误报"图缺失"（DOCX 正文图长期依赖的合法形态；SVG 与 imgurl 上的 data: 仍判不安全）。

## 防回归护栏

- **`tests/wechat-draft-survival.test.mjs`（新）**：把微信侧清洗规则固化为可执行模拟器（粘贴+保存两阶段），断言 v1 footer.html / v2 blocks 锚点两渠道的卡片全部存活；含反例守卫（旧回归形态必须被模拟器杀掉，防守卫空转）；锚点三文件字节一致断言（防 46b2c2b 型失配）；占位图 PNG 形态断言。
- CLAUDE.md 红线 #23 固化契约。
- `@playwright/test` 落为 devDependency（此前 e2e 脚手架依赖缺失、从未真正可跑）。

## 验证记录

- `npm test` 691/691 全绿（683 基线 + 8 新增）；`cd api && go test ./...` ok。
- Playwright 真实浏览器（生产端点等价环境）复验复制层：用户时序下卡片 `imgurl` 全部为 mmbiz 封面、`src` 为 OSS 合成图（叠字保留）；秒复制全 mmbiz；`_copyImageWarning` 9 → 0（误报消除）；预览截图无视觉回归。
- **待办（部署后）**：用户在真实微信后台 30 秒终验——重新导入文章 → 复制 → 粘贴 → 保存草稿 → 更多文章应保留。若仍消失，把保存后编辑器里更多文章区的实际 DOM 发回来，按模拟器规则修订。

## 时间线

- 07-02 调查：6-30 两 commit diff 审读、复制全链路通读、生产端点 curl 实测、HEAD vs 98b786c 探针对比（唯一 diff = wrapper margin）→ 根因钉死为 imgurl 外链清洗。
- 07-02 修复 + 测试 + 真实浏览器复验 + 文档固化（本文件 + CLAUDE.md 红线 #23）。
