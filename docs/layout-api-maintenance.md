# 排版接口维护说明

给维护排版接口的人（和 AI）看。对外说明书是 [layout-api.md](layout-api.md)，由代码生成，线上 `GET /api/layout/v1/docs` 回的就是它；这里不重复它写过的（字段、错误码、限额），只写怎么运转、坏了去哪看、常见改动怎么做。架构为什么这么定见 [ADR](adr/2026-10-26-layout-api.md)。

## 一句话

同事和公司内部系统交一篇文章（公众号/网页链接、飞书文档、Word、HTML、Markdown、文字）和一个账号，拿回和网页版点「复制」**逐字一样**的成品 HTML。只排版：不建发布单、不碰 `site-sync/official-sync`（红线 33），`layoutAPI` 里故意没有任何发布依赖（`TestLayoutNeverCreatesPublishJobs`）。

## 一篇稿子怎么走

```
POST /api/layout/v1/render（fbif-api，Go）
  ├ 暂停中（部署）→ 503 service_restarting
  ├ 钥匙：layout_callers.go，名单来自中台配置包 items.api_callers
  ├ 限额：layout_limits.go（每把钥匙并发 4、每分钟 30；全体每分钟抓公众号 20）
  ├ 认输入：layout_source.go 的 layoutInputSpecs 表（7 种输入 → 4 种载体）
  └ 建操作：layout_operations.go（内存里，最多等 wait_seconds，没排完回 202 + poll_url）
       后台：
       ├ 准备载体：链接走 /api/fetch-article 同一套抓取（不行退 Jina）；飞书走编辑器同一条导出 job（layout_feishu.go）；
       │          Word 先 .doc→.docx（LibreOffice），再拆图转存 OSS（和网页上传同一个函数），交给 Node 的是去掉图片的 docx
       ├ 排队：Go 是唯一的队列，同时只送 1 篇给 Node（layoutNodeConcurrent）
       ├ 排版：POST 127.0.0.1:19098/layout（fbif-render-layout，Node，render-service/layout-server.mjs → layout.mjs）
       ├ 图片：layout_images.go 转存不合格的图（外链、data: 位图；FoodTalks 账号连微信图也转），再过一道 HTML 解析器硬门
       └ 存结果 15 分钟；GET poll_url 只读存好的那份，绝不重排
```

## 逐字一样怎么保证

Node 不是另写一套排版，是**把网页编辑器的代码原样跑一遍**：

- `render-service/layout.mjs` 在 Node 里装好浏览器那几样（`window`、`localStorage`、`DOMParser`、`JSZip`、只认站内地址的假 `fetch`），从 `PUBLIC_DIR` 加载 `public/js/engine.js`、模板、规则包，调网页「导入 → 复制」同一个函数 `engine.layoutArticleForCopy`。
- 网页编辑器本身也走这个函数和 `public/js/engine-config.js`（规则 → 渲染配置，`app.html`、`site.html` 都调它）。所以改排版只改网页那份代码，接口自动跟着变。
- 载体照网页的入口拼：链接=抓到的块（默认转载）、Word=docx（默认原创，标题取文件名）、Markdown=md 文件、文字=粘贴（没图片链接只排正文、不加页脚）。FoodTalks 账号先 `extractSiteSummary` 拿走导语当 `summary`（同 `site.html`）。
- 每篇开一个干净的本地存储：等于「新导入、没手改过」的网页状态。

**看守**：`tests/layout-parity.test.mjs` 拿网页真抓的复制结果（`tests/fixtures/layout-parity/`）比 5 账号 × 5 输入，逐字一样；`tests/layout-server.test.mjs` 真起一个进程排。**只允许两处不同**：图片地址（样稿统一写 `IMG`）；公众号账号不出「更多文章」（接口传 `footerVisibilityOverride: {blocks: {more_articles: false}}`，样稿也是网页上关掉这一块再抓的）。

改了模板、渲染器、默认规则让它变红：先在网页上确认新样子对，再按 [tools/layout-parity/README.md](../tools/layout-parity/README.md) 重抓样稿。**不要改测试迁就。**

## 钥匙

- 名单在 FBIF 中台「接口调用方」表（code、name、owner、status、key_sha256），进排版工具的配置包 `GET /api/config/bundle/layout`（`api/hub_bundle.go`，和外发账号同一个客户端、同一个 ETag，每分钟问一次）。表里只有指纹。
- 发钥匙：`cd api && go run . -new-layout-key`，钥匙只打印这一次交给调用方，指纹填进表、状态「在用」。停用改状态（永不删行），一分钟内生效。
- 只放行「在用」；配置包里没有 `api_callers` 这一项、或离上次和中台核对成功超过 10 分钟，所有钥匙一律 503 `auth_unavailable`（宁可拒、不放）。
- 自测钥匙（表里 `layout-selftest`）在维护者本机 `~/.config/fbif-layout/selftest.key`（600），线上验收用它，别贴进聊天和仓库。
- 旧的 `RENDER_TOKEN` 只开 `/api/render`、`/api/html-to-blocks`（Feed 在用，`server.mjs` 字节不动）；Go 调 Node 的 `/layout` 也用它，但对外钥匙和它互不相通。

