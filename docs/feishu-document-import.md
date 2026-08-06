# 飞书云文档导入与 Markdown 输入（2026-07-28 调研落档）

> 本文档 2026-08-06 由 `HANDOFF-markdown-feishu-2026-07-28.md` 迁移整理：内容为三路调研（项目内链路 / 业界开源方案 / 飞书开放平台 API）的持久事实。实现现状以代码与 CLAUDE.md 红线为准；本文只记结论与事实，不替代代码。

## 0. 背景与产品目标（用户原话归纳）

1. 排版工具以 Word (DOCX) 上传为主。用户判断「未来一定是 Markdown 优先」，希望上传 Markdown 文档的排版效果与 Word 一样好，**不能出现排版有问题的情况**。
2. 接入飞书登录后，用户粘贴飞书云文档链接即可导入排版。
3. 已拍板方案：**飞书导入走「导出 Word → 复用现有 DOCX 链路」**，不走 Markdown 中转。Markdown 补齐是独立事项，与飞书线解耦。

## 1. 三个最重要的调研结论

1. **Markdown 已经是一等公民输入源，不是新功能。** `template.formats` 三个模板都含 `['.docx','.doc','.md','.txt']`，拖 .md 即可排版。真正的工作是「MD 解析器补完」，现状是简化版手写实现。
2. **DOCX 入口绝不要改成「先转 Markdown 再排版」。** pandoc / microsoft-markitdown / mammoth 会丢字号、颜色、居中、段距（Markdown 语言天生不表达这些），而现有规则恰恰依赖这些信号。Word 与 Markdown 是平行入口，各自 classify，汇入同一套中性 elem 模型。
3. **飞书官方导出任务 API 不支持 Markdown，但支持导出 docx**；同步内容接口可直出 Markdown 但图片形态未核实。飞书 → docx → 现有链路可完全复用排版规则且图片打包在文件内；保真度曾列为待验证项（2026-07-31 起已用真实飞书账号与真实文档验收）。

## 2. 项目现状链路（2026-07-28 核实的文件路径，此后有演进）

### 输入分流
- 前端入口：`public/app.html` `#fileInput` + `#dropZone`；`public/js/engine.js` 的 `handleUploadFiles` → `handleFile`（含版本门 `articleInputGate`，红线 #25）→ `parseFileToArticleInput` **按扩展名分流**：`.docx` → `kind:'docx'`；其余 → `template.processMd` → `kind:'md'`。
- DOCX 纯浏览器本地解析：`public/js/parser.js` `parseDocx`（JSZip + DOMParser）。LibreOffice/CloudConvert 只用于旧 `.doc` → `.docx`（`/api/doc-to-docx`）。
- 其他输入源：URL 导入（`POST /api/fetch-article`）、剪贴板、`POST /api/render`（只吃 blocks）。

### 中间模型（关键架构事实）
- DOCX 有共享归一层：`public/js/docx-article-model.js` `classifyDocxArticle` → `{elems, author, imgN}`；elem kinds：`txt/h/img/cap/refH/ref/table/video`。边界：零全局访问、正则由 opts 注入、只产中性 elem。
- **MD 支没有共享归一层**：三个模板各有一份 `classifyMd` 且口径曾漂移（`fbif.js` 对 `^#` 直接丢弃、不认 `##`；`foodtalks-site.js` 认 `^(#{1,6})\s+`）。后续补齐应在新增共享模型（如 `md-article-model.js`）中做，不要就地加功能。
- 渲染契约：三模板 `processDocx/processMd/processBlocks` 全汇入各自 `render(elems, author, source, opts)`。
- 现有 MD 基建：`parser.js` `parseMdRuns`（bold+链接）、`parseMdFrontmatter`（YAML 取 author/title）、规则项 `md_heading_max_chars`。零第三方 markdown 依赖。

## 3. Markdown 规范与选型（2026-07-28 实时数据）

- 输入规范建议：CommonMark 0.31.2 + GFM 5 项扩展（表格/任务列表/删除线/自动链接/危险标签过滤）+ YAML frontmatter。脚注、`> [!NOTE]`、Mermaid、数学公式不在 GFM 内，产品文案需明确不支持项。
- 解析库若引入：前端 markdown-it（`renderer.rules` 可精确接管单 token）；Go 侧 goldmark（默认剥 raw HTML，开 `WithUnsafe` 有 XSS 面）。鉴于解析全在前端且已有手写基建，可继续手写扩展，自行权衡。
- 微信输出层不需要第三方：doocs/md 验证的 juice 内联化，本项目复制层已有等价且更严实现（红线 #23）。
- 富文本差距的业界补法：图注用「图片后紧跟行」启发式或 `::: figure` fenced-div；文档级元数据用 frontmatter；字号/颜色/逐段样式明确不支持。

