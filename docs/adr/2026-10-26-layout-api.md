# 排版接口：怎么做到和网页逐字一样（2026-10-26）

状态：已采用。维护怎么做见 [排版接口维护说明](../layout-api-maintenance.md)，对外说明书 [layout-api.md](../layout-api.md)。

## 背景

用户 2026-10-25 定：把排版能力做成对内接口，调用方是公司内部系统和同事（含他们的 AI 助手），每家一把钥匙、能单独停用；第一版全部输入（公众号/网页链接、飞书文档、Word、HTML、Markdown、文字）× 全部 5 个账号。之前已有的 `/api/render` 只认 3 个账号、只收块，是另写的一套渲染（Feed 在用，字节不动）。

两个候选方案交叉评审后选「同步优先」：排版没有副作用，重发就是重算，任务落盘、检查点、幂等键换来的耐久性不值那么多新东西。Codex 审查（task-mvp9dr75）指出最大的风险：在服务端照着网页再写一遍配置组装、页脚兜底、复制层，必然写歪（当时 Word 参数位置、网站配置过滤、系列 behavior、fbif 页脚兜底都已歪）。

## 决定

1. **共用，不照抄。** 网页「导入 → 复制」的整段逻辑收成 `engine.js` 的 `layoutArticleForCopy`，规则 → 渲染配置收成 `engine-config.js`；网页和接口调的是同一份函数。接口的 Node 进程（`render-service/layout.mjs`）在 Node 里装好浏览器那几样后原样加载 `public/` 的代码。网页侧重构后复制结果与改前逐字一样（25/25 对照）。
2. **和网页只许两处不同**：图片地址（接口回答前就转存好）；公众号不出「更多文章」（要编辑挑往期、裁封面、画标题，接口替不了人；用编辑器自己的本篇显隐开关关掉）。由 `tests/layout-parity.test.mjs` 拿网页真抓的样稿逐字核对。样稿用 Claude 内置浏览器在本地网页上抓（项目不用 Playwright）。
3. **单独一个 Node 进程**：`fbif-render-layout`（`layout-server.mjs`，127.0.0.1:19098，MemoryMax 512M），和旧 `fbif-render`（19096）同一份代码、互不共享全局。自动发布和 Feed 的 `/api/render` 永远不排在接口后面。`PartOf=fbif-render`，中台换 `RENDER_TOKEN` 时一起重启。（原定 19097，被服务器上的 OnlyOffice 占了。）
4. **排队只在 Go**：同时只送 1 篇给 Node，等位带截止时间，过期的不送。
5. **每个请求都是内存里的操作**：POST 最多等 `wait_seconds`（默认 90、上限 100），没排完回 202 + `poll_url`；结果只算一次、存 15 分钟、总量 256MB；GET 只读存好的、绝不重排；重启就丢，回 `operation_lost` 让调用方重提。部署暂停期回 `service_restarting`。
6. **钥匙走 FBIF 中台配置包**：中台「接口调用方」表进 layout 包（`items.api_callers`），只存钥匙的 sha256 指纹；只放行「在用」；离上次和中台核对成功超过 10 分钟一律 `auth_unavailable`（宁可拒、不放，停用要及时生效）。不另做管理网页、不存本地钥匙库。
7. **图片硬门在所有改写之后**：成品里每个地址必须是 https 且在 FBIF 自己图床（两个 OSS 桶、`static.foodtalks.cn`，`LAYOUT_IMAGE_HOSTS`），或（只限公众号账号）微信图床；上传服务回的地址也要过白名单。Node 照网页把站内 `/assets` 图读成 `data:`，Go 用和网页同一个上传服务把 `data:` 位图传上 OSS；`blob:`、SVG `data:`、`/api/doc-cache/`、相对地址一律失败。
8. **Word 先在 Go 拆图转存**，交给 Node 的是去掉图片的 docx（≤16MB）；拆不了直接 `docx_images_unreadable`。`.doc` 只走自建 LibreOffice，不用付费转换服务。飞书文档用编辑器同一条导出 job，接口有自己的 2 个导出位。
9. **错误一张表**（`layoutErrorSpecs`），每个错误带给人看的 `fix`；对外说明书和 OpenAPI 由代码生成，线上 `/docs` 就是它。

## 没选的

- 任务落盘 + 幂等键（候选 2）：排版无副作用，重提的代价只是重算一次。
- 服务端照网页重写一套（最初的候选 1 细节）：Codex 审查证明会歪，改成共用。
- 接口进旧 19096 进程：会和自动发布、Feed 抢同一个串行渲染，还要共享全局。

## 后果

- 改模板、渲染器、默认规则时，接口自动跟着变；对照测试变红就要在网页上确认后重抓样稿。
- 接口可用依赖中台 `api_callers` 这一项；中台不通超过 10 分钟，接口全部拒绝。
- 多一个常驻 Node 进程（约 120MB）。