## 部署

- Node 进程：`deploy/systemd/fbif-render-layout.service`，`127.0.0.1:19098`（19097 被服务器上的 OnlyOffice 占了），`MemoryMax=512M`，和 `fbif-render` 同一份 `/var/www/render-service` 代码、同一个 `render-service.env` 和中台文件 `render.env`。`PartOf=fbif-render`：中台代理换 `RENDER_TOKEN` 只重启 `fbif-render`，这里跟着重启。
- 部署事务（`deploy/scripts/deploy-transaction.sh`）装单元、`enable`、重启、查 `19098/healthz` 的 `config_schema_version`，都在回滚窗口里；第一次装上的单元回滚时先停掉再删。公网验收查 `/api/layout/v1/openapi.json`。看守：`tests/deploy-transaction.test.mjs`。
- Go 这边的配置都有默认值，不用填：`LAYOUT_RENDER_URL`（默认 `http://127.0.0.1:19098`）、`LAYOUT_IMAGE_HOSTS`（FBIF 自己图床，默认两个 OSS 桶加 `static.foodtalks.cn`，整张替换）。图片转存用 `WECHAT_UPLOAD_ENDPOINT`（和发布流水线同一个上传服务）。

## 坏了去哪看

- 每个请求一行日志（不含正文）：`journalctl -u fbif-api | grep 'layout-api: request_id=<调用方报的 request_id>'`，带 caller、account、input、status、code、耗时、图数、规则版本；有内部原因的错误多一行 `request_id=… <code>: <原因>`。
- Node 出错：`journalctl -u fbif-render-layout`（`[layout] 排版失败：…`）。Node 的错误码在 Go 里翻成对外的（`layoutNodeError`）：两边对不上的（`unsupported_input`、`bad_request`、`unknown_account`）对外一律 `render_failed`，看 Go 日志里的原话。
- 常见：
  - `auth_unavailable`：中台配置包取不到或没有 `api_callers`，看 `fbif-api` 日志里「调用方名单」那几行。
  - `render_unavailable`：`systemctl status fbif-render-layout`；Go 与 Node 的 `RENDER_TOKEN` 对不上也会这样（部署前检查已经比过两个中台文件）。
  - `rules_unavailable`：Node 每篇现取 `RULES_API_ORIGIN/api/rules/active`，Go 没起来或规则库坏了。
  - `image_mirror_failed` / `image_not_allowed`：上传服务出错，或成品里有白名单外的图；message 写着是哪张。换桶改 `LAYOUT_IMAGE_HOSTS`。
- 本机复现一篇：起本地 Go（见样稿 README 第 2 步）后，`node render-service/layout-server.mjs` 带 `RENDER_TOKEN`、`RULES_API_ORIGIN`、`ASSET_ORIGIN` 起 Node，直接 `POST /layout`；载体样子见 `tests/layout-parity.test.mjs` 的 `INPUTS`。

## 常见改动

| 要做的 | 改哪 | 还要做 |
| --- | --- | --- |
| 加账号 | `account_catalog.go` + `rule_defaults.go`（同网页，红线 2），接口自动认 | 样稿表里加账号、重抓；说明书例子每个账号一个（`TestLayoutAPIExamplesCoverEveryAccount`） |
| 改排版规则默认值、模板、渲染器 | 网页那份代码 | 样稿重抓（上面「看守」） |
| 加一种输入 | `layoutInputSpecs` 表 + `prepare()`；载体只有 blocks/docx/markdown/text 四种，新载体要 Node 的 `articleInput` 一起加 | 样稿加一种输入 |
| 加错误码、提醒、限额 | `layout_errors.go`、`layoutWarningSpecs`、`layout_limits.go` 的表 | 说明书重新生成 |
| 改说明书 | `api/layout_api_doc.go`（表驱动，别手改 `docs/layout-api.md`） | `cd api && SPEC_UPDATE=1 go test -run TestLayoutAPIDocIsGenerated .` |
| 更新说明书里的例子回答 | `api/testdata/layout-examples/*.json` | 用自测钥匙对线上跑 `TestLayoutAPIExamplesRegenerate`（命令写在 `TestLayoutAPIExamplesExist` 的报错里），再重新生成说明书 |
| 停用某家 | 中台表改状态 | 不用部署 |

## 边界

- 只读规则、不写规则；不建发布单；不缓存稿子（结果 15 分钟后清掉，重启就丢，调用方按 `operation_lost` 重提）。
- 公众号的「更多文章」不出：要编辑挑往期、裁封面，接口替不了人（说明书「和网页版不一样的地方」）。
- 飞书文档用排版工具自己的飞书应用「排版工具」读，有自己的导出位（`layoutFeishuConcurrent`=2），不挤编辑器的。