## 4. 飞书开放平台事实（全部经官方文档核实）

### 导出 docx（主路线，已实现）
- `POST /open-apis/drive/v1/export_tasks`：`file_extension` 只支持 `docx/pdf/xlsx/csv`（无 markdown）；权限 `docs:document:export` 或 `drive:export:readonly`；频控 100 次/分钟。
- 三步：创建 → 轮询 `GET /drive/v1/export_tasks/:ticket`（job_status 0 成功）→ 下载 `GET /drive/v1/export_tasks/file/:file_token/download`。产物 10 分钟后删除；资源上限 1GB。
- 本仓库实现：`POST /api/feishu/docs/export/jobs` + long-poll（红线 #31），服务端批量镜像正文图到 OSS，交互式导入不得退回同步端点。

### 直出 Markdown（备选路线）
- `GET /open-apis/docs/v1/content?doc_token=...&doc_type=docx&content_type=markdown`：同步返回 Markdown。权限 `docs:document.content:read`；频控 5 次/秒，文档 ≤10MB。
- ⚠️ 未核实：返回 Markdown 的图片链接形态（官方文档未写明）。间接线索指向需登录态/有时效的飞书链接，不可直接进公众号内容。
- 图片单独下载：`GET /drive/v1/medias/:file_token/download`（5 QPS、10000 次/天）或 `batch_get_tmp_download_url`（一次 ≤5 个，链接 24h 过期，必须立刻转存自有图床）。

### 授权与取文档
- 必须 **user_access_token**（tenant token 只能访问显式授权给应用的文档）。OAuth：`accounts.feishu.cn/open-apis/authen/v1/authorize` → `POST /open-apis/authen/v2/oauth/token`；access_token ≈2 小时，**必须申请 `offline_access` 拿 refresh_token**；redirect_uri 后台白名单。
- 所需 scope：`docs:document:export`（docx 路线）/ `docs:document.content:read`（md 路线）/ `docs:document.media:download` / `offline_access`；文件列表才需要 `drive:drive:readonly`；wiki 链接需 `wiki:wiki:readonly`。
- URL 形态：`/docx/`、`/wiki/`、`/docs/`；**`/wiki/` 拿到的是节点 token，必须先经 `GET /wiki/v2/spaces/get_node` 换 obj_token**。

### 已知会丢的内容（两条路线都丢）
画板、思维笔记、流程图、嵌入的多维表格/电子表格、Callout 高亮块、分栏。飞书 heading7-9 导 md 需降级。

## 5. 任务状态

### 已完成（2026-07-31 真实飞书环境验收）
- 飞书登录、`@` 搜索、异步 DOCX 导出、真实总进度、导入期间保留旧预览、`100%` 后立即复制。
- 当前契约：CLAUDE.md 红线 #31（异步作业 + 服务端批量镜像）+ #32（粘贴即排版）；守护测试 `tests/e2e-feishu-import-progress.spec.mjs`、`api/feishu_export_jobs_test.go` 等。

### 未完成 / 独立事项
- **Markdown 解析补齐**（任务三，优先级用户定）：建议新建共享 `md-article-model.js` 逐字对标 `docx-article-model.js` 头 24 行边界，三模板 `processMd` 改调它，渠道差异走 opts；覆盖 ATX 标题、列表、GFM 表格、图注约定（复用 `图片来源：`/`图源：` 前缀，兼容红线 #16）、空段约定（≥2 连续空行 = blankBefore，对齐红线 #18）、frontmatter、bold 标题启发式。新增 `tests/md-article-model.test.mjs`。
- 已知天然缺失需约定：图片显示宽度（md 只有 URL）、居中、字号颜色。

## 6. 风险清单

| 风险 | 等级 | 应对 |
| --- | --- | --- |
| 飞书导出 docx 是机器生成，可能缺「人写 Word」的信号 | 已解决 | 2026-07-31 真实飞书文档验收 |
| `docs/v1/content` 图片形态未知 | 中 | 仅备选路线相关 |
| golden 测试字节级锁定 | 高 | 新逻辑只进 classify 层，不碰 render/shouldSpaceAfter |
| `processMd` 签名被测试锁定 | 中 | 改签名同步改 `foodtalks-site-config-flow.test.mjs` 断言 |
| user_access_token 2h 过期 | 中 | offline_access + refresh |
| 飞书特殊块丢失 | 低 | 产品文案告知；纯图文文章无影响 |

## 7. 未决事项（需要用户输入）

1. Markdown 补齐的优先级与验收口径（Word 版与 Markdown 版同文各排一遍、视觉一致）。
2. 若走 md 备选路线：真实飞书文档的 `content_type=markdown` 图片形态实测。
